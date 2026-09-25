"""Deployment files for the existing Core, Runtime and production console."""
import ipaddress
import os
import re
import stat
from pathlib import Path
from urllib.parse import urlsplit


_HOST_LABEL = re.compile(r"[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?")


def valid_core_origin(value):
    """Accept exactly the origins Core's ValidateSandboxCoreURL accepts
    (services/agents-api/internal/store/sandbox_deployment_setup.go), so an
    installer value never fails Core's AGENTS_API_PUBLIC_URL check at startup."""
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
        loopback = ipaddress.ip_address(host).is_loopback
    except ValueError:
        if netloc.startswith("[") or len(host) > 253 or not all(_HOST_LABEL.fullmatch(label) for label in host.split(".")):
            return False
        loopback = host == "localhost"
    return parsed.scheme == "https" or loopback


def environment_text(values):
    """The shared Compose/systemd subset: quoted, single-line literal values."""
    lines = ["# Core process configuration. Escape backslash, double quote and dollar with backslash.\n"]
    for key, value in values.items():
        if (not isinstance(key, str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key)
                or not isinstance(value, str) or any(char in value for char in "\x00\r\n")):
            raise RuntimeError("Core environment requires valid names and single-line string values")
        escaped = re.sub(r'([\\"$])', r'\\\1', value)
        lines.append(key + '="' + escaped + '"\n')
    return "".join(lines)


def read_core_environment(root, state=None):
    """Read the persisted authority without shell or ambient-variable expansion."""
    root = Path(root)
    path = root / "config/core.env"
    failure = "Core configuration is missing or unsafe; restore the private config/core.env file"
    try:
        directory = path.parent.lstat()
        if (not stat.S_ISDIR(directory.st_mode) or stat.S_IMODE(directory.st_mode) & 0o077
                or directory.st_uid != os.geteuid()):
            raise RuntimeError(failure)
        descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(descriptor, encoding="utf-8") as stream:
            info = os.fstat(stream.fileno())
            if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) & 0o077
                    or info.st_uid != os.geteuid() or info.st_nlink != 1):
                raise RuntimeError(failure)
            content = stream.read()
    except (OSError, UnicodeError):
        raise RuntimeError(failure) from None
    result = {}
    for line in content.split("\n"):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        match = re.fullmatch(r'([A-Za-z_][A-Za-z0-9_]*)="((?:[^\\"$\x00\r\n]|\\[\\"$])*)"', line)
        if not match or match[1] in result:
            raise RuntimeError("Core configuration requires unique names and double-quoted literal values")
        result[match[1]] = re.sub(r'\\([\\"$])', r'\1', match[2])
    if state is not None and result.get("AGENTS_API_SANDBOX_INSTALLATION_ID") != state["installation_id"]:
        raise RuntimeError("Core configuration installation identity differs")
    return result


def bind(source, target, readonly=True):
    return {"type": "bind", "source": str(source).replace("$", "$$"), "target": target, "read_only": readonly}


def core_environment(root, state):
    native = state["native_core"]
    config = str(Path(root) / "config") if native else "/config"
    database = f'127.0.0.1:{state["database_port"]}' if native else "database:5432"
    result = {
        # The password stays in its own file; the URL never carries it.
        "AGENTS_API_DATABASE_URL": f"postgres://agents_api@{database}/agents_api?sslmode=disable",
        "AGENTS_API_DATABASE_PASSWORD_FILE": config + "/database.password",
        "AGENTS_API_CREDENTIAL_KEY_FILE": config + "/credential.key",
        "AGENTS_API_ADDR": f'127.0.0.1:{state["core_port"]}' if native else ":8091",
        "AGENTS_API_ENGINE": "codex", "AGENTS_API_HARNESSES": "codex,claude_sdk,mcode",
        # The one origin applications, nodes, sandboxes and self-hosted executors
        # use. Without a public URL, only this host reaches Core's loopback port.
        "AGENTS_API_PUBLIC_URL": state.get("public_url") or f'http://127.0.0.1:{state["core_port"]}',
        "AGENTS_API_E2B_PROVIDER_BIN": (str(Path(root) / "native/e2b/agents-api-e2b-provider")
                                         if native else "/opt/parsar/e2b/agents-api-e2b-provider"),
        "AGENTS_API_E2B_STATE_DIR": str(Path(root) / "state/e2b") if native else "/state/e2b",
    }
    result["AGENTS_API_CORE_KEY_DIGESTS_FILE"] = (
        str(Path(root) / "admin/core-key-digests.json") if native else "/admin/core-key-digests.json")
    result["AGENTS_API_SANDBOX_INSTALLATION_ID"] = state["installation_id"]
    return result


def compose_config(root, state, manifest, database_password):
    root = Path(root)
    config = root / "config"
    identity = f'{state["uid"]}:{state["gid"]}'
    doc = {"name": state["project"], "services": {}}
    services = doc["services"]
    native = state["native_core"] and state["mode"] != "web-only"
    if state["mode"] != "web-only":
        services["database"] = {
            "image": manifest["images"]["database"], "restart": "unless-stopped",
            "environment": {"POSTGRES_USER": "agents_api", "POSTGRES_DB": "agents_api",
                            "POSTGRES_PASSWORD": database_password},
            "volumes": ["database:/var/lib/postgresql/data"],
            "healthcheck": {"test": ["CMD-SHELL", "pg_isready -U agents_api -d agents_api"],
                            "interval": "2s", "timeout": "5s", "retries": 30},
        }
        shared = {"image": manifest["images"]["core"], "user": identity,
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
            "image": manifest["images"]["web"], "user": identity, "restart": "unless-stopped",
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
