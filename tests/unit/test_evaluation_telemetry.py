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

from evaluation.telemetry import (TelemetryError, derive_observation_measurements,
                                  validate_observation_protocol)


def _observation(request_id: str, outcome: str, completion_ns: int, rejected: bool = False,
                 scheduled_nanos: int = 0, corpus_id: str = "corpus") -> pb.Observation:
    observation = json_format.ParseDict({
        "meta": {"schema_version": 1, "corpus_id": corpus_id, "record_id": request_id},
        "run_id": "run", "request_id": request_id,
        "scheduled_arrival": "2026-01-01T00:00:00Z", "outcome": outcome,
        "completion_ns": str(completion_ns), "rejected_arrival": rejected,
        "stage_durations": [{"stage": "search.dense", "duration_ns": "3000000", "queue_ns": "2000000"}],
    }, pb.Observation())
    observation.scheduled_arrival.nanos = scheduled_nanos
    return observation


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

    def test_schedule_corpus_and_stage_must_match_protocol(self):
        target = {"requests_per_run_min": 2, "requests_per_second": 10, "completion_deadline_ms": 20}
        protocol = {"measured_seconds_min": 0.2}
        valid = [
            _observation("one", "COMPLETION_STATUS_SUCCEEDED", 10_000_000, scheduled_nanos=0),
            _observation("two", "COMPLETION_STATUS_SUCCEEDED", 10_000_000, scheduled_nanos=100_000_000),
        ]
        validate_observation_protocol("run", "retrieval", valid, "corpus", target, protocol)
        invalid_schedule = [valid[0], _observation(
            "same-time", "COMPLETION_STATUS_SUCCEEDED", 10_000_000, scheduled_nanos=0)]
        with self.assertRaisesRegex(TelemetryError, "constant open-loop"):
            validate_observation_protocol("run", "retrieval", invalid_schedule, "corpus", target, protocol)
        foreign = [valid[0], _observation(
            "foreign", "COMPLETION_STATUS_SUCCEEDED", 10_000_000,
            scheduled_nanos=100_000_000, corpus_id="foreign")]
        with self.assertRaisesRegex(TelemetryError, "corpus"):
            validate_observation_protocol("run", "retrieval", foreign, "corpus", target, protocol)
        invalid_stage = [valid[0], _observation(
            "short", "COMPLETION_STATUS_SUCCEEDED", 4_000_000, scheduled_nanos=100_000_000)]
        with self.assertRaisesRegex(TelemetryError, "stage duration"):
            validate_observation_protocol("run", "retrieval", invalid_stage, "corpus", target, protocol)


if __name__ == "__main__":
    unittest.main()
