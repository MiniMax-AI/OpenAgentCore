"""Public release bootstrap tests; fixtures never start Docker or a real installer."""
import contextlib
import errno
import signal
import shlex
import sys
import hashlib
import http.client
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
        temporary_root = pathlib.Path.home() / ".oac/tests/release"
        temporary_root.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=temporary_root)
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
        self.metadata["assets"][0]["size"] = len(self.archive)
        self.metadata["assets"][1]["size"] = 67 + len(self.name)
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        self.http = stack.enter_context(mock.patch.object(bootstrap, "open_url", side_effect=self.response))
        stack.enter_context(mock.patch.object(bootstrap.pathlib.Path, "home", return_value=self.root))
        stack.enter_context(mock.patch.object(bootstrap.platform, "system", return_value="Linux"))
        stack.enter_context(mock.patch.object(bootstrap.platform, "machine", return_value="x86_64"))
        stack.enter_context(mock.patch.object(bootstrap.time, "sleep"))
        # The downloader waits for the installer before retaining its verified bundle.
        self.invoke = stack.enter_context(mock.patch.object(bootstrap, "run_installer"))

    def make_archive(self, files):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode="w:gz") as archive:
            for name, content in files.items():
                member = tarfile.TarInfo(self.stem + "/" + name)
                member.mode, member.size = 0o755, len(content)
                archive.addfile(member, io.BytesIO(content))
        return data.getvalue()

    def response(self, url, binary=False, offset=0):
        if url.endswith("/releases/latest") or "/releases/tags/" in url:
            return io.BytesIO(json.dumps(self.metadata).encode())
        if url.endswith("/assets/7"):
            return io.BytesIO(self.archive)
        if url.endswith("/assets/8"):
            return io.BytesIO((hashlib.sha256(self.archive).hexdigest() + "  " + self.name + "\n").encode())
        raise AssertionError("Unexpected URL: " + url)

    def test_default_latest_is_resolved_once_and_installer_arguments_forwarded(self):
        bootstrap.main(["--host", "127.0.0.1", "--web-port", "8088", "--public-url", "https://core.example"])
        urls = [c.args[0] for c in self.http.call_args_list]
        self.assertEqual(sum(url.endswith("/releases/latest") for url in urls), 1)
        self.assertTrue(urls[1].endswith("/assets/8"))
        self.assertTrue(urls[2].endswith("/assets/7"))
        root, arguments, lock = self.invoke.call_args.args
        self.assertEqual(arguments, ["--host", "127.0.0.1", "--web-port", "8088", "--public-url", "https://core.example"])
        self.assertEqual(len(list((self.root / ".oac/releases").glob("release-*/" + self.stem + "/install.sh"))), 1)
        self.assertFalse(list((self.root / ".oac/releases").glob(".download-*")))

    def test_control_plane_bundle_selected_without_execution_assets(self):
        for index, suffix in enumerate(("-offline.tar.gz", "-runtime.tar.gz", "-daemon", "-sandbox-node"), 20):
            self.metadata["assets"].append({"id": index, "name": self.stem + suffix})
        bootstrap.main([])
        urls = [call.args[0] for call in self.http.call_args_list]
        self.assertEqual(len(urls), 3)
        self.assertTrue(urls[-1].endswith("/assets/7"))
        self.assertFalse((self.invoke.call_args.args[0] / "artifacts").exists())

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
        self.assertEqual(self.invoke.call_args.args[1], ["--core-only"])

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
        existing.parent.chmod(0o700)
        (existing / "data").write_text("keep")
        def response(url, binary=False, offset=0):
            if url.endswith("/assets/8"):
                return io.BytesIO(("b" * 64 + "  " + self.name + "\n").encode())
            return self.response(url, binary)
        self.http.side_effect = response
        with self.assertRaisesRegex(bootstrap.ReleaseError, "checksum mismatch"):
            bootstrap.main([])
        self.invoke.assert_not_called()
        self.assertEqual(set(path.name for path in existing.parent.iterdir()), {"existing", ".download.lock"})
        self.assertEqual((existing / "data").read_text(), "keep")

    def test_archive_source_mismatch_is_refused(self):
        self.archive = self.make_archive({"install.sh": b"exit 0", "manifest.json": b'{"source_commit":"wrong"}'})
        self.metadata["assets"][0]["size"] = len(self.archive)
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
            bootstrap.main(["--core-only"])
        root, arguments, lock = self.invoke.call_args.args
        self.assertEqual(arguments, ["--core-only"])
        self.assertTrue(root.is_relative_to(self.root / ".oac/releases"))
        self.assertEqual(len(list((self.root / ".oac/releases").glob("release-*/" + self.stem + "/install.sh"))), 1)

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

    def test_short_download_resumes_only_the_selected_asset(self):
        calls = []
        def response(url, binary=False, offset=0):
            if url.endswith("/assets/7"):
                calls.append(offset)
                result = io.BytesIO(self.archive[:20] if not offset else self.archive[offset:])
                if offset:
                    result.status = 206
                    result.headers = {"Content-Range": f"bytes {offset}-{len(self.archive) - 1}/{len(self.archive)}"}
                return result
            return self.response(url, binary, offset)
        self.http.side_effect = response
        bootstrap.main([])
        self.assertEqual(calls, [0, 20])
        self.invoke.assert_called_once()

    def test_range_ignored_restarts_instead_of_appending(self):
        attempts = []
        def response(url, binary=False, offset=0):
            if url.endswith("/assets/7"):
                attempts.append(offset)
                return io.BytesIO(self.archive[:20] if len(attempts) == 1 else self.archive)
            return self.response(url, binary, offset)
        self.http.side_effect = response
        bootstrap.main([])
        self.assertEqual(attempts, [0, 20])
        self.invoke.assert_called_once()

    def test_network_failure_is_bounded_and_cleans_staging(self):
        for failure in (bootstrap.TransferError("network"), TimeoutError(), ConnectionResetError()):
            with self.subTest(failure=failure):
                self.http.reset_mock()
                self.http.side_effect = failure
                with self.assertRaisesRegex(bootstrap.ReleaseError, "3 attempts"):
                    bootstrap.main([])
                self.assertEqual(self.http.call_count, 3)
                self.assertFalse((self.root / ".oac/releases/.download").exists())
        self.invoke.assert_not_called()

    def test_truncated_http_metadata_is_retried_before_parsing(self):
        class Socket:
            def makefile(self, mode):
                return io.BytesIO(b'HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n{"tag_name":')
        partial = http.client.HTTPResponse(Socket())
        partial.begin()
        self.http.side_effect = [partial, self.response(bootstrap.API + "/releases/latest"),
                                 self.response(bootstrap.API + "/releases/assets/8"),
                                 self.response(bootstrap.API + "/releases/assets/7")]
        bootstrap.main([])
        self.assertEqual(sum(call.args[0].endswith("/releases/latest") for call in self.http.call_args_list), 2)
        self.invoke.assert_called_once()

    def test_malformed_metadata_and_asset_sizes_fail_without_execution(self):
        for metadata in ([], {}, {**self.metadata, "assets": [None]},
                         {**self.metadata, "assets": [{**self.metadata["assets"][0], "size": -1},
                                                       self.metadata["assets"][1]]}):
            with self.subTest(metadata=metadata):
                self.http.side_effect = lambda *a, **kw: io.BytesIO(json.dumps(metadata).encode())
                with self.assertRaises(bootstrap.ReleaseError):
                    bootstrap.main([])
                self.assertFalse((self.root / ".oac/releases/.download").exists())
        self.invoke.assert_not_called()

    def test_space_check_and_write_failure_remove_partial_downloads(self):
        with mock.patch.object(bootstrap.shutil, "disk_usage", return_value=types.SimpleNamespace(free=0)):
            with self.assertRaisesRegex(bootstrap.ReleaseError, "disk space"):
                bootstrap.main([])
        for failure in (OSError(errno.ENOSPC, "disk full"), OSError(errno.EDQUOT, "quota")):
            with mock.patch.object(bootstrap, "extract", side_effect=failure):
                with self.assertRaises(OSError):
                    bootstrap.main([])
        self.assertEqual([p.name for p in (self.root / ".oac/releases").iterdir()], [".download.lock"])
        self.invoke.assert_not_called()

    def test_extraction_checks_uncompressed_space(self):
        archive = self.root / "archive.tar.gz"
        archive.write_bytes(self.archive)
        with mock.patch.object(bootstrap.shutil, "disk_usage", return_value=types.SimpleNamespace(free=0)):
            with self.assertRaisesRegex(bootstrap.ReleaseError, "disk space"):
                bootstrap.extract(archive, self.root, self.stem)
        self.assertFalse((self.root / self.stem).exists())

    def test_handoff_failure_removes_extracted_bundle(self):
        self.invoke.side_effect = FileNotFoundError("bash")
        with self.assertRaises(FileNotFoundError):
            bootstrap.main([])
        self.assertEqual([p.name for p in (self.root / ".oac/releases").iterdir()], [".download.lock"])

    def test_stale_download_is_removed_even_when_network_is_unavailable(self):
        stale = self.root / ".oac/releases/.download"
        stale.mkdir(parents=True, mode=0o700)
        stale.parent.chmod(0o700)
        (stale / "partial").write_text("discard")
        outside = self.root / "user-data"
        outside.write_text("keep")
        (stale / "link").symlink_to(outside)
        self.http.side_effect = bootstrap.TransferError("network")
        with self.assertRaises(bootstrap.ReleaseError):
            bootstrap.main([])
        self.assertFalse(stale.exists())
        self.assertEqual(outside.read_text(), "keep")

    def test_concurrent_download_does_not_remove_active_staging(self):
        cache = self.root / ".oac/releases"
        cache.mkdir(parents=True, mode=0o700)
        with bootstrap.staging(cache) as (active, lock):
            (active / "partial").write_text("keep")
            with self.assertRaisesRegex(bootstrap.ReleaseError, "Another release download"):
                bootstrap.main([])
            self.assertEqual((active / "partial").read_text(), "keep")
        self.http.assert_not_called()

    def test_staging_symlink_is_refused_without_touching_target(self):
        cache = self.root / ".oac/releases"
        cache.mkdir(parents=True, mode=0o700)
        outside = self.root / "user-data"
        outside.mkdir()
        (outside / "keep").write_text("keep")
        (cache / ".download").symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(bootstrap.ReleaseError, "staging directory"):
            bootstrap.main([])
        self.assertEqual((outside / "keep").read_text(), "keep")

    def test_signal_during_download_cleans_before_exit(self):
        source = SCRIPT.read_text().split("3<<'PY'\n", 1)[1].rsplit("\nPY", 1)[0]
        fixture = '''
        platform.system = lambda: "Linux"
        platform.machine = lambda: "x86_64"
        def blocked(version):
            print("READY", flush=True)
            time.sleep(30)
        select_release = blocked
        main([])'''
        source = source.replace("        main(sys.argv[1:])", fixture)
        for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            with self.subTest(signal=number):
                process = subprocess.Popen([__import__("sys").executable, "-c", source],
                                           env={**os.environ, "HOME": str(self.root)},
                                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                try:
                    for line in process.stdout:
                        if line.strip() == "READY":
                            break
                    else:
                        self.fail("Downloader did not reach the signal fixture")
                    process.send_signal(number)
                    _, error = process.communicate(timeout=5)
                    self.assertEqual(process.returncode, 130, error)
                    self.assertNotIn("Traceback", error)
                    self.assertFalse((self.root / ".oac/releases/.download").exists())
                finally:
                    if process.poll() is None:
                        process.kill()
                    process.communicate()

    def test_installer_cleanup_finishes_before_bundle_removal_and_lock_survives_parent(self):
        source = SCRIPT.read_text().split("3<<'PY'\n", 1)[1].rsplit("\nPY", 1)[0]
        child = '''import os, pathlib, signal, time
def stop(number, frame):
    time.sleep(0.1)
    assert pathlib.Path.home().joinpath(".oac/releases/.download").is_dir()
    pathlib.Path.home().joinpath("cleaned").touch()
    raise SystemExit(130)
for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
    signal.signal(number, stop)
print("CHILD_READY " + str(os.getpid()), flush=True)
while True:
    signal.pause()
'''
        archive = self.make_archive({"install.sh": ("#!/bin/bash\nexec python3 -c " + shlex.quote(child)).encode(),
                                     "manifest.json": json.dumps({"source_commit": self.sha}).encode()})
        metadata = [dict(asset) for asset in self.metadata["assets"]]
        metadata[0]["size"] = len(archive)
        checksum = (hashlib.sha256(archive).hexdigest() + "  " + self.name + "\n").encode()
        fixture = f'''
        platform.system = lambda: "Linux"
        platform.machine = lambda: "x86_64"
        select_release = lambda version: ("v1", {metadata[0]!r}, {metadata[1]!r})
        def download(asset, destination, limit=None):
            destination.write_bytes({archive!r} if asset["id"] == 7 else {checksum!r})
        main([])'''
        source = source.replace("        main(sys.argv[1:])", fixture)
        for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP, signal.SIGKILL):
            with self.subTest(signal=number):
                cleaned = self.root / "cleaned"
                cleaned.unlink(missing_ok=True)
                child_pid = None
                process = subprocess.Popen([sys.executable, "-c", source],
                                           env={**os.environ, "HOME": str(self.root)},
                                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                try:
                    for line in process.stdout:
                        if line.startswith("CHILD_READY "):
                            child_pid = int(line.split()[1])
                            break
                    self.assertIsNotNone(child_pid)
                    process.send_signal(number)
                    if number == signal.SIGKILL:
                        process.wait(timeout=5)
                        with self.assertRaisesRegex(bootstrap.ReleaseError, "Another release download"):
                            bootstrap.main([])
                        os.kill(child_pid, signal.SIGTERM)
                    _, error = process.communicate(timeout=5)
                    child_pid = None
                    self.assertTrue(cleaned.exists(), error)
                    if number == signal.SIGKILL:
                        self.http.side_effect = bootstrap.TransferError("network")
                        with self.assertRaises(bootstrap.ReleaseError):
                            bootstrap.main([])
                    else:
                        self.assertEqual(process.returncode, 130, error)
                    self.assertFalse((self.root / ".oac/releases/.download").exists())
                    self.assertFalse(list((self.root / ".oac/releases").glob("release-*")))
                finally:
                    if child_pid is not None:
                        with contextlib.suppress(ProcessLookupError):
                            os.kill(child_pid, signal.SIGKILL)
                    if process.poll() is None:
                        process.kill()
                    process.communicate()

    def test_progress_is_dynamic_only_for_terminals_and_does_not_claim_success_on_exit(self):
        for terminal in (False, True):
            stream = io.StringIO()
            stream.isatty = lambda: terminal
            with mock.patch.dict(os.environ, {"TERM": "xterm"}), contextlib.redirect_stderr(stream):
                with bootstrap.Progress("Download", 100) as progress:
                    progress.update(50)
            self.assertEqual("50%" in stream.getvalue(), terminal)
            self.assertNotIn("100%", stream.getvalue())
            if not terminal:
                self.assertEqual(stream.getvalue(), "")


if __name__ == "__main__":
    unittest.main()
