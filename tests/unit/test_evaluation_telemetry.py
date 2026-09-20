"""Verify E01 telemetry keeps failed arrivals and queue time in measurement evidence.

Peran pengujian:
Menguji transformasi Observation C01 menjadi bentuk evidence tanpa mengklaim latency produksi. Fixture
kecil tidak memenuhi minimum workload sehingga hanya membuktikan semantik derivasi, bukan gate PASS.

Kontrak:
Rejected/failed arrival tetap berada pada denominator dan latency menjadi infinity. Stage queue yang
diwajibkan ikut dijumlahkan; nama stage duplikat ditolak agar nilai tidak tertimpa diam-diam.
"""
import math
import unittest

from google.protobuf import json_format
import evaluation_pb2 as pb

from evaluation.telemetry import TelemetryError, derive_observation_measurements


def _observation(request_id: str, outcome: str, completion_ns: int, rejected: bool = False) -> pb.Observation:
    return json_format.ParseDict({
        "meta": {"schema_version": 1, "corpus_id": "corpus", "record_id": request_id},
        "run_id": "run", "request_id": request_id,
        "scheduled_arrival": "2026-01-01T00:00:00Z", "outcome": outcome,
        "completion_ns": str(completion_ns), "rejected_arrival": rejected,
        "stage_durations": [{"stage": "search.dense", "duration_ns": "3000000", "queue_ns": "2000000"}],
    }, pb.Observation())


class EvaluationTelemetryTest(unittest.TestCase):
    def test_failed_and_rejected_arrivals_remain_in_evidence(self):
        observations = [
            _observation("ok", "COMPLETION_STATUS_SUCCEEDED", 10_000_000),
            _observation("rejected", "COMPLETION_STATUS_FAILED", 0, True),
        ]
        derived = derive_observation_measurements([("run", "retrieval", observations)])
        evidence = derived["QUERY.EVIDENCE_P95"]["runs"][0]
        self.assertEqual(evidence["sample_count"], 2)
        self.assertEqual(evidence["evidence"]["samples"][0], 10)
        self.assertTrue(math.isinf(evidence["evidence"]["samples"][1]))
        self.assertEqual(derived["QUERY.SUCCESS"]["runs"][0]["evidence"], {"numerator": 1, "denominator": 2})
        self.assertEqual(derived["SEARCH.DENSE_P95"]["runs"][0]["evidence"]["samples"], [5, 5])

    def test_duplicate_stage_is_rejected(self):
        observation = _observation("duplicate", "COMPLETION_STATUS_SUCCEEDED", 1)
        observation.stage_durations.add(stage="search.dense", duration_ns=1)
        with self.assertRaisesRegex(TelemetryError, "duplicate stage"):
            derive_observation_measurements([("run", "retrieval", [observation])])


if __name__ == "__main__":
    unittest.main()
