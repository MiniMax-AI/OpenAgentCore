"""Managed domain setup preserves a usable installation across failures and retries."""
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import ingress
import ingress_config
import config_model
import install
import oac_cli
from installer_fakes import FakeHost, MANIFEST, make_bundle, run_installer


class DomainTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".oac/tests/ingress"
        base.mkdir(parents=True, exist_ok=True)
        work = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(work.cleanup)
        self.root = Path(work.name) / "core"
        self.bundle, _ = make_bundle(Path(work.name) / "bundle", MANIFEST)
        self.host = FakeHost(self)
        self.reload_gateway = ingress_config.reload
        for owner, name, replacement in (
            (ingress_config, "preflight", mock.Mock(return_value={"docker_socket": "/var/run/docker.sock", "docker_gid": 999})),
            (ingress_config, "reload", mock.Mock()),
            (ingress, "verify", mock.Mock()),
            (ingress, "resolves", mock.Mock(return_value=True)),
        ):
            patch = mock.patch.object(owner, name, replacement)
            patch.start()
            self.addCleanup(patch.stop)
        with contextlib.redirect_stdout(io.StringIO()):
            run_installer(install, self.bundle, ["--install-dir", self.root])
        self.host.recreated.clear()

    def gateway_ports(self):
        return json.loads((self.root / "generated/compose.json").read_text())["services"]["gateway"]["ports"]

    def test_default_bootstrap_then_https_reuses_installer_and_retains_data(self):
        config = oac_cli.load_config(self.root)
        self.assertEqual((config["host"], config["ingress"], config["public_url"]), ("0.0.0.0", "managed", None))
        services = json.loads((self.root / "generated/compose.json").read_text())["services"]
        self.assertEqual(services["core"]["ports"], ["127.0.0.1:8091:8091"])
        self.assertNotIn("ports", services["web"])
        self.assertNotIn("ports", services["database"])
        self.assertEqual(services["gateway"]["ports"], ["0.0.0.0:8080:8080"])
        for name in ("core", "web"):
            self.assertNotIn("/docker.sock", str(services[name]))
        preserved = {name: (self.root / "secrets" / name).read_bytes()
                     for name in ("core.key", "credential.key", "database.password")}
        ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "ready")
        self.assertEqual(oac_cli.load_config(self.root)["public_url"], "https://core.example.com")
        self.assertEqual(set(self.host.recreated), {"core", "migrate", "web", "gateway"})
        self.assertEqual(self.gateway_ports(), ["0.0.0.0:8080:8080", "0.0.0.0:80:80", "0.0.0.0:443:443"])
        self.assertIn("redir https://core.example.com{uri} 308", (self.root / "generated/Caddyfile").read_text())
        self.assertEqual(preserved, {name: (self.root / "secrets" / name).read_bytes() for name in preserved})
        self.host.recreated.clear()
        ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(self.host.recreated, [])
        oac_cli.stop(self.root, out=lambda _: None)
        oac_cli.start(self.root, out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "ready")

    def test_certificate_failure_keeps_http_and_retry_finishes(self):
        before = (self.root / "config.json").read_bytes()
        ingress.verify.side_effect = ingress.DomainError("https_not_ready", "DNS not ready")
        with self.assertRaisesRegex(ingress.DomainError, "DNS not ready"):
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual((self.root / "config.json").read_bytes(), before)
        self.assertNotIn("redir", (self.root / "generated/Caddyfile").read_text())
        self.assertEqual(ingress.status(self.root)["state"], "failed")
        # The gateway published 80 and 443 for the attempt and was restored without them.
        self.assertEqual(self.host.recreated, ["gateway", "gateway"])
        self.assertEqual(self.gateway_ports(), ["0.0.0.0:8080:8080"])
        ingress.verify.side_effect = None
        ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "ready")

    def test_busy_https_port_refuses_setup_before_reserving_a_job(self):
        self.host.busy.add(("0.0.0.0", 80))
        before = (self.root / "ingress/status.json").read_bytes()
        with self.assertRaises(ingress.DomainError) as error:
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual((error.exception.code, error.exception.status), ("https_ports_unavailable", 409))
        self.assertIn("Port 80 is already in use", str(error.exception))
        self.assertEqual((self.root / "ingress/status.json").read_bytes(), before)
        self.assertEqual((self.gateway_ports(), self.host.recreated), (["0.0.0.0:8080:8080"], []))

    def test_unresolved_hostname_refuses_setup_before_reserving_a_job(self):
        ingress.resolves.return_value = False
        before = (self.root / "ingress/status.json").read_bytes()
        with self.assertRaises(ingress.DomainError) as error:
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual((error.exception.code, error.exception.status), ("hostname_unresolved", 409))
        self.assertEqual((self.root / "ingress/status.json").read_bytes(), before)
        self.assertEqual(self.host.recreated, [])

    def test_domain_change_with_https_on_passes_its_own_port_check(self):
        ingress.configure(self.root, "core.example.com", out=lambda _: None)
        # The gateway itself now holds 80 and 443.
        self.assertTrue({("0.0.0.0", 80), ("0.0.0.0", 443)} <= self.host.listening())
        ingress.configure(self.root, "another.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["public_url"], "https://another.example.com")

    def test_apply_failure_restores_previous_inputs_and_interruption_can_retry(self):
        self.host.core["rejects"] = lambda env: 'OAC_PUBLIC_URL="https://core.example.com"' in env
        with self.assertRaises(ingress.DomainError):
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertIsNone(oac_cli.load_config(self.root)["public_url"])
        self.assertTrue(self.host.core_listening(8091))
        self.host.core["rejects"] = lambda _: False
        with mock.patch.object(oac_cli, "_apply", side_effect=KeyboardInterrupt), self.assertRaises(KeyboardInterrupt):
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "failed")
        ingress_config.reload.reset_mock()
        ingress.verify.side_effect = ingress.DomainError("https_not_ready", "DNS not ready")
        with self.assertRaisesRegex(ingress.DomainError, "DNS not ready"):
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        for call in ingress_config.reload.call_args_list:
            self.assertNotIn("redir", call.args[1])
        self.assertIsNone(ingress.status(self.root)["public_url"])
        ingress.verify.side_effect = None
        ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "ready")

    def test_bindings_pending_edits_and_concurrent_changes_require_operator_action(self):
        self.host.bindings["self_hosted_executors"] = 1
        with self.assertRaises(ingress.DomainError) as error:
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(error.exception.code, "public_url_confirmation_required")
        with oac_cli.locked(self.root), self.assertRaisesRegex(oac_cli.OacError, "Another oac"):
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        ingress.configure(self.root, "core.example.com", "https://core.example.com", out=lambda _: None)
        config = oac_cli.load_config(self.root)
        config["log"]["level"] = "debug"
        oac_cli.write_private(self.root / "config.json", json.dumps(config))
        with self.assertRaisesRegex(ingress.DomainError, "pending config.json"):
            ingress.configure(self.root, "another.example.com", out=lambda _: None)

    def test_apply_recovers_after_containers_converged_before_gateway_reload(self):
        ingress_config.reload.side_effect = [None, KeyboardInterrupt]
        with self.assertRaises(KeyboardInterrupt):
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "failed")
        self.assertIn("redir https://core.example.com", (self.root / "generated/Caddyfile").read_text())
        self.host.recreated.clear()
        ingress_config.reload.reset_mock(side_effect=True)
        ingress.verify.reset_mock()
        oac_cli.apply(self.root, interactive=False, out=lambda _: None)
        ingress_config.reload.assert_called_once()
        ingress.verify.assert_called_once_with("https://core.example.com", oac_cli.load_state(self.root)["installation_id"])
        self.assertEqual(self.host.recreated, [])
        self.assertEqual(ingress.status(self.root)["state"], "ready")
        self.assertEqual(ingress.status(self.root)["public_url"], "https://core.example.com")
        config = oac_cli.load_config(self.root)
        config["public_url"] = "https://another.example.com"
        oac_cli.write_private(self.root / "config.json", json.dumps(config))
        oac_cli.apply(self.root, interactive=False, out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["public_url"], config["public_url"])
        self.assertEqual(ingress.status(self.root)["target_url"], config["public_url"])

    def test_old_address_probe_keeps_managed_core_on_ipv4_loopback(self):
        config = oac_cli.load_config(self.root)
        for host in ("203.0.113.10", "::"):
            with self.subTest(host=host):
                config["host"] = host
                self.host.requests.clear()
                _, base, answered = oac_cli.old_public_url(
                    self.root, config, config_model.values(config), {}, {"core": {"running": True}})
                self.assertEqual(base, "http://127.0.0.1:8091")
                self.assertTrue(answered)
                self.assertEqual(self.host.requests, [base + "/core/v1/installation"])

    def test_gateway_disconnect_uses_the_existing_apply_rollback(self):
        before = (self.root / "generated/core.env").read_bytes()
        config = oac_cli.load_config(self.root)
        config["log"]["level"] = "debug"
        oac_cli.write_private(self.root / "config.json", json.dumps(config))
        ingress_config.reload.side_effect = self.reload_gateway
        with mock.patch.object(ingress_config, "UnixHTTP") as connection:
            connection.return_value.request.side_effect = [ConnectionRefusedError(), None]
            connection.return_value.getresponse.return_value.status = 200
            with self.assertRaisesRegex(oac_cli.OacError, "previous generated files were restored"):
                oac_cli.apply(self.root, interactive=False, out=lambda _: None)
        self.assertEqual((self.root / "generated/core.env").read_bytes(), before)
        self.assertTrue(self.host.core_listening(8091))

    def test_status_requires_both_managed_ingress_services(self):
        for name in ("gateway", "installation"):
            with self.subTest(service=name):
                self.host.containers[name]["running"] = False
                with self.assertRaisesRegex(oac_cli.OacError, "services are unavailable"):
                    oac_cli.status(self.root, out=lambda _: None)
                self.host.containers[name]["running"] = True

    def test_failed_retry_restores_the_receipt_before_and_after_service_convergence(self):
        before = (self.root / "generated/core.env").read_bytes()
        self.host.bindings["self_hosted_executors"] = 1
        target = "https://core.example.com"
        converge = oac_cli.converge
        for restarted in (False, True):
            with self.subTest(services_restarted=restarted):
                calls = []
                def interrupt(*args, **kwargs):
                    # The gateway converges for the candidate; the switch stops before or after converging.
                    calls.append(args)
                    if len(calls) == 1 or restarted:
                        converge(*args, **kwargs)
                    if len(calls) == 2:
                        raise KeyboardInterrupt()
                with mock.patch.object(oac_cli, "converge", side_effect=interrupt), self.assertRaises(KeyboardInterrupt):
                    ingress.configure(self.root, "core.example.com", target, out=lambda _: None)
                self.assertEqual(len(calls), 2)
                # The retry returns Core to the applied address first, so it confirms the change from there.
                with self.assertRaises(ingress.DomainError) as error:
                    ingress.configure(self.root, "core.example.com", out=lambda _: None)
                self.assertEqual(error.exception.code, "public_url_confirmation_required")
                ingress_config.reload.reset_mock()
                ingress.verify.side_effect = ingress.DomainError("https_not_ready", "DNS not ready")
                with self.assertRaisesRegex(ingress.DomainError, "DNS not ready"):
                    ingress.configure(self.root, "core.example.com", target, out=lambda _: None)
                for call in ingress_config.reload.call_args_list:
                    self.assertNotIn("redir", call.args[1])
                self.assertIsNone(oac_cli.load_config(self.root)["public_url"])
                self.assertIsNone(ingress.status(self.root)["public_url"])
                self.assertEqual((self.root / "generated/core.env").read_bytes(), before)
                self.assertTrue(self.host.core_listening(8091))
                ingress.verify.side_effect = None

    def test_start_records_the_applied_address_after_an_offline_edit(self):
        oac_cli.stop(self.root, out=lambda _: None)
        config = oac_cli.load_config(self.root)
        config["public_url"] = "https://core.example.com"
        oac_cli.write_private(self.root / "config.json", json.dumps(config))
        oac_cli.apply(self.root, interactive=False, confirm_public_url_change=config["public_url"], out=lambda _: None)
        self.assertIsNone(ingress.status(self.root)["public_url"])
        oac_cli.start(self.root, out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["public_url"], config["public_url"])
        self.assertEqual(ingress.status(self.root)["state"], "ready")

    def test_untrusted_hostnames_never_reach_gateway(self):
        for value in ("https://example.com", "example.com:8443", "127.0.0.1", "localhost", "a.local", "a.com\n}", None):
            with self.subTest(value=value), self.assertRaises(ingress.DomainError):
                ingress.configure(self.root, value, out=lambda _: None)
        self.assertIsNone(oac_cli.load_config(self.root)["public_url"])
