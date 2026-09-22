"""Structured final answers after real native Docker workspace/tool execution."""
import importlib.metadata
import json
from pathlib import Path
import time
import uuid


def verify_hosted_structured(client, foreign, http, model, kind, restart, evidence):
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    dist = importlib.metadata.distribution("openai")
    assert dist.version == pin["sdk_version"]
    assert json.loads(dist.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    sessions = client.beta.agents.sessions
    root = str(client.base_url).rstrip("/") + "/agents"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    other_headers = {**headers, "Authorization": "Bearer " + foreign.api_key}
    proof = {"engine": kind, "checks": [], "runs": [], "calls": []}
    owned = []
    saved = None
    schema = {"type": "object", "properties": {name: {"type": "string"} for name in ("memory", "path", "marker")},
              "required": ["memory", "path", "marker"], "additionalProperties": False}
    output_format = {"type": "json_schema", "schema": schema}
    tool = {"type": "function", "name": "remember", "description": "Return the memory value for this task.",
            "parameters": {"type": "object", "properties": {}, "additionalProperties": False}}

    def save():
        Path(evidence).write_text(json.dumps(proof, indent=2))

    def check(name):
        proof["checks"].append(name)
        save()
        print(name, flush=True)

    def until(fn, timeout=240):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = fn()
            if value:
                return value
            time.sleep(0.2)
        raise AssertionError("Public workflow did not reach expected state")

    def message(text):
        return {"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": text}]}]}

    def prompt(path, marker, recall=False):
        source = "Recall the last memory from conversation history without calling remember or reading files." if recall else "Call remember exactly once to obtain the memory."
        return source + " Use native workspace tools to write that exact memory with no newline to " + path + ". Create the parent directory if needed. Return memory, that path, and marker '" + marker + "' using the requested output format."

    def items(sid):
        response = http.get(root + "/sessions/" + sid + "/items", headers=headers, params={"order": "asc", "limit": 100})
        assert response.status_code == 200 and not response.json()["has_more"]
        raw = response.json()["data"]
        assert raw == [i.to_dict() for i in sessions.items.list(sid, order="asc", limit=100).data]
        return raw

    def respond(sid, action, memory):
        assert action.name == "remember"
        result = {"type": "agent.session.input.tool_result", "turn_id": action.turn_id, "call_id": action.call_id, "success": True, "output": memory}
        key = "result-" + action.call_id
        assert http.post(root + "/sessions/" + sid + "/events", headers=other_headers, json={"events": [result]}).status_code == 404
        sessions.events.create(sid, events=[result], idempotency_key=key)
        sessions.events.create(sid, events=[result], idempotency_key=key)
        response = http.post(root + "/sessions/" + sid + "/events", headers={**headers, "Idempotency-Key": key}, json={"events": [{**result, "output": "conflict"}]})
        assert response.status_code == 409
        proof["calls"].append(result)

    def terminal(sid, count, status="completed"):
        def finished():
            turns = sessions.turns.list(sid, order="asc", limit=100).data
            if len(turns) < count or turns[-1].status not in {"completed", "failed", "cancelled"}:
                return False
            assert len(turns) == count and turns[-1].status == status, [t.to_dict() for t in turns]
            return turns[-1]
        return until(finished)

    def final(sid, eid, turn, expected, events=None):
        stored = items(sid)
        answers = [i for i in stored if i["type"] == "message" and i.get("role") == "assistant" and i.get("phase") == "final_answer" and i["turn_id"] == turn.id]
        assert len(answers) == 1, answers
        answer = answers[0]
        raw = answer["content"][0]["text"]
        assert json.loads(raw) == expected, raw
        if events:
            added = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.item.added" and e["item"]["id"] == answer["id"])
            done = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.item.done" and e["item"]["id"] == answer["id"])
            complete = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.completed")
            assert added < done < complete
            assert next(e["text"] for e in events if e["type"] == "agent.session.turn.output_text.done" and e["item_id"] == answer["id"]) == raw
        artifacts = [a for a in sessions.artifacts.list(sid, limit=100) if a.path == expected["path"] and a.turn_id == turn.id]
        assert len(artifacts) == 1
        with sessions.artifacts.with_streaming_response.content(artifacts[0].id, session_id=sid) as response:
            assert response.read() == expected["memory"].encode()
        assert any(f.path == expected["path"] for f in client.beta.agents.environments.files.list(eid, path="/workspace/outputs"))
        assert http.get(root + "/sessions/" + sid + "/artifacts/" + artifacts[0].id + "/content", headers=other_headers).status_code == 404
        proof.setdefault("answers", []).append(answer)

    def run(sid, text, handler=None):
        events = []
        handled = False
        try:
            with sessions.events.stream(sid, timeout=300) as stream:
                sessions.events.create(sid, events=[message(text)])
                for event in stream:
                    events.append(event.to_dict())
                    if event.type == "agent.session.requires_action":
                        assert handler is not None and not handled and len(event.session.required_actions) == 1
                        handled = True
                        handler(event.session.required_actions[0])
                    assert event.type not in {"agent.session.failed", "agent.session.turn.failed"}, event.to_dict()
                    if event.type == "agent.session.idle" and any(e["type"] == "agent.session.turn.created" for e in events):
                        break
                else:
                    raise AssertionError("SSE ended without idle")
        finally:
            proof["runs"].append(events)
            save()
        assert handled == (handler is not None)
        types = [e["type"] for e in events]
        terminals = [t for t in types if t in {"agent.session.turn.completed", "agent.session.turn.cancelled"}]
        assert len(terminals) == 1 and types[-1] == "agent.session.idle"
        assert types.index("agent.session.turn.created") < types.index(terminals[0]) < len(types) - 1
        assert len({e["event_id"] for e in events}) == len(events)
        return events

    try:
        saved = client.beta.agents.create(model=model, text={"format": output_format}, tools=[tool])
        first_memory = uuid.uuid4().hex
        path = "/workspace/outputs/initial.txt"
        session = sessions.create(agent_id=saved.id, environment={"type": "openai_hosted"}, input=prompt(path, "initial"))
        sid, eid = session.id, session.environment.id
        owned.append(sid)
        proof.update(session=sid, environment=eid, agent=saved.id, format=output_format)
        assert session.agent.text.format.to_dict() == output_format
        def pending():
            current = sessions.retrieve(sid)
            assert current.status != "failed", current.to_dict()
            return current.required_actions
        action = until(pending)[0]
        respond(sid, action, first_memory)
        first = terminal(sid, 1)
        final(sid, eid, first, {"memory": first_memory, "path": path, "marker": "initial"})
        check("saved_schema_initial_function_native_file_and_final_json")

        memory = uuid.uuid4().hex
        path = "/workspace/outputs/active.txt"
        def active(action):
            incoming = [message("Use marker 'active' for the final result, replacing the earlier marker. Keep the requested file path and memory task.")]
            sessions.events.create(sid, events=incoming, idempotency_key="active-marker")
            sessions.events.create(sid, events=incoming, idempotency_key="active-marker")
            respond(sid, action, memory)
        events = run(sid, prompt(path, "obsolete"), active)
        second = terminal(sid, 2)
        final(sid, eid, second, {"memory": memory, "path": path, "marker": "active"}, events)
        outputs = [i for i in items(sid) if i["type"] == "function_call_output"]
        assert len(outputs) == 2 and [i["output"] for i in outputs] == [first_memory, memory]
        check("prepared_schema_active_receipt_function_retry_and_exact_sse")

        for suffix in ("", "/items", "/turns", "/artifacts"):
            assert http.get(root + "/sessions/" + sid + suffix, headers=other_headers).status_code == 404
        assert http.get(root + "/environments/" + eid + "/files", headers=other_headers).status_code == 404
        assert http.post(root + "/sessions", headers=other_headers, json={"agent_id": saved.id, "environment": {"type": "openai_hosted"}}).status_code == 404
        before = items(sid)
        restart(sid, eid)
        assert items(sid) == before and sessions.retrieve(sid).agent.text.format.to_dict() == output_format
        path = "/workspace/outputs/resumed.txt"
        events = run(sid, prompt(path, "resumed", recall=True))
        third = terminal(sid, 3)
        final(sid, eid, third, {"memory": memory, "path": path, "marker": "resumed"}, events)
        assert len([i for i in items(sid) if i["type"] == "function_call"]) == 2
        check("cold_core_runtime_continuation_without_replay_and_tenant_isolation")

        def cancel(action):
            for _ in range(2):
                sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}], idempotency_key="cancel-pending")
        run(sid, prompt("/workspace/outputs/cancelled.txt", "cancelled"), cancel)
        cancelled = terminal(sid, 4, "cancelled")
        assert not any(i.get("turn_id") == cancelled.id and i["type"] == "message" and i.get("phase") == "final_answer" for i in items(sid))
        assert not sessions.retrieve(sid).required_actions
        check("pending_cancellation_has_no_structured_final")

        plain = sessions.create(agent_id=saved.id, agent={"text": {"format": {"type": "text"}}}, environment={"type": "openai_hosted"})
        owned.append(plain.id)
        run(plain.id, "Do not call remember. Use native tools to write exactly PLAIN_OK to /workspace/plain.txt with no newline, then reply only PLAIN_OK.")
        assert any(i["type"] == "message" and i.get("role") == "assistant" and "PLAIN_OK" in i["content"][0].get("text", "") for i in items(plain.id))
        foreign_result = proof["calls"][0]
        assert http.post(root + "/sessions/" + plain.id + "/events", headers=headers, json={"events": [foreign_result]}).status_code in {400, 404, 409}
        artifact = list(sessions.artifacts.list(sid, limit=100))[0]
        assert http.get(root + "/sessions/" + plain.id + "/artifacts/" + artifact.id + "/content", headers=headers).status_code == 404
        check("text_override_and_same_tenant_session_isolation")

        inline = sessions.create(agent={"model": model, "text": {"format": output_format}}, environment={"type": "openai_hosted"})
        owned.append(inline.id)
        inline_memory = uuid.uuid4().hex
        path = "/workspace/outputs/inline.txt"
        events = run(inline.id, "Use native tools to create the parent directory and write exactly " + inline_memory + " with no newline to " + path + ". Return that memory, path, and marker 'inline' using the requested output format.")
        final(inline.id, inline.environment.id, terminal(inline.id, 1), {"memory": inline_memory, "path": path, "marker": "inline"}, events)
        assert inline.agent.text.format.to_dict() == output_format
        check("inline_schema_prepared_execution_native_file_and_final_json")
        proof.update(passed=True, items=items(sid), turns=[t.to_dict() for t in sessions.turns.list(sid, order="asc", limit=100).data])
        return proof["checks"]
    finally:
        save()
        for sid in reversed(owned):
            sessions.delete(sid)
        if saved:
            client.beta.agents.delete(saved.id)
