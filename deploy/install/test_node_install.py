"""Exercise node installation without running providers or changing user services."""
import argparse
import hashlib
import gzip
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
import urllib.error
from unittest import mock

import node_install as installer


class NodeInstallTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".parsar/tests/node-install"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.home = Path(temporary.name).resolve()
        self.args = argparse.Namespace(source_url="https://console.example", core_url="https://172.29.144.1:24443",
                                       provider="docker", installation_id="94be54a1-138c-4f30-bc87-b13686272dbe")
        self.root = self.home / ".parsar/nodes" / self.args.installation_id
        self.manifest = {"platform": "linux/amd64", "source_commit": "a" * 40, "images": {"runtime": "sha256:" + "b" * 64},
                         "image_manifest_digests": {"runtime": "sha256:" + "c" * 64},
                         "runtime_ref": "parsar-core-runtime@sha256:" + "c" * 64,
                         "microsandbox": {"runtime_sha256": "d" * 64, "firmware_sha256": "e" * 64}}
        self.payloads = {name: b"fixture-payload-" + name.encode() for name in installer.COMMON + installer.MICRO}
        self.payloads["images/runtime.tar.gz"] = gzip.compress(b"runtime archive")
        self.refresh_manifest()
        self.containerd = False
        self.invalid_image = None
        self.image_present = False
        self.calls = []
        self.fail_service = False
        self.fail_registration = False
        for patch in (mock.patch.object(installer.Path, "home", return_value=self.home),
                      mock.patch.object(installer, "preflight"),
                      mock.patch.object(installer, "wait_ready"),
                      mock.patch.object(installer.distribution.urllib.request, "build_opener", return_value=mock.Mock(open=self.artifact_response)),
                      mock.patch.object(installer, "micro_home", return_value=self.home / "m"),
                      mock.patch.object(installer, "fetch", side_effect=lambda source, name: io.BytesIO(self.payloads[name])),
                      mock.patch.object(installer, "checked", side_effect=self.checked),
                      mock.patch.object(installer.distribution, "docker_command", side_effect=self.docker_command),
                      mock.patch.object(installer.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, b"statically linked", b""))):
            patch.start()
            self.addCleanup(patch.stop)

    def artifact_response(self, url, **kwargs):
        for name, item in self.manifest["artifacts"].items():
            if url.endswith("/" + item["filename"]):
                return io.BytesIO(self.payloads[name])
        raise AssertionError("Unexpected artifact URL: " + url)

    def refresh_manifest(self):
        self.manifest["artifact_base_url"] = "https://release.example/immutable"
        self.manifest["artifacts"] = {name: {"filename": name.replace("/", "-") + "-" + self.manifest["source_commit"],
                                             "size": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}
                                      for name, raw in self.payloads.items() if name.startswith("native/") or name == "images/runtime.tar.gz"}
        self.manifest["artifacts"]["images/runtime.tar.gz"].update(unpacked_sha256=hashlib.sha256(b"runtime archive").hexdigest(), unpacked_size=len(b"runtime archive"))
        self.payloads["manifest.json"] = json.dumps(self.manifest).encode()
        self.payloads["SHA256SUMS"] = "".join(hashlib.sha256(raw).hexdigest() + "  " + name + "\n"
                                               for name, raw in self.payloads.items() if name != "SHA256SUMS").encode()

    def checked(self, arguments, failure, **kwargs):
        self.calls.append((arguments, kwargs))
        self.assertNotIn("synthetic-once-token", str(arguments))
        self.assertNotIn("PARSAR_NODE_ENROLLMENT_TOKEN", os.environ)
        if "register" in arguments:
            secret = Path(arguments[arguments.index("--enrollment-token-file") + 1])
            self.assertEqual(secret.read_text(), "synthetic-once-token")
            self.assertEqual(stat.S_IMODE(secret.stat().st_mode), 0o600)
            if self.fail_registration:
                raise installer.InstallError(failure)
        if "enable" in arguments and self.fail_service:
            raise installer.InstallError(failure)
        if "load" in arguments:
            self.image_present = True
        if "inspect" in arguments:
            if not self.image_present:
                raise installer.InstallError(failure)
            if arguments[0] == "docker":
                if self.containerd and arguments[arguments.index("inspect") + 1] == self.manifest["images"]["runtime"]:
                    raise installer.InstallError(failure)
                identity = self.manifest["image_manifest_digests" if self.containerd else "images"]["runtime"]
                return self.invalid_image or identity + " linux/amd64"
            return json.dumps({"digest": self.manifest["runtime_ref"].split("@", 1)[1], "os": "linux", "architecture": "amd64"})
        return ""

    def docker_command(self, arguments, **kwargs):
        try:
            return subprocess.CompletedProcess(arguments, 0, self.checked(arguments, "image missing", **kwargs), "")
        except installer.InstallError:
            return subprocess.CompletedProcess(arguments, 1, "", "")

    def install(self):
        installer.install(self.args, "synthetic-once-token")

    def test_docker_installs_matched_payload_registers_and_starts_persistent_service(self):
        self.install()
        config = json.loads((self.root / "provider.json").read_text())
        self.assertEqual(config["docker"]["image"], self.manifest["images"]["runtime"])
        self.assertEqual(config["core_url"], self.args.core_url + "/api/v1")
        self.assertEqual(config["installation_id"], self.args.installation_id)
        self.assertFalse((self.root / installer.MICRO[0]).exists())
        unit = (self.root / ("parsar-node-" + self.args.installation_id + ".service")).read_text()
        self.assertIn(" run --config ", unit)
        self.assertIn("KillMode=process", unit)
        self.assertNotIn("synthetic-once-token", unit)
        self.assertEqual(list(self.root.glob(".enrollment-*")), [])
        self.assertTrue(any("register" in call for call, _ in self.calls))
        self.assertTrue(any("is-active" in call for call, _ in self.calls))

    def test_containerd_node_persists_actual_id_and_warm_retry_avoids_archive(self):
        self.containerd = True
        self.install()
        expected = self.manifest['image_manifest_digests']['runtime']
        self.assertEqual(json.loads((self.root / 'provider.json').read_text())['docker']['image'], expected)
        before = (self.root / 'provider.json').read_bytes()
        with mock.patch.object(installer.distribution, 'runtime_archive', side_effect=AssertionError('warm Runtime download')):
            self.install()
        self.assertEqual((self.root / 'provider.json').read_bytes(), before)

    def test_retained_provider_image_cannot_bypass_verified_selection(self):
        self.install()
        config = json.loads((self.root / 'provider.json').read_text())
        config['docker']['image'] = 'sha256:' + 'f' * 64
        (self.root / 'provider.json').write_text(json.dumps(config))
        self.calls.clear()
        with self.assertRaisesRegex(installer.InstallError, 'Retained Docker image differs'):
            self.install()
        self.assertFalse(any('register' in call or 'enable' in call for call, _ in self.calls))

    def test_wrong_loaded_runtime_cannot_register_or_write_provider_config(self):
        for observed in ('sha256:' + 'f' * 64 + ' linux/amd64', self.manifest['images']['runtime'] + ' linux/arm64'):
            self.invalid_image = observed
            self.image_present = False
            with self.subTest(observed=observed), self.assertRaisesRegex(installer.distribution.DistributionError, 'identity or platform'):
                self.install()
            self.assertFalse((self.root / 'provider.json').exists())
            self.assertFalse(any('register' in call or 'enable' in call for call, _ in self.calls))

    def test_microsandbox_imports_image_and_allows_only_explicit_private_core_endpoint(self):
        self.args.provider = "microsandbox"
        self.install()
        config = json.loads((self.root / "provider.json").read_text())["microsandbox"]
        self.assertEqual(config["runtime_sha256"], self.manifest["microsandbox"]["runtime_sha256"])
        self.assertEqual(config["idle_seconds"], 300)
        self.assertEqual(config["retention_seconds"], 86400)
        rules = config["network"]["rules"]
        self.assertIn({"action": "allow", "direction": "egress", "destination": "172.29.144.1", "protocol": "tcp", "port": "24443"}, rules)
        self.assertNotIn("private", [rule["destination"] for rule in rules])
        self.assertTrue(any("--tag" in call and self.manifest["runtime_ref"] in call for call, _ in self.calls))

    def test_repeat_preserves_registration_and_recovers_service_start_failure(self):
        self.fail_service = True
        with self.assertRaisesRegex(installer.InstallError, "Cannot start"):
            self.install()
        saved = (self.root / "registered.json").read_bytes()
        self.calls.clear()
        self.fail_service = False
        self.install()
        self.assertEqual((self.root / "registered.json").read_bytes(), saved)
        self.assertFalse(any("register" in call for call, _ in self.calls))
        self.assertFalse(any("load" in call for call, _ in self.calls))

    def test_unconfirmed_registration_retains_state_removes_token_and_does_not_start(self):
        self.fail_registration = True
        with self.assertRaisesRegex(installer.InstallError, "not confirmed"):
            self.install()
        self.assertTrue((self.root / "provider.json").is_file())
        self.assertFalse((self.root / "registered.json").exists())
        self.assertEqual(list(self.root.glob(".enrollment-*")), [])
        self.assertFalse(any("enable" in call for call, _ in self.calls))

    def test_microsandbox_registration_retry_retains_original_dns_policy(self):
        self.args.provider = "microsandbox"
        self.fail_registration = True
        with self.assertRaises(installer.InstallError):
            self.install()
        original = (self.root / "provider.json").read_bytes()
        self.fail_registration = False
        with mock.patch.object(installer.socket, "getaddrinfo", side_effect=OSError("DNS unavailable")):
            self.install()
        self.assertEqual((self.root / "provider.json").read_bytes(), original)
        self.assertTrue((self.root / "registered.json").exists())

    def test_different_commit_provider_or_core_cannot_overwrite_retained_installation(self):
        self.install()
        original = (self.root / "provider.json").read_bytes()
        for field, value in (("provider", "microsandbox"), ("core_url", "https://other.example")):
            before = getattr(self.args, field)
            setattr(self.args, field, value)
            with self.assertRaisesRegex(installer.InstallError, "configuration differs"):
                self.install()
            setattr(self.args, field, before)
        self.manifest["source_commit"] = "f" * 40
        self.refresh_manifest()
        with self.assertRaisesRegex(installer.InstallError, "configuration differs"):
            self.install()
        self.assertEqual((self.root / "provider.json").read_bytes(), original)

    def test_corrupt_manifest_and_payload_fail_before_provider_use(self):
        self.payloads["manifest.json"] += b" "
        with self.assertRaisesRegex(installer.InstallError, "manifest checksum"):
            self.install()
        self.refresh_manifest()
        self.payloads[installer.COMMON[0]] += b"corrupt"
        with self.assertRaisesRegex(installer.distribution.DistributionError, "published size|checksum"):
            self.install()
        self.assertFalse(self.calls)
        self.assertFalse((self.root / installer.COMMON[0]).exists())

    def test_installed_payload_and_config_are_not_overwritten(self):
        self.install()
        target = self.root / installer.COMMON[0]
        target.write_bytes(b"existing-different-payload")
        with self.assertRaisesRegex(installer.distribution.DistributionError, "Cached artifact differs"):
            self.install()
        self.assertEqual(target.read_bytes(), b"existing-different-payload")

    def test_warm_image_skips_archive_download_and_import_before_registration(self):
        for provider in ("docker", "microsandbox"):
            with self.subTest(provider=provider):
                self.args.provider = provider
                self.image_present = True
                with mock.patch.object(installer.distribution, "runtime_archive") as archive:
                    installer.prepare_runtime(self.root, self.args, self.manifest)
                archive.assert_not_called()
        self.assertFalse(any("load" in call for call, _ in self.calls))

    def test_retry_after_unconfirmed_enrollment_preserves_imported_image(self):
        self.args.provider = "microsandbox"
        self.fail_registration = True
        with self.assertRaises(installer.InstallError):
            self.install()
        self.calls.clear()
        self.fail_registration = False
        self.install()
        self.assertFalse(any("load" in call for call, _ in self.calls))

    def test_registered_node_reimports_deleted_image_without_reenrollment(self):
        self.install()
        self.image_present = False
        self.calls.clear()
        self.install()
        self.assertTrue(any("load" in call for call, _ in self.calls))
        self.assertFalse(any("register" in call for call, _ in self.calls))

    def test_symlink_installation_is_rejected(self):
        self.root.parent.mkdir(parents=True)
        elsewhere = self.home / "elsewhere"
        elsewhere.mkdir()
        self.root.symlink_to(elsewhere, target_is_directory=True)
        with self.assertRaisesRegex(installer.InstallError, "symlinks"):
            self.install()
        self.assertEqual(list(elsewhere.iterdir()), [])

    def test_main_removes_token_environment_before_any_subprocess(self):
        with mock.patch.dict(os.environ, {"PARSAR_NODE_ENROLLMENT_TOKEN": "synthetic-once-token"}):
            installer.main(["--source-url", self.args.source_url, "--core-url", self.args.core_url,
                            "--provider", "docker", "--installation-id", self.args.installation_id])
            self.assertNotIn("PARSAR_NODE_ENROLLMENT_TOKEN", os.environ)

    def test_origin_rejects_remote_http_credentials_paths_and_redirects(self):
        for value in ("http://private.example", "https://user@core.example", "https://@core.example", "https://core.example/v1", "https://core.example?", "https://core.example#", "https://core.example\\path", "https://core.example:bad", ""):
            with self.subTest(value=value), self.assertRaises(argparse.ArgumentTypeError):
                installer.origin(value)
        self.assertEqual(installer.origin("http://[::1]:8091/"), "http://[::1]:8091")
        with self.assertRaisesRegex(installer.InstallError, "redirects"):
            installer.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other.example")


