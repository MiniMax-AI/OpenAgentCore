"""Qualify the self-contained Compose initializer without building images."""

import base64
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import uuid


ROOT = Path(__file__).resolve().parents[2]
COMPOSE = ROOT / 'deploy/compose/compose.yaml'


class ComposeTests(unittest.TestCase):
    @classmethod
    def render(cls, public_url=None):
        env = dict(os.environ)
        env.pop('OAC_PUBLIC_URL', None)
        if public_url is not None:
            env['OAC_PUBLIC_URL'] = public_url
        return json.loads(subprocess.check_output(
            ['docker', 'compose', '--env-file', os.devnull, '-f', str(COMPOSE),
             '--profile', 'tools', 'config', '--format', 'json'], env=env))

    @classmethod
    def setUpClass(cls):
        cls.compose = cls.render()

    def setUp(self):
        base = Path.home() / '.oac/tests/compose'
        base.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / 'database-data').mkdir()
        self.code = {'__name__': 'compose_initializer'}
        exec(self.compose['configs']['init-script']['content'], self.code)
        self.files = {name: b'fixture' for name in self.code['MEMBERS']}
        self.files['manifest.json'] = json.dumps({'source_commit': self.code['REVISION'], 'platform': 'linux/amd64'}).encode()
        chown = patch('os.chown')
        chown.start()
        self.addCleanup(chown.stop)

    def initialize(self, fetch=None):
        self.code['initialize'](self.root, fetch or (lambda: self.files))

    def test_fresh_installation_and_restart_keep_identity_and_keys(self):
        self.initialize()
        saved = {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        key = saved['web/core.key'].strip()
        self.assertEqual(len(key), 64)
        self.assertEqual(json.loads(saved['core/core-key-digests.json']), [hashlib.sha256(key).hexdigest()])
        self.assertEqual(len(base64.b64decode(saved['core/credential.key'])), 32)
        uuid.UUID(saved['core/installation.id'].decode().strip())
        for name in ('web/core.key', 'database/password', 'core/credential.key'):
            self.assertEqual((self.root / name).stat().st_mode & 0o777, 0o600)
        self.initialize(lambda: self.fail('A completed installation must not download again'))
        self.assertEqual(saved, {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob('*') if p.is_file()})

    def test_existing_data_without_installation_secrets_is_refused(self):
        (self.root / 'database-data/PG_VERSION').write_text('16')
        with self.assertRaisesRegex(RuntimeError, 'original installation'):
            self.initialize(lambda: self.fail('Must refuse before downloading'))
        self.assertFalse((self.root / 'web/core.key').exists())

    def test_changed_or_missing_keys_and_another_release_are_refused(self):
        self.initialize()
        path = self.root / 'database/password'
        original = path.read_bytes()
        path.write_bytes(b'different')
        with self.assertRaisesRegex(RuntimeError, 'files changed'):
            self.initialize()
        path.unlink()
        with self.assertRaises(FileNotFoundError):
            self.initialize()
        path.write_bytes(original)
        self.code['REVISION'] = 'f' * 40
        with self.assertRaisesRegex(RuntimeError, 'another release'):
            self.initialize()

    def test_interrupted_fresh_initialization_retains_generated_keys(self):
        self.initialize()
        key = (self.root / 'web/core.key').read_bytes()
        (self.root / 'core/installation.json').unlink()
        self.initialize()
        self.assertEqual(key, (self.root / 'web/core.key').read_bytes())

    def archive(self):
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode='w:gz') as archive:
            for name, data in self.files.items():
                info = tarfile.TarInfo(self.code['ARCHIVE'] + '/' + name)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
            data = b'ignored image data' * 1000
            info = tarfile.TarInfo(self.code['ARCHIVE'] + '/images/core.tar')
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
        return output.getvalue()

    def test_streamed_release_is_fully_verified_and_only_metadata_is_retained(self):
        data = self.archive()
        self.code['CHECKSUM'] = hashlib.sha256(data).hexdigest()
        with patch('urllib.request.urlopen', return_value=io.BytesIO(data)):
            self.assertEqual(self.code['download'](), self.files)
        self.code['CHECKSUM'] = '0' * 64
        with patch('urllib.request.urlopen', return_value=io.BytesIO(data)):
            with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                self.code['download']()
        del self.files['node-install.pyz']
        data = self.archive()
        self.code['CHECKSUM'] = hashlib.sha256(data).hexdigest()
        with patch('urllib.request.urlopen', return_value=io.BytesIO(data)):
            with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                self.code['download']()

    def test_compose_uses_private_services_and_ordered_initialization(self):
        services = self.compose['services']
        self.assertEqual(services['database']['depends_on']['init']['condition'], 'service_completed_successfully')
        self.assertEqual(services['migrate']['depends_on']['database']['condition'], 'service_healthy')
        self.assertIn('pg_isready -h 127.0.0.1', services['database']['healthcheck']['test'][1])
        self.assertEqual(services['core']['depends_on']['migrate']['condition'], 'service_completed_successfully')
        for service in services.values():
            self.assertNotIn('build', service)
            self.assertNotIn('ports', service)
            self.assertIn('@sha256:', service['image'])
            for volume in service.get('volumes', []):
                self.assertNotIn('docker.sock', volume['source'])
                self.assertEqual(volume['type'], 'volume')
        self.assertEqual({v['source'] for v in services['web']['volumes']}, {'web-secret', 'node-payload'})
        self.assertEqual(services['credentials']['logging']['driver'], 'none')
        schema = json.loads((ROOT / 'deploy/install/config.schema.json').read_text())
        harnesses = schema['properties']['core']['properties']['harnesses']['default']
        self.assertEqual(services['core']['environment']['OAC_HARNESSES'].split(','), harnesses)

    def test_public_url_can_be_configured_after_initial_startup(self):
        for value in (None, '', 'https://oac.example.test', 'http://localhost:9080'):
            with self.subTest(public_url=value):
                configured = self.render(value)
                expected = value or 'http://localhost:8080'
                for name, setting in (('core', 'OAC_PUBLIC_URL'), ('migrate', 'OAC_PUBLIC_URL'),
                                      ('web', 'OAC_WEB_ORIGIN')):
                    self.assertEqual(configured['services'][name]['environment'][setting], expected)
                self.assertEqual(configured['volumes'], self.compose['volumes'])

    def test_platform_network_injection_keeps_the_credentials_profile_valid(self):
        # Dokploy isolated deployments attach a project network to every service.
        transformed = copy.deepcopy(self.compose)
        transformed['networks']['platform'] = {}
        for service in transformed['services'].values():
            service.setdefault('networks', {})['platform'] = None
        subprocess.run(
            ['docker', 'compose', '-f', '-', '--profile', 'tools', 'config', '--quiet'],
            input=json.dumps(transformed), text=True, check=True)


if __name__ == '__main__':
    unittest.main()
