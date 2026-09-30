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
        temporary_root = Path.home() / ".oac/tests/install-native"
        temporary_root.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=temporary_root)
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name).resolve()
        self.root = self.directory / 'install space %n $HOME'
        self.bundle = self.directory / "bundle"
        self.state = {"mode": "all", "native_core": True, "project": "oac-0123456789", "installation_id": "fixture-installation"}
        for name in service.REQUIRED:
            path = self.bundle / "native" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"\x7fELFfixture-" + name.encode())
        (self.root / "generated").mkdir(parents=True, mode=0o700)
        self.commands = mock.patch.object(service.subprocess, "run")
        self.run = self.commands.start()
        self.addCleanup(self.commands.stop)
        self.run.return_value = subprocess.CompletedProcess([], 0, "", "")

    def prepare(self):
        service.prepare(self.root, self.state, self.bundle)

    def unit_path(self):
        path = self.root / "generated" / "oac-0123456789-core.service"
        if not path.exists():
            path.write_text(service.unit_text(self.root, "fixture header"))
        return path

    def host_ready(self):
        for patch in (mock.patch.object(service.platform, "system", return_value="Linux"),
                      mock.patch.object(service.os, "access", return_value=True)):
            patch.start()
            self.addCleanup(patch.stop)

        def result(arguments, **kwargs):
            output = "yes\n" if arguments[0] == "loginctl" else ""
            return subprocess.CompletedProcess(arguments, 0, output, "")

        self.run.side_effect = result

    def test_native_core_is_explicit_and_independent_of_execution(self):
        for mode, native, expected in (("all", False, False), ("all", True, True),
                                       ("core-only", True, True), ("web-only", True, False)):
            with self.subTest(mode=mode, native=native):
                state = dict(self.state, mode=mode, native_core=native)
                self.assertEqual(service.is_native(state), expected)
                if not expected:
                    service.prepare(self.root, state, self.bundle)
                    service.start(self.root, state)
                    service.stop(self.root, state)
                    self.assertFalse(service.active(state))
        self.run.assert_not_called()

    def test_cloud_helper_remains_executable_but_private(self):
        self.prepare()
        helper = self.root / "native/e2b/oac-e2b-provider"
        self.assertEqual(stat.S_IMODE(helper.stat().st_mode), 0o700)

    def test_unit_preserves_direct_core_and_runtime_process_lifetime(self):
        unit = configparser.ConfigParser(interpolation=None)
        unit.read(self.unit_path())
        directives = unit["Service"]
        executable = str(self.root / "native/bin/oac-core").replace("%", "%%").replace('"', '\\"')
        self.assertEqual(directives["ExecStart"], ':"' + executable + '"')
        self.assertEqual(directives["WorkingDirectory"], str(self.root).replace("%", "%%"))
        self.assertEqual(directives["EnvironmentFile"], str(self.root / "generated/core.env").replace("%", "%%"))
        self.assertEqual(directives["KillMode"], "process")
        self.assertEqual(directives["Restart"], "on-failure")
        self.assertEqual(directives["UMask"], "0077")
        self.assertNotIn("ExecStop", directives)
        self.assertNotIn("ExecStopPost", directives)
        self.assertNotIn("DATABASE_URL", self.unit_path().read_text())
        self.run.assert_not_called()

    def test_repeat_keeps_binary_inodes(self):
        self.prepare()
        paths = [self.root / "native" / name for name in service.REQUIRED]
        for path in (self.root / "native", *paths):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700)
        original = [(path.stat().st_ino, path.stat().st_mtime_ns, path.read_bytes()) for path in paths]
        self.prepare()
        self.assertEqual(original, [(path.stat().st_ino, path.stat().st_mtime_ns, path.read_bytes()) for path in paths])
        self.assertEqual(sorted(path.name for path in self.root.iterdir()), ["generated", "native"])

    def test_changed_payload_refuses_without_overwriting_installed_binary(self):
        self.prepare()
        installed = self.root / "native/bin/oac-core"
        before = installed.read_bytes()
        (self.bundle / "native/bin/oac-core").write_bytes(b"changed")
        with self.assertRaisesRegex(RuntimeError, "differ"):
            self.prepare()
        self.assertEqual(installed.read_bytes(), before)
        self.run.assert_not_called()

    def test_ambiguous_paths_and_foreign_units_refuse(self):
        for suffix in ("bad\npath", "bad*path", "bad\\path", "bad\x7fpath", 'bad"path', "bad'path"):
            with self.assertRaises(RuntimeError):
                service.prepare(self.directory / suffix, self.state, self.bundle)
        for project in ("other-service", "../oac-0123456789", "oac-0123456789\n"):
            state = dict(self.state, project=project)
            with self.assertRaises(RuntimeError):
                service.prepare(self.root, state, self.bundle)
            with self.assertRaises(RuntimeError):
                service.stop(self.root, state)
            with self.assertRaises(RuntimeError):
                service.active(state)
        self.run.assert_not_called()

    def test_symlink_payload_is_not_followed(self):
        path = self.bundle / "native/bin/oac-core"
        path.unlink()
        outside = self.directory / "outside"
        outside.write_bytes(b"unchanged")
        path.symlink_to(outside)
        with self.assertRaisesRegex(RuntimeError, "regular"):
            self.prepare()
        self.assertEqual(outside.read_bytes(), b"unchanged")
        self.assertFalse((self.root / "native").exists())

    def test_preflight_checks_host_and_libraries_without_running_provider(self):
        self.host_ready()
        service.preflight(self.bundle, self.root)
        commands = [call.args[0] for call in self.run.call_args_list]
        self.assertEqual(commands[:2], [["systemctl", "--user", "show", "--property=Version", "--value"],
                                       ["loginctl", "show-user", str(service.os.getuid()), "--property=Linger", "--value"]])
        self.assertEqual(commands[2:], [["ldd", str(self.bundle / "native" / name)] for name in service.REQUIRED])

    def test_preflight_requires_linger_without_kvm_checks(self):
        self.host_ready()
        with mock.patch.object(service.os, "access", side_effect=AssertionError("No Core device checks")):
            service.preflight(self.bundle, self.root)
        self.run.reset_mock()
        self.run.side_effect = lambda arguments, **kwargs: subprocess.CompletedProcess(arguments, 0, "no\n", "")
        with self.assertRaisesRegex(RuntimeError, "lingering"):
            service.preflight(self.bundle, self.root)
        self.assertFalse(any(call.args[0][0] == "ldd" for call in self.run.call_args_list))

    def test_failed_commands_do_not_disclose_diagnostics(self):
        self.host_ready()
        self.run.side_effect = None
        self.run.return_value = subprocess.CompletedProcess([], 1, "synthetic-secret", "synthetic-secret")
        with self.assertRaises(RuntimeError) as error:
            service.preflight(self.bundle, self.root)
        self.assertNotIn("synthetic-secret", str(error.exception))
        self.prepare()
        self.unit_path()
        self.run.side_effect = OSError("synthetic-secret")
        with self.assertRaises(RuntimeError) as error:
            service.start(self.root, self.state)
        self.assertNotIn("synthetic-secret", str(error.exception))

    def test_missing_shared_library_refuses(self):
        self.host_ready()
        self.run.side_effect = lambda arguments, **kwargs: subprocess.CompletedProcess(
            arguments, 0, "libc.so => not found" if arguments[0] == "ldd" else "yes\n", "")
        with self.assertRaisesRegex(RuntimeError, "shared libraries"):
            service.preflight(self.bundle, self.root)

    def test_commands_target_only_this_installation(self):
        self.prepare()
        unit = self.unit_path()
        service.start(self.root, self.state)
        service.stop(self.root, self.state)
        self.assertTrue(service.active(self.state))
        self.run.return_value = subprocess.CompletedProcess([], 3, "", "")
        self.assertFalse(service.active(self.state))
        self.assertEqual([call.args[0] for call in self.run.call_args_list], [
            ["systemctl", "--user", "daemon-reload"],
            ["systemctl", "--user", "enable", "--now", str(unit)],
            ["systemctl", "--user", "stop", "oac-0123456789-core.service"],
            ["systemctl", "--user", "is-active", "--quiet", "oac-0123456789-core.service"],
            ["systemctl", "--user", "is-active", "--quiet", "oac-0123456789-core.service"],
        ])


if __name__ == "__main__":
    unittest.main()
