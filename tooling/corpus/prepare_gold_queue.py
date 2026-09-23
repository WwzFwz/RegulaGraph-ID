"""Prepare an immutable, reproducible G01 annotation queue from an audited D01 inventory.

This offline tool selects every eligible primary PDF by default, preserves portal/record/blob
provenance, and records only mechanical strata. Its output is **not** GoldQuestion data: legal
identity, answerability, source spans, graph paths, and review labels require human annotation.
The inventory's component hashes are checked before any output is published; raw PDFs are not
opened, so this is not a fresh blob-integrity audit. Sampling cost is linear in inventory rows
plus a deterministic sort; quality/latency targets remain REQUIRED_UNMEASURED in
configs/benchmark-targets.yaml.
"""

from __future__ import annotations

import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
from typing import Any

from tooling.corpus.profile_pdfs import Candidate, load_candidates, select_candidates


def _json_line(value: dict[str, Any]) -> bytes:
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")


def _record_details(inventory_path: Path, candidates: list[Candidate], records_path: str,
                    expected_sha256: str) -> dict[str, dict[str, str]]:
    wanted = {candidate.sha256: candidate.source_url for candidate in candidates}
    details: dict[str, dict[str, str]] = {}
    root = inventory_path.parent.resolve(strict=True)
    source_path = (root / records_path).resolve(strict=True)
    if not source_path.is_relative_to(root) or not source_path.is_file():
        raise ValueError("inventory records path escapes inventory root")
    source_bytes = source_path.read_bytes()
    if hashlib.sha256(source_bytes).hexdigest() != expected_sha256:
        raise ValueError("inventory records changed during queue preparation")
    for raw in source_bytes.splitlines():
        record = json.loads(raw)
        source_url = record.get("source_url", "")
        for pdf in record.get("pdfs") or ():
            sha = pdf.get("sha256", "")
            if wanted.get(sha) != source_url or pdf.get("kind") != "document":
                continue
            key = str(record.get("record_key", ""))
            if not key or not record.get("title"):
                raise ValueError(f"eligible PDF {sha} lacks record key or title")
            previous = details.get(sha)
            if previous is None or key < previous["record_key"]:
                details[sha] = {"record_key": key, "title": str(record["title"])}
    if set(details) != set(wanted):
        raise ValueError("eligible PDF has no matching source record")
    return details


def prepare_gold_queue(inventory_path: Path, output_dir: Path, *, limit: int = 0,
                       seed: str = "g01-queue-v1") -> dict[str, Any]:
    """Publish queue.jsonl and manifest.json atomically, rejecting overwrite and bad inventory."""
    if limit < 0 or not seed:
        raise ValueError("limit must be nonnegative and seed must be nonempty")
    manifest, candidates = load_candidates(inventory_path)
    chosen = select_candidates(candidates, limit, seed)
    details = _record_details(inventory_path, chosen, manifest["records_path"],
                              manifest["records_sha256"])
    queue = bytearray()
    portal_sizes: Counter[str] = Counter()
    years: Counter[str] = Counter()
    for candidate in chosen:
        row = {
            "queue_id": f"pdf:{candidate.sha256}",
            "pdf_sha256": candidate.sha256,
            "pdf_path": candidate.storage_key,
            "pdf_bytes": candidate.byte_size,
            "portal": candidate.portal,
            "source_url": candidate.source_url,
            "record_key": details[candidate.sha256]["record_key"],
            "title": details[candidate.sha256]["title"],
            "year_metadata": candidate.year,
            "size_bucket": candidate.size_bucket,
            "review_state": "UNREVIEWED",
        }
        queue.extend(_json_line(row))
        portal_sizes[f"{candidate.portal}/{candidate.size_bucket}"] += 1
        years[candidate.year] += 1
    queue_bytes = bytes(queue)
    result = {
        "schema_version": 1,
        "status": "CANDIDATES_ONLY",
        "inventory_id": manifest["inventory_id"],
        "inventory_records_sha256": manifest["records_sha256"],
        "selection_seed": seed,
        "selection_limit": limit,
        "eligible_primary_pdfs": len(candidates),
        "queued_pdfs": len(chosen),
        "queue_sha256": hashlib.sha256(queue_bytes).hexdigest(),
        "portal_size_counts": dict(sorted(portal_sizes.items())),
        "year_metadata_counts": dict(sorted(years.items())),
        "warning": "Metadata strata are unreviewed; this is not a gold dataset or frozen corpus snapshot.",
    }
    output_parent = output_dir.parent
    output_parent.mkdir(parents=True, exist_ok=True)
    if output_dir.exists():
        raise FileExistsError(f"refusing to overwrite annotation queue: {output_dir}")
    staging = output_dir.with_name(output_dir.name + ".staging")
    if staging.exists():
        raise FileExistsError(f"incomplete annotation queue requires inspection: {staging}")
    staging.mkdir()
    (staging / "queue.jsonl").write_bytes(queue_bytes)
    (staging / "manifest.json").write_bytes(_json_line(result))
    staging.rename(output_dir)
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", type=Path, default=Path("data/acquisition/inventory.json"))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--limit", type=int, default=0, help="0 queues every eligible primary PDF")
    parser.add_argument("--seed", default="g01-queue-v1")
    args = parser.parse_args()
    result = prepare_gold_queue(args.inventory, args.output, limit=args.limit, seed=args.seed)
    print(json.dumps({"output": str(args.output), "queued_pdfs": result["queued_pdfs"],
                      "queue_sha256": result["queue_sha256"]}, sort_keys=True))


if __name__ == "__main__":
    main()
