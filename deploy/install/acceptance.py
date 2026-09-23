#!/usr/bin/env python3
"""Opt-in real-model acceptance of an installed Core through its public API.

Run stages in order: run, optionally restart Core/Runtime externally, continue,
then cancel. Each stage uses the same private report directory. No service or
container restart is performed here. Sessions and files are retained for inspection.

Private model configuration shape (all three harnesses are required):
{"codex": {"model": "...", "model_provider": {"protocol": "responses", ...}},
 "claude_sdk": {"model": "...", "model_provider": {"protocol": "anthropic", ...}},
 "mcode": {"model": "...", "model_provider": {"protocol": "anthropic", ...}}}
See contracts/agents-api/model-execution.md for provider fields and limits.

Example: python3 deploy/install/acceptance.py run --base-url http://127.0.0.1:8091
  --caller-key-file /private/caller.key --model-config-file /private/models.json
  --report-dir /private/release-acceptance

This calls real models and leaves owned resources in place. A failed check remains
failed; this script does not translate native differences into synthetic success.
"""

import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import stat
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener
import uuid

HARNESSES = ("codex", "claude_sdk", "mcode")
TERMINAL = {"completed", "cancelled", "failed"}
MAX_RESPONSE = 8 * 1024 * 1024


class Failure(Exception):
    """Messages are fixed labels, never remote response text or private values."""


def require(condition, label):
    if not condition:
        raise Failure(label)


def identifier(value):
    require(isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_-]{1,200}", value),
            "invalid_resource_identifier")
    return value


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def private_file(path):
    require(path.is_absolute() and not path.is_symlink(), "private_file_requires_absolute_regular_path")
    info = path.stat()
    require(stat.S_ISREG(info.st_mode) and info.st_mode & 0o077 == 0,
            "private_file_permissions_must_exclude_group_and_others")
    require(info.st_size <= MAX_RESPONSE, "private_file_too_large")
    return path.read_text()


def validate_origin(raw, model=False):
    require(isinstance(raw, str), "invalid_origin")
    parsed = urlsplit(raw)
    host = (parsed.hostname or "").lower().rstrip(".")
    require(host and not parsed.username and not parsed.password and not parsed.query
            and not parsed.fragment, "invalid_origin")
    require(host not in {"api.openai.com", "platform.openai.com"}, "official_openai_origin_refused")
    if model:
        require(parsed.scheme == "https", "model_origin_requires_https")
    else:
        try:
            loopback = ipaddress.ip_address(host).is_loopback
        except ValueError:
            loopback = host == "localhost"
        require(parsed.scheme == "https" or (parsed.scheme == "http" and loopback),
                "core_origin_requires_https_or_loopback")
    return raw.rstrip("/")


def settings(args):
    token = private_file(args.caller_key_file).strip()
    require(token and not any(c in token for c in "\r\n\0"), "invalid_caller_key")
    models = json.loads(private_file(args.model_config_file))
    require(isinstance(models, dict) and set(models) == set(HARNESSES), "require_all_three_harness_configs")
    secrets = [token]
    for harness, entry in models.items():
        require(isinstance(entry, dict) and set(entry) == {"model", "model_provider"}, "invalid_model_config_fields")
        require(isinstance(entry["model"], str) and entry["model"].strip(), "invalid_model_name")
        provider = entry["model_provider"]
        require(isinstance(provider, dict), "invalid_model_provider")
        allowed = {"protocol", "base_url", "api_key", "context_window", "max_output_tokens"}
        require(set(provider) <= allowed and {"protocol", "base_url", "api_key"} <= set(provider),
                "invalid_model_provider_fields")
        validate_origin(provider["base_url"], model=True)
        protocol = "responses" if harness == "codex" else "anthropic"
        require(provider["protocol"] == protocol, "provider_protocol_does_not_match_harness")
        key = provider["api_key"]
        require(isinstance(key, str) and key and len(key) <= 16384
                and not any(c in key for c in "\r\n\0"), "invalid_model_provider_key")
        for name in ("context_window", "max_output_tokens"):
            require(type(provider.get(name, 0)) is int and provider.get(name, 0) >= 0,
                    "invalid_model_limits")
        if harness == "mcode":
            require(provider.get("context_window", 0) > 0 and provider.get("max_output_tokens", 0) > 0,
                    "mcode_requires_positive_model_limits")
        if provider.get("context_window") and provider.get("max_output_tokens"):
            require(provider["max_output_tokens"] <= provider["context_window"], "invalid_model_limits")
        secrets.append(key)
    return token, models, secrets


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise Failure("redirect_refused")


