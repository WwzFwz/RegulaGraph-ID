// Environment-gated Go-to-Rust HTTP/2 interoperability test for the real Worker service.
//
// The test writes one deterministic PDF under the shared artifact root, calls ProcessBatch/GetStatus,
// and verifies the returned C01 reference. It is a correctness probe, not a latency benchmark.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestRustWorkerInteroperability(t *testing.T) {
	target := os.Getenv("REGULAGRAPH_WORKER_INTEGRATION_ADDR")
	root := os.Getenv("REGULAGRAPH_WORKER_INTEGRATION_ARTIFACT_ROOT")
	if target == "" || root == "" {
		t.Skip("set REGULAGRAPH_WORKER_INTEGRATION_ADDR and REGULAGRAPH_WORKER_INTEGRATION_ARTIFACT_ROOT")
	}
	pdf := minimalPDF(strings.Repeat("Pasal satu berlaku untuk seluruh pihak dan mempertahankan bukti sumber. ", 3))
	digest := sha256.Sum256(pdf)
	key := filepath.ToSlash(filepath.Join("integration", fmt.Sprintf("go-rust-%d.pdf", os.Getpid())))
	path := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pdf, 0o640); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	client, err := New(target, insecure.NewCredentials(), DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(30 * time.Second)
	request := &pb.ProcessBatchRequest{
		Context: integrationContext("interop-process", deadline),
		JobId:   fmt.Sprintf("interop-job-%d", os.Getpid()),
		Attempt: 1,
		Lease: &pb.Lease{
			OwnerId:   "interop-go",
			Fence:     1,
			ExpiresAt: timestamppb.New(deadline.Add(time.Minute)),
		},
		Sources: []*pb.ArtifactRef{{
			ArtifactId:    "interop-source-1",
			ContentHash:   &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])},
			StorageKey:    key,
			MediaType:     "application/pdf",
			ByteSize:      uint64(len(pdf)),
			SchemaVersion: 1,
		}},
		Manifest: &pb.ProducerManifest{
			Software: "go-rust-interop", Build: "test", SchemaVersion: 1,
			ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)},
		},
		Stages: []pb.JobStage{pb.JobStage_JOB_STAGE_PARSE},
	}
	response, err := client.ProcessBatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetDocumentBatch() == nil || response.GetDocumentBatch().GetByteSize() == 0 {
		t.Fatal("Rust worker did not return a persisted document batch")
	}
	if response.GetCheckpoint() == nil || len(response.GetCheckpoint().GetCompletedBatchKeys()) != 1 ||
		response.GetCheckpoint().GetCompletedBatchKeys()[0] != response.GetDocumentBatch().GetArtifactId() {
		t.Fatal("Rust worker did not bind its document batch to a checkpoint")
	}
	statusResponse, err := client.GetStatus(context.Background(), &pb.WorkerStatusRequest{
		Context: integrationContext("interop-status", time.Now().Add(10*time.Second)),
		JobId:   request.JobId, Attempt: request.Attempt, Fence: request.Lease.Fence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if statusResponse.GetCompletedItems() != 1 || statusResponse.GetTotalItems() != 1 {
		t.Fatalf("unexpected worker progress: %d/%d", statusResponse.GetCompletedItems(), statusResponse.GetTotalItems())
	}

	structureDeadline := time.Now().Add(30 * time.Second)
	structureRequest := &pb.ProcessBatchRequest{
		Context: integrationContext("interop-structure", structureDeadline),
		JobId:   request.JobId, Attempt: 2,
		Lease:    &pb.Lease{OwnerId: "interop-go", Fence: 2, ExpiresAt: timestamppb.New(structureDeadline.Add(time.Minute))},
		Sources:  []*pb.ArtifactRef{response.DocumentBatch},
		Manifest: request.Manifest,
		Stages:   []pb.JobStage{pb.JobStage_JOB_STAGE_STRUCTURE},
	}
	structured, err := client.ProcessBatch(context.Background(), structureRequest)
	if err != nil {
		t.Fatal(err)
	}
	if structured.GetDocumentBatch() == nil || structured.GetCheckpoint().GetStage() != pb.JobStage_JOB_STAGE_STRUCTURE ||
		structured.GetDocumentBatch().GetArtifactId() == response.GetDocumentBatch().GetArtifactId() {
		t.Fatal("Rust worker did not produce a distinct checkpoint-bound STRUCTURE batch")
	}
}

func integrationContext(requestID string, deadline time.Time) *pb.RequestContext {
	return &pb.RequestContext{
		SchemaVersion: 1, RequestId: requestID, TraceId: "interop-trace", CorpusId: "regulagraph-id",
		Deadline: timestamppb.New(deadline), ConfigFingerprint: &pb.ContentHash{Sha256: strings.Repeat("b", 64)},
		AuthScopeRef: "interop-scope",
	}
}

func minimalPDF(text string) []byte {
	content := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	result := []byte("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for index, object := range objects {
		offsets = append(offsets, len(result))
		result = fmt.Appendf(result, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := len(result)
	result = fmt.Appendf(result, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		result = fmt.Appendf(result, "%010d 00000 n \n", offset)
	}
	return fmt.Appendf(result, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
}
