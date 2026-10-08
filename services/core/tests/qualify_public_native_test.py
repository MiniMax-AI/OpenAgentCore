"""Check qualification's secret handling and owned restart boundary without a model."""

import contextlib
import copy
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import httpx2
from openai import BadRequestError, OpenAI
from jsonschema import ValidationError

from official_environment_files_native import generate_files

import qualify_public_native as qualification


class QualificationTests(unittest.TestCase):
    def setUp(self):
        root = Path.home() / ".oac/tests"
        root.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(prefix="public-qualification-", dir=root)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.settings = {"agent": {"model": "fixture", "x_agents_core": {"harness": "codex"}},
                         "model_provider": {"protocol": "responses", "base_url": "https://model.example/v1", "api_key": "provider-secret"},
                         "environment": {"type": "none"}}
        for name, value in (("settings.json", json.dumps(self.settings)), ("foreign.key", "foreign-secret")):
            path = self.root / name
            path.write_text(value)
            path.chmod(0o600)
        self.evidence = self.root / "evidence.json"
        self.argv = ["qualify_public_native.py", "--settings", str(self.root / "settings.json"),
                     "--foreign-key-file", str(self.root / "foreign.key"), "--suite", "none", "--evidence", str(self.evidence)]
        self.enterContext(patch.dict(os.environ, {"OPENAI_BASE_URL": "https://core.example/v1", "OPENAI_API_KEY": "project-secret"}))
        self.enterContext(patch.object(qualification, "OpenAI"))
        self.enterContext(contextlib.redirect_stdout(io.StringIO()))

    def run_suite(self, suite):
        with patch("sys.argv", self.argv), patch.object(qualification, "verify_none", suite):
            qualification.main()

    def test_explicit_provider_reaches_suite_and_warm_run_does_not_claim_recovery(self):
        def suite(client, foreign, http, agent, options, ready, restart, record):
            self.assertEqual(agent, self.settings["agent"])
            self.assertEqual(options["extra_body"]["x_agents_core"]["model_provider"], self.settings["model_provider"])
            self.assertIsNone(restart)
            record({"checks": ["warm"]})
            return ["warm"]
        self.run_suite(suite)
        proof = json.loads(self.evidence.read_text())
        self.assertEqual((proof["status"], proof["cold_recovery"]), ("passed", "unverified"))
        self.assertEqual(self.evidence.stat().st_mode & 0o777, 0o600)
        self.assertNotIn("provider-secret", self.evidence.read_text())

    def test_leaked_credential_is_never_written_as_evidence(self):
        def suite(*args, record, **kwargs):
            record({"leak": "provider-secret"})
        with self.assertRaisesRegex(AssertionError, "Credential"):
            self.run_suite(suite)
        self.assertFalse(self.evidence.exists())

    def test_custom_workspace_uses_physical_tools_and_logical_public_file_paths(self):
        client = MagicMock()
        session = SimpleNamespace(status="idle", required_actions=[], environment=SimpleNamespace(
            type="self_hosted", id="environment", workspace_directory="/custom/work"))
        client.beta.agents.sessions.retrieve.return_value = session
        client.beta.agents.environments.retrieve.return_value = SimpleNamespace(status="connected")
        client.beta.agents.sessions.turns.list.side_effect = [[], [SimpleNamespace(id="turn", status="completed")]]
        fixture = generate_files(client, "session", "custom")
        prompt = client.beta.agents.sessions.events.create.call_args.kwargs["events"][0]["input"][0]["content"][0]["text"]
        self.assertIn("/custom/work/files-list-custom-", prompt)
        self.assertNotIn("/workspace/", prompt)
        self.assertTrue(fixture["directory"].startswith("/workspace/files-list-custom-"))
        self.assertTrue(fixture["sibling_directory"].startswith("/workspace/files-list-custom-"))
        self.assertTrue(all(path.startswith(fixture["directory"] + "/") for path in fixture["expected"]))
        self.assertTrue(all(path.startswith(fixture["sibling_directory"] + "/") for path in fixture["sibling_expected"]))
        self.assertNotIn("/custom/work", json.dumps(fixture))

    def test_pinned_sdk_serializes_explicit_session_selection(self):
        bodies = []

        def reject(request):
            bodies.append(json.loads(request.content))
            return httpx2.Response(400, json={"error": {"type": "invalid_request_error", "code": "fixture", "message": "stop before execution"}})

        with httpx2.Client(transport=httpx2.MockTransport(reject)) as http:
            client = OpenAI(base_url="https://core.example/v1", api_key="project-secret", http_client=http, max_retries=0)
            options = {"environment": {"type": "self_hosted", "workspace_directory": "/custom/work"},
                       "extra_body": {"x_agents_core": {"model_provider": self.settings["model_provider"]}}}
            for verify in (qualification.verify_none, qualification.verify_hosted_functions, qualification.verify_workspace_images, qualification.verify_pending_actions):
                kwargs = {"ready": lambda session: None, "record": lambda proof: None}
                if verify is not qualification.verify_pending_actions:
                    kwargs["restart"] = None
                with self.assertRaises(BadRequestError):
                    verify(client, client, http, self.settings["agent"], options, **kwargs)
                self.assertEqual(bodies[-1]["environment"], options["environment"])
                self.assertEqual(bodies[-1]["agent"]["x_agents_core"], self.settings["agent"]["x_agents_core"])
                self.assertEqual(bodies[-1]["x_agents_core"]["model_provider"], self.settings["model_provider"])
            with self.assertRaises(BadRequestError):
                qualification.verify_hosted_structured(client, client, http, self.settings["agent"], options,
                    ready=lambda session: None, restart=None, record=lambda proof: None)
            self.assertEqual(bodies[-1]["x_agents_core"], self.settings["agent"]["x_agents_core"])

    def test_restart_requires_a_running_host_and_observed_new_process(self):
        (self.root / "compose.yaml").write_text("services: {}\n")
        self.argv += ["--compose-directory", str(self.root), "--compose-project", "owned-qualification"]

        def suite(*args, restart, record, **kwargs):
            restart()
            record({"checks": ["cold"]})
            return ["cold"]

        results = [subprocess.CompletedProcess([], 0, value) for value in ("container-id\n", "before\n", "", "after\n")]
        with patch.object(qualification.subprocess, "run", side_effect=results) as run:
            self.run_suite(suite)
            command = run.call_args_list[2].args[0]
            self.assertEqual(command[-2:], ["restart", "agent-host"])
            self.assertEqual(command[command.index("--project-name") + 1], "owned-qualification")
            self.assertFalse(any(call.kwargs.get("shell") for call in run.call_args_list))
        self.assertEqual(json.loads(self.evidence.read_text())["cold_recovery"], "passed")
        self.evidence.unlink()
        with patch.object(qualification.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "")):
            with self.assertRaisesRegex(AssertionError, "Exactly one"):
                self.run_suite(suite)
        self.assertEqual(json.loads(self.evidence.read_text())["status"], "failed")


