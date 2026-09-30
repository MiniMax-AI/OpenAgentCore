"""Domain setup through one locked installer operation, from Web or the CLI."""
import argparse
import hmac
import http.client
from contextlib import closing
from http.server import BaseHTTPRequestHandler
import json
from pathlib import Path
import socket
import socketserver
import ssl
import threading
import time
from urllib.parse import urlsplit

import config_model
import configuration
import ingress_config as gateway
import oac_cli


FAILED = "Domain setup failed. Check gateway and installation logs, then retry."
UNFINISHED = ("checking", "applying")


class DomainError(oac_cli.OacError):
    def __init__(self, code, message, status=400):
        super().__init__(message)
        self.code, self.status = code, status


def save(root, value):
    oac_cli.write_private(Path(root) / "ingress/status.json", json.dumps(value) + "\n")


def unfinished(root):
    """Whether status.json records a domain setup that has not finished."""
    path = Path(root) / "ingress/status.json"
    return path.exists() and json.loads(oac_cli.read_private(path, "domain setup status"))["state"] in UNFINISHED


def status(root):
    config = oac_cli.load_config(root)
    supported = gateway.enabled(config)
    written = oac_cli.written_view(root, oac_cli.load_state(root), config)
    result = {"supported": supported, "state": "ready" if written["public_url"] else "unconfigured",
              "public_url": written["public_url"], "target_url": None, "message": None}
    if not supported:
        result["message"] = "This installation uses an external reverse proxy. Configure HTTPS there, then set public_url and run oac apply."
    else:
        path = Path(root) / "ingress/status.json"
        if path.exists():
            result.update(json.loads(oac_cli.read_private(path, "domain setup status")))
            if result["state"] in UNFINISHED:
                try:
                    with oac_cli.locked(Path(root)):
                        # A live CLI or Web operation holds this same lock. Read
                        # again after acquiring it in case the operation just finished.
                        result.update(json.loads(oac_cli.read_private(path, "domain setup status")))
                        if result["state"] in UNFINISHED:
                            result.update(state="failed", message="Domain setup was interrupted. Retry the same hostname, or run oac apply on the server: it restores the applied address, or finishes the switch if it had started.")
                except oac_cli.OacError:
                    pass
    return result


