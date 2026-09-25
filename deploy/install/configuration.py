"""Deployment files for the existing Core, Runtime and production console."""
from pathlib import Path
from urllib.parse import urlsplit


def bind(source, target, readonly=True):
    return {"type": "bind", "source": str(source), "target": target, "read_only": readonly}


def managed_config(root, state, manifest):
    root = Path(root)
    result = {"installation_id": state["installation_id"], "provider": state["provider"],
              "maintenance": False}
    if state["provider"] == "docker":
        result.update(core_url="http://core:8091/api/v1", docker={
            "host": "unix:///var/run/docker.sock", "image": manifest["images"]["runtime"],
            "network": state["project"] + "-runtime", "seccomp_file": "/config/seccomp.json",
            "nested_sandbox": True,
        })
    else:
        result.update(core_url=f'http://host.microsandbox.internal:{state["core_port"]}/api/v1', microsandbox={
            "helper_path": str(root / "native/bin/agents-api-microsandbox-provider"),
            "runtime_path": str(root / "native/microsandbox/msb"),
            "firmware_path": str(root / "native/microsandbox/libkrunfw.so.5.6.1"),
            "runtime_sha256": manifest["microsandbox"]["runtime_sha256"],
            "firmware_sha256": manifest["microsandbox"]["firmware_sha256"],
            "runtime_home": str(root / "state/msb"), "image": manifest["runtime_ref"],
            "memory_mib": 4096, "cpus": 2, "root_disk_mib": 8192, "environment_disk_mib": 8192,
            "idle_seconds": 300, "retention_seconds": 86400,
            "max_active": 4, "max_retained": 16,
            "network": {"default_egress": "deny", "default_ingress": "deny", "rules": [
                {"action": "allow", "direction": "egress", "destination": "host", "protocol": "tcp", "port": str(state["core_port"])},
                {"action": "allow", "direction": "egress", "destination": "host", "protocol": "udp", "port": "53"},
                {"action": "allow", "direction": "egress", "destination": "host", "protocol": "tcp", "port": "53"},
                {"action": "allow", "direction": "egress", "destination": "public"},
            ]},
        })
    return result


def core_environment(root, state, database_password):
    native = state["provider"] == "microsandbox"
    config = str(Path(root) / "config") if native else "/config"
    database = f'127.0.0.1:{state["database_port"]}' if native else "database:5432"
    daemon_host = f'host.microsandbox.internal:{state["core_port"]}' if native else "core:8091"
    daemon_url = f"ws://{daemon_host}/api/v1/agent-daemon/ws"
    if state.get("public_url"):
        origin = urlsplit(state["public_url"])
        daemon_url = origin._replace(scheme="wss" if origin.scheme == "https" else "ws",
                                     path="/api/v1/agent-daemon/ws").geturl()
    result = {
        "AGENTS_API_DATABASE_URL": f"postgres://agents_api:{database_password}@{database}/agents_api?sslmode=disable",
        "AGENTS_API_CREDENTIAL_KEY_FILE": config + "/credential.key",
        "AGENTS_API_ADDR": f'127.0.0.1:{state["core_port"]}' if native else ":8091",
        "AGENTS_API_ENGINE": "codex", "AGENTS_API_HARNESSES": "codex,claude_sdk,mcode",
        "AGENTS_API_DAEMON_WS_URL": daemon_url,
        "AGENTS_API_E2B_PROVIDER_BIN": (str(Path(root) / "native/e2b/agents-api-e2b-provider")
                                         if native else "/opt/parsar/e2b/agents-api-e2b-provider"),
        "AGENTS_API_E2B_STATE_DIR": str(Path(root) / "state/e2b") if native else "/state/e2b",
    }
    result["AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE"] = (
        str(Path(root) / "admin/digests.json") if native else "/admin/digests.json")
    if not state["provider"]:
        result["AGENTS_API_SANDBOX_INSTALLATION_ID"] = state["installation_id"]
    if state["provider"]:
        result["AGENTS_API_MANAGED_RUNTIMES_FILE"] = config + "/managed-runtimes.json"
        result["AGENTS_API_SANDBOX_NODE_STATE_DIR"] = (
            str(Path(root) / "state/sandbox-node") if native else "/state/sandbox-node")
    return result


