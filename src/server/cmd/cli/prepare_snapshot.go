// Operator entry point for preparing real CHUNK sources for the Rust population
// pass. Explicit job/artifact selection is resolved from the registry; indexing
// computes corpus facts, reserves the snapshot and binds immutable envelopes.
// Output is a new directory containing C01 binary refs and a JSON facts manifest,
// never handwritten model/statistics data. Partial failures may leave durable
// prerequisites: retry the same selection into a new output directory.
// Measure preparation I/O/RSS separately from configs/benchmark-targets.yaml
// query gates. This command neither schedules inference nor publishes a snapshot.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/indexing"
)

func runPrepareSnapshot(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("prepare-snapshot", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var cfg indexing.InitialSourceSnapshotConfig
	fs.StringVar(&cfg.CorpusID, "corpus", "", "Authorized corpus ID")
	fs.StringVar(&cfg.PublicationID, "publication", "", "New or identical initial publication ID")
	fs.StringVar(&cfg.GenerationID, "generation", "", "Representation generation ID to bind before population preparation")
	fs.StringVar(&cfg.AuthScope, "auth-scope", "", "Same authorized scope used by the CHUNK coordinator")
	var selections urlFlags
	fs.Var(&selections, "source", "Exact source-job-ID=registered-CHUNK-artifact-ID; repeat for the complete selection")
	directory := fs.String("out", "", "New output directory for snapshot/source refs and corpus facts")
	timeout := fs.Duration("timeout", 5*time.Minute, "Total preparation deadline, up to 30m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dsn, root := os.Getenv("REGULAGRAPH_POSTGRES_DSN"), os.Getenv("REGULAGRAPH_ARTIFACTS_DIR")
	if fs.NArg() != 0 || *directory == "" || dsn == "" || root == "" || *timeout <= 0 || *timeout > 30*time.Minute {
		fmt.Fprintln(errOut, "Invalid snapshot arguments/configuration")
		return 2
	}
	for _, id := range []string{cfg.PublicationID, cfg.GenerationID, cfg.AuthScope} {
		if domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: cfg.CorpusID, RecordId: id}, domain.DefaultWireLimits) != nil {
			fmt.Fprintln(errOut, "Invalid snapshot identity/scope")
			return 2
		}
	}
	selected, err := parseSnapshotSelections(cfg.CorpusID, selections)
	if err != nil {
		fmt.Fprintln(errOut, "Invalid source selection")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	repo, err := postgres.Open(bounded, postgres.Config{DSN: dsn, MaxConnections: 4, HealthTimeout: 5 * time.Second})
	if err != nil {
		fmt.Fprintln(errOut, "Snapshot database unavailable")
		return 1
	}
	defer repo.Close()
	for i, input := range selected {
		ref, e := repo.LoadArtifact(bounded, cfg.CorpusID, input.DocumentBatch.ArtifactId)
		if e != nil {
			fmt.Fprintln(errOut, "Selected source is not registered in this corpus")
			return 1
		}
		selected[i].DocumentBatch = ref
	}
	files, err := storage.NewFileStore(root)
	if err != nil {
		fmt.Fprintln(errOut, "Artifact store unavailable")
		return 1
	}
	defer files.Close()
	if err = os.Mkdir(*directory, 0700); err != nil {
		fmt.Fprintln(errOut, "Snapshot output must be a new directory under an existing parent")
		return 1
	}
	result, err := indexing.PrepareInitialSourceSnapshot(bounded, repo, files, files, cfg, selected)
	if err != nil {
		fmt.Fprintln(errOut, "Snapshot preparation failed; inspect source jobs and retry identical inputs into a new output directory")
		return 1
	}
	if err = writeSnapshotPreparation(*directory, result); err != nil {
		fmt.Fprintln(errOut, "Snapshot export failed; reservation/bindings may already be durable, retry identical inputs into a new output directory")
		return 1
	}
	returnStatus := struct {
		Status      string `json:"status"`
		Publication string `json:"publication"`
		Snapshot    string `json:"snapshot"`
		Sources     int    `json:"sources"`
	}{"prepared", cfg.PublicationID, result.Snapshot.SnapshotId, len(result.Sources)}
	if err = json.NewEncoder(out).Encode(returnStatus); err != nil {
		return 1
	}
	return 0
}

// Only the artifact ID is held here until registry lookup replaces the partial
// reference; it is never dispatched or accepted as a complete ArtifactRef.
func parseSnapshotSelections(corpus string, values []string) ([]indexing.InitialIndexSource, error) {
	if len(values) == 0 || len(values) > 256 {
		return nil, errors.New("1..256 sources required")
	}
	seenJobs, seenArtifacts := map[string]bool{}, map[string]bool{}
	result := make([]indexing.InitialIndexSource, 0, len(values))
	for _, value := range values {
		job, artifact, ok := strings.Cut(value, "=")
		if !ok || seenJobs[job] || seenArtifacts[artifact] {
			return nil, errors.New("invalid or duplicate source selection")
		}
		for _, id := range []string{job, artifact} {
			if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: id}, domain.DefaultWireLimits); err != nil {
				return nil, err
			}
		}
		seenJobs[job], seenArtifacts[artifact] = true, true
		result = append(result, indexing.InitialIndexSource{SourceJobID: job, DocumentBatch: &pb.ArtifactRef{ArtifactId: artifact}})
	}
	return result, nil
}

type preparedSourceFile struct {
	Job      string `json:"source_job_id"`
	Artifact string `json:"artifact_id"`
	Ref      string `json:"reference_file"`
}

func writeSnapshotPreparation(directory string, result *indexing.InitialSourceSnapshot) error {
	write := func(name string, raw []byte) error {
		file, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err = file.Write(raw); err != nil {
			return err
		}
		return file.Sync()
	}
	if err := write("corpus-facts.json", result.ManifestBytes); err != nil {
		return err
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(result.Snapshot)
	if err != nil {
		return err
	}
	if err = write("snapshot.pb", raw); err != nil {
		return err
	}
	index := []preparedSourceFile{}
	for i, source := range result.Sources {
		name := fmt.Sprintf("source-%03d.pb", i+1)
		raw, err = (proto.MarshalOptions{Deterministic: true}).Marshal(source.DocumentBatch)
		if err != nil {
			return err
		}
		if err = write(name, raw); err != nil {
			return err
		}
		index = append(index, preparedSourceFile{source.SourceJobID, source.DocumentBatch.ArtifactId, name})
	}
	raw, err = json.Marshal(index)
	if err != nil {
		return err
	}
	// Written last: presence means all referenced exports were flushed.
	return write("sources.json", raw)
}
