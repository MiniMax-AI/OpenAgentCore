"""Regression checks for offline distribution identity and archive integrity."""

import hashlib
import importlib.util
import json
import pathlib
import tarfile
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("distribution", pathlib.Path(__file__).with_name("core-distribution-manifest.py"))
distribution = importlib.util.module_from_spec(spec)
spec.loader.exec_module(distribution)


class DistributionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.stage = pathlib.Path(self.temporary.name)
        self.bundle = self.stage / "parsar-core-test-linux-amd64"
        self.bundle.mkdir()
        (self.bundle / "install.sh").write_text("#!/bin/sh\nexit 0\n")
        (self.bundle / "source.tar.gz").write_bytes(b"source archive")
        runtime = self.stage / "core/microsandbox"
        runtime.mkdir(parents=True)
        (runtime / "msb").write_bytes(b"runtime")
        (runtime / "libkrunfw.so.5.6.1").write_bytes(b"firmware")
        for number, name in enumerate(("core", "web", "runtime", "database"), 1):
            (self.stage / (name + ".id")).write_text("sha256:" + str(number) * 64 + "\n")
        self.inspection = {"digest": "sha256:" + "a" * 64, "architecture": "amd64", "os": "linux"}
        self.write_inspection()

    def write_inspection(self):
        (self.stage / "runtime-inspect.json").write_text(json.dumps(self.inspection))

    def test_oci_manifest_identity_is_distinct_from_docker_config_identity(self):
        distribution.manifest(self.bundle, self.stage, "commit", "tree")
        metadata = json.loads((self.bundle / "manifest.json").read_text())
        self.assertEqual(metadata["runtime_ref"], "parsar-core-runtime@sha256:" + "a" * 64)
        self.assertEqual(metadata["images"]["runtime"], "sha256:" + "3" * 64)
        self.assertEqual(metadata["microsandbox"]["runtime_sha256"], hashlib.sha256(b"runtime").hexdigest())
        for line in (self.bundle / "SHA256SUMS").read_text().splitlines():
            digest, name = line.split("  ", 1)
            self.assertEqual(digest, distribution.sha256(self.bundle / name))

    def test_missing_manifest_digest_does_not_fall_back_to_config_id(self):
        self.inspection.pop("digest")
        self.inspection["config"] = {"digest": "sha256:" + "3" * 64}
        self.write_inspection()
        with self.assertRaisesRegex(ValueError, "manifest digest"):
            distribution.manifest(self.bundle, self.stage, "commit", "tree")

    def test_wrong_guest_platform_rejected(self):
        self.inspection["architecture"] = "arm64"
        self.write_inspection()
        with self.assertRaisesRegex(ValueError, "platform"):
            distribution.manifest(self.bundle, self.stage, "commit", "tree")

    def test_archive_reproducible_and_installer_executable(self):
        native = self.bundle / "native/bin/agents-api"
        native.parent.mkdir(parents=True)
        native.write_bytes(b"native executable")
        native.chmod(0o555)
        distribution.manifest(self.bundle, self.stage, "commit", "tree")
        distribution.archive(self.bundle, "1700000000")
        archive = self.bundle.with_name(self.bundle.name + ".tar.gz")
        first = archive.read_bytes()
        distribution.archive(self.bundle, "1700000000")
        self.assertEqual(first, archive.read_bytes())
        self.assertEqual(archive.with_name(archive.name + ".sha256").read_text(), distribution.sha256(archive) + "  " + archive.name + "\n")
        with tarfile.open(archive) as contents:
            self.assertEqual(contents.getmember(self.bundle.name + "/install.sh").mode, 0o755)
            self.assertEqual(contents.getmember(self.bundle.name + "/manifest.json").mode, 0o644)
            self.assertEqual(contents.getmember(self.bundle.name + "/native/bin/agents-api").mode, 0o555)

    def test_bad_upstream_checksum_does_not_extract(self):
        archive = self.stage / "untrusted.tar.gz"
        archive.write_bytes(b"not the pinned release")
        destination = self.stage / "extracted"
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            distribution.extract_runtime(archive, destination)
        self.assertFalse(destination.exists())


if __name__ == "__main__":
    unittest.main()
