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
        for owner, name, replacement in (
            (ingress_config, "preflight", mock.Mock(return_value={"docker_socket": "/var/run/docker.sock", "docker_gid": 999})),
            (ingress_config, "reload", mock.Mock()),
            (ingress, "verify", mock.Mock()),
        ):
            patch = mock.patch.object(owner, name, replacement)
            patch.start()
            self.addCleanup(patch.stop)
        with contextlib.redirect_stdout(io.StringIO()):
            run_installer(install, self.bundle, ["--install-dir", self.root, "--sandbox", "none"])
        self.host.recreated.clear()

    def test_default_bootstrap_then_https_reuses_installer_and_retains_data(self):
        config = oac_cli.load_config(self.root)
        self.assertEqual((config["host"], config["ingress"], config["public_url"]), ("0.0.0.0", "managed", None))
        services = json.loads((self.root / "generated/compose.json").read_text())["services"]
        self.assertEqual(services["core"]["ports"], ["127.0.0.1:8091:8091"])
        self.assertNotIn("ports", services["web"])
        self.assertNotIn("ports", services["database"])
        self.assertIn("0.0.0.0:8080:8080", services["gateway"]["ports"])
        for name in ("core", "web"):
            self.assertNotIn("/docker.sock", str(services[name]))
        preserved = {name: (self.root / "secrets" / name).read_bytes()
                     for name in ("core.key", "credential.key", "database.password")}
        ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "ready")
        self.assertEqual(oac_cli.load_config(self.root)["public_url"], "https://core.example.com")
        self.assertEqual(set(self.host.recreated), {"core", "migrate", "web"})
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
        self.assertEqual(self.host.recreated, [])
        ingress.verify.side_effect = None
        ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "ready")

    def test_apply_failure_restores_previous_inputs_and_interruption_can_retry(self):
        self.host.core["rejects"] = lambda env: 'OAC_PUBLIC_URL="https://core.example.com"' in env
        with self.assertRaises(ingress.DomainError):
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertIsNone(oac_cli.load_config(self.root)["public_url"])
        self.assertTrue(self.host.core_listening(8091))
        self.host.core["rejects"] = lambda _: False
        with mock.patch.object(oac_cli, "_apply", side_effect=KeyboardInterrupt), self.assertRaises(KeyboardInterrupt):
            ingress.configure(self.root, "core.example.com", out=lambda _: None)
        self.assertEqual(ingress.status(self.root)["state"], "applying")
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

    def test_untrusted_hostnames_never_reach_gateway(self):
        for value in ("https://example.com", "example.com:8443", "127.0.0.1", "localhost", "a.local", "a.com\n}", None):
            with self.subTest(value=value), self.assertRaises(ingress.DomainError):
                ingress.configure(self.root, value, out=lambda _: None)
        self.assertIsNone(oac_cli.load_config(self.root)["public_url"])
