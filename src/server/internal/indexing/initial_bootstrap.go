// Connects frozen Rust population output and an explicitly pinned embedding
// model to the durable initial INDEX inventory. Go authenticates/imports lexical
// artifacts, constructs the supported generation, admits the full source set,
// persists plans and atomically schedules children. Rust remains the owner of
// rendering/statistics/embedding transforms; this importer does not invent DF.
// Failed preparation may leave immutable prerequisites but no partial inventory.
// Measure preparation I/O/RSS and scheduling p95 under benchmark-targets.yaml;
// model selection and corpus quality still require separate acceptance.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type InitialIndexBootstrapStore interface {
	IndexAuthority
	IndexPlanRegistry
	EnsureArtifactDependencyManifest(context.Context, string, string, *pb.DependencyManifest) error
	ReservePublication(context.Context, string, string, string, string, string) (domain.PublicationReservation, error)
	ScheduleIndexJobs(context.Context, domain.IndexJobInventory) error
}

type InitialIndexBootstrapConfig struct {
	PublicationID, Endpoint, Collection, AuthScope, OntologyVersion string
	Snapshot                                                        *pb.SnapshotRef
	Model                                                           *pb.ModelManifest
	Dictionary, Statistics                                          *pb.ArtifactRef
	ChunksPerBatch                                                  int
}

