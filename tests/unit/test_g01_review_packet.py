"""Verify deterministic PDF triage and byte-integrity failure before human G01 review.

These local fixtures exercise packet publication and path/hash guards, not legal labels or
parser/model quality. Gold and required benchmark gates remain unmeasured.
"""

import hashlib
import json
from pathlib import Path

import pytest

from tooling.corpus.prepare_review_packet import prepare_review_packet


def _line(value: dict) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()


def _queue(tmp_path: Path) -> tuple[Path, Path, list[dict]]:
    corpus = tmp_path / "corpus"
    blobs = corpus / "blobs"
    blobs.mkdir(parents=True)
    rows = []
    records = []
    for portal, number in (("bpk", 1), ("bpk", 2), ("jdih", 3)):
        pdf = f"%PDF-1.4\nfixture {number}\n".encode()
        sha = hashlib.sha256(pdf).hexdigest()
        (blobs / f"{sha}.pdf").write_bytes(pdf)
        rows.append({
            "queue_id": f"pdf:{sha}", "pdf_sha256": sha, "pdf_path": f"blobs/{sha}.pdf",
            "pdf_bytes": len(pdf), "portal": portal, "size_bucket": "small_lt_1_mib",
            "year_metadata": "2025", "title": f"Peraturan {number}",
            "source_url": f"https://example.test/{number}", "record_key": f"record:{number}",
            "review_state": "UNREVIEWED",
        })
        records.append({
            "integrity_status": "valid", "portal": portal, "source_url": f"https://example.test/{number}",
            "record_key": f"record:{number}", "title": f"Peraturan {number}",
            "portal_fields": {"year": ["2025"]},
            "pdfs": [{"kind": "document", "sha256": sha, "path": f"blobs/{sha}.pdf", "bytes": len(pdf)}],
        })
    records_bytes = b"".join(_line(record) for record in records)
    (corpus / "inventory.records.jsonl").write_bytes(records_bytes)
    records_hash = hashlib.sha256(records_bytes).hexdigest()
    empty_hash = hashlib.sha256(b"").hexdigest()
    inventory_id = hashlib.sha256((f"schema=1\nrecords={records_hash}\nobservations={empty_hash}\n"
                                   f"queue={empty_hash}\nblobs={empty_hash}\n").encode()).hexdigest()
    (corpus / "inventory.json").write_bytes(_line({
        "schema_version": 1, "integrity_valid": True, "inventory_id": inventory_id,
        "records_path": "inventory.records.jsonl", "records_sha256": records_hash,
        "observations_sha256": empty_hash, "queue_sha256": empty_hash, "blob_set_sha256": empty_hash,
    }))
    queue_dir = tmp_path / "queue"
    queue_dir.mkdir()
    queue_bytes = b"".join(_line(row) for row in rows)
    (queue_dir / "queue.jsonl").write_bytes(queue_bytes)
    (queue_dir / "manifest.json").write_bytes(_line({
        "schema_version": 1, "status": "CANDIDATES_ONLY", "inventory_id": inventory_id,
        "inventory_records_sha256": records_hash, "selection_limit": 0, "selection_seed": "queue-test",
        "queue_sha256": hashlib.sha256(queue_bytes).hexdigest(), "queued_pdfs": len(rows),
        "portal_size_counts": {"bpk/small_lt_1_mib": 2, "jdih/small_lt_1_mib": 1},
    }))
    return queue_dir, corpus, rows


def test_review_packet_is_deterministic_and_never_gold(tmp_path: Path) -> None:
    queue_dir, corpus, _ = _queue(tmp_path)
    first = prepare_review_packet(queue_dir, corpus, tmp_path / "first", seed="fixed")
    second = prepare_review_packet(queue_dir, corpus, tmp_path / "second", seed="fixed")
    assert first == second
    assert first["status"] == "SOURCE_TRIAGE_ONLY"
    assert first["selected_pdfs"] == 2
    assert (tmp_path / "first" / "packet.jsonl").read_bytes() == (tmp_path / "second" / "packet.jsonl").read_bytes()
    assert "buka PDF" in (tmp_path / "first" / "review.md").read_text(encoding="utf-8")
    with pytest.raises(FileExistsError):
        prepare_review_packet(queue_dir, corpus, tmp_path / "first", seed="fixed")


