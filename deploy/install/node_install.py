#!/usr/bin/env python3
"""Install and enroll one node from its Core console's matched distribution."""
import argparse
import contextlib
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
import shutil
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


class InstallError(Exception):
    pass


COMMON = ("native/bin/parsar-sandbox-node", "runtime/seccomp.json")
MICRO = ("native/bin/agents-api-microsandbox-provider", "native/microsandbox/msb",
         "native/microsandbox/libkrunfw.so.5.6.1")
DOCKER = ("docker", "--host", "unix:///var/run/docker.sock")
DOCKER_SOCKET = Path("/var/run/docker.sock")
KVM = Path("/dev/kvm")

# Sudo mode: run as root, the installer prepares the host itself. The node runs as
# this dedicated service user under a root-owned system unit; root never runs a
# file the service user can write.
SERVICE_USER = "parsar-node"
SERVICE_HOME = Path("/var/lib/parsar-node")
SYSTEM_RECORDS = Path("/etc/parsar-node")
SYSTEM_UNITS = Path("/etc/systemd/system")
SYSTEM_LOCKS = Path("/run")
SYSTEMD_RUNNING = Path("/run/systemd/system")
SELINUX_ENFORCE = Path("/sys/fs/selinux/enforce")
USER_RUNTIME = Path("/run/user")
SAFE_PATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
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
                raise InstallError("Node bootstrap metadata is too large: " + name)
            return io.BytesIO(raw)
        except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead) as error:
            if not transient(error) or attempt == 2:
                status = " (HTTP " + str(error.code) + ")" if isinstance(error, urllib.error.HTTPError) else ""
                raise InstallError("Cannot download node metadata " + name + status + "; check the console URL, TLS and network, then rerun") from None
            time.sleep(attempt + 1)


def metadata(source, bundle=None):
    if bundle is not None:
        def read(name):
            path = bundle / name
            if path.is_symlink() or not path.is_file() or not path.resolve().is_relative_to(bundle):
                raise InstallError("Invalid local distribution metadata")
            return path.open("rb")
    else:
        read = lambda name: fetch(source, name)
    with read("SHA256SUMS") as response:
        raw = response.read(1024 * 1024 + 1)
    if len(raw) > 1024 * 1024:
        raise InstallError("Invalid distribution checksum list")
    sums = {}
    for line in raw.decode().splitlines():
        checksum, name = line.split("  ", 1)
        if name in sums or not re.fullmatch(r"[0-9a-f]{64}", checksum):
            raise InstallError("Invalid distribution checksum entry")
        sums[name] = checksum
    with read("manifest.json") as response:
        raw = response.read(1024 * 1024 + 1)
    if len(raw) > 1024 * 1024 or hashlib.sha256(raw).hexdigest() != sums.get("manifest.json"):
        raise InstallError("Distribution manifest checksum mismatch")
    manifest = json.loads(raw)
    if (manifest.get("platform") != "linux/amd64" or not re.fullmatch(r"[0-9a-f]{40}", manifest.get("source_commit", ""))
            or not re.fullmatch(r"sha256:[0-9a-f]{64}", manifest.get("images", {}).get("runtime", ""))):
        raise InstallError("Unsupported node distribution")
    distribution.image_identities(manifest, "runtime")
    for name in (COMMON[0], "images/runtime.tar.gz") + MICRO:
        distribution.artifact(manifest, name)
    # Nodes download only from their console, never from a release URL the build recorded.
    manifest["artifact_base_url"] = source + "/node-install/artifacts" if source else ""
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


def download(source, name, root, expected):
    target = root / name
    safe_directory(target.parent)
    if existing_file(target):
        if file_digest(target) != expected:
            raise InstallError("Installed node payload differs; refusing to overwrite it")
        return
    descriptor, temporary = tempfile.mkstemp(prefix=".download-", dir=target.parent)
    try:
        with os.fdopen(descriptor, "wb") as output, fetch(source, name) as response:
            for block in iter(lambda: response.read(1024 * 1024), b""):
                output.write(block)
        if file_digest(Path(temporary)) != expected:
            raise InstallError("Node payload checksum mismatch: " + name)
        os.replace(temporary, target)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    if name.startswith("native/"):
        os.chmod(target, 0o700)


