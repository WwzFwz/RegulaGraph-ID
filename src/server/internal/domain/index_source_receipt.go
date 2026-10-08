// Defines the local storage receipt for an initial snapshot source envelope.
// Existing C01 references carry all wire identities; this record binds original
// CHUNK authority to derived immutable bytes under one publication fence.
// Validation is structural; the coordinator must prove the envelope transform
// and storage must check the original checkpoint and reserved snapshot.
package domain

import (
	"errors"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type IndexSourceBinding struct {
	PublicationID   string
	Fence           uint64
	SourceJobID     string
	Snapshot        *pb.SnapshotRef
	AuthScope       string
	Original, Bound *pb.ArtifactRef
}

func ValidateIndexSourceBinding(binding IndexSourceBinding) error {
	for _, message := range []proto.Message{binding.Snapshot, binding.Original, binding.Bound} {
		if err := ValidateWire(message, DefaultWireLimits); err != nil {
			return err
		}
	}
	for _, id := range []string{binding.PublicationID, binding.SourceJobID, binding.AuthScope} {
		if err := ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: binding.Snapshot.CorpusId, RecordId: id}, DefaultWireLimits); err != nil {
			return err
		}
	}
	const media = "application/x-protobuf; message=regulagraph.v1.DocumentBatch"
	if binding.Fence == 0 || binding.Fence > 1<<63-1 || binding.Original.ArtifactId == binding.Bound.ArtifactId || proto.Equal(binding.Original.ContentHash, binding.Bound.ContentHash) ||
		binding.Original.MediaType != media || binding.Bound.MediaType != media || binding.Original.ByteSize == 0 || binding.Bound.ByteSize == 0 || binding.Original.ByteSize > 16<<20 || binding.Bound.ByteSize > 16<<20 {
		return errors.New("invalid initial snapshot source binding")
	}
	return nil
}
