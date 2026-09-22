"""Common hosted image acceptance through fixed SDK, HTTP and real native tools."""

import base64
import importlib.metadata
import json
from pathlib import Path
import secrets
import time

from image_fixture import picture


def verify_workspace_images(client, foreign, http, model, kind, restart, evidence):
    """restart(session_id, environment_id) cold-restarts the owned Core/Runtime."""
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    assert distribution.version == pin["sdk_version"]
    assert json.loads(distribution.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    sessions = client.beta.agents.sessions
    root = str(client.base_url).rstrip("/") + "/agents"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    foreign_headers = {**headers, "Authorization": "Bearer " + foreign.api_key}
    proof = {"engine": kind, "checks": [], "runs": [], "calls": []}
    expected_messages = []
    sid = None

    def save():
        Path(evidence).write_text(json.dumps(proof, indent=2))

    def check(name):
        proof["checks"].append(name)
        save()
        print(name, flush=True)

    def text(value):
        return {"type": "input_text", "text": value}

    def messages(parts):
        return [{"role": "user", "content": parts}]

    def event(value):
        return {"type": "agent.session.input.message", "input": value}

    def image_messages(url, path):
        return messages([text("Inspect this image."), {"type": "input_image", "image_url": url},
                         text("Remember its four band colors in left-to-right order.")]) + messages([
            text("Use native workspace tools to write exactly the four lowercase color names, separated by commas and no newline, to " + path +
                 ". Create the parent directory if necessary. Do not call get_visual. Then reply with the same colors.")])

    def until(predicate, timeout=240):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            result = predicate()
            if result:
                return result
            time.sleep(0.2)
        raise AssertionError("Public workflow did not reach expected state")

    def items():
        response = http.get(root + "/sessions/" + sid + "/items", headers=headers, params={"order": "asc", "limit": 100})
        assert response.status_code == 200
        raw = response.json()["data"]
        assert not response.json()["has_more"], "Acceptance exceeded its bounded item page"
        assert raw == [i.to_dict() for i in sessions.items.list(sid, order="asc", limit=100).data]
        return raw

    def history():
        stored = items()
        assert [i["content"] for i in stored if i["type"] == "message" and i.get("role") == "user"] == [m["content"] for m in expected_messages]
        outputs = {i["call_id"]: i for i in stored if i["type"] == "function_call_output"}
        for call in proof["calls"]:
            assert outputs[call["call_id"]]["output"] == call["output"]
        return stored

    def post(events, key=None, foreign_request=False):
        hdr = dict(foreign_headers if foreign_request else headers)
        if key:
            hdr["Idempotency-Key"] = key
        return http.post(root + "/sessions/" + sid + "/events", headers=hdr, json={"events": events})

    def verify_file(path, expected, turn):
        answers = [i for i in items() if i["type"] == "message" and i.get("role") == "assistant"]
        answer = " ".join(p["text"] for p in answers[-1]["content"] if p["type"] == "output_text").lower()
        positions = [answer.find(color) for color in expected]
        assert all(p >= 0 for p in positions) and positions == sorted(positions), answer
        artifacts = list(sessions.artifacts.list(sid, limit=100))
        matches = [a for a in artifacts if a.path == path and a.turn_id == turn]
        assert len(matches) == 1, [a.to_dict() for a in artifacts]
        artifact = matches[0]
        with sessions.artifacts.with_streaming_response.content(artifact.id, session_id=sid) as response:
            assert response.read() == ",".join(expected).encode()
        assert any(f.path == path for f in client.beta.agents.environments.files.list(eid, path="/workspace/outputs"))
        for suffix in ("", "/content"):
            assert http.get(root + "/sessions/" + sid + "/artifacts/" + artifact.id + suffix, headers=foreign_headers).status_code == 404

    def run(value, handler=None):
        observed = []
        handled = False
        with sessions.events.stream(sid, timeout=300) as stream:
            sessions.events.create(sid, events=[event(value)])
            expected_messages.extend(value)
            for received in stream:
                observed.append(received.to_dict())
                if received.type == "agent.session.requires_action":
                    assert handler is not None and not handled
                    assert len(received.session.required_actions) == 1
                    action = received.session.required_actions[0]
                    assert action.name == "get_visual"
                    handled = True
                    handler(action)
                assert received.type not in {"agent.session.failed", "agent.session.turn.failed"}, received.to_dict()
                if received.type == "agent.session.idle" and any(e["type"] == "agent.session.turn.created" for e in observed):
                    break
            else:
                raise AssertionError("SSE ended without idle")
        proof["runs"].append(observed)
        save()
        types = [e["type"] for e in observed]
        terminal = [t for t in types if t in {"agent.session.turn.completed", "agent.session.turn.cancelled"}]
        assert len(terminal) == 1
        assert types[-1] == "agent.session.idle"
        assert types.index("agent.session.turn.created") < types.index(terminal[0]) < len(types) - 1
        assert len({e["event_id"] for e in observed}) == len(observed)
        assert handled == (handler is not None)
        turn = sessions.turns.list(sid, order="desc").data[0]
        assert terminal[0] == "agent.session.turn." + turn.status
        history()
        return turn

    def submit(action, output, validate=False):
        result = {"type": "agent.session.input.tool_result", "turn_id": action.turn_id,
                  "call_id": action.call_id, "success": True, "output": output}
        key = "result-" + action.call_id
        before = items()
        if validate:
            if kind == "claude_sdk":
                invalid = [{**result, "success": False}, {**result, "output": [{"type": "input_image", "image_url": "https://example.test/image.png"}]}]
                for bad in invalid:
                    assert post([bad], key).status_code == 400
                    assert items() == before
                    assert sessions.retrieve(sid).required_actions[0].call_id == action.call_id
            assert post([result], foreign_request=True).status_code == 404
            assert post([{**result, "call_id": "unknown-call"}]).status_code in {400, 404, 409}
            assert items() == before
        sessions.events.create(sid, events=[result], idempotency_key=key)
        assert post([result], key).status_code == 202
        assert post([{**result, "output": "conflict"}], key).status_code == 409
        proof["calls"].append(result)

    colors = ["red", "green", "blue", "yellow"]
    secrets.SystemRandom().shuffle(colors)
    initial = image_messages(picture(colors), "/workspace/outputs/initial.txt")
    try:
        session = sessions.create(agent={"model": model, "tools": [{"type": "function", "name": "get_visual",
            "description": "Wait for a visual supplied by the caller.", "parameters": {"type": "object", "properties": {}, "additionalProperties": False}}]},
            environment={"type": "openai_hosted"}, input=initial)
        sid, eid = session.id, session.environment.id
        proof.update(session=sid, environment=eid, initial_colors=colors)
        expected_messages.extend(initial)
        save()

        def initial_done():
            turns = sessions.turns.list(sid, order="asc", limit=100).data
            if not turns or turns[-1].status not in {"completed", "failed", "cancelled"}:
                return False
            assert len(turns) == 1 and turns[0].status == "completed", [t.to_dict() for t in turns]
            return turns[0]

        first = until(initial_done)
        verify_file("/workspace/outputs/initial.txt", colors, first.id)
        history()
        check("initial_png_native_workspace_files_artifact")

        jpeg = "data:image/jpeg;base64," + base64.b64encode((Path(__file__).parent / "testdata/function-bands.jpg").read_bytes()).decode()
        idle = messages([{"type": "input_image", "image_url": jpeg}]) + messages([text(
            "Write this image's four lowercase band colors from left to right, comma-separated with no newline, to /workspace/outputs/idle.txt using native tools. Reply with those colors. Do not call get_visual.")])
        turn = run(idle)
        verify_file("/workspace/outputs/idle.txt", ["yellow", "blue", "red", "green"], turn.id)
        check("prepared_image_only_jpeg_and_message_boundary")

        active_colors = colors[1:] + colors[:1]
        def active(action):
            incoming = image_messages(picture(active_colors), "/workspace/outputs/active.txt")
            batch = [event(incoming)]
            sessions.events.create(sid, events=batch, idempotency_key="active-image")
            expected_messages.extend(incoming)
            assert post(batch, "active-image").status_code == 202
            assert post([event(messages([text("conflict")]))], "active-image").status_code == 409
            submit(action, "The user supplied an image. Follow its instructions, then stop.")
        turn = run(messages([text("Call get_visual once and wait. Then follow the incoming image instructions.")]), active)
        verify_file("/workspace/outputs/active.txt", active_colors, turn.id)
        check("active_image_input_retry_and_native_tools")

        result_colors = colors[2:] + colors[:2]
        output = [text("Inspect this visual."), {"type": "input_image", "image_url": picture(result_colors, 15)}, text("Remember these band colors.")]
        turn = run(messages([text("Call get_visual exactly once. Read its returned image and use native tools to write its four lowercase band colors in left-to-right order, comma-separated with no newline, to /workspace/outputs/result.txt. Reply with the colors. Do not call get_visual again.")]),
                   lambda action: submit(action, output, validate=True))
        verify_file("/workspace/outputs/result.txt", result_colors, turn.id)
        check("large_function_image_result_receipt_retry_isolation_and_artifact")

        before = history()
        invalid = [event(messages([text("must not persist")])), event(messages([{"type": "input_image", "image_url": "https://example.test/image.png"}]))]
        assert post(invalid).status_code == 400
        assert history() == before
        for suffix in ("", "/items", "/turns", "/artifacts"):
            assert http.get(root + "/sessions/" + sid + suffix, headers=foreign_headers).status_code == 404
        assert post([event(initial)], foreign_request=True).status_code == 404
        assert http.get(root + "/environments/" + eid + "/files", headers=foreign_headers).status_code == 404
        check("unsupported_message_batch_is_atomic_and_foreign_resources_hidden")

        other = sessions.create(agent={"model": model}, environment={"type": "openai_hosted"})
        try:
            source_artifact = list(sessions.artifacts.list(sid, limit=100))[0]
            for suffix in ("", "/content"):
                assert http.get(root + "/sessions/" + other.id + "/artifacts/" + source_artifact.id + suffix, headers=headers).status_code == 404
            response = http.post(root + "/sessions/" + other.id + "/events", headers=headers, json={"events": [proof["calls"][-1]]})
            assert response.status_code in {400, 404, 409}
            assert sessions.items.list(other.id).data == []
            until(lambda: client.beta.agents.environments.retrieve(other.environment.id).status == "connected")
            response = http.get(root + "/environments/" + other.environment.id + "/files", headers=headers, params={"path": "/workspace/outputs"})
            assert response.status_code in {200, 404}
            if response.status_code == 200:
                assert response.json()["data"] == []
            assert history() == before
            check("same_tenant_session_result_artifact_and_workspace_isolation")
        finally:
            sessions.delete(other.id)

        restart(sid, eid)
        assert history() == before
        turn = run(messages([text("Recall the most recent image returned by get_visual from conversation history, without reading any file or calling get_visual. Write its four lowercase band colors in order, comma-separated with no newline, to /workspace/outputs/resumed.txt using native tools, then reply with them.")]))
        verify_file("/workspace/outputs/resumed.txt", result_colors, turn.id)
        assert len(sessions.turns.list(sid, limit=100).data) == 5
        check("cold_core_runtime_history_continuation_without_replay")

        pending = []
        def cancel(action):
            pending.append(action.call_id)
            for _ in range(2):
                sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}], idempotency_key="cancel-image-pending")
        turn = run(messages([text("Call get_visual exactly once and wait for its result.")]), cancel)
        assert turn.status == "cancelled" and not sessions.retrieve(sid).required_actions
        assert not any(i["type"] == "function_call_output" and i["call_id"] == pending[0] for i in items())
        run(messages([text("Reply only PLAIN_OK. Do not call any tools.")]))
        assert len(sessions.turns.list(sid, limit=100).data) == 7
        assert any(i["type"] == "message" and i.get("role") == "assistant" and any("PLAIN_OK" in p.get("text", "") for p in i["content"]) for i in items())
        check("pending_cancel_retry_and_text_continuation")
        proof["passed"] = True
        return proof["checks"]
    finally:
        save()
        if sid:
            sessions.delete(sid)
