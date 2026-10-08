// Operator handoff from prepare-snapshot + Rust freeze to durable INDEX jobs.
// Reads bounded C01 refs/snapshot and the local source export index, pins the real
// native model manifest, resolves authoritative registry refs, and invokes the
// indexing bootstrap. The source JSON is an export manifest, not a wire schema.
// No inference or publication happens inline. Errors are redacted and successful
// output means scheduled, not search-ready. Measure preparation separately from
// required query/model benchmarks in configs/benchmark-targets.yaml.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/indexing"
)

func readPreparationFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maximum {
		return nil, errors.New("preparation input must be a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maximum {
		return nil, errors.New("preparation input exceeded bound")
	}
	return raw, nil
}

func readPreparedSnapshot(directory string) (*pb.SnapshotRef, []indexing.InitialIndexSource, error) {
	raw, err := readPreparationFile(filepath.Join(directory, "snapshot.pb"), 64<<10)
	if err != nil {
		return nil, nil, err
	}
	snapshot := new(pb.SnapshotRef)
	if err = domain.DecodeWire(raw, snapshot, domain.DefaultWireLimits); err != nil {
		return nil, nil, err
	}
	raw, err = readPreparationFile(filepath.Join(directory, "sources.json"), 1<<20)
	if err != nil {
		return nil, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var index []preparedSourceFile
	if err = decoder.Decode(&index); err != nil {
		return nil, nil, err
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return nil, nil, errors.New("source manifest has trailing data")
	}
	values := make([]string, len(index))
	for i, source := range index {
		values[i] = source.Job + "=" + source.Artifact
	}
	sources, err := parseSnapshotSelections(snapshot.CorpusId, values)
	return snapshot, sources, err
}

func runPrepareIndex(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("prepare-index", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var cfg indexing.InitialIndexBootstrapConfig
	fs.StringVar(&cfg.PublicationID, "publication", "", "Publication reserved by prepare-snapshot")
	fs.StringVar(&cfg.Collection, "collection", "", "New immutable Qdrant collection binding")
	fs.StringVar(&cfg.AuthScope, "auth-scope", "", "Same scope as prepare-snapshot/coordinator")
	fs.StringVar(&cfg.OntologyVersion, "ontology-version", "", "Pinned ontology version for this generation")
	fs.IntVar(&cfg.ChunksPerBatch, "chunks-per-batch", 64, "1..128 chunks per durable child INDEX job")
	directory := fs.String("snapshot-directory", "", "Output directory of prepare-snapshot")
	dictionary := fs.String("dictionary-ref", "", "C01 binary reference produced by prepare-dictionary")
	statistics := fs.String("statistics-ref", "", "C01 binary reference produced by Rust freeze")
	model := fs.String("model-manifest", "", "Native embedding model.pbjson")
	modelHash := fs.String("model-sha256", "", "SHA-256 of exact native model manifest bytes")
	timeout := fs.Duration("timeout", 5*time.Minute, "Total preparation deadline, up to 30m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dsn, root := os.Getenv("REGULAGRAPH_POSTGRES_DSN"), os.Getenv("REGULAGRAPH_ARTIFACTS_DIR")
	cfg.Endpoint = os.Getenv("REGULAGRAPH_QDRANT_URL")
	if fs.NArg() != 0 || *directory == "" || *dictionary == "" || *statistics == "" || *model == "" || *modelHash == "" || dsn == "" || root == "" || cfg.Endpoint == "" || cfg.PublicationID == "" || cfg.Collection == "" || cfg.AuthScope == "" || cfg.OntologyVersion == "" || cfg.ChunksPerBatch < 1 || cfg.ChunksPerBatch > 128 || *timeout <= 0 || *timeout > 30*time.Minute {
		fmt.Fprintln(errOut, "Invalid INDEX preparation arguments/configuration")
		return 2
	}
	snapshot, sources, err := readPreparedSnapshot(*directory)
	if err != nil {
		fmt.Fprintln(errOut, "Invalid prepared snapshot/source export")
		return 2
	}
	cfg.Snapshot = snapshot
	cfg.Dictionary, cfg.Statistics = new(pb.ArtifactRef), new(pb.ArtifactRef)
	for _, input := range []struct {
		path    string
		message proto.Message
	}{{*dictionary, cfg.Dictionary}, {*statistics, cfg.Statistics}} {
		raw, e := readPreparationFile(input.path, 64<<10)
		if e != nil || domain.DecodeWire(raw, input.message, domain.DefaultWireLimits) != nil {
			fmt.Fprintln(errOut, "Invalid lexical artifact reference")
			return 2
		}
	}
	cfg.Model, err = config.LoadNativeModel(*model, *modelHash, pb.ModelTask_MODEL_TASK_EMBED)
	if err != nil {
		fmt.Fprintln(errOut, "Invalid embedding model manifest/pin")
		return 2
	}
	modelPath, binaryHash, err := exportWorkerEmbeddingModel(*directory, cfg.Model)
	if err != nil {
		fmt.Fprintln(errOut, "Cannot export pinned worker model; existing bytes must match exactly")
		return 1
	}
	bounded, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	repo, err := postgres.Open(bounded, postgres.Config{DSN: dsn, MaxConnections: 4, HealthTimeout: 5 * time.Second})
	if err != nil {
		fmt.Fprintln(errOut, "INDEX database unavailable")
		return 1
	}
	defer repo.Close()
	for i, source := range sources {
		ref, e := repo.LoadArtifact(bounded, snapshot.CorpusId, source.DocumentBatch.ArtifactId)
		if e != nil {
			fmt.Fprintln(errOut, "Selected source no longer registered in the snapshot corpus")
			return 1
		}
		sources[i].DocumentBatch = ref
	}
	files, err := storage.NewFileStore(root)
	if err != nil {
		fmt.Fprintln(errOut, "Artifact store unavailable")
		return 1
	}
	defer files.Close()
	inventory, err := indexing.BootstrapInitialIndex(bounded, repo, files, files, cfg, sources)
	if err != nil {
		fmt.Fprintln(errOut, "INDEX preparation failed; inspect snapshot/source/statistics/model consistency and retry identical inputs")
		return 1
	}
	result := struct {
		Status      string `json:"status"`
		Publication string `json:"publication"`
		Generation  string `json:"generation"`
		Jobs        int    `json:"jobs"`
		WorkerModel string `json:"worker_model_manifest"`
		WorkerHash  string `json:"worker_model_manifest_sha256"`
	}{"scheduled", cfg.PublicationID, snapshot.RepresentationGeneration, len(inventory.Assignments), modelPath, binaryHash}
	if err = json.NewEncoder(out).Encode(result); err != nil {
		return 1
	}
	return 0
}

// Export the same validated manifest consumed by native Go configuration as C01
// binary for the Rust worker. The binary pin differs from the JSON byte pin.
// Exact replay is allowed; differing or interrupted exports are never overwritten.
// Export precedes scheduling so queued work always has an available model pin.
func exportWorkerEmbeddingModel(directory string, model *pb.ModelManifest) (string, string, error) {
	if err := domain.ValidateWire(model, domain.DefaultWireLimits); err != nil {
		return "", "", err
	}
	if model.Task != pb.ModelTask_MODEL_TASK_EMBED {
		return "", "", errors.New("embedding manifest required")
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(model)
	if err != nil || len(raw) == 0 || len(raw) > 64<<10 {
		return "", "", errors.New("invalid bounded worker model")
	}
	path, err := filepath.Abs(filepath.Join(directory, "embedding-model.pb"))
	if err != nil {
		return "", "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := readPreparationFile(path, 64<<10)
		if readErr != nil || !bytes.Equal(existing, raw) {
			return "", "", errors.New("existing worker model differs")
		}
	} else if err != nil {
		return "", "", err
	} else {
		_, err = file.Write(raw)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return "", "", err
		}
		if closeErr != nil {
			return "", "", closeErr
		}
	}
	return path, fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
