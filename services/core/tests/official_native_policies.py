"""Public disabled-policy and Codex configuration checks through a real native Turn."""

from contextlib import ExitStack
import uuid

from session_cleanup import delete_session


def verify_native_policies(client, foreign, http, agent_options, session_options, ready, record):
    agents, sessions = client.beta.agents, client.beta.agents.sessions
    harness = agent_options["x_agents_core"]["harness"]
    assert harness in {"claude_sdk", "codex"}, "Policies qualify Claude Code or Codex"
    root = str(client.base_url).rstrip("/") + "/agents"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    foreign_headers = {**headers, "Authorization": "Bearer " + foreign.api_key}
    disabled = [{"type": "web_search", "mode": "disabled"}, {"type": "programmatic_tool_calling", "enabled": False}]
    resolved = [{**disabled[0], "context_size": "medium", "allowed_domains": None, "location": None}, disabled[1]]
    instructions = "Follow the requested answer format. Do not substitute another tool for an unavailable tool."
    agent = {**agent_options, "instructions": instructions, "tools": disabled}
    proof = {"harness": harness, "model": agent_options["model"], "checks": [], "runs": [], "rejections": [], "disabled_controls": resolved,
             "unverified": [
                 "Public configuration and absent tool events do not prove native policy enforcement or tool inventory; model self-reports are not enforcement evidence.",
                 "Disabled web search does not disable ordinary shell HTTP or establish network isolation.",
                 "Native catalog support and launch overrides are not collected here; completed verbosity Turns do not prove the model honored a verbosity level.",
                 "Native managed-policy injection and provider lifecycle require separate qualification."]}

    def reject(label, spec, message=None):
        before = {session.id for session in sessions.list()}
        body = {"environment": session_options["environment"], **session_options.get("extra_body", {}), **spec}
        response = http.post(root + "/sessions", headers=headers, json=body)
        # Track an incorrectly admitted Session so failure still cleans it up.
        if response.status_code == 201:
            cleanup.callback(delete_session, sessions, response.json()["id"])
        error = response.json().get("error", {})
        proof["rejections"].append({"case": label, "status": response.status_code, "error": error})
        record(proof)
        assert response.status_code == 400 and error.get("type") == "invalid_request_error", label
        if message is not None:
            assert error.get("message") == message and error.get("code") == "unsupported_or_invalid_configuration" and error.get("param") is None, label
        assert {session.id for session in sessions.list()} == before, "Rejected configuration created a Session"
        proof["checks"].append(label)

    def run(session, label, prompt, marker, verbosity="medium"):
        sid = session.id
        run_proof = {"case": label, "session": sid, "prompt": prompt, "verbosity": verbosity, "events": [], "status": "running", "step": "readiness", "agent": session.agent.to_dict()}
        if verbosity in {"low", "high"}:
            run_proof["prerequisite"] = "The native model catalog must declare verbosity support; catalog contents are not independently observed here. A failed run does not identify the failure cause."
        proof["runs"].append(run_proof)
        try:
            ready(session)
            run_proof["step"] = "native_turn"
            assert session.agent.instructions == instructions and session.agent.model == agent_options["model"]
            assert session.agent.text.verbosity == verbosity
            assert [tool.to_dict() for tool in session.agent.tools] == resolved
            with sessions.stream(sid, input=prompt, timeout=360) as stream:
                for event in stream:
                    run_proof["events"].append(event.to_dict())
                    assert event.type not in {"agent.session.requires_action", "agent.session.failed", "agent.session.turn.failed", "agent.session.turn.cancelled"}, label + " requires a completed native Turn; unsupported verbosity is not a pass"
            events = run_proof["events"]
            types = [event["type"] for event in events]
            assert types.count("agent.session.turn.created") == types.count("agent.session.turn.completed") == 1
            assert types[-1] == "agent.session.idle" and types.index("agent.session.turn.created") < types.index("agent.session.turn.completed") < len(types) - 1
            assert len({event["event_id"] for event in events}) == len(events)
            run_proof["step"] = "public_persistence"
            stored = {}
            for resource in ("items", "turns"):
                response = http.get(root + "/sessions/" + sid + "/" + resource, headers=headers, params={"order": "asc", "limit": 100})
                assert response.status_code == 200 and not response.json()["has_more"]
                assert response.json() == getattr(sessions, resource).list(sid, order="asc", limit=100).to_dict()
                stored[resource] = response.json()["data"]
            assert len(stored["turns"]) == 1 and stored["turns"][0]["status"] == "completed"
            observed_items = stored["items"] + [event["item"] for event in events if "item" in event]
            assert not any(item["type"] in {"web_search_call", "function_call", "function_call_output", "mcp_call"} for item in observed_items)
            answers = [item for item in stored["items"] if item["type"] == "message" and item.get("role") == "assistant" and item["turn_id"] == stored["turns"][0]["id"]]
            assert answers and answers[-1]["status"] == "completed"
            answer = answers[-1]
            text = "".join(part.get("text", "") for part in answer["content"])
            assert text.strip() == marker, "Model did not produce the requested fixed answer"
            done = [event for event in events if event["type"] == "agent.session.turn.output_text.done" and event["item_id"] == answer["id"]]
            assert len(done) == 1 and done[0]["text"] == text
            assert "".join(event["delta"] for event in events if event["type"] == "agent.session.turn.output_text.delta" and event["item_id"] == answer["id"]) == text
            assert [item for item in stored["items"] if item["type"] == "message" and item.get("role") == "user"][0]["content"] == [{"type": "input_text", "text": prompt}]
            current = sessions.retrieve(sid)
            assert current.required_actions == [] and current.agent == session.agent
            for suffix in ("", "/items", "/turns"):
                assert http.get(root + "/sessions/" + sid + suffix, headers=foreign_headers).status_code == 404
            run_proof.update(status="passed", **stored)
            proof["checks"].append(label)
        finally:
            proof["current_case"] = {"case": label, "step": run_proof["step"], "verbosity": verbosity}
            if run_proof["status"] != "passed":
                run_proof["status"] = "failed"
            record(proof)

    with ExitStack() as cleanup:
        try:
            for label, tool, message in (
                ("web_search_live_rejected", {"type": "web_search", "mode": "live"}, "Only disabled web_search is qualified for execution."),
                ("web_search_cached_rejected", {"type": "web_search", "mode": "cached"}, "Only disabled web_search is qualified for execution."),
                ("web_search_default_rejected", {"type": "web_search"}, "Only disabled web_search is qualified for execution."),
                ("programmatic_enabled_rejected", {"type": "programmatic_tool_calling", "enabled": True}, "Programmatic tool calling is not qualified for execution."),
            ):
                reject(label, {"agent": {**agent, "tools": [tool]}}, message)
            saved = agents.create(**{key: value for key, value in agent.items() if key != "x_agents_core"}, extra_body={"x_agents_core": agent["x_agents_core"]})
            cleanup.callback(agents.delete, saved.id)
            assert [tool.to_dict() for tool in agents.retrieve(saved.id).tools] == resolved
            frozen = sessions.create(agent_id=saved.id, **session_options)
            cleanup.callback(delete_session, sessions, frozen.id)
            # Saved configuration admits enabled controls; only Session admission rejects them.
            for tool, message in (({"type": "web_search", "mode": "live"}, "Only disabled web_search is qualified for execution."),
                                  ({"type": "programmatic_tool_calling", "enabled": True}, "Programmatic tool calling is not qualified for execution.")):
                updated = agents.update(saved.id, tools=[tool])
                expected = {**tool, "context_size": "medium", "allowed_domains": None, "location": None} if tool["type"] == "web_search" else tool
                assert [value.to_dict() for value in updated.tools] == [expected]
                assert agents.retrieve(saved.id) == updated
                assert sessions.retrieve(frozen.id).agent == frozen.agent
                reject("saved_" + tool["type"] + "_enabled_rejected", {"agent_id": saved.id}, message)
            inline = sessions.create(agent=agent, **session_options)
            cleanup.callback(delete_session, sessions, inline.id)
            for label, session in (("saved_disabled_controls_frozen", frozen), ("inline_disabled_controls", inline)):
                marker = "POLICY_" + uuid.uuid4().hex
                prompt = "Attempt native web search and programmatic tool calling. If either tool is unavailable, do not substitute tools. Regardless of availability, answer with exactly " + marker + "."
                run(session, label, prompt, marker)
            if harness == "codex":
                for key, value in (("features.code_mode", True), ("features.hooks", True), ("features.plugins", True),
                                   ("web_search", "live"), ("model_verbosity", "high"), ("tools.experimental_request_user_input.enabled", True),
                                   ("model_reasoning_effort", True), ("model_reasoning_effort", "unknown")):
                    extension = {**agent["x_agents_core"], "harness_config": {key: value}}
                    reject("codex_hidden_config_" + key + "_" + str(value), {"agent": {**agent, "x_agents_core": extension}}, "harness_config contains unsupported or invalid native model parameters")
                for verbosity in (None, "medium", "low", "high"):
                    options = dict(agent)
                    if verbosity is not None:
                        options["text"] = {"verbosity": verbosity}
                    proof["current_case"] = {"case": "codex_verbosity_" + (verbosity or "default"), "step": "session_admission", "verbosity": verbosity or "medium"}
                    record(proof)
                    session = sessions.create(agent=options, **session_options)
                    cleanup.callback(delete_session, sessions, session.id)
                    run(session, "codex_verbosity_" + (verbosity or "default"), "What is two plus two? Answer with exactly 4 and do not use tools.", "4", verbosity or "medium")
                options = {**agent, "x_agents_core": {**agent["x_agents_core"], "harness_config": {"model_reasoning_effort": "medium"}}}
                session = sessions.create(agent=options, **session_options)
                cleanup.callback(delete_session, sessions, session.id)
                assert session.agent.to_dict()["x_agents_core"]["harness_config"] == {"model_reasoning_effort": "medium"}
                run(session, "codex_valid_reasoning_effort", "What is two plus two? Answer with exactly 4 and do not use tools.", "4")
            proof["passed"] = True
            return proof["checks"]
        finally:
            record(proof)
