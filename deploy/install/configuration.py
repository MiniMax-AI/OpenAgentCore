"""Derive every file under generated/ from config.json, state.json and secrets/.

Generated files hold no secret except runtime-history.json, which carries the
operator's export headers. Secrets stay in secrets/, one copy each, and reach the
services as read-only single-file mounts or file paths.
"""
import hashlib
import json
from pathlib import Path
import re
from urllib.parse import urlencode

import config_model
import native_service

# Where Core and Web containers see secrets and generated inputs.
RUN = "/run/parsar"
POOL = (("max_conns", "pool_max_conns"), ("min_conns", "pool_min_conns"),
        ("max_conn_lifetime", "pool_max_conn_lifetime"), ("max_conn_idle_time", "pool_max_conn_idle_time"),
        ("health_check_period", "pool_health_check_period"))


def environment_text(values, header):
    """The shared Compose/systemd subset: quoted, single-line literal values."""
    lines = ["# " + header + "\n",
             "# Values are literal. Escape backslash, double quote and dollar with backslash.\n"]
    for key, value in values.items():
        if (not isinstance(key, str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key)
                or not isinstance(value, str) or any(char in value for char in "\x00\r\n")):
            raise RuntimeError("Core environment requires valid names and single-line string values")
        escaped = re.sub(r'([\\"$])', r'\\\1', value)
        lines.append(key + '="' + escaped + '"\n')
    return "".join(lines)


def read_environment(text):
    """Parse the environment_text format without shell or variable expansion."""
    result = {}
    for line in text.split("\n"):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        match = re.fullmatch(r'([A-Za-z_][A-Za-z0-9_]*)="((?:[^\\"$\x00\r\n]|\\[\\"$])*)"', line)
        if not match or match[1] in result:
            raise RuntimeError("An environment file requires unique names and double-quoted literal values")
        result[match[1]] = re.sub(r'\\([\\"$])', r'\1', match[2])
    return result


def bind(source, target, readonly=True):
    return {"type": "bind", "source": str(source).replace("$", "$$"), "target": target, "read_only": readonly}


def sha256(data):
    return hashlib.sha256(data.encode() if isinstance(data, str) else data).hexdigest()


def edit_hint(root):
    return f"Generated from {Path(root) / 'config.json'}. Do not edit; change config.json and run {Path(root) / 'parsar'} apply."


def read_core_key(root):
    """The Core key, checked the way Web checks it."""
    key = (Path(root) / "secrets/core.key").read_text().strip()
    if len(key) < 32 or any(char.isspace() or char == "\x00" for char in key):
        raise RuntimeError("secrets/core.key must hold one Core key of at least 32 characters without whitespace")
    return key


def secret_digests(root, mode):
    names = ["core.key"] if mode == "web-only" else ["core.key", "credential.key", "database.password"]
    return {name: sha256((Path(root) / "secrets" / name).read_bytes()) for name in names}


def local_public_url(config):
    """AGENTS_API_PUBLIC_URL: the public origin, or Core's loopback origin for local use."""
    return config["public_url"] or f'http://127.0.0.1:{config["ports"]["core"]}'


def log_environment(log):
    result = {"PARSAR_LOG_LEVEL": log["level"]}
    if log["format"] != "auto":
        result["PARSAR_LOG_FORMAT"] = log["format"]
    if log["add_source"]:
        result["PARSAR_LOG_ADD_SOURCE"] = "1"
    return result


