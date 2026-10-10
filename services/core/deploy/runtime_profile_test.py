import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from runtime_profile import DEBIAN_PATH, preserve_runtime_path


class RuntimeProfileTest(unittest.TestCase):
    def test_rejects_changed_or_duplicate_upstream_block(self):
        for value in ('', DEBIAN_PATH.replace('/usr/games', '/changed'), DEBIAN_PATH * 2,
                      preserve_runtime_path(DEBIAN_PATH)):
            with self.subTest(profile=value):
                self.assertRaises(ValueError, preserve_runtime_path, value)

    def test_preserves_other_profile_content(self):
        result = preserve_runtime_path('# before\n' + DEBIAN_PATH + '# after\n')
        self.assertTrue(result.startswith('# before\n'))
        self.assertTrue(result.endswith('# after\n'))

    @unittest.skipUnless(os.name == 'posix' and Path('/bin/bash').exists(), 'Bash profile')
    def test_exported_unset_and_empty_path(self):
        with tempfile.TemporaryDirectory() as directory:
            original, fixed = Path(directory) / 'original', Path(directory) / 'fixed'
            original.write_text(DEBIAN_PATH)
            fixed.write_text(preserve_runtime_path(DEBIAN_PATH))
            def run(profile, path):
                env = dict(os.environ)
                env.pop('BASH_ENV', None)
                if path is None:
                    env.pop('PATH', None)
                else:
                    env['PATH'] = path
                return subprocess.check_output(['/bin/bash', '--noprofile', '--norc', '-c',
                    '. "$1"; printf %s "$PATH"', 'profile-test', str(profile)], env=env, text=True)
            custom = '/custom package/bin:/usr/bin:/bin'
            self.assertEqual(run(fixed, custom), custom)
            self.assertEqual(run(fixed, ''), '')
            self.assertEqual(run(fixed, None), run(original, None))


if __name__ == '__main__':
    unittest.main()
