//! Offline worker entry point for corpus vocabulary and frozen BM25 preparation.
//! Inputs are operator-selected C01 binary snapshot/source/dictionary references;
//! library code authenticates artifacts and renders text. Go remains responsible
//! for source authorization, term allocation, artifact registration and publication.
//! Output files use create-new to prevent accidental replacement. No models,
//! network listeners or database connections are initialized. Measure corpus I/O,
//! RSS and throughput separately from required query/quality acceptance.

use protobuf::{Message, MessageFull};
use regulagraph_ingestion::{
    adapters::storage::{ArtifactStore, ArtifactStoreConfig},
    document::normalization::text::TextNormalizerConfig,
    domain::wire::{self, Limits},
    indexing::{
        build::load_typed,
        dictionary_artifact::CheckedDictionary,
        population::{prepare_population, PopulationLimits},
    },
    wire::{common, evidence},
};
use std::{
    collections::HashMap,
    fs::{File, OpenOptions},
    io::{Read, Write},
    path::Path,
    sync::atomic::AtomicBool,
};

const USAGE: &str = "regulagraph-lexical vocabulary|freeze --artifacts ROOT --snapshot SNAPSHOT.pb --auth-scope SCOPE --source-ref SOURCE-REF.pb [--source-ref ...] --output NEW-FILE [--dictionary-ref ROOT-REF.pb ... --statistics-id ID --k1 1.2 --b 0.75]";

struct Options {
    command: String,
    single: HashMap<String, String>,
    sources: Vec<String>,
    dictionaries: Vec<String>,
}

fn parse(args: Vec<String>) -> Result<Options, String> {
    if args.is_empty()
        || args.len() > 660
        || !matches!(args[0].as_str(), "vocabulary" | "freeze")
        || args.len() % 2 != 1
    {
        return Err(USAGE.into());
    }
    let mut result = Options {
        command: args[0].clone(),
        single: HashMap::new(),
        sources: vec![],
        dictionaries: vec![],
    };
    for pair in args[1..].chunks_exact(2) {
        if pair[1].is_empty() {
            return Err("empty argument".into());
        }
        match pair[0].as_str() {
            "--source-ref" => result.sources.push(pair[1].clone()),
            "--dictionary-ref" => result.dictionaries.push(pair[1].clone()),
            "--artifacts" | "--snapshot" | "--auth-scope" | "--output" | "--statistics-id"
            | "--k1" | "--b" => {
                if result
                    .single
                    .insert(pair[0].clone(), pair[1].clone())
                    .is_some()
                {
                    return Err("duplicate argument".into());
                }
            }
            _ => return Err(USAGE.into()),
        }
    }
    for key in ["--artifacts", "--snapshot", "--auth-scope", "--output"] {
        if !result.single.contains_key(key) {
            return Err(format!("missing {key}: {USAGE}"));
        }
    }
    if result.sources.is_empty() || result.sources.len() > 256 || result.dictionaries.len() > 64 {
        return Err("source/dictionary count outside bounds".into());
    }
    let freeze_keys = ["--statistics-id", "--k1", "--b"];
    if result.command == "freeze" {
        if result.dictionaries.is_empty()
            || freeze_keys.iter().any(|k| !result.single.contains_key(*k))
        {
            return Err("freeze requires dictionary, statistics ID and explicit k1/b".into());
        }
        parameters(&result)?;
    } else if !result.dictionaries.is_empty()
        || freeze_keys.iter().any(|k| result.single.contains_key(*k))
    {
        return Err("vocabulary does not accept dictionary/statistics parameters".into());
    }
    Ok(result)
}

fn parameters(opts: &Options) -> Result<(f64, f64), String> {
    let k1: f64 = opts.single["--k1"].parse().map_err(|_| "invalid k1")?;
    let b: f64 = opts.single["--b"].parse().map_err(|_| "invalid b")?;
    if !k1.is_finite() || k1 <= 0.0 || !b.is_finite() || !(0.0..=1.0).contains(&b) {
        return Err("invalid BM25 parameters".into());
    }
    Ok((k1, b))
}

fn read_message<M: MessageFull>(path: &str) -> Result<M, String> {
    let file = File::open(path).map_err(|e| e.to_string())?;
    let info = file.metadata().map_err(|e| e.to_string())?;
    if !info.is_file() || info.len() == 0 || info.len() > 64 << 10 {
        return Err("reference must be a regular file within 64 KiB".into());
    }
    let mut raw = vec![];
    file.take((64 << 10) + 1)
        .read_to_end(&mut raw)
        .map_err(|e| e.to_string())?;
    if raw.len() > 64 << 10 {
        return Err("reference byte budget exceeded".into());
    }
    wire::decode(&raw, &M::descriptor(), Limits::default())?
        .downcast_box::<M>()
        .map(|m| *m)
        .map_err(|_| "reference type mismatch".into())
}

