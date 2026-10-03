"""oac: status, start, stop, apply, rotate-core-key and uninstall for one installation.

The installation directory is the directory that holds the command. The bundle it
was installed from is never needed. config.json is the only file an operator edits;
apply renders generated/ from it and converges the running services on that render.
What runs is the truth: each Compose container carries the inputs digest it was
created with (label io.oac.inputs), so an interrupted apply, rotation or
rollback is finished by the next apply.
"""
import argparse
import contextlib
import datetime
import errno
import fcntl
from http.client import HTTPException
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import shlex
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
from urllib.parse import urlsplit

import config_model
import ingress_config
import configuration


SOURCE_COMMIT = None  # Set by the packaged entrypoint from its build revision.
UNSUPPORTED_VERSION = ("This installation version is not supported; "
                       "preserve its data and reinstall into a new empty directory. Nothing was changed.")
INCOMPLETE = ("This installation did not finish installing. Rerun the installer command, which removes what is "
              "left and installs again, or remove it with oac uninstall.")


class OacError(Exception):
    pass


class ApplyFailed(OacError):
    """apply wrote the files, but the services did not converge on them; cause says why."""
    def __init__(self, message, cause):
        super().__init__(message)
        self.cause = cause


def run(args, **kwargs):
    # Never print a generated Compose file, process environment or secret value.
    return subprocess.run(args, **dict({"check": True}, **kwargs))


def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


# Private files ---------------------------------------------------------------

def private_parent(target):
    """Create the canonical installation parent and keep the default home private."""
    parent = target.parent
    if parent.is_symlink() or parent.resolve() != parent:
        raise OacError("The destination's parent must be canonical and not a symlink")
    parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if parent == Path.home() / ".oac":
        os.chmod(parent, 0o700)


def check_private(path, what):
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        raise OacError(f"{what} is missing") from None
    if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) & 0o077
            or info.st_uid != os.geteuid() or info.st_nlink != 1):
        raise OacError(f"{what} must be a regular file with mode 0600, owned by you, and not a link")


def check_directories(root):
    """The installation, secrets/ and generated/ are real private directories of this user."""
    for path, what in ((root, str(root)), (root / "secrets", "secrets/"), (root / "generated", "generated/")):
        try:
            info = os.lstat(path)
        except FileNotFoundError:
            raise OacError(f"{what} is missing") from None
        if (not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) & 0o077
                or info.st_uid != os.geteuid()):
            raise OacError(f"{what} must be a directory with mode 0700, owned by you, and not a link")


def read_private(path, what):
    check_private(path, what)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as stream:
        return stream.read()


def create_private(path, data):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(data.encode() if isinstance(data, str) else data)


