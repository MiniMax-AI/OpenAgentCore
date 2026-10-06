"""Read the Core-owned node specification and verify its matched release."""
import hashlib
import http.client
import json
import re
import urllib.error
import urllib.request
import uuid


class SpecificationError(Exception):
    pass


PUBLIC_URL_CHANGED = ("Core's public URL changed after this command was generated. Generate a new command on the "
                      "Nodes page and run it on this host.")


def release(manifest):
    return {"source_commit": manifest["source_commit"],
            "image_id": manifest["images"]["runtime"],
            "image_manifest_digest": manifest["image_manifest_digests"]["runtime"],
            "microsandbox_ref": manifest["runtime_ref"],
            "runtime_sha256": manifest["microsandbox"]["runtime_sha256"],
            "firmware_sha256": manifest["microsandbox"]["firmware_sha256"]}


# BEGIN GENERATED DEPLOYMENT CONTRACT
# Generated from sandbox/deployment_contract.go; do not edit.
_CONTRACT = json.loads("{\"resources\":[{\"name\":\"cpus\",\"min\":1,\"max\":255,\"omit_zero\":false},{\"name\":\"memory_mib\",\"min\":512,\"max\":1048576,\"omit_zero\":false},{\"name\":\"root_disk_mib\",\"min\":0,\"max\":4294967295,\"omit_zero\":true},{\"name\":\"environment_disk_mib\",\"min\":0,\"max\":4294967295,\"omit_zero\":true}],\"runtime\":[{\"name\":\"source_commit\",\"pattern\":\"[0-9a-f]{40}\"},{\"name\":\"image_id\",\"pattern\":\"sha256:[0-9a-f]{64}\"},{\"name\":\"image_manifest_digest\",\"pattern\":\"sha256:[0-9a-f]{64}\"},{\"name\":\"microsandbox_ref\",\"pattern\":\"oac-runtime@sha256:[0-9a-f]{64}\"},{\"name\":\"runtime_sha256\",\"pattern\":\"[0-9a-f]{64}\"},{\"name\":\"firmware_sha256\",\"pattern\":\"[0-9a-f]{64}\"}],\"providers\":{\"docker\":{\"disk\":false,\"runtime\":true},\"e2b\":{\"disk\":false,\"runtime\":false},\"microsandbox\":{\"disk\":true,\"runtime\":true}},\"minimum_disk\":1024}")
# END GENERATED DEPLOYMENT CONTRACT


def canonical_spec(provider, specification, validate=True):
    rules = _CONTRACT["providers"][provider]
    if validate and (not isinstance(specification, dict) or set(specification) != ({"resources", "runtime"} if rules["runtime"] else {"resources"})):
        raise ValueError("Invalid specification fields")
    resources = specification["resources"]
    if validate and (not isinstance(resources, dict) or set(resources) - {rule["name"] for rule in _CONTRACT["resources"]}):
        raise ValueError("Invalid resource fields")
    ordered = {}
    for rule in _CONTRACT["resources"]:
        name = rule["name"]
        value = resources.get(name, 0) if rule["omit_zero"] else resources[name]
        minimum, maximum = rule["min"], rule["max"]
        if rule["omit_zero"]:
            minimum, maximum = (_CONTRACT["minimum_disk"], maximum) if rules["disk"] else (0, 0)
        if validate and (type(value) is not int or not minimum <= value <= maximum):
            raise ValueError("Invalid resource value")
        if value or not rule["omit_zero"]:
            ordered[name] = value
    result = {"provider": provider, "resources": ordered}
    if rules["runtime"]:
        runtime = specification["runtime"]
        if validate and (not isinstance(runtime, dict) or set(runtime) != {rule["name"] for rule in _CONTRACT["runtime"]}):
            raise ValueError("Invalid release fields")
        ordered_runtime = {}
        for rule in _CONTRACT["runtime"]:
            value = runtime[rule["name"]]
            if validate and (not isinstance(value, str) or not re.fullmatch(rule["pattern"], value)):
                raise ValueError("Invalid release identity")
            ordered_runtime[rule["name"]] = value
        result["runtime"] = ordered_runtime
    return result


