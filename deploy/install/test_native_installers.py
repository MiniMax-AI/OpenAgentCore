"""Online/offline catalog retention shared by container and native Core."""
import json
from pathlib import Path
import tempfile
import unittest

import native_installers
from distribution import digest


class NativeInstallersTests(unittest.TestCase):
    def test_online_install_offline_addition_and_repair_preserve_verified_content(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root, bundle = base / "root", base / "bundle"
            root.mkdir()
            source = bundle / "native-installers"
            source.mkdir(parents=True)
            archive = source / "linux-amd64.tar.gz"
            archive.write_bytes(b"qualified")
            payload_hash = digest(archive)
            archive.unlink()
            catalog = {"version": "a" * 40, "artifacts": {"linux-amd64": {"sha256": payload_hash}}}
            (source / "catalog.json").write_text(json.dumps(catalog))
            state = {"mode": "core-only", "source_commit": "a" * 40}
            native_installers.prepare(root, state, bundle)
            self.assertEqual([p.name for p in (root / "native-installers").iterdir()], ["catalog.json"])
            archive.write_bytes(b"qualified")
            native_installers.prepare(root, state, bundle)
            installed = root / "native-installers/linux-amd64.tar.gz"
            self.assertEqual(installed.read_bytes(), b"qualified")
            self.assertEqual(installed.stat().st_mode & 0o777, 0o600)
            archive.unlink()
            native_installers.prepare(root, state, bundle)
            self.assertEqual(installed.read_bytes(), b"qualified")
            installed.write_bytes(b"corrupt")
            with self.assertRaisesRegex(RuntimeError, "files differ"):
                native_installers.prepare(root, state, bundle)
            self.assertEqual(installed.read_bytes(), b"corrupt")

    def test_foreign_catalog_refuses_writes(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            source = base / "bundle/native-installers"
            source.mkdir(parents=True)
            (source / "catalog.json").write_text(json.dumps({"version": "foreign", "artifacts": {}}))
            with self.assertRaisesRegex(RuntimeError, "does not match"):
                native_installers.prepare(base / "root", {"mode": "all", "source_commit": "a" * 40}, base / "bundle")
            self.assertFalse((base / "root").exists())
