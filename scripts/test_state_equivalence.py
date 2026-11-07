"""Checks that the differential verifier cannot erase meaningful text changes."""
import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("state_equivalence", Path(__file__).with_name("verify-state-equivalence.py"))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class NormalizationTests(unittest.TestCase):
    def test_business_timestamps_and_keys_are_not_wall_clocks(self):
        value = {"Text": "ran at 2026-11-07T12:00:00Z", "Error": "2026-11-07T12:00:00Z", "2026-11-07T12:00:00Z": 1}
        self.assertEqual(verifier.normalize_observation(value, {}), value)

    def test_only_generated_receipt_clock_is_removed(self):
        value = {"sentAt": "2026-11-07T12:00:00Z", "startedAt": "2026-11-07T12:00:00Z", "completedAt": None}
        self.assertEqual(verifier.normalize_observation(value, {}), {**value, "sentAt": "<wall-clock>"})

    def test_identifier_references_keep_identity_relationships(self):
        first = "11111111-1111-4111-8111-111111111111"
        other = "22222222-2222-4222-8222-222222222222"
        value = {"id": first, "input": "input:" + first, "Text": "literal " + other}
        self.assertEqual(verifier.normalize_observation(value, {}), {"id": "<id:0>", "input": "input:<id:0>", "Text": "literal " + other})

    def test_different_business_timestamps_compare_different(self):
        before = verifier.normalize_observation({"Text": "2026-11-07T12:00:00Z"}, {})
        after = verifier.normalize_observation({"Text": "2026-11-08T12:00:00Z"}, {})
        self.assertNotEqual(before, after)


if __name__ == "__main__":
    unittest.main()
