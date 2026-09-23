#!/usr/bin/env python3
"""Opt-in real-model Runtime history acceptance against an isolated deployment.

Run `collect`, restart the owned Core externally, then run `verify-restart`.
The caller owns deployment, private model configuration and final Session cleanup.
Use a dedicated report directory per provider. This never restarts any service.
Raw native/SQL sample comparisons are separate operator evidence: successive live
observations cannot be expected to have identical counter values.
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import time
import uuid


ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("installer_acceptance", ROOT / "deploy/install/acceptance.py")
base = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(base)


class SafeAPI(base.API):
    def __init__(self, origin, token, evidence, secrets):
        super().__init__(origin, token, evidence)
        self.secrets = secrets

    def request(self, *args, **kwargs):
        value = super().request(*args, **kwargs)
        encoded = json.dumps(value)
        base.require(not any(secret in encoded for secret in self.secrets), "public_response_exposed_secret")
        base.require(not any(marker in encoded for marker in (
            "postgres://", "postgresql://", "runtime_history_samples", "token_sha256",
            "unix:///", "host.microsandbox.internal", "/home/parsar-acceptance/")),
            "public_response_exposed_backend_configuration")
        return value


def stable_history(value):
    return {key: item for key, item in value.items() if key != "generated_at"}


def check_isolation(api, foreign, record, query):
    path = base.session_path(record)
    for suffix in ("/runtime-observation", "/runtime-history"):
        try:
            foreign.get(path + suffix, "foreign" + suffix, query if suffix.endswith("history") else None)
        except base.Failure:
            base.require(foreign.evidence[-1].get("http_status") == 404, "foreign_resource_not_hidden")
        else:
            raise base.Failure("foreign_resource_visible")
    page = foreign.get("/runtime-observations", "foreign.observations")
    base.require(not any(row["session_id"] == record["session_id"] for row in page["data"]),
                 "foreign_observation_list_exposed_session")
    record["checks"].append("foreign_history_and_current_observation_hidden")


def collect(api, foreign, record, config, args, save):
    base.require("session_id" not in record, "collection_already_attempted")
    capabilities = api.get("/runtime-history/capabilities", "history.capabilities")
    base.require(capabilities.get("available") is True and capabilities.get("collection_mode") == "periodic",
                 "builtin_periodic_history_unavailable")
    record["capabilities"] = capabilities
    record["start"] = int(time.time()) - 60
    nonce = uuid.uuid4().hex
    prompt = ("Use your native shell tool to run exactly once: python3 -c 'import time; "
              "end=time.monotonic()+45; x=0\nwhile time.monotonic()<end: x=(x+1)%1000003\n"
              "print(\"" + nonce + "\")'. Wait for it to finish, then reply with " + nonce + ". Do not delegate.")
    session = api.request("POST", "/sessions", "session.create", body={
        "agent": {"model": config["model"], "x_agents_core": {"harness": args.harness}},
        "environment": {"type": "openai_hosted"},
        "x_agents_core": {"model_provider": config["model_provider"]},
        "input": prompt, "stream": False}, key=uuid.uuid4().hex)
    record.update(session_id=base.identifier(session["id"]), environment_id=session["environment"]["id"])
    save()
    deadline = time.monotonic() + args.timeout
    observations = []
    completed = None
    while time.monotonic() < deadline:
        observations.append(api.get(base.session_path(record) + "/runtime-observation", "current.observe"))
        turns = api.listing(base.session_path(record) + "/turns", "turns.list")
        if turns and turns[-1]["status"] in base.TERMINAL:
            base.require(turns[-1]["status"] == "completed", "real_model_turn_failed")
            completed = turns[-1]
            break
        time.sleep(3)
    base.require(completed is not None, "real_model_turn_timeout")
    base.verify_answer(api, record, completed["id"], nonce)
    record["checks"].append("real_model_native_tool_execution_completed")
    observed = [sample for sample in observations if sample["status"] == "observed"]
    base.require(len(observed) >= 2, "insufficient_live_observations")
    base.require(all(sample["provider_type"] == args.provider for sample in observed), "wrong_runtime_provider")
    base.require(all(sample["cpu"]["usage_seconds_total"] is not None and sample["memory"]["usage_bytes"] is not None
                     for sample in observed), "missing_native_counters")
    base.require(any(sample["cpu"]["usage_seconds_total"] > observed[0]["cpu"]["usage_seconds_total"]
                     for sample in observed[1:]), "cpu_counter_did_not_advance_during_execution")
    record["observations"] = observations
    record["turn"] = completed
    record["checks"].append("provider_current_cpu_and_memory_observed_during_execution")
    time.sleep(min(capabilities["sample_interval_seconds"] + 2, 60))
    query = {"start": record["start"], "end": int(time.time()), "max_points": 120}
    history = api.get(base.session_path(record) + "/runtime-history", "history.before_restart", query)
    base.require(history.get("source") == "durable" and history["coverage"]["sample_count"] >= 2,
                 "periodic_history_not_persisted")
    base.require(bool(history["series"]), "native_resource_history_missing")
    base.require(bool(history["token_usage"]), "canonical_usage_history_missing")
    record.update(query=query, history=history)
    check_isolation(api, foreign, record, query)
    record["checks"].append("periodic_cpu_memory_and_usage_history_read")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("collect", "verify-restart"))
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--caller-key-file", type=Path, required=True)
    parser.add_argument("--foreign-key-file", type=Path, required=True)
    parser.add_argument("--model-config-file", type=Path, required=True)
    parser.add_argument("--report-dir", type=Path, required=True)
    parser.add_argument("--provider", choices=("docker", "microsandbox"), required=True)
    parser.add_argument("--harness", choices=base.HARNESSES, default="codex")
    parser.add_argument("--timeout", type=int, default=300)
    args = parser.parse_args()
    os.umask(0o077)
    token, models, secrets = base.settings(args)
    foreign_token = base.private_file(args.foreign_key_file).strip()
    secrets.append(foreign_token)
    origin = base.validate_origin(args.base_url)
    base.require(args.report_dir.is_absolute(), "absolute_report_directory_required")
    args.report_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    base.require(args.report_dir.stat().st_mode & 0o077 == 0, "report_directory_must_be_private")
    path = args.report_dir / "history.json"
    record = json.loads(base.private_file(path)) if path.exists() else {
        "provider": args.provider, "harness": args.harness, "checks": [], "stages": {}}
    base.require(record["provider"] == args.provider and record["harness"] == args.harness,
                 "report_configuration_mismatch")
    base.require(args.stage not in record["stages"], "stage_already_attempted")
    stage = {"passed": False, "requests": []}
    record["stages"][args.stage] = stage

    def save():
        encoded = json.dumps(record, indent=2) + "\n"
        base.require(not any(secret in encoded for secret in secrets), "secret_in_report")
        path.write_text(encoded)
        path.chmod(0o600)

    api = SafeAPI(origin, token, stage["requests"], secrets)
    foreign = SafeAPI(origin, foreign_token, stage["requests"], secrets)
    try:
        if args.stage == "collect":
            collect(api, foreign, record, models[args.harness], args, save)
        else:
            base.require(record["stages"].get("collect", {}).get("passed"), "collection_did_not_pass")
            history = api.get(base.session_path(record) + "/runtime-history", "history.after_restart", record["query"])
            base.require(stable_history(history) == stable_history(record["history"]), "history_changed_across_restart")
            record["after_restart_history"] = history
            check_isolation(api, foreign, record, record["query"])
            record["checks"].append("fixed_range_history_survives_operator_core_restart_and_new_reader")
        stage["passed"] = True
    except Exception as error:
        stage["failure"] = str(error) if isinstance(error, base.Failure) else "unexpected_local_or_response_error"
        stage["cleanup"] = base.settle_failed_work(api, record)
    save()
    print(args.provider + " " + args.stage + (" passed" if stage["passed"] else " FAILED: " + stage["failure"]))
    return 0 if stage["passed"] else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception:
        raise SystemExit("Acceptance configuration failed; private values withheld.") from None
