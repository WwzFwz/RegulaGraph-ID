//! C ABI for the inference runtime's pinned tokenizer and artifact hashing.
//! C++ supplies bounded UTF-8 buffers; ownership never crosses allocators. No RPC,
//! model loading, implicit truncation or padding occurs here. Panics become errors.
//! Exact token-ID parity and tokenization-inclusive MODEL latency gates are required.
use sha2::{Digest, Sha256};
use std::{io::Read, panic::{catch_unwind, AssertUnwindSafe}, slice, str};
use tokenizers::{EncodeInput, Encoding, Tokenizer};

fn guarded(f: impl FnOnce() -> Result<(), String>) -> i32 {
    match catch_unwind(AssertUnwindSafe(f)) { Ok(Ok(())) => 0, _ => 1 }
}

/// The caller guarantees valid buffers of the stated lengths and exclusive output storage.
#[no_mangle]
pub unsafe extern "C" fn rg_tokenizer_load(data: *const u8, len: usize, out: *mut *mut Tokenizer) -> i32 {
    if data.is_null() || out.is_null() || len == 0 { return 1; }
    *out = std::ptr::null_mut();
    guarded(|| {
        let mut tokenizer = Tokenizer::from_bytes(slice::from_raw_parts(data, len)).map_err(|e| e.to_string())?;
        tokenizer.with_padding(None);
        tokenizer.with_truncation(None).map_err(|e| e.to_string())?;
        *out = Box::into_raw(Box::new(tokenizer));
        Ok(())
    })
}

/// A handle is freed exactly once, after all concurrent calls using it have completed.
#[no_mangle]
pub unsafe extern "C" fn rg_tokenizer_free(tokenizer: *mut Tokenizer) {
    if !tokenizer.is_null() { drop(Box::from_raw(tokenizer)); }
}

/// A null second input selects a single sequence; a non-null input selects a pair.
#[no_mangle]
pub unsafe extern "C" fn rg_encode(tokenizer: *const Tokenizer, first: *const u8, first_len: usize,
    second: *const u8, second_len: usize, out: *mut *mut Encoding) -> i32 {
    if tokenizer.is_null() || first.is_null() || out.is_null() { return 1; }
    *out = std::ptr::null_mut();
    guarded(|| {
        let a = str::from_utf8(slice::from_raw_parts(first, first_len)).map_err(|e| e.to_string())?;
        let input: EncodeInput = if second.is_null() { a.into() } else {
            let b = str::from_utf8(slice::from_raw_parts(second, second_len)).map_err(|e| e.to_string())?;
            (a,b).into()
        };
        let encoding = (&*tokenizer).encode(input, true).map_err(|e| e.to_string())?;
        *out = Box::into_raw(Box::new(encoding));
        Ok(())
    })
}

#[no_mangle]
pub unsafe extern "C" fn rg_encoding_len(encoding: *const Encoding) -> usize {
    if encoding.is_null() { 0 } else { (&*encoding).len() }
}
/// Borrowed array pointers remain valid until rg_encoding_free; they must not be mutated.
#[no_mangle]
pub unsafe extern "C" fn rg_encoding_ids(encoding: *const Encoding) -> *const u32 {
    if encoding.is_null() { std::ptr::null() } else { (&*encoding).get_ids().as_ptr() }
}
#[no_mangle]
pub unsafe extern "C" fn rg_encoding_types(encoding: *const Encoding) -> *const u32 {
    if encoding.is_null() { std::ptr::null() } else { (&*encoding).get_type_ids().as_ptr() }
}
#[no_mangle]
pub unsafe extern "C" fn rg_encoding_free(encoding: *mut Encoding) {
    if !encoding.is_null() { drop(Box::from_raw(encoding)); }
}

/// Hash bytes into the caller's 32-byte buffer. This binds small manifest/tokenizer buffers.
#[no_mangle]
pub unsafe extern "C" fn rg_sha256(data: *const u8, len: usize, out: *mut u8) -> i32 {
    if data.is_null() || out.is_null() { return 1; }
    guarded(|| { let digest = Sha256::digest(slice::from_raw_parts(data, len));
        std::ptr::copy_nonoverlapping(digest.as_ptr(), out, 32); Ok(()) })
}
/// Hash large weights incrementally, avoiding a second model-sized allocation.
#[no_mangle]
pub unsafe extern "C" fn rg_sha256_file(path: *const u8, len: usize, out: *mut u8) -> i32 {
    if path.is_null() || out.is_null() { return 1; }
    guarded(|| {
        let name = str::from_utf8(slice::from_raw_parts(path, len)).map_err(|e| e.to_string())?;
        let mut file = std::fs::File::open(name).map_err(|e| e.to_string())?;
        let mut hash = Sha256::new(); let mut buffer = [0u8; 65536];
        loop { let n=file.read(&mut buffer).map_err(|e| e.to_string())?; if n==0 {break;} hash.update(&buffer[..n]); }
        let digest=hash.finalize(); std::ptr::copy_nonoverlapping(digest.as_ptr(), out, 32); Ok(())
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn ffi_rejects_invalid_utf8_and_owns_encoding_buffers() {
        let config = br#"{"version":"1.0","truncation":null,"padding":null,"added_tokens":[],"normalizer":null,"pre_tokenizer":{"type":"Whitespace"},"post_processor":null,"decoder":null,"model":{"type":"WordLevel","vocab":{"[UNK]":0,"izin":1,"usaha":2},"unk_token":"[UNK]"}}"#;
        unsafe {
            let mut tokenizer=std::ptr::null_mut();
            assert_eq!(rg_tokenizer_load(config.as_ptr(),config.len(),&mut tokenizer),0);
            let mut encoding=std::ptr::null_mut();
            let text="izin usaha";
            assert_eq!(rg_encode(tokenizer,text.as_ptr(),text.len(),std::ptr::null(),0,&mut encoding),0);
            assert_eq!(slice::from_raw_parts(rg_encoding_ids(encoding),rg_encoding_len(encoding)),[1,2]);
            rg_encoding_free(encoding);
            let invalid=[255u8];
            assert_eq!(rg_encode(tokenizer,invalid.as_ptr(),1,std::ptr::null(),0,&mut encoding),1);
            assert!(encoding.is_null());
            let repeated="izin ".repeat(8193);
            assert_eq!(rg_encode(tokenizer,repeated.as_ptr(),repeated.len(),std::ptr::null(),0,&mut encoding),0);
            assert_eq!(rg_encoding_len(encoding),8193); // C++ applies capacity; ABI never truncates.
            rg_encoding_free(encoding);rg_tokenizer_free(tokenizer);
        }
    }

    #[test]
    fn ffi_hash_matches_known_sha256_and_invalid_handles_fail() {
        unsafe {
            let mut digest=[0u8;32];
            assert_eq!(rg_sha256(b"abc".as_ptr(),3,digest.as_mut_ptr()),0);
            assert_eq!("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",digest.iter().map(|b|format!("{b:02x}")).collect::<String>());
            assert_eq!(rg_sha256(std::ptr::null(),0,digest.as_mut_ptr()),1);
            assert_eq!(rg_encoding_len(std::ptr::null()),0);
        }
    }
}
