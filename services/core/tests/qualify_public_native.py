"""Real public API acceptance against an already installed, isolated Core deployment."""

import argparse
import importlib.metadata
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import time
import traceback
import uuid

import httpx2
from openai import OpenAI

from official_hosted_functions_native import verify_hosted_functions
from official_hosted_structured_native import verify_hosted_structured
from official_native_policies import verify_native_policies
from official_native_steering import verify_native_steering
from official_pending_actions_native import verify_pending_actions
from official_workspace_images_native import verify_workspace_images
from official_environment_composition import verify_composition
from session_cleanup import delete_session


def private_file(path):
    path = Path(path)
    assert path.is_absolute() and stat.S_IMODE(path.stat().st_mode) & 0o077 == 0, "Private absolute file required"
    return path.read_text().strip()


def verify_none(client, foreign, http, agent_options, session_options, ready, restart, record):
    sessions = client.beta.agents.sessions
    marker = "memory-" + uuid.uuid4().hex
    key = "create-" + uuid.uuid4().hex
    session = sessions.create(agent=agent_options, **session_options, idempotency_key=key)
    proof = {"checks": [], "session": session.id, "runs": []}
    root = str(client.base_url).rstrip("/") + "/agents/sessions/" + session.id
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    try:
        assert sessions.create(agent=agent_options, **session_options, idempotency_key=key).id == session.id
        ready(session)
        for suffix in ("", "/items", "/turns"):
            assert http.get(root + suffix, headers={**headers, "Authorization": "Bearer " + foreign.api_key}).status_code == 404
        proof["checks"].append("create_retry_and_foreign_history_rejection")
        for index, prompt in enumerate(("Remember " + marker + ". Reply with exactly that string. Do not use tools.",
                                        "Recall the string from our previous turn. Reply with exactly that string. Do not use tools.")):
            if index and restart is not None:
                restart()
            with sessions.stream(session.id, input=prompt, idempotency_key=key + str(index), timeout=240) as stream:
                events = [event.to_dict() for event in stream]
            proof["runs"].append(events)
            record(proof)
            types = [event["type"] for event in events]
            terminals = [event for event in events if event["type"] in {
                "agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled"}]
            assert len(terminals) == 1 and terminals[0]["type"] == "agent.session.turn.completed"
            assert types[-1] == "agent.session.idle"
            assert types.index("agent.session.turn.created") < types.index("agent.session.turn.completed") < len(types) - 1
            assert len({event["event_id"] for event in events}) == len(events)
            for name in ("items", "turns"):
                response = http.get(root + "/" + name, headers=headers, params={"limit": 100, "order": "asc"})
                assert response.status_code == 200 and not response.json()["has_more"]
                expected = getattr(sessions, name).list(session.id, limit=100, order="asc").to_dict()
                assert response.json() == expected
            items = list(sessions.items.list(session.id, limit=100, order="asc"))
            answers = [item.to_dict() for item in items if item.type == "message" and item.role == "assistant"]
            assert marker in "".join(part.get("text", "") for part in answers[-1]["content"])
            assert len(sessions.turns.list(session.id).data) == index + 1
            assert sessions.retrieve(session.id).required_actions == []
        proof["checks"].append(("cold_agent_host" if restart is not None else "warm") + "_text_history_sse_and_sdk_raw_parity")
        return proof["checks"]
    finally:
        record(proof)
        delete_session(sessions, session.id)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--settings", required=True, help="Private JSON with agent, model_provider and environment")
    parser.add_argument("--foreign-key-file", required=True, help="Private key of a different Project")
    parser.add_argument("--suite", required=True, choices=("none", "functions", "tool-search", "policies", "steering", "pending-actions", "images", "structured", "composition"))
    parser.add_argument("--evidence", required=True, type=Path, help="New evidence file under ~/.oac")
    parser.add_argument("--compose-directory", type=Path, help="Owned installation to restart for cold recovery")
    parser.add_argument("--compose-project", help="Exact owned Compose project; required with --compose-directory")
    args = parser.parse_args()
    assert bool(args.compose_directory) == bool(args.compose_project), "Supply both Compose selectors or neither"
    assert args.suite not in {"pending-actions", "policies", "steering"} or args.compose_directory is None, "This suite does not qualify process restart"
    evidence = args.evidence
    assert evidence.is_absolute() and not evidence.exists(), "Use a new absolute evidence path"
    assert evidence.parent.resolve().is_relative_to((Path.home() / ".oac").resolve()), "Evidence belongs under ~/.oac"
    evidence.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    settings = json.loads(private_file(args.settings))
    assert set(settings) == {"agent", "model_provider", "environment"}, "Expected agent, model_provider and environment only"
    agent = settings["agent"]
    assert set(agent) <= {"model", "x_agents_core"} and agent.get("model"), "Set the model and explicit Harness configuration"
    assert agent.get("x_agents_core", {}).get("harness"), "Explicit Harness required"
    environment = settings["environment"]
    placement = environment.get("type")
    assert placement in {"none", "self_hosted", "openai_hosted"}, "Explicit Environment required"
    if args.suite == "none":
        assert placement == "none", "The none suite requires environment:none"
    elif args.suite == "composition":
        assert environment == {"type": "openai_hosted"}, "Composition supplies its own fresh hosted preparation"
    elif args.suite != "pending-actions":
        assert placement != "none", "This suite verifies native workspace tools and Artifacts"
    if placement == "self_hosted":
        assert Path(environment["workspace_directory"]).is_absolute(), "Absolute sandbox workspace required"
    provider = settings["model_provider"]
    assert provider.get("api_key"), "Explicit provider key required"
    base, key = os.environ["OPENAI_BASE_URL"], os.environ["OPENAI_API_KEY"]
    assert base.rstrip("/").endswith("/v1") and key, "Set OPENAI_BASE_URL ending in /v1 and OPENAI_API_KEY"
    foreign_key = private_file(args.foreign_key_file)
    assert foreign_key and foreign_key != key, "Distinct Project credentials required"
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    dist = importlib.metadata.distribution("openai")
    assert dist.version == pin["sdk_version"] and json.loads(dist.read_text("direct_url.json") or "{}").get("vcs_info", {}).get("commit_id") == pin["commit"], "Install the pinned official SDK"
    secrets = (key, foreign_key, provider["api_key"])
    report = {"suite": args.suite, "harness": agent["x_agents_core"]["harness"], "placement": placement,
              "model_protocol": provider["protocol"], "sdk_commit": pin["commit"], "status": "running",
              "cold_recovery": "requested" if args.compose_directory else "unverified",
              "unverified": ["Provider lifecycle, native identity, credential isolation and unselected suites need separate qualification."]}

    def record(proof):
        report["proof"] = proof
        serialized = json.dumps(report, indent=2)
        assert all(secret not in serialized for secret in secrets), "Credential in public acceptance evidence"
        # Create privately even if the caller has a permissive umask.
        descriptor = os.open(evidence, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, "w") as output:
            output.write(serialized + "\n")

    restart = None
    if args.compose_directory:
        directory = args.compose_directory.resolve(strict=True)
        assert args.compose_directory.is_absolute() and (directory / "compose.yaml").is_file()
        assert re.fullmatch(r"[a-z0-9][a-z0-9_-]*", args.compose_project), "Exact Compose project required"

        def restart():
            command = ["docker", "compose", "--project-name", args.compose_project,
                       "--project-directory", str(directory), "--env-file", str(directory / ".env"),
                       "-f", str(directory / "compose.yaml")]
            ids = subprocess.run(command + ["ps", "-q", "agent-host"], check=True, capture_output=True, text=True, timeout=30).stdout.split()
            assert len(ids) == 1, "Exactly one running agent host in the owned project is required"
            inspect = ["docker", "inspect", "--format", "{{.State.StartedAt}}", ids[0]]
            before = subprocess.run(inspect, check=True, capture_output=True, text=True, timeout=30).stdout.strip()
            subprocess.run(command + ["restart", "agent-host"], check=True, capture_output=True, timeout=120)
            after = subprocess.run(inspect, check=True, capture_output=True, text=True, timeout=30).stdout.strip()
            assert before and after and before != after, "Agent-host process did not restart"
            report["restart"] = {"container": ids[0], "before": before, "after": after}

    with httpx2.Client(trust_env=False, timeout=30) as http:
        client = OpenAI(base_url=base, api_key=key, max_retries=0, _strict_response_validation=True, http_client=http)
        foreign = OpenAI(base_url=base, api_key=foreign_key, max_retries=0, _strict_response_validation=True, http_client=http)

        def ready(session):
            if placement == "none":
                return
            print("Session " + session.id + ": waiting for its Environment; install self-hosted Sessions through their public installation command.", flush=True)
            deadline = time.monotonic() + 300
            while time.monotonic() < deadline:
                current = client.beta.agents.environments.retrieve(session.environment.id)
                assert current.status != "failed", "Environment preparation failed"
                if current.status == "connected":
                    return
                time.sleep(0.5)
            raise AssertionError("Environment did not connect; use a separate sandbox for each Session")

        session_options = {"environment": environment, "extra_body": {"x_agents_core": {"model_provider": provider}}}
        suites = {"none": verify_none, "functions": verify_hosted_functions, "tool-search": verify_hosted_functions,
                  "policies": verify_native_policies, "steering": verify_native_steering, "pending-actions": verify_pending_actions,
                  "images": verify_workspace_images, "structured": verify_hosted_structured, "composition": verify_composition}
        try:
            kwargs = {"ready": ready, "record": record}
            if args.suite not in {"pending-actions", "policies", "steering"}:
                kwargs["restart"] = restart
            if args.suite == "tool-search":
                kwargs["deferred"] = True
            checks = suites[args.suite](client, foreign, http, agent, session_options, **kwargs)
            report.update(status="passed", checks=checks)
            if restart is not None:
                report["cold_recovery"] = "passed"
        except Exception:
            report["status"] = "failed"
            raise
        finally:
            record(report.get("proof", {}))
    print("Passed " + args.suite + "; cold recovery: " + report["cold_recovery"], flush=True)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        locations = " -> ".join(f"{Path(frame.filename).name}:{frame.lineno}" for frame in traceback.extract_tb(error.__traceback__))
        raise SystemExit(f"Public acceptance failed ({type(error).__name__} at {locations}); response bodies withheld.") from None
