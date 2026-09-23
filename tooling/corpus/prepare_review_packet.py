"""Prepare a small, source-verified PDF triage packet for human G01 reviewers.

The offline packet samples one PDF per portal/size stratum by a pinned seed, rebuilds
queue metadata from the audited inventory/records, verifies selected PDF bytes, and writes
a local Markdown reading list with escaped source metadata.
It does not infer legal labels or freeze a corpus snapshot. Work is O(queue rows plus selected
PDF bytes); record hash/check time and reviewer coverage before treating strata as gold.
Required quality/latency targets remain REQUIRED_UNMEASURED in configs/benchmark-targets.yaml.
"""

from __future__ import annotations

import argparse
from collections import Counter
import hashlib
import html
import json
import os
from pathlib import Path
from typing import Any
from urllib.parse import quote

from tooling.corpus.prepare_gold_queue import _record_details
from tooling.corpus.profile_pdfs import load_candidates, select_candidates


def _json_line(value: dict[str, Any]) -> bytes:
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")


def _sha256_file(path: Path) -> tuple[str, int]:
    digest = hashlib.sha256()
    length = 0
    with path.open("rb") as stream:
        while block := stream.read(1024 * 1024):
            digest.update(block)
            length += len(block)
    return digest.hexdigest(), length


def _markdown_cell(value: object) -> str:
    escaped = str(value).replace("\r", " ").replace("\n", " ")
    for character in ("\\", "|", "[", "]", "(", ")", "!", "*", "_", "`"):
        escaped = escaped.replace(character, "\\" + character)
    return html.escape(escaped, quote=True)


