"""Collection retains bytes until local helper ownership settles."""
import fcntl
import os
import json
import hashlib
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import node_generations
import node_install as installer
import node_spec


class CollectionTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.directory = self.root / "state/node/generations"
        self.directory.mkdir(parents=True, mode=0o700)
        self.value = {"installation_id": "test-installation", "generation": 1, "provider": "docker", "docker": {"image": "sha256:" + "a" * 64},
                      "specification": {"resources": {"cpus": 1, "memory_mib": 1024}, "runtime": {"source_commit": "b" * 40, "image_id": "sha256:" + "a" * 64, "image_manifest_digest": "sha256:" + "c" * 64, "microsandbox_ref": "oac-runtime@sha256:" + "d" * 64, "runtime_sha256": "e" * 64, "firmware_sha256": "f" * 64}}}
        self.args = SimpleNamespace(installation_id="test-installation", generation=1, specification_digest=node_spec.digest("docker", self.value["specification"]))
        self.release = self.root / "releases" / ("b" * 40)
        self.value["docker"]["seccomp_file"] = str(self.release / "runtime/seccomp.json")
        self.release.mkdir(parents=True)
        (self.release / "artifact").write_bytes(b"immutable bytes")
        node_generations.atomic_json(self.root / "provider.json", self.value)
        self.initialize_lease()
        for patch in (mock.patch.object(node_generations, "owned_root", return_value=(self.root, {})),
                      mock.patch.object(installer, "checked", return_value="")):
            patch.start()
            self.addCleanup(patch.stop)

    def initialize_lease(self):
        with node_generations.collection_lease(self.root, self.args.generation, installer,
                                               node_generations.marker_identity(self.args), initialize=True):
            pass

    def test_busy_helper_refuses_all_mutations_then_same_inode_collects(self):
        lease = self.directory / "1.lease"
        descriptor = os.open(lease, os.O_CREAT | os.O_RDWR, 0o600)
        self.addCleanup(os.close, descriptor)
        original = os.fstat(descriptor)
        fcntl.flock(descriptor, fcntl.LOCK_SH)
        with self.assertRaisesRegex(installer.InstallError, "helper is still active"):
            node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertEqual((self.release / "artifact").read_bytes(), b"immutable bytes")
        self.assertFalse((self.directory / "1.dropped").exists())
        fcntl.flock(descriptor, fcntl.LOCK_UN)
        node_generations.collect(self.args, installer)
        self.assertFalse(self.release.exists())
        self.assertTrue((self.directory / "1.dropped").exists())
        node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        final = lease.stat()
        self.assertEqual((original.st_dev, original.st_ino), (final.st_dev, final.st_ino))

    def test_collection_holds_exclusive_lease_during_deletion(self):
        lease = self.directory / "1.lease"
        original = node_generations.shutil.rmtree
        def remove(path):
            descriptor = os.open(lease, os.O_RDWR)
            try:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(descriptor, fcntl.LOCK_SH | fcntl.LOCK_NB)
            finally:
                os.close(descriptor)
            return original(path)
        with mock.patch.object(node_generations.shutil, "rmtree", side_effect=remove):
            node_generations.collect(self.args, installer)

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
                installer.checked.assert_not_called()
                self.assertTrue((self.release / "artifact").exists())
            finally:
                lease.unlink()

    def test_native_inventory_failure_and_unknown_output_retain_every_byte(self):
        self.micro_fixture()
        for failure in (installer.InstallError("daemon unavailable"), "not a verified image ID"):
            installer.checked.side_effect = failure if isinstance(failure, Exception) else None
            installer.checked.return_value = failure
            with self.assertRaises(installer.InstallError):
                node_generations.collect(self.args, installer)
            self.assertTrue((self.release / "artifact").exists())
            self.assertFalse((self.directory / "1.dropped").exists())
            self.assertFalse(json.loads((self.directory / "1.collecting").read_text())["native_complete"])

    def test_restart_after_partial_file_cleanup_skips_native_and_finishes(self):
        def interrupted(path):
            (path / "artifact").unlink()
            raise OSError("interrupted file cleanup")
        with mock.patch.object(node_generations.shutil, "rmtree", side_effect=interrupted):
            with self.assertRaises(OSError):
                node_generations.collect(self.args, installer)
        self.assertTrue(json.loads((self.directory / "1.collecting").read_text())["native_complete"])
        self.assertFalse((self.directory / "1.dropped").exists())
        installer.checked.reset_mock()
        node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertFalse(self.release.exists())
        self.assertEqual(node_generations.retained_configs(self.root, installer), {})

    def test_restart_after_files_removed_before_final_marker(self):
        original = node_generations.atomic_json
        def interrupt(path, value):
            if path.suffix == ".dropped":
                raise OSError("interrupted final marker")
            original(path, value)
        with mock.patch.object(node_generations, "atomic_json", side_effect=interrupt):
            with self.assertRaises(OSError):
                node_generations.collect(self.args, installer)
        self.assertFalse(self.release.exists())
        installer.checked.reset_mock()
        node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertTrue((self.directory / "1.dropped").exists())

    def test_invalid_journal_never_authorizes_deletion_or_hides_configuration(self):
        for suffix in (".collecting", ".dropped", ".preparing"):
            path = self.directory / ("1" + suffix)
            value = node_generations.marker_identity(self.args)
            if suffix == ".collecting": value["native_complete"] = True
            if suffix == ".preparing": value["import_started"] = False
            for field, invalid in (("installation_id", "foreign"), ("generation", 2), ("specification_digest", "a" * 64)):
                node_generations.atomic_json(path, dict(value, **{field: invalid}))
                with self.assertRaises(installer.InstallError):
                    node_generations.collect(self.args, installer)
                self.assertTrue(self.release.exists())
                path.unlink()
        installer.checked.assert_not_called()

    def test_shared_image_and_release_survive_until_last_generation(self):
        other = dict(self.value, generation=2)
        node_generations.atomic_json(self.directory / "2.json", other)
        node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertTrue((self.release / "artifact").exists())
        self.args.generation = 2
        self.initialize_lease()
        node_generations.collect(self.args, installer)
        self.assertFalse(self.release.exists())

    def test_never_imported_generation_can_collect_missing_executable(self):
        runtime, _, _ = self.micro_fixture()
        runtime.unlink()
        node_generations.atomic_json(self.directory / "1.preparing", dict(node_generations.marker_identity(self.args), import_started=False))
        node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertFalse(self.release.exists())

    def micro_fixture(self):
        self.value["provider"] = "microsandbox"
        del self.value["docker"]
        home = self.root / "micro-store"
        home.mkdir(mode=0o700)
        node_generations.atomic_json(home / "oac-installation.json", {"installation_id": self.args.installation_id})
        runtime = self.release / "msb"
        runtime.write_bytes(b"verified native executable")
        runtime.chmod(0o700)
        image = self.value["specification"]["runtime"]["microsandbox_ref"]
        self.value["microsandbox"] = {"helper_path": str(self.release / "helper"), "runtime_home": str(home), "runtime_path": str(runtime), "firmware_path": str(self.release / "firmware"), "runtime_sha256": hashlib.sha256(runtime.read_bytes()).hexdigest(), "image": image}
        self.args.specification_digest = node_spec.digest("microsandbox", self.value["specification"])
        node_generations.atomic_json(self.root / "provider.json", self.value)
        # This fixture changes provider before any helper exists.
        for suffix in (".lease", ".lease-identity"):
            (self.directory / ("1" + suffix)).unlink()
        self.initialize_lease()
        return runtime, image, home

    def test_micro_interrupted_native_removal_and_missing_runtime_fail_closed(self):
        runtime, image, home = self.micro_fixture()
        present = [True]
        def native(command, *_args, **_kwargs):
            if command[1:3] == ["image", "list"]: return image + "\n" if present[0] else ""
            if command[1:3] == ["image", "remove"]:
                present[0] = False
                raise installer.InstallError("interrupted native removal")
            if command[1:3] == ["sandbox", "list"]: return "[]"
            raise AssertionError(command)
        installer.checked.side_effect = native
        with self.assertRaises(installer.InstallError): node_generations.collect(self.args, installer)
        raw = runtime.read_bytes()
        runtime.unlink()
        installer.checked.reset_mock()
        with self.assertRaisesRegex(installer.InstallError, "executable"):
            node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertTrue(self.release.exists())
        runtime.write_bytes(raw)
        runtime.chmod(0o700)
        node_generations.collect(self.args, installer)
        self.assertFalse(self.release.exists())
        self.assertTrue((home / "oac-installation.json").exists())

    def test_docker_collection_preserves_another_installations_idle_serving_image(self):
        second = self.root.parent / (self.root.name + "-second")
        second.mkdir(mode=0o700)
        self.addCleanup(lambda: node_generations.shutil.rmtree(second))
        # The second installation's enrolled generation still backs an idle
        # serving pin. No container is needed to protect its daemon-level image.
        other = dict(self.value, installation_id="second-installation")
        node_generations.atomic_json(second / "provider.json", other)
        before = (second / "provider.json").read_bytes()
        host_images = {self.value["docker"]["image"]}
        def docker(command, *_args, **_kwargs):
            if "rm" in command or "prune" in command: host_images.clear()
            raise AssertionError("generation GC must not manage shared Docker images")
        installer.checked.side_effect = docker
        node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertEqual(host_images, {other["docker"]["image"]})
        self.assertEqual((second / "provider.json").read_bytes(), before)
        self.assertFalse(self.release.exists())
        self.assertEqual(node_generations.retained_configs(self.root, installer), {})

    def test_fifo_journal_is_refused_without_blocking(self):
        path = self.directory / "1.collecting"
        os.mkfifo(path, 0o600)
        with self.assertRaises(installer.InstallError): node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertTrue(self.release.exists())

    def test_release_path_conflict_is_refused_before_native_mutation(self):
        foreign = self.root / "foreign"
        self.release.rename(foreign)
        self.release.symlink_to(foreign, target_is_directory=True)
        with self.assertRaises(installer.InstallError): node_generations.collect(self.args, installer)
        installer.checked.assert_not_called()
        self.assertTrue((foreign / "artifact").exists())


if __name__ == "__main__":
    unittest.main()
