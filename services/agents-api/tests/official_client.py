"""Exercise the real service and PostgreSQL with the pinned official Python SDK."""

import base64
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
from urllib.parse import parse_qs, urlsplit
import uuid
import sys

sys.dont_write_bytecode = True

import httpx2
from jsonschema import Draft4Validator
from official_items import verify_items
from official_agents import verify_agents
from official_vaults import verify_vaults, verify_vault_recovery
from official_vault_list import verify_vault_list, verify_vault_list_recovery
from official_credentials import verify_credentials, verify_credential_recovery, verify_credential_storage_disabled
from official_credential_list import verify_credential_list, verify_credential_list_recovery
from official_mcp_credentials import verify_mcp_credentials, verify_mcp_credential_recovery
from official_credential_rotation import verify_credential_rotation, verify_rotation_recovery
from official_credential_delete import verify_credential_deletion, verify_credential_deletion_recovery, verify_keyless_credential_deletion
from official_vault_delete import verify_vault_deletion, verify_vault_deletion_recovery, verify_keyless_vault_deletion
from official_agent_list import verify_agent_list
from official_http_routing import verify_http_routing
from official_agent_references import verify_agent_references
from official_session_requests import verify_session_create_requests
from official_session_metadata import verify_session_metadata, verify_active_session_metadata
from official_session_creators import verify_session_creators, verify_creator_recovery
from official_source_file_list import verify_source_file_list, verify_source_file_list_recovery
from openai import AuthenticationError, BadRequestError, ConflictError, InternalServerError, NotFoundError, OpenAI
import yaml


def nullable_schema(value):
    """Translate Swagger 2's nullable extension for JSON Schema validation."""
    if isinstance(value, list):
        return [nullable_schema(item) for item in value]
    if not isinstance(value, dict):
        return value
    converted = {key: nullable_schema(item) for key, item in value.items() if key != "x-nullable"}
    return {"anyOf": [{"type": "null"}, converted]} if value.get("x-nullable") else converted


