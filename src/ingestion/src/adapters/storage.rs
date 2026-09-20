//! Menyimpan dan membaca artefak worker secara immutable serta content-addressed.
//!
//! Peran dalam komponen:
//! Adapter ini mengubah byte hasil batch menjadi descriptor hash/size/storage key yang dapat
//! diteruskan ke coordinator Go. Ia tidak mengubah publication state dan tidak memilih snapshot;
//! Go tetap menjadi pemilik commit serta visibility.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Root storage harus direktori operator-owned. Key dibentuk dari SHA-256, bukan path dari input.
//! Write menolak symlink/reparse point, memeriksa canonical containment, memakai temporary file pada
//! direktori tujuan, optional data sync, lalu atomic rename. Existing object selalu diverifikasi hash
//! dan size sebelum dipakai ulang. Read membatasi ukuran sebelum alokasi dan memverifikasi hash.
//!
//! Benchmark dan gate penerimaan:
//! Ukur throughput byte, p95/p99 write/read, fsync cost, peak RSS, dedup hit, dan cleanup failure.
//! Target configs/benchmark-targets.yaml tetap REQUIRED_UNMEASURED; unit test filesystem kecil
//! tidak membuktikan durability filesystem produksi atau throughput corpus.
//!
//! Status: immutable local artifact store aktif; remote/object storage dan publication belum aktif.

