"""Real-model structured output through the pinned SDK and public HTTP surface."""
import importlib.metadata
import json
import sys
import uuid
from pathlib import Path

import httpx2
from openai import BadRequestError, NotFoundError, OpenAI

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


def message(text):
    return {"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": text}]}]}


def run(session, prompt, respond=False, cancel=False):
    events, handled = [], False
    with sessions.events.stream(session, timeout=150) as stream:
        sessions.events.create(session, events=[message(prompt)], idempotency_key=str(uuid.uuid4()))
        for event in stream:
            events.append(event.to_dict())
            if event.type == "agent.session.requires_action":
                assert not handled and (respond or cancel), event.to_dict()
                handled = True
                action = event.session.required_actions[0]
                assert action.name == "remember"
                if cancel:
                    sessions.events.create(session, events=[{"type": "agent.session.input.cancel"}])
                else:
                    proof.update(turn=action.turn_id, call=action.call_id)
                    payload = {"type": "agent.session.input.tool_result", "turn_id": action.turn_id,
                               "call_id": action.call_id, "success": True,
                               "output": [{"type": "input_text", "text": proof["memory"]}]}
                    for _ in range(2):
                        sessions.events.create(session, events=[payload], idempotency_key="same-tool-result")
            assert event.type not in {"agent.session.failed", "agent.session.turn.failed"}, event.to_dict()
            if event.type == "agent.session.idle":
                break
        else:
            raise AssertionError("stream closed without idle")
    proof.setdefault("runs", []).append(events)
    Path(evidence).write_text(json.dumps(proof, ensure_ascii=False, indent=2))
    assert len({e["event_id"] for e in events}) == len(events)
    turns = sessions.turns.list(session, order="asc", limit=100).data
    assert turns[-1].status == ("cancelled" if cancel else "completed"), turns[-1].to_dict()
    # Claude retains native counters; the public breakdown remains unqualified.
    assert turns[-1].usage is None
    terminal = "agent.session.turn." + ("cancelled" if cancel else "completed")
    assert [e["type"] for e in events].index(terminal) < [e["type"] for e in events].index("agent.session.idle")
    return events, turns[-1]


def final(session, events=None):
    items = sessions.items.list(session, order="asc", limit=100).data
    answers = [item for item in items if item.type == "message" and item.role == "assistant" and item.phase == "final_answer"]
    assert answers
    text = answers[-1].content[0].text
    assert json.loads(text) == {"memory": proof["memory"]}, text
    with httpx2.Client(trust_env=False) as raw:
        response = raw.get(base + "/v1/agents/sessions/" + session + "/items", headers=headers, params={"order": "asc", "limit": 100})
        assert response.status_code == 200
        assert any(i["id"] == answers[-1].id and i["content"][0]["text"] == text for i in response.json()["data"])
    if events is not None:
        item_id = answers[-1].id
        added = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.item.added" and e["item"]["id"] == item_id)
        done = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.item.done" and e["item"]["id"] == item_id)
        terminal = next(i for i, e in enumerate(events) if e["type"] == "agent.session.turn.completed")
        assert added < done < terminal
        assert next(e["text"] for e in events if e["type"] == "agent.session.turn.output_text.done" and e["item_id"] == item_id) == text
    return answers[-1].id


try:
    if stage == "initial":
        schema = {"type": "object", "properties": {"memory": {"type": "string"}}, "required": ["memory"], "additionalProperties": False}
        config = {"model": model, "text": {"format": {"type": "json_schema", "schema": schema}},
                  "tools": [{"type": "function", "name": "remember", "description": "Return a private memory value.",
                             "parameters": {"type": "object", "properties": {}, "additionalProperties": False}}]}
        saved = client.beta.agents.create(**config)
        session = sessions.create(agent_id=saved.id, environment={"type": "none"})
        proof.update(session=session.id, agent=saved.id, memory=str(uuid.uuid4()), format=config["text"]["format"])
        assert session.agent.text.format.to_dict() == proof["format"]
        events, turn = run(session.id, "Call remember exactly once and return its exact memory value as the requested JSON. Do not invent it.", respond=True)
        proof.update(first_events=events, first_output=final(session.id, events))
        assert len([i for i in sessions.items.list(session.id, limit=100).data if i.type == "function_call"]) == 1
        try:
            other.beta.agents.sessions.retrieve(session.id)
            raise AssertionError("cross-tenant read accepted")
        except NotFoundError:
            pass
        try:
            other.beta.agents.sessions.create(agent_id=saved.id, environment={"type": "none"})
            raise AssertionError("cross-tenant Agent reference accepted")
        except NotFoundError:
            pass
        # Saving arbitrary schemas remains separate from runtime qualification.
        huge = client.beta.agents.create(model=model, text={"format": {"type": "json_schema", "schema": {"type": "object", "const": 9007199254740993}}})
        assert huge.text.format.to_dict()["schema"]["const"] == 9007199254740993
        try:
            sessions.create(agent_id=huge.id, environment={"type": "none"})
            raise AssertionError("lossy runtime schema accepted")
        except BadRequestError:
            pass
        client.beta.agents.delete(huge.id)
    else:
        session = sessions.retrieve(proof["session"])
        assert session.agent.text.format.to_dict() == proof["format"]
        assert final(session.id) == proof["first_output"]
        events, turn = run(session.id, "Recall the exact memory from the previous turn and return it using the same JSON format. Do not call remember again.")
        proof.update(resume_events=events, resumed_output=final(session.id, events), resumed_turn=turn.id)
        assert proof["resumed_output"] != proof["first_output"]
        assert len(sessions.turns.list(session.id).data) == 2
        assert len([i for i in sessions.items.list(session.id, limit=100).data if i.type == "function_call"]) == 1
        cancelled = sessions.create(agent_id=proof["agent"], environment={"type": "none"})
        events, _ = run(cancelled.id, "Call remember to obtain the memory, then return it as JSON.", cancel=True)
        assert not any(i.type == "message" and i.role == "assistant" and i.phase == "final_answer" for i in sessions.items.list(cancelled.id, limit=100).data)
        proof["cancel_events"] = events
        plain = sessions.create(agent_id=proof["agent"], agent={"text": {"format": {"type": "text"}}}, environment={"type": "none"})
        events, _ = run(plain.id, "Do not use tools. Say PLAIN_OK in ordinary text.")
        assert any(i.type == "message" and i.role == "assistant" and "PLAIN_OK" in i.content[0].text for i in sessions.items.list(plain.id, limit=100).data)
        proof["plain_events"] = events
        sessions.delete(cancelled.id)
        sessions.delete(plain.id)
        proof["passed"] = True
    Path(evidence).write_text(json.dumps(proof, ensure_ascii=False, indent=2))
finally:
    Path(evidence).write_text(json.dumps(proof, ensure_ascii=False, indent=2))
    client.close()
    other.close()
