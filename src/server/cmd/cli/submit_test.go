// Exercises CLI admission for a durable ingestion request before any PostgreSQL side effect.
// It checks exact corpus policy selection, preservation of producer inputs, and repeatable
// hash attachment; database idempotency and end-to-end model quality are separate checks.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func submitFixture(t *testing.T) ([]byte, *domain.Ontology, map[string]domain.CandidatePlanningPolicy) {
	t.Helper()
	ontologyBytes, err := os.ReadFile("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	ontology, err := domain.ParseOntologyJSONC(ontologyBytes)
	if err != nil {
		t.Fatal(err)
	}
	configDigest := sha256.Sum256([]byte("fixture-ingestion-config"))
	sourceDigest := sha256.Sum256([]byte("fixture-pdf"))
	request := &pb.IngestionRequest{CorpusId: "corpus:submit-fixture",
		Sources: []*pb.SourceLocator{{PortalId: "bpk",
			Locator: &pb.SourceLocator_Blob{Blob: &pb.ArtifactRef{
				ArtifactId: "artifact:submit-fixture", ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(sourceDigest[:])},
				StorageKey: "sha256/fixture.pdf", MediaType: "application/pdf", ByteSize: 11,
				SchemaVersion: 1}}}},
		Operation: pb.JobOperation_JOB_OPERATION_INGEST, IdempotencyKey: "submit:fixture",
		ConfigManifest: &pb.ProducerManifest{Software: "submit-fixture", Build: "test",
			SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: hex.EncodeToString(configDigest[:])}}}
	raw, err := protojson.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	policies := map[string]domain.CandidatePlanningPolicy{request.CorpusId: {
		ScopesByType:    map[string][]string{"organization": {"ID:national"}},
		MaximumMentions: 100, MaximumScopesPerMention: 4, MaximumTotalScopes: 400}}
	return raw, ontology, policies
}

