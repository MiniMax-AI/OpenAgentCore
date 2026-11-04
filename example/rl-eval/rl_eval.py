#!/usr/bin/env python3
"""Run batch rollouts through the OpenAgentCore Agents API and grade them outside the sandbox."""
import argparse
import base64
import collections
import concurrent.futures
import datetime
import json
import math
import os
import pathlib
import sys
import time

HERE = pathlib.Path(__file__).resolve().parent
ANSWER_PATH = "/workspace/outputs/answer.json"
TERMINAL = {"completed", "failed", "cancelled"}
POLL_SECONDS = 2
CANCEL_WAIT_SECONDS = 60


def load_tasks(root, only=()):
    """Read tasks/<id>/{instruction.md,expected.json,files/}; files become inline Environment files."""
    tasks = []
    for d in sorted(p for p in pathlib.Path(root).iterdir() if (p / "instruction.md").is_file()):
        if only and d.name not in only:
            continue
        files = [
            {"type": "inline", "path": "/workspace/" + f.relative_to(d / "files").as_posix(),
             "data": base64.b64encode(f.read_bytes()).decode()}
            for f in sorted((d / "files").rglob("*")) if f.is_file()
        ]
        tasks.append({"id": d.name, "instruction": (d / "instruction.md").read_text(),
                      "expected": json.loads((d / "expected.json").read_text()), "files": files})
    return tasks


def matches(got, want):
    if isinstance(want, bool) or isinstance(got, bool):
        return got is want
    if isinstance(want, (int, float)):
        return isinstance(got, (int, float)) and math.isclose(got, want, rel_tol=1e-6, abs_tol=1e-9)
    if isinstance(want, str):
        return isinstance(got, str) and got.strip() == want.strip()
    if isinstance(want, list):
        return isinstance(got, list) and len(got) == len(want) and all(map(matches, got, want))
    if isinstance(want, dict):
        return isinstance(got, dict) and all(k in got and matches(got[k], v) for k, v in want.items())
    return got == want


def grade(answer, expected):
    """1.0 when the answer is an object holding every expected key with a matching value, else 0.0."""
    return 1.0 if isinstance(answer, dict) and matches(answer, expected) else 0.0


def pass_at_k(rows):
    """Share of tasks with at least one reward-1 sample."""
    best = collections.defaultdict(float)
    for r in rows:
        best[r["task"]] = max(best[r["task"]], r["reward"] or 0.0)
    return sum(best.values()) / len(best) if best else 0.0


def summarize(rows):
    groups = collections.defaultdict(list)
    for r in rows:
        groups[(r["harness"], r["model"])].append(r)
    out = ["| harness | model | rollouts | mean reward | pass@k | errors/timeouts | mean duration (s) |", "| --- | --- | --- | --- | --- | --- | --- |"]
    for (harness, model), g in sorted(groups.items()):
        bad = sum(r["status"] in ("error", "timeout") for r in g)
        out.append(f"| {harness} | {model} | {len(g)} | {sum(r['reward'] for r in g) / len(g):.3f} | {pass_at_k(g):.3f} | {bad} | {sum(r['duration_s'] for r in g) / len(g):.1f} |")
    return "\n".join(out)


def latest_turn(client, sid):
    turns = client.beta.agents.sessions.turns.list(sid, order="desc", limit=1).data
    return turns[0] if turns else None


def wait_turn(client, sid, seconds):
    """Return the terminal Turn, or None when the time passes or the Session failed while preparing."""
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        turn = latest_turn(client, sid)
        if turn is not None and turn.status in TERMINAL:
            return turn
        if turn is None and client.beta.agents.sessions.retrieve(sid).status == "failed":
            raise RuntimeError("Session preparation failed")
        time.sleep(POLL_SECONDS)
    return None


def read_answer(client, sid):
    """Parse the newest /workspace/outputs/answer.json Artifact; None when absent or not JSON."""
    found = [a for a in client.beta.agents.sessions.artifacts.list(sid) if a.path == ANSWER_PATH]
    if not found:
        return None
    newest = max(found, key=lambda a: a.created_at)
    try:
        return json.loads(client.beta.agents.sessions.artifacts.content(newest.id, session_id=sid).read())
    except ValueError:
        return None


