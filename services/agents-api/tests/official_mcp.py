"""HTTP MCP resource semantics; execution requires the separate real-provider run."""

from itertools import product

from openai import BadRequestError, NotFoundError


def verify_mcp_configuration(client, other, expect_error):
    agents, sessions = client.beta.agents, client.beta.agents.sessions
    transport = {"type": "http", "server_url": "https://mcp.example.invalid/mcp"}
    tool = {"type": "mcp", "server_label": "tickets", "transport": transport,
            "connection_origin": "service"}
    recovered, saved = [], []
    model_override = {"x_agents_core": {"model_provider": {"protocol": "responses", "base_url": "https://model.invalid/v1", "api_key": "synthetic-mcp-model-key"}}}
    for origin, allow, readiness in product(
        ("service", "environment"),
        ({}, {"allowed_tools": None}, {"allowed_tools": []},
         {"allowed_tools": ["lookup_ticket"]}),
        ({}, {"required": False}, {"required": True}),
    ):
        declared = {**tool, "connection_origin": origin, **allow, **readiness}
        placement = {"type": "none"} if origin == "service" else {"type": "self_hosted", "workspace_directory": "/tmp/oac-official-mcp"}
        response = agents.with_raw_response.create(model="requested-model", tools=[declared])
        resource, body = response.parse(), response.http_response.json()
        canonical = {**declared, "allowed_tools": allow.get("allowed_tools"),
                     "credential_id": None, "request_metadata": {}, "required": readiness.get("required", False),
                     "transport": {**transport, "headers": {}}}
        assert body["tools"] == [canonical]
        spec = {"input": "Verify mcp fixture admission.", "agent_id": resource.id, "environment": placement, "extra_body": model_override if origin == "environment" else {}}
        headers = {"Idempotency-Key": "mcp-snapshot-" + resource.id}
        response = sessions.with_raw_response.create(**spec, extra_headers=headers)
        session, body = response.parse(), response.http_response.json()
        assert body["agent"]["tools"] == [{**canonical, "transport": transport}]
        expect_error(NotFoundError, lambda: other.beta.agents.sessions.create(**spec))
        assert sessions.create(agent={"model": "requested-model", "tools": [declared]},
                               input="Verify mcp fixture admission.", environment=placement, extra_body=model_override if origin == "environment" else {}).agent.tools == session.agent.tools
        override = sessions.create(**spec, agent={"tools": []})
        assert override.agent.tools == []
        changed = agents.update(resource.id, tools=[])
        assert changed.tools == [] and sessions.retrieve(session.id) == session
        assert sessions.create(**spec, extra_headers=headers) == session
        recovered.extend([session, override])
        saved.append(changed)

    # An omitted or null origin on HTTP transport is saved exactly as "service",
    # the pinned SDK's minimal tool form (MV-01).
    explicit = agents.with_raw_response.create(model="requested-model", tools=[tool]).http_response.json()
    minimal = {key: value for key, value in tool.items() if key != "connection_origin"}
    for declaration in (minimal, {**tool, "connection_origin": None}):
        response = agents.with_raw_response.create(model="requested-model", tools=[declaration])
        resource, body = response.parse(), response.http_response.json()
        assert body["tools"] == explicit["tools"] and body["tools"][0]["connection_origin"] == "service"
        updated = agents.update(resource.id, tools=[declaration])
        assert updated.tools == resource.tools
        inline = sessions.create(agent={"model": "requested-model", "tools": [declaration]},
                                 input="Verify mcp fixture admission.", environment={"type": "none"})
        assert inline.to_dict()["agent"]["tools"] == [{**explicit["tools"][0], "transport": transport}]
        replaced = sessions.create(agent_id=resource.id, agent={"tools": [declaration]},
                                   input="Verify mcp fixture admission.", environment={"type": "none"})
        assert replaced.agent.tools == inline.agent.tools
        recovered.extend([inline, replaced])
        saved.append(updated)
    assert agents.delete(explicit["id"]).deleted

    before = {item.id for item in sessions.list()}
    saved_before = {item.id for item in agents.list()}
    expect_error(BadRequestError, lambda: sessions.create(agent={"model": "requested-model", "tools": [{**tool, "connection_origin": "environment"}]}, environment={"type": "none"}))
    expect_error(BadRequestError, lambda: sessions.create(agent={"model": "requested-model", "tools": [tool]}, environment={"type": "self_hosted", "workspace_directory": "/tmp/oac-official-mcp"}, extra_body=model_override))
    invalid = [{**tool, "connection_origin": "unknown"}]
    invalid += [{**tool, "required": "true"}, {**tool, "required": None},
                {**tool, "request_metadata": {"x": "y"}},
                {**tool, "allowed_tools": [None]}]
    for changes in ({"headers": {"Authorization": "synthetic-private"}},
                    {"authorization": "synthetic-private"},
                    {"server_url": "https://mcp.example.invalid/mcp?token=synthetic-private"}):
        invalid.append({**tool, "transport": {**transport, **changes}})
    # Transports other than HTTP stay unsupported with or without an origin.
    stdio = {"type": "stdio", "command": "synthetic-private"}
    invalid += [{**minimal, "transport": stdio}, {**tool, "transport": stdio}]
    for declaration in invalid:
        for operation in (
            lambda: agents.create(model="requested-model", tools=[declaration]),
            lambda: sessions.create(agent={"model": "requested-model", "tools": [declaration]},
                                    input="Verify mcp fixture admission.", environment={"type": "none"}),
        ):
            error = expect_error(BadRequestError, operation)
            assert "synthetic-private" not in str(error.body)
            if declaration["transport"] is stdio:
                assert error.body["message"] == "MCP currently supports HTTP transport only."
    assert {item.id for item in sessions.list()} == before
    assert {item.id for item in agents.list()} == saved_before
    print("HTTP MCP: pinned saved/Session projections, omitted/null origins, null/empty allowlists, immutable snapshots and rejected writes passed; no native execution claimed.")
    return recovered, saved
