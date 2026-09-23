"""Verify native packaging without starting a service or Runtime."""

import configparser
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest import mock

import native_service as service


class NativeServiceTests(unittest.TestCase):
    def setUp(self):
        temporary_root = Path.home() / ".parsar/tests/install-native"
        temporary_root.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=temporary_root)
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name).resolve()
        self.root = self.directory / 'install space %n $HOME "quote"'
        self.bundle = self.directory / "bundle"
        self.state = {"mode": "all", "provider": "microsandbox", "project": "parsar-0123456789"}
        self.environment = {"DATABASE_URL": 'synthetic:"quoted"\\path$HOME`value`%n', "PORT": "8091"}
        for name in service.REQUIRED:
            path = self.bundle / "native" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"\x7fELFfixture-" + name.encode())
        self.commands = mock.patch.object(service.subprocess, "run")
        self.run = self.commands.start()
        self.addCleanup(self.commands.stop)
        self.run.return_value = subprocess.CompletedProcess([], 0, "", "")

    def prepare(self):
        service.prepare(self.root, self.state, self.bundle, self.environment)

    def unit_path(self):
        return self.root / "config" / "parsar-0123456789-core.service"

    def host_ready(self):
        for patch in (mock.patch.object(service.platform, "system", return_value="Linux"),
                      mock.patch.object(service.os, "access", return_value=True)):
            patch.start()
            self.addCleanup(patch.stop)

        def result(arguments, **kwargs):
            output = "yes\n" if arguments[0] == "loginctl" else ""
            return subprocess.CompletedProcess(arguments, 0, output, "")

        self.run.side_effect = result

    def test_only_core_microsandbox_uses_native_service(self):
        for mode, provider, expected in (("all", None, False),
                                         ("all", "microsandbox", True),
                                         ("core-only", "microsandbox", True),
                                         ("web-only", "microsandbox", False),
                                         ("all", "docker", False)):
            with self.subTest(mode=mode, provider=provider):
                state = dict(self.state, mode=mode, provider=provider)
                self.assertEqual(service.is_native(state), expected)
                if not expected:
                    service.prepare(self.root, state, self.bundle, {})
                    service.start(self.root, state)
                    service.stop(self.root, state)
                    self.assertFalse(service.active(state))
        self.run.assert_not_called()

    def test_prepare_preserves_direct_core_and_runtime_process_lifetime(self):
        self.prepare()
        unit = configparser.ConfigParser(interpolation=None)
        unit.read(self.unit_path())
        directives = unit["Service"]
        executable = str(self.root / "native/bin/agents-api").replace("%", "%%").replace('"', '\\"')
        self.assertEqual(directives["ExecStart"], ':"' + executable + '"')
        self.assertEqual(directives["WorkingDirectory"], str(self.root).replace("%", "%%"))
        self.assertEqual(directives["EnvironmentFile"], str(self.root / "config/core.env").replace("%", "%%"))
        self.assertEqual(directives["KillMode"], "process")
        self.assertEqual(directives["Restart"], "on-failure")
        self.assertEqual(directives["UMask"], "0077")
        self.assertNotIn("ExecStop", directives)
        self.assertNotIn("ExecStopPost", directives)
        self.assertNotIn("DATABASE_URL", self.unit_path().read_text())
        self.run.assert_not_called()

    def test_private_files_and_environment_literal_values(self):
        self.prepare()
        env = self.root / "config/core.env"
        self.assertEqual(env.read_text(), 'DATABASE_URL="synthetic:\\"quoted\\"\\\\path\\$HOME\\`value\\`%n"\nPORT="8091"\n')
        for path in (env, self.unit_path(), self.root / "native/microsandbox/libkrunfw.so.5.6.1"):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        for path in (self.root / "config", self.root / "native/bin/agents-api",
                     self.root / "native/bin/agents-api-microsandbox-provider", self.root / "native/microsandbox/msb"):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700)

    def test_repeat_keeps_binary_and_private_file_inodes(self):
        self.prepare()
        paths = [self.root / "native" / name for name in service.REQUIRED]
        paths += [self.root / "config/core.env", self.unit_path()]
        original = [(path.stat().st_ino, path.stat().st_mtime_ns, path.read_bytes()) for path in paths]
        self.prepare()
        self.assertEqual(original, [(path.stat().st_ino, path.stat().st_mtime_ns, path.read_bytes()) for path in paths])

    def test_changed_payload_refuses_without_overwriting_installed_binary(self):
        self.prepare()
        installed = self.root / "native/bin/agents-api"
        before = installed.read_bytes()
        (self.bundle / "native/bin/agents-api").write_bytes(b"changed")
        with self.assertRaisesRegex(RuntimeError, "differ"):
            self.prepare()
        self.assertEqual(installed.read_bytes(), before)
        self.run.assert_not_called()

    def test_invalid_environment_is_rejected_before_writes(self):
        for environment in ({"KEY": "secret\nInjected=value"}, {"KEY": "secret\x00"},
                            {"KEY": "secret\r"}, {"BAD=KEY": "secret"}, {1: "secret", "OK": "value"}):
            with self.subTest(environment=environment):
                with self.assertRaises(RuntimeError) as error:
                    service.prepare(self.root, self.state, self.bundle, environment)
                self.assertNotIn("secret", str(error.exception))
                self.assertFalse(self.root.exists())

    def test_ambiguous_paths_and_foreign_units_refuse(self):
        for suffix in ("bad\npath", "bad*path", "bad\\path"):
            with self.assertRaises(RuntimeError):
                service.prepare(self.directory / suffix, self.state, self.bundle, {})
        for project in ("other-service", "../parsar-0123456789", "parsar-0123456789\n"):
            state = dict(self.state, project=project)
            with self.assertRaises(RuntimeError):
                service.prepare(self.root, state, self.bundle, {})
            with self.assertRaises(RuntimeError):
                service.stop(self.root, state)
            with self.assertRaises(RuntimeError):
                service.active(state)
        self.run.assert_not_called()

    def test_symlink_payload_is_not_followed(self):
        path = self.bundle / "native/bin/agents-api"
        path.unlink()
        outside = self.directory / "outside"
        outside.write_bytes(b"unchanged")
        path.symlink_to(outside)
        with self.assertRaisesRegex(RuntimeError, "regular"):
            self.prepare()
        self.assertEqual(outside.read_bytes(), b"unchanged")
        self.assertFalse(self.root.exists())

    def test_symlink_configuration_does_not_overwrite_target(self):
        self.prepare()
        env = self.root / "config/core.env"
        env.unlink()
        outside = self.directory / "outside"
        outside.write_text("unchanged")
        env.symlink_to(outside)
        with self.assertRaisesRegex(RuntimeError, "regular"):
            self.prepare()
        self.assertEqual(outside.read_text(), "unchanged")

    def test_preflight_checks_host_and_libraries_without_running_provider(self):
        self.host_ready()
        service.preflight(self.bundle)
        commands = [call.args[0] for call in self.run.call_args_list]
        self.assertEqual(commands[:2], [["systemctl", "--user", "show", "--property=Version", "--value"],
                                       ["loginctl", "show-user", str(service.os.getuid()), "--property=Linger", "--value"]])
        self.assertEqual(commands[2:], [["ldd", str(self.bundle / "native" / name)] for name in service.REQUIRED[1:]])

    def test_preflight_requires_kvm_access_and_linger(self):
        self.host_ready()
        with mock.patch.object(service.os, "access", return_value=False):
            with self.assertRaisesRegex(RuntimeError, "/dev/kvm"):
                service.preflight(self.bundle)
        self.run.assert_not_called()
        self.run.side_effect = lambda arguments, **kwargs: subprocess.CompletedProcess(arguments, 0, "no\n", "")
        with self.assertRaisesRegex(RuntimeError, "lingering"):
            service.preflight(self.bundle)
        self.assertFalse(any(call.args[0][0] == "ldd" for call in self.run.call_args_list))

    def test_failed_commands_do_not_disclose_diagnostics(self):
        self.host_ready()
        self.run.side_effect = None
        self.run.return_value = subprocess.CompletedProcess([], 1, "synthetic-secret", "synthetic-secret")
        with self.assertRaises(RuntimeError) as error:
            service.preflight(self.bundle)
        self.assertNotIn("synthetic-secret", str(error.exception))
        self.prepare()
        self.run.side_effect = OSError("synthetic-secret")
        with self.assertRaises(RuntimeError) as error:
            service.start(self.root, self.state)
        self.assertNotIn("synthetic-secret", str(error.exception))

    def test_missing_shared_library_refuses(self):
        self.host_ready()
        self.run.side_effect = lambda arguments, **kwargs: subprocess.CompletedProcess(
            arguments, 0, "libc.so => not found" if arguments[0] == "ldd" else "yes\n", "")
        with self.assertRaisesRegex(RuntimeError, "shared libraries"):
            service.preflight(self.bundle)

    def test_commands_target_only_this_installation(self):
        self.prepare()
        service.start(self.root, self.state)
        service.stop(self.root, self.state)
        self.assertTrue(service.active(self.state))
        self.run.return_value = subprocess.CompletedProcess([], 3, "", "")
        self.assertFalse(service.active(self.state))
        self.assertEqual([call.args[0] for call in self.run.call_args_list], [
            ["systemctl", "--user", "daemon-reload"],
            ["systemctl", "--user", "enable", "--now", str(self.unit_path())],
            ["systemctl", "--user", "stop", "parsar-0123456789-core.service"],
            ["systemctl", "--user", "is-active", "--quiet", "parsar-0123456789-core.service"],
            ["systemctl", "--user", "is-active", "--quiet", "parsar-0123456789-core.service"],
        ])


if __name__ == "__main__":
    unittest.main()
