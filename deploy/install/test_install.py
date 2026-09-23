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
        for name in ("install.py", "configuration.py", "native_service.py", "node_install.py", "install.sh"):
            shutil.copyfile(Path(__file__).with_name(name), bundle / name)
        for name in self.manifest["images"]:
            (bundle / "images" / (name + ".tar")).write_bytes(("synthetic " + name).encode())
        for name in ("bin/agents-api", "bin/agents-api-migrate", "bin/agents-api-microsandbox-provider",
                     "bin/parsar-sandbox-node", "microsandbox/msb", "microsandbox/libkrunfw.so.5.6.1"):
            path = bundle / "native" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"synthetic native file")
        self.write_checksums(bundle)
        return bundle

    @staticmethod
    def write_checksums(bundle):
        files = sorted(path for path in bundle.rglob("*") if path.is_file() and path.name != "SHA256SUMS")
        (bundle / "SHA256SUMS").write_text("".join(
            hashlib.sha256(path.read_bytes()).hexdigest() + "  " + str(path.relative_to(bundle)) + "\n"
            for path in files))

    def test_node_payload_exports_only_matched_distribution_files(self):
        state = self.initialize()
        bundle = self.bundle()
        install.prepare_node_payload(self.root, state, bundle)
        payload = self.root / "node-payload"
        exported = {str(path.relative_to(payload)) for path in payload.rglob("*") if path.is_file()}
        self.assertEqual(len(exported), 9)
        self.assertNotIn("config/caller.key", exported)
        self.assertNotIn("admin/sandbox-admin.key", exported)
        for name in exported:
            self.assertEqual((payload / name).read_bytes(), (bundle / name).read_bytes())
        install.prepare_node_payload(self.root, state, bundle)
        (payload / "node_install.py").write_text("changed")
        with self.assertRaises(install.InstallError):
            install.prepare_node_payload(self.root, state, bundle)

    def test_interrupted_payload_copy_can_be_retried(self):
        state = self.initialize()
        bundle = self.bundle()
        def interrupted(source, target):
            Path(target).write_bytes(b"partial")
            raise OSError("copy interrupted")
        with mock.patch.object(install.shutil, "copyfile", side_effect=interrupted):
            with self.assertRaises(OSError):
                install.prepare_node_payload(self.root, state, bundle)
        self.assertFalse((self.root / "node-payload/node_install.py").exists())
        install.prepare_node_payload(self.root, state, bundle)
        self.assertEqual((self.root / "node-payload/node_install.py").read_bytes(),
                         (bundle / "node_install.py").read_bytes())

    def test_repeat_installation_preserves_execution_identity_and_all_secrets(self):
        first = self.initialize()
        keys = self.document("config/keys.json")
        caller = (self.root / "config/caller.key").read_bytes()
        encryption = (self.root / "config/credential.key").read_text()
        self.assertEqual(hashlib.sha256(caller).hexdigest(), keys[0]["token_sha256"])
        self.assertEqual(len(base64.b64decode(encryption, validate=True)), 32)
        self.assertIsNone(first["provider"])
        self.assertFalse((self.root / "config/managed-runtimes.json").exists())
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
        for flags in (("--core-only",), ("--sandbox-provider", "true", "--provider", "docker"), ("--core-port", "8092"), ("--web-port", "8081")):
            with self.subTest(flags=flags), self.assertRaises(install.InstallError):
                self.initialize(*flags)
            self.assertEqual(before, self.snapshot())
        changed = dict(self.manifest, source_commit="b" * 40)
        with self.assertRaises(install.InstallError):
            install.initialize(self.root, self.args(), changed)
        self.assertEqual(before, self.snapshot())

    def test_opt_in_microsandbox_keeps_core_and_vm_processes_outside_compose(self):
        state = self.initialize("--sandbox-provider", "true")
        self.assertEqual(state["provider"], "microsandbox")
        managed = self.document("config/managed-runtimes.json")
        self.assertNotIn("docker", managed)
        micro = managed["microsandbox"]
        self.assertEqual(micro["root_disk_mib"], 8192)
        self.assertEqual(micro["environment_disk_mib"], 8192)
        self.assertEqual(micro["network"]["default_ingress"], "deny")
        self.assertEqual(micro["network"]["default_egress"], "deny")
        self.assertEqual(micro["image"], self.manifest["runtime_ref"])
        self.assertEqual(micro["runtime_home"], str(self.root / "state/msb"))
        self.assertEqual(micro["helper_path"], str(self.root / "native/bin/agents-api-microsandbox-provider"))
        services = self.document("compose.json")["services"]
        self.assertEqual(set(services), {"database", "web"})
        for service in services.values():
            self.assertFalse(service.get("privileged", False))
            self.assertNotIn("devices", service)
            self.assertNotIn("docker.sock", json.dumps(service.get("volumes", [])))
        self.assertEqual(services["database"]["ports"], [f'127.0.0.1:{state["database_port"]}:5432'])
        web = services["web"]
        self.assertEqual(web["network_mode"], "host")
        self.assertEqual(web["environment"]["CORE_CONSOLE_ADDR"], "127.0.0.1:8080")
        self.assertEqual(web["environment"]["CORE_CONSOLE_UPSTREAM"], "http://127.0.0.1:8091")
        self.assertTrue(web["read_only"])
        self.assertIn("no-new-privileges:true", web["security_opt"])

    def test_docker_provider_socket_and_runtime_network_belong_only_to_core(self):
        state = self.initialize("--sandbox-provider", "true", "--provider", "docker", "--core-only")
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

    def test_local_node_identity_has_persistent_private_state_only_on_core(self):
        for provider in ("docker", "microsandbox"):
            with self.subTest(provider=provider):
                self.root = self.work / provider
                state = self.initialize("--sandbox-provider", "true", "--provider", provider)
                directory = self.root / "state/sandbox-node"
                self.assertEqual(stat.S_IMODE(directory.stat().st_mode), 0o700)
                identity = directory / "identity.json"
                install.private_write(identity, "synthetic persistent node identity")
                before = self.snapshot()
                self.initialize("--sandbox-provider", "true", "--provider", provider)
                self.assertEqual(self.snapshot(), before)
                environment = install.core_environment(self.root, state, "synthetic database password")
                expected = str(directory) if provider == "microsandbox" else "/state/sandbox-node"
                self.assertEqual(environment["AGENTS_API_SANDBOX_NODE_STATE_DIR"], expected)
                services = self.document("compose.json")["services"]
                for name, service in services.items():
                    mounts = service.get("volumes", [])
                    node_mounts = [m for m in mounts if isinstance(m, dict) and m["target"] == "/state/sandbox-node"]
                    self.assertEqual(len(node_mounts), 1 if name == "core" else 0)
                    if node_mounts:
                        self.assertEqual(node_mounts[0]["source"], str(directory))
                        self.assertFalse(node_mounts[0]["read_only"])
                        self.assertTrue(service["read_only"])
                    for mount in mounts:
                        if isinstance(mount, dict) and mount["target"] == "/config":
                            self.assertTrue(mount["read_only"])

    def test_sandbox_admin_credential_is_separate_and_belongs_only_to_core(self):
        for provider in (None, "docker", "microsandbox"):
            with self.subTest(provider=provider):
                self.root = self.work / ("admin-" + str(provider))
                flags = ("--sandbox-provider", "true", "--provider", provider) if provider else ()
                state = self.initialize(*flags)
                admin = self.root / "admin"
                token = (admin / "sandbox-admin.key").read_text()
                self.assertEqual(self.document("admin/digests.json"), [hashlib.sha256(token.encode()).hexdigest()])
                self.assertNotEqual(token, (self.root / "config/caller.key").read_text())
                self.assertNotEqual(token, (self.root / "config/console.password").read_text())
                self.assertEqual(stat.S_IMODE(admin.stat().st_mode), 0o700)
                for path in admin.iterdir():
                    self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
                before = self.snapshot()
                self.initialize(*flags)
                self.assertEqual(self.snapshot(), before)
                environment = install.core_environment(self.root, state, "synthetic database password")
                expected = str(admin / "digests.json") if provider == "microsandbox" else "/admin/digests.json"
                self.assertEqual(environment["AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE"], expected)
                for name, service in self.document("compose.json")["services"].items():
                    mounts = [m for m in service.get("volumes", []) if isinstance(m, dict) and m["target"].startswith("/admin")]
                    self.assertEqual(len(mounts), 1 if name in ("core", "web") else 0)
                    if mounts:
                        filename = "digests.json" if name == "core" else "sandbox-admin.key"
                        self.assertEqual(mounts[0]["source"], str(admin / filename))
                        self.assertEqual(mounts[0]["target"], "/admin/" + filename)
                        self.assertTrue(mounts[0]["read_only"])
                    for mount in service.get("volumes", []):
                        if isinstance(mount, dict) and name != "web":
                            source = Path(mount["source"])
                            self.assertFalse((admin / "sandbox-admin.key").is_relative_to(source))
                    if name != "core":
                        self.assertNotIn("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE", service.get("environment", {}))
                    self.assertNotIn(token, json.dumps(service))

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
        for name in ("images/core.tar", "images/runtime.tar", "manifest.json", "native/bin/parsar-sandbox-node"):
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
        for name in ("images/runtime.tar", "native/bin/parsar-sandbox-node"):
            checksums.write_text("".join(line + "\n" for line in original.splitlines()
                                        if not line.endswith("  " + name)))
            with self.subTest(name=name), self.assertRaises(install.InstallError):
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

    def test_default_has_no_provider_authority_or_runtime_configuration(self):
        state = self.initialize()
        self.assertIsNone(state["provider"])
        self.assertEqual(self.device_probes, [])
        self.assertNotIn("device_gid", state)
        self.assertFalse((self.root / "state").exists())
        self.assertTrue((self.root / "admin/digests.json").is_file())
        self.assertFalse((self.root / "config/managed-runtimes.json").exists())
        compose = self.document("compose.json")
        self.assertEqual(set(compose["services"]), {"database", "migrate", "core", "web"})
        self.assertNotIn("networks", compose)
        self.assertEqual(compose["services"]["core"]["environment"]["AGENTS_API_SANDBOX_INSTALLATION_ID"], state["installation_id"])
        for service in compose["services"].values():
            self.assertNotIn("devices", service)
            self.assertNotIn("group_add", service)
            self.assertNotIn("docker.sock", json.dumps(service.get("volumes", [])))
            self.assertNotIn("AGENTS_API_MANAGED_RUNTIMES_FILE", service.get("environment", {}))
            self.assertNotIn("AGENTS_API_SANDBOX_NODE_STATE_DIR", service.get("environment", {}))

    def test_public_origin_is_explicit_and_preserved(self):
        state = self.initialize("--public-url", "https://core.example")
        web = self.document("compose.json")["services"]["web"]
        self.assertEqual(web["environment"]["CORE_CONSOLE_ORIGIN"], "https://core.example")
        self.assertEqual(web["ports"], ["127.0.0.1:8080:8080"])
        self.assertEqual(state["public_url"], "https://core.example")
        with self.assertRaises(install.InstallError):
            self.initialize("--public-url", "https://other.example")

    def test_provider_requires_explicit_enablement_and_cannot_belong_to_web_only(self):
        self.assertIsNone(self.args("--sandbox-provider", "false").provider)
        self.assertEqual(self.args("--sandbox-provider").provider, "microsandbox")
        for flags in (("--provider", "docker"), ("--provider", "microsandbox"),
                      ("--sandbox-provider", "false", "--provider", "docker"),
                      ("--web-only", "--sandbox-provider", "true")):
            with self.subTest(flags=flags), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                self.args(*flags)

    def test_default_main_skips_kvm_native_service_and_runtime_import(self):
        bundle = self.bundle()
        calls = []
        output = io.StringIO()
        with mock.patch.object(install, "__file__", str(bundle / "install.py")), \
                mock.patch.object(install.platform, "system", return_value="Linux"), \
                mock.patch.object(install.platform, "machine", return_value="x86_64"), \
                mock.patch.object(install, "run", side_effect=lambda args, **kw: calls.append(args)), \
                mock.patch.object(install, "wait_http", return_value=True), \
                mock.patch.object(install.native_service, "preflight", side_effect=AssertionError("native preflight on default")), \
                mock.patch.object(install.native_service, "prepare", side_effect=AssertionError("native install on default")), \
                contextlib.redirect_stdout(output):
            install.main(["--install-dir", str(self.root)])
        self.assertEqual(self.device_probes, [])
        self.assertEqual([call[-1] for call in calls if call[:2] == ["docker", "load"]],
                         [str(bundle / ("images/" + name + ".tar")) for name in ("core", "database", "web")])
        self.assertFalse((self.root / "native").exists())
        self.assertFalse((self.root / "config/seccomp.json").exists())
        self.assertIn("No execution node installed", output.getvalue())

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

    def test_status_rejects_failed_database_even_while_http_processes_are_alive(self):
        services = [{"Service": name, "State": "running", "Health": ""}
                    for name in ("database", "core", "web")]
        for database in ({"State": "running", "Health": "unhealthy"}, {"State": "exited"}, None):
            rows = services[1:] + ([{**services[0], **database}] if database else [])
            with self.subTest(database=database), \
                    mock.patch.object(install, "compose", return_value=SimpleNamespace(stdout=json.dumps(rows))), \
                    mock.patch.object(install, "wait_http", return_value=True), \
                    contextlib.redirect_stdout(io.StringIO()), self.assertRaises(install.InstallError):
                install.status(self.root, {"mode": "all", "provider": "docker", "core_port": 8091, "web_port": 8080})

    def test_status_accepts_web_only_without_database_or_core_services(self):
        rows = [{"Service": "web", "State": "running", "Health": ""}]
        with mock.patch.object(install, "compose", return_value=SimpleNamespace(stdout=json.dumps(rows))), \
                mock.patch.object(install, "wait_http", return_value=True), \
                contextlib.redirect_stdout(io.StringIO()):
            install.status(self.root, {"mode": "web-only", "provider": "microsandbox", "web_port": 8080})


if __name__ == "__main__":
    unittest.main()
