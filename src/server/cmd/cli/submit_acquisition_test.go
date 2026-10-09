// Tests collector-record admission and production CLI import against isolated PostgreSQL.
// An optional downloaded record exercises actual corpus bytes without modifying acquisition.
// Queued PARSE is the expected output; this does not claim parser/model or release acceptance.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/ingestion/sources"
)

func submitAcquisitionFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	data := []byte("%PDF-1.7\nfixture\n%%EOF")
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	record := sources.Record{SchemaVersion: 1, ParserVersion: sources.MetadataParserVersion, Status: "complete",
		SourceURL: "https://peraturan.bpk.go.id/Details/1/example", Portal: "peraturan.bpk.go.id", FetchedAt: time.Now().UTC(),
		Metadata: sources.Page{Title: "Fixture"}, PDFs: []sources.PDFReceipt{{URL: "https://peraturan.bpk.go.id/Download/1/main.pdf",
			Kind: "document", SHA256: hash, Bytes: int64(len(data)), Path: "blobs/" + hash + ".pdf", ContentType: "application/pdf"}}}
	if err := os.Mkdir(filepath.Join(root, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, record.PDFs[0].Path), data, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "record.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestSubmitAcquisitionDecode(t *testing.T) {
	_, path := submitAcquisitionFixture(t)
	raw, _, _ := submitFixture(t)
	request := new(pb.IngestionRequest)
	if err := protojson.Unmarshal(raw, request); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSubmitAcquisition(raw, path); err == nil {
		t.Fatal("overwritten sources accepted")
	}
	request.Sources = nil
	raw, _ = protojson.Marshal(request)
	prepared, err := prepareSubmitAcquisition(raw, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Request().Observations) != 1 {
		t.Fatal("missing observation")
	}
	data, _ := os.ReadFile(path)
	for _, bad := range [][]byte{append(append([]byte{}, data...), []byte(" {}")...), []byte(`{"unknown":true}`)} {
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareSubmitAcquisition(raw, path); err == nil {
			t.Fatal("invalid record accepted")
		}
	}
}

func TestSubmitAcquisitionAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	schema := pgx.Identifier{fmt.Sprintf("import_%d", time.Now().UnixNano())}
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), "DROP SCHEMA "+schema.Sanitize()+" CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema[0])
	u.RawQuery = query.Encode()
	repo, err := postgres.Open(ctx, postgres.Config{DSN: u.String(), MaxConnections: 4, HealthTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.ApplyMigrations(ctx, os.DirFS("../../../../migrations")); err != nil {
		t.Fatal(err)
	}
	root, recordPath := submitAcquisitionFixture(t)
	if actual := os.Getenv("REGULAGRAPH_TEST_ACQUISITION_RECORD"); actual != "" {
		recordPath = actual
		root = os.Getenv("REGULAGRAPH_TEST_ACQUISITION_ROOT")
		if root == "" {
			t.Fatal("real record requires acquisition root")
		}
	}
	raw, ontology, policies := submitFixture(t)
	request := new(pb.IngestionRequest)
	if err := protojson.Unmarshal(raw, request); err != nil {
		t.Fatal(err)
	}
	request.Sources = nil
	policy := policies[request.CorpusId]
	policyConfig := map[string]any{"scopes_by_type": policy.ScopesByType, "maximum_mentions": policy.MaximumMentions, "maximum_scopes_per_mention": policy.MaximumScopesPerMention, "maximum_total_scopes": policy.MaximumTotalScopes}
	policyBytes, err := json.Marshal(map[string]any{"schema_version": 1, "corpora": map[string]any{request.CorpusId: policyConfig, "corpus:second-import": policyConfig}})
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	policyPath := filepath.Join(temp, "policy.json")
	requestPath := filepath.Join(temp, "request.json")
	if err := os.WriteFile(policyPath, policyBytes, 0600); err != nil {
		t.Fatal(err)
	}
	policyDigest := sha256.Sum256(policyBytes)
	t.Setenv("REGULAGRAPH_POSTGRES_DSN", u.String())
	t.Setenv("REGULAGRAPH_ARTIFACTS_DIR", filepath.Join(temp, "objects"))
	t.Setenv("REGULAGRAPH_ONTOLOGY_PATH", "../../../../configs/ontology-v1.jsonc")
	t.Setenv("REGULAGRAPH_ONTOLOGY_SHA256", ontology.ContentHash().Sha256)
	t.Setenv("REGULAGRAPH_CANDIDATE_POLICY_PATH", policyPath)
	t.Setenv("REGULAGRAPH_CANDIDATE_POLICY_SHA256", hex.EncodeToString(policyDigest[:]))
	t.Setenv("REGULAGRAPH_RESOLUTION_PRODUCER_PATH", "")
	t.Setenv("REGULAGRAPH_RESOLUTION_PRODUCER_SHA256", "")
	var previous *pb.ArtifactRef
	for index, corpus := range []string{request.CorpusId, "corpus:second-import"} {
		request.CorpusId = corpus
		request.IdempotencyKey = fmt.Sprintf("import:%d", index)
		requestBytes, _ := protojson.Marshal(request)
		if err := os.WriteFile(requestPath, requestBytes, 0600); err != nil {
			t.Fatal(err)
		}
		jobID := fmt.Sprintf("job:import:%d", index)
		for attempt := 0; attempt < 2; attempt++ {
			var out, errOut bytes.Buffer
			code := run(ctx, []string{"submit", "-request", requestPath, "-job-id", jobID, "-acquisition-root", root, "-acquisition-record", recordPath}, &out, &errOut)
			var result struct {
				Reused bool   `json:"reused"`
				Stage  string `json:"stage"`
			}
			if code != 0 || json.Unmarshal(out.Bytes(), &result) != nil || result.Reused != (attempt == 1) || result.Stage != "JOB_STAGE_PARSE" {
				t.Fatalf("code=%d out=%s error=%s", code, &out, &errOut)
			}
		}
		stored, err := repo.LoadIngestionRequest(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.Observations) == 0 {
			t.Fatal("durable provenance missing")
		}
		expectedImport, err := prepareSubmitAcquisition(requestBytes, recordPath)
		if err != nil {
			t.Fatal(err)
		}
		expected := expectedImport.Request()
		if len(stored.Observations) != len(expected.Observations) {
			t.Fatal("observation count changed")
		}
		for i, observation := range expected.Observations {
			if !proto.Equal(stored.Observations[i], observation) {
				t.Fatal("portal metadata changed during submit")
			}
		}
		for _, locator := range stored.Sources {
			ref, err := repo.LoadArtifact(ctx, corpus, locator.GetBlob().ArtifactId)
			if err != nil || !proto.Equal(ref, locator.GetBlob()) {
				t.Fatalf("registered source mismatch: %v", err)
			}
			files, err := storage.NewFileStore(os.Getenv("REGULAGRAPH_ARTIFACTS_DIR"))
			if err != nil {
				t.Fatal(err)
			}
			file, err := files.OpenVerified(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			file.Close()
			files.Close()
			t.Logf("corpus=%s source_sha256=%s bytes=%d observations=%d", corpus, ref.ContentHash.Sha256, ref.ByteSize, len(stored.Observations))
			if previous != nil && previous.StorageKey == ref.StorageKey {
				t.Fatal("storage address crossed corpus")
			}
			previous = ref
		}
	}
	// Use a separate corrupt root, including in real-record mode: acquisition is read-only.
	corruptRoot := t.TempDir()
	recordBytes, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record sources.Record
	if err := json.Unmarshal(recordBytes, &record); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(corruptRoot, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range record.PDFs {
		if err := os.WriteFile(filepath.Join(corruptRoot, receipt.Path), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request.IdempotencyKey = "import:corrupt"
	requestBytes, _ := protojson.Marshal(request)
	if err := os.WriteFile(requestPath, requestBytes, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run(ctx, []string{"submit", "-request", requestPath, "-job-id", "job:import:corrupt", "-acquisition-root", corruptRoot, "-acquisition-record", recordPath}, &out, &errOut); code != 1 || out.Len() != 0 {
		t.Fatalf("corrupt import code=%d out=%s err=%s", code, &out, &errOut)
	}
	if _, err := repo.LoadIngestionRequest(ctx, "job:import:corrupt"); err == nil {
		t.Fatal("corrupt import submitted a job")
	}
}
