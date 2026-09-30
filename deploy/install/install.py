#!/usr/bin/env python3
"""Install one matched Core distribution, repair it, without changing versions.

A new installation's flags seed <install-dir>/config.json. Afterwards, edit that file
and run <install-dir>/oac apply; rerunning this installer only repairs. A new installation
that fails before its services first start removes what it created; rerun the same command.
"""
import argparse
import base64
import contextlib
import errno
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
import secrets
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import traceback
import uuid
from urllib.parse import urlsplit

import config_model
import configuration
import ingress_config
from configuration import valid_core_origin
import native_installers
import native_service
import oac_cli
import sandbox_setup
import install_output
import install_display
from install_output import choose_where
from install_display import step
from distribution import DistributionError, artifact, image_identities, ensure_docker_image

SETTING_ARGUMENTS = {
    config_model.annotation(node, "install_flag"): (key, node)
    for key, node, _ in config_model.leaves()
    if config_model.annotation(node, "install_flag") and key not in ("mode", "native_core")
}
SETTING_FLAGS = ("core_only", "web_only", "native_core", *(
    flag.removeprefix("--").replace("-", "_") for flag in SETTING_ARGUMENTS))
AVOID = 20  # An omitted Core or Web port moves at most this far above its default.
DOCKER_RISKS = """Docker sandboxes isolate less than microsandbox, the default:
- Containers share the node's kernel, so a container escape reaches the host;
  microsandbox runs each sandbox in its own microVM.
- Each node's service account is in the docker group, which is root-equivalent
  on that host.
- Choose Docker only for trusted workloads or for node hosts without KVM."""


class InstallError(Exception):
    pass


REPORTED = (InstallError, oac_cli.OacError, config_model.ConfigError, sandbox_setup.SandboxSetupError,
            DistributionError, RuntimeError)
NOTHING_KEPT = "Nothing was kept; fix the problem and rerun the same command."


def error_text(error):
    """What the installer prints for an error, then what became of a new installation.

    It never includes generated configuration or command output.
    """
    if isinstance(error, REPORTED):
        text = str(error)
    elif isinstance(error, KeyboardInterrupt):
        text = "interrupted"
    elif isinstance(error, OSError) and error.errno in (errno.ENOSPC, errno.EDQUOT):
        text = "Disk space or quota exhausted; free space on the installation filesystem and rerun"
    elif isinstance(error, PermissionError):
        text = "Permission denied; use a directory writable by your current account (--install-dir)"
    else:
        text = "inspect prerequisites and private deployment files"
    removal = getattr(error, "removal", None)
    return text + ("\n" + removal if removal else "")


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
    required = {"manifest.json", "install.sh", "install.py", "configuration.py", "config_model.py", "ingress_config.py", "ingress.py",
                "config.schema.json", "oac_cli.py", "oac.pyz", "native_service.py",
                "sandbox_setup.py", "install_output.py", "install_display.py", "standard-sizes.json", "node_spec.py", "node-install.pyz",
                "distribution.py", "runtime/seccomp.json"}
    required.update(f"images/{name}.tar" for name in ("core", "web", "database", "ingress"))
    required.update("native/bin/" + name for name in ("oac-core", "oac-core-migrate"))
    required.add("native/e2b/oac-e2b-provider")
    if not required.issubset(covered):
        raise InstallError("Distribution checksum list is incomplete")
    manifest = json.loads((bundle / "manifest.json").read_text())
    for name in ("core", "web", "database", "runtime", "ingress"):
        image_identities(manifest, name)
    for name in ("images/runtime.tar.gz", "native/bin/oac-node",
                 "native/bin/oac-microsandbox-provider", "native/microsandbox/msb",
                 "native/microsandbox/libkrunfw.so.5.6.1"):
        artifact(manifest, name)
    return manifest


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
    parser.add_argument("--e2b-api-url", help="With --sandbox e2b: compatible service HTTPS API origin")
    parser.add_argument("--e2b-domain", help="With --sandbox e2b: compatible service data-plane domain")
    parser.add_argument("--install-dir", type=Path)
    for flag, (_, node) in SETTING_ARGUMENTS.items():
        value_type = int if node.get("type") == "integer" else public_origin if config_model.annotation(node, "check") == "origin" else str
        parser.add_argument(flag, type=value_type, help=node["description"])
    parser.add_argument("--core-key-file", type=Path, help="Web-only: private file containing the existing Core's Core key")
    parser.add_argument("--config", type=Path, help="Seed a new installation's config.json from this file")
    args = parser.parse_args(argv)
    args.install_dir = args.install_dir or Path.home() / ".oac/core"
    if not args.install_dir.is_absolute():
        parser.error("--install-dir must be absolute")
    args.given = [name for name, value in vars(args).items()
                  if name not in ("install_dir", "given") and value not in (None, False)]
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
    if choice != "e2b" and (args.e2b_api_key_file or args.e2b_template or args.e2b_api_url or args.e2b_domain):
        raise InstallError("E2B flags require --sandbox e2b")
    if choice == "e2b" and not sandbox_setup.e2b_template(args.e2b_template):
        raise InstallError("--e2b-template must name a template build as template-id:build-uuid")
    if choice == "e2b" and not sandbox_setup.e2b_endpoint(args.e2b_api_url, args.e2b_domain):
        raise InstallError("--e2b-api-url and --e2b-domain must both name public HTTPS compatible-service endpoints")
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
    values = {key: getattr(args, flag.removeprefix("--").replace("-", "_"))
              for flag, (key, _) in SETTING_ARGUMENTS.items()}
    values["ingress"] = values["ingress"] or ("managed" if mode == "all" and not native else "external")
    if values["ingress"] == "managed" and values["host"] is None:
        values["host"] = "0.0.0.0"
    values["ports.database"] = database_port() if native else None
    return config_model.initial(mode, native, **values)


