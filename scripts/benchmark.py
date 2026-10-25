#!/usr/bin/env python3
"""Measure the sandbox lifecycle of an OpenAgentCore installation, and of E2B for comparison.

  sessions OUT --model M [--harness H]
      Drives hosted Sessions through the official OpenAI SDK, which reads OPENAI_BASE_URL and
      OPENAI_API_KEY. Writes OUT/sessions.jsonl and OUT/window.json.
  e2b OUT --template T
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

SAMPLES = 10
# Longer than the default 300 s idle timeout, so every Session has suspended.
IDLE = 420
PROMPT = "Reply with exactly the word OK and nothing else."
# The Core log fields the measurements read; every other field is dropped.
CORE_FIELDS = ("time", "msg", "allocation_id", "step", "duration_ms", "succeeded", "hit", "to")
CORE_MESSAGES = {"Runtime compute step", "Runtime compute phase saved", "Environment preparation cache", "Environment initialization step"}
# A process holding 650 MiB of memory; it writes /tmp/held once every page is touched.
HOLD = ("python3 -c \"import time; b=bytearray(650*1024*1024)\n"
        "for i in range(0, len(b), 4096): b[i]=1\n"
        "open('/tmp/held','w').write('ok'); time.sleep(3600)\"")


def records(out, name):
    """Creates OUT and returns an empty records file in it."""
    out = Path(out)
    out.mkdir(parents=True, exist_ok=True)
    (out / name).write_text("")
    return out / name


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
    if "agent.session.turn.output_text.delta" not in first:
        raise RuntimeError(f"Session {session}: the Turn produced no text; events: {sorted(first)}")
    return session, first


def sessions(args):
    from openai import OpenAI

    client = OpenAI(timeout=900, max_retries=3)
    api, templates = client.beta.agents.sessions, client.beta.agents.environments.templates
    log = records(args.out, "sessions.jsonl")
    extra = {"x_agents_core": {"harness": args.harness}} if args.harness else None
    agent = client.beta.agents.create(name="benchmark", model=args.model, tools=[], instructions="Follow the user's instructions exactly.", extra_body=extra).id
    created, completed, made = [], [], []

    def start(environment):
        session, events = turn(lambda: api.create(agent_id=agent, environment=environment, input=PROMPT, stream=True))
        created.append(api.retrieve(session).created_at)
        return session, events

    # Cold starts: each sample's Template is new, so its first Session misses the preparation cache
    # and its second hits. Deleting each Session keeps capacity free, so no start waits for a suspension.
    for sample in range(SAMPLES):
        nonce = secrets.token_hex(4)
        made.append(templates.create(
            name=f"benchmark-{nonce}", network={"access": "enabled"}, packages={"npm": ["prettier@3.3.3"]},
            files=[{"type": "inline", "path": "/workspace/notes.txt", "data": base64.b64encode(nonce.encode()).decode()}],
            setup_commands=[{"command": "prettier --version > prettier-version.txt"}]).id)
        for cache in ("miss", "hit"):
            session, events = start({"type": "openai_hosted", "environment_template_id": made[-1]})
            append(log, {"phase": "cold", "sample": sample, "cache": cache, "session": session, "events": events})
            api.delete(session)

    # Suspend and resume: once the node runs its maximum number of sandboxes, each new Session waits
    # for an idle one to suspend, and the rest suspend after the idle timeout. Each resumed Session
    # is deleted before the next resumes.
    lifecycle = []
    for sample in range(SAMPLES):
        session, events = start({"type": "openai_hosted"})
        append(log, {"phase": "start", "sample": sample, "session": session, "created": created[-1], "events": events})
        lifecycle.append(session)
    print(f"waiting {IDLE} s for the idle timeout", flush=True)
    time.sleep(IDLE)
    for sample, session in enumerate(lifecycle):
        for phase in ("resume", "warm"):
            _, events = turn(lambda: api.stream(session, input=PROMPT, timeout=900))
            append(log, {"phase": phase, "sample": sample, "session": session, "events": events})
        completed.append(api.turns.list(session, order="desc", limit=1).data[0].completed_at)
        api.delete(session)
        time.sleep(10)
    (Path(args.out) / "window.json").write_text(json.dumps({"core_start": min(created) - 1, "core_end": max(completed) + 2}) + "\n")
    for template in made:
        templates.delete(template)
    client.beta.agents.delete(agent)


def e2b(args):
    import httpx
    from e2b import Sandbox

    log = records(args.out, "e2b.jsonl")
    # The SDK's own HTTP client on one kept-alive connection; the first request only opens it.
    with httpx.Client(timeout=30) as api:
        for attempt in range(SAMPLES + 1):
            seconds, _ = clock(lambda: api.get("https://api.e2b.app/health").raise_for_status())
            if attempt:
                append(log, {"phase": "api_round_trip", "seconds": seconds})
    paused = []
    try:
        for sample in range(SAMPLES):
            record = {"phase": "lifecycle", "sample": sample}
            record["create"], sandbox = clock(lambda: Sandbox.create(template=args.template, timeout=IDLE + 600))
            paused.append((record, sandbox))
            sandbox.commands.run(HOLD, user="root", background=True)
            sandbox.commands.run("dd if=/dev/urandom of=/root/written bs=1M count=100 status=none && sync", user="root", timeout=120)
            sandbox.commands.run("while [ ! -e /tmp/held ]; do sleep 0.2; done", user="root", timeout=120)
            record["pause"], _ = clock(sandbox.pause)
        print(f"waiting {IDLE} s, as the sessions benchmark does", flush=True)
        time.sleep(IDLE)
        for record, sandbox in paused:
            record["resume_connect"], sandbox = clock(lambda: sandbox.connect(timeout=600))
            record["resume_command"], _ = clock(lambda: sandbox.commands.run("true", user="root"))
            append(log, record)
    finally:
        for _, sandbox in paused:
            try:
                sandbox.kill()
            except Exception as error:
                print(f"could not kill E2B sandbox {sandbox.sandbox_id}: {error}", flush=True)


def core_time(value):
    # Core writes RFC 3339 in UTC with up to nine fractional digits and no trailing zeros.
    whole, _, fraction = value.rstrip("Z").partition(".")
    return datetime.fromisoformat(whole + "+00:00").timestamp() + float("0." + (fraction or "0"))


def keep_core_lines(source, window, target):
    with open(source) as lines, open(target, "w") as out:
        for line in lines:
            if not line.startswith("{"):
                continue
            entry = json.loads(line)
            if entry.get("msg") in CORE_MESSAGES and window["core_start"] <= core_time(entry["time"]) <= window["core_end"]:
                kept = {key: entry[key] for key in CORE_FIELDS if key in entry}
                if "allocation_id" in kept:
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
    suspended = [core_time(e["time"]) for e in steps("suspend")]
    ready = lambda r: r["events"]["agent.session.environment.ready"] - r["events"]["agent.session.created"]
    # created_at has whole seconds, so the window starts at most a second early and gets a second more.
    waited = lambda r: any(r["created"] <= t <= r["created"] + 1 + r["events"]["agent.session.environment.ready"] for t in suspended)
    first_text = lambda phase: [r["events"]["agent.session.turn.output_text.delta"] for r in runs if r["phase"] == phase]
    lifecycle = [r for r in e2b_runs if r["phase"] == "lifecycle"]
    return {
        "Sandbox create": [e["duration_ms"] / 1000 for e in steps("create")],
        "Sandbox created → initialization start": [initialized[e["allocation_id"]] - core_time(e["time"])
                                                   for e in steps("create") if e["allocation_id"] in initialized],
        "Session created → environment ready, preparation cache miss": [ready(r) for r in runs if r["phase"] == "cold" and r["cache"] == "miss"],
        "npm install, preparation cache miss": [e["duration_ms"] / 1000 for e in core if e["msg"] == "Environment initialization step" and e["step"] == "npm"],
        "Session created → environment ready, preparation cache hit": [ready(r) for r in runs if r["phase"] == "cold" and r["cache"] == "hit"],
        "Session created → environment ready, free slot": [ready(r) for r in runs if r["phase"] == "start" and not waited(r)],
        "Session created → environment ready, full node": [ready(r) for r in runs if r["phase"] == "start" and waited(r)],
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
    run.set_defaults(func=sessions)
    vendor = commands.add_parser("e2b")
    vendor.add_argument("out")
    vendor.add_argument("--template", required=True)
    vendor.set_defaults(func=e2b)
    table = commands.add_parser("report")
    table.add_argument("out")
    table.add_argument("core_log", nargs="?")
    table.set_defaults(func=report)
    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
