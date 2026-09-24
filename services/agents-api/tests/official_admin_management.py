"""Admin management and real copied-asset execution against a deployed Core.

Read private JSON settings from stdin: base, admin, model, harness and optional
model_provider (the complete write-only provider object). run_model defaults to
true; false performs resource checks only and never reports model acceptance.
The deployment and its qualified runtime must already be running. This script
creates two independent key spaces and cleans up only its own resources.
"""
import base64
import io
import json
import secrets
import sys
import time
import traceback
import uuid
import zipfile

import httpx
import openai
from openai import DefaultHttpxClient, OpenAI


ADMIN_PATHS = {"agent": "agents", "skill": "skills", "file": "files",
               "environment_template": "environment-templates", "vault": "vaults",
               "session": "sessions"}
PUBLIC_PATHS = {"agent": "agents", "skill": "skills", "file": "files",
                "environment_template": "agents/environments/templates", "vault": "vaults",
                "session": "agents/sessions"}


def proof_bundle(marker):
    manifest = ("---\nname: copy-proof\ndescription: Verify copied confidential assets.\n---\n"
                "Run python3 /environment/initialization/capabilities/skills/copy-proof/scripts/check.py.\n")
    script = f'''import os
from pathlib import Path
assert os.environ['COPY_PRIVATE'] == {marker!r}
assert Path('/workspace/copy-source.txt').read_text() == {marker!r}
assert Path('/workspace/copy-setup.txt').read_text() == {marker!r}
assert Path('/workspace/copy-inline.txt').read_text() == {marker!r}
Path('/workspace/outputs').mkdir(exist_ok=True)
Path('/workspace/outputs/copy-proof.txt').write_text('COPY_VERIFIED')
print('COPY_VERIFIED')
'''
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("copy-proof/SKILL.md", manifest)
        archive.writestr("copy-proof/scripts/check.py", script)
    return output.getvalue()


