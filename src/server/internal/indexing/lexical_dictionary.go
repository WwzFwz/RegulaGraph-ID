// Allocates a complete operator-selected vocabulary in bounded registry batches
// and persists one immutable root dictionary for the Rust statistics pass.
// Input terms must come from the same verified rendered population. This layer
// owns registry calls/artifact handoff, never corpus membership or publication.
// Deterministic operation keys repair interrupted writes without allocating new
// IDs on replay. Record allocation/I/O/RSS and contention against required
// configs/benchmark-targets.yaml; fixture success is not quality acceptance.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type LexicalDictionaryRegistry interface {
	AllocateLexicalTerms(context.Context, string, string, string, uint64, []string) ([]domain.LexicalTerm, uint64, error)
	ExportLexicalDictionary(context.Context, string, string, string, uint64, int, domain.WireLimits) (*pb.LexicalDictionaryArtifact, error)
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
}

func PrepareLexicalDictionary(ctx context.Context, registry LexicalDictionaryRegistry, writer IndexPlanArtifactWriter, corpus string, terms []string) (*pb.ArtifactRef, error) {
	if ctx == nil || registry == nil || writer == nil || len(terms) == 0 || len(terms) > (domain.DefaultWireLimits.MaxItems-2)/2 {
		return nil, errors.New("bounded vocabulary and dictionary dependencies required")
	}
	if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "dictionary:preparation"}, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	owned := append([]string(nil), terms...)
	total := 0
	for i, term := range owned {
		total += len(term)
		if len(term) == 0 || len(term) > 256 || total > 16<<20 || !utf8.ValidString(term) || i > 0 && owned[i-1] >= term {
			return nil, errors.New("vocabulary must be sorted, distinct, bounded UTF-8 terms")
		}
		for _, r := range term {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return nil, errors.New("invalid vocabulary term")
			}
		}
	}
	// Include all terms, not just the current page, so a changed inventory cannot
	// accidentally replay part of a previous preparation as the same operation.
	fields := append([]string{corpus, domain.LexicalAnalyzerV1}, owned...)
	inventory := initialPlanID("vocabulary-v1", fields...)
	var revision uint64
	for start := 0; start < len(owned); start += 5000 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page := owned[start:min(start+5000, len(owned))]
		key := initialPlanID("dictionary-allocation-v1", inventory, fmt.Sprint(start))
		mapped, next, err := registry.AllocateLexicalTerms(ctx, corpus, domain.LexicalAnalyzerV1, key, 0, append([]string(nil), page...))
		if err != nil {
			return nil, err
		}
		if next == 0 || next < revision || len(mapped) != len(page) {
			return nil, errors.New("dictionary allocation accounting/revision mismatch")
		}
		for i, term := range mapped {
			if term.Term != page[i] || term.ID == 0 {
				return nil, errors.New("dictionary allocation term mismatch")
			}
		}
		revision = next
	}
	id := initialPlanID("dictionary-v1", corpus, domain.LexicalAnalyzerV1, fmt.Sprint(revision))
	artifact, err := registry.ExportLexicalDictionary(ctx, corpus, domain.LexicalAnalyzerV1, id, revision, (domain.DefaultWireLimits.MaxItems-2)/2, domain.DefaultWireLimits)
	if err != nil {
		return nil, err
	}
	checked, err := domain.CheckLexicalDictionaryArtifact(artifact, corpus, nil, domain.DefaultWireLimits)
	if err != nil {
		return nil, err
	}
	if artifact.Meta.RecordId != id || checked.RegistryRevision() != revision || checked.AnalyzerID() != domain.LexicalAnalyzerV1 {
		return nil, errors.New("dictionary export identity drift")
	}
	mapping := checked.Terms()
	for _, term := range owned {
		if mapping[term] == 0 {
			return nil, errors.New("dictionary export omitted allocated term")
		}
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(artifact)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	ref := &pb.ArtifactRef{ArtifactId: id, SchemaVersion: 1, ContentHash: &pb.ContentHash{Sha256: digest}, ByteSize: uint64(len(raw)), MediaType: "application/x-protobuf; message=" + string(artifact.ProtoReflect().Descriptor().FullName()), StorageKey: "sha256/" + digest[:2] + "/" + digest[2:4] + "/" + digest + ".bin"}
	if err = domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if _, err = writer.Put(ctx, ref, bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	if err = registry.RegisterArtifact(ctx, corpus, ref); err != nil {
		return nil, err
	}
	return ref, nil
}
