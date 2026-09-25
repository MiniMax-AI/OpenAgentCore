"""Verify opt-in installation uses one admin deployment and ordinary enrollment."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import local_node


class LocalNodeTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".parsar/tests/local-node"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "admin").mkdir()
        (self.root / "admin/core.key").write_text("fixture-admin")
        self.state = {"core_port": 8091, "installation_id": "94be54a1-138c-4f30-bc87-b13686272dbe",
                      "provider": "microsandbox", "public_url": "https://core.example"}
        self.manifest = {"source_commit": "a" * 40, "images": {"runtime": "sha256:" + "b" * 64},
                         "image_manifest_digests": {"runtime": "sha256:" + "c" * 64},
                         "runtime_ref": "parsar-core-runtime@sha256:" + "d" * 64,
                         "microsandbox": {"runtime_sha256": "e" * 64, "firmware_sha256": "f" * 64}}
        self.current = {"installation_id": self.state["installation_id"], "provider": self.state["provider"],
                        "core_url": self.state["public_url"]}
        self.run = mock.Mock()

    def test_first_install_saves_spec_and_launches_ordinary_node_without_secret_arguments(self):
        # Web's sandbox setup proposes the same initial sizes.
        for provider, resources in (("docker", {"cpus": 2, "memory_mib": 2048}),
                                    ("microsandbox", {"cpus": 2, "memory_mib": 4096, "root_disk_mib": 8192,
                                                      "environment_disk_mib": 8192})):
            with self.subTest(provider=provider):
                self.run.reset_mock()
                state, current = dict(self.state, provider=provider), dict(self.current, provider=provider)
                empty = dict(current, provider="")
                with mock.patch.object(local_node, "request", side_effect=[empty, current, {"token": "one-time"}]) as request, \
                        mock.patch.object(local_node.Path, "home", return_value=self.root):
                    local_node.install(self.root, state, self.manifest, self.root / "bundle", self.run)
                self.assertEqual([(call.args[2], call.args[3]) for call in request.call_args_list],
                                 [("GET", "deployment"), ("POST", "deployment"), ("POST", "enrollment-tokens")])
                setup = request.call_args_list[1].args[4]
                self.assertEqual(setup["resources"], resources)
        self.assertEqual(setup["runtime"]["image_id"], self.manifest["images"]["runtime"])
        self.assertNotIn("core_url", setup)  # Core derives it from its public URL.
        self.assertNotIn("one-time", str(self.run.call_args.args))
        self.assertEqual(self.run.call_args.kwargs["env"]["PARSAR_NODE_ENROLLMENT_TOKEN"], "one-time")
        self.assertIn("--bundle", self.run.call_args.args[0])

    def test_repeat_never_overwrites_admin_configuration_or_reenrolls(self):
        marker = self.root / ".parsar/nodes" / self.state["installation_id"] / "registered.json"
        marker.parent.mkdir(parents=True)
        marker.write_text("{}")
        current = dict(self.current, specification={"resources": {"cpus": 8, "memory_mib": 16384}})
        with mock.patch.object(local_node, "request", return_value=current) as request, \
                mock.patch.object(local_node.Path, "home", return_value=self.root):
            local_node.install(self.root, self.state, self.manifest, self.root / "bundle", self.run)
        self.assertEqual(request.call_count, 1)
        self.assertEqual(request.call_args.args[2], "GET")
        self.assertNotIn("PARSAR_NODE_ENROLLMENT_TOKEN", self.run.call_args.kwargs["env"])

    def test_changed_selection_does_not_initialize_enroll_or_start_service(self):
        with mock.patch.object(local_node, "request", return_value=dict(self.current, provider="docker")) as request:
            with self.assertRaisesRegex(local_node.LocalNodeError, "selection differs"):
                local_node.install(self.root, self.state, self.manifest, self.root / "bundle", self.run)
        self.assertEqual(request.call_count, 1)
        self.run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