def check_listeners(args, document, config):
    """Check every listener of a new installation, before anything slow runs.

    A taken port that the flags or the --config file set fails, and so does one that a
    loopback public_url names. An omitted Core or Web port moves to the first free port
    above its default that no other listener uses. Returns the config and
    (purpose, taken port, chosen port) for each move.
    """
    if document is None:
        names, where = {key: flag for flag, (key, _) in SETTING_ARGUMENTS.items()}, ""
        given = {key for key, flag in names.items() if getattr(args, flag.removeprefix("--").replace("-", "_")) is not None}
    else:
        names, where = {}, " in the --config file"
        given = {key for key in ("ports.core", "ports.web") if config_model.lookup(document, key) is not None}
    if not oac_cli.address_available(config["host"]):
        raise InstallError(f"{config['host']} ({names.get('host', 'host')}{where}) is not an address of this machine; "
                           "use one of its addresses")
    public_url, moved = config["public_url"], []
    for listener in configuration.listeners(config):
        if oac_cli.port_free(listener.host, listener.port):
            continue
        if listener.purpose == "HTTPS":
            remedy = "--ingress external" if document is None else '"ingress": "external" in the --config file'
            raise InstallError(f"Automatic HTTPS needs ports 80 and 443, and port {listener.port} is already in use on "
                               f"{listener.host}. Free it, or use an existing reverse proxy with {remedy}; "
                               f"find the process with: sudo ss -ltnp 'sport = :{listener.port}'")
        name = names.get(listener.setting, listener.setting)
        # Moving the port would leave a loopback public_url pointing at the old one.
        pinned = bool(public_url) and loopback_origin(public_url) and origin_port(public_url) == listener.port
        if pinned:
            name += " and " + names.get("public_url", "public_url")
        if listener.setting in given or pinned or listener.purpose not in ("Core", "Web"):
            raise InstallError(oac_cli.port_in_use(listener, name + where))
        taken = {other.port for other in configuration.listeners(config)}
        port = next((port for port in range(listener.port + 1, listener.port + AVOID + 1)
                     if port not in taken and oac_cli.port_free(listener.host, port)), None)
        if port is None:
            raise InstallError(f"Ports {listener.port} to {listener.port + AVOID} are in use on {listener.host}; "
                               f"set a free port with {name}{where}")
        config["ports"][listener.setting.removeprefix("ports.")] = port
        moved.append((listener.purpose, listener.port, port))
    return config, moved


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
    try:
        version = run(["docker", "compose", "version", "--short"], capture_output=True, text=True,
                      timeout=30).stdout.strip()
    except (OSError, subprocess.SubprocessError):
        raise InstallError("Docker with the Compose plugin is required; check docker compose version") from None
    match = re.fullmatch(r"v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?", version)
    if not match or tuple(map(int, match.groups())) < (2, 26, 0):
        raise InstallError("Docker Compose 2.26.0 or newer is required for literal Core environment values")


