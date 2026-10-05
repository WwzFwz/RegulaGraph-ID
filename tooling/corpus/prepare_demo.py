"""Prepare a bounded, reproducible text-layer PDF sample for the local interview demo.

This offline experiment reads existing D01 receipts, verifies PDF bytes, and exports
UTF-8 page text plus unchanged acquisition records with a SHA256SUMS manifest.
It does not create production C01 documents, canonical identities, OCR, or legal
version assertions. Go owns interactive retrieval. Empty/scanned pages and skipped
documents remain visible in the report; required parsing/quality/performance gates
in configs/benchmark-targets.yaml remain NOT_MEASURED.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import time

import fitz


def prepare(root: Path, output: Path, limit: int, maximum_pages: int) -> dict:
    root = root.resolve(strict=True)
    if output.exists():
        raise ValueError("Output already exists; choose a new immutable run directory")
    if not 1 <= limit <= 100 or not 1 <= maximum_pages <= 500:
        raise ValueError("Use 1..100 documents and 1..500 pages per document")
    priorities = ["pelindungan data pribadi", "informasi dan transaksi elektronik",
                  "sistem dan transaksi elektronik", "tata kelola penyelenggaraan sistem elektronik",
                  "telekomunikasi"]
    candidates = []
    for path in sorted((root / "records").glob("*.json")):
        raw = path.read_bytes()
        record = json.loads(raw)
        if record.get("status") != "complete" or record.get("schema_version") != 1:
            continue
        title = record.get("metadata", {}).get("title", "").lower()
        rank = next((i for i, phrase in enumerate(priorities) if phrase in title), len(priorities))
        for pdf in record.get("pdfs", []):
            if pdf.get("kind") == "document" and re.fullmatch(r"[a-f0-9]{64}", pdf.get("sha256", "")):
                candidates.append((rank, path.name, raw, pdf))
    candidates.sort(key=lambda row: (row[0], row[1], row[3]["sha256"]))
    output.mkdir(parents=True)
    manifest = []
    report = {"mode": "local-text-layer-demo", "parser": f"PyMuPDF/{fitz.VersionBind}",
              "documents": [], "skipped": [], "empty_pages": 0, "pages": 0,
              "legal_status": "UNREVIEWED", "ocr": False}
    seen = set()
    started = time.perf_counter()

    def write(name: str, raw: bytes) -> None:
        destination = output / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(raw)
        manifest.append(f"{hashlib.sha256(raw).hexdigest()}  {name}\n")

    for _, _, raw, pdf in candidates:
        digest = pdf["sha256"]
        if digest in seen:
            continue
        seen.add(digest)
        if len(report["documents"]) >= limit:
            break
        try:
            path = (root / pdf["path"]).resolve(strict=True)
            if not path.is_relative_to(root):
                raise ValueError("PDF path escapes acquisition root")
            if pdf.get("bytes", 0) <= 0 or pdf["bytes"] > 50 << 20:
                raise ValueError("PDF outside demo 50 MiB bound")
            data = path.read_bytes()
            if len(data) != pdf["bytes"] or hashlib.sha256(data).hexdigest() != digest:
                raise ValueError("PDF hash or size differs from receipt")
            with fitz.open(stream=data, filetype="pdf") as document:
                if document.needs_pass or document.page_count > maximum_pages:
                    raise ValueError("Encrypted PDF or page count outside demo bound")
                pages = [page.get_text(sort=True).replace("\x00", "").encode("utf-8") for page in document]
            if any(len(page) > 1 << 20 for page in pages):
                raise ValueError("Extracted page exceeds 1 MiB")
            if not any(len(page.strip()) >= 80 for page in pages):
                raise ValueError("No usable text layer; OCR not run")
            write(f"{digest}/record.json", raw)
            for number, page in enumerate(pages, 1):
                write(f"{digest}/page-{number:05d}.txt", page)
            report["pages"] += len(pages)
            report["empty_pages"] += sum(len(page.strip()) < 80 for page in pages)
            report["documents"].append({"sha256": digest, "pages": len(pages),
                                         "title": json.loads(raw)["metadata"]["title"]})
        except (ValueError, OSError, RuntimeError) as error:
            report["skipped"].append({"sha256": digest, "reason": str(error)})
    if not report["documents"]:
        raise ValueError("No usable documents; no completed manifest published")
    report["elapsed_seconds"] = round(time.perf_counter() - started, 3)
    write("report.json", json.dumps(report, ensure_ascii=False, indent=2).encode("utf-8"))
    # Final marker: consumers never open a run lacking the full manifest.
    (output / "SHA256SUMS").write_text("".join(sorted(manifest)), encoding="utf-8", newline="\n")
    return report


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--acquisition", type=Path, default=Path("data/acquisition"))
    parser.add_argument("--out", type=Path, default=Path("artifacts/interview-demo"))
    parser.add_argument("--documents", type=int, default=24)
    parser.add_argument("--max-pages", type=int, default=150)
    args = parser.parse_args()
    result = prepare(args.acquisition, args.out, args.documents, args.max_pages)
    print(json.dumps({"documents": len(result["documents"]), "pages": result["pages"],
                      "skipped": len(result["skipped"]), "out": str(args.out)}))


if __name__ == "__main__":
    main()
