// Package domain defines immutable physical index bindings and deterministic
// point identities. PostgreSQL must collision-check the UUID against the full
// digest and record identity; a truncated hash alone is not an allocation proof.
// Bindings are internal coordinator inputs, never accepted from a search request.
// Profile batch allocation/readback RSS and p95 under benchmark-targets.yaml;
// required production performance remains unmeasured.
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type IndexCatalogBinding struct {
	PublicationID string
	Fence         uint64
	Endpoint      string
	Collection    string
	Generation    *pb.IndexGeneration
}

type IndexPointAssignment struct {
	RecordID       string
	PointID        string
	IdentityDigest string
}

var indexCollectionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidateIndexCatalogBinding(b IndexCatalogBinding) error {
	if err := ValidatePairedIndexGeneration(b.Generation); err != nil {
		return err
	}
	if err := ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: b.Generation.Meta.CorpusId, RecordId: b.PublicationID}, DefaultWireLimits); err != nil {
		return err
	}
	u, err := url.Parse(b.Endpoint)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
		!indexCollectionPattern.MatchString(b.Collection) || b.Fence == 0 || b.Fence > uint64(1<<63-1) || b.Generation.DenseManifest.Task != pb.ModelTask_MODEL_TASK_EMBED ||
		b.Generation.DenseManifest.GetDimensions() == 0 || b.Generation.DenseManifest.GetDimensions() > 16384 {
		return errors.New("invalid index physical binding")
	}
	return nil
}

// IndexPointIdentity v1 hashes domain + length-prefixed UTF-8 corpus/generation/
// record IDs (u64 big endian). UUID uses the first 16 digest bytes with RFC9562
// version 8/variant bits. The complete digest is retained for collision checks.
func IndexPointIdentity(corpus, generation, record string) (IndexPointAssignment, error) {
	for _, id := range []string{generation, record} {
		if err := ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: id}, DefaultWireLimits); err != nil {
			return IndexPointAssignment{}, err
		}
	}
	h := sha256.New()
	h.Write([]byte("regulagraph-index-point-v1\x00"))
	for _, value := range []string{corpus, generation, record} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		h.Write(size[:])
		h.Write([]byte(value))
	}
	sum := h.Sum(nil)
	digest := hex.EncodeToString(sum)
	id := append([]byte(nil), sum[:16]...)
	id[6] = (id[6] & 15) | 128
	id[8] = (id[8] & 63) | 128
	encoded := hex.EncodeToString(id)
	return IndexPointAssignment{RecordID: record, IdentityDigest: digest, PointID: fmt.Sprintf("%s-%s-%s-%s-%s", encoded[:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:])}, nil
}
