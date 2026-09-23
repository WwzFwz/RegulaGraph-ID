// Memeriksa bentuk jawaban, keabsahan referensi, konflik bukti, dan kecukupan dukungan.
//
// Peran dalam komponen:
// Menjadi pemeriksaan setelah generation sebelum hasil diterbitkan.
//
// Kontrak integrasi dan perhatian implementasi:
// Pisahkan validasi referensi dari penilaian semantik; jika memakai judge model, ukur tambahan latency/biaya dan kalibrasinya.
//
// Benchmark dan gate penerimaan:
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: gate struktur jawaban terhadap konteks terpilih dan URL sumber tepercaya aktif;
// pemeriksaan dukungan semantik dan kalibrasi model masih belum aktif.
// Rekomendasi implementasi berikutnya:
// Hubungkan generation/streaming dan uji dukungan semantik pada gold; jangan menafsirkan gate
// struktur sebagai bukti faithfulness isi klaim.
// Bukti verifikasi: Test unsupported factual clauses, konteks parsial, URL/versi palsu,
// dan bukti yang tidak masuk context; kalibrasi semantic judge terhadap label manusia.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package answering

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// ValidateGroundedAnswer enforces structural grounding at the retrieval-to-answer boundary.
// It cannot judge whether a cited clause semantically entails the claim; that needs gold review.
func ValidateGroundedAnswer(answer *pb.Answer, context *pb.ContextBundle,
	evidence *pb.EvidenceBundle, trustedURLs domain.SourceURLLookup) error {
	if answer == nil || context == nil || evidence == nil {
		return errors.New("answer, rendered context, and evidence are required")
	}
	if err := domain.ValidateWire(context, domain.DefaultWireLimits); err != nil {
		return fmt.Errorf("invalid rendered context: %w", err)
	}
	if err := domain.VerifyCitationEvidence(answer, evidence, trustedURLs); err != nil {
		return fmt.Errorf("untrusted answer citation: %w", err)
	}
	if !proto.Equal(answer.Snapshot, context.Snapshot) ||
		!proto.Equal(context.Snapshot, evidence.Snapshot) ||
		answer.Meta.CorpusId != context.Meta.CorpusId ||
		answer.Meta.CorpusId != evidence.Meta.CorpusId ||
		answer.Meta.SchemaVersion != context.Meta.SchemaVersion ||
		answer.Meta.SchemaVersion != evidence.Meta.SchemaVersion {
		return errors.New("answer, context, and evidence use different corpus, schema, or snapshot")
	}
	selected := make(map[string]bool, len(context.OrderedEvidenceIds))
	byID := make(map[string]*pb.Evidence, len(evidence.Items))
	for _, item := range evidence.Items {
		if item == nil || item.Meta == nil || byID[item.Meta.RecordId] != nil ||
			item.Meta.SchemaVersion != evidence.Meta.SchemaVersion ||
			item.Meta.CorpusId != evidence.Meta.CorpusId ||
			!proto.Equal(item.SnapshotRef, evidence.Snapshot) {
			return errors.New("evidence bundle contains missing or duplicate identity")
		}
		byID[item.Meta.RecordId] = item
	}
	for index, id := range context.OrderedEvidenceIds {
		if selected[id] {
			return errors.New("rendered context contains duplicate evidence")
		}
		if byID[id] == nil || context.RenderedBlocks[index].RenderedText != renderEvidence(byID[id]) {
			return errors.New("rendered context text differs from trusted evidence")
		}
		selected[id] = true
	}
	// Reconstruct the builder's omissions from the authoritative bundle. Neither a
	// caller-supplied COMPLETE flag nor a caller-supplied empty omission list is proof.
	expectedOmissions := map[string]bool{}
	for _, item := range evidence.Items {
		if !selected[item.Meta.RecordId] {
			expectedOmissions[item.Meta.RecordId] = true
			continue
		}
		for _, parent := range item.ParentRefs {
			expectedOmissions[parent] = true
		}
	}
	for _, required := range evidence.RequiredPathSets {
		if required == nil {
			return errors.New("required path set is missing")
		}
		for _, id := range required.PathIds {
			expectedOmissions[id] = true
		}
	}
	for _, id := range evidence.MissingDependencies {
		expectedOmissions[id] = true
	}
	observedOmissions := map[string]bool{}
	for _, id := range context.OmittedRequiredRefs {
		if !expectedOmissions[id] || observedOmissions[id] {
			return errors.New("context contains unexpected or duplicate omission")
		}
		observedOmissions[id] = true
	}
	if len(observedOmissions) != len(expectedOmissions) {
		return errors.New("context hides required omitted evidence")
	}
	expectedCompleteness := pb.Completeness_COMPLETENESS_COMPLETE
	switch {
	case len(selected) == 0 && len(expectedOmissions) == 0 &&
		evidence.Completeness == pb.Completeness_COMPLETENESS_NONE:
		expectedCompleteness = pb.Completeness_COMPLETENESS_NONE
	case len(expectedOmissions) > 0 || evidence.Completeness != pb.Completeness_COMPLETENESS_COMPLETE:
		expectedCompleteness = pb.Completeness_COMPLETENESS_PARTIAL
	case len(selected) == 0:
		expectedCompleteness = pb.Completeness_COMPLETENESS_NONE
	}
	if context.Completeness != expectedCompleteness {
		return errors.New("context completeness contradicts selected evidence")
	}
	// Graph paths are not yet rendered by BuildContext; returning them as answer
	// proof would let a well-shaped but invented path pass structural validation.
	if len(answer.Paths) > 0 {
		return errors.New("answer graph paths require rendered verified path proof")
	}
	missing := make(map[string]bool, len(answer.MissingEvidence))
	for _, id := range answer.MissingEvidence {
		missing[id] = true
	}
	for _, id := range context.OmittedRequiredRefs {
		if !missing[id] {
			return errors.New("answer hides omitted required context")
		}
	}
	claimed := make(map[string]bool, len(answer.Claims))
	for _, claim := range answer.Claims {
		if claim.SupportStatus == pb.SupportStatus_SUPPORT_STATUS_SUPPORTED && len(claim.EvidenceIds) == 0 {
			return errors.New("supported claim has no evidence")
		}
		if claim.SupportStatus == pb.SupportStatus_SUPPORT_STATUS_SUPPORTED &&
			(claim.AnswerTextSpan == nil || claim.AnswerTextSpan.StartByte == claim.AnswerTextSpan.EndByte) {
			return errors.New("supported claim has no answer text span")
		}
		for _, id := range claim.EvidenceIds {
			if !selected[id] {
				return errors.New("claim cites evidence absent from rendered context")
			}
		}
		claimed[claim.ClaimId] = false
	}
	for _, citation := range answer.Citations {
		if !selected[citation.EvidenceId] {
			return errors.New("citation points outside rendered context")
		}
		for _, id := range citation.ClaimIds {
			claimed[id] = true
		}
	}
	for _, conflict := range answer.Conflicts {
		seenConflictEvidence := map[string]bool{}
		for _, id := range conflict.EvidenceIds {
			if !selected[id] || seenConflictEvidence[id] {
				return errors.New("conflict refers to absent or duplicate evidence")
			}
			seenConflictEvidence[id] = true
		}
	}
	for _, claim := range answer.Claims {
		if claim.SupportStatus == pb.SupportStatus_SUPPORT_STATUS_SUPPORTED && !claimed[claim.ClaimId] {
			return errors.New("supported claim has no verified citation")
		}
	}
	if answer.SemanticStatus == pb.SemanticStatus_SEMANTIC_STATUS_COMPLETE {
		if context.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
			evidence.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
			len(context.OmittedRequiredRefs) > 0 || len(answer.MissingEvidence) > 0 ||
			len(answer.Conflicts) > 0 || len(answer.Claims) == 0 {
			return errors.New("complete answer lacks complete evidence and claims")
		}
		for _, claim := range answer.Claims {
			if claim.SupportStatus != pb.SupportStatus_SUPPORT_STATUS_SUPPORTED {
				return errors.New("complete answer contains an unverified claim")
			}
		}
	}
	if answer.SemanticStatus == pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN &&
		(len(answer.Claims) > 0 || len(answer.Citations) > 0 || len(answer.Conflicts) > 0) {
		return errors.New("abstention cannot carry factual claims, citations, or conflicts")
	}
	if answer.SemanticStatus == pb.SemanticStatus_SEMANTIC_STATUS_CONFLICT &&
		len(answer.Conflicts) == 0 {
		return errors.New("conflict status requires explicit conflict evidence")
	}
	return nil
}
