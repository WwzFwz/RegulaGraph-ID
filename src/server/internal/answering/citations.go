// Memetakan klaim dan citation pada identitas sumber, pasal, ayat, versi, serta rentang teks.
//
// Peran dalam komponen:
// Membuat jawaban dapat ditelusuri oleh API, pengguna, dan evaluator.
//
// Kontrak integrasi dan perhatian implementasi:
// Referensi ada belum tentu mendukung klaim; simpan mapping deterministik dan hindari citation bebas yang tidak tercantum dalam evidence.
//
// Benchmark dan gate penerimaan:
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: pemetaan sitasi deterministik dari evidence/context dan URL tepercaya aktif sebagai helper;
// dukungan semantik klaim, hidrasi sumber produksi, dan generation masih belum aktif.
// Bukti verifikasi: panggil VerifyCitationEvidence, uji sumber ganda, locator hilang,
// URL tidak tepercaya, serta klaim di luar konteks; ukur jumlah lookup dan p95/p99.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package answering

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type citationSourceKey struct {
	blobID, versionID string
}

// This input ceiling protects URL mapping from a flood of unsupported claims that produce
// no citations; maximumCitations separately limits emitted citation records.
const maximumCitationClaims = 256

// BuildCitations maps supported claim/evidence pairs to source versions and observed URLs.
// It only cites evidence rendered in context. Flat source spans can be attributed safely only
// for single-source evidence; multi-source evidence needs a blob-bound page locator. The URL
// lookup is snapshot-pinned by the caller and cached per blob/version within this batch.
// This function establishes structural provenance, never semantic entailment of a claim.
func BuildCitations(claims []*pb.Claim, context *pb.ContextBundle, bundle *pb.EvidenceBundle,
	lookup domain.SourceURLLookup, maximumCitations int) ([]*pb.Citation, error) {
	if context == nil || bundle == nil || lookup == nil || maximumCitations <= 0 {
		return nil, errors.New("context, evidence, trusted URL lookup, and citation limit are required")
	}
	if len(claims) > maximumCitationClaims {
		return nil, errors.New("claim count exceeds citation input limit")
	}
	if err := domain.ValidateWire(context, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid citation context: %w", err)
	}
	if err := domain.ValidateWire(bundle, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid citation evidence: %w", err)
	}
	if !proto.Equal(context.Snapshot, bundle.Snapshot) || context.Meta.CorpusId != bundle.Meta.CorpusId ||
		context.Meta.SchemaVersion != bundle.Meta.SchemaVersion ||
		bundle.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		return nil, errors.New("citation context and evidence have different snapshot or incomplete transport")
	}
	selected := make(map[string]bool, len(context.OrderedEvidenceIds))
	for _, id := range context.OrderedEvidenceIds {
		if selected[id] {
			return nil, errors.New("citation context repeats evidence")
		}
		selected[id] = true
	}
	items := make(map[string]*pb.Evidence, len(bundle.Items))
	for _, item := range bundle.Items {
		if item == nil || item.Meta == nil || items[item.Meta.RecordId] != nil ||
			item.Meta.CorpusId != bundle.Meta.CorpusId || !proto.Equal(item.SnapshotRef, bundle.Snapshot) {
			return nil, errors.New("citation evidence identity or snapshot is invalid")
		}
		items[item.Meta.RecordId] = item
	}
	if len(context.RenderedBlocks) != len(context.OrderedEvidenceIds) {
		return nil, errors.New("citation context block count differs from selected evidence")
	}
	for index, id := range context.OrderedEvidenceIds {
		block := context.RenderedBlocks[index]
		if items[id] == nil || block == nil || block.EvidenceId != id ||
			block.RenderedText != renderEvidence(items[id]) {
			return nil, errors.New("citation context was not rendered from selected evidence")
		}
	}
	urls := make(map[citationSourceKey]string)
	result := make([]*pb.Citation, 0, min(len(claims), maximumCitations))
	seenClaims := make(map[string]bool, len(claims))
	for _, claim := range claims {
		if err := domain.ValidateWire(claim, domain.DefaultWireLimits); err != nil {
			return nil, fmt.Errorf("invalid citation claim: %w", err)
		}
		if seenClaims[claim.ClaimId] {
			return nil, errors.New("duplicate claim ID in citation mapping")
		}
		seenClaims[claim.ClaimId] = true
		if claim.SupportStatus != pb.SupportStatus_SUPPORT_STATUS_SUPPORTED {
			continue
		}
		if len(claim.EvidenceIds) == 0 || len(claim.EvidenceIds) > maximumCitations-len(result) {
			return nil, errors.New("supported claim has no evidence or exceeds citation limit")
		}
		for _, id := range claim.EvidenceIds {
			item := items[id]
			if !selected[id] || item == nil {
				return nil, fmt.Errorf("claim %q cites evidence outside rendered context", claim.ClaimId)
			}
			citations, err := citationsFromEvidence(claim.ClaimId, item, lookup, urls)
			if err != nil {
				return nil, fmt.Errorf("cite evidence %q: %w", id, err)
			}
			if len(citations) > maximumCitations-len(result) {
				return nil, errors.New("source citations exceed citation limit")
			}
			result = append(result, citations...)
		}
	}
	return result, nil
}

