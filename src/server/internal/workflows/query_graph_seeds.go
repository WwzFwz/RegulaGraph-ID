// Connects exact-alias query discovery to a live published graph registry view.
// Serving configuration fixes namespaces and resource budgets; no request chooses
// arbitrary registry revisions. The result preserves ambiguous candidates and
// unresolved reasons for graph completeness, without mutating canonical identity.
// Required linking recall/latency targets remain unmeasured until corpus/gold runs.
package workflows

import (
	"context"
	"errors"
	"time"

	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/query"
)

// NewQueryGraphSeedResolver owns policy slices and reuses the batched registry
// reader. It performs no I/O/model loading until invoked under an admitted pin.
func NewQueryGraphSeedResolver(reader query.PinnedAliasReader, policy query.EntityLinkingPolicy) (GraphSeedResolver, error) {
	if reader == nil {
		return nil, errors.New("pinned alias reader required")
	}
	if _, err := policy.Fingerprint(); err != nil {
		return nil, err
	}
	policy.Namespaces = append([]query.EntityNamespace(nil), policy.Namespaces...)
	return func(ctx context.Context, question string, view *domain.PinnedGraph) (*query.EntityLinks, error) {
		if ctx == nil || view == nil || view.Snapshot == nil || view.AuthScope == "" || view.Pin.CorpusID != view.Snapshot.CorpusId || view.Pin.SnapshotID != view.Snapshot.SnapshotId || view.Pin.Sequence != view.Snapshot.Sequence || !view.Pin.ExpiresAt.After(time.Now()) || view.Catalog.Binding.CorpusID != view.Pin.CorpusID || view.Catalog.Binding.Sequence != view.Pin.Sequence {
			return nil, errors.New("matching live graph view required for query linking")
		}
		return query.LinkQueryAliases(ctx, reader, view.Pin, view.Catalog.Binding.RegistryRevision, question, policy)
	}, nil
}
