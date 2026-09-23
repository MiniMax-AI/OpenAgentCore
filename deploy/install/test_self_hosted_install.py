import argparse
import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace

import distribution
import unittest
from unittest.mock import patch
import uuid

import self_hosted_install as installer


class SelfHostedInstallTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / '.parsar/tests/selfhost-install'
        base.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.environment = str(uuid.uuid4())
        self.remote = 'wss://core.example/api/v1/agent-daemon/ws'
        self.key = {'key_id': str(uuid.uuid4()), 'environment_id': self.environment,
                    'executor_token': 'synthetic-private-token'}
        self.source_key = self.root / 'source-key.json'
        installer.write_private(self.source_key, self.key)
        self.args = argparse.Namespace(source_url='https://core.example', offline_root=None,
            environment_id=self.environment, remote=self.remote, credential_file=str(self.source_key))
        self.wait = patch.object(installer, 'wait_connected').start()
        self.addCleanup(patch.stopall)
        self.manifest = {'source_commit': 'a' * 40, 'platform': 'linux/amd64',
                         'images': {'runtime': 'sha256:' + 'b' * 64},
                         'image_manifest_digests': {'runtime': 'sha256:' + 'd' * 64}}
        patch.object(distribution, 'docker_command', side_effect=self.docker_command).start()

    def docker_command(self, arguments, timeout=30):
        try:
            return SimpleNamespace(returncode=0, stdout=installer.checked(arguments, 'image missing', timeout=timeout))
        except installer.InstallError:
            return SimpleNamespace(returncode=1, stdout='')

    def test_exact_restricted_credential_and_tls_identity(self):
        self.assertEqual(installer.credential(json.dumps(self.key), self.environment), self.key)
        for change in ({'environment_id': ''}, {'executor_token': ''}, {'key_id': 'bad'}, {'extra': True}):
            with self.assertRaises(installer.InstallError):
                installer.credential(json.dumps(dict(self.key, **change)), self.environment)
        for remote in ('ws://localhost/api/v1/agent-daemon/ws', self.remote+'?token=secret',
                       'wss://user:secret@core.example/api/v1/agent-daemon/ws', self.remote+'#fragment'):
            with self.assertRaises(installer.InstallError):
                installer.identity(self.environment, remote)

    def test_private_file_rejects_symlink_permissions_and_hardlinks(self):
        self.assertEqual(json.loads(installer.private_read(self.source_key)), self.key)
        self.source_key.chmod(0o644)
        with self.assertRaises(installer.InstallError): installer.private_read(self.source_key)
        self.source_key.chmod(0o600)
        alias = self.root / 'alias'
        alias.symlink_to(self.source_key)
        with self.assertRaises(installer.InstallError): installer.private_read(alias)
        alias.unlink()
        os.link(self.source_key, alias)
        with self.assertRaises(installer.InstallError): installer.private_read(self.source_key)

    def test_one_command_keeps_secrets_out_of_arguments_and_retains_attempt(self):
        commands = []
        def checked(command, message, timeout=30):
            commands.append(command)
            self.assertNotIn(self.key['executor_token'], json.dumps(command))
            if 'inspect' in command: return self.manifest['images']['runtime'] + ' linux/amd64'
            if command[0].endswith('parsar-runtime'):
                self.assertTrue((self.root / 'launch.json').exists())
                return json.dumps({'container': 'parsar-selfhost-'+'c'*32, 'status': 'started'})
            return ''
        with patch.object(installer, 'load_manifest', return_value=self.manifest), \
                patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                patch.object(installer, 'runtime_archive', return_value=self.root/'runtime.tar'), \
                patch.object(installer, 'checked', side_effect=checked), contextlib.redirect_stdout(io.StringIO()):
            installer.install(self.args, self.root)
            self.assertEqual(len(commands), 2)
            self.assertEqual(json.loads(installer.private_read(self.root/'executor-key.json')), self.key)
            with self.assertRaises(installer.InstallError): installer.install(self.args, self.root)
            self.assertEqual(len(commands), 3)

    def test_connection_failure_retry_preserves_same_container_and_credential(self):
        commands = []
        name = 'parsar-selfhost-'+'c'*32
        def checked(command, message, timeout=30):
            commands.append(command)
            if 'container' in command:
                state = json.loads(installer.private_read(self.root/'installation.json'))
                labels = {'io.parsar.agents-api.installation': state['installation_id'],
                          'io.parsar.agents-api.environment': self.environment,
                          'io.parsar.agents-api.user-owned': 'true'}
                return json.dumps(labels)+' running '+state['runtime_image']
            if 'inspect' in command: return self.manifest['images']['runtime'] + ' linux/amd64'
            return json.dumps({'container': name, 'status': 'started'})
        self.wait.side_effect = [installer.InstallError('connection timed out'), None]
        with patch.object(installer, 'load_manifest', return_value=self.manifest), \
                patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                patch.object(installer, 'checked', side_effect=checked), contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(installer.InstallError, 'timed out'):
                installer.install(self.args, self.root)
            original = {name: (self.root/name).read_bytes() for name in ('installation.json', 'launch.json', 'started.json', 'executor-key.json')}
            installer.install(self.args, self.root)
        self.assertEqual(len([c for c in commands if c[0].endswith('parsar-runtime')]), 1)
        self.assertEqual(self.wait.call_count, 2)
        self.wait.assert_called_with(self.remote, self.environment, self.key, name)
        self.assertEqual(original, {name: (self.root/name).read_bytes() for name in original})

    def test_cached_runtime_avoids_archive_download_and_load(self):
        def checked(command, message, timeout=30):
            self.assertNotIn('load', command)
            if 'inspect' in command: return self.manifest['images']['runtime'] + ' linux/amd64'
            return json.dumps({'container': 'parsar-selfhost-'+'c'*32, 'status': 'started'})
        with patch.object(installer, 'load_manifest', return_value=self.manifest), \
                patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                patch.object(installer, 'runtime_archive') as archive, \
                patch.object(installer, 'checked', side_effect=checked), contextlib.redirect_stdout(io.StringIO()):
            installer.install(self.args, self.root)
            archive.assert_not_called()

    def test_missing_runtime_loads_and_verifies_before_launch(self):
        commands = []
        def checked(command, message, timeout=30):
            commands.append(command)
            if 'inspect' in command and not any('load' in item for item in commands):
                raise installer.InstallError('image missing')
            if 'inspect' in command: return self.manifest['images']['runtime'] + ' linux/amd64'
            if command[0].endswith('parsar-runtime'):
                return json.dumps({'container': 'parsar-selfhost-'+'c'*32, 'status': 'started'})
            return ''
        with patch.object(installer, 'load_manifest', return_value=self.manifest), \
                patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                patch.object(installer, 'runtime_archive', return_value=self.root/'runtime.tar') as archive, \
                patch.object(installer, 'checked', side_effect=checked), contextlib.redirect_stdout(io.StringIO()):
            installer.install(self.args, self.root)
            archive.assert_called_once()
            self.assertIn('load', commands[2])
            self.assertIn('inspect', commands[3])
            self.assertTrue(commands[4][0].endswith('parsar-runtime'))

    def test_containerd_cache_passes_actual_id_to_launcher_without_changing_manifest(self):
        expected = self.manifest['image_manifest_digests']['runtime']
        published = json.dumps(self.manifest, sort_keys=True)
        def checked(command, message, timeout=30):
            if 'inspect' in command:
                if command[command.index('inspect') + 1] == self.manifest['images']['runtime']:
                    raise installer.InstallError('image missing')
                return expected + ' linux/amd64'
            self.assertEqual(command[command.index('--image') + 1], expected)
            return json.dumps({'container': 'parsar-selfhost-'+'c'*32, 'status': 'started'})
        with patch.object(installer, 'load_manifest', return_value=self.manifest), \
                patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                patch.object(installer, 'runtime_archive', side_effect=AssertionError('warm Runtime download')), \
                patch.object(installer, 'checked', side_effect=checked), contextlib.redirect_stdout(io.StringIO()):
            installer.install(self.args, self.root)
        self.assertEqual(json.dumps(self.manifest, sort_keys=True), published)
        state = json.loads(installer.private_read(self.root/'installation.json'))
        self.assertEqual(state['runtime_image'], self.manifest['images']['runtime'])
        self.assertEqual(state['runtime_manifest'], expected)

    def test_wrong_loaded_id_or_platform_prevents_launch_receipt(self):
        for observed in ('sha256:' + 'f' * 64 + ' linux/amd64', self.manifest['images']['runtime'] + ' linux/arm64'):
            loaded = False
            def checked(command, message, timeout=30):
                nonlocal loaded
                if 'load' in command:
                    loaded = True
                    return ''
                if 'inspect' in command:
                    if not loaded:
                        raise installer.InstallError('image missing')
                    return observed
                self.fail('Runtime launcher must not run')
            with self.subTest(observed=observed), patch.object(installer, 'load_manifest', return_value=self.manifest), \
                    patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                    patch.object(installer, 'runtime_archive', return_value=self.root/'runtime.tar'), \
                    patch.object(installer, 'checked', side_effect=checked), \
                    self.assertRaisesRegex(distribution.DistributionError, 'identity or platform'):
                installer.install(self.args, self.root)
            self.assertFalse((self.root/'launch.json').exists())

    def test_existing_launch_verifies_labels_and_reports_state(self):
        name = 'parsar-selfhost-'+'c'*32
        state = {'installation_id': str(uuid.uuid4()), 'environment_id': self.environment,
                 'runtime_image': self.manifest['images']['runtime'],
                 'runtime_manifest': self.manifest['image_manifest_digests']['runtime']}
        labels = {'io.parsar.agents-api.installation': state['installation_id'],
                  'io.parsar.agents-api.environment': self.environment,
                  'io.parsar.agents-api.user-owned': 'true'}
        installer.write_private(self.root/'started.json', {'container': name, 'status': 'started'})
        with patch.object(installer, 'checked', return_value=json.dumps(labels)+' running '+state['runtime_image']), \
                contextlib.redirect_stdout(io.StringIO()) as output:
            installer.inspect_prior_launch(self.root, state)
            self.assertIn('already running', output.getvalue())
            self.assertNotIn('connected to Environment', output.getvalue())
        with patch.object(installer, 'checked', return_value=json.dumps(labels)+' running '+state['runtime_manifest']), \
                contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(installer.inspect_prior_launch(self.root, state), name)
        with patch.object(installer, 'checked', return_value=json.dumps(labels)+' exited '+state['runtime_image']):
            with self.assertRaisesRegex(installer.InstallError, 'start '+name):
                installer.inspect_prior_launch(self.root, state)
        with patch.object(installer, 'checked', return_value=json.dumps(labels)+' running sha256:'+'f'*64):
            with self.assertRaisesRegex(installer.InstallError, 'container image does not match'):
                installer.inspect_prior_launch(self.root, state)
        labels['io.parsar.agents-api.environment'] = str(uuid.uuid4())
        with patch.object(installer, 'checked', return_value=json.dumps(labels)+' running '+state['runtime_image']):
            with self.assertRaisesRegex(installer.InstallError, 'does not match'):
                installer.inspect_prior_launch(self.root, state)

    def test_uncertain_launch_has_specific_safe_inspection_commands(self):
        state = {'installation_id': str(uuid.uuid4()), 'environment_id': self.environment,
                 'runtime_image': self.manifest['images']['runtime'],
                 'runtime_manifest': self.manifest['image_manifest_digests']['runtime']}
        with self.assertRaises(installer.InstallError) as failure:
            installer.inspect_prior_launch(self.root, state)
        message = str(failure.exception)
        self.assertIn('ps -a --filter label=io.parsar.agents-api.installation='+state['installation_id'], message)
        self.assertIn('volume ls --filter', message)
        self.assertIn('Do not delete the receipt', message)

    def test_failed_launch_is_not_replayed(self):
        def checked(command, message, timeout=30):
            if 'inspect' in command: return self.manifest['images']['runtime'] + ' linux/amd64'
            if command[0].endswith('parsar-runtime'): raise installer.InstallError('uncertain launch')
            return ''
        with patch.object(installer, 'load_manifest', return_value=self.manifest), \
                patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                patch.object(installer, 'runtime_archive', return_value=self.root/'runtime.tar'), \
                patch.object(installer, 'checked', side_effect=checked):
            with self.assertRaises(installer.InstallError): installer.install(self.args, self.root)
            self.assertTrue((self.root/'launch.json').exists())
            with self.assertRaisesRegex(installer.InstallError, 'already attempted'): installer.install(self.args, self.root)


if __name__ == '__main__':
    unittest.main()
