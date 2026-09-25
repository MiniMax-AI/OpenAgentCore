"""install.sh --convert from installations made before config.json."""
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import configuration
import convert
import install
from installer_fakes import MANIFEST, FakeHost, make_bundle, run_installer

OLD_IMAGES = {name: "sha256:" + digit * 64 for name, digit in (("core", "7"), ("database", "8"), ("web", "9"))}


class ConvertTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".parsar/tests/convert"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name).resolve()
        self.root = self.work / "core"
        self.bundle, self.manifest = make_bundle(self.work / "bundle", MANIFEST)
        self.host = FakeHost(self)
        self.host.native_root = self.root
        self.output = io.StringIO()

    def private(self, name, text):
        path = self.root / name
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        path.write_text(text)
        path.chmod(0o600)
        return path

    def legacy(self, mode="all", native=False, public_url=None, environment=None, core_url=None):
        """An installation exactly as the installer before config.json wrote it, and its running Core."""
        old = {"version": 1, "source_commit": "b" * 40, "mode": mode, "native_core": native,
               "installation_id": "94be54a1-138c-4f30-bc87-b13686272dbe", "project": "parsar-0123456789",
               "uid": os.getuid(), "gid": os.getgid(), "core_port": 8091, "web_port": 8080,
               "core_url": core_url, "public_url": public_url}
        if native:
            old["database_port"] = 15432
        self.root.mkdir(mode=0o700, exist_ok=True)
        key = "c" * 64
        self.private("admin/core.key", key)
        password = ""
        if mode != "web-only":
            (self.root / "state/e2b").mkdir(mode=0o700, parents=True)
            password = "d" * 64
            self.private("config/credential.key", "credential-key-base64")
            self.private("config/database.password", password)
            self.private("admin/core-key-digests.json", json.dumps([hashlib.sha256(key.encode()).hexdigest()]))
            values = convert.legacy_core_environment(self.root, old, password)
            values.update(environment or {})
            self.private("config/core.env", configuration.environment_text(values, "Core process configuration."))
            if native:
                self.private(f"config/{old['project']}-core.service", "[Unit]\n")
                self.host.native.update(active=True, addr=8091, digests=[hashlib.sha256(key.encode()).hexdigest()])
            else:
                self.host.running.add("core")
                self.host.core.update(port=8091, digests=[hashlib.sha256(key.encode()).hexdigest()])
        self.private("compose.json", json.dumps(convert.legacy_compose_config(self.root, old, OLD_IMAGES, password), indent=2))
        self.private("installation.json", json.dumps(old, indent=2))
        if mode != "core-only":
            self.private("node-payload/node-install.pyz", "old payload")
        if native:
            for name in ("bin/agents-api", "bin/agents-api-migrate", "e2b/agents-api-e2b-provider"):
                self.private("native/" + name, "old native binary")
        self.host.remote_core[core_url or "https://unused.example"] = (200, self.host.core_installation_id)
        return old

    def convert(self, *flags):
        with contextlib.redirect_stdout(self.output):
            run_installer(install, self.bundle, ["--install-dir", self.root, "--convert", "--yes", *flags])

    def snapshot(self):
        return {str(path.relative_to(self.root)): path.read_bytes() if path.is_file() else None
                for path in self.root.rglob("*")}

    def document(self, name):
        return json.loads((self.root / name).read_text())

    def test_all_mode_moves_secrets_maps_settings_and_starts_the_new_release(self):
        options = self.private("config/execution-options.json", '{"codex_provider": {"bearer_token": "model-secret"}}')
        self.private("config/history.json", json.dumps({"endpoint": "collector.example:4317",
                                                        "headers": {"Authorization": "Bearer export-secret"}}))
        self.legacy(public_url="https://core.example", environment={
            "AGENTS_API_EXECUTION_CONCURRENCY": "8", "PARSAR_LOG_LEVEL": "debug",
            "AGENTS_API_HARNESSES": "codex,mcode", "AGENTS_API_RUNTIME_HISTORY_FILE": "/config/history.json",
            "AGENTS_API_EXECUTION_OPTIONS_FILE": "/config/execution-options.json",
            "AGENTS_API_DATABASE_URL": "postgres://agents_api:" + "d" * 64 + "@database:5432/agents_api?sslmode=disable&pool_max_conns=16"})
        self.host.deployment_core_url = "https://core.example"
        inodes = {name: (self.root / source).stat().st_ino for source, name in (
            ("admin/core.key", "core.key"), ("config/credential.key", "credential.key"),
            ("config/database.password", "database.password"))}
        self.convert()
        config, state = self.document("config.json"), self.document("state.json")
        self.assertEqual(config["public_url"], "https://core.example")
        self.assertEqual(config["core"]["execution_concurrency"], 8)
        self.assertEqual(config["core"]["harnesses"], ["codex", "mcode"])
        self.assertEqual(config["core"]["database_pool"]["max_conns"], 16)
        self.assertEqual(config["log"]["level"], "debug")
        self.assertEqual(config["core"]["runtime_history"]["headers"], {"Authorization": "Bearer export-secret"})
        self.assertNotIn("execution_options", json.dumps(config))
        self.assertEqual((state["installation_id"], state["project"]), ("94be54a1-138c-4f30-bc87-b13686272dbe", "parsar-0123456789"))
        self.assertEqual(state["source_commit"], "a" * 40)
        self.assertEqual(state["converted_from"]["source_commit"], "b" * 40)
        for name, inode in inodes.items():
            self.assertEqual((self.root / "secrets" / name).stat().st_ino, inode)
        for name in ("installation.json", "compose.json", "config/core.env", "config/credential.key",
                     "admin", "config/parsar-0123456789-core.service"):
            self.assertFalse((self.root / name).exists(), name)
        self.assertEqual(sorted(path.name for path in (self.root / "config").iterdir()),
                         ["execution-options.json", "history.json"])
        self.assertEqual(state["execution_options_file"], {"variable": "/config/execution-options.json", "path": str(options)})
        environment = configuration.read_environment((self.root / "generated/core.env").read_text())
        self.assertEqual(environment["AGENTS_API_EXECUTION_OPTIONS_FILE"], "/config/execution-options.json")
        core = self.document("generated/compose.json")["services"]["core"]
        self.assertIn({"type": "bind", "source": str(options), "target": "/config/execution-options.json",
                       "read_only": True}, core["volumes"])
        self.assertIn("AGENTS_API_EXECUTION_OPTIONS_FILE", self.output.getvalue())
        self.assertNotIn("model-secret", self.output.getvalue() + (self.root / "generated/settings.json").read_text())
        self.assertNotIn("export-secret", self.output.getvalue())
        self.assertEqual(self.host.running, {"database", "core", "web"})
        self.assertEqual((self.root / "node-payload/node-install.pyz").read_bytes(), b"synthetic verified Python bootstrap")
        self.assertTrue((self.root / "parsar").is_file())

    def test_core_only_and_adopted_deployment_address(self):
        self.legacy("core-only")
        self.host.deployment_core_url = "https://nodes.example"
        self.convert()
        config = self.document("config.json")
        self.assertEqual((config["mode"], config["public_url"]), ("core-only", "https://nodes.example"))
        self.assertNotIn("web", self.document("generated/compose.json")["services"])
        self.assertIn("adopted from the sandbox deployment", self.output.getvalue())

    def test_web_only_moves_only_the_core_key(self):
        self.legacy("web-only", core_url="https://core.example", public_url="https://console.example")
        self.convert()
        config = self.document("config.json")
        self.assertEqual(config["web"], {"core_url": "https://core.example"})
        self.assertEqual(config["public_url"], "https://console.example")
        self.assertEqual(sorted(path.name for path in (self.root / "secrets").iterdir()), ["core.key"])
        self.assertFalse((self.root / "admin").exists())
        self.assertEqual(self.document("state.json")["core_installation_id"], self.host.core_installation_id)
        self.assertNotIn("/core/v1/sandbox/deployment", str(self.host.commands))

    def test_native_core_keeps_its_unit_name_and_database_port(self):
        self.legacy(native=True)
        self.convert()
        config = self.document("config.json")
        self.assertEqual((config["native_core"], config["ports"]["database"]), (True, 15432))
        self.assertIn(["systemctl", "--user", "disable", "--now", "parsar-0123456789-core.service"], self.host.commands)
        unit = self.root / "generated/parsar-0123456789-core.service"
        self.assertIn(["systemctl", "--user", "enable", "--now", str(unit)], self.host.commands)
        self.assertIn(f"EnvironmentFile={self.root / 'generated/core.env'}", unit.read_text())
        self.assertIn(b"a" * 40, (self.root / "native/bin/agents-api").read_bytes())
        self.assertTrue(self.host.native["active"])

    def test_unconvertible_installation_is_refused_without_changes(self):
        self.legacy(environment={"AGENTS_API_UNKNOWN": "1", "AGENTS_API_ADDR": ":9999"})
        compose = self.document("compose.json")
        compose["services"]["web"]["environment"]["EXTRA"] = "1"
        (self.root / "compose.json").write_text(json.dumps(compose))
        before = self.snapshot()
        with self.assertRaises(convert.ConvertError) as raised:
            self.convert()
        message = str(raised.exception)
        for part in ("compose.json: services.web.environment.EXTRA", "config/core.env: AGENTS_API_UNKNOWN",
                     "config/core.env: AGENTS_API_ADDR", "Nothing was changed."):
            self.assertIn(part, message)
        self.assertEqual(self.snapshot(), before)

    def test_conflicting_public_addresses_need_an_explicit_choice(self):
        self.legacy(public_url="https://core.example")
        self.host.deployment_core_url = "https://nodes.example"
        before = self.snapshot()
        with self.assertRaisesRegex(convert.ConvertError, "--public-url https://core.example or --public-url https://nodes.example"):
            self.convert()
        self.assertEqual(self.snapshot(), before)
        self.convert("--public-url", "https://nodes.example")
        self.assertEqual(self.document("config.json")["public_url"], "https://nodes.example")

    def test_interrupted_conversion_resumes(self):
        self.legacy()
        rename = os.rename
        calls = []

        def interrupted(source, target):
            calls.append(source)
            if len(calls) == 2:
                raise OSError("interrupted")
            rename(source, target)

        with mock.patch.object(convert.os, "rename", side_effect=interrupted), self.assertRaises(OSError):
            self.convert()
        self.assertTrue((self.root / "installation.json").exists() and (self.root / "config.json").exists())
        with contextlib.redirect_stdout(self.output), self.assertRaisesRegex(install.InstallError, "interrupted"):
            run_installer(install, self.bundle, ["--install-dir", self.root])
        self.convert()
        self.assertFalse((self.root / "installation.json").exists())
        self.assertEqual(sorted(path.name for path in (self.root / "secrets").iterdir()),
                         ["core.key", "credential.key", "database.password"])
        self.assertIn("Resuming an interrupted conversion.", self.output.getvalue())


if __name__ == "__main__":
    unittest.main()
