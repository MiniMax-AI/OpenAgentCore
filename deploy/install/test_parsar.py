"""Acceptance behavior of the parsar command against a fake Docker/systemd host."""
import hashlib
import json
from pathlib import Path
import stat
import tempfile
from types import SimpleNamespace
import unittest

import config_model
import install
import parsar_cli
from installer_fakes import FakeHost

IMAGES = {name: "sha256:" + digit * 64 for name, digit in (("core", "1"), ("database", "3"), ("web", "4"))}


class ParsarTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".parsar/tests/parsar"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name).resolve()
        self.root = self.work / "core"
        self.host = FakeHost(self)
        self.host.native_root = self.root
        self.output = []

    def install(self, mode="all", native=False, **values):
        if native:
            values["ports.database"] = 15432
        config = config_model.initial(mode, native, **values)
        key = None
        if mode == "web-only":
            key = self.work / "existing.key"
            key.write_text("k" * 40)
            key.chmod(0o600)
        manifest = {"source_commit": "a" * 40}
        images = {name: IMAGES[name] for name in install.image_names(mode, native)}
        install.create(self.root, SimpleNamespace(core_key_file=key), config, manifest, images)
        self.apply(start=True)
        self.host.recreated.clear()
        self.output.clear()

    def apply(self, **options):
        options.setdefault("interactive", False)
        return parsar_cli.apply(self.root, out=self.output.append, **options)

    def edit(self, change):
        path = self.root / "config.json"
        config = json.loads(path.read_text())
        change(config)
        path.write_text(json.dumps(config, indent=2))

    def generated(self, name):
        return (self.root / "generated" / name).read_text()

    def environment(self):
        return dict(line.split("=", 1) for line in self.generated("core.env").splitlines() if not line.startswith("#"))

    def test_apply_changes_a_port_and_the_log_level_and_recreates_only_affected_services(self):
        self.install()
        self.edit(lambda config: config["ports"].update(web=18080))
        self.apply(dry_run=True)
        self.assertEqual(self.host.recreated, [])
        self.assertIn("Services to restart: web. Every Web sign-in session ends.", self.output)
        self.assertIn('"127.0.0.1:8080:8080"', self.generated("compose.json"))
        self.apply()
        self.assertEqual(self.host.recreated, ["web"])
        self.assertEqual(self.host.web_port, 18080)
        self.host.recreated.clear()
        self.edit(lambda config: config["log"].update(level="debug"))
        self.apply()
        self.assertEqual(sorted(self.host.recreated), ["core", "migrate", "web"])
        self.assertEqual(self.environment()["PARSAR_LOG_LEVEL"], '"debug"')
        self.assertEqual(json.loads(self.generated("compose.json"))["services"]["web"]["environment"]["PARSAR_LOG_LEVEL"], "debug")
        self.output.clear()
        self.apply()
        self.assertIn("Nothing to apply.", self.output)

    def test_native_core_port_restarts_the_unit_and_moves_the_web_upstream(self):
        self.install(native=True)
        reloads = self.host.native["reloads"]
        self.edit(lambda config: config["ports"].update(core=18091))
        self.apply()
        self.assertEqual(self.host.native["restarts"], 1)
        self.assertEqual(self.host.native["addr"], 18091)
        self.assertEqual(self.host.recreated, ["web"])
        self.assertEqual(self.host.native["reloads"], reloads)
        web = json.loads(self.generated("compose.json"))["services"]["web"]
        self.assertEqual(web["environment"]["CORE_CONSOLE_UPSTREAM"], "http://127.0.0.1:18091")

    def test_stopped_services_stay_stopped(self):
        self.install()
        parsar_cli.stop(self.root, out=self.output.append)
        self.edit(lambda config: config["ports"].update(web=18080))
        self.apply()
        self.assertEqual(self.host.recreated, [])
        self.assertEqual(self.host.running, set())
        self.assertIn("Stopped services stay stopped: web", self.output)
        parsar_cli.start(self.root, out=self.output.append)
        self.assertEqual(self.host.recreated, ["web"])
        self.assertEqual(self.host.web_port, 18080)

    def test_hand_edited_generated_file_is_refused_until_discarded(self):
        self.install()
        path = self.root / "generated/core.env"
        edited = path.read_text() + 'AGENTS_API_EXECUTION_CONCURRENCY="9"\n'
        path.write_text(edited)
        with self.assertRaisesRegex(parsar_cli.ParsarError, "generated/core.env was edited by hand"):
            self.apply()
        self.assertEqual(path.read_text(), edited)
        parsar_cli.status(self.root, out=self.output.append)
        self.assertTrue(any("generated/core.env was edited by hand" in line for line in self.output))
        self.apply(discard_edits=True)
        self.assertNotIn('"9"', path.read_text())
        [copy] = self.root.glob("generated/core.env.edited-*")
        self.assertEqual(copy.read_text(), edited)
        self.assertEqual(stat.S_IMODE(copy.stat().st_mode), 0o600)

    def test_changed_credential_key_or_fixed_field_is_refused(self):
        self.install()
        self.edit(lambda config: config.update(native_core=True, ports=dict(config["ports"], database=15432)))
        with self.assertRaisesRegex(parsar_cli.ParsarError, "native_core is fixed"):
            self.apply()
        self.edit(lambda config: (config.pop("native_core"), config["ports"].pop("database")))
        (self.root / "secrets/credential.key").write_text("replaced")
        with self.assertRaisesRegex(parsar_cli.ParsarError, "credential.key changed"):
            self.apply()

    def test_rotate_core_key(self):
        self.install()
        old = (self.root / "secrets/core.key").read_text()
        parsar_cli.rotate_core_key(self.root, yes=True, out=self.output.append)
        new = (self.root / "secrets/core.key").read_text()
        self.assertNotEqual(new, old)
        self.assertEqual(stat.S_IMODE((self.root / "secrets/core.key").stat().st_mode), 0o600)
        self.assertFalse((self.root / "secrets/core.key.new").exists())
        self.assertEqual(json.loads(self.generated("core-key-digests.json")), [hashlib.sha256(new.encode()).hexdigest()])
        self.assertLess(self.host.recreated.index("core"), self.host.recreated.index("web"))
        url = "http://127.0.0.1:8091/core/v1/installation"
        self.assertEqual(self.host.http(url, parsar_cli.bearer(new))[0], 200)
        self.assertEqual(self.host.http(url, parsar_cli.bearer(old))[0], 401)
        self.assertTrue(any("Sign in to Web again" in line for line in self.output))
        self.output.clear()
        self.apply()
        self.assertIn("Nothing to apply.", self.output)

    def test_web_only_cannot_rotate_the_core_key(self):
        self.host.remote_core["https://core.example"] = (200, self.host.core_installation_id)
        self.install("web-only", **{"web.core_url": "https://core.example"})
        before = (self.root / "secrets/core.key").read_text()
        with self.assertRaisesRegex(parsar_cli.ParsarError, "Core owns the Core key"):
            parsar_cli.rotate_core_key(self.root, yes=True, out=self.output.append)
        self.assertEqual((self.root / "secrets/core.key").read_text(), before)

    def test_public_url_change_lists_bindings_and_requires_confirmation(self):
        self.install(public_url="https://core.example")
        self.host.bindings.update(nodes=1, hosted_sandboxes=2)
        self.host.nodes = [{"name": "node-a", "online": True}]
        self.edit(lambda config: config.update(public_url="https://new.example"))
        before = self.generated("core.env")
        with self.assertRaisesRegex(parsar_cli.ParsarError, "--confirm-public-url-change https://new.example"):
            self.apply()
        self.assertIn("  node node-a: online", self.output)
        with self.assertRaisesRegex(parsar_cli.ParsarError, "must equal the new public URL"):
            self.apply(confirm_public_url_change="https://other.example")
        self.assertEqual(self.generated("core.env"), before)
        self.apply(confirm_public_url_change="https://new.example")
        self.assertEqual(self.environment()["AGENTS_API_PUBLIC_URL"], '"https://new.example"')
        self.host.bindings.update(nodes=0, hosted_sandboxes=0)
        self.edit(lambda config: config.update(public_url=None))
        self.apply()
        self.assertEqual(self.environment()["AGENTS_API_PUBLIC_URL"], '"http://127.0.0.1:8091"')

    def test_core_startup_failure_restores_the_previous_files(self):
        self.install()
        before = {name: self.generated(name) for name in ("core.env", "compose.json", "settings.json")}
        state = (self.root / "state.json").read_text()
        self.edit(lambda config: config["core"].update(execution_concurrency=8))
        self.host.core["fails"] = True
        self.host.core["log"] = 'noise\nlevel=ERROR msg="agents-api startup failed" error="synthetic rejection"\n'
        with self.assertRaisesRegex(parsar_cli.ParsarError, "config.json not applied"):
            self.apply()
        self.assertEqual({name: self.generated(name) for name in before}, before)
        self.assertEqual((self.root / "state.json").read_text(), state)
        self.assertIn('level=ERROR msg="agents-api startup failed" error="synthetic rejection"', self.output)


if __name__ == "__main__":
    unittest.main()