def micro_home(installation_id):
    directory = Path.home() / ".parsar/m" / hashlib.sha256(installation_id.encode()).hexdigest()[:12]
    if len(os.fsencode(directory)) > 48:
        raise InstallError("HOME is too long for microsandbox Unix socket paths; use a user with a shorter persistent home directory")
    return directory


def provider_config(root, args, manifest, runtime_image):
    result = {"installation_id": args.installation_id, "provider": args.provider, "core_url": args.core_url + "/api/v1",
              "specification": args.configuration["specification"], "generation": args.configuration["generation"]}
    if args.provider == "docker":
        result["docker"] = {"host": "unix:///var/run/docker.sock", "image": runtime_image,
                            "network": "parsar-node-" + args.installation_id,
                            "seccomp_file": str(root / "runtime/seccomp.json"), "nested_sandbox": True}
    else:
        endpoint = urlsplit(args.core_url)
        port = endpoint.port or (443 if endpoint.scheme == "https" else 80)
        addresses = sorted({entry[4][0] for entry in socket.getaddrinfo(endpoint.hostname, port, type=socket.SOCK_STREAM)})
        core_rules = [{"action": "allow", "direction": "egress", "destination": address, "protocol": "tcp", "port": str(port)} for address in addresses]
        result["microsandbox"] = {
            "helper_path": str(root / MICRO[0]), "runtime_path": str(root / MICRO[1]), "firmware_path": str(root / MICRO[2]),
            "runtime_sha256": manifest["microsandbox"]["runtime_sha256"], "firmware_sha256": manifest["microsandbox"]["firmware_sha256"],
            "runtime_home": str(micro_home(args.installation_id)), "image": manifest["runtime_ref"],
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
        network = "parsar-node-" + args.installation_id
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
        env = dict(os.environ, MSB_BACKEND="local", MSB_HOME=str(micro_home(args.installation_id)),
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
    return ("[Unit]\nDescription=Parsar sandbox node\nStartLimitIntervalSec=0\n\n[Service]\nType=exec\nExecStart=:"
            + quote(root / COMMON[0]) + " run --config " + quote(root / "provider.json") + " --state-dir " + quote(root / "state/node")
            + "\nWorkingDirectory=" + str(root).replace("%", "%%")
            + "\nRestart=on-failure\nRestartSec=5s\nRestartPreventExitStatus=78\nKillMode=process\nUMask=0077"
            + "\n\n[Install]\nWantedBy=default.target\n")


def unit_name(installation_id):
    return "parsar-node-" + installation_id + ".service"


def open_node(args, token, system=False):
    """Read the Core specification and check the host; returns the node's state directory."""
    root = Path.home() / ".parsar/nodes" / args.installation_id
    safe_directory(root)
    identity_file = root / "state/node/identity.json"
    retained = json.loads(identity_file.read_text()) if existing_file(identity_file) else None
    print("Reading the Core deployment specification...", flush=True)
    args.configuration = node_spec.fetch(args, token, retained, open_request, allow_enrollment=not (root / "registered.json").exists())
    args.provider = args.configuration["provider"]
    print("Checking host requirements...", flush=True)
    preflight(args.provider, system)
    if args.provider == "microsandbox":
        runtime_home = micro_home(args.installation_id)
        safe_directory(runtime_home)
        owner = runtime_home / "parsar-installation.json"
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


def register_node(root, args, token):
    """Download and verify the payload, prepare the Runtime and register; not the service."""
    print("Downloading and verifying node files...", flush=True)
    manifest, sums = metadata(args.source_url, getattr(args, "bundle", None))
    node_spec.verify_release(args.configuration, manifest)
    names = COMMON + (MICRO if args.provider == "microsandbox" else ())
    if "runtime/seccomp.json" not in sums:
        raise InstallError("The distribution is missing required node checksums")
    state = {"installation_id": args.installation_id, "provider": args.provider, "core_url": args.core_url,
             "source_commit": manifest["source_commit"], "generation": args.configuration["generation"],
             "specification_digest": args.configuration["specification_digest"]}
    write_once(root / "installation.json", json_text(state))
    for name in names:
        if name == "runtime/seccomp.json":
            if getattr(args, "bundle", None) is None:
                download(args.source_url, name, root, sums[name])
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
            distribution.obtain_artifact(manifest, name, target, getattr(args, "bundle", None))
            os.chmod(target, 0o700)
    safe_directory(root / "state/node")
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


def install(args, token):
    """Non-root mode: the node runs as this user's systemd user service."""
    if (SYSTEM_RECORDS / (args.installation_id + ".json")).exists():
        raise InstallError("This host already runs a node for this installation as a system service. Rerun the command "
                           "with sudo, or uninstall that node with sudo first." + NOTHING_CHANGED)
    root = open_node(args, token)
    with install_lock(root):
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


def prepare_service_node(args, token):
    """Sudo mode, as the service user: everything but the root-owned system unit."""
    root = open_node(args, token, system=True)
    with install_lock(root):
        register_node(root, args, token)



# Sudo mode -----------------------------------------------------------------

class ChildFailed(Exception):
    """The service-user step failed and already printed why."""


def as_service_user(account, function, *arguments):
    """Run a step with the service user's credentials in a forked child.

    Every installer module is already imported, so the child never reads the root
    caller's private copy of this program. The token stays in memory."""
    sys.stdout.flush()
    sys.stderr.flush()
    pid = os.fork()
    if pid == 0:
        code = 1
        try:
            os.setgroups(os.getgrouplist(account.pw_name, account.pw_gid))
            os.setgid(account.pw_gid)
            os.setuid(account.pw_uid)
            os.umask(0o077)
            os.environ.clear()
            os.environ.update(HOME=account.pw_dir, USER=account.pw_name, LOGNAME=account.pw_name, PATH=SAFE_PATH, LANG="C.UTF-8")
            os.chdir(account.pw_dir)
            function(*arguments)
            code = 0
        except (InstallError, node_spec.SpecificationError, distribution.DistributionError) as error:
            print(str(error), file=sys.stderr)
        except (OSError, ValueError, KeyError, subprocess.SubprocessError):
            print("Node installation failed; check host prerequisites and retained private files", file=sys.stderr)
        finally:
            sys.stdout.flush()
            sys.stderr.flush()
            os._exit(code)
    _, status = os.waitpid(pid, 0)
    if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
        raise ChildFailed()


run_as = as_service_user


@contextlib.contextmanager
def system_lock(installation_id):
    descriptor = os.open(SYSTEM_LOCKS / ("parsar-node-" + installation_id + ".lock"), os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise InstallError("Another node installation or uninstallation for this installation is running") from None
        yield


def host_checks():
    if sys.version_info < (3, 9) or platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64"):
        raise InstallError("Run with Python 3.9+ on Linux amd64." + NOTHING_CHANGED)
    if not SYSTEMD_RUNNING.is_dir():
        raise InstallError("systemd is not the init system here, so this host cannot run the node service." + NOTHING_CHANGED)
    for tool in ("systemctl", "useradd", "usermod", "userdel"):
        if shutil.which(tool) is None:
            raise InstallError(tool + " is required to prepare the node service user." + NOTHING_CHANGED)
    try:
        enforcing = SELINUX_ENFORCE.read_text().strip() == "1"
    except OSError:
        enforcing = False
    if enforcing:
        raise InstallError("SELinux is enforcing on this host, which sudo mode does not support yet. Run the no-sudo "
                           "command as a prepared user instead." + NOTHING_CHANGED)


def root_file(path, content):
    """Write a root-owned 0644 file, or accept an identical existing one."""
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise InstallError(str(path) + " must be a regular file")
    if path.exists():
        if path.stat().st_uid != os.geteuid() or path.read_text() != content:
            raise InstallError(str(path) + " differs from this installer's version; preserve it and inspect the node")
        return
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o755)
    descriptor, temporary = tempfile.mkstemp(prefix=".parsar-node-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w") as stream:
            stream.write(content)
        os.chmod(temporary, 0o644)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def read_root_json(path):
    if not path.exists():
        return None
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != os.geteuid():
        raise InstallError(str(path) + " must be a regular file owned by root")
    return json.loads(path.read_text())


def service_account():
    try:
        return pwd.getpwnam(SERVICE_USER)
    except KeyError:
        return None


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
        return group_name(info.st_gid), details
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
    return group_name(info.st_gid), {}


def group_name(gid):
    try:
        return grp.getgrgid(gid).gr_name
    except KeyError:
        raise InstallError("The device's group " + str(gid) + " has no name; give it a named group, then rerun." + NOTHING_CHANGED) from None


def other_node(args, provider):
    """Refuse a second node for this installation on the same host or Docker engine."""
    sudo_user = os.environ.get("SUDO_USER", "")
    if sudo_user and sudo_user != "root":
        try:
            home = Path(pwd.getpwnam(sudo_user).pw_dir)
        except KeyError:
            home = None
        if home is not None and (home / ".parsar/nodes" / args.installation_id).exists():
            raise InstallError("This host already runs a node for this installation, installed without sudo by " + sudo_user
                               + ". Remove it on the Nodes page and uninstall it as that user first." + NOTHING_CHANGED)
    if provider == "docker" and not (SERVICE_HOME / ".parsar/nodes" / args.installation_id).exists():
        networks = checked(list(DOCKER) + ["network", "ls", "--format", "{{.Name}}"], "Cannot inspect Docker networks").splitlines()
        if "parsar-node-" + args.installation_id in networks:
            raise InstallError("Another node for this installation already uses this Docker engine (network parsar-node-"
                               + args.installation_id + "). Remove it on the Nodes page and uninstall it first." + NOTHING_CHANGED)


def prepare_account(group):
    """Create or adopt the service user and give it the provider's group."""
    account = service_account()
    if account is None:
        shell = next((path for path in ("/usr/sbin/nologin", "/sbin/nologin") if os.path.exists(path)), "/bin/false")
        checked(["useradd", "--system", "--user-group", "--no-create-home", "--home-dir", str(SERVICE_HOME),
                 "--shell", shell, SERVICE_USER], "Cannot create the " + SERVICE_USER + " service user")
        account = service_account()
        root_file(SYSTEM_RECORDS / "user.json", json_text({"format": 1, "created": True, "uid": account.pw_uid}))
    elif account.pw_dir != str(SERVICE_HOME) or not account.pw_shell.endswith(("nologin", "false")):
        raise InstallError("An account named " + SERVICE_USER + " exists but was not created by this installer (home "
                           + account.pw_dir + ", shell " + account.pw_shell + "). Rename or remove it, then rerun." + NOTHING_CHANGED)
    if SERVICE_HOME.is_symlink():
        raise InstallError(str(SERVICE_HOME) + " must not be a symlink")
    if not SERVICE_HOME.exists():
        SERVICE_HOME.mkdir(mode=0o700, parents=True)
        os.chown(SERVICE_HOME, account.pw_uid, account.pw_gid)
    if SERVICE_HOME.stat().st_uid != account.pw_uid:
        raise InstallError(str(SERVICE_HOME) + " is owned by another user; preserve it and inspect the host")
    os.chmod(SERVICE_HOME, 0o700)
    if group and SERVICE_USER not in grp.getgrnam(group).gr_mem:
        checked(["usermod", "--append", "--groups", group, SERVICE_USER], "Cannot add " + SERVICE_USER + " to the " + group + " group")
    return service_account()


def system_unit(root, provider):
    def quote(value):
        return '"' + str(value).replace("\\", "\\\\").replace('"', '\\"').replace("%", "%%") + '"'
    after = "network-online.target docker.service" if provider == "docker" else "network-online.target"
    # Root owns this file; the service user can change only its own node files.
    return ("[Unit]\nDescription=Parsar sandbox node " + root.name + "\nWants=network-online.target\nAfter=" + after
            + "\nStartLimitIntervalSec=0\n\n[Service]\nType=exec\nUser=" + SERVICE_USER + "\nGroup=" + SERVICE_USER
            + "\nExecStart=:" + quote(root / COMMON[0]) + " run --config " + quote(root / "provider.json")
            + " --state-dir " + quote(root / "state/node") + "\nWorkingDirectory=" + str(root).replace("%", "%%")
            + "\nRestart=on-failure\nRestartSec=5s\nRestartPreventExitStatus=78\nKillMode=process\nUMask=0077"
            + "\n\n[Install]\nWantedBy=multi-user.target\n")


def install_system(args, token):
    """Sudo mode: prepare the host, then run the node as a root-owned system service."""
    host_checks()
    with system_lock(args.installation_id):
        record_path = SYSTEM_RECORDS / (args.installation_id + ".json")
        record = read_root_json(record_path)
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
        account = prepare_account(group)
        root = Path(account.pw_dir) / ".parsar/nodes" / args.installation_id
        unit = SYSTEM_UNITS / unit_name(args.installation_id)
        root_file(record_path, json_text({"format": 1, "installation_id": args.installation_id, "provider": provider,
                                          "core_url": args.core_url, "node_root": str(root), "unit": str(unit)}))
        args.system, args.provider = True, provider
        run_as(account, prepare_service_node, args, token)
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

def confirm_removed(root, owner, force):
    """Continue only once Core rejects the node's credential, or when it never registered."""
    identity_file = root / "state/node/identity.json"
    if not (root / "registered.json").exists() and not identity_file.exists():
        return
    if force:
        print("Skipping the Core check (--force).", flush=True)
        return
    if identity_file.is_symlink() or not identity_file.is_file() or identity_file.stat().st_uid != owner:
        raise InstallError("Retained node identity is missing or unsafe; rerun with --force only if Core no longer exists." + NOTHING_CHANGED)
    try:
        stored = json.loads(identity_file.read_text())
        node_id, credential, core = stored["identity"]["node_id"], stored["credential"], stored["core_url"]
    except (ValueError, KeyError, TypeError):
        raise InstallError("Retained node identity is invalid; rerun with --force only if Core no longer exists." + NOTHING_CHANGED) from None
    request = urllib.request.Request(core + "/api/v1/sandbox-node/identity?" + urlencode({"node_id": node_id}),
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


def release_docker_network(root, installation_id):
    """Remove the node's Docker network; never containers, volumes or images."""
    if shutil.which("docker") is None:
        return
    network = "parsar-node-" + installation_id
    try:
        if network not in checked(list(DOCKER) + ["network", "ls", "--format", "{{.Name}}"], "Docker unavailable").splitlines():
            return
        remaining = checked(list(DOCKER) + ["ps", "--all", "--filter", "label=io.parsar.agents-api.installation=" + installation_id,
                                            "--format", "{{.Names}}"], "Docker unavailable").split()
    except InstallError:
        print("Docker is unavailable, so network " + network + " was not removed.")
        return
    if remaining:
        print("Kept Docker network " + network + ": these containers still exist: " + ", ".join(remaining))
        return
    checked(list(DOCKER) + ["network", "rm", network], "Cannot remove Docker network " + network)


def remove_node_files(root, installation_id, home):
    try:
        image = json.loads((root / "provider.json").read_text()).get("docker", {}).get("image")
    except (OSError, ValueError, AttributeError):
        image = None
    runtime_home = home / ".parsar/m" / hashlib.sha256(installation_id.encode()).hexdigest()[:12]
    try:
        owned = json.loads((runtime_home / "parsar-installation.json").read_text()).get("installation_id") == installation_id
    except (OSError, ValueError, AttributeError):
        owned = False
    if owned and not runtime_home.is_symlink():
        shutil.rmtree(runtime_home)
    if root.exists() and not root.is_symlink():
        shutil.rmtree(root)
    if image:
        print("Kept the Runtime image " + image + "; remove it with `docker image rm " + image + "` if no other node uses it.")


def uninstall_system(args):
    """Sudo mode: remove the system service, the node files and, when unused, the service user."""
    if shutil.which("systemctl") is None:
        raise InstallError("systemctl is required." + NOTHING_CHANGED)
    with system_lock(args.installation_id):
        record_path = SYSTEM_RECORDS / (args.installation_id + ".json")
        read_root_json(record_path)
        account = service_account()
        home = Path(account.pw_dir) if account else SERVICE_HOME
        root = home / ".parsar/nodes" / args.installation_id
        unit = SYSTEM_UNITS / unit_name(args.installation_id)
        if not record_path.exists() and not root.exists() and not unit.exists():
            print("No node for installation " + args.installation_id + " was installed with sudo on this host. If it was "
                  "installed without sudo, run the uninstall command as that user without sudo.")
        else:
            confirm_removed(root, account.pw_uid if account else -1, args.force)
            if unit.exists():
                checked(["systemctl", "disable", "--now", unit.name], "Cannot stop the node service " + unit.name)
                unit.unlink()
                checked(["systemctl", "daemon-reload"], "Cannot reload systemd")
            release_docker_network(root, args.installation_id)
            remove_node_files(root, args.installation_id, home)
            record_path.unlink(missing_ok=True)
            print("Node for installation " + args.installation_id + " uninstalled from this host.")
        remove_unused_account(account)


def remove_unused_account(account):
    """Delete the service user only when this installer created it and no node uses it."""
    created = read_root_json(SYSTEM_RECORDS / "user.json")
    if not account or not created or not created.get("created") or any(SYSTEM_RECORDS.glob("*-*-*-*-*.json")):
        return
    checked(["userdel", SERVICE_USER], "Cannot remove the " + SERVICE_USER + " user; stop its processes and rerun the uninstall command")
    if SERVICE_HOME.exists() and not SERVICE_HOME.is_symlink():
        shutil.rmtree(SERVICE_HOME)
    (SYSTEM_RECORDS / "user.json").unlink()
    with contextlib.suppress(OSError):
        SYSTEM_RECORDS.rmdir()
    print("Removed the " + SERVICE_USER + " service user, which this installer created.")


def uninstall_user(args):
    root = Path.home() / ".parsar/nodes" / args.installation_id
    if not root.exists():
        print("No node for installation " + args.installation_id + " is installed for this user. If it was installed "
              "with sudo, run the uninstall command with sudo.")
        return
    confirm_removed(root, os.getuid(), args.force)
    unit = root / unit_name(args.installation_id)
    service = unit.exists()
    if service:
        user_bus()
        checked(["systemctl", "--user", "disable", "--now", unit.name], "Cannot stop the node service " + unit.name)
    release_docker_network(root, args.installation_id)
    remove_node_files(root, args.installation_id, Path.home())
    if service:
        checked(["systemctl", "--user", "daemon-reload"], "Cannot reload the systemd user manager")
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


def read_token(args, parser):
    """The one-time token comes on standard input, never in argv or a sudo command line."""
    # The installer's own local node passes it in this variable to its child process.
    environment = os.environ.pop("PARSAR_NODE_ENROLLMENT_TOKEN", None)
    if args.enrollment_token_stdin and environment is not None:
        parser.error("pass the enrollment token on standard input only")
    if not args.enrollment_token_stdin:
        return environment or ""
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
    parser.add_argument("--uninstall", action="store_true", help="Remove this host's node after it was removed on the Nodes page")
    parser.add_argument("--force", action="store_true", help="With --uninstall: skip the Core check, for a Core that no longer exists")
    args = parser.parse_args(argv)
    if str(uuid.UUID(args.installation_id)) != args.installation_id:
        raise InstallError("Installation ID must be a canonical UUID")
    if args.uninstall:
        if args.source_url or args.bundle or args.core_url or args.provider or args.enrollment_token_stdin:
            parser.error("--uninstall takes only --installation-id and --force")
        os.environ.pop("PARSAR_NODE_ENROLLMENT_TOKEN", None)
        (uninstall_system if os.geteuid() == 0 else uninstall_user)(args)
        return
    if args.force:
        parser.error("--force applies only to --uninstall")
    if not (args.source_url or args.bundle) or not args.core_url:
        parser.error("--source-url (or --bundle) and --core-url are required")
    if args.bundle is not None and (not args.bundle.is_absolute() or args.bundle.resolve() != args.bundle):
        raise InstallError("Local bundle must be an absolute directory without symlinks")
    token = read_token(args, parser)
    if len(token) > 4096 or any(c.isspace() for c in token):
        raise InstallError("A valid one-time enrollment credential is required")
    if os.geteuid() == 0:
        install_system(args, token)
    else:
        install(args, token)


if __name__ == "__main__":
    try:
        main()
    except ChildFailed:
        sys.exit(1)
    except (InstallError, node_spec.SpecificationError, distribution.DistributionError, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(str(error) if isinstance(error, (InstallError, node_spec.SpecificationError, distribution.DistributionError)) else "Node installation failed; check host prerequisites and retained private files", file=sys.stderr)
        sys.exit(1)
