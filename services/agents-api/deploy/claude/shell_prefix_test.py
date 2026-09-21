"""Verify the boundary between a native MCP argv and a complete Bash command."""
from pathlib import Path
import runpy
import shlex
import unittest


prefix = runpy.run_path(str(Path(__file__).with_name('shell-prefix.py')))
resolve = prefix['invocation']
entry = prefix['MCP_ENTRY'] + ['plugins/0', 'installed']


class ShellPrefixTest(unittest.TestCase):
    def test_native_stdio_exec_preserves_arguments(self):
        self.assertEqual(resolve(shlex.join(entry)), entry)
        # Never interpret shell syntax inside quoted package/server arguments.
        unusual = prefix['MCP_ENTRY'] + ['plugins/$(touch marker)', 'server;exit']
        self.assertEqual(resolve(shlex.join(unusual)), unusual)

    def test_shell_commands_remain_inside_tool_root(self):
        original = shlex.join(entry)
        for command in [original + ' && pwd', original + '; id', original + ' extra',
                        original + ' # comment', 'exec ' + original,
                        original.replace('/usr/bin/python3', 'python3'),
                        original.replace(' -I -S ', ' -I '),
                        'eval ' + shlex.quote(original) + '; pwd -P > /tmp/cwd',
                        "printf '%s\\n' 'a && b'; exit 7", "unterminated '"]:
            with self.subTest(command=command):
                self.assertEqual(resolve(command), [prefix['TOOL_ROOT'], command])


if __name__ == '__main__':
    unittest.main()
