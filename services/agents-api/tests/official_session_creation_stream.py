"""Fixed SDK and raw HTTP checks for creation streams on a paused worker."""
import json
import uuid


def event_data(lines):
    for line in lines:
        if line.startswith("data: "):
            return json.loads(line[6:])
    raise AssertionError("stream ended before an event")


def verify_creation_streams(client, raw, base, headers, foreign, unsupported):
    sessions = client.beta.agents.sessions
    spec = {"agent": {"model": "test-model"}, "environment": {"type": "none"}}
    saved = client.beta.agents.create(model="test-model", instructions="Saved stream configuration.")
    forms = ["First", [{"role": "user", "content": [{"type": "input_text", "text": "First"}]},
                              {"role": "user", "content": [{"type": "input_text", "text": "Second"}]}]]
    cancel = [{"type": "agent.session.input.cancel"}]

    def message(text):
        return [{"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": text}]}]}]

    for index, initial in enumerate(forms):
        config = spec if index != 1 else {"agent_id": saved.id, "environment": {"type": "none"}}
        request = {**config, "input": initial}
        key = {"Idempotency-Key": str(uuid.uuid4())}
        count = 2 if index == 1 else 1
        observed = []
        # The pinned-SDK loop ends because Core closes the creation stream right
        # after the initial Turn settles at idle.
        with sessions.create(**request, stream=True, extra_headers=key) as stream:
            for event in stream:
                observed.append(event)
                if len(observed) == 1:
                    assert event.type == "agent.session.created"
                    assert set(event.to_dict()) == {"type", "event_id", "session"}
                    session = event.session
                    # The snapshot is the committed post-admission Session, identical
                    # to the JSON 201 body that a same-key retry recovers.
                    assert session.status == "in_progress" and session.required_actions == [] and session.usage is None
                    reply = raw.post(base + "/v1/agents/sessions", headers={**headers, **key}, json=request)
                    assert reply.status_code == 201 and reply.json() == event.to_dict()["session"]
                    if index == 1:
                        assert session.agent.id == saved.id and session.agent.instructions == saved.instructions
                elif event.type == "agent.session.in_progress":
                    sessions.events.create(session.id, events=cancel)
                assert len(observed) < 32, "creation stream did not end at idle"
        assert [event.type for event in observed] == (["agent.session.created", "agent.session.turn.created"] + ["agent.session.turn.item.added"] * count +
                                                       ["agent.session.in_progress", "agent.session.turn.cancelled", "agent.session.idle"])
        assert len({event.event_id for event in observed}) == len(observed)
        turns = list(sessions.turns.list(session.id))
        assert len(turns) == 1 and observed[1].turn.id == turns[0].id
        assert observed[2 + count].session.status == "in_progress"
        items = list(sessions.items.list(session.id, order="asc"))
        assert [event.item.to_dict() for event in observed[2:2 + count]] == [item.to_dict() for item in items]
        assert [item.content[0].text for item in items] == (["First", "Second"] if count == 2 else ["First"])
        # Terminal Turn events carry the Turn snapshot's usage, null when unknown.
        terminal = observed[-2].to_dict()
        assert set(terminal) == {"type", "event_id", "session_id", "turn_id", "turn", "usage"}
        assert terminal["usage"] is None and terminal["usage"] == terminal["turn"]["usage"]
        assert all("usage" not in event.to_dict() for event in observed if event is not observed[-2])
        assert observed[-1].session.status == "idle"

        # GET stays open after idle and observes later Turns; disconnect never cancels them.
        with sessions.events.stream(session.id) as live:
            sessions.events.create(session.id, events=message("Next"))
            assert [next(live).type for _ in range(3)] == ["agent.session.turn.created", "agent.session.turn.item.added", "agent.session.in_progress"]
            sessions.events.create(session.id, events=cancel)
            assert [next(live).type for _ in range(2)] == ["agent.session.turn.cancelled", "agent.session.idle"]
            sessions.events.create(session.id, events=message("After idle"))
            assert next(live).type == "agent.session.turn.created"
        current = sessions.retrieve(session.id)
        assert current.status == "in_progress", "disconnect cancelled admitted work"
        assert sessions.create(**request, stream=False, extra_headers=key).id == session.id
        assert len(list(sessions.turns.list(session.id))) == 3
        sessions.events.create(session.id, events=cancel)

        # A same-key stream retry of an existing creation admits nothing and ends
        # at once, whether the Session is settled or running another client's Turn:
        # no created snapshot, Turn, Item or later event is sent.
        def retry_stream():
            with raw.stream("POST", base + "/v1/agents/sessions", headers={**headers, **key, "Last-Event-ID": observed[0].event_id},
                            json={**request, "stream": True}) as response:
                assert response.status_code == 201 and response.headers["content-type"] == "text/event-stream"
                assert [line for line in response.iter_lines() if line] == [": connected"]

        retry_stream()
        assert len(list(sessions.turns.list(session.id))) == 3
        sessions.events.create(session.id, events=message("After retry"))
        assert sessions.retrieve(session.id).status == "in_progress"
        retry_stream()
        assert len(list(sessions.turns.list(session.id))) == 4
        sessions.events.create(session.id, events=cancel)
        # The pinned SDK sees the empty retry stream end without events.
        with sessions.create(**request, stream=True, extra_headers=key) as stream:
            assert list(stream) == []
        assert len(list(sessions.turns.list(session.id))) == 4
        response = raw.get(base + "/v1/agents/sessions/" + session.id, headers={**headers, "Authorization": "Bearer " + foreign})
        assert response.status_code == 404

    # Raw first-frame shape, early disconnect and same-key JSON recovery.
    key = {"Idempotency-Key": str(uuid.uuid4())}
    request = {**spec, "input": "Disconnect after creation"}
    with raw.stream("POST", base + "/v1/agents/sessions", headers={**headers, **key}, json={**request, "stream": True}) as response:
        event = event_data(response.iter_lines())
        assert set(event) == {"type", "event_id", "session"} and event["type"] == "agent.session.created"
    recovered = sessions.create(**request, extra_headers=key)
    assert recovered.id == event["session"]["id"] and recovered.status == "in_progress"
    reply = raw.post(base + "/v1/agents/sessions", headers={**headers, **key}, json=request)
    assert reply.status_code == 201 and reply.json() == event["session"]
    assert len(list(sessions.turns.list(recovered.id))) == 1
    sessions.events.create(recovered.id, events=[{"type": "agent.session.input.cancel"}])

    before = {session.id for session in sessions.list()}
    for changed_headers, fields, status in [({"Authorization": "Bearer invalid"}, {}, 401),
                                            ({"OpenAI-Beta": ""}, {}, 400),
                                            ({}, {"input": []}, 400),
                                            ({}, {"stream": None}, 400),
                                            ({**key}, {"input": "Changed"}, 409)]:
        response = raw.post(base + "/v1/agents/sessions", headers={**headers, **changed_headers},
                            json={**request, "stream": True, **fields})
        assert response.status_code == status and response.headers["content-type"].startswith("application/json")
    response = raw.post(unsupported + "/v1/agents/sessions", headers=headers, json={**request, "stream": True})
    assert response.status_code == 400 and response.headers["content-type"].startswith("application/json")
    assert {session.id for session in sessions.list()} == before
    print("Creation streams: fixed SDK/raw HTTP, initial admission, post-admission snapshots, Turn start order, terminal usage, settlement closure, live GET continuation, saved Agents, immediately ending retries, disconnect recovery and pre-stream errors passed.")
