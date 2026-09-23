"""Check Session-create field presence against the pinned SDK request types."""

import uuid

import httpx2
from openai import BadRequestError


def verify_session_create_requests(client, spec):
    # SessionCreateParamsBase permits null metadata, not null string values;
    # agent_id is str and stream is a boolean literal union without None.
    sessions = client.beta.agents.sessions
    before = {session.id for session in sessions.list()}
    # Non-string metadata values use the official code and metadata.<key> param.
    invalid = [
        ({"stream": None}, None), ({"stream": "false"}, None), ({"stream": 0}, None),
        ({"agent_id": None}, None), ({"agent_id": 0}, None),
        ({"metadata": {"label": None}}, "metadata.label"),
        ({"metadata": {"empty": "", "label": None}}, "metadata.label"),
        ({"metadata": {"label": 0}}, "metadata.label"), ({"metadata": []}, None),
    ]
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        for fields, param in invalid:
            code = "invalid_request_error" if param else "invalid_request"
            response = raw.post(str(client.base_url).rstrip("/") + "/agents/sessions",
                                headers=headers, json={**spec, **fields})
            assert response.status_code == 400, (fields, response.status_code)
            assert response.json()["error"]["code"] == code and response.json()["error"]["param"] == param
            try:
                sessions.create(**spec, extra_body=fields)
            except BadRequestError as error:
                assert error.body["code"] == code and error.body["param"] == param
            else:
                raise AssertionError(f"Official client accepted invalid fields: {fields}")
        assert {session.id for session in sessions.list()} == before

        key = {"Idempotency-Key": str(uuid.uuid4())}
        first = sessions.create(**spec, extra_headers=key)
        assert first.metadata == {}
        for fields in [{}, {"stream": False}, {"metadata": None}, {"metadata": {}},
                       {"stream": False, "metadata": None}]:
            assert sessions.create(**spec, extra_body=fields, extra_headers=key) == first
            response = raw.post(str(client.base_url).rstrip("/") + "/agents/sessions",
                                headers={**headers, **key}, json={**spec, **fields})
            assert response.status_code == 201
            assert response.json()["id"] == first.id and response.json()["metadata"] == {}
        metadata = {"empty": "", "label": "中文🧪"}
        preserved = sessions.create(**spec, metadata=metadata)
        assert sessions.retrieve(preserved.id).metadata == metadata
    print("Session create requests: raw HTTP and pinned SDK null rejection, no writes on rejection, default equivalence, idempotency and exact metadata passed.")
    return [first, preserved]
