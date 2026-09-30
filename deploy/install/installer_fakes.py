"""A fake Docker, systemd and HTTP host for installer tests. Nothing real is started.

Compose is modelled by its observable contract: `up` recreates a container exactly
when its resolved configuration (including env_file content) changed, each container
keeps the labels it was created with, and Core loads its Core key digests when it
starts. systemd runs the unit it last loaded; its process keeps the environment it
started with.
"""
import hashlib
import json
from pathlib import Path
import re
import subprocess
from types import SimpleNamespace
from unittest import mock

import native_service
import oac_cli
import sandbox_setup

LABEL = "io.oac.inputs"
STANDARD_SIZES = Path(__file__).resolve().parents[2] / "apps/web/src/features/sandbox/standard-sizes.json"


def sha256(data):
    return hashlib.sha256(data.encode() if isinstance(data, str) else data).hexdigest()


class FakeHost:
    def __init__(self, test):
        self.commands, self.requests = [], []
        self.containers = {}  # service -> {hash, inputs, running}
        self.project = None
        self.recreated = []
        self.native = {"active": False, "starts": 0, "restarts": 0, "reloads": 0, "addr": None, "digests": [],
                       "inputs": None, "loaded": None, "environment": ""}
        # fails: Core never starts; rejects(core.env text): Core refuses that configuration.
        self.core = {"port": None, "digests": [], "fails": False, "log": "", "rejects": lambda environment: False}
        self.web_port = None
        self.native_root = None  # the installation whose native unit systemctl manages
        self.missing_images = set()
        self.core_installation_id = "11111111-2222-4333-8444-555555555555"
        self.bindings = {"nodes": 0, "nodes_on_other_address": 0, "hosted_sandboxes": 0, "self_hosted_executors": 0}
        self.nodes = []
        self.remote_core = {}  # web-only: origin -> (status, installation_id)
        self.deployment_core_url = ""  # what an old Core reports for its sandbox deployment
        self.deployment = {"provider": "", "generation": 0, "reset": None, "resources": {"allocations": 0, "pending": 0}}  # what sandbox_setup reads and posts
        self.deployment_posts = []
        self.deployment_refusal = None  # Core's message when it refuses the POST
        for target, name, value in ((subprocess, "run", mock.Mock(side_effect=self.run)),
                                    (oac_cli, "http", self.http),
                                    (oac_cli, "time", SimpleNamespace(sleep=lambda seconds: None)),
                                    (native_service, "_process_environment", self.process_environment),
                                    (sandbox_setup, "send", self.sandbox_send)):
            patcher = mock.patch.object(target, name, value)
            patcher.start()
            test.addCleanup(patcher.stop)

    def running(self):
        return {name for name, item in self.containers.items() if item["running"]}

    def run_container(self, name, port=None, digests=None):
        """A container of an installation made outside the test, such as an earlier release."""
        self.containers[name] = {"hash": "external", "inputs": None, "running": True}
        if name == "core":
            self.core.update(port=port, digests=digests or [])

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
        elif args[:2] == ["docker", "ps"]:
            stdout = "\n".join(name for name, item in self.containers.items() if item["running"]) if "--format" in args else "\n".join("id-" + name for name in self.containers)
        elif args[:2] == ["docker", "inspect"]:
            stdout = "\n".join(f'{name}\t{self.containers[name]["inputs"] or ""}\t'
                               f'{"running" if self.containers[name]["running"] else "exited"}\t'
                               for name in (item[3:] for item in args[4:]) if name in self.containers)
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
        if args[:1] == ["down"]:
            self.containers.clear()
            return 0, ""
        if args[:1] == ["stop"]:
            for name in args[1:] or services:
                if name in self.containers:
                    self.containers[name]["running"] = False
            return 0, ""
        if args[:1] == ["restart"]:
            for name in args[1:]:
                self.containers[name]["running"] = True
                if name == "core":
                    self.load_core(path.parent.parent, services[name])
            return 0, ""
        if args[:1] == ["logs"]:
            return 0, self.core["log"]
        if args[:1] == ["up"]:
            targets = [item for item in args[1:] if not item.startswith("-") and not item.isdigit()]
            names = list(services) if not targets else [
                name for name in ("database", "migrate", *targets) if name in services and
                (name in targets or name in ("database", "migrate") and "core" in targets)]
            for name in names:
                digest = self.service_hash(document, name)
                current = self.containers.get(name)
                if current is None or current["hash"] != digest:
                    self.containers[name] = {"hash": digest, "inputs": services[name].get("labels", {}).get(LABEL),
                                             "running": False}
                    self.recreated.append(name)
                    if name == "core":
                        self.load_core(path.parent.parent, services[name])
                if name != "migrate":
                    self.containers[name]["running"] = True
            if "web" in names:
                web = services["web"]
                if "gateway" in services:
                    web = services["gateway"]
                self.web_port = int(web["ports"][0].rsplit(":", 2)[1]) if "ports" in web else int(
                    web["environment"]["OAC_WEB_ADDR"].rsplit(":", 1)[1])
            environment = path.parent / "core.env"
            if "core" in names and (self.core["fails"] or
                                    self.core["rejects"](environment.read_text() if environment.exists() else "")):
                self.containers["core"]["running"] = False
                self.core["failed"] = True
                return 1, ""
        return 0, ""

    def load_core(self, root, service):
        if "ports" in service:
            self.core["port"] = int(service["ports"][0].rsplit(":", 2)[1])
        digests = root / "generated/core-key-digests.json"
        self.core["digests"] = json.loads(digests.read_text())
        environment = root / "generated/core.env"
        self.core["environment"] = environment.read_text() if environment.exists() else ""

    def unit_file(self):
        found = sorted(self.native_root.glob("generated/oac-*-core.service"))
        return found[0].read_text() if found else ""

    def systemctl(self, args):
        native = self.native
        if args[0] == "daemon-reload":
            native["reloads"] += 1
            native["loaded"] = self.unit_file()
        elif args[0] in ("enable", "restart", "start"):
            if args[0] != "restart" and native["active"]:
                return 0, ""  # systemd leaves an active unit alone on start and enable --now
            native["starts" if args[0] != "restart" else "restarts"] += 1
            if native["loaded"] is None:
                native["loaded"] = self.unit_file()
            environment = self.native_root / "generated/core.env"
            refused = self.core["fails"] or self.core["rejects"](environment.read_text() if environment.exists() else "")
            native["active"] = not refused
            self.core["failed"] = self.core.get("failed") or refused
            match = re.search(r"^Environment=OAC_INPUTS=(\w+)$", native["loaded"], re.M)
            native["inputs"] = match[1] if match and native["active"] else None
            root = self.native_root
            environment = root / "generated/core.env"
            if environment.exists():
                address = next(line for line in environment.read_text().splitlines() if line.startswith("OAC_ADDR="))
                native["addr"] = int(address.rsplit(":", 1)[1].rstrip('"'))
                native["digests"] = json.loads((root / "generated/core-key-digests.json").read_text())
                native["environment"] = environment.read_text()
        elif args[0] == "stop":
            native["active"], native["inputs"] = False, None
        elif args[0] == "is-active":
            return (0 if native["active"] else 3), ""
        elif args[0] == "show" and "--property=MainPID" in args:
            return 0, "4242" if native["active"] else "0"
        elif args[0] == "show":
            return 0, "252"
        return 0, ""

    def process_environment(self, pid):
        return {"OAC_INPUTS": self.native["inputs"]} if self.native["inputs"] else {}

    # HTTP -------------------------------------------------------------------
    def core_listening(self, port):
        if self.containers.get("core", {}).get("running") and self.core["port"] == port:
            return self.core["digests"]
        if self.native["active"] and self.native["addr"] == port:
            return self.native["digests"]
        return None

    def http(self, url, headers=None, timeout=5):
        self.requests.append(url)
        headers = headers or {}
        origin, _, path = url.partition("://")[2].partition("/")
        path = "/" + path
        key = headers.get("Authorization", "").removeprefix("Bearer ")
        for remote, (status, installation) in self.remote_core.items():
            if url.startswith(remote + "/"):
                return status, json.dumps({"installation_id": installation}).encode()
        port = int(origin.rsplit(":", 1)[1])
        digests = self.core_listening(port)
        web_up = self.containers.get("web", {}).get("running") and self.web_port == port
        if path == "/healthz":
            return (200, b"{}") if digests is not None or web_up else (0, b"")
        if path == "/console/auth":
            return (200, b"{}") if web_up else (0, b"")
        if digests is None:
            return 0, b""
        if sha256(key) not in digests:
            return 401, b""
        if path == "/core/v1/installation":
            native = self.native["active"] and self.native["addr"] == port
            environment = self.native["environment"] if native else self.core.get("environment", "")
            match = re.search(r'^OAC_PUBLIC_URL="([^"]+)"$', environment, re.M)
            public = match[1] if match else None
            return 200, json.dumps({"installation_id": self.core_installation_id, "public_url": public,
                                    "address_bindings": self.bindings}).encode()
        if path == "/core/v1/sandbox/nodes":
            return 200, json.dumps({"data": self.nodes}).encode()
        if path == "/core/v1/sandbox/deployment":
            return 200, json.dumps(dict(self.deployment, core_url=self.deployment_core_url,
                installation_id=self.core_installation_id)).encode()
        return 404, b""

    def sandbox_send(self, req):
        origin, _, path = req.full_url.partition("://")[2].partition("/")
        port = int(origin.rsplit(":", 1)[1])
        digests = self.core_listening(port)
        if digests is None:
            raise ConnectionRefusedError()
        if sha256(req.get_header("Authorization", "").removeprefix("Bearer ")) not in digests:
            return 401, b'{"error": {"message": "Invalid Core key"}}'
        if path != "core/v1/sandbox/deployment":
            return 404, b""
        if req.get_method() in ("POST", "PUT"):
            selection = json.loads(req.data)
            self.deployment_posts.append(selection)
            if selection.get("expected_generation") != self.deployment.get("generation", 0):
                return 409, b'{"error":{"code":"generation_stale","message":"Deployment generation changed"}}'
            if self.deployment_refusal:
                return 409, json.dumps({"error": {"message": self.deployment_refusal}}).encode()
            # E2B adopts the template build's size.
            self.deployment = {"provider": selection["provider"], "generation": self.deployment.get("generation", 0) + 1,
                "reset": None, "resources": {"allocations": 0, "pending": 0},
                "specification": {"runtime": selection.get("runtime"), "resources": selection.get("resources", {"cpus": 2, "memory_mib": 2048})}}
        native = self.native["active"] and self.native["addr"] == port
        environment = self.native["environment"] if native else self.core.get("environment", "")
        installation = re.search(r'^OAC_INSTALLATION_ID="([^"]+)"$', environment, re.M)
        return 200, json.dumps(dict(self.deployment, installation_id=installation[1] if installation else self.core_installation_id)).encode()