use crate::domain::wire::{self, Limits};
use crate::wire::common;
use protobuf::MessageField;
use sha2::{Digest, Sha256};
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::fs::{self, File, OpenOptions};
use std::io::{BufReader, BufWriter, Read, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

static TEMP_SEQUENCE: AtomicU64 = AtomicU64::new(1);

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ArtifactStoreConfig {
    pub maximum_artifact_bytes: usize,
    pub sync_data: bool,
}

impl Default for ArtifactStoreConfig {
    fn default() -> Self {
        Self {
            maximum_artifact_bytes: 512 * 1024 * 1024,
            sync_data: true,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ArtifactDescriptor {
    pub artifact_id: String,
    pub sha256: String,
    pub storage_key: String,
    pub media_type: String,
    pub byte_size: u64,
    pub schema_version: u32,
}

impl ArtifactDescriptor {
    /// Memproyeksikan descriptor lokal ke kontrak C01 dan memvalidasinya sebelum keluar dari worker.
    pub fn to_wire_ref(&self) -> Result<common::ArtifactRef, ArtifactStoreError> {
        validate_descriptor(self)?;
        let reference = common::ArtifactRef {
            artifact_id: self.artifact_id.clone(),
            content_hash: MessageField::some(common::ContentHash {
                sha256: self.sha256.clone(),
                ..Default::default()
            }),
            storage_key: self.storage_key.clone(),
            media_type: self.media_type.clone(),
            byte_size: self.byte_size,
            schema_version: self.schema_version,
            ..Default::default()
        };
        wire::validate(&reference, Limits::default())
            .map_err(ArtifactStoreError::WireValidation)?;
        Ok(reference)
    }
}

#[derive(Debug)]
pub struct ArtifactStore {
    root: PathBuf,
    config: ArtifactStoreConfig,
}

#[derive(Debug, PartialEq, Eq)]
pub enum ArtifactStoreError {
    InvalidConfig(&'static str),
    InvalidMetadata(&'static str),
    UnsafeStorageKey,
    ArtifactTooLarge {
        actual: u64,
        maximum: usize,
    },
    HashMismatch {
        expected: String,
        actual: String,
    },
    SizeMismatch {
        expected: u64,
        actual: u64,
    },
    WireValidation(String),
    Io {
        operation: &'static str,
        detail: String,
    },
}

impl Display for ArtifactStoreError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidConfig(field) => {
                write!(formatter, "invalid artifact store config: {field}")
            }
            Self::InvalidMetadata(field) => write!(formatter, "invalid artifact metadata: {field}"),
            Self::UnsafeStorageKey => write!(formatter, "unsafe artifact storage key"),
            Self::ArtifactTooLarge { actual, maximum } => {
                write!(formatter, "artifact bytes {actual} exceed limit {maximum}")
            }
            Self::HashMismatch { expected, actual } => {
                write!(
                    formatter,
                    "artifact hash mismatch: expected {expected}, got {actual}"
                )
            }
            Self::SizeMismatch { expected, actual } => {
                write!(
                    formatter,
                    "artifact size mismatch: expected {expected}, got {actual}"
                )
            }
            Self::WireValidation(detail) => {
                write!(formatter, "artifact wire validation failed: {detail}")
            }
            Self::Io { operation, detail } => {
                write!(formatter, "artifact {operation} failed: {detail}")
            }
        }
    }
}

impl Error for ArtifactStoreError {}

impl ArtifactStore {
    pub fn open(root: &Path, config: ArtifactStoreConfig) -> Result<Self, ArtifactStoreError> {
        if config.maximum_artifact_bytes == 0 {
            return Err(ArtifactStoreError::InvalidConfig("maximum_artifact_bytes"));
        }
        fs::create_dir_all(root).map_err(|error| io_error("create root", error))?;
        let root = fs::canonicalize(root).map_err(|error| io_error("canonicalize root", error))?;
        if !root.is_dir() {
            return Err(ArtifactStoreError::InvalidConfig("root"));
        }
        Ok(Self { root, config })
    }

    pub fn put_bytes(
        &self,
        namespace: &str,
        media_type: &str,
        schema_version: u32,
        bytes: &[u8],
    ) -> Result<ArtifactDescriptor, ArtifactStoreError> {
        validate_metadata(namespace, media_type, schema_version)?;
        if bytes.len() > self.config.maximum_artifact_bytes {
            return Err(ArtifactStoreError::ArtifactTooLarge {
                actual: bytes.len() as u64,
                maximum: self.config.maximum_artifact_bytes,
            });
        }
        let sha256 = sha256_bytes(bytes);
        let storage_key = storage_key(&sha256);
        let destination = self.resolve_key(&storage_key)?;
        let parent = destination
            .parent()
            .ok_or(ArtifactStoreError::UnsafeStorageKey)?;
        self.reject_existing_links(parent)?;
        fs::create_dir_all(parent).map_err(|error| io_error("create shard", error))?;
        self.verify_directory(parent)?;

        match fs::symlink_metadata(&destination) {
            Ok(metadata) => {
                if is_link_or_reparse_point(&metadata) || !metadata.is_file() {
                    return Err(ArtifactStoreError::UnsafeStorageKey);
                }
                self.verify_file(&destination, &sha256, bytes.len() as u64)?;
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                let sequence = TEMP_SEQUENCE.fetch_add(1, Ordering::Relaxed);
                let temporary =
                    parent.join(format!(".{sha256}.{}.{}.tmp", std::process::id(), sequence));
                let write_result = self.write_temporary(&temporary, bytes).and_then(|()| {
                    match fs::rename(&temporary, &destination) {
                        Ok(()) => Ok(()),
                        Err(rename_error) => match fs::symlink_metadata(&destination) {
                            Ok(_) => {
                                let _ = fs::remove_file(&temporary);
                                self.verify_file(&destination, &sha256, bytes.len() as u64)
                            }
                            Err(_) => Err(io_error("atomic rename", rename_error)),
                        },
                    }
                });
                if write_result.is_err() {
                    let _ = fs::remove_file(&temporary);
                }
                write_result?;
            }
            Err(error) => return Err(io_error("inspect destination", error)),
        }

        Ok(ArtifactDescriptor {
            artifact_id: format!("artifact:{namespace}:{sha256}"),
            sha256,
            storage_key,
            media_type: media_type.to_owned(),
            byte_size: bytes.len() as u64,
            schema_version,
        })
    }

    pub fn read_verified(
        &self,
        descriptor: &ArtifactDescriptor,
    ) -> Result<Vec<u8>, ArtifactStoreError> {
        validate_descriptor(descriptor)?;
        if descriptor.byte_size > self.config.maximum_artifact_bytes as u64 {
            return Err(ArtifactStoreError::ArtifactTooLarge {
                actual: descriptor.byte_size,
                maximum: self.config.maximum_artifact_bytes,
            });
        }
        let path = self.resolve_key(&descriptor.storage_key)?;
        self.verify_existing_file(&path)?;
        let metadata = fs::metadata(&path).map_err(|error| io_error("metadata", error))?;
        if metadata.len() != descriptor.byte_size {
            return Err(ArtifactStoreError::SizeMismatch {
                expected: descriptor.byte_size,
                actual: metadata.len(),
            });
        }
        let capacity = usize::try_from(descriptor.byte_size).map_err(|_| {
            ArtifactStoreError::ArtifactTooLarge {
                actual: descriptor.byte_size,
                maximum: self.config.maximum_artifact_bytes,
            }
        })?;
        let mut bytes = Vec::with_capacity(capacity.saturating_add(1));
        let reader = BufReader::new(File::open(&path).map_err(|error| io_error("open", error))?);
        let read_limit =
            descriptor
                .byte_size
                .checked_add(1)
                .ok_or(ArtifactStoreError::ArtifactTooLarge {
                    actual: descriptor.byte_size,
                    maximum: self.config.maximum_artifact_bytes,
                })?;
        let mut reader = reader.take(read_limit);
        reader
            .read_to_end(&mut bytes)
            .map_err(|error| io_error("read", error))?;
        if bytes.len() as u64 != descriptor.byte_size {
            return Err(ArtifactStoreError::SizeMismatch {
                expected: descriptor.byte_size,
                actual: bytes.len() as u64,
            });
        }
        let actual = sha256_bytes(&bytes);
        if actual != descriptor.sha256 {
            return Err(ArtifactStoreError::HashMismatch {
                expected: descriptor.sha256.clone(),
                actual,
            });
        }
        Ok(bytes)
    }

    fn write_temporary(&self, path: &Path, bytes: &[u8]) -> Result<(), ArtifactStoreError> {
        let file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(path)
            .map_err(|error| io_error("create temporary", error))?;
        let mut writer = BufWriter::new(file);
        writer
            .write_all(bytes)
            .map_err(|error| io_error("write", error))?;
        writer.flush().map_err(|error| io_error("flush", error))?;
        if self.config.sync_data {
            writer
                .get_ref()
                .sync_data()
                .map_err(|error| io_error("sync", error))?;
        }
        Ok(())
    }

    fn verify_file(
        &self,
        path: &Path,
        expected_hash: &str,
        expected_size: u64,
    ) -> Result<(), ArtifactStoreError> {
        self.verify_existing_file(path)?;
        let metadata = fs::metadata(path).map_err(|error| io_error("metadata", error))?;
        if metadata.len() != expected_size {
            return Err(ArtifactStoreError::SizeMismatch {
                expected: expected_size,
                actual: metadata.len(),
            });
        }
        if metadata.len() > self.config.maximum_artifact_bytes as u64 {
            return Err(ArtifactStoreError::ArtifactTooLarge {
                actual: metadata.len(),
                maximum: self.config.maximum_artifact_bytes,
            });
        }
        let actual = sha256_file(path)?;
        if actual != expected_hash {
            return Err(ArtifactStoreError::HashMismatch {
                expected: expected_hash.to_owned(),
                actual,
            });
        }
        Ok(())
    }

    fn resolve_key(&self, key: &str) -> Result<PathBuf, ArtifactStoreError> {
        validate_storage_key(key)?;
        Ok(key
            .split('/')
            .fold(self.root.clone(), |path, part| path.join(part)))
    }

    fn reject_existing_links(&self, path: &Path) -> Result<(), ArtifactStoreError> {
        let relative = path
            .strip_prefix(&self.root)
            .map_err(|_| ArtifactStoreError::UnsafeStorageKey)?;
        let mut current = self.root.clone();
        for component in relative.components() {
            current.push(component.as_os_str());
            match fs::symlink_metadata(&current) {
                Ok(metadata) => {
                    if is_link_or_reparse_point(&metadata) || !metadata.is_dir() {
                        return Err(ArtifactStoreError::UnsafeStorageKey);
                    }
                    let canonical = fs::canonicalize(&current)
                        .map_err(|error| io_error("canonicalize path", error))?;
                    if !canonical.starts_with(&self.root) {
                        return Err(ArtifactStoreError::UnsafeStorageKey);
                    }
                }
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => break,
                Err(error) => return Err(io_error("inspect path", error)),
            }
        }
        Ok(())
    }

    fn verify_directory(&self, path: &Path) -> Result<(), ArtifactStoreError> {
        self.reject_existing_links(path)?;
        let canonical =
            fs::canonicalize(path).map_err(|error| io_error("canonicalize path", error))?;
        if !canonical.starts_with(&self.root) {
            return Err(ArtifactStoreError::UnsafeStorageKey);
        }
        Ok(())
    }

    fn verify_existing_file(&self, path: &Path) -> Result<(), ArtifactStoreError> {
        let parent = path.parent().ok_or(ArtifactStoreError::UnsafeStorageKey)?;
        self.verify_directory(parent)?;
        let metadata = fs::symlink_metadata(path).map_err(|error| io_error("metadata", error))?;
        if is_link_or_reparse_point(&metadata) || !metadata.is_file() {
            return Err(ArtifactStoreError::UnsafeStorageKey);
        }
        let canonical =
            fs::canonicalize(path).map_err(|error| io_error("canonicalize file", error))?;
        if !canonical.starts_with(&self.root) {
            return Err(ArtifactStoreError::UnsafeStorageKey);
        }
        Ok(())
    }
}

#[cfg(windows)]
fn is_link_or_reparse_point(metadata: &fs::Metadata) -> bool {
    use std::os::windows::fs::MetadataExt;

    const FILE_ATTRIBUTE_REPARSE_POINT: u32 = 0x400;
    metadata.file_type().is_symlink()
        || metadata.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0
}

#[cfg(not(windows))]
fn is_link_or_reparse_point(metadata: &fs::Metadata) -> bool {
    metadata.file_type().is_symlink()
}

fn validate_metadata(
    namespace: &str,
    media_type: &str,
    schema_version: u32,
) -> Result<(), ArtifactStoreError> {
    if !valid_ascii_id(namespace) {
        return Err(ArtifactStoreError::InvalidMetadata("namespace"));
    }
    if media_type.trim().is_empty()
        || media_type.len() > 128
        || !media_type.bytes().all(|byte| (32..=126).contains(&byte))
    {
        return Err(ArtifactStoreError::InvalidMetadata("media_type"));
    }
    if schema_version == 0 {
        return Err(ArtifactStoreError::InvalidMetadata("schema_version"));
    }
    Ok(())
}

fn validate_descriptor(descriptor: &ArtifactDescriptor) -> Result<(), ArtifactStoreError> {
    let mut id_parts = descriptor.artifact_id.split(':');
    let prefix = id_parts.next();
    let namespace = id_parts.next();
    let id_hash = id_parts.next();
    if prefix != Some("artifact") || id_parts.next().is_some() {
        return Err(ArtifactStoreError::InvalidMetadata("artifact_id"));
    }
    let namespace = namespace.ok_or(ArtifactStoreError::InvalidMetadata("artifact_id"))?;
    validate_metadata(namespace, &descriptor.media_type, descriptor.schema_version)?;
    if !is_sha256(&descriptor.sha256) {
        return Err(ArtifactStoreError::InvalidMetadata("sha256"));
    }
    if id_hash != Some(descriptor.sha256.as_str()) {
        return Err(ArtifactStoreError::InvalidMetadata("artifact_id hash"));
    }
    if descriptor.storage_key != storage_key(&descriptor.sha256) {
        return Err(ArtifactStoreError::UnsafeStorageKey);
    }
    Ok(())
}

fn validate_storage_key(key: &str) -> Result<(), ArtifactStoreError> {
    if key.contains(':')
        || key.contains('\\')
        || key.starts_with('/')
        || key
            .split('/')
            .any(|part| part.is_empty() || matches!(part, "." | ".."))
    {
        return Err(ArtifactStoreError::UnsafeStorageKey);
    }
    Ok(())
}

fn storage_key(sha256: &str) -> String {
    format!("sha256/{}/{}/{}.bin", &sha256[..2], &sha256[2..4], sha256)
}

fn sha256_bytes(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}

fn sha256_file(path: &Path) -> Result<String, ArtifactStoreError> {
    let file = File::open(path).map_err(|error| io_error("open hash input", error))?;
    let mut reader = BufReader::new(file);
    let mut hasher = Sha256::new();
    let mut buffer = [0u8; 64 * 1024];
    loop {
        let read = reader
            .read(&mut buffer)
            .map_err(|error| io_error("hash read", error))?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
    }
    Ok(format!("{:x}", hasher.finalize()))
}

fn valid_ascii_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-' | b'.'))
}

fn is_sha256(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
}

fn io_error(operation: &'static str, error: std::io::Error) -> ArtifactStoreError {
    ArtifactStoreError::Io {
        operation,
        detail: error.to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    static TEST_SEQUENCE: AtomicU64 = AtomicU64::new(1);

    struct TestDir(PathBuf);

    impl TestDir {
        fn new() -> Self {
            let sequence = TEST_SEQUENCE.fetch_add(1, Ordering::Relaxed);
            let path = std::env::temp_dir().join(format!(
                "regulagraph-artifact-test-{}-{sequence}",
                std::process::id()
            ));
            fs::create_dir_all(&path).expect("test root is created");
            Self(path)
        }
    }

    impl Drop for TestDir {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn store(root: &Path) -> ArtifactStore {
        ArtifactStore::open(
            root,
            ArtifactStoreConfig {
                maximum_artifact_bytes: 1024 * 1024,
                sync_data: false,
            },
        )
        .expect("artifact store opens")
    }

    fn artifact_path(store: &ArtifactStore, descriptor: &ArtifactDescriptor) -> PathBuf {
        descriptor
            .storage_key
            .split('/')
            .fold(store.root.clone(), |path, part| path.join(part))
    }

    fn list_files(path: &Path, output: &mut Vec<PathBuf>) {
        for entry in fs::read_dir(path).expect("directory is readable") {
            let entry = entry.expect("directory entry is readable");
            let path = entry.path();
            if path.is_dir() {
                list_files(&path, output);
            } else {
                output.push(path);
            }
        }
    }

    #[test]
    fn writes_reads_and_deduplicates_content_addressed_artifacts() {
        let root = TestDir::new();
        let store = store(&root.0);
        let bytes = b"document batch bytes";

        let first = store
            .put_bytes("document-batch", "application/x-protobuf", 1, bytes)
            .expect("first write succeeds");
        let second = store
            .put_bytes("document-batch", "application/x-protobuf", 1, bytes)
            .expect("deduplicated write succeeds");

        assert_eq!(first, second);
        assert_eq!(store.read_verified(&first).unwrap(), bytes);
        assert_eq!(first.byte_size, bytes.len() as u64);
        assert_eq!(first.storage_key, storage_key(&first.sha256));
        let wire = first.to_wire_ref().expect("descriptor projects to C01");
        assert_eq!(wire.artifact_id, first.artifact_id);
        assert_eq!(wire.content_hash.sha256, first.sha256);
        assert_eq!(wire.storage_key, first.storage_key);
        assert_eq!(wire.media_type, first.media_type);
        assert_eq!(wire.byte_size, first.byte_size);
        assert_eq!(wire.schema_version, first.schema_version);
        let mut files = Vec::new();
        list_files(&store.root, &mut files);
        assert_eq!(files, [artifact_path(&store, &first)]);
    }

    #[test]
    fn concurrent_identical_writes_leave_one_verified_object() {
        let root = TestDir::new();
        let store = Arc::new(store(&root.0));
        let mut handles = Vec::new();
        for _ in 0..8 {
            let store = Arc::clone(&store);
            handles.push(std::thread::spawn(move || {
                store.put_bytes(
                    "document-batch",
                    "application/x-protobuf",
                    1,
                    b"same concurrent payload",
                )
            }));
        }
        let descriptors: Vec<_> = handles
            .into_iter()
            .map(|handle| {
                handle
                    .join()
                    .expect("writer thread joins")
                    .expect("write succeeds")
            })
            .collect();

        assert!(descriptors.windows(2).all(|pair| pair[0] == pair[1]));
        let mut files = Vec::new();
        list_files(&store.root, &mut files);
        assert_eq!(files.len(), 1);
        assert_eq!(
            store.read_verified(&descriptors[0]).unwrap(),
            b"same concurrent payload"
        );
    }

    #[test]
    fn rejects_tampered_truncated_and_unsafe_descriptors() {
        let root = TestDir::new();
        let store = store(&root.0);
        let descriptor = store
            .put_bytes("batch", "application/x-protobuf", 1, b"original")
            .expect("fixture write succeeds");
        let path = artifact_path(&store, &descriptor);

        fs::write(&path, b"tampered").expect("same-size tamper succeeds");
        assert!(matches!(
            store.read_verified(&descriptor),
            Err(ArtifactStoreError::HashMismatch { .. })
        ));
        fs::write(&path, b"tiny").expect("truncation succeeds");
        assert_eq!(
            store.read_verified(&descriptor).unwrap_err(),
            ArtifactStoreError::SizeMismatch {
                expected: 8,
                actual: 4
            }
        );

        let mut unsafe_descriptor = descriptor.clone();
        unsafe_descriptor.storage_key = "../escape.bin".to_owned();
        assert_eq!(
            store.read_verified(&unsafe_descriptor).unwrap_err(),
            ArtifactStoreError::UnsafeStorageKey
        );
        let mut wrong_id = descriptor;
        wrong_id.artifact_id = format!("artifact:batch:{}", "0".repeat(64));
        assert_eq!(
            store.read_verified(&wrong_id).unwrap_err(),
            ArtifactStoreError::InvalidMetadata("artifact_id hash")
        );
        assert_eq!(
            wrong_id.to_wire_ref().unwrap_err(),
            ArtifactStoreError::InvalidMetadata("artifact_id hash")
        );
    }

    #[test]
    fn enforces_size_and_metadata_before_writing() {
        let root = TestDir::new();
        let store = ArtifactStore::open(
            &root.0,
            ArtifactStoreConfig {
                maximum_artifact_bytes: 4,
                sync_data: false,
            },
        )
        .expect("limited store opens");

        assert_eq!(
            store
                .put_bytes("batch", "application/octet-stream", 1, b"12345")
                .unwrap_err(),
            ArtifactStoreError::ArtifactTooLarge {
                actual: 5,
                maximum: 4
            }
        );
        assert_eq!(
            store
                .put_bytes("bad/name", "application/octet-stream", 1, b"1")
                .unwrap_err(),
            ArtifactStoreError::InvalidMetadata("namespace")
        );
        assert_eq!(
            store.put_bytes("batch", "", 1, b"1").unwrap_err(),
            ArtifactStoreError::InvalidMetadata("media_type")
        );
        let mut files = Vec::new();
        list_files(&store.root, &mut files);
        assert!(files.is_empty());
    }

    #[cfg(windows)]
    #[test]
    fn rejects_junction_shards_before_writing_outside_root() {
        use std::process::Command;

        let root = TestDir::new();
        let outside = TestDir::new();
        let store = store(&root.0);
        let junction = root.0.join("sha256");
        let status = Command::new("cmd")
            .args(["/c", "mklink", "/J"])
            .arg(&junction)
            .arg(&outside.0)
            .status()
            .expect("junction command runs");
        assert!(status.success(), "junction fixture is created");

        let result = store.put_bytes("batch", "application/octet-stream", 1, b"outside");
        fs::remove_dir(&junction).expect("junction is removed without traversing its target");

        assert_eq!(result.unwrap_err(), ArtifactStoreError::UnsafeStorageKey);
        assert_eq!(
            fs::read_dir(&outside.0).unwrap().count(),
            0,
            "rejected write must not create shards in the junction target"
        );
    }
}
