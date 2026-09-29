#!/usr/bin/env python3
"""Install and enroll one node from its Core console's matched distribution."""
import argparse
import codecs
import contextlib
import ctypes
import errno
import fcntl
import getpass
import grp
import hashlib
import http.client
import io
import ipaddress
import json
import os
from pathlib import Path
import platform
import pwd
import re
import selectors
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from urllib.parse import urlencode, urlsplit
import uuid

import distribution
import node_spec
import node_generations


class InstallError(Exception):
    pass


class RuntimeDownloadError(InstallError):
    """A fixed private helper exit category for transfer/provenance failures."""


COMMON = ("native/bin/oac-node", "runtime/seccomp.json")
MICRO = ("native/bin/oac-microsandbox-provider", "native/microsandbox/msb",
         "native/microsandbox/libkrunfw.so.5.6.1")
DOCKER = ("docker", "--host", "unix:///var/run/docker.sock")
DOCKER_SOCKET = Path("/var/run/docker.sock")
KVM = Path("/dev/kvm")

# Sudo mode: run as root, the installer prepares the host itself. The node runs as
# this dedicated service user under a root-owned system unit; root never runs a
# file the service user can write.
SERVICE_USER = "oac-node"
SERVICE_HOME = Path("/var/lib/oac-node")
SYSTEM_RECORDS = Path("/etc/oac-node")
SYSTEM_UNITS = Path("/etc/systemd/system")
SYSTEM_LOCKS = Path("/run")
SYSTEMD_RUNNING = Path("/run/systemd/system")
SELINUX_ENFORCE = Path("/sys/fs/selinux/enforce")
USER_RUNTIME = Path("/run/user")
SAFE_PATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
# A root-owned, empty Docker configuration for the service user's docker calls, so no
# CLI plugin or credential helper the service user controls runs in the installer.
CHILD_DOCKER_CONFIG = Path("/run/oac-node-docker")
LOCK_WAIT_SECONDS = 600
NOTHING_CHANGED = " Nothing was changed."


def origin(value):
    try:
        parsed = urlsplit(value)
        parsed.port
    except ValueError:
        raise argparse.ArgumentTypeError("Invalid Core origin") from None
    try:
        local = parsed.hostname == "localhost" or ipaddress.ip_address(parsed.hostname).is_loopback
    except (ValueError, TypeError):
        local = parsed.hostname == "localhost"
    if (parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.path not in ("", "/")
            or any(c.isspace() for c in value) or any(c in value for c in "?#\\")
            or (parsed.scheme == "http" and not local)):
        raise argparse.ArgumentTypeError("Use an HTTPS origin, or loopback HTTP for a local node")
    return value.rstrip("/")


def checked(arguments, failure, explain=None, **kwargs):
    # explain(stderr) may replace the failure with an InstallError carrying fixed text only.
    try:
        result = subprocess.run(arguments, check=False, stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=kwargs.pop("timeout", 30), **kwargs)
        if result.returncode:
            raise explain(result.stderr) if explain else InstallError(failure)
        return result.stdout.decode().strip()
    except (OSError, subprocess.SubprocessError):
        raise InstallError(failure) from None


REGISTRATION_UNCONFIRMED = ("Node enrollment was not confirmed. Check the Core URL, enrollment expiry and local provider "
                            "prerequisites; keep its state and rerun the command to recover.")
# The node program's rejection line: Core's status and fixed error code, nothing else.
REJECTION = re.compile(rb"node enrollment rejected \(HTTP (\d{3})(?: ([a-z_]{1,64}))?\)")


class AddressChanged(InstallError):
    """Core refused the node's address before consuming the token, so it has no record of this node."""


def registration_failure(stderr):
    """A fixed message for Core's answer to registration; never the node program's own text."""
    matches = list(REJECTION.finditer(stderr or b""))
    if not matches:
        return InstallError(REGISTRATION_UNCONFIRMED)
    status, code = matches[-1].group(1).decode(), (matches[-1].group(2) or b"").decode()
    if code == "sandbox_node_address_mismatch":
        return AddressChanged(node_spec.PUBLIC_URL_CHANGED + " The token was not used; downloaded files are kept.")
    if status == "401":
        return InstallError("The enrollment command expired or was already used. Generate a new command on the Nodes "
                            "page and run it on this host; downloaded files are kept.")
    return InstallError(REGISTRATION_UNCONFIRMED + " Core answered HTTP " + status + (" " + code if code else "") + ".")


def discard_unregistered(root):
    """Remove the files that name the old address, so a new command can register this host.

    Only for a node Core never recorded: downloads, the image and the service file stay."""
    for name in ("installation.json", "provider.json"):
        if existing_file(root / name):
            (root / name).unlink()
    if (root / "state/node").is_dir() and not (root / "state/node").is_symlink():
        shutil.rmtree(root / "state/node")


def preflight(provider, system=False):
    """Host access for the node's own user; a system service needs no user session."""
    if sys.version_info < (3, 9) or platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64") or os.getuid() == 0:
        raise InstallError("Run with Python 3.9+ as a non-root user on Linux amd64")
    if not system:
        user_bus()
        checked(["systemctl", "--user", "show", "--property=Version", "--value"], "A systemd user session is required")
        if checked(["loginctl", "show-user", str(os.getuid()), "--property=Linger", "--value"], "Cannot check user lingering") != "yes":
            raise InstallError("Ask the host administrator to enable user lingering before installing a node, or run the command with sudo")
    if provider == "docker":
        checked(list(DOCKER) + ["info", "--format", "{{.ServerVersion}}"], "Docker access through /var/run/docker.sock is required")
    elif not os.access(KVM, os.R_OK | os.W_OK):
        raise InstallError("microsandbox requires read/write access to /dev/kvm")


def user_bus():
    """Reach this user's systemd manager from a session that has no bus address.

    su or sudo -iu from a root shell leaves XDG_RUNTIME_DIR unset; lingering keeps the
    user manager and its bus running under /run/user/<uid>."""
    if os.environ.get("XDG_RUNTIME_DIR"):
        return
    runtime = USER_RUNTIME / str(os.getuid())
    bus = runtime / "bus"
    try:
        info = bus.lstat()
    except OSError:
        info = None
    if info is None or not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid():
        user = pwd.getpwuid(os.getuid()).pw_name
        raise InstallError("No systemd user manager is running for " + user + ". Ask the host administrator to run "
                           "`sudo loginctl enable-linger " + user + "`, or run the command with sudo.")
    os.environ["XDG_RUNTIME_DIR"] = str(runtime)
    os.environ["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + str(bus)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        raise InstallError("Node bootstrap redirects are not supported")


def open_request(request, timeout=15):
    return urllib.request.build_opener(NoRedirect()).open(request, timeout=timeout)


def transient(error):
    if isinstance(error, urllib.error.HTTPError):
        return error.code in (408, 429, 500, 502, 503, 504)
    return isinstance(error, (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead))


def fetch(source, name):
    for attempt in range(3):
        try:
            with open_request(source + "/node-install/" + name) as response:
                raw = response.read(1024 * 1024 + 1)
            if len(raw) > 1024 * 1024:
                raise RuntimeDownloadError("Node bootstrap metadata is too large: " + name)
            return io.BytesIO(raw)
        except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead) as error:
            if not transient(error) or attempt == 2:
                status = " (HTTP " + str(error.code) + ")" if isinstance(error, urllib.error.HTTPError) else ""
                raise RuntimeDownloadError("Cannot download node metadata " + name + status + "; check the console URL, TLS and network, then rerun") from None
            time.sleep(attempt + 1)


def metadata(source, bundle=None, prefix=""):
    if bundle is not None:
        def read(name):
            path = bundle / name
            if path.is_symlink() or not path.is_file() or not path.resolve().is_relative_to(bundle):
                raise InstallError("Invalid local distribution metadata")
            return path.open("rb")
    else:
        read = lambda name: fetch(source, prefix + name)
    with read("SHA256SUMS") as response:
        raw = response.read(1024 * 1024 + 1)
    if len(raw) > 1024 * 1024:
        raise RuntimeDownloadError("Invalid distribution checksum list")
    sums = {}
    try:
        for line in raw.decode().splitlines():
            checksum, name = line.split("  ", 1)
            if name in sums or not re.fullmatch(r"[0-9a-f]{64}", checksum):
                raise RuntimeDownloadError("Invalid distribution checksum entry")
            sums[name] = checksum
    except ValueError:
        raise RuntimeDownloadError("Invalid distribution checksum list") from None
    with read("manifest.json") as response:
        raw = response.read(1024 * 1024 + 1)
    if len(raw) > 1024 * 1024 or hashlib.sha256(raw).hexdigest() != sums.get("manifest.json"):
        raise RuntimeDownloadError("Distribution manifest checksum mismatch")
    try:
        manifest = json.loads(raw)
    except (ValueError, TypeError):
        raise RuntimeDownloadError("Invalid distribution manifest") from None
    if (not isinstance(manifest, dict) or manifest.get("platform") != "linux/amd64" or not re.fullmatch(r"[0-9a-f]{40}", manifest.get("source_commit", ""))
            or not re.fullmatch(r"sha256:[0-9a-f]{64}", manifest.get("images", {}).get("runtime", ""))):
        raise RuntimeDownloadError("Unsupported node distribution")
    distribution.image_identities(manifest, "runtime")
    for name in (COMMON[0], "images/runtime.tar.gz") + MICRO:
        distribution.artifact(manifest, name)
    # Metadata stays on the console; artifact requests may redirect to its pinned release.
    manifest["artifact_base_url"] = source + "/node-install/releases/" + manifest["source_commit"] + "/artifacts" if source else ""
    return manifest, sums


