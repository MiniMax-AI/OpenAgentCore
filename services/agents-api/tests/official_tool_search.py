"""Real deferred functions through the pinned SDK and raw Agents API HTTP."""
import importlib.metadata
import json
import secrets
import sys
import uuid
from pathlib import Path

import httpx2
from openai import BadRequestError, NotFoundError, OpenAI
from image_fixture import picture

base, token, foreign, model, stage, evidence = sys.argv[1:]
pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
source = json.loads(importlib.metadata.distribution("openai").read_text("direct_url.json"))
assert source["vcs_info"]["commit_id"] == pin["commit"]
client = OpenAI(base_url=base + "/v1", api_key=token, max_retries=0,
                _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=150))
other = client.with_options(api_key=foreign)
sessions = client.beta.agents.sessions
headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
proof = {} if stage == "initial" else json.loads(Path(evidence).read_text())


def run(session, name, cancel=False, images=False):
    marker = "RESULT-" + str(uuid.uuid4())
    events, handled = [], False
    prompt = "Call " + name + " exactly once, using the exact required ticket from its schema. Return only the fresh tool result. Discover its definition if needed."
    message = {"type":"agent.session.input.message", "input":[{"role":"user","content":[{"type":"input_text","text":prompt}]}]}
    colors = secrets.SystemRandom().sample(["red", "green", "blue", "yellow"], 4)
    active_colors = secrets.SystemRandom().sample(["red", "green", "blue", "yellow"], 4)
    if images:
        message["input"][0]["content"].append({"type":"input_image","image_url":picture(colors)})
        message["input"][0]["content"].append({"type":"input_text","text":"Also remember these four band colors in left-to-right order."})
    with sessions.events.stream(session, timeout=150) as stream:
        sessions.events.create(session, events=[message], idempotency_key=str(uuid.uuid4()))
        for event in stream:
            events.append(event.to_dict())
            if event.type == "agent.session.requires_action":
                assert not handled and len(event.session.required_actions) == 1, event.to_dict()
                handled = True
                action = event.session.required_actions[0]
                assert action.name == name and action.arguments == {"ticket":proof["parameter"]}, action.to_dict()
                assert sessions.turns.retrieve(action.turn_id, session_id=session).status == "waiting"
                if cancel:
                    sessions.events.create(session, events=[{"type":"agent.session.input.cancel"}])
                else:
                    if images:
                        update = {"type":"agent.session.input.message", "input":[{"role":"user","content":[
                            {"type":"input_text","text":"New instruction: use this latest image instead. In your final answer print its four band colors from left to right and the tool result. Do not make another tool call."},
                            {"type":"input_image","image_url":picture(active_colors)}]}]}
                        sessions.events.create(session, events=[update], idempotency_key="active-image")
                    if "turn" not in proof:
                        proof.update(turn=action.turn_id, call=action.call_id)
                    payload = {"type":"agent.session.input.tool_result", "turn_id":action.turn_id, "call_id":action.call_id,
                               "success":True, "output":[{"type":"input_text","text":marker}]}
                    result_key = str(uuid.uuid4())
                    for _ in range(2):
                        sessions.events.create(session, events=[payload], idempotency_key=result_key)
            assert event.type not in {"agent.session.failed", "agent.session.turn.failed"}, event.to_dict()
            if event.type == "agent.session.idle":
                break
        else:
            raise AssertionError("stream ended without idle")
    assert handled
    proof.setdefault("runs", []).append(events)
    Path(evidence).write_text(json.dumps(proof, indent=2))
    turns = sessions.turns.list(session, order="asc", limit=100).data
    turn = turns[-1]
    assert turn.status == ("cancelled" if cancel else "completed"), turn.to_dict()
    kinds = [e["type"] for e in events]
    assert kinds.index("agent.session.turn." + turn.status) < kinds.index("agent.session.idle")
    assert len({e["event_id"] for e in events}) == len(events)
    items = sessions.items.list(session, limit=100, order="asc").data
    calls = [i for i in items if i.type == "function_call" and i.turn_id == turn.id]
    outputs = [i for i in items if i.type == "function_call_output" and i.call_id == action.call_id]
    assert len(calls) == 1 and calls[0].call_id == action.call_id
    assert len(outputs) == (0 if cancel else 1)
    if not cancel:
        answers = [i for i in items if i.type == "message" and i.role == "assistant" and i.turn_id == turn.id]
        assert any(marker in json.dumps(i.to_dict()) for i in answers), [i.to_dict() for i in answers]
        if images:
            answer = " ".join(c.text for i in answers for c in i.content if c.type == "output_text").lower()
            positions = [answer.find(c) for c in active_colors]
            assert all(p >= 0 for p in positions) and positions == sorted(positions), answer
            proof["image_colors"] = {"initial":colors,"active":active_colors}
        assert outputs[0].output[0].text == marker
        assert any(e["type"] == "agent.session.turn.item.added" and e["item"]["id"] == outputs[0].id for e in events)
    with httpx2.Client(trust_env=False) as raw:
        response = raw.get(base + "/v1/agents/sessions/" + session + "/items", headers=headers, params={"order":"asc","limit":100})
        assert response.status_code == 200
        assert [i["id"] for i in response.json()["data"]] == [i.id for i in items]
    return turn.id


