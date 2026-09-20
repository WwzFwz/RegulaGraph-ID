//! Generate Rust wire types from the sole C01 proto definitions at build time.
//! Codegen/runtime versions are paired; build output stays in OUT_DIR, with no network/model I/O here.
//! The pure parser removes a protoc install requirement for Rust-only builds; wire parity is tested externally.
fn main() {
    let root = std::path::Path::new("../contracts/proto");
    let names = [
        "common",
        "documents",
        "graph",
        "evidence",
        "answers",
        "jobs",
        "inference",
    ];
    let mut inputs: Vec<std::path::PathBuf> = names
        .iter()
        .map(|name| root.join(format!("regulagraph/v1/{name}.proto")))
        .collect();
    inputs.push(std::path::PathBuf::from(
        "../../evaluation/datasets/evaluation.proto",
    ));
    for path in &inputs {
        println!("cargo:rerun-if-changed={}", path.display());
    }
    protobuf_codegen::Codegen::new()
        .pure()
        .includes([root, std::path::Path::new("../../evaluation/datasets")])
        .inputs(&inputs)
        .cargo_out_dir("wire")
        .run_from_script();
}
