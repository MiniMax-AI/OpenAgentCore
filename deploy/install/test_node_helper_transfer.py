"""Retain the verified bootstrap across the system installer's UID boundary."""
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock
import zipfile

import node_generations
import node_install as installer


class HelperTransferTests(unittest.TestCase):
    def test_captured_archive_survives_removal_of_private_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "node-install.pyz"
            source.write_bytes(node_generations.helper_archive(SimpleNamespace(bundle=None), installer))
            args = SimpleNamespace(bundle=None, source_url="https://core.example", core_url="https://core.example")
            with mock.patch.object(sys, "argv", [str(source)]):
                captured = node_generations.helper_archive(args, installer)
                source.unlink()
                node_generations.install_helper(root, args, installer, captured)
            helper = root / "generation-preparer.pyz"
            self.assertEqual(helper.read_bytes(), captured)
            self.assertEqual(stat.S_IMODE(helper.stat().st_mode), 0o600)
            with zipfile.ZipFile(helper) as archive:
                self.assertEqual(set(archive.namelist()), {"__main__.py", "node_spec.py", "distribution.py", "node_generations.py", "install_display.py", "node_output.py"})
            self.assertEqual(json.loads((root / "preparation.json").read_text()), {"source_url": "https://core.example"})

    @unittest.skipUnless(hasattr(os, "fork") and os.geteuid() == 0, "requires a disposable Linux root test environment")
    def test_real_service_uid_cannot_read_private_download_but_can_install_snapshot(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            parent.chmod(0o711)
            download, root = parent / "download", parent / "service-state"
            download.mkdir(mode=0o700)
            root.mkdir(mode=0o700)
            uid = gid = 65534
            os.chown(root, uid, gid)
            source = download / "node-install.pyz"
            source.write_bytes(node_generations.helper_archive(SimpleNamespace(bundle=None), installer))
            source.chmod(0o600)
            args = SimpleNamespace(bundle=None, source_url="https://core.example", core_url="https://core.example")
            with mock.patch.object(sys, "argv", [str(source)]):
                captured = node_generations.helper_archive(args, installer)
                pid = os.fork()
                if pid == 0:
                    try:
                        os.setgroups([])
                        os.setgid(gid)
                        os.setuid(uid)
                        try:
                            source.read_bytes()
                        except PermissionError:
                            pass
                        else:
                            raise AssertionError("fixture does not reproduce the private-directory boundary")
                        node_generations.install_helper(root, args, installer, captured)
                        helper = root / "generation-preparer.pyz"
                        result = subprocess.run([sys.executable, str(helper), "--help"], capture_output=True, timeout=10)
                        assert result.returncode == 0, result.stderr.decode()
                        assert b"--generation" not in result.stdout  # Private actions remain hidden.
                        node_generations.install_helper(root, args, installer, captured)
                    except BaseException:
                        import traceback
                        traceback.print_exc()
                        os._exit(1)
                    os._exit(0)
                _, status = os.waitpid(pid, 0)
            self.assertEqual(os.waitstatus_to_exitcode(status), 0)
            helper = root / "generation-preparer.pyz"
            self.assertEqual(hashlib.sha256(helper.read_bytes()).digest(), hashlib.sha256(captured).digest())
            self.assertEqual((helper.stat().st_uid, stat.S_IMODE(helper.stat().st_mode)), (uid, 0o600))
            self.assertEqual((download.stat().st_uid, stat.S_IMODE(download.stat().st_mode)), (0, 0o700))
            self.assertEqual(stat.S_IMODE(source.stat().st_mode), 0o600)


if __name__ == "__main__":
    unittest.main()
