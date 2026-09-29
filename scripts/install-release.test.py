"""Public release bootstrap tests; fixtures never start Docker or a real installer."""
import contextlib
import hashlib
import io
import json
import os
import stat
import pathlib
import subprocess
import tarfile
import tempfile
import types
import unittest
from unittest import mock

SCRIPT = pathlib.Path(__file__).parents[1] / "deploy/install-release.sh"
bootstrap = types.ModuleType("bootstrap")
exec(compile(SCRIPT.read_text().split("3<<'PY'\n", 1)[1].rsplit("\nPY", 1)[0],
             str(SCRIPT), "exec"), bootstrap.__dict__)


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.sha = "a" * 40
        self.stem = "oac-" + self.sha + "-linux-amd64"
        self.name = self.stem + ".tar.gz"
        self.metadata = {"tag_name": "v1.2.3", "draft": False, "prerelease": False,
                         "assets": [{"id": 7, "name": self.name},
                                    {"id": 8, "name": self.name + ".sha256"}]}
        self.archive = self.make_archive({
            "install.sh": b"#!/bin/sh\nexit 0\n",
            "manifest.json": json.dumps({"source_commit": self.sha}).encode()})
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        self.http = stack.enter_context(mock.patch.object(bootstrap, "open_url", side_effect=self.response))
        stack.enter_context(mock.patch.object(bootstrap.pathlib.Path, "home", return_value=self.root))
        stack.enter_context(mock.patch.object(bootstrap.platform, "system", return_value="Linux"))
        stack.enter_context(mock.patch.object(bootstrap.platform, "machine", return_value="x86_64"))
        stack.enter_context(mock.patch.object(bootstrap.os, "geteuid", return_value=1000))
        self.invoke = stack.enter_context(mock.patch.object(bootstrap.subprocess, "call", return_value=0))

    def make_archive(self, files):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode="w:gz") as archive:
            for name, content in files.items():
                member = tarfile.TarInfo(self.stem + "/" + name)
                member.mode, member.size = 0o755, len(content)
                archive.addfile(member, io.BytesIO(content))
        return data.getvalue()

    def response(self, url, binary=False):
        if url.endswith("/releases/latest") or "/releases/tags/" in url:
            return io.BytesIO(json.dumps(self.metadata).encode())
        if url.endswith("/assets/7"):
            return io.BytesIO(self.archive)
        if url.endswith("/assets/8"):
            return io.BytesIO((hashlib.sha256(self.archive).hexdigest() + "  " + self.name + "\n").encode())
        raise AssertionError("Unexpected URL: " + url)

    def test_default_latest_is_resolved_once_and_installer_arguments_forwarded(self):
        self.assertEqual(bootstrap.main(["--host", "127.0.0.1", "--port", "8088", "--public-url", "https://core.example"]), 0)
        urls = [c.args[0] for c in self.http.call_args_list]
        self.assertEqual(sum(url.endswith("/releases/latest") for url in urls), 1)
        self.assertTrue(urls[1].endswith("/assets/8"))
        self.assertTrue(urls[2].endswith("/assets/7"))
        command = self.invoke.call_args.args[0]
        self.assertEqual(command[0], "bash")
        self.assertEqual(command[2:], ["--host", "127.0.0.1", "--port", "8088", "--public-url", "https://core.example"])
        self.assertTrue(pathlib.Path(command[1]).is_file())
        self.assertFalse(list((self.root / ".oac/releases").glob(".download-*")))

    def test_control_plane_bundle_selected_without_execution_assets(self):
        for index, suffix in enumerate(("-offline.tar.gz", "-runtime.tar.gz", "-daemon", "-sandbox-node"), 20):
            self.metadata["assets"].append({"id": index, "name": self.stem + suffix})
        bootstrap.main([])
        urls = [call.args[0] for call in self.http.call_args_list]
        self.assertEqual(len(urls), 3)
        self.assertTrue(urls[-1].endswith("/assets/7"))
        self.assertFalse((pathlib.Path(self.invoke.call_args.args[0][1]).parent / "artifacts").exists())

    def test_offline_only_release_is_not_silently_downloaded(self):
        self.metadata["assets"][0]["name"] = self.stem + "-offline.tar.gz"
        with self.assertRaisesRegex(bootstrap.ReleaseError, "control-plane bundle"):
            bootstrap.main([])
        self.assertEqual(len(self.http.call_args_list), 1)
        self.invoke.assert_not_called()

    def test_selected_prerelease_is_allowed(self):
        self.metadata.update(tag_name="v2.0.0-rc.1", prerelease=True)
        bootstrap.main(["--version", "v2.0.0-rc.1", "--core-only"])
        self.assertTrue(self.http.call_args_list[0].args[0].endswith("/releases/tags/v2.0.0-rc.1"))
        self.assertEqual(self.invoke.call_args.args[0][-1], "--core-only")

    def test_latest_never_selects_prerelease_or_draft(self):
        for key in ("prerelease", "draft"):
            with self.subTest(key=key):
                self.metadata[key] = True
                with self.assertRaises(bootstrap.ReleaseError):
                    bootstrap.main([])
                self.metadata[key] = False
        self.invoke.assert_not_called()

    def test_missing_or_duplicate_asset_is_refused(self):
        self.metadata["assets"].append(dict(self.metadata["assets"][0]))
        with self.assertRaisesRegex(bootstrap.ReleaseError, "exactly one"):
            bootstrap.main([])
        self.metadata["assets"] = self.metadata["assets"][:1]
        with self.assertRaisesRegex(bootstrap.ReleaseError, "checksum"):
            bootstrap.main([])
        self.invoke.assert_not_called()

    def test_checksum_failure_never_executes_and_removes_only_own_download(self):
        existing = self.root / ".oac/releases/existing"
        existing.mkdir(parents=True)
        (existing / "data").write_text("keep")
        def response(url, binary=False):
            if url.endswith("/assets/8"):
                return io.BytesIO(("b" * 64 + "  " + self.name + "\n").encode())
            return self.response(url, binary)
        self.http.side_effect = response
        with self.assertRaisesRegex(bootstrap.ReleaseError, "checksum mismatch"):
            bootstrap.main([])
        self.invoke.assert_not_called()
        self.assertEqual(list(existing.parent.iterdir()), [existing])
        self.assertEqual((existing / "data").read_text(), "keep")

    def test_archive_source_mismatch_is_refused(self):
        self.archive = self.make_archive({"install.sh": b"exit 0", "manifest.json": b'{"source_commit":"wrong"}'})
        with self.assertRaisesRegex(bootstrap.ReleaseError, "source"):
            bootstrap.main([])
        self.invoke.assert_not_called()

    def test_unsafe_tar_paths_and_links_are_rejected(self):
        for name, kind in (("../escape", tarfile.REGTYPE), ("/tmp/escape", tarfile.REGTYPE),
                           (self.stem + "/link", tarfile.SYMTYPE),
                           (self.stem + "/hard", tarfile.LNKTYPE)):
            with self.subTest(name=name):
                path = self.root / "unsafe.tar.gz"
                with tarfile.open(path, "w:gz") as archive:
                    member = tarfile.TarInfo(name)
                    member.type, member.linkname = kind, "/tmp/escape"
                    archive.addfile(member, io.BytesIO())
                with self.assertRaisesRegex(bootstrap.ReleaseError, "unsafe"):
                    bootstrap.extract(path, self.root, self.stem)

    def test_duplicate_paths_are_rejected(self):
        path = self.root / "duplicate.tar.gz"
        with tarfile.open(path, "w:gz") as archive:
            for _ in range(2):
                archive.addfile(tarfile.TarInfo(self.stem + "/same"), io.BytesIO())
        with self.assertRaisesRegex(bootstrap.ReleaseError, "duplicate"):
            bootstrap.extract(path, self.root, self.stem)

    def test_unsupported_platform_does_not_download(self):
        with mock.patch.object(bootstrap.platform, "system", return_value="Darwin"):
            with self.assertRaisesRegex(bootstrap.ReleaseError, "Linux amd64"):
                bootstrap.main([])
        self.http.assert_not_called()

    def test_root_uses_the_same_verified_installation_path(self):
        with mock.patch.object(bootstrap.os, "geteuid", return_value=0):
            self.assertEqual(bootstrap.main(["--core-only"]), 0)
        command = self.invoke.call_args.args[0]
        self.assertEqual(command, ["bash", command[1], "--core-only"])
        self.assertTrue(pathlib.Path(command[1]).is_relative_to(self.root / ".oac/releases"))
        self.assertTrue(pathlib.Path(command[1]).is_file())

    def test_installer_failure_is_returned_and_bundle_retained(self):
        self.invoke.return_value = 17
        self.assertEqual(bootstrap.main([]), 17)
        self.assertTrue(pathlib.Path(self.invoke.call_args.args[0][1]).is_file())

    def test_new_private_directories_do_not_inherit_public_umask(self):
        previous = os.umask(0o022)
        try:
            bootstrap.main([])
        finally:
            os.umask(previous)
        for path in (self.root / ".oac", self.root / ".oac/releases"):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700)

    def test_shell_entrypoint_help_needs_no_network(self):
        result = subprocess.run(["bash", str(SCRIPT), "--help"], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)
        self.assertIn("--version", result.stdout)
        self.assertIn("latest stable release", result.stdout)


if __name__ == "__main__":
    unittest.main()