func BootstrapInitialIndex(ctx context.Context, store InitialIndexBootstrapStore, reader IndexArtifactReader, writer IndexPlanArtifactWriter, cfg InitialIndexBootstrapConfig, sources []InitialIndexSource) (domain.IndexJobInventory, error) {
	var empty domain.IndexJobInventory
	if ctx == nil || store == nil || reader == nil || writer == nil || len(sources) == 0 || len(sources) > 256 || cfg.ChunksPerBatch < 1 || cfg.ChunksPerBatch > 128 {
		return empty, errors.New("bounded initial bootstrap configuration required")
	}
	for _, m := range []proto.Message{cfg.Snapshot, cfg.Model, cfg.Dictionary, cfg.Statistics} {
		if err := domain.ValidateWire(m, domain.DefaultWireLimits); err != nil {
			return empty, err
		}
	}
	corpus := cfg.Snapshot.CorpusId
	loader := &initialArtifactLoader{authority: store, reader: reader, corpus: corpus, remaining: 64 << 20, cache: map[string]initialArtifact{}}
	dictionary := new(pb.LexicalDictionaryArtifact)
	if err := loader.read(ctx, cfg.Dictionary, dictionary); err != nil {
		return empty, err
	}
	checked, err := domain.CheckLexicalDictionaryArtifact(dictionary, corpus, nil, domain.DefaultWireLimits)
	if err != nil {
		return empty, err
	}
	if err = store.VerifyIndexDictionary(ctx, corpus, dictionary); err != nil {
		return empty, err
	}
	// Statistics are the one external worker artifact not yet registered by Go.
	ref := cfg.Statistics
	if ref.MediaType != "application/x-protobuf; message=regulagraph.v1.LexicalStatisticsArtifact" || ref.ByteSize == 0 || ref.ByteSize > 16<<20 {
		return empty, errors.New("bounded typed statistics artifact required")
	}
	raw, err := reader.ReadVerified(ctx, ref, ref.ByteSize)
	if err != nil {
		return empty, err
	}
	if uint64(len(raw)) != ref.ByteSize || fmt.Sprintf("%x", sha256.Sum256(raw)) != ref.ContentHash.Sha256 {
		return empty, domain.ErrPersistentIntegrity
	}
	statistics := new(pb.LexicalStatisticsArtifact)
	if err = domain.DecodeWire(raw, statistics, domain.DefaultWireLimits); err != nil {
		return empty, err
	}
	if err = domain.CheckLexicalStatisticsArtifact(statistics, checked, domain.DefaultWireLimits); err != nil {
		return empty, err
	}
	if statistics.Meta.RecordId != ref.ArtifactId || !proto.Equal(statistics.PopulationSnapshot, cfg.Snapshot) || statistics.InputPolicy != "structure-labels-v1" {
		return empty, errors.New("statistics identity/population policy mismatch")
	}
	analyzer := &pb.LexicalAnalyzerArtifact{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: initialPlanID("lexical-analyzer-v1", corpus, domain.LexicalAnalyzerV1)}, AnalyzerId: domain.LexicalAnalyzerV1, UnicodeVersion: domain.LexicalUnicodeV1, MaximumInputBytes: 2_000_000, MaximumTermBytes: 256, MaximumDocumentTerms: 100_000, MaximumQueryTerms: 1_024}
	if err = domain.ValidateLexicalAnalyzerArtifact(analyzer, corpus, domain.DefaultWireLimits); err != nil {
		return empty, err
	}
	analyzerBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(analyzer)
	if err != nil {
		return empty, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(analyzerBytes))
	analyzerRef := &pb.ArtifactRef{SchemaVersion: 1, ArtifactId: analyzer.Meta.RecordId, ContentHash: &pb.ContentHash{Sha256: digest}, ByteSize: uint64(len(analyzerBytes)), MediaType: "application/x-protobuf; message=regulagraph.v1.LexicalAnalyzerArtifact", StorageKey: "sha256/" + digest[:2] + "/" + digest[2:4] + "/" + digest + ".bin"}
	generation := &pb.IndexGeneration{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: cfg.Snapshot.RepresentationGeneration}, DenseManifest: proto.Clone(cfg.Model).(*pb.ModelManifest), LexicalAnalyzer: analyzerRef, LexicalDictionary: proto.Clone(cfg.Dictionary).(*pb.ArtifactRef), LexicalStatistics: proto.Clone(ref).(*pb.ArtifactRef), OntologyVersion: cfg.OntologyVersion, FilterFormat: pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1, EmbeddingInputPolicy: "structure-labels-v1"}
	binding := domain.IndexCatalogBinding{PublicationID: cfg.PublicationID, Fence: 1, Endpoint: cfg.Endpoint, Collection: cfg.Collection, Generation: generation}
	if err = domain.ValidateIndexCatalogBinding(binding); err != nil {
		return empty, err
	}
	genBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(generation)
	if err != nil {
		return empty, err
	}
	producer := &pb.ProducerManifest{Software: "regulagraph-initial-index", Build: "v1", SchemaVersion: 1, Models: []*pb.ModelManifest{proto.Clone(cfg.Model).(*pb.ModelManifest)}, ConfigHash: &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256(genBytes))}, InputHashes: []*pb.ContentHash{proto.Clone(cfg.Snapshot.ManifestHash).(*pb.ContentHash)}}
	if _, err = writer.Put(ctx, analyzerRef, bytes.NewReader(analyzerBytes)); err != nil {
		return empty, err
	}
	if err = store.RegisterArtifact(ctx, corpus, analyzerRef); err != nil {
		return empty, err
	}
	if err = store.RegisterArtifact(ctx, corpus, ref); err != nil {
		return empty, err
	}
	importProducer := &pb.ProducerManifest{Software: "regulagraph-statistics-import", Build: "v1", SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256([]byte("statistics-import-v1")))}, InputHashes: []*pb.ContentHash{proto.Clone(ref.ContentHash).(*pb.ContentHash)}}
	deps := &pb.DependencyManifest{ArtifactId: ref.ArtifactId, ProducerManifest: importProducer, Dependencies: []*pb.Dependency{{DependencyId: cfg.Dictionary.ArtifactId, Fingerprint: proto.Clone(cfg.Dictionary.ContentHash).(*pb.ContentHash)}}}
	for _, source := range sources {
		if err = domain.ValidateWire(source.DocumentBatch, domain.DefaultWireLimits); err != nil {
			return empty, err
		}
		deps.Dependencies = append(deps.Dependencies, &pb.Dependency{DependencyId: source.DocumentBatch.ArtifactId, Fingerprint: proto.Clone(source.DocumentBatch.ContentHash).(*pb.ContentHash)})
	}
	reservation, err := store.ReservePublication(ctx, cfg.PublicationID, "", corpus, cfg.Snapshot.SnapshotId, "")
	if err != nil {
		return empty, err
	}
	if reservation.Sequence != cfg.Snapshot.Sequence || reservation.ParentSnapshotID != "" {
		return empty, errors.New("snapshot reservation differs from prepared population")
	}
	binding.Fence = reservation.Fence
	plans, err := PlanInitialIndex(ctx, store, reader, InitialIndexPlanConfig{Binding: binding, Snapshot: cfg.Snapshot, Producer: producer, DictionaryChain: []*pb.ArtifactRef{cfg.Dictionary}, AuthScope: cfg.AuthScope, ChunksPerBatch: cfg.ChunksPerBatch}, sources)
	if err != nil {
		return empty, err
	}
	if err = store.EnsureArtifactDependencyManifest(ctx, corpus, ref.ArtifactId, deps); err != nil {
		return empty, err
	}
	if err = plans.Persist(ctx, writer, store); err != nil {
		return empty, err
	}
	inventory, err := plans.JobInventory()
	if err != nil {
		return empty, err
	}
	if err = store.ScheduleIndexJobs(ctx, inventory); err != nil {
		return empty, err
	}
	return inventory, nil
}
