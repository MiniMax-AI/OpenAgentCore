"""Presentation remains readable in terminals, redirected logs and partial failure."""
import contextlib
import io
import os
from pathlib import Path
import unittest
from unittest import mock

import install_output as output
import install_display as display


class Terminal(io.StringIO):
    def isatty(self):
        return True


class OutputTests(unittest.TestCase):
    def test_color_only_in_capable_terminals_and_no_color_wins(self):
        for stream, environment, colored in ((Terminal(), {"TERM": "xterm"}, True),
                                              (Terminal(), {"TERM": "dumb"}, False),
                                              (Terminal(), {"TERM": "xterm", "NO_COLOR": ""}, False),
                                              (io.StringIO(), {"TERM": "xterm"}, False)):
            with self.subTest(environment=environment, colored=colored), \
                    mock.patch.dict(os.environ, environment, clear=True), contextlib.redirect_stdout(stream):
                display.step("Loading images")
            self.assertEqual("\033[" in stream.getvalue(), colored)
            self.assertIn("==> Loading images...", stream.getvalue())

    def test_progress_is_flushed_before_returning_and_errors_use_stderr(self):
        stream = io.StringIO()
        with contextlib.redirect_stdout(stream), mock.patch.object(stream, "flush") as flush:
            display.step("Checking services")
            flush.assert_called_once()
        error = io.StringIO()
        with contextlib.redirect_stderr(error):
            display.error("service did not become healthy")
        self.assertEqual(error.getvalue(), "Installation failed: service did not become healthy\n")
        self.assertNotIn("failed", stream.getvalue())

    def test_summary_groups_details_and_keeps_commands_copyable(self):
        stream = io.StringIO()
        root = Path("/tmp/install with spaces")
        config = {"mode": "all", "ports": {"core": 8091}}
        with contextlib.redirect_stdout(stream):
            output.summary(root, config, ["Console: http://localhost:8080 (local only)"],
                           True, None, None, False, False)
        text = stream.getvalue()
        sections = ["\nInstallation complete.\n", "\nAccess\n", "\nSign in\n", "\nNext\n", "\nManage\n"]
        positions = [text.index(section) for section in sections]
        self.assertEqual(positions, sorted(positions))
        self.assertIn("  Core key file: /tmp/install with spaces/secrets/core.key\n", text)
        self.assertIn("  Apply settings: '/tmp/install with spaces/oac' apply\n", text)
        self.assertIn("  Uninstall: '/tmp/install with spaces/oac' uninstall\n", text)
        self.assertNotIn("\033[", text)


if __name__ == "__main__":
    unittest.main()