def safe_directory(path):
    if not path.is_absolute() or path.resolve() != path or any(ord(c) < 32 or c in "\\*?[]" for c in str(path)):
        raise InstallError("Node installation path must be canonical without symlinks or control characters")
    path.mkdir(parents=True, mode=0o700, exist_ok=True)
    if not path.is_dir() or path.stat().st_uid != os.getuid():
        raise InstallError("Node installation directory must be owned by this user")
    os.chmod(path, 0o700)


def existing_file(path):
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise InstallError("Node installation files must be regular files, never symlinks")
    if path.exists() and (path.stat().st_uid != os.getuid() or stat.S_IMODE(path.stat().st_mode) & 0o077):
        raise InstallError("Node installation files must be private and owned by this user")
    return path.exists()


def write_once(path, value):
    if existing_file(path):
        if path.read_text() != value:
            raise InstallError("Existing node configuration differs; preserve its state and use the upgrade guide")
        return
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        stream.write(value)


def json_text(value):
    return json.dumps(value, indent=2, sort_keys=True) + "\n"


def file_digest(path):
    result = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def download(source, name, root, expected, prefix=""):
    target = root / name
    safe_directory(target.parent)
    if existing_file(target):
        if file_digest(target) != expected:
            raise RuntimeDownloadError("Installed node payload differs; refusing to overwrite it")
        return
    descriptor, temporary = tempfile.mkstemp(prefix=".download-", dir=target.parent)
    try:
        with os.fdopen(descriptor, "wb") as output, fetch(source, prefix + name) as response:
            for block in iter(lambda: response.read(1024 * 1024), b""):
                output.write(block)
        if file_digest(Path(temporary)) != expected:
            raise RuntimeDownloadError("Node payload checksum mismatch: " + name)
        os.replace(temporary, target)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    if name.startswith("native/"):
        os.chmod(target, 0o700)


def micro_home(installation_id):
    directory = Path.home() / ".oac/m" / hashlib.sha256(installation_id.encode()).hexdigest()[:12]
    if len(os.fsencode(directory)) > 48:
        raise InstallError("HOME is too long for microsandbox Unix socket paths; use a user with a shorter persistent home directory")
    return directory


def provider_config(root, args, manifest, runtime_image):
    result = {"installation_id": args.installation_id, "provider": args.provider, "core_url": args.core_url + "/api/v1",
              "specification": args.configuration["specification"], "generation": args.configuration["generation"]}
    if args.provider == "docker":
        result["docker"] = {"host": "unix:///var/run/docker.sock", "image": runtime_image,
                            "network": "oac-node-" + args.installation_id,
                            "seccomp_file": str(root / "runtime/seccomp.json"), "nested_sandbox": True}
    else:
        endpoint = urlsplit(args.core_url)
        port = endpoint.port or (443 if endpoint.scheme == "https" else 80)
        addresses = sorted({entry[4][0] for entry in socket.getaddrinfo(endpoint.hostname, port, type=socket.SOCK_STREAM)})
        core_rules = [{"action": "allow", "direction": "egress", "destination": address, "protocol": "tcp", "port": str(port)} for address in addresses]
        result["microsandbox"] = {
            "helper_path": str(root / MICRO[0]), "runtime_path": str(root / MICRO[1]), "firmware_path": str(root / MICRO[2]),
            "runtime_sha256": manifest["microsandbox"]["runtime_sha256"], "firmware_sha256": manifest["microsandbox"]["firmware_sha256"],
            "runtime_home": str(getattr(args, "runtime_home", micro_home(args.installation_id))), "image": manifest["runtime_ref"],
            **args.configuration["specification"]["resources"],
            "network": {"default_egress": "deny", "default_ingress": "deny", "rules": core_rules + [
                {"action": "allow", "direction": "egress", "destination": "public"},
                {"action": "allow", "direction": "egress", "destination": "host", "protocol": "udp", "port": "53"},
                {"action": "allow", "direction": "egress", "destination": "host", "protocol": "tcp", "port": "53"},
            ]},
        }
    return result