class API:
    def __init__(self, base, token, evidence):
        self.base = base.removesuffix("/v1") + "/v1/agents"
        self.token = token
        self.evidence = evidence
        self.opener = build_opener(ProxyHandler({}), NoRedirect())

    def request(self, method, path, operation, body=None, query=None, key=None, binary=False):
        headers = {"Authorization": "Bearer " + self.token, "OpenAI-Beta": "agents=v1"}
        data = None
        if body is not None:
            headers["Content-Type"] = "application/json"
            data = json.dumps(body).encode()
        if key:
            headers["Idempotency-Key"] = key
        url = self.base + path + ("?" + urlencode(query) if query else "")
        entry = {"operation": operation, "method": method}
        self.evidence.append(entry)
        try:
            with self.opener.open(Request(url, data=data, headers=headers, method=method), timeout=30) as response:
                entry["http_status"] = response.status
                request_id = response.headers.get("x-request-id", "")
                if re.fullmatch(r"[A-Za-z0-9_.:-]{1,200}", request_id):
                    entry["request_id"] = request_id
                raw = response.read(MAX_RESPONSE + 1)
        except HTTPError as error:
            entry["http_status"] = error.code
            error.close()
            raise Failure("public_api_http_error") from None
        except (URLError, TimeoutError, OSError):
            raise Failure("public_api_transport_error") from None
        require(len(raw) <= MAX_RESPONSE, "response_limit_exceeded")
        if binary:
            return raw
        if method == "POST" and path.endswith("/events") and entry["http_status"] == 202 and not raw:
            return {}
        try:
            value = json.loads(raw)
        except (ValueError, UnicodeError):
            raise Failure("invalid_public_json") from None
        require(isinstance(value, dict), "invalid_public_object")
        return value

    def get(self, path, operation, query=None):
        return self.request("GET", path, operation, query=query)

    def listing(self, path, operation, query=None, files=False):
        query = {"limit": 100, "order": "asc", **(query or {})}
        rows, cursors = [], set()
        for _ in range(100):
            page = self.get(path, operation, query)
            require(isinstance(page.get("data"), list), "invalid_list_page")
            rows.extend(page["data"])
            require(all(isinstance(row, dict) for row in page["data"]), "invalid_list_row")
            more = page.get("has_more") is not False and bool(page.get("next")) if files else page.get("has_more")
            require(page.get("has_more") is not True or not files or more, "missing_files_cursor")
            if not more:
                return rows
            require(page["data"], "empty_continuing_page")
            cursor = page.get("next") if files else identifier(page["data"][-1].get("id"))
            require(isinstance(cursor, str) and cursor not in cursors, "invalid_list_cursor")
            cursors.add(cursor)
            query["page" if files else "after"] = cursor
        raise Failure("pagination_limit_exceeded")


def session_path(record):
    return "/sessions/" + identifier(record["session_id"])


