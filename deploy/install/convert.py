"""install.sh --convert: move an installation made before config.json to the new layout.

Conversion is also an upgrade to this bundle's release, because the earlier Core does
not read the new environment names. The preflight changes no file: it builds
config.json and state.json in memory, and anything it can't convert stops it with
"nothing was changed". An interrupted conversion resumes while installation.json and
config.json both exist.
"""
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
from urllib.parse import parse_qsl, urlsplit

import config_model
import configuration
import native_service
import parsar_cli

POOL = {name: key for key, name in configuration.POOL}
MAPPED = {"AGENTS_API_EXECUTION_CONCURRENCY", "PARSAR_LOG_LEVEL", "PARSAR_LOG_FORMAT", "PARSAR_LOG_ADD_SOURCE",
          "AGENTS_API_WRITE_AUDIT_RETENTION", "AGENTS_API_ENGINE", "AGENTS_API_HARNESSES",
          "AGENTS_API_OAUTH_TRUSTED_ORIGINS", "AGENTS_API_RUNTIME_HISTORY_FILE", "AGENTS_API_EXECUTION_OPTIONS_FILE",
          "AGENTS_API_DATABASE_URL"}
DERIVED = ("AGENTS_API_ADDR", "AGENTS_API_CREDENTIAL_KEY_FILE", "AGENTS_API_CORE_KEY_DIGESTS_FILE",
           "AGENTS_API_SANDBOX_INSTALLATION_ID", "AGENTS_API_CONFIG_FILE", "AGENTS_API_E2B_PROVIDER_BIN",
           "AGENTS_API_E2B_STATE_DIR", "AGENTS_API_DAEMON_WS_URL")


class ConvertError(Exception):
    pass


# The installer's generator before config.json (deploy/install/configuration.py at
# 5c3dcc16, unchanged since #124). Conversion compares the retained files with it.

def legacy_core_environment(root, state, database_password):
    native = state["native_core"]
    config = str(Path(root) / "config") if native else "/config"
    database = f'127.0.0.1:{state["database_port"]}' if native else "database:5432"
    daemon_host = f'127.0.0.1:{state["core_port"]}' if native else "core:8091"
    daemon_url = f"ws://{daemon_host}/api/v1/agent-daemon/ws"
    if state.get("public_url"):
        origin = urlsplit(state["public_url"])
        daemon_url = origin._replace(scheme="wss" if origin.scheme == "https" else "ws",
                                     path="/api/v1/agent-daemon/ws").geturl()
    return {
        "AGENTS_API_DATABASE_URL": f"postgres://agents_api:{database_password}@{database}/agents_api?sslmode=disable",
        "AGENTS_API_CREDENTIAL_KEY_FILE": config + "/credential.key",
        "AGENTS_API_ADDR": f'127.0.0.1:{state["core_port"]}' if native else ":8091",
        "AGENTS_API_ENGINE": "codex", "AGENTS_API_HARNESSES": "codex,claude_sdk,mcode",
        "AGENTS_API_DAEMON_WS_URL": daemon_url,
        "AGENTS_API_E2B_PROVIDER_BIN": (str(Path(root) / "native/e2b/agents-api-e2b-provider")
                                         if native else "/opt/parsar/e2b/agents-api-e2b-provider"),
        "AGENTS_API_E2B_STATE_DIR": str(Path(root) / "state/e2b") if native else "/state/e2b",
        "AGENTS_API_CORE_KEY_DIGESTS_FILE": (str(Path(root) / "admin/core-key-digests.json") if native
                                             else "/admin/core-key-digests.json"),
        "AGENTS_API_SANDBOX_INSTALLATION_ID": state["installation_id"],
        "AGENTS_API_CONFIG_FILE": config + "/core.env",
    }


