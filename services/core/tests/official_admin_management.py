"""Administrator management and real Project-asset execution against a deployed Core.

Read private JSON settings from stdin: base, admin, model, harness and optional
model_provider (the complete write-only provider object). run_model defaults to
true; false performs resource checks only and never reports model acceptance.
The deployment and its qualified runtime must already be running. This script
creates two Projects, exercises shared keys and Project isolation, and cleans up
only its own resources.
"""
import base64
from datetime import datetime
import io
import json
import secrets
import sys
import time
import traceback
import uuid
import zipfile

import httpx2
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
    owned, keys, clients, projects, revoked = set(), [], [], [], set()
    session_id = None
    with httpx2.Client(trust_env=False, timeout=30) as http:
        def request(method, path, expected=200, token=None, headers=None, **kwargs):
            response = http.request(method, base + path, headers={
                **(admin if token is None else {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}),
                **(headers or {})}, **kwargs)
            assert response.status_code == expected, f"{method} {path}: expected {expected}, got {response.status_code}"
            return response

        def safe(response):
            assert all(value not in response.text for value in secret_values), "Private value in metadata response"
            return response.json()

        def project_path(key):
            return "/core/v1/projects/" + key["project_id"]

        def project_metadata(response):
            value = safe(response)
            assert set(value) == {"id", "name", "created_at", "archived_at", "active_key_count"}
            assert value["created_at"] is not None
            uuid.UUID(value["id"])
            return value

        def issue(project, name):
            key = request("POST", "/core/v1/projects/" + project["id"] + "/keys",
                          expected=201, json={"name": name}).json()
            keys.append(key)
            secret_values.append(key["key"])
            assert set(key) == {"id", "project_id", "name", "prefix", "created_at", "revoked_at", "key"}
            assert key["project_id"] == project["id"] and key["revoked_at"] is None
            assert key["created_at"] is not None
            uuid.UUID(key["id"])
            return key

        def revoke(key):
            request("DELETE", project_path(key) + "/keys/" + key["id"])
            revoked.add(key["id"])

        def operations(key, kind, resource):
            return safe(request("GET", project_path(key) + "/write-operations", params={
                "resource_type": kind, "resource_id": resource, "limit": 100}))["data"]

        def owner(key, kind, resource):
            return safe(request("GET", project_path(key) + "/resource-owners", params={
                "resource_type": kind, "resource_ids": resource}))["data"][0]

        def admin_resource(key, kind, resource):
            return project_path(key) + f"/{ADMIN_PATHS[kind]}/{resource}"

        def remember(key, kind, resource):
            owned.add((key["project_id"], kind, resource))
            return resource

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
                assert state.status != "failed", "Real Session failed"
                assert all(turn.status not in ("failed", "cancelled") for turn in turns), "Real Turn failed"
                if len(turns) >= wanted_turns and turns[-1].status == "completed" and state.status == "idle":
                    return state
                time.sleep(0.5)
            raise AssertionError("Real execution timed out")

        try:
            for name in ("source", "target"):
                created = request("POST", "/core/v1/projects", expected=201, json={"name": "admin-proof-" + name})
                project = created.json()
                projects.append(project)
                project_metadata(created)
                assert project["active_key_count"] == 0 and project["archived_at"] is None
            source = issue(projects[0], "source-first")
            target = issue(projects[1], "target-first")
            peer = issue(projects[0], "source-peer")
            for key in (source, target, peer):
                clients.append(OpenAI(base_url=base + "/v1", api_key=key["key"], max_retries=0,
                    http_client=DefaultHttpxClient(trust_env=False), _strict_response_validation=True))
            a, b, a_peer = clients
            assert source["id"] != peer["id"] and source["project_id"] == peer["project_id"] != target["project_id"]
            catalog = safe(request("GET", "/core/v1/projects", params={"limit": 100}))
            assert isinstance(catalog["has_more"], bool)
            assert {project["id"] for project in projects} <= {project["id"] for project in catalog["data"]}
            for key, count in ((source, 2), (target, 1)):
                listed = safe(request("GET", project_path(key) + "/keys"))
                assert listed["has_more"] is False and len(listed["data"]) == count
                assert all("key" not in item and item["project_id"] == key["project_id"] for item in listed["data"])
            for path in PUBLIC_PATHS.values():
                assert safe(request("GET", "/v1/" + path, token=target["key"]))["data"] == []
            request("GET", "/core/v1/projects", expected=401, token=source["key"])
            request("GET", "/v1/agents", expected=401, headers={"OpenAI-Beta": "agents=v1"})

            file = a.files.create(file=("copy-source.txt", private.encode()), purpose="user_data")
            remember(source, "file", file.id)
            archive = proof_bundle(private)
            with zipfile.ZipFile(io.BytesIO(archive)) as bundle:
                upload_files = [(name, bundle.read(name), "application/octet-stream") for name in bundle.namelist()]
            skill = a.skills.create(files=upload_files)
            remember(source, "skill", skill.id)
            # The peer key edits a Skill created by another key in the same Project.
            for _ in range(2):
                a_peer.skills.versions.create(skill_id=skill.id, files=upload_files, default=False)
            a_peer.skills.update(skill.id, default_version="3")
            a_peer.skills.versions.delete(version="2", skill_id=skill.id)
            template = a_peer.beta.agents.environments.templates.create(
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

            shared = [("agent", agent.id), ("skill", skill.id), ("file", file.id),
                      ("environment_template", template.id), ("vault", vault.id)]
            for key in (source, peer):
                for kind, resource in shared:
                    equal_reads(key, kind, resource)
                    listed = safe(request("GET", "/v1/" + PUBLIC_PATHS[kind], token=key["key"]))
                    assert resource in {item["id"] for item in listed["data"]}
                safe(request("GET", f"/v1/vaults/{vault.id}/credentials/{static.id}", token=key["key"]))
            for key, name in ((peer, "Peer edit"), (source, "Creator edit")):
                a_client = a_peer if key is peer else a
                a_client.beta.agents.update(agent.id, name=name)
                a_client.beta.agents.environments.templates.update(template.id, name=name)
                request("POST", f"/v1/vaults/{vault.id}/credentials/{static.id}", token=key["key"],
                        json={"auth": {"type": "static_bearer", "token": private}})
            for kind, resource, creator in [("agent", agent.id, source), ("skill", skill.id, source),
                    ("file", file.id, source), ("environment_template", template.id, peer),
                    ("vault", vault.id, source), ("credential", static.id, source)]:
                assert owner(source, kind, resource)["api_key"]["id"] == creator["id"]
                writes = operations(source, kind, resource)
                if kind in ("agent", "skill", "environment_template", "credential"):
                    assert {source["id"], peer["id"]} <= {item["api_key"]["id"] for item in writes}
            # Files and Vaults have no update endpoint; exercise cross-key deletion.
            for creator, remover in ((a, a_peer), (a_peer, a)):
                scratch_file = creator.files.create(file=("shared-delete.txt", b"shared"), purpose="user_data")
                remember(source, "file", scratch_file.id)
                remover.files.delete(scratch_file.id)
                scratch_vault = creator.beta.agents.vaults.create(name="Shared deletion")
                remember(source, "vault", scratch_vault.id)
                remover.beta.agents.vaults.delete(scratch_vault.id)
            renamed = project_metadata(request("POST", project_path(source), json={"name": "Renamed project proof"}))
            assert renamed == {**projects[0], "name": "Renamed project proof", "active_key_count": 2}
            projects[0].update(renamed)
            equal_reads(peer, "agent", agent.id)

            for kind, resource in [("agent", agent.id), ("skill", skill.id), ("file", file.id),
                                   ("environment_template", template.id), ("vault", vault.id)]:
                equal_reads(source, kind, resource)
                assert safe(request("GET", "/v1/" + PUBLIC_PATHS[kind], token=target["key"]))["data"] == []
                request("GET", admin_resource(target, kind, resource), expected=404)
                path = f"/v1/{PUBLIC_PATHS[kind]}/{resource}"
                for method in ("GET", "DELETE"):
                    request(method, path, expected=404, token=target["key"])
            for kind, resource, patch in [("agent", agent.id, {"name": "foreign"}),
                                           ("skill", skill.id, {"default_version": "1"}),
                                           ("environment_template", template.id, {"name": "foreign"})]:
                request("POST", f"/v1/{PUBLIC_PATHS[kind]}/{resource}", expected=404, token=target["key"], json=patch)
            for method in ("GET", "DELETE"):
                request(method, f"/v1/skills/{skill.id}/versions/1", expected=404, token=target["key"])
            request("POST", project_path(source) + "/keys/" + source["id"] + "/reset", expected=404)
            request("DELETE", project_path(target) + "/keys/" + source["id"], expected=404)
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
            request("GET", "/v1/vaults/" + target_vault.id, expected=404, token=source["key"])
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
            print("Shared keys, provenance, Project isolation and confidential metadata passed.", flush=True)

            if settings.get("run_model", True):
                # One real Turn with the Project's own assets. Its creation key is
                # revoked while the Turn runs; a replacement key continues the Session.
                session = a.beta.agents.sessions.create(agent_id=agent.id, environment={
                    "type": "openai_hosted", "environment_template_id": template.id},
                    input="Run the installed copy-proof skill's Python verifier now.",
                    extra_headers={"Idempotency-Key": str(uuid.uuid4())})
                session_id = remember(source, "session", session.id)
                creation_key, old_secret = source["id"], source["key"]
                replacement = issue(projects[0], "source-rotation")
                revoke(source)
                revoked_at = next(key for key in safe(request("GET", project_path(replacement) + "/keys"))["data"]
                                  if key["id"] == creation_key)["revoked_at"]
                source = replacement
                a.api_key = replacement["key"]
                request("GET", "/v1/agents/sessions/" + session_id, expected=401, token=old_secret)
                completed = wait_idle(a, session_id, 1)
                turn = list(a.beta.agents.sessions.turns.list(session_id))[0]
                assert turn.completed_at >= int(datetime.fromisoformat(revoked_at).timestamp()), "Turn ended before revocation"
                assert completed.usage is not None and completed.usage.total_tokens > 0, "Real model usage missing"
                artifacts = list(a.beta.agents.sessions.artifacts.list(session_id))
                artifact = next(item for item in artifacts if item.path == "/workspace/outputs/copy-proof.txt")
                assert a.beta.agents.sessions.artifacts.content(artifact.id, session_id=session_id).read() == b"COPY_VERIFIED"
                a.beta.agents.sessions.update(session_id, metadata={"continued_by": "replacement"})
                equal_reads(source, "session", session_id)
                for suffix in ("", "/turns", "/items", "/artifacts", "/artifacts/" + artifact.id):
                    management = request("GET", admin_resource(source, "session", session_id) + suffix)
                    public = request("GET", "/v1/agents/sessions/" + session_id + suffix, token=source["key"])
                    assert management.content == public.content, "Admin Session history differs"
                    request("GET", "/v1/agents/sessions/" + session_id + suffix, expected=404, token=target["key"])
                assert request("GET", admin_resource(source, "session", session_id) + "/artifacts/" + artifact.id + "/content").content == b"COPY_VERIFIED"
                request("POST", "/v1/agents/sessions/" + session_id, expected=404, token=target["key"],
                        json={"metadata": {"forbidden": "foreign-project"}})
                request("DELETE", "/v1/agents/sessions/" + session_id, expected=404, token=target["key"])
                request("POST", "/v1/agents/sessions/" + session_id + "/events", expected=404, token=target["key"],
                        json={"events": [{"type": "agent.session.input.message", "input": [
                            {"role": "user", "content": [{"type": "input_text", "text": "foreign"}]}]}]})
                assert owner(source, "session", session_id)["api_key"]["id"] == creation_key
                assert {creation_key, replacement["id"]} <= {item["api_key"]["id"] for item in operations(source, "session", session_id)}
                key_summary = safe(request("GET", "/core/v1/summary", params={
                    "group_by": "key", "project_id": source["project_id"]}))["data"]
                assert sum(row["sessions"]["total"] for row in key_summary) == 1
                group = next(row for row in key_summary if row["key_id"] == creation_key)
                assert group["project_id"] == source["project_id"] and group["sessions"]["total"] == 1 and group["assets"] is None
                assert group["usage"]["total_tokens"] == a.beta.agents.sessions.retrieve(session_id).usage.total_tokens
                assert all(row["sessions"]["total"] == 0 for row in key_summary if row["key_id"] != creation_key)
                print("Real Project-asset execution, Artifact reads and key rotation continuation passed.", flush=True)
            else:
                print("Real model execution not requested; resource checks only.", flush=True)

            before_archive = {kind: request("GET", admin_resource(source, kind, resource)).content for kind, resource in shared}
            revoke(source)
            request("GET", "/v1/agents", expected=401, token=source["key"])
            equal_reads(peer, "agent", agent.id)
            peer_metadata = safe(request("GET", project_path(peer) + "/keys"))["data"]
            assert next(key for key in peer_metadata if key["id"] == peer["id"])["revoked_at"] is None
            archived = project_metadata(request("POST", project_path(source) + "/archive"))
            assert archived["archived_at"] is not None and archived["active_key_count"] == 0
            assert all(archived[field] == projects[0][field] for field in ("id", "name", "created_at"))
            projects[0].update(archived)
            for key in (source, peer):
                request("GET", "/v1/agents", expected=401, token=key["key"])
            assert all(key["revoked_at"] is not None for key in safe(request("GET", project_path(source) + "/keys"))["data"])
            for kind, resource in shared:
                assert request("GET", admin_resource(source, kind, resource)).content == before_archive[kind]
            request("POST", project_path(source) + "/keys", expected=409, json={"name": "archived"})
            for group_by in ("project", "agent", "key"):
                summary = safe(request("GET", "/core/v1/summary", params={
                    "group_by": group_by, "project_id": target["project_id"]}))
                assert all(row["project_id"] == target["project_id"] for row in summary["data"])
            safe(request("GET", "/core/v1/audit-log", params={"project_id": target["project_id"]}))
            print("Project rename, archive and retained reads passed.", flush=True)
        finally:
            failures = []
            if session_id is not None:
                # Read through the administrator route; cancel only with a key this run
                # has not revoked, in a Project that is not archived.
                session_path = base + f"/core/v1/projects/{projects[0]['id']}/sessions/{session_id}"
                try:
                    state = http.get(session_path, headers=admin).json()["status"]
                    if state == "in_progress":
                        live = [key for key in keys if key["project_id"] == projects[0]["id"] and key["id"] not in revoked]
                        if projects[0]["archived_at"] is not None or not live:
                            raise RuntimeError("no usable key")
                        http.post(base + f"/v1/agents/sessions/{session_id}/events", json={"events": [{"type": "agent.session.input.cancel"}]},
                                  headers={"Authorization": "Bearer " + live[0]["key"], "OpenAI-Beta": "agents=v1", "Idempotency-Key": str(uuid.uuid4())})
                        deadline = time.monotonic() + 60
                        while state == "in_progress" and time.monotonic() < deadline:
                            time.sleep(0.5)
                            state = http.get(session_path, headers=admin).json()["status"]
                        if state == "in_progress":
                            failures.append("session_stop")
                except (httpx2.HTTPError, KeyError, ValueError, RuntimeError):
                    failures.append("session_stop")
            # Sessions must finish before deletion; never remove another run's assets.
            priority = {kind: index for index, kind in enumerate(("session", "agent", "environment_template", "skill", "vault", "file"))}
            for project_id, kind, resource in sorted(owned, key=lambda value: priority[value[1]]):
                try:
                    response = http.delete(base + f"/core/v1/projects/{project_id}/{ADMIN_PATHS[kind]}/{resource}", headers=admin)
                    if response.status_code not in (200, 204, 404):
                        failures.append(kind)
                except httpx2.HTTPError:
                    failures.append(kind)
            for project in projects:
                if project["archived_at"] is None:
                    response = http.post(base + "/core/v1/projects/" + project["id"] + "/archive", headers=admin)
                    if response.status_code != 200:
                        failures.append("project_archive")
            for key in keys:
                if http.get(base + "/v1/agents", headers={"Authorization": "Bearer " + key["key"], "OpenAI-Beta": "agents=v1"}).status_code != 401:
                    failures.append("revoked_key_authentication")
            for client in clients:
                client.close()
            if failures:
                raise AssertionError("Owned cleanup incomplete: " + ", ".join(failures)) from None
            print("All owned resources deleted, Projects archived and issued keys revoked.", flush=True)


if __name__ == "__main__":
    try:
        main()
    except openai.APIError as error:
        frames = [frame.lineno for frame in traceback.extract_tb(error.__traceback__) if frame.filename == __file__]
        print("SDK failure at acceptance lines:", frames, file=sys.stderr)
        raise RuntimeError(f"Official SDK failed: {type(error).__name__}, HTTP {getattr(error, 'status_code', 'unavailable')}") from None