def wait_turn(api, record, previous, timeout, status="completed"):
    deadline = time.monotonic() + timeout
    path = session_path(record)
    while time.monotonic() < deadline:
        current = api.get(path, "session.retrieve")
        require(current.get("status") != "failed", "session_failed")
        require(not current.get("required_actions"), "unexpected_required_action")
        new = [row for row in api.listing(path + "/turns", "turns.list") if row.get("id") not in previous]
        require(len(new) <= 1, "input_created_multiple_turns")
        if new and new[0].get("status") in TERMINAL:
            turn = new[0]
            record.setdefault("terminal_turns", {})[identifier(turn.get("id"))] = turn["status"]
            require(turn["status"] == status, "unexpected_terminal_turn_status")
            if current.get("status") == "idle":
                return turn
        time.sleep(1)
    raise Failure("turn_timeout")


def submit(api, record, text):
    event = {"type": "agent.session.input.message", "input": [
        {"role": "user", "content": [{"type": "input_text", "text": text}]}]}
    api.request("POST", session_path(record) + "/events", "events.message",
                body={"events": [event]}, key=uuid.uuid4().hex)


def items(api, record):
    return api.listing(session_path(record) + "/items", "items.list")


def turn_ids(api, record):
    return {identifier(row.get("id")) for row in api.listing(session_path(record) + "/turns", "turns.list")}


def file_rows(api, record, directory):
    return api.listing("/environments/" + identifier(record["environment_id"]) + "/files",
                       "files.list", {"path": directory}, files=True)


def verify_output(api, record, turn_id, path, expected):
    rows = file_rows(api, record, "/workspace/outputs")
    matching = [row for row in rows if row.get("path") == path]
    require(len(matching) == 1, "native_output_missing_from_files")
    row = matching[0]
    require(row.get("environment_id") == record["environment_id"]
            and row.get("object") == "agent.environment.file"
            and row.get("size_bytes") == len(expected), "native_output_metadata_mismatch")
    artifacts = api.listing(session_path(record) + "/artifacts", "artifacts.list")
    matches = [row for row in artifacts if row.get("path") == path and row.get("turn_id") == turn_id]
    require(len(matches) == 1, "captured_artifact_missing_or_duplicate")
    artifact = matches[0]
    aid = identifier(artifact.get("id"))
    require(artifact.get("session_id") == record["session_id"]
            and artifact.get("environment_id") == record["environment_id"]
            and artifact.get("object") == "agent.session.artifact"
            and artifact.get("size_bytes") == len(expected), "artifact_metadata_mismatch")
    endpoint = session_path(record) + "/artifacts/" + aid
    require(api.get(endpoint, "artifact.retrieve") == artifact, "artifact_retrieve_mismatch")
    content = api.request("GET", endpoint + "/content", "artifact.content", binary=True)
    require(content == expected, "artifact_bytes_mismatch")
    return {"id": aid, "path": path, "turn_id": turn_id, "size_bytes": len(content),
            "sha256": hashlib.sha256(content).hexdigest()}


def write_prompt(path, value):
    command = "mkdir -p /workspace/outputs && printf %s " + shlex.quote(value) + " > " + shlex.quote(path)
    return ("Use your native shell tool to execute exactly once: " + command
            + ". Do not delegate or repeat the command. Read the file with your native tools "
              "to verify its exact contents, then reply with those contents.")


def verify_answer(api, record, turn_id, wanted):
    stored = items(api, record)
    text = "\n".join(part.get("text", "") for row in stored
                     if row.get("turn_id") == turn_id and row.get("type") == "message"
                     and row.get("role") == "assistant" for part in row.get("content", [])
                     if part.get("type") == "output_text")
    require(wanted in text, "native_answer_did_not_contain_expected_nonce")
    return stored