MANIFEST = {
    "source_commit": "a" * 40,
    "images": {name: "sha256:" + digit * 64 for name, digit in (
        ("core", "1"), ("runtime", "2"), ("database", "3"), ("web", "4"), ("ingress", "7"))},
    "image_manifest_digests": {name: "sha256:" + digit * 64 for name, digit in (
        ("core", "a"), ("runtime", "b"), ("database", "c"), ("web", "d"), ("ingress", "e"))},
    "runtime_ref": "oac-runtime@sha256:" + "b" * 64,
    "microsandbox": {"runtime_sha256": "5" * 64, "firmware_sha256": "6" * 64},
}
MODULES = ("install.py", "install_output.py", "install_display.py", "configuration.py", "config_model.py", "ingress.py", "ingress_config.py", "config.schema.json", "oac_cli.py",
           "native_service.py", "sandbox_setup.py", "node_spec.py", "distribution.py", "install.sh")


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
    (bundle / "standard-sizes.json").write_bytes(STANDARD_SIZES.read_bytes())
    (bundle / "node-install.pyz").write_bytes(b"synthetic verified Python bootstrap")
    (bundle / "oac.pyz").write_bytes(b"synthetic oac command " + manifest["source_commit"].encode())
    manifest["artifacts"] = {}
    for name in ("images/runtime.tar.gz", "native/bin/oac-node",
                 "native/bin/oac-microsandbox-provider", "native/microsandbox/msb",
                 "native/microsandbox/libkrunfw.so.5.6.1"):
        manifest["artifacts"][name] = {"filename": "oac-" + manifest["source_commit"] + "-" + name.replace("/", "-"),
                                       "sha256": "a" * 64, "size": 1}
    (bundle / "manifest.json").write_text(json.dumps(manifest))
    for name in manifest["images"]:
        (bundle / "images" / (name + ".tar")).write_bytes(("synthetic " + name).encode())
    for name in ("bin/oac-core", "bin/oac-core-migrate", "bin/oac-microsandbox-provider",
                 "bin/oac-node", "microsandbox/msb", "microsandbox/libkrunfw.so.5.6.1",
                 "e2b/oac-e2b-provider"):
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
