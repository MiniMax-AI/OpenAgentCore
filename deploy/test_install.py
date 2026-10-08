"""The POSIX launcher selects and verifies a binary; lifecycle tests live in oac."""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('install.sh').resolve()


class LauncherTests(unittest.TestCase):
    def run_launcher(self, system='Darwin', machine='arm64', corrupt=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = b'#!/bin/sh\nprintf "%s\\n" "$@"\n'
            digest = hashlib.sha256(binary).hexdigest()
            scripts = {
                'uname': '#!/bin/sh\nif [ "$1" = -s ]; then echo ' + system + '; else echo ' + machine + '; fi\n',
                'curl': '''#!/usr/bin/env python3
import pathlib,sys
args=sys.argv
url=args[-3]; target=pathlib.Path(args[-1])
asset=url.rsplit('/',1)[1]
if asset.endswith('.sha256'):
    target.write_text(DIGEST + '  ' + asset.removesuffix('.sha256') + '\\n')
else:
    target.write_bytes(BINARY)
'''.replace('DIGEST', repr('0' * 64 if corrupt else digest)).replace('BINARY', repr(binary)),
            }
            for name, contents in scripts.items():
                (root / name).write_text(contents); (root / name).chmod(0o700)
            return subprocess.run(['bash', str(SCRIPT), '--install-dir', '/path with spaces/core', '--version', 'v1.2.3'],
                                  env={**os.environ, 'PATH': str(root) + os.pathsep + os.environ['PATH']}, capture_output=True, text=True)

    def test_linux_and_mac_share_argument_forwarding(self):
        for system, machine in [('Linux', 'x86_64'), ('Linux', 'aarch64'), ('Darwin', 'arm64'), ('Darwin', 'x86_64')]:
            with self.subTest(system=system, machine=machine):
                result = self.run_launcher(system, machine)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines(), ['install', '--install-dir', '/path with spaces/core', '--version', 'v1.2.3'])

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