def legacy_compose_config(root, state, images, database_password):
    root = Path(root)
    config = root / "config"
    identity = f'{state["uid"]}:{state["gid"]}'
    doc = {"name": state["project"], "services": {}}
    services = doc["services"]
    native = state["native_core"] and state["mode"] != "web-only"
    bind = configuration.bind
    if state["mode"] != "web-only":
        services["database"] = {
            "image": images.get("database"), "restart": "unless-stopped",
            "environment": {"POSTGRES_USER": "agents_api", "POSTGRES_DB": "agents_api",
                            "POSTGRES_PASSWORD": database_password},
            "volumes": ["database:/var/lib/postgresql/data"],
            "healthcheck": {"test": ["CMD-SHELL", "pg_isready -U agents_api -d agents_api"],
                            "interval": "2s", "timeout": "5s", "retries": 30},
        }
        shared = {"image": images.get("core"), "user": identity,
                  "env_file": [str(config / "core.env").replace("$", "$$")], "volumes": [bind(config, "/config")],
                  "read_only": True, "tmpfs": ["/tmp:mode=1777"], "init": True,
                  "security_opt": ["no-new-privileges:true"]}
        services["migrate"] = dict(shared, command=["/usr/local/bin/agents-api-migrate"],
                                   depends_on={"database": {"condition": "service_healthy"}})
        core = dict(shared, restart="unless-stopped", ports=[f'127.0.0.1:{state["core_port"]}:8091'],
                    depends_on={"migrate": {"condition": "service_completed_successfully"}})
        core["volumes"] = list(shared["volumes"])
        if native:
            services["database"]["ports"] = [f'127.0.0.1:{state["database_port"]}:5432']
            services.pop("migrate")
        else:
            core["volumes"].append(bind(root / "admin/core-key-digests.json", "/admin/core-key-digests.json"))
            core["volumes"].append(bind(root / "state/e2b", "/state/e2b", False))
            services["core"] = core
        doc["volumes"] = {"database": {}}
    if state["mode"] != "core-only":
        services["web"] = {
            "image": images.get("web"), "user": identity, "restart": "unless-stopped",
            "ports": [f'127.0.0.1:{state["web_port"]}:8080'], "read_only": True,
            "security_opt": ["no-new-privileges:true"],
            "volumes": [bind(root / "admin/core.key", "/admin/core.key")],
            "environment": {"CORE_CONSOLE_ORIGIN": state.get("public_url") or f'http://127.0.0.1:{state["web_port"]}',
                            "CORE_CONSOLE_UPSTREAM": (f'http://127.0.0.1:{state["core_port"]}' if native
                                                      else state.get("core_url") or "http://core:8091"),
                            "CORE_CONSOLE_CORE_KEY_FILE": "/admin/core.key"},
        }
        services["web"]["volumes"].append(bind(root / "node-payload", "/node-payload"))
        services["web"]["environment"]["CORE_CONSOLE_NODE_PAYLOAD_DIR"] = "/node-payload"
        if state["mode"] == "web-only" or native:
            services["web"].pop("ports")
            services["web"]["network_mode"] = "host"
            services["web"]["environment"]["CORE_CONSOLE_ADDR"] = f'127.0.0.1:{state["web_port"]}'
    return doc


def legacy_read_environment(root):
    """The earlier installer's strict reader: a private file of literal values."""
    path = Path(root) / "config/core.env"
    directory = path.parent.lstat()
    if (not stat.S_ISDIR(directory.st_mode) or stat.S_IMODE(directory.st_mode) & 0o077
            or directory.st_uid != os.geteuid()):
        raise RuntimeError("config/ must be a private directory")
    parsar_cli.check_private(path, "config/core.env")
    return configuration.read_environment(path.read_text())


# Preflight -------------------------------------------------------------------

def json_differences(expected, actual, path=""):
    if isinstance(expected, dict) and isinstance(actual, dict):
        result = []
        for key in sorted(set(expected) | set(actual), key=str):
            result += json_differences(expected.get(key), actual.get(key), f"{path}.{key}" if path else str(key))
        return result
    if isinstance(expected, list) and isinstance(actual, list) and len(expected) == len(actual):
        result = []
        for index, (left, right) in enumerate(zip(expected, actual)):
            result += json_differences(left, right, f"{path}[{index}]")
        return result
    return [] if expected == actual else [path]


