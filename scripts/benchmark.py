#!/usr/bin/env python3
"""Measure the sandbox lifecycle of an OpenAgentCore installation, and of E2B for comparison.

  sessions OUT --model M [--harness H] [--samples N] [--idle S]
      Drives hosted Sessions through the official OpenAI SDK, which reads OPENAI_BASE_URL and
      OPENAI_API_KEY. Writes OUT/sessions.jsonl and OUT/window.json.
  e2b OUT --template T [--samples N] [--idle S]
      Times E2B create, pause and resume with the e2b SDK, which reads E2B_API_KEY. Writes OUT/e2b.jsonl.
  report OUT [CORE_LOG]
      With CORE_LOG, keeps the Core log lines of the sessions window that the measurements use in
      OUT/core.jsonl. Prints the results table from the records in OUT.

docs/performance.md describes each measurement and how to reproduce it.
"""

import argparse
import base64
import json
import secrets
import statistics
import time
from datetime import datetime
from pathlib import Path

PROMPT = "Reply with exactly the word OK and nothing else."
# The Core log fields the measurements read; every other field is dropped.
CORE_FIELDS = ("time", "msg", "allocation_id", "step", "duration_ms", "succeeded", "hit", "to")
CORE_MESSAGES = {"Runtime compute step", "Runtime compute phase saved", "Environment preparation cache"}
# A process holding 650 MiB plus 100 MiB written to disk, close to a sandbox after one Harness Turn.
HOLD = ("python3 -c \"import time; b=bytearray(650*1024*1024)\n"
        "for i in range(0, len(b), 4096): b[i]=1\n"
        "open('/tmp/held','w').write('ok'); time.sleep(3600)\"")


def append(path, record):
    with open(path, "a") as out:
        out.write(json.dumps(record) + "\n")


def clock(fn):
    started = time.monotonic()
    value = fn()
    return round(time.monotonic() - started, 3), value


def turn(request):
    """Sends one streamed request and returns the Session ID and how many seconds after sending it each event type first arrived."""
    started, session, first = time.monotonic(), None, {}
    with request() as stream:
        for event in stream:
            event = event.to_dict()
            first.setdefault(event.get("type"), round(time.monotonic() - started, 3))
            session = session or (event.get("session") or {}).get("id") or event.get("session_id")
    return session, first


def sessions(args):
    from openai import OpenAI

    client = OpenAI(timeout=900, max_retries=3)
    api = client.beta.agents.sessions
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    log = out / "sessions.jsonl"
    extra = {"x_agents_core": {"harness": args.harness}} if args.harness else None
    agent = client.beta.agents.create(name="benchmark", model=args.model, tools=[], instructions="Follow the user's instructions exactly.", extra_body=extra).id
    created, completed = [], []

    def start(environment):
        session, events = turn(lambda: api.create(agent_id=agent, environment=environment, input=PROMPT, stream=True))
        created.append(api.retrieve(session).created_at)
        return session, events

    # Cold starts: each sample's Template is new, so its first Session misses the preparation cache
    # and its second hits. Deleting each Session keeps capacity free, so no start waits for a suspension.
    for sample in range(args.samples):
        nonce = secrets.token_hex(4)
        template = client.beta.agents.environments.templates.create(
            name=f"benchmark-{nonce}", network={"access": "enabled"}, env={"BENCHMARK_NONCE": nonce},
            packages={"npm": ["prettier@3.3.3"]},
            files=[{"type": "inline", "path": "/workspace/notes.txt", "data": base64.b64encode(nonce.encode()).decode()}],
            setup_commands=[{"command": "prettier --version > prettier-version.txt", "cwd": "/workspace"}]).id
        for cache in ("miss", "hit"):
            session, events = start({"type": "openai_hosted", "environment_template_id": template})
            append(log, {"phase": "cold", "sample": sample, "cache": cache, "session": session, "events": events})
            api.delete(session)

    # Suspend and resume: Sessions beyond the node's capacity suspend idle ones, and the rest suspend
    # after the idle timeout. Each resumed Session is deleted before the next resumes.
    lifecycle = []
    for sample in range(args.samples):
        session, events = start({"type": "openai_hosted"})
        append(log, {"phase": "start", "sample": sample, "session": session, "events": events})
        lifecycle.append(session)
    print(f"waiting {args.idle} s for the idle timeout", flush=True)
    time.sleep(args.idle)
    for sample, session in enumerate(lifecycle):
        for phase in ("resume", "warm"):
            _, events = turn(lambda: api.stream(session, input=PROMPT, timeout=900))
            append(log, {"phase": phase, "sample": sample, "session": session, "events": events})
        completed.append(api.turns.list(session, order="desc", limit=1).data[0].completed_at)
        api.delete(session)
        time.sleep(10)
    (out / "window.json").write_text(json.dumps({"core_start": min(created) - 1, "core_end": max(completed) + 2}) + "\n")


