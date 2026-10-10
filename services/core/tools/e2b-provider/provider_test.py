"""Controlled SDK boundary failures; live qualification remains separate."""
import copy
import io
import httpx
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import tempfile
import subprocess
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import MagicMock, Mock, patch
from uuid import uuid4

from e2b import FileType, SandboxState
from e2b.connection_config import ConnectionConfig
from e2b.sandbox_sync.filesystem.filesystem import Filesystem
from packaging.version import Version
from e2b.exceptions import AuthenticationException, FileNotFoundException, SandboxNotFoundException

from provider import BOOTSTRAP_SCRIPT, Provider, create_stage, observe_bootstrap
from helper_contract_generated import PROTOCOL_VERSION
from sdk import restore, run
from state import Failure, Receipt


class BootstrapScriptTest(unittest.TestCase):
    def test_sdk_timings_preserve_single_start_and_wait(self):
        for failure in (None, 'start', 'wait'):
            with self.subTest(failure=failure):
                sandbox = Mock()
                process = sandbox.commands.run.return_value
                process.wait.return_value = SimpleNamespace(stdout='receipt', stderr='', exit_code=0)
                if failure == 'start':
                    sandbox.commands.run.side_effect = TimeoutError('private error')
                if failure == 'wait':
                    process.wait.side_effect = TimeoutError('private error')
                stream = io.StringIO()
                with patch('provider.sys.stderr', stream):
                    if failure:
                        with self.assertRaises(Failure):
                            run(sandbox, {'Args': ['python3']}, lambda: 30, observe_stage=create_stage)
                    else:
                        self.assertEqual(run(sandbox, {'Args': ['python3']}, lambda: 30, observe_stage=create_stage)['Stdout'], 'receipt')
                rows = [json.loads(line) for line in stream.getvalue().splitlines()]
                self.assertEqual([row['stage'] for row in rows], ['bootstrap_stream_open'] + ([] if failure == 'start' else ['bootstrap_stream_completion']))
                self.assertEqual(rows[-1]['completed'], failure is None)
                sandbox.commands.run.assert_called_once()
                self.assertEqual(process.wait.call_count, 0 if failure == 'start' else 1)
                self.assertNotIn('private', stream.getvalue())

    def test_guest_observations_are_bounded_and_secret_safe(self):
        valid = dict(event='e2b_bootstrap_stage', stage='bootstrap_claim', duration_us=42, completed=True)
        bad = [dict(valid, stage='private-secret'), dict(valid, credential='private-secret'),
               dict(valid, duration_us=True), dict(valid, duration_us=-1), dict(valid, duration_us=1.5),
               dict(valid, duration_us=1_800_000_001), dict(valid, completed='true'),
               dict(valid, stage=[]), None, []]
        raw = '\n'.join(['private stderr'] + [json.dumps(row) for row in bad] + [json.dumps(valid)] * 2)
        stream = io.StringIO()
        with patch('provider.sys.stderr', stream):
            observe_bootstrap(raw)
            observe_bootstrap('x' * 8193)
            observe_bootstrap(None)
        self.assertEqual([json.loads(line) for line in stream.getvalue().splitlines()],
                         [dict(valid, event='e2b_create_stage')])
        with patch('provider.sys.stderr') as stream:
            stream.write.side_effect = RuntimeError('sink failure')
            observe_bootstrap(json.dumps(valid))

    def execute(self, startup, receipt=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            entry = root / 'managed_init.py'
            ready = root / 'managed-ready.json'
            entry.write_text(startup.replace('READY_PATH', repr(str(ready))))
            if receipt is not None:
                ready.write_bytes(receipt)
            script = BOOTSTRAP_SCRIPT.replace('/opt/oac-e2b/managed_init.py', str(entry)).replace(
                '/root/.oac/e2b/managed-ready.json', str(ready))
            return subprocess.run([sys.executable, '-I', '-c', script],
                                  capture_output=True, timeout=5)

    def test_success_reads_receipt_after_initialization(self):
        result = self.execute("from pathlib import Path; Path(READY_PATH).write_bytes(b'new receipt')",
                              receipt=b'stale receipt')
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, b'new receipt')

    def test_failed_initialization_cannot_return_old_receipt(self):
        result = self.execute('raise SystemExit(7)', receipt=b'stale receipt')
        self.assertEqual(result.returncode, 7)
        self.assertEqual(result.stdout, b'')

    def test_missing_and_oversized_receipts_leave_startup_recoverable(self):
        for receipt in (None, b'x' * 4097):
            with self.subTest(size=len(receipt) if receipt else None):
                result = self.execute('pass', receipt=receipt)
                self.assertEqual(result.returncode, 0)
                self.assertEqual(result.stdout, b'')


class ProviderTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.reference = {key: str(uuid4()) for key in ['TenantID', 'EnvironmentID', 'AllocationID']}
        self.config = {'StateDir': self.temporary.name, 'InstallationID': str(uuid4()),
                       'APIKey': 'private-account-secret', 'Template': 'test:' + str(uuid4()), 'TimeoutSeconds': 120}
        self.request = {'Version': PROTOCOL_VERSION, 'Operation': 'create', 'Config': self.config,
                        'Reference': self.reference, 'Deadline': (datetime.now(timezone.utc) + timedelta(seconds=30)).isoformat(),
                        'Bootstrap': dict(self.reference, SessionID=str(uuid4()), DeviceID=str(uuid4()),
                                          CoreURL='https://core.example/api/v1', Credential='private-runtime-secret',
                                          Harness='codex', NetworkAccess='enabled', AllowedDomains=[])}
        self.request['RuntimeBootstrap'] = {
            'version': 2, 'harness': 'codex', 'core_url': self.request['Bootstrap']['CoreURL'],
            'device_id': self.request['Bootstrap']['DeviceID'],
            'credential': self.request['Bootstrap']['Credential']}
        self.cloud = Mock(sandbox_id='owned-id', sandbox_domain='e2b.app', _envd_version='0.5.0',
                          _envd_access_token='private-envd-secret', traffic_access_token=None, state='running')
        self.cloud.metadata = Provider(self.request).metadata
        self.cloud.template_id = self.config['Template']
        self.identity = dict(self.reference, InstallationID=self.config['InstallationID'],
                             SessionID=self.request['Bootstrap']['SessionID'], DeviceID=self.request['Bootstrap']['DeviceID'])
        self.ready = json.dumps({'identity': self.identity, 'status': 'daemon_started', 'daemon_pid': 123})
        self.cloud.files.get_info.return_value = SimpleNamespace(type=FileType.FILE)
        self.template_stream = MagicMock()
        self.cloud.files.read.return_value = self.ready
        self.cloud.files.read.side_effect = lambda path, **kwargs: (
            self.template_stream if path == '/opt/oac-e2b/managed_init.py'
            else self.cloud.files.read.return_value)
        self.api = Mock()
        self.api.create.return_value = self.cloud
        self.api.get_info.return_value = self.cloud
        self.api.list.return_value = SimpleNamespace(has_next=False)
        self.patch = patch('provider.Sandbox', self.api)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.runtime = patch('provider.restore', return_value=self.cloud)
        self.runtime.start()
        self.addCleanup(self.runtime.stop)
        self.command = patch('provider.run', side_effect=lambda *a, **k: {
            'Stdout': self.cloud.files.read.return_value, 'Stderr': '', 'ExitCode': 0})
        self.command.start()
        self.addCleanup(self.command.stop)

    def call(self, operation):
        return Provider(dict(self.request, Operation=operation)).execute()

    def record(self):
        return json.loads(next(Path(self.temporary.name).glob('*.json')).read_text())

    def test_create_stages_preserve_order_and_exclude_secrets(self):
        stream = io.StringIO()
        with patch('provider.sys.stderr', stream):
            result = self.call('create')
        self.assertEqual(result['ErrorCode'], '')
        rows = [json.loads(line) for line in stream.getvalue().splitlines()]
        self.assertEqual([row['stage'] for row in rows], [
            'sandbox_create', 'ownership_check', 'template_check',
            'bootstrap_write', 'bootstrap_run', 'ready_inspect'])
        for row in rows:
            self.assertEqual(set(row), {'event', 'stage', 'duration_us', 'completed'})
            self.assertTrue(row['completed'])
            self.assertGreaterEqual(row['duration_us'], 0)
        self.assertNotIn('private', stream.getvalue())

    def test_failed_create_stage_does_not_expose_exception_or_replay(self):
        stream = io.StringIO()
        self.api.create.side_effect = TimeoutError('private SDK secret')
        with patch('provider.sys.stderr', stream):
            self.assertEqual(self.call('create')['ErrorCode'], 'unconfirmed')
            self.assertEqual(self.call('create')['ErrorCode'], 'exists')
        rows = [json.loads(line) for line in stream.getvalue().splitlines()]
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]['stage'], 'sandbox_create')
        self.assertFalse(rows[0]['completed'])
        self.assertNotIn('private', stream.getvalue())
        self.api.create.assert_called_once()

    def test_failed_guest_stage_is_forwarded_without_receipt_replay(self):
        stream = io.StringIO()
        stage = dict(event='e2b_bootstrap_stage', stage='bootstrap_spawn', duration_us=123, completed=False)
        with patch('provider.run', return_value={'Stdout': '', 'Stderr': json.dumps(stage) + '\nprivate error', 'ExitCode': 1}) as command, \
                patch('provider.sys.stderr', stream):
            self.assertEqual(self.call('create')['ErrorCode'], 'unconfirmed')
            self.assertEqual(self.call('create')['ErrorCode'], 'exists')
        rows = [json.loads(line) for line in stream.getvalue().splitlines()]
        self.assertIn(dict(stage, event='e2b_create_stage'), rows)
        self.assertNotIn('private', stream.getvalue())
        command.assert_called_once()

    def test_broken_diagnostic_sink_does_not_change_create(self):
        with patch('provider.sys.stderr') as stream:
            stream.write.side_effect = OSError('sink closed')
            self.assertEqual(self.call('create')['ErrorCode'], '')

    def test_stage_uses_monotonic_duration(self):
        stream = io.StringIO()
        with patch('provider.sys.stderr', stream), patch('provider.time.monotonic_ns', side_effect=[1000, 2501000]):
            with create_stage('sandbox_create'):
                pass
        self.assertEqual(json.loads(stream.getvalue())['duration_us'], 2500)

    def test_create_recover_and_never_replay(self):
        result = self.call('create')
        self.assertEqual(result['ErrorCode'], '')
        self.assertTrue(result['Info']['BootstrapComplete'])
        self.assertTrue(result['Info']['CreateSettled'])
        startup = json.loads(self.cloud.files.write.call_args.args[1])
        self.assertEqual(startup['RuntimeBootstrap'], self.request['RuntimeBootstrap'])
        self.assertNotIn('CoreURL', startup)
        self.assertNotIn('Credential', startup)
        self.assertEqual(self.call('create')['ErrorCode'], 'exists')
        self.assertTrue(self.call('inspect')['Info']['BootstrapComplete'])
        self.api.create.assert_called_once()
        kwargs = self.api.create.call_args.kwargs
        self.assertEqual(kwargs['retries'], 0)
        self.assertEqual(kwargs['lifecycle'], {'on_timeout': 'kill', 'auto_resume': False})
        self.assertEqual(kwargs['metadata'], self.cloud.metadata)
        serialized = json.dumps(self.record())
        self.assertNotIn(self.config['APIKey'], serialized)
        self.assertNotIn(self.request['Bootstrap']['Credential'], serialized)
        self.assertEqual(self.record()['connection']['envd_access_token'], 'private-envd-secret')
        self.api.connect.assert_not_called()

    def test_create_response_without_metadata_reads_exact_id_before_bootstrap(self):
        created = SimpleNamespace(sandbox_id=self.cloud.sandbox_id,
                                  sandbox_domain=self.cloud.sandbox_domain,
                                  _envd_version=self.cloud._envd_version,
                                  _envd_access_token=self.cloud._envd_access_token,
                                  traffic_access_token=self.cloud.traffic_access_token,
                                  files=self.cloud.files)
        self.api.create.return_value = created
        self.cloud.files.write.side_effect = lambda *args, **kwargs: self.assertEqual(
            self.api.get_info.call_count, 1)
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.assertEqual(self.api.get_info.call_args.args, (created.sandbox_id,))
        self.cloud.files.write.assert_called_once()

    def test_create_without_resource_override_refuses_other_build_before_credentials(self):
        self.cloud.template_id = self.config['Template'].split(':', 1)[0] + ':' + str(uuid4())
        self.assertEqual(self.call('create')['ErrorCode'], 'invalid')
        self.api.get_info.assert_called_once()
        self.cloud.files.write.assert_not_called()

    def test_create_refuses_other_owner_before_credentials(self):
        self.cloud.metadata = {}
        self.assertEqual(self.call('create')['ErrorCode'], 'ownership')
        self.api.get_info.assert_called_once()
        self.cloud.files.write.assert_not_called()
        self.cloud.files.read.assert_not_called()

    def test_qualified_gateway_template_id_accepts_only_selected_build(self):
        self.config['Resources'] = {'cpus': 2, 'memory_mib': 2048}
        self.cloud.template_id = self.config['Template']
        self.cloud.cpu_count = 2
        self.cloud.memory_mb = 2048
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.cloud.template_id = self.config['Template'].split(':', 1)[0] + ':' + str(uuid4())
        self.assertEqual(self.call('inspect')['ErrorCode'], 'invalid')

    def test_custom_endpoint_reaches_create_inspect_and_renew(self):
        self.config.update(APIURL='https://sandbox-test.sandbase.ai', Domain='sandbox-test.sandbase.ai')
        self.cloud.sandbox_domain = 'sandbox-test.sandbase.ai'
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.assertEqual(self.call('renew')['ErrorCode'], '')
        for operation in (self.api.create, self.api.get_info, self.api.set_timeout):
            self.assertEqual(operation.call_args.kwargs['api_url'], self.config['APIURL'])
            self.assertEqual(operation.call_args.kwargs['domain'], self.config['Domain'])
        self.assertEqual(self.record()['endpoint'], {'api_url': self.config['APIURL'],
                                                     'domain': self.config['Domain']})

    def test_custom_endpoint_reaches_unknown_create_discovery(self):
        self.config.update(APIURL='https://sandbox-test.sandbase.ai', Domain='sandbox-test.sandbase.ai')
        self.api.create.side_effect = TimeoutError('uncertain')
        self.assertEqual(self.call('create')['ErrorCode'], 'unconfirmed')
        self.call('inspect')
        self.assertEqual(self.api.list.call_args.kwargs['api_url'], self.config['APIURL'])
        self.assertEqual(self.api.list.call_args.kwargs['domain'], self.config['Domain'])

    def test_create_refuses_foreign_data_plane_before_envd(self):
        self.cloud.sandbox_domain = 'foreign.example'
        self.assertEqual(self.call('create')['ErrorCode'], 'ownership')
        self.assertEqual(self.record()['ids'], ['owned-id'])
        self.cloud.files.write.assert_not_called()
        self.cloud.commands.run.assert_not_called()

    def test_template_invalid_refuses_before_credentials_and_retains_owned_cleanup(self):
        with patch.object(self.cloud.files, 'read', side_effect=FileNotFoundException('missing')):
            result = self.call('create')
        self.assertEqual(result['ErrorCode'], 'template_invalid')
        self.assertTrue(result['Info']['CreateSettled'])
        self.assertFalse(result['Info']['BootstrapComplete'])
        self.assertEqual(self.record()['ids'], ['owned-id'])
        self.cloud.files.write.assert_not_called()
        self.api.kill.assert_not_called()
        self.assertEqual(self.call('create')['ErrorCode'], 'exists')

    def test_template_non_regular_files_are_rejected_before_open_or_credentials(self):
        for kind in (FileType.DIR, None):
            with self.subTest(kind=kind):
                self.reference['AllocationID'] = str(uuid4())
                self.request['Bootstrap']['AllocationID'] = self.reference['AllocationID']
                self.cloud.metadata = Provider(self.request).metadata
                self.cloud.files.get_info.return_value = SimpleNamespace(type=kind)
                self.assertEqual(self.call('create')['ErrorCode'], 'template_invalid')
                self.cloud.files.read.assert_not_called()
                self.cloud.files.write.assert_not_called()

    def test_template_read_is_closed_and_does_not_launch_a_probe(self):
        with patch('provider.run', return_value={'ExitCode': 0, 'Stdout': self.ready}) as command:
            self.assertEqual(self.call('create')['ErrorCode'], '')
        self.template_stream.__enter__.assert_called_once()
        self.template_stream.__exit__.assert_called_once()
        self.template_stream.__iter__.assert_not_called()
        command.assert_called_once()
        self.assertEqual(command.call_args.args[1]['Args'],
                         ['/usr/bin/python3', '-I', '-c', BOOTSTRAP_SCRIPT])
        args, options = self.cloud.files.read.call_args_list[0]
        self.assertEqual(args, ('/opt/oac-e2b/managed_init.py',))
        self.assertEqual(options['format'], 'stream')
        self.assertEqual(options['user'], 'root')
        self.assertGreater(options['request_timeout'], 0)

    def test_template_probe_uses_sdk_stream_and_closes_without_downloading(self):
        class Body(httpx.SyncByteStream):
            closed = False

            def __iter__(self):
                raise AssertionError('template contents must not be downloaded')
                yield b''

            def close(self):
                self.closed = True

        body = Body()
        requests = []

        def respond(request):
            requests.append(request)
            return httpx.Response(200, stream=body)

        with httpx.Client(base_url='https://fixture.invalid',
                          transport=httpx.MockTransport(respond)) as client:
            with patch('e2b.sandbox_sync.filesystem.filesystem.get_envd_api', return_value=client), \
                 patch('e2b.sandbox_sync.filesystem.filesystem.create_rpc_client'):
                files = Filesystem('https://fixture.invalid', Version('0.5.0'),
                                   ConnectionConfig(api_key='fixture'), client)
            original_read = self.cloud.files.read.side_effect
            self.cloud.files.read.side_effect = lambda path, **kwargs: (
                files.read(path, **kwargs) if path == '/opt/oac-e2b/managed_init.py'
                else original_read(path, **kwargs))
            self.assertEqual(self.call('create')['ErrorCode'], '')
        self.assertTrue(body.closed)
        self.assertEqual(len(requests), 1)
        self.assertEqual(requests[0].method, 'GET')
        self.assertEqual(requests[0].url.params['path'], '/opt/oac-e2b/managed_init.py')
        self.assertEqual(requests[0].url.params['username'], 'root')

    def test_uncertain_template_read_never_writes_credentials_or_replays_create(self):
        with patch.object(self.cloud.files, 'read', side_effect=TimeoutError('private secret')):
            self.assertEqual(self.call('create')['ErrorCode'], 'unconfirmed')
        self.cloud.files.write.assert_not_called()
        self.assertFalse(self.record().get('bootstrap_complete', False))
        self.assertEqual(self.call('create')['ErrorCode'], 'exists')
        self.api.create.assert_called_once()

    def test_unknown_create_empty_lookup_never_proves_cleanup(self):
        self.api.create.side_effect = TimeoutError('confidential SDK diagnostic')
        self.assertEqual(self.call('create')['ErrorCode'], 'unconfirmed')
        missing = self.call('inspect')
        self.assertEqual(missing['ErrorCode'], 'not_found')
        self.assertFalse(missing['Info']['CreateSettled'])
        self.assertEqual(self.api.list.call_args.kwargs['query'].state,
                         [SandboxState.RUNNING, SandboxState.PAUSED])
        self.assertEqual([value.value for value in self.api.list.call_args.kwargs['query'].state], ['running', 'paused'])
        self.assertEqual(self.call('kill')['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.call('create')['ErrorCode'], 'exists')
        self.api.create.assert_called_once()

    def test_explicit_rejected_create_has_successful_absence_proof(self):
        self.api.create.side_effect = AuthenticationException('secret diagnostic')
        self.assertTrue(self.call('create')['Info']['CreateSettled'])
        rejection = self.record()
        self.api.reset_mock()
        self.api.list.side_effect = AuthenticationException('revoked key')
        self.api.get_info.side_effect = AuthenticationException('revoked key')
        for operation in ('inspect', 'kill', 'inspect', 'kill'):
            with self.subTest(operation=operation):
                missing = self.call(operation)
                self.assertEqual(missing['ErrorCode'], '')
                self.assertEqual(missing['Info']['State'], 'absent')
                self.assertEqual(missing['Info']['ProviderID'], '')
                self.assertTrue(missing['Info']['CreateSettled'])
                self.assertEqual(self.record(), rejection)
        self.assertEqual(self.api.mock_calls, [])

    def test_other_empty_receipts_still_require_cloud_discovery(self):
        for status, settled in [('create_pending', False), ('rejected', False), ('killed', True)]:
            with self.subTest(status=status, settled=settled):
                with Receipt(self.request, lambda: 30) as receipt:
                    receipt.save(status=status, settled=settled, ids=[])
                self.api.reset_mock()
                self.api.list.side_effect = AuthenticationException('revoked key')
                for operation in ('inspect', 'kill'):
                    self.assertEqual(self.call(operation)['ErrorCode'], 'unconfirmed')
                self.assertEqual(self.api.list.call_count, 2)
                self.api.kill.assert_not_called()

    def test_rejected_receipt_with_ids_still_verifies_ownership(self):
        with Receipt(self.request, lambda: 30) as receipt:
            receipt.save(status='rejected', settled=True, ids=[self.cloud.sandbox_id])
        self.cloud.metadata = {}
        for operation in ('inspect', 'kill'):
            self.assertEqual(self.call(operation)['ErrorCode'], 'ownership')
        self.assertEqual(self.api.get_info.call_count, 2)
        self.api.kill.assert_not_called()

    def test_killed_known_vm_still_requires_cloud_absence_check(self):
        self.call('create')
        self.api.get_info.side_effect = SandboxNotFoundException()
        self.assertEqual(self.call('kill')['ErrorCode'], '')
        self.api.reset_mock()
        self.api.get_info.side_effect = AuthenticationException('revoked key')
        for operation in ('inspect', 'kill'):
            self.assertEqual(self.call(operation)['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.api.get_info.call_count, 2)
        self.api.kill.assert_not_called()

    def test_unknown_bootstrap_only_settles_after_exact_kill(self):
        self.cloud.files.write.side_effect = TimeoutError()
        self.assertFalse(self.call('create')['Info']['CreateSettled'])
        self.api.kill.side_effect = lambda *args, **kwargs: setattr(self.api.get_info, 'side_effect', SandboxNotFoundException())
        self.assertEqual(self.call('kill')['ErrorCode'], '')
        self.assertTrue(self.call('inspect')['Info']['CreateSettled'])
        self.assertIsNone(self.record()['connection'])
        self.api.kill.assert_called_once_with('owned-id', **self.api.kill.call_args.kwargs)

    def test_bootstrap_failure_is_settled_but_not_ready(self):
        with patch('provider.run', return_value={'ExitCode': 1}):
            result = self.call('create')
        self.assertTrue(result['Info']['CreateSettled'])
        self.assertFalse(result['Info']['BootstrapComplete'])
        self.assertEqual(self.call('inspect')['Info']['State'], 'running')

    def test_mismatched_receipt_cannot_prove_bootstrap_complete(self):
        self.cloud.files.read.return_value = json.dumps({'identity': {}, 'status': 'daemon_started', 'daemon_pid': 2})
        self.assertEqual(self.call('create')['ErrorCode'], 'ownership')
        self.assertTrue(self.record()['settled'])
        self.assertFalse(self.record()['bootstrap_complete'])

    def test_successful_create_returns_receipt_without_remote_receipt_read(self):
        self.assertTrue(self.call('create')['Info']['BootstrapComplete'])
        self.assertEqual([call.args[0] for call in self.cloud.files.read.call_args_list],
                         ['/opt/oac-e2b/managed_init.py'])
        self.assertEqual(self.api.get_info.call_count, 2)

    def test_lost_command_receipt_recovers_from_file_without_replaying_startup(self):
        with patch('provider.run', return_value={'ExitCode': 0, 'Stdout': ''}) as command:
            result = self.call('create')
            self.assertEqual(result['ErrorCode'], 'unconfirmed')
            self.assertTrue(result['Info']['CreateSettled'])
            self.assertFalse(result['Info']['BootstrapComplete'])
            self.assertTrue(self.call('inspect')['Info']['BootstrapComplete'])
            self.assertEqual(self.call('create')['ErrorCode'], 'exists')
            command.assert_called_once()
        self.api.create.assert_called_once()

    def test_foreign_owner_prevents_renew_and_delete(self):
        self.call('create')
        self.cloud.metadata = {}
        self.assertEqual(self.call('renew')['ErrorCode'], 'ownership')
        self.assertEqual(self.call('kill')['ErrorCode'], 'ownership')
        self.api.set_timeout.assert_not_called()
        self.api.kill.assert_not_called()

    def test_expired_unsettled_known_vm_is_cleanable(self):
        self.cloud.files.write.side_effect = TimeoutError()
        self.call('create')
        self.api.get_info.side_effect = SandboxNotFoundException()
        self.assertEqual(self.call('inspect')['ErrorCode'], 'not_found')
        self.assertEqual(self.call('kill')['ErrorCode'], '')
        self.assertTrue(self.call('inspect')['Info']['CreateSettled'])

    def test_paused_vm_never_resumes(self):
        self.call('create')
        self.cloud.state = 'paused'
        self.assertEqual(self.call('inspect')['Info']['State'], 'paused')
        self.assertEqual(self.call('renew')['ErrorCode'], 'unconfirmed')
        self.api.set_timeout.assert_not_called()
        self.api.connect.assert_not_called()

    def test_lost_create_discovers_every_page_and_cleans_all_candidates(self):
        self.api.create.side_effect = TimeoutError()
        self.call('create')
        other = SimpleNamespace(sandbox_id='other-owned-id', metadata=self.cloud.metadata, state='running')
        pages = iter([[self.cloud], [other]])
        paginator = SimpleNamespace(has_next=True)
        def next_items(**options):
            self.assertEqual(options["retries"], 0)
            page = next(pages)
            if page == [other]:
                paginator.has_next = False
            return page
        paginator.next_items = next_items
        self.api.list.return_value = paginator
        self.assertEqual(self.call('inspect')['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.record()['ids'], ['other-owned-id', 'owned-id'])
        deleted = set()
        self.api.get_info.side_effect = lambda sandbox_id, **kwargs: self.maybe_missing(sandbox_id, deleted, other)
        self.api.kill.side_effect = lambda sandbox_id, **kwargs: deleted.add(sandbox_id)
        self.assertEqual(self.call('kill')['ErrorCode'], '')
        self.assertEqual(deleted, {'other-owned-id', 'owned-id'})

    def maybe_missing(self, sandbox_id, deleted, other):
        if sandbox_id in deleted:
            raise SandboxNotFoundException()
        return self.cloud if sandbox_id == self.cloud.sandbox_id else other


class SDKTest(unittest.TestCase):
    def test_constructor_restores_without_a_control_plane_operation(self):
        material = {'sandbox_id': 'fixture', 'sandbox_domain': 'e2b.app', 'envd_version': '0.5.0',
                    'envd_access_token': 'synthetic-envd-secret', 'traffic_access_token': None}
        with patch('e2b.sandbox_sync.sandbox_api.SandboxApi._cls_connect', side_effect=AssertionError('connect forbidden')), \
                patch('e2b.sandbox_sync.sandbox_api.SandboxApi._create_sandbox', side_effect=AssertionError('create forbidden')):
            client = restore(material, {'api_key': 'synthetic-key', 'retries': 0, 'debug': False})
        self.assertEqual(client.sandbox_id, material['sandbox_id'])
        self.assertEqual(client.connection_config.sandbox_headers['X-Access-Token'], material['envd_access_token'])
        self.assertEqual(client.connection_config.retries, 0)

    def test_stdin_is_separate_from_quoted_command_and_receives_eof(self):
        import base64
        client = Mock()
        client.commands.run.return_value.pid = 23
        client.commands.run.return_value.wait.return_value = SimpleNamespace(stdout='ok', stderr='', exit_code=7)
        result = run(client, {'Args': ['cat', 'a;touch /unwanted'], 'Directory': '/workspace',
                              'Stdin': base64.b64encode(b'private-input').decode()}, lambda: 3)
        self.assertEqual(result['ExitCode'], 7)
        self.assertEqual(client.commands.run.call_args.args, ("cat 'a;touch /unwanted'",))
        client.commands.send_stdin.assert_called_once_with(23, b'private-input', request_timeout=3)
        client.commands.close_stdin.assert_called_once_with(23, request_timeout=3)

    def test_unknown_command_does_not_replay(self):
        client = Mock()
        client.commands.run.side_effect = TimeoutError('private command')
        with self.assertRaises(Failure) as result:
            run(client, {'Args': ['true']}, lambda: 3)
        self.assertEqual(result.exception.code, 'command_unconfirmed')
        client.commands.run.assert_called_once()


if __name__ == '__main__':
    unittest.main()
