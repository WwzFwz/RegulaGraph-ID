// Menyimpan dan membaca artefak sumber lokal berdasarkan referensi serta hash.
//
// Peran dalam komponen:
// Menjadi implementasi kontrak penyimpanan sumber untuk ingestion dan provenance. Bytes
// asli dipertahankan secara immutable agar parsing dapat diulang dan citation dapat diaudit.
//
// Kontrak integrasi dan perhatian implementasi:
// ArtifactRef memakai storage key relatif. Tolak traversal, absolute path, dan komponen
// symlink. os.Root mengurung seluruh operasi saat komponen path berubah secara concurrent.
// Put melakukan streaming ke temporary file satu direktori, memverifikasi byte count dan
// SHA-256, fsync, lalu atomic no-replace publish dengan hard link. Konten existing hanya
// dipakai ulang setelah diverifikasi; jangan mengeksekusi konten dokumen sebagai kode.
//
// Benchmark dan gate penerimaan:
// [SOURCE] Ukur keberhasilan akuisisi, bytes/detik, retry, dan waktu p50/p95 per sumber.
// Gate: konten memiliki hash dan asal yang terlacak; unduhan parsial tidak diterbitkan
// sebagai sumber lengkap.
// [STORAGE] Ukur latency p50/p95/p99, throughput batch, peak buffer/RSS, retry, dan error
// rate. Gate: cancellation terlapor, resource dilepas, tulis idempotent, serta corrupt hash,
// short write, traversal, symlink escape, dan crash sebelum publish tidak menerbitkan data.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: implementasi S01 aktif untuk scoped key, streaming hash-verified read, dan atomic
// immutable write. Directory chain di-fsync pada platform yang mendukungnya; Windows tidak
// mengizinkan directory-handle sync melalui API ini sehingga power-loss durability entry
// ditangani sebagai batas deployment/backup O01. Retention cleanup menunggu U01/O01.
// Bukti verifikasi mengikuti doc/verification.md; build/unit test bukan bukti target performa.
package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

var (
	ErrInvalidStorageKey = errors.Join(errors.New("invalid artifact storage key"), domain.ErrPersistentIntegrity)
	ErrArtifactMismatch  = errors.Join(errors.New("artifact content does not match its reference"), domain.ErrPersistentIntegrity)
	ErrSymlinkPath       = errors.Join(errors.New("artifact path contains a symbolic link"), domain.ErrPersistentIntegrity)
)

type FileStore struct {
	root *os.Root
}

func NewFileStore(root string) (*FileStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root: %w", err)
	}
	if err = os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("inspect artifact root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, ErrSymlinkPath
	}
	rootHandle, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open confined artifact root: %w", err)
	}
	return &FileStore{root: rootHandle}, nil
}

func (s *FileStore) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

func (s *FileStore) Put(ctx context.Context, ref *pb.ArtifactRef, source io.Reader) (bool, error) {
	if err := validateRef(ref); err != nil {
		return false, err
	}
	target, err := s.resolve(ref.StorageKey)
	if err != nil {
		return false, err
	}
	parent := filepath.Dir(target)
	if err = s.root.MkdirAll(parent, 0o750); err != nil {
		return false, fmt.Errorf("create artifact directory: %w", err)
	}
	if err = s.rejectSymlinks(parent); err != nil {
		return false, err
	}
	if info, statErr := s.root.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, ErrSymlinkPath
		}
		if err = verifyFile(s.root, target, ref); err != nil {
			return false, err
		}
		return true, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("inspect artifact destination: %w", statErr)
	}
	var temp *os.File
	var tempName string
	for attempt := 0; attempt < 8; attempt++ {
		random := make([]byte, 16)
		if _, err = rand.Read(random); err != nil {
			return false, fmt.Errorf("generate artifact temporary name: %w", err)
		}
		tempName = filepath.Join(parent, ".regulagraph-artifact-"+hex.EncodeToString(random))
		temp, err = s.root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return false, fmt.Errorf("create artifact temporary file: %w", err)
		}
	}
	if temp == nil {
		return false, errors.New("could not allocate unique artifact temporary file")
	}
	defer s.root.Remove(tempName)
	hasher := sha256.New()
	written, copyErr := io.CopyBuffer(io.MultiWriter(temp, hasher), &contextReader{ctx: ctx, reader: source}, make([]byte, 128*1024))
	if copyErr == nil {
		copyErr = temp.Sync()
	}
	closeErr := temp.Close()
	if copyErr != nil {
		return false, fmt.Errorf("write artifact: %w", copyErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close artifact: %w", closeErr)
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if uint64(written) != ref.ByteSize || actualHash != ref.ContentHash.Sha256 {
		return false, fmt.Errorf("expected bytes=%d sha256=%s, got bytes=%d sha256=%s: %w",
			ref.ByteSize, ref.ContentHash.Sha256, written, actualHash, ErrArtifactMismatch)
	}
	if err = s.root.Chmod(tempName, 0o640); err != nil {
		return false, fmt.Errorf("set artifact permissions: %w", err)
	}
	// A hard-link publish is atomic and never replaces an existing destination. The
	// temporary file lives on the same filesystem, then is removed by the deferred cleanup.
	if err = s.root.Link(tempName, target); err != nil {
		if info, statErr := s.root.Lstat(target); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return false, ErrSymlinkPath
			}
			if verifyErr := verifyFile(s.root, target, ref); verifyErr == nil {
				return true, nil
			}
		}
		return false, fmt.Errorf("publish artifact atomically: %w", err)
	}
	if err = syncDirectoryChain(s.root, parent); err != nil {
		return false, fmt.Errorf("sync artifact directory chain: %w", err)
	}
	return false, nil
}