def test_review_packet_rejects_changed_pdf_and_forged_queue_path(tmp_path: Path) -> None:
    queue_dir, corpus, rows = _queue(tmp_path)
    selected = prepare_review_packet(queue_dir, corpus, tmp_path / "original", seed="fixed")
    assert selected["selected_pdfs"] == 2
    picked = json.loads((tmp_path / "original" / "packet.jsonl").read_text().splitlines()[0])
    Path(picked["pdf_path"]).write_bytes(b"tampered")
    with pytest.raises(ValueError, match="PDF bytes differ"):
        prepare_review_packet(queue_dir, corpus, tmp_path / "tampered", seed="fixed")
    assert not (tmp_path / "tampered").exists()
    # Restore bytes, then prove an inventory path cannot reach outside the corpus root.
    row = next(row for row in rows if row["pdf_sha256"] == picked["pdf_sha256"])
    number = row["title"].split()[-1]
    Path(picked["pdf_path"]).write_bytes(f"%PDF-1.4\nfixture {number}\n".encode())
    rows[0]["pdf_path"] = "../outside.pdf"
    new_queue = b"".join(_line(row) for row in rows)
    (queue_dir / "queue.jsonl").write_bytes(new_queue)
    manifest = json.loads((queue_dir / "manifest.json").read_text())
    manifest["queue_sha256"] = hashlib.sha256(new_queue).hexdigest()
    (queue_dir / "manifest.json").write_bytes(_line(manifest))
    output = tmp_path / "escape"
    with pytest.raises(ValueError, match="differs from source inventory"):
        prepare_review_packet(queue_dir, corpus, output, seed="fixed")
    assert not output.exists()


def test_review_packet_rejects_forged_metadata_and_escapes_title(tmp_path: Path) -> None:
    queue_dir, corpus, rows = _queue(tmp_path)
    malicious_title = "<img src=x onerror=alert(1)> [lihat](https://example.test)"
    rows[0]["title"] = malicious_title
    rows[1]["title"] = malicious_title
    forged = b"".join(_line(row) for row in rows)
    (queue_dir / "queue.jsonl").write_bytes(forged)
    manifest = json.loads((queue_dir / "manifest.json").read_text())
    manifest["queue_sha256"] = hashlib.sha256(forged).hexdigest()
    (queue_dir / "manifest.json").write_bytes(_line(manifest))
    with pytest.raises(ValueError, match="differs from source inventory"):
        prepare_review_packet(queue_dir, corpus, tmp_path / "forged")

    records_path = corpus / "inventory.records.jsonl"
    records = [json.loads(line) for line in records_path.read_text().splitlines()]
    records[0]["title"] = malicious_title
    records[1]["title"] = malicious_title
    raw_records = b"".join(_line(record) for record in records)
    records_path.write_bytes(raw_records)
    inventory_path = corpus / "inventory.json"
    inventory = json.loads(inventory_path.read_text())
    inventory["records_sha256"] = hashlib.sha256(raw_records).hexdigest()
    inventory["inventory_id"] = hashlib.sha256((
        f"schema=1\nrecords={inventory['records_sha256']}\nobservations={inventory['observations_sha256']}\n"
        f"queue={inventory['queue_sha256']}\nblobs={inventory['blob_set_sha256']}\n"
    ).encode()).hexdigest()
    inventory_path.write_bytes(_line(inventory))
    manifest["inventory_id"] = inventory["inventory_id"]
    manifest["inventory_records_sha256"] = inventory["records_sha256"]
    (queue_dir / "manifest.json").write_bytes(_line(manifest))
    prepare_review_packet(queue_dir, corpus, tmp_path / "escaped", seed="fixed")
    review = (tmp_path / "escaped" / "review.md").read_text(encoding="utf-8")
    assert "<img" not in review
    assert "[lihat](https://example.test)" not in review


def test_review_packet_rejects_omitted_inventory_pdf(tmp_path: Path) -> None:
    queue_dir, corpus, rows = _queue(tmp_path)
    omitted = b"".join(_line(row) for row in rows[:1])
    (queue_dir / "queue.jsonl").write_bytes(omitted)
    manifest = json.loads((queue_dir / "manifest.json").read_text())
    manifest["queue_sha256"] = hashlib.sha256(omitted).hexdigest()
    manifest["queued_pdfs"] = 1
    manifest["portal_size_counts"] = {"bpk/small_lt_1_mib": 1}
    (queue_dir / "manifest.json").write_bytes(_line(manifest))
    with pytest.raises(ValueError, match="row count or strata"):
        prepare_review_packet(queue_dir, corpus, tmp_path / "omitted")
