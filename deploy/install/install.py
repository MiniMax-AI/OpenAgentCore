#!/usr/bin/env python3
"""Install one matched Core distribution without changing execution ownership."""
import argparse
import base64
import hashlib
import json
import ipaddress
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
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit
import uuid

from configuration import compose_config, core_environment, environment_text, read_core_environment
import local_node
import native_service
from distribution import DistributionError, artifact, image_identities, ensure_docker_image


class InstallError(Exception):
    pass


def run(args, **kwargs):
    # Never print a generated Compose file, process environment or secret value.
    return subprocess.run(args, check=True, **kwargs)


def private_write(path, value):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        stream.write(value)


def write_json(path, value):
    private_write(path, json.dumps(value, indent=2) + "\n")


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
    required = {"manifest.json", "install.sh", "install.py", "configuration.py", "native_service.py", "local_node.py", "node_spec.py", "node-install.pyz", "self-hosted-install.pyz", "distribution.py", "runtime/seccomp.json"}
    required.update(f"images/{name}.tar" for name in ("core", "web", "database"))
    required.update("native/bin/" + name for name in ("agents-api", "agents-api-migrate"))
    required.add("native/e2b/agents-api-e2b-provider")
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


def core_target(value):
    from urllib.parse import urlsplit
    parsed = urlsplit(value)
    if (parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or
            parsed.password or parsed.query or parsed.fragment or parsed.path not in ("", "/")):
        raise argparse.ArgumentTypeError("Core URL must be an HTTP(S) origin without credentials")
    if parsed.scheme != "https" and parsed.hostname not in ("127.0.0.1", "localhost"):
        raise argparse.ArgumentTypeError("Remote Core requires HTTPS")
    return value.rstrip("/")


def loopback_origin(value):
    hostname = urlsplit(value or "").hostname
    try:
        return ipaddress.ip_address(hostname).is_loopback
    except ValueError:
        return hostname == "localhost"


def origin_port(value):
    parsed = urlsplit(value)
    return parsed.port or (443 if parsed.scheme == "https" else 80)


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--core-only", action="store_true")
    modes.add_argument("--web-only", action="store_true")
    parser.add_argument("--native-core", action="store_true", help="Run Core as a systemd user service")
    parser.add_argument("--sandbox-provider", choices=("true", "false"), nargs="?", const="true", default="false",
                        help="Prepare a local sandbox provider (default: false)")
    parser.add_argument("--provider", choices=("microsandbox", "docker"),
                        help="Local sandbox provider when enabled (default: microsandbox)")
    parser.add_argument("--install-dir", type=Path, default=Path.home() / ".parsar/core")
    parser.add_argument("--core-port", type=int, default=8091)
    parser.add_argument("--web-port", type=int, default=8080)
    parser.add_argument("--core-url", type=core_target)
    parser.add_argument("--public-url", type=core_target, help="Public HTTPS Core/Web origin behind your TLS reverse proxy")
    parser.add_argument("--core-key-file", type=Path, help="Web-only: private file containing the existing Core's Core key")
    parser.add_argument("--admin-token-file", type=Path, help=argparse.SUPPRESS)
    parser.add_argument("--status", action="store_true", help="Read installation health; never invoke a model")
    parser.add_argument("--stop", action="store_true", help="Stop installed services; retain all data")
    args = parser.parse_args(argv)
    if args.admin_token_file:
        parser.error("--admin-token-file was renamed; use --core-key-file")
    if args.web_only and args.native_core:
        parser.error("--web-only cannot install native Core")
    args.sandbox_provider = args.sandbox_provider == "true"
    if args.provider and not args.sandbox_provider:
        parser.error("--provider requires --sandbox-provider true")
    if args.web_only and args.sandbox_provider:
        parser.error("--web-only cannot install a sandbox provider")
    args.provider = (args.provider or "microsandbox") if args.sandbox_provider else None
    if args.provider and not (args.status or args.stop):
        if urlsplit(args.public_url or "").scheme != "https" or loopback_origin(args.public_url):
            parser.error("Local sandbox installation requires --public-url with HTTPS reachable from sandbox guests; loopback origins cannot be used")
    if args.status and args.stop:
        parser.error("Choose status or stop")
    if not args.install_dir.is_absolute():
        parser.error("--install-dir must be absolute")
    if any(not 1024 <= p <= 65535 for p in (args.core_port, args.web_port)):
        parser.error("Ports must be between 1024 and 65535")
    if not args.core_only and not args.web_only and args.core_port == args.web_port:
        parser.error("Core and Web need different ports")
    if args.web_only and not (args.core_url and args.core_key_file):
        parser.error("--web-only requires --core-url and --core-key-file")
    if not args.web_only and (args.core_url or args.core_key_file):
        parser.error("Existing Core connection flags require --web-only")
    return args


