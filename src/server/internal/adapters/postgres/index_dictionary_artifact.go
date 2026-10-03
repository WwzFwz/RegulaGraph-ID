// Exports an authoritative, pinned PostgreSQL term mapping into the shared typed
// dictionary artifact. This read-only adapter produces a root snapshot proving
// no earlier ancestry; storage publication, artifact hashes and generation binding
// remain coordinator responsibilities. Bound vocabulary and wire bytes, load once
// per generation, and measure RSS/load p95 under configs/benchmark-targets.yaml.
package postgres

import (
	"context"
	"errors"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) ExportLexicalDictionary(ctx context.Context, corpusID, analyzerID, recordID string,
	revision uint64, maxTerms int, limits domain.WireLimits) (*pb.LexicalDictionaryArtifact, error) {
	meta := &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: recordID}
	if err := domain.ValidateWire(meta, limits); err != nil {
		return nil, err
	}
	// Full root wire cost is 2 items per term plus meta and mapping hash.
	// Bound before SQL loads the mapping; final builder still checks byte size.
	if limits.MaxItems < 2 || maxTerms > (limits.MaxItems-2)/2 {
		return nil, errors.New("dictionary term bound exceeds wire item budget")
	}
	entries, err := r.LoadLexicalDictionary(ctx, corpusID, analyzerID, revision, maxTerms)
	if err != nil {
		return nil, err
	}
	return domain.BuildLexicalDictionaryArtifact(meta, analyzerID, revision, entries, nil, limits)
}