def host_path(root, old, variable):
    """Where a file named in the old core.env lives on this host."""
    if old["native_core"]:
        return Path(variable)
    for prefix, directory in (("/config/", root / "config"), ("/state/e2b/", root / "state/e2b")):
        if variable.startswith(prefix) and "/" not in variable[len(prefix):]:
            return directory / variable[len(prefix):]
    return None


def detect(root):
    """The old installation record, or ConvertError naming what is missing."""
    try:
        old = json.loads((root / "installation.json").read_text())
    except (OSError, ValueError):
        raise ConvertError("installation.json can't be read; this installation can't be converted") from None
    if old.get("version") != 1:
        raise ConvertError("installation.json has an unknown version; this installation can't be converted")
    required = ["compose.json", "admin/core.key"]
    if old["mode"] != "web-only":
        required += ["admin/core-key-digests.json", "config/core.env", "config/credential.key",
                     "config/database.password"]
    missing = [name for name in required if not (root / name).is_file()]
    if missing:
        raise ConvertError("This installation predates the supported layout and can't be converted; missing: "
                           + ", ".join(missing))
    return old


class Plan:
    def __init__(self):
        self.problems, self.notes, self.leftovers = [], [], []
        self.config = self.state = None
        self.moves, self.deletions = [], []

    def problem(self, where, key, reason):
        self.problems.append(f"{where}{': ' + key if key else ''}: {reason}")


def old_core_deployment(root, old, plan, run):
    """Read the sandbox deployment's core_url from the old Core, starting it with its old files."""
    base = f'http://127.0.0.1:{old["core_port"]}'
    started = False
    if parsar_cli.http(base + "/healthz")[0] != 200:
        run(["docker", "compose", "-f", str(root / "compose.json"), "up", "--detach", "--wait"])
        if native_service.is_native(old):
            run(["systemctl", "--user", "start", native_service.unit_name(old)])
        started = True
        parsar_cli.wait_status(base + "/healthz")
    key = (root / "admin/core.key").read_text().strip()
    status, body = parsar_cli.http(base + "/core/v1/sandbox/deployment", parsar_cli.bearer(key))
    if status != 200:
        plan.problem("sandbox deployment", "core_url", "the old Core did not return it; make sure it starts with its "
                     "own files and that admin/core.key is current")
        return None, started
    return json.loads(body).get("core_url") or "", started