def prepare(root, name, confirmation):
    """Validate and reserve a job while the caller holds the installation lock."""
    config, state = oac_cli.load_config(root), oac_cli.load_state(root)
    if state.get("complete") is not True:
        raise DomainError("installation_incomplete", oac_cli.INCOMPLETE, 409)
    if not gateway.enabled(config):
        raise DomainError("domain_setup_unavailable", "Managed HTTPS is available in combined Docker installations only")
    try:
        target = "https://" + gateway.hostname(name)
    except ValueError as error:
        raise DomainError("invalid_hostname", str(error)) from None
    if confirmation is not None and confirmation != target:
        raise DomainError("invalid_confirmation", "The confirmation must equal the new HTTPS URL")
    # Do not apply unrelated pending operator edits from a Web domain action.
    previous, _ = oac_cli.disk_view(oac_cli.read_generated(root, {"settings.json", "runtime-history.json"}))
    if previous is None or any(previous.get(key) != value for key, value in config_model.values(config).items()
                               if key != "public_url"):
        raise DomainError("configuration_pending", "Apply or revert pending config.json changes before changing the domain", 409)
    # Both desired and generated files may be ahead of the running services.
    # Common apply/start records this address only after successful convergence.
    receipt = Path(root) / "ingress/status.json"
    if not receipt.exists():
        raise DomainError("installation_not_ready", "Run oac apply before configuring the domain", 409)
    applied = json.loads(oac_cli.read_private(receipt, "domain setup status"))
    config = dict(config, public_url=applied["public_url"])
    candidate = dict(config, public_url=target)
    config_model.validate(candidate)
    actual = oac_cli.observe(state)
    if not all(actual.get(name, {}).get("running") for name in ("core", "web", "gateway", "installation")):
        raise DomainError("installation_not_running", "Start the installation with oac start before configuring its domain", 409)
    rendered, disk, old_settings = oac_cli.render_now(root, config, state)
    if oac_cli.edited_files(state, disk, rendered):
        raise DomainError("generated_files_edited", "Resolve hand-edited generated files with oac apply before configuring the domain", 409)
    host = urlsplit(target).hostname
    if not resolves(host):
        raise DomainError("hostname_unresolved", f"{host} does not resolve. Add A/AAAA records for it that point at this server, then retry when DNS returns them.", 409)
    # The gateway publishes 80 and 443 only for HTTPS; the ports it already publishes are its own.
    busy = [str(listener.port) for listener in oac_cli.taken_listeners(candidate, oac_cli.own_listeners(config, old_settings, disk, actual))]
    if busy:
        named = f"Port {busy[0]} is" if len(busy) == 1 else "Ports " + " and ".join(busy) + " are"
        raise DomainError("https_ports_unavailable", f"{named} already in use on this server. Automatic HTTPS cannot run beside another program on ports 80 and 443: free both and retry. Find the program with: sudo ss -ltnp '( sport = :80 or sport = :443 )'", 409)
    _, base, answered = oac_cli.old_public_url(root, config, old_settings, disk, actual)
    args = argparse.Namespace(dry_run=False, yes=False, confirm_public_url_change=confirmation)
    try:
        # execute returns Core to the applied address first, even after an interrupted switch,
        # so the change is confirmed from there.
        oac_cli.confirm_public_url(root, candidate, configuration.local_public_url(config), base, answered, args, False, lambda _: None)
    except oac_cli.OacError:
        raise DomainError("public_url_confirmation_required", "Changing this address can disconnect existing nodes and executors. Confirm the new URL to continue; retain the old route while existing work uses it.", 409) from None
    # The final apply makes the change confirmed here.
    args = argparse.Namespace(dry_run=False, yes=False, confirm_public_url_change=target)
    job = {"state": "checking", "target_url": target, "public_url": config["public_url"], "message": None}
    save(root, job)
    return config, state, candidate, args, job


def resolves(host):
    """Whether DNS answers with an A or AAAA record."""
    try:
        # The trailing dot keeps the resolver from appending its search domains.
        return bool(socket.getaddrinfo(host + ".", 443, type=socket.SOCK_STREAM))
    except (OSError, UnicodeError):
        return False


def verify(target, installation_id, timeout=180):
    """A trusted certificate and this gateway's identity must both answer at the public address."""
    host = urlsplit(target).hostname
    deadline = time.monotonic() + timeout
    while True:
        try:
            # No redirects, ambient proxy or credentials are used for this probe.
            with closing(http.client.HTTPSConnection(host, timeout=8, context=ssl.create_default_context())) as client:
                client.request("GET", "/_oac/installation/verify")
                response = client.getresponse()
                body = response.read(256)
                if response.status == 200 and body == installation_id.encode():
                    return
        except (OSError, http.client.HTTPException):
            pass
        if time.monotonic() >= deadline:
            raise DomainError("https_not_ready", f"HTTPS verification failed: {host} did not reach this installation with a trusted certificate. Check that its A/AAAA records point at this server, that no firewall or NAT blocks inbound ports 80 and 443, and the gateway logs for certificate errors. The previous address is kept; retry after the fix.")
        time.sleep(2)