fn run(opts: Options) -> Result<(), String> {
    // Reserve the output name first. A failed run can leave an empty/incomplete
    // file; exit success and its printed status are required before consumption.
    let mut output = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&opts.single["--output"])
        .map_err(|e| e.to_string())?;
    let snapshot: common::SnapshotRef = read_message(&opts.single["--snapshot"])?;
    let sources = opts
        .sources
        .iter()
        .map(|p| read_message::<common::ArtifactRef>(p))
        .collect::<Result<Vec<_>, _>>()?;
    let store = ArtifactStore::open(
        Path::new(&opts.single["--artifacts"]),
        ArtifactStoreConfig::default(),
    )
    .map_err(|e| e.to_string())?;
    let cancelled = AtomicBool::new(false);
    let population = prepare_population(
        &store,
        &sources,
        &snapshot,
        &opts.single["--auth-scope"],
        &TextNormalizerConfig::default(),
        PopulationLimits::default(),
        &cancelled,
    )?;
    if opts.command == "vocabulary" {
        // Analyzer terms never contain line endings; UTF-8 lines are a CLI input
        // for the operator's Go allocator, not a second dictionary wire schema.
        for term in population.terms() {
            writeln!(output, "{term}").map_err(|e| e.to_string())?;
        }
    } else {
        let mut checked = None;
        let mut total = 0u64;
        for path in &opts.dictionaries {
            let reference: common::ArtifactRef = read_message(path)?;
            total = total
                .checked_add(reference.byte_size)
                .ok_or("dictionary bytes overflow")?;
            if total > 64 << 20 {
                return Err("dictionary byte budget exceeded".into());
            }
            let dictionary: evidence::LexicalDictionaryArtifact =
                load_typed(&store, &reference, &cancelled)?;
            if dictionary.meta.record_id != reference.artifact_id {
                return Err("dictionary reference identity mismatch".into());
            }
            checked = Some(CheckedDictionary::from_artifact(
                &dictionary,
                &snapshot.corpus_id,
                checked.as_ref(),
                Limits::default(),
            )?);
        }
        let (k1, b) = parameters(&opts)?;
        let meta = common::RecordMeta {
            schema_version: 1,
            corpus_id: snapshot.corpus_id.clone(),
            record_id: opts.single["--statistics-id"].clone(),
            ..Default::default()
        };
        let statistics =
            population.freeze(&meta, checked.as_ref().ok_or("dictionary missing")?, k1, b)?;
        let raw = statistics.write_to_bytes().map_err(|e| e.to_string())?;
        let mut reference = store
            .put_bytes(
                "lexical-statistics",
                "application/x-protobuf; message=regulagraph.v1.LexicalStatisticsArtifact",
                1,
                &raw,
            )
            .map_err(|e| e.to_string())?
            .to_wire_ref()
            .map_err(|e| e.to_string())?;
        reference.artifact_id = meta.record_id;
        output
            .write_all(&reference.write_to_bytes().map_err(|e| e.to_string())?)
            .map_err(|e| e.to_string())?;
    }
    output.sync_all().map_err(|e| e.to_string())?;
    println!(
        "prepared {} documents, {} tokens; publication pending",
        population.document_count(),
        population.total_tokens()
    );
    Ok(())
}

fn main() {
    let options = match parse(std::env::args().skip(1).collect()) {
        Ok(value) => value,
        Err(error) => {
            eprintln!("{error}");
            std::process::exit(2);
        }
    };
    if let Err(error) = run(options) {
        eprintln!("lexical preparation failed: {error}");
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn args() -> Vec<String> {
        "vocabulary --artifacts root --snapshot snapshot.pb --auth-scope scope --source-ref source.pb --output output.txt".split_whitespace().map(str::to_owned).collect()
    }
    #[test]
    fn configuration_rejects_ambiguous_or_incomplete_invocations() {
        assert!(parse(args()).is_ok());
        let mut duplicated = args();
        duplicated.extend(["--output".into(), "other".into()]);
        assert!(parse(duplicated).is_err());
        let mut missing = args();
        missing[0] = "freeze".into();
        assert!(parse(missing.clone()).is_err());
        missing.extend(
            "--dictionary-ref dict.pb --statistics-id stats:one --k1 1.2 --b 0.75"
                .split_whitespace()
                .map(str::to_owned),
        );
        assert!(parse(missing.clone()).is_ok());
        *missing.last_mut().unwrap() = "NaN".into();
        assert!(parse(missing).is_err());
    }
}