def compose_config(root, state, manifest, database_password):
    root = Path(root)
    config = root / "config"
    identity = f'{state["uid"]}:{state["gid"]}'
    doc = {"name": state["project"], "services": {}}
    services = doc["services"]
    native = state["provider"] == "microsandbox" and state["mode"] != "web-only"
    if state["mode"] != "web-only":
        services["database"] = {
            "image": manifest["images"]["database"], "restart": "unless-stopped",
            "environment": {"POSTGRES_USER": "agents_api", "POSTGRES_DB": "agents_api",
                            "POSTGRES_PASSWORD": database_password},
            "volumes": ["database:/var/lib/postgresql/data"],
            "healthcheck": {"test": ["CMD-SHELL", "pg_isready -U agents_api -d agents_api"],
                            "interval": "2s", "timeout": "5s", "retries": 30},
        }
        env = core_environment(root, state, database_password)
        shared = {"image": manifest["images"]["core"], "user": identity,
                  "environment": env, "volumes": [bind(config, "/config")],
                  "read_only": True, "tmpfs": ["/tmp:mode=1777"], "init": True,
                  "security_opt": ["no-new-privileges:true"]}
        services["migrate"] = dict(shared, command=["/usr/local/bin/agents-api-migrate"],
            environment={key: value for key, value in env.items()
                         if key != "AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE"},
            depends_on={"database": {"condition": "service_healthy"}})
        core = dict(shared, restart="unless-stopped", ports=[f'127.0.0.1:{state["core_port"]}:8091'],
                    depends_on={"migrate": {"condition": "service_completed_successfully"}})
        core["volumes"] = list(shared["volumes"])
        if native:
            services["database"]["ports"] = [f'127.0.0.1:{state["database_port"]}:5432']
            services.pop("migrate")
        else:
            core["volumes"].append(bind(root / "admin/digests.json", "/admin/digests.json"))
            core["volumes"].append(bind(root / "state/e2b", "/state/e2b", False))
            if state["provider"] == "docker":
                core["volumes"].append(bind("/var/run/docker.sock", "/var/run/docker.sock", False))
                core["volumes"].append(bind(root / "state/sandbox-node", "/state/sandbox-node", False))
                core["group_add"] = [str(state["device_gid"])]
                core["networks"] = ["default", "runtime"]
                doc["networks"] = {"runtime": {"name": state["project"] + "-runtime"}}
            services["core"] = core
        doc["volumes"] = {"database": {}}
    if state["mode"] != "core-only":
        services["web"] = {
            "image": manifest["images"]["web"], "user": identity, "restart": "unless-stopped",
            "ports": [f'127.0.0.1:{state["web_port"]}:8080'], "read_only": True,
            "security_opt": ["no-new-privileges:true"],
            "volumes": [bind(root / "admin/sandbox-admin.key", "/admin/sandbox-admin.key")],
            "environment": {"CORE_CONSOLE_ORIGIN": state.get("public_url") or f'http://127.0.0.1:{state["web_port"]}',
                "CORE_CONSOLE_UPSTREAM": (f'http://127.0.0.1:{state["core_port"]}' if native
                                          else state.get("core_url") or "http://core:8091"),
                "CORE_CONSOLE_ADMIN_TOKEN_FILE": "/admin/sandbox-admin.key"},
        }
        if state.get("console_auth") == "account":
            services["web"]["volumes"].extend([
                bind(root / "state/console", "/state/console", False),
            ])
            services["web"]["environment"].update(
                CORE_CONSOLE_AUTH_MODE="account",
                CORE_CONSOLE_STATE_DIR="/state/console",
            )
        else:
            services["web"]["volumes"].append(bind(config / "console.password", "/config/console.password"))
            services["web"]["environment"]["CORE_CONSOLE_PASSWORD_FILE"] = "/config/console.password"
        if state["mode"] == "all":
            services["web"]["volumes"].extend([
                bind(root / "node-payload", "/node-payload"),
            ])
            services["web"]["environment"].update(
                CORE_CONSOLE_NODE_PAYLOAD_DIR="/node-payload",
            )
        if state["mode"] == "web-only" or native:
            services["web"].pop("ports")
            services["web"]["network_mode"] = "host"
            services["web"]["environment"]["CORE_CONSOLE_ADDR"] = f'127.0.0.1:{state["web_port"]}'
    return doc
