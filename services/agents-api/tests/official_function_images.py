"""Function images through pinned SDK/raw HTTP and a real native Runtime/model."""
import base64
import importlib.metadata
import json
import secrets
import sys
from pathlib import Path

import httpx2
from openai import OpenAI
from image_fixture import picture

base, token, foreign, model, stage, evidence = sys.argv[1:]
pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
dist = importlib.metadata.distribution("openai")
assert dist.version == pin["sdk_version"]
assert json.loads(dist.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
http = httpx2.Client(trust_env=False, timeout=180)
client = OpenAI(base_url=base + "/v1", api_key=token, max_retries=0,
                _strict_response_validation=True, http_client=http)
sessions = client.beta.agents.sessions
headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
proof = {"calls": [], "runs": []} if stage == "initial" else json.loads(Path(evidence).read_text())


def save():
    Path(evidence).write_text(json.dumps(proof, indent=2))


def text(value):
    return {"type": "input_text", "text": value}


def post(sid, events, key=None, foreign_request=False):
    hdr = {**headers}
    if key:
        hdr["Idempotency-Key"] = key
    if foreign_request:
        hdr["Authorization"] = "Bearer " + foreign
    return http.post(base + "/v1/agents/sessions/" + sid + "/events", headers=hdr, json={"events": events})


def items(sid):
    return [i.to_dict() for i in sessions.items.list(sid, order="asc", limit=100).data]


def answer(sid, expected):
    answers = [i for i in items(sid) if i["type"] == "message" and i["role"] == "assistant"]
    assert answers, "missing model answer"
    result = " ".join(c["text"] for c in answers[-1]["content"] if c["type"] == "output_text").lower()
    positions = [result.find(name) for name in expected]
    assert all(p >= 0 for p in positions) and positions == sorted(positions), result


def run(sid, output=None, expected=None, cancel=False, failed_text=False, validate=False, recall=False, creation=None):
    events, handled = [], False
    prompt = "Call get_visual exactly once. Read the image returned by that tool and reply with its four band colors from left to right. Do not call it again."
    if recall:
        prompt = "Without calling tools, recall the most recent image from get_visual and repeat its four band colors from left to right."
    with (creation or sessions.events.stream(sid, timeout=180)) as stream:
        if creation is None:
            sessions.events.create(sid, events=[{"type": "agent.session.input.message", "input": [{"role": "user", "content": [text(prompt)]}]}])
        for event in stream:
            events.append(event.to_dict())
            if event.type == "agent.session.requires_action":
                assert not handled and not recall
                handled = True
                action = event.session.required_actions[0]
                assert action.name == "get_visual" and len(event.session.required_actions) == 1
                if cancel:
                    sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}])
                else:
                    result = {"type": "agent.session.input.tool_result", "turn_id": action.turn_id,
                              "call_id": action.call_id, "success": not failed_text, "output": output}
                    if failed_text:
                        result["error"] = "No image available; reply only TOOL-ERROR-RECEIVED."
                    key = "result-" + action.call_id
                    if validate:
                        before = items(sid)
                        invalid_urls = ["https://example.test/image.png", "data:image/png;base64,?", "data:image/png;base64,AQID"]
                        # Claude rejects malformed/remote and error images before consuming a call.
                        if proof["kind"] == "claude_sdk":
                            invalid = [{**result, "output": [{"type": "input_image", "image_url": u}]} for u in invalid_urls]
                            invalid.append({**result, "success": False})
                            for bad in invalid:
                                assert post(sid, [{"type": "agent.session.input.message", "input": "must not persist"}, bad], key).status_code == 400
                                assert items(sid) == before
                                assert sessions.retrieve(sid).required_actions[0].call_id == action.call_id
                        assert post(sid, [result], foreign_request=True).status_code == 404
                        assert post(sid, [{**result, "call_id": "unknown-call"}]).status_code in {400, 404, 409}
                        assert items(sid) == before
                    assert sessions.events.create(sid, events=[result], idempotency_key=key) is None
                    assert post(sid, [result], key).status_code == 202
                    assert post(sid, [{**result, "output": "different"}], key).status_code == 409
                    proof["calls"].append({"turn": action.turn_id, "call": action.call_id, "output": output, "success": not failed_text})
            assert event.type not in {"agent.session.failed", "agent.session.turn.failed"}, event.to_dict()
            if event.type == "agent.session.idle":
                break
        else:
            raise AssertionError("stream ended without idle")
    proof["runs"].append(events)
    save()
    types = [e["type"] for e in events]
    terminal = "agent.session.turn." + ("cancelled" if cancel else "completed")
    assert types.index("agent.session.turn.created") < types.index(terminal) < types.index("agent.session.idle")
    assert len({e["event_id"] for e in events}) == len(events)
    assert handled != recall
    assert sessions.turns.list(sid, order="desc").data[0].status == ("cancelled" if cancel else "completed")
    if expected:
        answer(sid, expected)
    recovered = {i["call_id"]: i for i in items(sid) if i["type"] == "function_call_output"}
    for call in proof["calls"]:
        assert recovered[call["call"]]["output"] == call["output"]
        assert "error" in recovered[call["call"]]
        assert (recovered[call["call"]]["error"] is None) == call["success"]


try:
    if stage == "initial":
        import os
        proof["kind"] = os.environ["OAC_TEST_FUNCTION_IMAGE_ENGINE"]
        colors = ["red", "green", "blue", "yellow"]
        secrets.SystemRandom().shuffle(colors)
        proof["colors"] = colors
        creation = sessions.create(agent={"model": model, "tools": [{"type": "function", "name": "get_visual",
            "description": "Return a visual to inspect.", "parameters": {"type": "object", "properties": {}, "additionalProperties": False}}]}, environment={"type": "none"}, input="Call get_visual exactly once. Read the image returned by that tool and reply with its four band colors from left to right. Do not call it again.", stream=True)
        session = next(creation).session
        proof["session"] = sid = session.id
        save()
        for scale in [1, 15]:
            output = [text("Read this visual."), {"type": "input_image", "image_url": picture(colors, scale)}, text("Return its four band colors in order.")]
            run(sid, output, colors, validate=scale == 1, creation=creation if scale == 1 else None)
        jpeg = base64.b64encode((Path(__file__).parent / "testdata/function-bands.jpg").read_bytes()).decode()
        proof["colors"] = ["yellow", "blue", "red", "green"]
        run(sid, [{"type": "input_image", "image_url": "data:image/jpeg;base64," + jpeg}], proof["colors"])
        # Failed text remains supported and must not be mistaken for failed images.
        run(sid, [text("No image was returned.")], failed_text=True)
        for suffix in ["", "/items", "/turns"]:
            response = http.get(base + "/v1/agents/sessions/" + sid + suffix, headers={**headers, "Authorization": "Bearer " + foreign})
            assert response.status_code == 404
    else:
        sid = proof["session"]
        run(sid, expected=proof["colors"], recall=True)
        run(sid, cancel=True)
        assert len(sessions.turns.list(sid, limit=100).data) == 6
    save()
finally:
    client.close()
