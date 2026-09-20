// Menguji regresi FileStore S01 dengan direktori sementara.
// Peran: membuktikan immutable reuse, verified read, cancellation, mismatch, dan path confinement.
// Kontrak: setiap test mengisolasi side effect dan membandingkan error sentinel yang dipakai caller.
// Benchmark: suite ini hanya correctness; bytes/detik, peak buffer, dan p95/p99 harus diukur
// terpisah terhadap configs/benchmark-targets.yaml.
// Target numerik required: status REQUIRED_UNMEASURED.
// Status: unit regression FileStore aktif; symlink race dan crash-process injection tetap
// menjadi pemeriksaan integrasi platform.
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestFileStorePutReuseAndVerifiedOpen(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	content := []byte("immutable regulation bytes")
	ref := artifactRef("sha256/ab/artifact.bin", content)
	reused, err := store.Put(context.Background(), ref, bytes.NewReader(content))
	if err != nil || reused {
		t.Fatalf("first put reused=%v err=%v", reused, err)
	}
	reused, err = store.Put(context.Background(), ref, bytes.NewReader([]byte("ignored because existing content is verified")))
	if err != nil || !reused {
		t.Fatalf("idempotent put reused=%v err=%v", reused, err)
	}
	file, err := store.OpenVerified(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	defer file.Close()
	actual, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("read=%q err=%v", actual, err)
	}
	bounded, err := store.ReadVerified(context.Background(), ref, uint64(len(content)))
	if err != nil || !bytes.Equal(bounded, content) {
		t.Fatalf("bounded read=%q err=%v", bounded, err)
	}
	if _, err = store.ReadVerified(context.Background(), ref, uint64(len(content)-1)); err == nil {
		t.Fatal("bounded read accepted an artifact above its allocation limit")
	}
	if !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("deterministic bound violation was marked retryable: %v", err)
	}
	if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(ref.StorageKey)), bytes.Repeat([]byte("x"), 4<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadVerified(context.Background(), ref, uint64(len(content))); !errors.Is(err, ErrArtifactMismatch) ||
		!errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("oversized backing file was not rejected before bounded allocation: %v", err)
	}
}

func TestFileStoreRejectsTraversalMismatchAndCancellation(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	content := []byte("expected")
	badPath := artifactRef("../escape.bin", content)
	if _, err = store.Put(context.Background(), badPath, bytes.NewReader(content)); !errors.Is(err, ErrInvalidStorageKey) {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
	badHash := artifactRef("objects/bad.bin", content)
	badHash.ContentHash.Sha256 = hex.EncodeToString(make([]byte, sha256.Size))
	if _, err = store.Put(context.Background(), badHash, bytes.NewReader(content)); !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = store.Put(cancelled, artifactRef("objects/cancelled.bin", content), bytes.NewReader(content)); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestFileStoreRejectsLeafSymlinkAndPublishesOnceConcurrently(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "objects"), 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.bin")
	if err = os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("leaf symlink", func(t *testing.T) {
		link := filepath.Join(root, "objects", "link.bin")
		if linkErr := os.Symlink(outside, link); linkErr != nil {
			t.Skipf("platform did not permit symlink fixture: %v", linkErr)
		}
		if _, putErr := store.Put(context.Background(), artifactRef("objects/link.bin", []byte("outside")), bytes.NewReader([]byte("outside"))); !errors.Is(putErr, ErrSymlinkPath) {
			t.Fatalf("expected leaf symlink rejection, got %v", putErr)
		}
		if _, openErr := store.OpenVerified(context.Background(), artifactRef("objects/link.bin", []byte("outside"))); !errors.Is(openErr, ErrSymlinkPath) {
			t.Fatalf("expected symlink open rejection, got %v", openErr)
		}
	})
	content := bytes.Repeat([]byte("concurrent immutable bytes"), 1024)
	ref := artifactRef("objects/concurrent.bin", content)
	var group sync.WaitGroup
	results := make(chan bool, 8)
	errorsSeen := make(chan error, 8)
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			reused, putErr := store.Put(context.Background(), ref, bytes.NewReader(content))
			results <- reused
			errorsSeen <- putErr
		}()
	}
	group.Wait()
	close(results)
	close(errorsSeen)
	for putErr := range errorsSeen {
		if putErr != nil {
			t.Fatalf("concurrent put failed: %v", putErr)
		}
	}
	created := 0
	for reused := range results {
		if !reused {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("expected one publisher, got %d", created)
	}
	if err = store.Close(); err != nil {
		t.Fatalf("close concurrent file store: %v", err)
	}
}

func artifactRef(key string, content []byte) *pb.ArtifactRef {
	digest := sha256.Sum256(content)
	return &pb.ArtifactRef{ArtifactId: "artifact-1", StorageKey: key, MediaType: "application/octet-stream",
		ByteSize: uint64(len(content)), SchemaVersion: 1,
		ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])}}
}
