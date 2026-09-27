import argparse
import contextlib
import io
import json
import os
from pathlib import Path
import pty
import tempfile
import termios
from types import SimpleNamespace

import distribution
import unittest
from unittest.mock import patch
import uuid

import self_hosted_install as installer


class SelfHostedInstallTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / '.oac/tests/selfhost-install'
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
        self.verdict = patch.object(installer, 'credential_verdict', return_value='accepted').start()
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
            if command[0].endswith('oac-selfhost'):
                self.assertTrue((self.root / 'launch.json').exists())
                return json.dumps({'container': 'oac-selfhost-'+'c'*32, 'status': 'started'})
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
        name = 'oac-selfhost-'+'c'*32
        def checked(command, message, timeout=30):
            commands.append(command)
            if 'container' in command:
                state = json.loads(installer.private_read(self.root/'installation.json'))
                labels = {'io.oac.installation': state['installation_id'],
                          'io.oac.environment': self.environment,
                          'io.oac.user-owned': 'true'}
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
        self.assertEqual(len([c for c in commands if c[0].endswith('oac-selfhost')]), 1)
        self.assertEqual(self.wait.call_count, 2)
        self.wait.assert_called_with(self.remote, self.environment, self.key, name)
        self.assertEqual(original, {name: (self.root/name).read_bytes() for name in original})

    def test_hidden_prompt_reads_pretty_or_compact_credential_without_echo(self):
        for text in (json.dumps(self.key, indent=2) + '\n', json.dumps(self.key) + '\n'):
            pid, terminal = pty.fork()
            if pid == 0:
                try:
                    accepted = installer.prompt_credential(self.environment) == self.key
                    os._exit(0 if accepted and termios.tcgetattr(0)[3] & termios.ECHO else 3)
                except BaseException:
                    os._exit(4)
            output = b''
            while b'input hidden' not in output:
                output += os.read(terminal, 1024)
            os.write(terminal, text.encode())
            while True:
                try:
                    chunk = os.read(terminal, 1024)
                except OSError:
                    break
                if not chunk:
                    break
                output += chunk
            os.close(terminal)
            self.assertEqual(os.waitstatus_to_exitcode(os.waitpid(pid, 0)[1]), 0)
            self.assertNotIn(self.key['executor_token'].encode(), output)

    def launched_runtime(self):
        """Launch with self.key, then rerun without --credential-file; docker commands in `commands`."""
        name = 'oac-selfhost-' + 'c' * 32
        commands, interrupt = [], []
        def checked(command, message, timeout=30):
            commands.append(command)
            self.assertNotIn('private-token', json.dumps(command))
            if interrupt and interrupt[0] in command:
                interrupt.pop(0)
                raise installer.InstallError(message)
            if 'container' in command:
                state = json.loads(installer.private_read(self.root/'installation.json'))
                labels = {'io.oac.installation': state['installation_id'],
                          'io.oac.environment': self.environment,
                          'io.oac.user-owned': 'true'}
                return json.dumps(labels)+' running '+state['runtime_image']
            if 'inspect' in command: return self.manifest['images']['runtime'] + ' linux/amd64'
            if command[1:2] == ['replace-credential']:
                self.assertEqual(command[2:4], ['--container', name])
            return json.dumps({'container': name, 'status': 'started'})
        patch.object(installer, 'load_manifest', return_value=self.manifest).start()
        patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest).start()
        patch.object(installer, 'runtime_archive', return_value=self.root/'runtime.tar').start()
        patch.object(installer, 'checked', side_effect=checked).start()
        with contextlib.redirect_stdout(io.StringIO()):
            installer.install(self.args, self.root)
        self.args.credential_file = None
        return name, commands, interrupt

    def stored(self):
        return json.loads(installer.private_read(self.root/'executor-key.json'))

    def rerun(self, pasted):
        with patch.object(installer, 'prompt_credential', return_value=pasted), contextlib.redirect_stdout(io.StringIO()) as output:
            installer.install(self.args, self.root)
        return output.getvalue()

    def test_revoked_credential_is_replaced_only_by_the_same_key_in_the_same_container(self):
        name, commands, _ = self.launched_runtime()
        rotated = dict(self.key, executor_token='rotated-private-token')
        issued = dict(self.key, key_id=str(uuid.uuid4()), executor_token='issued-private-token')
        # Core's connection check accepts a new key while the revoked bound key has no authority.
        verdicts = {self.key['executor_token']: 401, rotated['executor_token']: 'accepted', issued['executor_token']: 'accepted'}
        self.verdict.side_effect = lambda remote, environment, key: verdicts[key['executor_token']]
        retained = {n: (self.root/n).read_bytes() for n in ('installation.json', 'launch.json', 'started.json')}
        launched = len(commands)
        with self.assertRaisesRegex(installer.InstallError, 'not ' + self.key['key_id'] + '.*same credential'):
            self.rerun(issued)
        self.assertFalse([c for c in commands[launched:] if 'stop' in c or c[0].endswith('oac-selfhost')])
        self.assertEqual(self.stored(), self.key)
        output = self.rerun(rotated)
        replaced = [c for c in commands[launched:] if 'inspect' not in c]
        self.assertEqual([(c[-2], c[-1]) if c[0] == 'docker' else c[1] for c in replaced],
                         [('stop', name), 'replace-credential', ('start', name)])
        self.assertEqual(len([c for c in commands if c[0].endswith('oac-selfhost') and c[1] != 'replace-credential']), 1)
        self.assertEqual(self.stored(), rotated)
        self.assertEqual(retained, {n: (self.root/n).read_bytes() for n in retained})
        self.assertFalse(list(self.root.glob('.executor-key-*')))
        self.wait.assert_called_with(self.remote, self.environment, rotated, name)
        self.assertIn('no longer accepted', output)
        self.assertNotIn('private-token', output)

    def test_binding_conflict_asks_for_the_credential_first_used(self):
        self.launched_runtime()
        self.verdict.side_effect = lambda remote, environment, key: 409
        other = dict(self.key, key_id=str(uuid.uuid4()), executor_token='other-private-token')
        with patch.object(installer, 'prompt_credential', return_value=other), contextlib.redirect_stdout(io.StringIO()) as output, \
                self.assertRaisesRegex(installer.InstallError, 'first used for this Environment') as failure:
            installer.install(self.args, self.root)
        self.assertIn('first used for this Environment', output.getvalue())
        self.assertNotIn('same credential', output.getvalue() + str(failure.exception))
        self.assertEqual(self.stored(), self.key)

    def test_replacement_interrupted_before_start_or_store_is_finished_by_rerunning(self):
        rotated = dict(self.key, executor_token='rotated-private-token')
        name, commands, interrupt = self.launched_runtime()
        verdicts = {self.key['executor_token']: 401, rotated['executor_token']: 'accepted'}
        self.verdict.side_effect = lambda remote, environment, key: verdicts[key['executor_token']]
        replace = os.replace
        def store_interrupted(source, target):
            if str(target).endswith('executor-key.json'):
                raise OSError('interrupted')
            return replace(source, target)
        for point in ('start', 'store'):
            interrupt[:] = ['start'] if point == 'start' else []
            with self.subTest(point=point), self.assertRaises((installer.InstallError, OSError)), \
                    patch.object(installer.os, 'replace', side_effect=store_interrupted if point == 'store' else replace):
                self.rerun(rotated)
            self.assertEqual(commands[-1][-2:], ['start', name])
            self.assertEqual(self.stored(), self.key)
            self.assertFalse(list(self.root.glob('.executor-key-*')))
        launched = len(commands)
        self.rerun(rotated)
        self.assertEqual([c[3] if c[0] == 'docker' else c[1] for c in commands[launched:] if 'inspect' not in c],
                         ['stop', 'replace-credential', 'start'])
        self.assertEqual(self.stored(), rotated)
        self.wait.assert_called_with(self.remote, self.environment, rotated, name)

    def test_cached_runtime_avoids_archive_download_and_load(self):
        def checked(command, message, timeout=30):
            self.assertNotIn('load', command)
            if 'inspect' in command: return self.manifest['images']['runtime'] + ' linux/amd64'
            return json.dumps({'container': 'oac-selfhost-'+'c'*32, 'status': 'started'})
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
            if command[0].endswith('oac-selfhost'):
                return json.dumps({'container': 'oac-selfhost-'+'c'*32, 'status': 'started'})
            return ''
        with patch.object(installer, 'load_manifest', return_value=self.manifest), \
                patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                patch.object(installer, 'runtime_archive', return_value=self.root/'runtime.tar') as archive, \
                patch.object(installer, 'checked', side_effect=checked), contextlib.redirect_stdout(io.StringIO()):
            installer.install(self.args, self.root)
            archive.assert_called_once()
            self.assertIn('load', commands[2])
            self.assertIn('inspect', commands[3])
            self.assertTrue(commands[4][0].endswith('oac-selfhost'))

    def test_containerd_cache_passes_actual_id_to_launcher_without_changing_manifest(self):
        expected = self.manifest['image_manifest_digests']['runtime']
        published = json.dumps(self.manifest, sort_keys=True)
        def checked(command, message, timeout=30):
            if 'inspect' in command:
                if command[command.index('inspect') + 1] == self.manifest['images']['runtime']:
                    raise installer.InstallError('image missing')
                return expected + ' linux/amd64'
            self.assertEqual(command[command.index('--image') + 1], expected)
            return json.dumps({'container': 'oac-selfhost-'+'c'*32, 'status': 'started'})
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
        name = 'oac-selfhost-'+'c'*32
        state = {'installation_id': str(uuid.uuid4()), 'environment_id': self.environment,
                 'runtime_image': self.manifest['images']['runtime'],
                 'runtime_manifest': self.manifest['image_manifest_digests']['runtime']}
        labels = {'io.oac.installation': state['installation_id'],
                  'io.oac.environment': self.environment,
                  'io.oac.user-owned': 'true'}
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
        for status in ('exited', 'restarting', 'created'):
            with patch.object(installer, 'checked', return_value=json.dumps(labels)+' '+status+' '+state['runtime_image']):
                self.assertEqual(installer.inspect_prior_launch(self.root, state, replacing=True), name)
        with patch.object(installer, 'checked', return_value=json.dumps(labels)+' running sha256:'+'f'*64):
            with self.assertRaisesRegex(installer.InstallError, 'container image does not match'):
                installer.inspect_prior_launch(self.root, state)
        labels['io.oac.environment'] = str(uuid.uuid4())
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
        self.assertIn('ps -a --filter label=io.oac.installation='+state['installation_id'], message)
        self.assertIn('volume ls --filter', message)
        self.assertIn('Do not delete the receipt', message)

    def test_failed_launch_is_not_replayed(self):
        def checked(command, message, timeout=30):
            if 'inspect' in command: return self.manifest['images']['runtime'] + ' linux/amd64'
            if command[0].endswith('oac-selfhost'): raise installer.InstallError('uncertain launch')
            return ''
        with patch.object(installer, 'load_manifest', return_value=self.manifest), \
                patch.object(installer, 'obtain_artifact', side_effect=lambda m, n, dest, o: dest), \
                patch.object(installer, 'runtime_archive', return_value=self.root/'runtime.tar'), \
                patch.object(installer, 'checked', side_effect=checked):
            with self.assertRaises(installer.InstallError): installer.install(self.args, self.root)
            self.assertTrue((self.root/'launch.json').exists())
            with self.assertRaisesRegex(installer.InstallError, 'already attempted'): installer.install(self.args, self.root)




