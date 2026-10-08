"""MiniMax active-message delivery through the public API and native workspace."""

import shlex
import time
import uuid

from session_cleanup import delete_session


def verify_native_steering(client, foreign, http, agent_options, session_options, ready, record):
    assert agent_options["x_agents_core"]["harness"] == "mcode", "Select the MiniMax Harness"
    assert session_options["environment"]["type"] != "none", "A native workspace is required"
    sessions = client.beta.agents.sessions
    files = client.beta.agents.environments.files
    workspace = session_options["environment"].get("workspace_directory", "/workspace").rstrip("/")
    nonce = uuid.uuid4().hex
    marker = "steering-started-" + nonce
    memory = "steering-memory-" + uuid.uuid4().hex
    command = "printf start >> " + shlex.quote(workspace + "/" + marker) + "; sleep 45"
    initial = ("Run this command exactly once with your native shell tool in the foreground. "
               "Wait until it finishes before replying. Do not background, delegate, retry or write other files.\n" + command)
    followup = "Remember this conversation-only token for our next turn: " + memory + ". Keep waiting for the foreground command; do not write this token to a file."
    proof = {"harness": agent_options["x_agents_core"]["harness"], "model": agent_options["model"],
             "checks": [], "runs": [], "passed": False,
             "unverified": ["Direct ACP receipt and native duplicate mode are not observed by public HTTP retries.",
                            "Native acceptance does not guarantee model consumption of the active input."],
             "initial_input": initial, "active_input": followup}
    session = sessions.create(agent=agent_options, **session_options)
    endpoint = str(client.base_url).rstrip("/") + "/agents/sessions/" + session.id
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    proof.update(session=session.id, environment=session.environment.id)

    def message(text):
        return {"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": text}]}]}

    def items():
        response = http.get(endpoint + "/items", headers=headers, params={"limit": 100, "order": "asc"})
        assert response.status_code == 200 and not response.json()["has_more"]
        raw = response.json()["data"]
        assert raw == [item.to_dict() for item in sessions.items.list(session.id, limit=100, order="asc").data]
        return raw

    def collect(stream, events):
        for event in stream:
            events.append(event.to_dict())
            assert event.type not in {"error", "agent.session.failed", "agent.session.turn.failed", "agent.session.turn.cancelled", "agent.session.requires_action"}
            if event.type == "agent.session.idle" and any(e["type"] == "agent.session.turn.created" for e in events):
                break
        else:
            raise AssertionError("SSE ended without a completed Turn and idle Session")
        created = [e for e in events if e["type"] == "agent.session.turn.created"]
        terminal = [e for e in events if e["type"] == "agent.session.turn.completed"]
        assert len(created) == len(terminal) == 1
        turn = terminal[0]["turn"]["id"]
        assert created[0]["turn"]["id"] == turn
        assert events.index(created[0]) < events.index(terminal[0]) < len(events) - 1
        assert len({e["event_id"] for e in events}) == len(events)
        assert all(str(uuid.UUID(e["event_id"])) == e["event_id"] for e in events)
        assert all(e.get("turn_id") == turn for e in events if e["type"].startswith("agent.session.turn."))
        assert sessions.retrieve(session.id).status == "idle" and not sessions.retrieve(session.id).required_actions
        return turn

    try:
        ready(session)
        observed = []
        proof["runs"].append(observed)
        with sessions.events.stream(session.id, timeout=300) as stream:
            sessions.events.create(session.id, events=[message(initial)], idempotency_key="initial-" + nonce)
            deadline = time.monotonic() + 180
            while time.monotonic() < deadline:
                turns = list(sessions.turns.list(session.id, limit=100))
                assert len(turns) <= 1 and all(t.status not in {"completed", "failed", "cancelled"} for t in turns), "Native Turn ended before active input"
                markers = [f for f in files.list(session.environment.id, path="/workspace") if f.path == "/workspace/" + marker]
                if markers:
                    assert len(markers) == 1 and markers[0].size_bytes == 5, "Native foreground command was repeated"
                    assert len(turns) == 1 and turns[0].status == "in_progress"
                    active_turn = turns[0].id
                    break
                time.sleep(0.2)
            else:
                raise AssertionError("Native started marker was not observed")
            assert sessions.turns.retrieve(active_turn, session_id=session.id).status == "in_progress"
            proof["barrier"] = {"path": "/workspace/" + marker, "size_bytes": 5, "turn_id": active_turn}
            key = "active-" + nonce
            response = sessions.events.with_raw_response.create(session.id, events=[message(followup)], idempotency_key=key)
            assert response.status_code == 202 and response.content == b"" and response.parse() is None
            assert sessions.turns.retrieve(active_turn, session_id=session.id).status == "in_progress", "Turn settled during active-input admission"
            proof["admission"] = {"status": 202, "turn_id": active_turn}
            response = http.post(endpoint + "/events", headers={**headers, "Idempotency-Key": key}, json={"events": [message(followup)]})
            assert response.status_code == 202 and response.content == b""
            proof["retry_status"] = response.status_code
            for text, auth, request_key, expected in (
                ("conflicting-" + nonce, headers, key, 409),
                ("foreign-" + nonce, {**headers, "Authorization": "Bearer " + foreign.api_key}, "foreign-" + nonce, 404),
            ):
                response = http.post(endpoint + "/events", headers={**auth, "Idempotency-Key": request_key}, json={"events": [message(text)]})
                assert response.status_code == expected
                error = response.json()["error"]
                assert (error["type"], error["code"], error["param"]) == (("conflict_error", "idempotency_conflict", None) if expected == 409 else ("not_found_error", "not_found_error", None))
                proof.setdefault("rejections", []).append({"status": expected, "error": error})
            assert collect(stream, observed) == active_turn
        stored = items()
        users = [i for i in stored if i["type"] == "message" and i.get("role") == "user"]
        assert len(users) == 2 and all(i["turn_id"] == active_turn for i in users)
        assert [i["content"] for i in users] == [[{"type": "input_text", "text": text}] for text in (initial, followup)]
        assert [t.id for t in sessions.turns.list(session.id, limit=100)] == [active_turn]
        assert [f.size_bytes for f in files.list(session.environment.id, path="/workspace") if f.path == "/workspace/" + marker] == [5]
        proof["active_items"] = stored
        proof["model_observations"] = {"active_turn_mentions_token": any(memory in p.get("text", "") for i in stored if i.get("role") == "assistant" for p in i.get("content", []))}
        proof["checks"].append("public_active_input_same_turn_persistence_completion_retry_and_foreign_isolation")
        record(proof)

        output = "/outputs/steering-continuation-" + nonce + ".txt"
        prompt = ("Recall the exact conversation-only steering-memory- token from the previous turn without reading files or rerunning its command. "
                  "Use native tools to create the parent directory and write only that token, with no newline, to " + workspace + output + ". Then reply with that token. Do not delegate.")
        observed = []
        proof["runs"].append(observed)
        with sessions.events.stream(session.id, timeout=300) as stream:
            sessions.events.create(session.id, events=[message(prompt)], idempotency_key="continuation-" + nonce)
            continuation = collect(stream, observed)
        assert continuation != active_turn
        assert {t.id for t in sessions.turns.list(session.id, limit=100)} == {active_turn, continuation}
        stored = items()
        assert [i for i in stored if i["type"] == "message" and i.get("role") == "user" and i["turn_id"] == active_turn] == users
        answers = [i for i in stored if i.get("role") == "assistant" and i["turn_id"] == continuation]
        assert any(memory in p.get("text", "") for i in answers for p in i["content"]), "Model did not recall the active-input token"
        artifacts = [a for a in sessions.artifacts.list(session.id, limit=100) if a.turn_id == continuation and a.path == "/workspace" + output]
        assert len(artifacts) == 1
        with sessions.artifacts.with_streaming_response.content(artifacts[0].id, session_id=session.id) as response:
            assert response.read() == memory.encode(), "Native continuation artifact did not contain the exact token"
        assert [f.size_bytes for f in files.list(session.environment.id, path="/workspace") if f.path == "/workspace/" + marker] == [5]
        proof["model_observations"]["continuation_recalled_token_and_wrote_artifact"] = True
        proof["checks"].append("warm_continuation_with_model_dependent_token_recall_and_native_artifact")
        proof.update(passed=True, items=stored, artifact=artifacts[0].to_dict())
        return proof["checks"]
    finally:
        try:
            record(proof)
        finally:
            delete_session(sessions, session.id)
