#!/usr/bin/env python3
"""install.sh acceptance behavior: fresh install, repair, retired flags and bundle checks."""

import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import config_model
import distribution
import install
from installer_fakes import MANIFEST, FakeHost, make_bundle, run_installer, write_checksums


class InstallerTests(unittest.TestCase):
    def setUp(self):
        temporary_root = Path.home() / ".parsar/tests/install"
        temporary_root.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix="unit-", dir=temporary_root)
        self.addCleanup(self.temporary.cleanup)
        self.work = Path(self.temporary.name).resolve()
        self.root = self.work / "deployment"
        self.bundle, self.manifest = make_bundle(self.work / "bundle", MANIFEST)
        self.host = FakeHost(self)
        self.host.native_root = self.root
        self.output = io.StringIO()

    def install(self, *flags):
        with contextlib.redirect_stdout(self.output), contextlib.redirect_stderr(self.output):
            run_installer(install, self.bundle, ["--install-dir", self.root, *flags])

    def document(self, name):
        return json.loads((self.root / name).read_text())

    def snapshot(self):
        return {str(path.relative_to(self.root)): (stat.S_IMODE(path.stat().st_mode),
                path.read_bytes() if path.is_file() else None)
                for path in [self.root, *self.root.rglob("*")] if path.name != ".parsar.lock"}

    def key_file(self, contents="synthetic-existing-core-key-0123456789", mode=0o600):
        path = self.work / "existing-core.key"
        path.write_text(contents)
        path.chmod(mode)
        return path

    def test_fresh_install_writes_config_json_and_the_layout(self):
        previous = os.umask(0)
        try:
            self.install("--public-url", "https://core.example", "--web-port", "8181")
        finally:
            os.umask(previous)
        config = self.document("config.json")
        self.assertEqual(config, config_model.initial("all", public_url="https://core.example", **{"ports.web": 8181}))
        state = self.document("state.json")
        self.assertEqual((state["mode"], state["native_core"], state["source_commit"]), ("all", False, "a" * 40))
        self.assertEqual(set(state["images"]), {"core", "database", "web"})
        self.assertEqual(set(state["secrets_sha256"]), {"credential.key", "database.password"})
        names = {str(path.relative_to(self.root)) for path in self.root.rglob("*") if path.parent.name != "node-payload"
                 and "runtime" not in path.parts}
        self.assertEqual(names, {
            ".parsar.lock", "config.json", "state.json", "parsar", "node-payload", "secrets", "secrets/core.key",
            "secrets/credential.key", "secrets/database.password", "generated", "generated/compose.json",
            "generated/core.env", "generated/core-key-digests.json", "generated/settings.json",
            "generated/config.schema.json", "state", "state/e2b"})
        for path in [self.root, *self.root.rglob("*")]:
            expected = 0o700 if path.is_dir() or path.name == "parsar" else 0o600
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), expected, path)
        self.assertEqual(self.host.running, {"database", "core", "web"})
        output = self.output.getvalue()
        for name in ("core.key", "credential.key", "database.password"):
            self.assertNotIn((self.root / "secrets" / name).read_text(), output)
        self.assertIn("Console: https://core.example", output)
        self.assertIn(f"Settings: {self.root / 'config.json'}. Edit it, then run {self.root / 'parsar'} apply.", output)

    def test_rerun_reads_config_json_rejects_flags_and_repairs(self):
        self.install()
        before = self.snapshot()
        with self.assertRaisesRegex(install.InstallError, "config.json. Edit it and run .*parsar apply"):
            self.install("--web-port", "8081")
        self.assertEqual(self.snapshot(), before)
        (self.root / "parsar").unlink()
        config = self.document("config.json")
        config["ports"]["web"] = 8181
        (self.root / "config.json").write_text(json.dumps(config))
        self.host.recreated.clear()
        self.install()
        self.assertEqual((self.root / "parsar").read_bytes(), (self.bundle / "parsar.pyz").read_bytes())
        self.assertEqual(self.host.recreated, ["web"])
        other, _ = make_bundle(self.work / "other", MANIFEST, commit="b" * 40)
        with self.assertRaisesRegex(install.InstallError, "parsar upgrade"):
            with contextlib.redirect_stdout(self.output):
                run_installer(install, other, ["--install-dir", self.root])

    def test_retired_flags_and_old_layouts_name_their_replacement(self):
        for flag, command in (("--status", "parsar status"), ("--stop", "parsar stop")):
            output = io.StringIO()
            with self.subTest(flag=flag), contextlib.redirect_stderr(output), self.assertRaises(SystemExit):
                install.arguments(["--install-dir", str(self.root), flag])
            self.assertIn(f"{flag} is retired; run {self.root / command}", output.getvalue())
        output = io.StringIO()
        with contextlib.redirect_stderr(output), self.assertRaises(SystemExit):
            install.arguments(["--admin-token-file", str(self.key_file())])
        self.assertIn("--core-key-file", output.getvalue())
        self.root.mkdir()
        (self.root / "installation.json").write_text("{}")
        with self.assertRaisesRegex(install.InstallError, "predates config.json; run ./install.sh --convert"):
            self.install()

    def test_web_only_uses_the_existing_core_key_and_records_its_core(self):
        source = self.key_file()
        self.host.remote_core["http://127.0.0.1:9091"] = (200, self.host.core_installation_id)
        self.install("--web-only", "--core-url", "http://127.0.0.1:9091", "--core-key-file", source)
        self.assertEqual((self.root / "secrets/core.key").read_bytes(), source.read_bytes())
        self.assertEqual(sorted(path.name for path in (self.root / "secrets").iterdir()), ["core.key"])
        self.assertFalse((self.root / "state").exists())
        self.assertEqual(self.document("state.json")["core_installation_id"], self.host.core_installation_id)
        web = self.document("generated/compose.json")["services"]["web"]
        self.assertEqual(set(self.document("generated/compose.json")["services"]), {"web"})
        self.assertEqual((web["network_mode"], web["environment"]["CORE_CONSOLE_UPSTREAM"]), ("host", "http://127.0.0.1:9091"))
        self.assertNotIn(source.read_text(), self.output.getvalue())
        self.assertFalse(any(command[:2] == ["docker", "load"] and not command[-1].endswith("web.tar")
                             for command in self.host.commands))

    def test_web_only_rejects_exposed_or_malformed_core_key_files(self):
        link = self.work / "linked.key"
        link.symlink_to(self.key_file())
        for source in [self.key_file(contents, mode) for contents, mode in (
                ("synthetic-token-0123456789abcdefghij", 0o644), ("", 0o600), ("two tokens", 0o600),
                ("x" * 4097, 0o600), ("x" * 31, 0o600))] + [link]:
            with self.subTest(source=source), self.assertRaises(install.InstallError):
                self.install("--web-only", "--core-url", "http://localhost:8091", "--core-key-file", source)
            self.assertFalse(self.root.exists())

    def test_native_core_migrates_before_enabling_its_generated_unit(self):
        self.install("--native-core")
        config = self.document("config.json")
        self.assertTrue(config["native_core"])
        self.assertIn("database", config["ports"])
        commands = self.host.commands
        migrate = commands.index([str(self.root / "native/bin/agents-api-migrate")])
        unit = self.root / "generated/parsar-{}-core.service".format(self.document("state.json")["project"][7:])
        self.assertLess(migrate, commands.index(["systemctl", "--user", "enable", "--now", str(unit)]))
        self.assertEqual(set(self.document("generated/compose.json")["services"]), {"database", "web"})
        self.assertTrue(self.host.native["active"])

    def test_local_provider_flags_and_enrollment(self):
        for flags, message in ((("--provider", "docker"), "--provider requires"),
                               (("--web-only", "--sandbox-provider", "true"), "cannot install a sandbox provider"),
                               (("--sandbox-provider", "true", "--public-url", "http://localhost:8080"), "requires public_url with HTTPS")):
            with self.subTest(flags=flags), self.assertRaisesRegex(install.InstallError, message):
                self.install(*flags)
            self.assertFalse(self.root.exists())
        with mock.patch.object(install.local_node, "install") as enroll:
            self.install("--sandbox-provider", "true", "--provider", "docker", "--public-url", "https://core.example")
        state = enroll.call_args.args[1]
        self.assertEqual((state["provider"], state["core_port"], state["public_url"]), ("docker", 8091, "https://core.example"))
        self.assertNotIn("provider", self.document("config.json"))

    def test_node_payload_exports_only_matched_distribution_files(self):
        self.install()
        payload = self.root / "node-payload"
        exported = {str(path.relative_to(payload)) for path in payload.rglob("*") if path.is_file()}
        self.assertEqual(exported, {"node-install.pyz", "self-hosted-install.pyz", "manifest.json", "SHA256SUMS",
                                    "runtime/seccomp.json"})
        (payload / "node-install.pyz").write_text("changed")
        with self.assertRaisesRegex(install.InstallError, "node payload differs"):
            install.prepare_node_payload(self.root, self.document("state.json"), self.bundle)

    def test_bundle_verifies_transferred_bytes_and_checksum_list(self):
        self.assertEqual(install.verify_bundle(self.bundle)["source_commit"], "a" * 40)
        for name in ("images/core.tar", "manifest.json", "parsar.pyz", "config.schema.json"):
            with self.subTest(name=name):
                path = self.bundle / name
                original = path.read_bytes()
                path.write_bytes(original + b"modified")
                with self.assertRaises(install.InstallError):
                    install.verify_bundle(self.bundle)
                path.write_bytes(original)
        checksums = self.bundle / "SHA256SUMS"
        original = checksums.read_text()
        checksums.write_text("".join(line + "\n" for line in original.splitlines() if not line.endswith("  parsar.pyz")))
        with self.assertRaisesRegex(install.InstallError, "incomplete"):
            install.verify_bundle(self.bundle)
        checksums.write_text(original + original.splitlines()[0] + "\n")
        with self.assertRaisesRegex(install.InstallError, "Duplicate"):
            install.verify_bundle(self.bundle)
        checksums.write_text(install.digest(self.key_file()) + "  ../existing-core.key\n")
        with self.assertRaisesRegex(install.InstallError, "Invalid distribution path"):
            install.verify_bundle(self.bundle)

    def test_wrong_image_identity_creates_no_installation(self):
        original = distribution.docker_command
        with mock.patch.object(distribution, "docker_command", side_effect=lambda arguments, **kwargs: (
                subprocess.CompletedProcess(arguments, 0, "sha256:" + "f" * 64 + " linux/amd64", "")
                if "inspect" in arguments else original(arguments, **kwargs))):
            with self.assertRaisesRegex(distribution.DistributionError, "identity or platform"):
                self.install()
        self.assertFalse(self.root.exists())

    def test_cli_failure_does_not_print_external_command_secrets(self):
        secret = "synthetic-sensitive-command-value"
        failure = subprocess.CalledProcessError(1, ["docker", secret], output=secret, stderr=secret)
        output = io.StringIO()
        with mock.patch.object(sys, "argv", [install.__file__, "--install-dir", str(self.root)]), \
                mock.patch.object(install.platform, "system", return_value="Linux"), \
                mock.patch.object(install.platform, "machine", return_value="x86_64"), \
                mock.patch.object(subprocess, "run", side_effect=failure), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(output), \
                self.assertRaises(SystemExit) as raised:
            runpy.run_path(install.__file__, run_name="__main__")
        self.assertEqual(raised.exception.code, 1)
        self.assertNotIn(secret, output.getvalue())
        self.assertFalse(self.root.exists())

    def test_origins_must_be_canonical_and_https_unless_loopback(self):
        for flag in ("--public-url", "--core-url"):
            for url in ("http://remote.example:8091", "https://user:synthetic-secret@core.example",
                        "https://core.example/?token=synthetic-secret", "https://core.example/"):
                output = io.StringIO()
                with self.subTest(flag=flag, url=url), contextlib.redirect_stderr(output), self.assertRaises(SystemExit):
                    install.arguments([flag, url])
                self.assertNotIn("synthetic-secret", output.getvalue())


class ComposePrerequisiteTests(unittest.TestCase):
    def test_compose_requires_the_literal_environment_parser(self):
        for version in ("2.26.0", "v2.26.1", "2.40.0-desktop.1", "5.0.0"):
            with self.subTest(version=version), mock.patch.object(install, "run", return_value=subprocess.CompletedProcess([], 0, version)):
                install.check_compose()
        for version in ("2.15.1", "2.25.9", "unknown"):
            with self.subTest(version=version), mock.patch.object(install, "run", return_value=subprocess.CompletedProcess([], 0, version)):
                with self.assertRaisesRegex(install.InstallError, "2.26.0"):
                    install.check_compose()


if __name__ == "__main__":
    unittest.main()
