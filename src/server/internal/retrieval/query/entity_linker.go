// Mengaitkan penyebutan dalam pertanyaan ke canonical entity yang sudah tersedia.
//
// Peran dalam komponen:
// Menyediakan seed langsung bagi retrieval graph.
//
// Kontrak integrasi dan perhatian implementasi:
// Linking bukan ingestion resolution: tidak melakukan merge atau menulis canonical baru; kandidat ambigu dan tidak ditemukan diteruskan secara eksplisit.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
//
// [RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: bounded exact-alias phrase discovery and snapshot-pinned registry lookup
// aktif. Every alias alternative remains a candidate, never a probability or LINK
// decision. Scope policy and phrase window must be pinned by serving configuration.
// Fuzzy/model disambiguation and measured candidate recall remain future work.
// Bukti verifikasi: Test homonyms, same article number across laws and unresolved mentions; measure candidate recall and false merges.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"regulagraph.local/server/internal/domain"
)

const AliasLinkingMethod = "exact-alias-phrases-v1"

// EntityNamespace is a configured legal namespace, not an inferred issuer.
type EntityNamespace struct{ EntityType, Scope string }
type EntityLinkingPolicy struct {
	Namespaces                                                             []EntityNamespace
	MaximumQueryBytes, MaximumPhraseTokens, MaximumPhrases, MaximumLookups int
	MaximumAliasesPerLookup, MaximumSeeds                                  int
}

func (p EntityLinkingPolicy) Fingerprint() (string, error) {
	if len(p.Namespaces) == 0 || len(p.Namespaces) > 64 || p.MaximumQueryBytes < 1 || p.MaximumQueryBytes > 64<<10 || p.MaximumPhraseTokens < 1 || p.MaximumPhraseTokens > 16 || p.MaximumPhrases < 1 || p.MaximumPhrases > 2048 || p.MaximumLookups < 1 || p.MaximumLookups > 4096 || p.MaximumAliasesPerLookup < 1 || p.MaximumAliasesPerLookup > 128 || p.MaximumSeeds < 1 || p.MaximumSeeds > 64 || p.MaximumLookups > domain.MaximumRegistryLookupAliases/p.MaximumAliasesPerLookup {
		return "", errors.New("bounded alias linking policy required")
	}
	seen := map[EntityNamespace]bool{}
	for _, n := range p.Namespaces {
		if domain.CanonicalEntityTypeCode(n.EntityType) == 0 || n.Scope == "" || strings.TrimSpace(n.Scope) != n.Scope || len(n.Scope) > 512 || !utf8.ValidString(n.Scope) || strings.ContainsRune(n.Scope, 0) || seen[n] {
			return "", errors.New("invalid or duplicate entity namespace")
		}
		seen[n] = true
	}
	raw, _ := json.Marshal(p)
	h := sha256.Sum256(append([]byte(AliasLinkingMethod+"\x00"), raw...))
	return hex.EncodeToString(h[:]), nil
}

type PinnedAliasReader interface {
	LookupPinnedCanonicalAliases(context.Context, domain.SnapshotPin, []domain.RegistryLookupScope, int, int) ([]domain.RegistryLookupResult, uint64, error)
}

// QueryAliasMatch offsets refer to original UTF-8 question bytes [start,end).
// Candidates across types/scopes are deliberately kept together for ambiguity.
type QueryAliasMatch struct {
	StartByte, EndByte     int
	Surface, Normalized    string
	CanonicalIDs, AliasIDs []string
}
type AliasObservation struct {
	Scope    domain.RegistryLookupScope
	Revision uint64
	Empty    bool
}
type EntityLinks struct {
	Method, PolicyHash, CorpusID, SnapshotID string
	RegistryRevision                         uint64
	Matches                                  []QueryAliasMatch
	Observations                             []AliasObservation
	CanonicalIDs, StopReasons                []string
}