def check_host():
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64"):
        raise InstallError("Core installation requires Linux amd64 with Docker access")
    check_compose()
    try:
        run(["docker", "info", "--format", "{{.ServerVersion}}"], capture_output=True, timeout=30)
    except (OSError, subprocess.SubprocessError):
        raise InstallError("Cannot reach Docker as the current account. Check docker info, the daemon and this "
                           "account's Docker access; the installer does not require root or invoke sudo") from None


def image_names(mode, native, managed=False):
    if mode == "web-only":
        return ["web"]
    names = ["database"] if native else ["core", "database"]
    return names + (["web"] if mode == "all" else []) + (["ingress"] if managed else [])


def image_loader(manifest, bundle):
    def load(names):
        images = {}
        for name in names:
            message = "Preparing " + {"database": "PostgreSQL", "core": "Core", "web": "Web"}.get(name, name) + " image"
            step(message)
            with install_display.busy(message):
                images[name] = ensure_docker_image(manifest, name, lambda name=name: bundle / f"images/{name}.tar")
        return images
    return load


def prepare_node_payload(root, state, bundle):
    if state["mode"] == "core-only":
        return
    destination = root / "node-payload"
    # Each release remains immutable and addressable while old nodes retain it.
    # The only mutable publication is a small, atomically replaced active pointer.
    def publish(source):
        manifest = json.loads((source / "manifest.json").read_text())
        revision = manifest.get("source_commit", "")
        if not re.fullmatch(r"[0-9a-f]{40}", revision):
            raise InstallError("Invalid node payload release identity")
        metadata_names = ("node-install.pyz", "manifest.json", "SHA256SUMS", "runtime/seccomp.json")
        names = list(metadata_names)
        for logical in manifest.get("artifacts", {}):
            entry = artifact(manifest, logical)
            name = "artifacts/" + entry["filename"]
            path = source / name
            if path.exists():
                if (path.is_symlink() or not path.is_file()
                        or not path.resolve().is_relative_to(source.resolve())
                        or path.stat().st_size != entry["size"] or digest(path) != entry["sha256"]):
                    raise InstallError("Offline artifact verification failed: " + logical)
                names.append(name)
        target = destination / "releases" / revision
        if target.is_symlink() or target.parent.is_symlink():
            raise InstallError("Installed node payload differs; preserve it and inspect the distribution")
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        if target.exists():
            # Validate the complete published metadata and every existing declared
            # artifact before filling any absence. Existing bytes are immutable.
            for name in metadata_names:
                previous = target / name
                if (previous.parent.is_symlink() or previous.is_symlink() or not previous.is_file()
                        or digest(previous) != digest(source / name)):
                    raise InstallError("Installed node payload differs; preserve it and inspect the distribution")
            artifacts = target / "artifacts"
            if artifacts.is_symlink() or artifacts.exists() and not artifacts.is_dir():
                raise InstallError("Installed node artifact directory differs")
            missing = []
            for logical in manifest.get("artifacts", {}):
                entry = artifact(manifest, logical)
                name = "artifacts/" + entry["filename"]
                previous = target / name
                if previous.is_symlink() or previous.exists() and (not previous.is_file()
                        or previous.stat().st_size != entry["size"] or digest(previous) != entry["sha256"]):
                    raise InstallError("Installed node artifact differs; refusing repair")
                if not previous.exists() and name in names:
                    missing.append((name, entry))
            if missing:
                artifacts.mkdir(mode=0o700, exist_ok=True)
                for name, entry in missing:
                    descriptor, temporary = tempfile.mkstemp(prefix=".payload-", dir=artifacts)
                    try:
                        with os.fdopen(descriptor, "wb") as outgoing, (source / name).open("rb") as incoming:
                            shutil.copyfileobj(incoming, outgoing)
                            outgoing.flush()
                            os.fsync(outgoing.fileno())
                        if Path(temporary).stat().st_size != entry["size"] or digest(Path(temporary)) != entry["sha256"]:
                            raise InstallError("Node artifact changed during repair")
                        # Publish without replacing bytes introduced concurrently.
                        os.link(temporary, target / name)
                    finally:
                        os.unlink(temporary)
                for directory in (artifacts, target):
                    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
                    try:
                        os.fsync(descriptor)
                    finally:
                        os.close(descriptor)
            return revision
        with tempfile.TemporaryDirectory(prefix=".payload-", dir=target.parent) as temporary:
            stage = Path(temporary) / "release"
            stage.mkdir(mode=0o700)
            for name in names:
                path = source / name
                if path.is_symlink() or not path.is_file():
                    raise InstallError("Invalid node payload source file")
                copied = stage / name
                copied.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                with path.open("rb") as incoming, copied.open("xb") as outgoing:
                    os.chmod(copied, 0o600)
                    shutil.copyfileobj(incoming, outgoing)
                    outgoing.flush()
                    os.fsync(outgoing.fileno())
            os.rename(stage, target)
        return revision

    if destination.is_symlink():
        raise InstallError("Invalid node payload directory")
    destination.mkdir(mode=0o700, exist_ok=True)
    revision = publish(bundle)
    pointer = destination / "active.json"
    if pointer.is_symlink():
        raise InstallError("Invalid active node payload pointer")
    if pointer.exists():
        if json.loads(pointer.read_text()) != {"source_commit": revision}:
            raise InstallError("Installed node payload differs; preserve it and inspect the distribution")
        return
    descriptor, temporary = tempfile.mkstemp(prefix=".active-", dir=destination)
    try:
        with os.fdopen(descriptor, "w") as stream:
            json.dump({"source_commit": revision}, stream)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, pointer)
        descriptor = os.open(destination, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
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
    if not root.exists() or not any(path.name != ".oac.lock" for path in root.iterdir()):
        return "empty"
    if (root / "state.json").exists():
        try:
            complete = json.loads((root / "state.json").read_text()).get("complete")
        except (OSError, ValueError, AttributeError):
            complete = None
        # create() writes state.json before anything else, with complete: false; the first
        # successful start sets it.
        if complete is not True:
            return "incomplete"
        return "config" if (root / "config.json").exists() else "missing-config"
    # Without state.json nothing here is known to be the installer's, so nothing is taken over or removed.
    return "config" if (root / "config.json").exists() else "other"


def written_state(root):
    """state.json, or None before create() wrote it; then the directory holds only the lock."""
    return oac_cli.load_state(root) if (root / "state.json").exists() else None


def remove_created(root, state, created):
    """Remove what this run created; returns the line printed after its error."""
    try:
        if state is not None:
            oac_cli.remove(root, state, keep_root=not created)
        elif created:
            # Never remove anything else from a directory without this installation's state.json.
            with contextlib.suppress(OSError):
                (root / ".oac.lock").unlink()
                root.rmdir()
    except oac_cli.OacError as left:
        return str(left)
    return NOTHING_KEPT


def create(root, args, config, manifest, images):
    """Write the new installation's state.json, secrets and config.json, in that order."""
    mode = config["mode"]
    token = read_core_key_file(args.core_key_file) if mode == "web-only" else secrets.token_hex(32)
    if root.parent == Path.home() / ".oac":
        oac_cli.private_parent(root)
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(root, 0o700)
    state = {"format": 2, "installation_id": str(uuid.uuid4()), "project": "oac-" + secrets.token_hex(5),
             "uid": os.getuid(), "gid": os.getgid(), "mode": mode, "native_core": config.get("native_core", False),
             "source_commit": manifest["source_commit"], "images": images, "secrets_sha256": {},
             "core_installation_id": None, "generated": {}, "complete": False}
    if ingress_config.enabled(config):
        state["ingress"] = ingress_config.preflight()
    # state.json first, written whole: it marks everything after it as this installation's.
    oac_cli.save_state(root, state)
    for name in ["secrets", "generated"] + ([] if mode == "web-only" else ["state", "state/e2b"]):
        (root / name).mkdir(mode=0o700)
    write = oac_cli.create_private
    write(root / "secrets/core.key", token)
    if mode != "web-only":
        write(root / "secrets/credential.key", base64.b64encode(secrets.token_bytes(32)).decode())
        write(root / "secrets/database.password", secrets.token_hex(32))
    if ingress_config.enabled(config):
        ingress_config.prepare(root)
    digests = configuration.secret_digests(root, mode)
    digests.pop("core.key")
    oac_cli.save_state(root, dict(state, secrets_sha256=digests))
    # config.json last: whenever it exists, the installation can be repaired.
    write(root / "config.json", json.dumps(config, indent=2) + "\n")


def finish(root, bundle, manifest, fresh=False, selection=None, moved=()):
    """Repair and start this release while the installer holds the installation lock."""
    state = oac_cli.load_state(root)
    step("Preparing service files")
    prepare_node_payload(root, state, bundle)
    native_service.prepare(root, state, bundle)
    native_installers.prepare(root, state, bundle)
    install_oac(root, bundle)
    if ingress_config.enabled(oac_cli.load_config(root)):
        ingress_config.prepare(root)
    args = argparse.Namespace(dry_run=False, yes=False, confirm_public_url_change=None)
    step("Applying settings and starting services as needed")
    try:
        oac_cli._apply(root, args, False, True, sys.stdin.isatty(),
                       lambda message: print(message, flush=True), retry=f"rerun ./install.sh --install-dir {root}")
    except oac_cli.ApplyFailed as error:
        if not fresh:
            raise
        # The installer removes what it created and says how to retry.
        raise InstallError(f"The services did not start: {error.cause}") from None
    config = oac_cli.load_config(root)
    mode = config["mode"]
    if mode == "web-only":
        step("Checking Core connection and authentication")
    if mode == "web-only" and oac_cli.paired_core(root, config)[0] != 200:
        where = "--core-key-file and the Core URL" if fresh else "secrets/core.key and web.core_url"
        raise InstallError(f"Core key authentication failed. Inspect {where}; no model was called")
    if fresh:
        # The first start finished, Web-only reaching its Core with the key: from now on the installation is kept.
        oac_cli.save_state(root, dict(oac_cli.load_state(root), complete=True))
    deployment = failure = None
    if selection:
        step("Configuring sandbox backend")
        try:
            deployment = sandbox_setup.initialize(root, config, state, selection)
        except sandbox_setup.SandboxSetupError as error:
            failure = error
    summary(root, config, fresh, selection, deployment, incomplete=failure is not None, moved=moved)
    if failure:
        raise InstallError(f"{str(failure).rstrip('.')}. Services are installed and running; "
                           f"choose the sandbox backend {choose_where(mode)}")


def summary(root, config, fresh, selection=None, deployment=None, incomplete=False, moved=()):
    mode, public_url, ports = config["mode"], config["public_url"], config["ports"]
    addresses = []
    if mode != "core-only":
        # Web accepts only its configured origin.
        console = ingress_config.console_origin(config) if ingress_config.enabled(config) else configuration.web_origin(config)
        addresses.append("Console: " + console + (" (local only)" if loopback_origin(console) else ""))
    if mode != "web-only":
        api = configuration.service_origin(config, "core") + "/v1"
        if public_url and not loopback_origin(public_url):
            label = "Local-only API on this host: " if configuration.loopback_listener(config["host"]) else "Direct API on this host: "
            addresses += ["API base URL: " + public_url + "/v1", label + api]
        elif public_url and origin_port(public_url) != ports.get("web"):
            addresses.append("API base URL: " + public_url + "/v1 (local only)")
        else:
            # The loopback Web port does not serve the public API.
            addresses.append("API base URL: " + api + " (local only)")
    install_output.summary(root, config, addresses, fresh, selection, deployment,
                           nodes_reach(public_url), incomplete, moved)


def main(argv=None):
    args = arguments(argv)
    root = args.install_dir
    if root.is_symlink() or root.resolve() != root:
        raise InstallError("Installation directory must be canonical and not a symlink")
    bundle = Path(__file__).resolve().parent
    # Settings and listeners take seconds to check, so they come before hashing the bundle.
    prepared = None
    if layout(root) == "empty":
        step("Checking installation settings")
        prepared = prepare_fresh(args)
    step("Verifying installation files")
    with install_display.busy("Verifying installation files"):
        manifest = verify_bundle(bundle)
    # Refuse foreign state before even creating a lock; repeat under the lock to
    # protect against another current installer finishing between these reads.
    check_release(root, manifest)
    if root.parent == Path.home() / ".oac":
        root.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    created = not root.exists()
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    with oac_cli.locked(root):
        check_release(root, manifest)
        install_locked(args, root, bundle, manifest, prepared, created)


def check_release(root, manifest):
    if (root / "state.json").exists():
        state = oac_cli.load_state(root)
        if state.get("complete") is True and state.get("source_commit") != manifest["source_commit"]:
            raise InstallError(oac_cli.UNSUPPORTED_VERSION)


def prepare_fresh(args):
    document = seed_document(args)
    choice = check_flags(args, document)
    config = seed_config(args, document)
    check_public_url(config, choice)
    config, moved = check_listeners(args, document, config)
    if config["mode"] == "web-only":
        key = read_core_key_file(args.core_key_file)
        if oac_cli.core_installation(config["web"]["core_url"], key)[0] == 404:
            raise InstallError("The paired Core version is not supported; preserve its data and reinstall "
                               "the current release separately. Nothing was changed.")
    e2b = ({"api_key": read_private_file(args.e2b_api_key_file, "E2B API key file"), "template": args.e2b_template,
            **({"api_url": args.e2b_api_url, "domain": args.e2b_domain} if args.e2b_api_url else {})}
           if choice == "e2b" else None)
    if choice == "docker":
        confirm_docker(args.accept_docker_risks)
    return config, choice, e2b, moved


def install_locked(args, root, bundle, manifest, prepared, created):
    kind = layout(root)
    if kind == "config":
        if args.given:
            raise InstallError(f"This installation is configured by {root / 'config.json'}. Edit it and run "
                               f"{root / 'oac'} apply; install.sh accepts only --install-dir to repair it")
        state = oac_cli.load_state(root)
        if state["mode"] == "web-only" and oac_cli.paired_core(root, oac_cli.load_config(root))[0] == 404:
            raise InstallError("The paired Core version is not supported; preserve its data and reinstall "
                               "the current release separately. Nothing was changed.")
        step("Checking host requirements for repair")
        check_host()
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
    if kind == "other":
        raise InstallError("Installation directory is not empty; refusing to overwrite existing state")
    if kind == "incomplete":
        # A first installation stopped without its cleanup, such as by kill -9 or power loss.
        step("Removing an incomplete earlier installation")
        oac_cli.remove(root, oac_cli.load_state(root), keep_root=True)
    if prepared is None:
        # Checked only now that the earlier installation is gone, so the ports it held count as free.
        step("Checking installation settings")
        prepared = prepare_fresh(args)
    try:
        install_fresh(args, root, bundle, manifest, prepared)
    except (Exception, KeyboardInterrupt) as error:
        state = written_state(root)
        if state and state.get("complete") is True:
            raise
        # A first installation that did not finish removes what it created, before printing
        # anything, so the same command can run again. The error goes on with the outcome.
        error.removal = remove_created(root, state, created)
        raise


def install_fresh(args, root, bundle, manifest, prepared):
    """Check the host, load images, create the installation and start it for the first time."""
    config, choice, e2b, moved = prepared
    step("Checking host requirements")
    check_host()
    if config.get("native_core"):
        native_service.preflight(bundle, root)
    if ingress_config.enabled(config):
        ingress_config.preflight()
    selection = None if choice == "none" else sandbox_setup.selection(bundle, manifest, choice, e2b)
    images = image_loader(manifest, bundle)(image_names(config["mode"], config.get("native_core", False), ingress_config.enabled(config)))
    step("Creating installation settings and credentials")
    create(root, args, config, manifest, images)
    finish(root, bundle, manifest, fresh=True, selection=selection, moved=moved)


def interrupted(signum, frame):
    raise KeyboardInterrupt


if __name__ == "__main__":
    # SIGTERM and SIGHUP, such as from a dropped SSH session, stop the installer as Ctrl-C does,
    # so a new installation still removes what it created.
    for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, interrupted)
    try:
        main()
    except (*REPORTED, OSError, ValueError, KeyError, subprocess.CalledProcessError, KeyboardInterrupt) as error:
        with contextlib.suppress(OSError):  # The terminal may be gone.
            install_display.error(error_text(error))
        sys.exit(130 if isinstance(error, KeyboardInterrupt) else 1)
    except Exception as error:
        # An unexpected error keeps its traceback, followed by what became of a new installation.
        traceback.print_exc()
        if getattr(error, "removal", None):
            print(error.removal, file=sys.stderr)
        sys.exit(1)
