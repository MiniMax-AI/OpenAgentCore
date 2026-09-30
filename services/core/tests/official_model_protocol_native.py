"""Opt-in real-model protocol acceptance through public Sessions and the pinned SDK.

OAC_TEST_MODEL_PROTOCOL_OPTIONS points to a private JSON file containing engine,
model, model_provider and optional harness_config. Evidence contains only scenario markers, resource IDs,
event counts and controlled check names; never provider options or raw errors.
"""
import importlib.metadata
import json
import logging
import os
import sys
import uuid
from pathlib import Path

sys.dont_write_bytecode = True
logging.disable(logging.CRITICAL)
os.environ.pop("OPENAI_LOG", None)


class CheckFailed(Exception):
    pass


def require(condition, code):
    if not condition:
        raise CheckFailed(code)


def main():
    base, token, options_file, stage, output = sys.argv[1:]
    record = {"stage": stage, "checks": [], "calls": [], "runs": []}

    def save():
        path = Path(output)
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, "w") as handle:
            json.dump(record, handle, indent=2)

    client = None
    try:
        import httpx2
        from openai import OpenAI

        require(stage in {"initial", "resume"}, "invalid_stage")
        settings = json.loads(Path(options_file).read_text())
        engine, model, provider = settings["engine"], settings["model"], settings["model_provider"]
        harness_config = settings.get("harness_config", {})
        require(isinstance(harness_config, dict), "invalid_harness_config")
        require(engine in {"codex", "claude_sdk", "mcode"}, "invalid_engine")
        require(provider["protocol"] in {"anthropic", "responses", "chat_completions"}, "invalid_protocol")
        require(isinstance(model, str) and bool(model), "missing_model")
        require(isinstance(provider.get("api_key"), str) and bool(provider["api_key"]), "missing_provider_key")
        pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
        distribution = importlib.metadata.distribution("openai")
        source = json.loads(distribution.read_text("direct_url.json") or "{}")
        require(distribution.version == pin["sdk_version"] and
                source.get("vcs_info", {}).get("commit_id") == pin["commit"], "official_sdk_pin_mismatch")
        if stage == "resume":
            record = json.loads(Path(output).read_text())
            record["stage"] = stage
            require(record["engine"] == engine and record["protocol"] == provider["protocol"], "resume_configuration_changed")
        else:
            record.update(engine=engine, protocol=provider["protocol"],
                          marker="PROTOCOL-MEMORY-" + uuid.uuid4().hex[:12])
        save()
        client = OpenAI(base_url=base + "/v1", api_key=token, max_retries=0,
                        _strict_response_validation=True,
                        http_client=httpx2.Client(trust_env=False, timeout=240))
        sessions = client.beta.agents.sessions

        def message(text):
            return {"type": "agent.session.input.message", "input": [
                {"role": "user", "content": [{"type": "input_text", "text": text}]}]}

        def answer(sid, expected):
            items = [item.to_dict() for item in sessions.items.list(sid, order="asc", limit=100).data]
            replies = [item for item in items if item["type"] == "message" and item["role"] == "assistant"]
            require(bool(replies), "missing_assistant_answer")
            actual = " ".join(part["text"] for part in replies[-1]["content"] if part["type"] == "output_text")
            require(expected in actual, "answer_marker_mismatch")
            outputs = {item["call_id"]: item for item in items if item["type"] == "function_call_output"}
            for call in record["calls"]:
                require(call["call"] in outputs, "function_result_not_persisted")
                value = outputs[call["call"]]
                require((value.get("error") is None) == call["success"], "function_result_error_state_mismatch")

        def run(sid, prompt, scenario, expected=None, tool_result=None, failed=False, cancel=False, creation=None):
            events, deltas, handled, cancellation = [], 0, False, False
            with (creation if creation is not None else sessions.events.stream(sid, timeout=240)) as stream:
                if creation is None:
                    sessions.events.create(sid, events=[message(prompt)], idempotency_key=str(uuid.uuid4()))
                for event in stream:
                    # Retain event types only, never the event's provider content.
                    kind = event.type
                    require(kind not in {"agent.session.failed", "agent.session.turn.failed"}, "native_turn_failed")
                    events.append(kind)
                    if kind == "agent.session.turn.output_text.delta":
                        deltas += 1
                        if cancel and not cancellation:
                            sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}])
                            cancellation = True
                    if kind == "agent.session.requires_action":
                        require(engine != "mcode" and tool_result is not None and not handled, "unexpected_function_action")
                        require(len(event.session.required_actions) == 1, "unexpected_parallel_function_actions")
                        action = event.session.required_actions[0]
                        require(action.name == "protocol_lookup", "unexpected_function_name")
                        result = {"type": "agent.session.input.tool_result", "turn_id": action.turn_id,
                                  "call_id": action.call_id, "success": not failed, "output": tool_result}
                        if failed:
                            result["error"] = "Fixture lookup unavailable. Reply PROTOCOL-TOOL-ERROR and do not retry."
                        sessions.events.create(sid, events=[result], idempotency_key="protocol-" + action.call_id)
                        record["calls"].append({"turn": action.turn_id, "call": action.call_id, "success": not failed})
                        handled = True
                    if kind == "agent.session.idle":
                        break
                else:
                    raise CheckFailed("stream_ended_without_idle")
            terminal = "agent.session.turn.cancelled" if cancel else "agent.session.turn.completed"
            require("agent.session.turn.created" in events and terminal in events, "missing_turn_lifecycle")
            require(events.index("agent.session.turn.created") < events.index(terminal) < len(events) - 1,
                    "turn_lifecycle_order")
            require(deltas > 0, "missing_sse_text_delta")
            require(handled == (tool_result is not None), "function_action_not_observed")
            if cancel:
                require(cancellation, "cancel_not_submitted")
            current = sessions.turns.list(sid, order="desc", limit=1).data
            require(bool(current) and current[0].status == ("cancelled" if cancel else "completed"),
                    "persisted_turn_status_mismatch")
            usage = current[0].to_dict().get("usage")
            # Adapters preserve native usage scope. A partial native breakdown
            # remains null publicly; protocol conversion must not invent it.
            if usage is not None:
                require(isinstance(usage, dict) and usage.get("input_tokens", -1) >= 0 and
                        usage.get("output_tokens", -1) >= 0 and
                        usage.get("total_tokens") == usage["input_tokens"] + usage["output_tokens"],
                        "inconsistent_public_usage")
            if expected is not None:
                answer(sid, expected)
            record["runs"].append({"scenario": scenario, "text_deltas": deltas, "completed": not cancel,
                                   "cancelled": cancel, "function_result": handled,
                                   "usage_present": isinstance(usage, dict)})
            record["checks"].append(scenario)
            save()

        if stage == "initial":
            marker = record["marker"]
            agent = {"model": model, "instructions": (
                "Follow the user's exact requested tool calls and remember supplied markers. "
                "Only call protocol_lookup when explicitly requested. After a tool result, answer in text and do not repeat the call.")}
            if engine != "mcode":
                agent["tools"] = [{"type": "function", "name": "protocol_lookup",
                                  "description": "Return the requested fixture value.",
                                  "parameters": {"type": "object", "properties": {"key": {"type": "string"}},
                                                 "required": ["key"], "additionalProperties": False}}]
                prompt = "Call protocol_lookup exactly once with key first. Then reply with the text returned by the tool."
            else:
                agent["instructions"] = "Remember user-supplied markers. Answer in text and do not use tools."
                prompt = "Remember " + marker + ". Reply exactly " + marker + "."
                record["not_exercised"] = ["public_functions_not_supported_by_mcode", "function_error", "cancel"]
            # Explicit model selection clears deployment-native defaults, so exercise
            # the public Session override instead of relying on fixture injection.
            creation = sessions.create(agent=agent, environment={"type": "none"}, input=prompt, stream=True,
                                       extra_body={"x_agents_core": {"harness_config": harness_config}})
            first = next(creation)
            sid = first.session.id
            record["session"] = sid
            save()
            public = sessions.retrieve(sid).to_dict()
            require(provider["api_key"] not in json.dumps(public), "provider_key_exposed_by_public_session")
            require(public.get("agent", {}).get("x_agents_core", {}).get("harness_config", {}) == harness_config,
                    "harness_config_snapshot_mismatch")
            record["checks"].append("public_harness_config_snapshot")
            if engine == "mcode":
                run(sid, prompt, "native_protocol_memory_first_turn", expected=marker, creation=creation)
            else:
                first_value = "PROTOCOL-FIRST-" + uuid.uuid4().hex[:12]
                run(sid, prompt, "function_text_first_turn", expected=first_value,
                    tool_result=first_value, creation=creation)
                run(sid, "Call protocol_lookup exactly once with key second. Remember its result and repeat it verbatim.",
                    "function_text_second_turn", expected=marker, tool_result=marker)
                run(sid, "Call protocol_lookup once with key failure. If it fails, reply PROTOCOL-TOOL-ERROR and do not retry.",
                    "function_error_result", expected="PROTOCOL-TOOL-ERROR", tool_result="Lookup failed.", failed=True)
                run(sid, "Do not call tools. Print integers 1 through 10000, one per line. Begin with 1 immediately.",
                    "cancel_during_sse_text", cancel=True)
                run(sid, "Without calling tools, repeat the PROTOCOL-MEMORY marker returned by the second lookup.",
                    "continue_after_cancel", expected=marker)
        else:
            sid = record["session"]
            run(sid, "Without calling tools, repeat the PROTOCOL-MEMORY marker you remembered earlier, and nothing else.",
                "cold_daemon_session_continuation", expected=record["marker"])
            turns = sessions.turns.list(sid, order="asc", limit=100).data
            require(len(turns) == (2 if engine == "mcode" else 6), "unexpected_turn_count")
            record["passed"] = True
        save()
    except CheckFailed as failure:
        record["failure"] = {"stage": stage, "check": str(failure)}
        save()
        return 1
    except Exception as failure:
        # API and validation exceptions can include private request/response
        # bodies. Do not format them or persist their traceback.
        record["failure"] = {"stage": stage, "check": "client_or_fixture_exception"}
        status = getattr(failure, "status_code", None)
        if isinstance(status, int):
            record["failure"]["http_status"] = status
        trace = failure.__traceback__
        while trace:
            if trace.tb_frame.f_code.co_filename == __file__:
                record["failure"]["script_line"] = trace.tb_lineno
            trace = trace.tb_next
        save()
        return 1
    finally:
        if client is not None:
            client.close()
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        sys.exit(1)
