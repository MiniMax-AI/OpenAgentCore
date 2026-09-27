"""Regression checks for offline distribution identity and archive integrity."""

import gzip
import hashlib
import importlib.util
import io
import json
import pathlib
import tarfile
import tempfile
import unittest
from unittest import mock
import zipfile


spec = importlib.util.spec_from_file_location("distribution", pathlib.Path(__file__).with_name("core-distribution-manifest.py"))
distribution = importlib.util.module_from_spec(spec)
spec.loader.exec_module(distribution)


REVISION = "a" * 40
TREE = "b" * 40
RELEASE_BASE = "https://example.com/releases/" + REVISION


def image_archive(path, name, *, nested=False, architecture="amd64", corrupt=None, multiple=False):
    blobs = {}

    def descriptor(data, media_type, label):
        raw = json.dumps(data).encode() if isinstance(data, dict) else data
        digest = hashlib.sha256(raw).hexdigest()
        blobs["blobs/sha256/" + digest] = raw if corrupt != label else b"!" + raw[1:]
        return {"digest": "sha256:" + digest, "size": len(raw), "mediaType": media_type}

    config = descriptor({"os": "linux", "architecture": architecture, "image": name},
                        "application/vnd.oci.image.config.v1+json", "config")
    layer = descriptor(b"layer:" + name.encode(), "application/vnd.oci.image.layer.v1.tar", "layer")
    image = descriptor({"schemaVersion": 2, "config": config, "layers": [layer]},
                       "application/vnd.oci.image.manifest.v1+json", "manifest")
    if nested:
        image = descriptor({"schemaVersion": 2, "manifests": [image]},
                           "application/vnd.oci.image.index.v1+json", "index")
    blobs["index.json"] = json.dumps({"schemaVersion": 2, "manifests": [image] * (2 if multiple else 1)}).encode()
    with tarfile.open(path, "w") as archive:
        for filename, raw in blobs.items():
            entry = tarfile.TarInfo(filename)
            entry.size = len(raw)
            archive.addfile(entry, io.BytesIO(raw))
    return config["digest"], image["digest"]


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
        self.inspection = {"digest": "sha256:" + "a" * 64, "architecture": "amd64", "os": "linux"}
        self.write_inspection()
        for logical in distribution.ARTIFACTS:
            path = self.bundle / logical.replace("images/runtime.tar.gz", "images/runtime.tar")
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"payload:" + logical.encode())
            if logical.startswith("native/"):
                path.chmod(0o555)
        self.identities = {}
        for name in ("core", "web", "runtime", "database"):
            self.identities[name] = image_archive(self.bundle / "images" / (name + ".tar"), name)
            (self.stage / (name + ".id")).write_text(self.identities[name][0] + "\n")
        self.runtime_bytes = (self.bundle / "images/runtime.tar").read_bytes()

    def manifest(self, base=RELEASE_BASE, offline="0"):
        distribution.manifest(self.bundle, self.stage, REVISION, TREE, base, offline)

    def write_inspection(self):
        (self.stage / "runtime-inspect.json").write_text(json.dumps(self.inspection))

    def test_oci_manifest_identity_is_distinct_from_docker_config_identity(self):
        self.manifest()
        metadata = json.loads((self.bundle / "manifest.json").read_text())
        self.assertEqual(metadata["runtime_ref"], "parsar-core-runtime@sha256:" + "a" * 64)
        self.assertEqual(metadata["images"]["runtime"], self.identities["runtime"][0])
        self.assertEqual(metadata["image_manifest_digests"]["runtime"], self.identities["runtime"][1])
        self.assertEqual(metadata["microsandbox"]["runtime_sha256"], hashlib.sha256(b"runtime").hexdigest())
        for line in (self.bundle / "SHA256SUMS").read_text().splitlines():
            digest, name = line.split("  ", 1)
            self.assertEqual(digest, distribution.sha256(self.bundle / name))

    def test_built_image_records_the_id_each_docker_store_resolves(self):
        config, manifest, other = ("sha256:" + digit * 64 for digit in "123")
        metadata = self.stage / "build.json"
        both = {"containerimage.config.digest": config, "containerimage.digest": manifest}
        # Classic stores resolve the config digest, containerd stores the manifest digest. A digest
        # resolving to another image, or metadata without a config digest, identifies nothing.
        cases = ((dict(both, **{"containerimage.digest": config}), {config: config}, config),
                 (both, {manifest: manifest}, manifest),
                 (both, {config: other}, "does not identify"),
                 ({"containerimage.digest": manifest}, {manifest: manifest}, "lacks valid image digests"))
        for build, store, expected in cases:
            with self.subTest(build=build, store=store):
                metadata.write_text(json.dumps(build))
                inspect = lambda command, **_: mock.Mock(returncode=0 if command[-1] in store else 1,
                                                         stdout=store.get(command[-1], "") + "\n")
                with mock.patch.object(distribution.subprocess, "run", side_effect=inspect), \
                        mock.patch.object(distribution, "verify_image") as verify, \
                        mock.patch("builtins.print") as output:
                    if expected.startswith("sha256:"):
                        distribution.built_image(metadata)
                        verify.assert_called_once_with(expected)
                        output.assert_called_once_with(expected)
                    else:
                        with self.assertRaisesRegex(ValueError, expected):
                            distribution.built_image(metadata)
                        verify.assert_not_called()

    def test_containerd_build_ids_still_publish_archive_config_ids(self):
        for name, identity in self.identities.items():
            (self.stage / (name + ".id")).write_text(identity[1])
        self.manifest()
        metadata = json.loads((self.bundle / "manifest.json").read_text())
        self.assertEqual(metadata["images"], {name: identity[0] for name, identity in self.identities.items()})
        self.assertEqual(metadata["image_manifest_digests"], {name: identity[1] for name, identity in self.identities.items()})

    def test_nested_single_platform_index_retains_its_containerd_identity(self):
        path = self.stage / "nested.tar"
        config, index = image_archive(path, "nested", nested=True)
        self.assertEqual(distribution.image_identities(path, index), (config, index))
        self.assertEqual(distribution.image_identities(path, config), (config, index))

    def test_archive_identity_rejects_unrelated_build_or_ambiguous_platform(self):
        path = self.stage / "bad-image.tar"
        for options, expected in (({}, "selected build"), ({"multiple": True}, "exactly one image"),
                                  ({"architecture": "arm64"}, "unexpected platform")):
            with self.subTest(options=options):
                image_archive(path, "bad", **options)
                with self.assertRaisesRegex(ValueError, expected):
                    distribution.image_identities(path, "sha256:" + "f" * 64)

    def test_archive_identity_verifies_manifest_config_and_layer_bytes(self):
        path = self.stage / "corrupt.tar"
        for corrupt in ("manifest", "config", "layer", "index"):
            with self.subTest(corrupt=corrupt):
                config, _ = image_archive(path, "corrupt", nested=True, corrupt=corrupt)
                with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                    distribution.image_identities(path, config)

    def test_missing_manifest_digest_does_not_fall_back_to_config_id(self):
        self.inspection.pop("digest")
        self.inspection["config"] = {"digest": "sha256:" + "3" * 64}
        self.write_inspection()
        with self.assertRaisesRegex(ValueError, "manifest digest"):
            self.manifest()

    def test_wrong_guest_platform_rejected(self):
        self.inspection["architecture"] = "arm64"
        self.write_inspection()
        with self.assertRaisesRegex(ValueError, "platform"):
            self.manifest()

    def test_archive_reproducible_and_installer_executable(self):
        native = self.bundle / "native/bin/oac-core"
        native.parent.mkdir(parents=True, exist_ok=True)
        native.write_bytes(b"native executable")
        native.chmod(0o555)
        self.manifest()
        distribution.archive(self.bundle, "1700000000")
        archive = self.bundle.with_name(self.bundle.name + ".tar.gz")
        first = archive.read_bytes()
        distribution.archive(self.bundle, "1700000000")
        self.assertEqual(first, archive.read_bytes())
        self.assertEqual(archive.with_name(archive.name + ".sha256").read_text(), distribution.sha256(archive) + "  " + archive.name + "\n")
        with tarfile.open(archive) as contents:
            self.assertEqual(contents.getmember(self.bundle.name + "/install.sh").mode, 0o755)
            self.assertEqual(contents.getmember(self.bundle.name + "/manifest.json").mode, 0o644)
            self.assertEqual(contents.getmember(self.bundle.name + "/native/bin/oac-core").mode, 0o555)

    def test_bad_upstream_checksum_does_not_extract(self):
        archive = self.stage / "untrusted.tar.gz"
        archive.write_bytes(b"not the pinned release")
        destination = self.stage / "extracted"
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            distribution.extract_runtime(archive, destination)
        self.assertFalse(destination.exists())

    def test_thin_archive_and_detached_payload_share_one_manifest(self):
        self.manifest()
        raw_manifest = (self.bundle / "manifest.json").read_bytes()
        metadata = json.loads(raw_manifest)
        self.assertEqual(metadata["artifact_base_url"], RELEASE_BASE)
        self.assertEqual(set(metadata["artifacts"]), set(distribution.ARTIFACTS))
        for logical, artifact in metadata["artifacts"].items():
            path = self.stage / "artifacts" / artifact["filename"]
            self.assertEqual((self.bundle / logical).exists(), logical == "runtime/seccomp.json")
            self.assertTrue(artifact["filename"].startswith("parsar-core-" + REVISION + "-linux-amd64-"))
            self.assertEqual(artifact["size"], path.stat().st_size)
            self.assertEqual(artifact["sha256"], distribution.sha256(path))
        runtime = self.stage / "artifacts" / metadata["artifacts"]["images/runtime.tar.gz"]["filename"]
        self.assertEqual(gzip.decompress(runtime.read_bytes()), self.runtime_bytes)
        runtime_entry = metadata["artifacts"]["images/runtime.tar.gz"]
        self.assertEqual(runtime_entry["unpacked_size"], len(self.runtime_bytes))
        self.assertEqual(runtime_entry["unpacked_sha256"], hashlib.sha256(self.runtime_bytes).hexdigest())
        self.assertFalse((self.bundle / "images/runtime.tar").exists())
        distribution.archive(self.bundle, "1700000000")
        thin = self.bundle.with_name(self.bundle.name + ".tar.gz")
        with tarfile.open(thin) as contents:
            self.assertFalse(any("/artifacts/" in entry.name or "/native/" in entry.name for entry in contents))
        (self.stage / "artifacts").rename(self.bundle / "artifacts")
        distribution.archive(self.bundle, "1700000000", "offline")
        offline = self.bundle.with_name(self.bundle.name + "-offline.tar.gz")
        with tarfile.open(offline) as contents:
            self.assertEqual(contents.extractfile(self.bundle.name + "/manifest.json").read(), raw_manifest)
            for artifact in metadata["artifacts"].values():
                self.assertIsNotNone(contents.getmember(self.bundle.name + "/artifacts/" + artifact["filename"]))
        distribution.checksums(self.bundle)
        self.assertNotIn("artifacts/", (self.bundle / "SHA256SUMS").read_text())

    def test_release_location_and_explicit_offline_requirements(self):
        for invalid in ("http://example.com/release/v1", "https://example.com/", "https://example.com/latest",
                        "https://user:secret@example.com/v1", "https://example.com/v1?token=secret"):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                distribution.release_base(invalid)
        with self.assertRaisesRegex(ValueError, "explicit offline"):
            self.manifest(base="")
        self.manifest(base="", offline="1")
        self.assertEqual(json.loads((self.bundle / "manifest.json").read_text())["artifact_base_url"], "")

    def test_bootstraps_include_shared_downloader_and_are_reproducible(self):
        for name in ("node_install.py", "self_hosted_install.py", *distribution.OAC_CLI_MODULES):
            (self.bundle / name).write_text("# " + name + "\n")
        distribution.bootstraps(self.bundle, "1700000000")
        first = [(self.bundle / name).read_bytes() for name in ("node-install.pyz", "parsar.pyz")]
        distribution.bootstraps(self.bundle, "1700000000")
        self.assertEqual(first, [(self.bundle / name).read_bytes() for name in ("node-install.pyz", "parsar.pyz")])
        self.assertTrue(first[1].startswith(b"#!/usr/bin/env python3\n"))
        with zipfile.ZipFile(self.bundle / "parsar.pyz") as contents:
            self.assertEqual(set(contents.namelist()), {"__main__.py", *distribution.OAC_CLI_MODULES})
        for script, filename in (("node_install.py", "node-install.pyz"),
                                 ("self_hosted_install.py", "self-hosted-install.pyz")):
            with zipfile.ZipFile(self.bundle / filename) as contents:
                self.assertEqual(set(contents.namelist()), {"__main__.py", "distribution.py"} | ({"node_spec.py"} if script == "node_install.py" else set()))
                self.assertEqual(contents.read("__main__.py"), (self.bundle / script).read_bytes())


