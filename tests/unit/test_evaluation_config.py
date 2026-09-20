"""Verify strict E01 loaders reject target drift and fingerprint exact source bytes.

Tests use temporary synthetic mutations of repository YAML. They do not change benchmark targets or
claim any gate result, and they perform no network, model, or database I/O.
"""
from pathlib import Path
import os
import unittest

import yaml

from evaluation.config import ConfigError, load_evaluation_config, load_profiles, load_target_suite


ROOT = Path(__file__).resolve().parents[2]


class EvaluationConfigTest(unittest.TestCase):
    def mutation_path(self, suffix: str) -> Path:
        return ROOT / ".cache" / f"evaluation-config-{os.getpid()}-{suffix}.yaml"

    def test_repository_config_and_all_gates_load(self):
        config = load_evaluation_config(ROOT / "configs/evaluation.yaml", ROOT)
        suite = load_target_suite(config.target_file)
        profiles = load_profiles(config.profiles_file)
        self.assertEqual(suite.suite_id, config.target_suite)
        self.assertEqual(len(suite.gates), 59)
        self.assertEqual(set(profiles), {"vector_rag", "hybrid_rag", "graph_rag", "hybrid_graphrag"})
        self.assertEqual(len(suite.sha256), 64)

    def test_unknown_or_relaxed_target_is_rejected(self):
        raw = yaml.safe_load((ROOT / "configs/benchmark-targets.yaml").read_text(encoding="utf-8"))
        path = self.mutation_path("relaxation")
        try:
            raw["automatic_relaxation"] = True
            path.write_text(yaml.safe_dump(raw), encoding="utf-8")
            with self.assertRaises(ConfigError):
                load_target_suite(path)
            raw["automatic_relaxation"] = False
            raw["unreviewed"] = 1
            path.write_text(yaml.safe_dump(raw), encoding="utf-8")
            with self.assertRaises(ConfigError):
                load_target_suite(path)
        finally:
            path.unlink(missing_ok=True)

    def test_gate_unit_operator_and_workload_are_strict(self):
        raw = yaml.safe_load((ROOT / "configs/benchmark-targets.yaml").read_text(encoding="utf-8"))
        path = self.mutation_path("operator")
        try:
            raw["gates"]["QUERY.EVIDENCE_P95"]["operator"] = "approximately"
            path.write_text(yaml.safe_dump(raw), encoding="utf-8")
            with self.assertRaises(ConfigError):
                load_target_suite(path)
        finally:
            path.unlink(missing_ok=True)

    def test_profile_flags_cannot_disagree_with_retrievers(self):
        raw = yaml.safe_load((ROOT / "evaluation/experiments/profiles.yaml").read_text(encoding="utf-8"))
        path = self.mutation_path("profile")
        try:
            raw["profiles"]["vector_rag"]["graph_traversal"] = True
            path.write_text(yaml.safe_dump(raw), encoding="utf-8")
            with self.assertRaises(ConfigError):
                load_profiles(path)
        finally:
            path.unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main()
