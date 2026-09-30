import unittest
from ci_metrics import measure


class MetricsTests(unittest.TestCase):
    def test_overlap_queue_platforms_and_failed_jobs(self):
        run = {"id": 1, "head_sha": "a", "created_at": "2026-09-30T00:00:00Z", "status": "completed", "conclusion": "failure"}
        def job(start, end, label, conclusion="success"):
            return {"started_at": f"2026-09-30T00:{start}:00Z", "completed_at": f"2026-09-30T00:{end}:00Z", "labels": [label], "conclusion": conclusion, "runner_id": 1}
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

    def test_rerun_uses_its_own_start_and_only_its_jobs(self):
        run = {"id": 1, "head_sha": "a", "run_attempt": 2, "created_at": "2026-09-29T00:00:00Z",
               "run_started_at": "2026-09-30T00:00:00Z", "status": "completed", "conclusion": "success"}
        jobs = [{"started_at": "2026-09-30T00:01:00Z", "completed_at": "2026-09-30T00:03:00Z",
                 "labels": ["ubuntu-22.04"], "conclusion": "success", "run_attempt": 2, "runner_id": 1},
                {"started_at": "2026-09-29T00:01:00Z", "completed_at": "2026-09-29T00:04:00Z",
                 "labels": ["ubuntu-22.04"], "conclusion": "success", "run_attempt": 2, "runner_id": 1}]
        report = measure(run, jobs)
        self.assertEqual(report["attempt"], 2)
        self.assertEqual(report["runner_minutes"], 2)
        self.assertEqual(report["elapsed_minutes"], 3)
        self.assertEqual(report["initial_queue_seconds"], 60)
        self.assertEqual(report["job_outcomes"], {"success": 1})
        self.assertEqual(report["reused_job_outcomes"], {"success": 1})
        del run["run_started_at"]
        with self.assertRaises(ValueError):
            measure(run, jobs)

    def test_cancelled_queue_time_is_not_runner_time(self):
        run = {"id": 1, "head_sha": "a", "created_at": "2026-09-30T00:00:00Z", "status": "completed", "conclusion": "cancelled"}
        jobs = [{"started_at": "2026-09-30T00:01:00Z", "completed_at": "2026-09-30T00:03:00Z",
                 "labels": ["ubuntu-22.04"], "conclusion": "success", "runner_id": 1},
                {"started_at": "2026-09-30T00:00:00Z", "completed_at": "2026-09-30T00:04:00Z",
                 "labels": ["windows-2025"], "conclusion": "cancelled", "runner_id": 0, "runner_name": ""}]
        report = measure(run, jobs)
        self.assertEqual(report["runner_minutes"], 2)
        self.assertEqual(report["platform_minutes"], {"Linux": 2})
        self.assertEqual(report["peak_parallel_jobs"], 1)
        self.assertEqual(report["elapsed_minutes"], 4)
        self.assertEqual(report["initial_queue_seconds"], 60)
        self.assertEqual(report["job_outcomes"], {"success": 1, "cancelled": 1})


if __name__ == "__main__":
    unittest.main()
