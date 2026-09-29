"""Installer listener selection and subsequent operator lifecycle behavior."""
import contextlib
import io
import json
from pathlib import Path
import socket
import tempfile
import unittest
from unittest import mock

import config_model
import configuration
import install
import oac_cli
from installer_fakes import MANIFEST, FakeHost, make_bundle, run_installer


class ListenerTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".oac/tests/listeners"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name)
        self.root = self.work / "installation"
        self.bundle, _ = make_bundle(self.work / "bundle", MANIFEST)
        self.host = FakeHost(self)
        self.host.native_root = self.root
        self.output = io.StringIO()

    def install(self, *flags):
        with contextlib.redirect_stdout(self.output):
            run_installer(install, self.bundle, ["--install-dir", self.root, "--sandbox", "none", *flags])

    def document(self, name):
        return json.loads((self.root / name).read_text())

    def test_custom_ipv4_listener_drives_services_and_operator_requests(self):
        self.install("--host", "127.0.0.2", "--port", "18080", "--core-port", "18091")
        config = self.document("config.json")
        self.assertEqual(config["host"], "127.0.0.2")
        self.assertEqual(config["ports"], {"core": 18091, "web": 18080})
        self.assertNotIn("host", self.document("state.json"))
        services = self.document("generated/compose.json")["services"]
        self.assertEqual(services["core"]["ports"], ["127.0.0.2:18091:8091"])
        self.assertEqual(services["web"]["ports"], ["127.0.0.2:18080:8080"])
        self.assertEqual(services["web"]["environment"]["OAC_WEB_ORIGIN"], "http://127.0.0.2:18080")
        self.assertNotIn("ports", services["database"])
        self.host.requests.clear()
        oac_cli.status(self.root, out=lambda _: None)
        self.assertTrue(self.host.requests)
        self.assertTrue(all(request.startswith("http://127.0.0.2:") for request in self.host.requests))
        self.assertIn("Console: http://127.0.0.2:18080", self.output.getvalue())

    def test_core_only_completion_reports_configured_address(self):
        self.install("--core-only", "--host", "127.0.0.2", "--core-port", "18091")
        self.assertIn("http://127.0.0.2:18091/core/v1 (local only)", self.output.getvalue())
        self.assertNotIn("http://127.0.0.1:", self.output.getvalue())

    def test_ipv6_native_and_container_listeners(self):
        for native in (False, True):
            config = config_model.initial("all", native, host="::1", **{"ports.database": 15432 if native else None})
            state = dict(MANIFEST, mode="all", native_core=native, uid=1000, gid=1000,
                         images={name: "sha256:" + "a" * 64 for name in ("core", "web", "database")},
                         project="oac-test", installation_id="fixture")
            services = configuration.compose_config(self.root, config, state)["services"]
            self.assertEqual(configuration.service_origin(config, "core"), "http://[::1]:8091")
            if native:
                self.assertEqual(configuration.core_environment(self.root, config, state)["OAC_ADDR"], "[::1]:8091")
                self.assertEqual(services["web"]["environment"]["OAC_WEB_ADDR"], "[::1]:8080")
                self.assertEqual(services["web"]["environment"]["OAC_WEB_UPSTREAM"], "http://[::1]:8091")
                self.assertEqual(services["database"]["ports"], ["127.0.0.1:15432:5432"])
            else:
                self.assertEqual(services["core"]["ports"], ["[::1]:8091:8091"])
                self.assertEqual(services["web"]["ports"], ["[::1]:8080:8080"])

    def test_wildcards_use_loopback_for_operator_connections(self):
        for host, address in (("0.0.0.0", "127.0.0.1"), ("::", "[::1]")):
            config = config_model.initial("all", host=host, public_url="https://core.example")
            self.assertEqual(configuration.service_origin(config, "core"), f"http://{address}:8091")
            self.assertEqual(configuration.web_origin(config), "https://core.example")

    def test_web_only_listener_does_not_change_upstream(self):
        config = config_model.initial("web-only", host="127.0.0.2", **{"ports.web": 18080, "web.core_url": "https://core.example"})
        services = configuration.compose_config(self.root, config, dict(mode="web-only", uid=1000, gid=1000,
            project="oac-test", images={"web": "sha256:" + "a" * 64}))["services"]
        self.assertEqual(set(services), {"web"})
        self.assertEqual(services["web"]["environment"]["OAC_WEB_ADDR"], "127.0.0.2:18080")
        self.assertEqual(services["web"]["environment"]["OAC_WEB_UPSTREAM"], "https://core.example")

    def test_apply_contacts_previous_host_before_switching_and_status_uses_applied_host(self):
        self.install("--host", "127.0.0.2", "--public-url", "https://core.example")
        config = self.document("config.json")
        config["host"] = "127.0.0.3"
        config["ports"]["web"] = 18080
        (self.root / "config.json").write_text(json.dumps(config))
        self.host.requests.clear()
        oac_cli.status(self.root, out=lambda _: None)
        self.assertTrue(all(request.startswith("http://127.0.0.2:") for request in self.host.requests))
        self.host.requests.clear()
        oac_cli.apply(self.root, interactive=False, out=lambda _: None)
        urls = self.host.requests
        old = "http://127.0.0.2:8091/core/v1/installation"
        new = "http://127.0.0.3:8091/healthz"
        self.assertIn(old, urls)
        self.assertIn(new, urls)
        self.assertLess(urls.index(old), urls.index(new))
        self.assertEqual(self.document("generated/compose.json")["services"]["web"]["ports"], ["127.0.0.3:18080:8080"])
        self.host.requests.clear()
        oac_cli.status(self.root, out=lambda _: None)
        self.assertTrue(all(request.startswith("http://127.0.0.3:") for request in self.host.requests))

    def test_failed_apply_restores_the_previous_listener(self):
        self.install("--host", "127.0.0.2", "--public-url", "https://core.example")
        config = self.document("config.json")
        config["host"] = "127.0.0.3"
        (self.root / "config.json").write_text(json.dumps(config))
        original = oac_cli.health
        def health(root, applied, expected):
            if applied["host"] == "127.0.0.3":
                raise oac_cli.OacError("Listener unavailable")
            return original(root, applied, expected)
        with mock.patch.object(oac_cli, "health", side_effect=health), self.assertRaisesRegex(oac_cli.OacError, "previous generated files were restored"):
            oac_cli.apply(self.root, interactive=False, out=lambda _: None)
        self.assertEqual(self.document("generated/compose.json")["services"]["core"]["ports"], ["127.0.0.2:8091:8091"])
        self.host.requests.clear()
        oac_cli.status(self.root, out=lambda _: None)
        self.assertTrue(all(url.startswith("http://127.0.0.2:") for url in self.host.requests))

    def test_invalid_listener_and_port_fail_before_creating_installation(self):
        for flags in (("--host", "localhost"), ("--host", "https://core.example"),
                      ("--host", "[::1]:8080"), ("--host", "fe80::1%eth0"),
                      ("--host", "0.0.0.0"), ("--port", "65536"), ("--port", "8091"),
                      ("--core-only", "--port", "8088")):
            with self.subTest(flags=flags), self.assertRaises(config_model.ConfigError):
                self.install(*flags)
            self.assertFalse((self.root / "config.json").exists())
            self.assertFalse(self.host.running())
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            install.arguments(["--web-port", "8088"])

    def test_config_seed_rejects_listener_overrides(self):
        path = self.work / "seed.json"
        path.write_text(json.dumps(config_model.initial("all")))
        for flags in (("--host", "127.0.0.2"), ("--port", "8088")):
            with self.assertRaisesRegex(install.InstallError, "--config replaces"):
                self.install("--config", path, *flags)

    def test_port_probe_uses_requested_address(self):
        with socket.socket() as listener:
            listener.bind(("127.0.0.2", 0))
            port = listener.getsockname()[1]
            with self.assertRaises(install.InstallError):
                install.free_port(port, "127.0.0.2")
            install.free_port(port, "127.0.0.1")


if __name__ == "__main__":
    unittest.main()
