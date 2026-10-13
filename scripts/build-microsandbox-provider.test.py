#!/usr/bin/env python3
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("build_provider", Path(__file__).with_name("build-microsandbox-provider.py"))
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)


class OfficialBundleTests(unittest.TestCase):
    def test_only_verified_payload_replaces_empty_sentinel(self):
        with tempfile.TemporaryDirectory() as directory:
            source, destination = Path(directory) / "ffi", Path(directory) / "sentinel"
            source.write_bytes(b"official ffi")
            destination.touch()
            with patch.object(build, "FFI_SHA256", hashlib.sha256(source.read_bytes()).hexdigest()):
                build.install_ffi(source, destination)
                self.assertEqual(destination.read_bytes(), source.read_bytes())
                with self.assertRaisesRegex(ValueError, "empty FFI"):
                    build.install_ffi(source, destination)

    def test_corrupt_payload_does_not_replace_sentinel(self):
        with tempfile.TemporaryDirectory() as directory:
            source, destination = Path(directory) / "ffi", Path(directory) / "sentinel"
            source.write_bytes(b"wrong release")
            destination.touch()
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                build.install_ffi(source, destination)
            self.assertEqual(destination.read_bytes(), b"")

    def test_symlink_sentinel_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            source, destination, outside = (Path(directory) / name for name in ("ffi", "sentinel", "outside"))
            source.write_bytes(b"official ffi")
            outside.touch()
            destination.symlink_to(outside)
            with patch.object(build, "FFI_SHA256", hashlib.sha256(source.read_bytes()).hexdigest()):
                with self.assertRaisesRegex(ValueError, "empty FFI"):
                    build.install_ffi(source, destination)
            self.assertEqual(outside.read_bytes(), b"")


if __name__ == "__main__":
    unittest.main()
