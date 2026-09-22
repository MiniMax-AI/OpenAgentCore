"""Ordered image input through the pinned client, Core and a real native Runtime."""
import importlib.metadata
import json
import secrets
import sys
import time
import uuid
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
proof = {} if stage == "initial" else json.loads(Path(evidence).read_text())


def save():
    Path(evidence).write_text(json.dumps(proof, indent=2))


def text(value):
    return {"type": "input_text", "text": value}


def messages(parts):
    return [{"role": "user", "content": parts}]


def image_messages(url):
    return messages([text("Read this image. "), {"type": "input_image", "image_url": url},
                     text(" Remember this as the latest image.")]) + messages([
        text("Reply only with the four band colors from left to right, separated by commas. Do not use tools.")])


def message_event(value):
    return {"type": "agent.session.input.message", "input": value}


def answer(sid, expected):
    items = sessions.items.list(sid, order="asc", limit=100).data
    answers = [i for i in items if i.type == "message" and i.role == "assistant"]
    assert answers, "missing native answer"
    result = " ".join(c.text for c in answers[-1].content if c.type == "output_text").lower()
    positions = [result.find(name) for name in expected]
    assert all(p >= 0 for p in positions) and positions == sorted(positions), result
    return answers[-1].id


def wait_initial(sid):
    deadline = time.monotonic() + 150
    while time.monotonic() < deadline:
        turns = sessions.turns.list(sid, order="asc", limit=100).data
        if turns and turns[-1].status in {"completed", "failed", "cancelled"}:
            assert len(turns) == 1 and turns[-1].status == "completed", [t.to_dict() for t in turns]
            return turns[-1].id
        time.sleep(.25)
    raise AssertionError("initial input did not complete")


def run(sid, prompt, active=False, cancel=False):
    events, handled = [], False
    with sessions.events.stream(sid, timeout=180) as stream:
        sessions.events.create(sid, events=[message_event(messages([text(prompt)]))])
        for event in stream:
            events.append(event.to_dict())
            if event.type == "agent.session.requires_action":
                assert not handled and (active or cancel)
                handled = True
                action = event.session.required_actions[0]
                assert action.name == "wait_for_image"
                if cancel:
                    sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}])
                else:
                    proof.update(turn=action.turn_id, call=action.call_id)
                    batch = [message_event(image_messages(proof["second_url"]))]
                    for _ in range(2):
                        sessions.events.create(sid, events=batch, idempotency_key="same-active-image")
                    sessions.events.create(sid, events=[{"type": "agent.session.input.tool_result", "turn_id": action.turn_id,
                        "call_id": action.call_id, "success": True, "output": "The latest image has been supplied. Read it and give the colors; do not call tools again."}])
            assert event.type not in {"agent.session.failed", "agent.session.turn.failed"}, event.to_dict()
            if event.type == "agent.session.idle":
                break
        else:
            raise AssertionError("stream ended without idle")
    proof.setdefault("runs", []).append(events)
    save()
    terminal = "agent.session.turn." + ("cancelled" if cancel else "completed")
    types = [e["type"] for e in events]
    assert types.index("agent.session.turn.created") < types.index(terminal) < types.index("agent.session.idle")
    assert len({e["event_id"] for e in events}) == len(events)
    for index, event in enumerate(events):
        if event["type"] == "agent.session.turn.item.done" and event["item"].get("role") == "assistant":
            item_id = event["item"]["id"]
            added = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.item.added" and e["item"]["id"] == item_id)
            assert added < index < types.index(terminal)
    turns = sessions.turns.list(sid, order="asc", limit=100).data
    assert turns[-1].status == ("cancelled" if cancel else "completed")
    if active or cancel:
        assert handled
    return events


def check_items(sid):
    expected = image_messages(proof["first_url"]) + messages([text("Call wait_for_image exactly once, then follow the incoming image instructions.")]) + image_messages(proof["second_url"])
    response = http.get(base + "/v1/agents/sessions/" + sid + "/items", headers=headers, params={"order": "asc", "limit": 100})
    assert response.status_code == 200
    actual = [i for i in response.json()["data"] if i["type"] == "message" and i["role"] == "user"]
    assert [i["content"] for i in actual] == [m["content"] for m in expected]
    assert [i.content for i in sessions.items.list(sid, order="asc", limit=100).data if i.type == "message" and i.role == "user"]


try:
    if stage == "initial":
        first = ["red", "green", "blue", "yellow"]
        secrets.SystemRandom().shuffle(first)
        second = first[1:] + first[:1]
        proof.update(first=first, second=second, first_url=picture(first), second_url=picture(second))
        session = sessions.create(agent={"model": model, "tools": [{"type": "function", "name": "wait_for_image",
            "description": "Wait for the user to supply the next image.", "parameters": {"type": "object", "properties": {}, "additionalProperties": False}}]},
            environment={"type": "none"}, input=image_messages(proof["first_url"]))
        proof["session"] = session.id
        save()
        proof["initial_turn"] = wait_initial(session.id)
        proof["initial_answer"] = answer(session.id, first)
        run(session.id, "Call wait_for_image exactly once, then follow the incoming image instructions.", active=True)
        proof["active_answer"] = answer(session.id, second)
        assert len(sessions.turns.list(session.id).data) == 2
        check_items(session.id)
        before = [t.id for t in sessions.turns.list(session.id).data]
        invalid = [message_event(messages([text("must not be admitted")])), message_event(messages([{"type": "input_image", "image_url": "https://example.test/image.png"}]))]
        response = http.post(base + "/v1/agents/sessions/" + session.id + "/events", headers=headers, json={"events": invalid})
        assert response.status_code == 400
        assert [t.id for t in sessions.turns.list(session.id).data] == before
        check_items(session.id)
        foreign_headers = {**headers, "Authorization": "Bearer " + foreign}
        for suffix in ["", "/items", "/turns"]:
            assert http.get(base + "/v1/agents/sessions/" + session.id + suffix, headers=foreign_headers).status_code == 404
        assert http.post(base + "/v1/agents/sessions/" + session.id + "/events", headers=foreign_headers,
                         json={"events": [message_event(image_messages(proof["second_url"]))]}).status_code == 404
    else:
        sid = proof["session"]
        check_items(sid)
        run(sid, "Recall the latest image I supplied, not the first. Reply only with its four band colors in order. Do not call tools.")
        proof["resumed_answer"] = answer(sid, proof["second"])
        assert len(sessions.turns.list(sid).data) == 3
        run(sid, "Call wait_for_image exactly once and wait for its result.", cancel=True)
        run(sid, "Reply PLAIN_OK only. Do not call tools.")
        items = sessions.items.list(sid, order="asc", limit=100).data
        assert any(i.type == "message" and i.role == "assistant" and any(c.type == "output_text" and "PLAIN_OK" in c.text for c in i.content) for i in items)
        proof["passed"] = True
finally:
    save()
    client.close()