def preflight(root, bundle_manifest, images, public_url_override, run):
    """Build config.json and state.json in memory; change nothing."""
    old = detect(root)
    plan = Plan()
    mode = old["mode"]
    native = bool(old["native_core"]) and mode != "web-only"
    if (root / "config/managed-runtimes.json").exists():
        plan.problem("config/managed-runtimes.json", "", "retired file-managed provider configuration; preserve its "
                     "resources and follow the deployment replacement guide")
    password = (root / "config/database.password").read_text() if mode != "web-only" else ""
    compose = json.loads((root / "compose.json").read_text())
    own = {("core" if name == "migrate" else name): service.get("image")
           for name, service in compose.get("services", {}).items()}
    for key in json_differences(legacy_compose_config(root, old, own, password), compose):
        plan.problem("compose.json", key, "differs from the file the installer generated; undo the edit")

    config = {"$schema": "generated/config.schema.json", "format": 1, "mode": mode}
    if mode != "web-only":
        config["native_core"] = native
    ports = {}
    if mode != "web-only":
        ports["core"] = old["core_port"]
    if mode != "core-only":
        ports["web"] = old["web_port"]
    if native:
        ports["database"] = old["database_port"]
    config["ports"] = ports
    if mode == "web-only":
        config["web"] = {"core_url": old.get("core_url")}
    config["log"] = {"level": "info", "format": "auto", "add_source": False}
    retained = None
    known = {"config": set(), "admin": {"core.key"}}
    if mode != "web-only":
        known["config"] = {"core.env", "credential.key", "database.password"}
        known["admin"].add("core-key-digests.json")
        if native:
            known["config"].add(native_service.unit_name(old))
        try:
            env = legacy_read_environment(root)
        except (RuntimeError, parsar_cli.ParsarError, OSError, UnicodeError):
            plan.problem("config/core.env", "", "must be a private file of double-quoted literal values")
            env = None
        if env is not None:
            retained = map_environment(root, old, env, password, config, plan, known)
        try:
            digests = json.loads((root / "admin/core-key-digests.json").read_text())
        except ValueError:
            digests = None
        current = hashlib.sha256((root / "admin/core.key").read_text().strip().encode()).hexdigest()
        if not isinstance(digests, list) or current not in digests:
            plan.problem("admin/core-key-digests.json", "", "does not authorize admin/core.key; restore it")
        elif len(digests) > 1:
            plan.notes.append(f"admin/core-key-digests.json lists {len(digests) - 1} more Core key digest(s). "
                              "They are dropped; only admin/core.key works after conversion.")

    public = old.get("public_url")
    started = False
    if mode != "web-only" and not plan.problems:
        deployment, started = old_core_deployment(root, old, plan, run)
        if deployment:
            if public is None:
                public = deployment
                plan.notes.append(f"public_url {deployment} is adopted from the sandbox deployment, whose nodes use it.")
            elif public != deployment:
                if public_url_override in (public, deployment):
                    if public_url_override == public:
                        plan.notes.append(f"public_url {public} differs from the deployment's {deployment}: its nodes "
                                          "get no new sandboxes; remove them in Web and add them again.")
                    public = public_url_override
                    public_url_override = None
                else:
                    plan.problem("public_url", "", f"the installation's {public} differs from the sandbox deployment's "
                                 f"{deployment}. Rerun with --public-url {public} or --public-url {deployment}; "
                                 f"choosing {public} means the deployment's nodes must be removed and added again")
    if public_url_override is not None and not plan.problems:
        plan.problem("--public-url", "", "only settles a conflict between the installation and the sandbox deployment; "
                     "change public_url in config.json after conversion")
    config["public_url"] = public
    try:
        plan.config = config_model.validate(config)
    except config_model.ConfigError as error:
        for item in error.problems:
            plan.problem("config.json", "", item)

    for directory in ("config", "admin"):
        path = root / directory
        if path.is_dir():
            for entry in sorted(path.iterdir()):
                if entry.name not in known[directory]:
                    plan.leftovers.append(f"{directory}/{entry.name}")
    plan.moves = [("admin/core.key", "secrets/core.key")]
    plan.deletions = ["compose.json"]
    if mode != "web-only":
        plan.moves += [("config/credential.key", "secrets/credential.key"),
                       ("config/database.password", "secrets/database.password")]
        plan.deletions += ["config/core.env", "admin/core-key-digests.json"]
        if native:
            plan.deletions.append("config/" + native_service.unit_name(old))
    plan.state = {
        "format": 1, "installation_id": old["installation_id"], "project": old["project"],
        "uid": old["uid"], "gid": old["gid"], "mode": mode, "native_core": native,
        "source_commit": bundle_manifest["source_commit"], "images": images,
        "secrets_sha256": {Path(target).name: configuration.sha256((root / source).read_bytes())
                           for source, target in plan.moves},
        "core_installation_id": None, "execution_options_file": retained, "applied": None,
        "converted_from": {"source_commit": old["source_commit"],
                           "at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")}}
    return old, plan, started


