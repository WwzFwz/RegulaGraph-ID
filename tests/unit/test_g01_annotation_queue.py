"""Exercise deterministic G01 annotation preparation and fail-closed inventory provenance.

Synthetic portal rows test queue mechanics only; they are not human gold labels or an accuracy
measurement. A changed inventory must never publish a queue under the old inventory ID.
"""

from __future__ import annotations

import hashlib
import json

import pytest

from tooling.corpus.prepare_gold_queue import prepare_gold_queue


def _sha(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def _inventory(tmp_path):
    records = []
    for index, portal in enumerate(("peraturan.bpk.go.id", "jdih.komdigi.go.id"), 1):
        pdf_hash = _sha(f"pdf-{index}".encode())
        records.append({
            "record_key": _sha(f"record-{index}".encode()),
            "portal": portal,
            "source_url": f"https://{portal}/detail/{index}",
            "title": f"Peraturan {index}",
            "integrity_status": "valid",
            "portal_fields": {"year": [str(2024 + index)]},
            "pdfs": [{"sha256": pdf_hash, "path": f"blobs/{pdf_hash}.pdf",
                      "bytes": 1000 * index, "kind": "document"}],
        })
    raw = b"".join((json.dumps(record, sort_keys=True) + "\n").encode() for record in records)
    (tmp_path / "records.jsonl").write_bytes(raw)
    hashes = {"records_sha256": _sha(raw), "observations_sha256": _sha(b"observations"),
              "queue_sha256": _sha(b"queue"), "blob_set_sha256": _sha(b"blobs")}
    identity = (f"schema=1\nrecords={hashes['records_sha256']}\n"
                f"observations={hashes['observations_sha256']}\n"
                f"queue={hashes['queue_sha256']}\nblobs={hashes['blob_set_sha256']}\n").encode()
    manifest = {"schema_version": 1, "integrity_valid": True, "inventory_id": _sha(identity),
                "records_path": "records.jsonl", **hashes}
    path = tmp_path / "inventory.json"
    path.write_text(json.dumps(manifest), encoding="utf-8")
    return path, records


def test_queue_is_reproducible_and_never_pretends_to_be_gold(tmp_path):
    inventory, records = _inventory(tmp_path)
    first = prepare_gold_queue(inventory, tmp_path / "first")
    second = prepare_gold_queue(inventory, tmp_path / "second")
    assert first == second
    assert first["status"] == "CANDIDATES_ONLY"
    assert first["queued_pdfs"] == len(records)
    rows = [json.loads(line) for line in (tmp_path / "first" / "queue.jsonl").read_text().splitlines()]
    assert {row["record_key"] for row in rows} == {record["record_key"] for record in records}
    assert all(row["review_state"] == "UNREVIEWED" for row in rows)
    assert (tmp_path / "first" / "queue.jsonl").read_bytes() == (tmp_path / "second" / "queue.jsonl").read_bytes()
    with pytest.raises(FileExistsError):
        prepare_gold_queue(inventory, tmp_path / "first")


def test_changed_records_cannot_publish_old_inventory(tmp_path):
    inventory, _ = _inventory(tmp_path)
    with (tmp_path / "records.jsonl").open("ab") as source:
        source.write(b"{}\n")
    with pytest.raises(ValueError, match="hash mismatch"):
        prepare_gold_queue(inventory, tmp_path / "bad")
    assert not (tmp_path / "bad").exists()
