"""Derive every file under generated/ from config.json, state.json and secrets/.

Generated files hold no secret except runtime-history.json, which carries the
operator's export headers. Secrets stay in secrets/, one copy each, and reach the
services as read-only single-file mounts or file paths.
"""
import hashlib
import ipaddress
import json
from pathlib import Path
import re
from urllib.parse import urlencode, urlsplit

import config_model
import native_service

# Where Core and Web containers see secrets and generated inputs.
RUN = "/run/oac"
POOL = (("max_conns", "pool_max_conns"), ("min_conns", "pool_min_conns"),
        ("max_conn_lifetime", "pool_max_conn_lifetime"), ("max_conn_idle_time", "pool_max_conn_idle_time"),
        ("health_check_period", "pool_health_check_period"))


_HOST_LABEL = re.compile(r"[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?")


def valid_core_origin(value):
    """Accept exactly the origins Core's ValidateSandboxCoreURL accepts
    (services/agents-api/internal/store/sandbox_deployment_setup.go), so an
    installer value never fails Core's OAC_PUBLIC_URL check at startup."""
    if not isinstance(value, str) or any(char in value for char in "?#@\\% \t\r\n"):
        return False
    try:
        parsed = urlsplit(value)
    except ValueError:
        return False
    netloc = parsed.netloc
    if (parsed.scheme not in ("http", "https") or value != parsed.scheme + "://" + netloc
            or not netloc or netloc != netloc.lower() or netloc.endswith(":")):
        return False
    if netloc.startswith("["):
        host, _, rest = netloc[1:].partition("]")
        if rest and not rest.startswith(":"):
            return False
        port = rest[1:] if rest else ""
    else:
        host, _, port = netloc.partition(":")
    if port and not (port.isdigit() and str(int(port)) == port and 1 <= int(port) <= 65535):
        return False
    try:
        address = ipaddress.ip_address(host)
        # Go's IsLoopback also counts an IPv4-mapped loopback address.
        mapped = getattr(address, "ipv4_mapped", None)
        loopback = address.is_loopback or bool(mapped and mapped.is_loopback)
    except ValueError:
        if netloc.startswith("[") or len(host) > 253 or not all(_HOST_LABEL.fullmatch(label) for label in host.split(".")):
            return False
        loopback = host == "localhost"
    return parsed.scheme == "https" or loopback


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
    return f"Generated from {Path(root) / 'config.json'}. Do not edit; change config.json and run {Path(root) / 'oac'} apply."


def read_core_key(root):
    """The Core key, checked the way Web checks it."""
    key = (Path(root) / "secrets/core.key").read_text().strip()
    if len(key) < 32 or any(char.isspace() or char == "\x00" for char in key):
        raise RuntimeError("secrets/core.key must hold one Core key of at least 32 characters without whitespace")
    return key


def secret_digests(root, mode):
    names = ["core.key"] if mode == "web-only" else ["core.key", "credential.key", "database.password"]
    return {name: sha256((Path(root) / "secrets" / name).read_bytes()) for name in names}


def valid_listen_host(value):
    try:
        return "%" not in value and bool(ipaddress.ip_address(value))
    except ValueError:
        return False


def loopback_listener(host):
    address = ipaddress.ip_address(host)
    mapped = getattr(address, "ipv4_mapped", None)
    return address.is_loopback or bool(mapped and mapped.is_loopback)


def service_address(config, service, connect=False):
    """One derivation for listen addresses and local operator connections."""
    host = config["host"]
    address = ipaddress.ip_address(host)
    host = str(address)
    if connect and address.is_unspecified:
        host = "::1" if address.version == 6 else "127.0.0.1"
    if address.version == 6:
        host = "[" + host + "]"
    return f'{host}:{config["ports"][service]}'


def service_origin(config, service):
    return "http://" + service_address(config, service, connect=True)


def web_origin(config):
    return config["public_url"] or service_origin(config, "web")


def local_public_url(config):
    """OAC_PUBLIC_URL: the public origin, or Core's loopback origin for local use."""
    return config["public_url"] or service_origin(config, "core")


