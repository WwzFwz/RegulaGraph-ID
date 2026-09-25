// Submits a durable ingestion job from a bounded ProtoJSON request through the Go scheduler.
// The CLI loads exact-byte-pinned ontology and candidate-policy files once, attaches their
// fingerprints to the request manifest, and lets PostgreSQL own idempotency and job state.
// Input/output remain machine-readable; secrets are read from the environment and never logged.
// Measure submit latency, queue delay, and DB pool saturation with benchmark-targets.yaml;
// required targets remain REQUIRED_UNMEASURED.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	serverconfig "regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

const maximumSubmitRequestBytes = 16 << 20

func runSubmit(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	fs.SetOutput(errOut)
	requestPath := fs.String("request", "", "Path to a bounded IngestionRequest ProtoJSON file")
	jobID := fs.String("job-id", "", "Durable job ID (opaque ASCII ID)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *requestPath == "" || *jobID == "" ||
		os.Getenv("REGULAGRAPH_POSTGRES_DSN") == "" {
		fmt.Fprintln(errOut, "submit requires -request, -job-id, and REGULAGRAPH_POSTGRES_DSN")
		return 2
	}
	ontology, err := serverconfig.LoadOntology(os.Getenv("REGULAGRAPH_ONTOLOGY_PATH"),
		os.Getenv("REGULAGRAPH_ONTOLOGY_SHA256"))
	if err != nil {
		fmt.Fprintln(errOut, "load pinned ontology:", err)
		return 2
	}
	policies, err := serverconfig.LoadCandidatePlanningPolicies(
		os.Getenv("REGULAGRAPH_CANDIDATE_POLICY_PATH"),
		os.Getenv("REGULAGRAPH_CANDIDATE_POLICY_SHA256"))
	if err != nil {
		fmt.Fprintln(errOut, "load pinned candidate policies:", err)
		return 2
	}
	requestBytes, err := readBoundedSubmitRequest(*requestPath)
	if err != nil {
		fmt.Fprintln(errOut, "read ingestion request:", err)
		return 2
	}
	request, err := prepareSubmitRequest(requestBytes, ontology, policies)
	if err != nil {
		fmt.Fprintln(errOut, "prepare ingestion request:", err)
		return 2
	}
	// An optional resolver pin enables later automatic RESOLVE for this immutable job.
	// Validate its bytes before persisting; a partial environment must not omit the pin.
	producerPath, producerHash := os.Getenv("REGULAGRAPH_RESOLUTION_PRODUCER_PATH"), os.Getenv("REGULAGRAPH_RESOLUTION_PRODUCER_SHA256")
	if producerPath != "" || producerHash != "" {
		if _, err := serverconfig.LoadResolutionProducer(producerPath, producerHash); err != nil {
			fmt.Fprintln(errOut, "load pinned resolution producer:", err)
			return 2
		}
		pin := &pb.ContentHash{Sha256: producerHash}
		found := false
		for _, hash := range request.ConfigManifest.InputHashes {
			found = found || proto.Equal(hash, pin)
		}
		if !found {
			request.ConfigManifest.InputHashes = append(request.ConfigManifest.InputHashes, pin)
		}
	}
	repository, err := postgres.Open(ctx, postgres.Config{DSN: os.Getenv("REGULAGRAPH_POSTGRES_DSN"),
		MaxConnections: 4, MinConnections: 1, ConnectTimeout: 5 * time.Second,
		HealthTimeout: 5 * time.Second})
	if err != nil {
		fmt.Fprintln(errOut, "open job store failed; check PostgreSQL connection settings")
		return 1
	}
	defer repository.Close()
	scheduler, err := workflows.NewJobScheduler(repository, ontology, policies)
	if err != nil {
		fmt.Fprintln(errOut, "configure job scheduler:", err)
		return 2
	}
	job, reused, err := scheduler.Submit(ctx, *jobID, request)
	if err != nil {
		if errors.Is(err, postgres.ErrConflict) {
			fmt.Fprintln(errOut, "submit ingestion job: idempotency key conflicts with another request")
		} else if ctx.Err() != nil {
			fmt.Fprintln(errOut, "submit ingestion job cancelled")
		} else {
			fmt.Fprintln(errOut, "submit ingestion job failed; check job store state")
		}
		return 1
	}
	if err := writeSubmitResult(out, job.JobID, job.CorpusID, job.State.String(),
		job.Stage.String(), reused); err != nil {
		fmt.Fprintln(errOut, "write submit result:", err)
		return 1
	}
	return 0
}

func readBoundedSubmitRequest(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumSubmitRequestBytes {
		return nil, errors.New("request must be a regular file within 16 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumSubmitRequestBytes+1))
	if err != nil || len(raw) > maximumSubmitRequestBytes {
		return nil, errors.New("request exceeded bounded read")
	}
	return raw, nil
}

// prepareSubmitRequest injects only the trusted hashes. Other manifest fields stay caller-owned
// and are validated by the wire boundary; wrong corpus or malformed requests never reach storage.
func prepareSubmitRequest(raw []byte, ontology *domain.Ontology,
	policies map[string]domain.CandidatePlanningPolicy) (*pb.IngestionRequest, error) {
	request := new(pb.IngestionRequest)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, request); err != nil {
		return nil, err
	}
	policy, ok := policies[request.CorpusId]
	if !ok || request.ConfigManifest == nil || ontology == nil {
		return nil, errors.New("request corpus, manifest, and pinned ontology/policy required")
	}
	policyHash, err := policy.Fingerprint()
	if err != nil {
		return nil, err
	}
	for _, hash := range []*pb.ContentHash{ontology.ContentHash(), policyHash} {
		found := false
		for _, inputHash := range request.ConfigManifest.InputHashes {
			found = found || proto.Equal(inputHash, hash)
		}
		if !found {
			request.ConfigManifest.InputHashes = append(request.ConfigManifest.InputHashes, hash)
		}
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	for _, source := range request.Sources {
		if source.GetBlob() == nil {
			return nil, errors.New("submit requires source blob references until ACQUIRE dispatch is available")
		}
	}
	return request, nil
}

func writeSubmitResult(out io.Writer, jobID, corpusID, state, stage string, reused bool) error {
	return json.NewEncoder(out).Encode(map[string]any{"job_id": jobID, "corpus_id": corpusID,
		"state": state, "stage": stage, "reused": reused})
}
