#!/usr/bin/env python3
"""Install and enroll one node from its Core console's matched distribution."""
import argparse
import fcntl
import hashlib
import http.client
import io
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
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


def checked(arguments, failure, **kwargs):
    try:
        result = subprocess.run(arguments, check=False, stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=kwargs.pop("timeout", 30), **kwargs)
        if result.returncode:
            raise InstallError(failure)
        return result.stdout.decode().strip()
    except (OSError, subprocess.SubprocessError):
        raise InstallError(failure) from None


def preflight(provider):
    if sys.version_info < (3, 9) or platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64") or os.getuid() == 0:
        raise InstallError("Run with Python 3.9+ as a non-root user on Linux amd64")
    checked(["systemctl", "--user", "show", "--property=Version", "--value"], "A systemd user session is required")
    if checked(["loginctl", "show-user", str(os.getuid()), "--property=Linger", "--value"], "Cannot check user lingering") != "yes":
        raise InstallError("Ask the host administrator to enable user lingering before installing a node")
    if provider == "docker":
        checked(["docker", "--host", "unix:///var/run/docker.sock", "info", "--format", "{{.ServerVersion}}"], "Docker access through /var/run/docker.sock is required")
    elif not os.access("/dev/kvm", os.R_OK | os.W_OK):
        raise InstallError("microsandbox requires read/write access to /dev/kvm")


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
    if not manifest.get("artifact_base_url") and source:
        manifest["artifact_base_url"] = source + "/node-install/artifacts"
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
            **args.configuration["specification"]["resources"], "idle_seconds": 300, "retention_seconds": 86400,
            "max_active": 4, "max_retained": 16,
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
    return ("[Unit]\nDescription=Parsar sandbox node\n\n[Service]\nType=exec\nExecStart=:"
            + quote(root / COMMON[0]) + " run --config " + quote(root / "provider.json") + " --state-dir " + quote(root / "state/node")
            + "\nWorkingDirectory=" + str(root).replace("%", "%%")
            + "\nRestart=on-failure\nKillMode=process\nUMask=0077\n\n[Install]\nWantedBy=default.target\n")


def install(args, token):
    root = Path.home() / ".parsar/nodes" / args.installation_id
    safe_directory(root)
    identity_file = root / "state/node/identity.json"
    retained = json.loads(identity_file.read_text()) if existing_file(identity_file) else None
    print("Reading the Core deployment specification...", flush=True)
    args.configuration = node_spec.fetch(args, token, retained, open_request, allow_enrollment=not (root / "registered.json").exists())
    args.provider = args.configuration["provider"]
    print("Checking host requirements...", flush=True)
    preflight(args.provider)
    if args.provider == "microsandbox":
        runtime_home = micro_home(args.installation_id)
        safe_directory(runtime_home)
        owner = runtime_home / "parsar-installation.json"
        if not owner.exists() and any(runtime_home.iterdir()):
            raise InstallError("Microsandbox home contains unowned state; refusing to adopt it")
        write_once(owner, json_text({"installation_id": args.installation_id}))
    root = Path.home() / ".parsar/nodes" / args.installation_id
    safe_directory(root)
    descriptor = os.open(root / "install.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise InstallError("Another node installation is running") from None
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
        unit = root / ("parsar-node-" + args.installation_id + ".service")
        write_once(unit, service_unit(root))
        marker = root / "registered.json"
        if not existing_file(marker):
            # The one-time credential is never passed through process arguments or service environments.
            descriptor, secret_path = tempfile.mkstemp(prefix=".enrollment-", dir=root)
            try:
                with os.fdopen(descriptor, "w") as secret:
                    secret.write(token)
                print("Registering this node with Core...", flush=True)
                checked([str(root / COMMON[0]), "register", "--config", str(root / "provider.json"), "--state-dir", str(root / "state/node"),
                         "--core-url", args.core_url, "--name", socket.gethostname(), "--max-active", "4", "--max-retained", "16",
                         "--enrollment-token-file", secret_path], "Node enrollment was not confirmed. Check the Core URL, enrollment expiry and local provider prerequisites; keep its state and rerun the command to recover.")
                write_once(marker, json_text(state))
            finally:
                if os.path.exists(secret_path):
                    os.unlink(secret_path)
        elif marker.read_text() != json_text(state):
            raise InstallError("Registered node identity differs; refusing to replace it")
        print("Starting the node service...", flush=True)
        checked(["systemctl", "--user", "daemon-reload"], "Cannot reload the systemd user manager")
        checked(["systemctl", "--user", "enable", "--now", str(unit)], "Cannot start the node service; retained identity is unchanged")
        checked(["systemctl", "--user", "is-active", "--quiet", unit.name], "Node service is unavailable; inspect its systemd user journal")
        print("Waiting for Core connection and provider readiness...", flush=True)
        wait_ready(root, args)
    print("Node connected to Core and provider ready. State: " + str(root))


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
    request = urllib.request.Request(args.core_url + "/core/v1/sandbox/node/identity?" + urlencode({"node_id": identity["node_id"]}),
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
    raise InstallError(detail + "; state and service are retained. Inspect journalctl --user -u parsar-node-"
                       + args.installation_id + ".service, then rerun the installation command")


def main(argv=None):
    token = os.environ.pop("PARSAR_NODE_ENROLLMENT_TOKEN", "")
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--source-url", type=origin)
    source.add_argument("--bundle", type=Path)
    parser.add_argument("--core-url", type=origin, required=True)
    parser.add_argument("--provider", choices=("docker", "microsandbox"), help="Optional assertion; Core owns provider selection")
    parser.add_argument("--installation-id", required=True)
    args = parser.parse_args(argv)
    if str(uuid.UUID(args.installation_id)) != args.installation_id:
        raise InstallError("Installation ID must be a canonical UUID")
    if args.bundle is not None and (not args.bundle.is_absolute() or args.bundle.resolve() != args.bundle):
        raise InstallError("Local bundle must be an absolute directory without symlinks")
    if len(token) > 4096 or any(c.isspace() for c in token):
        raise InstallError("A valid one-time enrollment credential is required")
    install(args, token)


if __name__ == "__main__":
    try:
        main()
    except (InstallError, node_spec.SpecificationError, distribution.DistributionError, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(str(error) if isinstance(error, (InstallError, node_spec.SpecificationError, distribution.DistributionError)) else "Node installation failed; check host prerequisites and retained private files", file=sys.stderr)
        sys.exit(1)
