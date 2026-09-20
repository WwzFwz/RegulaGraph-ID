"""Check the C01 descriptor against its versioned semantic compatibility baseline.

The lock is derived from authoritative proto files, never a second editable wire schema.
Existing tags/types/presence/rules/enums/RPC signatures cannot silently change; additions are allowed.
This structural guard complements real runtime fixtures and does not prove all future semantic compatibility.
"""
import argparse
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]


def describe(raw):
    sys.path.insert(0, str(ROOT / ".cache/contracts/python"))
    from regulagraph.v1 import common_pb2  # registers shared validation-rule extensions
    from google.protobuf import descriptor_pb2, json_format
    descriptor = descriptor_pb2.FileDescriptorSet.FromString(raw)
    result = {"description": "Generated C01 schema baseline; regenerate intentionally after compatibility review, never edit field entries manually.", "schema_version": 1, "messages": {}, "enums": {}, "services": {}}
    for file in descriptor.file:
        if file.package != "regulagraph.v1":
            continue
        def visit(messages, prefix):
            for message in messages:
                name = prefix + message.name
                fields = {}
                for f in message.field:
                    fields[str(f.number)] = {
                        "name": f.name, "type": f.type, "type_name": f.type_name, "label": f.label,
                        "optional": f.proto3_optional,
                        "oneof": message.oneof_decl[f.oneof_index].name if f.HasField("oneof_index") else None,
                        "rules": json_format.MessageToDict(f.options.Extensions[common_pb2.rules], preserving_proto_field_name=True),
                    }
                result["messages"][name] = fields
                visit(message.nested_type, name + ".")
        visit(file.message_type, file.package + ".")
        for enum in file.enum_type:
            result["enums"][file.package + "." + enum.name] = {str(v.number): v.name for v in enum.value}
        for service in file.service:
            result["services"][file.package + "." + service.name] = {m.name: [m.input_type, m.output_type, m.client_streaming, m.server_streaming] for m in service.method}
    return result


def compatibility_errors(baseline, current):
    errors = []
    for section in ("messages", "enums", "services"):
        for name, members in baseline[section].items():
            if name not in current[section]:
                errors.append(f"removed {section}: {name}")
                continue
            for key, value in members.items():
                if current[section][name].get(key) != value:
                    errors.append(f"changed/removed {section}: {name}.{key}")
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write-baseline", action="store_true", help="Explicitly establish a reviewed baseline; normal verification must omit this flag")
    args = parser.parse_args()
    current = describe((ROOT / ".cache/contracts/schema.pb").read_bytes())
    lock = ROOT / "src/contracts/schema-lock.json"
    if args.write_baseline:
        lock.write_text(json.dumps(current, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    else:
        errors = compatibility_errors(json.loads(lock.read_text(encoding="utf-8")), current)
        if errors:
            raise SystemExit("\n".join(errors))
        # Counterexamples exercise the checker rather than merely accepting its own generated baseline.
        changed = json.loads(json.dumps(current))
        name = "regulagraph.v1.TextSpan"
        changed["messages"][name]["2"]["type"] = 9
        assert compatibility_errors(current, changed), "type change accepted"
        changed = json.loads(json.dumps(current))
        changed["messages"][name]["2"]["rules"] = {"required": True}
        assert compatibility_errors(current, changed), "semantic constraint change accepted"
    print(json.dumps({"messages": len(current["messages"]), "enums": len(current["enums"]), "services": len(current["services"]), "baseline_written": args.write_baseline}))


if __name__ == "__main__":
    main()
