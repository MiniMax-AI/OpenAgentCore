"""Publication failure tests using the documented GitHub REST response shapes."""
import contextlib
import hashlib
import importlib.util
import json
import io
import tarfile
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock
from urllib.parse import unquote

spec = importlib.util.spec_from_file_location(
    "publisher", pathlib.Path(__file__).with_name("publish-core-release.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)
REAL_API = publisher.api


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
        self.canonical_repository = "MiniMax-AI/parsar-core"
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        self.api = stack.enter_context(mock.patch.object(publisher, "api", side_effect=self.response))

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
        if endpoint == "https://api.github.com/repos/MiniMax-AI/parsar-core":
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
        publisher.publish(self.assets, "MiniMax-AI/parsar-core", self.revision, tag, mode)

    def writes(self):
        return [c for c in self.api.call_args_list if "--method" in c.args]

    def test_version_tag_publishes_complete_fixed_id(self):
        self.publish()
        self.assertFalse(self.release["draft"])
        self.assertFalse(self.release["prerelease"])
        self.assertEqual(len(self.release["assets"]), 12)
        self.assertEqual(self.api.call_args.args[1:],
                         ("releases/7", "--method", "PATCH", "-F", "draft=false"))

    def test_repository_rename_uses_current_identity_before_writes(self):
        self.canonical_repository = "MiniMax-AI/OpenAgentCore"
        self.publish()
        calls = self.api.call_args_list
        self.assertEqual(calls[0].args, ("MiniMax-AI/parsar-core",
                                       "https://api.github.com/repos/MiniMax-AI/parsar-core"))
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
        self.assertEqual(len(self.writes()), 2)
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
            url = "https://uploads.github.com/repos/MiniMax-AI/parsar-core/releases/7/assets?name=x"
            self.assertEqual(REAL_API("MiniMax-AI/parsar-core", url, "--method", "POST",
                                       "--input", "/tmp/asset"), {"id": 7})
            self.assertEqual(command.call_args.args[0],
                             ["gh", "api", url, "--method", "POST", "--input", "/tmp/asset"])


if __name__ == "__main__":
    unittest.main()
