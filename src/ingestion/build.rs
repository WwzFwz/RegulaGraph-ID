//! Generate Rust domain and gRPC transport types from the sole C01 proto definitions at build time.
//! Codegen/runtime versions are paired; build output stays in OUT_DIR, with no network/model I/O here.
//! rust-protobuf preserves descriptor validation while Prost/Tonic supplies the HTTP/2 service boundary.
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

    let protoc = protoc_bin_vendored::protoc_bin_path().expect("vendored protoc is available");
    let protoc_include =
        protoc_bin_vendored::include_path().expect("vendored protobuf includes are available");
    std::env::set_var("PROTOC", protoc);
    tonic_build::configure()
        .build_client(false)
        .build_server(true)
        .compile_protos(
            &inputs,
            &[
                root,
                std::path::Path::new("../../evaluation/datasets"),
                &protoc_include,
            ],
        )
        .expect("Tonic transport bindings compile from C01 schemas");
}
