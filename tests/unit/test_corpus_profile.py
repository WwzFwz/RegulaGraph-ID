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
from pypdf.generic import DecodedStreamObject, DictionaryObject, NameObject, NumberObject

from tooling.corpus.profile_pdfs import Candidate, _claim_output_directory, _safe_pdf_path, analyze_pdf, load_candidates, main, profile, select_candidates


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
        records_hash = hashlib.sha256(rows).hexdigest()
        component_hashes = {"records_sha256": records_hash, "observations_sha256": "b" * 64,
                            "queue_sha256": "c" * 64, "blob_set_sha256": "d" * 64}
        identity_seed = (f"schema=1\nrecords={records_hash}\nobservations={'b' * 64}\n"
                         f"queue={'c' * 64}\nblobs={'d' * 64}\n").encode()
        manifest = {"schema_version": 1, "integrity_valid": True,
                    "inventory_id": hashlib.sha256(identity_seed).hexdigest(), "records_path": "inventory.records.jsonl"} | component_hashes
        path = root / "inventory.json"
        path.write_text(json.dumps(manifest), encoding="utf-8")
        _, candidates = load_candidates(path)
        self.assertEqual(len(candidates), 1)
        (root / "inventory.records.jsonl").write_bytes(rows + b"\n")
        with self.assertRaisesRegex(ValueError, "hash mismatch"):
            load_candidates(path)

    def test_inventory_identity_must_bind_component_hashes(self):
        root = self.workspace_directory()
        rows = b"{}\n"
        (root / "inventory.records.jsonl").write_bytes(rows)
        manifest = {"schema_version": 1, "integrity_valid": True, "inventory_id": "0" * 64,
                    "records_path": "inventory.records.jsonl", "records_sha256": hashlib.sha256(rows).hexdigest(),
                    "observations_sha256": "b" * 64, "queue_sha256": "c" * 64, "blob_set_sha256": "d" * 64}
        path = root / "inventory.json"
        path.write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "identity"):
            load_candidates(path)

    def test_integrity_flag_must_be_json_boolean_true(self):
        root = self.workspace_directory()
        path = root / "inventory.json"
        path.write_text(json.dumps({"schema_version": 1, "integrity_valid": "false"}), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "pass integrity"):
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
        mupdf_result = analyze_pdf(path, "pymupdf")
        self.assertEqual(mupdf_result["status"], "ok")
        self.assertEqual(mupdf_result["page_count"], 1)
        self.assertEqual(mupdf_result["document_class"], "sparse_or_blank")
        self.assertEqual(mupdf_result["text_chars"], 0)

    def test_unknown_engine_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "unsupported PDF engine"):
            analyze_pdf(Path("unused.pdf"), "unknown")

    def test_image_xobject_is_detected_through_indirect_resources(self):
        root = self.workspace_directory()
        source = root / "image.pdf"
        writer = PdfWriter()
        page = writer.add_blank_page(width=64, height=64)
        image = DecodedStreamObject()
        image.set_data(b"\x00")
        image.update({NameObject("/Type"): NameObject("/XObject"), NameObject("/Subtype"): NameObject("/Image"),
                      NameObject("/Width"): NumberObject(1), NameObject("/Height"): NumberObject(1),
                      NameObject("/ColorSpace"): NameObject("/DeviceGray"), NameObject("/BitsPerComponent"): NumberObject(8)})
        xobjects = DictionaryObject({NameObject("/Im0"): writer._add_object(image)})
        resources = DictionaryObject({NameObject("/XObject"): writer._add_object(xobjects)})
        page[NameObject("/Resources")] = writer._add_object(resources)
        with source.open("wb") as handle:
            writer.write(handle)
        result = analyze_pdf(source)
        self.assertGreater(result["pages"][0]["image_xobjects"], 0)

    def test_storage_key_cannot_escape_inventory_root(self):
        root = self.workspace_directory()
        outside_pdf = self.workspace_directory() / "outside.pdf"
        outside_pdf.write_bytes(b"pdf")
        candidate = Candidate(hashlib.sha256(b"pdf").hexdigest(), str(outside_pdf.resolve()), 3,
                              "a.example", "https://a.example/1", "2020")
        with self.assertRaises((ValueError, OSError)):
            _safe_pdf_path(root, candidate)

    def test_non_finite_timeout_is_rejected_by_cli(self):
        with self.assertRaises(SystemExit) as raised:
            main(["--timeout", "nan"])
        self.assertEqual(raised.exception.code, 2)

    def test_existing_profile_outputs_are_immutable(self):
        output = self.workspace_directory()
        (output / "pdf-profile.manifest.json").write_text("old", encoding="utf-8")
        with self.assertRaisesRegex(FileExistsError, "refusing to overwrite"):
            profile(output / "missing-inventory.json", output, 1, "seed", 1, 1.0)

    def test_output_directory_has_exclusive_claim(self):
        output = self.workspace_directory()
        claim = _claim_output_directory(output)
        self.assertTrue(claim.exists())
        with self.assertRaisesRegex(FileExistsError, "another profile run"):
            _claim_output_directory(output)
        claim.unlink()


if __name__ == "__main__":
    unittest.main()
