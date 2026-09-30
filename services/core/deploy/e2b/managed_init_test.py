"""Managed bootstrap reuses image protection without changing self-hosted enrollment."""
import copy
import json
from pathlib import Path
import tempfile
import shutil
import subprocess
import sys
import unittest
from unittest.mock import Mock, patch
from uuid import uuid4

import managed_init


def payload():
    value = {key: str(uuid4()) for key in ['InstallationID', 'TenantID', 'EnvironmentID',
                                          'AllocationID', 'SessionID', 'DeviceID']}
    return dict(value, RuntimeBootstrap={'version': 1, 'core_url': 'https://core.example/api/v1',
                'device_id': value['DeviceID'], 'credential': 'private-managed-token'},
                NetworkAccess='restricted', AllowedDomains=['example.com'])


class ManagedStartupTest(unittest.TestCase):
    def test_isolated_packaged_import(self):
        with tempfile.TemporaryDirectory() as temporary:
            for name in ('init.py', 'managed_init.py', 'helper_contract_generated.py'):
                shutil.copy2(Path(__file__).with_name(name), Path(temporary, name))
            subprocess.run([sys.executable, '-I', '-c',
                            "import runpy,sys; runpy.run_path(sys.argv[1], run_name='fixture')",
                            str(Path(temporary, 'managed_init.py'))], cwd=temporary, check=True,
                           capture_output=True)

    def test_shared_bootstrap_exchanges(self):
        fixture = Path(__file__).resolve().parents[2] / 'tools/e2b-provider/testdata/contract.json'
        for case in json.loads(fixture.read_text()):
            if case['kind'] != 'managed':
                continue
            with self.subTest(name=case['name']):
                try:
                    managed_init.identity(case['payload'])
                    valid = True
                except (ValueError, TypeError, KeyError):
                    valid = False
                self.assertEqual(valid, case['valid'])

    def test_invalid_binding_rejected(self):
        source = payload()
        for key, value in [('DeviceID', 'other'), ('NetworkAccess', 'unknown')]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                managed_init.identity(dict(source, **{key: value}))

    def exercise(self, failed=False):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / 'receipt'
            root.mkdir()
            profile = Path(temporary) / 'profile'
            profile.mkdir()
            source = root / 'managed-bootstrap.json'
            data = payload()
            source.write_text(json.dumps(data))
            process = Mock(return_value=Mock(pid=456))
            if failed:
                process.side_effect = RuntimeError('private process diagnostic')
            image_env = {'PATH': '/usr/local/bin:/usr/bin:/bin', 'OAC_RUNTIME_HOME': str(Path(temporary) / '.oac'),
                         'OAC_RUNTIME_WORKSPACE': '/environment/workspace',
                         'OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE': str(Path(temporary) / 'control' / 'custom-suspend.json')}
            with patch.object(managed_init.shared, 'ROOT', root), patch.object(managed_init.shared, 'PROFILE', profile), \
                    patch.object(managed_init.shared, 'prepare_runtime', return_value=image_env), \
                    patch.object(managed_init.os, 'chown') as chown, \
                    patch.object(managed_init.os, 'fchown'), patch.object(managed_init.subprocess, 'Popen', process):
                if failed:
                    with self.assertRaises(RuntimeError):
                        managed_init.initialize()
                else:
                    managed_init.initialize()
                self.assertTrue((root / 'managed-launch.json').exists())
                connection_file = Path(temporary) / 'runtime-bootstrap.json'
                self.assertEqual(json.loads(connection_file.read_text()), data['RuntimeBootstrap'])
                self.assertEqual(connection_file.stat().st_mode & 0o777, 0o600)
                self.assertFalse((profile / 'auth.json').exists())
                self.assertEqual(process.call_args.args[0][-2:], ['--bootstrap-file', str(connection_file)])
                self.assertFalse(source.exists())
                self.assertNotIn(data['RuntimeBootstrap']['credential'], json.dumps(process.call_args.args))
                self.assertNotIn(data['RuntimeBootstrap']['credential'], json.dumps(process.call_args.kwargs['env']))
                self.assertEqual(process.call_args.kwargs['env']['OAC_RUNTIME_ENVIRONMENT_ID'], data['EnvironmentID'])
                self.assertEqual(process.call_args.kwargs['user'], 1000)
                control = Path(temporary) / 'control'
                self.assertEqual(control.stat().st_mode & 0o777, 0o700)
                chown.assert_called_once_with(control, 1000, 1000)
                self.assertEqual(process.call_args.kwargs['env']['OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE'], str(control / 'custom-suspend.json'))
                if failed:
                    self.assertFalse((root / 'managed-ready.json').exists())
                else:
                    receipt = json.loads((root / 'managed-ready.json').read_text())
                    self.assertEqual(receipt['identity'], managed_init.identity(data))
                    self.assertNotIn(data['RuntimeBootstrap']['credential'], json.dumps(receipt))
                source.write_text(json.dumps(data))
                with self.assertRaises(RuntimeError):
                    managed_init.initialize()
                process.assert_called_once()

    def test_managed_credentials_and_environment(self):
        self.exercise()

    def test_unknown_start_preserves_claim_and_never_replays(self):
        self.exercise(failed=True)

    def test_existing_self_hosted_claim_prevents_managed_start(self):
        with tempfile.TemporaryDirectory() as temporary, patch.object(managed_init.shared, 'ROOT', Path(temporary)):
            Path(temporary, 'launch.json').write_text('{}')
            with self.assertRaises(RuntimeError):
                managed_init.initialize()


if __name__ == '__main__':
    unittest.main()
