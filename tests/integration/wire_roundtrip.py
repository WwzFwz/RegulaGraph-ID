"""Exercise shared C01 fixtures through real Go, Rust, C++ and Python serialization.

Requires generated bindings, compiler/runtime dependencies and C++ probe. Missing prerequisites fail,
never report PASS. Uses only synthetic data; emits raw results under .cache/contracts/interop.
"""
import argparse
import importlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default="go")
    parser.add_argument("--cargo", default="cargo")
    parser.add_argument("--cpp", default=str(ROOT / ".cache/contracts-build/Release/regulagraph_wire_probe.exe"))
    args = parser.parse_args()
    out = ROOT / ".cache/contracts/interop"
    out.mkdir(parents=True, exist_ok=True)
    (out / "result.json").write_text(json.dumps({"status": "RUNNING", "scope": "C01 wire fixtures"}) + "\n", encoding="utf-8")
    sys.path[:0] = [str(ROOT / ".cache/contracts/python"), str(ROOT)]
    from google.protobuf import json_format, symbol_database
    for name in ("common", "documents", "graph", "evidence", "answers", "jobs", "inference"):
        importlib.import_module(f"regulagraph.v1.{name}_pb2")
    importlib.import_module("evaluation_pb2")
    from evaluation.datasets.schema import validate, to_json, from_json, check_utf8_span
    db = symbol_database.Default()
    cases = json.loads((ROOT / "tests/fixtures/wire-cases.json").read_text(encoding="utf-8"))["cases"]
    lines = []
    messages = {}
    for case in cases:
        name = case["name"]
        cls = db.GetSymbol("regulagraph.v1." + case["type"])
        message = json_format.ParseDict(case["value"], cls())
        if "mutate" in case:
            parts = case["mutate"]["path"].split(".")
            parent = message
            for part in parts[:-1]:
                parent = getattr(parent, part)
            setattr(parent, parts[-1], case["mutate"]["value"])
        raw = message.SerializeToString(deterministic=True) + bytes.fromhex(case.get("append_hex", ""))
        message.ParseFromString(raw)
        limit = case.get("max_bytes", 16 << 20)
        try:
            validate(message, max_bytes=limit)
            valid = True
        except (ValueError, UnicodeError):
            valid = False
        assert valid == case["valid"], f"Python validation mismatch: {name}"
        (out / f"{name}.bin").write_bytes(raw)
        lines.append(f"{name}\t{message.DESCRIPTOR.full_name}\t{'valid' if valid else 'invalid'}\t{limit}")
        messages[name] = message
    (out / "cases.tsv").write_text("\n".join(lines) + "\n", encoding="utf-8")
    env = dict(os.environ, REGULAGRAPH_WIRE_FIXTURES=str(out), CARGO_TARGET_DIR=str(ROOT / ".cache/rust-target"))
    subprocess.run([args.go, "test", "./src/server/internal/domain", "-run", "TestWireFixtureInterop", "-count=1"], cwd=ROOT, env=env, check=True)
    subprocess.run([args.cargo, "test", "--workspace", "--locked", "wire_fixture_interop", "--", "--ignored"], cwd=ROOT, env=env, check=True)
    subprocess.run([args.cpp, str(out)], cwd=ROOT, env=env, check=True)
    for language in ("go", "rust", "cpp"):
        for name, original in messages.items():
            other = type(original)()
            other.ParseFromString((out / language / f"{name}.bin").read_bytes())
            # Deterministic re-encoding also compares unknown fields; float NaN is compared as wire bytes.
            assert original.SerializeToString(deterministic=True) == other.SerializeToString(deterministic=True), (language, name)
    visibility = messages["visibility-uint64"]
    assert from_json(to_json(visibility), type(visibility)) == visibility
    assert not messages["visibility-absent-end"].HasField("to_seq")
    assert messages["visibility-present-zero"].HasField("to_seq")
    try:
        to_json(messages["unknown-field-preserved"])
    except ValueError:
        pass
    else:
        raise AssertionError("lossy unknown-field JSON bridge accepted")
    check_utf8_span("Pasal \u00e9", 0, 8)
    try:
        check_utf8_span("Pasal \u00e9", 0, 7)
    except UnicodeError:
        pass
    else:
        raise AssertionError("UTF-8 codepoint split accepted")
    result = {"status": "PASS", "fixtures": len(cases), "languages": ["Go", "Rust", "C++", "Python"], "scope": "C01 synthetic wire compatibility; not model/retrieval benchmark"}
    (out / "result.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