def prepare_runtime(root, args, manifest):
    if args.provider == "docker":
        docker = ["docker", "--host", "unix:///var/run/docker.sock"]
        image = distribution.ensure_docker_image(
            manifest, "runtime", lambda: distribution.runtime_archive(manifest, root, getattr(args, "bundle", None)), docker)
        network = "oac-node-" + args.installation_id
        networks = checked(docker + ["network", "ls", "--format", "{{.Name}}"], "Cannot inspect Docker networks").splitlines()
        if network not in networks:
            checked(docker + ["network", "create", network], "Cannot create node Docker network")
        return image
    else:
        for name in MICRO:
            result = subprocess.run(["ldd", str(root / name)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=30)
            output = result.stdout.decode()
            if "not found" in output or (result.returncode and "statically linked" not in output and "not a dynamic executable" not in output):
                raise InstallError("Install the microsandbox host shared-library prerequisites")
        env = dict(os.environ, MSB_BACKEND="local", MSB_HOME=str(getattr(args, "runtime_home", micro_home(args.installation_id))),
                   MSB_PATH=str(root / MICRO[1]), MSB_LIBKRUNFW_PATH=str(root / MICRO[2]))
        inspect = [str(root / MICRO[1]), "image", "inspect", manifest["runtime_ref"], "--format", "json"]
        def matches():
            try:
                value = json.loads(checked(inspect, "Runtime image is not installed", env=env))
                return (value.get("digest") == manifest["runtime_ref"].split("@", 1)[1]
                        and value.get("architecture") == "amd64" and value.get("os") == "linux")
            except (InstallError, ValueError, AttributeError):
                return False
        if not matches():
            archive = distribution.runtime_archive(manifest, root, getattr(args, "bundle", None))
            checked([str(root / MICRO[1]), "image", "load", "--input", str(archive), "--tag", manifest["runtime_ref"], "--quiet"],
                    "Cannot import the microsandbox runtime image; check free disk space and host libraries", timeout=1800, env=env)
            if not matches():
                raise InstallError("Imported microsandbox runtime image identity or platform differs")


def service_unit(root):
    def quote(value):
        return '"' + str(value).replace("\\", "\\\\").replace('"', '\\"').replace("%", "%%") + '"'
    # The node keeps retrying while Core is unreachable (no start limit). It exits 78
    # when Core rejects its credential, as after removal; that ends the restarts.
    return ("[Unit]\nDescription=OpenAgentCore sandbox node\nStartLimitIntervalSec=0\n\n[Service]\nType=exec\nExecStart=:"
            + quote(root / COMMON[0]) + " run --config " + quote(root / "provider.json") + " --state-dir " + quote(root / "state/node")
            + "\nWorkingDirectory=" + str(root).replace("%", "%%")
            + "\nRestart=on-failure\nRestartSec=5s\nRestartPreventExitStatus=78\nKillMode=process\nUMask=0077"
            + "\n\n[Install]\nWantedBy=default.target\n")


def unit_name(installation_id):
    return "oac-node-" + installation_id + ".service"


def open_node(args, token, system=False):
    """Read the Core specification and check the host; returns the node's state directory."""
    root = Path.home() / ".oac/nodes" / args.installation_id
    safe_directory(root)
    identity_file = root / "state/node/identity.json"
    retained = json.loads(identity_file.read_text()) if existing_file(identity_file) else None
    if not system:  # In sudo mode, root has already said so.
        print("Reading the Core deployment specification...", flush=True)
    args.configuration = node_spec.fetch(args, token, retained, open_request, allow_enrollment=not (root / "registered.json").exists())
    args.provider = args.configuration["provider"]
    if not system:
        print("Checking host requirements...", flush=True)
    preflight(args.provider, system)
    if args.provider == "microsandbox":
        runtime_home = micro_home(args.installation_id)
        safe_directory(runtime_home)
        owner = runtime_home / "oac-installation.json"
        if not owner.exists() and any(runtime_home.iterdir()):
            raise InstallError("Microsandbox home contains unowned state; refusing to adopt it")
        write_once(owner, json_text({"installation_id": args.installation_id}))
    return root


@contextlib.contextmanager
def install_lock(root):
    descriptor = os.open(root / "install.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise InstallError("Another node installation is running") from None
        yield


def register_node(root, args, token, helper_archive=None):
    """Download and verify the payload, prepare the Runtime and register; not the service."""
    print("Downloading and verifying node files...", flush=True)
    program_manifest, program_sums = metadata(args.source_url, getattr(args, "bundle", None))
    selected = args.configuration["specification"]["runtime"]
    manifest, sums = program_manifest, program_sums
    if selected["source_commit"] != program_manifest["source_commit"]:
        if getattr(args, "bundle", None) is not None:
            raise InstallError("This bundle does not contain Core's selected Runtime; install through the console --source-url that retains its release")
        manifest, sums = metadata(args.source_url, prefix="releases/" + selected["source_commit"] + "/")
    node_spec.verify_release(args.configuration, manifest)
    runtime_prefix = "releases/" + manifest["source_commit"] + "/"
    names = COMMON + (MICRO if args.provider == "microsandbox" else ())
    if "runtime/seccomp.json" not in sums:
        raise InstallError("The distribution is missing required node checksums")
    state = {"installation_id": args.installation_id, "provider": args.provider, "core_url": args.core_url,
             "source_commit": manifest["source_commit"], "generation": args.configuration["generation"],
             "specification_digest": args.configuration["specification_digest"]}
    write_once(root / "installation.json", json_text(state))
    node_generations.record_root_runtime(root, args, manifest, sums, sys.modules[__name__])
    for name in names:
        if name == "runtime/seccomp.json":
            if getattr(args, "bundle", None) is None:
                download(args.source_url, name, root, sums[name], prefix=runtime_prefix)
            else:
                source = args.bundle / name
                if source.is_symlink() or file_digest(source) != sums[name]:
                    raise InstallError("Local node payload checksum mismatch")
                safe_directory((root / name).parent)
                write_once(root / name, source.read_text())
        else:
            target = root / name
            safe_directory(target.parent)
            existing_file(target)
            distribution.obtain_artifact(program_manifest if name == COMMON[0] else manifest, name, target, getattr(args, "bundle", None))
            os.chmod(target, 0o700)
    node_generations.install_helper(root, args, sys.modules[__name__], helper_archive)
    safe_directory(root / "state/node")
    lease_identity = {"installation_id": args.installation_id, "generation": args.configuration["generation"],
                      "specification_digest": args.configuration["specification_digest"]}
    with node_generations.collection_lease(root, args.configuration["generation"], sys.modules[__name__], lease_identity,
                                           initialize=not (root / "provider.json").exists()):
        pass
    print("Checking the sandbox runtime...", flush=True)
    runtime_image = prepare_runtime(root, args, manifest)
    # Retain the original network policy when recovering a partial installation.
    if not existing_file(root / "provider.json"):
        write_once(root / "provider.json", json_text(provider_config(root, args, manifest, runtime_image)))
    else:
        node_spec.verify_provider(json.loads((root / "provider.json").read_text()), args.configuration, runtime_image)
    marker = root / "registered.json"
    if not existing_file(marker):
        # The one-time credential is never passed through process arguments or service environments.
        descriptor, secret_path = tempfile.mkstemp(prefix=".enrollment-", dir=root)
        try:
            with os.fdopen(descriptor, "w") as secret:
                secret.write(token)
            print("Registering this node with Core...", flush=True)
            try:
                checked([str(root / COMMON[0]), "register", "--config", str(root / "provider.json"), "--state-dir", str(root / "state/node"),
                         "--core-url", args.core_url, "--name", socket.gethostname(),
                         "--enrollment-token-file", secret_path], REGISTRATION_UNCONFIRMED, explain=registration_failure)
            except AddressChanged:
                discard_unregistered(root)
                raise
            write_once(marker, json_text(state))
        finally:
            if os.path.exists(secret_path):
                os.unlink(secret_path)
    elif marker.read_text() != json_text(state):
        raise InstallError("Registered node identity differs; refusing to replace it")


# These paths are inspected only to refuse an unremoved node from an older
# release. They are never adopted, rewritten or removed by this installer.
LEGACY_RECORDS = Path("/etc/parsar-node")
LEGACY_SERVICE_HOME = Path("/var/lib/parsar-node")


def legacy_path_present(path):
    """Inspect only metadata, without following links in user-controlled directories."""
    descriptors = []
    try:
        descriptors.append(os.open(path.anchor, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW))
        for part in path.parts[1:-1]:
            descriptors.append(os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                                       dir_fd=descriptors[-1]))
        os.stat(path.name, dir_fd=descriptors[-1], follow_symlinks=False)
        return True
    except FileNotFoundError:
        return False
    finally:
        for descriptor in reversed(descriptors):
            os.close(descriptor)


def refuse_legacy_node(args):
    """Reject pre-rename resources for this installation, leaving every other one alone."""
    installation = args.installation_id
    unit = "parsar-node-" + installation + ".service"
    homes = {Path.home()}
    if os.geteuid() == 0:
        sudo_user = os.environ.get("SUDO_USER", "")
        if sudo_user and sudo_user != "root":
            try:
                home = Path(pwd.getpwnam(sudo_user).pw_dir)
                if home.is_absolute():
                    homes.add(home)
            except KeyError:
                pass
    paths = [LEGACY_RECORDS / (installation + ".json"), SYSTEM_UNITS / unit]
    paths += [home / ".parsar/nodes" / installation for home in homes]
    paths += [home / ".config/systemd/user" / unit for home in homes]
    # Non-root users cannot inspect the old mode-0700 service home; its matching
    # root-owned record and system unit above remain observable without reading it.
    if os.geteuid() == 0 or os.access(LEGACY_SERVICE_HOME, os.R_OK | os.X_OK):
        paths.append(LEGACY_SERVICE_HOME / ".parsar/nodes" / installation)
    found = []
    for path in paths:
        try:
            if legacy_path_present(path):  # A dangling final symlink is still retained state.
                found.append(str(path))
        except OSError:
            raise InstallError("Cannot inspect possible legacy node state at " + str(path)
                               + "; check this path before installing." + NOTHING_CHANGED) from None
    if not found and shutil.which("systemctl") is not None:
        modes = [[], ["--user"]] if os.geteuid() != 0 else [[]]
        for mode in modes:
            result = subprocess.run(["systemctl", *mode, "show", unit, "--property=LoadState", "--value"],
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=10)
            if result.returncode == 0 and result.stdout.strip() not in ("", "not-found"):
                found.append(unit)
    # Inspect only the fixed local engine. A microsandbox user without Docker
    # access does not need that unrelated host capability to install its node.
    if not found and shutil.which("docker") is not None and DOCKER_SOCKET.exists() and (
            getattr(args, "provider", None) == "docker" or os.access(DOCKER_SOCKET, os.R_OK | os.W_OK)):
        network = "parsar-node-" + installation
        networks = checked(list(DOCKER) + ["network", "ls", "--format", "{{.Name}}"],
                           "Cannot inspect legacy node networks; check the local Docker engine." + NOTHING_CHANGED).splitlines()
        if network in networks:
            found.append("Docker network " + network)
    if found:
        raise InstallError("This host still has a node for this installation from before the OpenAgentCore rename ("
                           + ", ".join(found) + "). Remove it on the Nodes page, then uninstall it with the previous "
                           "release's node-install.pyz --uninstall --installation-id " + installation
                           + ", or follow \"Remove a node added before the rename\" in the node guide." + NOTHING_CHANGED)


def install(args, token):
    """Non-root mode: the node runs as this user's systemd user service."""
    try:
        system_node = (SYSTEM_RECORDS / (args.installation_id + ".json")).exists()
    except PermissionError:
        system_node = True
    if system_node:
        raise InstallError("This host already runs a node for this installation as a system service. Rerun the command "
                           "with sudo, or uninstall that node with sudo first." + NOTHING_CHANGED)
    refuse_legacy_node(args)
    root = open_node(args, token)
    with host_lock(), install_lock(root):
        register_node(root, args, token)
        unit = root / unit_name(args.installation_id)
        write_once(unit, service_unit(root))
        print("Starting the node service...", flush=True)
        checked(["systemctl", "--user", "daemon-reload"], "Cannot reload the systemd user manager")
        checked(["systemctl", "--user", "enable", "--now", str(unit)], "Cannot start the node service; retained identity is unchanged")
        checked(["systemctl", "--user", "is-active", "--quiet", unit.name], "Node service is unavailable; inspect its systemd user journal")
        print("Waiting for Core connection and provider readiness...", flush=True)
        wait_ready(root, args)
    print("Node connected to Core and provider ready. State: " + str(root))
    print("Logs: journalctl --user -u " + unit.name)


def prepare_service_node(args, token, helper_archive):
    """Sudo mode, as the service user: everything but the root-owned system unit."""
    root = open_node(args, token, system=True)
    with install_lock(root):
        register_node(root, args, token, helper_archive)



# Sudo mode -----------------------------------------------------------------

class ChildFailed(Exception):
    """The service-user step failed and already printed why."""


# What a service-user step must not send to the administrator's terminal: C0 controls
# other than tab and newline, DEL and C1. ESC starts OSC 52 clipboard writes, title
# changes and cursor moves; carriage returns and erases could hide or forge lines.
TERMINAL_CONTROLS = re.compile(r"[\x00-\x08\x0b-\x1f\x7f-\x9f]")


def plain(text, encoding=None):
    """Text the terminal shows as written, in an encoding the stream can write."""
    encoding = encoding or "utf-8"
    return TERMINAL_CONTROLS.sub("?", text).encode(encoding, "replace").decode(encoding)


def relay(output, errors):
    """Copy the child's stdout and stderr pipes to ours as plain text until both close."""
    streams = {output: (sys.stdout, codecs.getincrementaldecoder("utf-8")("replace")),
               errors: (sys.stderr, codecs.getincrementaldecoder("utf-8")("replace"))}
    with selectors.DefaultSelector() as selector:
        for descriptor in streams:
            selector.register(descriptor, selectors.EVENT_READ)
        while streams:
            for key, _ in selector.select():
                data = os.read(key.fd, 65536)
                stream, decoder = streams[key.fd]
                # The decoder keeps a character split across reads until its last byte arrives.
                text = plain(decoder.decode(data, final=not data), getattr(stream, "encoding", None))
                if text:
                    stream.write(text)
                    stream.flush()
                if not data:
                    selector.unregister(key.fd)
                    os.close(key.fd)
                    del streams[key.fd]


STOP_WAIT_SECONDS = 5


def signal_child(pid, number):
    # The child's own process group once it has started its session, and the child
    # itself before then. It is never reaped before this, so pid is still its own.
    for send in (os.killpg, os.kill):
        with contextlib.suppress(ProcessLookupError):
            send(pid, number)


def stop_child(pid):
    """End the child and everything it started; its own session gets no terminal signals."""
    def exited():
        # WNOWAIT leaves the child a zombie, so its process group ID cannot be reused
        # before the SIGKILL below reaches what the child left running.
        return os.waitid(os.P_PID, pid, os.WEXITED | os.WNOHANG | os.WNOWAIT) is not None
    try:
        exited()
    except ChildProcessError:
        return  # Already reaped.
    signal_child(pid, signal.SIGTERM)
    deadline = time.monotonic() + STOP_WAIT_SECONDS
    while not exited() and time.monotonic() < deadline:
        time.sleep(0.1)
    signal_child(pid, signal.SIGKILL)
    os.waitpid(pid, 0)


def interrupted(number, frame):
    raise InstallError(INTERRUPTED)


INTERRUPTED = "The command was interrupted; run it again to continue."
# The child's own session gets none of the terminal's signals, so while it runs the
# parent turns these into an error that stops the child before the parent exits.
STOP_SIGNALS = (signal.SIGINT, signal.SIGHUP, signal.SIGTERM)
KEYCTL_SYSCALL = 250  # x86_64, the only architecture this installer supports.
KEYCTL_JOIN_SESSION_KEYRING = 1
PR_SET_PDEATHSIG = 1


def libc_call(name, *arguments):
    function = getattr(ctypes.CDLL(None, use_errno=True), name)
    function.restype = ctypes.c_long
    if function(*(ctypes.c_long(argument) for argument in arguments)) == -1:
        number = ctypes.get_errno()
        raise OSError(number, os.strerror(number))


def as_service_user(account, function, *arguments):
    """Run a step with the service user's credentials in a forked child.

    The child starts a new session with /dev/null as standard input and pipes as
    output, so no program it runs holds the administrator's terminal (TIOCSTI); the
    parent shows that output only as plain text. Interrupting the installer or
    closing its terminal stops the child too, and the child dies with the parent.
    Every installer module is already imported, so the child never reads the root
    caller's private copy of this program. Its retained-helper bytes are captured
    before the fork, and the token stays in memory. Files the
    service user owns are read, written and deleted only here, never by root."""
    sys.stdout.flush()
    sys.stderr.flush()
    output_read, output_write = os.pipe()
    errors_read, errors_write = os.pipe()
    parent = os.getpid()
    previous = {number: signal.getsignal(number) for number in STOP_SIGNALS}
    # Held from before the fork until each side has its handlers, so neither a signal
    # nor the resulting error can reach the wrong process.
    mask = signal.pthread_sigmask(signal.SIG_BLOCK, STOP_SIGNALS)
    pid = None
    try:
        for number, handler in previous.items():
            if handler is not signal.SIG_IGN:  # As under nohup, an ignored signal stays ignored.
                signal.signal(number, interrupted)
        pid = os.fork()
        if pid == 0:
            service_child(account, function, arguments, parent, mask, (output_read, errors_read), output_write, errors_write)
        signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        os.close(output_write)
        os.close(errors_write)
        relay(output_read, errors_read)
        _, status = os.waitpid(pid, 0)
    except BaseException:
        for number in STOP_SIGNALS:  # A second signal must not cut the stop short.
            signal.signal(number, signal.SIG_IGN)
        if pid:
            stop_child(pid)
        raise
    finally:
        signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        for number, handler in previous.items():
            signal.signal(number, handler)
    if os.WIFSIGNALED(status):
        number = os.WTERMSIG(status)
        raise ChildFailed("The step running as " + SERVICE_USER + " was stopped by signal " + str(number)
                          + " (" + (signal.strsignal(number) or "unknown") + "). Run the command again to continue.")
    if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
        raise ChildFailed()


def service_child(account, function, arguments, parent, mask, read_ends, output_write, errors_write):
    """The forked child of as_service_user; it never returns into the root caller's code."""
    code = 1
    try:
        for number in STOP_SIGNALS:
            signal.signal(number, signal.SIG_DFL)
        signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        for descriptor in read_ends:
            os.close(descriptor)
        os.setsid()
        null = os.open(os.devnull, os.O_RDWR)
        os.dup2(null, 0)
        os.dup2(output_write, 1)
        os.dup2(errors_write, 2)
        for descriptor in (null, output_write, errors_write):
            if descriptor > 2:
                os.close(descriptor)
        sys.stdout = open(1, "w", buffering=1, closefd=False)
        sys.stderr = open(2, "w", buffering=1, closefd=False)
        os.setgroups(os.getgrouplist(account.pw_name, account.pw_gid))
        os.setgid(account.pw_gid)
        os.setuid(account.pw_uid)
        try:
            # The session keyring survives fork, setuid and exec and gives its possessor
            # the administrator's keys (keyrings(7)); join a new, empty one instead.
            libc_call("syscall", KEYCTL_SYSCALL, KEYCTL_JOIN_SESSION_KEYRING, 0)
        except OSError as error:
            if error.errno != errno.ENOSYS:  # Without kernel keyrings there is none to leave.
                raise InstallError("Cannot give the " + SERVICE_USER + " step its own session keyring: "
                                   + error.strerror + ". Inspect the host, then rerun the command.") from None
        # Set after setuid, which clears it: the child dies with the parent, and a
        # parent that already died is noticed here.
        libc_call("prctl", PR_SET_PDEATHSIG, signal.SIGKILL)
        if os.getppid() != parent:
            os._exit(1)
        os.umask(0o077)
        os.environ.clear()
        os.environ.update(HOME=account.pw_dir, USER=account.pw_name, LOGNAME=account.pw_name, PATH=SAFE_PATH,
                          LANG="C.UTF-8", DOCKER_CONFIG=str(CHILD_DOCKER_CONFIG))
        os.chdir(account.pw_dir)
        function(*arguments)
        code = 0
    except (InstallError, node_spec.SpecificationError, distribution.DistributionError) as error:
        print(str(error), file=sys.stderr)
    except Exception as error:  # noqa: BLE001 - the child must always report and exit
        print("The step running as " + SERVICE_USER + " failed unexpectedly (" + type(error).__name__
              + "). Inspect the host, then rerun the command.", file=sys.stderr)
    finally:
        # A closed output pipe must not skip the exit below.
        for stream in (sys.stdout, sys.stderr):
            with contextlib.suppress(BaseException):
                stream.flush()
        os._exit(code)


run_as = as_service_user


def child_docker_config():
    """Create the root-owned empty directory the service user's docker calls read."""
    path = CHILD_DOCKER_CONFIG
    if not path.exists() and not path.is_symlink():
        path.mkdir(mode=0o755)
        os.chmod(path, 0o755)
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o022:
        raise InstallError(str(path) + " must be a directory only root can write; inspect the host.")


@contextlib.contextmanager
def host_lock():
    """One node installation or uninstallation at a time on this host.

    Root takes it exclusively around every sudo-mode change, so installing one node
    never races removing another's account. Normal users share it when it exists.
    It lives in /run, which the next boot clears."""
    path = SYSTEM_LOCKS / "oac-node.lock"
    root = os.geteuid() == 0
    descriptor = None
    try:
        if root:
            descriptor = os.open(path, os.O_CREAT | os.O_RDONLY | os.O_NOFOLLOW, 0o644)
            os.fchmod(descriptor, 0o644)  # Readable by normal users despite the command's umask 077.
        else:
            descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    except OSError:
        if root:
            raise
    if descriptor is None:
        yield  # No sudo-mode run has happened on this host since boot.
        return
    with os.fdopen(descriptor) as lock:
        operation = fcntl.LOCK_EX if root else fcntl.LOCK_SH
        deadline = time.monotonic() + LOCK_WAIT_SECONDS
        waiting = False
        while True:
            try:
                fcntl.flock(lock, operation | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() > deadline:
                    raise InstallError("Another node installation or uninstallation on this host still holds "
                                       + str(path) + "; find it with `sudo fuser " + str(path) + "`, then rerun.") from None
                if not waiting:
                    print("Waiting for another node installation or uninstallation on this host...", flush=True)
                    waiting = True
                time.sleep(1)
        yield


def host_checks():
    if sys.version_info < (3, 9) or platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64"):
        raise InstallError("Run with Python 3.9+ on Linux amd64." + NOTHING_CHANGED)
    if not SYSTEMD_RUNNING.is_dir():
        raise InstallError("systemd is not the init system here, so this host cannot run the node service." + NOTHING_CHANGED)
    for tool in ("systemctl", "useradd", "usermod", "userdel", "gpasswd"):
        if shutil.which(tool) is None:
            raise InstallError(tool + " is required to prepare the node service user." + NOTHING_CHANGED)
    try:
        enforcing = SELINUX_ENFORCE.read_text().strip() == "1"
    except OSError:
        enforcing = False
    if enforcing:
        raise InstallError("SELinux is enforcing on this host, which sudo mode does not support yet. Run the no-sudo "
                           "command as a prepared user instead." + NOTHING_CHANGED)


def root_file(path, content, replace=False):
    """Write a root-owned 0644 file, or accept an identical existing one (or replace it)."""
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise InstallError(str(path) + " must be a regular file")
    if path.exists():
        if path.stat().st_uid != os.geteuid():
            raise InstallError(str(path) + " must be owned by root; preserve it and inspect the node")
        if path.read_text() == content:
            return
        if not replace:
            raise InstallError(str(path) + " differs from this installer's version; preserve it and inspect the node")
    if not path.parent.exists():
        # Readable by every user, so a no-sudo run can see a sudo-mode node; the caller's umask is 077.
        path.parent.mkdir(parents=True, mode=0o755)
        os.chmod(path.parent, 0o755)
    descriptor, temporary = tempfile.mkstemp(prefix=".oac-node-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w") as stream:
            stream.write(content)
        os.chmod(temporary, 0o644)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def read_root_json(path):
    if not path.exists() and not path.is_symlink():
        return None
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != os.geteuid():
        raise InstallError(str(path) + " must be a regular file owned by root")
    return json.loads(path.read_text())


def node_records():
    """Installation IDs of the sudo-mode nodes recorded on this host."""
    if not SYSTEM_RECORDS.is_dir():
        return []
    return sorted(path.name[:-5] for path in SYSTEM_RECORDS.glob("*-*-*-*-*.json"))


def listdir_nofollow(base, *parts):
    """Entries of base/parts, opening every component without following links; [] if absent or linked."""
    descriptors = []
    try:
        descriptors.append(os.open(base, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW))
        for part in parts:
            descriptors.append(os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptors[-1]))
        return sorted(os.listdir(descriptors[-1]))
    except OSError:
        return []
    finally:
        for descriptor in descriptors:
            os.close(descriptor)


def service_account():
    try:
        return pwd.getpwnam(SERVICE_USER)
    except KeyError:
        return None


def group_id(name):
    try:
        return grp.getgrnam(name).gr_gid
    except (KeyError, AttributeError):
        return None


def ours(account, recorded_uid=None):
    """The account the installer created or adopted.

    It has the service home, a nologin shell, the recorded uid when there is one,
    neither uid nor gid 0, and no groups besides its own, docker and kvm."""
    if (account is None or account.pw_dir != str(SERVICE_HOME) or not account.pw_shell.endswith(("nologin", "false"))
            or recorded_uid not in (None, account.pw_uid) or account.pw_uid == 0 or account.pw_gid == 0):
        return False
    allowed = {account.pw_gid} | {gid for gid in map(group_id, DEVICE_GROUPS.values()) if gid is not None}
    return set(os.getgrouplist(account.pw_name, account.pw_gid)) <= allowed


def host_capacity(provider, resources, docker_info):
    if provider == "docker":
        cpus, memory = docker_info.get("NCPU", 0), docker_info.get("MemTotal", 0)
    else:
        cpus, memory = os.cpu_count() or 0, 0
        with open("/proc/meminfo") as stream:
            for line in stream:
                if line.startswith("MemTotal:"):
                    memory = int(line.split()[1]) * 1024
    if cpus < resources["cpus"] or memory < resources["memory_mib"] * 1024 * 1024:
        raise InstallError("This host has %d CPUs and %d MiB of memory; each sandbox needs %d CPUs and %d MiB."
                           % (cpus, memory // (1024 * 1024), resources["cpus"], resources["memory_mib"]) + NOTHING_CHANGED)


# The service user joins only the group that owns the provider's device, and only
# these names; a device owned by a privileged group (disk, sudo, wheel, adm) is refused.
DEVICE_GROUPS = {"docker": "docker", "microsandbox": "kvm"}


def provider_group(provider):
    """Check Docker or KVM without installing either; return the group that grants access."""
    if provider == "docker":
        if shutil.which("docker") is None:
            raise InstallError("Docker Engine is not installed. Install it (https://docs.docker.com/engine/install/), "
                               "then rerun this command." + NOTHING_CHANGED)
        try:
            info = DOCKER_SOCKET.stat()
        except OSError:
            info = None
        if info is None or not stat.S_ISSOCK(info.st_mode):
            raise InstallError("Docker is not running: run `sudo systemctl enable --now docker`, then rerun this command." + NOTHING_CHANGED)
        if info.st_gid == 0 or stat.S_IMODE(info.st_mode) & 0o060 != 0o060:
            raise InstallError("Nodes reach Docker at /var/run/docker.sock through its group, but the socket is not "
                               "group-accessible (rootless Docker is not supported)." + NOTHING_CHANGED)
        try:
            details = json.loads(checked(list(DOCKER) + ["info", "--format", "{{json .}}"], "Docker is not running"))
        except (InstallError, ValueError):
            raise InstallError("Docker is not running: run `sudo systemctl enable --now docker`, then rerun this command." + NOTHING_CHANGED) from None
        # docker info's JSON uses the Engine API names (CpuCfsQuota), not the Go field names.
        if details.get("MemoryLimit") is not True or details.get("CpuCfsQuota") is not True:
            raise InstallError("Docker on this host does not enforce CPU and memory limits; use cgroup v2, then rerun this command." + NOTHING_CHANGED)
        return device_group(provider, DOCKER_SOCKET, info.st_gid), details
    try:
        info = KVM.stat()
    except OSError:
        info = None
    if info is None or not stat.S_ISCHR(info.st_mode):
        raise InstallError("KVM is unavailable: enable hardware virtualization (or nested virtualization for this VM) and "
                           "load kvm_intel or kvm_amd, then rerun this command." + NOTHING_CHANGED)
    if stat.S_IMODE(info.st_mode) & 0o006 == 0o006:
        return None, {}
    if info.st_gid == 0 or stat.S_IMODE(info.st_mode) & 0o060 != 0o060:
        raise InstallError("/dev/kvm must be group-accessible, for example root:kvm with mode 0660 (your distribution's "
                           "KVM package sets this), then rerun this command." + NOTHING_CHANGED)
    return device_group(provider, KVM, info.st_gid), {}


def device_group(provider, device, gid):
    try:
        name = grp.getgrgid(gid).gr_name
    except KeyError:
        name = str(gid)
    if name != DEVICE_GROUPS[provider]:
        raise InstallError(str(device) + " belongs to the group " + name + "; the node's service user joins only the "
                           + DEVICE_GROUPS[provider] + " group. Give the device that group, then rerun this command." + NOTHING_CHANGED)
    return name


def other_node(args, provider):
    """Refuse a second sudo-mode Core on this host, or a second node for this installation.

    Sudo-mode nodes share the oac-node account, so one host serves one Core. Nodes
    installed without sudo are found in the invoking user's home and, for Docker, by
    their network on this engine; other users' homes are not searched."""
    for installation in node_records():
        if installation != args.installation_id:
            raise InstallError("This host already runs a sudo-mode node for another Core (installation " + installation
                               + "). Sudo mode serves one Core per host: remove that node on its Nodes page and "
                               "uninstall it first." + NOTHING_CHANGED)
    sudo_user = os.environ.get("SUDO_USER", "")
    if sudo_user and sudo_user != "root":
        try:
            home = pwd.getpwnam(sudo_user).pw_dir
        except KeyError:
            home = ""
        if home.startswith("/") and listdir_nofollow(home, ".oac", "nodes", args.installation_id):
            raise InstallError("This host already runs a node for this installation, installed without sudo by " + sudo_user
                               + ". Remove it on the Nodes page and uninstall it as that user first." + NOTHING_CHANGED)
    if provider == "docker":
        networks = checked(list(DOCKER) + ["network", "ls", "--format", "{{.Name}}"], "Cannot inspect Docker networks").splitlines()
        if "oac-node-" + args.installation_id in networks:
            raise InstallError("Another node for this installation already uses this Docker engine (network oac-node-"
                               + args.installation_id + "). Remove it on the Nodes page and uninstall it first." + NOTHING_CHANGED)


def account_plan():
    """Check the service account, its record and home before changing anything.

    Returns the existing account (or None when it must be created) and the account record."""
    account, record = service_account(), read_root_json(SYSTEM_RECORDS / "account.json")
    if account is not None and not ours(account, (record or {}).get("uid")):
        raise InstallError("An account named " + SERVICE_USER + " exists but was not created or adopted by this installer "
                           "(home " + account.pw_dir + ", shell " + account.pw_shell + ", uid " + str(account.pw_uid)
                           + ", or groups beyond its own, docker and kvm). Rename or remove it, then rerun." + NOTHING_CHANGED)
    if SERVICE_HOME.is_symlink() or (SERVICE_HOME.exists() and not SERVICE_HOME.is_dir()):
        raise InstallError(str(SERVICE_HOME) + " must be a directory, not a symbolic link." + NOTHING_CHANGED)
    if SERVICE_HOME.exists() and (account is None or SERVICE_HOME.stat().st_uid != account.pw_uid):
        raise InstallError(str(SERVICE_HOME) + " exists but does not belong to the " + SERVICE_USER + " account; preserve "
                           "it and inspect the host." + NOTHING_CHANGED)
    return account, record


def prepare_account(account, record, group):
    """Create or adopt the service user and give it the provider's group; record every change at once."""
    records = SYSTEM_RECORDS / "account.json"
    if account is None:
        # A record of an account that no longer exists is stale; the new account replaces it.
        records.unlink(missing_ok=True)
        shell = next((path for path in ("/usr/sbin/nologin", "/sbin/nologin") if os.path.exists(path)), "/bin/false")
        checked(["useradd", "--system", "--user-group", "--no-create-home", "--home-dir", str(SERVICE_HOME),
                 "--shell", shell, SERVICE_USER], "Cannot create the " + SERVICE_USER + " service user")
        account = service_account()
        record = {"format": 1, "created": True, "uid": account.pw_uid, "groups_added": []}
        root_file(records, json_text(record), replace=True)
    elif record is None:
        record = {"format": 1, "created": False, "uid": account.pw_uid, "groups_added": [],
                  "home_mode": oct(stat.S_IMODE(SERVICE_HOME.stat().st_mode)) if SERVICE_HOME.exists() else None}
        root_file(records, json_text(record), replace=True)
    if not SERVICE_HOME.exists():
        SERVICE_HOME.mkdir(mode=0o700, parents=True)
        os.chown(SERVICE_HOME, account.pw_uid, account.pw_gid)
    os.chmod(SERVICE_HOME, 0o700)
    if group and SERVICE_USER not in grp.getgrnam(group).gr_mem:
        checked(["usermod", "--append", "--groups", group, SERVICE_USER], "Cannot add " + SERVICE_USER + " to the " + group + " group")
        record = dict(record, groups_added=sorted(set(record["groups_added"]) | {group}))
        root_file(records, json_text(record), replace=True)
    return service_account()


def system_unit(root, provider):
    def quote(value):
        return '"' + str(value).replace("\\", "\\\\").replace('"', '\\"').replace("%", "%%") + '"'
    after = "network-online.target docker.service" if provider == "docker" else "network-online.target"
    # Root owns this file; the service user can change only its own node files. The
    # service runs with the account's own primary group.
    return ("[Unit]\nDescription=OpenAgentCore sandbox node " + root.name + "\nWants=network-online.target\nAfter=" + after
            + "\nStartLimitIntervalSec=0\n\n[Service]\nType=exec\nUser=" + SERVICE_USER
            + "\nExecStart=:" + quote(root / COMMON[0]) + " run --config " + quote(root / "provider.json")
            + " --state-dir " + quote(root / "state/node") + "\nWorkingDirectory=" + str(root).replace("%", "%%")
            + "\nRestart=on-failure\nRestartSec=5s\nRestartPreventExitStatus=78\nKillMode=process\nUMask=0077"
            + "\n\n[Install]\nWantedBy=multi-user.target\n")


def recorded_origin(value, source):
    """A Core address read from a file, checked with the same rule as the command line."""
    try:
        return origin(value)
    except (argparse.ArgumentTypeError, TypeError, AttributeError):
        raise InstallError(source + " holds an invalid Core address; preserve it and inspect the host." + NOTHING_CHANGED) from None


def node_record(installation_id):
    """The root-owned record of this installation's sudo-mode node, checked against the fixed paths."""
    record = read_root_json(SYSTEM_RECORDS / (installation_id + ".json"))
    if record is not None:
        expected = {"installation_id": installation_id, "node_root": str(SERVICE_HOME / ".oac/nodes" / installation_id),
                    "unit": str(SYSTEM_UNITS / unit_name(installation_id))}
        if any(record.get(key) != value for key, value in expected.items()) or record.get("provider") not in DEVICE_GROUPS:
            raise InstallError(str(SYSTEM_RECORDS / (installation_id + ".json")) + " is not this installer's record; preserve "
                               "it and inspect the host." + NOTHING_CHANGED)
        recorded_origin(record.get("core_url"), str(SYSTEM_RECORDS / (installation_id + ".json")))
    return record


def install_system(args, token):
    """Sudo mode: prepare the host, then run the node as a root-owned system service."""
    os.environ["PATH"] = SAFE_PATH
    host_checks()
    refuse_legacy_node(args)
    # Checks that change nothing run first, so a refusal leaves no trace, not even a lock.
    record = node_record(args.installation_id)
    configuration = None
    if record is None:
        print("Reading the Core deployment specification...", flush=True)
        configuration = node_spec.fetch(args, token, None, open_request, allow_enrollment=True)
        provider = configuration["provider"]
    else:
        provider = record["provider"]
        if record["core_url"] != args.core_url:
            raise InstallError("This host's node uses " + record["core_url"] + ", but this command uses " + args.core_url
                               + ". Remove the node on the Nodes page, uninstall it, then run a new command." + NOTHING_CHANGED)
    print("Checking host requirements...", flush=True)
    group, details = provider_group(provider)
    if record is None:
        other_node(args, provider)
        host_capacity(provider, configuration["specification"]["resources"], details)
    with host_lock():
        if node_record(args.installation_id) != record or (record is None and [i for i in node_records() if i != args.installation_id]):
            raise InstallError("Another node installation changed this host meanwhile; rerun the command." + NOTHING_CHANGED)
        account, account_record = account_plan()
        helper_archive = node_generations.helper_archive(args, sys.modules[__name__])
        # Every check has passed; from here on the host changes.
        account = prepare_account(account, account_record, group)
        child_docker_config()
        root = SERVICE_HOME / ".oac/nodes" / args.installation_id
        unit = SYSTEM_UNITS / unit_name(args.installation_id)
        root_file(SYSTEM_RECORDS / (args.installation_id + ".json"),
                  json_text({"format": 1, "installation_id": args.installation_id, "provider": provider,
                             "core_url": args.core_url, "node_root": str(root), "unit": str(unit)}))
        args.system, args.provider = True, provider
        run_as(account, prepare_service_node, args, token, helper_archive)
        root_file(unit, system_unit(root, provider))
        print("Starting the node service...", flush=True)
        checked(["systemctl", "daemon-reload"], "Cannot reload systemd")
        checked(["systemctl", "enable", "--now", unit.name], "Cannot start the node service; retained identity is unchanged")
        checked(["systemctl", "is-active", "--quiet", unit.name], "Node service is unavailable; inspect sudo journalctl -u " + unit.name)
        print("Waiting for Core connection and provider readiness...", flush=True)
        run_as(account, wait_ready, root, args)
    print("Node connected to Core and provider ready. It runs as " + SERVICE_USER + " in the system service " + unit.name + ".")
    print("Logs: sudo journalctl -u " + unit.name)


# Uninstall -------------------------------------------------------------------

def private_json(path):
    """Read one of this user's private JSON objects without following links or racing a swap."""
    try:
        descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    except OSError:
        return None
    with os.fdopen(descriptor) as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_size > 16384:
            return None
        try:
            value = json.loads(stream.read())
        except ValueError:
            return None
    return value if isinstance(value, dict) else None


def no_links(path):
    """Refuse a path whose components include a symbolic link."""
    if os.path.realpath(path) != str(path):
        raise InstallError(str(path) + " is reached through a symbolic link; inspect and remove it by hand." + NOTHING_CHANGED)


def confirm_removed(root, core_url, force):
    """Continue only once Core rejects the node's credential, or when it never registered.

    Runs as the node's own user. The Core address comes from the caller: in sudo mode
    the root-owned record, never a file the service user can write."""
    no_links(root)
    identity_file = root / "state/node/identity.json"
    if not (root / "registered.json").exists() and not identity_file.exists():
        return
    if force:
        print("Skipping the Core check (--force).", flush=True)
        return
    stored = private_json(identity_file) or {}
    try:
        node_id, credential = stored["identity"]["node_id"], stored["credential"]
        if str(uuid.UUID(node_id)) != node_id or not re.fullmatch(r"[0-9a-f]{64}", credential):
            raise ValueError()
    except (TypeError, KeyError, ValueError, AttributeError):
        raise InstallError("Retained node identity is missing or invalid; rerun with --force only if Core no longer exists." + NOTHING_CHANGED) from None
    request = urllib.request.Request(core_url + "/api/v1/sandbox-node/identity?" + urlencode({"node_id": node_id}),
                                     headers={"Authorization": "Bearer " + credential})
    try:
        with open_request(request, timeout=15):
            pass
    except urllib.error.HTTPError as error:
        if error.code == 401:
            return
        detail = "HTTP " + str(error.code)
    except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.HTTPException, InstallError):
        detail = "Core unreachable"
    else:
        raise InstallError("Core still lists this node. Remove it on the Nodes page first; Core refuses while it keeps "
                           "sandboxes." + NOTHING_CHANGED)
    raise InstallError("Cannot confirm with Core that this node was removed (" + detail + "). Rerun with --force only "
                       "if this Core no longer exists." + NOTHING_CHANGED)


def release_docker_network(installation_id):
    """Remove the node's Docker network; never containers, volumes or images."""
    if shutil.which("docker") is None:
        return
    network = "oac-node-" + installation_id
    try:
        if network not in checked(list(DOCKER) + ["network", "ls", "--format", "{{.Name}}"], "Docker unavailable").splitlines():
            return
        remaining = checked(list(DOCKER) + ["ps", "--all", "--filter", "label=io.oac.installation=" + installation_id,
                                            "--format", "{{.Names}}"], "Docker unavailable").split()
    except InstallError:
        print("Docker is unavailable, so network " + network + " was not removed.")
        return
    if remaining:
        print("Kept Docker network " + network + ": these containers still exist: " + ", ".join(remaining))
        return
    checked(list(DOCKER) + ["network", "rm", network], "Cannot remove Docker network " + network)


def remove_node_files(root, installation_id):
    """Delete the node's state directory; keep the Runtime image and the microsandbox store.

    Runs as the node's own user, so a link it planted can never reach another user's files."""
    no_links(root)
    provider = private_json(root / "provider.json") or {}
    image = (provider.get("docker") or {}).get("image")
    runtime_home = micro_home(installation_id)
    if root.exists():
        shutil.rmtree(root)
    # The service user can write provider.json; print only what an image ID can be.
    if isinstance(image, str) and re.fullmatch(r"sha256:[0-9a-f]{64}", image):
        print("Kept the Runtime image " + image + "; remove it with `docker image rm " + image + "` if no other node uses it.")
    if runtime_home.exists():
        sudo_mode = str(Path.home()) == str(SERVICE_HOME)
        remove = ("sudo -u " + SERVICE_USER + " rm -rf " if sudo_mode else "rm -rf ") + str(runtime_home)
        print("Kept the microsandbox store " + str(runtime_home) + " with its images and any sandbox state. When no "
              "microVM runs (`pgrep -u " + os.environ.get("USER", "") + "` is empty), remove it with `" + remove
              + "`, then rerun this uninstall command.")


def uninstall_system(args):
    """Sudo mode: remove the system service, the node files and, when unused, the service user."""
    os.environ["PATH"] = SAFE_PATH
    if shutil.which("systemctl") is None:
        raise InstallError("systemctl is required." + NOTHING_CHANGED)
    refuse_legacy_node(args)
    record = node_record(args.installation_id)
    unit = SYSTEM_UNITS / unit_name(args.installation_id)
    # Only root-owned files decide whether a node is installed; the service home is not read here.
    if record is None and not unit.exists() and not (SYSTEM_RECORDS / "account.json").exists():
        print("No node for installation " + args.installation_id + " was installed with sudo on this host. If it was "
              "installed without sudo, run the uninstall command as that user without sudo.")
        return
    with host_lock():
        record = node_record(args.installation_id)
        account, account_record = service_account(), read_root_json(SYSTEM_RECORDS / "account.json")
        root = SERVICE_HOME / ".oac/nodes" / args.installation_id
        if record is None and not unit.exists():
            print("No node for installation " + args.installation_id + " was installed with sudo on this host.")
        else:
            if not ours(account, (account_record or {}).get("uid")):
                raise InstallError("The " + SERVICE_USER + " account is missing or differs from the one this installer "
                                   "recorded, so its files are not removed. Inspect the host." + NOTHING_CHANGED)
            core_url = record["core_url"] if record else None
            if core_url is None and not args.force:
                raise InstallError("This node's record is missing, so Core cannot be asked; rerun with --force only if "
                                   "Core no longer exists." + NOTHING_CHANGED)
            child_docker_config()
            run_as(account, confirm_removed, root, core_url, args.force)
            if unit.exists():
                checked(["systemctl", "disable", "--now", unit.name], "Cannot stop the node service " + unit.name)
                unit.unlink()
                checked(["systemctl", "daemon-reload"], "Cannot reload systemd")
                # A removed node's service ends failed (exit 78); drop that state with the unit.
                with contextlib.suppress(InstallError):
                    checked(["systemctl", "reset-failed", unit.name], "Cannot reset " + unit.name)
            release_docker_network(args.installation_id)
            run_as(account, remove_node_files, root, args.installation_id)
            (SYSTEM_RECORDS / (args.installation_id + ".json")).unlink(missing_ok=True)
            print("Node for installation " + args.installation_id + " uninstalled from this host.")
        release_account(account, account_record)


def release_account(account, record):
    """When no node remains, undo the account changes: delete a created account, or leave an adopted one as found."""
    if record is None or node_records():
        return
    if not ours(account, record.get("uid")):
        print("The " + SERVICE_USER + " account differs from the one this installer recorded; it was left alone.")
        return
    if record.get("created"):
        stores = [str(SERVICE_HOME / ".oac/m" / name) for name in listdir_nofollow(SERVICE_HOME, ".oac", "m")]
        if stores:
            # The names come from the service user's home; show them only as plain text.
            print("Kept the " + SERVICE_USER + " user while microsandbox stores remain: " + plain(", ".join(stores), sys.stdout.encoding)
                  + ". Remove them with `sudo -u " + SERVICE_USER + " rm -rf <store>`, then rerun this uninstall command.")
            return
        checked(["userdel", SERVICE_USER], "Cannot remove the " + SERVICE_USER + " user; stop its processes and rerun the uninstall command")
        # No process can run as the deleted user, so nothing races this removal.
        if SERVICE_HOME.is_dir() and not SERVICE_HOME.is_symlink():
            shutil.rmtree(SERVICE_HOME)
        print("Removed the " + SERVICE_USER + " service user, which this installer created.")
    else:
        for group in record.get("groups_added", []):
            try:
                member = SERVICE_USER in grp.getgrnam(group).gr_mem
            except KeyError:
                member = False
            if member:
                checked(["gpasswd", "--delete", SERVICE_USER, group], "Cannot remove " + SERVICE_USER + " from the " + group + " group")
                print("Removed " + SERVICE_USER + " from the " + group + " group, which this installer added.")
        if record.get("home_mode") and SERVICE_HOME.is_dir() and not SERVICE_HOME.is_symlink():
            os.chmod(SERVICE_HOME, int(record["home_mode"], 8))
    (SYSTEM_RECORDS / "account.json").unlink()
    with contextlib.suppress(OSError):
        SYSTEM_RECORDS.rmdir()


def uninstall_user(args):
    refuse_legacy_node(args)
    with host_lock():
        root = Path.home() / ".oac/nodes" / args.installation_id
        if not root.exists():
            print("No node for installation " + args.installation_id + " is installed for this user. If it was installed "
                  "with sudo, run the uninstall command with sudo.")
            return
        identity = private_json(root / "state/node/identity.json") or {}
        core_url = identity.get("core_url")
        if core_url is not None:
            recorded_origin(core_url, str(root / "state/node/identity.json"))
        elif (root / "registered.json").exists() and not args.force:
            raise InstallError("Retained node identity is missing or invalid; rerun with --force only if Core no longer exists." + NOTHING_CHANGED)
        confirm_removed(root, core_url, args.force)
        unit = root / unit_name(args.installation_id)
        service = unit.exists()
        if service:
            user_bus()
            checked(["systemctl", "--user", "disable", "--now", unit.name], "Cannot stop the node service " + unit.name)
        release_docker_network(args.installation_id)
        remove_node_files(root, args.installation_id)
        if service:
            checked(["systemctl", "--user", "daemon-reload"], "Cannot reload the systemd user manager")
            with contextlib.suppress(InstallError):
                checked(["systemctl", "--user", "reset-failed", unit.name], "Cannot reset " + unit.name)
    print("Node for installation " + args.installation_id + " uninstalled for this user.")


def wait_ready(root, args, timeout=60):
    identity_file = root / "state/node/identity.json"
    if not existing_file(identity_file) or stat.S_IMODE(identity_file.stat().st_mode) != 0o600:
        raise InstallError("Retained node identity is missing or is not private (0600); preserve state and inspect enrollment")
    with identity_file.open() as stream:
        raw = stream.read(16385)
    try:
        stored = json.loads(raw)
        identity = stored["identity"]
        credential = stored["credential"]
        if (len(raw) > 16384 or stored["core_url"] != args.core_url
                or identity["installation_id"] != args.installation_id or identity["provider"] != args.provider
                or str(uuid.UUID(identity["node_id"])) != identity["node_id"]
                or not re.fullmatch(r"[0-9a-f]{64}", credential)):
            raise ValueError()
    except (ValueError, KeyError, TypeError, AttributeError):
        raise InstallError("Retained node identity differs or is invalid; preserve state and inspect enrollment") from None
    request = urllib.request.Request(args.core_url + "/api/v1/sandbox-node/identity?" + urlencode({"node_id": identity["node_id"]}),
                                     headers={"Authorization": "Bearer " + credential})
    deadline = time.monotonic() + timeout
    detail = "Core has not confirmed the node connection"
    while time.monotonic() < deadline:
        try:
            with open_request(request, timeout=min(10, max(0.1, deadline - time.monotonic()))) as response:
                raw = response.read(16385)
            if len(raw) > 16384:
                raise ValueError()
            data = json.loads(raw)
            if any(data.get(key) != identity[key] for key in ("node_id", "installation_id", "provider", "deployment_generation", "specification_digest")):
                raise InstallError("Core returned a different node identity; preserve state and inspect the Core URL")
            if data.get("connected") is True and data.get("provider_ready") is True:
                return
            detail = "Node is connected but its provider is not ready" if data.get("connected") is True else "Core has not confirmed the node connection"
        except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead) as error:
            if not transient(error):
                raise InstallError("Core rejected the node readiness request (HTTP " + str(error.code) + "); verify its retained credential and Core URL") from None
            detail = "Core readiness endpoint is temporarily unreachable; check TLS and network access"
        except (ValueError, AttributeError):
            raise InstallError("Core returned invalid node readiness data; check the matched Core release") from None
        remaining = deadline - time.monotonic()
        if remaining > 0:
            time.sleep(min(2, remaining))
    journal = ("sudo journalctl -u " if getattr(args, "system", False) else "journalctl --user -u ") + unit_name(args.installation_id)
    raise InstallError(detail + "; state and service are retained. Inspect " + journal + ", then rerun the installation command")


# A token in the environment could reach sudo's log (`sudo VAR=... python3`) and every
# program the installer starts; it is refused rather than read.
RETIRED_TOKEN_VARIABLE = "PARSAR_NODE_ENROLLMENT_TOKEN"


def read_token(args):
    """The one-time token comes on standard input, never in argv, the environment or a sudo command line."""
    if not args.enrollment_token_stdin:
        return ""  # A registered node's rerun uses its retained credential.
    if sys.stdin.isatty():
        return getpass.getpass("Enrollment token: ").strip()
    return sys.stdin.readline(4098).strip()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group()
    source.add_argument("--source-url", type=origin)
    source.add_argument("--bundle", type=Path)
    parser.add_argument("--core-url", type=origin)
    parser.add_argument("--provider", choices=("docker", "microsandbox"), help="Optional assertion; Core owns provider selection")
    parser.add_argument("--installation-id", required=True)
    parser.add_argument("--enrollment-token-stdin", action="store_true", help="Read the one-time enrollment token from standard input")
    parser.add_argument("--generation-action", choices=("prepare", "collect"), help=argparse.SUPPRESS)
    parser.add_argument("--generation", type=int, help=argparse.SUPPRESS)
    parser.add_argument("--specification-digest", help=argparse.SUPPRESS)
    parser.add_argument("--update", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--uninstall", action="store_true", help="Remove this host's node after it was removed on the Nodes page")
    parser.add_argument("--force", action="store_true", help="With --uninstall: skip the Core check, for a Core that no longer exists")
    args = parser.parse_args(argv)
    if RETIRED_TOKEN_VARIABLE in os.environ:
        parser.exit(2, RETIRED_TOKEN_VARIABLE + " is retired: pass the enrollment token on standard input with "
                       "--enrollment-token-stdin.\n")
    if str(uuid.UUID(args.installation_id)) != args.installation_id:
        raise InstallError("Installation ID must be a canonical UUID")
    if args.update:
        raise InstallError("Node version updates are not supported; preserve the existing state and reinstall "
                           "separately. Nothing was changed.")
    if args.generation_action:
        if args.generation is None or not 1 <= args.generation <= 9223372036854775807 or not re.fullmatch(r"[0-9a-f]{64}", args.specification_digest or ""):
            parser.error("Invalid generation authorization")
        try:
            (node_generations.prepare if args.generation_action == "prepare" else node_generations.collect)(args, sys.modules[__name__])
        except (RuntimeDownloadError, distribution.ArtifactError):
            print("Runtime artifact transfer or verification failed", file=sys.stderr)
            raise SystemExit(65) from None
        return
    if args.uninstall:
        if args.source_url or args.bundle or args.core_url or args.provider or args.enrollment_token_stdin:
            parser.error("--uninstall takes only --installation-id and --force")
        (uninstall_system if os.geteuid() == 0 else uninstall_user)(args)
        return
    if args.force:
        parser.error("--force applies only to --uninstall")
    if not (args.source_url or args.bundle) or not args.core_url:
        parser.error("--source-url (or --bundle) and --core-url are required")
    if args.bundle is not None and (not args.bundle.is_absolute() or args.bundle.resolve() != args.bundle):
        raise InstallError("Local bundle must be an absolute directory without symlinks")
    token = read_token(args)
    if len(token) > 4096 or any(c.isspace() for c in token):
        raise InstallError("A valid one-time enrollment credential is required")
    if os.geteuid() == 0:
        install_system(args, token)
    else:
        install(args, token)


if __name__ == "__main__":
    try:
        main()
    except ChildFailed as failure:
        if str(failure):
            print(str(failure), file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:  # Ctrl-C outside the service-user steps, as while waiting for the host lock.
        print(INTERRUPTED, file=sys.stderr)
        sys.exit(130)
    except (InstallError, node_spec.SpecificationError, distribution.DistributionError, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(str(error) if isinstance(error, (InstallError, node_spec.SpecificationError, distribution.DistributionError)) else "Node installation failed; check host prerequisites and retained private files", file=sys.stderr)
        sys.exit(1)
