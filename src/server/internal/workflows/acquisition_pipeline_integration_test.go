// Exercises downloaded collector records through production import, durable coordinator,
// registry binding and an actual Rust PDF/tokenizer worker. Optional EXTRACT uses a pinned
// real gateway producer. Each run owns a disposable PostgreSQL schema; original acquisition
// is read-only and worker artifacts are retained for inspection. This is integration evidence,
// not gold quality or required performance acceptance (configs/benchmark-targets.yaml).
package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/storage"
	workeradapter "regulagraph.local/server/internal/adapters/worker"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/ingestion/sources"
)

func TestAcquiredPDFThroughNativeDocumentPipeline(t *testing.T) {
	endpoint := os.Getenv("REGULAGRAPH_TEST_DOCUMENT_WORKER")
	if endpoint == "" {
		t.Skip("actual document worker and downloaded record required")
	}
	root, recordPath, artifactRoot := os.Getenv("REGULAGRAPH_TEST_ACQUISITION_ROOT"), os.Getenv("REGULAGRAPH_TEST_ACQUISITION_RECORD"), os.Getenv("REGULAGRAPH_TEST_DOCUMENT_ARTIFACT_ROOT")
	if root == "" || recordPath == "" || artifactRoot == "" {
		t.Fatal("acquisition root/record and shared artifact root required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	repo, _ := reviewDatabase(t)
	ontologyBytes, err := os.ReadFile("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	ontology, err := domain.ParseOntologyJSONC(ontologyBytes)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record sources.Record
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	corpus := fmt.Sprintf("corpus:native-pdf:%d", time.Now().UnixNano())
	policy := domain.CandidatePlanningPolicy{ScopesByType: map[string][]string{"organization": {"ID:national"}}, MaximumMentions: 100, MaximumScopesPerMention: 4, MaximumTotalScopes: 400}
	policyHash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	// This request config pins the actual ontology and lookup policy. EXTRACT's model
	// producer is supplied explicitly when enabled; no synthetic model is substituted.
	producer := &pb.ProducerManifest{Software: "regulagraph-native-pdf-integration", Build: "actual-worker", SchemaVersion: 1, ConfigHash: ontology.ContentHash()}
	extract := os.Getenv("REGULAGRAPH_TEST_EXTRACTION_PRODUCER") != ""
	if extract {
		data, err := os.ReadFile(os.Getenv("REGULAGRAPH_TEST_EXTRACTION_PRODUCER"))
		if err != nil {
			t.Fatal(err)
		}
		if err = protojson.Unmarshal(data, producer); err != nil {
			t.Fatal(err)
		}
		if len(producer.Models) != 1 || producer.Models[0].Task != pb.ModelTask_MODEL_TASK_EXTRACT {
			t.Fatal("real EXTRACT producer required")
		}
	}
	for _, pin := range []*pb.ContentHash{ontology.ContentHash(), policyHash} {
		found := false
		for _, existing := range producer.InputHashes {
			found = found || proto.Equal(existing, pin)
		}
		if !found {
			producer.InputHashes = append(producer.InputHashes, pin)
		}
	}
	request := &pb.IngestionRequest{CorpusId: corpus, Operation: pb.JobOperation_JOB_OPERATION_INGEST, IdempotencyKey: "native-pdf:import", ConfigManifest: producer}
	prepared, err := PrepareAcquisitionImport(request, record)
	if err != nil {
		t.Fatal(err)
	}
	input, err := storage.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	artifacts, err := storage.NewFileStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer artifacts.Close()
	if err = prepared.Import(ctx, input, artifacts, repo); err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewJobScheduler(repo, ontology, map[string]domain.CandidatePlanningPolicy{corpus: policy})
	if err != nil {
		t.Fatal(err)
	}
	jobID := "job:" + corpus
	if _, _, err = scheduler.Submit(ctx, jobID, prepared.Request()); err != nil {
		t.Fatal(err)
	}
	client, err := workeradapter.New(endpoint, insecure.NewCredentials(), 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	combined, err := CombineParseExecutionStore(repo, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	parser, err := NewParseExecutor(combined, client, ParseExecutorConfig{Ontology: ontology, OwnerID: "native-pdf-test", AuthScope: "scope:native-pdf",
		Lease: 5 * time.Minute, CallTimeout: 4 * time.Minute, MaximumBatchBytes: 64 << 20, WireLimits: domain.WireLimits{MaxBytes: 64 << 20, MaxDepth: 64, MaxItems: 100000}})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := NewBindingExecutor(repo, artifacts, BindingExecutorConfig{OwnerID: "native-pdf-test", Jurisdiction: "ID", Software: "regulagraph-server", Build: "actual-worker", Language: "id",
		DocumentKind: pb.DocumentKind_DOCUMENT_KIND_REGULATION, Lease: 5 * time.Minute, MaximumBytes: 64 << 20, MaximumRecords: 100000,
		WireLimits: domain.WireLimits{MaxBytes: 64 << 20, MaxDepth: 64, MaxItems: 100000}})
	if err != nil {
		t.Fatal(err)
	}
	stages := []pb.JobStage{pb.JobStage_JOB_STAGE_PARSE, pb.JobStage_JOB_STAGE_STRUCTURE, pb.JobStage_JOB_STAGE_BIND, pb.JobStage_JOB_STAGE_CHUNK}
	if extract {
		stages = append(stages, pb.JobStage_JOB_STAGE_EXTRACT)
	}
	for _, stage := range stages {
		started := time.Now()
		if stage == pb.JobStage_JOB_STAGE_BIND {
			job, result, err := binder.RunOnce(ctx)
			if err != nil {
				cp, cpErr := repo.LoadLatestCheckpoint(ctx, jobID)
				if cpErr == nil && len(cp.CompletedBatchKeys) == 1 {
					ref, refErr := repo.LoadArtifact(ctx, corpus, cp.CompletedBatchKeys[0])
					if refErr == nil {
						data, readErr := artifacts.ReadVerified(ctx, ref, 64<<20)
						if readErr == nil {
							batch := new(pb.DocumentBatch)
							if proto.Unmarshal(data, batch) == nil {
								for _, node := range batch.Structures {
									t.Logf("structure: %v", node)
								}
							}
						}
					}
				}
				t.Fatalf("BIND: %v", err)
			}
			if job.JobID != jobID || result.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
				if raw, readErr := artifacts.ReadVerified(ctx, result.DocumentBatch, 64<<20); readErr == nil {
					batch := new(pb.DocumentBatch)
					if proto.Unmarshal(raw, batch) == nil {
						t.Logf("BIND diagnostics: %v", batch.Issues)
					}
				}
				t.Fatalf("BIND incomplete: job=%s result=%v", job.JobID, result)
			}
		} else {
			job, result, err := parser.RunOnce(ctx)
			if err != nil {
				t.Fatalf("%s: %v", stage, err)
			}
			if job.JobID != jobID || job.Stage != stage || result.Status != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
				t.Fatalf("%s unexpected job/response: %v %v", stage, job, result)
			}
		}
		checkpoint, err := repo.LoadLatestCheckpoint(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if checkpoint.Stage != stage || checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
			t.Fatalf("%s checkpoint not committed", stage)
		}
		ref, err := repo.LoadArtifact(ctx, corpus, checkpoint.CompletedBatchKeys[0])
		if err != nil {
			t.Fatal(err)
		}
		data, err := artifacts.ReadVerified(ctx, ref, 64<<20)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("stage=%s elapsed=%s corpus=%s artifact=%s bytes=%d sha256=%s", stage, time.Since(started), corpus, ref.StorageKey, len(data), ref.ContentHash.Sha256)
		if stage != pb.JobStage_JOB_STAGE_EXTRACT {
			batch := new(pb.DocumentBatch)
			if err := proto.Unmarshal(data, batch); err != nil {
				t.Fatal(err)
			}
			t.Logf("document batch: %s", batch.Meta.RecordId)
			if stage == pb.JobStage_JOB_STAGE_CHUNK {
				if batch.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || len(batch.Chunks) == 0 || len(batch.Versions) == 0 {
					t.Fatal("actual PDF must yield complete, version-bound chunks")
				}
				versions := make(map[string]bool, len(batch.Versions))
				for _, version := range batch.Versions {
					versions[version.Meta.RecordId] = true
				}
				for _, chunk := range batch.Chunks {
					if chunk.TextSpan == nil || len(chunk.StructureNodeRefs) == 0 || len(chunk.TokenCounts) == 0 || len(chunk.ProvisionVersionRefs) == 0 {
						t.Fatal("chunk missing source, structure, tokenizer accounting or provision version")
					}
					for _, ref := range chunk.ProvisionVersionRefs {
						if !versions[ref] {
							t.Fatalf("chunk refers to absent provision version %s", ref)
						}
					}
				}
				t.Logf("verified chunks=%d versions=%d structures=%d", len(batch.Chunks), len(batch.Versions), len(batch.Structures))
			}
		} else {
			batch := new(pb.ExtractionBatch)
			if err := proto.Unmarshal(data, batch); err != nil {
				t.Fatal(err)
			}
			t.Logf("extraction model=%s mentions=%d assertions=%d", batch.ModelManifest.ModelId, len(batch.Mentions), len(batch.Assertions))
		}
	}
}