def compose(root, *args, **kwargs):
    return run(["docker", "compose", "-f", str(root / "compose.json"), *args], **kwargs)


def check_compose():
    version = run(["docker", "compose", "version", "--short"], capture_output=True, text=True).stdout.strip()
    match = re.fullmatch(r"v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?", version)
    if not match or tuple(map(int, match.groups())) < (2, 26, 0):
        raise InstallError("Docker Compose 2.26.0 or newer is required for literal Core environment values")


def wait_http(url, headers=None, attempts=60):
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, response_headers, newurl):
            return None

    # Probe credentials belong only to this endpoint, never a redirect or an
    # ambient HTTP proxy. This also applies to the remote web-only Core probe.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    for attempt in range(attempts):
        try:
            request = urllib.request.Request(url, headers=headers or {})
            with opener.open(request, timeout=2) as response:
                if response.status == 200:
                    return True
        except (urllib.error.URLError, TimeoutError):
            pass
        if attempt + 1 < attempts:
            time.sleep(1)
    return False


def status(root, state):
    output = compose(root, "ps", "--all", "--format", "json", capture_output=True, text=True).stdout
    # Compose versions may return one array or one object per line.
    rows = json.loads(output) if output.lstrip().startswith("[") else [json.loads(line) for line in output.splitlines() if line]
    required = {"web"} if state["mode"] == "web-only" else {"database", "core"}
    if state["mode"] == "all":
        required.add("web")
    observed = {row["Service"]: row for row in rows}
    if native_service.is_native(state):
        observed["core"] = {"State": "running" if native_service.active(state) else "stopped"}
        print("Core service: " + observed["core"]["State"])
    healthy = all(name in observed and observed[name]["State"] == "running"
                  and observed[name].get("Health", "") in ("", "healthy") for name in required)
    for row in rows:
        print(f'{row["Service"]}: {row["State"]} {row.get("Health", "")}')
    if state["mode"] != "web-only":
        core_ok = wait_http(f'http://127.0.0.1:{state["core_port"]}/healthz', attempts=1)
        healthy = healthy and core_ok
        print("Core API: " + ("healthy" if core_ok else "unavailable"))
    if state["mode"] != "core-only":
        web_ok = wait_http(f'http://127.0.0.1:{state["web_port"]}/healthz', attempts=1)
        healthy = healthy and web_ok
        print("Web: " + ("healthy" if web_ok else "unavailable"))
    print("Service health does not prove model execution. This check makes no model requests.")
    if not healthy:
        raise InstallError("One or more installed services are unavailable")