func (s *FileStore) OpenVerified(ctx context.Context, ref *pb.ArtifactRef) (*os.File, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	target, err := s.resolve(ref.StorageKey)
	if err != nil {
		return nil, err
	}
	if err = s.rejectSymlinks(filepath.Dir(target)); err != nil {
		return nil, err
	}
	info, err := s.root.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.Join(fmt.Errorf("inspect artifact: %w", err), domain.ErrPersistentIntegrity)
		}
		return nil, fmt.Errorf("inspect artifact: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, ErrSymlinkPath
	}
	if info.Size() < 0 || uint64(info.Size()) != ref.ByteSize {
		return nil, ErrArtifactMismatch
	}
	file, err := s.root.Open(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.Join(fmt.Errorf("open artifact: %w", err), domain.ErrPersistentIntegrity)
		}
		return nil, fmt.Errorf("open artifact: %w", err)
	}
	hasher := sha256.New()
	read, verifyErr := io.CopyBuffer(hasher, &contextReader{ctx: ctx, reader: io.LimitReader(file, info.Size()+1)}, make([]byte, 128*1024))
	if verifyErr == nil && (uint64(read) != ref.ByteSize || hex.EncodeToString(hasher.Sum(nil)) != ref.ContentHash.Sha256) {
		verifyErr = ErrArtifactMismatch
	}
	if verifyErr != nil {
		file.Close()
		return nil, fmt.Errorf("verify artifact: %w", verifyErr)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("rewind artifact: %w", err)
	}
	return file, nil
}

// ReadVerified loads one bounded immutable artifact after the same confinement, size, and hash
// checks as OpenVerified. Callers choose the bound before allocation; large streaming consumers
// should continue using OpenVerified to avoid retaining the full payload in memory.
func (s *FileStore) ReadVerified(ctx context.Context, ref *pb.ArtifactRef, maximumBytes uint64) ([]byte, error) {
	if maximumBytes == 0 || ref == nil || ref.ByteSize > maximumBytes {
		return nil, errors.Join(errors.New("artifact exceeds configured read bound"), domain.ErrPersistentIntegrity)
	}
	file, err := s.OpenVerified(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	capacity, err := sizeToInt(ref.ByteSize)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, 0, capacity)
	limited := io.LimitReader(file, int64(capacity)+1)
	buffer, err = io.ReadAll(&contextReader{ctx: ctx, reader: limited})
	if err != nil {
		return nil, fmt.Errorf("read verified artifact: %w", err)
	}
	digest := sha256.Sum256(buffer)
	if uint64(len(buffer)) != ref.ByteSize || hex.EncodeToString(digest[:]) != ref.ContentHash.Sha256 {
		return nil, ErrArtifactMismatch
	}
	return buffer, nil
}

func sizeToInt(size uint64) (int, error) {
	if size > math.MaxInt {
		return 0, errors.Join(errors.New("artifact size exceeds addressable memory"), domain.ErrPersistentIntegrity)
	}
	converted := int(size)
	if converted < 0 || uint64(converted) != size {
		return 0, errors.Join(errors.New("artifact size exceeds addressable memory"), domain.ErrPersistentIntegrity)
	}
	return converted, nil
}

func (s *FileStore) resolve(key string) (string, error) {
	if key == "" || strings.Contains(key, `\`) || path.IsAbs(key) || path.Clean(key) != key || key == "." || strings.HasPrefix(key, "../") {
		return "", ErrInvalidStorageKey
	}
	return filepath.FromSlash(key), nil
}

func (s *FileStore) rejectSymlinks(directory string) error {
	current := ""
	if directory == "." {
		return nil
	}
	for _, component := range strings.Split(directory, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := s.root.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return errors.Join(fmt.Errorf("inspect artifact directory: %w", statErr), domain.ErrPersistentIntegrity)
			}
			return fmt.Errorf("inspect artifact directory: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return ErrSymlinkPath
		}
	}
	return nil
}

func validateRef(ref *pb.ArtifactRef) error {
	if ref == nil || ref.ContentHash == nil || ref.ArtifactId == "" || ref.MediaType == "" ||
		ref.SchemaVersion == 0 || !sha256Pattern.MatchString(ref.ContentHash.Sha256) {
		return errors.Join(errors.New("complete ArtifactRef with SHA-256 is required"), domain.ErrPersistentIntegrity)
	}
	return nil
}

var sha256Pattern = func() interface{ MatchString(string) bool } {
	return hexPattern{}
}()

type hexPattern struct{}

func (hexPattern) MatchString(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func verifyFile(root *os.Root, filename string, ref *pb.ArtifactRef) error {
	file, err := root.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	hasher := sha256.New()
	read, err := io.CopyBuffer(hasher, file, make([]byte, 128*1024))
	if err != nil {
		return err
	}
	if uint64(read) != ref.ByteSize || hex.EncodeToString(hasher.Sum(nil)) != ref.ContentHash.Sha256 {
		return ErrArtifactMismatch
	}
	return nil
}

func syncDirectoryChain(root *os.Root, directory string) error {
	for current := directory; ; current = filepath.Dir(current) {
		file, err := root.Open(current)
		if err != nil {
			return err
		}
		err = file.Sync()
		closeErr := file.Close()
		if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
			// Windows does not permit flushing the directory handle opened by os.Root.
			// File bytes are flushed before link publication; power-loss durability of
			// directory entries remains a documented platform boundary for deployment.
			err = nil
		}
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if current == "." {
			return nil
		}
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