def rollout(client, job, args):
    task, harness, model, sample = job
    rid = f"{task['id']}-{sample}-{os.urandom(4).hex()}"
    row = {"rollout_id": rid, "task": task["id"], "harness": harness or "default", "model": model or "default", "sample": sample,
           "session_id": None, "status": "error", "reward": 0.0, "answer": None, "duration_s": 0.0, "usage": None, "items": [], "error": None}
    start, sid = time.monotonic(), None
    try:
        agent = {"model": model} if model else {}
        if harness:
            agent["x_agents_core"] = {"harness": harness}
        extra = {"agent": agent} if agent else {}
        if args.model_base_url:
            extra["x_agents_core"] = {"model_provider": {
                "protocol": args.model_protocol, "base_url": args.model_base_url.replace("{rollout_id}", rid),
                "api_key": os.environ["MODEL_API_KEY"]}}
        session = client.beta.agents.sessions.create(
            environment={"type": "openai_hosted", "network": {"access": "disabled"}, "files": task["files"]},
            input=task["instruction"], metadata={"rollout_id": rid, "task": task["id"]}, extra_body=extra)
        sid = row["session_id"] = session.id
        turn = wait_turn(client, sid, args.timeout)
        if turn is None:
            client.beta.agents.sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}])
            turn = wait_turn(client, sid, CANCEL_WAIT_SECONDS)
            row["status"] = "timeout"
        else:
            row["status"] = turn.status
        if turn is not None:
            row["usage"] = turn.usage.model_dump(mode="json") if turn.usage else None
        if row["status"] == "completed":
            row["answer"] = read_answer(client, sid)
            row["reward"] = grade(row["answer"], task["expected"])
        row["items"] = [i.model_dump(mode="json") for i in client.beta.agents.sessions.items.list(sid, order="asc")]
    except Exception as e:  # one failed rollout must not stop the batch
        row["status"], row["error"] = "error", f"{type(e).__name__}: {e}"
    finally:
        if sid:
            try:
                client.beta.agents.sessions.delete(sid)
            except Exception as e:
                print(f"warning: could not delete Session {sid}: {e}", file=sys.stderr)
        row["duration_s"] = round(time.monotonic() - start, 1)
    return row


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--harness", action="append", help="Harness id; repeatable. Omitted: the deployment default")
    p.add_argument("--model", action="append", help="model id; repeatable. Omitted: the installation default")
    p.add_argument("--samples", type=int, default=1, help="rollouts per task, Harness and model (K)")
    p.add_argument("--concurrency", type=int, default=4)
    p.add_argument("--tasks", default=str(HERE / "tasks"))
    p.add_argument("--task", action="append", help="run only this task id; repeatable")
    p.add_argument("--timeout", type=int, default=900, help="seconds per rollout")
    p.add_argument("--out", help="JSONL path, default runs/<UTC timestamp>.jsonl beside this script")
    p.add_argument("--model-base-url", help="your own model endpoint; {rollout_id} is replaced per rollout. The key comes from MODEL_API_KEY")
    p.add_argument("--model-protocol", choices=["anthropic", "responses", "chat_completions"], help="protocol of --model-base-url")
    args = p.parse_args()
    if args.samples < 1 or args.concurrency < 1:
        p.error("--samples and --concurrency must be at least 1")
    if args.model_base_url and not (args.model_protocol and args.model and os.environ.get("MODEL_API_KEY")):
        p.error("--model-base-url needs --model-protocol, --model and the MODEL_API_KEY environment variable")
    tasks = load_tasks(args.tasks, args.task or ())
    if not tasks:
        p.error("no tasks found")

    from openai import OpenAI  # imported here so the tests run without the package

    client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
    out = pathlib.Path(args.out) if args.out else HERE / "runs" / (datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ") + ".jsonl")
    out.parent.mkdir(parents=True, exist_ok=True)
    jobs = [(t, h, m, s) for t in tasks for h in args.harness or [None] for m in args.model or [None] for s in range(args.samples)]
    rows = []
    with out.open("a") as f, concurrent.futures.ThreadPoolExecutor(args.concurrency) as pool:
        for fut in concurrent.futures.as_completed(pool.submit(rollout, client, j, args) for j in jobs):
            row = fut.result()
            rows.append(row)
            f.write(json.dumps(row) + "\n")
            f.flush()
            print(f"[{len(rows)}/{len(jobs)}] {row['task']} {row['harness']} {row['model']} #{row['sample']}: {row['status']} reward={row['reward']} {row['duration_s']}s", flush=True)
    print(f"\nResults written to {out}\n\n{summarize(rows)}")


if __name__ == "__main__":
    main()
