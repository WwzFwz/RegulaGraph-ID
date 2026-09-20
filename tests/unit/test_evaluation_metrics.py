"""Exercise E01 math at threshold boundaries and reject ambiguous evidence shapes.

These deterministic cases validate calculations only; they are not benchmark observations and do not
claim model, retrieval, graph, or answer quality.
"""
import math
import unittest

from evaluation.metrics import answers, citations, graph, retrieval, runtime


class EvaluationMetricsTest(unittest.TestCase):
    def test_nearest_rank_includes_unfinished_arrival(self):
        self.assertEqual(runtime.nearest_rank(range(1, 101), 0.95).value, 95)
        self.assertTrue(math.isinf(runtime.nearest_rank([1.0] * 95 + [math.inf] * 5, 0.99).value))
        with self.assertRaises(runtime.MetricError):
            runtime.nearest_rank([], 0.95)
        with self.assertRaises(runtime.MetricError):
            runtime.nearest_rank([-1], 0.95)

    def test_count_metrics_reject_empty_or_impossible_counts(self):
        self.assertEqual(runtime.ratio(99, 100).value, 0.99)
        self.assertAlmostEqual(runtime.micro_f1(8, 1, 1).value, 8 / 9)
        with self.assertRaises(runtime.MetricError):
            runtime.ratio(0, 0)
        with self.assertRaises(runtime.MetricError):
            graph.pair_precision_recall(3, 2, 4)
        with self.assertRaises(runtime.MetricError):
            runtime.estimate("ratio", {"numerator": 1, "denominator": 1, "ignored": 1})
        with self.assertRaises(runtime.MetricError):
            runtime.estimate("minimum_slice_ratio", {"slice_counts": {"factual": []}}, ["factual"])
        with self.assertRaises(runtime.MetricError):
            runtime.estimate("macro_mean", {"group_scores": [1]})
        with self.assertRaises(runtime.MetricError):
            runtime.estimate("count", {"value": -1, "denominator": 1})

    def test_retrieval_ranking_and_required_sets(self):
        ranked = ["b", "a", "c"]
        self.assertEqual(retrieval.recall_at_k(ranked, {"a", "b"}, 2), 1)
        self.assertAlmostEqual(retrieval.ndcg_at_k(ranked, {"a": 3, "b": 2}, 2), 0.8339912324)
        self.assertTrue(retrieval.required_set_complete(ranked, [{"a", "b"}, {"z"}], 2))
        with self.assertRaises(runtime.MetricError):
            retrieval.recall_at_k(["a", "a"], {"a"}, 2)

    def test_answer_and_citation_denominators_stay_separate(self):
        self.assertEqual(answers.answer_accuracy(95, 100).value, 0.95)
        abstain, false_abstain = answers.abstention_metrics(98, 100, 2, 900)
        self.assertEqual(abstain.value, 0.98)
        self.assertAlmostEqual(false_abstain.value, 2 / 900)
        self.assertEqual(citations.citation_precision(99, 100).value, 0.99)
        self.assertEqual(citations.citation_coverage(98, 100).value, 0.98)


if __name__ == "__main__":
    unittest.main()
