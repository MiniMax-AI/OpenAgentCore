import pathlib
import unittest

import rl_eval

HERE = pathlib.Path(__file__).resolve().parent


class GradeTest(unittest.TestCase):
    def test_numbers_use_tolerance(self):
        self.assertEqual(rl_eval.grade({"v": 100.0000001}, {"v": 100}), 1.0)
        self.assertEqual(rl_eval.grade({"v": 100.01}, {"v": 100}), 0.0)

    def test_strings_are_stripped(self):
        self.assertEqual(rl_eval.grade({"r": " South\n"}, {"r": "South"}), 1.0)
        self.assertEqual(rl_eval.grade({"r": "south"}, {"r": "South"}), 0.0)

    def test_missing_key_and_extra_keys(self):
        self.assertEqual(rl_eval.grade({"a": 1}, {"a": 1, "b": 2}), 0.0)
        self.assertEqual(rl_eval.grade({"a": 1, "b": 2, "note": "x"}, {"a": 1, "b": 2}), 1.0)

    def test_bool_is_not_a_number(self):
        self.assertEqual(rl_eval.grade({"v": True}, {"v": 1}), 0.0)
        self.assertEqual(rl_eval.grade({"v": 1}, {"v": True}), 0.0)
        self.assertEqual(rl_eval.grade({"v": True}, {"v": True}), 1.0)

    def test_nested_values(self):
        want = {"rows": [{"id": "a", "n": 1.5}, {"id": "b", "n": 2}]}
        self.assertEqual(rl_eval.grade({"rows": [{"id": "a ", "n": 1.5}, {"id": "b", "n": 2.0}]}, want), 1.0)
        self.assertEqual(rl_eval.grade({"rows": [{"id": "b", "n": 2}, {"id": "a", "n": 1.5}]}, want), 0.0)
        self.assertEqual(rl_eval.grade({"rows": [{"id": "a", "n": 1.5}]}, want), 0.0)

    def test_invalid_answers(self):
        for answer in (None, [], "text", 3):
            self.assertEqual(rl_eval.grade(answer, {"a": 1}), 0.0)


class SummaryTest(unittest.TestCase):
    def test_pass_at_k_counts_tasks_with_any_success(self):
        rows = [{"task": "a", "reward": 0.0}, {"task": "a", "reward": 1.0}, {"task": "b", "reward": 0.0}, {"task": "b", "reward": 0.0}]
        self.assertEqual(rl_eval.pass_at_k(rows), 0.5)

    def test_summary_groups_by_harness_and_model(self):
        base = {"task": "a", "sample": 0, "duration_s": 10.0}
        rows = [dict(base, harness="codex", model="m", status="completed", reward=1.0),
                dict(base, harness="codex", model="m", status="timeout", reward=0.0),
                dict(base, harness="claude_sdk", model="m", status="completed", reward=1.0)]
        table = rl_eval.summarize(rows).splitlines()
        self.assertEqual(len(table), 4)
        self.assertIn("| codex | m | 2 | 0.500 | 1.000 | 1 | 10.0 |", table)


class TaskTest(unittest.TestCase):
    def test_bundled_tasks_load(self):
        tasks = rl_eval.load_tasks(HERE / "tasks")
        self.assertEqual(len(tasks), 5)
        for t in tasks:
            self.assertIn("/workspace/outputs/answer.json", t["instruction"], t["id"])
            self.assertTrue(t["files"], t["id"])
            self.assertTrue(all(f["path"].startswith("/workspace/") for f in t["files"]), t["id"])
            self.assertIsInstance(t["expected"], dict)
            self.assertNotIn("expected.json", [f["path"] for f in t["files"]])


if __name__ == "__main__":
    unittest.main()
