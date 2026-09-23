#!/usr/bin/env python3
"""Check installation state and deployment boundaries without starting Docker."""

import base64
import contextlib
import hashlib
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
from types import SimpleNamespace
import unittest
from unittest import mock

import install


class InstallerTests(unittest.TestCase):
    def setUp(self):
        temporary_root = Path.home() / ".parsar/tests/install"
        temporary_root.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix="unit-", dir=temporary_root)
        self.addCleanup(self.temporary.cleanup)
        self.work = Path(self.temporary.name).resolve()
        self.root = self.work / "deployment"
        self.manifest = {
            "source_commit": "a" * 40,
            "images": {name: "sha256:" + digit * 64 for name, digit in (
                ("core", "1"), ("runtime", "2"), ("database", "3"), ("web", "4"))},
            "runtime_ref": "localhost/parsar-runtime:test-install",
            "microsandbox": {"runtime_sha256": "5" * 64, "firmware_sha256": "6" * 64},
        }
        self.ports = self.patched(mock.patch.object(install, "free_port"))
        self.device_probes = []
        original_stat = os.stat

        def controlled_device_stat(name, *args, **kwargs):
            if os.fspath(name) in ("/dev/kvm", "/var/run/docker.sock"):
                self.device_probes.append(os.fspath(name))
                return SimpleNamespace(st_gid=1234, st_mode=stat.S_IFCHR | 0o660)
            return original_stat(name, *args, **kwargs)

        self.patched(mock.patch.object(install.os, "stat", side_effect=controlled_device_stat))
        self.patched(mock.patch.object(install.os, "getuid", return_value=1000))
        self.patched(mock.patch.object(install.os, "getgid", return_value=1000))

    def patched(self, patcher):
        result = patcher.start()
        self.addCleanup(patcher.stop)
        return result

    def args(self, *values):
        return install.arguments(["--install-dir", str(self.root), *values])

    def initialize(self, *values):
        return install.initialize(self.root, self.args(*values), self.manifest)

    def document(self, name):
        return json.loads((self.root / name).read_text())

    def snapshot(self):
        return {
            str(path.relative_to(self.root)): (stat.S_IMODE(path.stat().st_mode),
                path.read_bytes() if path.is_file() else None)
            for path in [self.root, *self.root.rglob("*")]
        }

    def caller_file(self, contents="synthetic-existing-core-token", mode=0o600):
        path = self.work / "existing-core.key"
        path.write_text(contents)
        path.chmod(mode)
        return path

    def bundle(self):
        bundle = self.work / "bundle"
        (bundle / "images").mkdir(parents=True)
        (bundle / "runtime").mkdir()
        (bundle / "manifest.json").write_text(json.dumps(self.manifest))
        (bundle / "runtime/seccomp.json").write_text('{"defaultAction":"SCMP_ACT_ERRNO"}')
        for name in ("install.py", "configuration.py", "install.sh"):
            shutil.copyfile(Path(__file__).with_name(name), bundle / name)
        for name in self.manifest["images"]:
            (bundle / "images" / (name + ".tar")).write_bytes(("synthetic " + name).encode())
        self.write_checksums(bundle)
        return bundle

    @staticmethod
    def write_checksums(bundle):
        files = sorted(path for path in bundle.rglob("*") if path.is_file() and path.name != "SHA256SUMS")
        (bundle / "SHA256SUMS").write_text("".join(
            hashlib.sha256(path.read_bytes()).hexdigest() + "  " + str(path.relative_to(bundle)) + "\n"
            for path in files))

    def test_repeat_installation_preserves_execution_identity_and_all_secrets(self):
        first = self.initialize()
        keys = self.document("config/keys.json")
        caller = (self.root / "config/caller.key").read_bytes()
        encryption = (self.root / "config/credential.key").read_text()
        self.assertEqual(hashlib.sha256(caller).hexdigest(), keys[0]["token_sha256"])
        self.assertEqual(len(base64.b64decode(encryption, validate=True)), 32)
        self.assertEqual(first["installation_id"], self.document("config/managed-runtimes.json")["installation_id"])
        before = self.snapshot()
        self.ports.reset_mock()
        # Re-running against already listening services must not reserve their ports.
        self.ports.side_effect = AssertionError("repeat installation re-probed an occupied port")
        second = self.initialize()
        self.assertEqual(first, second)
        self.assertEqual(before, self.snapshot())
        self.assertEqual(keys, self.document("config/keys.json"))

    def test_configuration_changes_refuse_without_mutating_existing_deployment(self):
        self.initialize()
        before = self.snapshot()
        for flags in (("--core-only",), ("--provider", "docker"), ("--core-port", "8092"), ("--web-port", "8081")):
            with self.subTest(flags=flags), self.assertRaises(install.InstallError):
                self.initialize(*flags)
            self.assertEqual(before, self.snapshot())
        changed = dict(self.manifest, source_commit="b" * 40)
        with self.assertRaises(install.InstallError):
            install.initialize(self.root, self.args(), changed)
        self.assertEqual(before, self.snapshot())

    def test_default_microsandbox_exposes_only_kvm_to_core(self):
        state = self.initialize()
        self.assertEqual(state["provider"], "microsandbox")
        managed = self.document("config/managed-runtimes.json")
        self.assertIn("microsandbox", managed)
        self.assertNotIn("docker", managed)
        self.assertEqual(managed["microsandbox"]["network"]["default_ingress"], "deny")
        self.assertEqual(managed["microsandbox"]["network"]["default_egress"], "deny")
        self.assertEqual(managed["microsandbox"]["image"], self.manifest["runtime_ref"])
        services = self.document("compose.json")["services"]
        self.assertEqual(set(services), {"core", "database", "migrate", "web"})
        self.assertEqual(services["core"]["devices"], ["/dev/kvm:/dev/kvm"])
        self.assertIn("1234", services["core"]["group_add"])
        for name, service in services.items():
            self.assertFalse(service.get("privileged", False))
            self.assertNotIn("docker.sock", json.dumps(service.get("volumes", [])))
            if name != "core":
                self.assertNotIn("devices", service)
        self.assertNotIn("ports", services["database"])
        for name in ("core", "migrate", "web"):
            self.assertEqual(services[name]["user"], "1000:1000")
            self.assertTrue(services[name]["read_only"])
            self.assertIn("no-new-privileges:true", services[name]["security_opt"])
        for name in ("core", "web"):
            self.assertTrue(all(port.startswith("127.0.0.1:") for port in services[name]["ports"]))
        self.assertEqual(services["core"]["depends_on"]["migrate"]["condition"], "service_completed_successfully")

    def test_docker_provider_socket_and_runtime_network_belong_only_to_core(self):
        state = self.initialize("--provider", "docker", "--core-only")
        managed = self.document("config/managed-runtimes.json")
        self.assertNotIn("microsandbox", managed)
        self.assertEqual(managed["docker"]["host"], "unix:///var/run/docker.sock")
        self.assertEqual(managed["docker"]["image"], self.manifest["images"]["runtime"])
        self.assertTrue(managed["docker"]["nested_sandbox"])
        compose = self.document("compose.json")
        services = compose["services"]
        self.assertNotIn("web", services)
        self.assertFalse((self.root / "config/console.password").exists())
        self.assertEqual(compose["networks"]["runtime"]["name"], managed["docker"]["network"])
        self.assertEqual(managed["installation_id"], state["installation_id"])
        for name, service in services.items():
            self.assertNotIn("devices", service)
            sockets = [mount for mount in service.get("volumes", [])
                       if isinstance(mount, dict) and mount["target"] == "/var/run/docker.sock"]
            self.assertEqual(len(sockets), 1 if name == "core" else 0)
            self.assertEqual("runtime" in service.get("networks", []), name == "core")
        self.assertNotIn("/dev/kvm", self.device_probes)

    def test_web_only_uses_existing_local_core_without_database_or_provider(self):
        source = self.caller_file()
        flags = ("--web-only", "--core-url", "http://127.0.0.1:9091", "--core-token-file", str(source))
        state = self.initialize(*flags)
        self.assertEqual(self.device_probes, [])
        self.assertEqual(self.ports.call_args_list, [mock.call(8080)])
        compose = self.document("compose.json")
        self.assertEqual(set(compose["services"]), {"web"})
        self.assertNotIn("volumes", compose)
        web = compose["services"]["web"]
        self.assertEqual(web["network_mode"], "host")
        self.assertEqual(web["environment"]["CORE_CONSOLE_ADDR"], "127.0.0.1:8080")
        self.assertEqual(web["environment"]["CORE_CONSOLE_UPSTREAM"], "http://127.0.0.1:9091")
        self.assertNotIn("ports", web)
        self.assertNotIn("devices", web)
        self.assertEqual({path.name for path in (self.root / "config").iterdir()}, {"caller.key", "console.password"})
        self.assertEqual((self.root / "config/caller.key").read_bytes(), source.read_bytes())
        before = self.snapshot()
        self.assertEqual(state, self.initialize(*flags))
        self.assertEqual(before, self.snapshot())

    def test_private_files_are_owner_only_even_under_permissive_umask(self):
        previous = os.umask(0)
        try:
            self.initialize()
        finally:
            os.umask(previous)
        for path in [self.root, *self.root.rglob("*")]:
            with self.subTest(path=path.relative_to(self.root)):
                expected = 0o700 if path.is_dir() else 0o600
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), expected)
        self.assertNotEqual((self.root / "config/caller.key").read_bytes(),
                            (self.root / "config/console.password").read_bytes())

    def test_web_only_rejects_exposed_or_malformed_caller_files(self):
        for contents, mode in (("synthetic-token", 0o644), ("", 0o600), ("two tokens", 0o600),
                               ("token\x00", 0o600), ("x" * 4097, 0o600)):
            with self.subTest(contents=contents, mode=mode):
                source = self.caller_file(contents, mode)
                with self.assertRaises(install.InstallError):
                    self.initialize("--web-only", "--core-url", "http://localhost:8091",
                                    "--core-token-file", str(source))
                self.assertFalse(self.root.exists(), "invalid input left a non-retryable partial deployment")

    def test_web_only_rejects_directory_or_symlink_as_caller_file(self):
        source = self.caller_file()
        link = self.work / "linked.key"
        link.symlink_to(source)
        private_directory = self.work / "directory.key"
        private_directory.mkdir(mode=0o700)
        for path in (link, private_directory):
            with self.subTest(path=path.name), self.assertRaises(install.InstallError):
                self.initialize("--web-only", "--core-url", "http://localhost:8091",
                                "--core-token-file", str(path))
            self.assertFalse(self.root.exists())

    def test_bundle_verifies_transferred_bytes_before_trusting_manifest(self):
        bundle = self.bundle()
        self.assertEqual(install.verify_bundle(bundle), self.manifest)
        for name in ("images/core.tar", "images/runtime.tar", "manifest.json"):
            with self.subTest(name=name):
                path = bundle / name
                original = path.read_bytes()
                path.write_bytes(original + b"modified")
                with self.assertRaises(install.InstallError):
                    install.verify_bundle(bundle)
                path.write_bytes(original)
                path.unlink()
                with self.assertRaises(install.InstallError):
                    install.verify_bundle(bundle)
                path.write_bytes(original)

    def test_bundle_rejects_omitted_or_duplicate_checksum_entries(self):
        bundle = self.bundle()
        checksums = bundle / "SHA256SUMS"
        original = checksums.read_text()
        checksums.write_text("".join(line + "\n" for line in original.splitlines()
                                    if not line.endswith("  images/runtime.tar")))
        with self.assertRaises(install.InstallError):
            install.verify_bundle(bundle)
        checksums.write_text(original + original.splitlines()[0] + "\n")
        with self.assertRaises(install.InstallError):
            install.verify_bundle(bundle)

    def test_bundle_rejects_paths_outside_distribution(self):
        bundle = self.bundle()
        outside = self.caller_file()
        checksums = bundle / "SHA256SUMS"
        checksums.write_text(install.digest(outside) + "  ../existing-core.key\n")
        with self.assertRaises(install.InstallError):
            install.verify_bundle(bundle)

    def test_main_web_only_never_imports_runtime_or_leaks_caller_password(self):
        source = self.caller_file()
        bundle = self.bundle()
        calls = []

        def external_command(args, **_kwargs):
            calls.append(args)
            return SimpleNamespace(stdout="", returncode=0)

        output = io.StringIO()
        with mock.patch.object(install, "__file__", str(bundle / "install.py")), \
                mock.patch.object(install.platform, "system", return_value="Linux"), \
                mock.patch.object(install.platform, "machine", return_value="x86_64"), \
                mock.patch.object(install, "run", side_effect=external_command), \
                mock.patch.object(install, "wait_http", return_value=True) as health, \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            install.main(["--install-dir", str(self.root), "--web-only", "--core-url",
                          "http://127.0.0.1:9091", "--core-token-file", str(source)])
        self.assertEqual(self.device_probes, [])
        imports = [call for call in calls if call[:2] == ["docker", "load"]]
        self.assertEqual(imports, [["docker", "load", "--input", str(bundle / "images/web.tar")]])
        self.assertFalse(any(call[:2] == ["docker", "run"] for call in calls))
        self.assertEqual([call.args[0] for call in health.call_args_list], ["http://127.0.0.1:8080/v1/agents"])
        for path in (self.root / "config").iterdir():
            self.assertNotIn(path.read_text(), output.getvalue())

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

    def test_core_connection_rejects_remote_cleartext_and_embedded_credentials(self):
        source = self.caller_file()
        for url in ("http://remote.example:8091", "https://user:synthetic-secret@core.example",
                    "https://core.example/?token=synthetic-secret"):
            output = io.StringIO()
            with self.subTest(url=url), contextlib.redirect_stderr(output), self.assertRaises(SystemExit):
                self.args("--web-only", "--core-url", url, "--core-token-file", str(source))
            self.assertNotIn("synthetic-secret", output.getvalue())


if __name__ == "__main__":
    unittest.main()
