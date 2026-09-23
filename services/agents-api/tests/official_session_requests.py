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
        # Inline agent configuration errors use the official fields with the agent.
        # prefix, and precede the none input requirement (TV-01..04).
        lookup = {"type": "function", "name": "lookup", "description": "Look up a value.", "parameters": {"type": "object"}}
        agent_errors = [
            ({"tools": [{"type": "bogus_tool"}]}, "agent.tools[0].type", "Invalid value: 'bogus_tool'. Supported values are: 'function', 'tool_search', 'programmatic_tool_calling', 'mcp', and 'web_search'."),
            ({"tools": [{k: v for k, v in lookup.items() if k != "parameters"}]}, "agent.tools[0].parameters", "Missing required parameter: 'agent.tools[0].parameters'."),
            ({"reasoning": {"effort": "extreme"}}, "agent.reasoning.effort", "Invalid value: 'extreme'. Supported values are: 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', and 'max'."),
            ({"tool_choice": "auto"}, "agent.tool_choice", "Unknown parameter: 'agent.tool_choice'."),
            ({"tools": [lookup, {**lookup, "description": "Second."}]}, None, "duplicate function tool name: lookup"),
            ({"tools": [{**lookup, "parameters": {"type": "string"}}]}, None, "Invalid schema for function 'lookup': schema must be a JSON Schema of 'type: \"object\"', got 'type: \"string\"'."),
            ({"text": {"format": {"type": "json_schema", "schema": {"type": "array", "items": {"type": "string"}}}}}, None, 'agent.text.format.schema must have top-level type "object"; got "array"'),
        ]
        for fields, param, message in agent_errors:
            expected = {"type": "invalid_request_error", "code": "invalid_request_error", "param": param, "message": message}
            request = {**spec, "agent": {**spec["agent"], **fields}}
            for body in (request, {k: v for k, v in request.items() if k != "input"}):
                response = raw.post(str(client.base_url).rstrip("/") + "/agents/sessions", headers=headers, json=body)
                assert response.status_code == 400 and response.json()["error"] == expected, (fields, response.text)
            try:
                sessions.create(**request)
            except BadRequestError as error:
                assert error.body == expected, error.body
            else:
                raise AssertionError(f"Official client accepted invalid agent: {fields}")
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
    print("Session create requests: raw HTTP and pinned SDK null and inline agent configuration rejection, no writes on rejection, default equivalence, idempotency and exact metadata passed.")
    return [first, preserved]
