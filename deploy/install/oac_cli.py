"""oac: status, start, stop, apply and rotate-core-key for one installation.

The installation directory is the directory that holds the command. The bundle it
was installed from is never needed. config.json is the only file an operator edits;
apply renders generated/ from it and converges the running services on that render.
What runs is the truth: each Compose container carries the inputs digest it was
created with (label io.oac.inputs) and native Core carries it in OAC_INPUTS,
so an interrupted apply, rotation or rollback is finished by the next apply.
"""
import argparse
import contextlib
import datetime
import fcntl
import json
import os
from pathlib import Path
import secrets
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit

import config_model
import configuration
import native_service


class OacError(Exception):
    pass


def run(args, **kwargs):
    # Never print a generated Compose file, process environment or secret value.
    return subprocess.run(args, **dict({"check": True}, **kwargs))


def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


# Private files ---------------------------------------------------------------

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
    if state.get("format") != 2:
        raise OacError("state.json has an unknown format; use the oac command of this installation's release")
    return state


def save_state(root, state):
    write_private(root / "state.json", json.dumps(state, indent=2) + "\n")


@contextlib.contextmanager
def locked(root):
    descriptor = os.open(root / ".oac.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise OacError("Another oac command is running for this installation") from None
        yield
    finally:
        os.close(descriptor)


# What runs ---------------------------------------------------------------------

def compose(root, *args, **kwargs):
    return run(["docker", "compose", "-f", str(root / "generated/compose.json"), *args], **kwargs)


INSPECT = ('{{index .Config.Labels "com.docker.compose.service"}}\t{{index .Config.Labels "' + configuration.LABEL
           + '"}}\t{{.State.Status}}\t{{if .State.Health}}{{.State.Health.Status}}{{end}}')


def observe(state):
    """{service: {running, inputs, health}} of this installation's containers and native Core."""
    result = {}
    ids = run(["docker", "ps", "-aq", "--filter", f'label=com.docker.compose.project={state["project"]}',
               "--filter", "label=com.docker.compose.oneoff=False"], capture_output=True, text=True).stdout.split()
    if ids:
        for line in run(["docker", "inspect", "--format", INSPECT, *ids], capture_output=True, text=True).stdout.splitlines():
            service, inputs, status, health = (line.split("\t") + ["", "", "", ""])[:4]
            if service:
                result[service] = {"running": status == "running", "inputs": inputs or None, "health": health}
    if native_service.is_native(state):
        inputs = native_service.running_inputs(state)
        result["core"] = {"running": inputs is not None or native_service.active(state), "inputs": inputs, "health": ""}
    return result


def stale(actual, desired, will_run):
    """Services that should run but don't, or run with other inputs."""
    return {name for name in will_run if name != "migrate" and (
        not actual.get(name, {}).get("running") or actual[name]["inputs"] != desired.get(name))}


def migrate_native(root):
    environment = configuration.read_environment((root / "generated/core.env").read_text())
    run([str(root / "native/bin/oac-core-migrate")], env=dict(os.environ, **environment),
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def converge(root, state, desired, will_run, force=()):
    """Bring every service in will_run to the desired inputs; Core first, then the rest.

    Compose recreates exactly the containers whose configuration (and so label)
    differs; native Core restarts when its running OAC_INPUTS differ.
    """
    native = native_service.is_native(state)
    actual = observe(state)
    todo = stale(actual, desired, will_run) | set(force)
    if not todo:
        return set()
    up = ["up", "--detach", "--wait", "--wait-timeout", "300"]
    if "core" in todo:
        if native:
            if "database" in will_run and "database" in stale(actual, desired, {"database"}):
                compose(root, *up, "database")
            native_service.daemon_reload()
            if actual.get("core", {}).get("running"):
                native_service.restart(state)
            else:
                migrate_native(root)
                native_service.start(root, state)
        elif "core" in force and not stale(actual, desired, {"core"}):
            compose(root, "restart", "core")
        else:
            compose(root, *up, "core")
    containers = will_run - ({"core"} if native else set())
    if todo - {"core"} or ("core" in todo and not native):
        if containers:
            compose(root, *up)
        for name in sorted(set(force) & containers - {"core"}):
            if not stale(actual, desired, {name}):
                compose(root, "restart", name)
    return todo


def core_error_line(root, state):
    """Core's single startup failure line. Core logs no environment values."""
    try:
        if native_service.is_native(state):
            output = run(["journalctl", "--user", "--unit", native_service.unit_name(state), "--lines", "200",
                          "--no-pager", "--output", "cat"], capture_output=True, text=True).stdout
        else:
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
    return f'http://127.0.0.1:{config["ports"]["core"]}'


def health(root, config, expected):
    """config needs public_url and ports; it may be the view of what was last written."""
    if "core" in expected:
        base = core_base(config)
        if wait_status(base + "/healthz") is None:
            raise OacError("Core did not become healthy")
        if wait_status(base + "/core/v1/installation", bearer(configuration.read_core_key(root)), attempts=10) is None:
            raise OacError("Core did not accept the Core key at /core/v1/installation")
    if "web" in expected:
        url = f'http://127.0.0.1:{config["ports"]["web"]}'
        if wait_status(url + "/console/auth", {"Host": urlsplit(config["public_url"] or url).netloc}) is None:
            raise OacError("Web sign-in is unavailable")


# Files -------------------------------------------------------------------------

def check_fixed(config, state):
    """state.json records mode and native_core at installation; it wins over config.json."""
    for key in ("mode", "native_core"):
        if config.get(key, False) != state[key]:
            raise OacError(f"{key} is fixed after installation ({json.dumps(state[key])}). "
                              "Install into a new directory to change it; nothing was applied.")


def check_secrets(root, config, state):
    reasons = {"credential.key": "Stored credentials can only be read with the original key.",
               "database.password": "PostgreSQL keeps the password it was initialized with."}
    if config["mode"] != "web-only":
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


def render_now(root, config, state):
    """The render of config.json, keeping the written stamp unless something changed."""
    names = set((state.get("generated") or {}))
    disk = read_generated(root, names | {"settings.json", "runtime-history.json"})
    previous, stamp = disk_view(disk)
    rendered = configuration.render(root, config, state, stamp or now())
    disk = read_generated(root, names | set(rendered.files) | {"runtime-history.json"})
    if any(comparable(name, disk.get(name)) != comparable(name, text) for name, text in rendered.files.items()) \
            or any(disk.get(name) is not None for name in names - set(rendered.files)):
        rendered = configuration.render(root, config, state, now())
    return rendered, disk, previous


# Apply -----------------------------------------------------------------------

def old_public_url(root, config, previous, disk, actual):
    """The public URL things are bound to: Core's own answer, else the written core.env."""
    port = (previous or {}).get("ports.core") or config["ports"]["core"]
    if actual.get("core", {}).get("running"):
        status, body = http(f"http://127.0.0.1:{port}/core/v1/installation", bearer(configuration.read_core_key(root)))
        if status == 200:
            return json.loads(body).get("public_url"), port, True
    try:
        written = configuration.read_environment((disk.get("core.env") or b"").decode())
    except RuntimeError:
        written = {}
    return written.get("OAC_PUBLIC_URL"), port, False


def confirm_public_url(root, config, old, port, core_answered, args, interactive, out):
    """Changing the public URL strands what is bound to the old one; list it and confirm.

    old is None when it can't be read; then the change always needs confirmation.
    """
    new = configuration.local_public_url(config)
    if old == new:
        return
    counts, nodes = None, []
    if core_answered:
        base, key = f"http://127.0.0.1:{port}", configuration.read_core_key(root)
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


def paired_core(root, config):
    """(HTTP status, installation ID) of the Core that web.core_url reaches."""
    status, body = http(config["web"]["core_url"] + "/core/v1/installation", bearer(configuration.read_core_key(root)))
    return status, (json.loads(body).get("installation_id") if status == 200 else None)


def check_paired_core(root, config, state, previous, args, interactive, out):
    """Web-only: detect a web.core_url that now reaches a different Core."""
    if args.dry_run and previous is not None and previous.get("web.core_url") != config["web"]["core_url"]:
        # The Core key goes only to a Core that apply is asked to use.
        out("web.core_url changes; apply checks which Core it reaches.")
        return state.get("core_installation_id")
    status, installation = paired_core(root, config)
    if status == 401:
        out("Warning: Core rejects this Web host's Core key; the key is out of date. "
            "Copy secrets/core.key from the Core host, then run oac apply.")
    elif status == 404:
        out("Note: the paired Core runs an earlier release without /core/v1/installation. Convert or upgrade the "
            "Core host, then run oac apply here to record which Core Web is paired with.")
    if status != 200:
        return state.get("core_installation_id")
    recorded = state.get("core_installation_id")
    if recorded and installation != recorded:
        out(f"web.core_url reaches Core installation {installation}, not the paired installation {recorded}.")
        if args.dry_run:
            out("Applying this change needs confirmation.")
        elif not (args.yes or (interactive and input("Type yes to pair Web with this Core: ").strip() == "yes")):
            raise OacError("Pairing Web with a different Core was not confirmed; nothing was applied")
    return installation


def apply(root, dry_run=False, yes=False, discard_edits=False, confirm_public_url_change=None,
          start=False, interactive=None, out=print, retry=None):
    root = Path(root)
    args = argparse.Namespace(dry_run=dry_run, yes=yes, confirm_public_url_change=confirm_public_url_change)
    interactive = sys.stdin.isatty() if interactive is None else interactive
    with locked(root):
        return _apply(root, args, discard_edits, start, interactive, out, retry=retry)


def _apply(root, args, discard_edits, start, interactive, out, rollback=True, retry=None):
    retry = retry or f"run {root / 'oac'} apply again"
    check_directories(root)
    config = load_config(root)
    state = load_state(root)
    check_fixed(config, state)
    check_secrets(root, config, state)
    if not args.dry_run:
        # A rotation that stopped before using its new key leaves only this file.
        (root / "secrets/core.key.new").unlink(missing_ok=True)
    rendered, disk, previous = render_now(root, config, state)
    edited = edited_files(state, disk, rendered)
    if edited and not discard_edits:
        raise OacError("\n".join(f"generated/{name} was edited by hand." for name in edited)
                          + "\nPut the change in config.json and run oac apply --discard-edits, which keeps the"
                          " edited copy as generated/<file>.edited-<time>. Nothing was applied.")
    changed = [name for name, text in rendered.files.items() if disk.get(name) != text.encode()]
    removed = [name for name in (state.get("generated") or {}) if name not in rendered.files and disk.get(name) is not None]

    actual = observe(state)
    running = {name for name, item in actual.items() if item["running"] and name != "migrate"}
    will_run = (set(rendered.services) - {"migrate"}) if (start or running) else set()
    todo = stale(actual, rendered.services, will_run)
    force = set()
    if "core" in will_run and "core" not in todo and config["mode"] != "web-only":
        # The digest file follows secrets/core.key; a Core that rejects the key restarts, then Web.
        if http(core_base(config) + "/core/v1/installation", bearer(configuration.read_core_key(root)))[0] == 401:
            force = {"core"} | ({"web"} & will_run)
    written_inputs = configuration.rendered_inputs(disk, rendered.unit)
    in_sync = bool(running) and all(actual.get(name, {}).get("running") and actual[name]["inputs"] == written_inputs.get(name)
                                    for name in set(written_inputs) - {"migrate"})

    core_installation_id = state.get("core_installation_id")
    if config["mode"] == "web-only":
        core_installation_id = check_paired_core(root, config, state, previous, args, interactive, out)
    elif state.get("generated"):
        old, port, answered = old_public_url(root, config, previous, disk, actual)
        confirm_public_url(root, config, old, port, answered, args, interactive, out)

    if previous is not None:
        keys = [key for key, value in config_model.values(config).items() if key not in previous or previous[key] != value]
        out("Changed settings: " + (", ".join(keys) if keys else "none"))
    for name in edited:
        out(f"generated/{name} was edited by hand; it is overwritten and the edited copy is kept.")
    out("Files to write: " + (", ".join("generated/" + name for name in sorted(set(changed) | set(removed))) or "none"))
    restarts = [name for name in ("database", "core", "web") if name in (todo | force)]
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
    if not (changed or removed or restarts or edited):
        state = dict(state, core_installation_id=core_installation_id)
        if record_digests(state, rendered.files) != load_state(root):
            save_state(root, record_digests(state, rendered.files))
        out("Nothing to apply.")
        return

    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    for name in edited:
        create_private(root / "generated" / f"{name}.edited-{stamp}", disk[name])
    # Digests first: a file written from here on is recognized as oac's.
    save_state(root, record_digests(dict(state, core_installation_id=core_installation_id), rendered.files))
    for name in sorted(set(changed) | set(edited)):
        write_private(root / "generated" / name, rendered.files[name])
    for name in removed:
        (root / "generated" / name).unlink()
    try:
        converge(root, state, rendered.services, will_run, force)
        health(root, config, will_run)
    except (OacError, RuntimeError, subprocess.CalledProcessError) as error:
        line = core_error_line(root, state)
        if line:
            out(line)
        if not (rollback and in_sync):
            raise OacError(f"config.json not applied: {describe(error)}. The services were not all running with "
                              f"the previous files, so nothing was rolled back; run oac status, fix the cause "
                              f"and {retry}") from None
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
        except (OacError, RuntimeError, subprocess.CalledProcessError) as second:
            raise OacError(f"config.json not applied: {describe(error)}. The previous generated files were restored, "
                              f"but the services could not be started with them either ({describe(second)}); run "
                              f"oac status, fix the cause and {retry}") from None
        raise OacError(f"config.json not applied: {describe(error)}. The previous generated files were restored "
                          f"and the services converged on them; fix config.json and {retry}") from None
    out("Applied config.json.")


# Commands --------------------------------------------------------------------

def written_view(root, state, config):
    """public_url, ports and web.core_url as last written, which the services were started with."""
    values, _ = disk_view(read_generated(root, {"settings.json"}))
    if values is None:
        if config is None:
            raise OacError("Neither config.json nor generated/settings.json can be read")
        return config
    return {"mode": state["mode"], "public_url": values.get("public_url"),
            "ports": {name: values[f"ports.{name}"] for name in ("core", "web", "database") if f"ports.{name}" in values},
            "web": {"core_url": values.get("web.core_url")}}


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
    mode = config["mode"]
    actual = observe(state)
    rendered = None
    if loaded is not None and loaded.get("mode") == state["mode"] and loaded.get("native_core", False) == state["native_core"]:
        rendered, disk, _ = render_now(root, loaded, state)
    for name, item in sorted(actual.items()):
        line = f'{name}: {"running" if item["running"] else "stopped"} {item["health"]}'.rstrip()
        if rendered and item["running"] and name != "migrate" and item["inputs"] != rendered.services.get(name):
            line += " (runs with other inputs than config.json renders; run oac apply)"
        out(line)
    required = set(config_model.SERVICES[mode])
    healthy = all(actual.get(name, {}).get("running") and actual[name]["health"] in ("", "healthy") for name in required)
    if mode != "web-only":
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
    if mode != "core-only":
        web_ok = http(f'http://127.0.0.1:{config["ports"]["web"]}/healthz')[0] == 200
        healthy = healthy and web_ok
        out("Web: " + ("healthy" if web_ok else "unavailable"))
    out("Public URL: " + (config["public_url"] or "none (local access only)"))
    if mode != "web-only":
        out("API base URL: " + configuration.local_public_url(config) + "/v1")
    if mode != "core-only":
        out("Console: " + (config["public_url"] or f'http://127.0.0.1:{config["ports"]["web"]}'))
    out("Source commit: " + state["source_commit"])
    if loaded is not None:
        for key in ("mode", "native_core"):
            if loaded.get(key, False) != state[key]:
                out(f"config.json sets {key} to {json.dumps(loaded.get(key, False))}, but it is fixed at "
                    f"{json.dumps(state[key])} for this installation (state.json); restore it")
    if rendered is not None:
        if any(comparable(name, disk.get(name)) != comparable(name, text) for name, text in rendered.files.items()):
            out(f"config.json has changes that are not applied; run {root / 'oac'} apply")
        for name in edited_files(state, disk, rendered):
            out(f"generated/{name} was edited by hand; put the change in config.json and run oac apply --discard-edits")
    if mode == "web-only":
        out("Reverse proxy: /v1 and /api/v1 go to Core; everything else goes to "
            f'127.0.0.1:{config["ports"]["web"]}')
        code, installation = paired_core(root, config)
        if code == 401:
            out("Paired Core: rejects this Web host's Core key; the key is out of date. Copy secrets/core.key "
                "from the Core host, then run oac apply.")
            healthy = False
        elif code == 404:
            out("Paired Core: runs an earlier release without /core/v1/installation; upgrade or convert the Core host")
        elif code != 200:
            out("Paired Core: unreachable at " + config["web"]["core_url"])
            healthy = False
        elif state.get("core_installation_id") not in (None, installation):
            out(f"Paired Core: web.core_url reaches installation {installation}, "
                f'not the paired installation {state["core_installation_id"]}')
        else:
            out(f"Paired Core: installation {installation}")
    elif mode == "core-only":
        out(f'Reverse proxy: /v1 and /api/v1 go to 127.0.0.1:{config["ports"]["core"]}; Web runs elsewhere')
    else:
        out(f'Reverse proxy: /v1 and /api/v1 go to 127.0.0.1:{config["ports"]["core"]}; '
            f'everything else goes to 127.0.0.1:{config["ports"]["web"]}')
    out("Service health does not prove model execution. This check makes no model requests.")
    if not healthy:
        raise OacError("One or more installed services are unavailable")


def start(root, out=print):
    """Start every service with the files last written, not with unapplied config.json changes."""
    with locked(root):
        check_directories(root)
        state = load_state(root)
        config = load_config_or_report(root, out)
        if config is not None:
            rendered, disk, _ = render_now(root, config, state)
            if any(comparable(name, disk.get(name)) != comparable(name, text) for name, text in rendered.files.items()):
                out("Warning: config.json has changes that are not applied; starting with the files last written.")
            for name in edited_files(state, disk, rendered):
                out(f"Warning: generated/{name} was edited by hand.")
        for name, image in state["images"].items():
            if run(["docker", "image", "inspect", image], check=False, stdin=subprocess.DEVNULL,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
                raise OacError(f"The {name} image is missing; rerun install.sh from bundle "
                                  f'{state["source_commit"]} to reload images')
        unit = native_service.unit_name(state) if native_service.is_native(state) else None
        desired = configuration.rendered_inputs(read_generated(root, {"compose.json"} | ({unit} if unit else set())), unit)
        will_run = set(desired) - {"migrate"}
        converge(root, state, desired, will_run)
        health(root, written_view(root, state, config), will_run)
    out("Services started.")


def stop(root, out=print):
    with locked(root):
        check_directories(root)
        state = load_state(root)
        native_service.stop(root, state)
        if (root / "generated/compose.json").exists():
            compose(root, "stop")
    out("Control-plane services stopped. Data, nodes and sandbox resources are kept; running sandbox work may continue.")


def rotate_core_key(root, yes=False, interactive=None, out=print):
    interactive = sys.stdin.isatty() if interactive is None else interactive
    with locked(root):
        check_directories(root)
        state = load_state(root)
        if state["mode"] == "web-only":
            raise OacError("Core owns the Core key. Copy secrets/core.key from the Core host into this "
                              "installation, then run oac apply.")
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
    out("A separate Web-only installation keeps its own copy: copy secrets/core.key to that host and run its oac apply.")


def main(argv=None, root=None, out=print):
    root = Path(root) if root else Path(sys.argv[0]).resolve().parent
    if Path(sys.argv[0]).name == "parsar":
        raise OacError(f"parsar was renamed to oac. Run {root / 'oac'} <command>.")
    parser = argparse.ArgumentParser(prog=str(root / "oac"), description=__doc__.split("\n\n")[0])
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("status", help="Show service health, addresses and configuration drift")
    commands.add_parser("start", help="Start the installed services with the files last written")
    commands.add_parser("stop", help="Stop the installed services; data is kept")
    apply_parser = commands.add_parser("apply", help="Apply config.json and restart what changed")
    apply_parser.add_argument("--dry-run", action="store_true", help="Show the plan without changing anything")
    apply_parser.add_argument("--yes", action="store_true", help="Pair Web-only with a different Core without asking")
    apply_parser.add_argument("--discard-edits", action="store_true",
                              help="Overwrite hand-edited generated files, keeping each edited copy")
    apply_parser.add_argument("--confirm-public-url-change", metavar="URL",
                              help="Confirm a public URL change non-interactively; must equal the new URL")
    rotate = commands.add_parser("rotate-core-key", help="Replace the Core key; the old key stops working")
    rotate.add_argument("--yes", action="store_true", help="Do not ask for confirmation")
    args = parser.parse_args(argv)
    if not (root / "state.json").exists():
        raise OacError(f"{root} is not an installation directory; run the oac command inside it")
    state = load_state(root)
    if state.get("renamed_from") and not state["renamed_from"].get("finished"):
        raise OacError(f"Conversion is unfinished; rerun ./install.sh --convert --install-dir {root}")
    if args.command == "apply":
        apply(root, dry_run=args.dry_run, yes=args.yes, discard_edits=args.discard_edits,
              confirm_public_url_change=args.confirm_public_url_change, out=out)
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
