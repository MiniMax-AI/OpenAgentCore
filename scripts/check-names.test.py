#!/usr/bin/env python3
"""Regression tests for scoped name exceptions and repository scanning."""
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("check_names", Path(__file__).with_name("check-names.py"))
names = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = names
SPEC.loader.exec_module(names)


class NameGuardTests(unittest.TestCase):
    def rule(self, pattern, path="*"):
        return names.ExceptionRule(path, re.compile(pattern), "Deferred source identity")

    def test_allowed_import_does_not_hide_setting_on_same_line(self):
        content = 'import "github.com/MiniMax-AI-Dev/parsar/internal/foo"; setting = "PARSAR_HOME"'
        rules = [self.rule(r"github\.com/MiniMax-AI-Dev/parsar/[a-z/]+")]
        self.assertEqual(names.violations("main.go", content, rules), [(1, 68, "PARSAR")])

    def test_partial_exception_cannot_hide_longer_retired_setting(self):
        self.assertEqual(names.violations("main.go", "AGENTS_API_PORT", [self.rule("AGENTS_API")]),
                         [(1, 1, "AGENTS_API_PORT")])

    def test_exception_is_path_scoped_and_case_sensitive(self):
        rule = self.rule("parsar", "history/*")
        self.assertFalse(names.violations("history/source.json", "parsar", [rule]))
        self.assertTrue(names.violations("README.md", "parsar", [rule]))
        self.assertTrue(names.violations("history/source.json", "PARSAR", [rule]))

    def test_detections_include_commands_settings_labels_and_display(self):
        for value in ("PaRsAr", "io.parsar.installation", "AGENTS_API_PORT", "CORE_CONSOLE_BIND", "AGENTS_CORE_WEB_ADDR",
                      "agents-api", "agents-api-codex-write", "core-console", "agents-runtime-dev",
                      "Agent Core", "Agents Core Web"):
            with self.subTest(value=value):
                self.assertTrue(names.violations("x", value, []))

    def test_new_names_and_pinned_public_contract_are_not_retired(self):
        content = ("OpenAgentCore oac-core OAC_CORE_PORT agents_api AgentCoreError x_agents_core core_console_session"
                   " github.com/MiniMax-AI/OpenAgentCore")
        self.assertFalse(names.violations("x", content, []))

    def test_coordinates_and_multiple_matches_do_not_disclose_line_content(self):
        content = "first\n  parsar secret=value CORE_CONSOLE_BIND"
        self.assertEqual(names.violations("x", content, []),
                         [(2, 3, "parsar"), (2, 23, "CORE_CONSOLE_BIND")])

    def test_multiline_exception_covers_only_its_span(self):
        content = "Parsar\nproduct\nPARSAR_HOME"
        rules = [self.rule("Parsar\\nproduct")]
        self.assertEqual(names.violations("x", content, rules), [(3, 1, "PARSAR")])

    def test_invalid_allowlist_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "rules.json"
            for value in ({}, [{"path": "*", "regex": "parsar", "reason": " "}],
                          [{"path": "*", "regex": ".*", "reason": "too broad"}],
                          [{"path": "*", "regex": "[", "reason": "invalid"}],
                          [{"path": "*", "regex": "parsar", "reason": "valid", "typo": True}]):
                path.write_text(json.dumps(value))
                with self.subTest(value=value), self.assertRaises((ValueError, re.error)):
                    names.load_rules(path)

    def test_repository_scan_uses_tracked_text_and_skips_binary_without_following_links(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q", directory], check=True)
            (root / "tracked.txt").write_text("PARSAR_HOME\n")
            (root / "untracked.txt").write_text("PARSAR_OTHER\n")
            (root / "binary").write_bytes(b"\0parsar")
            (root / "link").symlink_to("untracked.txt")
            subprocess.run(["git", "-C", directory, "add", "tracked.txt", "binary", "link"], check=True)
            rules = root / "rules.json"
            rules.write_text("[]")
            command = [sys.executable, str(Path(names.__file__)), "--root", directory, "--allowlist", str(rules)]
            result = subprocess.run(command, text=True, capture_output=True)
            self.assertEqual(result.returncode, 1)
            self.assertIn("tracked.txt:1:1:", result.stdout)
            self.assertNotIn("untracked.txt", result.stdout)
            self.assertNotIn("binary:", result.stdout)
            (root / "tracked.txt").write_text("OAC_RUNTIME_HOME\n")
            self.assertEqual(subprocess.run(command, capture_output=True).returncode, 0)

    def test_exception_that_excuses_no_identifier_fails_the_scan(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q", directory], check=True)
            (root / "tracked.txt").write_text("PARSAR_HOME X-Core-Console-Actor\n")
            subprocess.run(["git", "-C", directory, "add", "tracked.txt"], check=True)
            rules = root / "rules.json"
            used = {"path": "tracked.txt", "regex": "PARSAR_HOME", "reason": "Excuses the setting"}
            command = [sys.executable, str(Path(names.__file__)), "--root", directory, "--allowlist", str(rules)]
            for unused in ({"path": "missing/*", "regex": "parsar", "reason": "Matches no file"},
                           {"path": "*", "regex": "X-Core-Console-Actor", "reason": "Matches only allowed text"}):
                with self.subTest(unused=unused["regex"]):
                    rules.write_text(json.dumps([used, unused]))
                    result = subprocess.run(command, text=True, capture_output=True)
                    self.assertEqual(result.returncode, 1)
                    self.assertIn(repr(unused["regex"]), result.stdout)
                    self.assertNotIn("PARSAR_HOME", result.stdout)
            rules.write_text(json.dumps([used]))
            self.assertEqual(subprocess.run(command, capture_output=True).returncode, 0)

    def test_persisted_domain_exception_does_not_allow_other_settings_or_paths(self):
        rules = names.load_rules(Path(__file__).with_name("name-allowlist.json"))
        content = '"parsar.agents-api.credential"; "PARSAR_HOME"'
        found = names.violations("services/core/internal/credentialcrypto/cipher.go", content, rules)
        self.assertEqual([item[2] for item in found], ["PARSAR"])
        self.assertTrue(names.violations("README.md", content, rules))

    def test_scoped_exception_does_not_hide_runtime_setting(self):
        rules = [self.rule(r"AGENTS_API_ADDR", path="services/core/cmd/server/process_configuration.go")]
        content = '{"AGENTS_API_ADDR", "OAC_ADDR"}; os.Getenv("PARSAR_HOME")'
        found = names.violations("services/core/cmd/server/process_configuration.go", content, rules)
        self.assertEqual([item[2] for item in found], ["PARSAR"])

    def test_checked_in_exceptions_do_not_hide_unrelated_retired_setting(self):
        rules = names.load_rules(Path(__file__).with_name("name-allowlist.json"))
        for content in ('"contracts/agents-api/index.md" PARSAR_HOME',
                        '"Parsar product" PARSAR_HOME'):
            with self.subTest(content=content):
                self.assertEqual(len(names.violations("README.md", content, rules)), 1)

    def test_retired_organization_is_rejected(self):
        for content in ("Copyright (c) 2026 MiniMax-AI-Dev", "https://github.com/minimax-ai-dev/OpenAgentCore"):
            with self.subTest(content=content):
                self.assertEqual([item[2].lower() for item in names.violations("LICENSE", content, [])],
                                 ["minimax-ai-dev"])

    def test_former_repository_identities_are_rejected(self):
        rules = names.load_rules(Path(__file__).with_name("name-allowlist.json"))
        for content in ("https://github.com/MiniMax-AI/parsar-core", '"github.com/MiniMax-AI-Dev/parsar/internal/foo"',
                        "services/agents-api/cmd/server", "services/core-console", "apps/parsar-daemon/cmd/parsar-daemon",
                        "scripts/build-agents-api-image.sh", "scripts/build-core-console.sh",
                        "scripts/agents-api-subagents-acceptance.py", "make check-agents-api", '"name": "parsar-core"',
                        "@agents-core-web/web", "@parsar/claude-sdk-adapter", "parsar-mcode-harness"):
            with self.subTest(content=content):
                self.assertTrue(names.violations("README.md", content, rules))


if __name__ == "__main__":
    unittest.main()