def initialize(root, args, manifest):
    mode = "core-only" if args.core_only else "web-only" if args.web_only else "all"
    if (root / "installation.json").exists():
        state = json.loads((root / "installation.json").read_text())
        wanted = (mode, args.native_core, args.core_port, args.web_port, args.core_url, args.public_url)
        actual = (state["mode"], state["native_core"], state["core_port"], state["web_port"], state.get("core_url"), state.get("public_url"))
        if wanted != actual or state["source_commit"] != manifest["source_commit"]:
            raise InstallError("Existing installation differs; preserve it and follow the upgrade guide")
        services = json.loads((root / "compose.json").read_text())["services"]
        for service, config in services.items():
            name = "core" if service == "migrate" else service
            if config.get("image") != manifest["images"].get(name):
                raise InstallError("Retained Docker image differs; preserve the installation and inspect its configuration")
        if (root / "config/managed-runtimes.json").exists():
            raise InstallError("Retired file-managed provider configuration exists; preserve its resources and follow the deployment replacement guide")
        if mode != "web-only":
            read_core_environment(root, state)
            directory = root / "state/e2b"
            if (not directory.is_dir() or directory.is_symlink() or
                    stat.S_IMODE(directory.stat().st_mode) & 0o077):
                raise InstallError("Private provider receipts are missing or unsafe; restore the retained installation")
        return state
    if root.exists() and any(root.iterdir()):
        raise InstallError("Installation directory is not empty; refusing to overwrite existing state")
    if mode == "web-only":
        source = args.core_key_file
        info = source.stat()
        if (not source.is_absolute() or source.is_symlink() or not stat.S_ISREG(info.st_mode)
                or stat.S_IMODE(info.st_mode) & 0o077 or info.st_size > 4096):
            raise InstallError("Core key file must be an absolute, private regular file")
        token = source.read_text().strip()
        if not token or any(c.isspace() for c in token) or "\x00" in token:
            raise InstallError("Invalid Core key file")
        if len(token) < 32:
            raise InstallError("The Core key must have at least 32 characters")
    if mode != "web-only":
        free_port(args.core_port)
    if mode != "core-only":
        free_port(args.web_port)
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(root, 0o700)
    directories = ["config", "admin"]
    if mode != "web-only":
        directories += ["state", "state/e2b"]
    for name in directories:
        (root / name).mkdir(mode=0o700)
    state = {"version": 1, "source_commit": manifest["source_commit"], "mode": mode,
             "native_core": args.native_core, "installation_id": str(uuid.uuid4()),
             "project": "parsar-" + secrets.token_hex(5), "uid": os.getuid(), "gid": os.getgid(),
             "core_port": args.core_port, "web_port": args.web_port, "core_url": args.core_url, "public_url": args.public_url}
    if native_service.is_native(state):
        state["database_port"] = database_port()
    config = root / "config"
    if mode != "web-only":
        private_write(config / "credential.key", base64.b64encode(secrets.token_bytes(32)).decode())
        private_write(config / "database.password", secrets.token_hex(32))
        core_key = secrets.token_hex(32)
        private_write(root / "admin/core.key", core_key)
        write_json(root / "admin/core-key-digests.json", [hashlib.sha256(core_key.encode()).hexdigest()])
    if mode == "web-only":
        private_write(root / "admin/core.key", token)
    password = (config / "database.password").read_text() if mode != "web-only" else ""
    if mode != "web-only":
        private_write(config / "core.env", environment_text(core_environment(root, state, password)))
    write_json(root / "compose.json", compose_config(root, state, manifest, password))
    write_json(root / "installation.json", state)
    return state


def prepare_node_payload(root, state, bundle):
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


