"""parsar: status, start, stop, apply and rotate-core-key for one installation.

The installation directory is the directory that holds the command. The bundle it
was installed from is never needed. config.json is the only file an operator edits;
apply derives generated/ from it and restarts exactly the services whose inputs
changed.
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


class ParsarError(Exception):
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
        raise ParsarError(f"{what} is missing") from None
    if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) & 0o077
            or info.st_uid != os.geteuid() or info.st_nlink != 1):
        raise ParsarError(f"{what} must be a regular file with mode 0600, owned by you, and not a link")


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
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def load_config(root):
    raw = read_private(root / "config.json", "config.json")
    try:
        document = json.loads(raw)
    except ValueError as error:
        line = getattr(error, "lineno", "?")
        raise ParsarError(f"config.json is not valid JSON (line {line})") from None
    return config_model.validate(document), configuration.sha256(raw)


def load_state(root):
    state = json.loads(read_private(root / "state.json", "state.json"))
    if state.get("format") != 1:
        raise ParsarError("state.json has an unknown format; use the parsar command of this installation's release")
    return state


def save_state(root, state):
    write_private(root / "state.json", json.dumps(state, indent=2) + "\n")


@contextlib.contextmanager
def locked(root):
    descriptor = os.open(root / ".parsar.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ParsarError("Another parsar command is running for this installation") from None
        yield
    finally:
        os.close(descriptor)


# Services --------------------------------------------------------------------

def compose(root, *args, **kwargs):
    return run(["docker", "compose", "-f", str(root / "generated/compose.json"), *args], **kwargs)


def service_states(root):
    if not (root / "generated/compose.json").exists():
        return {}
    output = compose(root, "ps", "--all", "--format", "json", capture_output=True, text=True).stdout
    # Compose versions may return one array or one object per line.
    rows = json.loads(output) if output.lstrip().startswith("[") else [
        json.loads(line) for line in output.splitlines() if line.strip()]
    return {row["Service"]: row for row in rows}


def running(root, state):
    names = {name for name, row in service_states(root).items() if row.get("State") == "running"}
    if native_service.is_native(state) and native_service.active(state):
        names.add("core")
    return names


def migrate_native(root):
    environment = configuration.read_environment((root / "generated/core.env").read_text())
    run([str(root / "native/bin/agents-api-migrate")], env=dict(os.environ, **environment),
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def plan_running(state, services, was_running, start):
    """Services running after apply. Compose starts every container once any runs."""
    native = native_service.is_native(state)
    containers = set(services) - {"migrate"} - ({"core"} if native else set())
    result = containers if start or was_running & containers else set()
    if native and (start or "core" in was_running):
        result = result | {"core"}
    return result


def converge(root, state, services, was_running, core_port, restarts=(), start=False, reload_unit=False,
             core_first=False):
    """Bring the services that run to the written files. Stopped services stay stopped.

    Compose recreates exactly the containers whose configuration changed. Native Core
    restarts only when its inputs changed; `enable --now` leaves an active unit alone.
    """
    native = native_service.is_native(state)
    will_run = plan_running(state, services, was_running, start)
    restart_native = native and "core" in was_running and "core" in restarts
    if native and reload_unit:
        native_service.daemon_reload()
    if core_first and "core" in will_run:
        if native:
            native_service.restart(state)
        else:
            compose(root, "up", "--detach", "--wait", "--wait-timeout", "300", "core")
        if wait_status(f"http://127.0.0.1:{core_port}/healthz") is None:
            raise ParsarError("Core did not become healthy")
    if will_run - ({"core"} if native else set()):
        compose(root, "up", "--detach", "--wait", "--wait-timeout", "300")
    if native and "core" in will_run and not core_first:
        if start:
            migrate_native(root)
            native_service.start(root, state)
        if restart_native:
            native_service.restart(state)


def describe(error):
    """A failure message without command paths, output or environment."""
    if isinstance(error, subprocess.CalledProcessError):
        words = [word for word in error.cmd if not word.startswith(("/", "-"))][:3]
        return f"`{' '.join(words)}` failed"
    return str(error)


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
    lines = [line.strip() for line in output.splitlines() if "agents-api startup failed" in line]
    return lines[-1] if lines else None


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
    """config needs public_url and ports; it may be the applied view of a changed config.json."""
    if "core" in expected:
        base = core_base(config)
        if wait_status(base + "/healthz") is None:
            raise ParsarError("Core did not become healthy")
        if wait_status(base + "/core/v1/installation", bearer(configuration.read_core_key(root)), attempts=10) is None:
            raise ParsarError("Core did not accept the Core key at /core/v1/installation")
    if "web" in expected:
        url = f'http://127.0.0.1:{config["ports"]["web"]}'
        if wait_status(url + "/console/auth", {"Host": urlsplit(config["public_url"] or url).netloc}) is None:
            raise ParsarError("Web sign-in is unavailable")


# Apply -----------------------------------------------------------------------

def check_fixed(config, state):
    for key in ("mode", "native_core"):
        if config.get(key, False) != state[key]:
            raise ParsarError(f"{key} is fixed after installation ({json.dumps(state[key])}). "
                              "Install into a new directory to change it; nothing was applied.")


def check_secrets(root, config, state):
    reasons = {"credential.key": "Stored credentials can only be read with the original key.",
               "database.password": "PostgreSQL keeps the password it was initialized with."}
    if config["mode"] != "web-only":
        for name, reason in reasons.items():
            data = read_private(root / "secrets" / name, "secrets/" + name)
            if configuration.sha256(data) != state["secrets_sha256"][name]:
                raise ParsarError(f"secrets/{name} changed since installation. {reason} "
                                  "Restore the original file; nothing was applied.")
    check_private(root / "secrets/core.key", "secrets/core.key")
    configuration.read_core_key(root)


def execution_options_import(root, state, out):
    """Hook for phase 3's one-time import of a retained execution options file.

    install.sh --convert keeps an existing AGENTS_API_EXECUTION_OPTIONS_FILE and its
    file unchanged (state.json execution_options_file). Phase 3 replaces this body with
    the import into Core's deployment model providers and then clears the record.
    """
    retained = state.get("execution_options_file")
    if retained:
        out(f"Note: Core still reads {retained['path']} through AGENTS_API_EXECUTION_OPTIONS_FILE. "
            "A later release imports it into Core once and removes it.")
    return state


def edited_files(root, state):
    """Generated files whose content differs from what an apply wrote.

    An apply records the digests it is about to write as `pending` before writing,
    so a run interrupted between the files and state.json is not mistaken for edits.
    """
    applied = (state.get("applied") or {}).get("files") or {}
    pending = state.get("pending") or {}
    edited = []
    for name in sorted(set(applied) | set(pending)):
        path = root / "generated" / name
        current = None if path.is_symlink() or not path.is_file() else configuration.sha256(path.read_bytes())
        # A file the last apply did not write may be absent; so may one an interrupted run removed.
        if current not in ({applied.get(name), pending.get(name)} if pending else {applied.get(name)}):
            edited.append(name)
    return edited


def previous_values(root, state, edited):
    """The applied settings, from the snapshot and the one generated file with sensitive values."""
    if not state.get("applied") or "settings.json" in edited:
        return None
    values = {item["key"]: item["value"] for item in
              json.loads((root / "generated/settings.json").read_text())["settings"]}
    history = root / "generated/runtime-history.json"
    if "core.runtime_history.headers" in values and "runtime-history.json" not in edited:
        values["core.runtime_history.headers"] = json.loads(history.read_text()).get("headers") if history.exists() else None
    return values


def confirm_public_url(root, config, old, core_port, was_running, args, interactive, out):
    """Changing the public URL strands what is bound to the old one; list it and confirm.

    old is the applied public URL, or None when it can't be read; then the change
    always needs confirmation.
    """
    new = configuration.local_public_url(config)
    if old == new:
        return
    counts, nodes = None, []
    if "core" in was_running and old is not None:
        base, key = f"http://127.0.0.1:{core_port}", configuration.read_core_key(root)
        status, body = http(base + "/core/v1/installation", bearer(key))
        if status == 200:
            counts = json.loads(body).get("address_bindings") or {}
            status, body = http(base + "/core/v1/sandbox/nodes", bearer(key))
            nodes = json.loads(body).get("data", []) if status == 200 else []
    if old is None:
        out(f"The applied public URL can't be read, so the change to {new} needs confirmation.")
    else:
        out(f"The public URL changes from {old} to {new}.")
    if counts is not None:
        out(f'Bound to the current address: {counts.get("nodes", 0)} node(s), '
            f'{counts.get("hosted_sandboxes", 0)} hosted sandbox(es), '
            f'{counts.get("self_hosted_executors", 0)} self-hosted executor credential(s).')
        for node in nodes:
            out(f'  node {node.get("name")}: {"online" if node.get("online") else "offline"}')
        if not any(counts.get(name) for name in ("nodes", "hosted_sandboxes", "self_hosted_executors")):
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
            raise ParsarError(f"--confirm-public-url-change must equal the new public URL {new}; nothing was applied")
    elif interactive:
        if input("Type the new public URL to continue: ").strip() != new:
            raise ParsarError("The public URL change was not confirmed; nothing was applied")
    else:
        raise ParsarError(f"Confirm with --confirm-public-url-change {new}; nothing was applied")


def paired_core(root, config):
    """(HTTP status, installation ID) of the Core that web.core_url reaches."""
    status, body = http(config["web"]["core_url"] + "/core/v1/installation", bearer(configuration.read_core_key(root)))
    return status, (json.loads(body).get("installation_id") if status == 200 else None)


def check_paired_core(root, config, state, args, interactive, out):
    """Web-only: detect a web.core_url that now reaches a different Core."""
    status, installation = paired_core(root, config)
    if status == 401:
        out("Warning: Core rejects this Web host's Core key; the key is out of date. "
            "Copy secrets/core.key from the Core host, then run parsar apply.")
    elif status == 404:
        out("Note: the paired Core runs an earlier release without /core/v1/installation. Convert or upgrade the "
            "Core host, then run parsar apply here to record which Core Web is paired with.")
    if status != 200:
        return state.get("core_installation_id")
    recorded = state.get("core_installation_id")
    if recorded and installation != recorded:
        out(f"web.core_url reaches Core installation {installation}, not the paired installation {recorded}.")
        if args.dry_run:
            out("Applying this change needs confirmation.")
        elif not (args.yes or (interactive and input("Type yes to pair Web with this Core: ").strip() == "yes")):
            raise ParsarError("Pairing Web with a different Core was not confirmed; nothing was applied")
    return installation


def read_generated(root, names):
    result = {}
    for name in names:
        path = root / "generated" / name
        result[name] = path.read_bytes() if path.is_file() and not path.is_symlink() else None
    return result


def apply(root, dry_run=False, yes=False, discard_edits=False, confirm_public_url_change=None,
          start=False, interactive=None, out=print):
    root = Path(root)
    args = argparse.Namespace(dry_run=dry_run, yes=yes, confirm_public_url_change=confirm_public_url_change)
    interactive = sys.stdin.isatty() if interactive is None else interactive
    with locked(root):
        return _apply(root, args, discard_edits, start, interactive, out)


def _apply(root, args, discard_edits, start, interactive, out, core_first=False, was_running=None, restore=None,
           progress=None):
    """Plan and apply config.json. progress["committed"] is true while state.json holds the new apply."""
    progress = {} if progress is None else progress
    config, config_digest = load_config(root)
    state = load_state(root)
    check_fixed(config, state)
    check_secrets(root, config, state)
    state = execution_options_import(root, state, out)
    applied = state.get("applied") or {}
    edited = edited_files(root, state)
    if edited and not discard_edits:
        raise ParsarError("\n".join(f"generated/{name} was edited by hand." for name in edited)
                          + "\nPut the change in config.json and run parsar apply --discard-edits, which keeps the"
                          " edited copy as generated/<file>.edited-<time>. Nothing was applied.")
    previous = previous_values(root, state, edited)
    names = set(applied.get("files") or {}) | set(state.get("pending") or {})

    def plan(applied_at):
        rendered = configuration.render(root, config, state, applied_at)
        before = read_generated(root, set(rendered.files) | names)
        changed = [name for name, text in rendered.files.items() if before[name] != text.encode()]
        removed = [name for name in names if name not in rendered.files and before[name] is not None]
        return rendered, before, changed, removed

    rendered, before, changed, removed = plan(applied.get("at") or now())
    if changed or removed:
        # Anything to write also stamps the snapshot, so settings.json changes too.
        rendered, before, changed, removed = plan(now())
    restarts = [name for name, digest in rendered.services.items() if (applied.get("services") or {}).get(name) != digest]
    was_running = running(root, state) if was_running is None else was_running
    current = config_model.values(config)
    keys = sorted(current) if previous is None else [
        key for key in current if key not in previous or previous[key] != current[key]]
    core_installation_id = state.get("core_installation_id")
    if config["mode"] == "web-only":
        core_installation_id = check_paired_core(root, config, state, args, interactive, out)
    elif state.get("applied"):
        if previous is not None:
            old = configuration.local_public_url({"public_url": previous["public_url"],
                                                  "ports": {"core": previous["ports.core"]}})
            core_port = previous["ports.core"]
        else:
            old, core_port = applied.get("public_url"), config["ports"]["core"]
        confirm_public_url(root, config, old, core_port, was_running, args, interactive, out)

    if previous is not None:
        out("Changed settings: " + (", ".join(keys) if keys else "none"))
    for name in edited:
        out(f"generated/{name} was edited by hand; it is overwritten and the edited copy is kept.")
    out("Files to write: " + (", ".join("generated/" + name for name in sorted(set(changed) | set(removed))) or "none"))
    will_run = plan_running(state, rendered.services, was_running, start)
    affected = [name for name in restarts if name != "migrate"]
    active = [name for name in affected if name in will_run]
    stopped = [name for name in affected if name not in will_run]
    if active:
        out("Services to restart: " + ", ".join(active)
            + (". Every Web sign-in session ends." if "web" in active else ""))
    if stopped:
        out("Stopped services stay stopped: " + ", ".join(stopped))
    if args.dry_run:
        out("Dry run: nothing was changed.")
        return
    if not (changed or removed or start or edited or restarts or state.get("pending")):
        if config_digest != applied.get("config_sha256") or core_installation_id != state.get("core_installation_id"):
            save_state(root, dict(state, core_installation_id=core_installation_id,
                                  applied=dict(applied, config_sha256=config_digest)))
        out("Nothing to apply.")
        return

    files = {name: configuration.sha256(text) for name, text in rendered.files.items()}
    # Record what is about to be written, so an interrupted write is not taken for hand edits.
    save_state(root, dict(state, pending=files))
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    for name in edited:
        if before.get(name) is not None:
            create_private(root / "generated" / f"{name}.edited-{stamp}", before[name])
    for name in sorted(set(changed) | set(edited)):
        if name in rendered.files:
            write_private(root / "generated" / name, rendered.files[name])
    for name in removed:
        (root / "generated" / name).unlink()
    new_state = {key: value for key, value in state.items() if key != "pending"}
    new_state.update(core_installation_id=core_installation_id, applied={
        "at": json.loads(rendered.files["settings.json"])["applied_at"], "config_sha256": config_digest,
        "public_url": configuration.local_public_url(config) if config["mode"] != "web-only" else config["public_url"],
        "files": files, "services": rendered.services})
    save_state(root, new_state)
    progress["committed"] = True
    try:
        converge(root, new_state, rendered.services, was_running, config["ports"].get("core"), restarts, start=start,
                 reload_unit=rendered.unit in changed, core_first=core_first)
        health(root, config, will_run)
    except (ParsarError, RuntimeError, subprocess.CalledProcessError) as error:
        if not state.get("applied"):
            # The first apply stays unfinished, so rerunning install.sh starts it again.
            save_state(root, {key: value for key, value in state.items() if key != "pending"})
            progress["committed"] = False
            raise ParsarError(f"{describe(error)}. The installation files were kept; run parsar status, fix the "
                              "cause and rerun install.sh with the same options") from None
        line = core_error_line(root, new_state)
        for name, data in before.items():
            path = root / "generated" / name
            if data is None:
                path.unlink(missing_ok=True)
            else:
                write_private(path, data)
        save_state(root, {key: value for key, value in state.items() if key != "pending"})
        progress["committed"] = False
        if restore:
            restore()
        with contextlib.suppress(ParsarError, RuntimeError, subprocess.CalledProcessError):
            converge(root, state, applied.get("services") or rendered.services, was_running,
                     previous["ports.core"] if previous and "ports.core" in previous else None, restarts,
                     reload_unit=rendered.unit in changed)
        if line:
            out(line)
        raise ParsarError(f"config.json not applied: {describe(error)}. The previous generated files were restored") from None
    out("Applied config.json.")


# Commands --------------------------------------------------------------------

def applied_view(root, state, config):
    """public_url, ports and web.core_url as last applied, which the running services use."""
    values = previous_values(root, state, edited_files(root, state))
    if values is None:
        if config is None:
            raise ParsarError("Neither config.json nor the last applied settings can be read")
        return config
    return {"mode": state["mode"], "public_url": values.get("public_url"),
            "ports": {name: values[f"ports.{name}"] for name in ("core", "web", "database") if f"ports.{name}" in values},
            "web": {"core_url": values.get("web.core_url")}}


def load_config_or_report(root, out):
    """config.json, or None after printing why it can't be used; the applied files still can."""
    try:
        return load_config(root)
    except (ParsarError, config_model.ConfigError) as error:
        out(str(error))
        return None, None