def core_environment(root, config, state):
    root = Path(root)
    native = config["native_core"]
    generated = str(root / "generated") if native else RUN
    secrets = str(root / "secrets") if native else RUN
    ports, core = config["ports"], config["core"]
    database = f'127.0.0.1:{ports["database"]}' if native else "database:5432"
    query = [("sslmode", "disable")] + [(name, str(core["database_pool"][key])) for key, name in POOL
                                        if core["database_pool"][key] is not None]
    result = {
        "AGENTS_API_ADDR": f'127.0.0.1:{ports["core"]}' if native else ":8091",
        "AGENTS_API_PUBLIC_URL": local_public_url(config),
        "AGENTS_API_DATABASE_URL": f"postgres://agents_api@{database}/agents_api?" + urlencode(query),
        "AGENTS_API_DATABASE_PASSWORD_FILE": secrets + "/database.password",
        "AGENTS_API_CREDENTIAL_KEY_FILE": secrets + "/credential.key",
        "AGENTS_API_CORE_KEY_DIGESTS_FILE": generated + "/core-key-digests.json",
        "AGENTS_API_SANDBOX_INSTALLATION_ID": state["installation_id"],
        "AGENTS_API_SETTINGS_FILE": generated + "/settings.json",
        "AGENTS_API_E2B_STATE_DIR": str(root / "state/e2b") if native else "/state/e2b",
        "AGENTS_API_ENGINE": core["default_harness"],
        "AGENTS_API_HARNESSES": ",".join(core["harnesses"]),
        "AGENTS_API_EXECUTION_CONCURRENCY": str(core["execution_concurrency"]),
        "AGENTS_API_WRITE_AUDIT_RETENTION": core["write_audit_retention"],
    }
    if native:
        result["AGENTS_API_E2B_PROVIDER_BIN"] = str(root / "native/e2b/agents-api-e2b-provider")
    if core["oauth_trusted_origins"]:
        result["AGENTS_API_OAUTH_TRUSTED_ORIGINS"] = ",".join(core["oauth_trusted_origins"])
    if core["runtime_history"] is not None:
        result["AGENTS_API_RUNTIME_HISTORY_FILE"] = generated + "/runtime-history.json"
    retained = state.get("execution_options_file")
    if retained:
        # Kept by --convert until phase 3's one-time import moves it into Core.
        result["AGENTS_API_EXECUTION_OPTIONS_FILE"] = retained["variable"]
    result.update(log_environment(config["log"]))
    return result


def retained_digest(retained):
    if not retained:
        return None
    try:
        return sha256(Path(retained["path"]).read_bytes())
    except OSError:
        raise RuntimeError(f'{retained["path"]}, named by AGENTS_API_EXECUTION_OPTIONS_FILE, is missing; '
                           "restore it") from None


def settings_document(root, config, applied_at):
    root = Path(root)
    return {"path": str(root / "config.json"), "apply_command": f"{root / 'parsar'} apply",
            "applied_at": applied_at, "settings": config_model.settings(config)}


def compose_config(root, config, state, inputs):
    root = Path(root)
    mode, native = config["mode"], config.get("native_core", False)
    identity = f'{state["uid"]}:{state["gid"]}'
    images = state["images"]
    doc = {"name": state["project"],
           "x-parsar": {"generated_from": str(root / "config.json"), "edit": "config.json, then parsar apply"},
           "services": {}}
    services = doc["services"]
    if mode != "web-only":
        services["database"] = {
            "image": images["database"], "restart": "unless-stopped",
            "environment": {"POSTGRES_USER": "agents_api", "POSTGRES_DB": "agents_api",
                            "POSTGRES_PASSWORD_FILE": "/run/secrets/database.password"},
            "volumes": ["database:/var/lib/postgresql/data",
                        bind(root / "secrets/database.password", "/run/secrets/database.password")],
            "healthcheck": {"test": ["CMD-SHELL", "pg_isready -U agents_api -d agents_api"],
                            "interval": "2s", "timeout": "5s", "retries": 30},
        }
        doc["volumes"] = {"database": {}}
        if native:
            services["database"]["ports"] = [f'127.0.0.1:{config["ports"]["database"]}:5432']
        else:
            mounts = [bind(root / "secrets" / name, f"{RUN}/{name}") for name in ("credential.key", "database.password")]
            mounts += [bind(root / "generated" / name, f"{RUN}/{name}") for name in ("core-key-digests.json", "settings.json")]
            if config["core"]["runtime_history"] is not None:
                mounts.append(bind(root / "generated/runtime-history.json", f"{RUN}/runtime-history.json"))
            retained = state.get("execution_options_file")
            if retained and not retained["variable"].startswith("/state/e2b/"):
                mounts.append(bind(retained["path"], retained["variable"]))
            shared = {"image": images["core"], "user": identity,
                      "env_file": [str(root / "generated/core.env").replace("$", "$$")], "volumes": mounts,
                      "read_only": True, "tmpfs": ["/tmp:mode=1777"], "init": True,
                      "security_opt": ["no-new-privileges:true"], "labels": {"io.parsar.inputs": inputs["core"]}}
            services["migrate"] = dict(shared, command=["/usr/local/bin/agents-api-migrate"],
                                       depends_on={"database": {"condition": "service_healthy"}})
            services["core"] = dict(shared, restart="unless-stopped", ports=[f'127.0.0.1:{config["ports"]["core"]}:8091'],
                                    depends_on={"migrate": {"condition": "service_completed_successfully"}},
                                    volumes=mounts + [bind(root / "state/e2b", "/state/e2b", False)])
    if mode != "core-only":
        if mode == "web-only":
            upstream = config["web"]["core_url"]
        else:
            upstream = f'http://127.0.0.1:{config["ports"]["core"]}' if native else "http://core:8091"
        environment = {
            "CORE_CONSOLE_ORIGIN": config["public_url"] or f'http://127.0.0.1:{config["ports"]["web"]}',
            "CORE_CONSOLE_UPSTREAM": upstream,
            "CORE_CONSOLE_CORE_KEY_FILE": f"{RUN}/core.key",
            "CORE_CONSOLE_NODE_PAYLOAD_DIR": "/node-payload",
        }
        environment.update(log_environment(config["log"]))
        web = {"image": images["web"], "user": identity, "restart": "unless-stopped",
               "ports": [f'127.0.0.1:{config["ports"]["web"]}:8080'], "read_only": True,
               "security_opt": ["no-new-privileges:true"],
               "volumes": [bind(root / "secrets/core.key", f"{RUN}/core.key"), bind(root / "node-payload", "/node-payload")],
               "environment": environment, "labels": {"io.parsar.inputs": inputs["web"]}}
        if mode == "web-only" or native:
            web.pop("ports")
            web["network_mode"] = "host"
            environment["CORE_CONSOLE_ADDR"] = f'127.0.0.1:{config["ports"]["web"]}'
        services["web"] = web
    return doc