class LegacySelfHostedTests(unittest.TestCase):
    def test_same_environment_refuses_even_with_custom_install_dir(self):
        base = Path.home() / '.oac/tests/selfhost-legacy'
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=base) as temporary:
            home = Path(temporary)
            environment = str(uuid.uuid4())
            legacy = home / '.parsar/self-hosted' / environment
            legacy.mkdir(parents=True)
            retained = legacy / 'executor-key.json'
            retained.write_text('private-retained-key')
            destination = home / 'custom-new-root'
            argv = ['self-hosted-install.pyz', '--source-url', 'https://core.example', '--environment-id', environment,
                    '--remote', 'wss://core.example/api/v1/agent-daemon/ws', '--install-dir', str(destination)]
            with patch.object(installer.Path, 'home', return_value=home), patch.object(installer, 'preflight'), \
                    patch.object(installer.sys, 'argv', argv), patch.object(installer, 'install') as install:
                with self.assertRaisesRegex(installer.InstallError, 'before the OpenAgentCore rename.*volumes are not reused') as error:
                    installer.main()
                self.assertNotIn('private-retained-key', str(error.exception))
                install.assert_not_called()
                self.assertFalse(destination.exists())
                self.assertEqual(retained.read_text(), 'private-retained-key')
                installer.refuse_legacy_executor(str(uuid.uuid4()))


if __name__ == '__main__':
    unittest.main()
