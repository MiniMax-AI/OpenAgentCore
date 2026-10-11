"""Controlled startup contract tests; these do not qualify an E2B Runtime."""
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

import init
import launch


PAYLOAD = {
    'launch_id': 'bfe27bc4-dcd7-4aa3-9196-94f7617c9d6b',
    'environment_id': 'b03296db-a32d-49e3-afc3-0b2a3c00f73b',
    'remote_url': 'wss://core.example.com/api/v1/agent-daemon/ws',
    'executor_key': {'key_id': '96e3ba3e-6eb7-4f1b-b65b-1c836c419297',
                     'executor_token': 'test-private-key'},
}
TEMPLATE = 'qualified:' + PAYLOAD['launch_id']


class StartupTest(unittest.TestCase):
    def test_identity_rejects_confused_binding_and_secret_urls(self):
        for field, value in [('environment_id', 'not-a-uuid'),
                             ('remote_url', PAYLOAD['remote_url'] + '?token=private'),
                             ('remote_url', 'wss://secret@core.example.com/api/v1/agent-daemon/ws'),
                             ('executor_key', dict(PAYLOAD['executor_key'], environment_id=PAYLOAD['launch_id']))]:
            with self.subTest(field=field), self.assertRaises(ValueError):
                init.launch_identity(dict(PAYLOAD, **{field: value}))
        self.assertEqual(init.launch_identity(PAYLOAD)['remote_url'], PAYLOAD['remote_url'])

    def test_launch_handoff_is_private_and_cannot_replay(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / 'root'
            root.mkdir()
            profile = Path(temporary) / 'private/default'
            environment_file = Path(temporary) / 'image.json'
            environment_file.write_text(json.dumps({'HOME': '/home/runtime',
                'OAC_RUNTIME_HOME': '/home/runtime/.oac', 'OAC_RUNTIME_WORKSPACE': '/environment/workspace',
                'OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE': '/private/control/fixture.json'}))
            (root / 'bootstrap.json').write_text(json.dumps(PAYLOAD))
            real_chmod = Path.chmod

            def chmod(path, mode):
                if str(path).startswith(temporary):
                    real_chmod(path, mode)

            with patch.object(init, 'ROOT', root), patch.object(init, 'PROFILE', profile), \
                    patch.object(init, 'IMAGE_ENV', environment_file), \
                    patch.object(init.os, 'chown'), patch.object(init.os, 'fchown'), \
                    patch.object(init.os, 'chmod'), patch.object(Path, 'chmod', chmod), \
                    patch.object(init.subprocess, 'run') as run, \
                    patch.object(init.subprocess, 'Popen', return_value=Mock(pid=321)) as popen:
                init.initialize()
                argv = popen.call_args.args[0]
                options = popen.call_args.kwargs
                self.assertEqual(argv[argv.index('--remote') + 1], PAYLOAD['remote_url'])
                self.assertNotIn('test-private-key', repr(popen.call_args))
                self.assertNotIn('OAC_RUNTIME_SESSION_ID', options['env'])
                self.assertNotIn('OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE', options['env'])
                self.assertEqual((options['user'], options['group'], options['extra_groups']), (1000, 1000, []))
                self.assertEqual(options['umask'], 0o077)
                key = profile.parent / 'executor-key.json'
                self.assertEqual(json.loads(key.read_text()), PAYLOAD['executor_key'])
                self.assertEqual(key.stat().st_mode & 0o777, 0o600)
                self.assertFalse((profile / 'auth.json').exists())
                self.assertFalse((root / 'bootstrap.json').exists())
                receipt = json.loads((root / 'ready.json').read_text())
                self.assertEqual(receipt['status'], 'daemon_started')
                self.assertNotIn('session_id', receipt)
                self.assertNotIn('test-private-key', (root / 'launch.json').read_text())
                run.reset_mock()
                with self.assertRaises(RuntimeError):
                    init.initialize()
                run.assert_not_called()
                popen.assert_called_once()

    def test_failed_initialization_retains_claim_without_ready(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'bootstrap.json').write_text(json.dumps(PAYLOAD))
            with patch.object(init, 'ROOT', root), patch.object(init.subprocess, 'run', side_effect=RuntimeError):
                with self.assertRaises(RuntimeError):
                    init.initialize()
                self.assertTrue((root / 'launch.json').exists())
                self.assertFalse((root / 'ready.json').exists())
                with self.assertRaisesRegex(RuntimeError, 'cannot be replayed'):
                    init.initialize()

    def test_sdk_startup_retains_id_and_never_retries_unknown_outcomes(self):
        for fail in ['create', 'start', None]:
            with self.subTest(fail=fail), tempfile.TemporaryDirectory() as temporary:
                record = Path(temporary) / 'launch.json'
                sandbox = Mock(sandbox_id='owned-id')
                sandbox.files.read.return_value = json.dumps(dict(init.launch_identity(PAYLOAD),
                    status='daemon_started', daemon_pid=123))
                if fail == 'start':
                    sandbox.commands.run.side_effect = RuntimeError('test-private-key')
                with patch.object(launch.Sandbox, 'create', return_value=sandbox) as create:
                    if fail == 'create':
                        create.side_effect = RuntimeError('test-private-key')
                    if fail:
                        with self.assertRaisesRegex(RuntimeError, 'outcome uncertain') as raised:
                            launch.launch(copy.deepcopy(PAYLOAD), TEMPLATE, 'e2b-private-key', record)
                        self.assertNotIn('test-private-key', str(raised.exception))
                    else:
                        result = launch.launch(PAYLOAD, TEMPLATE, 'e2b-private-key', record)
                        self.assertEqual(result['status'], 'daemon_started')
                    persisted = json.loads(record.read_text())
                    self.assertNotIn('private-key', record.read_text())
                    self.assertEqual(record.stat().st_mode & 0o777, 0o600)
                    self.assertEqual(persisted.get('sandbox_id'), None if fail == 'create' else 'owned-id')
                    self.assertEqual(create.call_args.kwargs['lifecycle'],
                                     {'on_timeout': 'kill', 'auto_resume': False})
                    self.assertNotIn('private-key', repr(create.call_args.kwargs['metadata']))
                    with self.assertRaises(FileExistsError):
                        launch.launch(PAYLOAD, TEMPLATE, 'e2b-private-key', record)
                    create.assert_called_once()
                    sandbox.kill.assert_not_called()


if __name__ == '__main__':
    unittest.main()
