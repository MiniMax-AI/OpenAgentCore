"""Project principal retries with synthetic configurations and no model execution.

Keys in one Project share the same principal and Session creation retries.
These checks exercise local storage and HTTP behavior, not hosted API semantics.
"""

import uuid

import httpx2
from openai import BadRequestError, NotFoundError

from official_session_creation_stream import event_data
from official_session_metadata import without_metadata


def assert_no_creator_fields(payload):
    private = {"creator", "creator_kind", "creator_id", "subject_kind", "subject_id"}
    assert private.isdisjoint(payload), payload


def verify_session_creators(client, owner, other, replacement_key, peer_key, additional_key, spec, expect_error):
    sessions = owner.beta.agents.sessions
    endpoint = str(owner.base_url).rstrip("/") + "/agents/sessions"
    auth = {"Authorization": "Bearer " + owner.api_key, "OpenAI-Beta": "agents=v1"}
    forged = {"X-User-ID": "test-peer", "X-Subject-Kind": "user", "X-Subject-ID": "test-peer",
              "X-Creator-Kind": "user", "X-Creator-ID": "test-peer"}
    metadata = {"creator_kind": "user", "creator_id": "test-peer"}
    recovered = []
    with client(replacement_key) as replacement, client(peer_key) as collaborator, client(additional_key) as additional_peer, \
            httpx2.Client(trust_env=False, timeout=10) as raw:

        def check_retries(request, key, current):
            turn_ids = [turn.id for turn in sessions.turns.list(current.id)]
            for caller in (owner, replacement, collaborator, additional_peer):
                assert caller.beta.agents.sessions.create(**request, extra_headers=key) == current
            for caller in (collaborator, additional_peer):
                headers = auth | key | forged | {"Authorization": "Bearer " + caller.api_key}
                with raw.stream("POST", endpoint, headers=headers, json=request | {"stream": True}) as response:
                    assert response.status_code == 201
                    assert response.headers["content-type"].split(";")[0] == "text/event-stream"
                    # Creation retries close without replaying historical events.
                    assert [line for line in response.iter_lines() if line] == [": connected"]
                assert caller.beta.agents.sessions.create(**request, extra_headers=key) == current
                assert caller.beta.agents.sessions.retrieve(current.id) == current
                assert [turn.id for turn in caller.beta.agents.sessions.turns.list(current.id)] == turn_ids
            response = raw.post(endpoint, headers=auth | key, json=request)
            assert response.status_code == 201 and response.json() == current.to_dict()
            assert_no_creator_fields(response.json())

        # Inline requests reach the authoritative creation upsert. Untrusted
        # metadata and forwarded identity headers cannot choose the creator.
        request = spec | {"metadata": metadata}
        key = {"Idempotency-Key": str(uuid.uuid4())}
        first = sessions.create(**request, extra_headers=key | forged)
        assert first.status == "in_progress" and first.metadata == metadata
        check_retries(request, key, first)
        foreign = other.beta.agents.sessions.create(**request, extra_headers=key)
        assert foreign.id != first.id
        expect_error(NotFoundError, lambda: other.beta.agents.sessions.retrieve(first.id))
        expect_error(NotFoundError, lambda: collaborator.beta.agents.sessions.retrieve(foreign.id))

        # Project keys share reads, metadata writes and the same creator principal.
        for caller in (collaborator, additional_peer):
            assert caller.beta.agents.sessions.retrieve(first.id) == first
            assert first.id in {item.id for item in caller.beta.agents.sessions.list()}
            assert len(list(caller.beta.agents.sessions.turns.list(first.id))) == 1
            assert [item.content[0].text for item in caller.beta.agents.sessions.items.list(first.id)] == [request["input"]]
        current = collaborator.beta.agents.sessions.update(first.id, metadata={"creator_id": "test-peer"})
        assert without_metadata(current) == without_metadata(first)
        assert_no_creator_fields(current.to_dict())
        check_retries(request, key, current)
        recovered.append((request, key, current))
        response = raw.get(endpoint + "/" + current.id, headers=auth | forged)
        assert response.status_code == 200 and response.json() == current.to_dict()
        assert_no_creator_fields(response.json())
        response = raw.get(endpoint, headers=auth, params={"agent_id": current.agent.id})
        assert response.status_code == 200
        assert [item["id"] for item in response.json()["data"]] == [current.id]
        assert_no_creator_fields(response.json()["data"][0])
        for fields in ({"creator_kind": "user", "creator_id": "test-peer"},
                       {"creator": {"kind": "user", "id": "test-peer"}}):
            expect_error(BadRequestError, lambda: sessions.create(**spec, extra_body=fields))
            expect_error(BadRequestError, lambda: sessions.update(current.id, extra_body=fields))
        check_retries(request, key, current)

        # Saved references recover before source resolution, even after a peer
        # changes or deletes the source Agent.
        source = owner.beta.agents.create(model="creator-fixture-model", instructions="Frozen source.")
        request = {"input": "Verify session creators fixture admission.", "agent_id": source.id, "environment": {"type": "none"}, "metadata": metadata}
        key = {"Idempotency-Key": str(uuid.uuid4())}
        saved = sessions.create(**request, extra_headers=key | forged)
        check_retries(request, key, saved)
        collaborator.beta.agents.update(source.id, model="updated-fixture-model", instructions="Changed source.")
        check_retries(request, key, saved)
        assert saved.agent.model == "creator-fixture-model" and saved.agent.instructions == "Frozen source."
        collaborator.beta.agents.delete(source.id)
        expect_error(NotFoundError, lambda: sessions.create(**request))
        saved = additional_peer.beta.agents.sessions.update(saved.id, metadata={"shared": "updated"})
        check_retries(request, key, saved)
        recovered.append((request, key, saved))

        # Streaming creation uses the same Project principal for every key;
        # all Project keys may recover the retry or delete the Session.
        key = {"Idempotency-Key": str(uuid.uuid4())}
        headers = auth | key | forged | {"Authorization": "Bearer " + additional_peer.api_key}
        with raw.stream("POST", endpoint, headers=headers, json=spec | {"stream": True}) as response:
            assert response.status_code == 201 and response.headers["content-type"] == "text/event-stream"
            created = event_data(response.iter_lines())
            assert created["type"] == "agent.session.created"
            assert_no_creator_fields(created["session"])
        streamed = additional_peer.beta.agents.sessions.create(**spec, extra_headers=key)
        assert streamed.id == created["session"]["id"]
        for caller in (owner, collaborator):
            assert caller.beta.agents.sessions.create(**spec, extra_headers=key) == streamed
        collaborator.beta.agents.sessions.events.create(streamed.id, events=[{"type": "agent.session.input.cancel"}])
        deleted = collaborator.beta.agents.sessions.delete(streamed.id)
        assert deleted.deleted is True and deleted.id == streamed.id
        expect_error(NotFoundError, lambda: additional_peer.beta.agents.sessions.retrieve(streamed.id))

    print("Session creator local policy: shared Project principal, multiple keys, cross-Project isolation, immutable retries, source update/deletion, private wire fields and streaming retries passed; no model execution or hosted semantics verified.")
    return recovered


def verify_creator_recovery(client, replacement_key, peer_key, additional_key, retries, expect_error):
    with client(replacement_key) as creator, client(peer_key) as collaborator, client(additional_key) as additional_peer:
        for request, key, current in retries:
            assert creator.beta.agents.sessions.create(**request, extra_headers=key) == current
            for caller in (collaborator, additional_peer):
                assert caller.beta.agents.sessions.retrieve(current.id) == current
                assert caller.beta.agents.sessions.create(**request, extra_headers=key) == current
    print("Session creator local policy: all Project keys recovered the same creation retries after service restart.")