def main(argv=None):
    args = arguments(argv)
    root = args.install_dir
    if root.is_symlink() or root.resolve() != root:
        raise InstallError("Installation directory must be canonical and not a symlink")
    if args.status or args.stop:
        state = json.loads((root / "installation.json").read_text())
        if args.stop:
            if native_service.is_native(state):
                native_service.stop(root, state)
            compose(root, "stop")
            print("Control-plane services stopped. Sandbox resources and data retained; running sandbox work may continue.")
        else:
            status(root, state)
        return
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64") or os.getuid() == 0:
        raise InstallError("Run as a non-root user on Linux amd64 with Docker access")
    check_compose()
    run(["docker", "info", "--format", "{{.ServerVersion}}"], stdout=subprocess.DEVNULL)
    bundle = Path(__file__).resolve().parent
    manifest = verify_bundle(bundle)
    if args.native_core:
        native_service.preflight(bundle, root)
    if args.web_only:
        images = ["web"]
    elif args.native_core:
        images = ["database"]
    else:
        images = ["core", "database"]
    if not args.core_only and not args.web_only:
        images.append("web")
    local_images = dict(manifest["images"])
    for name in images:
        archive = lambda name=name: bundle / f"images/{name}.tar"
        local_images[name] = ensure_docker_image(manifest, name, archive)
    # Deployment configuration uses Docker's local IDs; published metadata is unchanged.
    deployment = dict(manifest, images=local_images)
    state = initialize(root, args, deployment)
    prepare_node_payload(root, state, bundle)
    if native_service.is_native(state):
        native_service.prepare(root, state, bundle)
    compose(root, "up", "--detach", "--wait")
    if native_service.is_native(state):
        migration_environment = read_core_environment(root, state)
        run([str(root / "native/bin/agents-api-migrate")], env=dict(os.environ, **migration_environment),
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        native_service.start(root, state)
    if state["mode"] != "web-only" and not wait_http(f'http://127.0.0.1:{state["core_port"]}/healthz'):
        raise InstallError("Core did not become healthy. Use --status; retained state has not been removed")
    if args.provider:
        local_node.install(root, dict(state, provider=args.provider), manifest, bundle, run)
    public_url = state.get("public_url")
    if state["mode"] != "core-only":
        url = f'http://127.0.0.1:{state["web_port"]}'
        host = urlsplit(public_url or url).netloc
        if not wait_http(url + "/console/auth", {"Host": host}):
            raise InstallError("Web sign-in is unavailable. Use --status and inspect the Web service")
        core_url = state.get("core_url") or f'http://127.0.0.1:{state["core_port"]}'
        token = (root / "admin/core.key").read_text().strip()
        if not wait_http(core_url + "/core/v1/projects", {"Authorization": "Bearer " + token}):
            raise InstallError("Core key authentication failed. Inspect private configuration; no model was called")
        # The console accepts only its configured origin, so a public URL has no loopback console.
        console = public_url or url
        print("Console: " + console + (" (local only)" if loopback_origin(console) else ""))
    if state["mode"] != "web-only":
        api = f'http://127.0.0.1:{state["core_port"]}/v1'
        if public_url and not loopback_origin(public_url):
            print("API base URL: " + public_url + "/v1")
            print("Local-only API on this host: " + api)
        elif public_url and origin_port(public_url) != state["web_port"]:
            print("API base URL: " + public_url + "/v1 (local only)")
        else:
            # Web answers 404 on /v1, so only Core's own port serves the API locally.
            print("API base URL: " + api + " (local only)")
    core_key = root / "admin/core.key"
    if state["mode"] == "core-only":
        print(f'Next: create a Project and its API key through the Core management API at '
              f'http://127.0.0.1:{state["core_port"]}/core/v1 (local only) with the Core key in {core_key}.')
    else:
        print(f"Next: sign in to Web with the Core key in {core_key}, then create a Project and its API key on the Projects and keys page.")
    print("Keep the Core key private; it also authorizes the Core management API.")
    if state["mode"] != "web-only":
        print("Core configuration file: " + str(root / "config/core.env"))
        if args.provider:
            print("Provider: " + args.provider + ". Local node enrolled; Core provisions Sessions on demand.")
        elif state["mode"] == "all":
            print("No execution node was installed by this run. Choose a sandbox backend and add nodes on the Nodes page in Web.")
        else:
            print("No execution node was installed by this run.")
    print("Services installed. No model request was made. See docs/getting-started/quickstart.md.")


if __name__ == "__main__":
    try:
        main()
    except (InstallError, local_node.LocalNodeError, DistributionError, RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        # Errors never include generated configuration or external process output.
        print(str(error) if isinstance(error, (InstallError, local_node.LocalNodeError, DistributionError, RuntimeError)) else "Installation failed; inspect prerequisites and private deployment files", file=sys.stderr)
        sys.exit(1)
