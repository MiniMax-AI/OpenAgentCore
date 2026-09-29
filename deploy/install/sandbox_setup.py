"""Choose a new installation's sandbox backend through Core's administrator API.

The installer applies what Web's setup would: Docker and microsandbox get the
Standard size from the bundled copy of Web's standard-sizes.json and the bundle's
Runtime release; E2B gets the account key and template build, and Core adopts the
build's size. Nodes are added afterwards from Web, this host included.
"""
import json
import http.client
import re
import urllib.error
import urllib.request
import uuid

import configuration
import node_spec

CHOICES = ("docker", "microsandbox", "e2b", "none")
NAMES = {"docker": "Docker", "microsandbox": "microsandbox", "e2b": "E2B"}
# The fields of each provider's size in Web's standard-sizes.json (standard-sizes.md).
SIZE_FIELDS = {"docker": {"cpus", "memory_mib"},
               "microsandbox": {"cpus", "memory_mib", "root_disk_mib", "environment_disk_mib"}}


class SandboxSetupError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        raise SandboxSetupError("Core redirected the sandbox setup request")


def send(req):
    """(status, body) of one request to Core: no redirects, no ambient proxy."""
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        # E2B validation reads the template build from E2B before Core answers.
        with opener.open(req, timeout=60) as response:
            return response.status, response.read(16385)
    except urllib.error.HTTPError as error:
        return error.code, error.read(16385)


def refusal(status, raw, secret=None):
    """Core's own error message, printable, bounded and without the E2B key."""
    try:
        message = json.loads(raw)["error"]["message"]
    except (ValueError, KeyError, TypeError):
        message = None
    if not isinstance(message, str) or not message.strip():
        return f"Core refused the sandbox setup (HTTP {status})"
    if secret:
        message = message.replace(secret, "[E2B API key]")
    return "Core refused the sandbox setup: " + "".join(c if c.isprintable() else " " for c in message.strip())[:500]


def request(core, token, method, path, value=None):
    data = json.dumps(value).encode() if value is not None else None
    headers = {"Authorization": "Bearer " + token, "Content-Type": "application/json"}
    try:
        status, raw = send(urllib.request.Request(core + "/core/v1/sandbox/" + path, data=data,
                                                  headers=headers, method=method))
    except (urllib.error.URLError, OSError, http.client.HTTPException, ValueError):
        raise SandboxSetupError("Core did not confirm the sandbox setup") from None
    if not 200 <= status < 300:
        raise SandboxSetupError(refusal(status, raw, ((value or {}).get("e2b") or {}).get("api_key")))
    try:
        if len(raw) > 16384:
            raise ValueError()
        result = json.loads(raw)
        if not isinstance(result, dict):
            raise ValueError()
        return result
    except ValueError:
        raise SandboxSetupError("Core did not confirm the sandbox setup") from None


def e2b_template(value):
    """`template-id:build-uuid`, as Core accepts it."""
    template, _, build = value.partition(":")
    try:
        exact = uuid.UUID(build)
    except ValueError:
        exact = None
    return (re.fullmatch(r"[A-Za-z0-9_-]{1,128}", template) is not None and exact is not None
            and exact.int != 0 and str(exact) == build)


def selection(bundle, manifest, choice, e2b=None):
    """The request Web's setup sends for this backend."""
    if choice == "e2b":
        return {"provider": "e2b", "e2b": e2b}
    try:
        resources = json.loads((bundle / "standard-sizes.json").read_text())[choice]
    except (OSError, ValueError, KeyError, TypeError):
        resources = None
    if (not isinstance(resources, dict) or set(resources) != SIZE_FIELDS[choice]
            or not all(type(value) is int for value in resources.values())):
        raise SandboxSetupError("The bundle's standard-sizes.json is invalid")
    return {"provider": choice, "resources": resources, "runtime": node_spec.release(manifest)}


def initialize(root, config, state, request_body):
    """Save the selection unless Core already has one; returns Core's deployment."""
    core = configuration.service_origin(config, "core")
    key = (root / "secrets/core.key").read_text().strip()
    current = request(core, key, "GET", "deployment")
    if current.get("installation_id") != state["installation_id"]:
        raise SandboxSetupError("Core reports a different installation")
    provider = current.get("provider")
    if provider == request_body["provider"]:
        return current
    if provider:
        raise SandboxSetupError(f"Core already uses {NAMES.get(provider, provider)} sandboxes")
    generation = current.get("generation")
    if type(generation) is not int or generation < 0:
        raise SandboxSetupError("Core returned an invalid deployment generation")
    if current.get("reset") is not None:
        raise SandboxSetupError("Core is resetting its sandbox deployment")
    return request(core, key, "POST", "deployment", dict(request_body, expected_generation=generation))
