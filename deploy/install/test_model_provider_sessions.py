"""Verify the pre-upgrade check is read-only and reports each environment."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import model_provider_sessions


class ModelProviderSessionsTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "compose.json").write_text(json.dumps({"services": {"database": {}, "core": {}}}))

    def test_counts_through_a_read_only_query(self):
        result = subprocess.CompletedProcess([], 0, stdout="self_hosted|2\n", stderr="")
        with mock.patch.object(model_provider_sessions.subprocess, "run", return_value=result) as run:
            self.assertEqual(model_provider_sessions.count(self.root), {"openai_hosted": 0, "self_hosted": 2})
        command = run.call_args.args[0]
        self.assertIn("PGOPTIONS=-c default_transaction_read_only=on", command)
        self.assertTrue(command[-1].lstrip().startswith("SELECT"))

    def test_refuses_web_only_and_unexpected_output(self):
        with mock.patch.object(model_provider_sessions.subprocess, "run",
                               return_value=subprocess.CompletedProcess([], 0, stdout="none|1\n", stderr="")):
            with self.assertRaises(model_provider_sessions.CheckError):
                model_provider_sessions.count(self.root)
        (self.root / "compose.json").write_text(json.dumps({"services": {"web": {}}}))
        with self.assertRaises(model_provider_sessions.CheckError):
            model_provider_sessions.count(self.root)


if __name__ == "__main__":
    unittest.main()
