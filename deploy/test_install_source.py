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
        self.root = Path(self.temp.name)
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

    def install(self):
        with patch('distribution.ensure_docker_image', return_value='sha256:' + '1' * 64), patch.object(installer, 'run', side_effect=lambda *a, **k: self.calls.append((a, k))):
            installer.install(self.bundle, self.destination, '127.0.0.1', 19873, 'http://127.0.0.1:19873')

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
        with self.assertRaisesRegex(ValueError, 'not empty'):
            self.install()
        self.assertEqual((self.destination / 'keep').read_text(), 'existing')
        self.assertEqual(self.calls, [])

    def test_thin_bundle_rejected_before_creating_installation(self):
        self.manifest['artifacts'] = {'native/bin/node': {'filename': 'missing'}}
        (self.bundle / 'manifest.json').write_text(json.dumps(self.manifest))
        self.write_sums()
        with self.assertRaisesRegex(ValueError, 'offline distribution'):
            self.install()
        self.assertFalse(self.destination.exists())


if __name__ == '__main__':
    unittest.main()
