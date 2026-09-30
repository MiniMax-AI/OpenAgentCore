"""sandbox_setup: Core's existing selection wins, and E2B template builds follow Core's rule."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import sandbox_setup
import config_model

INSTALLATION = "94be54a1-138c-4f30-bc87-b13686272dbe"


class SandboxSetupTests(unittest.TestCase):
    def test_shared_e2b_selectors(self):
        fixture = Path(__file__).resolve().parents[2] / "services/core/internal/sandbox/e2b/testdata/configuration-selectors.json"
        cases = json.loads(fixture.read_text())
        for case in cases["endpoints"]:
            with self.subTest(endpoint=case["name"]):
                self.assertEqual(sandbox_setup.e2b_endpoint(case["api_url"], case["domain"]), case["valid"])
        for case in cases["templates"]:
            with self.subTest(template=case["name"]):
                self.assertEqual(sandbox_setup.e2b_template(case["value"]), case["valid"])

    def setUp(self):
        base = Path.home() / ".oac/tests/sandbox-setup"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "secrets").mkdir()
        (self.root / "secrets/core.key").write_text("fixture-core-key\n")
        self.config, self.state = config_model.initial("all"), {"installation_id": INSTALLATION}

    def initialize(self, current, selection):
        requests = []

        def send(req):
            requests.append((req.get_method(), req.full_url, req.get_header("Authorization")))
            return 200, json.dumps(current).encode()
        with mock.patch.object(sandbox_setup, "send", side_effect=send):
            result = sandbox_setup.initialize(self.root, self.config, self.state, selection)
        return result, requests

    def test_an_existing_selection_is_kept_or_reported(self):
        docker = {"provider": "docker", "resources": {"cpus": 2, "memory_mib": 2048}, "runtime": {}}
        current = {"installation_id": INSTALLATION, "provider": "docker"}
        self.assertEqual(self.initialize(current, docker), (current, [
            ("GET", "http://127.0.0.1:8091/core/v1/sandbox/deployment", "Bearer fixture-core-key")]))
        with self.assertRaisesRegex(sandbox_setup.SandboxSetupError, "already uses microsandbox"):
            self.initialize(dict(current, provider="microsandbox"), docker)
        with self.assertRaisesRegex(sandbox_setup.SandboxSetupError, "different installation"):
            self.initialize(dict(current, installation_id="other", provider=""), docker)

    def test_initialization_uses_observed_generation_once(self):
        for generation in (0, 8):
            with self.subTest(generation=generation):
                current = {"installation_id": INSTALLATION, "provider": "", "generation": generation, "reset": None}
                selection = {"provider": "docker", "resources": {"cpus": 2, "memory_mib": 2048}, "runtime": {}}
                writes = []
                def send(req):
                    if req.get_method() == "POST":
                        writes.append(json.loads(req.data))
                        return 409, b'{"error":{"code":"generation_stale","message":"Deployment generation changed"}}'
                    return 200, json.dumps(current).encode()
                with mock.patch.object(sandbox_setup, "send", side_effect=send), self.assertRaises(sandbox_setup.SandboxSetupError):
                    sandbox_setup.initialize(self.root, self.config, self.state, selection)
                self.assertEqual(writes, [dict(selection, expected_generation=generation)])
                self.assertNotIn("expected_generation", selection)



if __name__ == "__main__":
    unittest.main()