def log_environment(log):
    result = {"OAC_LOG_LEVEL": log["level"]}
    if log["format"] != "auto":
        result["OAC_LOG_FORMAT"] = log["format"]
    if log["add_source"]:
        result["OAC_LOG_ADD_SOURCE"] = "1"
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
        "OAC_ADDR": service_address(config, "core") if native else ":8091",
        "OAC_PUBLIC_URL": local_public_url(config),
        "OAC_DATABASE_URL": f"postgres://agents_api@{database}/agents_api?" + urlencode(query),
        "OAC_DATABASE_PASSWORD_FILE": secrets + "/database.password",
        "OAC_CREDENTIAL_KEY_FILE": secrets + "/credential.key",
        "OAC_CORE_KEY_DIGESTS_FILE": generated + "/core-key-digests.json",
        "OAC_INSTALLATION_ID": state["installation_id"],
        "OAC_SETTINGS_FILE": generated + "/settings.json",
        "OAC_E2B_STATE_DIR": str(root / "state/e2b") if native else "/state/e2b",
        "OAC_DEFAULT_HARNESS": core["default_harness"],
        "OAC_HARNESSES": ",".join(core["harnesses"]),
        "OAC_EXECUTION_CONCURRENCY": str(core["execution_concurrency"]),
        "OAC_WRITE_AUDIT_RETENTION": core["write_audit_retention"],
    }
    if native:
        result["OAC_E2B_PROVIDER_BIN"] = str(root / "native/e2b/oac-e2b-provider")
    if core["oauth_trusted_origins"]:
        result["OAC_OAUTH_TRUSTED_ORIGINS"] = ",".join(core["oauth_trusted_origins"])
    if core["runtime_history"] is not None:
        result["OAC_HISTORY_SETTINGS_FILE"] = generated + "/runtime-history.json"
    result.update(log_environment(config["log"]))
    return result


def settings_document(root, config, applied_at):
    root = Path(root)
    return {"path": str(root / "config.json"), "apply_command": f"{root / 'oac'} apply",
            "applied_at": applied_at, "settings": config_model.settings(config)}



def conversion_labels(state):
    return {"io.oac.installation": state["installation_id"], "io.oac.conversion": state["renamed_from"]["at"]}