class NodePrerequisiteTests(unittest.TestCase):
    def test_preflight_rejects_missing_linger_or_kvm_before_downloads(self):
        with mock.patch.object(installer.platform, "system", return_value="Linux"), \
             mock.patch.object(installer.platform, "machine", return_value="x86_64"), \
             mock.patch.object(installer.os, "getuid", return_value=1000), \
             mock.patch.object(installer, "checked", return_value="no"), \
             mock.patch.object(installer, "fetch") as fetch:
            with self.assertRaisesRegex(installer.InstallError, "lingering"):
                installer.preflight("docker")
            fetch.assert_not_called()
            with mock.patch.object(installer, "checked", return_value="yes"), mock.patch.object(installer.os, "access", return_value=False):
                with self.assertRaisesRegex(installer.InstallError, "/dev/kvm"):
                    installer.preflight("microsandbox")

    def test_microsandbox_short_home_is_stable_and_rejects_long_user_home(self):
        with mock.patch.object(installer.Path, "home", return_value=Path("/home/node")):
            first = installer.micro_home("94be54a1-138c-4f30-bc87-b13686272dbe")
            self.assertEqual(first, installer.micro_home("94be54a1-138c-4f30-bc87-b13686272dbe"))
            self.assertLessEqual(len(os.fsencode(first)), 48)
        with mock.patch.object(installer.Path, "home", return_value=Path("/home/" + "long" * 20)):
            with self.assertRaisesRegex(installer.InstallError, "HOME is too long"):
                installer.micro_home("94be54a1-138c-4f30-bc87-b13686272dbe")


if __name__ == "__main__":
    unittest.main()
