"""Compile the C01 schema into Go/Python/C++ bindings and an import-complete descriptor.

This build entry point runs pinned compilers without downloading dependencies or loading models.
Go message and gRPC bindings come from the same authoritative proto files. Rust generation belongs
to ingestion/build.rs. Generated files are tool output; compiler time is not a runtime benchmark.
"""
import argparse
from pathlib import Path
import subprocess


def main():
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--protoc", default=str(root / ".cache/contracts-tools/protoc/bin/protoc.exe"))
    parser.add_argument("--go-plugin", default=str(root / ".cache/contracts-tools/bin/protoc-gen-go.exe"))
    parser.add_argument(
        "--go-grpc-plugin",
        default=str(root / ".cache/contracts-tools/bin/protoc-gen-go-grpc.exe"),
    )
    args = parser.parse_args()
    version = subprocess.check_output([args.protoc, "--version"], text=True).strip()
    if version != "libprotoc 34.1":
        raise SystemExit(f"Expected libprotoc 34.1, got {version}")
    plugin = subprocess.check_output([args.go_plugin, "--version"], text=True).strip()
    if plugin not in ("protoc-gen-go v1.36.12", "protoc-gen-go.exe v1.36.12"):
        raise SystemExit(f"Expected protoc-gen-go v1.36.12, got {plugin}")
    grpc_plugin = subprocess.check_output([args.go_grpc_plugin, "--version"], text=True).strip()
    if grpc_plugin not in ("protoc-gen-go-grpc 1.6.2", "protoc-gen-go-grpc.exe 1.6.2"):
        raise SystemExit(f"Expected protoc-gen-go-grpc 1.6.2, got {grpc_plugin}")
    cache = root / ".cache/contracts"
    for name in ("python", "cpp"):
        (cache / name).mkdir(parents=True, exist_ok=True)
    proto_root = root / "src/contracts/proto"
    inputs = sorted(str(p.relative_to(proto_root)).replace("\\", "/") for p in proto_root.rglob("*.proto"))
    inputs.append("evaluation.proto")
    subprocess.run([
        args.protoc, f"-I{proto_root}", f"-I{root / 'evaluation/datasets'}",
        f"-I{Path(args.protoc).resolve().parents[1] / 'include'}",
        f"--plugin=protoc-gen-go={args.go_plugin}",
        f"--plugin=protoc-gen-go-grpc={args.go_grpc_plugin}",
        f"--go_out={root / 'src/server'}", "--go_opt=module=regulagraph.local/server",
        f"--go-grpc_out={root / 'src/server'}", "--go-grpc_opt=module=regulagraph.local/server",
        f"--python_out={cache / 'python'}", f"--cpp_out={cache / 'cpp'}",
        f"--descriptor_set_out={cache / 'schema.pb'}", "--include_imports", "--include_source_info",
        *inputs,
    ], check=True, cwd=root)
    print(f"Generated {len(inputs)} schemas: Go messages/gRPC, Python, C++, descriptor; Rust via cargo build.")


if __name__ == "__main__":
    main()