def write_private(path, data):
    """Replace a file atomically with a 0600 copy in the same directory."""
    path = Path(path)
    descriptor, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(data.encode() if isinstance(data, str) else data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def load_config(root):
    raw = read_private(root / "config.json", "config.json")
    try:
        document = json.loads(raw)
    except ValueError as error:
        line = getattr(error, "lineno", "?")
        raise OacError(f"config.json is not valid JSON (line {line})") from None
    return config_model.validate(document)


def load_state(root):
    state = json.loads(read_private(root / "state.json", "state.json"))
    if (state.get("format") != 2
            or SOURCE_COMMIT is not None and state.get("source_commit") != SOURCE_COMMIT):
        raise OacError(UNSUPPORTED_VERSION)
    return state


def save_state(root, state):
    write_private(root / "state.json", json.dumps(state, indent=2) + "\n")


def check_complete(state):
    """Only the installer uses an installation before its first start has finished."""
    if state.get("complete") is not True:
        raise OacError(INCOMPLETE)


@contextlib.contextmanager
def locked(root):
    if (root / "state.json").exists():
        load_state(root)
    descriptor = os.open(root / ".oac.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise OacError("Another oac command is running for this installation") from None
        try:
            current = os.stat(root / ".oac.lock", follow_symlinks=False)
        except FileNotFoundError:
            current = None
        if current is None or not os.path.samestat(current, os.fstat(descriptor)):
            # The holder removed the installation, lock file included, after this command opened it.
            raise OacError("Another oac command is running for this installation")
        if (root / "state.json").exists():
            load_state(root)
        yield
    finally:
        os.close(descriptor)


# What runs ---------------------------------------------------------------------

def compose(root, *args, **kwargs):
    return run(["docker", "compose", "-f", str(root / "generated/compose.json"), *args], **kwargs)


INSPECT = ('{{index .Config.Labels "com.docker.compose.service"}}\t{{index .Config.Labels "' + configuration.LABEL
           + '"}}\t{{.State.Status}}\t{{if .State.Health}}{{.State.Health.Status}}{{end}}')


def observe(state):
    """{service: {running, inputs, health}} of this installation's containers."""
    result = {}
    ids = run(["docker", "ps", "-aq", "--filter", f'label=com.docker.compose.project={state["project"]}',
               "--filter", "label=com.docker.compose.oneoff=False"], capture_output=True, text=True).stdout.split()
    if ids:
        for line in run(["docker", "inspect", "--format", INSPECT, *ids], capture_output=True, text=True).stdout.splitlines():
            service, inputs, status, health = (line.split("\t") + ["", "", "", ""])[:4]
            if service:
                result[service] = {"running": status == "running", "inputs": inputs or None, "health": health}
    return result


def tcp_listeners():
    """(address, port) of each listening TCP socket in this network namespace."""
    for table in ("/proc/net/tcp", "/proc/net/tcp6"):
        try:
            lines = Path(table).read_text().splitlines()[1:]
        except OSError:
            continue
        for line in lines:
            fields = line.split()
            if fields[3] != "0A":  # TCP_LISTEN
                continue
            raw, _, port = fields[1].partition(":")
            # The kernel prints each 32-bit word of the address in host byte order, little-endian on amd64.
            packed = b"".join(bytes.fromhex(raw[index:index + 8])[::-1] for index in range(0, len(raw), 8))
            yield ipaddress.ip_address(packed), int(port, 16)


def overlaps(first, second):
    if first.version != second.version:
        # Of two families, only a dual-stack IPv6 wildcard also takes IPv4 addresses.
        return (first if first.version == 6 else second).is_unspecified
    return first.is_unspecified or second.is_unspecified or first == second


def bind_error(host, port):
    """The errno of binding host:port as the services do, or 0 when the bind succeeds.

    SO_REUSEADDR, which Go and Docker listeners set, lets connections in TIME_WAIT pass.
    """
    try:
        with socket.socket(socket.AF_INET6 if ipaddress.ip_address(host).version == 6 else socket.AF_INET) as probe:
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            probe.bind((host, port))
    except OSError as error:
        return error.errno
    return 0


def address_available(host):
    """Whether host is an address of this machine or a wildcard, so services can bind to it."""
    return bind_error(host, 0) not in (errno.EADDRNOTAVAIL, errno.EAFNOSUPPORT)


def port_free(host, port, own=()):
    """Whether a listener could bind host:port now.

    own holds this installation's (address, port) listeners, which do not count. Where
    one overlaps host:port, or the account may not bind a port below 1024, the kernel's
    table of listening sockets decides instead of a bind.
    """
    address = ipaddress.ip_address(host)
    own = {(other, held) for other, held in own if held == port and overlaps(address, other)}
    if not own:
        error = bind_error(host, port)
        if error not in (errno.EACCES, errno.EPERM):
            return error != errno.EADDRINUSE
    return not any(held == port and overlaps(address, other) and (other, held) not in own
                   for other, held in tcp_listeners())


def port_in_use(listener, name, outcome=""):
    """The message for a port that another program holds; name is its flag or config.json key."""
    remedy = "Free it" if listener.purpose == "HTTPS" else "Free it or choose another port"
    return (f"Port {listener.port} ({name}) is already in use on {listener.host}.{outcome} {remedy}; "
            f"find the process with: sudo ss -ltnp 'sport = :{listener.port}'")


def stale(actual, desired, will_run):
    """Services that should run but don't, or run with other inputs."""
    return {name for name in will_run if name != "migrate" and (
        not actual.get(name, {}).get("running") or actual[name]["inputs"] != desired.get(name))}


def converge(root, state, desired, will_run, force=()):
    """Bring every service in will_run to the desired inputs; Core first, then the rest.

    Compose recreates exactly the containers whose configuration (and so label)
    differs.
    """
    actual = observe(state)
    todo = stale(actual, desired, will_run) | set(force)
    if not todo:
        return set()
    up = ["up", "--detach", "--wait", "--wait-timeout", "300"]
    if "core" in todo:
        if "core" in force and not stale(actual, desired, {"core"}):
            compose(root, "restart", "core")
        else:
            compose(root, *up, "core")
    if will_run:
        compose(root, *up)
    for name in sorted(set(force) & will_run - {"core"}):
        if not stale(actual, desired, {name}):
            compose(root, "restart", name)
    return todo


def core_error_line(root):
    """Core's single startup failure line. Core logs no environment values."""
    try:
        output = compose(root, "logs", "--no-log-prefix", "--tail", "200", "core",
                         capture_output=True, text=True).stdout
    except (subprocess.CalledProcessError, OSError):
        return None
    lines = [line.strip() for line in output.splitlines() if "oac-core startup failed" in line]
    return lines[-1] if lines else None


def describe(error):
    """A failure message without command paths, output or environment."""
    if isinstance(error, subprocess.CalledProcessError):
        # A command's own path shows as its name; other paths and options are left out.
        words = [Path(word).name if index == 0 else word for index, word in enumerate(error.cmd)
                 if index == 0 or not word.startswith(("/", "-"))][:3]
        return f"`{' '.join(words)}` failed"
    if isinstance(error, KeyboardInterrupt):
        return "it was interrupted"
    return str(error)


# HTTP ------------------------------------------------------------------------

class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def http(url, headers=None, timeout=5):
    """(status, body). Credentials go only to this URL: no redirects, no ambient proxy."""
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect())
    try:
        with opener.open(urllib.request.Request(url, headers=headers or {}), timeout=timeout) as response:
            return response.status, response.read(1024 * 1024)
    except urllib.error.HTTPError as error:
        return error.code, b""
    except (urllib.error.URLError, OSError, ValueError):
        return 0, b""


def wait_status(url, headers=None, expect=200, attempts=60):
    for attempt in range(attempts):
        status, body = http(url, headers)
        if status == expect:
            return body
        if attempt + 1 < attempts:
            time.sleep(1)
    return None


def bearer(key):
    return {"Authorization": "Bearer " + key}


def core_base(config):
    return configuration.service_origin(config, "core")


def health(root, config, expected):
    """config needs public_url and ports; it may be the view of what was last written."""
    if "core" in expected:
        base = core_base(config)
        if wait_status(base + "/healthz") is None:
            raise OacError("Core did not become healthy")
        if wait_status(base + "/core/v1/installation", bearer(configuration.read_core_key(root)), attempts=10) is None:
            raise OacError("Core did not accept the Core key at /core/v1/installation")
    if "web" in expected:
        url = configuration.service_origin(config, "web")
        path = "/healthz" if ingress_config.enabled(config) else "/console/auth"
        if wait_status(url + path, {"Host": urlsplit(config["public_url"] or url).netloc}) is None:
            raise OacError("Web sign-in is unavailable")


# Files -------------------------------------------------------------------------

def check_fixed(config, state):
    """state.json records the ingress images at installation; it wins over config.json."""
    if ingress_config.enabled(config) != ("ingress" in state["images"]):
        raise OacError("ingress is fixed after installation; install separately to change it")


def check_secrets(root, state):
    reasons = {"credential.key": "Stored credentials can only be read with the original key.",
               "database.password": "PostgreSQL keeps the password it was initialized with."}
    for name, reason in reasons.items():
        data = read_private(root / "secrets" / name, "secrets/" + name)
        if configuration.sha256(data) != state["secrets_sha256"][name]:
            raise OacError(f"secrets/{name} changed since installation. {reason} "
                              "Restore the original file; nothing was applied.")
    check_private(root / "secrets/core.key", "secrets/core.key")
    configuration.read_core_key(root)


def read_generated(root, names):
    result = {}
    for name in names:
        path = root / "generated" / name
        result[name] = path.read_bytes() if path.is_file() and not path.is_symlink() else None
    return result


def comparable(name, data):
    """File content as compared for edits; the snapshot's applied_at is not an edit."""
    if data is not None and name == "settings.json":
        try:
            document = json.loads(data)
            document.pop("applied_at", None)
            return json.dumps(document, sort_keys=True).encode()
        except (ValueError, AttributeError):
            return data
    return data.encode() if isinstance(data, str) else data


def edited_files(state, disk, rendered):
    """Generated files that match neither a digest oac wrote nor the current render.

    state.json keeps the last few digests written for each file, so a run
    interrupted between writing files and state.json is not taken for an edit. A
    missing file is simply written again.
    """
    edited = []
    for name, digests in sorted((state.get("generated") or {}).items()):
        data = disk.get(name)
        if data is None or configuration.sha256(data) in digests:
            continue
        if name in rendered.files and comparable(name, data) == comparable(name, rendered.files[name]):
            continue
        edited.append(name)
    return edited


def record_digests(state, files):
    generated = dict(state.get("generated") or {})
    for name, text in files.items():
        digest = configuration.sha256(text)
        generated[name] = ([item for item in generated.get(name, []) if item != digest] + [digest])[-3:]
    return dict(state, generated=generated)


def disk_view(disk):
    """The settings last written, by key, and the snapshot's stamp.

    Sensitive values are not in the snapshot; the one sensitive setting is read back
    from the runtime-history.json that carries it.
    """
    try:
        document = json.loads(disk.get("settings.json") or b"")
        values = {item["key"]: item["value"] for item in document["settings"]}
    except (ValueError, KeyError, TypeError):
        return None, None
    if "core.runtime_history.headers" in values:
        history = disk.get("runtime-history.json")
        try:
            values["core.runtime_history.headers"] = json.loads(history).get("headers") if history else None
        except (ValueError, AttributeError):
            values["core.runtime_history.headers"] = None
    return values, document.get("applied_at")


def render_now(root, config, state, candidate=None):
    """The render of config.json, keeping the written stamp unless something changed."""
    names = set((state.get("generated") or {}))
    disk = read_generated(root, names | {"settings.json", "runtime-history.json"})
    previous, stamp = disk_view(disk)
    rendered = configuration.render(root, config, state, stamp or now(), candidate)
    disk = read_generated(root, names | set(rendered.files) | {"runtime-history.json"})
    if any(comparable(name, disk.get(name)) != comparable(name, text) for name, text in rendered.files.items()) \
            or any(disk.get(name) is not None for name in names - set(rendered.files)):
        rendered = configuration.render(root, config, state, now(), candidate)
    return rendered, disk, previous


# Apply -----------------------------------------------------------------------

def old_public_url(root, config, previous, disk, actual):
    """The public URL things are bound to: Core's own answer, else the written core.env."""
    old_config = applied_view(previous) if previous else config
    base = core_base(old_config)
    if actual.get("core", {}).get("running"):
        status, body = http(base + "/core/v1/installation", bearer(configuration.read_core_key(root)))
        if status == 200:
            return json.loads(body).get("public_url"), base, True
    try:
        written = configuration.read_environment((disk.get("core.env") or b"").decode())
    except RuntimeError:
        written = {}
    return written.get("OAC_PUBLIC_URL"), base, False


def confirm_public_url(root, config, old, base, core_answered, args, interactive, out):
    """Changing the public URL strands what is bound to the old one; list it and confirm.

    old is None when it can't be read; then the change always needs confirmation.
    """
    new = configuration.local_public_url(config)
    if old == new:
        return
    counts, nodes = None, []
    if core_answered:
        key = configuration.read_core_key(root)
        status, body = http(base + "/core/v1/installation", bearer(key))
        if status == 200:
            counts = json.loads(body).get("address_bindings") or {}
            # The node list only names them; the count comes from Core's own bindings.
            status, body = http(base + "/core/v1/sandbox/nodes", bearer(key))
            nodes = [node for node in (json.loads(body).get("data", []) if status == 200 else [])
                     if node.get("core_url") == old]
    if old is None:
        out(f"The public URL in use can't be read, so the change to {new} needs confirmation.")
    else:
        out(f"The public URL changes from {old} to {new}.")
    if counts is not None:
        bound = max(0, counts.get("nodes", 0) - counts.get("nodes_on_other_address", 0))
        out(f'Bound to the current address: {bound} node(s), {counts.get("hosted_sandboxes", 0)} hosted sandbox(es), '
            f'{counts.get("self_hosted_executors", 0)} self-hosted executor credential(s).')
        for node in nodes:
            out(f'  node {node.get("name")}: {"online" if node.get("online") else "offline"}')
        if not (bound or counts.get("hosted_sandboxes") or counts.get("self_hosted_executors")):
            return
    elif old is not None:
        out("Core is not running, so the nodes, sandboxes and executors bound to the address can't be counted.")
    out("After the change, nodes on the old address get no new sandboxes; remove them in Web and add them again.\n"
        "Existing sandboxes and executors keep working only while the old address still reaches this Core, so\n"
        "keep the old route until they are replaced. Self-hosted executors must restart with the new remote_url.")
    if args.dry_run:
        out("Applying this change needs confirmation.")
    elif args.confirm_public_url_change is not None:
        if args.confirm_public_url_change != new:
            raise OacError(f"--confirm-public-url-change must equal the new public URL {new}; nothing was applied")
    elif interactive:
        if input("Type the new public URL to continue: ").strip() != new:
            raise OacError("The public URL change was not confirmed; nothing was applied")
    else:
        raise OacError(f"Confirm with --confirm-public-url-change {new}; nothing was applied")


def own_listeners(config, previous, disk, actual):
    """(address, port) of this installation's listeners: those of the settings last written, and
    those of the gateway, which domain setup widens before public_url changes, while it runs as written."""
    applied = applied_view(previous)
    own = {(ipaddress.ip_address(listener.host), listener.port) for listener in configuration.listeners(applied)}
    gateway = actual.get("gateway", {})
    if gateway.get("running") and gateway.get("inputs") == configuration.rendered_inputs(disk).get("gateway"):
        own |= ingress_config.written_listeners(disk.get("compose.json"))
    return own


def taken_listeners(config, own, candidate=None):
    """The listeners of config, and of a domain candidate, that another program holds."""
    return [listener for listener in configuration.listeners(config, candidate)
            if (ipaddress.ip_address(listener.host), listener.port) not in own
            and not port_free(listener.host, listener.port, own)]


def check_new_listeners(config, previous, disk, actual, candidate=None):
    """Each listener this change adds must be free; this installation's own listeners do not count."""
    if previous is None:
        return
    applied = applied_view(previous)
    if config["host"] != applied["host"] and not address_available(config["host"]):
        raise OacError(f"{config['host']} (host) is not an address of this machine; use one of its addresses. "
                       "Nothing was applied.")
    taken = taken_listeners(config, own_listeners(config, previous, disk, actual), candidate)
    if taken:
        raise OacError(port_in_use(taken[0], taken[0].setting, " Nothing was applied."))


def finish_apply(root, config, state, gateway_document, will_run, candidate=None, keep_unfinished=False):
    """With a candidate, domain setup verifies it and records its own status. keep_unfinished
    leaves an unfinished domain setup recorded, so its status reports the interruption."""
    managed = ingress_config.enabled(config) and "gateway" in will_run
    if managed:
        import ingress
        # Container input labels cannot prove which configuration Caddy loaded.
        ingress_config.reload(root, gateway_document)
        if config["public_url"] and not candidate:
            ingress.verify(config["public_url"], state["installation_id"])
    health(root, config, will_run)
    if managed and not candidate and not (keep_unfinished and ingress.unfinished(root)):
        ingress.save(root, {"state": "ready" if config["public_url"] else "unconfigured",
                            "public_url": config["public_url"], "target_url": config["public_url"], "message": None})


def apply(root, dry_run=False, discard_edits=False, confirm_public_url_change=None,
          start=False, interactive=None, out=print, retry=None):
    root = Path(root)
    args = argparse.Namespace(dry_run=dry_run, confirm_public_url_change=confirm_public_url_change)
    interactive = sys.stdin.isatty() if interactive is None else interactive
    with locked(root):
        check_complete(load_state(root))
        return _apply(root, args, discard_edits, start, interactive, out, retry=retry)


def _apply(root, args, discard_edits, start, interactive, out, rollback=True, retry=None, candidate=None):
    """candidate: see configuration.render."""
    retry = retry or f"run {root / 'oac'} apply again"
    check_directories(root)
    config = load_config(root)
    state = load_state(root)
    check_fixed(config, state)
    check_secrets(root, state)
    rendered, disk, previous = render_now(root, config, state, candidate)
    edited = edited_files(state, disk, rendered)
    if edited and not discard_edits:
        raise OacError("\n".join(f"generated/{name} was edited by hand." for name in edited)
                          + "\nPut the change in config.json and run oac apply --discard-edits, which keeps the"
                          " edited copy as generated/<file>.edited-<time>. Nothing was applied.")
    actual = observe(state)
    check_new_listeners(config, previous, disk, actual, candidate)
    changed = [name for name, text in rendered.files.items() if disk.get(name) != text.encode()]
    removed = [name for name in (state.get("generated") or {}) if name not in rendered.files and disk.get(name) is not None]

    running = {name for name, item in actual.items() if item["running"] and name != "migrate"}
    will_run = (set(rendered.services) - {"migrate"}) if (start or running) else set()
    todo = stale(actual, rendered.services, will_run)
    force = set()
    if "core" in will_run and "core" not in todo:
        # The digest file follows secrets/core.key; a Core that rejects the key restarts, then Web.
        if http(core_base(config) + "/core/v1/installation", bearer(configuration.read_core_key(root)))[0] == 401:
            force = {"core"} | ({"web"} & will_run)
    written_inputs = configuration.rendered_inputs(disk)
    in_sync = bool(running) and all(actual.get(name, {}).get("running") and actual[name]["inputs"] == written_inputs.get(name)
                                    for name in set(written_inputs) - {"migrate"})

    if state.get("generated"):
        old, base, answered = old_public_url(root, config, previous, disk, actual)
        confirm_public_url(root, config, old, base, answered, args, interactive, out)

    if previous is not None:
        keys = [key for key, value in config_model.values(config).items() if key not in previous or previous[key] != value]
        out("Changed settings: " + (", ".join(keys) if keys else "none"))
    for name in edited:
        out(f"generated/{name} was edited by hand; it is overwritten and the edited copy is kept.")
    out("Files to write: " + (", ".join("generated/" + name for name in sorted(set(changed) | set(removed))) or "none"))
    restarts = [name for name in ("database", "core", "web", "gateway", "installation") if name in (todo | force)]
    stale_stopped = [name for name in rendered.services if name != "migrate" and name not in will_run
                     and actual.get(name, {}).get("inputs") not in (None, rendered.services[name])]
    if restarts:
        verb = "start" if not running else "restart"
        out(f"Services to {verb}: " + ", ".join(restarts) + (". Every Web sign-in session ends." if "web" in restarts else ""))
    if stale_stopped or (not will_run and changed):
        out("The installation is stopped; it stays stopped and starts with these files.")
    if args.dry_run:
        out("Dry run: nothing was changed.")
        return
    if not args.dry_run:
        # A rotation that stopped before using its new key leaves only this file.
        (root / "secrets/core.key.new").unlink(missing_ok=True)
    if not (changed or removed or restarts or edited):
        if ingress_config.enabled(config):
            finish_apply(root, config, state, rendered.files.get("Caddyfile"), will_run, candidate)
        if record_digests(state, rendered.files) != load_state(root):
            save_state(root, record_digests(state, rendered.files))
        out("Nothing to apply.")
        return

    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    for name in edited:
        create_private(root / "generated" / f"{name}.edited-{stamp}", disk[name])
    # Digests first: a file written from here on is recognized as oac's.
    save_state(root, record_digests(state, rendered.files))
    for name in sorted(set(changed) | set(edited)):
        write_private(root / "generated" / name, rendered.files[name])
    for name in removed:
        (root / "generated" / name).unlink()
    try:
        converge(root, state, rendered.services, will_run, force)
        finish_apply(root, config, state, rendered.files.get("Caddyfile"), will_run, candidate)
    except (OacError, RuntimeError, subprocess.CalledProcessError) as error:
        line = core_error_line(root)
        if line:
            out(line)
        if not (rollback and in_sync):
            raise ApplyFailed(f"config.json not applied: {describe(error)}. The services were not all running with "
                              f"the previous files, so nothing was rolled back; run oac status, fix the cause "
                              f"and {retry}", describe(error)) from None
        # Record the restored files as oac's own before writing them back; a restored
        # hand edit stays one.
        restored = {name: data for name, data in disk.items()
                    if data is not None and name in (state.get("generated") or {}) and name not in edited}
        save_state(root, record_digests(load_state(root), restored))
        for name, data in disk.items():
            path = root / "generated" / name
            if data is None:
                path.unlink(missing_ok=True)
            else:
                write_private(path, data)
        try:
            converge(root, state, written_inputs, will_run)
            if ingress_config.enabled(config) and "gateway" in will_run:
                ingress_config.reload(root, disk["Caddyfile"].decode())
        except (OacError, RuntimeError, subprocess.CalledProcessError) as second:
            raise ApplyFailed(f"config.json not applied: {describe(error)}. The previous generated files were restored, "
                              f"but the services could not be started with them either ({describe(second)}); run "
                              f"oac status, fix the cause and {retry}", describe(error)) from None
        raise ApplyFailed(f"config.json not applied: {describe(error)}. The previous generated files were restored "
                          f"and the services converged on them; fix config.json and {retry}", describe(error)) from None
    out("Applied config.json.")


# Removal -----------------------------------------------------------------------

def _remove_last_files(root, state, keep_root):
    """Finish the few final unlinks without a handled signal losing the recovery state."""
    handlers = {}
    try:
        for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            handlers[signum] = signal.signal(signum, signal.SIG_IGN)
        try:
            for name in ("state.json", "oac", ".oac.lock"):
                if not (keep_root and name == ".oac.lock"):
                    (root / name).unlink(missing_ok=True)
            if not keep_root:
                root.rmdir()
        except (OSError, KeyboardInterrupt):
            # A failed command unlink must leave both the command and its state for a retry.
            if (root / "oac").exists() and not (root / "state.json").exists():
                with contextlib.suppress(OSError):
                    save_state(root, state)
            raise
    finally:
        for signum, handler in handlers.items():
            signal.signal(signum, handler)


def remove(root, state, keep_root=False, images=False):
    """Remove one installation: its Compose project with its volumes, then its files.

    state is the loaded state.json. Only this installation's own project is touched.
    Loaded images are kept unless images is set; see remove_images. keep_root keeps
    the directory and its .oac.lock, which the caller holds, and removes everything else in
    it. The files stay while a service is left or an image removal fails, so state.json still
    names them. Nothing is printed, so a closed terminal can't stop the removal. Returns a
    note for each image kept; raises OacError naming what is left and the commands that
    remove it.
    """
    root = Path(root)
    if not root.is_absolute() or root.is_symlink() or root.resolve() != root:
        raise OacError("The installation directory must be canonical and not a symlink; nothing was removed")
    project = state.get("project")
    if not isinstance(project, str) or not re.fullmatch(r"oac-[0-9a-f]{10}", project):
        raise OacError("state.json names no Compose project of this installation; nothing was removed")
    left, kept = [], []
    # -p without -f, outside any project directory and without COMPOSE_* settings: Compose
    # reads no project file and acts on this project's labels alone.
    down = ["docker", "compose", "-p", project, "down", "--volumes", "--remove-orphans"]
    compose_names = sorted(name for name in os.environ if name.startswith("COMPOSE_"))
    environment = {name: value for name, value in os.environ.items() if name not in compose_names}
    try:
        run(down, cwd="/", env=environment, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL)
    except (subprocess.CalledProcessError, OSError, KeyboardInterrupt):
        manual = shlex.join(["env", *(arg for name in compose_names for arg in ("-u", name)), *down])
        left.append((f"Compose project {project} and its volumes", f"(cd / && {manual})"))
    if images and not left:
        try:
            kept = remove_images(state, environment)
        except (subprocess.CalledProcessError, OSError, ValueError, KeyboardInterrupt):
            left.append(("the images of this installation", f"{shlex.quote(str(root / 'oac'))} uninstall"))
    target = shlex.quote(str(root))
    files = (f"the files in {root}", f"find {target} -mindepth 1 -delete" if keep_root else f"rm -rf {target}")
    if left:
        left.append(files)
    else:
        try:
            for path in root.iterdir():
                if path.name in ("state.json", "oac", ".oac.lock"):
                    continue
                if path.is_dir() and not path.is_symlink():
                    shutil.rmtree(path)
                else:
                    path.unlink()
            _remove_last_files(root, state, keep_root)
        except (OSError, KeyboardInterrupt):
            # A lock file alone, such as one another command just created, is harmless.
            if root.is_dir() and any(path.name != ".oac.lock" for path in root.iterdir()):
                left.append(files)
    if left:
        raise OacError("Removal did not finish. Left: " + "; ".join(what for what, _ in left) + ". Remove them with:\n"
                       + "\n".join("  " + command for _, command in left))
    return kept


def remove_images(state, environment):
    """Remove the images state.json records; returns a note for each one kept.

    The installer loads images untagged, so a tag, or a container of any project, means
    something outside this installation uses the image, and it stays. Docker runs as remove
    runs Compose, and prints nothing.
    """
    kept = []
    quiet = {"cwd": "/", "env": environment, "stdin": subprocess.DEVNULL, "stderr": subprocess.DEVNULL}
    for name, image in sorted(state["images"].items()):
        found = run(["docker", "image", "inspect", image, "--format", "{{json .RepoTags}}"], check=False,
                    stdout=subprocess.PIPE, text=True, **quiet)
        if found.returncode:
            present = run(["docker", "image", "ls", "--all", "--quiet", "--no-trunc"],
                          stdout=subprocess.PIPE, text=True, **quiet).stdout.split()
            if image in present:
                raise subprocess.CalledProcessError(found.returncode, found.args)
            continue  # Docker confirmed that the image is absent.
        tags = json.loads(found.stdout) or []
        users = run(["docker", "ps", "-aq", "--filter", "ancestor=" + image], stdout=subprocess.PIPE, text=True,
                    **quiet).stdout.split()
        if users or tags:
            kept.append(f"Kept the {name} image {image}: " + ("another container uses it." if users else
                        f"it is tagged {', '.join(tags)}."))
        else:
            run(["docker", "image", "rm", image], stdout=subprocess.DEVNULL, **quiet)
    return kept


# Commands --------------------------------------------------------------------

def written_view(root, state, config):
    """Addresses as last written, which the services were started with."""
    values, _ = disk_view(read_generated(root, {"settings.json"}))
    if values is None:
        if config is None:
            raise OacError("Neither config.json nor generated/settings.json can be read")
        return config
    return applied_view(values)


def applied_view(values):
    """The config the settings last written describe."""
    return {"ingress": values.get("ingress"), "public_url": values.get("public_url"), "host": values["host"],
            "ports": {name: values[f"ports.{name}"] for name in ("core", "web") if f"ports.{name}" in values}}


def load_config_or_report(root, out):
    """config.json, or None after printing why it can't be used; the written files still can."""
    try:
        return load_config(root)
    except (OacError, config_model.ConfigError) as error:
        out(str(error))
        return None


def status(root, out=print):
    check_directories(root)
    loaded = load_config_or_report(root, out)
    state = load_state(root)
    config = written_view(root, state, loaded)
    actual = observe(state)
    rendered = None
    if loaded is not None:
        rendered, disk, _ = render_now(root, loaded, state)
    for name, item in sorted(actual.items()):
        line = f'{name}: {"running" if item["running"] else "stopped"} {item["health"]}'.rstrip()
        if rendered and item["running"] and name != "migrate" and item["inputs"] != rendered.services.get(name):
            line += " (runs with other inputs than config.json renders; run oac apply)"
        out(line)
    required = set(config_model.SERVICES)
    if ingress_config.enabled(config):
        required.update(("gateway", "installation"))
    healthy = all(actual.get(name, {}).get("running") and actual[name]["health"] in ("", "healthy") for name in required)
    core_ok = http(core_base(config) + "/healthz")[0] == 200
    healthy = healthy and core_ok
    out("Core API: " + ("healthy" if core_ok else "unavailable"))
    key = configuration.read_core_key(root)
    digests = root / "generated/core-key-digests.json"
    if digests.is_file() and configuration.sha256(key) not in json.loads(digests.read_text()):
        out("secrets/core.key does not match generated/core-key-digests.json; run oac apply")
    if core_ok and http(core_base(config) + "/core/v1/installation", bearer(key))[0] == 401:
        out("Core rejects secrets/core.key because it started with another key; run oac apply")
        healthy = False
    web_ok = http(configuration.service_origin(config, "web") + "/healthz")[0] == 200
    healthy = healthy and web_ok
    out("Web: " + ("healthy" if web_ok else "unavailable"))
    out("Public URL: " + (config["public_url"] or ("not configured; set up HTTPS in Web" if ingress_config.enabled(config) else "none (local access only)")))
    out("API base URL: " + configuration.local_public_url(config) + "/v1")
    out("Console: " + (ingress_config.console_origin(config) if ingress_config.enabled(config) else
                       config["public_url"] or configuration.service_origin(config, "web")))
    out("Source commit: " + state["source_commit"])
    if rendered is not None:
        if any(comparable(name, disk.get(name)) != comparable(name, text) for name, text in rendered.files.items()):
            out(f"config.json has changes that are not applied; run {root / 'oac'} apply")
        for name in edited_files(state, disk, rendered):
            out(f"generated/{name} was edited by hand; put the change in config.json and run oac apply --discard-edits")
    out(f'Reverse proxy: /v1 and /api/v1 go to {configuration.service_address(config, "core", connect=True)}; '
        f'everything else goes to {configuration.service_address(config, "web", connect=True)}')
    out("Service health does not prove model execution. This check makes no model requests.")
    check_complete(state)
    if not healthy:
        raise OacError("One or more installed services are unavailable")


def start(root, out=print):
    """Start every service with the files last written, not with unapplied config.json changes."""
    with locked(root):
        check_directories(root)
        state = load_state(root)
        check_complete(state)
        config = load_config_or_report(root, out)
        if config is not None:
            rendered, disk, _ = render_now(root, config, state)
            if any(comparable(name, disk.get(name)) != comparable(name, text) for name, text in rendered.files.items()):
                out("Warning: config.json has changes that are not applied; starting with the files last written.")
            for name in edited_files(state, disk, rendered):
                out(f"Warning: generated/{name} was edited by hand.")
        written = written_view(root, state, config)
        for name, image in state["images"].items():
            if run(["docker", "image", "inspect", image], check=False, stdin=subprocess.DEVNULL,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
                raise OacError(f"The {name} image is missing; rerun install.sh from bundle "
                                  f'{state["source_commit"]} to reload images')
        desired = configuration.rendered_inputs(read_generated(root, {"compose.json"}))
        will_run = set(desired) - {"migrate"}
        converge(root, state, desired, will_run)
        gateway_document = (root / "generated/Caddyfile").read_text() if ingress_config.enabled(written) else None
        finish_apply(root, written, state, gateway_document, will_run, keep_unfinished=True)
    out("Services started.")


def stop(root, out=print):
    with locked(root):
        check_directories(root)
        load_state(root)
        if (root / "generated/compose.json").exists():
            compose(root, "stop")
    out("Control-plane services stopped. Data, nodes and sandbox resources are kept; running sandbox work may continue.")


def rotate_core_key(root, yes=False, interactive=None, out=print):
    interactive = sys.stdin.isatty() if interactive is None else interactive
    with locked(root):
        check_directories(root)
        state = load_state(root)
        check_complete(state)
        config = load_config(root)
        rendered, disk, _ = render_now(root, config, state)
        if any(comparable(name, disk.get(name)) != comparable(name, text) for name, text in rendered.files.items()) \
                or edited_files(state, disk, rendered):
            raise OacError("config.json has changes that are not applied, or generated files were edited; "
                              "run oac apply first")
        out("Rotating the Core key ends every Web sign-in session, and the current key stops working at once.")
        if not (yes or (interactive and input("Type yes to rotate the Core key: ").strip() == "yes")):
            raise OacError("Core key rotation was not confirmed; nothing was changed")
        old_key = configuration.read_core_key(root)
        replacement = root / "secrets/core.key.new"
        replacement.unlink(missing_ok=True)
        create_private(replacement, secrets.token_hex(32))
        running = {name for name, item in observe(state).items() if item["running"]}
        args = argparse.Namespace(dry_run=False, yes=True, confirm_public_url_change=None)
        try:
            if "web" in running:
                compose(root, "stop", "web")  # Web holds the old key; its sessions end anyway.
            os.replace(replacement, root / "secrets/core.key")
            _apply(root, args, False, False, False, out, rollback=False, retry="run oac apply to finish the rotation")
        except (OacError, RuntimeError, subprocess.CalledProcessError, KeyboardInterrupt) as error:
            # Whatever key secrets/core.key holds now, the next apply converges on it.
            raise OacError(f"Core key rotation did not finish: {describe(error)}. secrets/core.key holds the key to "
                              "use; run oac apply to finish") from None
    if "core" in running:
        new = bearer(configuration.read_core_key(root))
        url = core_base(config) + "/core/v1/installation"
        if http(url, new)[0] != 200 or http(url, bearer(old_key))[0] != 401:
            raise OacError("Core did not confirm the new key and reject the old one; run oac status")
    out(f"New Core key: {root / 'secrets/core.key'}. Sign in to Web again and update scripts that use the key.")


def core_sandboxes(root, state):
    """Core's (nodes, sandbox deployment), or None when Core does not answer. Any part of the installation may be missing."""
    try:
        if not root.is_absolute() or root.resolve() != root:
            return None
        check_directories(root)
        check_private(root / "secrets/core.key", "secrets/core.key")
        try:
            config = load_config(root)
        except (OacError, config_model.ConfigError):
            config = None
        # The address the running Core was started with.
        base, key = core_base(written_view(root, state, config)), bearer(configuration.read_core_key(root))
        answers = [http(base + "/core/v1/sandbox/" + name, key) for name in ("nodes", "deployment")]
        if any(status != 200 for status, _ in answers):
            return None
        return json.loads(answers[0][1])["data"], json.loads(answers[1][1])
    except (OacError, RuntimeError, OSError, ValueError, KeyError, TypeError, HTTPException):
        return None


def uninstall(root, yes=False, interactive=None, out=print):
    """Remove this installation and all its data from this host, finished or not. Nodes and sandboxes are only listed."""
    interactive = sys.stdin.isatty() if interactive is None else interactive
    if not (root / "state.json").exists():
        # Without state.json nothing here is known to be the installation's, so nothing is removed.
        out(f"No installation is left in {root}: it has no state.json. Nothing was removed.")
        return
    with locked(root):
        state = load_state(root)
        project = state["project"]
        out(f"This removes the installation in {root} from this host:")
        out(f"  Compose project {project}: its containers and networks, and the database volume {project}_database")
        for name, image in sorted(state["images"].items()):
            out(f"  The {name} image {image}, unless another container or a tag uses it")
        out(f"  The directory {root}")
        nodes = []
        out("All data is deleted: the database with every Project, API key, Session and stored credential, and "
            "the Core key. To keep it, back it up first: docs/getting-started/operations.md#back-up")
        found = core_sandboxes(root, state)
        release = ("While Core is still up, archive their Sessions, or choose Reset deployment in Web "
                   "(System → Manage sandbox configuration) and let it complete.")
        if found is None:
            nodes = None
            out("Core did not answer, so its nodes and sandboxes can't be listed. Nodes stay on their hosts.")
            out("Uninstall stops no sandbox: node sandboxes keep running on their nodes, and E2B keeps running, "
                "and billing for, its sandboxes. " + release)
        else:
            nodes, deployment = found
            if nodes:
                out("Nodes registered with this Core, which stay on their hosts: " + ", ".join(
                    f'{node.get("name")} ({"online" if node.get("online") else "offline"})' for node in nodes))
            count = (deployment.get("resources") or {}).get("allocations") or 0
            if count:
                where = ("E2B keeps running them, and billing for them" if deployment.get("provider") == "e2b"
                         else "they keep running on their nodes")
                out(f"Core has {count} sandbox(es) in use. Uninstall does not stop them: {where}. {release}")
        if not yes:
            if not interactive:
                raise OacError("Confirm the uninstall with --yes, or run it in a terminal; nothing was removed")
            if input(f"Type the installation directory, {root}, to remove it: ").strip() != str(root):
                raise OacError("The uninstall was not confirmed; nothing was removed")
        kept = remove(root, state, images=True)
    for note in kept:
        out(note)
    out(f"Removed the installation in {root}.")
    if nodes != []:
        hosts = (f'each node host ({", ".join(str(node.get("name")) for node in nodes)})' if nodes else
                 "each node host that served this installation")
        out(f"This Core is gone, so on {hosts}, uninstall the node with --force using node-install.pyz from this "
            "release's bundle:")
        out(f'  sudo python3 node-install.pyz --uninstall --installation-id {state["installation_id"]} --force')
        out("See docs/getting-started/nodes.md#remove-a-node")


def main(argv=None, root=None, out=print):
    root = Path(root) if root else Path(sys.argv[0]).resolve().parent
    parser = argparse.ArgumentParser(prog=str(root / "oac"), description=__doc__.split("\n\n")[0])
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("status", help="Show service health, addresses and configuration drift")
    commands.add_parser("start", help="Start the installed services with the files last written")
    commands.add_parser("stop", help="Stop the installed services; data is kept")
    apply_parser = commands.add_parser("apply", help="Apply config.json and restart what changed")
    apply_parser.add_argument("--dry-run", action="store_true", help="Show the plan without changing anything")
    apply_parser.add_argument("--discard-edits", action="store_true",
                              help="Overwrite hand-edited generated files, keeping each edited copy")
    apply_parser.add_argument("--confirm-public-url-change", metavar="URL",
                              help="Confirm a public URL change non-interactively; must equal the new URL")
    domain = commands.add_parser("domain", help="Configure and verify managed HTTPS using a DNS hostname")
    domain.add_argument("hostname")
    domain.add_argument("--confirm-public-url-change", metavar="URL")
    commands.add_parser("domain-server", help=argparse.SUPPRESS)
    rotate = commands.add_parser("rotate-core-key", help="Replace the Core key; the old key stops working")
    rotate.add_argument("--yes", action="store_true", help="Do not ask for confirmation")
    remove_parser = commands.add_parser("uninstall", help="Remove this installation and all its data from this host")
    remove_parser.add_argument("--yes", action="store_true", help="Do not ask for confirmation")
    args = parser.parse_args(argv)
    if args.command == "uninstall":
        return uninstall(root, yes=args.yes, out=out)
    if not (root / "state.json").exists():
        raise OacError(f"{root} is not an installation directory; run the oac command inside it")
    load_state(root)
    if args.command == "apply":
        apply(root, dry_run=args.dry_run, discard_edits=args.discard_edits,
              confirm_public_url_change=args.confirm_public_url_change, out=out)
    elif args.command == "domain":
        import ingress
        ingress.configure(root, args.hostname, args.confirm_public_url_change, out)
    elif args.command == "domain-server":
        import ingress
        ingress.serve(root)
    elif args.command == "rotate-core-key":
        rotate_core_key(root, yes=args.yes, out=out)
    else:
        {"status": status, "start": start, "stop": stop}[args.command](root, out=out)


def entry():
    try:
        main()
    except (OacError, config_model.ConfigError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError):
        # Errors never include generated configuration or external process output.
        print("oac failed; run oac status and inspect the installation files", file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)


if __name__ == "__main__":
    entry()
