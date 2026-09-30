"""The installer's managed HTTPS gateway; Core and Web retain no Docker authority."""
import http.client
from contextlib import closing
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import stat
import subprocess
from urllib.parse import urlsplit


def enabled(config):
    return config.get("ingress") == "managed"


def hostname(value):
    if not isinstance(value, str):
        raise ValueError("Enter a DNS hostname, without a scheme, port or path")
    value = value.strip().lower()
    if (len(value) > 253 or "." not in value or
            not all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label) for label in value.split("."))):
        raise ValueError("Enter a DNS hostname, without a scheme, port or path")
    try:
        ipaddress.ip_address(value)
    except ValueError:
        if value.endswith((".localhost", ".local", ".internal")):
            raise ValueError("Use a publicly registered DNS hostname") from None
        return value
    raise ValueError("Use a DNS hostname rather than an IP address")


def preflight():
    endpoint = os.environ.get("DOCKER_HOST")
    if not endpoint:
        result = subprocess.run(["docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"],
                                check=True, capture_output=True, text=True)
        endpoint = result.stdout.strip()
    if not endpoint.startswith("unix://"):
        raise RuntimeError("Managed HTTPS requires a local Docker Unix socket; use --ingress external with a remote Docker engine")
    path = Path(endpoint.removeprefix("unix://")).resolve()
    info = path.stat()
    if not path.is_absolute() or not stat.S_ISSOCK(info.st_mode):
        raise RuntimeError("Managed HTTPS requires access to the local Docker socket")
    return {"docker_socket": str(path), "docker_gid": info.st_gid}


def prepare(root):
    if len(str(Path(root) / "ingress/admin/caddy.sock").encode()) >= 108:
        raise RuntimeError("Installation path is too long for managed HTTPS Unix sockets; choose a shorter --install-dir")
    for name in ("ingress", "ingress/api", "ingress/admin", "ingress/data"):
        path = Path(root) / name
        if path.is_symlink():
            raise RuntimeError("Managed HTTPS directories must not be symlinks")
        path.mkdir(mode=0o700, exist_ok=True)
        info = path.stat()
        if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise RuntimeError("Managed HTTPS directories must be private and owned by the installation account")


def caddyfile(config, state, candidate=None):
    origin = candidate or config["public_url"]
    bootstrap = "handle /healthz {\n reverse_proxy web:8080\n}\n"
    if config["public_url"]:
        bootstrap += f'handle {{\n redir {config["public_url"]}{{uri}} 308\n}}\n'
    else:
        bootstrap += "handle {\n reverse_proxy web:8080\n}\n"
    result = "{\n admin unix//control/caddy.sock\n persist_config off\n}\n\n"
    result += "http://:8080 {\n" + bootstrap + "}\n"
    for origin in dict.fromkeys(value for value in (config["public_url"], origin) if value):
        # config_model also applies these rules to manual config.json edits.
        parsed = urlsplit(origin)
        if origin != "https://" + hostname(parsed.hostname) or parsed.port is not None:
            raise ValueError("Managed HTTPS requires https:// followed by a DNS hostname, without a port")
        result += (f"{origin} {{\n"
                   ' handle /_oac/installation/verify {\n'
                   f'  respond "{state["installation_id"]}" 200\n'
                   ' }\n'
                   " @api path /v1 /v1/* /api/v1 /api/v1/*\n"
                   " handle @api {\n  reverse_proxy core:8091\n }\n"
                   " handle {\n  reverse_proxy web:8080\n }\n}\n")
    return result


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path, timeout=15):
        super().__init__("localhost", timeout=timeout)
        self.path = str(path)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


def reload(root, document):
    try:
        with closing(UnixHTTP(Path(root) / "ingress/admin/caddy.sock")) as client:
            client.request("POST", "/load", document.encode(), {"Content-Type": "text/caddyfile"})
            response = client.getresponse()
            response.read(65536)
            if response.status != 200:
                raise RuntimeError("HTTPS gateway refused the configuration; inspect gateway logs and retry")
    except (OSError, http.client.HTTPException):
        raise RuntimeError("HTTPS gateway is unavailable; inspect gateway logs and retry") from None


def published(config):
    """(host port, gateway port) of each port the gateway publishes on config["host"]."""
    return [(config["ports"]["web"], 8080), (80, 80), (443, 443)]


def services(root, config, state, bind):
    root = Path(root)
    identity = f'{state["uid"]}:{state["gid"]}'
    common = {"image": state["images"]["ingress"], "user": identity, "restart": "unless-stopped",
              "read_only": True, "tmpfs": ["/tmp"], "security_opt": ["no-new-privileges:true"]}
    host = config["host"]
    if ":" in host:
        host = "[" + host + "]"
    gateway = dict(common, command=["caddy", "run", "--config", "/generated/Caddyfile", "--adapter", "caddyfile"],
                   ports=[f"{host}:{port}:{target}" for port, target in published(config)],
                   environment={"XDG_DATA_HOME": "/data", "XDG_CONFIG_HOME": "/data/config",
                                "NO_PROXY": "core,web,localhost,127.0.0.1,::1",
                                "no_proxy": "core,web,localhost,127.0.0.1,::1"},
                   volumes=[bind(root / "generated", "/generated"), bind(root / "ingress/admin", "/control", False),
                            bind(root / "ingress/data", "/data", False)])
    manager = dict(common, command=["python3", str(root / "oac").replace("$", "$$"), "domain-server"], network_mode="host",
                   environment={"DOCKER_HOST": "unix:///docker.sock"}, group_add=[str(state["ingress"]["docker_gid"])],
                   volumes=[bind(root, str(root).replace("$", "$$"), False), bind(root / "ingress/api", "/control", False),
                            bind(state["ingress"]["docker_socket"], "/docker.sock", False)])
    for service, path in ((gateway, "/control/caddy.sock"), (manager, "/control/api.sock")):
        service["healthcheck"] = {
            "test": ["CMD", "python3", "-c", "import socket; s=socket.socket(socket.AF_UNIX); s.connect(" + repr(path) + "); s.close()"],
            "interval": "2s", "timeout": "5s", "retries": 30,
        }
    return {"gateway": gateway, "installation": manager}


def console_origin(config):
    """A useful initial URL; NAT users may substitute their reachable server IP."""
    if config["public_url"]:
        return config["public_url"]
    host = config["host"]
    address = ipaddress.ip_address(host)
    if address.is_unspecified:
        try:
            family = socket.AF_INET6 if address.version == 6 else socket.AF_INET
            destination = "2001:db8::1" if address.version == 6 else "192.0.2.1"
            with socket.socket(family, socket.SOCK_DGRAM) as probe:
                probe.connect((destination, 80))  # Route lookup only; no packet is sent.
                host = probe.getsockname()[0]
        except OSError:
            return f'http://<server-ip>:{config["ports"]["web"]}'
    if ":" in host:
        host = "[" + host + "]"
    return f'http://{host}:{config["ports"]["web"]}'
