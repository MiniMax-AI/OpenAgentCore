"""Source installation uses production publication and real Compose merging."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'deploy/node'))
spec = importlib.util.spec_from_file_location('install_source', ROOT / 'deploy/install-source.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class SourceInstallTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.bundle, self.destination = self.root / 'bundle', self.root / 'installation'
        self.bundle.mkdir()
        self.revision = 'a' * 40
        self.manifest = {'source_commit': self.revision, 'platform': 'linux/amd64', 'artifacts': {}}
        for name, data in [('manifest.json', json.dumps(self.manifest).encode()), ('node-install.pyz', b'installer'), ('runtime/seccomp.json', b'{}')]:
            target = self.bundle / name
            target.parent.mkdir(exist_ok=True)
            target.write_bytes(data)
        self.write_sums()
        shutil.copyfile(ROOT / 'deploy/compose/compose.yaml', self.bundle / 'compose.yaml.template')
        (self.bundle / 'oac').write_bytes(b'operator')
        self.calls = []

    def write_sums(self):
        (self.bundle / 'SHA256SUMS').write_text(hashlib.sha256((self.bundle / 'manifest.json').read_bytes()).hexdigest() + '  manifest.json\n')

    def install(self, upgrade=False, image='sha256:' + '1' * 64, failure=None):
        def run(message, *args, **kwargs):
            self.calls.append((args, kwargs))
            if failure and failure in args:
                raise ValueError(message)
        with patch.object(installer, 'ensure_docker_image', return_value=image), patch.object(installer, 'run', side_effect=run):
            installer.install(self.bundle, self.destination, '127.0.0.1', 19873, None if upgrade else 'http://127.0.0.1:19873', upgrade)

    def main(self):
        """Run the command line and return its exit message."""
        argv = ['install.py', '--install-dir', str(self.destination), '--public-url', 'http://127.0.0.1:19873']
        with patch.object(sys, 'argv', argv), patch.object(installer, 'BUNDLE', self.bundle), self.assertRaises(SystemExit) as exit:
            installer.main()
        return exit.exception.code

    def commands(self):
        """Compose subcommands, without the staged file arguments."""
        return [args[args.index('--project-directory') + 2:] if '--project-directory' in args else args[2:] for args, _ in self.calls]

    def snapshot(self):
        return {name: (self.destination / name).read_bytes() for name in ('compose.yaml', 'compose.override.yaml', '.env', 'oac', 'node-payload/active.json')}

    def test_complete_compose_preserves_secrets_and_serves_whole_payload(self):
        self.install()
        self.assertNotIn('__OAC_', (self.destination / 'compose.yaml').read_text())
        config = json.loads(subprocess.check_output(['docker', 'compose', 'config', '--format', 'json'], cwd=self.destination, text=True))
        services = config['services']
        for name in ('core', 'web', 'database', 'init'):
            self.assertEqual(services[name]['image'], 'sha256:' + '1' * 64)
        mounts = {v['target']: v for v in services['web']['volumes']}
        self.assertEqual(mounts['/run/oac']['volume']['subpath'], 'secrets/web')
        self.assertEqual(mounts['/node-payload']['type'], 'bind')
        self.assertNotIn('volume', mounts['/node-payload'])
        self.assertTrue(mounts['/node-payload']['read_only'])
        self.assertEqual(services['init']['environment']['OAC_REVISION'], self.revision)
        self.assertEqual(services['web']['environment']['OAC_PUBLIC_URL'], 'http://127.0.0.1:19873')
        self.assertEqual(json.loads((self.destination / 'node-payload/active.json').read_text()), {'source_commit': self.revision})
        self.assertTrue((self.destination / 'node-payload/releases' / self.revision / 'node-install.pyz').is_file())
        self.assertEqual(os.stat(self.destination / '.env').st_mode & 0o777, 0o600)
        self.assertIn('--pull', self.calls[-2][0])
        self.assertIn('never', self.calls[-2][0])

    def test_existing_installation_is_not_replaced(self):
        self.destination.mkdir()
        (self.destination / 'keep').write_text('existing')
        with self.assertRaisesRegex(ValueError, 'Incomplete installation'):
            self.install()
        self.assertEqual((self.destination / 'keep').read_text(), 'existing')
        self.assertEqual(self.calls, [])

    def test_failed_first_install_leaves_nothing_and_reruns(self):
        with self.assertRaisesRegex(ValueError, '^Compose configuration check failed; see the output above. Nothing was installed.$'):
            self.install(failure='config')
        self.assertFalse(self.destination.exists())
        # A killed run leaves only its stage, which the rerun removes.
        (self.destination / '.staging-killed/node-payload').mkdir(parents=True)
        self.install()
        self.assertEqual(sorted(path.name for path in self.destination.iterdir()), ['.env', 'compose.override.yaml', 'compose.yaml', 'node-payload', 'oac'])

    def test_same_release_reruns_init_before_the_configuration_check(self):
        # A first start that stopped before init left no secrets for check-config.
        self.install()
        self.calls = []
        self.install()
        self.assertEqual([command[-1] for command in self.commands()], ['init', 'check-config', '--remove-orphans'])
        with self.assertRaisesRegex(ValueError, '^Data volume check failed; no service was changed. See the output above.$'):
            self.install(failure='init')

    def test_upgrade_checks_the_new_release_before_replacing_it_and_keeps_operator_entries(self):
        self.install()
        override = json.loads((self.destination / 'compose.override.yaml').read_text())
        nfs = {'type': 'bind', 'source': '/srv/workspaces', 'target': '/srv/workspaces'}
        override['services']['core']['volumes'] = [nfs]
        (self.destination / 'compose.override.yaml').write_text(json.dumps(override))
        before = self.snapshot()
        upgraded = 'b' * 40
        self.manifest['source_commit'] = upgraded
        (self.bundle / 'manifest.json').write_text(json.dumps(self.manifest))
        self.write_sums()
        (self.bundle / 'oac').write_bytes(b'upgraded operator')

        self.calls = []
        with self.assertRaisesRegex(ValueError, 'rerun with --upgrade'):
            self.install(image='sha256:' + '2' * 64)
        self.assertEqual(self.snapshot(), before)
        with self.assertRaisesRegex(ValueError, '^Upgrade stopped; the running services are unchanged. Data volume check failed; see the output above.$'):
            self.install(upgrade=True, image='sha256:' + '2' * 64, failure='init')
        self.assertEqual(self.snapshot(), before)

        self.calls = []
        self.install(upgrade=True, image='sha256:' + '2' * 64)
        self.assertEqual([command[-1] for command in self.commands()], ['check-config', 'init', '--remove-orphans'])
        services = json.loads((self.destination / 'compose.override.yaml').read_text())['services']
        self.assertEqual(services['core']['volumes'], [nfs])
        self.assertEqual(os.stat(self.destination / 'compose.override.yaml').st_mode & 0o777, 0o600)
        self.assertEqual({services[name]['image'] for name in ('core', 'web', 'init', 'database')}, {'sha256:' + '2' * 64})
        self.assertEqual([volume['target'] for volume in services['web']['volumes']], ['/node-payload'])
        self.assertIn('OAC_REVISION: ' + upgraded, (self.destination / 'compose.yaml').read_text())
        self.assertEqual((self.destination / 'oac').read_bytes(), b'upgraded operator')
        self.assertEqual((self.destination / '.env').read_bytes(), before['.env'])
        payload = self.destination / 'node-payload'
        self.assertEqual(json.loads((payload / 'active.json').read_text()), {'source_commit': upgraded})
        self.assertEqual(sorted(path.name for path in (payload / 'releases').iterdir()), [self.revision, upgraded])

    def test_thin_bundle_rejected_before_creating_installation(self):
        self.manifest['artifacts'] = {'native/bin/node': {'filename': 'missing'}}
        (self.bundle / 'manifest.json').write_text(json.dumps(self.manifest))
        self.write_sums()
        with self.assertRaisesRegex(ValueError, 'offline distribution'):
            self.install()
        self.assertFalse(self.destination.exists())

    def test_failures_exit_with_one_line_and_the_next_step(self):
        (self.bundle / 'SHA256SUMS').write_text('0' * 64 + '  manifest.json\n')
        self.assertEqual(self.main(), 'Distribution manifest checksum mismatch')
        self.write_sums()

        def interrupt(*args, **kwargs):
            raise KeyboardInterrupt
        with patch.object(installer, 'ensure_docker_image', return_value='sha256:' + '1' * 64), patch.object(installer, 'run', side_effect=interrupt):
            self.assertEqual(self.main(), 'Interrupted; rerun the same command to continue.')
        self.assertFalse(self.destination.exists())

        if os.geteuid():
            # Root-owned after an earlier sudo run; root itself is never denied.
            lock = Path(str(self.destination) + '.lock')
            lock.chmod(0)
            self.addCleanup(lock.chmod, 0o700)
            self.assertEqual(self.main(), f'{lock} is not writable by this account; if an earlier run used sudo, return it with: sudo chown -R "$(id -un)" {lock}')


if __name__ == '__main__':
    unittest.main()
