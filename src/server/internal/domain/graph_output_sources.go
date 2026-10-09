// Verifies ASSEMBLE text bytes and the exact inherited dependency union before
// output admission. Missing negative lookups, altered provenance, UTF-8 boundary
// errors and conflicting hashes fail closed. Inputs are wire/budget validated by
// graph_output.go; hashes are computed once per required text, not per mention.
package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func validateGraphOutputTexts(in GraphOutputSources) error {
	refs := map[string]*pb.ArtifactRef{}
	for _, text := range in.Document.TextArtifacts {
		id := text.Meta.RecordId
		if refs[id] != nil {
			return errors.New("duplicate graph text artifact")
		}
		refs[id] = text.NormalizedTextRef
	}
	var spans []*pb.TextSpan
	for _, m := range in.Extraction.Mentions {
		spans = append(spans, m.TextSpan)
	}
	for _, s := range in.Extraction.Supports {
		spans = append(spans, s.EvidenceSpans...)
	}
	checked := map[string]bool{}
	for _, span := range spans {
		id := span.TextArtifactId
		raw, ok := in.NormalizedTexts[id]
		ref := refs[id]
		if !ok || ref == nil {
			return errors.New("missing graph source text")
		}
		if !checked[id] {
			if uint64(len(raw)) != ref.ByteSize || fmt.Sprintf("%x", sha256.Sum256(raw)) != ref.ContentHash.Sha256 || !utf8.Valid(raw) {
				return errors.New("graph source text hash, size or UTF-8 mismatch")
			}
			checked[id] = true
		}
		if span.StartByte >= span.EndByte || span.EndByte > uint64(len(raw)) ||
			span.StartByte < uint64(len(raw)) && !utf8.RuneStart(raw[span.StartByte]) ||
			span.EndByte < uint64(len(raw)) && !utf8.RuneStart(raw[span.EndByte]) {
			return errors.New("invalid graph evidence byte span")
		}
	}
	if len(checked) != len(in.NormalizedTexts) {
		return errors.New("unexpected normalized text in graph input")
	}
	for _, m := range in.Extraction.Mentions {
		s := m.TextSpan
		if string(in.NormalizedTexts[s.TextArtifactId][s.StartByte:s.EndByte]) != m.SurfaceForm {
			return errors.New("graph mention differs from source text")
		}
	}
	return nil
}

func expectedGraphDependencies(p *pb.GraphAssemblyPlan, e *pb.ExtractionBatch, r *pb.ResolutionBatch, o *Ontology) (*pb.DependencyManifest, error) {
	deps := map[string]*pb.ContentHash{}
	lookups := map[string]*pb.LookupScopeRevision{}
	add := func(id string, h *pb.ContentHash) error {
		if id == p.OutputArtifactId || deps[id] != nil && !proto.Equal(deps[id], h) {
			return errors.New("conflicting graph dependency")
		}
		deps[id] = h
		return nil
	}
	for _, manifest := range []*pb.DependencyManifest{e.Dependencies, r.Dependencies} {
		for _, dep := range manifest.Dependencies {
			if err := add(dep.DependencyId, dep.Fingerprint); err != nil {
				return nil, err
			}
		}
		for _, scope := range manifest.LookupScopeRevisions {
			if scope.Revision > p.RegistryRevision || lookups[scope.ScopeId] != nil && !proto.Equal(lookups[scope.ScopeId], scope) {
				return nil, errors.New("conflicting graph lookup scope")
			}
			lookups[scope.ScopeId] = scope
		}
	}
	for _, ref := range []*pb.ArtifactRef{p.DocumentBatch, p.ExtractionBatch, p.ResolutionBatch, p.RegistryView} {
		if err := add(ref.ArtifactId, ref.ContentHash); err != nil {
			return nil, err
		}
	}
	if err := add("ontology:"+o.Version(), o.ContentHash()); err != nil {
		return nil, err
	}
	result := &pb.DependencyManifest{ArtifactId: p.OutputArtifactId, ProducerManifest: proto.Clone(p.ProducerManifest).(*pb.ProducerManifest)}
	for id, h := range deps {
		result.Dependencies = append(result.Dependencies, &pb.Dependency{DependencyId: id, Fingerprint: proto.Clone(h).(*pb.ContentHash)})
	}
	for _, scope := range lookups {
		result.LookupScopeRevisions = append(result.LookupScopeRevisions, proto.Clone(scope).(*pb.LookupScopeRevision))
	}
	sort.Slice(result.Dependencies, func(i, j int) bool { return result.Dependencies[i].DependencyId < result.Dependencies[j].DependencyId })
	sort.Slice(result.LookupScopeRevisions, func(i, j int) bool {
		return result.LookupScopeRevisions[i].ScopeId < result.LookupScopeRevisions[j].ScopeId
	})
	return result, nil
}
