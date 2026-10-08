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
from jsonschema import Draft202012Validator

from official_hosted_functions_native import verify_hosted_functions
from official_hosted_structured_native import verify_hosted_structured
from official_pending_actions_native import verify_pending_actions
from official_workspace_images_native import verify_workspace_images
from official_schema import ResponseValidator
from official_session_initial_input import verify_initial_input_retry
from session_cleanup import delete_session


def private_file(path):
    path = Path(path)
    assert path.is_absolute() and stat.S_IMODE(path.stat().st_mode) & 0o077 == 0, "Private absolute file required"
    return path.read_text().strip()


def verify_none(client, foreign, http, agent_options, session_options, ready, restart, record):
    sessions = client.beta.agents.sessions
    marker = "memory-" + uuid.uuid4().hex
    key = "create-" + uuid.uuid4().hex
    initial = "Remember " + marker + ". Reply with exactly that string. Do not use tools."
    request = {"agent": agent_options, **session_options, "input": initial}
    request.update(request.pop("extra_body", {}))
    endpoint = str(client.base_url).rstrip("/") + "/agents/sessions"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    validate = ResponseValidator(Path(__file__).resolve().parents[3] / "contracts/agents-api/openapi.yaml")
    events_schema = Draft202012Validator({"components": validate.contract["components"],
                                         "$ref": "#/components/schemas/SessionEvent"})
    session = None
    proof = {"checks": [], "runs": [], "snapshots": [], "unverified": []}
    try:
        for index in range(2):
            if index == 0:
                stream = sessions.create(agent=agent_options, **session_options, input=initial,
                                         stream=True, extra_headers={"Idempotency-Key": key}, timeout=240)
            else:
                if restart is not None:
                    restart()
                stream = sessions.stream(session.id,
                    input="Recall the string from our previous turn. Reply with exactly that string. Do not use tools.",
                    idempotency_key=key + "-continue", timeout=240)
            events = []
            proof["runs"].append(events)
            with stream:
                for event in stream:
                    value = event.to_dict()
                    events.append(value)
                    if index == 0 and len(events) == 1:
                        assert event.type == "agent.session.created"
                        session = event.session
                        proof["session"] = session.id
                        events_schema.validate(value)
                        ready(session)
                        root = endpoint + "/" + session.id
                        # Admission has committed before the first creation event.
                        for response in verify_initial_input_retry(sessions, http, endpoint,
                                {**headers, "Idempotency-Key": key}, request, session.id):
                            validate(response)
                        assert sessions.create(agent=agent_options, **session_options, input=initial,
                                               extra_headers={"Idempotency-Key": key}).id == session.id
                        for suffix in ("", "/items", "/turns"):
                            response = http.get(root + suffix, headers={**headers, "Authorization": "Bearer " + foreign.api_key})
                            assert response.status_code == 404
                            validate(response)
                    else:
                        events_schema.validate(value)
            record(proof)
            types = [event["type"] for event in events]
            terminals = [event for event in events if event["type"] in {
                "agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled"}]
            assert len(terminals) == 1 and terminals[0]["type"] == "agent.session.turn.completed"
            terminal = terminals[0]
            assert types[-1] == "agent.session.idle"
            assert types.count("agent.session.turn.created") == 1
            assert types.index("agent.session.turn.created") < types.index("agent.session.turn.completed") < len(types) - 1
            assert len({event["event_id"] for event in events}) == len(events)
            assert all("usage" not in event for event in events if event is not terminal)
            snapshots = {}
            for name in ("items", "turns"):
                response = http.get(root + "/" + name, headers=headers, params={"limit": 100, "order": "asc"})
                assert response.status_code == 200 and not response.json()["has_more"]
                validate(response)
                expected = getattr(sessions, name).list(session.id, limit=100, order="asc").to_dict()
                assert response.json() == expected
                snapshots[name] = response.json()["data"]
            response = http.get(root, headers=headers)
            assert response.status_code == 200
            validate(response)
            current = response.json()
            assert current == sessions.retrieve(session.id).to_dict()
            assert current["status"] == "idle" and current["required_actions"] == [] and current["error"] is None
            turns = snapshots["turns"]
            assert len(turns) == index + 1
            assert all(turn["status"] == "completed" and turn["error"] is None and turn["subagent_id"] is None for turn in turns)
            assert terminal["turn_id"] == turns[-1]["id"]
            assert terminal["turn"] == turns[-1] and terminal["usage"] == turns[-1]["usage"]
            answers = [item for item in snapshots["items"] if item["type"] == "message" and item["role"] == "assistant"]
            assert marker in "".join(part.get("text", "") for part in answers[-1]["content"])
            measured = [turn["usage"] for turn in turns if turn["usage"] is not None]
            for usage in measured:
                assert usage["input_tokens"] > 0 and usage["output_tokens"] > 0
                assert usage["total_tokens"] == usage["input_tokens"] + usage["output_tokens"]
                assert 0 <= usage["input_tokens_details"]["cached_tokens"] <= usage["input_tokens"]
                assert 0 <= usage["output_tokens_details"]["reasoning_tokens"] <= usage["output_tokens"]
            if len(measured) == len(turns):
                totals = {field: sum(usage[field] for usage in measured)
                          for field in ("input_tokens", "output_tokens", "total_tokens")}
                for field, detail in (("input_tokens_details", "cached_tokens"), ("output_tokens_details", "reasoning_tokens")):
                    totals[field] = {detail: sum(usage[field][detail] for usage in measured)}
                assert current["usage"] == totals
            else:
                assert current["usage"] is None
            for turn in turns:
                if turn["usage"] is None:
                    gap = "Native measured usage unavailable for Turn " + turn["id"]
                    if gap not in proof["unverified"]:
                        proof["unverified"].append(gap)
            proof["snapshots"].append({**snapshots, "session": current})
            if index == 0:
                for response in verify_initial_input_retry(sessions, http, endpoint,
                        {**headers, "Idempotency-Key": key}, request, session.id):
                    validate(response)
                proof["checks"].append("initial_input_create_retry_conflict_one_native_turn_and_foreign_history_rejection")
        proof["checks"].append("native_usage_measurements_or_explicit_unknown_and_session_totals")
        proof["checks"].append(("cold_agent_host" if restart is not None else "warm") + "_text_history_sse_and_sdk_raw_schema_parity")
        return proof["checks"]
    finally:
        record(proof)
        if session is not None:
            delete_session(sessions, session.id)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--settings", required=True, help="Private JSON with agent, model_provider and environment")
    parser.add_argument("--foreign-key-file", required=True, help="Private key of a different Project")
    parser.add_argument("--suite", required=True, choices=("none", "functions", "pending-actions", "images", "structured"))
    parser.add_argument("--evidence", required=True, type=Path, help="New evidence file under ~/.oac")
    parser.add_argument("--compose-directory", type=Path, help="Owned installation to restart for cold recovery")
    parser.add_argument("--compose-project", help="Exact owned Compose project; required with --compose-directory")
    args = parser.parse_args()
    assert bool(args.compose_directory) == bool(args.compose_project), "Supply both Compose selectors or neither"
    assert args.suite != "pending-actions" or args.compose_directory is None, "Pending actions tests client reconnect, not process restart"
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
        suites = {"none": verify_none, "functions": verify_hosted_functions, "pending-actions": verify_pending_actions,
                  "images": verify_workspace_images, "structured": verify_hosted_structured}
        try:
            kwargs = {"ready": ready, "record": record}
            if args.suite != "pending-actions":
                kwargs["restart"] = restart
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