def run(api, record, config, timeout, save):
    require("session_id" not in record, "run_already_created_session_use_fresh_report_directory")
    nonce = uuid.uuid4().hex
    record.update(nonce=nonce, memory="remember-" + uuid.uuid4().hex,
                  output_path="/workspace/outputs/release-" + nonce + ".txt")
    payload = {"agent": {"model": config["model"], "x_agents_core": {"harness": record["harness"]}},
               "environment": {"type": "openai_hosted"},
               "x_agents_core": {"model_provider": config["model_provider"]},
               "input": "Remember this conversation-only token without writing it to any file: "
               + record["memory"] + ". " + write_prompt(record["output_path"], nonce), "stream": False}
    creation_key = uuid.uuid4().hex
    record["creation_idempotency_key"] = creation_key
    save()
    session = api.request("POST", "/sessions", "session.create", body=payload, key=creation_key)
    record["session_id"] = identifier(session.get("id"))
    environment = session.get("environment") or {}
    record["environment_id"] = identifier(environment.get("id"))
    save()
    require(environment.get("type") == "openai_hosted", "wrong_environment_type")
    turn = wait_turn(api, record, set(), timeout)
    stored = verify_answer(api, record, turn["id"], nonce)
    record["artifact"] = verify_output(api, record, turn["id"], record["output_path"], nonce.encode())
    record["items_digest"] = digest(stored)


def continuation(api, record, timeout):
    path = session_path(record)
    current = api.get(path, "session.retrieve")
    require(current.get("status") == "idle", "continuation_requires_idle_session")
    deadline = time.monotonic() + min(timeout, 60)
    while time.monotonic() < deadline:
        environment = api.get("/environments/" + identifier(record["environment_id"]), "environment.retrieve")
        if environment.get("status") == "connected":
            break
        time.sleep(1)
    else:
        raise Failure("runtime_not_reconnected")
    require(digest(items(api, record)) == record["items_digest"], "committed_items_changed")
    old = record["artifact"]
    verify_output(api, record, old["turn_id"], old["path"], record["nonce"].encode())
    before = turn_ids(api, record)
    resumed = "/workspace/outputs/resumed-" + record["nonce"] + ".txt"
    prompt = ("Recall the exact conversation-only remember- token from our first turn without reading "
              "files or rerunning earlier commands. Write only that token, with no newline, to " + resumed
              + " using native tools. Then reply with that token. Do not delegate.")
    submit(api, record, prompt)
    turn = wait_turn(api, record, before, timeout)
    stored = verify_answer(api, record, turn["id"], record["memory"])
    record["resumed_artifact"] = verify_output(api, record, turn["id"], resumed, record["memory"].encode())
    record["items_digest"] = digest(stored)


def cancellation(api, record, timeout):
    path = session_path(record)
    require(api.get(path, "session.retrieve").get("status") == "idle", "cancel_requires_idle_session")
    before = turn_ids(api, record)
    directory = "/workspace"
    prefix = directory + "/release-cancel-" + record["nonce"]
    command = ("printf start >> " + shlex.quote(prefix + "-starts")
               + "; while true; do printf x >> " + shlex.quote(prefix + "-ticks") + "; sleep 1; done")
    submit(api, record, "Run this command exactly once with your native shell tool in the foreground: "
           + command + ". Wait for it; it runs until cancelled. Do not background, delegate or retry.")
    deadline, observed = time.monotonic() + timeout, 0
    while time.monotonic() < deadline:
        turns = [row for row in api.listing(path + "/turns", "turns.list") if row.get("id") not in before]
        require(len(turns) <= 1 and not any(row.get("status") in TERMINAL for row in turns),
                "cancel_work_finished_before_cancellation")
        rows = file_rows(api, record, directory)
        sizes = {row.get("path"): row.get("size_bytes") for row in rows}
        observed = sizes.get(prefix + "-ticks", 0)
        if type(observed) is int and observed >= 3:
            require(sizes.get(prefix + "-starts") == 5, "cancel_command_repeated")
            break
        time.sleep(1)
    else:
        raise Failure("native_cancel_effects_not_observed")
    api.request("POST", path + "/events", "events.cancel",
                body={"events": [{"type": "agent.session.input.cancel"}]}, key=uuid.uuid4().hex)
    wait_turn(api, record, before, timeout, "cancelled")
    def effects():
        return {row.get("path"): row.get("size_bytes") for row in file_rows(api, record, directory)}
    settled = effects()
    require(settled.get(prefix + "-starts") == 5 and settled.get(prefix + "-ticks", 0) >= observed,
            "cancel_effect_metadata_mismatch")
    time.sleep(3)
    require(effects() == settled, "native_effects_continued_after_cancel")
    record["cancel_effects"] = {"ticks_before_cancel": observed,
                                "ticks_after_cancel": settled[prefix + "-ticks"], "stable_seconds": 3}
    record["items_digest"] = digest(items(api, record))


