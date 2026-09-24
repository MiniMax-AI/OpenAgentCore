#!/usr/bin/env python3
"""Install one matched Core distribution without changing execution ownership."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import platform
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

from configuration import compose_config, core_environment, managed_config
import native_service
from distribution import DistributionError, artifact, obtain_artifact, runtime_archive, image_identities, ensure_docker_image


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
    required = {"manifest.json", "install.sh", "install.py", "configuration.py", "native_service.py", "node-install.pyz", "self-hosted-install.pyz", "distribution.py", "runtime/seccomp.json"}
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


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--core-only", action="store_true")
    modes.add_argument("--web-only", action="store_true")
    parser.add_argument("--sandbox-provider", choices=("true", "false"), nargs="?", const="true", default="false",
                        help="Prepare a local sandbox provider (default: false)")
    parser.add_argument("--provider", choices=("microsandbox", "docker"),
                        help="Local sandbox provider when enabled (default: microsandbox)")
    parser.add_argument("--install-dir", type=Path, default=Path.home() / ".parsar/core")
    parser.add_argument("--core-port", type=int, default=8091)
    parser.add_argument("--web-port", type=int, default=8080)
    parser.add_argument("--core-url", type=core_target)
    parser.add_argument("--public-url", type=core_target, help="Public HTTPS Core/Web origin behind your TLS reverse proxy")
    parser.add_argument("--admin-token-file", type=Path)
    parser.add_argument("--status", action="store_true", help="Read installation health; never invoke a model")
    parser.add_argument("--stop", action="store_true", help="Stop installed services; retain all data")
    args = parser.parse_args(argv)
    args.sandbox_provider = args.sandbox_provider == "true"
    if args.provider and not args.sandbox_provider:
        parser.error("--provider requires --sandbox-provider true")
    if args.web_only and args.sandbox_provider:
        parser.error("--web-only cannot install a sandbox provider")
    args.provider = (args.provider or "microsandbox") if args.sandbox_provider else None
    if args.status and args.stop:
        parser.error("Choose status or stop")
    if not args.install_dir.is_absolute():
        parser.error("--install-dir must be absolute")
    if any(not 1024 <= p <= 65535 for p in (args.core_port, args.web_port)):
        parser.error("Ports must be between 1024 and 65535")
    if not args.core_only and not args.web_only and args.core_port == args.web_port:
        parser.error("Core and Web need different ports")
    if args.web_only and not (args.core_url and args.admin_token_file):
        parser.error("--web-only requires --core-url and --admin-token-file")
    if not args.web_only and (args.core_url or args.admin_token_file):
        parser.error("Existing Core connection flags require --web-only")
    return args


def compose(root, *args, **kwargs):
    return run(["docker", "compose", "-f", str(root / "compose.json"), *args], **kwargs)


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
        wanted = (mode, args.provider, args.core_port, args.web_port, args.core_url, args.public_url)
        actual = (state["mode"], state["provider"], state["core_port"], state["web_port"], state.get("core_url"), state.get("public_url"))
        if wanted != actual or state["source_commit"] != manifest["source_commit"]:
            raise InstallError("Existing installation differs; preserve it and follow the upgrade/provider-change guide")
        services = json.loads((root / "compose.json").read_text())["services"]
        for service, config in services.items():
            name = "core" if service == "migrate" else service
            if config.get("image") != manifest["images"].get(name):
                raise InstallError("Retained Docker image differs; preserve the installation and inspect its configuration")
        if state["provider"] == "docker":
            managed = json.loads((root / "config/managed-runtimes.json").read_text())
            if managed.get("docker", {}).get("image") != manifest["images"]["runtime"]:
                raise InstallError("Retained Runtime image differs; preserve the installation and inspect its configuration")
        if mode != "web-only":
            directory = root / "state/e2b"
            if (not directory.is_dir() or directory.is_symlink() or
                    stat.S_IMODE(directory.stat().st_mode) & 0o077):
                raise InstallError("Private provider receipts are missing or unsafe; restore the retained installation")
        if state.get("console_auth") == "account":
            # Never let Compose create replacement bind sources for lost auth
            # state. An existing install must retain its account directory.
            directory = root / "state/console"
            if (not directory.is_dir() or directory.is_symlink() or
                    stat.S_IMODE(directory.stat().st_mode) & 0o077):
                raise InstallError("Private console authentication state is missing or unsafe; restore the retained installation")
            if (directory / "registered").exists() and not (directory / "admin.json").is_file():
                raise InstallError("Registered console account is missing; restore its private state backup")
        return state
    if root.exists() and any(root.iterdir()):
        raise InstallError("Installation directory is not empty; refusing to overwrite existing state")
    if mode == "web-only":
        source = args.admin_token_file
        info = source.stat()
        if (not source.is_absolute() or source.is_symlink() or not stat.S_ISREG(info.st_mode)
                or stat.S_IMODE(info.st_mode) & 0o077 or info.st_size > 4096):
            raise InstallError("Administrator token file must be an absolute, private regular file")
        token = source.read_text().strip()
        if not token or any(c.isspace() for c in token) or "\x00" in token:
            raise InstallError("Invalid administrator token file")
    else:
        if args.provider:
            device_gid = os.stat("/dev/kvm" if args.provider == "microsandbox" else "/var/run/docker.sock").st_gid
    if mode != "web-only":
        free_port(args.core_port)
    if mode != "core-only":
        free_port(args.web_port)
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(root, 0o700)
    directories = ["config", "admin"]
    directories.append("state")
    if mode != "web-only":
        directories.append("state/e2b")
    if args.provider:
        directories.append("state/sandbox-node")
    if mode != "core-only":
        directories.append("state/console")
    if args.provider == "microsandbox":
        directories.append("state/msb")
    for name in directories:
        (root / name).mkdir(mode=0o700)
    state = {"version": 1, "source_commit": manifest["source_commit"], "mode": mode,
             "provider": args.provider, "installation_id": str(uuid.uuid4()),
             "project": "parsar-" + secrets.token_hex(5), "uid": os.getuid(), "gid": os.getgid(),
             "core_port": args.core_port, "web_port": args.web_port, "core_url": args.core_url, "public_url": args.public_url}
    if native_service.is_native(state):
        state["database_port"] = database_port()
    config = root / "config"
    if mode != "web-only":
        if args.provider:
            state["device_gid"] = device_gid
        private_write(config / "credential.key", base64.b64encode(secrets.token_bytes(32)).decode())
        private_write(config / "database.password", secrets.token_hex(32))
        admin_token = secrets.token_hex(32)
        private_write(root / "admin/sandbox-admin.key", admin_token)
        write_json(root / "admin/digests.json", [hashlib.sha256(admin_token.encode()).hexdigest()])
        if args.provider:
            write_json(config / "managed-runtimes.json", managed_config(root, state, manifest))
    if mode == "web-only":
        private_write(root / "admin/sandbox-admin.key", token)
    if mode != "core-only":
        state["console_auth"] = "account"
    password = (config / "database.password").read_text() if mode != "web-only" else ""
    write_json(root / "compose.json", compose_config(root, state, manifest, password))
    write_json(root / "installation.json", state)
    return state


def prepare_node_payload(root, state, bundle):
    if state["mode"] != "all":
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


def import_runtime(root, state, manifest, bundle):
    if not native_service.is_native(state):
        return
    runtime = root / "native/microsandbox"
    env = dict(os.environ, MSB_BACKEND="local", MSB_HOME=str(root / "state/msb"),
               MSB_PATH=str(runtime / "msb"), MSB_LIBKRUNFW_PATH=str(runtime / "libkrunfw.so.5.6.1"))
    run([str(runtime / "msb"), "image", "load", "--input", str(bundle / "images/runtime.tar"),
         "--tag", manifest["runtime_ref"], "--quiet"], env=env)


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
    run(["docker", "compose", "version"], stdout=subprocess.DEVNULL)
    run(["docker", "info", "--format", "{{.ServerVersion}}"], stdout=subprocess.DEVNULL)
    if not args.web_only and args.provider == "microsandbox" and not Path("/dev/kvm").exists():
        raise InstallError("microsandbox requires host KVM; enable virtualization or explicitly choose --provider docker")
    bundle = Path(__file__).resolve().parent
    manifest = verify_bundle(bundle)
    if args.provider and not args.web_only:
        print("Preparing the selected local sandbox provider...", flush=True)
        if args.provider == "microsandbox":
            runtime_archive(manifest, bundle, bundle)
            for name in ("native/bin/agents-api-microsandbox-provider", "native/microsandbox/msb",
                         "native/microsandbox/libkrunfw.so.5.6.1"):
                obtain_artifact(manifest, name, bundle / name, bundle)
            native_service.preflight(bundle)
    if args.web_only:
        images = ["web"]
    elif args.provider == "microsandbox":
        images = ["database"]
    else:
        images = ["core", "database"]
    if args.provider == "docker":
        images.append("runtime")
    if not args.core_only and not args.web_only:
        images.append("web")
    local_images = dict(manifest["images"])
    for name in images:
        if name == "runtime":
            archive = lambda: runtime_archive(manifest, bundle, bundle)
        else:
            archive = lambda name=name: bundle / f"images/{name}.tar"
        local_images[name] = ensure_docker_image(manifest, name, archive)
    # Deployment configuration uses Docker's local IDs; published metadata is unchanged.
    deployment = dict(manifest, images=local_images)
    state = initialize(root, args, deployment)
    prepare_node_payload(root, state, bundle)
    if state["provider"] == "docker":
        seccomp = bundle / "runtime/seccomp.json"
        if not (root / "config/seccomp.json").exists():
            private_write(root / "config/seccomp.json", seccomp.read_text())
    if native_service.is_native(state):
        password = (root / "config/database.password").read_text()
        environment = core_environment(root, state, password)
        native_service.prepare(root, state, bundle, environment)
    import_runtime(root, state, manifest, bundle)
    compose(root, "up", "--detach", "--wait")
    if native_service.is_native(state):
        migration_environment = {key: value for key, value in environment.items()
                                 if key != "AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE"}
        run([str(root / "native/bin/agents-api-migrate")], env=dict(os.environ, **migration_environment),
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        native_service.start(root, state)
    if state["mode"] != "web-only" and not wait_http(f'http://127.0.0.1:{state["core_port"]}/healthz'):
        raise InstallError("Core did not become healthy. Use --status; retained state has not been removed")
    if state["mode"] != "core-only":
        url = f'http://127.0.0.1:{state["web_port"]}'
        host = urlsplit(state.get("public_url") or url).netloc
        if state.get("console_auth") == "account":
            if not wait_http(url + "/console/auth", {"Host": host}):
                raise InstallError("Web authentication is unavailable. Inspect private console state")
            core_url = state.get("core_url") or f'http://127.0.0.1:{state["core_port"]}'
            token = (root / "admin/sandbox-admin.key").read_text().strip()
            if not wait_http(core_url + "/core/v1/admin/projects", {"Authorization": "Bearer " + token}):
                raise InstallError("Core administrator authentication failed. Inspect private configuration; no model was called")
            print("Console: " + (state.get("public_url") or url))
            if (root / "state/console/admin.json").exists():
                print("Sign in with your administrator account. Keep its password and private state backup safe.")
            else:
                print("Open Web to register the administrator with your chosen username and password.")
                print("Keep your administrator username and password safe; there is no email password reset.")
        else:
            auth = base64.b64encode(("admin:" + (root / "config/console.password").read_text()).encode()).decode()
            if not wait_http(url + "/core/v1/admin/projects", {"Authorization": "Basic " + auth,
                    "Host": host}):
                raise InstallError("Web could not authenticate to Core. Inspect private configuration; no model was called")
            print("Console: " + (state.get("public_url") or url) + " (user: admin)")
            print("Console password file: " + str(root / "config/console.password"))
    if state["mode"] != "web-only":
        print(f'API: http://127.0.0.1:{state["core_port"]}/v1')
        print("Create a Project and issue its API key through the administrator API before calling the direct Core API.")
        if state["provider"]:
            print("Provider: " + state["provider"] + ". Runtime image prepared; Core provisions Sessions on demand.")
        else:
            print("No execution node installed. Open Hosted Sandbox Manager to choose a provider and add nodes.")
        print("Sandbox administrator key file: " + str(root / "admin/sandbox-admin.key"))
    print("Services installed. No model request was made. See docs/getting-started/quickstart.md.")


if __name__ == "__main__":
    try:
        main()
    except (InstallError, DistributionError, RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        # Errors never include generated configuration or external process output.
        print(str(error) if isinstance(error, (InstallError, DistributionError, RuntimeError)) else "Installation failed; inspect prerequisites and private deployment files", file=sys.stderr)
        sys.exit(1)
