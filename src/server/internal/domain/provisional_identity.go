// Derives a provisional identity for one authenticated source occurrence. This is
// not a claim that similarly named entities are equivalent across documents.
// The caller must validate Document/EXTRACT closure and exact source bytes first;
// storage must reject positive candidate lookups and commit identity/profile/alias
// with the operator review atomically. Keys ignore model, chunk and mention IDs.
// Measure false splits/merges and registry latency against benchmark-targets.yaml;
// deterministic fixture checks do not establish legal or model quality.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const ProvisionalSourceIdentityNamespace = "source-occurrence-provisional:v1"

func provisionalSourceIdentity(in SourcedAliasInput, doc *pb.DocumentBatch, mention *pb.Mention) (*DocumentRegistryIdentity, error) {
	kind := CanonicalEntityTypeCode(mention.CandidateType)
	if kind == 0 || kind <= 3 {
		return nil, errors.New("BIND-owned types require an existing independently verified identity")
	}
	span := mention.TextSpan
	var text *pb.TextArtifact
	for _, t := range doc.TextArtifacts {
		if t.Meta.RecordId == span.TextArtifactId {
			text = t
		}
	}
	if text == nil {
		return nil, errors.New("provisional source text missing")
	}
	// Derive anchors from the complete document, not the model-selected subset of
	// SourceRefs or chunk membership, which may change on a repeated extraction.
	anchors := []string{}
	for _, version := range doc.Versions {
		for _, s := range version.Spans {
			if s.TextArtifactId == span.TextArtifactId && s.StartByte <= span.StartByte && s.EndByte >= span.EndByte {
				anchors = append(anchors, version.Meta.RecordId)
				break
			}
		}
	}
	if len(anchors) == 0 {
		return nil, errors.New("provisional source has no containing provision version")
	}
	sort.Strings(anchors)
	h := sha256.New()
	for _, v := range []string{ProvisionalSourceIdentityNamespace, in.Corpus, mention.CandidateType, in.Scope,
		text.SourceBlobId, text.NormalizedTextRef.ContentHash.Sha256, strconv.FormatUint(span.StartByte, 10), strconv.FormatUint(span.EndByte, 10)} {
		aliasHashPart(h, []byte(v))
	}
	for _, producer := range []*pb.ProducerManifest{text.ParserManifest, text.NormalizerManifest} {
		raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(producer)
		if err != nil {
			return nil, err
		}
		aliasHashPart(h, raw)
	}
	for _, id := range anchors {
		aliasHashPart(h, []byte(id))
	}
	key := hex.EncodeToString(h.Sum(nil))
	return &DocumentRegistryIdentity{CanonicalID: "canonical:provisional:" + key, EntityType: kind,
		IdentityScope: ProvisionalSourceIdentityNamespace, IdentityKey: key}, nil
}
