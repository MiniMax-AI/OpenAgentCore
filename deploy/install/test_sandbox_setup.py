"""sandbox_setup: Core's existing selection wins, and E2B template builds follow Core's rule."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import sandbox_setup

INSTALLATION = "94be54a1-138c-4f30-bc87-b13686272dbe"


class SandboxSetupTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".parsar/tests/sandbox-setup"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "secrets").mkdir()
        (self.root / "secrets/core.key").write_text("fixture-core-key\n")
        self.config, self.state = {"ports": {"core": 8091}}, {"installation_id": INSTALLATION}

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

    def test_e2b_template_is_an_exact_build(self):
        build = "0f6c1e8e-7d3a-4b8e-9a51-2b7f7f0c9d11"
        for value in ("base:" + build, "my_template-2:" + build):
            self.assertTrue(sandbox_setup.e2b_template(value), value)
        for value in ("base", "base:", ":" + build, "base:" + build.upper(), "base:" + build.replace("-", ""),
                      "base:00000000-0000-0000-0000-000000000000", "ba.se:" + build, "x" * 129 + ":" + build):
            self.assertFalse(sandbox_setup.e2b_template(value), value)


if __name__ == "__main__":
    unittest.main()
