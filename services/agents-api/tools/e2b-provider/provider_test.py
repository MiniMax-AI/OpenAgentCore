"""Controlled SDK boundary failures; live qualification remains separate."""
import copy
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch
from uuid import uuid4

from e2b import SandboxState
from e2b.exceptions import AuthenticationException, SandboxNotFoundException

from provider import Provider
from sdk import restore, run
from state import Failure, Receipt


class ProviderTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.reference = {key: str(uuid4()) for key in ['TenantID', 'EnvironmentID', 'AllocationID']}
        self.config = {'StateDir': self.temporary.name, 'InstallationID': str(uuid4()),
                       'APIKey': 'private-account-secret', 'Template': 'test:' + str(uuid4()), 'TimeoutSeconds': 120}
        self.request = {'Version': 1, 'Operation': 'create', 'Config': self.config,
                        'Reference': self.reference, 'Deadline': (datetime.now(timezone.utc) + timedelta(seconds=30)).isoformat(),
                        'Bootstrap': dict(self.reference, SessionID=str(uuid4()), DeviceID=str(uuid4()),
                                          CoreURL='https://core.example/api/v1', Credential='private-runtime-secret',
                                          NetworkAccess='enabled', AllowedDomains=[])}
        self.cloud = Mock(sandbox_id='owned-id', sandbox_domain='e2b.app', _envd_version='0.5.0',
                          _envd_access_token='private-envd-secret', traffic_access_token=None, state='running')
        self.cloud.metadata = Provider(self.request).metadata
        self.identity = dict(self.reference, InstallationID=self.config['InstallationID'],
                             SessionID=self.request['Bootstrap']['SessionID'], DeviceID=self.request['Bootstrap']['DeviceID'])
        self.ready = json.dumps({'identity': self.identity, 'status': 'daemon_started', 'daemon_pid': 123})
        self.cloud.files.read.return_value = self.ready
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
        self.command = patch('provider.run', return_value={'Stdout': '', 'Stderr': '', 'ExitCode': 0})
        self.command.start()
        self.addCleanup(self.command.stop)

    def call(self, operation):
        return Provider(dict(self.request, Operation=operation)).execute()

    def record(self):
        return json.loads(next(Path(self.temporary.name).glob('*.json')).read_text())

    def test_create_recover_and_never_replay(self):
        result = self.call('create')
        self.assertEqual(result['ErrorCode'], '')
        self.assertTrue(result['Info']['BootstrapComplete'])
        self.assertTrue(result['Info']['CreateSettled'])
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

    def test_legacy_template_refuses_before_credentials_and_retains_owned_cleanup(self):
        with patch('provider.run', return_value={'ExitCode': 78, 'Stdout': '', 'Stderr': ''}):
            result = self.call('create')
        self.assertEqual(result['ErrorCode'], 'legacy_template')
        self.assertTrue(result['Info']['CreateSettled'])
        self.assertFalse(result['Info']['BootstrapComplete'])
        self.assertEqual(self.record()['ids'], ['owned-id'])
        self.cloud.files.write.assert_not_called()
        self.api.kill.assert_not_called()
        self.assertEqual(self.call('create')['ErrorCode'], 'exists')

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