def map_environment(root, old, env, password, config, plan, known):
    expected = legacy_core_environment(root, old, password)
    for name in DERIVED:
        if env.get(name) != expected[name]:
            plan.problem("config/core.env", name, "differs from the value the installer generated; restore it")
    for name in sorted(set(env) - set(DERIVED) - MAPPED):
        plan.problem("config/core.env", name, "is not a setting config.json can hold; remove it")
    base, _, query = env.get("AGENTS_API_DATABASE_URL", "").partition("?")
    pool = {}
    parameters = parse_qsl(query, keep_blank_values=True)
    if base + "?sslmode=disable" != expected["AGENTS_API_DATABASE_URL"] or ("sslmode", "disable") not in parameters:
        plan.problem("config/core.env", "AGENTS_API_DATABASE_URL", "names another database, user or password; "
                     "only the installation's own PostgreSQL can be converted")
    for name, value in parameters:
        if name == "sslmode":
            continue
        if name not in POOL or POOL[name] in pool:
            plan.problem("config/core.env", "AGENTS_API_DATABASE_URL", f"query parameter {name} can't be converted")
            continue
        pool[POOL[name]] = int(value) if value.isdigit() and POOL[name].endswith("conns") else value
    core = {"database_pool": {key: pool.get(key) for key, _ in configuration.POOL}}
    concurrency = env.get("AGENTS_API_EXECUTION_CONCURRENCY", "4")
    core["execution_concurrency"] = int(concurrency) if concurrency.isdigit() else concurrency
    engine = env.get("AGENTS_API_ENGINE") or "codex"
    enabled = {item.strip() for item in env.get("AGENTS_API_HARNESSES", "").split(",") if item.strip()} | {engine}
    core["harnesses"], core["default_harness"] = sorted(enabled), engine
    core["write_audit_retention"] = env.get("AGENTS_API_WRITE_AUDIT_RETENTION") or "2160h"
    core["oauth_trusted_origins"] = [item.strip() for item in env.get("AGENTS_API_OAUTH_TRUSTED_ORIGINS", "").split(",")
                                     if item.strip()]
    core["runtime_history"] = None
    if env.get("AGENTS_API_RUNTIME_HISTORY_FILE"):
        path = host_path(root, old, env["AGENTS_API_RUNTIME_HISTORY_FILE"])
        try:
            core["runtime_history"] = json.loads(path.read_text())
            plan.notes.append(f"Runtime history settings from {path} are now in config.json. The file is kept; "
                              "delete it if it holds export credentials.")
            if path.parent == root / "config":
                known["config"].add(path.name)
        except (AttributeError, OSError, ValueError):
            plan.problem("config/core.env", "AGENTS_API_RUNTIME_HISTORY_FILE", "names a file that can't be read as JSON")
    retained = None
    if env.get("AGENTS_API_EXECUTION_OPTIONS_FILE"):
        variable = env["AGENTS_API_EXECUTION_OPTIONS_FILE"]
        path = host_path(root, old, variable)
        if path is None or path.is_symlink() or not path.is_file():
            plan.problem("config/core.env", "AGENTS_API_EXECUTION_OPTIONS_FILE", "names a file that can't be found")
        else:
            retained = {"variable": variable, "path": str(path)}
            if path.parent == root / "config":
                known["config"].add(path.name)
            plan.notes.append(f"AGENTS_API_EXECUTION_OPTIONS_FILE and {path} stay as they are; config.json does not "
                              "hold model settings. A later release imports them into Core once, through parsar apply.")
    log = config["log"]
    level = env.get("PARSAR_LOG_LEVEL", "").strip().lower()
    log["level"] = {"": "info", "warning": "warn"}.get(level, level)
    log["format"] = env.get("PARSAR_LOG_FORMAT", "").strip().lower() or "auto"
    add_source = env.get("PARSAR_LOG_ADD_SOURCE", "")
    log["add_source"] = add_source == "1" if add_source in ("", "0", "1") else add_source
    if any(name.startswith("PARSAR_LOG_") for name in env):
        plan.notes.append("PARSAR_LOG_* had no effect before this release; the log settings now apply.")
    config["core"] = core
    return retained


# Steps -----------------------------------------------------------------------