// CheckBudget runs before reports cross callback/clone boundaries. Limits include
// repeated phrase matches, not only deduplicated registry rows or canonical IDs.
func (r *EntityLinks) CheckBudget() error {
	if r == nil || len(r.Matches) > 2048 || len(r.Observations) > 4096 || len(r.CanonicalIDs) > 64 || len(r.StopReasons) > 16 {
		return errors.New("query linker report exceeds item budget")
	}
	remaining := 4 << 20
	charge := func(s string) bool {
		if len(s)+16 > remaining {
			return false
		}
		remaining -= len(s) + 16
		return true
	}
	for _, s := range []string{r.Method, r.PolicyHash, r.CorpusID, r.SnapshotID} {
		if !charge(s) {
			return errors.New("query linker report exceeds bytes")
		}
	}
	for _, m := range r.Matches {
		if len(m.CanonicalIDs) > 64 || len(m.AliasIDs) > domain.MaximumRegistryLookupAliases || !charge(m.Surface) || !charge(m.Normalized) {
			return errors.New("query linker match exceeds budget")
		}
		for _, ids := range [][]string{m.CanonicalIDs, m.AliasIDs} {
			for _, id := range ids {
				if !charge(id) {
					return errors.New("query linker repeated identities exceed bytes")
				}
			}
		}
	}
	for _, o := range r.Observations {
		for _, s := range []string{o.Scope.EntityType, o.Scope.CanonicalScope, o.Scope.NormalizedLookup} {
			if !charge(s) {
				return errors.New("query linker observations exceed bytes")
			}
		}
	}
	for _, ids := range [][]string{r.CanonicalIDs, r.StopReasons} {
		for _, id := range ids {
			if !charge(id) {
				return errors.New("query linker report exceeds bytes")
			}
		}
	}
	return nil
}

// Clone owns all slices; query hydration callbacks cannot rewrite linker audit.
func (r *EntityLinks) Clone() *EntityLinks {
	if r == nil {
		return nil
	}
	c := *r
	c.Matches = append([]QueryAliasMatch(nil), r.Matches...)
	for i := range c.Matches {
		c.Matches[i].CanonicalIDs = append([]string(nil), r.Matches[i].CanonicalIDs...)
		c.Matches[i].AliasIDs = append([]string(nil), r.Matches[i].AliasIDs...)
	}
	c.Observations = append([]AliasObservation(nil), r.Observations...)
	c.CanonicalIDs = append([]string(nil), r.CanonicalIDs...)
	c.StopReasons = append([]string(nil), r.StopReasons...)
	return &c
}

