// Menggabungkan bukti terpilih dengan konteks induk, kondisi, pengecualian, dan jalur graph.
//
// Peran dalam komponen:
// Menyiapkan konteks sumber yang dapat dipakai generator dan citation mapper.
//
// Kontrak integrasi dan perhatian implementasi:
// Pemulihan parent harus memakai versi yang sama; de-duplikasi dan pemangkasan mengikuti budget
// token dengan pelaporan bukti yang terpotong. Implementasi saat ini menerima EvidenceBundle
// terpin dan penghitung tokenizer generator yang eksak, mempertahankan urutan, serta menandai
// parent/path/dependency yang belum terhidrasi sebagai konteks parsial. Teks sumber di-escape.
//
// Benchmark dan gate penerimaan:
// [CONTEXT] Ukur cakupan gold evidence, kelengkapan jalur graph, duplikasi, token count, dan
// waktu membangun konteks. Gate: tiap item konteks dapat dipetakan ke sumber dan versi;
// pemotongan/ketidakcukupan bukti dilaporkan. Context budget tidak boleh diam-diam menghapus
// syarat penting.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: packing bukti langsung berbatas aktif; hydrasi parent, pengecualian, dan set jalur
// yang lengkap belum aktif sehingga referensi tersebut ditandai sebagai terlewat.
// Rekomendasi implementasi berikutnya:
// Hydrasi parent/kondisi/pengecualian pada snapshot serta versi yang sama, lalu gunakan
// tokenizer generator aktual sebelum mengizinkan completeness penuh.
// Bukti verifikasi: uji klausul terlalu panjang, parent berulang, budget token kurang, dan
// pencatatan bukti wajib yang terpotong; ukur token nyata dan p95/p99.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
package answering

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// TokenCounter must count the full rendered prompt fragment with the generator's pinned
// tokenizer. It must not estimate tokens from bytes or words.
type TokenCounter func(context.Context, string) (uint64, error)

// BuildContext packs verified evidence in rank order. It never fabricates parent text: a
// parent_ref still missing from the bundle appears in omitted_required_refs and makes the
// context partial. The caller must not report a complete answer from a partial context.
func BuildContext(ctx context.Context, bundle *pb.EvidenceBundle, recordID string,
	tokenizerHash *pb.ContentHash, maximumTokens uint64, maximumEvidence int,
	count TokenCounter) (*pb.ContextBundle, error) {
	if bundle == nil || bundle.Meta == nil || bundle.Snapshot == nil ||
		bundle.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		maximumTokens == 0 || maximumEvidence <= 0 || count == nil || tokenizerHash == nil ||
		recordID == "" || len(bundle.Items) > maximumEvidence {
		return nil, errors.New("complete bounded context inputs are required")
	}
	if err := domain.ValidateWire(bundle, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid evidence bundle: %w", err)
	}
	result := &pb.ContextBundle{
		Meta: &pb.RecordMeta{SchemaVersion: bundle.Meta.SchemaVersion,
			CorpusId: bundle.Meta.CorpusId, RecordId: recordID},
		TokenizerHash: proto.Clone(tokenizerHash).(*pb.ContentHash),
		Snapshot:      proto.Clone(bundle.Snapshot).(*pb.SnapshotRef),
	}
	seenEvidence := map[string]bool{}
	omitted := map[string]bool{}
	addOmission := func(id string) {
		if id != "" && !omitted[id] {
			omitted[id] = true
			result.OmittedRequiredRefs = append(result.OmittedRequiredRefs, id)
		}
	}
	rendered := ""
	for _, item := range bundle.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item == nil || item.Meta == nil || item.Meta.RecordId == "" ||
			item.Meta.CorpusId != bundle.Meta.CorpusId ||
			!proto.Equal(item.SnapshotRef, bundle.Snapshot) || seenEvidence[item.Meta.RecordId] {
			return nil, errors.New("evidence identity, corpus, or snapshot is inconsistent")
		}
		seenEvidence[item.Meta.RecordId] = true
		block := renderEvidence(item)
		trial := block
		if rendered != "" {
			trial = rendered + "\n" + block
		}
		tokens, err := count(ctx, trial)
		if err != nil {
			return nil, fmt.Errorf("count exact context tokens: %w", err)
		}
		if tokens > maximumTokens {
			addOmission(item.Meta.RecordId)
			continue
		}
		rendered = trial
		result.TokenCount = tokens
		result.OrderedEvidenceIds = append(result.OrderedEvidenceIds, item.Meta.RecordId)
		result.RenderedBlocks = append(result.RenderedBlocks, &pb.ContextBlock{
			EvidenceId: item.Meta.RecordId, RenderedText: block})
		for _, parent := range item.ParentRefs {
			addOmission(parent)
		}
	}
	for _, required := range bundle.RequiredPathSets {
		if required == nil {
			return nil, errors.New("nil required path set")
		}
		for _, pathID := range required.PathIds {
			// A GraphPath ID alone does not render its ordered nodes, edges, or supports.
			addOmission(pathID)
		}
	}
	for _, dependency := range bundle.MissingDependencies {
		addOmission(dependency)
	}
	switch {
	case len(result.OrderedEvidenceIds) == 0 && len(result.OmittedRequiredRefs) == 0 &&
		bundle.Completeness == pb.Completeness_COMPLETENESS_NONE:
		result.Completeness = pb.Completeness_COMPLETENESS_NONE
	case len(result.OmittedRequiredRefs) > 0 || bundle.Completeness != pb.Completeness_COMPLETENESS_COMPLETE:
		result.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	case len(result.OrderedEvidenceIds) == 0:
		result.Completeness = pb.Completeness_COMPLETENESS_NONE
	default:
		result.Completeness = pb.Completeness_COMPLETENESS_COMPLETE
	}
	if err := domain.ValidateWire(result, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid rendered context: %w", err)
	}
	return result, nil
}

func renderEvidence(item *pb.Evidence) string {
	versions := make([]string, 0, len(item.SourceRefs))
	for _, source := range item.SourceRefs {
		if source != nil {
			versions = append(versions, source.ProvisionVersionId)
		}
	}
	return "Evidence " + item.Meta.RecordId + " versions [" + strings.Join(versions, ",") +
		"] text " + strconv.Quote(item.Text)
}
