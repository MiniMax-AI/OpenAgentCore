#!/usr/bin/env python3
"""Report observed runner time and concurrency, without estimating billed cost."""

import argparse
from collections import Counter, defaultdict
from datetime import datetime
import json
import subprocess


def timestamp(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def measure(run, jobs):
    # created_at belongs to the original run; run_started_at resets on reruns.
    attempt_start = run.get("run_started_at") or (run["created_at"] if run.get("run_attempt", 1) == 1 else None)
    intervals = []
    platform_seconds = defaultdict(float)
    outcomes = Counter()
    for job in jobs:
        outcomes[job.get("conclusion") or "unfinished"] += 1
        if not job.get("started_at") or not job.get("completed_at") or job.get("conclusion") == "skipped":
            continue
        start, end = timestamp(job["started_at"]), timestamp(job["completed_at"])
        if end < start:
            raise ValueError("Job completed before it started")
        if end > start:
            intervals.extend(((start, 1), (end, -1)))
        labels = ",".join(job.get("labels", []))
        platform = "macOS" if any(x in labels.lower() for x in ("macos", "darwin")) else "Windows" if "windows" in labels.lower() else "Linux" if any(x in labels.lower() for x in ("ubuntu", "linux")) else "unknown"
        platform_seconds[platform] += end - start
    active = peak = 0
    for _, delta in sorted(intervals):
        active += delta
        peak = max(peak, active)
    starts = [time for time, delta in intervals if delta == 1]
    ends = [time for time, delta in intervals if delta == -1]
    finished = sum(outcomes[c] for c in ("success", "failure", "timed_out", "cancelled", "action_required", "startup_failure"))
    return {
        "run_id": run["id"], "attempt": run.get("run_attempt", 1), "head": run["head_sha"],
        "status": run["status"], "conclusion": run.get("conclusion"),
        "runner_minutes": round(sum(platform_seconds.values()) / 60, 2),
        "platform_minutes": {p: round(seconds / 60, 2) for p, seconds in sorted(platform_seconds.items())},
        "elapsed_minutes": round((max(ends) - timestamp(attempt_start)) / 60, 2) if ends and attempt_start else None,
        "initial_queue_seconds": min(starts) - timestamp(attempt_start) if starts and attempt_start else None,
        "peak_parallel_jobs": peak, "job_outcomes": dict(outcomes),
        "failed_job_fraction": (outcomes["failure"] + outcomes["timed_out"] + outcomes["startup_failure"]) / finished if finished else None,
    }


def api(path):
    return json.loads(subprocess.check_output(["gh", "api", path]))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("runs", nargs="+", type=int)
    parser.add_argument("--repo", default="MiniMax-AI/OpenAgentCore")
    args = parser.parse_args()
    reports = []
    for ident in args.runs:
        root = f"repos/{args.repo}/actions/runs/{ident}"
        run = api(root)
        run = api(f"{root}/attempts/{run['run_attempt']}")
        jobs = []
        page = 1
        while True:
            batch = api(f"{root}/attempts/{run['run_attempt']}/jobs?per_page=100&page={page}")["jobs"]
            jobs.extend(batch)
            if len(batch) < 100:
                break
            page += 1
        reports.append(measure(run, jobs))
    print(json.dumps(reports, indent=2))


if __name__ == "__main__":
    main()
