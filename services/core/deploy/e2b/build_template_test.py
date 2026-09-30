"""Exercise real Runtime tar metadata without Docker or provider calls."""
import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import stat
import sys
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch


class BundlePermissionsTest(unittest.TestCase):
    def test_public_parents_and_runtime_modes_under_both_umasks(self):
        for mask in (0o022, 0o077):
            with self.subTest(umask=oct(mask)), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                key = root / 'key'
                key.write_text('fixture-only')
                key.chmod(0o600)
                output = root / 'private/result.json'
                source_modes = {'bin': 0o755, 'bin/daemon': 0o751,
                                'codex-resources': 0o755, 'codex': 0o700,
                                'codex/config': 0o600, 'opt': 0o755,
                                'opt/private': 0o700, 'opt/private/key': 0o600}
                image = {'Architecture': 'amd64', 'Os': 'linux', 'Id': 'sha256:fixture',
                         'Config': {'Env': ['OAC_RUNTIME_WORKSPACE=/environment/workspace']}}

                def check_output(argv, **kwargs):
                    if argv == ['docker', 'image', 'inspect', 'sha256:fixture']:
                        return json.dumps([image]).encode()
                    self.assertEqual(argv, ['docker', 'create', 'sha256:fixture'])
                    return 'fixture-container\n'

                def run(argv, **kwargs):
                    if argv == ['docker', 'rm', 'fixture-container']:
                        return SimpleNamespace(returncode=0)
                    self.assertEqual(argv[:2], ['docker', 'cp'])
                    self.assertEqual(argv[-1], '-')
                    name = argv[2].split(':', 1)[1].rsplit('/', 1)[-1]
                    with tarfile.open(fileobj=kwargs['stdout'], mode='w') as archive:
                        for path, mode in source_modes.items():
                            if path != name and not path.startswith(name + '/'):
                                continue
                            entry = tarfile.TarInfo(path)
                            entry.mode = mode
                            if path in ('bin', 'codex-resources', 'codex', 'opt', 'opt/private'):
                                entry.type = tarfile.DIRTYPE
                                archive.addfile(entry)
                            else:
                                entry.size = 7
                                archive.addfile(entry, io.BytesIO(b'fixture'))
                    return SimpleNamespace(returncode=0)

                template = Mock()
                for method in ('from_image', 'run_cmd', 'copy', 'set_user', 'set_workdir'):
                    getattr(template, method).return_value = template

                def build(instance, **kwargs):
                    self.assertIs(instance, template)
                    context = Path(factory.call_args.kwargs['file_context_path'])
                    self.assertEqual(stat.S_IMODE(context.stat().st_mode), 0o700)
                    with tarfile.open(context / 'runtime.tar.gz') as archive:
                        modes = {m.name: stat.S_IMODE(m.mode) for m in archive.getmembers()}
                    for parent in ('usr', 'usr/local', 'etc'):
                        self.assertEqual(modes[parent], 0o755)
                    for path, mode in source_modes.items():
                        prefix = 'usr/local/' if path.split('/')[0] in ('bin', 'codex-resources') else 'etc/' if path.startswith('codex') else ''
                        self.assertEqual(modes[prefix + path], mode)
                    self.assertEqual(stat.S_IMODE((context / 'runtime.tar.gz').stat().st_mode), 0o666 & ~mask)
                    self.assertEqual(stat.S_IMODE(key.stat().st_mode), 0o600)
                    return SimpleNamespace(template_id='fixture', build_id='build')

                factory = Mock(return_value=template)
                factory.build.side_effect = build
                original_mask = os.umask(mask)
                try:
                    with patch.dict(sys.modules, {'e2b': SimpleNamespace(Template=factory)}), \
                            patch.dict(os.environ, {'OAC_DEV_HOME': str(root / 'state')}), \
                            patch.object(sys, 'argv', ['build-template.py', '--image', 'sha256:fixture',
                                '--name', 'fixture', '--api-key-file', str(key), '--output', str(output)]), \
                            patch('subprocess.check_output', side_effect=check_output), \
                            patch('subprocess.run', side_effect=run), contextlib.redirect_stdout(io.StringIO()):
                        runpy.run_path(str(Path(__file__).with_name('build-template.py')), run_name='__main__')
                    self.assertEqual(os.umask(mask), mask)
                    self.assertEqual(stat.S_IMODE(output.stat().st_mode), 0o666 & ~mask)
                    self.assertEqual(stat.S_IMODE(output.parent.stat().st_mode), 0o777 & ~mask)
                    self.assertEqual(key.read_text(), 'fixture-only')
                    factory.build.assert_called_once()
                finally:
                    os.umask(original_mask)


if __name__ == '__main__':
    unittest.main()