func citationsFromEvidence(claimID string, item *pb.Evidence, lookup domain.SourceURLLookup,
	urls map[citationSourceKey]string) ([]*pb.Citation, error) {
	if len(item.SourceRefs) == 0 {
		return nil, errors.New("evidence has no source version")
	}
	result := make([]*pb.Citation, 0, len(item.SourceRefs))
	seenSources := make(map[citationSourceKey]bool, len(item.SourceRefs))
	for _, source := range item.SourceRefs {
		if source == nil || source.SourceBlobId == "" || source.ProvisionVersionId == "" {
			return nil, errors.New("evidence has an invalid source version")
		}
		key := citationSourceKey{source.SourceBlobId, source.ProvisionVersionId}
		if seenSources[key] {
			return nil, errors.New("evidence repeats a source version")
		}
		seenSources[key] = true
		var span *pb.TextSpan
		if len(item.SourceRefs) == 1 {
			for _, candidate := range item.SourceSpans {
				if candidate != nil && candidate.StartByte < candidate.EndByte {
					span = proto.Clone(candidate).(*pb.TextSpan)
					break
				}
			}
		}
		var locator *pb.PageLocator
		for _, candidate := range item.Locators {
			if candidate != nil && candidate.SourceBlobId == source.SourceBlobId {
				locator = proto.Clone(candidate).(*pb.PageLocator)
				break
			}
		}
		if span == nil && locator == nil {
			return nil, errors.New("source version lacks an unambiguous locator")
		}
		trusted, found := urls[key]
		if !found {
			observed, err := lookup(key.blobID, key.versionID)
			if err != nil {
				return nil, fmt.Errorf("trusted source lookup: %w", err)
			}
			if len(observed) > 32 {
				return nil, errors.New("trusted source metadata contains too many URLs")
			}
			for _, candidate := range observed {
				parsed, parseErr := url.Parse(candidate)
				if parseErr == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") &&
					parsed.Host != "" && parsed.User == nil {
					trusted = candidate
					break
				}
			}
			urls[key] = trusted
		}
		if trusted == "" {
			return nil, errors.New("source version lacks a trusted URL")
		}
		citation := &pb.Citation{ClaimIds: []string{claimID}, EvidenceId: item.Meta.RecordId,
			ProvisionVersionId: source.ProvisionVersionId, SourceUrl: trusted,
			SourceSpan: span, PageLocator: locator}
		encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(citation)
		if err != nil {
			return nil, fmt.Errorf("hash citation: %w", err)
		}
		digest := sha256.Sum256(encoded)
		citation.CitationId = "citation:" + hex.EncodeToString(digest[:])
		if err := domain.ValidateWire(citation, domain.DefaultWireLimits); err != nil {
			return nil, fmt.Errorf("invalid built citation: %w", err)
		}
		result = append(result, citation)
	}
	return result, nil
}