func TestSubmitCommandPersistsPinnedRequestAndReplays(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_SUBMIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("REGULAGRAPH_TEST_SUBMIT_POSTGRES_DSN is required for an isolated PostgreSQL submit test")
	}
	if general := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN"); general != "" && general == dsn {
		t.Fatal("submit integration DB must be separate from concurrently cleaned PostgreSQL package DB")
	}
	raw, ontology, policies := submitFixture(t)
	request := new(pb.IngestionRequest)
	if err := protojson.Unmarshal(raw, request); err != nil {
		t.Fatal(err)
	}
	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	request.IdempotencyKey = "submit:cli:" + unique
	requestBytes, err := protojson.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	requestPath := filepath.Join(temp, "request.json")
	policyPath := filepath.Join(temp, "policy.json")
	producerPath := filepath.Join(temp, "resolver.json")
	resolver := proto.Clone(request.ConfigManifest).(*pb.ProducerManifest)
	resolver.Models = []*pb.ModelManifest{{ModelId: "local:resolver", Version: "v1", Task: pb.ModelTask_MODEL_TASK_RESOLVE,
		WeightsHash: ontology.ContentHash(), TokenizerHash: ontology.ContentHash(), PromptHash: ontology.ContentHash(),
		MaxTokens: 1024, Precision: "fp32", Backend: "fixture"}}
	producerBytes, err := protojson.Marshal(resolver)
	if err != nil {
		t.Fatal(err)
	}
	producerDigest := sha256.Sum256(producerBytes)
	for path, content := range map[string][]byte{
		requestPath:  requestBytes,
		producerPath: producerBytes,
		policyPath:   []byte(`{"schema_version":1,"corpora":{"corpus:submit-fixture":{"scopes_by_type":{"organization":["ID:national"]},"maximum_mentions":100,"maximum_scopes_per_mention":4,"maximum_total_scopes":400}}}`),
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policyBytes, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest := sha256.Sum256(policyBytes)
	ontologyPath := "../../../../configs/ontology-v1.jsonc"
	t.Setenv("REGULAGRAPH_POSTGRES_DSN", dsn)
	t.Setenv("REGULAGRAPH_ONTOLOGY_PATH", ontologyPath)
	t.Setenv("REGULAGRAPH_ONTOLOGY_SHA256", ontology.ContentHash().Sha256)
	t.Setenv("REGULAGRAPH_CANDIDATE_POLICY_PATH", policyPath)
	t.Setenv("REGULAGRAPH_CANDIDATE_POLICY_SHA256", hex.EncodeToString(policyDigest[:]))
	t.Setenv("REGULAGRAPH_RESOLUTION_PRODUCER_PATH", producerPath)
	t.Setenv("REGULAGRAPH_RESOLUTION_PRODUCER_SHA256", hex.EncodeToString(producerDigest[:]))
	jobID := "job:cli:" + unique
	var first, second struct {
		JobID  string `json:"job_id"`
		Reused bool   `json:"reused"`
	}
	for attempt, result := range []*struct {
		JobID  string `json:"job_id"`
		Reused bool   `json:"reused"`
	}{&first, &second} {
		var out, errOut bytes.Buffer
		code := run(context.Background(), []string{"submit", "-request", requestPath, "-job-id", jobID}, &out, &errOut)
		if code != 0 || json.Unmarshal(out.Bytes(), result) != nil || result.JobID != jobID || result.Reused != (attempt == 1) {
			t.Fatalf("submit attempt %d: code=%d out=%s err=%s", attempt, code, out.String(), errOut.String())
		}
	}
	repo, err := postgres.Open(context.Background(), postgres.Config{DSN: dsn,
		MaxConnections: 2, MinConnections: 1, ConnectTimeout: 5 * time.Second, HealthTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	stored, err := repo.LoadIngestionRequest(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	policyHash, err := policies[request.CorpusId].Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.GetConfigManifest().GetInputHashes()) != 3 ||
		!proto.Equal(stored.ConfigManifest.InputHashes[1], policyHash) ||
		stored.ConfigManifest.InputHashes[2].Sha256 != hex.EncodeToString(producerDigest[:]) {
		t.Fatal("persisted submit request lacks exact corpus policy pin")
	}
}

func TestPrepareSubmitRequestPinsConfiguredCorpusPolicy(t *testing.T) {
	raw, ontology, policies := submitFixture(t)
	request, err := prepareSubmitRequest(raw, ontology, policies)
	if err != nil {
		t.Fatal(err)
	}
	policyHash, err := policies[request.CorpusId].Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	hasHash := func(hash *pb.ContentHash) bool {
		for _, input := range request.ConfigManifest.InputHashes {
			if proto.Equal(input, hash) {
				return true
			}
		}
		return false
	}
	if len(request.ConfigManifest.InputHashes) != 2 || !hasHash(ontology.ContentHash()) || !hasHash(policyHash) {
		t.Fatalf("request lost ontology/policy pins: %+v", request.ConfigManifest.InputHashes)
	}
	againRaw, err := protojson.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := prepareSubmitRequest(againRaw, ontology, policies)
	if err != nil || !proto.Equal(request, again) {
		t.Fatalf("CLI appended duplicate pins on replay: %v", err)
	}
	foreign := proto.Clone(request).(*pb.IngestionRequest)
	foreign.CorpusId = "corpus:foreign"
	foreignRaw, err := protojson.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepareSubmitRequest(foreignRaw, ontology, policies); err == nil {
		t.Fatal("CLI accepted an unconfigured corpus")
	}
	urlOnly := proto.Clone(request).(*pb.IngestionRequest)
	urlOnly.Sources[0].Locator = &pb.SourceLocator_Url{Url: "https://example.invalid/regulation.pdf"}
	urlRaw, err := protojson.Marshal(urlOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepareSubmitRequest(urlRaw, ontology, policies); err == nil {
		t.Fatal("CLI queued a URL for an ACQUIRE stage without a dispatcher")
	}
	if _, err = prepareSubmitRequest([]byte(strings.TrimSuffix(string(raw), "}")+`,"unexpected":true}`),
		ontology, policies); err == nil {
		t.Fatal("CLI silently discarded an unknown request field")
	}
}
