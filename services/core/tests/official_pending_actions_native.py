"""Real hosted function-action recovery through the pinned SDK and public HTTP."""

import importlib.metadata
import json
from pathlib import Path
import uuid

from session_cleanup import delete_session


def verify_pending_actions(client, foreign, http, model, evidence):
    """The private runner supplies an isolated Core/native provider deployment."""
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source["vcs_info"]["commit_id"] == pin["commit"]
    sessions = client.beta.agents.sessions
    endpoint = str(client.base_url).rstrip("/") + "/agents/sessions"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    foreign_headers = {**headers, "Authorization": "Bearer " + foreign.api_key}
    proof = {"sdk": pin, "checks": [], "rounds": [], "passed": False}
    created = []
    marker = "pending-value-" + uuid.uuid4().hex
    agent = {"model": model, "instructions": "Call lookup exactly once when asked. Never retry a failed tool call. Use no other tools.", "tools": [{
        "type": "function", "name": "lookup", "description": "Retrieve the requested test value.",
        "parameters": {"type": "object", "properties": {"key": {"type": "string"}}, "required": ["key"], "additionalProperties": False},
    }]}

    def read(path, sdk):
        response = http.get(endpoint + path, headers=headers)
        assert response.status_code == 200, (path, response.status_code)
        raw = response.json()
        assert raw == sdk.to_dict(), path
        return raw

    def items(sid):
        response = http.get(endpoint + "/" + sid + "/items", headers=headers, params={"limit": 100, "order": "asc"})
        assert response.status_code == 200
        raw = response.json()["data"]
        assert raw == [item.to_dict() for item in sessions.items.list(sid, limit=100, order="asc").data]
        return raw

    # Expected (type, code) of each public rejection; every error param is null.
    missing = ("not_found_error", "not_found_error")
    bad_target = ("invalid_request_error", "invalid_request_error")
    conflict = ("conflict_error", "conflict_error")
    key_reuse = ("conflict_error", "idempotency_conflict")

    def submit(sid, event, key, expected=202, auth=None, error=None):
        response = http.post(endpoint + "/" + sid + "/events", headers={**(auth or headers), "Idempotency-Key": key}, json={"events": [event]})
        assert response.status_code == expected, (key, response.status_code, response.text)
        if expected == 202:
            assert response.content == b""
        else:
            body = response.json()["error"]
            assert (body["type"], body["code"], body["param"]) == (*error, None), (key, body)
        return {"request": key, "status": response.status_code}

    try:
        session = sessions.create(agent=agent, environment={"type": "openai_hosted"})
        created.append(session.id)
        other = sessions.create(agent=agent, environment={"type": "openai_hosted"})
        created.append(other.id)
        proof.update(session=session.id, other_session=other.id)
        for index, mode in enumerate(("success", "error", "cancel")):
            current = {"mode": mode, "before_disconnect": [], "after_reconnect": [], "refusals": []}
            proof["rounds"].append(current)
            prompt = "Call lookup exactly once with key " + mode + ". Then reply with its returned text. If it fails, acknowledge the error without retrying."
            with sessions.events.stream(session.id, timeout=240) as stream:
                sessions.events.create(session.id, events=[{"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": prompt}]}]}], idempotency_key="pending-message-" + mode)
                for event in stream:
                    current["before_disconnect"].append(event.to_dict())
                    assert event.type not in ("agent.session.turn.failed", "agent.session.failed"), event.to_dict()
                    if event.type == "agent.session.requires_action":
                        break
                else:
                    raise AssertionError("Stream ended before a pending action")

            # The closed stream is not the recovery authority. Query both resources.
            recovered = read("/" + session.id, sessions.retrieve(session.id))
            actions = recovered["required_actions"]
            assert recovered["status"] == "requires_action" and len(actions) == 1
            action = actions[0]
            assert action == {"type": "function_call", "name": "lookup", "arguments": {"key": mode}, "call_id": action["call_id"], "turn_id": action["turn_id"]}
            turn_path = "/" + session.id + "/turns/" + action["turn_id"]
            turn = read(turn_path, sessions.turns.retrieve(action["turn_id"], session_id=session.id))
            assert turn["status"] == "waiting"
            assert sessions.turns.list(session.id).data[0].id == action["turn_id"]
            current.update(recovered_session=recovered, recovered_turn=turn, items_while_pending=items(session.id))
            event = {"type": "agent.session.input.tool_result", "turn_id": action["turn_id"], "call_id": action["call_id"], "success": mode != "error", "output": [{"type": "input_text", "text": marker}, {"type": "input_text", "text": "original second part"}]}
            if mode == "error":
                event["error"] = "original caller failure " + marker

            def pending_unchanged():
                state = sessions.retrieve(session.id)
                assert state.status == "requires_action" and [a.to_dict() for a in state.required_actions] == actions
                assert sessions.turns.retrieve(action["turn_id"], session_id=session.id).status == "waiting"
                assert not any(item["type"] == "function_call_output" and item["call_id"] == action["call_id"] for item in items(session.id))
                assert sessions.retrieve(other.id).status == "idle"
                assert sessions.turns.list(other.id).data == [] and items(other.id) == []

            # A foreign Session is not found; inside an owned Session an unknown
            # call or a call of another Turn is a request error.
            for label, target, value, auth, status, error in (
                ("wrong-tenant", session.id, event, foreign_headers, 404, missing),
                ("wrong-session", other.id, event, headers, 400, bad_target),
                ("wrong-turn", session.id, {**event, "turn_id": proof["rounds"][index - 1]["recovered_turn"]["id"] if index else str(uuid.uuid4())}, headers, 400, bad_target),
                ("malformed-turn", session.id, {**event, "turn_id": "turn_" + uuid.uuid4().hex}, headers, 400, bad_target),
                ("wrong-call", session.id, {**event, "call_id": "absent-" + uuid.uuid4().hex}, headers, 400, bad_target),
            ):
                current["refusals"].append(submit(target, value, label + "-" + mode, status, auth, error))
                pending_unchanged()
            for path in ("/" + session.id, turn_path, "/" + session.id + "/items"):
                assert http.get(endpoint + path, headers=foreign_headers).status_code == 404
            proof["checks"].append(mode + "_disconnected_query_and_target_isolation")

            # Opening another live stream does not submit or replay any input.
            with sessions.events.stream(session.id, timeout=240) as stream:
                pending_unchanged()
                submitted = {"type": "agent.session.input.cancel"} if mode == "cancel" else event
                key = "pending-submit-" + mode
                sessions.events.create(session.id, events=[submitted], idempotency_key=key)
                current["retry"] = submit(session.id, submitted, key)
                if mode != "cancel":
                    current["conflict"] = submit(session.id, {**event, "output": "changed"}, key, 409, error=key_reuse)
                    current["new_identity_conflict"] = submit(session.id, {**event, "output": "changed"}, key + "-changed", 409, error=conflict)
                for received in stream:
                    current["after_reconnect"].append(received.to_dict())
                    if received.type == "agent.session.idle":
                        break
                    assert received.type != "agent.session.failed", received.to_dict()
                else:
                    raise AssertionError("Reconnected stream ended before settlement")

            terminal = sessions.turns.retrieve(action["turn_id"], session_id=session.id)
            assert terminal.status == ("cancelled" if mode == "cancel" else "completed"), terminal.to_dict()
            settled = read("/" + session.id, sessions.retrieve(session.id))
            assert settled["status"] == "idle" and settled["required_actions"] == []
            current["terminal"] = read(turn_path, terminal)
            before_ids = {e["event_id"] for e in current["before_disconnect"]}
            after_ids = {e["event_id"] for e in current["after_reconnect"]}
            assert before_ids.isdisjoint(after_ids), "Reconnected live stream replayed previous events"
            terminal_events = [e for e in current["after_reconnect"] if e["type"] in ("agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled")]
            assert len(terminal_events) == 1 and terminal_events[0]["turn"]["id"] == action["turn_id"]
            assert terminal_events[0]["type"] == "agent.session.turn." + terminal.status
            current["terminal_retry"] = submit(session.id, submitted, key)
            if mode == "cancel":
                current["late_result"] = submit(session.id, event, key + "-late-result", 409, error=conflict)
            else:
                current["same_result_new_identity"] = submit(session.id, event, key + "-same-result")

            durable = items(session.id)
            outputs = [i for i in durable if i["type"] == "function_call_output" and i["call_id"] == action["call_id"]]
            assert len(outputs) == (0 if mode == "cancel" else 1)
            if outputs:
                assert {k: outputs[0][k] for k in ("output", "error") if k in outputs[0]} == {k: event[k] for k in ("output", "error") if k in event}
            if mode == "success":
                answers = [i for i in durable if i["type"] == "message" and i["role"] == "assistant"]
                assert marker in "\n".join(p.get("text", "") for p in answers[-1]["content"])
            assert len(sessions.turns.list(session.id).data) == index + 1
            current["durable_items"] = durable
            proof["checks"].append(mode + "_reconnect_settlement_retry_and_original_items")

        # Reads and retries leave the three native calls and two original results intact.
        durable = items(session.id)
        calls = [i for i in durable if i["type"] == "function_call"]
        assert len(calls) == 3 and len({i["call_id"] for i in calls}) == 3
        assert len([i for i in durable if i["type"] == "function_call_output"]) == 2
        assert len(sessions.turns.list(session.id).data) == 3
        assert sessions.retrieve(session.id).required_actions == []
        proof["checks"].append("durable_queries_no_implicit_call_replay")
        proof["passed"] = True
        return proof["checks"]
    finally:
        Path(evidence).write_text(json.dumps(proof, indent=2))
        for sid in reversed(created):
            delete_session(sessions, sid)
