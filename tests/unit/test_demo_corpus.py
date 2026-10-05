"""Verify offline demo export hashes and fail-closed PDF admission on tiny PDFs.

This tests artifact preparation, not parsing accuracy or gold/legal quality.
Real sample/browser/model smoke results are recorded separately in artifacts.
"""

import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import fitz

from tooling.corpus.prepare_demo import prepare


class DemoCorpusTests(unittest.TestCase):
    def fixture(self, root: Path) -> None:
        (root / "records").mkdir(parents=True)
        (root / "blobs").mkdir()
        with fitz.open() as pdf:
            page = pdf.new_page()
            page.insert_text((60, 60), "Pasal 1. Pelindungan data pribadi. " * 5)
            raw = pdf.tobytes()
        digest = hashlib.sha256(raw).hexdigest()
        self.pdf_path = root / "blobs" / f"{digest}.pdf"
        self.pdf_path.write_bytes(raw)
        record = {"schema_version": 1, "status": "complete", "metadata": {"title": "Pelindungan data pribadi"},
                  "pdfs": [{"kind": "document", "sha256": digest, "path": f"blobs/{digest}.pdf", "bytes": len(raw)}]}
        (root / "records" / "source.json").write_text(json.dumps(record), encoding="utf-8")

    def test_manifest_pins_all_exports_and_refuses_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "input"
            self.fixture(root)
            out = Path(directory) / "output"
            report = prepare(root, out, 1, 10)
            self.assertEqual(len(report["documents"]), 1)
            self.assertEqual(report["pages"], 1)
            for line in (out / "SHA256SUMS").read_text().splitlines():
                digest, path = line.split("  ", 1)
                self.assertEqual(digest, hashlib.sha256((out / path).read_bytes()).hexdigest())
            with self.assertRaisesRegex(ValueError, "already exists"):
                prepare(root, out, 1, 10)

    def test_corrupt_pdf_cannot_publish_a_completed_export(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "input"
            self.fixture(root)
            self.pdf_path.write_bytes(b"changed")
            out = Path(directory) / "output"
            with self.assertRaisesRegex(ValueError, "No usable documents"):
                prepare(root, out, 1, 10)
            self.assertFalse((out / "SHA256SUMS").exists())


if __name__ == "__main__":
    unittest.main()
