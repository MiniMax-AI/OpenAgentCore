"""install.sh --convert from installations made before config.json.

Fixtures come from the earlier installers' own generators, kept verbatim in
testdata/: deploy/install/configuration.py at 5c3dcc16 (since #124) and at b37e43b9
(#138, which changed core.env only).
"""
import contextlib
import hashlib
import importlib.util
import inspect
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import configuration
import convert
import install
import parsar_cli
from installer_fakes import MANIFEST, FakeHost, make_bundle, run_installer

OLD_IMAGES = {name: "sha256:" + digit * 64 for name, digit in (("core", "7"), ("database", "8"), ("web", "9"))}
GENERATORS = {"5c3dcc16": "aa09530ffe7d6f41c0bf9b273261bd1e6cc94a26", "b37e43b9": "b79f653ce2e086852a474d35030fbf585b6a0acf"}


def generator(version):
    path = Path(__file__).with_name("testdata") / f"configuration_{version}.py"
    spec = importlib.util.spec_from_file_location(f"configuration_{version}", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


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

    def legacy(self, version="5c3dcc16", mode="all", native=False, public_url=None, environment=None, core_url=None,
               drop=()):
        """An installation as that release's installer wrote it, with its services running."""
        old_generator = generator(version)
        old = {"version": 1, "source_commit": "b" * 40, "mode": mode, "native_core": native,
               "installation_id": "94be54a1-138c-4f30-bc87-b13686272dbe", "project": "parsar-0123456789",
               "uid": os.getuid(), "gid": os.getgid(), "core_port": 8091, "web_port": 8080,
               "core_url": core_url, "public_url": public_url}
        if native:
            old["database_port"] = 15432
        self.root.mkdir(mode=0o700, exist_ok=True)
        self.root.chmod(0o700)
        key = "c" * 64
        self.private("admin/core.key", key)
        password = ""
        digests = [hashlib.sha256(key.encode()).hexdigest()]
        if mode != "web-only":
            (self.root / "state/e2b").mkdir(mode=0o700, parents=True)
            password = "d" * 64
            self.private("config/credential.key", "credential-key-base64")
            self.private("config/database.password", password)
            self.private("admin/core-key-digests.json", json.dumps(digests))
            arguments = (self.root, old, password)[:len(inspect.signature(old_generator.core_environment).parameters)]
            values = old_generator.core_environment(*arguments)
            values.update(environment or {})
            for name in drop:
                values.pop(name)
            self.private("config/core.env", old_generator.environment_text(values))
            if native:
                self.private(f"config/{old['project']}-core.service", "[Unit]\n")
                self.host.native.update(active=True, addr=8091, digests=digests)
        compose = old_generator.compose_config(self.root, old, {"images": OLD_IMAGES}, password)
        self.private("compose.json", json.dumps(compose, indent=2))
        for name in compose["services"]:
            if name != "migrate":
                self.host.run_container(name, port=8091, digests=digests)
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

    def assertConverted(self):
        state = self.document("state.json")
        config = parsar_cli.load_config(self.root)
        rendered, _, _ = parsar_cli.render_now(self.root, config, state)
        actual = parsar_cli.observe(state)
        for name, digest in rendered.services.items():
            if name != "migrate":
                self.assertEqual((actual[name]["running"], actual[name]["inputs"]), (True, digest), name)
        for name in ("installation.json", "compose.json", "config/core.env", "admin"):
            self.assertFalse((self.root / name).exists(), name)
        self.assertTrue(state["converted_from"]["finished"])

    def test_the_fixtures_are_the_earlier_generators(self):
        for version, blob in GENERATORS.items():
            path = Path(__file__).with_name("testdata") / f"configuration_{version}.py"
            # Popen: the fake host replaces subprocess.run.
            with subprocess.Popen(["git", "hash-object", str(path)], stdout=subprocess.PIPE, text=True) as git:
                output = git.communicate()[0]
            if git.returncode:
                self.skipTest("git is unavailable")
            self.assertEqual(output.strip(), blob, version)

    def test_every_mode_of_both_earlier_layouts_converts(self):
        for version in GENERATORS:
            for mode, native in (("all", False), ("core-only", False), ("web-only", False), ("all", True)):
                with self.subTest(version=version, mode=mode, native=native):
                    self.root = self.work / f"{version}-{mode}-{native}"
                    self.host.native_root, self.host.containers = self.root, {}
                    self.host.native.update(active=False, inputs=None, loaded=None)
                    inodes = {}
                    self.legacy(version, mode, native, public_url="https://core.example",
                                core_url="https://core.example" if mode == "web-only" else None)
                    self.host.deployment_core_url = "https://core.example"
                    for source in ("admin/core.key", "config/credential.key", "config/database.password"):
                        if (self.root / source).exists():
                            inodes[Path(source).name] = (self.root / source).stat().st_ino
                    self.convert()
                    config = self.document("config.json")
                    self.assertEqual((config["mode"], config["public_url"]), (mode, "https://core.example"))
                    self.assertEqual({name: (self.root / "secrets" / name).stat().st_ino for name in inodes}, inodes)
                    self.assertEqual(set(self.document("state.json")["secrets_sha256"]),
                                     set() if mode == "web-only" else {"credential.key", "database.password"})
                    if native:
                        self.assertEqual(config["ports"]["database"], 15432)
                        self.assertIn(["systemctl", "--user", "disable", "--now", "parsar-0123456789-core.service"],
                                      self.host.commands)
                    self.assertConverted()

    def test_a_local_only_138_install_keeps_no_public_url(self):
        # The #138 deployment reports Core's loopback fallback; it is derived, not a public URL.
        for mode in ("all", "core-only"):
            with self.subTest(mode=mode):
                self.root, self.host.containers = self.work / f"local-{mode}", {}
                self.legacy("b37e43b9", mode)
                self.host.deployment_core_url = "http://127.0.0.1:8091"
                self.convert()
                self.assertIsNone(self.document("config.json")["public_url"])
                environment = configuration.read_environment((self.root / "generated/core.env").read_text())
                self.assertEqual(environment["AGENTS_API_PUBLIC_URL"], "http://127.0.0.1:8091")
                services = self.document("generated/compose.json")["services"]
                if mode == "all":
                    self.assertEqual(services["web"]["environment"]["CORE_CONSOLE_ORIGIN"], "http://127.0.0.1:8080")
                self.assertFalse([url for url in self.host.requests if url.endswith("/core/v1/sandbox/deployment")])
                self.assertConverted()

    def test_native_conversion_checks_the_host_first(self):
        self.legacy(native=True)
        before = self.snapshot()
        refused = mock.patch.object(install.native_service, "preflight",
                                    side_effect=RuntimeError("Native Core shared libraries cannot load on this host"))
        with refused, self.assertRaisesRegex(RuntimeError, "shared libraries"):
            self.convert()
        self.assertEqual(self.snapshot(), before)
        # Finishing a conversion whose first start failed checks the host again.
        self.host.core["fails"] = True
        with self.assertRaises(parsar_cli.ParsarError):
            self.convert()
        self.host.core["fails"] = False
        before = self.snapshot()
        with refused, mock.patch.object(install, "finish") as finish, self.assertRaisesRegex(RuntimeError, "shared libraries"):
            self.convert()
        finish.assert_not_called()
        self.assertEqual(self.snapshot(), before)

    def test_hand_set_settings_move_into_config_json(self):
        self.private("config/execution-options.json", '{"codex_provider": {"bearer_token": "model-secret"}}')
        self.private("config/history.json", json.dumps({"endpoint": "collector.example:4317",
                                                        "headers": {"Authorization": "Bearer export-secret"}}))
        self.legacy(public_url="https://core.example", environment={
            "AGENTS_API_EXECUTION_CONCURRENCY": "8", "PARSAR_LOG_LEVEL": "debug",
            "AGENTS_API_HARNESSES": "codex,mcode", "AGENTS_API_RUNTIME_HISTORY_FILE": "/config/history.json",
            "AGENTS_API_EXECUTION_OPTIONS_FILE": "/config/execution-options.json",
            "AGENTS_API_DATABASE_URL": "postgres://agents_api:" + "d" * 64 + "@database:5432/agents_api?sslmode=disable&pool_max_conns=16"})
        self.host.deployment_core_url = "https://core.example"
        self.convert()
        config, state = self.document("config.json"), self.document("state.json")
        self.assertEqual(config["core"]["execution_concurrency"], 8)
        self.assertEqual(config["core"]["harnesses"], ["codex", "mcode"])
        self.assertEqual(config["core"]["database_pool"]["max_conns"], 16)
        self.assertEqual(config["log"]["level"], "debug")
        self.assertEqual(config["core"]["runtime_history"]["headers"], {"Authorization": "Bearer export-secret"})
        self.assertFalse((self.root / "config/history.json").exists())
        # The retired options file is not carried over: Core refuses to start while it is set.
        self.assertNotIn("execution_options", json.dumps(config) + json.dumps(state))
        self.assertEqual(sorted(path.name for path in (self.root / "config").iterdir()), ["execution-options.json"])
        self.assertNotIn("EXECUTION_OPTIONS", (self.root / "generated/core.env").read_text())
        self.assertIn("AGENTS_API_EXECUTION_OPTIONS_FILE is retired by this release", self.output.getvalue())
        output = self.output.getvalue() + (self.root / "generated/settings.json").read_text()
        self.assertNotIn("model-secret", output)
        self.assertNotIn("export-secret", output)
        self.assertNotIn("No execution node was installed", self.output.getvalue())
        self.assertConverted()

    def test_a_core_upgraded_in_place_keeps_the_address_it_uses(self):
        # Following the #138 upgrade note: retired lines removed, the deployment's address set by hand.
        upgraded = {"drop": ("AGENTS_API_DAEMON_WS_URL", "AGENTS_API_CONFIG_FILE")}
        self.legacy("5c3dcc16", "core-only", environment={"AGENTS_API_PUBLIC_URL": "https://nodes.example"}, **upgraded)
        self.convert()
        self.assertEqual(self.document("config.json")["public_url"], "https://nodes.example")
        self.assertIn("taken from AGENTS_API_PUBLIC_URL", self.output.getvalue())
        self.assertConverted()
        # Core's own loopback fallback set by hand means no public URL.
        self.root, self.host.containers = self.work / "fallback", {}
        self.legacy("5c3dcc16", "core-only", environment={"AGENTS_API_PUBLIC_URL": "http://127.0.0.1:8091"}, **upgraded)
        self.convert()
        self.assertIsNone(self.document("config.json")["public_url"])
        # Another loopback address, or one that would move Web's origin, is reported.
        for name, mode, value, message in (
                ("localhost", "core-only", "http://localhost:8091", "is a loopback address other than Core's own"),
                ("web", "all", "https://nodes.example", "which Web uses, is none")):
            self.root, self.host.containers = self.work / name, {}
            self.legacy("5c3dcc16", mode, environment={"AGENTS_API_PUBLIC_URL": value}, **upgraded)
            before = self.snapshot()
            with self.assertRaisesRegex(convert.ConvertError, message):
                self.convert()
            self.assertEqual(self.snapshot(), before)
        self.convert("--public-url", "https://nodes.example")
        self.assertEqual(self.document("config.json")["public_url"], "https://nodes.example")

    def test_web_only_converts_before_its_core(self):
        self.legacy(mode="web-only", core_url="https://core.example")
        self.host.remote_core["https://core.example"] = (404, None)
        self.convert()
        self.assertIsNone(self.document("state.json")["core_installation_id"])
        self.assertIn("the paired Core runs an earlier release", self.output.getvalue())
        self.host.remote_core["https://core.example"] = (200, self.host.core_installation_id)
        parsar_cli.apply(self.root, interactive=False, out=lambda line: None)
        self.assertEqual(self.document("state.json")["core_installation_id"], self.host.core_installation_id)

    def test_unconvertible_installations_are_refused_without_changes(self):
        cases = {
            "edited": ({"AGENTS_API_UNKNOWN": "1", "AGENTS_API_ADDR": ":9999",
                        "AGENTS_API_DAEMON_WS_URL": "wss://elsewhere.example/api/v1/agent-daemon/ws"}, False),
            "unsafe secrets": ({}, True),
        }
        for name, (environment, unsafe) in cases.items():
            with self.subTest(name=name):
                self.root = self.work / name.replace(" ", "-")
                self.host.containers = {}
                self.legacy(environment=environment)
                if unsafe:
                    password = self.root / "config/database.password"
                    elsewhere = self.work / "elsewhere.password"
                    elsewhere.write_bytes(password.read_bytes())
                    password.unlink()
                    password.symlink_to(elsewhere)
                    (self.root / "admin/core.key").chmod(0o644)
                else:
                    compose = self.document("compose.json")
                    compose["services"]["web"]["environment"]["EXTRA"] = "1"
                    (self.root / "compose.json").write_text(json.dumps(compose))
                before = self.snapshot()
                with self.assertRaises(convert.ConvertError) as raised:
                    self.convert()
                message = str(raised.exception)
                expected = (["admin/core.key: must be a private regular file", "config/database.password: must be"]
                            if unsafe else ["compose.json: services.web.environment.EXTRA", "config/core.env: AGENTS_API_UNKNOWN",
                                            "config/core.env: AGENTS_API_ADDR", "AGENTS_API_DAEMON_WS_URL: is retired and was "
                                            "edited; remove the line"])
                for part in expected + ["Nothing was changed."]:
                    self.assertIn(part, message)
                self.assertNotIn("restore", message)
                self.assertEqual(self.snapshot(), before)

    def test_public_address_conflicts_and_letter_case(self):
        self.legacy(public_url="https://core.example")
        self.host.deployment_core_url = "https://nodes.example"
        before = self.snapshot()
        with self.assertRaisesRegex(convert.ConvertError, "--public-url https://core.example or --public-url https://nodes.example"):
            self.convert()
        self.assertEqual(self.snapshot(), before)
        self.convert("--public-url", "https://nodes.example")
        self.assertEqual(self.document("config.json")["public_url"], "https://nodes.example")
        self.root, self.host.containers = self.work / "case", {}
        self.legacy(public_url="https://Core.Example")
        self.host.deployment_core_url = "https://core.example"
        self.convert()
        self.assertEqual(self.document("config.json")["public_url"], "https://core.example")
        self.assertIn("is written as https://core.example", self.output.getvalue())

    def test_an_interrupted_or_failed_conversion_is_finished_by_rerunning_it(self):
        self.legacy(native=True, public_url="https://core.example")
        self.host.deployment_core_url = "https://core.example"
        rename, calls = os.rename, []

        def interrupted(source, target):
            calls.append(source)
            if len(calls) == 2:
                raise OSError("interrupted")
            rename(source, target)

        # The old native unit is already disabled when the layout moves, so it starts by path.
        with mock.patch.object(convert.os, "rename", side_effect=interrupted), self.assertRaises(OSError):
            self.convert()
        with self.assertRaisesRegex(install.InstallError, "interrupted"):
            run_installer(install, self.bundle, ["--install-dir", self.root])
        other, _ = make_bundle(self.work / "other", MANIFEST, commit="b" * 40)
        with self.assertRaisesRegex(convert.ConvertError, "bundle it started with"):
            run_installer(install, other, ["--install-dir", self.root, "--convert", "--yes"])
        with self.assertRaisesRegex(convert.ConvertError, "already set public_url"):
            self.convert("--public-url", "https://else.example")
        self.host.core["fails"] = True
        with self.assertRaisesRegex(parsar_cli.ParsarError, "rerun ./install.sh --convert --install-dir"):
            self.convert("--public-url", "https://core.example")
        self.host.core["fails"] = False
        self.convert()
        self.assertConverted()
        with self.assertRaisesRegex(install.InstallError, "already uses config.json"):
            self.convert()


if __name__ == "__main__":
    unittest.main()
