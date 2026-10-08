"""Check qualification's secret handling and owned restart boundary without a model."""

import contextlib
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
from openai import BadRequestError, OpenAI

from official_environment_files_native import generate_files
from official_environment_composition import composition_fixture
from official_environment_initial_files import assert_initial_bytes_script

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
            for verify in (qualification.verify_hosted_functions, qualification.verify_workspace_images, qualification.verify_pending_actions):
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


if __name__ == "__main__":
    unittest.main()
