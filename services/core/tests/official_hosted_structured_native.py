"""Structured final answers after real native workspace/tool execution."""
import importlib.metadata
from decimal import Decimal
import json
from pathlib import Path
import time
import uuid

from openai import BadRequestError

from session_cleanup import delete_session


def verify_hosted_structured(client, foreign, http, agent_options, session_options, ready, restart, record):
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    dist = importlib.metadata.distribution("openai")
    assert dist.version == pin["sdk_version"]
    assert json.loads(dist.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    sessions = client.beta.agents.sessions
    workspace = session_options["environment"].get("workspace_directory", "/workspace").rstrip("/")
    root = str(client.base_url).rstrip("/") + "/agents"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    other_headers = {**headers, "Authorization": "Bearer " + foreign.api_key}
    proof = {"engine": agent_options["x_agents_core"]["harness"], "model": agent_options["model"], "checks": [], "runs": [], "calls": []}
    owned = []
    saved = None
    schema = {"type": "object", "properties": {name: {"type": "string"} for name in ("memory", "path", "marker")},
              "required": ["memory", "path", "marker"], "additionalProperties": False}
    output_format = {"type": "json_schema", "schema": schema}
    tool = {"type": "function", "name": "remember", "description": "Return the memory value for this task.",
            "parameters": {"type": "object", "properties": {}, "additionalProperties": False}}

    def save():
        record(proof)

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
        proof.setdefault("answers", []).append(answer)
        raw = answer["content"][0]["text"]
        assert json.loads(raw, parse_float=Decimal) == expected, raw
        if events:
            added = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.item.added" and e["item"]["id"] == answer["id"])
            done = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.item.done" and e["item"]["id"] == answer["id"])
            complete = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.completed")
            assert added < done < complete
            assert next(e["text"] for e in events if e["type"] == "agent.session.turn.output_text.done" and e["item_id"] == answer["id"]) == raw
            # The native final is framed like a streamed message; deltas carry its exact text (EVT-10).
            assert events[added]["item"]["status"] == "in_progress" and events[added]["item"]["content"] == []
            deltas = [e["delta"] for e in events if e["type"] == "agent.session.turn.output_text.delta" and e["item_id"] == answer["id"]]
            assert deltas and "".join(deltas) == raw, deltas
        if "path" in expected:
            assert expected["path"].startswith(workspace + "/")
            public_path = "/workspace" + expected["path"][len(workspace):]
            artifacts = [a for a in sessions.artifacts.list(sid, limit=100) if a.path == public_path and a.turn_id == turn.id]
            assert len(artifacts) == 1
            with sessions.artifacts.with_streaming_response.content(artifacts[0].id, session_id=sid) as response:
                assert response.read() == expected["memory"].encode()
            assert any(f.path == public_path for f in client.beta.agents.environments.files.list(eid, path="/workspace/outputs"))
            assert http.get(root + "/sessions/" + sid + "/artifacts/" + artifacts[0].id + "/content", headers=other_headers).status_code == 404
        return raw

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
        saved = client.beta.agents.create(**{k: v for k, v in agent_options.items() if k != "x_agents_core"},
            extra_body={"x_agents_core": agent_options["x_agents_core"]}, text={"format": output_format}, tools=[tool])
        first_memory = uuid.uuid4().hex
        path = f"{workspace}/outputs/initial.txt"
        session = sessions.create(agent_id=saved.id, **session_options, input=prompt(path, "initial"))
        sid, eid = session.id, session.environment.id
        owned.append(sid)
        ready(session)
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
        path = f"{workspace}/outputs/active.txt"
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
        if restart is not None:
            restart()
        assert items(sid) == before and sessions.retrieve(sid).agent.text.format.to_dict() == output_format
        path = f"{workspace}/outputs/resumed.txt"
        events = run(sid, prompt(path, "resumed", recall=True))
        third = terminal(sid, 3)
        final(sid, eid, third, {"memory": memory, "path": path, "marker": "resumed"}, events)
        assert len([i for i in items(sid) if i["type"] == "function_call"]) == 2
        check(("cold_agent_host" if restart is not None else "warm") + "_continuation_without_replay_and_tenant_isolation")

        def cancel(action):
            for _ in range(2):
                sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}], idempotency_key="cancel-pending")
        run(sid, prompt(f"{workspace}/outputs/cancelled.txt", "cancelled"), cancel)
        cancelled = terminal(sid, 4, "cancelled")
        assert not any(i.get("turn_id") == cancelled.id and i["type"] == "message" and i.get("phase") == "final_answer" for i in items(sid))
        assert not sessions.retrieve(sid).required_actions
        check("pending_cancellation_has_no_structured_final")

        plain = sessions.create(agent_id=saved.id, agent={"text": {"format": {"type": "text"}}}, **session_options)
        owned.append(plain.id)
        ready(plain)
        run(plain.id, f"Do not call remember. Use native tools to write exactly PLAIN_OK to {workspace}/plain.txt with no newline, then reply only PLAIN_OK.")
        assert any(i["type"] == "message" and i.get("role") == "assistant" and "PLAIN_OK" in i["content"][0].get("text", "") for i in items(plain.id))
        foreign_result = proof["calls"][0]
        assert http.post(root + "/sessions/" + plain.id + "/events", headers=headers, json={"events": [foreign_result]}).status_code in {400, 404, 409}
        artifact = list(sessions.artifacts.list(sid, limit=100))[0]
        assert http.get(root + "/sessions/" + plain.id + "/artifacts/" + artifact.id + "/content", headers=headers).status_code == 404
        check("text_override_and_same_tenant_session_isolation")

        inline = sessions.create(agent={**agent_options, "text": {"format": output_format}}, **session_options)
        owned.append(inline.id)
        ready(inline)
        inline_memory = uuid.uuid4().hex
        path = f"{workspace}/outputs/inline.txt"
        events = run(inline.id, "Use native tools to create the parent directory and write exactly " + inline_memory + " with no newline to " + path + ". Return that memory, path, and marker 'inline' using the requested output format.")
        final(inline.id, inline.environment.id, terminal(inline.id, 1), {"memory": inline_memory, "path": path, "marker": "inline"}, events)
        assert inline.agent.text.format.to_dict() == output_format
        check("inline_schema_prepared_execution_native_file_and_final_json")
        numeric_schema = {"type": "object", "properties": {"n": {"type": "integer"}},
                          "required": ["n"], "additionalProperties": False}
        exact_schema = {**numeric_schema, "properties": {"n": {"type": "integer", "const": 9007199254740992}}}
        for source, selected_schema, number in (("saved", exact_schema, 9007199254740992),
                                                ("inline", exact_schema, 9007199254740992),
                                                ("inline", numeric_schema, 9007199254740993)):
            selected_format = {"type": "json_schema", "schema": selected_schema}
            if source == "saved":
                client.beta.agents.update(saved.id, tools=[], text={"format": selected_format})
                assert client.beta.agents.retrieve(saved.id).text.format.to_dict() == selected_format
                numeric = sessions.create(agent_id=saved.id, **session_options)
            else:
                numeric = sessions.create(agent={**agent_options, "text": {"format": selected_format}}, **session_options)
            owned.append(numeric.id)
            ready(numeric)
            assert sessions.retrieve(numeric.id).agent.text.format.to_dict() == selected_format
            proof.setdefault("numeric", []).append({"source": source, "session": numeric.id, "format": selected_format,
                                                     "expected_integer": number, "model_dependent_output": True})
            events = run(numeric.id, f"Return the JSON object with key n and the exact integer {number}. Preserve every digit and use the required output format. Do not use workspace tools or public functions.")
            raw = final(numeric.id, numeric.environment.id, terminal(numeric.id, 1), {"n": number}, events)
            assert sessions.retrieve(numeric.id).agent.text.format.to_dict() == selected_format
            proof["numeric"][-1]["raw_final_text"] = raw
            check(source + ("_exact_binary64_schema_constant" if selected_schema is exact_schema else "_large_integer_final_text_sse_and_storage"))

        # A saved Agent with an explicit Harness validates that selection at
        # update; inline Session configuration validates it at admission.
        if proof["engine"] == "claude_sdk":
            unsafe_format = {"type": "json_schema", "schema": {**numeric_schema,
                             "properties": {"n": {"type": "integer", "const": 9007199254740993}}}}
            before = client.beta.agents.retrieve(saved.id).to_dict()
            update = {"tools": [], "text": {"format": unsafe_format}}
            update_error = {"type": "invalid_request_error", "code": "unsupported_or_invalid_configuration",
                            "param": "text.format", "message": "This runtime requires an object schema with lossless JSON numbers."}
            response = http.post(root + "/" + saved.id, headers=headers, json=update)
            assert response.status_code == 400 and response.json() == {"error": update_error}, response.text
            assert client.beta.agents.retrieve(saved.id).to_dict() == before
            try:
                client.beta.agents.update(saved.id, **update)
            except BadRequestError as error:
                assert error.status_code == 400 and error.body == update_error, error.body
            else:
                raise AssertionError("Lossy numeric schema update was admitted")
            assert client.beta.agents.retrieve(saved.id).to_dict() == before
            proof.setdefault("numeric_rejections", []).append({"source": "saved_agent_update", "format": unsafe_format,
                                                                "error": response.json(), "saved_agent_unchanged": True})
            check("saved_agent_lossy_numeric_schema_rejected_without_mutation")
            expected_error = {"type": "invalid_request_error", "code": "unsupported_or_invalid_configuration",
                              "param": "agent.text.format", "message": "Harness claude_sdk does not support the requested Agent/environment configuration: This runtime requires an object schema with lossless JSON numbers."}
            existing = {session.id for session in sessions.list()}
            request = {"agent": {**agent_options, "text": {"format": unsafe_format}}, **session_options}
            wire = {**{k: v for k, v in request.items() if k != "extra_body"}, **request.get("extra_body", {})}
            response = http.post(root + "/sessions", headers=headers, json=wire)
            if response.status_code == 201:
                owned.append(response.json()["id"])
            assert response.status_code == 400 and response.json() == {"error": expected_error}, response.text
            try:
                unexpected = sessions.create(**request)
            except BadRequestError as error:
                assert error.status_code == 400 and error.body == expected_error, error.body
            else:
                owned.append(unexpected.id)
                raise AssertionError("Lossy numeric schema was admitted")
            assert {session.id for session in sessions.list()} == existing
            proof.setdefault("numeric_rejections", []).append({"source": "inline", "format": unsafe_format, "error": response.json()})
            check("inline_lossy_numeric_schema_rejected_before_execution")
        proof.update(passed=True, items=items(sid), turns=[t.to_dict() for t in sessions.turns.list(sid, order="asc", limit=100).data])
        return proof["checks"]
    finally:
        save()
        for sid in reversed(owned):
            delete_session(sessions, sid)
        if saved:
            client.beta.agents.delete(saved.id)
