"""The POSIX launcher selects and verifies a binary; lifecycle tests live in oac."""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('install.sh').resolve()


class LauncherTests(unittest.TestCase):
    def run_launcher(self, system='Darwin', machine='arm64', corrupt=False, arguments=('--install-dir', '/path with spaces/core', '--version', 'v1.2.3'), missing=False, outage=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = b'#!/bin/sh\nprintf "%s\\n" "$@"\n'
            digest = hashlib.sha256(binary).hexdigest()
            scripts = {
                'uname': '#!/bin/sh\nif [ "$1" = -s ]; then echo ' + system + '; else echo ' + machine + '; fi\n',
                'curl': '''#!/usr/bin/env python3
import pathlib,sys
args=sys.argv
url=args[-1]; target=pathlib.Path(args[args.index('--output') + 1])
asset=url.rsplit('/',1)[1]
if asset.endswith('.sha256'):
    target.write_text(DIGEST + '  ' + asset.removesuffix('.sha256') + '\\n')
else:
    target.write_bytes(BINARY)
if OUTAGE: sys.exit(22)
if MISSING:
    sys.stdout.write('404'); sys.exit(22)
'''.replace('OUTAGE', repr(outage)).replace('MISSING', repr(missing)).replace('DIGEST', repr('0' * 64 if corrupt else digest)).replace('BINARY', repr(binary)),
            }
            scripts['sleep'] = '#!/bin/sh\n'
            for name, contents in scripts.items():
                (root / name).write_text(contents); (root / name).chmod(0o700)
            return subprocess.run(['/bin/bash', str(SCRIPT), *arguments],
                                  env={**os.environ, 'PATH': str(root) + os.pathsep + os.environ['PATH']}, capture_output=True, text=True)

    def test_linux_and_mac_share_argument_forwarding(self):
        for system, machine in [('Linux', 'x86_64'), ('Linux', 'aarch64'), ('Darwin', 'arm64'), ('Darwin', 'x86_64')]:
            with self.subTest(system=system, machine=machine):
                result = self.run_launcher(system, machine)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines(), ['install', '--install-dir', '/path with spaces/core', '--version', 'v1.2.3'])

    def test_no_arguments_run_the_installer(self):
        result = self.run_launcher(arguments=())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ['install'])

    def test_version_forms_and_dangling_version(self):
        for arguments in [('--version=v1.2.3',), ('--version', 'v1.2.3')]:
            self.assertEqual(self.run_launcher(arguments=arguments).returncode, 0)
        result = self.run_launcher(arguments=('--upgrade', '--version'))
        self.assertEqual(result.returncode, 1)
        self.assertIn('Missing release tag', result.stderr)

    def test_download_failures_say_what_to_do(self):
        result = self.run_launcher(missing=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn('Release v1.2.3 not found', result.stderr)
        result = self.run_launcher(outage=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn('Check the network', result.stderr)
        self.assertEqual(result.stdout, '')

    def test_corrupt_binary_is_never_executed(self):
        result = self.run_launcher(corrupt=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('checksum mismatch', result.stderr)
        self.assertEqual(result.stdout, '')

    def test_unsupported_host_fails_before_downloading(self):
        result = self.run_launcher(system='FreeBSD')
        self.assertNotEqual(result.returncode, 0)


if __name__ == '__main__':
    unittest.main()
