"""Profil PDF corpus nyata secara deterministik untuk pembuktian parser/OCR M01.

Peran dalam komponen:
Memilih blob dari inventory D01 menurut portal dan bucket ukuran, menjalankan pypdf dalam proses terisolasi,
dan menulis hasil per dokumen beserta manifest run untuk perbandingan engine berikutnya.

Kontrak integrasi dan perhatian implementasi:
Input wajib inventory integrity-valid dan records hash-nya cocok. Storage key harus tetap di bawah root.
Setiap dokumen memiliki timeout keras; error/timeout tetap denominator. Heuristic text/scan/mixed/sparse
hanya membantu sampling G01 dan tidak boleh dianggap gold parsing atau keputusan OCR final.

Benchmark dan gate penerimaan:
Catat inventory/config/engine version, pages, elapsed, throughput, error, serta distribusi strata. Bandingkan
engine pada sampel dan budget yang sama. Target PARSING.* tetap configs/benchmark-targets.yaml dengan status
REQUIRED_UNMEASURED sampai native parser, source mapping, gold structure/CER, RSS, dan workload penuh tersedia.

Status: baseline pypdf M01 aktif untuk eksperimen offline; bukan parser produksi.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import dataclasses
import hashlib
import json
import math
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
from typing import Any, Iterable

import pypdf


SCHEMA_VERSION = 1
TEXT_PAGE_MIN_CHARS = 80
SCAN_PAGE_MAX_CHARS = 20


@dataclasses.dataclass(frozen=True)
class Candidate:
    sha256: str
    storage_key: str
    byte_size: int
    portal: str
    source_url: str
    year: str

    @property
    def size_bucket(self) -> str:
        if self.byte_size < 1 << 20:
            return "small_lt_1_mib"
        if self.byte_size < 10 << 20:
            return "medium_1_10_mib"
        return "large_ge_10_mib"


def _sha256(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def _is_sha256(value: Any) -> bool:
    return isinstance(value, str) and len(value) == 64 and all(character in "0123456789abcdef" for character in value)


def _canonical_json(value: Any) -> bytes:
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")


def _resolve_under(root: Path, relative_path: str, *, kind: str) -> Path:
    resolved_root = root.resolve(strict=True)
    resolved = (resolved_root / Path(relative_path)).resolve(strict=True)
    if not resolved.is_relative_to(resolved_root) or not resolved.is_file():
        raise ValueError(f"{kind} path escapes inventory root: {relative_path}")
    return resolved


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def load_candidates(inventory_path: Path) -> tuple[dict[str, Any], list[Candidate]]:
    manifest = json.loads(inventory_path.read_text(encoding="utf-8"))
    if type(manifest.get("schema_version")) is not int or manifest["schema_version"] != SCHEMA_VERSION or manifest.get("integrity_valid") is not True:
        raise ValueError("inventory must use schema 1 and pass integrity")
    component_hash_keys = ("records_sha256", "observations_sha256", "queue_sha256", "blob_set_sha256")
    if not all(_is_sha256(manifest.get(key)) for key in component_hash_keys):
        raise ValueError("inventory component hashes must be lowercase SHA-256 values")
    identity_seed = (
        f"schema={SCHEMA_VERSION}\n"
        f"records={manifest.get('records_sha256', '')}\n"
        f"observations={manifest.get('observations_sha256', '')}\n"
        f"queue={manifest.get('queue_sha256', '')}\n"
        f"blobs={manifest.get('blob_set_sha256', '')}\n"
    ).encode()
    if _sha256(identity_seed) != manifest.get("inventory_id"):
        raise ValueError("inventory identity does not bind declared component hashes")
    records_path = _resolve_under(inventory_path.parent, manifest["records_path"], kind="records")
    raw = records_path.read_bytes()
    if _sha256(raw) != manifest.get("records_sha256"):
        raise ValueError("inventory records hash mismatch")
    candidates: dict[str, Candidate] = {}
    for line_number, line in enumerate(raw.splitlines(), 1):
        if not line.strip():
            continue
        record = json.loads(line)
        if record.get("integrity_status") != "valid":
            continue
        fields = record.get("portal_fields") or {}
        year_values = fields.get("year") or []
        for receipt in record.get("pdfs") or []:
            if receipt.get("error") or receipt.get("kind") != "document":
                continue
            sha = receipt.get("sha256", "")
            key = receipt.get("path", "")
            size = receipt.get("bytes", 0)
            if not _is_sha256(sha) or key != f"blobs/{sha}.pdf" or type(size) is not int or size <= 0:
                raise ValueError(f"invalid PDF receipt at line {line_number}")
            candidate = Candidate(sha, key, size, record["portal"], record["source_url"], str(year_values[0]) if year_values else "unknown")
            existing = candidates.get(sha)
            if existing is None or (candidate.portal, candidate.source_url) < (existing.portal, existing.source_url):
                candidates[sha] = candidate
    if not candidates:
        raise ValueError("inventory contains no eligible primary PDF")
    return manifest, list(candidates.values())


def select_candidates(candidates: Iterable[Candidate], limit: int, seed: str) -> list[Candidate]:
    values = list(candidates)
    if limit <= 0 or limit >= len(values):
        return sorted(values, key=lambda item: item.sha256)
    groups: dict[tuple[str, str], list[Candidate]] = {}
    for item in values:
        groups.setdefault((item.portal, item.size_bucket), []).append(item)
    for key in groups:
        groups[key].sort(key=lambda item: _sha256(f"{seed}\0{item.sha256}".encode()))
    ordered_groups = sorted(groups)
    selected: list[Candidate] = []
    cursor = 0
    while len(selected) < limit:
        progressed = False
        for key in ordered_groups:
            group = groups[key]
            if cursor < len(group):
                selected.append(group[cursor])
                progressed = True
                if len(selected) == limit:
                    break
        if not progressed:
            break
        cursor += 1
    return selected


def classify_document(text_pages: int, scan_pages: int, sparse_pages: int) -> str:
    if text_pages and scan_pages:
        return "mixed_candidate"
    if scan_pages:
        return "scan_candidate"
    if text_pages:
        return "text_candidate"
    if sparse_pages:
        return "sparse_or_blank"
    return "empty"


def _count_page_images(page: Any) -> int:
    try:
        resources = page.get("/Resources") or {}
        resources = resources.get_object() if hasattr(resources, "get_object") else resources
        xobjects = resources.get("/XObject") or {}
        xobjects = xobjects.get_object() if hasattr(xobjects, "get_object") else xobjects
        count = 0
        for value in xobjects.values():
            obj = value.get_object() if hasattr(value, "get_object") else value
            if obj.get("/Subtype") == "/Image":
                count += 1
        return count
    except Exception:
        return 0


def analyze_pdf(path: Path) -> dict[str, Any]:
    started = time.perf_counter_ns()
    reader = pypdf.PdfReader(str(path), strict=False)
    if reader.is_encrypted and reader.decrypt("") == 0:
        raise ValueError("encrypted PDF cannot be opened with empty password")
    page_results: list[dict[str, Any]] = []
    for index, page in enumerate(reader.pages, 1):
        page_started = time.perf_counter_ns()
        try:
            text = page.extract_text() or ""
            characters = len("".join(text.split()))
            images = _count_page_images(page)
            if characters >= TEXT_PAGE_MIN_CHARS:
                page_class = "text"
            elif characters <= SCAN_PAGE_MAX_CHARS and images > 0:
                page_class = "scan_candidate"
            else:
                page_class = "sparse"
            page_results.append({"page": index, "status": "ok", "class": page_class, "text_chars": characters,
                                 "image_xobjects": images, "elapsed_ms": (time.perf_counter_ns() - page_started) / 1_000_000})
        except Exception as exc:
            page_results.append({"page": index, "status": "error", "error_type": type(exc).__name__,
                                 "error": str(exc)[:500], "elapsed_ms": (time.perf_counter_ns() - page_started) / 1_000_000})
    text_pages = sum(page.get("class") == "text" for page in page_results)
    scan_pages = sum(page.get("class") == "scan_candidate" for page in page_results)
    sparse_pages = sum(page.get("class") == "sparse" for page in page_results)
    return {"status": "ok" if all(page["status"] == "ok" for page in page_results) else "partial",
            "page_count": len(page_results), "text_pages": text_pages, "scan_candidate_pages": scan_pages,
            "sparse_pages": sparse_pages, "error_pages": sum(page["status"] == "error" for page in page_results),
            "text_chars": sum(page.get("text_chars", 0) for page in page_results),
            "document_class": classify_document(text_pages, scan_pages, sparse_pages), "pages": page_results,
            "elapsed_ms": (time.perf_counter_ns() - started) / 1_000_000}


def _worker_command(pdf_path: Path, timeout_seconds: float) -> dict[str, Any]:
    command = [sys.executable, "-m", "tooling.corpus.profile_pdfs", "--worker", str(pdf_path)]
    creationflags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
    started = time.perf_counter_ns()
    try:
        completed = subprocess.run(command, capture_output=True, text=True, encoding="utf-8", timeout=timeout_seconds,
                                   check=False, creationflags=creationflags)
    except subprocess.TimeoutExpired:
        return {"status": "timeout", "elapsed_ms": (time.perf_counter_ns() - started) / 1_000_000}
    if completed.returncode != 0:
        return {"status": "error", "error": completed.stderr.strip()[-1000:],
                "elapsed_ms": (time.perf_counter_ns() - started) / 1_000_000}
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError as exc:
        return {"status": "error", "error": f"invalid worker JSON: {exc}",
                "elapsed_ms": (time.perf_counter_ns() - started) / 1_000_000}


def _safe_pdf_path(root: Path, candidate: Candidate) -> Path:
    resolved = _resolve_under(root, candidate.storage_key, kind="storage key")
    if resolved.stat().st_size != candidate.byte_size:
        raise ValueError(f"byte size changed for {candidate.sha256}")
    if _sha256_file(resolved) != candidate.sha256:
        raise ValueError(f"content hash changed for {candidate.sha256}")
    return resolved


def _claim_output_directory(output_dir: Path) -> Path:
    output_dir.mkdir(parents=True, exist_ok=True)
    managed_outputs = (output_dir / "pdf-profile.results.jsonl", output_dir / "pdf-profile.manifest.json")
    if any(path.exists() for path in managed_outputs):
        raise FileExistsError(f"refusing to overwrite an existing profile run in {output_dir}")
    claim = output_dir / ".pdf-profile.lock"
    try:
        descriptor = os.open(claim, os.O_CREAT | os.O_EXCL | os.O_WRONLY)
    except FileExistsError as exc:
        raise FileExistsError(f"another profile run owns {output_dir}") from exc
    try:
        os.write(descriptor, f"pid={os.getpid()}\n".encode())
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    if any(path.exists() for path in managed_outputs):
        claim.unlink(missing_ok=True)
        raise FileExistsError(f"refusing to overwrite an existing profile run in {output_dir}")
    return claim


def profile(inventory_path: Path, output_dir: Path, limit: int, seed: str, workers: int, timeout_seconds: float) -> dict[str, Any]:
    claim = _claim_output_directory(output_dir)
    try:
        return _profile_claimed(inventory_path, output_dir, limit, seed, workers, timeout_seconds)
    finally:
        claim.unlink(missing_ok=True)


def _profile_claimed(inventory_path: Path, output_dir: Path, limit: int, seed: str, workers: int,
                     timeout_seconds: float) -> dict[str, Any]:
    inventory, candidates = load_candidates(inventory_path)
    selected = select_candidates(candidates, limit, seed)
    config = {"schema_version": SCHEMA_VERSION, "inventory_id": inventory["inventory_id"], "engine": "pypdf",
              "engine_version": pypdf.__version__, "sample_limit": limit, "seed": seed, "workers": workers,
              "document_timeout_seconds": timeout_seconds, "text_page_min_chars": TEXT_PAGE_MIN_CHARS,
              "scan_page_max_chars": SCAN_PAGE_MAX_CHARS}
    config_hash = _sha256(_canonical_json(config))
    root = inventory_path.parent
    results: list[dict[str, Any]] = []
    started = time.perf_counter_ns()

    def run(candidate: Candidate) -> dict[str, Any]:
        base = dataclasses.asdict(candidate) | {"size_bucket": candidate.size_bucket}
        try:
            return base | _worker_command(_safe_pdf_path(root, candidate), timeout_seconds)
        except Exception as exc:
            return base | {"status": "error", "error": str(exc)[:1000], "elapsed_ms": 0.0}

    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
        futures = [executor.submit(run, candidate) for candidate in selected]
        for future in concurrent.futures.as_completed(futures):
            results.append(future.result())
    results.sort(key=lambda item: item["sha256"])
    rows = b"".join(_canonical_json(result) for result in results)
    _atomic_write(output_dir / "pdf-profile.results.jsonl", rows)
    statuses: dict[str, int] = {}
    classes: dict[str, int] = {}
    for result in results:
        statuses[result["status"]] = statuses.get(result["status"], 0) + 1
        if result.get("document_class"):
            classes[result["document_class"]] = classes.get(result["document_class"], 0) + 1
    total_pages = sum(result.get("page_count", 0) for result in results)
    elapsed_ms = (time.perf_counter_ns() - started) / 1_000_000
    manifest = {"schema_version": SCHEMA_VERSION, "run_id": _sha256(f"{inventory['inventory_id']}\n{config_hash}\n".encode()),
                "inventory_id": inventory["inventory_id"], "config": config, "config_sha256": config_hash,
                "results_path": "pdf-profile.results.jsonl", "results_sha256": _sha256(rows),
                "selected_documents": len(selected), "status_counts": statuses, "document_classes": classes,
                "total_pages": total_pages, "total_text_chars": sum(result.get("text_chars", 0) for result in results),
                "elapsed_ms": elapsed_ms, "pages_per_second": total_pages / (elapsed_ms / 1000) if elapsed_ms else 0,
                "required_benchmark_status": "REQUIRED_UNMEASURED",
                "classification_status": "heuristic_not_gold", "generated_at_unix_ns": time.time_ns()}
    _atomic_write(output_dir / "pdf-profile.manifest.json", json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True).encode("utf-8") + b"\n")
    return manifest


def _atomic_write(path: Path, raw: bytes) -> None:
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".pending-", delete=False) as handle:
        temporary = Path(handle.name)
        handle.write(raw)
        handle.flush()
        os.fsync(handle.fileno())
    try:
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Profile verified acquisition PDFs for M01")
    parser.add_argument("--inventory", type=Path, default=Path("data/acquisition/inventory.json"))
    parser.add_argument("--output", type=Path, default=Path("artifacts/m01/pdf-profile"))
    parser.add_argument("--limit", type=int, default=120, help="0 profiles all eligible primary PDFs")
    parser.add_argument("--seed", default="m01-pdf-profile-v1")
    parser.add_argument("--workers", type=int, default=min(8, os.cpu_count() or 1))
    parser.add_argument("--timeout", type=float, default=120.0, help="hard timeout seconds per document")
    parser.add_argument("--worker", type=Path, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    if args.worker is not None:
        try:
            print(json.dumps(analyze_pdf(args.worker), ensure_ascii=False, separators=(",", ":")))
            return 0
        except Exception as exc:
            print(f"{type(exc).__name__}: {exc}", file=sys.stderr)
            return 1
    if args.limit < 0 or not 1 <= args.workers <= 32 or not math.isfinite(args.timeout) or args.timeout <= 0:
        parser.error("limit must be >=0, workers 1..32, and timeout must be finite and >0")
    try:
        manifest = profile(args.inventory, args.output, args.limit, args.seed, args.workers, args.timeout)
    except Exception as exc:
        print(f"profile failed: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(manifest, ensure_ascii=False, sort_keys=True))
    return 0 if manifest["status_counts"] == {"ok": manifest["selected_documents"]} else 1


if __name__ == "__main__":
    raise SystemExit(main())