class BundledDocsTests(unittest.TestCase):
    NAMES = ("README.md", "docs/install.md")

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.source = pathlib.Path(self.temporary.name) / "source"
        self.bundle = pathlib.Path(self.temporary.name) / "bundle"
        for name, text in {
            "README.md": "# Title\n\n![Banner](docs/banner.png) ![Chart](docs/chart.png)\n"
                         "[Install](docs/install.md#sign-in-to-web), [API](contracts/api.md#routes), [web](docs/web), "
                         "`[kept](missing.md)`, [`schema.json`](contracts/schema.json), "
                         "[![Chart](docs/chart.png)](contracts/api.md), "
                         "[main](https://github.com/MiniMax-AI/parsar-core/blob/main/LICENSE)\n"
                         "```sh\n[not a link](missing.md)\n```\n\n    [indented code](missing.md)\n",
            "docs/install.md": "# Install\n## Sign in to Web\n## C#\n## _Emphasis_ and snake_case\n"
                               "[Back](../README.md#title) [Here](#sign-in-to-web) [C](#c) [E](#emphasis-and-snake_case)\n"
                               "- A list item\n\n    [continued](#install)\n",
            "docs/banner.png": "png",
            "docs/chart.png": "png",
            "docs/web/README.md": "# Web\n",
            "contracts/api.md": "# API\n## Routes\n",
            "contracts/schema.json": "{}",
        }.items():
            (self.source / name).parent.mkdir(parents=True, exist_ok=True)
            (self.source / name).write_text(text)

    def bundle_docs(self):
        distribution.docs(self.source, self.bundle, REVISION, names=self.NAMES, files=("docs/banner.png",))

    def test_links_leaving_the_bundle_point_at_the_revision(self):
        self.bundle_docs()
        versioned = "https://github.com/MiniMax-AI/parsar-core/{}/" + REVISION + "/"
        self.assertEqual((self.bundle / "README.md").read_text(), (
            "# Title\n\n![Banner](docs/banner.png) ![Chart](" + versioned.format("raw") + "docs/chart.png)\n"
            "[Install](docs/install.md#sign-in-to-web), [API](" + versioned.format("blob") + "contracts/api.md#routes), "
            "[web](" + versioned.format("tree") + "docs/web), `[kept](missing.md)`, "
            "[`schema.json`](" + versioned.format("blob") + "contracts/schema.json), "
            "[![Chart](" + versioned.format("raw") + "docs/chart.png)](" + versioned.format("blob") + "contracts/api.md), "
            "[main](" + versioned.format("blob") + "LICENSE)\n```sh\n[not a link](missing.md)\n```\n\n"
            "    [indented code](missing.md)\n"))
        self.assertEqual((self.bundle / "docs/install.md").read_text(), (self.source / "docs/install.md").read_text())
        self.assertEqual((self.bundle / "docs/banner.png").read_text(), "png")
        self.assertFalse((self.bundle / "contracts").exists())

    def test_broken_links_and_anchors_fail_the_build(self):
        for text in ("[x](missing.md)", "[x](docs/install.md#no-such-heading)", "[x](../outside.md)", "[x](#nowhere)",
                     "[`code text`](missing.md)", "See [`a`](docs/install.md#gone) and more",
                     "[![inner](missing.png)](docs/install.md)", "A [link that\nspans lines](docs/install.md)"):
            with self.subTest(text=text):
                (self.source / "README.md").write_text("# Title\n" + text + "\n")
                with self.assertRaises(ValueError):
                    self.bundle_docs()

    def test_the_bundle_check_reads_code_span_links(self):
        self.bundle_docs()
        (self.bundle / "README.md").write_text("# Title\n[`schema.json`](contracts/schema.json)\n")
        with self.assertRaises(ValueError):
            distribution.check_docs(self.bundle, self.NAMES, ("docs/banner.png",))

    def test_repository_docs_are_self_consistent(self):
        repository = pathlib.Path(__file__).resolve().parent.parent
        distribution.docs(repository, self.bundle, REVISION)
        self.assertEqual({str(path.relative_to(self.bundle)) for path in self.bundle.rglob("*.md")},
                         set(distribution.BUNDLED_DOCS))
        for name in distribution.BUNDLED_FILES:
            self.assertTrue((self.bundle / name).is_file())
        for name in distribution.BUNDLED_DOCS:
            self.assertNotIn("@SOURCE_REVISION@", (self.bundle / name).read_text())
            self.assertNotIn("/blob/main/", (self.bundle / name).read_text())


if __name__ == "__main__":
    unittest.main()
