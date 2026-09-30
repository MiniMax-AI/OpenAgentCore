#!/usr/bin/env python3
"""install.sh acceptance behavior: fresh install, repair and bundle checks."""

import contextlib
import errno
import io
import json
import os
from pathlib import Path
import runpy
import signal
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import config_model
import distribution
import ingress
import ingress_config
import install
import node_spec
from installer_fakes import MANIFEST, STANDARD_SIZES, FakeHost, make_bundle, run_installer

REAL_RUN = subprocess.run

BUILD = "base:0f6c1e8e-7d3a-4b8e-9a51-2b7f7f0c9d11"


class InstallerTests(unittest.TestCase):
    def setUp(self):
        temporary_root = Path.home() / ".oac/tests/install"
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
        # These lifecycle tests exercise operator-managed reverse proxies. The
        # managed gateway has end-to-end setup/rollback coverage in test_ingress.
        if not (self.root / "state.json").exists() and "--config" not in flags:
            flags = (*flags, "--ingress", "external")
        with contextlib.redirect_stdout(self.output), contextlib.redirect_stderr(self.output):
            run_installer(install, self.bundle, ["--install-dir", self.root, *flags])

    def document(self, name):
        return json.loads((self.root / name).read_text())

    def snapshot(self):
        return {str(path.relative_to(self.root)): (stat.S_IMODE(path.stat().st_mode),
                path.read_bytes() if path.is_file() else None)
                for path in [self.root, *self.root.rglob("*")] if path.name != ".oac.lock"}

    def key_file(self, contents="synthetic-existing-core-key-0123456789", mode=0o600):
        path = self.work / "existing-core.key"
        path.write_text(contents)
        path.chmod(mode)
        return path

    def test_catalog_installed_for_container_and_native_core(self):
        import hashlib
        native = self.bundle / "native-installers"
        native.mkdir()
        raw = b"offline native archive"
        catalog = {"version": self.manifest["source_commit"], "artifacts": {
            "linux-amd64": {"sha256": hashlib.sha256(raw).hexdigest()}}}
        (native / "catalog.json").write_text(json.dumps(catalog))
        # The installer checks the outer checksum inventory before copying.
        with (self.bundle / "SHA256SUMS").open("a") as sums:
            for path in native.iterdir():
                sums.write(distribution.digest(path) + "  " + str(path.relative_to(self.bundle)) + "\n")
        self.install()
        first = self.document("generated/compose.json")["services"]["core"]["labels"]
        (native / "linux-amd64.tar.gz").write_bytes(raw)
        self.install()
        second = self.document("generated/compose.json")["services"]["core"]["labels"]
        self.assertNotEqual(first, second)
        (native / "linux-amd64.tar.gz").unlink()
        self.install()
        self.assertEqual(second, self.document("generated/compose.json")["services"]["core"]["labels"])
        installed = self.root / "native-installers"
        self.assertEqual((installed / "linux-amd64.tar.gz").read_bytes(), raw)
        compose = self.document("generated/compose.json")
        mount = next(m for m in compose["services"]["core"]["volumes"] if m["target"] == "/opt/oac/native-installers")
        self.assertTrue(mount["read_only"])
        self.assertEqual(mount["source"], str(installed))
        config = self.document("config.json")
        config["native_core"] = True
        config["ports"]["database"] = 5432
        environment = install.configuration.core_environment(self.root, config, self.document("state.json"))
        self.assertEqual(environment["OAC_NATIVE_INSTALLER_DIR"], str(installed))

    def test_host_check_accepts_current_account_including_root(self):
        for uid in (0, 1000):
            with self.subTest(uid=uid), mock.patch.object(install.os, "getuid", return_value=uid), \
                    mock.patch.object(install.platform, "system", return_value="Linux"), \
                    mock.patch.object(install.platform, "machine", return_value="x86_64"):
                self.host.commands.clear()
                install.check_host()
                self.assertEqual(self.host.commands, [
                    ["docker", "compose", "version", "--short"],
                    ["docker", "info", "--format", "{{.ServerVersion}}"],
                ])

    def test_host_failures_explain_the_required_action_without_secrets(self):
        for command, failure, message in (
                ("compose", FileNotFoundError(), "Compose plugin"),
                ("info", PermissionError("private detail"), "current account"),
                ("info", subprocess.TimeoutExpired(["docker", "info"], 30), "current account")):
            with self.subTest(command=command, failure=failure):
                original = self.host.run
                def run(args, **kwargs):
                    if command in args:
                        raise failure
                    return original(args, **kwargs)
                with mock.patch.object(subprocess, "run", side_effect=run):
                    with self.assertRaisesRegex(install.InstallError, message) as raised:
                        self.install()
                self.assertNotIn("private detail", str(raised.exception))
                self.assertFalse(self.root.exists())

    def test_space_and_permission_errors_are_actionable_and_leave_no_installation(self):
        for error, message in ((OSError(errno.ENOSPC, "full"), "Disk space"),
                               (OSError(errno.EDQUOT, "quota"), "quota"),
                               (PermissionError("private detail"), "current account")):
            with self.subTest(error=error), mock.patch.object(install, "create", side_effect=error):
                with self.assertRaises(OSError) as raised:
                    self.install()
                self.assertIn(message, install.error_text(raised.exception))
                self.assertIn(install.NOTHING_KEPT, install.error_text(raised.exception))
                self.assertFalse(self.root.exists())

    def test_new_release_cleans_an_incomplete_installation_before_starting_again(self):
        self.install()
        state = self.document("state.json")
        previous_id = state["installation_id"]
        install.oac_cli.save_state(self.root, dict(state, complete=False, source_commit="b" * 40))
        self.install("--ingress", "external")
        self.assertNotEqual(self.document("state.json")["installation_id"], previous_id)
        self.assertTrue(self.document("state.json")["complete"])
        self.assertTrue(any("down" in command for command in self.host.commands))

    def test_root_identity_is_preserved_in_service_configuration(self):
        with mock.patch.object(install.os, "getuid", return_value=0), \
                mock.patch.object(install.os, "getgid", return_value=0):
            self.install("--sandbox", "none")
        state = self.document("state.json")
        self.assertEqual((state["uid"], state["gid"]), (0, 0))
        services = self.document("generated/compose.json")["services"]
        for name in ("core", "migrate", "web"):
            self.assertEqual(services[name]["user"], "0:0")
        self.assertNotIn("user", services["database"])
        self.assertEqual(self.host.running(), {"database", "core", "web"})

    def test_install_and_repair_hold_same_lock_before_all_writes(self):
        original_create = install.create
        original_finish = install.finish
        inodes = []

        def assert_busy():
            inodes.append((self.root / ".oac.lock").stat().st_ino)
            probe = "import fcntl,sys; f=open(sys.argv[1], 'r+');\ntry: fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)\nexcept BlockingIOError: sys.exit(73)"
            result = REAL_RUN([sys.executable, "-c", probe, str(self.root / ".oac.lock")],
                                    capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 73, result.stderr)
            with self.assertRaisesRegex(install.oac_cli.OacError, "Another oac"):
                self.install()
            with self.assertRaisesRegex(install.oac_cli.OacError, "Another oac"):
                with install.oac_cli.locked(self.root):
                    self.fail("A second operation acquired the installation lock")

        def create(*args, **kwargs):
            assert_busy()
            return original_create(*args, **kwargs)

        def finish(*args, **kwargs):
            assert_busy()
            return original_finish(*args, **kwargs)

        with mock.patch.object(install, "create", create), mock.patch.object(install, "finish", finish):
            self.install()
            before = self.document("state.json")
            keys = {p.name: p.read_bytes() for p in (self.root / "secrets").iterdir()}
            self.install()
        self.assertEqual(len(set(inodes)), 1)
        self.assertEqual(before["installation_id"], self.document("state.json")["installation_id"])
        self.assertEqual(keys, {p.name: p.read_bytes() for p in (self.root / "secrets").iterdir()})

    def test_repair_contention_changes_no_installation_bytes(self):
        self.install()
        before = self.snapshot()
        with install.oac_cli.locked(self.root):
            with self.assertRaisesRegex(install.oac_cli.OacError, "Another oac"):
                self.install()
        self.assertEqual(before, self.snapshot())

    def test_an_interrupted_first_start_removes_what_it_created(self):
        self.root.mkdir()
        with mock.patch.object(install.oac_cli, "health", side_effect=KeyboardInterrupt), \
                self.assertRaises(KeyboardInterrupt) as raised:
            self.install()
        self.assertEqual(install.error_text(raised.exception), "interrupted\n" + install.NOTHING_KEPT)
        # The directory existed, so it stays, with only the lock that a waiting command may hold.
        self.assertEqual([path.name for path in self.root.iterdir()], [".oac.lock"])
        self.assertIn(["docker", "compose", "-p", self.host.project, "down", "--volumes", "--remove-orphans"],
                      self.host.commands)
        self.assertEqual(self.host.containers, {})

    def test_a_rerun_replaces_an_incomplete_installation(self):
        self.install("--sandbox", "none")
        for marker in (False, None):
            with self.subTest(marker=marker):
                old = self.host.project
                # A first start killed before completion; its services still hold their ports.
                state = dict(self.document("state.json"), complete=marker)
                if marker is None:
                    del state["complete"]
                install.oac_cli.save_state(self.root, state)
                for command in (install.oac_cli.start, install.oac_cli.apply, install.oac_cli.status):
                    with self.assertRaisesRegex(install.oac_cli.OacError, "did not finish installing. Rerun the installer"):
                        command(self.root, out=lambda _: None)
                with self.assertRaisesRegex(ingress.DomainError, "did not finish installing"):
                    ingress.prepare(self.root, "core.example", None)
                self.host.busy = {8080, 8091}
                remove = install.oac_cli.remove
                with mock.patch.object(install.oac_cli, "remove",
                                       side_effect=lambda *args, **kwargs: (remove(*args, **kwargs), self.host.busy.clear())):
                    self.install("--core-only", "--sandbox", "none")
                self.assertIn(["docker", "compose", "-p", old, "down", "--volumes", "--remove-orphans"], self.host.commands)
                self.assertEqual((self.document("config.json")["mode"], self.document("config.json")["ports"]["core"]),
                                 ("core-only", 8091))
                state = self.document("state.json")
                self.assertNotEqual(state["project"], old)
                self.assertTrue(state["complete"])
                self.assertEqual(self.host.running(), {"database", "core"})

    def test_removal_keeps_recovery_state_on_failure_and_finishes_despite_signals(self):
        self.install("--sandbox", "none")
        state = dict(self.document("state.json"), complete=False)
        install.oac_cli.save_state(self.root, state)
        command = (self.root / "oac").read_bytes()
        unlink = Path.unlink

        def fail_command(path, *args, **kwargs):
            if path == self.root / "oac":
                raise PermissionError("command unlink refused")
            return unlink(path, *args, **kwargs)

        with install.oac_cli.locked(self.root), mock.patch.object(Path, "unlink", fail_command), \
                self.assertRaisesRegex(install.oac_cli.OacError, "Removal did not finish"):
            install.oac_cli.remove(self.root, state, keep_root=True)
        self.assertEqual(self.document("state.json"), state)
        self.assertEqual((self.root / "oac").read_bytes(), command)
        self.assertEqual(install.layout(self.root), "incomplete")

        signals = (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)
        for signum in signals:
            previous = signal.signal(signum, install.interrupted)
            self.addCleanup(signal.signal, signum, previous)
        delivered = []

        def interrupt_command(path, *args, **kwargs):
            if path == self.root / "oac":
                self.assertFalse((self.root / "state.json").exists())
                for signum in signals:
                    signal.raise_signal(signum)
                    delivered.append(signum)
            return unlink(path, *args, **kwargs)

        with install.oac_cli.locked(self.root), mock.patch.object(Path, "unlink", interrupt_command):
            install.oac_cli.remove(self.root, state, keep_root=True)
        self.assertEqual(delivered, list(signals))
        self.assertEqual([path.name for path in self.root.iterdir()], [".oac.lock"])
        for signum in signals:
            self.assertIs(signal.getsignal(signum), install.interrupted)

    def test_manual_removal_command_keeps_automatic_compose_isolation(self):
        self.install("--sandbox", "none")
        state = self.document("state.json")
        foreign = self.work / "other-project"
        foreign.mkdir()
        (foreign / "compose.yaml").write_text("volumes:\n  database:\n    name: unrelated-database\n")
        binaries = self.work / "bin"
        binaries.mkdir()
        docker = binaries / "docker"
        docker.write_text(f"#!{sys.executable}\nimport json, os, sys\n"
                          "print(json.dumps({'cwd': os.getcwd(), 'args': sys.argv[1:], "
                          "'compose': [key for key in os.environ if key.startswith('COMPOSE_')], "
                          "'docker_host': os.environ.get('DOCKER_HOST')}))\n")
        docker.chmod(0o700)
        environment = dict(os.environ, COMPOSE_FILE=str(foreign / "compose.yaml"), COMPOSE_PROJECT_NAME="unrelated",
                           DOCKER_HOST="unix:///synthetic-docker.sock", PATH=str(binaries) + os.pathsep + os.environ["PATH"])
        with mock.patch.dict(os.environ, environment, clear=True):
            with install.oac_cli.locked(self.root), \
                    mock.patch.object(install.oac_cli, "run", side_effect=OSError("Docker unavailable")) as run, \
                    self.assertRaises(install.oac_cli.OacError) as raised:
                install.oac_cli.remove(self.root, state)
            self.assertEqual(run.call_args.kwargs["cwd"], "/")
            self.assertFalse(any(name.startswith("COMPOSE_") for name in run.call_args.kwargs["env"]))
            manual = str(raised.exception).splitlines()[1].strip()
            result = REAL_RUN(["sh", "-c", manual], cwd=foreign, capture_output=True, text=True, check=True)
        observed = json.loads(result.stdout)
        self.assertEqual(observed, {"cwd": "/", "args": run.call_args.args[0][1:], "compose": [],
                                    "docker_host": environment["DOCKER_HOST"]})
        self.assertEqual(self.document("state.json"), state)

    def test_a_directory_without_state_json_is_refused_and_untouched(self):
        key = self.root / "secrets/e2b.key"
        key.parent.mkdir(parents=True)
        key.write_text("synthetic-e2b-key-0123456789")
        key.chmod(0o600)
        before = self.snapshot()
        with self.assertRaisesRegex(install.InstallError, "not empty"):
            self.install("--sandbox", "e2b", "--e2b-api-key-file", key, "--e2b-template", BUILD,
                         "--public-url", "https://core.example")
        self.assertEqual(self.snapshot(), before)

    def test_a_complete_installation_is_never_removed(self):
        self.install("--sandbox", "none")
        before = self.snapshot()
        with self.assertRaisesRegex(install.InstallError, "configured by"):
            self.install("--core-only")
        self.host.containers.clear()
        self.host.core["fails"] = True
        with self.assertRaisesRegex(install.oac_cli.OacError, "config.json not applied"):
            self.install()
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(any("down" in command for command in self.host.commands))

    def test_fresh_web_refuses_old_core_before_creating_installation(self):
        self.host.remote_core["https://core.example"] = (404, None)
        with mock.patch.object(install, "check_host", side_effect=AssertionError("host touched")), \
                mock.patch.object(install, "create", side_effect=AssertionError("installation created")), \
                self.assertRaisesRegex(install.InstallError, "not supported;.*reinstall"):
            self.install("--web-only", "--core-url", "https://core.example", "--core-key-file", self.key_file())
        self.assertFalse(self.root.exists())

    def test_fresh_web_accepts_current_core_and_records_its_identity(self):
        self.host.remote_core["https://core.example"] = (200, self.host.core_installation_id)
        key = self.key_file()
        self.install("--web-only", "--core-url", "https://core.example", "--core-key-file", key)
        self.assertEqual(self.document("state.json")["core_installation_id"], self.host.core_installation_id)
        self.assertEqual((self.root / "secrets/core.key").read_text(), key.read_text())
        self.assertEqual(self.host.running(), {"web"})

    def test_web_repair_refuses_old_paired_core_before_payload_or_state_writes(self):
        self.host.remote_core["https://core.example"] = (200, self.host.core_installation_id)
        self.install("--web-only", "--core-url", "https://core.example", "--core-key-file", self.key_file())
        before = self.snapshot()
        self.host.remote_core["https://core.example"] = (404, None)
        with self.assertRaisesRegex(install.InstallError, "not supported;.*reinstall"):
            self.install()
        self.assertEqual(before, self.snapshot())

    def test_different_revision_refuses_before_mutation(self):
        self.install()
        state = self.document("state.json")
        state["source_commit"] = "b" * 40
        install.oac_cli.save_state(self.root, state)
        before = self.snapshot()
        with self.assertRaisesRegex(install.InstallError, "not supported;.*reinstall"):
            self.install()
        self.assertEqual(before, self.snapshot())

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
        names = {str(path.relative_to(self.root)) for path in self.root.rglob("*") if "node-payload" not in path.relative_to(self.root).parts[:-1]
                 and "runtime" not in path.parts}
        self.assertEqual(names, {
            ".oac.lock", "config.json", "state.json", "oac", "node-payload", "secrets", "secrets/core.key",
            "secrets/credential.key", "secrets/database.password", "generated", "generated/compose.json",
            "generated/core.env", "generated/core-key-digests.json", "generated/settings.json",
            "generated/config.schema.json", "state", "state/e2b"})
        for path in [self.root, *self.root.rglob("*")]:
            expected = 0o700 if path.is_dir() or path.name == "oac" else 0o600
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), expected, path)
        self.assertEqual(self.host.running(), {"database", "core", "web"})
        output = self.output.getvalue()
        for name in ("core.key", "credential.key", "database.password"):
            self.assertNotIn((self.root / "secrets" / name).read_text(), output)
        for line in ("Console: https://core.example", "API base URL: https://core.example/v1",
                     "Local-only API on this host: http://127.0.0.1:8091/v1",
                     f"Settings: {self.root / 'config.json'}", f"Apply settings: {self.root / 'oac'} apply"):
            self.assertIn("  " + line + "\n", output)

    def test_a_taken_explicit_port_fails_before_the_bundle_is_hashed(self):
        self.host.busy.update({("127.0.0.1", 18080), ("127.0.0.1", 8080)})
        # A loopback public URL names Web's port, so that port cannot move either.
        for flags, port, name in ((("--web-port", "18080"), 18080, "--web-port"),
                                  (("--public-url", "http://localhost:8080"), 8080, "--web-port and --public-url")):
            with self.subTest(flags=flags), \
                    mock.patch.object(install, "verify_bundle", side_effect=AssertionError("bundle hashed")), \
                    self.assertRaisesRegex(install.InstallError, rf"^Port {port} \({name}\) is already in use on 127.0.0.1. "
                                           rf"Free it or choose another port; find the process with: sudo ss -ltnp 'sport = :{port}'$"):
                self.install(*flags)
            self.assertFalse(self.root.exists())

    def test_taken_default_ports_move_to_the_next_free_port(self):
        self.host.busy.update({("0.0.0.0", 8080), ("0.0.0.0", 8091)})
        self.install()
        self.assertEqual(self.document("config.json")["ports"], {"core": 8092, "web": 8081})
        output = self.output.getvalue()
        self.assertIn("  Console: http://127.0.0.1:8081 (local only)\n", output)
        self.assertIn("  Port 8080 was in use; Web uses 8081.\n", output)
        self.assertIn("  Port 8091 was in use; Core uses 8092.\n", output)

    def test_managed_ingress_without_https_leaves_ports_80_and_443_alone(self):
        self.host.busy.add(("0.0.0.0", 80))
        with mock.patch.object(ingress_config, "preflight", return_value={"docker_socket": "/var/run/docker.sock", "docker_gid": 999}), \
                mock.patch.object(ingress_config, "reload"), contextlib.redirect_stdout(self.output):
            run_installer(install, self.bundle, ["--install-dir", self.root, "--sandbox", "none"])
        self.assertEqual(self.document("generated/compose.json")["services"]["gateway"]["ports"], ["0.0.0.0:8080:8080"])

    def test_managed_https_needs_ports_80_and_443(self):
        self.host.busy.add(("0.0.0.0", 80))
        with contextlib.redirect_stdout(self.output), self.assertRaisesRegex(
                install.InstallError, "^Automatic HTTPS needs ports 80 and 443, and port 80 is already in use on 0.0.0.0. "
                "Free it, or use an existing reverse proxy with --ingress external; find the process with: "
                "sudo ss -ltnp 'sport = :80'$"):
            run_installer(install, self.bundle, ["--install-dir", self.root, "--public-url", "https://core.example"])
        self.assertFalse(self.root.exists())

    def test_output_labels_public_and_local_addresses(self):
        cases = {
            "default": ([], ["Console: http://127.0.0.1:8080 (local only)",
                             "API base URL: http://127.0.0.1:8091/v1 (local only)",
                             "Use this key to sign in to Web.",
                             "Before adding nodes, configure a reachable HTTPS address",
                             "Add nodes: in Web, open Nodes and choose Add node"]),
            "core-only": (["--core-only"], ["API base URL: http://127.0.0.1:8091/v1 (local only)",
                                            "Create a Project and its API key through the Core management API:",
                                            "http://127.0.0.1:8091/core/v1 (local only)"]),
            "loopback": (["--public-url", "http://localhost:8080"], ["Console: http://localhost:8080 (local only)",
                                                                     "API base URL: http://127.0.0.1:8091/v1 (local only)"]),
        }
        for name, (flags, expected) in cases.items():
            with self.subTest(name=name):
                self.host.containers.clear()  # Each case installs on a host of its own.
                self.root, self.output = self.work / name, io.StringIO()
                self.install(*flags)
                output = " ".join(self.output.getvalue().split())
                for line in expected:
                    self.assertIn(line, output)
                self.assertIn(f"Core key file: {self.root / 'secrets/core.key'}", output)


    def test_no_change_repair_does_not_claim_service_health(self):
        self.install("--sandbox", "none")
        self.output = io.StringIO()
        original_http = install.oac_cli.http
        def unhealthy(url, *args, **kwargs):
            if url.endswith(("/healthz", "/console/auth")):
                return 503, None
            return original_http(url, *args, **kwargs)
        with mock.patch.object(install.oac_cli, "http", side_effect=unhealthy):
            self.install()
        output = self.output.getvalue()
        self.assertIn("Installation settings checked. Use Status below to inspect service health.", output)
        for misleading in ("Installation complete.", "Repair complete.", "checking their health"):
            self.assertNotIn(misleading, output)

    def test_rerun_reads_config_json_rejects_flags_and_repairs(self):
        self.install()
        before = self.snapshot()
        with self.assertRaisesRegex(install.InstallError, "config.json. Edit it and run .*oac apply"):
            self.install("--web-port", "8081")
        self.assertEqual(self.snapshot(), before)
        (self.root / "oac").unlink()
        config = self.document("config.json")
        config["ports"]["web"] = 8181
        (self.root / "config.json").write_text(json.dumps(config))
        self.host.recreated.clear()
        self.install()
        self.assertEqual((self.root / "oac").read_bytes(), (self.bundle / "oac.pyz").read_bytes())
        self.assertEqual(self.host.recreated, ["web"])
        other, _ = make_bundle(self.work / "other", MANIFEST, commit="b" * 40)
        with self.assertRaisesRegex(install.InstallError, "not supported;.*reinstall"):
            with contextlib.redirect_stdout(self.output):
                run_installer(install, other, ["--install-dir", self.root])

    def test_unsupported_state_format_refuses_before_lock_creation(self):
        self.root.mkdir()
        install.oac_cli.save_state(self.root, {"format": 1})
        before = self.snapshot()
        with self.assertRaisesRegex(install.oac_cli.OacError, "not supported;.*reinstall"):
            self.install()
        with self.assertRaisesRegex(install.oac_cli.OacError, "not supported;.*reinstall"):
            with install.oac_cli.locked(self.root):
                self.fail("Unsupported installation acquired the lock")
        self.assertEqual(before, self.snapshot())
        self.assertFalse((self.root / ".oac.lock").exists())

    def test_a_live_installation_missing_config_json_is_never_told_to_start_over(self):
        self.install()
        (self.root / "config.json").unlink()
        before = self.snapshot()
        with self.assertRaisesRegex(install.InstallError, "config.json is missing. Restore it from a backup; "
                                    ".*generated/settings.json lists the last applied values"):
            self.install()
        self.assertEqual(self.snapshot(), before)

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
        self.assertEqual((web["network_mode"], web["environment"]["OAC_WEB_UPSTREAM"]), ("host", "http://127.0.0.1:9091"))
        self.assertNotIn(source.read_text(), self.output.getvalue())
        self.assertIn("Console: http://127.0.0.1:8080 (local only)\n", self.output.getvalue())
        self.assertIn("Core key file: " + str(self.root / "secrets/core.key"), self.output.getvalue())
        self.assertIn("Create a Project and its API key", self.output.getvalue())
        self.assertNotIn("API base URL", self.output.getvalue())
        self.assertFalse(any(command[:2] == ["docker", "load"] and not command[-1].endswith("web.tar")
                             for command in self.host.commands))
        self.assertEqual(self.host.deployment_posts, [])

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
        migrate = commands.index([str(self.root / "native/bin/oac-core-migrate")])
        unit = self.root / ("generated/" + self.document("state.json")["project"] + "-core.service")
        self.assertLess(migrate, commands.index(["systemctl", "--user", "enable", "--now", str(unit)]))
        self.assertEqual(set(self.document("generated/compose.json")["services"]), {"database", "web"})
        self.assertTrue(self.host.native["active"])

    def test_a_new_installation_selects_microsandbox_at_web_standard_size(self):
        self.install("--public-url", "https://core.example")
        standard = json.loads(STANDARD_SIZES.read_text())
        self.assertEqual(self.host.deployment_posts, [{"provider": "microsandbox", "expected_generation": 0, "resources": standard["microsandbox"],
                                                       "runtime": node_spec.release(self.manifest)}])
        output = " ".join(self.output.getvalue().split())
        for message in ("Sandboxes: microsandbox, Standard (2 CPUs, 4 GiB).",
                        "Execution nodes need KVM (/dev/kvm). This host needs KVM only if you add it as a node.",
                        "Add nodes: in Web, open Nodes and choose Add node, then run the command on each execution host."):
            self.assertIn(message, output)
        self.assertNotIn(install.DOCKER_RISKS, self.output.getvalue())
        self.assertNotIn("sandbox", json.dumps(self.document("config.json")))
        # A repair never selects again.
        self.host.deployment = {"provider": ""}
        self.install()
        self.assertEqual(len(self.host.deployment_posts), 1)
        self.root, self.output = self.work / "none", io.StringIO()
        self.install("--sandbox", "none")
        self.assertEqual(len(self.host.deployment_posts), 1)
        self.assertIn("Sandboxes: none chosen. Choose a sandbox backend on the Nodes page in Web.", self.output.getvalue())

    def install_docker(self, *flags, answer=None):
        """--sandbox docker; answer is what an interactive operator types, None without a terminal."""
        with mock.patch.object(install.sys, "stdin", mock.Mock(isatty=lambda: answer is not None)), \
                mock.patch("builtins.input", return_value=answer) as prompt:
            self.install("--sandbox", "docker", *flags)
        return prompt

    def test_docker_needs_confirmation_before_anything_is_created(self):
        for answer in (None, "", "n"):
            with self.subTest(answer=answer), self.assertRaisesRegex(install.InstallError, "Docker sandboxes were not "
                                                                     "confirmed; nothing was installed. Rerun with "
                                                                     "--accept-docker-risks"):
                self.install_docker("--public-url", "https://core.example", answer=answer)
            self.assertFalse(self.root.exists())
            self.assertEqual((self.host.commands, self.host.deployment_posts), ([], []))
        output = self.output.getvalue()
        for risk in ("share the node's kernel", "own microVM", "root-equivalent", "trusted workloads or for node hosts "
                     "without KVM"):
            self.assertIn(risk, output)
        standard = json.loads(STANDARD_SIZES.read_text())
        docker = {"provider": "docker", "expected_generation": 0, "resources": standard["docker"], "runtime": node_spec.release(self.manifest)}
        prompt = self.install_docker("--core-only", "--accept-docker-risks")
        prompt.assert_not_called()
        self.assertEqual(self.host.deployment_posts, [docker])
        self.root, self.host.deployment = self.work / "confirmed", {"provider": "", "generation": 0, "reset": None}
        prompt = self.install_docker(answer="y")
        prompt.assert_called_once_with("Use Docker sandboxes anyway? [y/N] ")
        self.assertEqual(self.host.deployment_posts, [docker, docker])
        self.assertIn("\n  Sandboxes: Docker, Standard (2 CPUs, 2 GiB).\n", self.output.getvalue())

    def test_e2b_needs_a_public_address_a_private_key_file_and_an_exact_build(self):
        secret = "synthetic-e2b-key-0123456789"
        key = self.key_file(secret)
        for flags, message in (((), "E2B needs an HTTPS public_url that is not loopback"),
                               (("--public-url", "http://localhost:8080"), "E2B needs an HTTPS public_url"),
                               (("--public-url", "https://core.example", "--e2b-template", "base"), "template-id:build-uuid")):
            with self.subTest(flags=flags), self.assertRaisesRegex(install.InstallError, message):
                self.install("--sandbox", "e2b", "--e2b-api-key-file", key, "--e2b-template", BUILD, *flags)
            self.assertFalse(self.root.exists())
        link = self.work / "linked-e2b-key"
        link.symlink_to(key)
        large = self.work / "large-e2b-key"
        large.write_text("x" * 5000)
        large.chmod(0o600)
        key.chmod(0o644)
        for source in (key, link, large):
            with self.subTest(source=source), \
                    self.assertRaisesRegex(install.InstallError, "E2B API key file must be .* private regular file"):
                self.install("--sandbox", "e2b", "--e2b-api-key-file", source, "--e2b-template", BUILD,
                             "--public-url", "https://core.example")
            self.assertFalse(self.root.exists())
        key.chmod(0o600)
        self.install("--sandbox", "e2b", "--e2b-api-key-file", key, "--e2b-template", BUILD, "--public-url", "https://core.example")
        self.assertEqual(self.host.deployment_posts, [{"provider": "e2b", "expected_generation": 0, "e2b": {"api_key": secret, "template": BUILD}}])
        self.assertIn(f"Sandboxes: E2B template {BUILD} (2 CPUs, 2 GiB). E2B runs them; no nodes are needed.",
                      " ".join(self.output.getvalue().split()))
        self.assertNotIn(secret, self.output.getvalue() + (self.root / "config.json").read_text()
                         + (self.root / "state.json").read_text())

    def test_sandbox_flag_errors_create_nothing(self):
        for flags, message in ((("--web-only", "--core-key-file", self.key_file(), "--sandbox", "docker"),
                                "--web-only has no Core; choose the sandbox backend on the Core host"),
                               (("--sandbox", "e2b"), "requires --e2b-api-key-file and --e2b-template"),
                               (("--e2b-template", BUILD), "require --sandbox e2b"),
                               (("--accept-docker-risks",), "--accept-docker-risks requires --sandbox docker")):
            with self.subTest(flags=flags), self.assertRaisesRegex(install.InstallError, message):
                self.install(*flags)
            self.assertFalse(self.root.exists())

    def test_a_failed_first_start_removes_what_it_created(self):
        for flags, cause in (((), "`docker compose up` failed"), (("--native-core",), "Core did not become healthy")):
            with self.subTest(flags=flags):
                self.root = self.host.native_root = self.work / ("native" if flags else "compose")
                self.host.containers, self.output = {}, io.StringIO()
                self.host.core["fails"] = True
                with self.assertRaises(install.InstallError) as raised:
                    self.install("--sandbox", "microsandbox", *flags)
                self.assertEqual(install.error_text(raised.exception),
                                 f"The services did not start: {cause}\n{install.NOTHING_KEPT}")
                self.assertNotIn("Installation complete.", self.output.getvalue())
                self.assertFalse(self.root.exists())
                self.assertIn(["docker", "compose", "-p", self.host.project, "down", "--volumes", "--remove-orphans"],
                              self.host.commands)
                self.assertEqual(self.host.containers, {})
                if flags:
                    self.assertIn(["systemctl", "--user", "disable", "--now", self.host.project + "-core.service"],
                                  self.host.commands)
                    self.assertFalse(self.host.native["enabled"] or self.host.native["active"])
                # The same command installs once the cause is fixed, sandbox backend included.
                self.host.core["fails"] = False
                self.install("--sandbox", "microsandbox", *flags)
                self.assertEqual(self.host.deployment_posts[-1]["provider"], "microsandbox")

    def test_a_refused_selection_leaves_the_services_running(self):
        secret = "synthetic-e2b-key-0123456789"
        self.host.deployment_refusal = f"E2B rejected the API key {secret}."
        with self.assertRaises(install.InstallError) as raised:
            self.install("--sandbox", "e2b", "--e2b-api-key-file", self.key_file(secret), "--e2b-template", BUILD,
                         "--public-url", "https://core.example")
        self.assertEqual(str(raised.exception), "Core refused the sandbox setup: E2B rejected the API key [E2B API key]. "
                         "Services are installed and running; choose the sandbox backend on the Nodes page in Web")
        self.assertEqual(self.host.running(), {"database", "core", "web"})
        self.assertIn("Console: https://core.example\n", self.output.getvalue())
        self.assertNotIn("Sandboxes:", self.output.getvalue())
        self.assertNotIn("Installation complete.", self.output.getvalue())
        self.assertIn("Services are running; sandbox setup needs attention.", self.output.getvalue())

    def test_node_payload_exports_only_matched_distribution_files(self):
        self.install()
        payload = self.root / "node-payload/releases" / ("a" * 40)
        exported = {str(path.relative_to(payload)) for path in payload.rglob("*") if path.is_file()}
        self.assertEqual(exported, {"node-install.pyz", "manifest.json", "SHA256SUMS",
                                    "runtime/seccomp.json"})
        (payload / "node-install.pyz").write_text("changed")
        with self.assertRaisesRegex(install.InstallError, "node payload differs"):
            install.prepare_node_payload(self.root, self.document("state.json"), self.bundle)

    def test_bundle_verifies_transferred_bytes_and_checksum_list(self):
        self.assertEqual(install.verify_bundle(self.bundle)["source_commit"], "a" * 40)
        for name in ("images/core.tar", "manifest.json", "oac.pyz", "config.schema.json"):
            with self.subTest(name=name):
                path = self.bundle / name
                original = path.read_bytes()
                path.write_bytes(original + b"modified")
                with self.assertRaises(install.InstallError):
                    install.verify_bundle(self.bundle)
                path.write_bytes(original)
        checksums = self.bundle / "SHA256SUMS"
        original = checksums.read_text()
        for name in ("oac.pyz", "standard-sizes.json"):
            checksums.write_text("".join(line + "\n" for line in original.splitlines() if not line.endswith("  " + name)))
            with self.subTest(name=name), self.assertRaisesRegex(install.InstallError, "incomplete"):
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
                        "https://core.example/?token=synthetic-secret", "https://core_example"):
                output = io.StringIO()
                with self.subTest(flag=flag, url=url), contextlib.redirect_stderr(output), self.assertRaises(SystemExit):
                    install.arguments([flag, url])
                self.assertNotIn("synthetic-secret", output.getvalue())


    def test_public_url_flag_is_made_canonical_for_core(self):
        for origin, expected in (("HTTPS://Core.Example", "https://core.example"),
                                 ("http://localhost:8080/", "http://localhost:8080"),
                                 ("https://[2001:db8::1]", "https://[2001:db8::1]")):
            with self.subTest(origin=origin):
                self.assertEqual(install.arguments(["--public-url", origin]).public_url, expected)

    def test_public_url_rule_matches_core(self):
        # The same cases as Core's TestSandboxCoreURLValidation; config.json uses the same rule.
        for origin in ("https://core.example", "https://core.example:8443", "http://localhost:8091",
                       "http://127.0.0.2:8091", "http://[::1]:8091", "https://[2001:db8::1]"):
            self.assertTrue(install.valid_core_origin(origin), origin)
            self.assertTrue(config_model.CHECKS["origin"][0](origin), origin)
        for origin in ("", "http://core.example", "http://core:8091", "http://host.localhost", "https://core.example/",
                       "https://user:secret@core.example", "https://core.example/path", "https://core.example?",
                       "https://core.example#x", "https://CORE.example", "https://core.example:", "https://core.example:0",
                       "https://core.example:65536", "https://core.example:0080", "https://core.example:0443",
                       "https://core.example\\evil", "https://[not-an-ip]", "https://-core.example", "https://core..example",
                       "https://core_example", "https://core.example.", "https://b\u00fccher.example"):
            self.assertFalse(install.valid_core_origin(origin), origin)
            self.assertFalse(config_model.CHECKS["origin"][0](origin), origin)


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