def compose_config(root, config, state):
    root = Path(root)
    mode, native = config["mode"], config.get("native_core", False)
    identity = f'{state["uid"]}:{state["gid"]}'
    images = state["images"]
    doc = {"name": state["project"],
           # Compose interpolates every string, extension fields included.
           "x-oac": {"generated_from": str(root / "config.json").replace("$", "$$"),
                        "edit": "config.json, then oac apply"},
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
        if state.get("renamed_from"):
            # Keep the exact copied-volume definition: Compose must never offer
            # to replace a populated conversion volume because its labels differ.
            doc["volumes"]["database"]["labels"] = conversion_labels(state)
        if native:
            services["database"]["ports"] = [f'127.0.0.1:{config["ports"]["database"]}:5432']
        else:
            mounts = [bind(root / "secrets" / name, f"{RUN}/{name}") for name in ("credential.key", "database.password")]
            mounts += [bind(root / "generated" / name, f"{RUN}/{name}") for name in ("core-key-digests.json", "settings.json")]
            if config["core"]["runtime_history"] is not None:
                mounts.append(bind(root / "generated/runtime-history.json", f"{RUN}/runtime-history.json"))
            shared = {"image": images["core"], "user": identity,
                      "env_file": [str(root / "generated/core.env").replace("$", "$$")], "volumes": mounts,
                      "read_only": True, "tmpfs": ["/tmp:mode=1777"], "init": True,
                      "security_opt": ["no-new-privileges:true"]}
            services["migrate"] = dict(shared, command=["/usr/local/bin/oac-core-migrate"],
                                       depends_on={"database": {"condition": "service_healthy"}})
            services["core"] = dict(shared, restart="unless-stopped", ports=[service_address(config, "core") + ":8091"],
                                    depends_on={"migrate": {"condition": "service_completed_successfully"}},
                                    volumes=mounts + [bind(root / "state/e2b", "/state/e2b", False)])
    if mode != "core-only":
        if mode == "web-only":
            upstream = config["web"]["core_url"]
        else:
            upstream = service_origin(config, "core") if native else "http://core:8091"
        environment = {
            "OAC_WEB_ORIGIN": web_origin(config),
            "OAC_WEB_UPSTREAM": upstream,
            "OAC_WEB_CORE_KEY_FILE": f"{RUN}/core.key",
            "OAC_WEB_NODE_PAYLOAD_DIR": "/node-payload",
        }
        environment.update(log_environment(config["log"]))
        web = {"image": images["web"], "user": identity, "restart": "unless-stopped",
               "ports": [service_address(config, "web") + ":8080"], "read_only": True,
               "security_opt": ["no-new-privileges:true"],
               "volumes": [bind(root / "secrets/core.key", f"{RUN}/core.key"), bind(root / "node-payload", "/node-payload")],
               "environment": environment}
        if mode == "web-only" or native:
            web.pop("ports")
            web["network_mode"] = "host"
            environment["OAC_WEB_ADDR"] = service_address(config, "web")
        services["web"] = web
    return doc


LABEL = "io.oac.inputs"


class Rendered:
    """Generated file contents, plus the inputs digest each service must run with.

    The digest covers everything a service reads: its Compose definition, the
    env_file content and the files and secrets it mounts. It is the service's
    `io.oac.inputs` label, or OAC_INPUTS in the native unit, so the running
    services can be compared with a render at any time.
    """

    def __init__(self, files, services, unit):
        self.files, self.services, self.unit = files, services, unit


def render(root, config, state, applied_at):
    root = Path(root)
    mode, native = config["mode"], config.get("native_core", False)
    secrets = secret_digests(root, mode)
    settings = settings_document(root, config, applied_at)
    files = {"config.schema.json": config_model.SCHEMA_TEXT,
             "settings.json": json.dumps(settings, indent=2) + "\n"}
    external, core_env, unit = {}, "", None
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
        external["core"] = json.dumps({
            "core-key-digests.json": sha256(files["core-key-digests.json"]),
            "settings": sha256(json.dumps([settings["path"], settings["apply_command"], core_settings], sort_keys=True)),
            "runtime-history.json": sha256(files.get("runtime-history.json", "")),
            "credential.key": secrets["credential.key"], "database.password": secrets["database.password"],
        }, sort_keys=True)
    if mode != "core-only":
        external["web"] = json.dumps({"core.key": secrets["core.key"]})
    compose = compose_config(root, config, state)
    services = {}
    for name, service in compose["services"].items():
        # Compose resolves env_file into the service configuration, so its content counts.
        text = json.dumps(service, sort_keys=True) + (core_env if "env_file" in service else "")
        services[name] = sha256(text + external.get("core" if name == "migrate" else name, ""))
        service["labels"] = {LABEL: services[name]}
    files["compose.json"] = json.dumps(compose, indent=2) + "\n"
    if native:
        unit = native_service.unit_name(state)
        services["core"] = sha256(core_env + native_service.unit_text(root, edit_hint(root)) + external["core"])
        files[unit] = native_service.unit_text(root, edit_hint(root), services["core"])
    return Rendered(files, services, unit)


def rendered_inputs(files, unit):
    """The inputs digest per service that a set of generated files asks for."""
    result = {}
    if files.get("compose.json"):
        try:
            for name, service in json.loads(files["compose.json"])["services"].items():
                result[name] = service.get("labels", {}).get(LABEL)
        except (ValueError, KeyError, AttributeError):
            return {}
    if unit and files.get(unit):
        text = files[unit].decode() if isinstance(files[unit], bytes) else files[unit]
        match = re.search(r"^Environment=OAC_INPUTS=([0-9a-f]{64})$", text, re.M)
        result["core"] = match[1] if match else None
    return result
