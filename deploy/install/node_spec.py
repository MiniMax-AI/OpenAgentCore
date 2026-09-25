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


def digest(provider, specification):
    resources = specification["resources"]
    ordered = {"cpus": resources["cpus"], "memory_mib": resources["memory_mib"]}
    for field in ("root_disk_mib", "environment_disk_mib"):
        if resources.get(field):
            ordered[field] = resources[field]
    runtime = specification["runtime"]
    ordered_runtime = {key: runtime[key] for key in ("source_commit", "image_id", "image_manifest_digest",
                                                    "microsandbox_ref", "runtime_sha256", "firmware_sha256")}
    raw = json.dumps({"provider": provider, "resources": ordered, "runtime": ordered_runtime},
                     separators=(",", ":"), ensure_ascii=False).encode()
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
        resources = spec["resources"]
        if (not {"cpus", "memory_mib"}.issubset(resources)
                or set(resources) - {"cpus", "memory_mib", "root_disk_mib", "environment_disk_mib"}
                or any(type(value) is not int for value in resources.values())
                or not 1 <= resources["cpus"] <= 255 or not 512 <= resources["memory_mib"] <= 1048576):
            raise ValueError()
        for field in ("root_disk_mib", "environment_disk_mib"):
            if provider == "microsandbox":
                if not 1024 <= resources.get(field, 0) <= 4294967295:
                    raise ValueError()
            elif resources.get(field, 0) != 0:
                raise ValueError()
        runtime = spec["runtime"]
        patterns = {"source_commit": r"[0-9a-f]{40}", "image_id": r"sha256:[0-9a-f]{64}",
                    "image_manifest_digest": r"sha256:[0-9a-f]{64}",
                    "microsandbox_ref": r"parsar-core-runtime@sha256:[0-9a-f]{64}",
                    "runtime_sha256": r"[0-9a-f]{64}", "firmware_sha256": r"[0-9a-f]{64}"}
        if set(runtime) != set(patterns) or any(not isinstance(runtime[key], str) or not re.fullmatch(pattern, runtime[key])
                                               for key, pattern in patterns.items()):
            raise ValueError()
        if data["specification_digest"] != digest(provider, spec):
            raise ValueError()
    except (KeyError, ValueError, TypeError, AttributeError):
        raise SpecificationError("Core node configuration differs or is invalid; preserve retained state and inspect the deployment") from None
    return data


def fetch(args, token, retained, open_request, allow_enrollment=False):
    headers = {"Authorization": "Bearer " + token}
    if retained is not None:
        try:
            identity = retained["identity"]
            node_id = identity["node_id"]
            if (str(uuid.UUID(node_id)) != node_id or identity["installation_id"] != args.installation_id
                    or retained["core_url"] != args.core_url or not re.fullmatch(r"[0-9a-f]{64}", retained["credential"])):
                raise ValueError()
            headers = {"Authorization": "Bearer " + retained["credential"], "X-Parsar-Node-ID": node_id}
        except (KeyError, ValueError, TypeError, AttributeError):
            raise SpecificationError("Retained node identity differs or is invalid; preserve its state") from None
    elif not token:
        raise SpecificationError("A one-time enrollment credential is required for a new node")
    request = urllib.request.Request(args.core_url + "/api/v1/sandbox-node/configuration", headers=headers)
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
            raise SpecificationError("Core refused node configuration (HTTP 409): the deployment is in maintenance or conflicts with this node's retained specification; inspect the deployment before retrying") from None
        raise SpecificationError("Core rejected the node configuration read (HTTP " + str(error.code) + "); verify the retained or enrollment credential") from None
    except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead):
        raise SpecificationError("Cannot reach Core node configuration; verify the Core origin and that the reverse proxy routes /api/v1 to Core") from None
    except (ValueError, AttributeError):
        raise SpecificationError("Core returned invalid node configuration") from None
    if retained is not None:
        identity = retained["identity"]
        if (identity.get("provider") != data["provider"]
                or identity.get("specification_digest") != data["specification_digest"]
                or identity.get("deployment_generation") != data["generation"]):
            raise SpecificationError("Retained node specification differs from Core; preserve its state and follow the deployment change procedure")
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
