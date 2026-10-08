// Builds a publication-bound metadata envelope around an unpublished CHUNK
// DocumentBatch. Legal records, text/span mappings and chunk identities remain
// unchanged; the original immutable artifact becomes the explicit dependency.
// Callers must authenticate original bytes/checkpoint, reserve the target and
// persist a fenced source-binding receipt before using this derived artifact.
// This pure transform proves no database authority or legal applicability.
// Measure binding RSS/bytes/time under configs/benchmark-targets.yaml.
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const SnapshotSourceBindingPolicy = "initial-snapshot-source-envelope-v1"

// BindInitialSnapshotSource is restricted to unpublished source envelopes.
// Reusing content from an existing snapshot requires a separate membership proof.
func BindInitialSnapshotSource(sourceJob string, original *pb.DocumentBatch, originalRef *pb.ArtifactRef, target *pb.SnapshotRef, scope string) (*pb.DocumentBatch, error) {
	for _, m := range []proto.Message{originalRef, target} {
		if err := ValidateWire(m, DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	if _, err := NewIndexSourceView(original, 1_000_000); err != nil {
		return nil, err
	}
	for _, id := range []string{sourceJob, scope} {
		if err := ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: target.CorpusId, RecordId: id}, DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	if original.Meta.CorpusId != target.CorpusId || original.Context.CorpusId != target.CorpusId || original.Context.AuthScopeRef != scope || original.Context.SnapshotRef != nil ||
		originalRef.MediaType != "application/x-protobuf; message=regulagraph.v1.DocumentBatch" || originalRef.ByteSize == 0 || len(original.Chunks) == 0 {
		return nil, errors.New("initial snapshot binding requires complete unpublished CHUNK source in the same scope/corpus")
	}
	refBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(originalRef)
	if err != nil {
		return nil, err
	}
	snapshotBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(target)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, part := range [][]byte{[]byte(SnapshotSourceBindingPolicy), []byte(sourceJob), refBytes, snapshotBytes, []byte(scope)} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write(part)
	}
	id := fmt.Sprintf("snapshot-source:%x", h.Sum(nil))
	policyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(SnapshotSourceBindingPolicy)))
	result := proto.Clone(original).(*pb.DocumentBatch)
	result.Meta.RecordId = id
	result.Context.RequestId = id
	result.Context.TraceId = id
	result.Context.SnapshotRef = proto.Clone(target).(*pb.SnapshotRef)
	result.Context.ConfigFingerprint = &pb.ContentHash{Sha256: policyHash}
	result.DependencyManifest = &pb.DependencyManifest{ArtifactId: id,
		ProducerManifest: &pb.ProducerManifest{Software: "regulagraph-snapshot-source-binder", Build: "v1", SchemaVersion: 1,
			ConfigHash: &pb.ContentHash{Sha256: policyHash}, InputHashes: []*pb.ContentHash{proto.Clone(originalRef.ContentHash).(*pb.ContentHash), proto.Clone(target.ManifestHash).(*pb.ContentHash)}},
		Dependencies: []*pb.Dependency{{DependencyId: originalRef.ArtifactId, Fingerprint: proto.Clone(originalRef.ContentHash).(*pb.ContentHash)}}}
	for _, dependency := range original.DependencyManifest.Dependencies {
		if dependency.DependencyId == originalRef.ArtifactId {
			return nil, errors.New("source artifact has a self dependency")
		}
		result.DependencyManifest.Dependencies = append(result.DependencyManifest.Dependencies, proto.Clone(dependency).(*pb.Dependency))
	}
	result.DependencyManifest.LookupScopeRevisions = proto.Clone(original.DependencyManifest).(*pb.DependencyManifest).LookupScopeRevisions
	if _, err = NewIndexSourceView(result, 1_000_000); err != nil {
		return nil, err
	}
	return result, nil
}
