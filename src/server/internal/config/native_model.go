// Loads the native bundle's C01 model.pbjson with exact bytes pinned by configuration.
// This startup-only read is bounded to 64 KiB and validates the expected task;
// runtime capabilities must independently confirm that the same model is loaded.
// Importing this package performs no I/O. Startup checks are not model-quality
// or latency acceptance under configs/benchmark-targets.yaml.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func LoadNativeModel(path, hash string, task pb.ModelTask) (*pb.ModelManifest, error) {
	if path == "" || !lowerSHA256.MatchString(hash) || (task != pb.ModelTask_MODEL_TASK_EMBED && task != pb.ModelTask_MODEL_TASK_RERANK) {
		return nil, errors.New("pinned native manifest path/hash/task required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<10 {
		return nil, errors.New("native manifest must be a regular file within 64 KiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	if len(raw) > 64<<10 || hex.EncodeToString(digest[:]) != hash {
		return nil, errors.New("native manifest hash/size mismatch")
	}
	model := new(pb.ModelManifest)
	if err = protojson.Unmarshal(raw, model); err != nil {
		return nil, err
	}
	if err = domain.ValidateWire(model, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if model.Task != task {
		return nil, errors.New("native manifest task mismatch")
	}
	return model, nil
}
