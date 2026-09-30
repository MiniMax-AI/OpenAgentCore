"""Verify public result admission against storage fixtures, not native execution."""

import importlib.metadata
import json
from pathlib import Path
import sys
import uuid

import httpx2
from openai import APIStatusError, OpenAI

base, token, foreign, session, turn, other = sys.argv[1:]
pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
source = json.loads(importlib.metadata.distribution("openai").read_text("direct_url.json") or "{}")
assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]

# Expected (type, code, message) by status; None skips the message check.
MISSING = ("not_found_error", "not_found_error", "Resource not found.")
UNKNOWN_CALL = ("invalid_request_error", "invalid_request_error", "Unknown pending tool call.")
OTHER_TURN = ("invalid_request_error", "invalid_request_error", "The tool call belongs to a different Turn.")
INVALID = ("invalid_request_error", "invalid_request", None)
KEY_REUSE = ("conflict_error", "idempotency_conflict", None)
TURN_CONFLICT = ("conflict_error", "conflict_error", "The Turn cannot accept this input in its current state.")
CHANGED = ("conflict_error", "conflict_error", "The tool call already has a different result.")


def result(call, target_turn=None, **values):
    return {"type": "agent.session.input.tool_result", "turn_id": target_turn or turn, "call_id": call, **values}


def submit(api, events, key, expected=202, error=None, target=session):
    try:
        response = api.beta.agents.sessions.events.with_raw_response.create(
            target, events=events, extra_headers={"Idempotency-Key": key})
        assert response.status_code == expected == 202
        assert response.content == b""
    except APIStatusError as failure:
        assert failure.status_code == expected, (failure.status_code, expected, failure.message)
        kind, code, message = error
        body = failure.body
        assert (body["type"], body["code"], body["param"]) == (kind, code, None), body
        assert message is None or body["message"] == message, body


with OpenAI(api_key=token, base_url=base+"/v1", max_retries=0,
            _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=10)) as api:
    output = [{"type":"input_text","text":""}, {"type":"input_image","image_url":"data:image/png;base64,AA=="}, {"type":"input_text","text":"last"}]
    message = {"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"follow up"}]}]}
    batch = [result("a", success=False, error="failure", output=output), result("b", success=True, output=None, error=None), result("c", success=True), message, {"type":"agent.session.input.cancel"}]
    submit(api, [message, result("rollback", success=True), result("missing", success=True)], "rollback", 400, UNKNOWN_CALL)
    submit(api, [result("a", str(uuid.uuid4()), success=True)], "other-turn", 400, OTHER_TURN)
    submit(api, [message, result("rollback", "turn_malformed", success=True)], "malformed-turn", 400, OTHER_TURN)
    submit(api, [result("a", success=True)], "other-session", 400, UNKNOWN_CALL, other)
    submit(api, [message, result("rollback", success=None)], "malformed", 400, INVALID)
    stranger = api.with_options(api_key=foreign)
    submit(stranger, [result("a", success=True)], "foreign", 404, MISSING)
    submit(stranger, [result("missing", "turn_malformed", success=True)], "foreign-malformed", 404, MISSING)
    submit(api, [result("a", success=True)], "missing-session", 404, MISSING, str(uuid.uuid4()))
    submit(api, batch, "batch")
    submit(api, batch, "batch")
    print("saved", flush=True)
    assert sys.stdin.readline() == "terminal\n"
    submit(api, batch, "batch")
    submit(api, list(reversed(batch)), "batch", 409, KEY_REUSE)
    submit(api, [result("late", success=True)], "late", 409, TURN_CONFLICT)
    submit(api, [result("b", success=True)], "changed-null", 409, CHANGED)
    submit(api, [result("b", success=True, output=None, error=None)], "same-after-terminal")
