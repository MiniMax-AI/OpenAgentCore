"""Pinned SDK/raw HTTP OAuth resource verification, with no provider requests.

The caller supplies an isolated Core/PostgreSQL service, strict official clients
for an owner, foreign tenant and same-tenant peer, and an HTTP transport. This
verifier creates and deletes only its own Vaults. External authorization, refresh
execution and provider revocation need separate real-provider acceptance.
"""

from copy import deepcopy
import importlib.metadata
import json
from pathlib import Path
import uuid


def verify_oauth_credentials(client, other, peer, raw, *, evidence_path=None):
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source["vcs_info"]["commit_id"] == pin["commit"]
    assert all(api._strict_response_validation for api in (client, other, peer))
    credentials = client.beta.agents.vaults.credentials
    base = str(client.base_url).rstrip("/")
    prefix = "oauth-resource-" + uuid.uuid4().hex
    secrets = [prefix + suffix for suffix in ("-access", "-refresh", "-client", "-replacement")]
    proof = {"sdk": pin, "variants": [], "checks": [], "passed": False}
    owned = []
    expiry = "2030-01-02T03:04:05.123Z"
    destination = "https://mcp.example.invalid/tools"

    def headers(api=client):
        return {"Authorization": "Bearer " + api.api_key, "OpenAI-Beta": "agents=v1"}

    def safe(response, status=200):
        assert response.status_code == status, "Unexpected OAuth resource HTTP status"
        assert not any(secret in response.text for secret in secrets), "OAuth secret appeared in a public response"
        assert response.headers["cache-control"] == "no-store"
        return response.json()

    def endpoint(vault, credential=None):
        return base + "/vaults/" + vault + "/credentials" + ("/" + credential if credential else "")

    def check_resource(body, vault, expected_auth):
        assert set(body) == {"id", "vault_id", "name", "object", "auth", "created_at", "updated_at"}
        assert body["object"] == "vault.credential" and body["vault_id"] == vault
        assert uuid.UUID(body["id"]).int != 0
        assert type(body["created_at"]) is int and type(body["updated_at"]) is int
        assert body["auth"] == expected_auth, "OAuth safe auth metadata or nullable fields changed"
        return body

    def retrieve(vault, credential, expected_auth):
        response = credentials.with_raw_response.retrieve(credential, vault_id=vault)
        body = check_resource(safe(response.http_response), vault, expected_auth)
        assert response.parse().to_dict() == body
        assert safe(raw.get(endpoint(vault, credential), headers=headers())) == body
        assert peer.beta.agents.vaults.credentials.retrieve(credential, vault_id=vault).to_dict() == body
        return body

    def update(vault, previous, patch, expected_auth, sdk=True):
        if sdk:
            response = credentials.with_raw_response.update(previous["id"], vault_id=vault, auth={"type": "mcp_oauth", **patch})
            body = safe(response.http_response)
            assert response.parse().to_dict() == body
        else:
            body = safe(raw.post(endpoint(vault, previous["id"]), headers=headers(), json={"auth": {"type": "mcp_oauth", **patch}}))
        check_resource(body, vault, expected_auth)
        assert {k: v for k, v in body.items() if k not in ("auth", "updated_at")} == {
            k: v for k, v in previous.items() if k not in ("auth", "updated_at")}
        assert body["updated_at"] >= previous["updated_at"]
        assert retrieve(vault, body["id"], expected_auth) == body
        return body

    try:
        vault = client.beta.agents.vaults.create(name=prefix)
        owned.append((client, vault.id))
        wrong = client.beta.agents.vaults.create(name=prefix + "-wrong")
        owned.append((client, wrong.id))
        foreign = other.beta.agents.vaults.create(name=prefix + "-foreign")
        owned.append((other, foreign.id))
        expected = {}
        for index, method in enumerate((None, "none", "client_secret_basic", "client_secret_post")):
            auth = {"type": "mcp_oauth", "mcp_server_url": destination, "access_token": secrets[0]}
            expected_auth = {"type": "mcp_oauth", "mcp_server_url": destination, "expires_at": None, "refresh": None}
            if method is not None:
                auth.update(expires_at=expiry, refresh={
                    "client_id": "public-test-client", "refresh_token": secrets[1],
                    "token_endpoint": "https://issuer.example.invalid/token", "token_endpoint_auth": {"type": method}})
                if method != "none":
                    auth["refresh"]["token_endpoint_auth"]["client_secret"] = secrets[2]
                    auth["refresh"].update(resource=destination, scope="read write")
                expected_auth.update(expires_at=expiry, refresh={
                    "client_id": "public-test-client", "token_endpoint": "https://issuer.example.invalid/token",
                    "token_endpoint_auth": {"type": method}, "resource": auth["refresh"].get("resource"),
                    "scope": auth["refresh"].get("scope")})
            response = credentials.with_raw_response.create(vault.id, name=" OAuth " + str(index) + " ", auth=auth)
            body = check_resource(safe(response.http_response, 201), vault.id, expected_auth)
            assert response.parse().to_dict() == body and body["name"] == "OAuth " + str(index)
            current = retrieve(vault.id, body["id"], expected_auth)
            changed = deepcopy(expected_auth)
            changed["expires_at"] = None
            current = update(vault.id, current, {"access_token": secrets[3]}, changed)
            expected_auth = deepcopy(expected_auth)
            expected_auth["expires_at"] = expiry
            current = update(vault.id, current, {"expires_at": expiry}, expected_auth, sdk=False)
            current = update(vault.id, current, {"expires_at": None}, changed)
            current = update(vault.id, current, {"access_token": secrets[0], "expires_at": expiry}, expected_auth)
            if method is not None:
                current = update(vault.id, current, {"refresh": {"refresh_token": secrets[3]}}, expected_auth, sdk=False)
                changed = deepcopy(expected_auth)
                changed["refresh"]["scope"] = None
                current = update(vault.id, current, {"refresh": {"scope": None}}, changed)
                expected_auth = changed
                changed = deepcopy(expected_auth)
                changed["refresh"]["scope"] = ""
                current = update(vault.id, current, {"refresh": {"scope": ""}}, changed, sdk=False)
                expected_auth = changed
                if method != "none":
                    for secret_patch in ({"client_secret": secrets[3]},):
                        current = update(vault.id, current, {"refresh": {"token_endpoint_auth": {"type": method, **secret_patch}}}, expected_auth)
                invalid_patches = [
                    {"refresh": {"token_endpoint_auth": {"type": "client_secret_post" if method != "client_secret_post" else "client_secret_basic"}}},
                    {"refresh": {"client_id": "changed"}}, {"refresh": {"token_endpoint": "https://other.example/token"}},
                    {"refresh": {"resource": None}}, {"refresh": {"token_endpoint_auth": {"type": "none"}}}]
            else:
                invalid_patches = [{"refresh": {}}, {"refresh": {"scope": "added"}}]
            invalid_patches += [{}, {"access_token": ""}, {"access_token": None, "refresh": None},
                                {"refresh": {}}, {"refresh": {"refresh_token": None, "token_endpoint_auth": None}},
                                {"refresh": {"token_endpoint_auth": {"type": "client_secret_basic", "client_secret": None}}},
                                {"access_token": 3}, {"expires_at": "tomorrow"}, {"token": secrets[0]},
                                {"refresh": {"scope": 3}}, {"refresh": {"refresh_token": 3}}]
            for patch in invalid_patches:
                safe(raw.post(endpoint(vault.id, current["id"]), headers=headers(), json={"auth": {"type": "mcp_oauth", **patch}}), 400)
                assert retrieve(vault.id, current["id"], expected_auth) == current
            expected[current["id"]] = current
            proof["variants"].append({"method": method, "credential": current})

        # Explicit null creation must have the same public nullable fields as omission.
        null_auth = {"type": "mcp_oauth", "mcp_server_url": destination, "expires_at": None, "refresh": None}
        null_body = safe(raw.post(endpoint(vault.id), headers=headers(), json={
            "name": "Raw nulls", "auth": {**null_auth, "access_token": secrets[0]}}), 201)
        check_resource(null_body, vault.id, null_auth)
        assert retrieve(vault.id, null_body["id"], null_auth) == null_body
        expected[null_body["id"]] = null_body
        static = credentials.create(vault.id, name="Static regression", auth={"type": "static_bearer", "mcp_server_url": destination, "token": secrets[0]})
        expected[static.id] = static.to_dict()
        assert expected[static.id]["auth"] == {"type": "static_bearer", "mcp_server_url": destination}
        safe(raw.post(endpoint(vault.id, static.id), headers=headers(), json={"auth": {"type": "mcp_oauth", "access_token": secrets[3]}}), 400)

        invalid_create = [
            {"type": "mcp_oauth", "mcp_server_url": destination},
            {"type": "mcp_oauth", "mcp_server_url": destination, "access_token": None},
            {"type": "mcp_oauth", "mcp_server_url": destination, "access_token": ""},
            {"type": "mcp_oauth", "mcp_server_url": "http://issuer.example", "access_token": secrets[0]},
            {"type": "mcp_oauth", "mcp_server_url": destination, "access_token": secrets[0], "refresh": {}},
            {"type": "mcp_oauth", "mcp_server_url": destination, "access_token": secrets[0], "expires_at": 3},
        ]
        for auth in invalid_create:
            safe(raw.post(endpoint(vault.id), headers=headers(), json={"name": "Invalid", "auth": auth}), 400)
        full = {"type": "mcp_oauth", "mcp_server_url": destination, "access_token": secrets[0], "refresh": {
            "client_id": "client", "refresh_token": secrets[1], "token_endpoint": "https://issuer.example/token",
            "token_endpoint_auth": {"type": "none"}}}
        for invalid in (None, {}, {"type": "client_secret_basic"}, {"type": "none", "client_secret": None},
                        {"type": "client_secret_post", "client_secret": 3}):
            auth = deepcopy(full)
            auth["refresh"]["token_endpoint_auth"] = invalid
            safe(raw.post(endpoint(vault.id), headers=headers(), json={"name": "Invalid auth", "auth": auth}), 400)

        response = credentials.with_raw_response.list(vault.id, order="asc", limit=100)
        listed = safe(response.http_response)
        assert response.parse().to_dict() == listed
        assert {row["id"]: row for row in listed["data"]} == expected and not listed["has_more"]
        assert safe(raw.get(endpoint(vault.id), headers=headers(), params={"order": "asc", "limit": 100})) == listed
        selected = null_body["id"]
        for target, api in ((endpoint(wrong.id, selected), client), (endpoint(foreign.id, selected), client),
                            (endpoint(vault.id, selected), other), (endpoint(vault.id, str(uuid.uuid4())), client)):
            for method in ("GET", "POST", "DELETE"):
                kwargs = {"json": {"auth": {"type": "mcp_oauth", "access_token": secrets[3]}}} if method == "POST" else {}
                safe(raw.request(method, target, headers=headers(api), **kwargs), 404)
        safe(raw.get(endpoint(vault.id), headers=headers(other)), 404)
        safe(raw.post(endpoint(vault.id), headers=headers(other), json={"name": "Foreign", "auth": full}), 404)
        safe(raw.get(endpoint(vault.id, selected), headers={"Authorization": "Bearer " + client.api_key}), 400)
        safe(raw.get(endpoint(vault.id, selected), headers={"OpenAI-Beta": "agents=v1"}), 401)
        assert retrieve(vault.id, selected, null_auth) == null_body
        proof["checks"].extend(["strict_sdk_raw_metadata", "all_endpoint_auth_variants", "omitted_null_patches",
                               "immutable_method_and_configuration", "tenant_and_vault_scope", "static_regression"])
        for index, (credential, previous) in enumerate(expected.items()):
            if index % 2:
                deleted = safe(raw.delete(endpoint(vault.id, credential), headers=headers()))
            else:
                response = credentials.with_raw_response.delete(credential, vault_id=vault.id)
                deleted = safe(response.http_response)
                assert response.parse().to_dict() == deleted
            assert deleted == {"id": credential, "deleted": True, "object": "vault.credential.deleted"}
            safe(raw.get(endpoint(vault.id, credential), headers=headers()), 404)
            safe(raw.delete(endpoint(vault.id, credential), headers=headers()), 404)
            safe(raw.post(endpoint(vault.id, credential), headers=headers(), json={"auth": {"type": "mcp_oauth", "access_token": secrets[3]}}), 404)
        assert list(credentials.list(vault.id)) == []
        proof["checks"].append("sdk_raw_deletion_and_no_resurrection")
        proof["passed"] = True
        return proof
    finally:
        cleanup_errors = []
        for api, vault_id in reversed(owned):
            try:
                api.beta.agents.vaults.delete(vault_id)
            except Exception as error:
                cleanup_errors.append(type(error).__name__)
        proof["cleanup_errors"] = cleanup_errors
        if cleanup_errors:
            proof["passed"] = False
        if evidence_path is not None:
            serialized = json.dumps(proof, indent=2)
            assert not any(secret in serialized for secret in secrets)
            Path(evidence_path).write_text(serialized + "\n")
        assert not cleanup_errors, "OAuth verifier could not remove owned Vaults"
