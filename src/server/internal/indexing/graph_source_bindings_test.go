// Exercises graph source receipt storage after a real INDEX publication fixture.
// Default EXTRACT/RESOLVE and checkpoint are empty synthetic inputs. The native
// variant supplies nonempty EXTRACT/review fixtures, commits real RESOLVE and
// dispatches Rust; neither variant proves live LLM quality or full ingestion.
// Derived artifacts use FileStore; PostgreSQL/Qdrant establish base membership.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func checkGraphSourceBindingReceipt(t *testing.T, ctx context.Context, repo *postgres.Repository, conn *pgx.Conn, dsn string,
	sourceBinding domain.IndexSourceBinding, artifacts indexMemoryArtifacts, nativeRPC bool) {
	t.Helper()
	corpus, job := sourceBinding.Snapshot.CorpusId, sourceBinding.SourceJobID
	original := new(pb.DocumentBatch)
	if err := domain.DecodeWire(artifacts[sourceBinding.Original.ArtifactId], original, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if nativeRPC {
		root = os.Getenv("REGULAGRAPH_TEST_GRAPH_ARTIFACT_ROOT")
		if root == "" {
			t.Fatal("REGULAGRAPH_TEST_GRAPH_ARTIFACT_ROOT required for native ASSEMBLE")
		}
	}
	files, err := storage.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	put := func(m proto.Message, kind, media string) domain.GraphSourceArtifact {
		t.Helper()
		raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		ref := &pb.ArtifactRef{ArtifactId: "artifact:" + kind + ":" + hash, ContentHash: &pb.ContentHash{Sha256: hash},
			StorageKey: "sha256/" + hash[:2] + "/" + hash[2:4] + "/" + hash + ".bin", ByteSize: uint64(len(raw)), SchemaVersion: 1, MediaType: media}
		if _, e = files.Put(ctx, ref, bytes.NewReader(raw)); e != nil {
			t.Fatal(e)
		}
		if e = repo.RegisterArtifact(ctx, corpus, ref); e != nil {
			t.Fatal(e)
		}
		return domain.GraphSourceArtifact{Reference: ref, Bytes: raw}
	}
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	ontologyBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "configs", "ontology-v1.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	ontology, err := domain.ParseOntologyJSONC(ontologyBytes)
	if err != nil {
		t.Fatal(err)
	}
	ontologyHash := ontology.ContentHash()
	model := &pb.ModelManifest{ModelId: "model:extract-fixture", Version: "1", Task: pb.ModelTask_MODEL_TASK_EXTRACT,
		WeightsHash: hash, TokenizerHash: hash, PromptHash: hash, MaxTokens: 128, Precision: "fp32", Backend: "fixture"}
	producer := &pb.ProducerManifest{Software: "fixture", Build: "1", SchemaVersion: 1, Models: []*pb.ModelManifest{model}, PromptHashes: []*pb.ContentHash{hash}, InputHashes: []*pb.ContentHash{ontologyHash}, ConfigHash: hash}
	extraction := &pb.ExtractionBatch{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "extract:empty-for-binding"},
		Context: proto.Clone(original.Context).(*pb.RequestContext), SourceDocumentBatch: sourceBinding.Original,
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependencies:empty-extract", ProducerManifest: producer,
			Dependencies: []*pb.Dependency{{DependencyId: sourceBinding.Original.ArtifactId, Fingerprint: sourceBinding.Original.ContentHash}}},
		Completeness: pb.Completeness_COMPLETENESS_COMPLETE, OntologyVersion: "id-regulation-ontology-v1", ModelManifest: model, PromptHash: hash,
		ItemCounts: &pb.Counts{Expected: uint64(len(original.Chunks)), Accepted: uint64(len(original.Chunks))}, TokenUsage: &pb.TokenUsage{TokenizerId: "fixture"}}
	if nativeRPC {
		populateNativeGraphExtraction(t, original, extraction, artifacts)
	}
	extract := put(extraction, "original-extract", domain.ExtractionBatchMediaType)
	var revision uint64
	if err = conn.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, corpus).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	resolveModel := proto.Clone(model).(*pb.ModelManifest)
	resolveModel.Task = pb.ModelTask_MODEL_TASK_RESOLVE
	resolveProducer := proto.Clone(producer).(*pb.ProducerManifest)
	resolveProducer.Models = []*pb.ModelManifest{resolveModel}
	resolution := &pb.ResolutionBatch{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "resolve:empty-for-binding"},
		Context: proto.Clone(original.Context).(*pb.RequestContext), SourceExtractionBatch: extract.Reference,
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependencies:empty-resolve", ProducerManifest: resolveProducer,
			Dependencies: []*pb.Dependency{{DependencyId: extract.Reference.ArtifactId, Fingerprint: extract.Reference.ContentHash}}},
		Completeness: pb.Completeness_COMPLETENESS_COMPLETE, OntologyVersion: extraction.OntologyVersion, RegistryRevision: revision,
		ModelManifest: resolveModel, ItemCounts: &pb.Counts{}, TokenUsage: &pb.TokenUsage{TokenizerId: "fixture"}}
	var resolve domain.GraphSourceArtifact
	sourceCheckpointID := "checkpoint:graph-binding"
	if nativeRPC {
		var output *workflows.SemanticResolutionOutput
		output = commitNativeGraphResolution(t, ctx, repo, conn, files, job, extraction, extract, resolveModel, resolveProducer, put)
		resolution, revision, sourceCheckpointID = output.Batch, output.Batch.RegistryRevision, output.Checkpoint.Meta.RecordId
		raw, e := files.ReadVerified(ctx, output.Artifact, uint64(domain.DefaultWireLimits.MaxBytes))
		if e != nil {
			t.Fatal(e)
		}
		resolve = domain.GraphSourceArtifact{Reference: output.Artifact, Bytes: raw}
		intent, e := repo.LoadSemanticResolutionIntent(ctx, corpus, job)
		if e != nil {
			t.Fatal(e)
		}
		artifacts[intent.CandidateRef.ArtifactId], e = files.ReadVerified(ctx, intent.CandidateRef, uint64(domain.DefaultWireLimits.MaxBytes))
		if e != nil {
			t.Fatal(e)
		}
	} else {
		resolve = put(resolution, "original-resolve", "application/x-protobuf")
	}
	artifacts[extract.Reference.ArtifactId] = extract.Bytes
	artifacts[resolve.Reference.ArtifactId] = resolve.Bytes
	checkpoint := func(id string) {
		t.Helper()
		cp := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: id}, JobId: job, Stage: pb.JobStage_JOB_STAGE_RESOLVE, Fence: 2,
			CompletedBatchKeys: []string{resolve.Reference.ArtifactId}, ArtifactHashes: []*pb.ContentHash{resolve.Reference.ContentHash},
			Manifest: resolveProducer, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
		raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = conn.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status) VALUES($1,$2,$3,2,$4,$5,$6)`, id, job, int16(cp.Stage), raw, fmt.Sprintf("%x", sha256.Sum256(raw)), int16(cp.TerminalStatus)); e != nil {
			t.Fatal(e)
		}
		if _, e = conn.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2,stage=$3,state=$4,lease_fence=2 WHERE job_id=$1`, job, id, int16(cp.Stage), int16(pb.JobState_JOB_STATE_STAGED)); e != nil {
			t.Fatal(e)
		}
	}
	if !nativeRPC {
		checkpoint(sourceCheckpointID)
	}
	boundE, boundR, err := domain.BindGraphSourceEnvelopes(job,
		domain.GraphSourceArtifact{Reference: sourceBinding.Original, Bytes: artifacts[sourceBinding.Original.ArtifactId]},
		domain.GraphSourceArtifact{Reference: sourceBinding.Bound, Bytes: artifacts[sourceBinding.Bound.ArtifactId]}, extract, resolve, 4096)
	if err != nil {
		t.Fatal(err)
	}
	target, err := repo.ReservePublication(ctx, "graph:"+sourceBinding.PublicationID, "", corpus, "graph:"+sourceBinding.Snapshot.SnapshotId, sourceBinding.Snapshot.SnapshotId)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.BindPublicationRegistry(ctx, target.PublicationID, corpus, target.Fence, revision); err != nil {
		t.Fatal(err)
	}
	binding := domain.GraphSourceBinding{Policy: domain.GraphSourceEnvelopePolicy, PublicationID: target.PublicationID, Fence: target.Fence,
		TargetSequence: target.Sequence, RegistryRevision: revision, SourceCheckpointID: sourceCheckpointID, Source: sourceBinding,
		OriginalExtraction: extract.Reference, OriginalResolution: resolve.Reference, BoundExtraction: boundE.Reference, BoundResolution: boundR.Reference}
	input := domain.GraphSourceBindingInputs{OriginalDocument: artifacts[sourceBinding.Original.ArtifactId], SnapshotDocument: artifacts[sourceBinding.Bound.ArtifactId], Extraction: extract.Bytes, Resolution: resolve.Bytes}
	pin, err := repo.PinActiveSnapshot(ctx, corpus, "lease:graph-binding", "owner:graph-binding", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), pin.LeaseID, pin.OwnerID)
	checkAbsent := func() {
		t.Helper()
		if _, e := repo.LoadGraphSourceBinding(ctx, corpus, target.PublicationID, job); !errors.Is(e, postgres.ErrNotFound) {
			t.Fatalf("failed admission left receipt: %v", e)
		}
	}
	if err = repo.RegisterGraphSourceBinding(ctx, pin, binding, input, 4096); err == nil {
		t.Fatal("unregistered derived artifacts admitted")
	}
	checkAbsent()
	for _, artifact := range []domain.GraphSourceArtifact{boundE, boundR} {
		if _, err = files.Put(ctx, artifact.Reference, bytes.NewReader(artifact.Bytes)); err != nil {
			t.Fatal(err)
		}
		if err = repo.RegisterArtifact(ctx, corpus, artifact.Reference); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"bytes", "fence", "checkpoint", "derived ref", "cancelled", "released pin"} {
		t.Run("receipt rejects "+name, func(t *testing.T) {
			bad, inputs, badPin := binding, input, pin
			switch name {
			case "bytes":
				inputs.Extraction = append([]byte(nil), input.Extraction...)
				inputs.Extraction[0] ^= 1
			case "fence":
				bad.Fence++
			case "checkpoint":
				bad.SourceCheckpointID += "-foreign"
			case "derived ref":
				bad.BoundExtraction = proto.Clone(binding.BoundExtraction).(*pb.ArtifactRef)
				bad.BoundExtraction.StorageKey += ".wrong"
			case "cancelled":
				if _, e := conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, job); e != nil {
					t.Fatal(e)
				}
				defer conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, job)
			case "released pin":
				badPin.LeaseID = "lease:absent"
			}
			if e := repo.RegisterGraphSourceBinding(ctx, badPin, bad, inputs, 4096); e == nil {
				t.Fatal("invalid receipt admitted")
			}
			checkAbsent()
		})
	}
	t.Run("cancellation while receipt waits for source lock", func(t *testing.T) {
		locked, e := conn.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer locked.Rollback(context.Background())
		if _, e = locked.Exec(ctx, `SELECT job_id FROM jobs WHERE job_id=$1 FOR UPDATE`, job); e != nil {
			t.Fatal(e)
		}
		waiting, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- repo.RegisterGraphSourceBinding(waiting, pin, binding, input, 4096) }()
		defer func() { cancel(); locked.Rollback(context.Background()) }()
		blocked := false
		deadline := time.Now().Add(3 * time.Second)
		for !blocked && time.Now().Before(deadline) {
			if e = locked.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND pg_backend_pid()=ANY(pg_blocking_pids(pid)))`).Scan(&blocked); e != nil {
				t.Fatal(e)
			}
			if !blocked {
				time.Sleep(10 * time.Millisecond)
			}
		}
		if !blocked {
			t.Fatal("receipt writer never waited on source lock")
		}
		if _, e = locked.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, job); e != nil {
			t.Fatal(e)
		}
		if e = locked.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		defer conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, job)
		if e = <-result; !errors.Is(e, postgres.ErrConflict) {
			t.Fatalf("late cancellation admitted receipt: %v", e)
		}
		checkAbsent()
	})
	request := binding
	t.Run("lease release while receipt waits for publication lock", func(t *testing.T) {
		racePin, e := repo.PinActiveSnapshot(ctx, corpus, "lease:graph-binding-race", "owner:graph-binding-race", 30*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		defer repo.ReleaseSnapshotPin(context.Background(), racePin.LeaseID, racePin.OwnerID)
		locked, e := conn.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer locked.Rollback(context.Background())
		if _, e = locked.Exec(ctx, `SELECT publication_id FROM snapshots WHERE publication_id=$1 FOR UPDATE`, target.PublicationID); e != nil {
			t.Fatal(e)
		}
		waiting, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- repo.RegisterGraphSourceBinding(waiting, racePin, binding, input, 4096) }()
		blocked := false
		deadline := time.Now().Add(3 * time.Second)
		for !blocked && time.Now().Before(deadline) {
			if e = locked.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND pg_backend_pid()=ANY(pg_blocking_pids(pid)))`).Scan(&blocked); e != nil {
				t.Fatal(e)
			}
			if !blocked {
				time.Sleep(10 * time.Millisecond)
			}
		}
		if !blocked {
			t.Fatal("receipt writer never waited on publication lock")
		}
		if e = repo.ReleaseSnapshotPin(ctx, racePin.LeaseID, racePin.OwnerID); e != nil {
			t.Fatal(e)
		}
		if e = locked.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
		if e = <-result; !errors.Is(e, domain.ErrLeaseUnavailable) {
			t.Fatalf("released lease admitted receipt: %v", e)
		}
		checkAbsent()
	})
	request.BoundExtraction, request.BoundResolution = nil, nil
	interrupted := &interruptedGraphSourceReceipt{Repository: repo, fail: true}
	if _, err = workflows.BindGraphSource(ctx, interrupted, artifacts, files, pin, request, 4096); !errors.Is(err, errGraphReceiptInterrupted) {
		t.Fatalf("expected interruption after immutable artifact writes: %v", err)
	}
	checkAbsent()
	interrupted.lostAck = true
	if _, err = workflows.BindGraphSource(ctx, interrupted, artifacts, files, pin, request, 4096); !errors.Is(err, errGraphReceiptLostAck) {
		t.Fatalf("expected lost acknowledgement after commit: %v", err)
	}
	if _, err = repo.LoadGraphSourceBinding(ctx, corpus, target.PublicationID, job); err != nil {
		t.Fatal("lost acknowledgement did not retain receipt", err)
	}
	if _, err = workflows.BindGraphSource(ctx, interrupted, artifacts, files, pin, request, 4096); err != nil {
		t.Fatal("workflow recovery", err)
	}
	for range 2 {
		if err = repo.RegisterGraphSourceBinding(ctx, pin, binding, input, 4096); err != nil {
			t.Fatal("receipt/replay", err)
		}
	}
	stored, err := repo.LoadGraphSourceBinding(ctx, corpus, target.PublicationID, job)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(binding)
	got, _ := json.Marshal(stored)
	if !bytes.Equal(want, got) {
		t.Fatal("receipt replay drift")
	}
	reopened, err := postgres.Open(ctx, postgres.Config{DSN: dsn, MaxConnections: 1, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.RegisterGraphSourceBinding(ctx, pin, binding, input, 4096); err != nil {
		t.Fatal("single-connection replay", err)
	}
	reopened.Close()
	artifacts[boundE.Reference.ArtifactId], artifacts[boundR.Reference.ArtifactId] = boundE.Bytes, boundR.Bytes
	assemblyConfig := workflows.GraphAssemblyPreparationConfig{CorpusID: corpus, PublicationID: target.PublicationID,
		SourceJobID: job, Producer: &pb.ProducerManifest{Software: "graph-assembly", Build: "fixture", SchemaVersion: 1, ConfigHash: hash},
		OntologyHash: ontologyHash, MaximumReferences: 4096, MaximumCandidates: 32}
	prepared, err := workflows.PrepareGraphAssembly(ctx, repo, artifacts, files, pin, assemblyConfig)
	if err != nil {
		t.Fatal("prepare graph plan", err)
	}
	// Corrupt a seeded identity while preserving all source bytes and receipts.
	// Preparation must execute the registry gate, not merely trust artifact hashes.
	if _, err = conn.Exec(ctx, `UPDATE canonical_identities SET identity_scope=identity_scope || ':fault' WHERE corpus_id=$1 AND canonical_id=$2`, corpus, original.Regulations[0].IssuerId); err != nil {
		t.Fatal(err)
	}
	if _, err = workflows.PrepareGraphAssembly(ctx, repo, artifacts, files, pin, assemblyConfig); !errors.Is(err, domain.ErrResolutionReplan) {
		t.Fatalf("changed document registry identity admitted by graph preparation: %v", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE canonical_identities SET identity_scope=$2 WHERE corpus_id=$1 AND canonical_id=$3`, corpus, domain.IssuerIdentityKeyNamespace, original.Regulations[0].IssuerId); err != nil {
		t.Fatal(err)
	}
	wrongOntology := assemblyConfig
	wrongOntology.OntologyHash = &pb.ContentHash{Sha256: strings.Repeat("b", 64)}
	if _, err = workflows.PrepareGraphAssembly(ctx, repo, artifacts, files, pin, wrongOntology); err == nil {
		t.Fatal("ontology differs from EXTRACT producer but plan accepted")
	}
	replayedPlan, err := workflows.PrepareGraphAssembly(ctx, repo, artifacts, files, pin, assemblyConfig)
	if err != nil || !proto.Equal(prepared.Plan, replayedPlan.Plan) || !proto.Equal(prepared.Reference, replayedPlan.Reference) {
		t.Fatalf("graph preparation replay differs: %v", err)
	}
	planBytes, err := files.ReadVerified(ctx, prepared.Reference, uint64(domain.DefaultWireLimits.MaxBytes))
	if err != nil {
		t.Fatal(err)
	}
	readPlan := new(pb.GraphAssemblyPlan)
	if err = domain.DecodeWire(planBytes, readPlan, domain.DefaultWireLimits); err != nil || !proto.Equal(readPlan, prepared.Plan) {
		t.Fatalf("persisted ASSEMBLE plan drift: %v", err)
	}
	viewBytes, err := files.ReadVerified(ctx, readPlan.RegistryView, uint64(domain.DefaultWireLimits.MaxBytes))
	if err != nil {
		t.Fatal(err)
	}
	readView := new(pb.RegistryEntityView)
	wantEntities := 0
	if nativeRPC {
		wantEntities = 2
	}
	if err = domain.DecodeWire(viewBytes, readView, domain.DefaultWireLimits); err != nil || len(readView.Entities) != wantEntities || domain.ValidateAssemblyRegistryBinding(readPlan, readView) != nil {
		t.Fatalf("persisted ASSEMBLE canonical view drift: %v", err)
	}
	if nativeRPC {
		checkNativeGraphExecution(t, ctx, repo, conn, files, artifacts, pin, prepared, binding, input, viewBytes, ontology)
		return // Native path now publishes the target; open-publication abort cases run in the non-native fixture.
	} else {
		checkGraphJobInventory(t, ctx, repo, conn, dsn, pin, prepared, binding, input, viewBytes)
	}
	cancelling := graphPreparationCancelledStore{Repository: repo, cancel: func() error {
		_, e := conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, job)
		return e
	}}
	if _, err = workflows.PrepareGraphAssembly(ctx, cancelling, artifacts, files, pin, assemblyConfig); err == nil {
		t.Fatal("source cancelled after artifact writes still returned prepared graph plan")
	}
	if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if _, err = workflows.PrepareGraphAssembly(ctx, repo, artifacts, files, pin, assemblyConfig); err != nil {
		t.Fatal("replay after late cancellation", err)
	}
	checkpoint("checkpoint:graph-binding-new")
	changed := binding
	changed.SourceCheckpointID = "checkpoint:graph-binding-new"
	if err = repo.RegisterGraphSourceBinding(ctx, pin, changed, input, 4096); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("changed immutable receipt: %v", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE jobs SET latest_checkpoint_id=$2 WHERE job_id=$1`, job, binding.SourceCheckpointID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE graph_source_bindings SET fence=fence WHERE publication_id=$1`, target.PublicationID); err == nil {
		t.Fatal("receipt mutable")
	}
	if _, err = conn.Exec(ctx, `DELETE FROM graph_source_bindings WHERE publication_id=$1`, target.PublicationID); err == nil {
		t.Fatal("receipt deletable")
	}
	if err = repo.AbortPublication(ctx, target.PublicationID); err != nil {
		t.Fatal(err)
	}
	if err = repo.RegisterGraphSourceBinding(ctx, pin, binding, input, 4096); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("aborted target admitted binding: %v", err)
	}
	if _, err = repo.LoadGraphSourceBinding(ctx, corpus, target.PublicationID, job); err != nil {
		t.Fatal("aborted audit receipt lost", err)
	}
}

var errGraphReceiptInterrupted = errors.New("fixture interruption before receipt commit")
var errGraphReceiptLostAck = errors.New("fixture lost acknowledgement after receipt commit")

type graphPreparationCancelledStore struct {
	*postgres.Repository
	cancel func() error
}

func (s graphPreparationCancelledStore) EnsureArtifactDependencyManifest(ctx context.Context, corpus, id string, manifest *pb.DependencyManifest) error {
	if err := s.Repository.EnsureArtifactDependencyManifest(ctx, corpus, id, manifest); err != nil {
		return err
	}
	if strings.HasPrefix(id, "plan:assembly:") {
		return s.cancel()
	}
	return nil
}

type interruptedGraphSourceReceipt struct {
	*postgres.Repository
	fail    bool
	lostAck bool
}

func (s *interruptedGraphSourceReceipt) RegisterGraphSourceBinding(ctx context.Context, pin domain.SnapshotPin,
	binding domain.GraphSourceBinding, inputs domain.GraphSourceBindingInputs, maximumEdges int) error {
	if s.fail {
		s.fail = false
		return errGraphReceiptInterrupted
	}
	err := s.Repository.RegisterGraphSourceBinding(ctx, pin, binding, inputs, maximumEdges)
	if err == nil && s.lostAck {
		s.lostAck = false
		return errGraphReceiptLostAck
	}
	return err
}