try:
    if stage == "initial":
        proof["parameter"] = "ARG-" + str(uuid.uuid4())
        schema = {"type":"object","properties":{"ticket":{"type":"string","enum":[proof["parameter"]]}}, "required":["ticket"],"additionalProperties":False}
        tools = [{"type":"tool_search"}] + [
            {"type":"function","name":name,"description":description,"parameters":schema,"defer_loading":deferred}
            for name, description, deferred in [("lookup_account","Return the account result.",True), ("clock","Return the current clock result.",False), ("unrelated_report","Read an unrelated report.",True)]]
        config = {"model":model,"tools":tools}
        saved = client.beta.agents.create(**config)
        session = sessions.create(agent_id=saved.id, environment={"type":"none"}, extra_headers={"Idempotency-Key":"discovery-create"})
        assert sessions.create(agent_id=saved.id, environment={"type":"none"}, extra_headers={"Idempotency-Key":"discovery-create"}).id == session.id
        assert [t.to_dict() for t in saved.tools] == tools
        assert [t.to_dict() for t in session.agent.tools] == tools[1:]
        proof.update(session=session.id, agent=saved.id, tools=tools)
        run(session.id, "lookup_account", images=True)
        run(session.id, "clock")
        for action in [lambda:other.beta.agents.sessions.retrieve(session.id), lambda:other.beta.agents.sessions.create(agent_id=saved.id,environment={"type":"none"})]:
            try:
                action()
                raise AssertionError("foreign tenant accessed discovery configuration")
            except NotFoundError:
                pass
        for invalid in [[tools[1]], [tools[0]], [tools[0],tools[0],tools[1]], [{"type":"tool_search","execution":"client"},tools[1]]]:
            try:
                sessions.create(agent={"model":model,"tools":invalid}, environment={"type":"none"})
                raise AssertionError("unqualified discovery configuration admitted")
            except BadRequestError:
                pass
    else:
        session = sessions.retrieve(proof["session"])
        assert [t.to_dict() for t in session.agent.tools] == proof["tools"][1:]
        run(session.id, "lookup_account")
        assert len(sessions.turns.list(session.id).data) == 3
        assert len([i for i in sessions.items.list(session.id,limit=100).data if i.type == "function_call"]) == 3
        # Inline configuration exercises a fresh native Session and pending-call cancellation.
        cancelled = sessions.create(agent={"model":model,"tools":proof["tools"]}, environment={"type":"none"})
        run(cancelled.id, "lookup_account", cancel=True)
        proof["cancel_session"] = cancelled.id
        proof["passed"] = True
finally:
    Path(evidence).write_text(json.dumps(proof, indent=2))
    client.close()
    other.close()