def settle_failed_work(api, record):
    """Try to stop only this acceptance Session; preserve the original failure."""
    if "session_id" not in record:
        return "no_known_session"
    try:
        path = session_path(record)
        current = api.get(path, "failure_cleanup.session")
        if current.get("status") in {"idle", "failed"}:
            return "already_settled"
        api.request("POST", path + "/events", "failure_cleanup.cancel",
                    body={"events": [{"type": "agent.session.input.cancel"}]}, key=uuid.uuid4().hex)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            current = api.get(path, "failure_cleanup.session")
            if current.get("status") in {"idle", "failed"}:
                return "settled_after_cancel"
            time.sleep(1)
    except Exception:
        pass
    return "unresolved_operator_cleanup_required"


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("stage", choices=("run", "continue", "cancel"))
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--caller-key-file", required=True, type=Path)
    parser.add_argument("--model-config-file", required=True, type=Path)
    parser.add_argument("--report-dir", required=True, type=Path)
    parser.add_argument("--timeout", type=int, default=300)
    parser.add_argument("--after-restart", action="store_true", help="Record an operator-performed restart before continue.")
    args = parser.parse_args()
    require(args.timeout > 0 and (not args.after_restart or args.stage == "continue"), "invalid_stage_options")
    base = validate_origin(args.base_url)
    token, models, secrets = settings(args)
    require(args.report_dir.is_absolute() and not args.report_dir.is_symlink(), "absolute_report_directory_required")
    args.report_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    require(args.report_dir.stat().st_mode & 0o077 == 0, "report_directory_must_be_private")
    failed = False
    for harness in HARNESSES:
        location = args.report_dir / (harness + ".json")
        record = json.loads(private_file(location)) if location.exists() else {"harness": harness, "stages": {}}
        require(record.get("harness") == harness, "report_harness_mismatch")
        require(args.stage not in record["stages"], "stage_already_attempted_use_new_report_directory")
        stage = {"passed": False, "requests": []}
        record["stages"][args.stage] = stage
        def save():
            serialized = json.dumps(record, indent=2) + "\n"
            require(not any(secret in serialized for secret in secrets), "credential_detected_in_evidence")
            temp = location.with_suffix(".tmp")
            fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            try:
                with os.fdopen(fd, "w") as output:
                    output.write(serialized)
                os.replace(temp, location)
            finally:
                temp.unlink(missing_ok=True)
        api = API(base, token, stage["requests"])
        try:
            save()
            if args.stage == "run":
                run(api, record, models[harness], args.timeout, save)
            else:
                require(record["stages"].get("run", {}).get("passed"), "initial_stage_did_not_pass")
                if args.stage == "continue":
                    stage["operator_reported_restart"] = args.after_restart
                    continuation(api, record, args.timeout)
                else:
                    cancellation(api, record, args.timeout)
            stage["passed"] = True
        except Exception as error:
            failed = True
            stage["failure"] = str(error) if isinstance(error, Failure) else "unexpected_local_or_response_error"
            stage["cleanup"] = settle_failed_work(api, record)
        save()
        print(harness + ": " + args.stage + " " + ("passed" if stage["passed"] else "FAILED"), flush=True)
    return 1 if failed else 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        label = str(error) if isinstance(error, Failure) else "invalid_configuration_or_local_io"
        raise SystemExit("Acceptance stopped: " + label + "; private values and response bodies withheld.") from None
