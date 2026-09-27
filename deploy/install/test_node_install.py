"""Exercise node installation without running providers or changing user services."""
import argparse
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
        self.root = self.home / ".parsar/nodes" / self.args.installation_id
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
                      mock.patch.object(installer, "fetch", side_effect=lambda source, name: io.BytesIO(self.payloads[name])),
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
        self.assertTrue(url.startswith(self.args.source_url + "/node-install/artifacts/"), url)
        for name, item in self.manifest["artifacts"].items():
            if url.endswith("/" + item["filename"]):
                return Response(self.payloads[name])
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
            self.account = SimpleNamespace(pw_name="parsar-node", pw_uid=os.getuid(), pw_gid=os.getgid(),
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
        self.root = service_home / ".parsar/nodes" / self.args.installation_id

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
                      mock.patch.object(installer.grp, "getgrnam", side_effect=lambda name: SimpleNamespace(gr_mem=["parsar-node"] if self.joined else [])),
                      mock.patch.object(installer.grp, "getgrgid", side_effect=lambda gid: SimpleNamespace(gr_name=self.device_group))):
            patch.start()
            self.addCleanup(patch.stop)
        return system

    def test_sudo_mode_prepares_the_host_and_reruns_without_changes(self):
        system = self.sudo_host()
        installer.install_system(self.args, "synthetic-once-token")
        commands = [call for call, _ in self.calls]
        self.assertIn(["useradd", "--system", "--user-group", "--no-create-home", "--home-dir", str(self.home / "service"),
                       "--shell", next(path for path in ("/usr/sbin/nologin", "/sbin/nologin", "/bin/false") if os.path.exists(path) or path == "/bin/false"),
                       "parsar-node"], commands)
        self.assertTrue(any(call[:3] == ["usermod", "--append", "--groups"] for call in commands))
        self.assertIn(["systemctl", "enable", "--now", "parsar-node-" + self.args.installation_id + ".service"], commands)
        unit = (system / "units" / ("parsar-node-" + self.args.installation_id + ".service")).read_text()
        for line in ("User=parsar-node", "After=network-online.target docker.service", "WantedBy=multi-user.target",
                     "RestartPreventExitStatus=78", "StartLimitIntervalSec=0"):
            self.assertIn(line, unit)
        self.assertNotIn("synthetic-once-token", unit)
        self.assertNotIn("Group=", unit)
        # Every step that touches the service user's files runs as that user.
        self.assertEqual(self.service_steps, ["prepare_service_node", "wait_ready"])
        self.assertTrue((self.root / "registered.json").exists())
        self.assertFalse((self.root / ("parsar-node-" + self.args.installation_id + ".service")).exists())
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

    def test_no_sudo_installs_can_share_the_host_lock_root_created(self):
        locks = self.home / "run"
        locks.mkdir()
        previous = os.umask(0o077)  # As the Web command sets it.
        self.addCleanup(os.umask, previous)
        with mock.patch.object(installer, "SYSTEM_LOCKS", locks), mock.patch.object(installer.os, "geteuid", return_value=0):
            with installer.host_lock():
                pass
        self.assertEqual(stat.S_IMODE((locks / "parsar-node.lock").stat().st_mode), 0o644)

    def test_sudo_mode_refusals_change_nothing(self):
        foreign = SimpleNamespace(pw_name="parsar-node", pw_uid=4242, pw_gid=4242, pw_dir="/home/parsar-node", pw_shell="/bin/bash")
        for case, message in (("selinux", "SELinux is enforcing"), ("docker", "Docker Engine is not installed"),
                              ("account", "not created or adopted by this installer"), ("group", "belongs to the group disk"),
                              ("home", "does not belong to the parsar-node account")):
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
        self.assertIn(["systemctl", "disable", "--now", "parsar-node-" + self.args.installation_id + ".service"], commands)
        self.assertIn(["userdel", "parsar-node"], commands)
        self.assertIn(["systemctl", "reset-failed", "parsar-node-" + self.args.installation_id + ".service"], commands)
        self.assertFalse(any(call[-2:] == ["rm", "--force"] or "prune" in call or ("image" in call and "rm" in call) for call in commands))
        self.assertFalse(self.root.exists())
        self.assertFalse((system / "units" / ("parsar-node-" + self.args.installation_id + ".service")).exists())
        self.assertFalse((system / "etc").exists())

    def test_uninstall_leaves_an_adopted_account_as_found(self):
        system = self.sudo_host()
        (self.home / "service").mkdir()
        os.chmod(self.home / "service", 0o755)
        self.account = SimpleNamespace(pw_name="parsar-node", pw_uid=os.getuid(), pw_gid=os.getgid(),
                                       pw_dir=str(self.home / "service"), pw_shell="/usr/sbin/nologin")
        installer.install_system(self.args, "synthetic-once-token")
        self.assertFalse([call for call, _ in self.calls if call[:1] == ["useradd"]])
        self.assertEqual(json.loads((system / "etc/account.json").read_text())["created"], False)
        self.calls.clear()
        with mock.patch.object(installer, "open_request", side_effect=urllib.error.HTTPError("https://core.example", 401, "", {}, None)):
            installer.uninstall_system(SimpleNamespace(installation_id=self.args.installation_id, force=False))
        commands = [call for call, _ in self.calls]
        self.assertNotIn(["userdel", "parsar-node"], commands)
        self.assertIn(["gpasswd", "--delete", "parsar-node", "docker"], commands)
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
        account = SimpleNamespace(pw_name="parsar-node", pw_uid=990, pw_gid=990, pw_dir=service, pw_shell="/usr/sbin/nologin")
        with mock.patch.object(installer.os, "getgrouplist", return_value=[990]):
            self.assertTrue(installer.ours(account))
            self.assertFalse(installer.ours(SimpleNamespace(**dict(vars(account), pw_uid=0))))
            self.assertFalse(installer.ours(SimpleNamespace(**dict(vars(account), pw_gid=0))))
        with mock.patch.object(installer.os, "getgrouplist", return_value=[990, 27]):
            self.assertFalse(installer.ours(account))

    def service_step(self, function, output=None, errors=None):
        """Run function through the real fork of as_service_user, as this test's user."""
        account = SimpleNamespace(pw_name="parsar-node", pw_uid=os.getuid(), pw_gid=os.getgid(),
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
        with open(self.home / "parsar-node.lock") as lock:  # The child held it too; nothing does now.
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
        (self.home / "service/.parsar").rename(victim)
        (self.home / "service/.parsar").symlink_to(victim)
        with mock.patch.object(installer, "open_request", side_effect=urllib.error.HTTPError("https://core.example", 401, "", {}, None)), \
                self.assertRaisesRegex(installer.InstallError, "symbolic link.*Nothing was changed"):
            installer.uninstall_system(SimpleNamespace(installation_id=self.args.installation_id, force=True))
        self.assertTrue((victim / "nodes" / self.args.installation_id / "registered.json").exists())
        self.assertTrue((system / "units" / ("parsar-node-" + self.args.installation_id + ".service")).exists())

    def test_token_comes_on_standard_input_only(self):
        arguments = ["--source-url", self.args.source_url, "--core-url", self.args.core_url, "--installation-id",
                     self.args.installation_id, "--enrollment-token-stdin"]
        with mock.patch.object(installer, "install") as install, mock.patch.object(installer.sys, "stdin", io.StringIO("synthetic-once-token\n")), \
                mock.patch.object(installer.os, "geteuid", return_value=1000):
            installer.main(arguments)
        self.assertEqual(install.call_args.args[1], "synthetic-once-token")

    def test_the_retired_token_variable_is_refused_in_every_mode(self):
        install = ["--source-url", self.args.source_url, "--core-url", self.args.core_url,
                   "--installation-id", self.args.installation_id, "--enrollment-token-stdin"]
        for euid in (1000, 0):
            for arguments in (install, install[:-1], ["--uninstall", "--installation-id", self.args.installation_id]):
                errors = io.StringIO()
                with mock.patch.dict(os.environ, {"PARSAR_NODE_ENROLLMENT_TOKEN": "synthetic-once-token"}), \
                        mock.patch.object(installer.os, "geteuid", return_value=euid), mock.patch.object(installer.sys, "stderr", errors), \
                        mock.patch.multiple(installer, install=mock.DEFAULT, install_system=mock.DEFAULT, uninstall_user=mock.DEFAULT,
                                            uninstall_system=mock.DEFAULT) as steps, self.assertRaises(SystemExit):
                    installer.main(arguments)
                self.assertEqual(errors.getvalue().splitlines(), ["PARSAR_NODE_ENROLLMENT_TOKEN is retired: pass the enrollment "
                                                                  "token on standard input with --enrollment-token-stdin."])
                self.assertFalse(any(step.called for step in steps.values()))

    def test_user_manager_bus_is_found_without_a_login_session(self):
        runtime = self.home / "run-user"
        (runtime / str(os.getuid())).mkdir(parents=True)
        with mock.patch.object(installer, "USER_RUNTIME", runtime), mock.patch.dict(os.environ, {}, clear=True):
            with self.assertRaisesRegex(installer.InstallError, "enable-linger"):
                installer.user_bus()
            bus = socket.socket(socket.AF_UNIX)
            self.addCleanup(bus.close)
            bus.bind(str(runtime / str(os.getuid()) / "bus"))
            installer.user_bus()
            self.assertEqual(os.environ["DBUS_SESSION_BUS_ADDRESS"], "unix:path=" + str(runtime / str(os.getuid()) / "bus"))

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