def digest(provider, specification):
    raw = json.dumps(canonical_spec(provider, specification, validate=False), separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(raw).hexdigest()


def validate(data, args):
    if isinstance(data, dict) and isinstance(data.get("core_url"), str) and data["core_url"] != args.core_url:
        raise SpecificationError(PUBLIC_URL_CHANGED)
    try:
        provider, spec = data["provider"], data["specification"]
        if (provider not in ("docker", "microsandbox") or data["installation_id"] != args.installation_id
                or data["core_url"] != args.core_url or type(data["generation"]) is not int or data["generation"] < 1
                or getattr(args, "provider", None) not in (None, provider)
                or set(spec) != {"resources", "runtime"}
                or type(data["max_active"]) is not int or type(data["max_retained"]) is not int
                or not 1 <= data["max_active"] <= data["max_retained"] <= 1000000):
            raise ValueError()
        canonical_spec(provider, spec)
        if data["specification_digest"] != digest(provider, spec):
            raise ValueError()
    except (KeyError, ValueError, TypeError, AttributeError):
        raise SpecificationError("Core node configuration differs or is invalid; preserve retained state and inspect the deployment") from None
    return data


def fetch(args, token, retained, open_request, allow_enrollment=False, generation=None, allow_selection_change=False):
    headers = {"Authorization": "Bearer " + token}
    if retained is not None:
        try:
            identity = retained["identity"]
            node_id = identity["node_id"]
            if (str(uuid.UUID(node_id)) != node_id or identity["installation_id"] != args.installation_id
                    or retained["core_url"] != args.core_url or not re.fullmatch(r"[0-9a-f]{64}", retained["credential"])):
                raise ValueError()
            headers = {"Authorization": "Bearer " + retained["credential"], "X-OAC-Node-ID": node_id}
        except (KeyError, ValueError, TypeError, AttributeError):
            raise SpecificationError("Retained node identity differs or is invalid; preserve its state") from None
    elif not token:
        raise SpecificationError("A new node needs its one-time enrollment token on standard input (--enrollment-token-stdin); copy the command from Add node")
    query = "?generation=" + str(generation) if generation is not None else ""
    request = urllib.request.Request(args.core_url + "/api/v1/sandbox-node/configuration" + query, headers=headers)
    try:
        try:
            response = open_request(request)
        except urllib.error.HTTPError as error:
            if error.code != 401 or retained is None or not token or not allow_enrollment:
                raise
            # Identity is persisted before enrollment. A failed first registration
            # may therefore have no durable credential on Core yet.
            request = urllib.request.Request(args.core_url + "/api/v1/sandbox-node/configuration",
                                             headers={"Authorization": "Bearer " + token})
            response = open_request(request)
        with response:
            raw = response.read(16385)
        if len(raw) > 16384:
            raise ValueError()
        data = validate(json.loads(raw), args)
    except urllib.error.HTTPError as error:
        if error.code == 401 and retained is not None and not allow_enrollment:
            raise SpecificationError("Core no longer accepts this node: it was removed on the Nodes page, or a sandbox "
                                     "deployment change retired it. Uninstall it with node-install.pyz --uninstall "
                                     "--installation-id " + args.installation_id + ", then add the host with a new command.") from None
        if error.code == 404:
            raise SpecificationError("Core node configuration was not found (HTTP 404); route /api/v1 on the Core origin directly to Core, not to Web") from None
        if error.code == 409:
            raise SpecificationError("Core refused node configuration (HTTP 409): the deployment is resetting or conflicts with this node's retained specification; inspect the deployment before retrying") from None
        raise SpecificationError("Core rejected the node configuration read (HTTP " + str(error.code) + "); verify the retained or enrollment credential") from None
    except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead):
        raise SpecificationError("Cannot reach Core node configuration; verify the Core origin and that the reverse proxy routes /api/v1 to Core") from None
    except (ValueError, AttributeError):
        raise SpecificationError("Core returned invalid node configuration") from None
    if retained is not None:
        identity = retained["identity"]
        if (identity.get("provider") != data["provider"]
                or not allow_selection_change and (identity.get("specification_digest") != data["specification_digest"]
                or identity.get("deployment_generation") != data["generation"])):
            raise SpecificationError("Retained node specification differs from Core; preserve its state and follow the deployment change procedure")
    if generation is not None and data["generation"] != generation:
        raise SpecificationError("Core returned a different generation")
    return data


def verify_release(configuration, manifest):
    if configuration["specification"]["runtime"] != release(manifest):
        raise SpecificationError("Core Runtime release differs from this distribution; use the matched installation artifacts")


def verify_provider(stored, configuration, runtime_image):
    spec, provider = configuration["specification"], configuration["provider"]
    if (stored.get("specification") != spec or stored.get("provider") != provider
            or stored.get("installation_id") != configuration["installation_id"]
            or stored.get("generation") != configuration["generation"]
            or stored.get("core_url") != configuration["core_url"] + "/api/v1"):
        raise SpecificationError("Retained node configuration differs from Core; preserve its state")
    if provider == "docker":
        if stored.get("docker", {}).get("image") != runtime_image:
            raise SpecificationError("Retained Docker image differs; preserve the node and inspect its configuration")
    else:
        micro = stored.get("microsandbox", {})
        if any(key in micro for key in ("max_active", "max_retained", "idle_seconds", "retention_seconds")):
            raise SpecificationError("Node capacity and lifecycle policy belong to Core; regenerate the stale provider file")
        expected = dict(spec["resources"], image=spec["runtime"]["microsandbox_ref"],
                        runtime_sha256=spec["runtime"]["runtime_sha256"], firmware_sha256=spec["runtime"]["firmware_sha256"])
        if any(micro.get(key) != value for key, value in expected.items()):
            raise SpecificationError("Retained microsandbox configuration differs from Core; preserve its state")
