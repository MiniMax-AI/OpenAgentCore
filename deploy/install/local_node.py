"""Install an explicitly requested local node through the ordinary admin API."""
import json
import http.client
import os
from pathlib import Path
import sys
import urllib.error
import urllib.request

import node_spec


class LocalNodeError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        raise LocalNodeError("Local node setup cannot follow redirects")


def request(core, token, method, path, value=None):
    data = json.dumps(value).encode() if value is not None else None
    headers = {"Authorization": "Bearer " + token, "Content-Type": "application/json"}
    req = urllib.request.Request(core + "/core/v1/sandbox/" + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.build_opener(NoRedirect()).open(req, timeout=30) as response:
            raw = response.read(16385)
        if len(raw) > 16384:
            raise ValueError()
        result = json.loads(raw)
        if not isinstance(result, dict):
            raise ValueError()
        return result
    except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead, ValueError):
        raise LocalNodeError("Local node setup was not confirmed; preserve installation state and inspect Core before rerunning") from None


def install(root, state, manifest, bundle, run):
    core = f'http://127.0.0.1:{state["core_port"]}'
    admin = (root / "secrets/core.key").read_text().strip()
    current = request(core, admin, "GET", "deployment")
    if current.get("installation_id") != state["installation_id"]:
        raise LocalNodeError("Core deployment identity differs; preserve its installation state")
    if not current.get("provider"):
        # The same initial size that Web's sandbox setup proposes for this provider.
        resources = {"cpus": 2, "memory_mib": 2048}
        if state["provider"] == "microsandbox":
            resources.update(memory_mib=4096, root_disk_mib=8192, environment_disk_mib=8192)
        current = request(core, admin, "POST", "deployment", {
            "provider": state["provider"], "resources": resources, "runtime": node_spec.release(manifest)})
    if current.get("provider") != state["provider"] or current.get("core_url") != state["public_url"]:
        raise LocalNodeError("Existing deployment selection differs; change it through maintenance instead of reinstalling")
    node_root = Path.home() / ".parsar/nodes" / state["installation_id"]
    # A completed registration needs only the retained credential. An interrupted
    # registration may use a new token; the node first tries its original identity.
    token = ""
    if not (node_root / "registered.json").exists():
        enrollment = request(core, admin, "POST", "enrollment-tokens", {})
        token = enrollment.get("token", "")
        if not isinstance(token, str) or not token or any(c.isspace() for c in token):
            raise LocalNodeError("Core returned invalid node enrollment data")
    environment = dict(os.environ)
    environment.pop("PARSAR_NODE_ENROLLMENT_TOKEN", None)
    if token:
        environment["PARSAR_NODE_ENROLLMENT_TOKEN"] = token
    run([sys.executable, str(bundle / "node-install.pyz"), "--bundle", str(bundle),
         "--core-url", state["public_url"], "--installation-id", state["installation_id"],
         "--provider", state["provider"]], env=environment)
