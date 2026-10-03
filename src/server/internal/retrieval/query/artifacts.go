// Loads a BM25 query encoder from the exact immutable artifacts referenced by an
// admitted index generation. Each payload is checked for size/hash/type/identity;
// dictionary ancestry is supplied as checked objects, not claimed by ID alone.
// Loading is bounded and done once; Encode performs no storage reads. The caller
// still proves publication, registry authority and population membership. Measure
// cold load RSS/time separately from warm query p95/p99; required gates follow
// configs/benchmark-targets.yaml and remain unmeasured.
package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type ArtifactReader interface {
	ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error)
}

// LoadArtifactBM25Encoder checks three generation-owned artifacts. parent is the
// exact dictionary parent, when declared. statisticsBase is required only when
// frozen statistics use an older checked dictionary in that same chain.
func LoadArtifactBM25Encoder(ctx context.Context, reader ArtifactReader, generation *pb.IndexGeneration,
	parent, statisticsBase *domain.CheckedLexicalDictionary, maximumBytes uint64, limits domain.WireLimits) (*PinnedBM25QueryEncoder, error) {
	if ctx == nil || reader == nil || generation == nil || maximumBytes == 0 || maximumBytes > 1<<30 {
		return nil, errors.New("bounded artifact reader and generation required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidatePairedIndexGeneration(generation); err != nil {
		return nil, err
	}
	if unicode.Version != domain.LexicalUnicodeV1 || norm.Version != domain.LexicalUnicodeV1 {
		return nil, errors.New("runtime Unicode tables differ from pinned analyzer")
	}
	generation = proto.Clone(generation).(*pb.IndexGeneration)
	remaining := maximumBytes
	seen := map[string]bool{}
	read := func(ref *pb.ArtifactRef, msg interface {
		proto.Message
		GetMeta() *pb.RecordMeta
	}) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := domain.ValidateWire(ref, limits); err != nil {
			return err
		}
		if ref.ByteSize == 0 || ref.ByteSize > remaining || ref.ByteSize > uint64(limits.MaxBytes) || seen[ref.ArtifactId] ||
			ref.MediaType != "application/x-protobuf; message="+string(msg.ProtoReflect().Descriptor().FullName()) {
			return errors.New("lexical artifact budget, identity or media type mismatch")
		}
		seen[ref.ArtifactId] = true
		raw, err := reader.ReadVerified(ctx, proto.Clone(ref).(*pb.ArtifactRef), ref.ByteSize)
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		if uint64(len(raw)) != ref.ByteSize || hex.EncodeToString(digest[:]) != ref.ContentHash.Sha256 {
			return domain.ErrPersistentIntegrity
		}
		remaining -= ref.ByteSize
		if err = domain.DecodeWire(raw, msg, limits); err != nil {
			return err
		}
		record := msg.GetMeta()
		if record.CorpusId != generation.Meta.CorpusId || record.RecordId != ref.ArtifactId {
			return domain.ErrPersistentIntegrity
		}
		return nil
	}
	analyzer := new(pb.LexicalAnalyzerArtifact)
	if err := read(generation.LexicalAnalyzer, analyzer); err != nil {
		return nil, fmt.Errorf("load analyzer: %w", err)
	}
	if err := domain.ValidateLexicalAnalyzerArtifact(analyzer, generation.Meta.CorpusId, limits); err != nil {
		return nil, err
	}
	dictionary := new(pb.LexicalDictionaryArtifact)
	if err := read(generation.LexicalDictionary, dictionary); err != nil {
		return nil, fmt.Errorf("load dictionary: %w", err)
	}
	checked, err := domain.CheckLexicalDictionaryArtifact(dictionary, generation.Meta.CorpusId, parent, limits)
	if err != nil {
		return nil, err
	}
	stats := new(pb.LexicalStatisticsArtifact)
	if err = read(generation.LexicalStatistics, stats); err != nil {
		return nil, fmt.Errorf("load statistics: %w", err)
	}
	if stats.InputPolicy != generation.EmbeddingInputPolicy {
		return nil, errors.New("lexical population input policy differs from index generation")
	}
	if stats.DictionaryRegistryRevision == checked.RegistryRevision() {
		statisticsBase = checked
	}
	if err = domain.CheckLexicalStatisticsArtifact(stats, statisticsBase, limits); err != nil {
		return nil, err
	}
	revision, _ := domain.LexicalRevisionName(checked.RegistryRevision())
	baseRevision, _ := domain.LexicalRevisionName(statisticsBase.RegistryRevision())
	df := make(map[uint32]uint64, len(stats.DocumentFrequencies))
	for _, item := range stats.DocumentFrequencies {
		df[item.TermId] = item.DocumentCount
	}
	encoder, err := NewPinnedBM25QueryEncoder(SparseDictionaryView{AnalyzerID: checked.AnalyzerID(), Revision: revision, Terms: checked.Terms(), Lineage: checked.Lineage()},
		FrozenBM25View{AnalyzerID: stats.AnalyzerId, DictionaryRevision: baseRevision, DictionaryFingerprint: statisticsBase.Fingerprint(), DocumentCount: stats.DocumentCount, TotalTokens: stats.TotalTokens, DFByID: df})
	if err != nil {
		return nil, err
	}
	encoder.generation = generation
	encoder.population = proto.Clone(stats.PopulationSnapshot).(*pb.SnapshotRef)
	return encoder, nil
}

// ArtifactBinding returns owned copies; nil means the legacy in-memory
// constructor did not authenticate the generation artifacts.
func (encoder *PinnedBM25QueryEncoder) ArtifactBinding() (*pb.IndexGeneration, *pb.SnapshotRef) {
	if encoder == nil || encoder.generation == nil {
		return nil, nil
	}
	return proto.Clone(encoder.generation).(*pb.IndexGeneration), proto.Clone(encoder.population).(*pb.SnapshotRef)
}
