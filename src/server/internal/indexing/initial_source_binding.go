// Authenticates an unpublished CHUNK artifact, builds a snapshot metadata
// envelope without rewriting legal records, persists immutable bytes/dependencies,
// and records the fenced original-to-derived receipt. Interrupted writes may
// leave unused immutable artifacts; only a committed receipt grants source
// authority. Final whole-population planning remains mandatory afterward.
// Bound source bytes are limited to16MiB; measure I/O/RSS and binding latency
// under configs/benchmark-targets.yaml (required acceptance unmeasured).
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

type IndexSourceBindingStore interface {
	IndexAuthority
	IndexPlanRegistry
	RegisterIndexSourceBinding(context.Context, domain.IndexSourceBinding) error
}

func BindInitialIndexSource(ctx context.Context, store IndexSourceBindingStore, reader IndexArtifactReader, writer IndexPlanArtifactWriter,
	publication string, fence uint64, snapshot *pb.SnapshotRef, scope string, input InitialIndexSource) (InitialIndexSource, error) {
	if ctx == nil || store == nil || reader == nil || writer == nil || snapshot == nil || input.DocumentBatch == nil {
		return InitialIndexSource{}, errors.New("source binding dependencies required")
	}
	if err := ctx.Err(); err != nil {
		return InitialIndexSource{}, err
	}
	loader := &initialArtifactLoader{authority: store, reader: reader, corpus: snapshot.CorpusId, remaining: 16 << 20, cache: map[string]initialArtifact{}}
	original := new(pb.DocumentBatch)
	if err := loader.read(ctx, input.DocumentBatch, original); err != nil {
		return InitialIndexSource{}, err
	}
	if err := store.VerifyIndexSourceCheckpoint(ctx, snapshot.CorpusId, input.SourceJobID, input.DocumentBatch); err != nil {
		return InitialIndexSource{}, err
	}
	bound, err := domain.BindInitialSnapshotSource(input.SourceJobID, original, input.DocumentBatch, snapshot, scope)
	if err != nil {
		return InitialIndexSource{}, err
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(bound)
	if err != nil {
		return InitialIndexSource{}, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	// Preserve the admitted media label, including legacy exact typed exports,
	// so replay never mutates registered metadata for otherwise identical bytes.
	ref := &pb.ArtifactRef{ArtifactId: "artifact:document-batch:" + hash, ContentHash: &pb.ContentHash{Sha256: hash}, StorageKey: "sha256/" + hash[:2] + "/" + hash[2:4] + "/" + hash + ".bin", SchemaVersion: 1, ByteSize: uint64(len(raw)), MediaType: input.DocumentBatch.MediaType}
	binding := domain.IndexSourceBinding{PublicationID: publication, Fence: fence, SourceJobID: input.SourceJobID, Snapshot: snapshot, AuthScope: scope, Original: input.DocumentBatch, Bound: ref}
	if err = domain.ValidateIndexSourceBinding(binding); err != nil {
		return InitialIndexSource{}, err
	}
	if _, err = writer.Put(ctx, ref, bytes.NewReader(raw)); err != nil {
		return InitialIndexSource{}, err
	}
	if err = store.RegisterArtifact(ctx, snapshot.CorpusId, ref); err != nil {
		return InitialIndexSource{}, err
	}
	if err = store.ReplaceArtifactDependencyManifest(ctx, snapshot.CorpusId, ref.ArtifactId, bound.DependencyManifest); err != nil {
		return InitialIndexSource{}, err
	}
	if err = store.RegisterIndexSourceBinding(ctx, binding); err != nil {
		return InitialIndexSource{}, err
	}
	return InitialIndexSource{SourceJobID: input.SourceJobID, DocumentBatch: ref}, nil
}