def main():
    assert openai.__version__ == "3.13.0", "Use the pinned official SDK"
    settings = json.load(sys.stdin)
    base = settings["base"].rstrip("/")
    admin = {"Authorization": "Bearer " + settings["admin"], "X-Core-Console-Actor": "admin-acceptance"}
    private = "copy-private-" + secrets.token_hex(20)
    secret_values = [private, settings["admin"]]
    if settings.get("model_provider"):
        secret_values.append(settings["model_provider"]["api_key"])
    owned, keys, clients = set(), [], []
    session_id = None
    with httpx.Client(trust_env=False, timeout=30) as http:
        def request(method, path, expected=200, token=None, headers=None, **kwargs):
            response = http.request(method, base + path, headers={
                **(admin if token is None else {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}),
                **(headers or {})}, **kwargs)
            assert response.status_code == expected, f"{method} {path}: expected {expected}, got {response.status_code}"
            return response

        def safe(response):
            assert all(value not in response.text for value in secret_values), "Private value in metadata response"
            return response.json()

        def admin_resource(key, kind, resource):
            return f"/core/v1/admin/api-keys/{key['id']}/{ADMIN_PATHS[kind]}/{resource}"

        def remember(key, kind, resource):
            owned.add((key["id"], kind, resource))
            return resource

        def copy(source, target, kind, resource, dependencies=False, vault=None):
            body = {"source_key_id": source["id"], "target_key_id": target["id"],
                    "resource_type": kind, "resource_id": resource, "include_dependencies": dependencies}
            if vault:
                body["target_vault_id"] = vault
            headers = {"Idempotency-Key": str(uuid.uuid4())}
            result = safe(request("POST", "/core/v1/admin/copies", json=body, headers=headers))
            assert safe(request("POST", "/core/v1/admin/copies", json=body, headers=headers)) == result
            request("POST", "/core/v1/admin/copies", expected=409,
                    json={**body, "include_dependencies": not dependencies}, headers=headers)
            for item in result["mappings"]:
                if item["type"] in ADMIN_PATHS:
                    remember(target, item["type"], item["target_id"])
            return result

        def mapped(result, kind, source):
            return next(item["target_id"] for item in result["mappings"]
                        if item["type"] == kind and item["source_id"] == source)

        def equal_reads(key, kind, resource):
            public = request("GET", f"/v1/{PUBLIC_PATHS[kind]}/{resource}", token=key["key"])
            management = request("GET", admin_resource(key, kind, resource))
            assert management.content == public.content, "Admin and public resource responses differ"
            safe(management)

        def denied_reference(key, body):
            safe(request("POST", "/v1/agents/sessions", expected=404, token=key["key"], json=body))

        def wait_idle(client, sid, wanted_turns):
            deadline = time.monotonic() + settings.get("timeout", 360)
            while time.monotonic() < deadline:
                state = client.beta.agents.sessions.retrieve(sid)
                turns = list(client.beta.agents.sessions.turns.list(sid, order="asc"))
                assert state.status != "failed", "Real copied-asset Session failed"
                assert all(turn.status not in ("failed", "cancelled") for turn in turns), "Real copied-asset Turn failed"
                if len(turns) >= wanted_turns and turns[-1].status == "completed" and state.status == "idle":
                    return state
                time.sleep(0.5)
            raise AssertionError("Real copied-asset execution timed out")

        try:
            for name in ("source", "target"):
                key = request("POST", "/core/v1/admin/api-keys", expected=201,
                              json={"id": str(uuid.uuid4()), "name": "admin-proof-" + name}).json()
                keys.append(key)
                secret_values.append(key["key"])
                safe(request("GET", "/core/v1/admin/api-keys/" + key["id"]))
                client = OpenAI(base_url=base + "/v1", api_key=key["key"], max_retries=0,
                                http_client=DefaultHttpxClient(trust_env=False), _strict_response_validation=True)
                clients.append(client)
            source, target = keys
            a, b = clients
            assert source["tenant_id"] != target["tenant_id"]
            for path in PUBLIC_PATHS.values():
                assert safe(request("GET", "/v1/" + path, token=target["key"]))["data"] == []
            request("GET", "/core/v1/admin/api-keys", expected=401, token=source["key"])
            request("GET", "/v1/agents", expected=401, headers={"OpenAI-Beta": "agents=v1"})
            safe(request("GET", "/core/v1/admin/startup-configuration"))

            file = a.files.create(file=("copy-source.txt", private.encode()), purpose="user_data")
            remember(source, "file", file.id)
            archive = proof_bundle(private)
            with zipfile.ZipFile(io.BytesIO(archive)) as bundle:
                upload_files = [(name, bundle.read(name), "application/octet-stream") for name in bundle.namelist()]
            skill = a.skills.create(files=upload_files)
            remember(source, "skill", skill.id)
            # Preserve a gap and a non-initial default across copies.
            for _ in range(2):
                a.skills.versions.create(skill_id=skill.id, files=upload_files, default=False)
            a.skills.update(skill.id, default_version="3")
            a.skills.versions.delete(version="2", skill_id=skill.id)
            archive = a.skills.content.retrieve(skill.id).read()
            template = a.beta.agents.environments.templates.create(
                name="Confidential copy proof", env={"COPY_PRIVATE": private},
                setup_commands=[{"command": "printf %s " + private + " > /workspace/copy-setup.txt"}],
                skills=[{"type": "skill_reference", "skill_id": skill.id, "version": "3"}],
                files=[{"type": "file_id", "path": "/workspace/copy-source.txt", "file_id": file.id},
                       {"type": "inline", "path": "/workspace/copy-inline.txt", "data": base64.b64encode(private.encode()).decode()}])
            remember(source, "environment_template", template.id)
            provider = {"harness": settings["harness"]}
            if settings.get("model_provider"):
                provider["model_provider"] = settings["model_provider"]
            agent = a.beta.agents.create(model=settings["model"],
                instructions="Use the installed copy-proof skill and run its Python verifier. Report COPY_VERIFIED only after it succeeds.",
                extra_body={"x_agents_core": provider})
            remember(source, "agent", agent.id)
            vault = a.beta.agents.vaults.create(name="Copy credentials")
            remember(source, "vault", vault.id)
            static = a.beta.agents.vaults.credentials.create(vault.id, name="Static", auth={
                "type": "static_bearer", "mcp_server_url": "https://copy.example/mcp", "token": private})
            oauth = a.beta.agents.vaults.credentials.create(vault.id, name="OAuth", auth={
                "type": "mcp_oauth", "mcp_server_url": "https://copy.example/oauth", "access_token": private})
            refresh = a.beta.agents.vaults.credentials.create(vault.id, name="Refresh", auth={
                "type": "mcp_oauth", "mcp_server_url": "https://copy.example/refresh", "access_token": private,
                "refresh": {"client_id": "copy-proof", "refresh_token": private,
                            "token_endpoint": "https://copy.example/token", "token_endpoint_auth": {"type": "none"}}})

            for kind, resource in [("agent", agent.id), ("skill", skill.id), ("file", file.id),
                                   ("environment_template", template.id), ("vault", vault.id)]:
                equal_reads(source, kind, resource)
                path = f"/v1/{PUBLIC_PATHS[kind]}/{resource}"
                for method in ("GET", "DELETE"):
                    request(method, path, expected=404, token=target["key"])
            for kind, resource, patch in [("agent", agent.id, {"name": "foreign"}),
                                           ("skill", skill.id, {"default_version": "1"}),
                                           ("environment_template", template.id, {"name": "foreign"})]:
                request("POST", f"/v1/{PUBLIC_PATHS[kind]}/{resource}", expected=404, token=target["key"], json=patch)
            credential_path = f"/v1/vaults/{vault.id}/credentials/{static.id}"
            for method in ("GET", "DELETE"):
                request(method, credential_path, expected=404, token=target["key"])
            request("POST", credential_path, expected=404, token=target["key"],
                    json={"auth": {"type": "static_bearer", "token": private}})
            credential_metadata = request("GET", credential_path, token=source["key"])
            credential_admin = request("GET", admin_resource(source, "vault", vault.id) + "/credentials/" + static.id)
            assert credential_admin.content == credential_metadata.content
            safe(credential_admin)
            request("GET", admin_resource(source, "file", file.id) + "/content", expected=404)
            denied_reference(target, {"agent_id": agent.id, "environment": {"type": "openai_hosted"}})
            denied_reference(target, {"agent": {"model": settings["model"]}, "environment": {
                "type": "openai_hosted", "environment_template_id": template.id}})
            denied_reference(target, {"agent": {"model": settings["model"]}, "environment": {
                "type": "openai_hosted", "skills": [{"type": "skill_reference", "skill_id": skill.id}]}})
            denied_reference(target, {"agent": {"model": settings["model"]}, "environment": {
                "type": "openai_hosted", "files": [{"type": "file_id", "path": "/workspace/foreign", "file_id": file.id}]}})
            denied_reference(target, {"agent": {"model": settings["model"]}, "environment": {"type": "openai_hosted"}, "vault_ids": [vault.id]})

            target_vault = b.beta.agents.vaults.create(name="Empty target attachment")
            remember(target, "vault", target_vault.id)
            credential_errors = []
            # The pinned MCP selection contract returns 400 for both missing
            # and foreign credentials within an explicitly attached Vault.
            for reference in (static.id, str(uuid.uuid4())):
                response = safe(request("POST", "/v1/agents/sessions", expected=400, token=target["key"], json={
                    "agent": {"model": settings["model"], "tools": [{"type": "mcp", "server_label": "foreign",
                        "transport": {"type": "http", "server_url": "https://copy.example/mcp"}, "credential_id": reference}]},
                    "environment": {"type": "openai_hosted"}, "vault_ids": [target_vault.id]}))
                response["error"]["message"] = response["error"]["message"].replace(reference, "reference")
                credential_errors.append(response)
            assert credential_errors[0] == credential_errors[1], "Foreign credential differs from missing credential"

            direct_file = copy(source, target, "file", file.id)
            copied_file = mapped(direct_file, "file", file.id)
            assert b.files.retrieve(copied_file).bytes == len(private.encode())
            request("GET", "/v1/files/" + copied_file + "/content", expected=400, token=target["key"])
            direct_skill = copy(source, target, "skill", skill.id)
            copied_skill = mapped(direct_skill, "skill", skill.id)
            assert b.skills.retrieve(copied_skill).default_version == "3"
            assert [item.version for item in b.skills.versions.list(copied_skill, order="asc")] == ["1", "3"]
            assert b.skills.content.retrieve(copied_skill).read() == archive
            request("POST", "/core/v1/admin/copies", expected=400, json={"source_key_id": source["id"],
                "target_key_id": target["id"], "resource_type": "environment_template", "resource_id": template.id})
            copied_template = copy(source, target, "environment_template", template.id, dependencies=True)
            template_id = mapped(copied_template, "environment_template", template.id)
            copied_agent = copy(source, target, "agent", agent.id)
            agent_id = mapped(copied_agent, "agent", agent.id)
            copied_vault = copy(source, target, "vault", vault.id)
            vault_id = mapped(copied_vault, "vault", vault.id)
            assert len(copied_vault["skipped"]) == 1 and copied_vault["skipped"][0]["source_id"] == refresh.id
            assert {item.id for item in b.beta.agents.vaults.credentials.list(vault_id)} == {
                mapped(copied_vault, "credential", static.id), mapped(copied_vault, "credential", oauth.id)}
            copy(source, target, "credential", static.id, vault=vault_id)
            for kind, resource in [("agent", agent_id), ("environment_template", template_id), ("skill", copied_skill), ("vault", vault_id)]:
                equal_reads(target, kind, resource)
            print("Admin resource copies, isolation, confidential metadata and idempotency passed.", flush=True)

            if settings.get("run_model", True):
                session = b.beta.agents.sessions.create(agent_id=agent_id, environment={
                    "type": "openai_hosted", "environment_template_id": template_id},
                    input="Run the installed copy-proof skill's Python verifier now.",
                    extra_headers={"Idempotency-Key": str(uuid.uuid4())})
                session_id = remember(target, "session", session.id)
                completed = wait_idle(b, session_id, 1)
                assert completed.usage is not None and completed.usage.total_tokens > 0, "Real model usage missing"
                artifacts = list(b.beta.agents.sessions.artifacts.list(session_id))
                artifact = next(item for item in artifacts if item.path == "/workspace/outputs/copy-proof.txt")
                assert b.beta.agents.sessions.artifacts.content(artifact.id, session_id=session_id).read() == b"COPY_VERIFIED"
                equal_reads(target, "session", session_id)
                for suffix in ("", "/turns", "/items", "/artifacts", "/artifacts/" + artifact.id):
                    management = request("GET", admin_resource(target, "session", session_id) + suffix)
                    public = request("GET", "/v1/agents/sessions/" + session_id + suffix, token=target["key"])
                    assert management.content == public.content, "Admin Session history differs"
                    request("GET", "/v1/agents/sessions/" + session_id + suffix, expected=404, token=source["key"])
                assert request("GET", admin_resource(target, "session", session_id) + "/artifacts/" + artifact.id + "/content").content == b"COPY_VERIFIED"
                request("POST", "/core/v1/admin/copies", expected=400, json={"source_key_id": target["id"],
                    "target_key_id": source["id"], "resource_type": "session", "resource_id": session_id})
                history = b.beta.agents.sessions.items.list(session_id, order="asc").to_dict()
                old_secret = target["key"]
                reset = request("POST", "/core/v1/admin/api-keys/" + target["id"] + "/reset",
                                json={"request_id": str(uuid.uuid4())}).json()
                assert reset["id"] == target["id"] and reset["tenant_id"] == target["tenant_id"]
                target["key"] = reset["key"]
                secret_values.append(reset["key"])
                b.api_key = reset["key"]
                request("GET", "/v1/agents/sessions/" + session_id, expected=401, token=old_secret)
                assert b.beta.agents.sessions.items.list(session_id, order="asc").to_dict() == history
                b.beta.agents.sessions.events.create(session_id, events=[{"type": "agent.session.input.message", "input": [
                    {"role": "user", "content": [{"type": "input_text", "text": "Reply COPY_CONTINUED."}]}]}])
                wait_idle(b, session_id, 2)
                assert len(list(b.beta.agents.sessions.turns.list(session_id))) == 2
                print("Real copied Template, Skill, File and Agent execution, Artifact read and post-reset continuation passed.", flush=True)
            else:
                print("Real model execution not requested; resource checks only.", flush=True)
            safe(request("GET", "/core/v1/admin/summary"))
            safe(request("GET", "/core/v1/admin/audit-log"))
        finally:
            failures = []
            if session_id is not None:
                try:
                    state = clients[1].beta.agents.sessions.retrieve(session_id)
                    if state.status == "in_progress":
                        clients[1].beta.agents.sessions.events.create(session_id,
                            events=[{"type": "agent.session.input.cancel"}], idempotency_key=str(uuid.uuid4()))
                        deadline = time.monotonic() + 60
                        while state.status == "in_progress" and time.monotonic() < deadline:
                            time.sleep(0.5)
                            state = clients[1].beta.agents.sessions.retrieve(session_id)
                except openai.APIError:
                    failures.append("session_stop")
            # Sessions must finish before deletion; never remove another run's assets.
            priority = {kind: index for index, kind in enumerate(("session", "agent", "environment_template", "skill", "vault", "file"))}
            for key_id, kind, resource in sorted(owned, key=lambda value: priority[value[1]]):
                try:
                    response = http.delete(base + f"/core/v1/admin/api-keys/{key_id}/{ADMIN_PATHS[kind]}/{resource}", headers=admin)
                    if response.status_code not in (200, 204, 404):
                        failures.append(kind)
                except httpx.HTTPError:
                    failures.append(kind)
            for key in keys:
                response = http.delete(base + "/core/v1/admin/api-keys/" + key["id"], headers=admin)
                if response.status_code not in (200, 204):
                    failures.append("key")
                elif http.get(base + "/v1/agents", headers={"Authorization": "Bearer " + key["key"], "OpenAI-Beta": "agents=v1"}).status_code != 401:
                    failures.append("revoked_key_authentication")
            for client in clients:
                client.close()
            if failures:
                raise AssertionError("Owned cleanup incomplete: " + ", ".join(failures)) from None
            print("All owned resources deleted and issued keys revoked.", flush=True)


if __name__ == "__main__":
    try:
        main()
    except openai.APIError as error:
        frames = [frame.lineno for frame in traceback.extract_tb(error.__traceback__) if frame.filename == __file__]
        print("SDK failure at acceptance lines:", frames, file=sys.stderr)
        raise RuntimeError(f"Official SDK failed: {type(error).__name__}, HTTP {getattr(error, 'status_code', 'unavailable')}") from None