def status(root, out=print):
    loaded, config_digest = load_config_or_report(root, out)
    state = load_state(root)
    config = applied_view(root, state, loaded)
    mode = config["mode"]
    states = service_states(root)
    for name, row in sorted(states.items()):
        out(f'{name}: {row.get("State", "")} {row.get("Health", "")}'.rstrip())
    required = set(config_model.SERVICES[mode])
    observed = {name for name, row in states.items()
                if row.get("State") == "running" and row.get("Health", "") in ("", "healthy")}
    if native_service.is_native(state):
        core_active = native_service.active(state)
        out("core (systemd): " + ("running" if core_active else "stopped"))
        observed.discard("core")
        if core_active:
            observed.add("core")
    healthy = required <= observed
    if mode != "web-only":
        core_ok = http(core_base(config) + "/healthz")[0] == 200
        healthy = healthy and core_ok
        out("Core API: " + ("healthy" if core_ok else "unavailable"))
        key = configuration.read_core_key(root)
        digests = root / "generated/core-key-digests.json"
        if digests.is_file() and configuration.sha256(key) not in json.loads(digests.read_text()):
            out("secrets/core.key does not match generated/core-key-digests.json; run parsar apply")
        if core_ok and http(core_base(config) + "/core/v1/installation", bearer(key))[0] == 401:
            out("Core rejects secrets/core.key because it started with another key. Run parsar apply, or "
                "parsar stop and parsar start when nothing is left to apply.")
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
    if state.get("pending"):
        out(f"An earlier apply was interrupted; run {root / 'parsar'} apply")
    elif config_digest != (state.get("applied") or {}).get("config_sha256"):
        out(f"config.json has unapplied changes; run {root / 'parsar'} apply")
    for name in edited_files(root, state):
        out(f"generated/{name} was edited by hand; put the change in config.json and run parsar apply --discard-edits")
    if mode == "web-only":
        out("Reverse proxy: /v1 and /api/v1 go to Core; everything else goes to "
            f'127.0.0.1:{config["ports"]["web"]}')
        code, installation = paired_core(root, config)
        if code == 401:
            out("Paired Core: rejects this Web host's Core key; the key is out of date. Copy secrets/core.key "
                "from the Core host, then run parsar apply.")
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
        raise ParsarError("One or more installed services are unavailable")