def confirm(root, old, plan, yes, interactive, out):
    out("Conversion writes config.json and state.json, moves the secrets into secrets/, removes the old generated")
    out("files and upgrades Core to this release. Database migrations can't be undone; back up first:")
    if old["mode"] != "web-only":
        out(f'  docker compose -f {root / "compose.json"} exec -T database pg_dump -U agents_api agents_api > parsar-backup.sql')
    for note in plan.notes:
        out("Note: " + note)
    for name in plan.leftovers:
        out(f"Left in place: {name} (not part of the installer's layout)")
    defaults = {key: node.get("default") for key, node, _ in config_model.leaves()}
    shown = {key: value for key, value in config_model.values(plan.config).items()
             if key not in config_model.sensitive_keys() and (key in ("mode", "public_url") or value != defaults[key])}
    out("config.json, apart from defaults: " + json.dumps(shown))
    if not (yes or (interactive and input("Type yes to convert this installation: ").strip() == "yes")):
        raise ConvertError("Conversion was not confirmed; nothing was changed. Rerun with --yes to skip the prompt")


def stop_old(root, old, run):
    if native_service.is_native(old):
        native_service.disable(old)
    run(["docker", "compose", "-f", str(root / "compose.json"), "stop"])


def move(root, source, target):
    source, target = root / source, root / target
    if source.exists() and target.exists():
        if source.read_bytes() != target.read_bytes():
            raise ConvertError(f"Both {source} and {target} exist with different content; keep the right one and rerun")
        source.unlink()
    elif source.exists():
        os.rename(source, target)
    elif not target.exists():
        raise ConvertError(f"{source} is missing; restore it and rerun --convert")


def write_layout(root, old, plan):
    """Step 5. Every operation checks its source and target, so a rerun resumes."""
    for name in ("secrets", "generated"):
        (root / name).mkdir(mode=0o700, exist_ok=True)
        os.chmod(root / name, 0o700)
    parsar_cli.write_private(root / "state.json", json.dumps(plan.state, indent=2) + "\n")
    parsar_cli.create_private(root / "config.json", json.dumps(plan.config, indent=2) + "\n")
    finish_layout(root, old, plan.moves, plan.deletions)


def finish_layout(root, old, moves, deletions):
    for source, target in moves:
        move(root, source, target)
    for name in deletions:
        (root / name).unlink(missing_ok=True)
    for directory in ("config", "admin"):
        path = root / directory
        if path.is_dir() and not any(path.iterdir()):
            path.rmdir()
    (root / "installation.json").unlink()


def resume(root, out):
    old = json.loads((root / "installation.json").read_text())
    moves = [("admin/core.key", "secrets/core.key")]
    deletions = ["compose.json"]
    if old["mode"] != "web-only":
        moves += [("config/credential.key", "secrets/credential.key"),
                  ("config/database.password", "secrets/database.password")]
        deletions += ["config/core.env", "admin/core-key-digests.json"]
        if native_service.is_native(old):
            deletions.append("config/" + native_service.unit_name(old))
    out("Resuming an interrupted conversion.")
    finish_layout(root, old, moves, deletions)


def convert(root, manifest, load_images, public_url_override, yes, run, interactive=None, out=print):
    """Steps 1-5. The caller then loads the bundle's files and applies (step 6)."""
    interactive = sys.stdin.isatty() if interactive is None else interactive
    if (root / "config.json").exists():
        load_images(json.loads((root / "state.json").read_text())["images"])
        resume(root, out)
        return
    old = detect(root)
    wanted = ["web"] if old["mode"] == "web-only" else (["database"] if old["native_core"] else ["core", "database"])
    if old["mode"] == "all":
        wanted.append("web")
    old, plan, started = preflight(root, manifest, {name: None for name in wanted}, public_url_override, run)
    try:
        if plan.problems:
            raise ConvertError("This installation can't be converted yet:\n"
                               + "\n".join("  - " + item for item in plan.problems) + "\nNothing was changed.")
        confirm(root, old, plan, yes, interactive, out)
    except ConvertError:
        if started:
            # Leave the services as they were found.
            run(["docker", "compose", "-f", str(root / "compose.json"), "stop"])
            native_service.stop(root, old)
        raise
    plan.state["images"] = load_images(wanted)
    stop_old(root, old, run)
    write_layout(root, old, plan)