class Rendered:
    """Generated file contents plus one digest per service for restart planning."""

    def __init__(self, files, services, unit):
        self.files, self.services, self.unit = files, services, unit


def render(root, config, state, applied_at):
    root = Path(root)
    mode, native = config["mode"], config.get("native_core", False)
    secrets = secret_digests(root, mode)
    settings = settings_document(root, config, applied_at)
    files = {"config.schema.json": config_model.SCHEMA_TEXT,
             "settings.json": json.dumps(settings, indent=2) + "\n"}
    inputs, core_env, unit = {}, "", None
    if mode != "web-only":
        files["core-key-digests.json"] = json.dumps([sha256(read_core_key(root))]) + "\n"
        history = config["core"]["runtime_history"]
        if history is not None:
            files["runtime-history.json"] = json.dumps(history, indent=2) + "\n"
        core_env = environment_text(core_environment(root, config, state), edit_hint(root))
        files["core.env"] = core_env
        # Only settings Core itself restarts for enter its inputs, so a Web-only change
        # leaves Core running. Its snapshot then refreshes on Core's next restart.
        core_settings = [item for item in settings["settings"] if "core" in item["restarts"]]
        retained = state.get("execution_options_file")
        inputs["core"] = sha256(json.dumps({
            "core-key-digests.json": sha256(files["core-key-digests.json"]),
            "settings": sha256(json.dumps([settings["path"], settings["apply_command"], core_settings], sort_keys=True)),
            "runtime-history.json": sha256(files.get("runtime-history.json", "")),
            "credential.key": secrets["credential.key"], "database.password": secrets["database.password"],
            "execution-options": retained_digest(retained),
        }, sort_keys=True))
        if native:
            unit = native_service.unit_name(state)
            files[unit] = native_service.unit_text(root, edit_hint(root))
    if mode != "core-only":
        inputs["web"] = sha256(json.dumps({"core.key": secrets["core.key"]}))
    compose = compose_config(root, config, state, inputs)
    files["compose.json"] = json.dumps(compose, indent=2) + "\n"
    services = {}
    for name, service in compose["services"].items():
        # Compose resolves env_file into the service configuration, so its content counts.
        text = json.dumps(service, sort_keys=True) + (core_env if "env_file" in service else "")
        services[name] = sha256(text)
    if native:
        services["core"] = sha256(core_env + files[unit] + inputs["core"])
    return Rendered(files, services, unit)
