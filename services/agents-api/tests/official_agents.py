"""Reusable Agent resource checks against the fixed SDK and real HTTP service."""

import time
import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, NotFoundError

import official_body


def verify_agents(client, other, invalid, expect_error):
    agents = client.beta.agents
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    base = str(client.base_url).rstrip("/") + "/agents"
    saved = []
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        # These assertions cover known resource defaults; they do not assert a
        # model-derived reasoning default that the service cannot yet resolve.
        for values in ({}, {"name": None, "instructions": None, "metadata": None,
                            "multi_agent": None, "reasoning": None,
                            "service_tier": None, "text": None, "tools": None}):
            response = agents.with_raw_response.create(model=" caller-model ", **values)
            assert response.status_code == 201
            body, agent = response.http_response.json(), response.parse()
            assert set(body) == {"id", "object", "created_at", "updated_at", "metadata", "model", "name",
                                 "instructions", "multi_agent", "reasoning", "service_tier", "text", "tools"}
            assert body["object"] == "agent" and body["model"] == " caller-model "
            assert body["name"] is None and body["instructions"] is None and body["metadata"] == {}
            assert body["multi_agent"] == {"enabled": False, "max_concurrent_subagents": None}
            assert body["text"] == {"format": {"type": "text"}, "verbosity": "medium"}
            # Both reasoning keys are present; the model-derived effort stays unresolved.
            assert body["reasoning"] == {"effort": None, "summary": None}
            assert body["tools"] == []
            assert agent.created_at == agent.updated_at and abs(agent.created_at - time.time()) < 10
            assert agents.retrieve(agent.id) == agent
            saved.append(agent)
        schema = {"type": "object", "properties": {"number": {"const": 9007199254740993}}}
        tools = [{"type": "function", "name": "lookup", "description": "", "parameters": schema,
                  "defer_loading": True}, {"type": "tool_search"}, {"type": "programmatic_tool_calling"}]
        metadata = {"empty": "", "🧪" * 64: "值" * 512}
        request = {"model": "arbitrary-provider-model", "name": " ", "instructions": " preserve whitespace ",
                   "metadata": metadata, "multi_agent": {"enabled": True},
                   "reasoning": {"effort": "max", "summary": "detailed"}, "service_tier": "fast",
                   "text": {"format": {"type": "json_schema", "schema": schema}, "verbosity": "high"},
                   "tools": tools}
        response = agents.with_raw_response.create(**request)
        assert response.status_code == 201
        body, agent = response.http_response.json(), response.parse()
        for field in ("model", "name", "instructions", "metadata", "reasoning", "service_tier", "text"):
            assert body[field] == request[field], field
        assert body["multi_agent"] == {"enabled": True, "max_concurrent_subagents": 6}
        assert body["tools"] == [*tools[:2], {"type": "programmatic_tool_calling", "enabled": True}]
        assert raw.get(base + "/" + agent.id, headers=headers).json() == body
        assert agents.retrieve(agent.id) == agent
        saved.append(agent)
        # Function names are not restricted, as officially (TV-07).
        for fields in [
            {"tools": [{"type": "function", "name": name, "description": "", "parameters": {"type": "object"}}
                       for name in ("bad name!", "n" * 65)]},
            {"model": "", "name": "🧪" * 128, "instructions": "", "metadata": {}},
            {"multi_agent": {"enabled": True, "max_concurrent_subagents": 4294967295}},
            {"multi_agent": {"enabled": False, "max_concurrent_subagents": 4}},
            {"text": {"format": None, "verbosity": None}},
            {"tools": [{"type": "function", "name": "", "description": "", "parameters": {}}]},
            {"tools": [{"type": "programmatic_tool_calling", "enabled": False}]},
        ]:
            item = agents.create(**{"model": "resource-model", **fields})
            assert agents.retrieve(item.id) == item
            saved.append(item)
        assert saved[-1].tools[0].enabled is False
        assert saved[-2].tools[0].defer_loading is False
        assert saved[-4].multi_agent.max_concurrent_subagents is None
        for tier in ("auto", "default", "flex", "priority", "fast"):
            item = agents.create(model="resource-model", service_tier=tier)
            assert item.service_tier == tier
            saved.append(item)
        for effort in ("none", "minimal", "low", "medium", "high", "xhigh", "max"):
            item = agents.create(model="resource-model", reasoning={"effort": effort})
            assert item.reasoning.effort == effort
            saved.append(item)

        invalid_fields = [
            {"model": None}, {"model": 4}, {"name": "x" * 129}, {"instructions": False},
            {"metadata": {"bad": None}}, {"metadata": {"x" * 65: "v"}},
            {"metadata": {str(i): "v" for i in range(17)}}, {"metadata": {"x": "v" * 513}},
            {"tenant_id": str(uuid.uuid4())}, {"reasoning": {"effort": "automatic"}},
            {"reasoning": {"summary": "never"}}, {"reasoning": {"unknown": 1}},
            {"service_tier": "premium"}, {"multi_agent": {}}, {"multi_agent": {"enabled": None}},
            {"multi_agent": {"enabled": True, "max_concurrent_subagents": None}},
            {"multi_agent": {"enabled": False, "max_concurrent_subagents": 0}},
            {"multi_agent": {"enabled": True, "max_concurrent_subagents": 1.5}},
            {"multi_agent": {"enabled": True, "max_concurrent_subagents": 4294967296}},
            {"text": {"format": {}}}, {"text": {"format": {"type": None}}},
            {"text": {"format": {"type": "text", "schema": {}}}},
            {"text": {"format": {"type": "json_schema", "schema": None}}},
            {"text": {"format": {"type": "json_schema", "schema": []}}},
            {"text": {"verbosity": "automatic"}}, {"text": {"unknown": True}},
            {"tools": [None]}, {"tools": [{"type": "function", "name": None}]},
            {"tools": [{"type": "function", "name": "x", "description": "", "parameters": None}]},
            {"tools": [{"type": "function", "name": "x", "description": "", "parameters": {}, "defer_loading": None}]},
            {"tools": [{"type": "programmatic_tool_calling", "enabled": None}]},
            {"tools": [{"type": "tool_search", "unknown": 1}]},
        ]
        for fields in invalid_fields:
            response = raw.post(base, headers=headers, json={"model": "resource-model", **fields})
            assert response.status_code == 400, (fields, response.status_code)
            assert response.json()["error"]["type"] == "invalid_request_error"
            expect_error(BadRequestError, lambda: agents.create(model="resource-model", extra_body=fields))
        # Rejections with official evidence report its code, param and message.
        lookup = {"type": "function", "name": "lookup", "description": "Look up a value.", "parameters": {"type": "object"}}
        field_errors = [
            ({"name": "x" * 129}, "name", "Invalid 'name': string too long. Expected a string with maximum length 128, but got a string with length 129 instead."),
            ({"metadata": {"k": 1}}, "metadata.k", "Invalid type for 'metadata.k': expected a string, but got an integer instead."),
            ({"metadata": {"k": None}}, "metadata.k", "Invalid type for 'metadata.k': expected a string, but got null instead."),
            ({"metadata": {str(i): "v" for i in range(17)}}, "metadata", "Invalid 'metadata': too many properties. Expected an object with at most 16 properties, but got an object with 17 properties instead."),
            ({"metadata": {"x" * 65: "v"}}, "metadata." + "x" * 65, "Invalid property name in 'metadata': '" + "x" * 65 + "' is too long. Expected a string with maximum length 64, but got a string with length 65 instead."),
            ({"metadata": {"k": "v" * 513}}, "metadata.k", "Invalid 'metadata.k': string too long. Expected a string with maximum length 512, but got a string with length 513 instead."),
            ({"metadata": {"k": "a\x00b"}}, "metadata.k", "Invalid 'metadata.k': string contains U+0000, which this service cannot store."),
            # Configuration protocol errors (TV-01..03); conflicts have a null param.
            ({"tools": [{**lookup, "parameters": []}]}, "tools[0].parameters", "Invalid type for 'tools[0].parameters': expected an object with string keys and unknown value values, but got an array instead."),
            ({"tools": [{k: v for k, v in lookup.items() if k != "parameters"}]}, "tools[0].parameters", "Missing required parameter: 'tools[0].parameters'."),
            ({"tools": [{"type": "tool_search", "max_results": 3}]}, "tools[0].max_results", "Unknown parameter: 'tools[0].max_results'."),
            ({"tools": [{"type": "bogus_tool"}]}, "tools[0].type", "Invalid value: 'bogus_tool'. Supported values are: 'function', 'tool_search', 'programmatic_tool_calling', 'mcp', and 'web_search'."),
            ({"tool_choice": "auto"}, "tool_choice", "Unknown parameter: 'tool_choice'."),
            ({"text": {"format": {"type": "json_object"}}}, "text.format.type", "Invalid value: 'json_object'. Supported values are: 'text' and 'json_schema'."),
            ({"multi_agent": {"enabled": True, "max_concurrent_subagents": 0}}, "multi_agent.max_concurrent_subagents", "Invalid 'multi_agent.max_concurrent_subagents': integer below minimum value. Expected a value >= 1, but got 0 instead."),
            ({"tools": [lookup, {**lookup, "description": "Second."}]}, None, "duplicate function tool name: lookup"),
            ({"tools": [{"type": "tool_search"}, {"type": "tool_search"}]}, None, "duplicate tool_search tool"),
            ({"tools": [{**lookup, "parameters": {"type": "string"}}]}, None, "Invalid schema for function 'lookup': schema must be a JSON Schema of 'type: \"object\"', got 'type: \"string\"'."),
            ({"text": {"format": {"type": "json_schema", "schema": {"type": "array"}}}}, None, 'agent.text.format.schema must have top-level type "object"; got "array"'),
        ]
        count = len(list(agents.list()))
        for fields, param, message in field_errors:
            response = raw.post(base, headers=headers, json={"model": "resource-model", **fields})
            assert response.status_code == 400 and response.json()["error"] == {
                "type": "invalid_request_error", "code": "invalid_request_error", "param": param, "message": message}, response.text
        # PostgreSQL cannot store U+0000 in other strings either; this local limit has no field param.
        for fields in ({"name": "a\x00b"}, {"instructions": "a\x00b"}, {"model": "a\x00b"}):
            response = raw.post(base, headers=headers, json={"model": "resource-model", **fields})
            assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_request_error"
            assert response.json()["error"]["param"] is None
        assert len(list(agents.list())) == count
        # The shared body gate rejects before any write (HP-09..HP-15); a zero-length
        # body or null is {} and reports the missing model (HP-13).
        official_body.check(raw, base, headers, official_body.rejected('{"model":"resource-model","name":"gate"}', "name", "name"))
        json_headers = {**headers, **official_body.JSON}
        for content in ("", "{}", "null"):
            response = raw.post(base, headers=json_headers, content=content)
            assert response.status_code == 400 and response.json()["error"]["param"] == "model", response.text
        assert raw.post(base, headers=json_headers, content='{"model":"' + "x" * (1024 * 1024) + '"}').status_code == 413
        # tenant_id is an ignored query key; it never selects another tenant.
        assert raw.post(base, headers=json_headers, params={"tenant_id": "other"}, content="{}").status_code == 400
        assert len(list(agents.list())) == count
        plain = raw.get(base + "/" + saved[0].id, headers=headers)
        scoped = raw.get(base + "/" + saved[0].id, headers=headers, params={"tenant_id": "other"})
        assert plain.status_code == scoped.status_code == 200 and scoped.json() == plain.json()
        for resource_id in (str(uuid.uuid4()), "unrecognized-agent", str(uuid.UUID(int=0))):
            expect_error(NotFoundError, lambda: agents.retrieve(resource_id))
        expect_error(NotFoundError, lambda: other.beta.agents.retrieve(saved[0].id))
        expect_error(AuthenticationError, lambda: invalid.beta.agents.retrieve(saved[0].id))
        expect_error(AuthenticationError, lambda: invalid.beta.agents.create(model="x"))
        expect_error(BadRequestError, lambda: agents.create(model="x", extra_headers={"OpenAI-Beta": ""}))
        # The Beta header is checked before authentication (HP-05).
        assert raw.post(base, json={"model": "x"}).json()["error"]["code"] == "invalid_beta"
        assert raw.post(base, headers={"OpenAI-Beta": "agents=v1"}, json={"model": "x"}).status_code == 401
        # The minimal pinned MCP tool saves its omitted origin as "service" (MV-01);
        # explicit environment origin is retained independently of execution placement.
        mcp = {"type": "mcp", "server_label": "x", "transport": {"type": "http", "server_url": "https://example.invalid"}}
        minimal = agents.with_raw_response.create(model="x", tools=[mcp]).http_response.json()
        assert minimal["tools"] == [{**mcp, "transport": {**mcp["transport"], "headers": {}}, "connection_origin": "service",
                                     "allowed_tools": None, "credential_id": None, "request_metadata": {}, "required": False}]
        assert agents.delete(minimal["id"]).deleted
        environment_mcp = agents.create(model="x", tools=[{**mcp, "connection_origin": "environment"}])
        assert environment_mcp.tools[0].connection_origin == "environment"
        assert agents.retrieve(environment_mcp.id).tools == environment_mcp.tools
        assert raw.get(base + "/" + environment_mcp.id, headers=headers).json()["tools"][0]["connection_origin"] == "environment"
        assert agents.delete(environment_mcp.id).deleted
        expect_error(BadRequestError, lambda: agents.create(model="x", tools=[{**mcp, "connection_origin": "unknown"}]))
        expect_error(BadRequestError, lambda: agents.create(model="x", tools=[{**mcp, "transport": {"type": "stdio", "command": "unqualified"}}]))
        # Every pinned web_search mode is saved as the official service does (TV-05);
        # omitted or null mode is saved as live. A supplied location, including {},
        # has all four keys (req_db41d2f6261b4abfb69465eafe719ab5,
        # req_165d53b88445490b9146d8272c54134d).
        live = {"type": "web_search", "mode": "live", "context_size": "medium", "allowed_domains": None, "location": None}
        for tool, expected in (
            ({"type": "web_search"}, live),
            ({"type": "web_search", "mode": None}, live),
            ({"type": "web_search", "mode": "cached", "context_size": "high", "allowed_domains": ["example.com"],
              "location": {"country": "FR", "city": "Paris"}},
             {"type": "web_search", "mode": "cached", "context_size": "high", "allowed_domains": ["example.com"],
              "location": {"city": "Paris", "country": "FR", "region": None, "timezone": None}}),
            ({"type": "web_search", "mode": "cached", "location": {}},
             {"type": "web_search", "mode": "cached", "context_size": "medium", "allowed_domains": None,
              "location": {"city": None, "country": None, "region": None, "timezone": None}}),
        ):
            response = agents.with_raw_response.create(model="x", tools=[tool])
            agent = response.parse()
            assert response.status_code == 201 and response.http_response.json()["tools"] == [expected]
            assert [t.to_dict() for t in agent.tools] == [expected]
            assert raw.get(base + "/" + agent.id, headers=headers).json()["tools"] == [expected]
            assert agents.retrieve(agent.id) == agent
            saved.append(agent)
    print("Reusable Agents: fixed SDK/raw HTTP create/retrieve, explicit configuration, known defaults, saved web_search modes, isolation and validation passed; model defaults/MCP/retry semantics and enabled web_search execution remain gaps.")
    return saved
