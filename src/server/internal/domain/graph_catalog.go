// Defines the immutable physical route and write-set description for one graph
// generation. These are local coordinator values built from C01 plans/deltas,
// not another wire schema. Hashing is versioned and length-prefixed; credentials
// are never persisted in the catalog. Storage must authenticate inventory/fence
// before reservation and receipt. Measure catalog/commit latency and RSS under
// configs/benchmark-targets.yaml; no quality/performance acceptance is implied.
package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type GraphBinding struct {
	CorpusID, Generation, PublicationID string
	Fence, Sequence, RegistryRevision   uint64
	BaseSnapshot                        *pb.SnapshotRef
}

func GraphBindingHash(b GraphBinding) (string, error) {
	for _, id := range []string{b.CorpusID, b.Generation, b.PublicationID} {
		if err := ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: b.CorpusID, RecordId: id}, DefaultWireLimits); err != nil {
			return "", err
		}
	}
	if err := ValidateWire(b.BaseSnapshot, DefaultWireLimits); err != nil {
		return "", err
	}
	if b.BaseSnapshot.CorpusId != b.CorpusID || b.Sequence <= b.BaseSnapshot.Sequence || b.Sequence > math.MaxInt64 || b.Fence == 0 || b.Fence > math.MaxInt64 || b.RegistryRevision == 0 || b.RegistryRevision > math.MaxInt64 {
		return "", errors.New("bounded graph target/fence/revision and matching base required")
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(b.BaseSnapshot)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, v := range []string{"graph-binding-v1", b.CorpusID, b.Generation, b.PublicationID, fmt.Sprint(b.Fence), fmt.Sprint(b.Sequence), fmt.Sprint(b.RegistryRevision), string(raw)} {
		fmt.Fprintf(h, "%d:%s", len(v), v)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func ValidateGraphEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Hostname() == "" || u.Port() == "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" ||
		(u.Scheme != "bolt" && u.Scheme != "bolt+s") || (u.Scheme == "bolt" && !net.ParseIP(u.Hostname()).IsLoopback()) {
		return errors.New("direct Bolt URI required; unencrypted transport is loopback-only")
	}
	return nil
}

type GraphCatalogBinding struct {
	Binding                                                        GraphBinding
	Endpoint, Database, BindingHash, OperationsHash, InventoryHash string
	Records, Edges, Operations                                     uint64
	Outputs                                                        []*pb.ArtifactRef
}

func ValidateGraphCatalogBinding(b GraphCatalogBinding) error {
	hash, err := GraphBindingHash(b.Binding)
	if err != nil {
		return err
	}
	if err = ValidateGraphEndpoint(b.Endpoint); err != nil {
		return err
	}
	if hash != b.BindingHash || b.Database == "" || len(b.Database) > 128 || b.Operations == 0 || b.Operations > 256 || len(b.Outputs) != int(b.Operations) || b.Records > math.MaxInt64 || b.Edges > math.MaxInt64-b.Records {
		return errors.New("invalid graph catalog ownership or counts")
	}
	for _, h := range []string{b.OperationsHash, b.InventoryHash} {
		if err = ValidateWire(&pb.ContentHash{Sha256: h}, DefaultWireLimits); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	remaining := uint64(64 << 20)
	metadataRemaining := DefaultWireLimits.MaxBytes
	for _, ref := range b.Outputs {
		if err = ValidateWire(ref, DefaultWireLimits); err != nil {
			return err
		}
		if !graphAssemblyKnownFields(ref.ProtoReflect()) || ref.SchemaVersion != 1 || ref.MediaType != GraphDeltaMediaType || ref.ByteSize == 0 || ref.ByteSize > uint64(DefaultWireLimits.MaxBytes) || ref.ByteSize > remaining || seen[ref.ArtifactId] {
			return errors.New("invalid graph catalog output coverage/budget")
		}
		remaining -= ref.ByteSize
		if size := proto.Size(ref); size > metadataRemaining {
			return errors.New("graph catalog metadata exceeds budget")
		} else {
			metadataRemaining -= size
		}
		seen[ref.ArtifactId] = true
	}
	return nil
}

func (b GraphCatalogBinding) ExpectedBackend() *pb.BackendGeneration {
	n := b.Records + b.Edges
	return &pb.BackendGeneration{Backend: pb.BackendKind_BACKEND_KIND_NEO4J, Generation: b.Binding.Generation,
		OperationsChecksum: &pb.ContentHash{Sha256: b.OperationsHash}, ExpectedCounts: &pb.Counts{Expected: n, Accepted: n}}
}
