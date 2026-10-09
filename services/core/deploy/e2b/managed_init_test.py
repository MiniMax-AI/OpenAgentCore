"""Managed startup starts only Sandbox I/O, once, with its input in a private file."""
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
    value = {key: str(uuid4()) for key in ['InstallationID', 'TenantID', 'EnvironmentID', 'AllocationID']}
    return dict(value, SandboxIO={'version': 1, 'link_url': 'wss://core.example/api/v1/sandbox-link', 'credential': 'private-serve-token',
                                  'resource': {'tenant_id': value['TenantID'], 'environment_id': value['EnvironmentID'],
                                               'kind': 'allocation', 'id': value['AllocationID'], 'generation': 1}})


class ManagedStartupTest(unittest.TestCase):
    def test_isolated_packaged_import(self):
        with tempfile.TemporaryDirectory() as temporary:
            for name in ('managed_init.py', 'helper_contract_generated.py'):
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

    def exercise(self, failed=False):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / 'receipt'
            root.mkdir()
            home = Path(temporary) / 'home'
            home.mkdir()
            source = root / 'managed-bootstrap.json'
            data = payload()
            source.write_text(json.dumps(data))

            def launch(*args, **kwargs):
                delegate.assert_called_once_with()
                if failed:
                    raise RuntimeError('private process diagnostic')
                return Mock(pid=456)

            process = Mock(side_effect=launch)
            with patch.object(managed_init, 'ROOT', root), patch.object(managed_init, 'HOME', home), \
                    patch.object(managed_init, 'prepare_sandbox'), \
                    patch.object(managed_init, 'delegate_process_group') as delegate, \
                    patch.object(managed_init.os, 'fchown'), patch.object(managed_init.subprocess, 'Popen', process):
                if failed:
                    with self.assertRaises(RuntimeError):
                        managed_init.initialize()
                else:
                    managed_init.initialize()
                self.assertTrue((root / 'managed-launch.json').exists())
                serve_file = home / 'sandbox-io-bootstrap.json'
                self.assertEqual(json.loads(serve_file.read_text()), data['SandboxIO'])
                self.assertEqual(serve_file.stat().st_mode & 0o777, 0o600)
                self.assertEqual(sorted(p.name for p in home.iterdir()), ['sandbox-io-bootstrap.json', 'sandbox-io.log'])
                self.assertFalse(source.exists())
                self.assertEqual(process.call_count, 1)
                serve = process.call_args
                self.assertEqual(serve.args[0], ['/usr/local/bin/oac-sandbox-io', '--bootstrap-file', str(serve_file)])
                self.assertEqual((serve.kwargs['env'], serve.kwargs['user'], serve.kwargs['group']), ({}, 1000, 1000))
                self.assertNotIn(data['SandboxIO']['credential'], json.dumps(serve.args))
                if failed:
                    self.assertFalse((root / 'managed-ready.json').exists())
                else:
                    receipt = json.loads((root / 'managed-ready.json').read_text())
                    self.assertEqual(receipt, {'identity': managed_init.identity(data), 'status': 'sandbox_io_started',
                                               'sandbox_io_pid': 456})
                source.write_text(json.dumps(data))
                with self.assertRaises(RuntimeError):
                    managed_init.initialize()
                self.assertEqual(process.call_count, 1)

    def test_starts_only_sandbox_io(self):
        self.exercise()

    def test_unknown_start_preserves_claim_and_never_replays(self):
        self.exercise(failed=True)

    def test_failed_delegation_never_starts_service_or_replays(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary, 'receipt')
            root.mkdir()
            home = Path(temporary, 'home')
            home.mkdir()
            (root / 'managed-bootstrap.json').write_text(json.dumps(payload()))
            with patch.object(managed_init, 'ROOT', root), patch.object(managed_init, 'HOME', home), \
                    patch.object(managed_init, 'prepare_sandbox'), patch.object(managed_init.os, 'fchown'), \
                    patch.object(managed_init, 'delegate_process_group', side_effect=OSError('delegation unavailable')), \
                    patch.object(managed_init.subprocess, 'Popen') as process:
                with self.assertRaises(OSError):
                    managed_init.initialize()
                self.assertTrue((root / 'managed-launch.json').exists())
                self.assertFalse((root / 'managed-ready.json').exists())
                with self.assertRaisesRegex(RuntimeError, 'cannot be replayed'):
                    managed_init.initialize()
                process.assert_not_called()

    def test_delegation_rejects_unmapped_membership_before_creating_group(self):
        mount = '1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup2 rw'
        for name, mounts, membership, processes in [
                ('wrong_filesystem', mount.replace('cgroup2', 'tmpfs'), '0::/', '123'),
                ('missing_membership', mount, '', '123'),
                ('outside_namespace', mount, '0::/../outside', '123'),
                ('unmapped_membership', mount, '0::/workload', '456')]:
            with self.subTest(name=name):
                files = {'/proc/self/mountinfo': mounts, '/proc/self/cgroup': membership,
                         '/sys/fs/cgroup/workload/cgroup.procs': processes}
                with patch.object(Path, 'read_text', lambda path: files[str(path)]), \
                        patch.object(Path, 'mkdir') as mkdir, patch.object(managed_init.os, 'getpid', return_value=123):
                    with self.assertRaises(RuntimeError):
                        managed_init.delegate_process_group()
                    mkdir.assert_not_called()


if __name__ == '__main__':
    unittest.main()
