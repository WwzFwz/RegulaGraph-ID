//! Loads one pinned Hugging Face tokenizer once and exposes exact model token counts to CHUNK.
//!
//! The tokenizer JSON is deployment input, bounded and hashed before parsing. Its SHA-256 becomes
//! the tokenizer identity carried by every chunk and by the chunker fingerprint. Loading happens at
//! worker startup; request handling performs only in-memory encoding. Token-count parity with the
//! selected embedding/reranker models and CHUNK throughput/RSS remain required benchmark gates.

use super::builder::TokenCounter;
use sha2::{Digest, Sha256};
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::fs::{self, File};
use std::io::Read;
use std::path::Path;
use tokenizers::Tokenizer;

const MAXIMUM_TOKENIZER_BYTES: u64 = 128 * 1024 * 1024;

pub struct HuggingFaceTokenizer {
    tokenizer: Tokenizer,
    id: String,
}

#[derive(Debug)]
pub enum TokenizerLoadError {
    Read(std::io::Error),
    TooLarge(u64),
    InvalidExpectedHash,
    HashMismatch { expected: String, actual: String },
    Decode(String),
}

impl Display for TokenizerLoadError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Read(error) => write!(formatter, "read tokenizer JSON: {error}"),
            Self::TooLarge(size) => write!(
                formatter,
                "tokenizer JSON size {size} exceeds {MAXIMUM_TOKENIZER_BYTES} bytes"
            ),
            Self::InvalidExpectedHash => {
                write!(
                    formatter,
                    "expected tokenizer SHA-256 must be 64 hexadecimal characters"
                )
            }
            Self::HashMismatch { expected, actual } => write!(
                formatter,
                "tokenizer JSON SHA-256 {actual} differs from deployment pin {expected}"
            ),
            Self::Decode(error) => write!(formatter, "decode tokenizer JSON: {error}"),
        }
    }
}

impl Error for TokenizerLoadError {}

impl HuggingFaceTokenizer {
    pub fn from_file(
        path: impl AsRef<Path>,
        expected_sha256: &str,
    ) -> Result<Self, TokenizerLoadError> {
        if expected_sha256.len() != 64
            || !expected_sha256.bytes().all(|byte| byte.is_ascii_hexdigit())
        {
            return Err(TokenizerLoadError::InvalidExpectedHash);
        }
        let expected_sha256 = expected_sha256.to_ascii_lowercase();
        let path = path.as_ref();
        let metadata = fs::metadata(path).map_err(TokenizerLoadError::Read)?;
        if !metadata.is_file() {
            return Err(TokenizerLoadError::Read(std::io::Error::new(
                std::io::ErrorKind::InvalidInput,
                "tokenizer path is not a regular file",
            )));
        }
        if metadata.len() == 0 || metadata.len() > MAXIMUM_TOKENIZER_BYTES {
            return Err(TokenizerLoadError::TooLarge(metadata.len()));
        }
        let file = File::open(path).map_err(TokenizerLoadError::Read)?;
        let mut bytes = Vec::with_capacity(metadata.len() as usize);
        file.take(MAXIMUM_TOKENIZER_BYTES + 1)
            .read_to_end(&mut bytes)
            .map_err(TokenizerLoadError::Read)?;
        if bytes.is_empty() || bytes.len() as u64 > MAXIMUM_TOKENIZER_BYTES {
            return Err(TokenizerLoadError::TooLarge(bytes.len() as u64));
        }
        let hash = format!("{:x}", Sha256::digest(&bytes));
        if hash != expected_sha256 {
            return Err(TokenizerLoadError::HashMismatch {
                expected: expected_sha256,
                actual: hash,
            });
        }
        let mut tokenizer = Tokenizer::from_bytes(&bytes)
            .map_err(|error| TokenizerLoadError::Decode(error.to_string()))?;
        tokenizer
            .with_truncation(None)
            .map_err(|error| TokenizerLoadError::Decode(error.to_string()))?;
        tokenizer.with_padding(None);
        Ok(Self {
            tokenizer,
            id: format!("hf-json:{expected_sha256}"),
        })
    }
}

impl TokenCounter for HuggingFaceTokenizer {
    fn tokenizer_id(&self) -> &str {
        &self.id
    }

    fn count_tokens(&self, text: &str) -> Result<u32, String> {
        let encoding = self
            .tokenizer
            .encode(text, true)
            .map_err(|error| error.to_string())?;
        u32::try_from(encoding.len()).map_err(|error| error.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ahash::AHashMap;
    use std::sync::atomic::{AtomicU64, Ordering};
    use tokenizers::models::wordlevel::WordLevel;
    use tokenizers::pre_tokenizers::whitespace::Whitespace;
    use tokenizers::utils::truncation::TruncationParams;

    static TEST_SEQUENCE: AtomicU64 = AtomicU64::new(1);

    #[test]
    fn loads_hash_pinned_json_and_counts_with_model_postprocessing() {
        let sequence = TEST_SEQUENCE.fetch_add(1, Ordering::Relaxed);
        let path = std::env::temp_dir().join(format!(
            "regulagraph-tokenizer-test-{}-{sequence}.json",
            std::process::id()
        ));
        let vocab = AHashMap::from([
            ("[UNK]".to_owned(), 0),
            ("Pasal".to_owned(), 1),
            ("satu".to_owned(), 2),
        ]);
        let model = WordLevel::builder()
            .vocab(vocab)
            .unk_token("[UNK]".to_owned())
            .build()
            .unwrap();
        let mut fixture = Tokenizer::new(model);
        fixture.with_pre_tokenizer(Some(Whitespace));
        fixture
            .with_truncation(Some(TruncationParams {
                max_length: 1,
                ..Default::default()
            }))
            .unwrap();
        fixture.save(&path, false).unwrap();

        let bytes = fs::read(&path).unwrap();
        let expected = format!("{:x}", Sha256::digest(&bytes));
        let loaded = HuggingFaceTokenizer::from_file(&path, &expected).unwrap();
        assert!(loaded.tokenizer_id().starts_with("hf-json:"));
        assert_eq!(loaded.tokenizer_id().len(), 72);
        assert_eq!(loaded.count_tokens("Pasal satu").unwrap(), 2);
        assert!(loaded.tokenizer.get_truncation().is_none());
        assert!(loaded.tokenizer.get_padding().is_none());
        assert!(matches!(
            HuggingFaceTokenizer::from_file(&path, &"0".repeat(64)),
            Err(TokenizerLoadError::HashMismatch { .. })
        ));
        fs::remove_file(path).unwrap();
    }
}
