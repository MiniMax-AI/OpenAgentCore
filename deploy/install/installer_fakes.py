"""A fake Docker, systemd and HTTP host for installer tests. Nothing real is started.

Compose is modelled by its observable contract: `up` recreates a service exactly when
its resolved configuration (including env_file content) changed, and Core loads its
Core key digests when it starts.
"""
import hashlib
import json
from pathlib import Path
import subprocess
from types import SimpleNamespace
from unittest import mock

import parsar_cli


def sha256(data):
    return hashlib.sha256(data.encode() if isinstance(data, str) else data).hexdigest()


class FakeHost:
    def __init__(self, test):
        self.commands = []
        self.containers = {}  # service -> configuration hash of its container
        self.running = set()
        self.recreated = []
        self.native = {"active": False, "starts": 0, "restarts": 0, "reloads": 0, "addr": None, "digests": []}
        self.core = {"port": None, "digests": [], "fails": False, "log": ""}
        self.web_port = None
        self.native_root = None  # the installation whose native unit systemctl manages
        self.missing_images = set()
        self.core_installation_id = "11111111-2222-4333-8444-555555555555"
        self.bindings = {"nodes": 0, "nodes_on_other_address": 0, "hosted_sandboxes": 0, "self_hosted_executors": 0}
        self.nodes = []
        self.remote_core = {}  # web-only: origin -> (status, installation_id)
        self.deployment_core_url = ""  # what an old Core reports for its sandbox deployment
        for target, replacement in ((subprocess, "run"),):
            patcher = mock.patch.object(target, replacement, side_effect=self.run)
            patcher.start()
            test.addCleanup(patcher.stop)
        for name, value in (("http", self.http), ("time", SimpleNamespace(sleep=lambda seconds: None))):
            patcher = mock.patch.object(parsar_cli, name, value)
            patcher.start()
            test.addCleanup(patcher.stop)

    # Commands ---------------------------------------------------------------
    def run(self, args, check=False, **kwargs):
        args = [str(item) for item in args]
        self.commands.append(args)
        code, stdout = 0, ""
        if args[:2] == ["docker", "compose"] and args[2:3] == ["-f"]:
            code, stdout = self.compose(Path(args[3]), args[4:])
        elif args[:2] == ["docker", "compose"]:
            stdout = "2.30.0"
        elif args[:3] == ["docker", "image", "inspect"]:
            code = 1 if args[3] in self.missing_images else 0
            stdout = "" if code else args[3] + " linux/amd64"
        elif args[0] == "systemctl":
            code, stdout = self.systemctl(args[2:])
        elif args[0] == "loginctl":
            stdout = "yes"
        elif args[0] == "journalctl":
            stdout = self.core["log"]
        if check and code:
            raise subprocess.CalledProcessError(code, args)
        text = kwargs.get("text") or kwargs.get("universal_newlines")
        return subprocess.CompletedProcess(args, code, stdout if text else stdout.encode(), "" if text else b"")

    def service_hash(self, document, name):
        service = document["services"][name]
        text = json.dumps(service, sort_keys=True)
        for path in service.get("env_file", []):
            text += Path(path.replace("$$", "$")).read_text()
        return sha256(text)

    def compose(self, path, args):
        document = json.loads(path.read_text()) if path.exists() else {"services": {}}
        services = document["services"]
        if args[:1] == ["ps"]:
            rows = [{"Service": name, "State": "running" if name in self.running else "exited", "Health": ""}
                    for name in self.containers if name in services]
            return 0, json.dumps(rows)
        if args[:1] == ["stop"]:
            self.running -= set(args[1:] or services)
            return 0, ""
        if args[:1] == ["logs"]:
            return 0, self.core["log"]
        if args[:1] == ["exec"]:
            return 0, ""
        if args[:1] == ["up"]:
            targets = [item for item in args[1:] if not item.startswith("-") and not item.isdigit()]
            names = list(services) if not targets else [name for name in ("database", "migrate", "core")
                                                         if name in services]
            for name in names:
                digest = self.service_hash(document, name)
                if self.containers.get(name) != digest:
                    self.containers[name] = digest
                    self.recreated.append(name)
                    if name == "core":
                        self.load_core(path.parent.parent, services[name])
                if name != "migrate":
                    self.running.add(name)
            if "web" in names:
                web = services["web"]
                self.web_port = int(web["ports"][0].split(":")[1]) if "ports" in web else int(
                    web["environment"]["CORE_CONSOLE_ADDR"].rsplit(":", 1)[1])
            if "core" in names and self.core["fails"]:
                self.running.discard("core")
                return 1, ""
        return 0, ""

    def load_core(self, root, service):
        if "ports" in service:
            self.core["port"] = int(service["ports"][0].split(":")[1])
        digests = root / "generated/core-key-digests.json"
        self.core["digests"] = json.loads(digests.read_text()) if digests.exists() else json.loads(
            (root / "admin/core-key-digests.json").read_text())

    def systemctl(self, args):
        native = self.native
        if args[0] == "daemon-reload":
            native["reloads"] += 1
        elif args[0] in ("enable", "restart", "start"):
            native["starts" if args[0] != "restart" else "restarts"] += 1
            native["active"] = not self.core["fails"]
            if (self.native_root / "generated/core.env").exists():
                environment = (self.native_root / "generated/core.env").read_text()
                address = next(line for line in environment.splitlines() if line.startswith("AGENTS_API_ADDR="))
                native["addr"] = int(address.rsplit(":", 1)[1].rstrip('"'))
                native["digests"] = json.loads((self.native_root / "generated/core-key-digests.json").read_text())
        elif args[0] in ("stop", "disable"):
            native["active"] = False
        elif args[0] == "is-active":
            return (0 if native["active"] else 3), ""
        elif args[0] == "show":
            return 0, "252"
        return 0, ""

    # HTTP -------------------------------------------------------------------
    def core_listening(self, port):
        if "core" in self.running and self.core["port"] == port:
            return self.core["digests"]
        if self.native["active"] and self.native["addr"] == port:
            return self.native["digests"]
        return None

    def http(self, url, headers=None, timeout=5):
        headers = headers or {}
        origin, _, path = url.partition("://")[2].partition("/")
        path = "/" + path
        key = headers.get("Authorization", "").removeprefix("Bearer ")
        for remote, (status, installation) in self.remote_core.items():
            if url.startswith(remote + "/"):
                return status, json.dumps({"installation_id": installation}).encode()
        port = int(origin.rsplit(":", 1)[1])
        digests = self.core_listening(port)
        if path == "/healthz":
            if digests is not None or ("web" in self.running and self.web_port == port):
                return 200, b"{}"
            return 0, b""
        if path == "/console/auth":
            return (200, b"{}") if "web" in self.running and self.web_port == port else (0, b"")
        if digests is None:
            return 0, b""
        if sha256(key) not in digests:
            return 401, b""
        if path == "/core/v1/installation":
            return 200, json.dumps({"installation_id": self.core_installation_id,
                                    "address_bindings": self.bindings}).encode()
        if path == "/core/v1/sandbox/nodes":
            return 200, json.dumps({"data": self.nodes}).encode()
        if path == "/core/v1/sandbox/deployment":
            return 200, json.dumps({"core_url": self.deployment_core_url}).encode()
        return 404, b""


