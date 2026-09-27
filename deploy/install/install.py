#!/usr/bin/env python3
"""Install one matched Core distribution, repair it, or convert an earlier installation.

A new installation's flags seed <install-dir>/config.json. Afterwards, edit that file
and run <install-dir>/oac apply; rerunning this installer only repairs.
"""
import argparse
import base64
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
import secrets
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import uuid
from urllib.parse import urlsplit

import config_model
import configuration
from configuration import valid_core_origin
import convert
import rename
import native_service
import oac_cli
import sandbox_setup
from distribution import DistributionError, artifact, image_identities, ensure_docker_image

SETTING_FLAGS = ("core_only", "web_only", "native_core", "core_port", "web_port", "core_url", "public_url")
RETIRED_NODE_FLAGS = ("--sandbox-provider and --provider are retired: use --sandbox docker|microsandbox|e2b|none. "
                      "The installer no longer adds this host as a node; add it with Add node on the Nodes page in Web.")
DOCKER_RISKS = """Docker sandboxes isolate less than microsandbox, the default:
- Containers share the node's kernel, so a container escape reaches the host;
  microsandbox runs each sandbox in its own microVM.
- Each node's service account is in the docker group, which is root-equivalent
  on that host.
- Choose Docker only for trusted workloads or for node hosts without KVM."""


class InstallError(Exception):
    pass


def run(args, **kwargs):
    # Never print a generated Compose file, process environment or secret value.
    return subprocess.run(args, **dict({"check": True}, **kwargs))


def digest(path):
    result = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def verify_bundle(bundle):
    covered = set()
    for line in (bundle / "SHA256SUMS").read_text().splitlines():
        expected, name = line.split("  ", 1)
        if name in covered:
            raise InstallError("Duplicate distribution checksum entry")
        covered.add(name)
        path = bundle / name
        if not path.resolve().is_relative_to(bundle.resolve()) or path.is_symlink() or not path.is_file():
            raise InstallError("Invalid distribution path")
        if digest(path) != expected:
            raise InstallError("Distribution checksum mismatch: " + name)
    required = {"manifest.json", "install.sh", "install.py", "configuration.py", "config_model.py",
                "config.schema.json", "oac_cli.py", "convert.py", "rename.py", "oac.pyz", "native_service.py",
                "sandbox_setup.py", "standard-sizes.json", "node_spec.py", "node-install.pyz",
                "self-hosted-install.pyz", "distribution.py", "runtime/seccomp.json"}
    required.update(f"images/{name}.tar" for name in ("core", "web", "database"))
    required.update("native/bin/" + name for name in ("oac-core", "oac-core-migrate"))
    required.add("native/e2b/oac-e2b-provider")
    if not required.issubset(covered):
        raise InstallError("Distribution checksum list is incomplete")
    manifest = json.loads((bundle / "manifest.json").read_text())
    for name in ("core", "web", "database", "runtime"):
        image_identities(manifest, name)
    for name in ("images/runtime.tar.gz", "native/bin/parsar-sandbox-node",
                 "native/bin/agents-api-microsandbox-provider", "native/microsandbox/msb",
                 "native/microsandbox/libkrunfw.so.5.6.1"):
        artifact(manifest, name)
    return manifest


def free_port(port):
    with socket.socket() as sock:
        try:
            sock.bind(("127.0.0.1", port))
        except OSError:
            raise InstallError(f"Port {port} is already in use; select another port") from None


