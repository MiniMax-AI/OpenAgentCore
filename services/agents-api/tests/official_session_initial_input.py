"""Pinned client and raw HTTP acceptance for atomic initial text admission."""
import json
import sys
import uuid

sys.dont_write_bytecode = True

import httpx2
from openai import OpenAI
from official_session_creation_stream import verify_creation_streams


def main():
    base, token, foreign, unsupported = sys.argv[1:]
    headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
    spec = {"agent": {"model": "test-model", "instructions": "Keep the snapshot."}, "environment": {"type": "none"}}
    with OpenAI(api_key=token, base_url=base + "/v1", max_retries=0,
                _strict_response_validation=True, http_client=httpx2.Client(trust_env=False)) as client, httpx2.Client(trust_env=False) as raw:
        sessions = client.beta.agents.sessions
        saved = client.beta.agents.create(model="test-model", instructions="Saved instructions.")
        before = {session.id for session in sessions.list()}
        for fields in ({}, {"input": None}):
            rejected = raw.post(base + "/v1/agents/sessions", headers=headers, json={**spec, **fields})
            assert rejected.status_code == 400 and rejected.json()["error"]["code"] == "invalid_request_error"
        assert {session.id for session in sessions.list()} == before
        idle = sessions.create(**spec, input="Verify empty event no-op preserves admitted history.")
        sessions.events.create(idle.id, events=[{"type": "agent.session.input.cancel"}])
        idle = sessions.retrieve(idle.id)
        prior_turns = list(sessions.turns.list(idle.id))
        prior_items = list(sessions.items.list(idle.id))
        assert len(prior_turns) == len(prior_items) == 1 and prior_turns[0].status == "cancelled"
        empty_headers = {**headers, "Idempotency-Key": str(uuid.uuid4())}
        empty_endpoint = base + "/v1/agents/sessions/" + idle.id + "/events"
        for _ in range(2):
            response = raw.post(empty_endpoint, headers=empty_headers, json={"events": []})
            assert response.status_code == 202 and response.content == b""
        assert sessions.retrieve(idle.id) == idle
        assert list(sessions.turns.list(idle.id)) == prior_turns and list(sessions.items.list(idle.id)) == prior_items
        foreign_empty = raw.post(empty_endpoint, headers={**empty_headers, "Authorization": "Bearer " + foreign}, json={"events": []})
        assert foreign_empty.status_code == 404
        # Empty requests do not consume a nonempty batch's retry identity.
        response = raw.post(empty_endpoint, headers=empty_headers, json={"events": [{"type": "agent.session.input.cancel"}]})
        assert response.status_code == 202 and response.content == b""
        response = raw.post(empty_endpoint, headers=empty_headers, json={"events": []})
        assert response.status_code == 202 and response.content == b""
        assert sessions.retrieve(idle.id) == idle
        forms = ["First", [{"role": "user", "content": [{"type": "input_text", "text": "First"}]},
                           {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Second"}]}]]
        for i, initial in enumerate(forms):
            configuration = spec if i == 0 else {"agent_id": saved.id, "environment": {"type": "none"}}
            key = {"Idempotency-Key": str(uuid.uuid4())}
            session = sessions.create(**configuration, input=initial, stream=False, extra_headers=key)
            assert session.status == "in_progress"
            assert session.agent.instructions == ("Keep the snapshot." if i == 0 else saved.instructions)
            turns = list(sessions.turns.list(session.id))
            assert len(turns) == 1
            items = list(sessions.items.list(session.id, order="asc"))
            assert [item.content[0].text for item in items] == (["First"] if i == 0 else ["First", "Second"])
            reply = raw.post(base + "/v1/agents/sessions", headers={**headers, **key},
                             json={**configuration, "input": initial})
            assert reply.status_code == 201 and reply.json()["id"] == session.id
            assert [turn.id for turn in sessions.turns.list(session.id)] == [turns[0].id]
            denied = raw.get(base + "/v1/agents/sessions/" + session.id,
                             headers={**headers, "Authorization": "Bearer " + foreign})
            assert denied.status_code == 404
            changed = raw.post(base + "/v1/agents/sessions", headers={**headers, **key},
                               json={**configuration, "input": "Changed"})
            assert changed.status_code == 409
            sessions.events.create(session.id, events=[{"type": "agent.session.input.cancel"}])
            assert sessions.create(**configuration, input=initial, extra_headers=key).status == "idle"
            assert len(list(sessions.turns.list(session.id))) == 1

        verify_creation_streams(client, raw, base, headers, foreign, unsupported)

        # Whitespace-only text is content: admitted and kept verbatim (SES-01..04).
        def user_texts(session_id):
            return [[part.text for part in item.content] for item in sessions.items.list(session_id, order="asc")]
        for initial, texts in (("   ", [["   "]]), ("\n\t", [["\n\t"]]),
                               ([{"role": "user", "content": [{"type": "input_text", "text": "\n\t"}]}], [["\n\t"]])):
            session = sessions.create(**spec, input=initial)
            assert session.status == "in_progress" and user_texts(session.id) == texts, (initial, user_texts(session.id))
        sessions.events.create(session.id, events=[{"type": "agent.session.input.cancel"}])
        sessions.events.create(session.id, events=[{"type": "agent.session.input.message", "input": [
            {"role": "user", "content": [{"type": "input_text", "text": "   "}]},
            {"role": "user", "content": [{"type": "input_text", "text": "\n\t"}]}]}])
        assert user_texts(session.id) == [["\n\t"], ["   "], ["\n\t"]]
        assert len(list(sessions.turns.list(session.id))) == 2

        before = {session.id for session in sessions.list()}
        for fields in [{"input": [{"type": None, "role": "user", "content": [{"type": "input_text", "text": "x"}]}]}, {"input": 0}, {"input": {}}, {"input": []}, {"input": ""},
                       {"input": [{"role": "user", "content": []}]}, {"input": [{"role": "user", "content": [{"type": "input_text", "text": ""}]}]},
                       {"input": [{"role": "assistant", "content": [{"type": "input_text", "text": "x"}]}]},
                       {"input": [{"role": "user", "content": [{"type": "input_image", "image_url": "https://example.com/x.png"}]}]}]:
            reply = raw.post(base + "/v1/agents/sessions", headers=headers, json={**spec, **fields})
            assert reply.status_code == 400, (fields, reply.status_code)
        reply = raw.post(unsupported + "/v1/agents/sessions", headers=headers, json={**spec, "input": "x"})
        assert reply.status_code == 400
        assert {session.id for session in sessions.list()} == before
    print("Initial text: pinned SDK/raw HTTP, saved and inline snapshots, null/omission, ordered Items, verbatim whitespace-only text, retries/cancellation, tenant isolation and no writes on rejection passed.")


if __name__ == "__main__":
    main()