def start(root, out=print):
    with locked(root):
        config, config_digest = load_config_or_report(root, out)
        state = load_state(root)
        if config_digest != (state.get("applied") or {}).get("config_sha256"):
            out("Warning: config.json has unapplied changes; starting with the last applied files.")
        for name in edited_files(root, state):
            out(f"Warning: generated/{name} was edited by hand.")
        for name, image in state["images"].items():
            if run(["docker", "image", "inspect", image], check=False, stdin=subprocess.DEVNULL,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
                raise ParsarError(f"The {name} image is missing; rerun install.sh from bundle "
                                  f'{state["source_commit"]} to reload images')
        services = (state.get("applied") or {}).get("services") or {}
        view = applied_view(root, state, config)
        converge(root, state, services, set(), view["ports"].get("core"), start=True)
        health(root, view, plan_running(state, services, set(), True))
    out("Services started.")


def stop(root, out=print):
    with locked(root):
        state = load_state(root)
        native_service.stop(root, state)
        compose(root, "stop")
    out("Control-plane services stopped. Data, nodes and sandbox resources are kept; running sandbox work may continue.")


def rotate_core_key(root, yes=False, interactive=None, out=print):
    interactive = sys.stdin.isatty() if interactive is None else interactive
    with locked(root):
        state = load_state(root)
        if state["mode"] == "web-only":
            raise ParsarError("Core owns the Core key. Copy secrets/core.key from the Core host into this "
                              "installation, then run parsar apply.")
        config, config_digest = load_config(root)
        if config_digest != (state.get("applied") or {}).get("config_sha256"):
            raise ParsarError("config.json has unapplied changes; run parsar apply first")
        if edited_files(root, state):
            raise ParsarError("Generated files were edited by hand; run parsar apply first")
        out("Rotating the Core key ends every Web sign-in session, and the current key stops working at once.")
        if not (yes or (interactive and input("Type yes to rotate the Core key: ").strip() == "yes")):
            raise ParsarError("Core key rotation was not confirmed; nothing was changed")
        old_key = configuration.read_core_key(root)
        original = read_private(root / "secrets/core.key", "secrets/core.key")
        was_running = running(root, state)
        replacement = root / "secrets/core.key.new"
        replacement.unlink(missing_ok=True)
        create_private(replacement, secrets.token_hex(32))
        if "web" in was_running:
            compose(root, "stop", "web")
        os.replace(replacement, root / "secrets/core.key")
        # Until state.json records the new apply, a failure keeps the current key; apply's
        # rollback restores its digest. After that point the new key is the installation's.
        restore = lambda: write_private(root / "secrets/core.key", original)
        args = argparse.Namespace(dry_run=False, yes=True, confirm_public_url_change=None)
        progress = {}
        try:
            _apply(root, args, False, False, False, out, core_first=True, was_running=was_running, restore=restore,
                   progress=progress)
        except BaseException:
            if not progress.get("committed"):
                restore()
                if "web" in was_running:
                    with contextlib.suppress(subprocess.CalledProcessError, OSError):
                        compose(root, "up", "--detach", "--wait", "--wait-timeout", "300", "web")
            raise
    if "core" in was_running:
        new = bearer(configuration.read_core_key(root))
        url = core_base(config) + "/core/v1/installation"
        if http(url, new)[0] != 200 or http(url, bearer(old_key))[0] != 401:
            raise ParsarError("Core did not confirm the new key and reject the old one; run parsar status")
    out(f"New Core key: {root / 'secrets/core.key'}. Sign in to Web again and update scripts that use the key.")
    out("A separate Web-only installation keeps its own copy: copy secrets/core.key to that host and run its parsar apply.")


def main(argv=None, root=None, out=print):
    root = Path(root) if root else Path(sys.argv[0]).resolve().parent
    parser = argparse.ArgumentParser(prog=str(root / "parsar"), description=__doc__.split("\n\n")[0])
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("status", help="Show service health, addresses and configuration drift")
    commands.add_parser("start", help="Start the installed services with the applied files")
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
        raise ParsarError(f"{root} is not an installation directory; run the parsar command inside it")
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
    except (ParsarError, config_model.ConfigError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError):
        # Errors never include generated configuration or external process output.
        print("parsar failed; run parsar status and inspect the installation files", file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)


if __name__ == "__main__":
    entry()
