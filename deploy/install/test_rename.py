"""Conversion from the real pre-rename renderer, including interrupted data copies."""
import contextlib
import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import config_model
import configuration
import install
import oac_cli
import rename
from installer_fakes import FakeHost, MANIFEST, make_bundle, run_installer
from test_oac import interrupt


def historical(name):
    path = Path(__file__).with_name("testdata") / (name + "_pre_rename.py")
    spec = importlib.util.spec_from_file_location(name + "_pre_rename", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class RenameTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".oac/tests/rename"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name)
        self.root = self.work / "custom"
        self.old_default, self.new_default = self.work / ".parsar/core", self.work / ".oac/core"
        patcher = mock.patch.object(rename, "defaults", return_value=(self.old_default, self.new_default))
        patcher.start()
        self.addCleanup(patcher.stop)
        self.host = FakeHost(self)
        self.bundle, self.manifest = make_bundle(self.work / "bundle", MANIFEST)
        self.output = io.StringIO()
        self.old = historical("configuration")
        self.old.native_service = historical("native_service")

    def fixture(self, mode="all", native=False, provider="docker", default=False):
        if default:
            self.root = self.old_default
        self.host.native_root = self.root
        config = config_model.initial(mode, native, public_url="https://core.example",
                                      **({"ports.database": 15432} if native else {}),
                                      **({"web.core_url": "https://core.example"} if mode == "web-only" else {}))
        key = self.work / "key"
        key.write_text("k" * 64)
        key.chmod(0o600)
        images = {name: "sha256:" + "8" * 64 for name in install.image_names(mode, native)}
        install.create(self.root, SimpleNamespace(core_key_file=key), config, {"source_commit": "b" * 40}, images)
        state = oac_cli.load_state(self.root)
        state.update(format=1, project="parsar-0123456789")
        rendered = self.old.render(self.root, config, state, "2026-09-26T00:00:00Z")
        for name, data in rendered.files.items():
            oac_cli.write_private(self.root / "generated" / name, data)
        oac_cli.save_state(self.root, oac_cli.record_digests(state, rendered.files))
        (self.root / "parsar").write_text("old command")
        (self.root / ".parsar.lock").touch(mode=0o600)
        if mode != "web-only":
            self.host.add_database(state["project"])
            digests = json.loads((self.root / "generated/core-key-digests.json").read_text())
            self.host.run_container("database")
            if not native:
                self.host.run_container("core", port=config["ports"]["core"], digests=digests)
                self.host.load_core(self.root, json.loads(rendered.files["compose.json"])["services"]["core"])
            else:
                self.host.native.update(active=True, addr=config["ports"]["core"], digests=digests)
                for name in self.old.native_service.REQUIRED:
                    path = self.root / "native" / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(b"old native executable")
        if mode != "core-only":
            self.host.run_container("web")
            self.host.web_port = config["ports"]["web"]
        self.host.core_installation_id = state["installation_id"]
        self.host.remote_core["https://core.example"] = (200, state["installation_id"])
        self.host.deployment = {"provider": provider, "generation": 4, "maintenance": True,
            "resources": {"allocations": 0, "pending": 0}, "specification": {
                "resources": {"cpus": 2, "memory_mib": 2048},
                "runtime": {"image": "sha256:" + "9" * 64}}}
        self.before = self.snapshot()
        self.before_volumes = copy.deepcopy(self.host.volumes)
        self.identity = state["installation_id"]
        return state

    def snapshot(self):
        return {str(path.relative_to(self.root)): path.read_bytes() for path in self.root.rglob("*") if path.is_file()}

    def convert(self, *extra, explicit=True, yes=True):
        args = ["--convert", *(["--yes"] if yes else []), *extra]
        if explicit:
            args += ["--install-dir", self.root]
        with contextlib.redirect_stdout(self.output):
            return run_installer(install, self.bundle, args)

    def state(self):
        return json.loads((self.root / "state.json").read_text())

    def assert_preserved(self):
        state = self.state()
        self.assertEqual(state["installation_id"], self.identity)
        self.assertEqual(state["format"], 2)
        self.assertTrue(state["renamed_from"]["finished"])
        self.assertEqual(state["project"], "oac-0123456789")
        for name in ("secrets/core.key", "secrets/database.password", "secrets/credential.key", "config.json"):
            if name in self.before:
                self.assertEqual((self.root / name).read_bytes(), self.before[name], name)
        if self.before_volumes:
            self.assertEqual(self.host.volumes["parsar-0123456789_database"], self.before_volumes["parsar-0123456789_database"])
            self.assertEqual(self.host.volumes["oac-0123456789_database"]["Data"], self.before_volumes["parsar-0123456789_database"]["Data"])

    def test_default_moves_custom_stays_preserves_identity_and_replaces_runtime(self):
        for default in (False, True):
            with self.subTest(default=default):
                if default:
                    self.host.containers.clear()
                    self.host.volumes.clear()
                self.fixture(default=default)
                old = self.root
                self.convert(explicit=not default)
                self.root = self.new_default if default else old
                self.assert_preserved()
                self.assertIn("parsar was renamed to oac", (old / "parsar").read_text())
                self.assertEqual(len(self.host.deployment_posts), 1 + int(default))
                self.assertEqual(self.host.deployment["generation"], 5)
                self.assertFalse(self.host.deployment["maintenance"])
                self.assertEqual(self.host.deployment_posts[-1]["resources"], {"cpus": 2, "memory_mib": 2048})
                self.assertEqual(self.host.deployment_posts[-1]["runtime"]["microsandbox_ref"], MANIFEST["runtime_ref"])

    def test_all_drain_refusals_are_read_only_and_aggregated(self):
        self.fixture()
        self.host.deployment.update(maintenance=False, resources={"allocations": 3, "pending": 2})
        self.host.nodes = [{"id": "node_one"}]
        with self.assertRaises(rename.RenameError) as error:
            self.convert()
        for text in ("maintenance", "allocations: 3", "pending: 2", "1 registered node", "No installation conversion"):
            self.assertIn(text, str(error.exception))
        self.assertEqual(self.snapshot(), self.before)
        self.assertEqual(self.host.volumes, self.before_volumes)
        self.assertFalse(any("stop" in command or "down" in command for command in self.host.commands))

    def test_static_refusal_does_not_start_stopped_core(self):
        self.fixture()
        self.host.containers["core"]["running"] = False
        path = self.root / "generated/core.env"
        path.write_text(path.read_text() + "EDITED=1\n")
        before = self.snapshot()
        with self.assertRaisesRegex(rename.RenameError, "edited"):
            self.convert()
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(any("up" in command for command in self.host.commands))

    def test_stopped_core_probe_restores_only_new_starts_on_refusal(self):
        self.fixture()
        self.host.containers["core"]["running"] = False
        self.host.deployment["maintenance"] = False
        before = self.host.running()
        with self.assertRaisesRegex(rename.RenameError, "maintenance"):
            self.convert()
        self.assertEqual(self.host.running(), before)
        self.assertEqual(self.snapshot(), self.before)
        stops = [command[4:] for command in self.host.commands if command[:2] == ["docker", "compose"] and "stop" in command]
        self.assertEqual(stops, [["stop", "core"]])
        self.host.deployment["maintenance"] = True
        self.convert()
        self.assert_preserved()

    def test_restore_failure_is_reported_without_false_no_change_claim(self):
        self.fixture()
        self.host.containers["core"]["running"] = False
        self.host.deployment["maintenance"] = False
        original = install.run
        def fail_stop(command, **kwargs):
            if "stop" in command:
                raise subprocess.CalledProcessError(1, command)
            return original(command, **kwargs)
        with mock.patch.object(install, "run", side_effect=fail_stop), self.assertRaisesRegex(rename.RenameError, "could not restore") as error:
            self.convert()
        self.assertNotIn("Nothing was changed", str(error.exception))
        self.assertEqual(self.snapshot(), self.before)

    def test_foreign_target_volume_and_container_refuse_without_removal(self):
        for container in (False, True):
            with self.subTest(container=container):
                if container:
                    self.host.volumes.pop("oac-0123456789_database", None)
                    self.host.project_containers["oac-0123456789"] = {"foreign": {"owner": "someone-else"}}
                else:
                    self.fixture()
                    self.host.add_database("oac-0123456789")
                before = copy.deepcopy(self.host.volumes)
                with self.assertRaisesRegex(rename.RenameError, "target"):
                    self.convert()
                self.assertEqual(self.host.volumes, before)
                self.assertEqual(self.snapshot(), self.before)
                self.assertFalse(any("rm" in command for command in self.host.commands))

    def test_interrupted_copy_restarts_only_owned_copy_and_preserves_old_data(self):
        self.assert_interrupted_copy_resumes(native=False)

    def test_native_interrupted_copy_resumes_after_old_unit_was_unlinked(self):
        self.assert_interrupted_copy_resumes(native=True)
        disable = ["systemctl", "--user", "disable", "--now", "parsar-0123456789-core.service"]
        self.assertEqual(self.host.commands.count(disable), 1)
        self.assertTrue(self.host.native["active"])

    def assert_interrupted_copy_resumes(self, native):
        self.fixture(native=native)
        original = install.run
        interrupted = []
        def fail_copy(command, **kwargs):
            if command[-1].startswith("cp -a") and not interrupted:
                interrupted.append(True)
                self.host.volumes["oac-0123456789_database"]["Data"] = {"partial": b"partial"}
                raise KeyboardInterrupt()
            return original(command, **kwargs)
        with mock.patch.object(install, "run", side_effect=fail_copy), self.assertRaises(KeyboardInterrupt):
            self.convert()
        self.assertFalse(self.state()["renamed_from"]["volume_copied"])
        self.assertEqual(self.host.volumes["parsar-0123456789_database"], self.before_volumes["parsar-0123456789_database"])
        self.convert()
        self.assert_preserved()
        self.assertIn(["docker", "volume", "rm", "oac-0123456789_database"], self.host.commands)
        self.assertNotIn(["docker", "volume", "rm", "parsar-0123456789_database"], self.host.commands)

    def test_copy_marker_loss_retries_and_foreign_replacement_is_never_deleted(self):
        self.fixture()
        with interrupt(rename, "save", lambda root, state: state["renamed_from"]["volume_copied"]), self.assertRaises(KeyboardInterrupt):
            self.convert()
        self.assertEqual(self.host.copy_count, 1)
        target = self.host.volumes["oac-0123456789_database"]
        labels = target["Labels"].copy()
        target["Labels"] = {"owner": "foreign"}
        with self.assertRaisesRegex(rename.RenameError, "unowned"):
            self.convert()
        self.assertEqual(target["Labels"], {"owner": "foreign"})
        target["Labels"] = labels
        self.convert()
        self.assertEqual(self.host.copy_count, 2)
        self.assert_preserved()

    def test_resume_after_directory_move_and_bundle_or_source_mismatch(self):
        self.fixture(default=True)
        with interrupt(rename, "stub"), self.assertRaises(KeyboardInterrupt):
            self.convert()
        self.assertFalse(self.old_default.exists())
        self.root = self.new_default
        other, _ = make_bundle(self.work / "other", MANIFEST, commit="c" * 40)
        with contextlib.redirect_stdout(self.output), self.assertRaisesRegex(rename.RenameError, "Finish.*bundle"):
            run_installer(install, other, ["--convert", "--yes"])
        state = self.state()
        state["installation_id"] = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
        oac_cli.save_state(self.root, state)
        with self.assertRaisesRegex(rename.RenameError, "identity"):
            self.convert()
        state["installation_id"] = self.identity
        oac_cli.save_state(self.root, state)
        self.convert(explicit=False)
        self.assert_preserved()
        self.assertEqual(self.host.copy_count, 1)

    def test_native_unit_is_disabled_before_move_and_new_unit_runs(self):
        self.fixture(native=True)
        self.convert()
        self.assertIn(["systemctl", "--user", "disable", "--now", "parsar-0123456789-core.service"], self.host.commands)
        self.assertFalse((self.root / "generated/parsar-0123456789-core.service").exists())
        self.assertTrue((self.root / "generated/oac-0123456789-core.service").is_file())
        self.assertTrue(self.host.native["active"])
        self.assert_preserved()

    def test_e2b_stays_in_maintenance_and_web_only_never_copies_a_database(self):
        self.fixture(provider="e2b")
        self.convert()
        self.assertTrue(self.host.deployment["maintenance"])
        self.assertFalse(self.host.deployment_posts)
        self.assertIn("E2B remains in maintenance", self.output.getvalue())
        self.root = self.work / "web-only"
        self.host.containers.clear()
        self.host.volumes.clear()
        self.fixture(mode="web-only")
        self.convert()
        self.assert_preserved()
        self.assertFalse(self.host.volumes)

    def test_no_space_does_not_stop_or_write(self):
        self.fixture()
        self.host.free_space = 99
        with self.assertRaisesRegex(rename.RenameError, "room"):
            self.convert()
        self.assertEqual(self.snapshot(), self.before)
        self.assertFalse(any("stop" in command for command in self.host.commands))

    def test_old_command_copy_and_old_default_fresh_install_are_refused(self):
        self.fixture(default=True)
        with mock.patch("sys.argv", [str(self.root / "parsar"), "status"]), self.assertRaisesRegex(oac_cli.OacError, "renamed to oac"):
            oac_cli.main(["status"], root=self.root)
        with self.assertRaisesRegex(install.InstallError, "installation made before"):
            run_installer(install, self.bundle, [])
        self.assertFalse(self.new_default.exists())


    def test_stopped_native_probe_restores_unit_and_database(self):
        self.fixture(native=True)
        self.host.native["active"] = False
        self.host.containers["database"]["running"] = False
        self.host.deployment["maintenance"] = False
        with self.assertRaisesRegex(rename.RenameError, "maintenance"):
            self.convert()
        self.assertFalse(self.host.native["active"])
        self.assertFalse(self.host.containers["database"]["running"])
        self.assertTrue(self.host.containers["web"]["running"])
        self.assertNotIn(["systemctl", "--user", "disable", "--now", "parsar-0123456789-core.service"], self.host.commands)
        self.assertEqual(self.snapshot(), self.before)

    def test_exact_runtime_retry_after_lost_put_response_keeps_one_generation(self):
        self.fixture(provider="microsandbox")
        self.host.deployment["specification"]["resources"].update(root_disk_mib=8192, environment_disk_mib=1024)
        original = rename.sandbox_setup.request
        lost = []
        def uncertain(core, key, method, path, value=None):
            response = original(core, key, method, path, value)
            if method == "PUT" and not lost:
                lost.append(True)
                raise rename.sandbox_setup.SandboxSetupError("lost response")
            return response
        with mock.patch.object(rename.sandbox_setup, "request", side_effect=uncertain), self.assertRaisesRegex(rename.sandbox_setup.SandboxSetupError, "lost response"):
            self.convert()
        self.assertEqual(self.host.deployment["generation"], 5)
        self.assertTrue(self.host.deployment["maintenance"])
        self.convert()
        self.assertEqual(len(self.host.deployment_posts), 1)
        self.assertEqual(self.host.deployment["generation"], 5)
        self.assertEqual(self.host.deployment["specification"]["resources"]["root_disk_mib"], 8192)
        self.assert_preserved()

    def test_retry_refuses_foreign_copy_container_even_when_volume_is_owned(self):
        self.fixture()
        with interrupt(rename, "save", lambda root, state: state["renamed_from"]["volume_copied"]), self.assertRaises(KeyboardInterrupt):
            self.convert()
        self.host.project_containers["oac-0123456789"]["copy-database"]["io.oac.conversion"] = "someone-else"
        before = copy.deepcopy(self.host.volumes)
        with self.assertRaisesRegex(rename.RenameError, "unowned container"):
            self.convert()
        self.assertEqual(before, self.host.volumes)
        self.assertFalse(any(command[:3] == ["docker", "volume", "rm"] for command in self.host.commands))

    def test_completed_volume_definition_matches_copy_and_confirmation_is_once(self):
        self.fixture()
        with mock.patch.object(rename.sys.stdin, "isatty", return_value=True), mock.patch("builtins.input", return_value="yes") as prompt:
            self.convert(yes=False)
        self.assertEqual(prompt.call_count, 1)
        compose = json.loads((self.root / "generated/compose.json").read_text())
        labels = self.host.volumes["oac-0123456789_database"]["Labels"]
        self.assertEqual(compose["volumes"]["database"]["labels"], {key: value for key, value in labels.items() if key.startswith("io.oac.")})
        self.assert_preserved()


    def test_rollback_before_old_project_removal_cannot_reuse_stale_copy(self):
        self.fixture()
        original = install.run
        def interrupted_down(command, **kwargs):
            if command[-1] == "down":
                raise KeyboardInterrupt()
            return original(command, **kwargs)
        with mock.patch.object(install, "run", side_effect=interrupted_down), self.assertRaises(KeyboardInterrupt):
            self.convert()
        self.assertTrue(self.state()["renamed_from"]["volume_copied"])
        # The operator used the documented pre-move rollback, then wrote new data.
        self.host.containers["database"]["running"] = True
        self.host.volumes["parsar-0123456789_database"]["Data"]["after-rollback"] = b"must survive"
        self.before_volumes = copy.deepcopy({"parsar-0123456789_database": self.host.volumes["parsar-0123456789_database"]})
        self.convert()
        self.assertEqual(self.host.copy_count, 2)
        self.assert_preserved()


    def test_same_container_name_with_foreign_labels_refuses_before_mutation(self):
        for mode in ("all", "web-only"):
            with self.subTest(mode=mode):
                self.root = self.work / ("foreign-name-" + mode)
                self.host.containers.clear()
                self.host.volumes.clear()
                self.fixture(mode=mode)
                self.host.named_containers["oac-0123456789-web-1"] = {"owner": "foreign"}
                commands_before = len(self.host.commands)
                with self.assertRaisesRegex(rename.RenameError, "target Compose project"):
                    self.convert()
                self.assertEqual(self.snapshot(), self.before)
                self.assertEqual(self.host.volumes, self.before_volumes)
                self.assertFalse(any("stop" in command or "rm" in command or "create" in command
                                     for command in self.host.commands[commands_before:]))