MANIFEST = {
    "source_commit": "a" * 40,
    "images": {name: "sha256:" + digit * 64 for name, digit in (
        ("core", "1"), ("runtime", "2"), ("database", "3"), ("web", "4"))},
    "image_manifest_digests": {name: "sha256:" + digit * 64 for name, digit in (
        ("core", "a"), ("runtime", "b"), ("database", "c"), ("web", "d"))},
    "runtime_ref": "parsar-core-runtime@sha256:" + "b" * 64,
    "microsandbox": {"runtime_sha256": "5" * 64, "firmware_sha256": "6" * 64},
}
MODULES = ("install.py", "configuration.py", "config_model.py", "config.schema.json", "parsar_cli.py", "convert.py",
           "native_service.py", "local_node.py", "node_spec.py", "distribution.py", "install.sh")


def write_checksums(bundle):
    files = sorted(path for path in bundle.rglob("*") if path.is_file() and path.name != "SHA256SUMS")
    (bundle / "SHA256SUMS").write_text("".join(
        sha256(path.read_bytes()) + "  " + str(path.relative_to(bundle)) + "\n" for path in files))


def make_bundle(directory, manifest, commit=None):
    """A synthetic distribution with this checkout's installer modules."""
    manifest = json.loads(json.dumps(manifest))
    if commit:
        manifest["source_commit"] = commit
    bundle = Path(directory)
    (bundle / "images").mkdir(parents=True)
    (bundle / "runtime").mkdir()
    (bundle / "runtime/seccomp.json").write_text('{"defaultAction":"SCMP_ACT_ERRNO"}')
    for name in MODULES:
        (bundle / name).write_bytes(Path(__file__).with_name(name).read_bytes())
    for name in ("node-install.pyz", "self-hosted-install.pyz"):
        (bundle / name).write_bytes(b"synthetic verified Python bootstrap")
    (bundle / "parsar.pyz").write_bytes(b"synthetic parsar command " + manifest["source_commit"].encode())
    manifest["artifacts"] = {}
    for name in ("images/runtime.tar.gz", "native/bin/parsar-sandbox-node",
                 "native/bin/agents-api-microsandbox-provider", "native/microsandbox/msb",
                 "native/microsandbox/libkrunfw.so.5.6.1"):
        manifest["artifacts"][name] = {"filename": "parsar-" + manifest["source_commit"] + "-" + name.replace("/", "-"),
                                       "sha256": "a" * 64, "size": 1}
    (bundle / "manifest.json").write_text(json.dumps(manifest))
    for name in manifest["images"]:
        (bundle / "images" / (name + ".tar")).write_bytes(("synthetic " + name).encode())
    for name in ("bin/agents-api", "bin/agents-api-migrate", "bin/agents-api-microsandbox-provider",
                 "bin/parsar-sandbox-node", "microsandbox/msb", "microsandbox/libkrunfw.so.5.6.1",
                 "e2b/agents-api-e2b-provider"):
        path = bundle / "native" / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(b"\x7fELFsynthetic native file " + manifest["source_commit"].encode())
    write_checksums(bundle)
    return bundle, manifest


def run_installer(install, bundle, argv):
    """install.main from the bundle on a Linux amd64 host."""
    with mock.patch.object(install, "__file__", str(bundle / "install.py")), \
            mock.patch.object(install.platform, "system", return_value="Linux"), \
            mock.patch.object(install.platform, "machine", return_value="x86_64"), \
            mock.patch.object(install.native_service.platform, "system", return_value="Linux"), \
            mock.patch.object(install, "free_port"):
        return install.main([str(item) for item in argv])
