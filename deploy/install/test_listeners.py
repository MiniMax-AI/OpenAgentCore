"""Installer listener selection and subsequent operator lifecycle behavior."""
import contextlib
import errno
import io
import ipaddress
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
            run_installer(install, self.bundle, ["--install-dir", self.root, "--sandbox", "none", "--ingress", "external", *flags])

    def document(self, name):
        return json.loads((self.root / name).read_text())

    def test_custom_ipv4_listener_drives_services_and_operator_requests(self):
        self.install("--host", "127.0.0.2", "--web-port", "18080", "--core-port", "18091")
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
                      ("--host", "0.0.0.0"), ("--web-port", "65536"), ("--web-port", "8091"),
                      ("--core-only", "--web-port", "8088")):
            with self.subTest(flags=flags), self.assertRaises(config_model.ConfigError):
                self.install(*flags)
            self.assertFalse((self.root / "config.json").exists())
            self.assertFalse(self.host.running())
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            install.arguments(["--port", "8088"])

    def test_config_seed_rejects_listener_overrides(self):
        path = self.work / "seed.json"
        path.write_text(json.dumps(config_model.initial("all")))
        for flags in (("--host", "127.0.0.2"), ("--web-port", "8088")):
            with self.assertRaisesRegex(install.InstallError, "--config replaces"):
                self.install("--config", path, *flags)

    def test_apply_checks_a_new_listener_before_changing_anything(self):
        self.install("--host", "127.0.0.2", "--public-url", "https://core.example")
        self.edit(lambda config: config["ports"].update(web=18080))
        # The installation's own services listen on 8080 and 8091; another program holds 18080.
        self.host.busy.update({("127.0.0.2", 8080), ("127.0.0.2", 8091), ("127.0.0.2", 18080)})
        self.assert_refused(r"^Port 18080 \(ports.web\) is already in use on 127.0.0.2. Nothing was applied. Free it or "
                            r"choose another port; find the process with: sudo ss -ltnp 'sport = :18080'$")

    def test_apply_checks_a_changed_host(self):
        self.install("--host", "127.0.0.2", "--public-url", "https://core.example")
        self.host.busy.update({("127.0.0.2", 8080), ("127.0.0.2", 8091), ("127.0.0.3", 8080)})
        self.host.unassigned.add("192.0.2.10")
        for host, message in (("192.0.2.10", r"^192.0.2.10 \(host\) is not an address of this machine; use one of its "
                                             r"addresses. Nothing was applied.$"),
                              # Another program holds the port on the new address, or on part of the wildcard.
                              ("127.0.0.3", r"^Port 8080 \(ports.web\) is already in use on 127.0.0.3. Nothing was applied."),
                              ("0.0.0.0", r"^Port 8080 \(ports.web\) is already in use on 0.0.0.0. Nothing was applied.")):
            with self.subTest(host=host):
                self.edit(lambda config: config.update(host=host))
                self.assert_refused(message)
        # The installation's own listeners on the old address do not count.
        self.host.busy.discard(("127.0.0.3", 8080))
        oac_cli.apply(self.root, interactive=False, out=lambda _: None)
        self.assertEqual(self.document("generated/compose.json")["services"]["web"]["ports"], ["0.0.0.0:8080:8080"])

    def edit(self, change):
        config = self.document("config.json")
        change(config)
        (self.root / "config.json").write_text(json.dumps(config))

    def assert_refused(self, message):
        generated = {path.name: path.read_bytes() for path in (self.root / "generated").iterdir()}
        containers = json.dumps(self.host.containers, sort_keys=True)
        self.host.recreated.clear()
        with self.assertRaisesRegex(oac_cli.OacError, message):
            oac_cli.apply(self.root, interactive=False, out=lambda _: None)
        self.assertEqual(generated, {path.name: path.read_bytes() for path in (self.root / "generated").iterdir()})
        self.assertEqual((containers, self.host.recreated), (json.dumps(self.host.containers, sort_keys=True), []))


class PortProbeTests(unittest.TestCase):
    def test_probe_binds_like_the_services_and_reads_privileged_ports_from_the_kernel(self):
        with socket.socket() as listener:
            listener.bind(("127.0.0.2", 0))
            listener.listen()
            port = listener.getsockname()[1]
            self.assertIn((ipaddress.ip_address("127.0.0.2"), port), set(oac_cli.tcp_listeners()))
            self.assertFalse(oac_cli.port_free("127.0.0.2", port))
            self.assertFalse(oac_cli.port_free("0.0.0.0", port))
            self.assertTrue(oac_cli.port_free("127.0.0.1", port))
            # The installation's own listener does not count.
            self.assertTrue(oac_cli.port_free("0.0.0.0", port, {(ipaddress.ip_address("127.0.0.2"), port)}))
            # An account that may not bind the port reads the listening sockets instead.
            with mock.patch.object(socket.socket, "bind", side_effect=OSError(errno.EACCES, "Permission denied")):
                self.assertFalse(oac_cli.port_free("0.0.0.0", port))
                self.assertTrue(oac_cli.port_free("127.0.0.1", port))
        # A connection in TIME_WAIT on a Go or Docker listener, which set SO_REUSEADDR, does not hold the port.
        with socket.socket() as listener:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            listener.bind(("127.0.0.1", 0))
            listener.listen()
            port = listener.getsockname()[1]
            with socket.create_connection(("127.0.0.1", port)):
                accepted, _ = listener.accept()
                accepted.close()
        self.assertTrue(oac_cli.port_free("127.0.0.1", port))


if __name__ == "__main__":
    unittest.main()
