"""Regression tests untuk sampling dan baseline profiler PDF M01.

Peran: membuktikan binding inventory, sampling lintas strata, klasifikasi eksplisit, dan confinement path.
Kontrak: fixture PDF sintetis hanya menguji perilaku tooling; ia bukan bukti kualitas parsing regulasi.
Benchmark: tidak mengukur throughput; target PARSING.* tetap REQUIRED_UNMEASURED.
Status: unit test profiler corpus aktif.
"""

import hashlib
import json
from pathlib import Path
import unittest
import uuid

from pypdf import PdfWriter

from tooling.corpus.profile_pdfs import Candidate, _safe_pdf_path, analyze_pdf, load_candidates, select_candidates


class CorpusProfileTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        Path(".cache").mkdir(exist_ok=True)

    def workspace_directory(self) -> Path:
        root = Path(".cache/test-corpus-profile") / uuid.uuid4().hex
        root.mkdir(parents=True)
        return root

    def test_sampling_is_deterministic_and_covers_groups(self):
        candidates = [
            Candidate(hashlib.sha256(f"{portal}-{size}".encode()).hexdigest(), "blobs/a.pdf", size, portal, f"https://{portal}/{size}", "2020")
            for portal in ("a.example", "b.example")
            for size in (100, 2 << 20, 20 << 20)
        ]
        first = select_candidates(candidates, 6, "seed")
        second = select_candidates(reversed(candidates), 6, "seed")
        self.assertEqual(first, second)
        self.assertEqual({(item.portal, item.size_bucket) for item in first}, {(item.portal, item.size_bucket) for item in candidates})

    def test_inventory_hash_and_receipt_shape_are_enforced(self):
        root = self.workspace_directory()
        rows = (json.dumps({"integrity_status": "valid", "portal": "a.example", "source_url": "https://a.example/1",
                            "portal_fields": {"year": ["2020"]}, "pdfs": [{"kind": "document", "sha256": "a" * 64,
                            "path": f"blobs/{'a' * 64}.pdf", "bytes": 10}]}, separators=(",", ":")) + "\n").encode()
        (root / "inventory.records.jsonl").write_bytes(rows)
        manifest = {"schema_version": 1, "integrity_valid": True, "inventory_id": "inventory-1",
                    "records_path": "inventory.records.jsonl", "records_sha256": hashlib.sha256(rows).hexdigest()}
        path = root / "inventory.json"
        path.write_text(json.dumps(manifest), encoding="utf-8")
        _, candidates = load_candidates(path)
        self.assertEqual(len(candidates), 1)
        manifest["records_sha256"] = "0" * 64
        path.write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "hash mismatch"):
            load_candidates(path)

    def test_blank_pdf_is_reported_as_sparse_without_false_text(self):
        path = self.workspace_directory() / "blank.pdf"
        writer = PdfWriter()
        writer.add_blank_page(width=612, height=792)
        with path.open("wb") as handle:
            writer.write(handle)
        result = analyze_pdf(path)
        self.assertEqual(result["status"], "ok")
        self.assertEqual(result["page_count"], 1)
        self.assertEqual(result["document_class"], "sparse_or_blank")
        self.assertEqual(result["text_chars"], 0)

    def test_storage_key_cannot_escape_inventory_root(self):
        root = self.workspace_directory()
        outside_pdf = self.workspace_directory() / "outside.pdf"
        outside_pdf.write_bytes(b"pdf")
        candidate = Candidate(hashlib.sha256(b"pdf").hexdigest(), str(outside_pdf.resolve()), 3,
                              "a.example", "https://a.example/1", "2020")
        with self.assertRaises((ValueError, OSError)):
            _safe_pdf_path(root, candidate)


if __name__ == "__main__":
    unittest.main()
