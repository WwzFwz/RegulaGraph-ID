// Tests durable ingestion scheduling decisions before any external worker or database side effect.
// These fixtures protect ACQUIRE/PARSE ownership and do not measure production throughput; latency and
// ingestion gates remain sourced from configs/benchmark-targets.yaml as REQUIRED_UNMEASURED.
package workflows

import (
	"strings"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestInitialIngestionStageRequiresAllSourcesAcquired(t *testing.T) {
	blob := &pb.SourceLocator{PortalId: "bpk", Locator: &pb.SourceLocator_Blob{Blob: &pb.ArtifactRef{
		ArtifactId: "source-1", ContentHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)},
		StorageKey: "inputs/source.pdf", MediaType: "application/pdf", ByteSize: 1, SchemaVersion: 1,
	}}}
	url := &pb.SourceLocator{PortalId: "bpk", Locator: &pb.SourceLocator_Url{Url: "https://example.test/source.pdf"}}
	if got := initialIngestionStage(&pb.IngestionRequest{Sources: []*pb.SourceLocator{blob}}); got != pb.JobStage_JOB_STAGE_PARSE {
		t.Fatalf("acquired sources start at %s", got)
	}
	if got := initialIngestionStage(&pb.IngestionRequest{Sources: []*pb.SourceLocator{blob, url}}); got != pb.JobStage_JOB_STAGE_ACQUIRE {
		t.Fatalf("mixed source request starts at %s", got)
	}
}