class NoneSessionTests(unittest.TestCase):
    def run_none(self, measured=True, fault=None):
        client, foreign, http = MagicMock(), SimpleNamespace(api_key="foreign"), MagicMock()
        client.base_url, client.api_key = "https://core.example/v1", "caller"
        sessions = client.beta.agents.sessions
        usage = {"input_tokens": 7, "input_tokens_details": {"cached_tokens": 2}, "output_tokens": 3,
                 "output_tokens_details": {"reasoning_tokens": 1}, "total_tokens": 10} if measured else None
        current = {"id": "session", "object": "agent.session", "metadata": {}, "created_at": 1, "last_active_at": 1,
                   "status": "idle", "required_actions": [], "error": None, "environment": {"type": "none"},
                   "vault_ids": [], "usage": None, "agent": {"id": "agent", "name": None, "model": "fixture",
                   "reasoning": {"effort": None, "summary": None}, "text": {"format": {"type": "text"}, "verbosity": "medium"},
                   "service_tier": "auto", "instructions": None, "tools": [],
                   "multi_agent": {"enabled": False, "max_concurrent_subagents": None}}}
        turns, items, prompts, proofs = [], [], [], []

        def sdk(value):
            return SimpleNamespace(**value, to_dict=lambda: copy.deepcopy(value))

        def page(values):
            return {"object": "list", "data": copy.deepcopy(values), "has_more": False,
                    "first_id": values[0]["id"] if values else None, "last_id": values[-1]["id"] if values else None}

        def listing(values):
            result = MagicMock()
            result.to_dict.side_effect = lambda: page(values)
            result.__iter__.side_effect = lambda: iter([sdk(value) for value in values])
            return result

        sessions.items.list.side_effect = lambda *a, **k: listing(items)
        sessions.turns.list.side_effect = lambda *a, **k: listing(turns)
        sessions.retrieve.side_effect = lambda *a, **k: sdk(current)

        def events(initial):
            number = len(turns) + 1
            turn = {"id": "turn-" + str(number), "object": "agent.session.turn", "session_id": "session", "agent_id": "agent",
                    "subagent_id": None, "status": "in_progress", "created_at": number, "started_at": number,
                    "completed_at": None, "error": None, "usage": None}
            turns.append(turn)
            current.update(status="in_progress", usage=None)
            if initial:
                created = {"type": "agent.session.created", "event_id": "created", "session": copy.deepcopy(current)}
                yield SimpleNamespace(type=created["type"], session=sdk(current), to_dict=lambda: created)
            yield sdk({"type": "agent.session.turn.created", "event_id": str(number) + "-created", "session_id": "session",
                       "turn_id": turn["id"], "turn": copy.deepcopy(turn)})
            turn.update(status="completed", completed_at=number, usage=copy.deepcopy(usage))
            marker = prompts[0].split("Remember ", 1)[1].split(".", 1)[0]
            items.append({"id": "answer-" + str(number), "turn_id": turn["id"], "phase": None, "type": "message", "role": "assistant", "status": "completed",
                          "content": [{"type": "output_text", "text": marker}]})
            if measured:
                current["usage"] = {"input_tokens": 7 * number, "input_tokens_details": {"cached_tokens": 2 * number},
                    "output_tokens": 3 * number, "output_tokens_details": {"reasoning_tokens": number}, "total_tokens": 10 * number}
                if fault == "totals":
                    current["usage"]["total_tokens"] += 1
            current["status"] = "idle"
            terminal_usage = copy.deepcopy(usage)
            if fault == "terminal_usage":
                terminal_usage["total_tokens"] += 1
            yield sdk({"type": "agent.session.turn.completed", "event_id": str(number) + "-completed", "session_id": "session",
                       "turn_id": turn["id"], "turn": copy.deepcopy(turn), "usage": terminal_usage})
            yield sdk({"type": "agent.session.idle", "event_id": str(number) + "-idle", "session": copy.deepcopy(current)})

        def create(**options):
            if not options.get("stream"):
                return sdk(current)
            prompts.append(options["input"])
            stream = MagicMock()
            stream.__iter__.side_effect = lambda: events(True)
            return stream

        def continuation(*args, **options):
            stream = MagicMock()
            stream.__iter__.side_effect = lambda: events(False)
            return stream

        sessions.create.side_effect, sessions.stream.side_effect = create, continuation

        def response(method, url, status, body):
            return httpx2.Response(status, json=body, request=httpx2.Request(method, url))

        def post(url, headers, json):
            if json["input"].startswith("Changed "):
                return response("POST", url, 409, {"error": {"message": "Conflict", "type": "conflict_error", "code": "conflict_error", "param": None}})
            if fault == "duplicate":
                turns.append({**turns[0], "id": "duplicate"})
            return response("POST", url, 201, current)

        def get(url, headers, **kwargs):
            if headers["Authorization"] == "Bearer foreign":
                return response("GET", url, 404, {"error": {"message": "Missing", "type": "not_found_error", "code": "not_found", "param": None}})
            if url.endswith("/items"):
                body = page(items)
            elif url.endswith("/turns"):
                body = page(turns)
            else:
                body = copy.deepcopy(current)
                if fault == "missing_usage":
                    del body["usage"]
            return response("GET", url, 200, body)

        http.get.side_effect, http.post.side_effect = get, post
        restart = MagicMock()
        with patch.object(qualification, "delete_session") as delete:
            checks = qualification.verify_none(client, foreign, http, {"model": "fixture"}, {"environment": {"type": "none"}},
                ready=lambda session: None, restart=restart, record=lambda proof: proofs.append(copy.deepcopy(proof)))
            delete.assert_called_once_with(sessions, "session")
        restart.assert_called_once_with()
        self.assertEqual(len(turns), 2)
        return checks, proofs[-1]

    def test_measured_turns_and_session_totals_survive_continuation(self):
        checks, proof = self.run_none()
        self.assertEqual(proof["unverified"], [])
        self.assertEqual(proof["snapshots"][-1]["session"]["usage"]["total_tokens"], 20)
        self.assertIn("initial_input_create_retry_conflict_one_native_turn_and_foreign_history_rejection", checks)

    def test_unknown_usage_is_explicit_not_a_measurement_claim(self):
        _, proof = self.run_none(measured=False)
        self.assertEqual(len(proof["unverified"]), 2)
        self.assertIsNone(proof["snapshots"][-1]["session"]["usage"])

    def test_missing_required_usage_is_not_nullable_usage(self):
        with self.assertRaises(ValidationError):
            self.run_none(fault="missing_usage")

    def test_rejects_incorrect_session_sum(self):
        with self.assertRaises(AssertionError):
            self.run_none(fault="totals")

    def test_terminal_usage_must_match_the_stored_turn(self):
        with self.assertRaises(AssertionError):
            self.run_none(fault="terminal_usage")

    def test_creation_retry_cannot_append_a_turn(self):
        with self.assertRaises(AssertionError):
            self.run_none(fault="duplicate")


if __name__ == "__main__":
    unittest.main()
