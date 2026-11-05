"""Public Automations through the fixed SDK, real delivery and PostgreSQL."""

import sys
import time

import httpx2
from openai import BadRequestError, ConflictError, NotFoundError, OpenAI

MESSAGE = [{"role": "user", "content": [{"type": "input_text", "text": "scheduled"}]}]


def fails(error, action, param=None):
    try:
        action()
    except error as e:
        if param is not None:
            assert e.response.json()["error"]["param"] == param, e.response.text
        return
    raise AssertionError("expected " + error.__name__)


def settled(read):
    deadline = time.time() + 20
    while time.time() < deadline:
        value = read()
        if value.status != "queued":
            return value
        time.sleep(0.2)
    raise AssertionError("delivery did not settle")


def main():
    base, token, foreign = sys.argv[1:]
    with httpx2.Client(trust_env=False, timeout=10) as http:
        make = lambda key: OpenAI(api_key=key, base_url=base + "/v1", http_client=http, max_retries=0, _strict_response_validation=True)
        client, other = make(token), make(foreign)
        agents = client.beta.agents
        automations, invocations = agents.automations, agents.automations.invocations
        saved = agents.create(model="saved-model", instructions="Saved instructions.")
        session = agents.sessions.create(input="hello", agent_id=saved.id, environment={"type": "none"})
        new_session = {"type": "new_session", "agent_id": saved.id, "environment": {"type": "none"}, "input": MESSAGE, "metadata": {"k": "v"}}
        existing = {"type": "existing_session", "session_id": session.id, "instructions": "wake up"}

        periodic = automations.create(type="periodic", name="yearly", expression="0 0 1 1 *", timezone="UTC", target=new_session, metadata={"a": "b"})
        assert periodic.status == "active" and periodic.created_by.type == "developer" and periodic.metadata == {"a": "b"}
        target = periodic.target
        assert target.agent_id == saved.id and target.agent is None and target.metadata == {"k": "v"} and target.vault_ids == [] and target.environment.type == "none"
        inline = automations.create(type="periodic", name="inline", expression="*/5 * * * *", timezone="Asia/Shanghai",
                                    target={"type": "new_session", "agent": {"model": "saved-model", "instructions": "Inline."}, "environment": {"type": "none"}, "input": MESSAGE})
        assert inline.target.agent.model == "saved-model" and inline.target.agent_id is None
        assert inline.target.agent.reasoning.effort is None and inline.target.agent.reasoning.summary is None
        # Reasoning keeps both members and a self-hosted environment lists its directories.
        rich = automations.create(type="periodic", name="rich", expression="0 0 1 1 *", timezone="UTC",
                                  target={"type": "new_session", "agent": {"model": "saved-model", "reasoning": {"effort": "low"}}, "environment": {"type": "none"}, "input": MESSAGE})
        assert rich.target.agent.reasoning.effort == "low" and rich.target.agent.reasoning.summary is None
        provider = {"x_agents_core": {"model_provider": {"protocol": "responses", "base_url": "https://model.fixture.example/v1", "api_key": "fixture-model-key"}}}
        hosted = agents.create(model="saved-model", extra_body=provider)
        own = automations.create(type="periodic", name="own", expression="0 0 1 1 *", timezone="UTC", target={"type": "new_session", "agent_id": hosted.id, "environment": {"type": "self_hosted", "workspace_directory": "/workspace"}, "input": MESSAGE})
        assert own.target.environment.type == "self_hosted" and own.target.environment.capability_directories == []
        for extra in (rich, own):
            automations.delete(extra.id)
        once = automations.create(type="once", name="later", run_at=int(time.time()) + 3600, target=existing)
        assert once.target.session_id == session.id and once.target.instructions == "wake up" and once.status == "active"
        for created in (periodic, inline, once):
            assert automations.retrieve(created.id) == created
        assert [a.id for a in automations.list().data] == [once.id, inline.id, periodic.id]
        assert [a.id for a in automations.list(order="asc", limit=2).data] == [periodic.id, inline.id]
        assert [a.id for a in automations.list(after=inline.id).data] == [periodic.id]
        assert [a.id for a in automations.list(session_id=session.id).data] == [once.id]
        assert automations.list(session_id="").data == []
        fails(NotFoundError, lambda: automations.list(after="missing"))
        fails(NotFoundError, lambda: other.beta.agents.automations.retrieve(periodic.id))

        # Update replaces whole fields and keeps the target type.
        updated = automations.update(periodic.id, name="renamed", metadata={}, expression="30 0 1 1 *")
        assert updated.name == "renamed" and updated.metadata == {} and updated.expression == "30 0 1 1 *" and automations.retrieve(periodic.id) == updated
        assert automations.update(once.id, target={**existing, "instructions": "changed"}).target.instructions == "changed"
        fails(BadRequestError, lambda: automations.update(once.id, target=new_session), "target.type")
        fails(BadRequestError, lambda: automations.update(periodic.id, run_at=int(time.time()) + 60), "run_at")
        fails(BadRequestError, lambda: automations.update(once.id, expression="* * * * *"), "expression")
        fails(BadRequestError, lambda: automations.update(once.id, run_at=int(time.time()) - 60), "run_at")

        # Pause holds scheduled delivery; both transitions are idempotent.
        assert automations.pause(periodic.id).status == "paused" and automations.pause(periodic.id).status == "paused"
        assert automations.resume(periodic.id).status == "active" and automations.resume(periodic.id).status == "active"

        # Manual invocations deliver for both target types.
        queued = invocations.create(once.id)
        assert queued.status == "queued" and queued.source.type == "manual" and queued.scheduled_at is None
        done = settled(lambda: invocations.retrieve(queued.id, automation_id=once.id))
        assert done.status == "completed" and done.session_id == session.id and done.error is None
        manual = invocations.create(periodic.id)
        created = settled(lambda: invocations.retrieve(manual.id, automation_id=periodic.id))
        assert created.status == "completed" and agents.sessions.retrieve(created.session_id).id == created.session_id
        listed = invocations.list(once.id)
        assert [i.id for i in listed.data] == [queued.id] and listed.data[0] == done

        # A one-time Automation runs at run_at and then completes.
        soon = automations.create(type="once", name="soon", run_at=int(time.time()) + 2, target=existing)
        deadline = time.time() + 20
        while automations.retrieve(soon.id).status != "completed":
            assert time.time() < deadline, "one-time automation did not complete"
            time.sleep(0.2)
        record = invocations.list(soon.id).data[0]
        assert record.status == "completed" and record.scheduled_at == soon.run_at and record.source.type == "automation" and record.source.automation.id == soon.id
        assert record.session_id == session.id
        fails(ConflictError, lambda: automations.pause(soon.id))
        fails(ConflictError, lambda: automations.resume(soon.id))
        fails(ConflictError, lambda: automations.update(soon.id, run_at=int(time.time()) + 60))

        # Rejected requests save nothing.
        count = len(automations.list(limit=100).data)
        periodic_args = {"type": "periodic", "name": "bad", "expression": "0 0 1 1 *", "timezone": "UTC", "target": new_session}
        fails(BadRequestError, lambda: automations.create(**periodic_args | {"expression": "bad"}), "expression")
        fails(BadRequestError, lambda: automations.create(**periodic_args | {"timezone": "Mars/Base"}), "timezone")
        fails(BadRequestError, lambda: automations.create(type="once", name="bad", run_at=int(time.time()) - 5, target=existing), "run_at")
        fails(BadRequestError, lambda: automations.create(type="once", name="bad", run_at=253402300800, target=existing), "run_at")
        fails(BadRequestError, lambda: automations.update(once.id, run_at=253402300800), "run_at")
        fails(BadRequestError, lambda: automations.create(**periodic_args | {"target": {**new_session, "environment": {"type": "openai_hosted", "environment_id": "env"}}}))
        fails(BadRequestError, lambda: automations.create(**periodic_args | {"target": {**new_session, "agent": {"model": "m"}}}), "target")
        inline_target = {k: v for k, v in new_session.items() if k != "agent_id"}
        fails(BadRequestError, lambda: automations.create(**periodic_args | {"target": inline_target | {"agent": {"instructions": "no model"}}}))
        fails(BadRequestError, lambda: automations.create(**periodic_args | {"target": inline_target | {"agent": {"model": "m", "tools": [{"type": "automations"}]}}}))
        fails(BadRequestError, lambda: automations.create(**periodic_args | {"target": {k: v for k, v in new_session.items() if k != "agent_id"} | {"agent": {"model": "m", "x_agents_core": {"harness": "codex"}}}}), "target.agent.x_agents_core")
        fails(BadRequestError, lambda: automations.create(**periodic_args | {"target": {**existing, "instructions": ""}}), "target.instructions")
        fails(NotFoundError, lambda: automations.create(**periodic_args | {"target": {**existing, "session_id": "missing"}}))
        # A Session named in another uuid form is saved under its canonical id.
        urn = automations.create(**periodic_args | {"name": "urn", "target": {**existing, "session_id": "urn:uuid:" + session.id}})
        assert urn.target.session_id == session.id and urn.id in [a.id for a in automations.list(session_id=session.id).data]
        automations.delete(urn.id)
        assert len(automations.list(limit=100).data) == count

        # Delete removes the Automation and its records, not the Session.
        deleted = automations.delete(periodic.id)
        assert deleted.id == periodic.id and deleted.deleted and deleted.object == "agent.automation.deleted"
        fails(NotFoundError, lambda: automations.retrieve(periodic.id))
        fails(NotFoundError, lambda: invocations.list(periodic.id))
        assert agents.sessions.retrieve(session.id).id == session.id


if __name__ == "__main__":
    main()
