// Loads exact-byte-pinned RESOLVE producer and output schema once at startup. Producer uses
// the existing ProtoJSON wire contract; no parallel model manifest is introduced. Reads are
// bounded to 1 MiB and imports do no I/O. Measure startup cost separately from steady-state
// model/queue p95/p99; targets remain REQUIRED_UNMEASURED in benchmark-targets.yaml.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"google.golang.org/protobuf/encoding/protojson"
	"io"
	"os"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func readPinnedResolutionFile(path, hash string) ([]byte, error) {
	if path == "" || !lowerSHA256.MatchString(hash) {
		return nil, errors.New("resolution path and lowercase SHA-256 required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, errors.New("resolution configuration must be a regular file within 1 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	if len(raw) > 1<<20 || hex.EncodeToString(digest[:]) != hash {
		return nil, errors.New("resolution bytes differ from pin or exceed budget")
	}
	return raw, nil
}

func LoadResolutionProducer(path, hash string) (*pb.ProducerManifest, error) {
	raw, err := readPinnedResolutionFile(path, hash)
	if err != nil {
		return nil, err
	}
	manifest := new(pb.ProducerManifest)
	if err = protojson.Unmarshal(raw, manifest); err != nil {
		return nil, err
	}
	if err = domain.ValidateWire(manifest, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if len(manifest.Models) != 1 || manifest.Models[0].Task != pb.ModelTask_MODEL_TASK_RESOLVE {
		return nil, errors.New("producer requires one RESOLVE model")
	}
	return manifest, nil
}

func LoadResolutionOutputSchema(path, hash string) (*pb.ArtifactRef, error) {
	raw, err := readPinnedResolutionFile(path, hash)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, errors.New("resolution schema must contain valid JSON")
	}
	return &pb.ArtifactRef{ArtifactId: "schema:resolve:" + hash, ContentHash: &pb.ContentHash{Sha256: hash},
		StorageKey: "schemas/" + hash + ".json", MediaType: "application/schema+json", ByteSize: uint64(len(raw)), SchemaVersion: 1}, nil
}
