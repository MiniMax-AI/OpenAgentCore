"""Exercise node installation without running providers or changing host services."""
import argparse
import contextlib
import copy
import fcntl
import hashlib
import gzip
import io
import json
import os
from pathlib import Path
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.error
from types import SimpleNamespace
from unittest import mock

import node_install as installer
import node_spec



def ended(pid, wait=5):
    """The process is gone or a zombie waiting for its new parent to reap it."""
    deadline = time.monotonic() + wait
    while time.monotonic() < deadline:
        try:
            if Path("/proc/%d/stat" % pid).read_text().rsplit(")", 1)[1].split()[0] == "Z":
                return True
        except (FileNotFoundError, ProcessLookupError):
            return True
        time.sleep(0.05)
    return False

class Response(io.BytesIO):
    """An HTTP response body with the status and headers the downloader reads."""
    status, headers = 200, {}


class NodeInstallTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".oac/tests/node-install"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.home = Path(temporary.name).resolve()
        self.args = argparse.Namespace(source_url="https://console.example", core_url="https://172.29.144.1:24443",
                                       provider="docker", installation_id="94be54a1-138c-4f30-bc87-b13686272dbe")
        self.root = self.home / ".oac/nodes" / self.args.installation_id
        self.manifest = {"platform": "linux/amd64", "source_commit": "a" * 40, "images": {"runtime": "sha256:" + "b" * 64},
                         "image_manifest_digests": {"runtime": "sha256:" + "c" * 64},
                         "runtime_ref": "oac-runtime@sha256:" + "c" * 64,
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
        self.register_stderr = None
        for patch in (mock.patch.object(installer.Path, "home", return_value=self.home),
                      mock.patch.object(installer, "preflight"),
                      mock.patch.object(installer, "wait_ready"),
                      mock.patch.object(installer, "open_request", side_effect=self.configuration_response),
                      mock.patch.object(installer.distribution.urllib.request, "build_opener", return_value=mock.Mock(open=self.artifact_response)),
                      mock.patch.object(installer, "micro_home", return_value=self.home / "m"),
                      mock.patch.object(installer, "fetch", side_effect=lambda source, name: io.BytesIO(self.payloads[name.split("/", 2)[2] if name.startswith("releases/") else name])),
                      mock.patch.object(installer, "checked", side_effect=self.checked),
                      mock.patch.object(installer.distribution, "docker_command", side_effect=self.docker_command),
                      mock.patch.object(installer.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, b"statically linked", b""))):
            patch.start()
            self.addCleanup(patch.stop)

    def configuration_response(self, request, **kwargs):
        resources = {"cpus": 3, "memory_mib": 6144}
        if self.args.provider == "microsandbox":
            resources.update(root_disk_mib=10240, environment_disk_mib=12288)
        spec = {"resources": resources, "runtime": node_spec.release(self.manifest)}
        configuration = {"installation_id": self.args.installation_id, "provider": self.args.provider,
                         "core_url": self.args.core_url, "generation": 1, "specification": spec, "max_active": 2, "max_retained": 8,
                         "specification_digest": node_spec.digest(self.args.provider, spec)}
        return io.BytesIO(json.dumps(configuration).encode())

    def artifact_response(self, request, **kwargs):
        url = getattr(request, "full_url", request)
        # The fixture manifest records a release URL; nodes still download from their console.
        self.assertTrue(url.startswith(self.args.source_url + "/node-install/releases/" + self.manifest["source_commit"] + "/artifacts/"), url)
        for name, item in self.manifest["artifacts"].items():
            if url.endswith("/" + item["filename"]):
                return Response(self.payloads[name])
        raise AssertionError("Unexpected artifact URL: " + url)

    def refresh_manifest(self):
        self.manifest["artifact_base_url"] = "https://release.example/immutable"
        self.manifest["artifacts"] = {name: {"filename": name.replace("/", "-") + "-" + self.manifest["source_commit"],
                                             "size": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}
                                      for name, raw in self.payloads.items() if name.startswith("native/") or name == "images/runtime.tar.gz"}
        self.manifest["microsandbox"] = {"runtime_sha256": hashlib.sha256(self.payloads[installer.MICRO[1]]).hexdigest(),
                                         "firmware_sha256": hashlib.sha256(self.payloads[installer.MICRO[2]]).hexdigest()}
        self.manifest["artifacts"]["images/runtime.tar.gz"].update(unpacked_sha256=hashlib.sha256(b"runtime archive").hexdigest(), unpacked_size=len(b"runtime archive"))
        self.payloads["manifest.json"] = json.dumps(self.manifest).encode()
        self.payloads["SHA256SUMS"] = "".join(hashlib.sha256(raw).hexdigest() + "  " + name + "\n"
                                               for name, raw in self.payloads.items() if name != "SHA256SUMS").encode()

    def checked(self, arguments, failure, **kwargs):
        self.calls.append((arguments, kwargs))
        self.assertNotIn("synthetic-once-token", str(arguments))
        if "register" in arguments:
            self.assertNotIn("--max-active", arguments)
            self.assertNotIn("--max-retained", arguments)
            secret = Path(arguments[arguments.index("--enrollment-token-file") + 1])
            self.assertEqual(secret.read_text(), "synthetic-once-token")
            self.assertEqual(stat.S_IMODE(secret.stat().st_mode), 0o600)
            identity = {"node_id": "634d97be-e54d-40f0-9468-ae6b62be85bf", "installation_id": self.args.installation_id,
                        "provider": self.args.provider, "deployment_generation": 1,
                        "specification_digest": node_spec.digest(self.args.provider, json.loads((self.root / "provider.json").read_text())["specification"])}
            path = self.root / "state/node/identity.json"
            path.write_text(json.dumps({"identity": identity, "credential": "a" * 64, "core_url": self.args.core_url}))
            path.chmod(0o600)
            if self.register_stderr:
                raise kwargs["explain"](self.register_stderr)
            if self.fail_registration:
                raise installer.InstallError(failure)
        if "enable" in arguments and self.fail_service:
            raise installer.InstallError(failure)
        if arguments[:1] == ["useradd"]:
            self.account = SimpleNamespace(pw_name="oac-node", pw_uid=os.getuid(), pw_gid=os.getgid(),
                                           pw_dir=str(installer.SERVICE_HOME), pw_shell="/usr/sbin/nologin")
        if arguments[:1] == ["usermod"]:
            if getattr(self, "fail_usermod", False):
                raise installer.InstallError(failure)
            self.joined = True
        if "{{json .}}" in arguments:
            return json.dumps({"MemoryLimit": True, "CpuCfsQuota": True, "NCPU": 8, "MemTotal": 16 << 30})
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
        installer.prepare_service_node(self.args, "synthetic-once-token", installer.node_generations.helper_archive(self.args, installer))

    def install_current_program_with_retained_runtime(self, provider):
        self.args.provider = provider
        old_source, new_source = self.manifest["source_commit"], "f" * 40
        program = copy.deepcopy(self.manifest)
        program["source_commit"] = new_source
        payloads = dict(self.payloads)
        payloads[installer.COMMON[0]] = b"new protocol program from current release"
        for name, artifact in program["artifacts"].items():
            artifact["filename"] = artifact["filename"].replace(old_source, new_source)
            artifact["size"] = len(payloads[name])
            artifact["sha256"] = hashlib.sha256(payloads[name]).hexdigest()
        payloads["manifest.json"] = json.dumps(program).encode()
        payloads["SHA256SUMS"] = "".join(hashlib.sha256(raw).hexdigest() + "  " + name + "\n"
                                               for name, raw in payloads.items() if name != "SHA256SUMS").encode()
        fetched = []
        def fetch(_source, name):
            fetched.append(name)
            if name.startswith("releases/" + old_source + "/"):
                return io.BytesIO(self.payloads[name.split("/", 2)[2]])
            self.assertIn(name, ("manifest.json", "SHA256SUMS"))
            return io.BytesIO(payloads[name])
        def artifact_response(request, **_kwargs):
            url = getattr(request, "full_url", request)
            for manifest, files in ((program, payloads), (self.manifest, self.payloads)):
                prefix = self.args.source_url + "/node-install/releases/" + manifest["source_commit"] + "/artifacts/"
                for name, item in manifest["artifacts"].items():
                    if url == prefix + item["filename"]:
                        # Only the node executable comes from the host release.
                        self.assertEqual(manifest["source_commit"], new_source if name == installer.COMMON[0] else old_source)
                        return Response(files[name])
            raise AssertionError("Unexpected immutable artifact URL " + url)
        with mock.patch.object(installer, "fetch", side_effect=fetch), mock.patch.object(installer.distribution.urllib.request, "build_opener", return_value=mock.Mock(open=artifact_response)):
            self.install()
        self.assertEqual((self.root / installer.COMMON[0]).read_bytes(), payloads[installer.COMMON[0]])
        config = json.loads((self.root / "provider.json").read_text())
        self.assertEqual(config["specification"]["runtime"], node_spec.release(self.manifest))
        self.assertEqual(json.loads((self.root / "registered.json").read_text())["source_commit"], old_source)
        self.assertIn("releases/" + old_source + "/runtime/seccomp.json", fetched)
        if provider == "microsandbox":
            for name in installer.MICRO:
                self.assertEqual((self.root / name).read_bytes(), self.payloads[name])

    def test_current_program_pairs_with_core_selected_retained_docker_runtime(self):
        self.install_current_program_with_retained_runtime("docker")

    def test_current_program_pairs_with_core_selected_retained_micro_runtime(self):
        self.install_current_program_with_retained_runtime("microsandbox")

    def test_bundle_without_selected_runtime_refuses_before_payload_or_registration(self):
        program = copy.deepcopy(self.manifest)
        program["source_commit"] = "f" * 40
        self.args.bundle = self.home / "bundle"
        self.args.bundle.mkdir()
        with mock.patch.object(installer, "metadata", return_value=(program, {})):
            with self.assertRaisesRegex(installer.InstallError, "does not contain Core's selected Runtime"):
                self.install()
        self.assertFalse((self.root / "installation.json").exists())
        self.assertFalse((self.root / installer.COMMON[0]).exists())
        self.assertFalse(any("register" in command or "load" in command or "enable" in command for command, _ in self.calls))

    def test_missing_retained_runtime_never_falls_back_to_current_runtime(self):
        program = copy.deepcopy(self.manifest)
        program["source_commit"] = "f" * 40
        with mock.patch.object(installer, "metadata", side_effect=[(program, {}), installer.InstallError("Retained release unavailable")]) as metadata:
            with self.assertRaisesRegex(installer.InstallError, "Retained release unavailable"):
                self.install()
        self.assertEqual(metadata.call_args_list[1].kwargs, {"prefix": "releases/" + self.manifest["source_commit"] + "/"})
        self.assertFalse((self.root / "installation.json").exists())
        self.assertFalse(any("register" in command or "load" in command or "enable" in command for command, _ in self.calls))

    def recovery_args(self):
        self.args.generation = 1
        self.args.specification_digest = json.loads((self.root / "state/node/identity.json").read_text())["identity"]["specification_digest"]
        return self.args

    def prepare_successor_runtime(self):
        self.manifest["source_commit"] = "f" * 40
        self.refresh_manifest()
        config = json.loads(self.configuration_response(None).read())
        config["generation"] = 2
        self.args.generation = 2
        self.args.specification_digest = config["specification_digest"]
        with mock.patch.object(installer.node_spec, "fetch", return_value=config):
            installer.node_generations.prepare(self.args, installer)
        return installer.private_json(self.root / "state/node/generations/2.json")

    def test_original_runtime_gc_interruption_preserves_restart_identity_and_successor(self):
        self.args.provider = "microsandbox"
        self.install()
        successor = self.prepare_successor_runtime()
        keep = (installer.COMMON[0], "generation-preparer.pyz", "provider.json", "registered.json",
                "installation.json", "preparation.json", "runtime-artifacts.json", "state/node/identity.json")
        before = {name: (self.root / name).read_bytes() for name in keep}
        original = installer.private_json(self.root / "provider.json")
        self.args.generation = 1
        self.args.specification_digest = node_spec.digest("microsandbox", original["specification"])
        real_unlink = Path.unlink
        def interrupted(path, *args, **kwargs):
            if path == self.root / installer.MICRO[1]:
                raise OSError("interrupted original Runtime cleanup")
            return real_unlink(path, *args, **kwargs)
        with mock.patch.object(Path, "unlink", new=interrupted):
            with self.assertRaises(OSError): installer.node_generations.collect(self.args, installer)
        self.assertFalse((self.root / installer.MICRO[0]).exists())
        self.assertTrue((self.root / installer.MICRO[1]).exists())
        self.assertTrue(installer.private_json(self.root / "state/node/generations/1.collecting")["native_complete"])
        self.assertEqual(installer.private_json(self.root / "provider.json"), original)
        self.assertEqual(installer.node_generations.retained_configs(self.root, installer)[2], successor)
        count = len(self.calls)
        installer.node_generations.collect(self.args, installer)
        self.assertEqual(len(self.calls), count)
        for name in ("runtime/seccomp.json", "images/runtime.tar.gz", "images/runtime.tar") + installer.MICRO:
            self.assertFalse((self.root / name).exists(), name)
        for name, raw in before.items(): self.assertEqual((self.root / name).read_bytes(), raw, name)
        self.assertEqual(installer.node_generations.retained_configs(self.root, installer), {2: successor})
        self.assertTrue(installer.node_generations.image_available(successor, installer))

    def test_original_runtime_gc_refuses_changed_sibling_before_deleting_any_file(self):
        self.args.provider = "microsandbox"
        self.install()
        self.prepare_successor_runtime()
        original = installer.private_json(self.root / "provider.json")
        self.args.generation = 1
        self.args.specification_digest = node_spec.digest("microsandbox", original["specification"])
        firmware = self.root / installer.MICRO[2]
        firmware.write_bytes(b"changed private file")
        before = {name: (self.root / name).read_bytes() for name in ("runtime/seccomp.json",) + installer.MICRO}
        count = len(self.calls)
        with self.assertRaisesRegex(installer.InstallError, "refusing deletion"):
            installer.node_generations.collect(self.args, installer)
        self.assertEqual(len(self.calls), count)
        for name, raw in before.items(): self.assertEqual((self.root / name).read_bytes(), raw, name)

    def test_original_runtime_gc_missing_ownership_refuses_and_preserves_unknown_files(self):
        self.install()
        self.prepare_successor_runtime()
        (self.root / "runtime-artifacts.json").unlink()
        unknown = self.root / "native/bin/operator-owned-file"
        unknown.write_bytes(b"unowned")
        self.args.generation = 1
        original = installer.private_json(self.root / "provider.json")
        self.args.specification_digest = node_spec.digest("docker", original["specification"])
        seccomp = (self.root / "runtime/seccomp.json").read_bytes()
        with self.assertRaisesRegex(installer.InstallError, "ownership is missing"):
            installer.node_generations.collect(self.args, installer)
        self.assertEqual((self.root / "runtime/seccomp.json").read_bytes(), seccomp)
        self.assertEqual(unknown.read_bytes(), b"unowned")

    def test_shared_original_firmware_survives_until_its_last_generation(self):
        self.args.provider = "microsandbox"
        self.install()
        successor = self.prepare_successor_runtime()
        successor["microsandbox"]["firmware_path"] = str(self.root / installer.MICRO[2])
        installer.node_generations.atomic_json(self.root / "state/node/generations/2.json", successor)
        original = installer.private_json(self.root / "provider.json")
        self.args.generation = 1
        self.args.specification_digest = node_spec.digest("microsandbox", original["specification"])
        installer.node_generations.collect(self.args, installer)
        self.assertFalse((self.root / installer.MICRO[0]).exists())
        self.assertFalse((self.root / installer.MICRO[1]).exists())
        self.assertTrue((self.root / installer.MICRO[2]).exists())
        self.assertTrue(installer.node_generations.image_available(successor, installer))
        self.args.generation = 2
        self.args.specification_digest = node_spec.digest("microsandbox", successor["specification"])
        with installer.node_generations.collection_lease(self.root, 2, installer, installer.node_generations.marker_identity(self.args), initialize=True):
            pass
        def inventory(command, *_args, **_kwargs):
            if command[1:3] == ["image", "list"]: return ""
            if command[1:3] == ["sandbox", "list"]: return "[]"
            raise AssertionError(command)
        with mock.patch.object(installer, "checked", side_effect=inventory):
            installer.node_generations.collect(self.args, installer)
        self.assertFalse((self.root / installer.MICRO[2]).exists())
        self.assertTrue((self.root / "provider.json").exists())
        self.assertTrue((self.root / installer.COMMON[0]).exists())

    def test_original_runtime_gc_preserves_exact_shared_native_file_references(self):
        self.args.provider = "microsandbox"
        self.install()
        original = installer.private_json(self.root / "provider.json")
        other = json.loads(json.dumps(original))
        other["generation"] = 2
        directory = self.root / "state/node/generations"
        directory.mkdir(mode=0o700, exist_ok=True)
        installer.node_generations.atomic_json(directory / "2.json", other)
        before = {name: (self.root / name).read_bytes() for name in ("runtime/seccomp.json",) + installer.MICRO}
        installer.node_generations.collect(self.recovery_args(), installer)
        for name, raw in before.items(): self.assertEqual((self.root / name).read_bytes(), raw, name)
        self.assertEqual(installer.node_generations.retained_configs(self.root, installer), {2: other})

    def test_interrupted_new_generation_recovers_partial_download_at_same_identity(self):
        self.args.provider = "microsandbox"
        self.install()
        original = (self.root / "provider.json").read_bytes()
        self.manifest["source_commit"] = "f" * 40
        self.refresh_manifest()
        config = json.loads(self.configuration_response(None).read())
        config["generation"] = 2
        self.args.generation = 2
        self.args.specification_digest = config["specification_digest"]
        real_obtain = installer.distribution.obtain_artifact
        def interrupted(manifest, name, path):
            if name == installer.MICRO[1]:
                raise OSError("interrupted partial download")
            return real_obtain(manifest, name, path)
        with mock.patch.object(installer.node_spec, "fetch", return_value=config):
            with mock.patch.object(installer.distribution, "obtain_artifact", side_effect=interrupted):
                with self.assertRaises(OSError):
                    installer.node_generations.prepare(self.args, installer)
            directory = self.root / "state/node/generations"
            saved = json.loads((directory / "2.preparing").read_text())["configuration"]
            self.assertFalse((directory / "2.json").exists())
            self.assertEqual(saved["specification"], config["specification"])
            self.assertFalse(json.loads((directory / "2.preparing").read_text())["import_started"])
            helper = Path(saved["microsandbox"]["helper_path"])
            inode = helper.stat().st_ino
            self.assertFalse(Path(saved["microsandbox"]["runtime_path"]).exists())
            installer.node_generations.prepare(self.args, installer)
            self.assertEqual(helper.stat().st_ino, inode)
            self.assertEqual(json.loads((directory / "2.json").read_text()), saved)
            self.assertFalse((directory / "2.preparing").exists())
        self.assertEqual((self.root / "provider.json").read_bytes(), original)

    def test_restart_repairs_only_missing_micro_bytes_at_original_paths(self):
        self.args.provider = "microsandbox"
        self.install()
        before = (self.root / "provider.json").read_bytes()
        helper = self.root / installer.MICRO[0]
        runtime = self.root / installer.MICRO[1]
        old_inode = runtime.stat().st_ino
        helper.unlink()
        installer.node_generations.prepare(self.recovery_args(), installer)
        self.assertEqual(helper.read_bytes(), self.payloads[installer.MICRO[0]])
        self.assertEqual(runtime.stat().st_ino, old_inode)
        self.assertEqual((self.root / "provider.json").read_bytes(), before)
        self.assertEqual(json.loads((self.root / "state/node/generations/1.json").read_text()), json.loads(before))

    def test_restart_refuses_conflicting_sibling_before_repairing_missing_file(self):
        self.args.provider = "microsandbox"
        self.install()
        helper = self.root / installer.MICRO[0]
        runtime = self.root / installer.MICRO[1]
        helper.unlink()
        runtime.write_bytes(b"conflicting retained runtime")
        with self.assertRaisesRegex(installer.InstallError, "artifact checksum differs"):
            installer.node_generations.prepare(self.recovery_args(), installer)
        self.assertFalse(helper.exists())
        self.assertEqual(runtime.read_bytes(), b"conflicting retained runtime")

    def test_live_helper_or_collection_excludes_restart_repair(self):
        self.args.provider = "microsandbox"
        self.install()
        helper = self.root / installer.MICRO[0]
        helper.unlink()
        directory = self.root / "state/node/generations"
        directory.mkdir(mode=0o700, exist_ok=True)
        descriptor = os.open(directory / "1.lease", os.O_CREAT | os.O_RDWR, 0o600)
        try:
            for mode in (fcntl.LOCK_SH, fcntl.LOCK_EX):
                fcntl.flock(descriptor, mode | fcntl.LOCK_NB)
                with self.assertRaisesRegex(installer.InstallError, "helper is still active"):
                    installer.node_generations.prepare(self.recovery_args(), installer)
                self.assertFalse(helper.exists())
                fcntl.flock(descriptor, fcntl.LOCK_UN)
        finally:
            os.close(descriptor)
        installer.node_generations.prepare(self.args, installer)
        self.assertEqual(helper.read_bytes(), self.payloads[installer.MICRO[0]])

    def test_docker_installs_matched_payload_registers_and_starts_persistent_service(self):
        system = self.sudo_host()
        installer.install_system(self.args, "synthetic-once-token")
        config = json.loads((self.root / "provider.json").read_text())
        self.assertEqual(config["docker"]["image"], self.manifest["images"]["runtime"])
        self.assertEqual(config["core_url"], self.args.core_url + "/api/v1")
        self.assertEqual(config["installation_id"], self.args.installation_id)
        self.assertFalse((self.root / installer.MICRO[0]).exists())
        unit = (system / "units" / ("oac-node-" + self.args.installation_id + ".service")).read_text()
        self.assertIn(" run --config ", unit)
        self.assertIn("KillMode=process", unit)
        # Keeps retrying while Core is down; stops once Core rejects the removed node's credential.
        self.assertIn("StartLimitIntervalSec=0", unit)
        self.assertIn("RestartPreventExitStatus=78", unit)
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
        with self.assertRaisesRegex(node_spec.SpecificationError, 'Retained Docker image differs'):
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
        self.assertEqual(config["cpus"], 3)
        self.assertEqual(config["memory_mib"], 6144)
        self.assertEqual(config["root_disk_mib"], 10240)
        self.assertEqual(config["environment_disk_mib"], 12288)
        for field in ("idle_seconds", "retention_seconds", "max_active", "max_retained"):
            self.assertNotIn(field, config)
        rules = config["network"]["rules"]
        self.assertIn({"action": "allow", "direction": "egress", "destination": "172.29.144.1", "protocol": "tcp", "port": "24443"}, rules)
        self.assertNotIn("private", [rule["destination"] for rule in rules])
        self.assertTrue(any("--tag" in call and self.manifest["runtime_ref"] in call for call, _ in self.calls))

    def test_repeat_preserves_registration_and_recovers_service_start_failure(self):
        system = self.sudo_host()
        self.fail_service = True
        with self.assertRaisesRegex(installer.InstallError, "Cannot start"):
            installer.install_system(self.args, "synthetic-once-token")
        saved = (self.root / "registered.json").read_bytes()
        self.calls.clear()
        self.fail_service = False
        installer.install_system(self.args, "synthetic-once-token")
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

    def test_registration_failure_names_only_cores_fixed_answer(self):
        self.assertEqual(str(installer.registration_failure(b"secret /home/path details")), installer.REGISTRATION_UNCONFIRMED)

    def test_changed_public_url_clears_unregistered_state_for_a_new_command(self):
        # Core refused the address before consuming the token, so it has no record of this node.
        self.register_stderr = b"node enrollment rejected (HTTP 409 sandbox_node_address_mismatch)\n"
        with self.assertRaisesRegex(installer.InstallError, "public URL changed.*token was not used"):
            self.install()
        for name in ("installation.json", "provider.json", "state/node/identity.json", "registered.json"):
            self.assertFalse((self.root / name).exists(), name)
        self.assertTrue((self.root / installer.COMMON[0]).is_file())
        self.register_stderr = None
        self.args.core_url = "https://core-new.example"
        self.install()
        self.assertEqual(json.loads((self.root / "registered.json").read_text())["core_url"], "https://core-new.example")

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
            with self.assertRaisesRegex((installer.InstallError, node_spec.SpecificationError), "differs"):
                self.install()
            setattr(self.args, field, before)
        self.manifest["source_commit"] = "f" * 40
        self.refresh_manifest()
        with self.assertRaisesRegex((installer.InstallError, node_spec.SpecificationError), "differs"):
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

    def sudo_host(self, enforcing=False):
        """Sudo mode against temporary system paths; the service-user step runs in-process."""
        system = self.home / "system"
        (system / "systemd").mkdir(parents=True)
        (system / "run").mkdir()
        (system / "selinux").write_text("1" if enforcing else "0")
        docker_socket = socket.socket(socket.AF_UNIX)
        docker_socket.bind(str(system / "docker.sock"))
        self.addCleanup(docker_socket.close)
        os.chmod(system / "docker.sock", 0o660)
        self.account, self.joined, self.docker_installed, self.device_group = None, False, True, "docker"
        service_home = self.home / "service"
        self.root = service_home / ".oac/nodes" / self.args.installation_id

        self.service_steps = []

        def run_as(account, function, *arguments):
            self.service_steps.append("wait_ready" if function is installer.wait_ready else function.__name__)
            with mock.patch.object(installer.Path, "home", return_value=Path(account.pw_dir)):
                function(*arguments)
        for patch in (mock.patch.multiple(installer, SERVICE_HOME=service_home, SYSTEM_RECORDS=system / "etc",
                                          SYSTEM_UNITS=system / "units", SYSTEM_LOCKS=system / "run",
                                          CHILD_DOCKER_CONFIG=system / "docker-config",
                                          SYSTEMD_RUNNING=system / "systemd", SELINUX_ENFORCE=system / "selinux",
                                          DOCKER_SOCKET=system / "docker.sock", run_as=run_as,
                                          service_account=lambda: self.account),
                      mock.patch.object(installer.shutil, "which", side_effect=lambda tool: None if tool == "docker" and not self.docker_installed else "/usr/bin/" + tool),
                      mock.patch.object(installer.grp, "getgrnam", side_effect=lambda name: SimpleNamespace(gr_mem=["oac-node"] if self.joined else [])),
                      mock.patch.object(installer.grp, "getgrgid", side_effect=lambda gid: SimpleNamespace(gr_name=self.device_group))):
            patch.start()
            self.addCleanup(patch.stop)
        return system

    def test_no_color_flag_survives_sudo_environment_reset(self):
        with mock.patch.dict(os.environ, {}, clear=True), \
                mock.patch.object(installer.os, "geteuid", return_value=0), \
                mock.patch.object(installer, "read_token", return_value="synthetic-once-token"), \
                mock.patch.object(installer, "install_system") as install:
            installer.main(["--no-color", "--source-url", "https://core.example", "--core-url", "https://core.example",
                            "--installation-id", self.args.installation_id, "--enrollment-token-stdin"])
            self.assertIn("NO_COLOR", os.environ)
            install.assert_called_once()

    def test_sudo_summary_commands_retain_permissions_of_invoking_account(self):
        for uid, prefix in (("1000", "sudo "), ("0", "")):
            with self.subTest(uid=uid), mock.patch.dict(os.environ, {"SUDO_UID": uid}):
                stream = io.StringIO()
                with contextlib.redirect_stdout(stream):
                    installer.node_output.summary(self.root, self.args, "oac-node-example.service", "oac-node")
                self.assertIn("  Status: " + prefix + "systemctl status oac-node-example.service", stream.getvalue())
                self.assertIn("  Logs: " + prefix + "journalctl -u oac-node-example.service -f", stream.getvalue())

    def test_completion_summary_follows_readiness_and_hides_enrollment_token(self):
        self.sudo_host()
        stream = io.StringIO()
        action = installer.install_system
        with contextlib.redirect_stdout(stream), mock.patch.object(installer, "wait_ready") as ready:
            ready.side_effect = installer.InstallError("fixture readiness timeout")
            with self.assertRaisesRegex(installer.InstallError, "fixture readiness timeout"):
                action(self.args, "synthetic-once-token")
            self.assertNotIn("Node installation complete.", stream.getvalue())
            ready.side_effect = None
            action(self.args, "")
        text = stream.getvalue()
        self.assertIn("\nStatus\n  Core connection: connected\n  Sandbox Provider: Docker (ready)\n", text)
        self.assertIn("\nNode\n", text)
        self.assertIn("\nManage\n", text)
        scope = ""
        self.assertIn("  Status: systemctl" + scope + " status oac-node-", text)
        self.assertIn("  Logs: journalctl" + scope + " -u oac-node-", text)
        self.assertNotIn("synthetic-once-token", text)
        self.assertNotIn("\033[", text)

    def test_sudo_mode_prepares_the_host_and_reruns_without_changes(self):
        system = self.sudo_host()
        installer.install_system(self.args, "synthetic-once-token")
        commands = [call for call, _ in self.calls]
        self.assertIn(["useradd", "--system", "--user-group", "--no-create-home", "--home-dir", str(self.home / "service"),
                       "--shell", next(path for path in ("/usr/sbin/nologin", "/sbin/nologin", "/bin/false") if os.path.exists(path) or path == "/bin/false"),
                       "oac-node"], commands)
        self.assertTrue(any(call[:3] == ["usermod", "--append", "--groups"] for call in commands))
        self.assertIn(["systemctl", "enable", "--now", "oac-node-" + self.args.installation_id + ".service"], commands)
        unit = (system / "units" / ("oac-node-" + self.args.installation_id + ".service")).read_text()
        for line in ("User=oac-node", "After=network-online.target docker.service", "WantedBy=multi-user.target",
                     "RestartPreventExitStatus=78", "StartLimitIntervalSec=0"):
            self.assertIn(line, unit)
        self.assertNotIn("synthetic-once-token", unit)
        self.assertNotIn("Group=", unit)
        # Every step that touches the service user's files runs as that user.
        self.assertEqual(self.service_steps, ["prepare_service_node", "wait_ready"])
        self.assertTrue((self.root / "registered.json").exists())
        self.assertFalse((self.root / ("oac-node-" + self.args.installation_id + ".service")).exists())
        account = json.loads((system / "etc/account.json").read_text())
        self.assertEqual((account["created"], account["groups_added"]), (True, ["docker"]))
        self.assertEqual(stat.S_IMODE((system / "etc").stat().st_mode), 0o755)
        before = {path: path.read_bytes() for path in (system / "etc").iterdir()}
        self.calls.clear()
        installer.install_system(self.args, "")
        commands = [call for call, _ in self.calls]
        self.assertFalse([call for call in commands if call[:1] in (["useradd"], ["usermod"]) or "register" in call])
        self.assertEqual({path: path.read_bytes() for path in (system / "etc").iterdir()}, before)
        # A command naming another Core address is refused before anything changes.
        self.args.core_url, self.calls[:] = "https://moved.example", []
        with self.assertRaisesRegex(installer.InstallError, "this command uses https://moved.example.*Nothing was changed"):
            installer.install_system(self.args, "")
        self.assertFalse([call for call, _ in self.calls if call[:1] in (["useradd"], ["usermod"], ["systemctl"])])
        self.assertEqual({path: path.read_bytes() for path in (system / "etc").iterdir()}, before)

    def test_system_install_captures_helper_before_entering_service_user(self):
        self.sudo_host()
        source = self.home / "private-download" / "node-install.pyz"
        source.parent.mkdir(mode=0o700)
        source.write_bytes(b"trusted executed installer snapshot")
        source.chmod(0o600)
        original_run_as = installer.run_as
        def run_as(account, function, *arguments):
            if function is installer.prepare_service_node:
                source.unlink()  # The child must not need the original path at all.
            return original_run_as(account, function, *arguments)
        with mock.patch.object(installer.sys, "argv", [str(source)]), \
                mock.patch.object(installer, "run_as", side_effect=run_as):
            installer.install_system(self.args, "synthetic-once-token")
        self.assertEqual((self.root / "generation-preparer.pyz").read_bytes(), b"trusted executed installer snapshot")
        self.assertTrue((self.root / "registered.json").is_file())

    def test_host_lock_is_private_and_exclusive(self):
        locks = self.home / "run"
        locks.mkdir()
        previous = os.umask(0o077)  # As the Web command sets it.
        self.addCleanup(os.umask, previous)
        with mock.patch.object(installer, "SYSTEM_LOCKS", locks), mock.patch.object(installer.os, "geteuid", return_value=0):
            with installer.host_lock():
                with (locks / "oac-node.lock").open() as other:
                    with self.assertRaises(BlockingIOError):
                        fcntl.flock(other, fcntl.LOCK_EX | fcntl.LOCK_NB)
        self.assertEqual(stat.S_IMODE((locks / "oac-node.lock").stat().st_mode), 0o600)

    def test_sudo_mode_refusals_change_nothing(self):
        foreign = SimpleNamespace(pw_name="oac-node", pw_uid=4242, pw_gid=4242, pw_dir="/home/oac-node", pw_shell="/bin/bash")
        for case, message in (("selinux", "SELinux is enforcing"), ("docker", "Docker Engine is not installed"),
                              ("account", "not created or adopted by this installer"), ("group", "belongs to the group disk"),
                              ("home", "does not belong to the oac-node account")):
            with self.subTest(case=case):
                system = self.sudo_host(enforcing=case == "selinux")
                self.docker_installed = case != "docker"
                self.account = foreign if case == "account" else None
                self.device_group = "disk" if case == "group" else "docker"
                if case == "home":  # a home directory left without its account
                    (self.home / "service").mkdir()
                self.calls.clear()
                with self.assertRaisesRegex(installer.InstallError, message + ".*Nothing was changed"):
                    installer.install_system(self.args, "synthetic-once-token")
                self.assertFalse([call for call, _ in self.calls if call[:1] in (["useradd"], ["usermod"], ["systemctl"])])
                self.assertFalse((system / "etc").exists())
                self.assertFalse((system / "units").exists())
                for path in sorted(system.rglob("*"), reverse=True):
                    path.unlink() if not path.is_dir() else path.rmdir()
                system.rmdir()
                if (self.home / "service").exists():
                    (self.home / "service").rmdir()

    def test_uninstall_waits_for_core_to_reject_the_node(self):
        system = self.sudo_host()
        installer.install_system(self.args, "synthetic-once-token")
        uninstall = SimpleNamespace(installation_id=self.args.installation_id, force=False)
        with mock.patch.object(installer, "open_request", return_value=io.BytesIO(b"{}")), \
                self.assertRaisesRegex(installer.InstallError, "still lists this node.*Nothing was changed"):
            installer.uninstall_system(uninstall)
        self.assertTrue((self.root / "registered.json").exists())
        rejected = urllib.error.HTTPError("https://core.example", 401, "", {}, None)
        self.calls.clear()
        with mock.patch.object(installer, "open_request", side_effect=rejected):
            installer.uninstall_system(uninstall)
        commands = [call for call, _ in self.calls]
        self.assertEqual(self.service_steps[-2:], ["confirm_removed", "remove_node_files"])
        self.assertIn(["systemctl", "disable", "--now", "oac-node-" + self.args.installation_id + ".service"], commands)
        self.assertIn(["userdel", "oac-node"], commands)
        self.assertIn(["systemctl", "reset-failed", "oac-node-" + self.args.installation_id + ".service"], commands)
        self.assertFalse(any(call[-2:] == ["rm", "--force"] or "prune" in call or ("image" in call and "rm" in call) for call in commands))
        self.assertFalse(self.root.exists())
        self.assertFalse((system / "units" / ("oac-node-" + self.args.installation_id + ".service")).exists())
        self.assertFalse((system / "etc").exists())

    def test_uninstall_leaves_an_adopted_account_as_found(self):
        system = self.sudo_host()
        (self.home / "service").mkdir()
        os.chmod(self.home / "service", 0o755)
        self.account = SimpleNamespace(pw_name="oac-node", pw_uid=os.getuid(), pw_gid=os.getgid(),
                                       pw_dir=str(self.home / "service"), pw_shell="/usr/sbin/nologin")
        installer.install_system(self.args, "synthetic-once-token")
        self.assertFalse([call for call, _ in self.calls if call[:1] == ["useradd"]])
        self.assertEqual(json.loads((system / "etc/account.json").read_text())["created"], False)
        self.calls.clear()
        with mock.patch.object(installer, "open_request", side_effect=urllib.error.HTTPError("https://core.example", 401, "", {}, None)):
            installer.uninstall_system(SimpleNamespace(installation_id=self.args.installation_id, force=False))
        commands = [call for call, _ in self.calls]
        self.assertNotIn(["userdel", "oac-node"], commands)
        self.assertIn(["gpasswd", "--delete", "oac-node", "docker"], commands)
        self.assertTrue((self.home / "service").is_dir())
        self.assertEqual(stat.S_IMODE((self.home / "service").stat().st_mode), 0o755)
        self.assertFalse((system / "etc").exists())

    def test_sudo_mode_serves_one_core_per_host(self):
        system = self.sudo_host()
        installer.install_system(self.args, "synthetic-once-token")
        self.args.installation_id, self.calls[:] = "0c6f35d2-6d7c-4a53-8d5c-3a3b1d3a0f11", []
        with self.assertRaisesRegex(installer.InstallError, "one Core per host.*Nothing was changed"):
            installer.install_system(self.args, "synthetic-once-token")
        self.assertFalse([call for call, _ in self.calls if call[:1] in (["useradd"], ["usermod"], ["systemctl"])])
        self.assertEqual(installer.node_records(), ["94be54a1-138c-4f30-bc87-b13686272dbe"])

    def test_created_account_is_recorded_before_any_later_step(self):
        system = self.sudo_host()
        self.fail_usermod = True
        with self.assertRaises(installer.InstallError):
            installer.install_system(self.args, "synthetic-once-token")
        self.assertEqual(json.loads((system / "etc/account.json").read_text())["created"], True)

    def test_adoption_refuses_root_ids_and_extra_groups(self):
        service = str(installer.SERVICE_HOME)
        account = SimpleNamespace(pw_name="oac-node", pw_uid=990, pw_gid=990, pw_dir=service, pw_shell="/usr/sbin/nologin")
        with mock.patch.object(installer.os, "getgrouplist", return_value=[990]):
            self.assertTrue(installer.ours(account))
            self.assertFalse(installer.ours(SimpleNamespace(**dict(vars(account), pw_uid=0))))
            self.assertFalse(installer.ours(SimpleNamespace(**dict(vars(account), pw_gid=0))))
        with mock.patch.object(installer.os, "getgrouplist", return_value=[990, 27]):
            self.assertFalse(installer.ours(account))

    def service_step(self, function, output=None, errors=None):
        """Run function through the real fork of as_service_user, as this test's user."""
        account = SimpleNamespace(pw_name="oac-node", pw_uid=os.getuid(), pw_gid=os.getgid(),
                                  pw_dir=str(self.home), pw_shell="/usr/sbin/nologin")
        with mock.patch.object(installer.os, "setgroups"), mock.patch.object(installer.os, "setgid"), \
                mock.patch.object(installer.os, "setuid"), mock.patch.object(installer.sys, "stdout", output or io.StringIO()), \
                mock.patch.object(installer.sys, "stderr", errors or io.StringIO()):
            installer.as_service_user(account, function)

    def test_service_user_step_has_no_terminal_and_reads_nothing(self):
        """The forked child starts its own session with /dev/null as input; its output is relayed."""
        def probe():
            try:
                open("/dev/tty").close()
                tty = "opened"
            except OSError:
                tty = "unavailable"
            print("session-leader=%s stdin=%s docker-config=%s tty=%s" % (os.getsid(0) == os.getpid(),
                  os.readlink("/proc/self/fd/0"), os.environ["DOCKER_CONFIG"], tty))
        output = io.StringIO()
        self.service_step(probe, output)
        self.assertIn("session-leader=True stdin=/dev/null docker-config=" + str(installer.CHILD_DOCKER_CONFIG) + " tty=unavailable",
                      output.getvalue())

    def test_service_user_output_reaches_the_terminal_as_plain_text(self):
        def forge():
            os.write(1, b"\x1b]52;c;ZWNobyBoaQ==\x07copied\r\x1b[2KFinish with: sudo sh\n")
            os.write(1, "caf\u00e9".encode()[:-1])  # A character split across two reads.
            time.sleep(0.2)
            os.write(1, "caf\u00e9".encode()[-1:] + b"\n")
            os.write(2, b"\x1b]0;title\x07\xc2\x9bwarning\n")
        output, errors = io.StringIO(), io.StringIO()
        self.service_step(forge, output, errors)
        self.assertEqual(output.getvalue(), "?]52;c;ZWNobyBoaQ==?copied??[2KFinish with: sudo sh\ncaf\u00e9\n")
        self.assertEqual(errors.getvalue(), "?]0;title??warning\n")

    def test_uninstall_prints_only_an_image_id_from_the_service_home(self):
        root = self.home / "node"
        root.mkdir()
        (root / "provider.json").write_text(json.dumps({"docker": {"image": "x\nFinish with: sudo sh"}}))
        output = io.StringIO()
        with mock.patch.object(installer.sys, "stdout", output):
            installer.remove_node_files(root, self.args.installation_id)
        self.assertNotIn("Finish", output.getvalue())

    def test_service_user_step_gets_its_own_session_keyring(self):
        libc = installer.ctypes.CDLL(None, use_errno=True)
        libc.syscall.restype = installer.ctypes.c_long

        def session_keyring():  # KEYCTL_GET_KEYRING_ID of KEY_SPEC_SESSION_KEYRING
            return libc.syscall(*(installer.ctypes.c_long(value) for value in (installer.KEYCTL_SYSCALL, 0, -3, 0)))
        before = session_keyring()
        if before == -1:
            self.skipTest("keyctl is unavailable here")
        output = io.StringIO()
        self.service_step(lambda: print("keyring=%d" % session_keyring()), output)
        inside = int(output.getvalue().split("keyring=")[1])
        self.assertGreater(inside, 0)
        self.assertNotEqual(inside, before)

    def test_closing_the_terminal_stops_the_step_and_everything_it_started(self):
        """The child's own session misses SIGHUP; the installer stops it and what it started, and frees the lock."""
        record = self.home / "pids"
        # As in a terminal session; a runner under nohup would otherwise ignore SIGHUP.
        self.addCleanup(signal.signal, signal.SIGHUP, signal.signal(signal.SIGHUP, signal.SIG_DFL))

        def long_step():
            # A program that ignores SIGTERM: only the SIGKILL to the child's process group ends it.
            stubborn = subprocess.Popen([sys.executable, "-c", "import signal, time; signal.signal(signal.SIGTERM, "
                                         "signal.SIG_IGN); print(flush=True); time.sleep(60)"], stdout=subprocess.PIPE)
            stubborn.stdout.readline()
            record.write_text("%d %d" % (os.getpid(), stubborn.pid))
            os.kill(os.getppid(), signal.SIGHUP)  # The administrator's terminal closes.
            time.sleep(30)
        started = time.monotonic()
        with mock.patch.object(installer, "SYSTEM_LOCKS", self.home), mock.patch.object(installer.os, "geteuid", return_value=0), \
                self.assertRaisesRegex(installer.InstallError, "interrupted"):
            with installer.host_lock():
                self.service_step(long_step)
        self.assertLess(time.monotonic() - started, 15)
        for pid in map(int, record.read_text().split()):
            self.assertTrue(ended(pid), pid)
        with open(self.home / "oac-node.lock") as lock:  # The child held it too; nothing does now.
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)

    def test_an_ignored_hangup_stays_ignored(self):
        """Under nohup a closed terminal ends neither the installer nor the step."""
        self.addCleanup(signal.signal, signal.SIGHUP, signal.signal(signal.SIGHUP, signal.SIG_IGN))

        def step():
            os.kill(os.getppid(), signal.SIGHUP)
            time.sleep(0.2)
            print("finished")
        output = io.StringIO()
        self.service_step(step, output)
        self.assertEqual(output.getvalue(), "finished\n")
        self.assertIs(signal.getsignal(signal.SIGHUP), signal.SIG_IGN)

    def test_uninstall_never_follows_a_link_in_the_service_home(self):
        # The service user owns its home; a link it plants must not steer deletion elsewhere.
        system = self.sudo_host()
        installer.install_system(self.args, "synthetic-once-token")
        victim = self.home / "victim"
        (self.home / "service/.oac").rename(victim)
        (self.home / "service/.oac").symlink_to(victim)
        with mock.patch.object(installer, "open_request", side_effect=urllib.error.HTTPError("https://core.example", 401, "", {}, None)), \
                self.assertRaisesRegex(installer.InstallError, "symbolic link.*Nothing was changed"):
            installer.uninstall_system(SimpleNamespace(installation_id=self.args.installation_id, force=True))
        self.assertTrue((victim / "nodes" / self.args.installation_id / "registered.json").exists())
        self.assertTrue((system / "units" / ("oac-node-" + self.args.installation_id + ".service")).exists())

    def test_non_root_install_and_uninstall_refuse_before_input_or_mutation(self):
        base = ["--installation-id", self.args.installation_id]
        for flags in ([], ["--uninstall"], ["--uninstall", "--force"]):
            with self.subTest(flags=flags), mock.patch.object(installer.os, "geteuid", return_value=1000), \
                    mock.patch.object(installer, "read_token") as token, \
                    mock.patch.object(installer, "install_system") as install, \
                    mock.patch.object(installer, "uninstall_system") as uninstall:
                with self.assertRaisesRegex(installer.InstallError, "require root.*sudo"):
                    installer.main(base + flags)
                token.assert_not_called()
                install.assert_not_called()
                uninstall.assert_not_called()
                self.assertFalse(self.root.exists())

    def test_retained_generation_actions_still_run_as_service_account(self):
        for action in ("prepare", "collect"):
            with self.subTest(action=action), mock.patch.object(installer.os, "geteuid", return_value=1000), \
                    mock.patch.object(installer.node_generations, action) as helper:
                installer.main(["--installation-id", self.args.installation_id, "--generation-action", action,
                                "--generation", "1", "--specification-digest", "a" * 64])
                helper.assert_called_once()

    def test_token_comes_on_standard_input_only(self):
        arguments = ["--source-url", self.args.source_url, "--core-url", self.args.core_url, "--installation-id",
                     self.args.installation_id, "--enrollment-token-stdin"]
        with mock.patch.object(installer, "install_system") as install, mock.patch.object(installer.sys, "stdin", io.StringIO("synthetic-once-token\n")), \
                mock.patch.object(installer.os, "geteuid", return_value=0):
            installer.main(arguments)
        self.assertEqual(install.call_args.args[1], "synthetic-once-token")

    def test_offline_bundle_uses_same_bootstrap_and_verified_artifacts(self):
        bundle = self.home / "bundle"
        bundle.mkdir()
        for name in ("manifest.json", "SHA256SUMS", "runtime/seccomp.json"):
            target = bundle / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(self.payloads[name])
        for name, entry in self.manifest["artifacts"].items():
            target = bundle / "artifacts" / entry["filename"]
            target.parent.mkdir(exist_ok=True)
            target.write_bytes(self.payloads[name])
        self.args.bundle = bundle
        self.args.source_url = None
        with mock.patch.object(installer, "fetch", side_effect=AssertionError("Unexpected metadata download")), \
                mock.patch.object(installer.distribution.urllib.request, "build_opener", side_effect=AssertionError("Unexpected artifact download")):
            self.install()
        self.assertEqual(json.loads((self.root / "provider.json").read_text())["specification"], self.args.configuration["specification"])

    def test_changed_local_micro_resources_cannot_reconnect(self):
        self.args.provider = "microsandbox"
        self.install()
        target = self.root / "provider.json"
        stored = json.loads(target.read_text())
        stored["microsandbox"]["cpus"] += 1
        target.write_text(json.dumps(stored))
        before = target.read_bytes()
        self.calls.clear()
        with self.assertRaisesRegex(node_spec.SpecificationError, "microsandbox configuration differs"):
            self.install()
        self.assertEqual(target.read_bytes(), before)
        self.assertFalse(any("register" in call or "enable" in call for call, _ in self.calls))

    def test_origin_rejects_remote_http_credentials_paths_and_redirects(self):
        for value in ("http://private.example", "https://user@core.example", "https://@core.example", "https://core.example/v1", "https://core.example?", "https://core.example#", "https://core.example\\path", "https://core.example:bad", ""):
            with self.subTest(value=value), self.assertRaises(argparse.ArgumentTypeError):
                installer.origin(value)
        self.assertEqual(installer.origin("http://[::1]:8091/"), "http://[::1]:8091")
        with self.assertRaisesRegex(installer.InstallError, "redirects"):
            installer.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other.example")