def database_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def public_origin(value):
    # Normalize case and a trailing slash, then apply Core's exact origin rule.
    try:
        parsed = urlsplit(value.strip())
        if parsed.path == "/":
            parsed = parsed._replace(path="")
        value = parsed._replace(scheme=parsed.scheme.lower(), netloc=parsed.netloc.lower()).geturl()
    except ValueError:
        value = ""
    if not valid_core_origin(value):
        raise argparse.ArgumentTypeError("Public URL must be an HTTPS origin such as https://core.example, "
                                         "without path, credentials, query or fragment; plain HTTP only for a loopback host")
    return value


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--core-only", action="store_true", default=None)
    modes.add_argument("--web-only", action="store_true", default=None)
    parser.add_argument("--native-core", action="store_true", default=None, help="Run Core as a systemd user service")
    parser.add_argument("--sandbox", choices=sandbox_setup.CHOICES,
                        help="Sandbox backend Core starts with, at Web's Standard size (default: microsandbox; "
                             "none with --web-only). Add nodes afterwards on the Nodes page in Web")
    parser.add_argument("--accept-docker-risks", action="store_true",
                        help="With --sandbox docker: accept its weaker isolation without asking")
    parser.add_argument("--e2b-api-key-file", type=Path, help="With --sandbox e2b: private file containing the E2B API key")
    parser.add_argument("--e2b-template", help="With --sandbox e2b: the ready template build, template-id:build-uuid")
    parser.add_argument("--sandbox-provider", nargs="?", const=True, help=argparse.SUPPRESS)
    parser.add_argument("--provider", nargs="?", const=True, help=argparse.SUPPRESS)
    parser.add_argument("--install-dir", type=Path)
    parser.add_argument("--core-port", type=int)
    parser.add_argument("--web-port", type=int)
    parser.add_argument("--core-url", type=public_origin, help="Web-only: origin of the existing Core")
    parser.add_argument("--public-url", type=public_origin, help="Public HTTPS origin behind your TLS reverse proxy")
    parser.add_argument("--core-key-file", type=Path, help="Web-only: private file containing the existing Core's Core key")
    parser.add_argument("--config", type=Path, help="Seed a new installation's config.json from this file")
    parser.add_argument("--convert", action="store_true",
                        help="Convert a pre-rename installation to OpenAgentCore and this release")
    parser.add_argument("--yes", action="store_true", help="With --convert: do not ask for confirmation")
    parser.add_argument("--admin-token-file", type=Path, help=argparse.SUPPRESS)
    parser.add_argument("--status", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--stop", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    args.explicit_install_dir = args.install_dir is not None
    args.install_dir = rename.choose_root(args.install_dir) if args.convert else (args.install_dir or Path.home() / ".oac/core")
    if args.admin_token_file:
        parser.error("--admin-token-file was renamed; use --core-key-file")
    if args.sandbox_provider is not None or args.provider is not None:
        parser.error(RETIRED_NODE_FLAGS)
    for retired in ("status", "stop"):
        if getattr(args, retired):
            parser.error(f"--{retired} is retired; run {args.install_dir / 'oac'} {retired}")
    if not args.install_dir.is_absolute():
        parser.error("--install-dir must be absolute")
    args.given = [name for name, value in vars(args).items()
                  if name not in ("install_dir", "given", "explicit_install_dir") and value not in (None, False)]
    return args


def seed_document(args):
    """The --config file, which replaces the setting flags."""
    if args.config is None:
        return None
    if any(getattr(args, name) is not None for name in SETTING_FLAGS):
        raise InstallError("--config replaces the setting flags; put those settings in the file")
    try:
        document = json.loads(args.config.read_text())
    except (OSError, ValueError):
        raise InstallError("--config must name a readable JSON file") from None
    if not isinstance(document, dict):
        raise InstallError("--config must hold a JSON object")
    return document


def check_flags(args, document):
    """Flag combinations for a new installation, before config.json is seeded."""
    if document is None:
        mode, native = ("core-only" if args.core_only else "web-only" if args.web_only else "all"), args.native_core
    else:
        mode, native = document.get("mode"), document.get("native_core")
    if mode == "web-only" and args.sandbox not in (None, "none"):
        raise InstallError("--web-only has no Core; choose the sandbox backend on the Core host")
    choice = args.sandbox or ("none" if mode == "web-only" else "microsandbox")
    if args.accept_docker_risks and choice != "docker":
        raise InstallError("--accept-docker-risks requires --sandbox docker")
    if choice == "e2b" and not (args.e2b_api_key_file and args.e2b_template):
        raise InstallError("--sandbox e2b requires --e2b-api-key-file and --e2b-template")
    if choice != "e2b" and (args.e2b_api_key_file or args.e2b_template):
        raise InstallError("--e2b-api-key-file and --e2b-template require --sandbox e2b")
    if choice == "e2b" and not sandbox_setup.e2b_template(args.e2b_template):
        raise InstallError("--e2b-template must name a template build as template-id:build-uuid")
    if mode == "web-only" and native:
        raise InstallError("--web-only cannot install native Core")
    if mode == "web-only" and not args.core_key_file:
        raise InstallError("--web-only requires --core-key-file (and --core-url, or web.core_url in --config)")
    if mode != "web-only" and args.core_key_file:
        raise InstallError("--core-key-file requires --web-only")
    return choice


def seed_config(args, document):
    """config.json for a new installation, from flags or from --config."""
    if document is not None:
        document.setdefault("$schema", "generated/config.schema.json")
        if document.get("native_core"):
            document.setdefault("ports", {}).setdefault("database", database_port())
        return config_model.validate(document)
    mode = "core-only" if args.core_only else "web-only" if args.web_only else "all"
    native = bool(args.native_core)
    return config_model.initial(mode, native, public_url=args.public_url, **{
        "ports.core": args.core_port, "ports.web": args.web_port, "web.core_url": args.core_url,
        "ports.database": database_port() if native else None})


def loopback_origin(value):
    hostname = urlsplit(value or "").hostname
    try:
        return ipaddress.ip_address(hostname).is_loopback
    except ValueError:
        return hostname == "localhost"


def origin_port(value):
    parsed = urlsplit(value)
    return parsed.port or (443 if parsed.scheme == "https" else 80)


def nodes_reach(public_url):
    """Nodes and their sandboxes need an HTTPS public URL that is not loopback."""
    return urlsplit(public_url or "").scheme == "https" and not loopback_origin(public_url)


def confirm_docker(accepted):
    """Docker sandboxes need an explicit yes to their weaker isolation, before anything is created."""
    print(DOCKER_RISKS, flush=True)
    if accepted:
        return
    try:
        answer = input("Use Docker sandboxes anyway? [y/N] ").strip().lower() if sys.stdin.isatty() else ""
    except EOFError:
        answer = ""
    if answer not in ("y", "yes"):
        raise InstallError("Docker sandboxes were not confirmed; nothing was installed. Rerun with "
                           "--accept-docker-risks, or without --sandbox for microsandbox")


def check_public_url(config, choice):
    # E2B's sandboxes reach Core from E2B's cloud; Core would refuse the selection.
    if choice == "e2b" and not nodes_reach(config["public_url"]):
        raise InstallError("E2B needs an HTTPS public_url that is not loopback (--public-url, or public_url "
                           "in the --config file)")


def read_private_file(source, name, limit=4096):
    """The one token in an absolute, private regular file of at most 4 KiB, never through a symlink."""
    refused = InstallError(f"{name} must be an absolute, private regular file of at most 4 KiB")
    if not source.is_absolute():
        raise refused
    try:
        # O_NONBLOCK: a FIFO in its place must not hang the installer.
        descriptor = os.open(source, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except OSError:
        raise refused from None
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) & 0o077 or info.st_size > limit:
            raise refused
        raw = stream.read(limit + 1)
    try:
        token = raw.decode().strip()
    except UnicodeDecodeError:
        token = ""
    if len(raw) > limit or not token or any(c.isspace() for c in token) or "\x00" in token:
        raise InstallError("Invalid " + name)
    return token


def read_core_key_file(source):
    token = read_private_file(source, "Core key file")
    if len(token) < 32:
        raise InstallError("The Core key must have at least 32 characters")
    return token


def check_compose():
    version = run(["docker", "compose", "version", "--short"], capture_output=True, text=True).stdout.strip()
    match = re.fullmatch(r"v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?", version)
    if not match or tuple(map(int, match.groups())) < (2, 26, 0):
        raise InstallError("Docker Compose 2.26.0 or newer is required for literal Core environment values")


def check_host():
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64") or os.getuid() == 0:
        raise InstallError("Run as a non-root user on Linux amd64 with Docker access")
    check_compose()
    run(["docker", "info", "--format", "{{.ServerVersion}}"], stdout=subprocess.DEVNULL)


def image_names(mode, native):
    if mode == "web-only":
        return ["web"]
    names = ["database"] if native else ["core", "database"]
    return names + (["web"] if mode == "all" else [])


def image_loader(manifest, bundle):
    def load(names):
        return {name: ensure_docker_image(manifest, name, lambda name=name: bundle / f"images/{name}.tar")
                for name in names}
    return load


def prepare_node_payload(root, state, bundle, replace=False):
    if state["mode"] == "core-only":
        return
    destination = root / "node-payload"
    # Public distribution files only. Never copy the private installation config.
    names = ["node-install.pyz", "self-hosted-install.pyz", "manifest.json", "SHA256SUMS", "runtime/seccomp.json"]
    manifest = json.loads((bundle / "manifest.json").read_text())
    for logical in manifest.get("artifacts", {}):
        entry = artifact(manifest, logical)
        name = "artifacts/" + entry["filename"]
        source = bundle / name
        if source.exists():
            if (source.is_symlink() or not source.is_file()
                    or not source.resolve().is_relative_to(bundle.resolve())
                    or source.stat().st_size != entry["size"] or digest(source) != entry["sha256"]):
                raise InstallError("Offline artifact verification failed: " + logical)
            names.append(name)
    if replace and destination.is_dir() and not destination.is_symlink():
        shutil.rmtree(destination)
    for name in names:
        source, target = bundle / name, destination / name
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        if target.exists():
            if target.is_symlink() or not target.is_file() or digest(target) != digest(source):
                raise InstallError("Installed node payload differs; preserve it and inspect the distribution")
        else:
            descriptor, temporary = tempfile.mkstemp(prefix=".payload-", dir=target.parent)
            os.close(descriptor)
            try:
                shutil.copyfile(source, temporary)
                os.replace(temporary, target)
            finally:
                if os.path.exists(temporary):
                    os.unlink(temporary)


def install_oac(root, bundle):
    """Copy the oac command into the installation; replace a missing or different copy."""
    target = root / "oac"
    source = bundle / "oac.pyz"
    if target.is_file() and not target.is_symlink() and digest(target) == digest(source):
        os.chmod(target, 0o700)
        return
    descriptor, temporary = tempfile.mkstemp(prefix=".oac-", dir=root)
    os.close(descriptor)
    try:
        shutil.copyfile(source, temporary)
        os.chmod(temporary, 0o700)
        os.replace(temporary, target)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def layout(root):
    if not root.exists() or not any(root.iterdir()):
        return "empty"
    config, legacy = (root / "config.json").exists(), (root / "installation.json").exists()
    if config and legacy:
        return "interrupted"
    if config or legacy:
        return "config" if config else "legacy"
    generated = root / "generated"
    never_applied = not (generated.is_dir() and any(generated.iterdir()))
    if (root / "state.json").exists():
        try:
            recorded = json.loads((root / "state.json").read_text()).get("generated")
        except (OSError, ValueError, AttributeError):
            recorded = True
        # Never applied: nothing started, so nothing depends on these secrets yet.
        return "incomplete" if never_applied and not recorded else "missing-config"
    if never_applied and {path.name for path in root.iterdir()} <= {"secrets", "generated", "state", ".oac.lock"}:
        return "incomplete"
    return "other"


def create(root, args, config, manifest, images):
    """Write the new installation's secrets, config.json and state.json."""
    mode = config["mode"]
    token = read_core_key_file(args.core_key_file) if mode == "web-only" else secrets.token_hex(32)
    if root.parent == Path.home() / ".oac":
        rename.private_parent(root)
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(root, 0o700)
    for name in ["secrets", "generated"] + ([] if mode == "web-only" else ["state", "state/e2b"]):
        (root / name).mkdir(mode=0o700)
    write = oac_cli.create_private
    write(root / "secrets/core.key", token)
    if mode != "web-only":
        write(root / "secrets/credential.key", base64.b64encode(secrets.token_bytes(32)).decode())
        write(root / "secrets/database.password", secrets.token_hex(32))
    state = {"format": 2, "installation_id": str(uuid.uuid4()), "project": "oac-" + secrets.token_hex(5),
             "uid": os.getuid(), "gid": os.getgid(), "mode": mode, "native_core": config.get("native_core", False),
             "source_commit": manifest["source_commit"], "images": images,
             "secrets_sha256": configuration.secret_digests(root, mode), "core_installation_id": None,
             "converted_from": None, "generated": {}}
    state["secrets_sha256"].pop("core.key")
    # state.json first: whenever config.json exists, the installation can be repaired.
    write(root / "state.json", json.dumps(state, indent=2) + "\n")
    write(root / "config.json", json.dumps(config, indent=2) + "\n")


def unfinished_conversion(state):
    return any(bool(state.get(key)) and not state[key].get("finished") for key in ("converted_from", "renamed_from"))


def finish(root, bundle, manifest, fresh=False, selection=None):
    """Put the bundle's files in place, apply config.json and start the services.

    A new installation then saves its sandbox selection; a repair or conversion never does.
    """
    state = oac_cli.load_state(root)
    converting = unfinished_conversion(state)
    # A converted installation replaces the earlier release's binaries and payload once.
    prepare_node_payload(root, state, bundle, converting)
    native_service.prepare(root, state, bundle, converting)
    install_oac(root, bundle)
    retry = f"rerun ./install.sh {'--convert ' if converting else ''}--install-dir {root}"
    try:
        renamed = state.get("renamed_from")
        confirmed_url = None
        if renamed and not renamed.get("finished"):
            config = oac_cli.load_config(root)
            if config["public_url"] != renamed["public_url"]:
                raise InstallError("public_url changed during conversion; restore the recorded value and finish conversion first")
            # The one conversion confirmation already approved this preserved URL.
            # Normal apply must never read historical environment names.
            if config["mode"] != "web-only":
                confirmed_url = configuration.local_public_url(config)
        oac_cli.apply(root, start=True, retry=retry, confirm_public_url_change=confirmed_url)
    except oac_cli.OacError as error:
        if not selection:
            raise
        # A repair never selects a backend, so the --sandbox choice would otherwise be lost silently.
        raise oac_cli.OacError(f"{str(error).rstrip('.')}. The sandbox backend was not chosen; after the "
                                     f"repair, choose it {choose_where(state['mode'])}") from None
    config = oac_cli.load_config(root)
    mode = config["mode"]
    # An earlier-release Core has no /core/v1/installation (404); apply noted it and Web still works.
    if mode == "web-only" and oac_cli.paired_core(root, config)[0] not in (200, 404):
        raise InstallError("Core key authentication failed. Inspect secrets/core.key and web.core_url; no model was called")
    deployment = failure = None
    if selection:
        try:
            deployment = sandbox_setup.initialize(root, config, state, selection)
        except sandbox_setup.SandboxSetupError as error:
            failure = error
    if converting and state.get("converted_from"):
        state = oac_cli.load_state(root)
        state["converted_from"] = dict(state["converted_from"], finished=True)
        oac_cli.save_state(root, state)
    summary(root, config, fresh, selection, deployment)
    if failure:
        raise InstallError(f"{str(failure).rstrip('.')}. Services are installed and running; "
                           f"choose the sandbox backend {choose_where(mode)}")


def choose_where(mode):
    """Where an operator chooses the sandbox backend when the installer did not."""
    return ("on the Nodes page in Web" if mode == "all" else
            "through the Core management API (POST /core/v1/sandbox/deployment)")


def size(resources):
    memory = resources["memory_mib"]
    return f'{resources["cpus"]} CPUs, ' + (f"{memory // 1024} GiB" if memory % 1024 == 0 else f"{memory} MiB")


def sandbox_lines(root, config, selection, deployment):
    """What a new installation's sandboxes are and how nodes are added."""
    web = config["mode"] == "all"
    if selection is None:
        return [f"Sandboxes: none chosen. Choose a sandbox backend {choose_where(config['mode'])}."]
    if deployment is None:
        return []  # The error that follows says what to do.
    if selection["provider"] == "e2b":
        resources = (deployment.get("specification") or {}).get("resources") or {}
        built = f" ({size(resources)})" if {"cpus", "memory_mib"} <= set(resources) else ""
        return [f'Sandboxes: E2B template {selection["e2b"]["template"]}{built}. E2B runs them; no nodes are needed.']
    line = f'Sandboxes: {sandbox_setup.NAMES[selection["provider"]]}, Standard ({size(selection["resources"])}).'
    if selection["provider"] == "microsandbox":
        # The installer adds no node, so this host needs no KVM of its own.
        line += " Its nodes need KVM (/dev/kvm); this host needs it only if you add it as a node."
    add = "in Web, open Nodes and choose Add node" if web else "in a Web console paired with this Core, open Nodes and choose Add node"
    lines = [line, f"Add nodes: {add}, then paste the command on each host, this one included."]
    if not nodes_reach(config["public_url"]):
        # Each sandbox calls Core at public_url; a loopback address is the sandbox itself.
        lines.insert(1, "Nodes need an HTTPS public URL that other machines and their sandboxes can reach: set "
                        f"public_url in {root / 'config.json'} and run {root / 'oac'} apply first.")
    return lines


def summary(root, config, fresh, selection=None, deployment=None):
    mode, public_url, ports = config["mode"], config["public_url"], config["ports"]
    if mode != "core-only":
        # The console accepts only its configured origin, so a public URL has no loopback console.
        console = public_url or f'http://127.0.0.1:{ports["web"]}'
        print("Console: " + console + (" (local only)" if loopback_origin(console) else ""))
    if mode != "web-only":
        api = f'http://127.0.0.1:{ports["core"]}/v1'
        if public_url and not loopback_origin(public_url):
            print("API base URL: " + public_url + "/v1")
            print("Local-only API on this host: " + api)
        elif public_url and origin_port(public_url) != ports.get("web"):
            print("API base URL: " + public_url + "/v1 (local only)")
        else:
            # Web answers 404 on /v1, so only Core's own port serves the API locally.
            print("API base URL: " + api + " (local only)")
    core_key = root / "secrets/core.key"
    if mode == "core-only":
        print(f'Next: create a Project and its API key through the Core management API at '
              f'http://127.0.0.1:{ports["core"]}/core/v1 (local only) with the Core key in {core_key}.')
    else:
        print(f"Next: sign in to Web with the Core key in {core_key}, then create a Project and its API key on the Projects and keys page.")
    print("Keep the Core key private; it also authorizes the Core management API.")
    print(f"Settings: {root / 'config.json'}. Edit it, then run {root / 'oac'} apply.")
    print(f"Manage the services with {root / 'oac'} status, start and stop.")
    # Only a new installation reports its sandboxes; a repaired or converted one keeps its own.
    if fresh and mode != "web-only":
        for line in sandbox_lines(root, config, selection, deployment):
            print(line)
    print("Services installed. No model request was made. See docs/getting-started/quickstart.md.")


def main(argv=None):
    args = arguments(argv)
    root = args.install_dir
    if root.is_symlink() or root.resolve() != root:
        raise InstallError("Installation directory must be canonical and not a symlink")
    bundle = Path(__file__).resolve().parent
    kind = layout(root)
    if args.convert:
        if set(args.given) - {"convert", "yes", "public_url"}:
            raise InstallError("--convert accepts only --install-dir, --yes and --public-url")
        if kind not in ("legacy", "interrupted", "config", "missing-config"):
            raise InstallError("--convert needs a pre-rename installation in --install-dir")
        check_host()
        manifest = verify_bundle(bundle)
        state = rename.read_state(root) if (root / "state.json").exists() else convert.detect(root)
        if native_service.is_native(state):
            native_service.preflight(bundle, rename.destination(root))
        rename.convert_installation(root, bundle, manifest, image_loader(manifest, bundle),
                                   args.public_url, args.yes, run, finish)
        return
    if kind in ("legacy", "interrupted"):
        raise InstallError(f"This installation predates config.json; run ./install.sh --convert --install-dir {root}")
    if not args.explicit_install_dir:
        old, _ = rename.defaults()
        if (old / "state.json").exists() or (old / "installation.json").exists():
            raise InstallError("An installation made before the OpenAgentCore rename is at ~/.parsar/core. "
                               "Convert it with ./install.sh --convert (it moves to ~/.oac/core), or pass "
                               "--install-dir to install another one. Nothing was changed.")
    if kind == "config":
        if args.given:
            raise InstallError(f"This installation is configured by {root / 'config.json'}. Edit it and run "
                               f"{root / 'oac'} apply; install.sh accepts only --install-dir to repair it")
        state = rename.read_state(root)
        if state.get("format") == 1 or unfinished_conversion(state):
            raise InstallError(f"Run ./install.sh --convert --install-dir {root} to finish the OpenAgentCore conversion")
        state = oac_cli.load_state(root)
        check_host()
        manifest = verify_bundle(bundle)
        if state["source_commit"] != manifest["source_commit"]:
            raise InstallError("This installation runs another release; upgrading an installation arrives with "
                               "oac upgrade")
        if native_service.is_native(state):
            native_service.preflight(bundle, root)
        images = image_loader(manifest, bundle)(list(state["images"]))
        if images != state["images"]:
            oac_cli.save_state(root, dict(state, images=images))
        finish(root, bundle, manifest)
        return
    if kind == "missing-config":
        raise InstallError(f"{root / 'config.json'} is missing. Restore it from a backup; "
                           f"{root / 'generated/settings.json'} lists the last applied values. The secrets and "
                           "database belong to this installation, so keep the directory. Nothing was changed")
    if kind == "incomplete":
        raise InstallError(f"An earlier installation into {root} stopped before writing config.json and started no "
                           "service. Remove the directory and install again")
    if kind == "other":
        raise InstallError("Installation directory is not empty; refusing to overwrite existing state")
    document = seed_document(args)
    choice = check_flags(args, document)
    config = seed_config(args, document)
    check_public_url(config, choice)
    if config["mode"] == "web-only":
        read_core_key_file(args.core_key_file)
    e2b = ({"api_key": read_private_file(args.e2b_api_key_file, "E2B API key file"), "template": args.e2b_template}
           if choice == "e2b" else None)
    if choice == "docker":
        confirm_docker(args.accept_docker_risks)
    check_host()
    manifest = verify_bundle(bundle)
    if config.get("native_core"):
        native_service.preflight(bundle, root)
    for key in ("core", "web", "database"):
        if key in config["ports"]:
            free_port(config["ports"][key])
    selection = None if choice == "none" else sandbox_setup.selection(bundle, manifest, choice, e2b)
    images = image_loader(manifest, bundle)(image_names(config["mode"], config.get("native_core", False)))
    create(root, args, config, manifest, images)
    finish(root, bundle, manifest, fresh=True, selection=selection)


if __name__ == "__main__":
    try:
        main()
    except (InstallError, convert.ConvertError, oac_cli.OacError, config_model.ConfigError,
            sandbox_setup.SandboxSetupError, DistributionError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError):
        # Errors never include generated configuration or external process output.
        print("Installation failed; inspect prerequisites and private deployment files", file=sys.stderr)
        sys.exit(1)