def e2b(args):
    import httpx
    from e2b import Sandbox

    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    log = out / "e2b.jsonl"
    # The SDK's own HTTP client on one kept-alive connection; the first request only opens it.
    with httpx.Client(timeout=30) as api:
        for attempt in range(6):
            seconds, _ = clock(lambda: api.get("https://api.e2b.app/health").raise_for_status())
            if attempt:
                append(log, {"phase": "api_round_trip", "seconds": seconds})
    paused = []
    try:
        for sample in range(args.samples):
            record = {"phase": "lifecycle", "sample": sample}
            record["create"], sandbox = clock(lambda: Sandbox.create(template=args.template, timeout=args.idle + 600))
            paused.append((record, sandbox))
            sandbox.commands.run(HOLD, user="root", background=True)
            sandbox.commands.run("dd if=/dev/urandom of=/root/written bs=1M count=100 status=none && sync", user="root", timeout=120)
            while sandbox.commands.run("cat /tmp/held 2>/dev/null || true", user="root").stdout.strip() != "ok":
                time.sleep(0.5)
            record["pause"], _ = clock(sandbox.pause)
        print(f"waiting {args.idle} s, as the sessions benchmark does", flush=True)
        time.sleep(args.idle)
        for record, sandbox in paused:
            record["resume_connect"], sandbox = clock(lambda: sandbox.connect(timeout=600))
            record["resume_command"], result = clock(lambda: sandbox.commands.run("cat /tmp/held", user="root"))
            record["held_after_resume"] = result.stdout.strip() == "ok"
            append(log, record)
    finally:
        for _, sandbox in paused:
            sandbox.kill()


def core_time(value):
    # Core writes RFC 3339 with nanoseconds; datetime parses microseconds.
    return datetime.fromisoformat(value.replace("Z", "")[:26] + "+00:00").timestamp()


def keep_core_lines(source, window, target):
    with open(source) as lines, open(target, "w") as out:
        for line in lines:
            if not line.startswith("{"):
                continue
            entry = json.loads(line)
            if entry.get("msg") in CORE_MESSAGES and window["core_start"] <= core_time(entry["time"]) <= window["core_end"]:
                kept = {key: entry[key] for key in CORE_FIELDS if key in entry}
                kept["allocation_id"] = kept["allocation_id"][:8]
                out.write(json.dumps(kept) + "\n")


def read(path):
    return [json.loads(line) for line in open(path)] if path.exists() else []


def measurements(out):
    """Returns each measurement's samples in seconds, in table order."""
    core, runs, e2b_runs = read(out / "core.jsonl"), read(out / "sessions.jsonl"), read(out / "e2b.jsonl")
    steps = lambda name: [e for e in core if e["msg"] == "Runtime compute step" and e["step"] == name and e["succeeded"]]
    started = lambda e: core_time(e["time"]) - e["duration_ms"] / 1000
    initialized = {}
    for e in core:
        if e["msg"] == "Environment preparation cache" and e["step"] == "restore_preparation":
            initialized.setdefault(e["allocation_id"], started(e))
    running = {}
    for e in core:
        if e["msg"] == "Runtime compute phase saved" and e["to"] == "running":
            running.setdefault(e["allocation_id"], []).append(core_time(e["time"]))
    resumed = []
    for e in steps("resume"):
        after = [t for t in running.get(e["allocation_id"], []) if t >= core_time(e["time"])]
        if after:
            resumed.append(after[0] - started(e))
    ready = lambda cache: [r["events"]["agent.session.environment.ready"] - r["events"]["agent.session.created"]
                           for r in runs if r["phase"] == "cold" and r["cache"] == cache]
    first_text = lambda phase: [r["events"]["agent.session.turn.output_text.delta"] for r in runs if r["phase"] == phase]
    lifecycle = [r for r in e2b_runs if r["phase"] == "lifecycle"]
    return {
        "Sandbox create": [e["duration_ms"] / 1000 for e in steps("create")],
        "Sandbox created → initialization start": [initialized[e["allocation_id"]] - core_time(e["time"])
                                                   for e in steps("create") if e["allocation_id"] in initialized],
        "Session created → environment ready, preparation cache miss": ready("miss"),
        "Session created → environment ready, preparation cache hit": ready("hit"),
        "Suspend": [e["duration_ms"] / 1000 for e in steps("suspend")],
        "Resume → sandbox running": resumed,
        "Turn first text, resumed Session": first_text("resume"),
        "Turn first text, running Session": first_text("warm"),
        "E2B create": [r["create"] for r in lifecycle],
        "E2B pause": [r["pause"] for r in lifecycle],
        "E2B resume → first command": [r["resume_connect"] + r["resume_command"] for r in lifecycle],
        "E2B API round trip": [r["seconds"] for r in e2b_runs if r["phase"] == "api_round_trip"],
    }


def report(args):
    out = Path(args.out)
    if args.core_log:
        keep_core_lines(args.core_log, json.loads((out / "window.json").read_text()), out / "core.jsonl")
    print("| Measurement | Median | Range | n |\n| --- | --- | --- | --- |")
    for name, samples in measurements(out).items():
        if samples:
            print(f"| {name} | {statistics.median(samples):.2f} s | {min(samples):.2f}–{max(samples):.2f} s | {len(samples)} |")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    run = commands.add_parser("sessions")
    run.add_argument("out")
    run.add_argument("--model", required=True)
    run.add_argument("--harness")
    run.add_argument("--samples", type=int, default=10)
    run.add_argument("--idle", type=int, default=420, help="seconds to wait for the idle timeout to suspend every Session")
    run.set_defaults(func=sessions)
    vendor = commands.add_parser("e2b")
    vendor.add_argument("out")
    vendor.add_argument("--template", required=True)
    vendor.add_argument("--samples", type=int, default=10)
    vendor.add_argument("--idle", type=int, default=420)
    vendor.set_defaults(func=e2b)
    table = commands.add_parser("report")
    table.add_argument("out")
    table.add_argument("core_log", nargs="?")
    table.set_defaults(func=report)
    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
