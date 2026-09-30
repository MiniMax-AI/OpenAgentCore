"""Failure diagnostics must preserve both the primary exception and fixture secrets."""

import io
import json
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from official_diagnostics import failure_facts, finish_server


class DiagnosticsTests(unittest.TestCase):
    def test_failed_owned_process_reports_exit_and_safe_error(self):
        with tempfile.TemporaryFile(mode="w+") as log:
            code = 'import json; print(json.dumps({"msg":"oac-core startup failed","error":"ERROR: deadlock detected (SQLSTATE 40P01)"})); raise SystemExit(7)'
            process = subprocess.Popen([sys.executable, "-c", code], stdout=log, stderr=log)
            self.assertEqual(process.wait(timeout=10), 7)
            diagnostics = io.StringIO()
            primary = RuntimeError("primary fixture failure")
            with self.assertRaises(RuntimeError) as caught:
                try:
                    raise primary
                finally:
                    finish_server(process, log, [], sys.exc_info()[1], diagnostics)
            self.assertIs(caught.exception, primary)
            facts = json.loads(diagnostics.getvalue().split(": ", 1)[1])
            self.assertEqual(facts["exit_code"], 7)
            self.assertEqual(facts["events"], [{"event": "service_exit", "error": "deadlock_detected", "sqlstate": "40P01"}])

    def test_secrets_and_untrusted_fields_never_enter_diagnostics(self):
        secret = "dynamic-issued-project-key"
        lines = [json.dumps({"msg": "oac-core startup failed", "error": secret}),
                 json.dumps({"msg": "oac-core startup failed", "error": "postgres://user:unknown-password@host/db", "Authorization": "Bearer unknown-key", "body": "private request"}),
                 "panic: private request", json.dumps({"msg": ["invalid log"]})]
        diagnostics = io.StringIO()
        primary = RuntimeError("primary")
        with self.assertRaises(RuntimeError) as caught:
            try:
                raise primary
            finally:
                finish_server(None, io.StringIO("\n".join(lines)), [secret], sys.exc_info()[1], diagnostics)
        self.assertIs(caught.exception, primary)
        facts = json.loads(diagnostics.getvalue().split(": ", 1)[1])
        self.assertTrue(facts["cleanup_failed"])
        self.assertEqual(facts["events"], [{"event": "service_exit", "error": "unclassified"}])
        self.assertEqual(facts["omitted_lines"], 3)
        self.assertNotIn(secret, diagnostics.getvalue())
        self.assertNotIn("private", diagnostics.getvalue())
        self.assertNotIn("unknown", diagnostics.getvalue())

    def test_timeout_kills_child_without_masking_primary(self):
        process = mock.Mock()
        process.poll.return_value = None
        process.wait.side_effect = [subprocess.TimeoutExpired("private command", 15), 0]
        output = io.StringIO()
        finish_server(process, io.StringIO(""), [], RuntimeError("primary"), output)
        process.kill.assert_called_once()
        self.assertEqual(process.wait.call_count, 2)
        facts = json.loads(output.getvalue().split(": ", 1)[1])
        self.assertIsNone(facts["exit_code"])
        self.assertTrue(facts["cleanup_failed"])

    def test_success_keeps_secret_assertion_and_has_no_diagnostic(self):
        with self.assertRaisesRegex(AssertionError, "Fixture secret"):
            finish_server(None, io.StringIO("secret"), ["secret"], None)
        output = io.StringIO()
        finish_server(None, io.StringIO(""), [], None, output)
        self.assertEqual(output.getvalue(), "")
        self.assertEqual(len(failure_facts('\n'.join([json.dumps({"msg": "oac-core startup failed"})] * 100), [])['events']), 32)


if __name__ == "__main__":
    unittest.main()
