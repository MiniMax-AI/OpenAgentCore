#!/usr/bin/env python3
"""Plan checks for a verified PR diff or an explicit full run; enforce their results."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess

from ci_policy import JOBS, expand, full, select


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True).stdout


def changed_paths(base, head):
    # Disable rename detection: both the deleted path and new path affect checks.
    fields = git("diff", "--no-renames", "--name-status", "-z", base, head, "--").decode("utf-8").split("\0")
    if fields.pop() != "" or len(fields) % 2:
        raise ValueError("Malformed git diff")
    if any(status not in {"A", "D", "M", "T"} for status in fields[::2]):
        raise ValueError("Unresolved diff status")
    return fields[1::2]


def event_plan(event_name, event, requested_ref="", scope="impact"):
    if scope == "full":
        if not re.fullmatch(r"[0-9a-f]{40}", requested_ref) or git("rev-parse", "HEAD").decode().strip() != requested_ref:
            raise ValueError("Full checks require the checked-out immutable source SHA")
        return full("Explicit full verification")
    if scope != "impact" or event_name != "pull_request" or requested_ref:
        raise ValueError("Impact checks require a pull_request merge checkout")
    try:
        pr = event["pull_request"]
        base, head = pr["base"]["sha"], pr["head"]["sha"]
        commit = git("cat-file", "-p", "HEAD").decode("utf-8")
        parents = [line[7:] for line in commit.split("\n\n", 1)[0].splitlines() if line.startswith("parent ")]
        if parents != [base, head]:
            raise ValueError("Checkout does not match the PR merge parents")
        return select(changed_paths(base, "HEAD"))
    except (KeyError, TypeError, ValueError, UnicodeError, subprocess.CalledProcessError) as err:
        raise ValueError(f"Cannot plan affected checks: {err}. Repair the diff or policy; no full run was started.") from err


def check_attempt(planned, current):
    if not planned or not planned.isdecimal() or int(planned) < 1 or planned != current:
        raise ValueError("The plan belongs to another attempt. Use Re-run all jobs to repeat the selected checks together.")


def validate_plan(plan):
    if not isinstance(plan, dict) or type(plan.get("version")) is not int or plan.get("version") != 1 or type(plan.get("image")) is not bool:
        raise ValueError("Invalid check plan")
    selected = plan.get("jobs")
    if not isinstance(selected, list) or any(not isinstance(j, str) for j in selected):
        raise ValueError("Invalid selected jobs")
    if len(selected) != len(set(selected)) or not set(selected) <= set(JOBS) or "hygiene" not in selected:
        raise ValueError("Invalid selected jobs")
    if plan["image"] and "api" not in selected:
        raise ValueError("Image checks require API acceptance")
    checks = set(selected) | ({"image"} if plan["image"] else set())
    if expand(checks) != checks:
        raise ValueError("Plan omits an execution prerequisite")
    return set(selected)


def check_results(plan, needs):
    selected = validate_plan(plan)
    if set(needs) != set(JOBS) | {"plan"} or needs["plan"].get("result") != "success":
        raise ValueError("Missing jobs or unsuccessful plan")
    failed = [job for job in JOBS if needs[job].get("result") != ("success" if job in selected else "skipped")]
    if failed:
        raise ValueError("Check results do not match the plan: " + ", ".join(failed))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    plan_parser = sub.add_parser("plan")
    plan_parser.add_argument("--base")
    plan_parser.add_argument("--full", action="store_true", help="Explicitly select all checks locally")
    plan_parser.add_argument("--head", default="HEAD")
    sub.add_parser("gate")
    args = parser.parse_args()
    if args.command == "gate":
        check_attempt(os.environ.get("PLAN_ATTEMPT"), os.environ.get("GITHUB_RUN_ATTEMPT"))
        check_results(json.loads(os.environ["PLAN"]), json.loads(os.environ["RESULTS"]))
        print("All checks selected by the plan passed.")
        return
    if args.full:
        plan = full("Explicit local full verification")
    elif args.base:
        plan = select(changed_paths(args.base, args.head))
    else:
        try:
            event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        except (OSError, ValueError, KeyError):
            event = {}
        plan = event_plan(os.environ.get("GITHUB_EVENT_NAME"), event, os.environ.get("REQUESTED_REF", ""), os.environ.get("CHECK_SCOPE", "impact"))
    print(json.dumps(plan, indent=2))
    if output := os.environ.get("GITHUB_OUTPUT"):
        with open(output, "a") as f:
            f.write("attempt=" + os.environ.get("GITHUB_RUN_ATTEMPT", "1") + "\n")
            f.write("plan=" + json.dumps(plan, separators=(",", ":")) + "\n")
            f.write("jobs=" + json.dumps(plan["jobs"]) + "\n")
            f.write("image=" + json.dumps(plan["image"]) + "\n")
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a") as f:
            f.write("### Selected checks\n\n```json\n" + json.dumps(plan, indent=2) + "\n```\n")


if __name__ == "__main__":
    main()