// LinkQueryAliases reads all unique keys once. Empty keys are audit observations;
// ordinary unmatched words are not claimed to be unresolved entity mentions.
// Window/namespace omissions are policy limits, not evidence of exhaustive recall.
func LinkQueryAliases(ctx context.Context, reader PinnedAliasReader, pin domain.SnapshotPin, revision uint64, question string, policy EntityLinkingPolicy) (*EntityLinks, error) {
	hash, err := policy.Fingerprint()
	if err != nil {
		return nil, err
	}
	if ctx == nil || reader == nil || revision == 0 || pin.CorpusID == "" || pin.SnapshotID == "" || !pin.ExpiresAt.After(time.Now()) || len(question) > policy.MaximumQueryBytes || !utf8.ValidString(question) || strings.ContainsRune(question, 0) || strings.TrimSpace(question) == "" {
		return nil, errors.New("live pin and bounded query required")
	}
	call, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	if err = call.Err(); err != nil {
		return nil, err
	}
	phrases, err := queryAliasPhrases(question, policy)
	if err != nil {
		return nil, err
	}
	out := &EntityLinks{Method: AliasLinkingMethod, PolicyHash: hash, CorpusID: pin.CorpusID, SnapshotID: pin.SnapshotID, RegistryRevision: revision}
	keys := []domain.RegistryLookupScope{}
	seen := map[domain.RegistryLookupScope]bool{}
	for _, phrase := range phrases {
		for _, n := range policy.Namespaces {
			key := domain.RegistryLookupScope{EntityType: n.EntityType, CanonicalScope: n.Scope, NormalizedLookup: phrase.Normalized}
			if !seen[key] {
				if len(keys) >= policy.MaximumLookups {
					return nil, errors.New("query alias lookup budget exceeded")
				}
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 {
		out.StopReasons = []string{"query_seed_unresolved"}
		if err = call.Err(); err != nil {
			return nil, err
		}
		return out, nil
	}
	rows, actual, err := reader.LookupPinnedCanonicalAliases(call, pin, keys, policy.MaximumLookups, policy.MaximumAliasesPerLookup)
	if err != nil {
		return nil, err
	}
	if err = call.Err(); err != nil {
		return nil, err
	}
	if actual != revision || len(rows) != len(keys) {
		return nil, errors.New("query registry revision or lookup coverage differs")
	}
	byKey, reasons, err := validateQueryAliasRows(rows, keys, pin.CorpusID, revision, policy.MaximumAliasesPerLookup)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out.Observations = append(out.Observations, AliasObservation{row.Scope, row.Revision.Revision, row.Revision.EmptyResult})
	}
	seeds := map[string]bool{}
	matchBytes := 0
	for _, phrase := range phrases {
		ids, aliases := map[string]bool{}, map[string]bool{}
		for _, n := range policy.Namespaces {
			for _, a := range byKey[domain.RegistryLookupScope{EntityType: n.EntityType, CanonicalScope: n.Scope, NormalizedLookup: phrase.Normalized}] {
				ids[a.CanonicalId] = true
				aliases[a.Meta.RecordId] = true
			}
		}
		if len(ids) == 0 {
			continue
		}
		phrase.CanonicalIDs = sortedLinkKeys(ids)
		phrase.AliasIDs = sortedLinkKeys(aliases)
		matchBytes += len(phrase.Surface) + len(phrase.Normalized)
		for _, ids := range [][]string{phrase.CanonicalIDs, phrase.AliasIDs} {
			for _, id := range ids {
				matchBytes += len(id)
			}
		}
		if matchBytes > 4<<20 {
			return nil, errors.New("query linker match report exceeds bytes")
		}
		out.Matches = append(out.Matches, phrase)
		if len(ids) > 1 {
			reasons["query_alias_ambiguous"] = true
		}
		for id := range ids {
			seeds[id] = true
			if len(seeds) > policy.MaximumSeeds {
				return nil, errors.New("query seed budget exceeded; alternatives not truncated")
			}
		}
	}
	out.CanonicalIDs = sortedLinkKeys(seeds)
	if len(seeds) == 0 {
		reasons["query_seed_unresolved"] = true
	}
	out.StopReasons = sortedLinkKeys(reasons)
	if err := out.CheckBudget(); err != nil {
		return nil, err
	}
	if err = call.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sortedLinkKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Enumerate contiguous whitespace phrases plus outer punctuation variants. Legal
// punctuation inside the phrase is preserved; no typo correction or stopword loss.
func queryAliasPhrases(question string, p EntityLinkingPolicy) ([]QueryAliasMatch, error) {
	type span struct{ start, end int }
	tokens := []span{}
	start := -1
	for i, r := range question {
		if unicode.IsSpace(r) {
			if start >= 0 {
				tokens = append(tokens, span{start, i})
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		tokens = append(tokens, span{start, len(question)})
	}
	out := []QueryAliasMatch{}
	seen := map[span]bool{}
	add := func(s span) error {
		if s.start == s.end || seen[s] {
			return nil
		}
		seen[s] = true
		if len(out) >= p.MaximumPhrases {
			return errors.New("query phrase budget exceeded")
		}
		n, err := domain.NormalizeCandidateSurface(question[s.start:s.end])
		if err != nil {
			return err
		}
		out = append(out, QueryAliasMatch{StartByte: s.start, EndByte: s.end, Surface: question[s.start:s.end], Normalized: n})
		return nil
	}
	for i := range tokens {
		for j := i; j < len(tokens) && j < i+p.MaximumPhraseTokens; j++ {
			raw := span{tokens[i].start, tokens[j].end}
			// Retain intermediate trims: "1(2)?" must query "1(2)", and
			// "PT.?" must query "PT." as well as the punctuation-free variant.
			for left := raw.start; left < raw.end; {
				for right := raw.end; right > left; {
					if err := add(span{left, right}); err != nil {
						return nil, err
					}
					r, n := utf8.DecodeLastRuneInString(question[left:right])
					if !unicode.IsPunct(r) {
						break
					}
					right -= n
				}
				r, n := utf8.DecodeRuneInString(question[left:raw.end])
				if !unicode.IsPunct(r) {
					break
				}
				left += n
			}
		}
	}
	return out, nil
}
