import unittest
from ci_metrics import measure


class MetricsTests(unittest.TestCase):
    def test_overlap_queue_platforms_and_failed_jobs(self):
        run = {"id": 1, "head_sha": "a", "created_at": "2026-09-30T00:00:00Z", "status": "completed", "conclusion": "failure"}
        def job(start, end, label, conclusion="success"):
            return {"started_at": f"2026-09-30T00:{start}:00Z", "completed_at": f"2026-09-30T00:{end}:00Z", "labels": [label], "conclusion": conclusion}
        jobs = [job("01", "03", "ubuntu-22.04"), job("02", "04", "macos-15", "failure"), job("03", "05", "windows-2025"), {"conclusion": "skipped"}]
        report = measure(run, jobs)
        self.assertEqual(report["runner_minutes"], 6)
        self.assertEqual(report["elapsed_minutes"], 5)
        self.assertEqual(report["initial_queue_seconds"], 60)
        self.assertEqual(report["peak_parallel_jobs"], 2)
        self.assertEqual(report["platform_minutes"], {"Linux": 2, "Windows": 2, "macOS": 2})
        self.assertEqual(report["failed_job_fraction"], 1 / 3)

    def test_incomplete_run_is_not_reported_as_a_pass(self):
        run = {"id": 1, "head_sha": "a", "created_at": "2026-09-30T00:00:00Z", "status": "in_progress", "conclusion": None}
        report = measure(run, [{"started_at": "2026-09-30T00:01:00Z", "completed_at": None}])
        self.assertIsNone(report["conclusion"])
        self.assertIsNone(report["elapsed_minutes"])
        self.assertEqual(report["job_outcomes"], {"unfinished": 1})


if __name__ == "__main__":
    unittest.main()