def execute(root, prepared):
    config, state, candidate, args, job = prepared
    # Core and Web keep the applied address until the candidate is verified.
    keep = argparse.Namespace(dry_run=False, yes=False, confirm_public_url_change=configuration.local_public_url(config))
    try:
        # Through common apply, the gateway publishes 80 and 443 and serves the candidate.
        oac_cli.write_private(Path(root) / "config.json", json.dumps(config, indent=2) + "\n")
        try:
            oac_cli._apply(Path(root), keep, False, False, False, lambda _: None, candidate=candidate["public_url"])
        except oac_cli.OacError:
            raise DomainError("domain_setup_failed", FAILED) from None  # Its message addresses oac apply users.
        verify(candidate["public_url"], state["installation_id"])
        job["state"] = "applying"
        save(root, job)
        # Persist desired inputs before apply. An interrupted operation can be retried
        # with the same hostname, or completed by the ordinary oac apply command.
        oac_cli.write_private(Path(root) / "config.json", json.dumps(candidate, indent=2) + "\n")
        oac_cli._apply(Path(root), args, False, False, False, lambda _: None)
        job.update(state="ready", public_url=candidate["public_url"], message=None)
        save(root, job)
    except Exception as error:
        recovery_failed = False
        try:
            # Restore through common apply: the gateway without the candidate, and without
            # 80 and 443 unless HTTPS was already on, and any partially applied services.
            oac_cli.write_private(Path(root) / "config.json", json.dumps(config, indent=2) + "\n")
            oac_cli._apply(Path(root), keep, False, False, False, lambda _: None)
            gateway.reload(root, gateway.caddyfile(config, state))
        except Exception:
            recovery_failed = True
        message = str(error) if isinstance(error, (DomainError, oac_cli.OacError)) else FAILED
        if recovery_failed:
            message += " Recovery was incomplete; run oac apply and oac status on the server."
        job.update(state="failed", message=message)
        save(root, job)
        raise DomainError("domain_setup_failed", message) from None


def configure(root, name, confirmation=None, out=print):
    with oac_cli.locked(root):
        prepared = prepare(root, name, confirmation)
        out("Requesting and verifying HTTPS. This can take a few minutes.")
        execute(root, prepared)
    out("HTTPS ready: " + prepared[2]["public_url"] + ". Sign in again with the Core key.")


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # Headers and requests can carry credentials.

    def reply(self, code, value):
        payload = json.dumps(value).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def authorized(self):
        expected = "Bearer " + configuration.read_core_key(self.server.root)
        if len(self.headers.get_all("Authorization", [])) != 1 or not hmac.compare_digest(self.headers.get("Authorization", ""), expected):
            self.reply(401, {"error": {"code": "unauthorized", "message": "Installation authentication failed"}})
            return False
        return True

    def do_GET(self):
        if not self.authorized():
            return
        if self.path != "/domain":
            self.send_error(404)
            return
        self.reply(200, status(self.server.root))

    def do_POST(self):
        if not self.authorized():
            return
        if self.path != "/domain":
            self.send_error(404)
            return
        guard = None
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 2048 or self.headers.get("Transfer-Encoding"):
                raise DomainError("invalid_request", "A small JSON request body is required")
            data = json.loads(self.rfile.read(length))
            if not isinstance(data, dict) or set(data) - {"hostname", "confirm_public_url_change"}:
                raise DomainError("invalid_request", "Expected hostname and optional confirm_public_url_change")
            guard = oac_cli.locked(self.server.root)
            guard.__enter__()
            prepared = prepare(self.server.root, data.get("hostname"), data.get("confirm_public_url_change"))
            held, guard = guard, None
            # Start independently of the response writer: a browser disconnect must
            # not leak the installation lock or abandon a reserved operation.
            def work():
                try:
                    execute(self.server.root, prepared)
                except DomainError:
                    pass
                finally:
                    held.__exit__(None, None, None)
            threading.Thread(target=work, daemon=True).start()
            self.reply(202, dict(prepared[-1], supported=True))
        except (DomainError, oac_cli.OacError, ValueError) as error:
            code = error.code if isinstance(error, DomainError) else "installation_busy" if isinstance(error, oac_cli.OacError) else "invalid_request"
            self.reply(error.status if isinstance(error, DomainError) else 409 if isinstance(error, oac_cli.OacError) else 400,
                       {"error": {"code": code, "message": str(error) if isinstance(error, oac_cli.OacError) else "Invalid JSON request"}})
        finally:
            if guard is not None:
                guard.__exit__(None, None, None)

    def setup(self):
        self.request.settimeout(15)
        super().setup()


def serve(root):
    root = Path(root)
    path = Path("/control/api.sock")
    path.unlink(missing_ok=True)
    import os
    previous = os.umask(0o077)
    try:
        with Server(str(path), Handler) as server:
            server.root = root
            os.chmod(path, 0o600)
            server.serve_forever()
    finally:
        os.umask(previous)
