// Prepares a first dense/BM25 snapshot from an explicit complete CHUNK selection.
// Authenticated sources are inspected before reservation; snapshot identity binds
// the ordered job/reference set, scope, publication and representation generation.
// Corpus facts use the existing evaluation JSON format: documents are distinct
// raw source hashes, chunks are distinct retained chunk IDs, graph counts are zero
// because this profile has no graph. Counts are observations, not eligibility PASS.
// Original sources remain immutable; fenced envelopes provide Rust population
// inputs. Replay repairs partial writes using the same selection and IDs.
// Measure source admission/binding RSS/I/O against benchmark-targets.yaml; bounds
// never silently truncate the selection or redefine the required workload.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type InitialSourceSnapshotStore interface {
	IndexSourceBindingStore
	ReservePublication(context.Context, string, string, string, string, string) (domain.PublicationReservation, error)
}

type InitialSourceSnapshotConfig struct{ CorpusID, PublicationID, GenerationID, AuthScope string }

type InitialSourceSnapshot struct {
	Reservation   domain.PublicationReservation
	Snapshot      *pb.SnapshotRef
	Manifest      *pb.ArtifactRef
	ManifestBytes []byte
	Sources       []InitialIndexSource
}

func PrepareInitialSourceSnapshot(ctx context.Context, store InitialSourceSnapshotStore, reader IndexArtifactReader, writer IndexPlanArtifactWriter, cfg InitialSourceSnapshotConfig, inputs []InitialIndexSource) (*InitialSourceSnapshot, error) {
	if ctx == nil || store == nil || reader == nil || writer == nil || len(inputs) == 0 || len(inputs) > 256 {
		return nil, errors.New("bounded CHUNK selection and snapshot dependencies required")
	}
	for _, id := range []string{cfg.PublicationID, cfg.GenerationID, cfg.AuthScope} {
		if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: cfg.CorpusID, RecordId: id}, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	selected := append([]InitialIndexSource(nil), inputs...)
	for i, input := range selected {
		if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: cfg.CorpusID, RecordId: input.SourceJobID}, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		if err := domain.ValidateWire(input.DocumentBatch, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		selected[i].DocumentBatch = proto.Clone(input.DocumentBatch).(*pb.ArtifactRef)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].SourceJobID < selected[j].SourceJobID })
	loader := &initialArtifactLoader{authority: store, reader: reader, corpus: cfg.CorpusID, remaining: 64 << 20, cache: map[string]initialArtifact{}}
	chunks, documents, artifacts := map[string]bool{}, map[string]bool{}, map[string]bool{}
	identity := []string{cfg.CorpusID, cfg.PublicationID, cfg.GenerationID, cfg.AuthScope}
	for i, input := range selected {
		if i > 0 && selected[i-1].SourceJobID == input.SourceJobID || artifacts[input.DocumentBatch.ArtifactId] {
			return nil, errors.New("duplicate source job/artifact in snapshot selection")
		}
		artifacts[input.DocumentBatch.ArtifactId] = true
		source := new(pb.DocumentBatch)
		if err := loader.read(ctx, input.DocumentBatch, source); err != nil {
			return nil, err
		}
		if _, err := domain.NewIndexSourceView(source, 1_000_000); err != nil {
			return nil, err
		}
		if source.Context.SnapshotRef != nil || source.Context.AuthScopeRef != cfg.AuthScope || len(source.Chunks) == 0 {
			return nil, errors.New("snapshot preparation requires unpublished complete CHUNK sources in the selected scope")
		}
		if err := store.VerifyIndexSourceCheckpoint(ctx, cfg.CorpusID, input.SourceJobID, input.DocumentBatch); err != nil {
			return nil, err
		}
		for _, chunk := range source.Chunks {
			if chunks[chunk.Meta.RecordId] {
				return nil, errors.New("duplicate chunk in snapshot selection")
			}
			chunks[chunk.Meta.RecordId] = true
			if len(chunks) > 32768 {
				return nil, errors.New("initial snapshot exceeds population chunk bound")
			}
		}
		for _, blob := range source.Sources {
			documents[blob.RawSha256.Sha256] = true
		}
		raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(input.DocumentBatch)
		if err != nil {
			return nil, err
		}
		identity = append(identity, input.SourceJobID, fmt.Sprintf("%x", sha256.Sum256(raw)))
	}
	// Release decoded-source cache before binding performs another bounded pass.
	loader.cache = nil
	snapshotID := initialPlanID("initial-source-snapshot-v1", identity...)
	facts := struct {
		SchemaVersion     int    `json:"schema_version"`
		CorpusID          string `json:"corpus_id"`
		SnapshotID        string `json:"snapshot_id"`
		Generation        string `json:"representation_generation"`
		Documents         int    `json:"documents"`
		Chunks            int    `json:"chunks"`
		CanonicalEntities int    `json:"canonical_entities"`
		GraphEdges        int    `json:"graph_edges"`
	}{1, cfg.CorpusID, snapshotID, cfg.GenerationID, len(documents), len(chunks), 0, 0}
	raw, err := json.Marshal(facts)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	ref := &pb.ArtifactRef{SchemaVersion: 1, ArtifactId: "corpus-facts:" + digest, ContentHash: &pb.ContentHash{Sha256: digest}, ByteSize: uint64(len(raw)), MediaType: "application/json", StorageKey: "sha256/" + digest[:2] + "/" + digest[2:4] + "/" + digest + ".bin"}
	reservation, err := store.ReservePublication(ctx, cfg.PublicationID, "", cfg.CorpusID, snapshotID, "")
	if err != nil {
		return nil, err
	}
	if reservation.PublicationID != cfg.PublicationID || reservation.CorpusID != cfg.CorpusID || reservation.SnapshotID != snapshotID || reservation.ParentSnapshotID != "" || reservation.Fence == 0 || reservation.Sequence == 0 || reservation.Sequence > 1<<53-1 ||
		(reservation.State != pb.SnapshotState_SNAPSHOT_STATE_STAGING && reservation.State != pb.SnapshotState_SNAPSHOT_STATE_VALIDATING) {
		return nil, errors.New("initial snapshot reservation is not live or exact")
	}
	snapshot := &pb.SnapshotRef{CorpusId: cfg.CorpusID, SnapshotId: snapshotID, Sequence: reservation.Sequence, ManifestHash: proto.Clone(ref.ContentHash).(*pb.ContentHash), RepresentationGeneration: cfg.GenerationID}
	if _, err = writer.Put(ctx, ref, bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	if err = store.RegisterArtifact(ctx, cfg.CorpusID, ref); err != nil {
		return nil, err
	}
	policy := fmt.Sprintf("%x", sha256.Sum256([]byte("initial-source-snapshot-v1")))
	dependencies := &pb.DependencyManifest{ArtifactId: ref.ArtifactId, ProducerManifest: &pb.ProducerManifest{Software: "regulagraph-initial-snapshot", Build: "v1", SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: policy}}}
	for _, input := range selected {
		dependencies.Dependencies = append(dependencies.Dependencies, &pb.Dependency{DependencyId: input.DocumentBatch.ArtifactId, Fingerprint: proto.Clone(input.DocumentBatch.ContentHash).(*pb.ContentHash)})
		dependencies.ProducerManifest.InputHashes = append(dependencies.ProducerManifest.InputHashes, proto.Clone(input.DocumentBatch.ContentHash).(*pb.ContentHash))
	}
	if err = store.ReplaceArtifactDependencyManifest(ctx, cfg.CorpusID, ref.ArtifactId, dependencies); err != nil {
		return nil, err
	}
	result := &InitialSourceSnapshot{Reservation: reservation, Snapshot: snapshot, Manifest: ref, ManifestBytes: raw}
	for _, input := range selected {
		bound, e := BindInitialIndexSource(ctx, store, reader, writer, reservation.PublicationID, reservation.Fence, snapshot, cfg.AuthScope, input)
		if e != nil {
			return nil, e
		}
		result.Sources = append(result.Sources, bound)
	}
	return result, nil
}
