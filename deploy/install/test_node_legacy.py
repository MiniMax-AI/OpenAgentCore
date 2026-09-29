"""Legacy-node refusals are read-only and scoped to the requested installation."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

import node_install as installer


class LegacyNodeTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".oac/tests/node-legacy"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.home = self.root / "user"
        self.home.mkdir()
        self.args = argparse.Namespace(installation_id="94be54a1-138c-4f30-bc87-b13686272dbe", provider="docker")
        self.other = "634d97be-e54d-40f0-9468-ae6b62be85bf"
        self.unit = "parsar-node-" + self.args.installation_id + ".service"
        self.networks = ""
        for patch in (
            mock.patch.multiple(installer, LEGACY_RECORDS=self.root / "records", LEGACY_SERVICE_HOME=self.root / "service",
                                SYSTEM_RECORDS=self.root / "new-records", SYSTEM_UNITS=self.root / "units",
                                DOCKER_SOCKET=self.root / "docker.sock"),
            mock.patch.object(installer.Path, "home", return_value=self.home),
            mock.patch.object(installer, "host_checks"),
            mock.patch.object(installer.os, "geteuid", return_value=1000),
            mock.patch.object(installer.shutil, "which", side_effect=lambda tool: "/usr/bin/" + tool),
            mock.patch.object(installer.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "not-found\n", "")),
            mock.patch.object(installer, "checked", side_effect=lambda *a, **k: self.networks),
        ):
            patch.start()
            self.addCleanup(patch.stop)

    def assert_refusal(self):
        with mock.patch.object(installer, "open_node") as opened, mock.patch.object(installer, "host_lock") as lock:
            with self.assertRaisesRegex(installer.InstallError, "before the OpenAgentCore rename.*Nothing was changed") as failure:
                installer.install_system(self.args, "private-token")
            self.assertIn(self.args.installation_id, str(failure.exception))
            self.assertNotIn("private-token", str(failure.exception))
            opened.assert_not_called()
            lock.assert_not_called()
        self.assertFalse((self.home / ".oac/nodes").exists())

    def test_each_same_installation_path_refuses_without_mutation(self):
        paths = [installer.LEGACY_RECORDS / (self.args.installation_id + ".json"),
                 self.home / ".parsar/nodes" / self.args.installation_id,
                 self.home / ".config/systemd/user" / self.unit,
                 installer.SYSTEM_UNITS / self.unit,
                 installer.LEGACY_SERVICE_HOME / ".parsar/nodes" / self.args.installation_id]
        for path in paths:
            with self.subTest(path=path):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("retained")
                self.assert_refusal()
                self.assertEqual(path.read_text(), "retained")
                path.unlink()

    def test_final_symlink_counts_as_retained_state_without_reading_its_target(self):
        path = self.home / ".parsar/nodes" / self.args.installation_id
        path.parent.mkdir(parents=True)
        path.symlink_to(self.root / "absent-private-target")
        self.assert_refusal()
        self.assertTrue(path.is_symlink())

    def test_intermediate_symlink_refuses_without_following_it(self):
        target = self.root / "private-target"
        target.mkdir()
        (self.home / ".parsar").symlink_to(target, target_is_directory=True)
        with mock.patch.object(installer, "open_node") as opened:
            with self.assertRaisesRegex(installer.InstallError, "Cannot inspect possible legacy node state.*Nothing was changed"):
                installer.install_system(self.args, "private-token")
            opened.assert_not_called()
        self.assertEqual(list(target.iterdir()), [])

    def test_other_installations_and_old_account_alone_do_not_refuse(self):
        for path in [installer.LEGACY_RECORDS / (self.other + ".json"),
                     installer.LEGACY_RECORDS / "account.json",
                     self.home / ".parsar/nodes" / self.other,
                     installer.LEGACY_SERVICE_HOME / ".parsar/nodes" / self.other,
                     installer.SYSTEM_UNITS / ("parsar-node-" + self.other + ".service")]:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("foreign")
        installer.DOCKER_SOCKET.touch()
        self.networks = "parsar-node-" + self.other + "\n"
        installer.refuse_legacy_node(self.args)
        self.assertTrue(all(p.read_text() == "foreign" for p in self.root.rglob("*") if p.is_file() and p != installer.DOCKER_SOCKET))

    def test_loaded_unit_and_exact_network_refuse(self):
        with mock.patch.object(installer.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "loaded\n", "")):
            self.assert_refusal()
        installer.DOCKER_SOCKET.touch()
        self.networks = "parsar-node-" + self.args.installation_id + "\n"
        self.assert_refusal()

    def test_sudo_invoking_user_legacy_node_refuses_before_account_creation(self):
        invoking = self.root / "invoking"
        old = invoking / ".parsar/nodes" / self.args.installation_id
        old.mkdir(parents=True)
        with mock.patch.object(installer.os, "geteuid", return_value=0), \
             mock.patch.dict(os.environ, {"SUDO_USER": "operator"}), \
             mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir=str(invoking))), \
             mock.patch.object(installer, "host_checks"), \
             mock.patch.object(installer, "prepare_account") as account, \
             mock.patch.object(installer, "node_record") as record:
            with self.assertRaisesRegex(installer.InstallError, "before the OpenAgentCore rename"):
                installer.install_system(self.args, "private-token")
            account.assert_not_called()
            record.assert_not_called()
        self.assertEqual(list(old.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
