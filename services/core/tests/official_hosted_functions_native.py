"""Common real-Harness acceptance for public functions and workspace execution."""

import importlib.metadata
import json
from pathlib import Path
import time
import uuid

from session_cleanup import delete_session


def verify_hosted_functions(client, foreign, http, agent_options, session_options, ready, restart, record, *, deferred=False):
    """Run against the selected workspace; restart, when supplied, restarts its agent host."""
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source["vcs_info"]["commit_id"] == pin["commit"]
    sessions = client.beta.agents.sessions
    workspace = session_options["environment"].get("workspace_directory", "/workspace").rstrip("/")
    endpoint = str(client.base_url).rstrip("/") + "/agents/sessions"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    marker = "function-memory-" + uuid.uuid4().hex
    private_error = "private-handler-" + uuid.uuid4().hex
    calls, observed, checks = [], [], []
    tool = {
        "type": "function", "name": "lookup", "description": "Retrieve the requested test value.",
        "parameters": {"type": "object", "properties": {"key": {"type": "string"}}, "required": ["key"], "additionalProperties": False},
    }
    if deferred:
        tool["defer_loading"] = True
    tools = [{"type": "tool_search"}, tool] if deferred else [tool]
    configuration = {**agent_options, "instructions": "Follow the requested tool calls exactly. Never repeat a failed call.", "tools": tools}
    owned, saved = [], None
    proof = {"checks": checks, "calls": calls, "events": observed, "deferred": deferred,
             "model": agent_options["model"], "harness": agent_options["x_agents_core"]["harness"]}
    if deferred:
        proof["evidence_limit"] = "Deferred callback execution; the public stream does not independently expose native ToolSearch discovery."
    discovery = "Search your tools for the function that retrieves the requested test value, then " if deferred else ""

    def until(predicate, timeout=120):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = predicate()
            if value:
                return value
            time.sleep(0.2)
        raise AssertionError("Public workflow did not reach expected state")

    def items():
        response = http.get(endpoint + "/" + session.id + "/items", headers=headers, params={"limit": 100, "order": "asc"})
        assert response.status_code == 200
        raw = response.json()["data"]
        assert raw == [item.to_dict() for item in sessions.items.list(session.id, limit=100, order="asc").data]
        return raw

    def invoke(text, key, handler):
        events = []
        observed.append(events)
        if saved is not None:
            proof["saved"]["events"].append(events)
        with sessions.stream(session.id, input=text, tool_handlers={"lookup": handler}, idempotency_key=key, timeout=240) as stream:
            events.extend(event.to_dict() for event in stream)
        terminals = [e for e in events if e["type"] in ("agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled")]
        assert len(terminals) == 1 and terminals[0]["type"] == "agent.session.turn.completed"
        assert events[-1]["type"] == "agent.session.idle"
        function = [e for e in events if e["type"] == "agent.session.turn.item.added" and e["item"]["type"] == "function_call"]
        result = [e for e in events if e["type"] == "agent.session.turn.item.added" and e["item"]["type"] == "function_call_output"]
        assert len(function) == len(result) == 1
        assert function[0]["item"]["call_id"] == result[0]["item"]["call_id"]
        assert events.index(function[0]) < events.index(result[0]) < events.index(terminals[0]) < len(events) - 1
        assert sessions.retrieve(session.id).required_actions == []
        return terminals[0]["turn"]["id"], result[0]["item"]

    def successful_file(path, key):
        turn, result = invoke(discovery + f"Call lookup once with key success. Remember its returned string in conversation. Then use the native shell to create {workspace}/outputs/{path} containing exactly that string, with no newline. Do not call any other function.", key, success)
        assert result["output"] == marker and "error" in result and result["error"] is None
        artifacts = list(sessions.artifacts.list(session.id, limit=100))
        output = [a for a in artifacts if a.turn_id == turn and a.path == "/workspace/outputs/" + path]
        assert len(output) == 1
        with sessions.artifacts.with_streaming_response.content(output[0].id, session_id=session.id) as response:
            assert response.read() == marker.encode()
        listing = client.beta.agents.environments.files.list(session.environment.id, path="/workspace/outputs")
        assert any(f.path == "/workspace/outputs/" + path for f in listing)
        assert any(i["type"] == "function_call_output" and i.get("output") == marker for i in items())

    try:
        session = sessions.create(agent=configuration, **session_options)
        owned.append(session.id)
        proof["session"] = session.id
        ready(session)

        def success(arguments):
            assert arguments == {"key": "success"}
            calls.append(arguments)
            return marker

        successful_file("function.txt", "function-success")
        checks.append("same_turn_function_native_file_and_public_artifact")

        if restart is not None:
            restart()

        def failure(arguments):
            assert arguments == {"key": "failure"}
            calls.append(arguments)
            raise RuntimeError(private_error)

        _, result = invoke(discovery + "Call lookup exactly once with key failure. If it fails, do not retry. Then reply with the exact string returned by lookup in our first turn, using conversation history and no file tools.", "function-failure", failure)
        assert result["error"] == "Tool handler failed." and "output" in result and result["output"] is None
        messages = [i for i in items() if i["type"] == "message" and i["role"] == "assistant"]
        assert marker in "\n".join(p.get("text", "") for p in messages[-1]["content"])
        assert len(calls) == 2
        checks.append(("cold" if restart is not None else "warm") + "_history_continuation_and_public_handler_error")

        sessions.events.create(session.id, events=[{"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": discovery + "Call lookup once with key pending and wait for the result."}]}]}], idempotency_key="function-pending")
        until(lambda: sessions.retrieve(session.id).required_actions)
        pending = [i for i in items() if i["type"] == "function_call"][-1]
        foreign_headers = {**headers, "Authorization": "Bearer " + foreign.api_key}
        result_event = {"type": "agent.session.input.tool_result", "call_id": pending["call_id"], "turn_id": sessions.turns.list(session.id).data[0].id, "success": True, "output": "foreign-value"}
        response = http.post(endpoint + "/" + session.id + "/events", headers=foreign_headers, json={"events": [result_event]})
        assert response.status_code == 404
        cancel = [{"type": "agent.session.input.cancel"}]
        sessions.events.create(session.id, events=cancel, idempotency_key="cancel-pending")
        sessions.events.create(session.id, events=cancel, idempotency_key="cancel-pending")
        until(lambda: len(sessions.turns.list(session.id).data) == 3 and sessions.turns.list(session.id).data[0].status == "cancelled")
        assert sessions.retrieve(session.id).required_actions == []
        assert not any(i["type"] == "function_call_output" and i["call_id"] == pending["call_id"] for i in items())
        checks.append("pending_function_tenant_isolation_and_cancel_retry")
        proof.update(events=list(observed), items=items())
        if deferred:
            record(proof)
            saved = client.beta.agents.create(**{k: v for k, v in configuration.items() if k != "x_agents_core"},
                extra_body={"x_agents_core": configuration["x_agents_core"]})
            proof["saved"] = {"agent": saved.id, "original_tools": tools, "events": []}
            stored = client.beta.agents.retrieve(saved.id)
            assert [entry.to_dict() for entry in stored.tools] == tools
            session = sessions.create(agent_id=saved.id, **session_options)
            owned.append(session.id)
            proof["saved"]["session"] = session.id
            ready(session)
            changed_tool = {**tool, "description": "Changed after Session creation.",
                            "parameters": {"type": "object", "properties": {"replacement": {"type": "string"}}, "required": ["replacement"], "additionalProperties": False}}
            client.beta.agents.update(saved.id, tools=[{"type": "tool_search"}, changed_tool])
            assert [entry.to_dict() for entry in client.beta.agents.retrieve(saved.id).tools] == [{"type": "tool_search"}, changed_tool]
            # The pinned Session tool union omits tool_search; the frozen
            # deferred function and its original schema must still be present.
            frozen = [entry.to_dict() for entry in sessions.retrieve(session.id).agent.tools]
            assert frozen == [tool]
            marker = "saved-function-memory-" + uuid.uuid4().hex
            successful_file("saved-function.txt", "saved-function-success")
            assert len(calls) == 3 and calls[-1] == {"key": "success"}
            checks.append("saved_deferred_function_schema_frozen_after_agent_update")
            proof["saved"].update(updated_tools=[{"type": "tool_search"}, changed_tool], session_tools=frozen, items=items())
        return checks
    finally:
        try:
            assert private_error not in json.dumps(proof)
            record(proof)
        finally:
            for sid in reversed(owned):
                delete_session(sessions, sid)
            if saved is not None:
                client.beta.agents.delete(saved.id)
