// Prepares query dependencies from one admitted published catalog binding.
// Endpoint credentials are selected by exact trusted origin; query preparation
// only reads Qdrant metadata and authenticated lexical artifacts, never creates
// collections. Native sessions and HTTP connections belong to reusable clients.
// Prepare once per generation; Bind creates only the request-scoped hydrator.
// Measure cold preparation separately from warm query p95/p99 and memory under
// configs/benchmark-targets.yaml; real model quality remains unmeasured.
package workflows

import (
	"context"
	"errors"
	"net/http"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
)

type PublishedQueryConfig struct {
	// Exact origins only; never attach a default credential to arbitrary catalog URLs.
	QdrantCredentials   map[string]string
	HTTPClient          *http.Client
	Profile             pb.RetrievalProfile
	Fusion              retrieval.RRFConfig
	Hydration           retrieval.HydrationConfig
	MaximumLexicalBytes uint64
	Reranker            *retrieval.EvidenceReranker // Optional explicit baseline choice, never failure fallback.
}

type PreparedQuery struct {
	binding domain.IndexCatalogBinding
	catalog retrieval.EvidenceCatalog
	reader  retrieval.EvidenceArtifactReader
	config  PublishedQueryConfig
	search  *CandidateSearch
	answer  *EvidenceAnswerWorkflow
}

func PreparePublishedQuery(ctx context.Context, index *domain.PinnedIndex, catalog retrieval.EvidenceCatalog, reader retrieval.EvidenceArtifactReader, embedding retrieval.EmbeddingClient, answer *EvidenceAnswerWorkflow, config PublishedQueryConfig) (*PreparedQuery, error) {
	if ctx == nil || index == nil || catalog == nil || reader == nil || embedding == nil || config.HTTPClient == nil {
		return nil, errors.New("published query dependencies required")
	}
	if err := domain.ValidateIndexCatalogBinding(index.Binding); err != nil {
		return nil, err
	}
	if config.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG && config.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG {
		return nil, errors.New("unsupported published query profile")
	}
	if config.Fusion.MaximumTotalInputs > config.Hydration.MaximumCandidates {
		return nil, errors.New("hydration budget must cover every fused input")
	}
	branches := []retrieval.RankedBranch{{Kind: pb.RetrieverKind_RETRIEVER_KIND_DENSE}}
	if config.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG {
		branches = append(branches, retrieval.RankedBranch{Kind: pb.RetrieverKind_RETRIEVER_KIND_BM25})
	}
	if _, err := retrieval.FuseRRF(branches, config.Fusion); err != nil {
		return nil, err
	}
	if _, err := retrieval.NewSourceHydrator(catalog, reader, index, config.Hydration); err != nil {
		return nil, err
	}
	key, ok := config.QdrantCredentials[index.Binding.Endpoint]
	if !ok {
		return nil, errors.New("published qdrant origin is not authorized by query configuration")
	}
	store, err := qdrant.New(index.Binding.Endpoint, key, config.HTTPClient, qdrant.Binding{Collection: index.Binding.Collection, CorpusID: index.Snapshot.CorpusId, Generation: index.Binding.Generation})
	if err != nil {
		return nil, err
	}
	if err = store.OpenExistingCollection(ctx); err != nil {
		return nil, err
	}
	search := &CandidateSearch{Fusion: config.Fusion, Dense: func(c context.Context, in retrieval.SearchInput) (*retrieval.BranchOutput, error) {
		return retrieval.RetrieveDense(c, in, embedding, store)
	}}
	if config.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG {
		// Initial snapshot catalogs have a self-contained dictionary. A declared
		// parent or older statistics base fails until checked ancestry is supplied.
		lexical, err := retrieval.LoadLexicalRetriever(ctx, registeredQueryArtifacts{catalog, reader, index.Snapshot.CorpusId}, index.Binding.Generation, store, nil, nil, config.MaximumLexicalBytes, domain.DefaultWireLimits)
		if err != nil {
			return nil, err
		}
		search.Lexical = lexical.Retrieve
	}
	owned := index.Binding
	owned.Generation = proto.Clone(owned.Generation).(*pb.IndexGeneration)
	config.Hydration.Producer = proto.Clone(config.Hydration.Producer).(*pb.ProducerManifest)
	weights := map[pb.RetrieverKind]float64{}
	for kind, value := range config.Fusion.Weights {
		weights[kind] = value
	}
	search.Fusion.Weights = weights
	return &PreparedQuery{binding: owned, catalog: catalog, reader: reader, config: config, search: search, answer: answer}, nil
}

// Bind is a PinnedRAGFactory. Generation rollover requires preparing a new
// immutable resource set; an old dictionary/model is never silently reused.
func (p *PreparedQuery) Bind(ctx context.Context, index *domain.PinnedIndex) (*RAGWorkflow, error) {
	if p == nil || ctx == nil || index == nil {
		return nil, errors.New("prepared query and pin required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := index.Binding
	if b.PublicationID != p.binding.PublicationID || b.Fence != p.binding.Fence || b.Endpoint != p.binding.Endpoint || b.Collection != p.binding.Collection || !proto.Equal(b.Generation, p.binding.Generation) {
		return nil, errors.New("query generation changed; prepare published resources again")
	}
	h, err := retrieval.NewSourceHydrator(p.catalog, p.reader, index, p.config.Hydration)
	if err != nil {
		return nil, err
	}
	hydrate, err := StorageCandidateHydrator(h, index.Snapshot)
	if err != nil {
		return nil, err
	}
	search := *p.search
	search.Fusion.Weights = map[pb.RetrieverKind]float64{}
	for kind, value := range p.search.Fusion.Weights {
		search.Fusion.Weights[kind] = value
	}
	return &RAGWorkflow{Search: &search, Hydrate: hydrate, Answer: p.answer, Reranker: p.config.Reranker, profile: p.config.Profile}, nil
}

type registeredQueryArtifacts struct {
	catalog retrieval.EvidenceCatalog
	reader  retrieval.EvidenceArtifactReader
	corpus  string
}

func (r registeredQueryArtifacts) ReadVerified(ctx context.Context, ref *pb.ArtifactRef, max uint64) ([]byte, error) {
	registered, err := r.catalog.LoadArtifact(ctx, r.corpus, ref.ArtifactId)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(registered, ref) {
		return nil, domain.ErrPersistentIntegrity
	}
	return r.reader.ReadVerified(ctx, ref, max)
}
