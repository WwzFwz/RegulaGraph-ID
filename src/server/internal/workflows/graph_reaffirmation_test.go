// Verifies the revision-envelope transform preserves original model decisions
// and evidence while binding only the derived assembly view to a later registry.
// Pure tests do not authorize reuse; native storage tests verify the receipt gates.
package workflows

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestGraphReaffirmationPreservesHistoricalDecisions(t *testing.T) {
	items := graphEnvelopeFixture(t, true)
	input := domain.GraphSourceBindingInputs{OriginalDocument: items[0].Bytes, SnapshotDocument: items[1].Bytes, Extraction: items[2].Bytes, Resolution: items[3].Bytes}
	binding := domain.GraphSourceBinding{Policy: domain.GraphSourceReaffirmationPolicy, RegistryRevision: 8,
		Source: domain.IndexSourceBinding{SourceJobID: "job:source", Original: items[0].Reference, Bound: items[1].Reference}, OriginalExtraction: items[2].Reference, OriginalResolution: items[3].Reference}
	oldRaw := append([]byte(nil), input.Resolution...)
	old := new(pb.ResolutionBatch)
	if err := proto.Unmarshal(oldRaw, old); err != nil {
		t.Fatal(err)
	}
	e, r, err := domain.BindGraphSourceReceiptEnvelopes(binding, input, 4096)
	if err != nil {
		t.Fatal(err)
	}
	current := new(pb.ResolutionBatch)
	if err = domain.DecodeWire(r.Bytes, current, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	if current.RegistryRevision != 8 || old.RegistryRevision != 7 || !proto.Equal(current.Decisions[0], old.Decisions[0]) || !proto.Equal(current.Proposals[0], old.Proposals[0]) || !proto.Equal(current.ModelManifest, old.ModelManifest) || !proto.Equal(current.Dependencies.ProducerManifest, old.Dependencies.ProducerManifest) || !bytes.Equal(input.Resolution, oldRaw) {
		t.Fatal("historic decision/model bytes rewritten")
	}
	if !proto.Equal(current.SourceExtractionBatch, e.Reference) {
		t.Fatal("reaffirmed source chain lost")
	}
	_, again, err := domain.BindGraphSourceReceiptEnvelopes(binding, input, 4096)
	if err != nil || !bytes.Equal(r.Bytes, again.Bytes) {
		t.Fatal("reaffirmation replay drift", err)
	}
	binding.RegistryRevision = 9
	_, later, err := domain.BindGraphSourceReceiptEnvelopes(binding, input, 4096)
	if err != nil || proto.Equal(r.Reference, later.Reference) {
		t.Fatal("different target view reused envelope identity", err)
	}
	for _, revision := range []uint64{0, 6, 7, 1 << 63} {
		binding.RegistryRevision = revision
		if _, _, err = domain.BindGraphSourceReceiptEnvelopes(binding, input, 4096); err == nil {
			t.Fatal("invalid reaffirmation revision", revision)
		}
	}
	binding.Policy = domain.GraphSourceEnvelopePolicy
	legacyE, legacyR, err := domain.BindGraphSourceReceiptEnvelopes(binding, input, 4096)
	if err != nil {
		t.Fatal(err)
	}
	wantE, wantR, err := domain.BindGraphSourceEnvelopes("job:source", items[0], items[1], items[2], items[3], 4096)
	if err != nil || !bytes.Equal(legacyE.Bytes, wantE.Bytes) || !bytes.Equal(legacyR.Bytes, wantR.Bytes) {
		t.Fatal("legacy envelope compatibility changed", err)
	}
}
