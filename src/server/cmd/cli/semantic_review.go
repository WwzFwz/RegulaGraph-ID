// Provides local operator inspection and explicit acceptance of queued semantic proposals.
// The OS account UID/SID supplies audit identity; configured corpus/scope and database access
// define authority. No actor argument, environment username or model field grants approval.
// Inspect emits existing C01 ProtoJSON for the response and hydrated input; accept binds its
// exact SHA-256 and revision. Redact backend errors; bound lifetime/bytes. Review timing is
// not model accuracy or acceptance under configs/benchmark-targets.yaml (unmeasured).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/workflows"
)

type reviewOptions struct {
	Job, OutputHash, Reason, Action string
	Revision                        uint64
	Timeout                         time.Duration
}

func runSemanticReview(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runSemanticReviewWith(ctx, args, out, errOut, executeSemanticReview)
}

func runSemanticReviewWith(ctx context.Context, args []string, out, errOut io.Writer, execute func(context.Context, reviewOptions) (any, error)) int {
	fs := flag.NewFlagSet("review-resolution", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var o reviewOptions
	fs.StringVar(&o.Action, "action", "inspect", "inspect or accept; acceptance applies to the complete unchanged LINK/DEFER batch")
	fs.StringVar(&o.Job, "job", "", "Job ID in the configured review corpus")
	fs.StringVar(&o.OutputHash, "output-sha256", "", "Required for accept: inspected model response hash")
	fs.Uint64Var(&o.Revision, "expected-revision", 0, "Required for accept: inspected registry revision")
	fs.StringVar(&o.Reason, "reason", "", "Required for accept: human review rationale, <=1024 bytes")
	fs.DurationVar(&o.Timeout, "timeout", 30*time.Second, "Total deadline including storage startup, <=5m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || o.Job == "" || o.Timeout <= 0 || o.Timeout > 5*time.Minute || (o.Action != "inspect" && o.Action != "accept") ||
		(o.Action == "accept" && (o.OutputHash == "" || o.Revision == 0 || o.Reason == "")) ||
		(o.Action == "inspect" && (o.OutputHash != "" || o.Revision != 0 || o.Reason != "")) {
		fmt.Fprintln(errOut, "Invalid review action, job, limits or acceptance pins")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	result, err := execute(bounded, o)
	if err != nil {
		fmt.Fprintln(errOut, "Review failed: check operator configuration, proposal pins, job cancellation and registry revision; no acceptance is implied.")
		return 1
	}
	if err = json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "Could not write review result; inspect/retry the exact request to reconcile acceptance.")
		return 1
	}
	return 0
}

func localReviewActor() (string, error) {
	account, err := user.Current()
	if err != nil || account.Uid == "" {
		return "", errors.New("OS account identity unavailable")
	}
	// UID is local to the machine (Windows Uid is a SID). Hostname namespaces Unix IDs;
	// changing host/account intentionally changes the audit principal, never old approvals.
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "", errors.New("host identity unavailable")
	}
	sum := sha256.Sum256([]byte(host + "\x00" + account.Uid))
	return "operator:os:" + hex.EncodeToString(sum[:]), nil
}

func executeSemanticReview(ctx context.Context, o reviewOptions) (any, error) {
	actor, err := localReviewActor()
	if err != nil {
		return nil, err
	}
	pin := os.Getenv("REGULAGRAPH_RESOLUTION_PRODUCER_SHA256")
	producer, err := config.LoadResolutionProducer(os.Getenv("REGULAGRAPH_RESOLUTION_PRODUCER_PATH"), pin)
	if err != nil {
		return nil, err
	}
	corpus, scope := os.Getenv("REGULAGRAPH_REVIEW_CORPUS_ID"), os.Getenv("REGULAGRAPH_COORDINATOR_AUTH_SCOPE")
	if corpus == "" || scope == "" || os.Getenv("REGULAGRAPH_POSTGRES_DSN") == "" || os.Getenv("REGULAGRAPH_ARTIFACTS_DIR") == "" {
		return nil, errors.New("explicit local operator configuration required")
	}
	repo, err := postgres.Open(ctx, postgres.Config{DSN: os.Getenv("REGULAGRAPH_POSTGRES_DSN"), MaxConnections: 4, HealthTimeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	files, err := storage.NewFileStore(os.Getenv("REGULAGRAPH_ARTIFACTS_DIR"))
	if err != nil {
		return nil, err
	}
	defer files.Close()
	reviewer, err := workflows.NewSemanticReviewer(repo, files, workflows.SemanticReviewConfig{Actor: actor, Corpus: corpus, AuthScope: scope, Producer: producer, ProducerPin: &pb.ContentHash{Sha256: pin}, MaximumBytes: 64 << 20, MaximumReferences: 100000, MaximumCandidates: 128})
	if err != nil {
		return nil, err
	}
	if o.Action == "accept" {
		fresh, e := reviewer.Accept(ctx, o.Job, o.OutputHash, o.Revision, o.Reason)
		if e != nil {
			return nil, e
		}
		status := "accepted_for_execution"
		if !fresh {
			status = "already_accepted"
		}
		return map[string]any{"status": status, "job_id": o.Job, "actor": actor, "output_sha256": o.OutputHash, "expected_revision": o.Revision, "registry_commit_confirmed": false}, nil
	}
	view, err := reviewer.Inspect(ctx, o.Job)
	if err != nil {
		return nil, err
	}
	output, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(view.Output)
	if err != nil {
		return nil, err
	}
	input, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(view.Input)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "inspection_only", "job_id": o.Job, "actor": actor, "output_sha256": view.Queue.Output.ContentHash.Sha256, "expected_revision": view.Candidates.RegistryRevision, "model_response": json.RawMessage(output), "model_input": json.RawMessage(input)}, nil
}
