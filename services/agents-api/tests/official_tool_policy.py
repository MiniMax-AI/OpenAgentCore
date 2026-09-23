"""Opt-in disabled tool policy acceptance through the pinned SDK and raw HTTP."""
import importlib.metadata
import json
import sys
import uuid
from pathlib import Path
from contextlib import ExitStack, nullcontext
from types import SimpleNamespace

import httpx2
from openai import BadRequestError, NotFoundError, OpenAI


def main():
    base, token, foreign, model, stage, evidence = sys.argv[1:]
    assert stage in {"initial", "resume"}
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    assert distribution.version == pin["sdk_version"]
    assert json.loads(distribution.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    client = OpenAI(base_url=base + "/v1", api_key=token, max_retries=0,
                    _strict_response_validation=True,
                    http_client=httpx2.Client(trust_env=False, timeout=150))
    other = client.with_options(api_key=foreign)
    raw = httpx2.Client(trust_env=False, timeout=150)
    sessions = client.beta.agents.sessions
    headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
    foreign_headers = {**headers, "Authorization": "Bearer " + foreign}
    proof = {"sessions": []} if stage == "initial" else json.loads(Path(evidence).read_text())
    tools = [{"type": "web_search", "mode": "disabled"},
             {"type": "programmatic_tool_calling", "enabled": False}]
    # These are pinned response defaults, including explicit nullable fields.
    expected = [{"type": "web_search", "mode": "disabled", "context_size": "medium",
                 "allowed_domains": None, "location": None}, tools[1]]
    config = {"model": model, "instructions": "Follow user instructions and remember supplied markers.", "tools": tools}

    def request(method, path, *, status=200, foreign_tenant=False, **kwargs):
        response = raw.request(method, base + "/v1/agents" + path,
                               headers=foreign_headers if foreign_tenant else headers, **kwargs)
        assert response.status_code == status, (method, path, response.status_code)
        return response.json()

    def save():
        Path(evidence).write_text(json.dumps(proof, indent=2))

    def check_config(sid):
        assert [tool.to_dict() for tool in sessions.retrieve(sid).agent.tools] == expected
        assert request("GET", "/sessions/" + sid)["agent"]["tools"] == expected

    def execute(entry, prompt, creation=None):
        sid = entry["id"]
        events = []
        with (nullcontext(creation) if creation is not None else sessions.events.stream(sid, timeout=150)) as stream:
            if creation is None:
                sessions.events.create(sid, events=[{"type": "agent.session.input.message", "input": [
                    {"role": "user", "content": [{"type": "input_text", "text": prompt}]}]}],
                    idempotency_key=str(uuid.uuid4()))
            for event in stream:
                events.append(event.type)
                assert event.type not in {"agent.session.failed", "agent.session.turn.failed", "agent.session.requires_action"}, event.type
                if event.type == "agent.session.idle":
                    break
            else:
                raise AssertionError("stream ended without idle")
        assert events.index("agent.session.turn.created") < events.index("agent.session.turn.completed") < events.index("agent.session.idle")
        turns = sessions.turns.list(sid, order="asc", limit=100).data
        assert turns[-1].status == "completed", turns[-1].status
        assert len(turns) == (1 if stage == "initial" else 2)
        items = sessions.items.list(sid, order="asc", limit=100).data
        answers = [item for item in items if item.type == "message" and item.role == "assistant" and item.turn_id == turns[-1].id]
        text = "\n".join(content.text for item in answers for content in item.content if content.type == "output_text")
        assert entry["marker"] in text, "Native answer did not retain the marker"
        assert not any(item.type in {"web_search_call", "function_call", "mcp_call", "command_execution"} for item in items)
        public = request("GET", "/sessions/" + sid + "/items", params={"order": "asc", "limit": 100})
        assert public["data"] == [item.to_dict() for item in items]
        entry["first_turn" if stage == "initial" else "resumed_turn"] = turns[-1].id
        entry[stage + "_events"] = events
        save()

    def reject_configuration(payload, message):
        error = request("POST", "/sessions", status=400, json=payload)["error"]
        assert error["code"] == "unsupported_or_invalid_configuration" and error["message"] == message, error
        try:
            sessions.create(**payload)
            raise AssertionError("Unsupported enabled tool configuration was admitted")
        except BadRequestError:
            pass

    try:
        if stage == "initial":
            sdk_agent = client.beta.agents.create(**config)
            assert [tool.to_dict() for tool in sdk_agent.tools] == expected
            raw_agent = request("POST", "", status=201, json=config)
            assert raw_agent["tools"] == expected
            proof["agents"] = [sdk_agent.id, raw_agent["id"]]
            for aid in proof["agents"]:
                assert [tool.to_dict() for tool in client.beta.agents.retrieve(aid).tools] == expected
                assert request("GET", "/" + aid)["tools"] == expected
            payloads = [
                ("sdk_saved", {"agent_id": sdk_agent.id}),
                ("sdk_inline", {"agent": config}),
                ("raw_saved", {"agent_id": raw_agent["id"]}),
                ("raw_inline", {"agent": config}),
            ]
            for name, payload in payloads:
                payload["environment"] = {"type": "none"}
                marker = "TOOL-POLICY-" + uuid.uuid4().hex
                prompt = "Remember this exact marker for later: " + marker + ". Reply with that marker only."
                payload.update(input=prompt, stream=True)
                with ExitStack() as stack:
                    if name.startswith("sdk"):
                        creation = stack.enter_context(sessions.create(**payload))
                        first = next(creation)
                        assert first.type == "agent.session.created"
                        sid = first.session.id
                    else:
                        response = stack.enter_context(raw.stream("POST", base + "/v1/agents/sessions", headers=headers, json=payload))
                        assert response.status_code == 201
                        frames = (json.loads(line[6:]) for line in response.iter_lines() if line.startswith("data: "))
                        first = next(frames)
                        assert first["type"] == "agent.session.created"
                        sid = first["session"]["id"]
                        creation = (SimpleNamespace(type=frame["type"]) for frame in frames)
                    entry = {"id": sid, "path": name, "marker": marker}
                    proof["sessions"].append(entry)
                    save()
                    check_config(sid)
                    execute(entry, prompt, creation=creation)
            search_only = "Only disabled web_search is qualified for execution."
            ptc_only = "Programmatic tool calling is not qualified for execution."
            for enabled, message in [({"type": "web_search", "mode": "cached"}, search_only),
                                     ({"type": "web_search", "mode": "live"}, search_only),
                                     ({"type": "programmatic_tool_calling", "enabled": True}, ptc_only)]:
                reject_configuration({"agent": {"model": model, "tools": [enabled]}, "environment": {"type": "none"}, "input": "Verify rejected tool policy configuration."}, message)
                reject_configuration({"agent_id": sdk_agent.id, "agent": {"tools": [enabled]}, "environment": {"type": "none"}, "input": "Verify rejected tool policy configuration."}, message)
            # Saving PTC intent is independent of Session execution qualification.
            enabled_agent = client.beta.agents.create(model=model, tools=[{"type": "programmatic_tool_calling", "enabled": True}])
            reject_configuration({"agent_id": enabled_agent.id, "environment": {"type": "none"}, "input": "Verify rejected tool policy configuration."}, ptc_only)
            # Saved enabled or omitted-mode search is resource data (TV-05); admission still rejects it.
            for search in ({"type": "web_search"}, {"type": "web_search", "mode": "cached"}):
                searched = client.beta.agents.create(model=model, tools=[search])
                reject_configuration({"agent_id": searched.id, "environment": {"type": "none"}, "input": "Verify rejected tool policy configuration."}, search_only)
            # Omitted-tool default projections are covered by the queued SDK/raw
            # resource fixture; these live cases exercise explicit disabled tools.
            for aid in proof["agents"]:
                request("GET", "/" + aid, status=404, foreign_tenant=True)
                request("POST", "/sessions", status=404, foreign_tenant=True,
                        json={"agent_id": aid, "environment": {"type": "none"}, "input": "Verify rejected tool policy configuration."})
                try:
                    other.beta.agents.sessions.create(agent_id=aid, environment={"type": "none"}, input="Verify foreign Agent rejection.")
                    raise AssertionError("Foreign tenant used a saved Agent")
                except NotFoundError:
                    pass
        else:
            for entry in proof["sessions"]:
                check_config(entry["id"])
                # The marker is absent from this input: success requires native history.
                execute(entry, "Reply with the exact TOOL-POLICY marker I asked you to remember earlier, and nothing else.")
            proof["passed"] = True
        for entry in proof["sessions"]:
            sid = entry["id"]
            for suffix in ["", "/items", "/turns"]:
                request("GET", "/sessions/" + sid + suffix, status=404, foreign_tenant=True)
            try:
                other.beta.agents.sessions.retrieve(sid)
                raise AssertionError("Foreign tenant retrieved a Session")
            except NotFoundError:
                pass
    finally:
        save()
        raw.close()
        other.close()
        client.close()


if __name__ == "__main__":
    main()
