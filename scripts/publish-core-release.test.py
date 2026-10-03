"""Publication failure tests using the documented GitHub REST response shapes."""
import contextlib
import hashlib
import gzip
import importlib.util
import json
import io
import tarfile
import pathlib
import subprocess
import tempfile
import unittest
import threading
from unittest import mock
from urllib.parse import unquote

spec = importlib.util.spec_from_file_location(
    "publisher", pathlib.Path(__file__).with_name("publish-core-release.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)
REAL_API = publisher.api
REAL_REGISTRY_MANIFEST = publisher.registry_manifest


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.assets = pathlib.Path(self.temp.name)
        self.revision = "a" * 40
        self.stem = "oac-" + self.revision + "-linux-amd64"
        for name in (self.stem + ".tar.gz", self.stem + "-offline.tar.gz", "install.sh"):
            (self.assets / name).write_bytes(b"archive fixture")
            (self.assets / (name + ".sha256")).write_text(
                hashlib.sha256(b"archive fixture").hexdigest() + "  " + name + "\n")
        catalog = {"version": self.revision, "artifacts": {}}
        for platform in ("linux-amd64", "darwin-arm64", "windows-amd64"):
            name = f"oac-native-{self.revision}-{platform}.tar.gz"
            (self.assets / name).write_bytes(b"native archive")
            checksum = hashlib.sha256(b"native archive").hexdigest()
            catalog["artifacts"][platform] = {"sha256": checksum}
            (self.assets / (name + ".sha256")).write_text(checksum + "  " + name + "\n")
        with tarfile.open(self.assets / (self.stem + ".tar.gz"), "w:gz") as archive:
            raw = json.dumps(catalog).encode()
            member = tarfile.TarInfo(self.stem + "/native-installers/catalog.json")
            member.size = len(raw)
            archive.addfile(member, io.BytesIO(raw))
        path = self.assets / (self.stem + ".tar.gz")
        path.with_name(path.name + ".sha256").write_text(publisher.distribution.sha256(path) + "  " + path.name + "\n")
        self.release = None
        self.existing = []
        self.context_repository = self.canonical_repository = "MiniMax-AI/OpenAgentCore"
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        self.images = stack.enter_context(mock.patch.object(publisher, "publish_images", return_value={}))
        self.api = stack.enter_context(mock.patch.object(publisher, "api", side_effect=self.response))

    def test_registry_failure_leaves_release_draft(self):
        self.images.side_effect = RuntimeError("registry unavailable")
        with self.assertRaisesRegex(RuntimeError, "registry unavailable"):
            self.publish()
        self.assertTrue(self.release["draft"])
        self.assertFalse(any("PATCH" in call.args for call in self.writes()))

    def test_draft_does_not_publish_images(self):
        self.publish(tag="build-" + self.revision, mode="draft")
        self.images.assert_not_called()

    def test_missing_native_asset_refuses_release_creation(self):
        (self.assets / f"oac-native-{self.revision}-windows-amd64.tar.gz").unlink()
        with self.assertRaises(FileNotFoundError):
            self.publish()
        self.assertEqual(self.writes(), [])

    def test_corrupt_native_asset_refuses_release_creation(self):
        (self.assets / f"oac-native-{self.revision}-linux-amd64.tar.gz").write_bytes(b"corrupt")
        with self.assertRaisesRegex(ValueError, "Native installer checksum"):
            self.publish()
        self.assertEqual(self.writes(), [])

    def response(self, repository, endpoint, *args):
        if endpoint == "https://api.github.com/repos/" + self.context_repository:
            return {"full_name": self.canonical_repository}
        if endpoint.startswith("git/"):
            return {"object": {"type": "commit", "sha": self.revision}}
        if endpoint.startswith("releases?"):
            return self.existing
        if endpoint == "releases" and args[:2] == ("--method", "POST"):
            fields = dict(v.split("=", 1) for v in args if "=" in v)
            self.release = {"id": 7, "draft": fields["draft"] == "true",
                            "prerelease": fields["prerelease"] == "true",
                            "tag_name": fields["tag_name"], "target_commitish": fields["target_commitish"],
                            "assets": []}
            return dict(self.release)
        if endpoint.startswith("https://uploads.github.com/"):
            self.assertIn("/releases/7/assets?", endpoint)
            self.assertEqual(args[:2], ("--method", "POST"))
            self.assertIn("Content-Type: application/octet-stream", args)
            name = unquote(endpoint.split("?name=", 1)[1])
            path = pathlib.Path(args[args.index("--input") + 1])
            self.assertEqual(path.name, name)
            self.assertIn("Content-Length: " + str(path.stat().st_size), args)
            asset = {"name": name, "size": path.stat().st_size, "state": "uploaded"}
            self.release["assets"].append(asset)
            return dict(asset)
        if endpoint == "releases/7":
            if args:
                self.assertEqual(args, ("--method", "PATCH", "-F", "draft=false"))
                self.release["draft"] = False
            return dict(self.release)
        # GET /releases/tags does not return drafts.
        raise subprocess.CalledProcessError(1, ["gh", "api", endpoint])

    def publish(self, tag="v1.2.3", mode="publish"):
        publisher.publish(self.assets, self.context_repository, self.revision, tag, mode)

    def writes(self):
        return [c for c in self.api.call_args_list if "--method" in c.args]

    def test_uploads_overlap_and_inventory_waits_for_all_transfers(self):
        barrier = threading.Barrier(4, timeout=5)
        active = 0
        peak = 0
        lock = threading.Lock()
        def response(repo, endpoint, *args):
            nonlocal active, peak
            if endpoint.startswith("https://uploads."):
                with lock:
                    active += 1
                    peak = max(peak, active)
                barrier.wait()
                result = self.response(repo, endpoint, *args)
                with lock:
                    active -= 1
                return result
            if endpoint == "releases/7":
                self.assertEqual(active, 0)
                self.assertEqual(len(self.release["assets"]), 12)
            return self.response(repo, endpoint, *args)
        self.api.side_effect = response
        self.publish()
        self.assertEqual(peak, 4)
        self.assertFalse(self.release["draft"])

    def test_version_tag_publishes_complete_fixed_id(self):
        self.publish()
        self.assertFalse(self.release["draft"])
        self.assertFalse(self.release["prerelease"])
        self.assertEqual(len(self.release["assets"]), 12)
        self.assertEqual(self.api.call_args.args[1:],
                         ("releases/7", "--method", "PATCH", "-F", "draft=false"))

    def test_repository_rename_uses_current_identity_before_writes(self):
        self.context_repository = "MiniMax-AI/previous-name"
        self.publish()
        calls = self.api.call_args_list
        self.assertEqual(calls[0].args, ("MiniMax-AI/previous-name",
                                       "https://api.github.com/repos/MiniMax-AI/previous-name"))
        self.assertTrue(all(c.args[0] == self.canonical_repository for c in calls[1:]))
        uploads = [c for c in calls if c.args[1].startswith("https://uploads.")]
        self.assertEqual(len(uploads), len(self.release["assets"]))
        self.assertTrue(all(c.args[1].startswith(
            "https://uploads.github.com/repos/MiniMax-AI/OpenAgentCore/releases/7/assets?name=")
            for c in uploads))
        self.assertFalse(self.release["draft"])

    def test_invalid_repository_identity_refuses_writes(self):
        for identity in (None, 7, "", "https://example.com/repo", "owner/repo?token=x",
                         "owner/repo/extra", "owner/repo#fragment"):
            with self.subTest(identity=identity):
                self.canonical_repository = identity
                self.api.reset_mock()
                with self.assertRaisesRegex(ValueError, "invalid repository identity"):
                    self.publish()
                self.assertEqual(self.writes(), [])

    def test_annotated_tag_and_prerelease(self):
        def response(repo, endpoint, *args):
            if endpoint.startswith("git/ref/"):
                return {"object": {"type": "tag", "sha": "b" * 40}}
            return self.response(repo, endpoint, *args)
        self.api.side_effect = response
        self.publish("v1.2.3-rc.1")
        self.assertTrue(self.release["prerelease"])
        self.assertEqual(sum(c.args[1] == "git/tags/" + "b" * 40 for c in self.api.call_args_list), 2)

    def test_build_metadata_is_not_prerelease(self):
        self.publish("v1.2.3+build-test")
        self.assertFalse(self.release["prerelease"])

    def test_manual_draft_does_not_publish(self):
        for suffix in ("-offline.tar.gz", "-offline.tar.gz.sha256"):
            (self.assets / (self.stem + suffix)).unlink()
        self.publish("build-" + self.revision, "draft")
        self.assertTrue(self.release["draft"])
        self.assertFalse(any(c.args[1].startswith("git/") for c in self.api.call_args_list))

    def test_existing_public_or_draft_release_is_refused(self):
        for draft in (True, False):
            with self.subTest(draft=draft):
                self.existing = [{"tag_name": "v1.2.3", "draft": draft}]
                with self.assertRaisesRegex(ValueError, "already exists"):
                    self.publish()
                self.assertEqual(self.writes(), [])

    def test_existing_release_on_later_page_is_refused(self):
        def response(repo, endpoint, *args):
            if endpoint.startswith("releases?"):
                return ([{"tag_name": "other"}] * 100 if endpoint.endswith("page=1")
                        else [{"tag_name": "v1.2.3"}])
            return self.response(repo, endpoint, *args)
        self.api.side_effect = response
        with self.assertRaisesRegex(ValueError, "already exists"):
            self.publish()
        self.assertEqual(self.writes(), [])

    def test_failed_lookup_never_creates_release(self):
        self.api.side_effect = subprocess.CalledProcessError(1, ["gh", "api"])
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.assertEqual(self.writes(), [])

    def test_wrong_tag_revision_is_refused(self):
        def response(repo, endpoint, *args):
            if endpoint.startswith("git/"):
                return {"object": {"type": "commit", "sha": "b" * 40}}
            return self.response(repo, endpoint, *args)
        self.api.side_effect = response
        with self.assertRaisesRegex(ValueError, "built source"):
            self.publish()
        self.assertEqual(self.writes(), [])

    def test_tag_moved_during_upload_keeps_draft_unpublished(self):
        def response(repo, endpoint, *args):
            if endpoint.startswith("git/") and self.release:
                return {"object": {"type": "commit", "sha": "b" * 40}}
            return self.response(repo, endpoint, *args)
        self.api.side_effect = response
        with self.assertRaisesRegex(ValueError, "built source"):
            self.publish()
        self.assertTrue(self.release["draft"])
        self.assertFalse(any("PATCH" in c.args for c in self.writes()))

    def test_upload_failure_preserves_draft_and_stops(self):
        def response(repo, endpoint, *args):
            if endpoint.startswith("https://uploads."):
                raise subprocess.CalledProcessError(1, ["gh", "api"])
            return self.response(repo, endpoint, *args)
        self.api.side_effect = response
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.assertTrue(self.release["draft"])
        uploads = [c for c in self.writes() if c.args[1].startswith("https://uploads.")]
        self.assertGreaterEqual(len(uploads), 1)
        self.assertEqual(len({c.args[1] for c in uploads}), len(uploads))
        self.images.assert_not_called()
        self.assertFalse(any("PATCH" in c.args or "DELETE" in c.args for c in self.writes()))

    def test_lost_publication_response_never_deletes_or_retries(self):
        def response(repo, endpoint, *args):
            result = self.response(repo, endpoint, *args)
            if "PATCH" in args:
                raise subprocess.CalledProcessError(1, ["gh", "api"])
            return result
        self.api.side_effect = response
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.assertFalse(self.release["draft"])
        self.assertEqual(sum("PATCH" in c.args for c in self.writes()), 1)
        self.assertFalse(any("DELETE" in c.args for c in self.writes()))

    def test_invalid_version_tag_is_refused(self):
        with self.assertRaisesRegex(ValueError, "Version tags"):
            self.publish("version1")
        self.assertEqual(self.writes(), [])

    def test_missing_offline_archive_is_refused(self):
        (self.assets / (self.stem + "-offline.tar.gz")).unlink()
        with self.assertRaises(FileNotFoundError):
            self.publish()
        self.assertEqual(self.writes(), [])

    def test_changed_archive_is_refused(self):
        (self.assets / (self.stem + ".tar.gz")).write_bytes(b"changed")
        with self.assertRaisesRegex(ValueError, "checksum"):
            self.publish()
        self.assertEqual(self.writes(), [])

    def test_empty_or_linked_payload_is_refused(self):
        for p in self.assets.iterdir():
            p.unlink()
        with self.assertRaisesRegex(ValueError, "nonempty"):
            self.publish()
        (self.assets / "link").symlink_to(__file__)
        with self.assertRaisesRegex(ValueError, "regular"):
            self.publish()
        self.assertEqual(self.writes(), [])


    def test_lost_create_response_is_not_replayed(self):
        def response(repo, endpoint, *args):
            result = self.response(repo, endpoint, *args)
            if endpoint == "releases":
                raise subprocess.CalledProcessError(1, ["gh", "api"])
            return result
        self.api.side_effect = response
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.assertTrue(self.release["draft"])
        self.assertEqual(len(self.writes()), 1)

    def test_incomplete_remote_inventory_blocks_publication(self):
        def response(repo, endpoint, *args):
            result = self.response(repo, endpoint, *args)
            if endpoint == "releases/7" and not args:
                result["assets"] = result["assets"][:-1]
            return result
        self.api.side_effect = response
        with self.assertRaisesRegex(ValueError, "inventory"):
            self.publish()
        self.assertTrue(self.release["draft"])
        self.assertFalse(any("PATCH" in c.args for c in self.writes()))

    def test_api_uses_full_upload_url_and_binary_input(self):
        with mock.patch.object(publisher.subprocess, "check_output", return_value='{"id": 7}') as command:
            url = "https://uploads.github.com/repos/MiniMax-AI/OpenAgentCore/releases/7/assets?name=x"
            self.assertEqual(REAL_API("MiniMax-AI/OpenAgentCore", url, "--method", "POST",
                                       "--input", "/tmp/asset"), {"id": 7})
            self.assertEqual(command.call_args.args[0],
                             ["gh", "api", url, "--method", "POST", "--input", "/tmp/asset"])


class RegistryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.assets = pathlib.Path(self.temp.name)
        self.revision = "a" * 40
        self.config = "sha256:" + "b" * 64
        self.digest = "sha256:" + "c" * 64
        runtime = self.assets / "runtime.tar.gz"
        runtime.write_bytes(gzip.compress(b"runtime"))
        self.manifest = {
            "source_commit": self.revision, "platform": "linux/amd64",
            "images": dict.fromkeys(publisher.IMAGE_NAMES, self.config),
            "image_manifest_digests": dict.fromkeys(publisher.IMAGE_NAMES, self.digest),
            "artifacts": {"images/runtime.tar.gz": {
                "filename": runtime.name, "sha256": publisher.distribution.sha256(runtime)}}}
        with tarfile.open(self.assets / ("oac-" + self.revision + "-linux-amd64.tar.gz"), "w:gz") as archive:
            for name, data in [("manifest.json", json.dumps(self.manifest).encode())] + [
                    ("images/" + name + ".tar", b"image") for name in ("core", "web", "ingress")]:
                member = tarfile.TarInfo("oac-" + self.revision + "-linux-amd64/" + name)
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        self.identities = stack.enter_context(mock.patch.object(publisher.distribution, "image_identities", return_value=(self.config, self.digest)))
        stack.enter_context(mock.patch.object(publisher.distribution, "resolve_image", return_value=self.digest))
        self.run = stack.enter_context(mock.patch.object(publisher.subprocess, "run"))
        stack.enter_context(mock.patch.object(publisher.subprocess, "check_output", return_value=json.dumps({"Descriptor": {"digest": self.digest}})))
        self.remote = stack.enter_context(mock.patch.object(publisher, "registry_manifest", return_value={"config": {"digest": self.config}}))

    def publish(self, tag="v1.2.3"):
        return publisher.publish_images(self.assets, "MiniMax-AI/OpenAgentCore", self.revision, tag)

    def pushes(self):
        return [c.args[0] for c in self.run.call_args_list if c.args[0][1] == "push"]

    def test_matching_tags_are_reused_and_receipts_use_registry_digest(self):
        result = self.publish()
        self.assertEqual(self.pushes(), [])
        self.assertEqual(result["core"]["digest"], "ghcr.io/minimax-ai/openagentcore/core@" + self.digest)

    def test_new_images_use_resolved_store_identity_and_version_only(self):
        self.remote.side_effect = [None] * 4 + [{"config": {"digest": self.config}}] * 4
        result = self.publish("v1.2.3-rc.1+build.2")
        self.assertEqual(len(self.pushes()), 4)
        self.assertTrue(all(c[-1].endswith(":v1.2.3-rc.1_build.2") for c in self.pushes()))
        tags = [c.args[0] for c in self.run.call_args_list if c.args[0][1] == "tag"]
        self.assertTrue(all(c[2] == self.digest for c in tags))
        self.assertEqual(len(result), 4)

    def test_single_platform_indexes_are_verified_by_child_config(self):
        index = {"manifests": [{"digest": self.digest}]}
        image = {"config": {"digest": self.config}}
        self.remote.side_effect = lambda reference: image if "@" in reference else index
        result = self.publish()
        self.assertEqual(self.pushes(), [])
        self.assertEqual(result["core"]["digest"], "ghcr.io/minimax-ai/openagentcore/core@" + self.digest)
        self.assertTrue(any("@" in call.args[0] for call in self.remote.call_args_list))

    def test_multi_image_index_is_rejected(self):
        self.remote.return_value = {"manifests": [{"digest": self.digest}] * 2}
        with self.assertRaisesRegex(ValueError, "Expected one"):
            self.publish()
        self.assertEqual(self.pushes(), [])

    def test_conflicting_tag_prevents_all_pushes(self):
        self.remote.side_effect = [None, {"config": {"digest": "different"}}]
        with self.assertRaisesRegex(ValueError, "different image"):
            self.publish()
        self.assertEqual(self.pushes(), [])

    def test_corrupt_runtime_prevents_loading(self):
        (self.assets / "runtime.tar.gz").write_bytes(b"corrupt")
        with self.assertRaisesRegex(ValueError, "checksum"):
            self.publish()
        self.run.assert_not_called()

    def test_archive_identity_mismatch_prevents_loading(self):
        self.identities.return_value = (self.config, "different")
        with self.assertRaisesRegex(ValueError, "identity mismatch"):
            self.publish()
        self.run.assert_not_called()

    def test_failed_push_stops_publication(self):
        self.remote.return_value = None
        def run(command, **kwargs):
            if command[1] == "push":
                raise subprocess.CalledProcessError(1, command)
        self.run.side_effect = run
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.assertGreaterEqual(len(self.pushes()), 1)
        self.assertEqual(len({command[-1] for command in self.pushes()}), len(self.pushes()))

    def test_registry_pushes_overlap_after_all_preflight_checks(self):
        barrier = threading.Barrier(4, timeout=5)
        pushed = set()
        lock = threading.Lock()
        def remote(reference):
            with lock:
                return {"config": {"digest": self.config}} if reference in pushed else None
        def run(command, **kwargs):
            if command[1] == "push":
                self.assertEqual(sum(c.args[0][1] == "load" for c in self.run.call_args_list), 4)
                barrier.wait()
                with lock:
                    pushed.add(command[-1])
        self.remote.side_effect = remote
        self.run.side_effect = run
        self.assertEqual(len(self.publish()), 4)
        self.assertEqual(len(pushed), 4)

    def test_registry_auth_failure_is_not_missing_image(self):
        # Test the actual inspection function separately from the publication fixture.
        with mock.patch.object(publisher.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "unauthorized")):
            with self.assertRaisesRegex(RuntimeError, "Cannot inspect"):
                REAL_REGISTRY_MANIFEST("ghcr.io/example/core:v1")

    def test_registry_missing_manifest(self):
        with mock.patch.object(publisher.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "manifest unknown")):
            self.assertIsNone(REAL_REGISTRY_MANIFEST("ghcr.io/example/core:v1"))


if __name__ == "__main__":
    unittest.main()
