"""Acceptance behavior of the oac command against a fake Docker/systemd host."""
import hashlib
import http.client
import json
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest import mock

import config_model
import install
import oac_cli
from installer_fakes import FakeHost

IMAGES = {name: "sha256:" + digit * 64 for name, digit in (("core", "1"), ("database", "3"), ("web", "4"))}


def interrupt(target, name, when=lambda *args, **kwargs: True):
    """Raise KeyboardInterrupt once, at the first call that matches."""
    original, fired = getattr(target, name), []

    def wrapper(*args, **kwargs):
        if not fired and when(*args, **kwargs):
            fired.append(True)
            raise KeyboardInterrupt
        return original(*args, **kwargs)
    return mock.patch.object(target, name, wrapper)


def compose_up(*services):
    def when(root, *args, **kwargs):
        named = [item for item in args[1:] if not item.startswith("-") and not item.isdigit()]
        return args[:1] == ("up",) and named == list(services)
    return when


class OacTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".oac/tests/oac"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name).resolve()
        self.root = self.work / "core"
        self.host = FakeHost(self)
        self.host.native_root = self.root
        self.output = []
        # Each stamp is a new second, so nothing depends on finishing within one.
        clock = iter(range(10 ** 6))
        patcher = mock.patch.object(oac_cli, "now", side_effect=lambda: f"2026-09-25T10:{next(clock):06d}Z")
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_default_parent_is_private_and_symlinks_are_refused(self):
        home = self.work / "home"
        home.mkdir()
        parent = home / ".oac"
        parent.mkdir(mode=0o755)
        with mock.patch.object(Path, "home", return_value=home):
            oac_cli.private_parent(parent / "core")
            self.assertEqual(stat.S_IMODE(parent.stat().st_mode), 0o700)
            linked = home / "linked"
            linked.symlink_to(parent, target_is_directory=True)
            with self.assertRaisesRegex(oac_cli.OacError, "canonical"):
                oac_cli.private_parent(linked / "core")
            self.assertFalse((parent / "core").exists())

    def test_foreign_operator_refuses_all_mutations_before_lock_creation(self):
        self.install()
        (self.root / ".oac.lock").unlink()
        before = {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()}
        with mock.patch.object(oac_cli, "SOURCE_COMMIT", "b" * 40):
            for operation in (oac_cli.start, oac_cli.stop, oac_cli.apply, oac_cli.rotate_core_key):
                with self.subTest(operation=operation.__name__), self.assertRaisesRegex(oac_cli.OacError, "not supported;.*reinstall"):
                    operation(self.root)
        self.assertEqual(before, {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()})

    def install(self, **values):
        config = config_model.initial(**values)
        images = {name: IMAGES[name] for name in install.image_names()}
        install.create(self.root, config, {"source_commit": "a" * 40}, images)
        # These tests use finished installations; the installer records that after the first start.
        oac_cli.save_state(self.root, dict(oac_cli.load_state(self.root), complete=True))
        self.apply(start=True)
        self.host.recreated.clear()
        self.output.clear()

    def apply(self, **options):
        options.setdefault("interactive", False)
        return oac_cli.apply(self.root, out=self.output.append, **options)

    def edit(self, change):
        path = self.root / "config.json"
        config = json.loads(path.read_text())
        change(config)
        path.write_text(json.dumps(config, indent=2))

    def generated(self, name):
        return (self.root / "generated" / name).read_text()

    def environment(self):
        return dict(line.split("=", 1) for line in self.generated("core.env").splitlines() if not line.startswith("#"))

    def status(self):
        self.output.clear()
        try:
            oac_cli.status(self.root, out=self.output.append)
        except oac_cli.OacError as error:
            self.output.append(str(error))
        return self.output

    def assertConverged(self):
        state, config = oac_cli.load_state(self.root), oac_cli.load_config(self.root)
        rendered, disk, _ = oac_cli.render_now(self.root, config, state)
        actual = oac_cli.observe(state)
        for name, digest in rendered.services.items():
            if name != "migrate":
                self.assertEqual((actual[name]["running"], actual[name]["inputs"]), (True, digest), name)
        for name, text in rendered.files.items():
            self.assertEqual(oac_cli.comparable(name, disk[name]), oac_cli.comparable(name, text), name)
        key = (self.root / "secrets/core.key").read_text()
        url = f'http://127.0.0.1:{config["ports"]["core"]}/core/v1/installation'
        self.assertEqual(self.host.http(url, oac_cli.bearer(key))[0], 200)
        for line in self.status():
            self.assertNotRegex(line, "edited by hand|other inputs|not applied|rejects|unavailable")

    def test_apply_changes_a_port_and_the_log_level_and_recreates_only_affected_services(self):
        self.install()
        self.edit(lambda config: config["ports"].update(web=18080))
        self.apply(dry_run=True)
        self.assertEqual(self.host.recreated, [])
        self.assertIn("Services to restart: web. Every Web sign-in session ends.", self.output)
        self.apply()
        self.assertEqual(self.host.recreated, ["web"])
        self.assertEqual(self.host.web_port, 18080)
        self.host.recreated.clear()
        self.edit(lambda config: config["log"].update(level="debug"))
        self.apply()
        self.assertEqual(sorted(self.host.recreated), ["core", "migrate", "web"])
        self.assertEqual(self.environment()["OAC_LOG_LEVEL"], '"debug"')
        self.output.clear()
        self.apply()
        self.assertIn("Nothing to apply.", self.output)
        self.assertConverged()

    def test_a_stopped_installation_stays_stopped(self):
        self.install()
        oac_cli.stop(self.root, out=self.output.append)
        self.edit(lambda config: config["ports"].update(web=18080))
        self.apply()
        self.assertEqual((self.host.recreated, self.host.running()), ([], set()))
        self.assertIn("The installation is stopped; it stays stopped and starts with these files.", self.output)
        oac_cli.start(self.root, out=self.output.append)
        self.assertEqual(self.host.recreated, ["web"])
        self.assertEqual(self.host.web_port, 18080)
        # While any service runs, apply starts the others too.
        self.host.containers["web"]["running"] = False
        self.apply()
        self.assertConverged()

    def test_hand_edits_are_refused_until_discarded_and_missing_files_are_rewritten(self):
        self.install()
        path = self.root / "generated/core.env"
        edited = path.read_text() + 'OAC_EXECUTION_CONCURRENCY="9"\n'
        path.write_text(edited)
        with self.assertRaisesRegex(oac_cli.OacError, "generated/core.env was edited by hand"):
            self.apply()
        self.assertEqual(path.read_text(), edited)
        self.assertIn("generated/core.env was edited by hand; put the change in config.json and run oac apply "
                      "--discard-edits", self.status())
        self.apply(discard_edits=True)
        self.assertNotIn('"9"', path.read_text())
        [copy] = self.root.glob("generated/core.env.edited-*")
        self.assertEqual((copy.read_text(), stat.S_IMODE(copy.stat().st_mode)), (edited, 0o600))
        (self.root / "generated/compose.json").unlink()
        (self.root / "generated/settings.json").unlink()
        self.apply()
        self.assertConverged()

    def test_secrets_fixed_fields_and_directories_are_checked(self):
        self.install()
        self.edit(lambda config: config.update(ingress="managed", host="0.0.0.0"))
        with self.assertRaisesRegex(oac_cli.OacError, "ingress is fixed"):
            self.apply()
        self.edit(lambda config: config.update(ingress="external", host="127.0.0.1"))
        (self.root / "generated").chmod(0o755)
        with self.assertRaisesRegex(oac_cli.OacError, "generated/ must be a directory with mode 0700"):
            self.apply()
        (self.root / "generated").chmod(0o700)
        (self.root / "secrets").rename(self.work / "secrets")
        (self.root / "secrets").symlink_to(self.work / "secrets")
        with self.assertRaisesRegex(oac_cli.OacError, "secrets/ must be a directory"):
            self.apply()
        (self.root / "secrets").unlink()
        (self.work / "secrets").rename(self.root / "secrets")
        (self.root / "secrets/credential.key").write_text("replaced")
        with self.assertRaisesRegex(oac_cli.OacError, "credential.key changed"):
            self.apply()

    def test_rotate_core_key(self):
        self.install()
        old = (self.root / "secrets/core.key").read_text()
        (self.root / "secrets/core.key.new").write_text("left by an interrupted rotation")
        oac_cli.rotate_core_key(self.root, yes=True, out=self.output.append)
        new = (self.root / "secrets/core.key").read_text()
        self.assertNotEqual(new, old)
        self.assertFalse((self.root / "secrets/core.key.new").exists())
        self.assertEqual(stat.S_IMODE((self.root / "secrets/core.key").stat().st_mode), 0o600)
        self.assertEqual(json.loads(self.generated("core-key-digests.json")), [hashlib.sha256(new.encode()).hexdigest()])
        self.assertLess(self.host.recreated.index("core"), self.host.recreated.index("web"))
        url = "http://127.0.0.1:8091/core/v1/installation"
        self.assertEqual(self.host.http(url, oac_cli.bearer(old))[0], 401)
        self.assertConverged()

    def test_public_url_change_lists_bindings_and_requires_confirmation(self):
        self.install(public_url="https://core.example")
        self.host.bindings.update(nodes=2, hosted_sandboxes=2)
        self.host.nodes = [{"name": "node-a", "online": True, "core_url": "https://core.example"},
                           {"name": "node-b", "online": True, "core_url": "https://older.example"}]
        self.edit(lambda config: config.update(public_url="https://new.example"))
        before = self.generated("core.env")
        with self.assertRaisesRegex(oac_cli.OacError, "--confirm-public-url-change https://new.example"):
            self.apply()
        self.assertIn("  node node-a: online", self.output)
        self.assertNotIn("  node node-b: online", self.output)
        with self.assertRaisesRegex(oac_cli.OacError, "must equal the new public URL"):
            self.apply(confirm_public_url_change="https://other.example")
        self.assertEqual(self.generated("core.env"), before)
        self.apply(confirm_public_url_change="https://new.example")
        self.assertEqual(self.environment()["OAC_PUBLIC_URL"], '"https://new.example"')
        # The count comes from Core's bindings, so a failed node list read still needs confirmation.
        self.host.bindings.update(nodes=1, nodes_on_other_address=0, hosted_sandboxes=0)
        self.edit(lambda config: config.update(public_url="https://fourth.example"))
        http = self.host.http
        with mock.patch.object(oac_cli, "http", side_effect=lambda url, *a, **k: (
                (500, b"") if url.endswith("/core/v1/sandbox/nodes") else http(url, *a, **k))), \
                self.assertRaisesRegex(oac_cli.OacError, "--confirm-public-url-change https://fourth.example"):
            self.apply()
        self.assertIn("Bound to the current address: 1 node(s), 0 hosted sandbox(es), 0 self-hosted executor credential(s).",
                      self.output)
        self.edit(lambda config: config.update(public_url="https://new.example"))
        # With Core stopped and core.env gone, the address in use is unknown: confirmation is needed.
        oac_cli.stop(self.root, out=self.output.append)
        (self.root / "generated/core.env").unlink()
        self.edit(lambda config: config.update(public_url="https://third.example"))
        with self.assertRaisesRegex(oac_cli.OacError, "--confirm-public-url-change https://third.example"):
            self.apply()
        self.apply(confirm_public_url_change="https://third.example")

    def test_core_startup_failure_rolls_back_only_from_a_converged_start(self):
        self.install()
        before = {name: self.generated(name) for name in ("core.env", "compose.json")}
        self.edit(lambda config: config["core"].update(execution_concurrency=8))
        self.host.core["fails"] = True
        self.host.core["log"] = 'noise\nlevel=ERROR msg="oac-core startup failed" error="synthetic rejection"\n'
        with self.assertRaisesRegex(oac_cli.OacError, "previous generated files were restored"):
            self.apply()
        self.assertEqual({name: self.generated(name) for name in before}, before)
        self.assertIn('level=ERROR msg="oac-core startup failed" error="synthetic rejection"', self.output)
        # Core is down now, so the next failure has nothing converged to roll back to.
        with self.assertRaisesRegex(oac_cli.OacError, "nothing was rolled back"):
            self.apply()
        self.host.core["fails"] = False
        self.apply()
        self.assertConverged()

    def test_repeated_rolled_back_applies_leave_no_false_edit(self):
        self.install()
        self.host.core["rejects"] = lambda environment: 'OAC_EXECUTION_CONCURRENCY="4"' not in environment
        for value in (5, 6, 7, 8):
            self.edit(lambda config: config["core"].update(execution_concurrency=value))
            with self.assertRaisesRegex(oac_cli.OacError, "services converged on them"):
                self.apply()
        self.host.core["rejects"] = lambda environment: False
        self.edit(lambda config: config["core"].update(execution_concurrency=4))
        self.apply()
        self.assertConverged()

    def test_a_hand_edit_restored_by_a_rollback_is_still_reported(self):
        self.install()
        path = self.root / "generated/core.env"
        path.write_text(path.read_text() + "# hand edit\n")
        self.host.core["rejects"] = lambda environment: 'OAC_EXECUTION_CONCURRENCY="5"' in environment
        self.edit(lambda config: config["core"].update(execution_concurrency=5))
        with self.assertRaisesRegex(oac_cli.OacError, "services converged on them"):
            self.apply(discard_edits=True)
        self.assertIn("# hand edit", path.read_text())
        self.assertIn("generated/core.env was edited by hand; put the change in config.json and run oac apply "
                      "--discard-edits", self.status())

    def test_the_next_apply_finishes_any_interrupted_apply_rotation_or_rollback(self):
        compose_stages = {
            "before any file": (oac_cli, "save_state", lambda *a: True),
            "between files": (oac_cli, "write_private",
                              lambda path, data: path.name in ("core.env", "core-key-digests.json")),
            "before converging": (oac_cli, "converge", lambda *a, **k: True),
            "after Core": (oac_cli, "compose", compose_up()),
            "before health": (oac_cli, "health", lambda *a: True),
        }
        for stage, (target, name, when) in compose_stages.items():
            for operation in ("apply", "rotate", "rollback"):
                with self.subTest(stage=stage, operation=operation):
                    self.root = self.work / f"{stage}-{operation}".replace(" ", "-")
                    self.host.containers = {}
                    self.install()
                    if operation == "rotate":
                        action = lambda: oac_cli.rotate_core_key(self.root, yes=True, out=self.output.append)
                    else:
                        self.edit(lambda config: (config["log"].update(level="debug"),
                                                  config["ports"].update(web=18080)))
                        action = self.apply
                    self.host.core.update(fails=operation == "rollback", failed=False)
                    # A rollback is interrupted in what it does after Core failed.
                    trigger = (lambda *a, when=when, **k: self.host.core["failed"] and when(*a, **k)) \
                        if operation == "rollback" else when
                    with interrupt(target, name, trigger), \
                            self.assertRaises((KeyboardInterrupt, oac_cli.OacError)) as raised:
                        action()
                    self.assertNotIn(": .", str(raised.exception))
                    self.host.core.update(fails=False, failed=False)
                    self.apply()
                    self.assertConverged()

    def test_uninstall_removes_the_services_volumes_unused_images_and_directory(self):
        self.install()
        project = oac_cli.load_state(self.root)["project"]
        self.host.other_containers[IMAGES["database"]] = ["id-of-another-installation"]
        self.host.deployment.update(provider="e2b", resources={"allocations": 2, "pending": 0})
        oac_cli.main(["uninstall", "--yes"], root=self.root, out=self.output.append)
        self.assertTrue(any(line.startswith("Core has 2 sandbox(es) in use. Uninstall does not stop them: E2B keeps "
                                            "running them, and billing for them.") for line in self.output))
        self.assertIn(["docker", "compose", "-p", project, "down", "--volumes", "--remove-orphans"], self.host.commands)
        self.assertEqual(self.host.containers, {})
        self.assertEqual(self.host.missing_images, {IMAGES["core"], IMAGES["web"]})
        self.assertIn(f'Kept the database image {IMAGES["database"]}: another container uses it.', self.output)
        self.assertFalse(self.root.exists())

    def test_uninstall_changes_nothing_unless_confirmed(self):
        self.install()
        before = {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()}
        with self.assertRaisesRegex(oac_cli.OacError, "--yes"):
            oac_cli.uninstall(self.root, interactive=False, out=self.output.append)
        with mock.patch("builtins.input", return_value=str(self.work)), \
                self.assertRaisesRegex(oac_cli.OacError, "not confirmed; nothing was removed"):
            oac_cli.uninstall(self.root, interactive=True, out=self.output.append)
        self.assertEqual(before, {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()})
        self.assertEqual(self.host.running(), {"database", "core", "web"})
        self.assertFalse(any("down" in command or "rm" in command for command in self.host.commands))

    def test_uninstall_removes_an_installation_that_never_finished(self):
        install.create(self.root, config_model.initial(),
                       {"source_commit": "a" * 40}, IMAGES)
        (self.root / "config.json").unlink()
        installation = oac_cli.load_state(self.root)["installation_id"]
        # create() marks it incomplete, which other mutating commands refuse.
        with self.assertRaisesRegex(oac_cli.OacError, "or remove it with oac uninstall"):
            oac_cli.start(self.root, out=self.output.append)
        oac_cli.uninstall(self.root, yes=True, out=self.output.append)
        self.assertFalse(self.root.exists())
        self.assertIn("Core did not answer, so its nodes and sandboxes can't be listed. Nodes stay on their hosts.",
                      self.output)
        self.assertIn(f"  sudo python3 node-install.pyz --uninstall --installation-id {installation} --force", self.output)

    def test_a_rerun_finishes_an_interrupted_uninstall(self):
        self.install()
        (self.root / "oac").write_text("the installed command")
        with interrupt(oac_cli.shutil, "rmtree"), self.assertRaisesRegex(oac_cli.OacError, "Removal did not finish"):
            oac_cli.uninstall(self.root, yes=True, out=self.output.append)
        self.assertEqual(self.host.containers, {})
        self.assertTrue((self.root / "state.json").exists() and (self.root / "oac").exists())
        oac_cli.uninstall(self.root, yes=True, out=self.output.append)
        self.assertFalse(self.root.exists())
        # Once state.json is gone, a rerun only says so.
        self.root.mkdir()
        (self.root / "oac").write_text("the installed command")
        oac_cli.uninstall(self.root, yes=True, out=self.output.append)
        self.assertIn(f"No installation is left in {self.root}: it has no state.json. Nothing was removed.", self.output)
        self.assertEqual([path.name for path in self.root.iterdir()], ["oac"])

    def test_uninstall_lists_registered_nodes_and_how_to_uninstall_them(self):
        self.install()
        self.host.nodes = [{"name": "node-a", "online": True}, {"name": "node-b", "online": False}]
        installation = oac_cli.load_state(self.root)["installation_id"]
        oac_cli.uninstall(self.root, yes=True, out=self.output.append)
        listed = self.output.index("Nodes registered with this Core, which stay on their hosts: node-a (online), node-b (offline)")
        self.assertLess(listed, self.output.index(f"Removed the installation in {self.root}."))
        self.assertIn(f"  sudo python3 node-install.pyz --uninstall --installation-id {installation} --force", self.output)

    def test_uninstall_preserves_state_when_docker_cannot_inspect_images(self):
        self.install()
        original = oac_cli.run
        for daemon_unavailable in (False, True):
            def fail_inspection(args, **kwargs):
                if args[:3] == ["docker", "image", "inspect"]:
                    return subprocess.CompletedProcess(args, 1, stdout="")
                if daemon_unavailable and args[:3] == ["docker", "image", "ls"]:
                    raise subprocess.CalledProcessError(1, args)
                return original(args, **kwargs)

            with self.subTest(daemon_unavailable=daemon_unavailable), \
                    mock.patch.object(oac_cli, "run", side_effect=fail_inspection), \
                    self.assertRaisesRegex(oac_cli.OacError, "the images of this installation"):
                oac_cli.uninstall(self.root, yes=True, out=self.output.append)
            self.assertTrue((self.root / "state.json").is_file())
            self.assertEqual(self.host.missing_images, set())
        self.host.missing_images.add(IMAGES["core"])
        oac_cli.uninstall(self.root, yes=True, out=self.output.append)
        self.assertFalse(self.root.exists())
        self.assertEqual(self.host.missing_images, set(IMAGES.values()))

    def test_uninstall_completes_when_core_returns_a_truncated_response(self):
        self.install()
        with mock.patch.object(oac_cli, "http", side_effect=http.client.IncompleteRead(b"{")):
            oac_cli.uninstall(self.root, yes=True, out=self.output.append)
        self.assertFalse(self.root.exists())
        self.assertIn("Core did not answer, so its nodes and sandboxes can't be listed. Nodes stay on their hosts.",
                      self.output)
        self.assertTrue(any("node-install.pyz --uninstall" in line and "--force" in line for line in self.output))

    def test_uninstall_does_not_probe_core_through_private_file_or_directory_links(self):
        self.install()
        for name in ("secrets/core.key", "generated", "secrets"):
            path = self.root / name
            outside = self.work / path.name
            path.rename(outside)
            path.symlink_to(outside, target_is_directory=outside.is_dir())
            with self.subTest(path=name), mock.patch.object(oac_cli, "http") as request, \
                    self.assertRaisesRegex(oac_cli.OacError, "--yes"):
                oac_cli.uninstall(self.root, interactive=False, out=self.output.append)
            request.assert_not_called()
            path.unlink()
            outside.rename(path)
        # An unsafe probe does not prevent removing the installation's own files.
        path.rename(outside)
        path.symlink_to(outside, target_is_directory=True)
        with mock.patch.object(oac_cli, "http") as request:
            oac_cli.uninstall(self.root, yes=True, out=self.output.append)
        request.assert_not_called()
        self.assertFalse(self.root.exists())
        self.assertTrue((outside / "core.key").is_file())


if __name__ == "__main__":
    unittest.main()
