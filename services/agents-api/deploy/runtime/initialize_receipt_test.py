"""Host-runnable checks of the initializer's failure receipt.

The receipt may carry only the integer exit status of a sandboxed step, never its
command, input or output. Real isolation is covered by initialize_test.py inside a
packaged Runtime; these checks need no Runtime and run with plain python3.
"""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location('agents_api_runtime_initialize', Path(__file__).with_name('initialize.py'))
initialize = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(initialize)
CANARY = 'CANARY-initializer-receipt-5d1c'
SANDBOXED = ['/usr/bin/bwrap', '--unshare-user', '--', '/bin/bash', '-c', 'echo ' + CANARY + '; exit 3']


def invoke(failure):
    """Run main() with a request whose step raises failure; return (code, stdout, stderr)."""
    request = json.dumps({'version': 1, 'action': 'setup', 'network': 'enabled', 'command': 'echo ' + CANARY})
    stdin = mock.Mock()
    stdin.buffer = io.BytesIO(request.encode())
    stdout, stderr = io.StringIO(), io.StringIO()
    with mock.patch.object(initialize.sys, 'stdin', stdin), mock.patch.object(initialize.sys, 'argv', ['initialize']), \
            mock.patch.object(initialize, 'roots', lambda: None), mock.patch.object(initialize, 'run', side_effect=failure), \
            contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
        code = initialize.main()
    return code, stdout.getvalue(), stderr.getvalue()


class FailureReceiptTest(unittest.TestCase):
    def assert_receipt(self, failure, expected):
        code, stdout, stderr = invoke(failure)
        self.assertEqual(code, 1)
        self.assertEqual(stderr, '')
        self.assertEqual(json.loads(stdout), expected)
        self.assertNotIn(CANARY, stdout)
        self.assertNotIn('echo', stdout)

    def test_sandboxed_step_reports_only_its_exit_status(self):
        for status in (1, 3, 100, 255):
            failure = subprocess.CalledProcessError(status, SANDBOXED, output=CANARY.encode(), stderr=CANARY.encode())
            self.assert_receipt(failure, {'version': 1, 'outcome': 'failed', 'exit_code': status})
        # The receipt keeps its compact, stable encoding.
        _, stdout, _ = invoke(subprocess.CalledProcessError(3, SANDBOXED))
        self.assertEqual(stdout, '{"version":1,"outcome":"failed","exit_code":3}\n')

    def test_runtime_helpers_signals_and_other_errors_stay_generic(self):
        generic = {'version': 1, 'outcome': 'failed'}
        for failure in (
            subprocess.CalledProcessError(2, ['/usr/bin/tar', '-xzf', CANARY]),
            subprocess.CalledProcessError(1, ['/usr/local/bin/agents-api-codex-write', CANARY]),
            subprocess.CalledProcessError(-9, SANDBOXED),
            subprocess.CalledProcessError(256, SANDBOXED),
            subprocess.CalledProcessError(3, ' '.join(SANDBOXED)),
            subprocess.TimeoutExpired(SANDBOXED, 120, output=CANARY.encode()),
            ValueError(CANARY),
            OSError(CANARY),
        ):
            with self.subTest(failure=type(failure).__name__):
                self.assert_receipt(failure, generic)
        _, stdout, _ = invoke(ValueError(CANARY))
        self.assertEqual(stdout, '{"version":1,"outcome":"failed"}\n')

    def test_real_child_output_never_reaches_the_receipt(self):
        # A real process failure, launched like run() with discarded output.
        try:
            subprocess.run(['/bin/sh', '-c', 'echo ' + CANARY + '; echo ' + CANARY + ' >&2; exit 7'],
                           stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
        except subprocess.CalledProcessError as error:
            failure = subprocess.CalledProcessError(error.returncode, ['/usr/bin/bwrap', '--', *error.cmd])
        self.assert_receipt(failure, {'version': 1, 'outcome': 'failed', 'exit_code': 7})


if __name__ == '__main__':
    unittest.main()
