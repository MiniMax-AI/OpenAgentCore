"""Acceptance behavior of the oac command against a fake Docker/systemd host."""
import hashlib
import json
from pathlib import Path
import stat
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import config_model
import install
import native_service
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


class ParsarTests(unittest.TestCase):
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

    def install(self, mode="all", native=False, **values):
        if native:
            values["ports.database"] = 15432
        config = config_model.initial(mode, native, **values)
        key = None
        if mode == "web-only":
            key = self.work / "existing.key"
            key.write_text("k" * 40)
            key.chmod(0o600)
        images = {name: IMAGES[name] for name in install.image_names(mode, native)}
        install.create(self.root, SimpleNamespace(core_key_file=key), config, {"source_commit": "a" * 40}, images)
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
        if config["mode"] != "web-only":
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

    def test_native_core_restarts_only_when_its_inputs_change(self):
        self.install(native=True)
        self.edit(lambda config: config["ports"].update(web=18080))
        self.apply()
        self.assertEqual((self.host.native["restarts"], self.host.recreated), (0, ["web"]))
        self.host.recreated.clear()
        self.edit(lambda config: config["ports"].update(core=18091))
        self.apply()
        self.assertEqual((self.host.native["restarts"], self.host.native["addr"]), (1, 18091))
        self.assertEqual(self.host.recreated, ["web"])
        web = json.loads(self.generated("compose.json"))["services"]["web"]
        self.assertEqual(web["environment"]["OAC_WEB_UPSTREAM"], "http://127.0.0.1:18091")
        # A repair (install.sh rerun) applies with start; the active unit still restarts for new inputs.
        self.edit(lambda config: config["log"].update(level="debug"))
        self.apply(start=True)
        self.assertEqual(self.host.native["restarts"], 2)
        self.assertIn('OAC_LOG_LEVEL="debug"', self.host.native["environment"])
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
        self.edit(lambda config: config.update(native_core=True, ports=dict(config["ports"], database=15432)))
        with self.assertRaisesRegex(oac_cli.OacError, "native_core is fixed"):
            self.apply()
        self.edit(lambda config: (config.pop("native_core"), config["ports"].pop("database")))
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

    def test_web_only_neither_rotates_nor_sends_its_key_to_an_unapplied_core(self):
        self.host.remote_core["https://core.example"] = (200, self.host.core_installation_id)
        self.install("web-only", **{"web.core_url": "https://core.example"})
        with self.assertRaisesRegex(oac_cli.OacError, "Core owns the Core key"):
            oac_cli.rotate_core_key(self.root, yes=True, out=self.output.append)
        self.edit(lambda config: config["web"].update(core_url="https://other.example"))
        calls = []
        with mock.patch.object(oac_cli, "http", side_effect=lambda url, *a, **k: calls.append(url) or (0, b"")):
            self.apply(dry_run=True)
        self.assertEqual(calls, [])
        self.assertIn("web.core_url changes; apply checks which Core it reaches.", self.output)

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
        for native in (False, True):
            with self.subTest(native=native):
                self.root = self.work / f"repeated-{native}"
                self.host.native_root, self.host.containers = self.root, {}
                self.host.native.update(active=False, inputs=None, loaded=None)
                self.install(native=native)
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
        native_stages = dict(compose_stages, **{
            "after reload": (native_service, "restart", lambda *a: True),
            "after Core": (oac_cli, "compose", compose_up()),
        })
        for native in (False, True):
            for stage, (target, name, when) in (native_stages if native else compose_stages).items():
                for operation in ("apply", "rotate", "rollback"):
                    with self.subTest(native=native, stage=stage, operation=operation):
                        self.root = self.work / f"{native}-{stage}-{operation}".replace(" ", "-")
                        self.host.native_root, self.host.containers = self.root, {}
                        self.host.native.update(active=False, inputs=None, loaded=None)
                        self.install(native=native)
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

if __name__ == "__main__":
    unittest.main()