def main():
    root = Path(__file__).resolve().parents[3]
    contract = nullable_schema(yaml.safe_load((root / "contracts/agents-api/openapi.yaml").read_text()))

    def validate_response(response):
        response.read()
        path = response.request.url.path.removeprefix("/v1")
        if path.startswith("/agents/sessions/"):
            suffix = path.split("/")[4:]
            path = "/agents/sessions/{session_id}"
            if suffix and suffix[0] == "turns":
                path += "/turns" + ("/{turn_id}" if len(suffix) > 1 else "")
            elif suffix and suffix[0] in ("items", "events"):
                path += "/" + suffix[0]
        elif path.startswith("/vaults/"):
            suffix = path.split("/")[3:]
            path = "/vaults/{vault_id}"
            if suffix and suffix[0] == "credentials":
                path += "/credentials" + ("/{credential_id}" if len(suffix) > 1 else "")
        elif path.startswith("/agents/") and path != "/agents/sessions":
            path = "/agents/{agent_id}"
        elif path.startswith("/files/"):
            path = "/files/{file_id}/content" if path.endswith("/content") else "/files/{file_id}"
        if path.endswith("/events") and response.status_code == 202:
            assert response.content == b""
            return
        schema = contract["paths"][path][response.request.method.lower()]["responses"][str(response.status_code)]["schema"]
        Draft4Validator({"definitions": contract["definitions"], **schema}).validate(response.json())
    pin = json.loads((root / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert source.get("vcs_info", {}).get("commit_id") == pin["commit"], "Install the pinned SDK commit first"
    dsn = os.environ["OAC_TEST_DATABASE_URL"]
    parts = urlsplit(dsn)
    database = parse_qs(parts.query).get("dbname", [parts.path.lstrip("/")])[0]
    assert parts.scheme in ("postgres", "postgresql") and database.startswith("oac_") and database.endswith("_tests"), "A dedicated execution test database is required"
    binary = os.environ["OAC_TEST_SERVER_BIN"]
    with socket.socket() as address:
        address.bind(("127.0.0.1", 0))
        port = address.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    admin_token = secrets.token_hex(32)
    tokens = []
    project_ids = []
    bindings = []

    def project_bindings(directory):
        # Only fixture setup reads internal scope; public clients use issued keys.
        path = Path(directory) / "project-identities.json"
        path.touch(mode=0o600)
        path.write_text(json.dumps({"project_ids": project_ids}))
        subprocess.run(["go", "run", "./services/agents-api/tests/fixtures"], cwd=root,
                       env=dict(os.environ, OAC_TEST_PROJECT_IDENTITIES_FIXTURE=str(path)),
                       check=True, timeout=120)
        return json.loads(path.read_text())["bindings"]

    process = None
    credential_canary = secrets.token_hex(32)
    with tempfile.TemporaryDirectory(prefix="agents-api-test-") as directory:
        core_key_digests = Path(directory) / "core-key-digests.json"
        core_key_digests.touch(mode=0o600)
        core_key_digests.write_text(json.dumps([hashlib.sha256(admin_token.encode()).hexdigest()]))
        credential_key = Path(directory) / "credential-key.txt"
        credential_key.touch(mode=0o600)
        credential_key.write_text(base64.b64encode(secrets.token_bytes(32)).decode() + "\n")
        env = dict(os.environ, OAC_DATABASE_URL=dsn, OAC_CORE_KEY_DIGESTS_FILE=str(core_key_digests), OAC_ADDR=f"127.0.0.1:{port}", OAC_DEFAULT_HARNESS="codex")
        env["OAC_CREDENTIAL_KEY_FILE"] = str(credential_key)
        # Enable the real Worker/gateway admission path without connecting a daemon.
        # Synthetic fixture inputs remain queued; this is not live model acceptance.
        env["OAC_PUBLIC_URL"] = f"http://127.0.0.1:{port}"
        with (Path(directory) / "server.log").open("w+") as log:
            def start():
                child = subprocess.Popen([binary], env=env, stdout=log, stderr=log)
                try:
                    deadline = time.monotonic() + 20
                    with httpx2.Client(trust_env=False, timeout=1) as probe:
                        while time.monotonic() < deadline:
                            if child.poll() is not None:
                                raise AssertionError("Agents API exited during startup")
                            try:
                                if probe.get(base + "/healthz").status_code == 200:
                                    return child
                            except httpx2.TransportError:
                                pass
                            time.sleep(0.1)
                    raise AssertionError("Agents API did not become healthy")
                except BaseException:
                    child.terminate()
                    child.wait(timeout=15)
                    raise

            def client(token, **scope):
                return OpenAI(api_key=token, **scope, base_url=base + "/v1", max_retries=0, _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=10, event_hooks={"response": [validate_response]}))

            def expect_error(error, operation):
                try:
                    operation()
                except error as result:
                    assert isinstance(result.body, dict) and "code" in result.body
                    assert isinstance(result.body.get("type"), str) and isinstance(result.body.get("message"), str)
                    return result
                else:
                    raise AssertionError(f"Expected {error.__name__}")

            try:
                process = start()
                with httpx2.Client(base_url=base + "/core/v1/", trust_env=False, timeout=10,
                                   headers={"Authorization": "Bearer " + admin_token}) as admin:
                    def issue_key(project_id, name):
                        response = admin.post(f"projects/{project_id}/keys", json={"name": name})
                        assert response.status_code == 201, "Fixture key issuance failed"
                        issued = response.json()
                        assert issued["project_id"] == project_id
                        return issued["key"]

                    for index in range(4):
                        response = admin.post("projects", json={"name": f"Official SDK fixture {index}"})
                        assert response.status_code == 201, "Fixture Project creation failed"
                        project_id = response.json()["id"]
                        project_ids.append(project_id)
                        tokens.append(issue_key(project_id, "SDK fixture"))
                    bindings.extend(project_bindings(directory))
                    replacement_key, peer_key, additional_key = [
                        issue_key(project_ids[0], name) for name in ("Replacement", "Peer", "Additional")]
                    tokens.extend([replacement_key, peer_key, additional_key])
                with client(tokens[0]) as a, client(tokens[1]) as b, client("invalid-key") as invalid:
                    with client(peer_key, organization=bindings[0]["organization_id"],
                                project=bindings[0]["project_id"]) as peer:
                        saved_vaults = verify_vaults(a, b, invalid, peer, bindings[0], expect_error)
                        saved_credentials = verify_credentials(a, b, invalid, peer, saved_vaults, credential_canary, expect_error)
                        listed_vaults = verify_vault_list(a, b, invalid, peer, bindings[0], saved_vaults,
                                                          root, directory, expect_error)
                        listed_credentials = verify_credential_list(
                            a, b, invalid, peer, bindings[0], saved_vaults, saved_credentials,
                            listed_vaults, root, directory, credential_canary, expect_error)
                        listed_files = verify_source_file_list(a, b, invalid, peer, expect_error)
                    saved_agents = verify_agents(a, b, invalid, expect_error)
                    listed_agents = verify_agent_list(a, b, invalid, saved_agents, expect_error)
                    verify_http_routing(base, tokens[0])
                    sessions = a.beta.agents.sessions
                    spec = {"input": "Verify client fixture admission.", "agent": {"model": "requested-test-model", "instructions": "Keep the configuration."}, "environment": {"type": "none"}}
                    headers = {"Idempotency-Key": "same-key"}
                    first = sessions.create(**spec, metadata={"workspace": "untrusted-reference"}, extra_headers=headers)
                    assert first.object == "agent.session" and first.status == "in_progress"
                    assert first.agent.model == spec["agent"]["model"] and first.agent.instructions == spec["agent"]["instructions"]
                    assert first.environment.type == "none" and first.required_actions == [] and first.vault_ids == []
                    assert first.agent.tools == [] and first.agent.multi_agent.enabled is False
                    default_raw = sessions.with_raw_response.retrieve(first.id)
                    assert default_raw.http_response.json()["agent"]["tools"] == []
                    assert first.created_at == first.last_active_at and isinstance(first.created_at, int)
                    replay = sessions.create(**spec, metadata={"workspace": "untrusted-reference"}, extra_headers=headers)
                    assert replay == first
                    assert sessions.retrieve(first.id) == first
                    from official_auth import verify_caller_principals
                    verify_caller_principals(client, base, bindings[0], tokens[0], replacement_key, peer_key, first)
                    changed = {**spec, "agent": {**spec["agent"], "instructions": "Changed"}}
                    expect_error(ConflictError, lambda: sessions.create(**changed, metadata={"workspace": "untrusted-reference"}, extra_headers=headers))
                    others = [sessions.create(**spec) for _ in range(2)]
                    expected = {first.id, *(item.id for item in others)}
                    asc = list(sessions.list(limit=1, order="asc"))
                    desc = list(sessions.list(limit=2, order="desc"))
                    assert {item.id for item in asc} == expected
                    assert [item.id for item in asc] == list(reversed([item.id for item in desc]))
                    assert list(sessions.list(after=asc[-1].id, order="asc")) == []
                    other = b.beta.agents.sessions.create(**spec, extra_headers=headers)
                    assert other.id != first.id
                    assert [item.id for item in b.beta.agents.sessions.list()] == [other.id]
                    expect_error(NotFoundError, lambda: b.beta.agents.sessions.retrieve(first.id))
                    expect_error(NotFoundError, lambda: b.beta.agents.sessions.list(after=first.id))
                    expect_error(AuthenticationError, lambda: invalid.beta.agents.sessions.retrieve(first.id))
                    expect_error(BadRequestError, lambda: sessions.retrieve(first.id, extra_headers={"OpenAI-Beta": ""}))
                    expect_error(BadRequestError, lambda: sessions.create(**{**spec, "input": [{"role": "user", "content": [{"type": "input_image", "image_url": "https://example.com/image.png"}]}]}))
                    # A self-hosted Session carries its own write-only model provider; nothing here calls it.
                    provider = {"protocol": "responses", "base_url": "https://model.fixture.example/v1", "api_key": "fixture-model-key"}
                    self_hosted = sessions.create(agent=spec["agent"], environment={"type": "self_hosted", "workspace_directory": "/workspace"},
                                                  extra_body={"x_agents_core": {"model_provider": provider}})
                    assert self_hosted.environment.type == "self_hosted"
                    assert list(sessions.turns.list(self_hosted.id)) == []
                    assert sessions.delete(self_hosted.id).deleted
                    expect_error(BadRequestError, lambda: sessions.create(**spec, extra_body={"tenant_id": bindings[1]["tenant_id"]}))
                    assert list(sessions.list(agent_id="unknown-agent")) == []
                    assert list(sessions.list(agent_id=first.agent.id)) == [first]
                    assert list(b.beta.agents.sessions.list(agent_id=first.agent.id)) == []
                    assert sessions.list(limit=0).data == sessions.list(limit=1).data
                    assert {item.id for item in sessions.list()} == expected
                    metadata = {str(i): "🧪" * 512 for i in range(16)}
                    large = sessions.create(**spec, metadata=metadata)
                    assert sessions.retrieve(large.id).metadata == metadata
                    request_sessions = verify_session_create_requests(a, spec)
                    request_sessions.append(verify_session_metadata(a, b, invalid, spec, expect_error))
                    turn_session = sessions.create(**spec)
                    sessions.events.create(turn_session.id, events=[{"type": "agent.session.input.cancel"}])
                    initial_turn = list(sessions.turns.list(turn_session.id))[0]
                    assert initial_turn.status == "cancelled"
                    fixture = Path(directory) / "turns.json"
                    fixture.write_text(json.dumps({"tenant": bindings[0]["tenant_id"], "session": turn_session.id}))
                    subprocess.run(["go", "run", "./services/agents-api/tests/fixtures"], cwd=root,
                                   env=dict(os.environ, OAC_TEST_TURN_FIXTURE=str(fixture)), check=True, timeout=120)
                    turn_ids = json.loads(fixture.read_text())["turns"]
                    turns = sessions.turns
                    recovered = list(turns.list(turn_session.id, limit=1, order="asc"))
                    assert recovered[0] == initial_turn
                    recovered = recovered[1:]
                    assert [turn.id for turn in recovered] == turn_ids
                    assert [turn.status for turn in recovered] == ["completed", "failed", "cancelled", "in_progress"]
                    assert all(turn.agent_id == turn_session.agent.id and turn.session_id == turn_session.id for turn in recovered)
                    assert all(turn.started_at is not None for turn in recovered)
                    assert all(turn.completed_at is not None for turn in recovered[:3]) and recovered[-1].completed_at is None
                    assert recovered[1].error.code == "internal_error" and all(turn.error is None for turn in [recovered[0], *recovered[2:]])
                    assert all(turn.usage is None for turn in recovered)
                    assert "SECRET" not in repr(recovered) and "PRIVATE" not in repr(recovered)
                    assert [turn.id for turn in turns.list(turn_session.id, limit=2)] == list(reversed([initial_turn.id, *turn_ids]))
                    assert list(turns.list(turn_session.id, after=turn_ids[-1], order="asc")) == []
                    assert len(list(turns.list(first.id))) == 1
                    assert turns.retrieve(turn_ids[0], session_id=turn_session.id) == recovered[0]
                    expect_error(NotFoundError, lambda: b.beta.agents.sessions.turns.list(turn_session.id))
                    expect_error(NotFoundError, lambda: b.beta.agents.sessions.turns.retrieve(turn_ids[0], session_id=turn_session.id))
                    expect_error(NotFoundError, lambda: turns.retrieve(turn_ids[0], session_id=first.id))
                    expect_error(NotFoundError, lambda: turns.list(first.id, after=turn_ids[0]))
                    expect_error(BadRequestError, lambda: turns.list(turn_session.id, limit=101))
                    # Unparsable identifiers share the missing-resource response,
                    # and so does a malformed Turn list cursor.
                    for malformed in ("sess_" + uuid.uuid4().hex, "invalid"):
                        expect_error(NotFoundError, lambda: sessions.retrieve(malformed))
                        expect_error(NotFoundError, lambda: turns.list(malformed))
                        expect_error(NotFoundError, lambda: sessions.items.list(malformed))
                    expect_error(NotFoundError, lambda: turns.retrieve("turn_" + uuid.uuid4().hex, session_id=turn_session.id))
                    expect_error(NotFoundError, lambda: turns.list(turn_session.id, after="invalid"))
                    saved_items = verify_items(a, b, invalid, turn_session.id, first.id, turn_ids, expect_error)
                    request_sessions.append(verify_active_session_metadata(a, turn_session.id))
                    referenced, reference_retry = verify_agent_references(a, b, expect_error)
                    request_sessions.extend(referenced)
                    creator_retries = verify_session_creators(
                        client, a, b, replacement_key, peer_key, additional_key, spec, expect_error)
                    request_sessions.extend(result for _, _, result in creator_retries)
                    from official_mcp import verify_mcp_configuration
                    mcp_sessions, mcp_agents = verify_mcp_configuration(a, b, expect_error)
                    request_sessions.extend(mcp_sessions)
                    saved_agents.extend(mcp_agents)
                    with client(peer_key) as peer:
                        mcp_credentials = verify_mcp_credentials(a, b, peer, credential_canary, expect_error)
                        credential_rotation = verify_credential_rotation(
                            a, b, invalid, peer, saved_vaults, saved_credentials, credential_canary, expect_error)
                        credential_deletion = verify_credential_deletion(a, b, invalid, peer, credential_canary, expect_error)
                        vault_deletion = verify_vault_deletion(a, b, invalid, peer, credential_canary, expect_error)
                    process.terminate()
                    process.wait(timeout=15)
                    process = start()
                    with client(peer_key) as peer:
                        verify_vault_recovery(a, b, peer, saved_vaults)
                        verify_vault_list_recovery(a, b, peer, listed_vaults)
                        verify_credential_recovery(a, b, peer, saved_credentials)
                        verify_credential_list_recovery(a, b, peer, listed_credentials, credential_canary)
                        verify_source_file_list_recovery(a, b, peer, listed_files)
                        verify_mcp_credential_recovery(a, mcp_credentials)
                        verify_rotation_recovery(a, peer, credential_rotation)
                        verify_credential_deletion_recovery(a, b, credential_deletion, expect_error)
                        verify_vault_deletion_recovery(a, b, vault_deletion, expect_error)
                    assert [a.beta.agents.retrieve(item.id) for item in saved_agents] == saved_agents
                    assert [item.id for item in a.beta.agents.list(limit=2, order="asc") if item.id in listed_agents] == listed_agents
                    # The active SQL fixture has no native process. The real Worker
                    # marks its interrupted Turn failed during restart recovery.
                    prior_history_session = next(item for item in request_sessions if item.id == turn_session.id)
                    final_history_session = sessions.retrieve(turn_session.id)
                    assert final_history_session.status == "failed"
                    assert final_history_session.error == "The execution could not complete."
                    lifecycle = {"status", "error", "last_active_at"}
                    assert {k: v for k, v in final_history_session.to_dict().items() if k not in lifecycle} == {k: v for k, v in prior_history_session.to_dict().items() if k not in lifecycle}
                    request_sessions = [final_history_session if item.id == turn_session.id else item for item in request_sessions]
                    interrupted = turns.retrieve(turn_ids[-1], session_id=turn_session.id)
                    assert interrupted.status == "failed" and interrupted.error.code == "internal_error"
                    assert interrupted.id == recovered[-1].id and interrupted.completed_at is not None
                    recovered[-1] = interrupted
                    assert [sessions.retrieve(item.id) for item in request_sessions] == request_sessions
                    reference_spec, reference_headers, reference_result = reference_retry
                    assert sessions.create(**reference_spec, extra_headers=reference_headers) == reference_result
                    saved_items = [item.model_copy(update={"status": "incomplete"}) if item.turn_id == interrupted.id and item.status == "in_progress" else item for item in saved_items]
                    assert list(sessions.items.list(turn_session.id, order="asc")) == saved_items
                    assert list(turns.list(turn_session.id, order="asc")) == [initial_turn, *recovered]
                    assert sessions.retrieve(first.id) == first
                    assert sessions.create(**spec, metadata={"workspace": "untrusted-reference"}, extra_headers=headers) == first
                    verify_creator_recovery(client, replacement_key, peer_key, additional_key,
                                            creator_retries, expect_error)
                go_env = dict(os.environ, OAC_TEST_CLIENT_BASE_URL=base + "/v1",
                              OAC_TEST_CLIENT_KEY=tokens[2], OAC_TEST_CLIENT_OTHER_KEY=tokens[3])
                subprocess.run(["go", "test", "./packages/agents-client/v1", "-run", "^TestService$", "-count=1"],
                               cwd=root, env=go_env, check=True, timeout=120)
                with client(tokens[2]) as go_tenant:
                    go_sessions = list(go_tenant.beta.agents.sessions.list())
                    assert len(go_sessions) == 3 and all(item.agent.model == "go-client-test-model" for item in go_sessions)
                process.terminate()
                process.wait(timeout=15)
                process = start()
                from official_auth import verify_scope_recovery
                assert project_bindings(directory) == bindings
                verify_scope_recovery(client, base, bindings[0],
                                      (tokens[0], replacement_key, peer_key, additional_key), first)
                with client(tokens[0]) as restored:
                    assert restored.beta.agents.sessions.retrieve(first.id) == first
                process.terminate()
                process.wait(timeout=15)
                # Reuse the same credential contract with the second public engine.
                env["OAC_DEFAULT_HARNESS"] = "claude_sdk"
                process = start()
                with client(tokens[0]) as a, client(tokens[1]) as b, client(peer_key) as peer:
                    claude_credentials = verify_mcp_credentials(a, b, peer, credential_canary, expect_error)
                process.terminate()
                process.wait(timeout=15)
                process = start()
                with client(tokens[0]) as a:
                    verify_mcp_credential_recovery(a, claude_credentials)
                process.terminate()
                process.wait(timeout=15)
                env["OAC_DEFAULT_HARNESS"] = "codex"
                env.pop("OAC_CREDENTIAL_KEY_FILE")
                process = start()
                with client(tokens[0]) as without_key, client(tokens[1]) as other, client(peer_key) as peer:
                    verify_credential_storage_disabled(without_key, saved_credentials[0][0], credential_canary, expect_error)
                    verify_keyless_credential_deletion(without_key, credential_deletion, expect_error)
                    verify_keyless_vault_deletion(without_key, vault_deletion, expect_error)
                    verify_credential_list_recovery(without_key, other, peer, listed_credentials,
                                                    credential_canary, phase="restart without the storage key")
                print("Caller principal: SDK/raw HTTP scope checks, shared Project access and persistent scope recovery passed.")
                print("Official Turn client: lifecycle, Agent identity, safe errors, restart recovery, pagination and tenant/Session isolation passed.")
                print("Official Go client: creation/retries, retrieval, bidirectional pagination and tenant isolation passed.")
                print("Official client: upstream and generated response schemas, persistence/restart, retries, pagination, tenant isolation and explicit unsupported options passed.")
            finally:
                if process and process.poll() is None:
                    process.terminate()
                    process.wait(timeout=15)
                log.flush()
                log.seek(0)
                output = log.read()
                assert admin_token not in output, "Administrator token leaked into the service log"
                assert all(token not in output for token in tokens), "API key leaked into the service log"
                assert credential_canary not in output, "Credential token leaked into the service log"
                assert credential_key.read_text().strip() not in output, "Credential key leaked into the service log"


if __name__ == "__main__":
    main()
