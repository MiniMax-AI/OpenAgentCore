"""Generation preparation copies the installation's Runtime; collection retains identity until local helper ownership settles."""
import fcntl
import os
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import node_generations
import node_install as installer
import node_spec


class GenerationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.directory = self.root / "state/node/generations"
        self.directory.mkdir(parents=True, mode=0o700)
        self.value = {"installation_id": "test-installation", "generation": 1, "provider": "docker", "core_url": "https://core.example/api/v1",
                      "native": {"image": "sha256:" + "a" * 64, "seccomp_file": str(self.root / "runtime/seccomp.json")},
                      "specification": {"resources": {"cpus": 1, "memory_mib": 1024}}}
        self.args = SimpleNamespace(installation_id="test-installation", generation=1, specification_digest=node_spec.digest("docker", self.value["specification"]))
        node_generations.atomic_json(self.root / "provider.json", self.value)
        self.initialize_lease()
        for patch in (mock.patch.object(node_generations, "owned_root", return_value=(self.root, {"core_url": "https://core.example"})),
                      mock.patch.object(installer, "checked", return_value="")):
            patch.start()
            self.addCleanup(patch.stop)

    def initialize_lease(self):
        with node_generations.collection_lease(self.root, self.args.generation, installer,
                                               node_generations.marker_identity(self.args), initialize=True):
            pass

    def next_generation(self, resources):
        specification = {"resources": resources}
        configuration = {"installation_id": "test-installation", "provider": "docker", "core_url": "https://core.example", "generation": 2, "specification": specification, "specification_digest": node_spec.digest("docker", specification)}
        self.args.generation, self.args.specification_digest = 2, configuration["specification_digest"]
        return configuration

    def test_prepare_publishes_the_installation_runtime_under_the_authorized_specification(self):
        configuration = self.next_generation({"cpus": 2, "memory_mib": 2048})
        with mock.patch.object(node_spec, "fetch", return_value=configuration), mock.patch.object(node_generations, "image_available", return_value=True):
            node_generations.prepare(self.args, installer)
            published = json.loads((self.directory / "2.json").read_text())
            self.assertEqual(published, dict(self.value, generation=2, specification=configuration["specification"]))
            node_generations.prepare(self.args, installer)
        self.assertEqual(json.loads((self.directory / "2.json").read_text()), published)
        self.assertEqual(json.loads((self.root / "provider.json").read_text()), self.value)

    def test_prepare_refuses_an_unauthorized_dropped_or_unavailable_generation(self):
        configuration = self.next_generation({"cpus": 2, "memory_mib": 2048})
        with mock.patch.object(node_spec, "fetch", return_value=dict(configuration, specification_digest="0" * 64)):
            with self.assertRaisesRegex(installer.InstallError, "differs from its authorization"):
                node_generations.prepare(self.args, installer)
        with mock.patch.object(node_spec, "fetch", return_value=configuration), mock.patch.object(node_generations, "image_available", return_value=False):
            with self.assertRaisesRegex(installer.InstallError, "Runtime is missing"):
                node_generations.prepare(self.args, installer)
        self.assertFalse((self.directory / "2.json").exists())
        node_generations.atomic_json(self.directory / "2.dropped", node_generations.marker_identity(self.args))
        with mock.patch.object(node_spec, "fetch", return_value=configuration):
            with self.assertRaisesRegex(installer.InstallError, "dropped generation"):
                node_generations.prepare(self.args, installer)

    def test_busy_helper_refuses_collection_then_same_inode_collects(self):
        lease = self.directory / "1.lease"
        descriptor = os.open(lease, os.O_CREAT | os.O_RDWR, 0o600)
        self.addCleanup(os.close, descriptor)
        original = os.fstat(descriptor)
        fcntl.flock(descriptor, fcntl.LOCK_SH)
        with self.assertRaisesRegex(installer.InstallError, "helper is still active"):
            node_generations.collect(self.args, installer)
        self.assertFalse((self.directory / "1.dropped").exists())
        fcntl.flock(descriptor, fcntl.LOCK_UN)
        node_generations.collect(self.args, installer)
        self.assertTrue((self.directory / "1.dropped").exists())
        self.assertEqual(node_generations.retained_configs(self.root, installer), {})
        node_generations.collect(self.args, installer)
        final = lease.stat()
        self.assertEqual((original.st_dev, original.st_ino), (final.st_dev, final.st_ino))

    def test_collection_leaves_the_installations_runtime_and_other_generations(self):
        other = dict(self.value, generation=2)
        node_generations.atomic_json(self.directory / "2.json", other)
        seccomp = self.root / "runtime/seccomp.json"
        seccomp.parent.mkdir()
        seccomp.write_text("{}")
        node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertTrue(seccomp.exists())
        self.assertEqual(node_generations.retained_configs(self.root, installer), {2: other})

    def test_symlink_and_shared_inode_are_not_collection_authority(self):
        lease = self.directory / "1.lease"
        foreign = self.root / "foreign"
        foreign.write_bytes(b"")
        foreign.chmod(0o600)
        lease.unlink()
        for link in (os.symlink, os.link):
            link(foreign, lease)
            try:
                with self.assertRaises((OSError, installer.InstallError)):
                    node_generations.collect(self.args, installer)
                self.assertFalse((self.directory / "1.dropped").exists())
            finally:
                lease.unlink()

    def test_restart_after_an_interrupted_final_marker_finishes(self):
        original = node_generations.atomic_json
        def interrupt(path, value):
            if path.suffix == ".dropped":
                raise OSError("interrupted final marker")
            original(path, value)
        with mock.patch.object(node_generations, "atomic_json", side_effect=interrupt):
            with self.assertRaises(OSError):
                node_generations.collect(self.args, installer)
        self.assertFalse((self.directory / "1.dropped").exists())
        node_generations.collect(self.args, installer)
        self.assertTrue((self.directory / "1.dropped").exists())

    def test_invalid_journal_never_authorizes_collection(self):
        path = self.directory / "1.dropped"
        for field, invalid in (("installation_id", "foreign"), ("generation", 2), ("specification_digest", "a" * 64)):
            node_generations.atomic_json(path, dict(node_generations.marker_identity(self.args), **{field: invalid}))
            with self.assertRaises(installer.InstallError):
                node_generations.collect(self.args, installer)
            path.unlink()

    def test_fifo_journal_is_refused_without_blocking(self):
        os.mkfifo(self.directory / "1.dropped", 0o600)
        with self.assertRaises(installer.InstallError):
            node_generations.collect(self.args, installer)


if __name__ == "__main__":
    unittest.main()