class NodePrerequisiteTests(unittest.TestCase):
    def test_preflight_rejects_missing_kvm_before_downloads(self):
        with mock.patch.object(installer.platform, "system", return_value="Linux"), \
                mock.patch.object(installer.platform, "machine", return_value="x86_64"), \
                mock.patch.object(installer.os, "getuid", return_value=1000), \
                mock.patch.object(installer.os, "access", return_value=False), \
                mock.patch.object(installer, "fetch") as fetch:
            with self.assertRaisesRegex(installer.InstallError, "/dev/kvm"):
                installer.preflight("microsandbox")
            fetch.assert_not_called()

    def test_microsandbox_short_home_is_stable_and_rejects_long_user_home(self):
        with mock.patch.object(installer.Path, "home", return_value=Path("/home/node")):
            first = installer.micro_home("94be54a1-138c-4f30-bc87-b13686272dbe")
            self.assertEqual(first, installer.micro_home("94be54a1-138c-4f30-bc87-b13686272dbe"))
            self.assertLessEqual(len(os.fsencode(first)), 48)
        for length in (28, 29):
            with mock.patch.object(installer.Path, "home", return_value=Path("/" + "h" * (length - 1))):
                if length == 28:
                    self.assertEqual(len(os.fsencode(installer.micro_home("94be54a1-138c-4f30-bc87-b13686272dbe"))), 48)
                else:
                    with self.assertRaisesRegex(installer.InstallError, "HOME is too long"):
                        installer.micro_home("94be54a1-138c-4f30-bc87-b13686272dbe")
        with mock.patch.object(installer.Path, "home", return_value=Path("/home/" + "long" * 20)):
            with self.assertRaisesRegex(installer.InstallError, "HOME is too long"):
                installer.micro_home("94be54a1-138c-4f30-bc87-b13686272dbe")


class UnsupportedNodeUpdateTests(unittest.TestCase):
    def test_update_refuses_without_host_operations(self):
        with self.assertRaisesRegex(installer.InstallError, "not supported;.*reinstall"):
            installer.main(["--installation-id", "00000000-0000-0000-0000-000000000001", "--update"])


if __name__ == "__main__":
    unittest.main()
