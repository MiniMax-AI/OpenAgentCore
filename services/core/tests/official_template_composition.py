"""Public template composition on Core HTTP/PostgreSQL; no native/model execution."""

import base64
import copy
import importlib.metadata
import json
import secrets
import sys
from contextlib import ExitStack
from pathlib import Path

import httpx2
from openai import DefaultHttpxClient, OpenAI


def main():
    settings = json.load(sys.stdin)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    assert distribution.version == pin["sdk_version"]
    assert json.loads(distribution.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    marker = settings["canary"]
    packages = {"python": ["packaging==25.0"], "npm": ["semver@7.7.2"]}
    populated_packages = {"python": ["idna==3.10"], "npm": []}
    sessions, rejected_keys, templates, files = {}, [], [], []
    succeeded = False

    def inline(path, suffix):
        return {"type": "inline", "path": path, "data": base64.b64encode((marker + suffix).encode()).decode()}

    with ExitStack() as stack:
        raw = stack.enter_context(httpx2.Client(trust_env=False, timeout=20))

        def sdk(base, token):
            return stack.enter_context(OpenAI(
                base_url=base + "/v1", api_key=token, max_retries=0,
                _strict_response_validation=True, http_client=DefaultHttpxClient(trust_env=False),
            ))

        owner = sdk(settings["base"], settings["token"])
        foreign = sdk(settings["base"], settings["foreign"])
        reopened = sdk(settings["recovered"], settings["token"])
        api = owner.beta.agents.environments.templates
        headers = {"Authorization": "Bearer " + settings["token"], "OpenAI-Beta": "agents=v1"}

        def safe(response):
            assert all(value not in response.text for value in (marker, settings["token"], settings["foreign"])), "private response content"
            assert "\"data\": \"" not in response.text, "inline source data in resource response"

        def request(body, *, key=None, token=None, base=None):
            response = raw.post((base or settings["base"]) + "/v1/agents/sessions", json=body,
                                headers={**headers, "Authorization": "Bearer " + (token or settings["token"]),
                                         "Idempotency-Key": key or secrets.token_hex(20)})
            safe(response)
            return response

        def rejected(body, status, *, token=None, param=None):
            key = secrets.token_hex(20)
            rejected_keys.append(key)
            response = request(body, key=key, token=token)
            assert response.status_code == status, (status, response.status_code, response.text)
            assert response.json()["error"]["param"] == param
            return response.json()

        def projection(client, session_id, expected_files, expected_packages):
            expected_packages = {**expected_packages, "system": []}
            response = client.beta.agents.sessions.with_raw_response.retrieve(session_id)
            assert response.status_code == 200
            safe(response.http_response)
            body = response.http_response.json()
            parsed = response.parse()
            environment = body["environment"]
            assert environment["packages"] == expected_packages
            assert parsed.environment.packages.to_dict() == expected_packages
            assert "env" not in environment and "setup_commands" not in environment
            actual = environment["files"]
            assert len(actual) == len(expected_files)
            for file, expected in zip(actual, expected_files):
                assert file["id"] and all(file[k] == value for k, value in expected.items()), file
                assert set(file) == set(expected) | {"id"}, file
            env = client.beta.agents.environments.with_raw_response.retrieve(environment["id"])
            assert env.status_code == 200
            safe(env.http_response)
            assert env.http_response.json()["files"] == actual
            assert [file.to_dict() for file in env.parse().files] == actual
            assert list(client.beta.agents.sessions.turns.list(session_id)) == []
            return body

        try:
            source = owner.files.create(file=("owned.txt", (marker + "-source").encode()), purpose="user_data")
            files.append((owner, source.id))
            other = foreign.files.create(file=("foreign.txt", (marker + "-foreign").encode()), purpose="user_data")
            files.append((foreign, other.id))
            template_files = [inline("/workspace/template-only.txt", "-template-file"),
                              {"type": "file_id", "path": "/workspace/overlap.txt", "file_id": source.id}]
            populated_files = [inline("/workspace/overlap.txt", "-inline-file"),
                               {"type": "file_id", "path": "/workspace/selected-source.txt", "file_id": source.id}]
            template_body = {
                "name": "composition", "network": {"access": "enabled"}, "files": template_files,
                "env": {"TEMPLATE_ONLY": marker + "-template-env", "SHARED": marker + "-template-shared"},
                "setup_commands": [{"command": "printf " + marker + "-template-command", "cwd": "/workspace"}],
                "packages": packages,
            }
            created = api.with_raw_response.create(**template_body)
            template_id = created.http_response.json()["id"]
            templates.append(template_id)
            assert created.status_code == 201
            safe(created.http_response)
            template_snapshot = created.parse().to_dict()
            assert template_snapshot["packages"] == {**packages, "system": []}
            base = {"agent": {"model": "composition-fixture"},
                    "environment": {"type": "openai_hosted", "environment_template_id": template_id}}
            default_files = [
                {"type": "inline", "path": "/workspace/template-only.txt", "size_bytes": len(marker + "-template-file")},
                {"type": "file_id", "path": "/workspace/overlap.txt", "file_id": source.id, "size_bytes": len(marker + "-source")},
            ]
            selected_files = [
                {"type": "inline", "path": "/workspace/overlap.txt", "size_bytes": len(marker + "-inline-file")},
                {"type": "file_id", "path": "/workspace/selected-source.txt", "file_id": source.id, "size_bytes": len(marker + "-source")},
            ]
            rows = [
                ("omitted", {}, default_files, packages),
                ("populated", {"env": {"SHARED": marker + "-inline-shared", "INLINE_ONLY": marker + "-inline-env"},
                               "setup_commands": [{"command": "printf " + marker + "-inline-one"},
                                                  {"command": "printf " + marker + "-inline-two", "cwd": "/workspace"}],
                               "files": populated_files, "packages": {"python": ["idna==3.10"], "npm": []}}, selected_files, populated_packages),
                ("empty", {"env": {}, "setup_commands": [], "files": [], "packages": {}}, [], packages),
                ("null", {"env": None, "setup_commands": None, "files": None, "packages": None}, default_files, packages),
                ("nested-null", {"packages": {"python": None, "npm": None}}, default_files, packages),
            ]
            for label, overrides, expected_files, expected_packages in rows:
                body = {**base, "environment": {**base["environment"], **overrides}}
                key = secrets.token_hex(20)
                response = request(body, key=key)
                if response.status_code == 201:
                    sessions[label] = response.json()["id"]
                assert response.status_code == 201, (label, response.status_code, response.text)
                projected = projection(owner, sessions[label], expected_files, expected_packages)
                assert response.json() == projected
                if label == "populated":
                    retry_body, retry_key, frozen = body, key, projected
            assert api.retrieve(template_id).to_dict() == template_snapshot

            for system in (None, [], ["jq"]):
                invalid = {**base, "environment": {**base["environment"], "packages": {"system": system}}}
                rejected(invalid, 400, param="packages.system")
                response = raw.post(settings["base"] + "/v1/agents/environments/templates",
                                    json={"packages": {"system": system}}, headers=headers)
                safe(response)
                assert response.status_code == 400, response.text
                assert response.json()["error"]["param"] == "packages.system"
            denied = rejected(base, 404, token=settings["foreign"])
            missing = copy.deepcopy(base)
            missing["environment"]["environment_template_id"] = "00000000-0000-0000-0000-000000000001"
            assert rejected(missing, 404, token=settings["foreign"]) == denied
            foreign_files = [{"type": "file_id", "path": "/workspace/foreign.txt", "file_id": other.id}]
            rejected({**base, "environment": {**base["environment"], "files": foreign_files}}, 404)
            rejected({**base, "environment": {**base["environment"], "env": {"PATH": marker}}}, 400)
            rejected({**base, "environment": {**base["environment"], "files": [inline("/workspace/../escape", "-bad")]}}, 400)
            # Referenced source ownership is checked only for the effective file list.
            api.update(template_id, files=foreign_files)
            rejected(base, 404)
            response = request({**base, "environment": {**base["environment"], "files": []}})
            if response.status_code == 201:
                sessions["excluded-source"] = response.json()["id"]
            assert response.status_code == 201, response.text
            projection(owner, sessions["excluded-source"], [], packages)
            # Individually valid maps must not bypass the combined setup-size limit.
            api.update(template_id, env={"LARGE_TEMPLATE": "t" * (300 * 1024)})
            rejected({**base, "environment": {**base["environment"], "files": [],
                                              "env": {"LARGE_INLINE": "i" * (300 * 1024)}}}, 400)
            api.delete(template_id)
            templates.remove(template_id)
            owner.files.delete(source.id)
            files.remove((owner, source.id))
            replay = request(retry_body, key=retry_key, base=settings["recovered"])
            assert replay.status_code == 201 and replay.json() == frozen, replay.text
            assert projection(reopened, sessions["populated"], selected_files, populated_packages) == frozen
            changed = copy.deepcopy(retry_body)
            changed["environment"]["env"]["SHARED"] = marker + "-changed"
            assert request(changed, key=retry_key, base=settings["recovered"]).status_code == 409
            succeeded = True
            print(json.dumps({"sessions": sessions, "rejected_keys": rejected_keys,
                              "result": "passed", "postgres": True, "native_model_execution": False}))
        finally:
            for template_id in templates:
                api.delete(template_id)
            for client, file_id in files:
                client.files.delete(file_id)
            if not succeeded:
                for session_id in sessions.values():
                    owner.beta.agents.sessions.delete(session_id)
            # Successful Sessions stay alive for the Go owner's private snapshot assertions.


if __name__ == "__main__":
    main()
