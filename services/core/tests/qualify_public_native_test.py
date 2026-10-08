"""Check qualification's secret handling and owned restart boundary without a model."""

import contextlib
import copy
import base64
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import zipfile
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import httpx2
from jsonschema import ValidationError
from openai import BadRequestError, NotFoundError, OpenAI

from official_environment_files_native import generate_files
from official_environment_composition import composition_fixture
from official_environment_initial_files import assert_initial_bytes_script
import official_environment_composition as composition

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

    def test_native_admission_budget_does_not_extend_raw_http_checks(self):
        requests = []

        def reject(request):
            requests.append(request)
            return httpx2.Response(400, json={"error": {"type": "invalid_request_error", "message": "fixture"}})

        def suite(client, foreign, http, *args, **kwargs):
            for sdk in (client, foreign):
                with self.assertRaises(BadRequestError):
                    sdk.beta.agents.sessions.events.create("fixture-session", events=[{
                        "type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": "fixture"}]}]}])
                self.assertEqual(requests[-1].extensions["timeout"]["read"], 360)
            http.get("https://core.example/v1/agents/sessions/fixture-session")
            self.assertEqual(requests[-1].extensions["timeout"]["read"], 30)
            return []

        transport = SimpleNamespace(Client=lambda **kwargs: httpx2.Client(transport=httpx2.MockTransport(reject), **kwargs))
        with patch.object(qualification, "OpenAI", OpenAI), patch.object(qualification, "httpx2", transport):
            self.run_suite(suite)

    def test_active_policy_suites_cannot_claim_process_restart(self):
        for name in ("policies", "steering"):
            with self.subTest(suite=name):
                argv = [name if value == "none" else value for value in self.argv]
                argv += ["--compose-directory", str(self.root), "--compose-project", "owned-qualification"]
                with patch("sys.argv", argv), patch.object(qualification.subprocess, "run") as run:
                    with self.assertRaisesRegex(AssertionError, "does not qualify process restart"):
                        qualification.main()
                    run.assert_not_called()

    def test_deferred_variant_is_explicit_and_policy_failures_do_not_pass(self):
        self.settings["environment"] = {"type": "openai_hosted"}
        (self.root / "settings.json").write_text(json.dumps(self.settings))
        for name, target in (("tool-search", "verify_hosted_functions"), ("policies", "verify_native_policies"), ("steering", "verify_native_steering")):
            with self.subTest(suite=name):
                argv = [name if value == "none" else value for value in self.argv]
                def suite(*args, **kwargs):
                    self.assertEqual(kwargs.get("deferred", False), name == "tool-search")
                    self.assertEqual("restart" in kwargs, name == "tool-search")
                    kwargs["record"]({"unverified": ["native policy behavior"]})
                    raise AssertionError("native admission blocked")
                with patch("sys.argv", argv), patch.object(qualification, target, suite):
                    with self.assertRaisesRegex(AssertionError, "native admission blocked"):
                        qualification.main()
                proof = json.loads(self.evidence.read_text())
                self.assertEqual((proof["status"], proof["cold_recovery"]), ("failed", "unverified"))
                self.assertEqual(proof["proof"]["unverified"], ["native policy behavior"])
                self.evidence.unlink()

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
            selected = {**self.settings["agent"], "x_agents_core": {"harness": "claude_sdk"}}
            with self.assertRaises(BadRequestError):
                qualification.verify_hosted_functions(client, client, http, selected, options,
                    ready=lambda session: None, restart=None, record=lambda proof: None, deferred=True)
            tools = bodies[-1]["agent"]["tools"]
            self.assertEqual(sum(tool["type"] == "tool_search" for tool in tools), 1)
            self.assertTrue(next(tool for tool in tools if tool["type"] == "function")["defer_loading"])
            self.assertEqual(bodies[-1]["x_agents_core"]["model_provider"], self.settings["model_provider"])
            selected["x_agents_core"] = {"harness": "mcode"}
            with self.assertRaises(BadRequestError):
                qualification.verify_native_steering(client, client, http, selected, options,
                    ready=lambda session: None, record=lambda proof: None)
            self.assertEqual(bodies[-1]["environment"], options["environment"])
            self.assertEqual(bodies[-1]["agent"], selected)

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

    def test_composition_rejects_preselected_environment_before_creating_resources(self):
        self.settings['environment'] = {'type': 'openai_hosted', 'environment_template_id': 'foreign-template'}
        (self.root / 'settings.json').write_text(json.dumps(self.settings))
        self.argv[self.argv.index('none')] = 'composition'
        with patch('sys.argv', self.argv), patch.object(qualification, 'verify_composition') as suite:
            with self.assertRaisesRegex(AssertionError, 'fresh hosted'):
                qualification.main()
            suite.assert_not_called()

    def test_composition_partial_failure_cleans_sources_and_keeps_provider_selection(self):
        client, http = MagicMock(), MagicMock()
        client.base_url = 'https://core.example/v1'
        client.api_key = 'project-secret'
        client.files.create.return_value = SimpleNamespace(id='source-file')
        client.skills.create.side_effect = RuntimeError('upload failed')
        http.post.return_value = SimpleNamespace(status_code=404, text='not found')
        with self.assertRaisesRegex(RuntimeError, 'upload failed'), contextlib.ExitStack() as cleanup:
            composition_fixture(client, SimpleNamespace(api_key='foreign-secret'), http,
                self.settings['agent'], self.settings['model_provider'], cleanup)
        client.files.delete.assert_called_once_with('source-file')
        self.assertEqual(http.post.call_args.kwargs['json']['x_agents_core']['model_provider'], self.settings['model_provider'])
        self.assertEqual(http.post.call_args.kwargs['headers']['Authorization'], 'Bearer foreign-secret')

    def test_composition_session_failure_cleans_template_and_all_sources(self):
        client, http = MagicMock(), MagicMock()
        client.base_url = 'https://core.example/v1'
        client.api_key = 'project-secret'
        client.files.create.return_value = SimpleNamespace(id='source-file')
        client.skills.create.return_value = SimpleNamespace(id='source-skill', name='proof-skill', description='proof', default_version='1')
        client.beta.agents.environments.templates.create.return_value = SimpleNamespace(id='template')
        client.beta.agents.sessions.create.side_effect = RuntimeError('session failed')
        http.post.return_value = SimpleNamespace(status_code=404, text='not found')
        options = {'environment': {'type': 'openai_hosted'}, 'extra_body': {'x_agents_core': {'model_provider': self.settings['model_provider']}}}
        with self.assertRaisesRegex(RuntimeError, 'session failed'):
            qualification.verify_composition(client, SimpleNamespace(api_key='foreign-secret'), http,
                self.settings['agent'], options, ready=MagicMock(), restart=None, record=MagicMock())
        client.files.delete.assert_called_once_with('source-file')
        client.skills.delete.assert_called_once_with('source-skill')
        client.beta.agents.environments.templates.delete.assert_called_once_with('template')
        self.assertEqual(client.beta.agents.sessions.create.call_args.kwargs['extra_body'], options['extra_body'])
        configuration = client.beta.agents.environments.templates.create.call_args.kwargs
        self.assertEqual(configuration['network'], {'access': 'enabled'})
        self.assertEqual(set(configuration['packages']), {'npm', 'python'})
        for plugin in configuration['plugins']:
            with zipfile.ZipFile(io.BytesIO(base64.b64decode(plugin['source']['data']))) as archive:
                mcp = json.loads(archive.read('proof/.mcp.json'))
                for server in mcp['mcpServers'].values():
                    self.assertNotIn('env_vars', server)
        # Exercise the actual prepared proof script's byte and freshness checks;
        # dependency installation and native execution remain live-only checks.
        workspace = self.root / 'prepared'
        workspace.mkdir()
        for item in configuration['files']:
            body = base64.b64decode(item['data']) if item['type'] == 'inline' else bytes(range(256))
            (workspace / item['path'].removeprefix('/workspace/')).write_bytes(body)
        (workspace / 'setup-sub').mkdir()
        (workspace / 'setup-sub/order').write_text('second')
        (workspace / 'setup-once').write_text('initialized')
        (workspace / 'plugin-mcp-setup-count').write_text('1')
        script = compile((workspace / 'verify.py').read_text(), 'prepared verify.py', 'exec')
        with contextlib.chdir(workspace), patch.dict(os.environ, configuration['env']), \
                patch.dict('sys.modules', {'packaging': SimpleNamespace(__version__='26.0')}), \
                patch.object(subprocess, 'check_output', return_value=b'1.2.3\n'), patch.object(subprocess, 'run'):
            for run in (1, 2):
                exec(script, {})
                self.assertEqual(json.loads((workspace / 'outputs/composition.json').read_text())['run'], run)
            (workspace / 'initial-source.bin').write_bytes(b'changed')
            with self.assertRaises(AssertionError):
                exec(script, {})
            self.assertEqual((workspace / 'composition-run-count').read_text(), '2')

    def test_initial_byte_assertion_uses_declared_working_directory(self):
        workspace = self.root / 'custom-workspace'
        workspace.mkdir()
        expected = b'\x00\xffbinary'
        (workspace / 'initial.bin').write_bytes(expected)
        script = assert_initial_bytes_script({'/workspace/initial.bin': expected})
        with contextlib.chdir(workspace):
            exec(compile(script, '<initial byte assertion>', 'exec'), {'Path': Path})
            (workspace / 'initial.bin').write_bytes(b'changed')
            with self.assertRaises(AssertionError):
                exec(compile(script, '<initial byte assertion>', 'exec'), {'Path': Path})

    def test_mcp_hold_waits_for_directory_and_turn_without_hiding_errors(self):
        request = httpx2.Request('GET', 'https://core.example/v1/files')
        missing = NotFoundError('missing', response=httpx2.Response(404, request=request), body=None)
        invalid = BadRequestError('invalid', response=httpx2.Response(400, request=request), body=None)
        output = SimpleNamespace(path='/workspace/outputs/composition.json', size_bytes=2)
        ticks = '/workspace/plugin-mcp-hold/ticks.jsonl'
        invocation = '/workspace/plugin-mcp-hold/invocation.json'
        growing = [[SimpleNamespace(path=ticks, size_bytes=size), SimpleNamespace(path=invocation, size_bytes=1)]
                   for size in (5, 10, 15, 20)]
        fixture = {'configuration': {}, 'prompt': 'verify', 'outputs': {output.path: b'{}'},
                   'composition_proof': {}, 'hold_prompt': 'hold', 'hold_server': 'server',
                   'hold_paths': {'ticks': ticks, 'invocation': invocation}}
        for label, polling, error in (
            ('delayed directory and turn', [missing, *growing, growing[-1]], None),
            ('directory disappeared', [missing, growing[0], missing], NotFoundError),
            ('other error', [invalid], BadRequestError),
        ):
            with self.subTest(label), contextlib.ExitStack() as patches:
                client = MagicMock()
                sessions = client.beta.agents.sessions
                session = SimpleNamespace(id='session', environment=SimpleNamespace(id='environment'), status='idle', required_actions=[])
                sessions.create.return_value = sessions.retrieve.return_value = session
                sessions.turns.list.side_effect = [[], [], [], [], [SimpleNamespace(id='hold-turn', status='in_progress')]]
                sessions.turns.retrieve.return_value = SimpleNamespace(status='cancelled')
                stream_events = [{'type': 'agent.session.turn.completed', 'turn': {'id': 'completed-turn'}},
                                 {'type': 'agent.session.idle'}]
                sessions.stream.return_value.__enter__.return_value = [
                    SimpleNamespace(to_dict=lambda event=event: event) for event in stream_events]
                client.beta.agents.environments.files.list.side_effect = [[output], [output], *polling]
                patches.enter_context(patch.object(composition, 'composition_fixture', return_value=fixture))
                for name in ('change_and_delete_sources', 'verify_composition_metadata', 'verify_session_artifacts', 'verify_plugin_mcp_items'):
                    patches.enter_context(patch.object(composition, name, return_value={}))
                patches.enter_context(patch.object(composition.time, 'sleep'))
                options = {'environment': {'type': 'openai_hosted'},
                           'extra_body': {'x_agents_core': {'model_provider': self.settings['model_provider']}}}
                if error is not None:
                    with self.assertRaises(error):
                        composition.verify_composition(client, MagicMock(), MagicMock(), self.settings['agent'],
                            options, ready=MagicMock(), restart=None, record=MagicMock())
                    sessions.turns.retrieve.assert_not_called()
                else:
                    checks = composition.verify_composition(client, MagicMock(), MagicMock(), self.settings['agent'],
                        options, ready=MagicMock(), restart=None, record=MagicMock())
                    self.assertIn('native_mcp_cancel_retry_stops_descendant_effects', checks)
                    sessions.turns.retrieve.assert_called_once_with('hold-turn', session_id='session')
                    self.assertEqual(sessions.events.create.call_count, 3)


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