def prepare_review_packet(queue_dir: Path, corpus_root: Path, output_dir: Path,
                          *, inventory_path: Path | None = None,
                          seed: str = "g01-review-pilot-v1") -> dict[str, Any]:
    """Select six portal/size strata, verify blobs, and publish an immutable reading packet."""
    if not seed or len(seed) > 128:
        raise ValueError("nonempty bounded seed required")
    queue_dir = queue_dir.resolve(strict=True)
    corpus_root = corpus_root.resolve(strict=True)
    inventory_path = inventory_path or corpus_root / "inventory.json"
    manifest = json.loads((queue_dir / "manifest.json").read_text(encoding="utf-8"))
    queue_bytes = (queue_dir / "queue.jsonl").read_bytes()
    if manifest.get("status") != "CANDIDATES_ONLY" or manifest.get("schema_version") != 1 or \
            hashlib.sha256(queue_bytes).hexdigest() != manifest.get("queue_sha256"):
        raise ValueError("annotation queue manifest or bytes are inconsistent")
    inventory, candidates = load_candidates(inventory_path)
    if inventory["inventory_id"] != manifest.get("inventory_id") or \
            inventory["records_sha256"] != manifest.get("inventory_records_sha256"):
        raise ValueError("annotation queue is not bound to the source inventory")
    queued = select_candidates(candidates, manifest["selection_limit"], manifest["selection_seed"])
    details = _record_details(inventory_path, queued, inventory["records_path"], inventory["records_sha256"])
    expected_rows = {
        candidate.sha256: {
            "queue_id": f"pdf:{candidate.sha256}", "pdf_sha256": candidate.sha256,
            "pdf_path": candidate.storage_key, "pdf_bytes": candidate.byte_size,
            "portal": candidate.portal, "size_bucket": candidate.size_bucket,
            "year_metadata": candidate.year, "source_url": candidate.source_url,
            "record_key": details[candidate.sha256]["record_key"],
            "title": details[candidate.sha256]["title"], "review_state": "UNREVIEWED",
        }
        for candidate in queued
    }
    selected: dict[tuple[str, str], tuple[str, dict[str, Any]]] = {}
    seen_pdf: set[str] = set()
    strata_counts: Counter[str] = Counter()
    for line in queue_bytes.splitlines():
        row = json.loads(line)
        pdf_hash = row.get("pdf_sha256")
        if not isinstance(pdf_hash, str) or len(pdf_hash) != 64 or \
                any(character not in "0123456789abcdef" for character in pdf_hash) or \
                pdf_hash in seen_pdf or row.get("review_state") != "UNREVIEWED":
            raise ValueError("queue contains an invalid, duplicate, or reviewed PDF")
        if row != expected_rows.get(pdf_hash):
            raise ValueError("annotation queue row differs from source inventory")
        seen_pdf.add(pdf_hash)
        relative = Path(row["pdf_path"])
        if relative.is_absolute() or ".." in relative.parts or relative.suffix.lower() != ".pdf":
            raise ValueError("queue PDF path must stay under the corpus root")
        stratum = (row["portal"], row["size_bucket"])
        strata_counts[f"{stratum[0]}/{stratum[1]}"] += 1
        rank = hashlib.sha256((seed + "\x00" + pdf_hash).encode("utf-8")).hexdigest()
        if stratum not in selected or rank < selected[stratum][0]:
            selected[stratum] = rank, row
    if seen_pdf != set(expected_rows) or len(seen_pdf) != manifest.get("queued_pdfs") or not selected or \
            dict(sorted(strata_counts.items())) != manifest.get("portal_size_counts"):
        raise ValueError("queue row count or strata are inconsistent")
    rows: list[dict[str, Any]] = []
    for portal, size_bucket in sorted(selected):
        row = selected[(portal, size_bucket)][1]
        relative = Path(row["pdf_path"])
        pdf_path = (corpus_root / relative).resolve(strict=True)
        if not pdf_path.is_relative_to(corpus_root) or not pdf_path.is_file():
            raise ValueError("PDF path escapes the corpus root")
        actual_hash, actual_bytes = _sha256_file(pdf_path)
        if actual_hash != row["pdf_sha256"] or actual_bytes != row["pdf_bytes"]:
            raise ValueError(f"PDF bytes differ from queue: {row['queue_id']}")
        rows.append({
            "queue_id": row["queue_id"], "portal": portal, "size_bucket": size_bucket,
            "year_metadata": row["year_metadata"], "title": row["title"],
            "source_url": row["source_url"], "pdf_sha256": actual_hash,
            "pdf_bytes": actual_bytes, "pdf_path": str(pdf_path),
            "review_state": "UNREVIEWED",
        })
    packet_bytes = b"".join(_json_line(row) for row in rows)
    result = {
        "schema_version": 1, "status": "SOURCE_TRIAGE_ONLY", "inventory_id": manifest["inventory_id"],
        "queue_sha256": manifest["queue_sha256"], "selection_seed": seed,
        "selected_pdfs": len(rows), "packet_sha256": hashlib.sha256(packet_bytes).hexdigest(),
        "strata": [f"{row['portal']}/{row['size_bucket']}" for row in rows],
        "warning": "PDF bytes verified; legal labels, parser output, and corpus snapshot remain unreviewed.",
    }
    packet_location = output_dir.resolve().parent / output_dir.name
    annotation_guide = Path(os.path.relpath(
        Path(__file__).resolve().parents[2] / "evaluation/datasets/annotation-guide.md",
        packet_location,
    ))
    lines = [
        "# Paket triase PDF G01", "",
        "Dokumen ini menunjuk PDF kandidat dari antrean D01 untuk pemeriksaan manusia awal.",
        "Hash byte PDF telah dicek saat paket dibuat. Ini bukan gold dataset atau snapshot corpus beku.",
        "Catat text/scan/mixed, tabel/kolom, versi, perubahan, dan gap sumber sesuai",
        f"[pedoman anotasi]({quote(annotation_guide.as_posix(), safe='/.:')}). Jangan memberi label",
        "pasal atau relasi hanya dari judul/metadata.", "",
        f"Inventory: `{result['inventory_id']}`; queue SHA-256: `{result['queue_sha256']}`;",
        f"packet SHA-256: `{result['packet_sha256']}`; seed: `{seed}`.", "",
        "| Portal / ukuran | Tahun metadata | Judul | PDF lokal | SHA-256 |", "| --- | --- | --- | --- | --- |",
    ]
    for row in rows:
        relative_pdf = Path(os.path.relpath(row["pdf_path"], packet_location))
        lines.append(f"| {_markdown_cell(row['portal'])} / {_markdown_cell(row['size_bucket'])} | "
                     f"{_markdown_cell(row['year_metadata'])} | {_markdown_cell(row['title'])} | "
                     f"[buka PDF]({quote(relative_pdf.as_posix(), safe='/.:')}) | `{row['pdf_sha256']}` |")
    lines.extend(["", "Review manusia: isi catatan terpisah dengan reviewer ID, tanggal, halaman yang diperiksa,",
                  "bukti, ketidakpastian, dan status. JDIHN belum terwakili dalam inventory lokal.", ""])
    output_dir.parent.mkdir(parents=True, exist_ok=True)
    if output_dir.exists():
        raise FileExistsError(f"refusing to overwrite review packet: {output_dir}")
    staging = output_dir.with_name(output_dir.name + ".staging")
    if staging.exists():
        raise FileExistsError(f"incomplete review packet requires inspection: {staging}")
    staging.mkdir()
    (staging / "packet.jsonl").write_bytes(packet_bytes)
    (staging / "manifest.json").write_bytes(_json_line(result))
    (staging / "review.md").write_text("\n".join(lines), encoding="utf-8")
    staging.rename(output_dir)
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--queue", type=Path, default=Path("artifacts/g01-annotation-queue-20260924"))
    parser.add_argument("--corpus-root", type=Path, default=Path("data/acquisition"))
    parser.add_argument("--inventory", type=Path, default=None,
                        help="audited D01 inventory; defaults to <corpus-root>/inventory.json")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--seed", default="g01-review-pilot-v1")
    args = parser.parse_args()
    result = prepare_review_packet(args.queue, args.corpus_root, args.output,
                                   inventory_path=args.inventory, seed=args.seed)
    print(json.dumps({"output": str(args.output), "selected_pdfs": result["selected_pdfs"],
                      "packet_sha256": result["packet_sha256"]}, sort_keys=True))


if __name__ == "__main__":
    main()
