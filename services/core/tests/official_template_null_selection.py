"""Template selection acceptance over Core HTTP and PostgreSQL, without execution."""

import base64
import copy
import hashlib
import importlib.metadata
import io
import json
import secrets
import sys
import zipfile
from contextlib import ExitStack
from pathlib import Path

import httpx2
from openai import DefaultHttpxClient, OpenAI


def bundle(files):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, content in files.items():
            archive.writestr("proof/" + name, content)
    return output.getvalue()


def skill_manifest(label, marker):
    return ("---\nname: selection-" + label + "\ndescription: Verify " + label +
            " selection.\n---\n" + marker + "-" + label).encode()


def inline_skill(label, marker):
    archive = bundle({"SKILL.md": skill_manifest(label, marker)})
    return {"type": "inline", "name": "selection-" + label,
            "description": "Verify " + label + " selection.",
            "source": {"type": "base64", "media_type": "application/zip",
                       "data": base64.b64encode(archive).decode()}}, archive


def inline_plugin(label, marker):
    metadata = {"name": "plugin-" + label, "description": "Verify " + label + " Plugin selection."}
    archive = bundle({".codex-plugin/plugin.json": json.dumps({**metadata, "skills": ["./skills"]}),
                      "skills/proof/SKILL.md": skill_manifest("plugin-" + label, marker)})
    return {"type": "inline", **metadata,
            "source": {"type": "base64", "media_type": "application/zip",
                       "data": base64.b64encode(archive).decode()}}, archive


def main():
    settings = json.load(sys.stdin)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    assert distribution.version == pin["sdk_version"] == "3.13.0"
    assert json.loads(distribution.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    marker = settings["canary"]
    sessions, expected, replays, rejected_keys = {}, {}, {}, []
    templates, sources, skills = [], [], []
    private = [marker, settings["token"], settings["foreign"]]
    succeeded = False
    with ExitStack() as stack:
        raw = stack.enter_context(httpx2.Client(trust_env=False, timeout=20))

        def sdk(base, key):
            return stack.enter_context(OpenAI(base_url=base + "/v1", api_key=key, max_retries=0,
                _strict_response_validation=True, http_client=DefaultHttpxClient(trust_env=False)))

        owner = sdk(settings["base"], settings["token"])
        foreign = sdk(settings["base"], settings["foreign"])
        reopened = sdk(settings["recovered"], settings["token"])
        api = owner.beta.agents.environments.templates
        headers = {"Authorization": "Bearer " + settings["token"], "OpenAI-Beta": "agents=v1"}

        def safe(response):
            assert all(value not in response.text for value in private), "confidential response content"

        def request(body, key=None, token=None, base=None):
            response = raw.post((base or settings["base"]) + "/v1/agents/sessions", json=body,
                               headers={**headers, "Authorization": "Bearer " + (token or settings["token"]),
                                        "Idempotency-Key": key or secrets.token_hex(20)})
            safe(response)
            return response

        def reject(body, status, token=None):
            key = secrets.token_hex(20)
            rejected_keys.append(key)
            response = request(body, key, token)
            assert response.status_code == status, (response.status_code, response.text)
            return response.json()

        def projection(client, session_id, selection):
            response = client.beta.agents.sessions.with_raw_response.retrieve(session_id)
            safe(response.http_response)
            assert response.status_code == 200
            parsed = response.parse()
            body = response.http_response.json()
            environment = body["environment"]
            for field in ("skills", "plugins", "capability_directories", "network"):
                assert environment[field] == selection[field], (field, environment[field], selection[field])
                assert parsed.environment.to_dict()[field] == selection[field]
            assert "env" not in environment and "setup_commands" not in environment
            assert len(environment["files"]) == 1
            file = environment["files"][0]
            assert file["path"] == "/workspace/source.txt" and file["file_id"] == source.id
            assert file["size_bytes"] == len(marker + "-source")
            resource = client.beta.agents.environments.with_raw_response.retrieve(environment["id"])
            safe(resource.http_response)
            assert resource.status_code == 200
            assert resource.parse().to_dict()["skills"] == selection["skills"]
            for field in ("skills", "plugins", "files"):
                assert resource.http_response.json()[field] == environment[field]
            assert list(client.beta.agents.sessions.turns.list(session_id)) == []
            return body

        def create(label, body, selection, sdk_create=False):
            key = secrets.token_hex(20)
            if sdk_create:
                result = owner.beta.agents.sessions.with_raw_response.create(**body, extra_headers={"Idempotency-Key": key})
                response = result.http_response
                result.parse()
                safe(response)
            else:
                response = request(body, key)
            if response.status_code == 201:
                sessions[label] = response.json()["id"]
            assert response.status_code == 201, (label, response.status_code, response.text)
            frozen = projection(owner, sessions[label], selection)
            assert response.json() == frozen
            expected[label] = selection
            replays[label] = (body, key, frozen)

        try:
            source = owner.files.create(file=("source.txt", (marker + "-source").encode()), purpose="user_data")
            sources.append(source.id)
            first = owner.skills.create(files=[("proof/SKILL.md", skill_manifest("template", marker), "text/markdown")])
            skills.append((owner, first.id))
            foreign_skill = foreign.skills.create(files=[("proof/SKILL.md", skill_manifest("foreign", marker), "text/markdown")])
            skills.append((foreign, foreign_skill.id))
            first_archive = owner.skills.versions.content.retrieve(skill_id=first.id, version="1").read()
            plugin, plugin_archive = inline_plugin("template", marker)
            replacement, replacement_archive = inline_skill("replacement", marker)
            replacement_plugin, replacement_plugin_archive = inline_plugin("replacement", marker)
            private.extend(item["source"]["data"] for item in (plugin, replacement, replacement_plugin))
            reference = {"type": "skill_reference", "skill_id": first.id}
            template_skills = [{**reference, "version": "1", "name": first.name, "description": first.description}]
            plugin_metadata = [{key: plugin[key] for key in ("type", "name", "description")}]
            network = {"access": "enabled", "allowed_domains": []}
            files = [{"type": "file_id", "path": "/workspace/source.txt", "file_id": source.id}]
            template = api.with_raw_response.create(name="selection", network=network, env={"PRIVATE_SELECTION": marker},
                files=files, skills=[reference], plugins=[plugin], capability_directories=["/workspace/template-capabilities"])
            template_id = template.http_response.json()["id"]
            templates.append(template_id)
            safe(template.http_response)
            template.parse()
            original = api.retrieve(template_id).to_dict()
            base = {"agent": {"model": "selection-fixture"},
                    "environment": {"type": "openai_hosted", "environment_template_id": template_id}}
            default = {"skills": template_skills, "plugins": plugin_metadata,
                       "capability_directories": ["/workspace/template-capabilities"], "network": network,
                       "skill_digests": [hashlib.sha256(first_archive).hexdigest()],
                       "plugin_digests": [hashlib.sha256(plugin_archive).hexdigest()]}
            replaced = {"skills": [{key: replacement[key] for key in ("type", "name", "description")}],
                        "plugins": [{key: replacement_plugin[key] for key in ("type", "name", "description")}],
                        "capability_directories": ["/workspace/replacement-capabilities"],
                        "network": {"access": "enabled", "allowed_domains": []},
                        "skill_digests": [hashlib.sha256(replacement_archive).hexdigest()],
                        "plugin_digests": [hashlib.sha256(replacement_plugin_archive).hexdigest()]}
            rows = [
                ("omitted", {}, default),
                ("null", {key: None for key in ("network", "skills", "plugins", "capability_directories")}, default),
                ("empty", {key: [] for key in ("skills", "plugins", "capability_directories")},
                 {**default, "skills": [], "plugins": [], "capability_directories": [], "skill_digests": [], "plugin_digests": []}),
                ("populated", {"network": replaced["network"], "skills": [replacement], "plugins": [replacement_plugin],
                               "capability_directories": replaced["capability_directories"]}, replaced),
                ("mixed-skill", {"skills": None, "plugins": [], "capability_directories": replaced["capability_directories"]},
                 {**default, "plugins": [], "plugin_digests": [], "capability_directories": replaced["capability_directories"]}),
                ("mixed-plugin", {"skills": [], "plugins": None, "capability_directories": None},
                 {**default, "skills": [], "skill_digests": []}),
            ]
            for label, overrides, selection in rows:
                create(label, {**base, "environment": {**base["environment"], **overrides}}, selection, sdk_create=label == "null")
            assert api.retrieve(template_id).to_dict() == original
            # Core retains caller-declared paths. Official managed internal paths
            # are a qualified projection difference, not values to copy locally.
            denied = reject(base, 404, settings["foreign"])
            missing = copy.deepcopy(base)
            missing["environment"]["environment_template_id"] = "00000000-0000-0000-0000-000000000001"
            assert reject(missing, 404, settings["foreign"]) == denied
            reject({**base, "environment": {**base["environment"], "network": {"access": "restricted", "allowed_domains": ["example.com"]}}}, 400)
            reject({**base, "environment": {**base["environment"], "skills": [{"type": "skill_reference", "skill_id": foreign_skill.id}]}}, 404)
            reject({**base, "environment": {**base["environment"], "capability_directories": ["/outside"]}}, 400)
            malformed = copy.deepcopy(plugin)
            malformed["source"]["data"] = base64.b64encode(b"invalid ZIP").decode()
            reject({**base, "environment": {**base["environment"], "plugins": [malformed]}}, 400)
            disabled = {"access": "disabled", "allowed_domains": []}
            api.update(template_id, network={"access": "disabled"})
            assert api.update(template_id, name="network omission preserves").network.to_dict() == disabled
            reject(base, 400)
            reject({**base, "environment": {**base["environment"], "network": None}}, 400)
            reject({**base, "environment": {**base["environment"], "network": {"access": "enabled"}}}, 400)
            enabled = {"access": "enabled", "allowed_domains": []}
            assert api.update(template_id, network=None).network.to_dict() == enabled
            create("reset-enabled", {**base, "environment": {**base["environment"], "network": None}}, {**default, "network": enabled})
            inline = {"agent": base["agent"], "environment": {"type": "openai_hosted", "network": None,
                      "env": {"PRIVATE_SELECTION": marker}, "files": files}}
            create("inline-null", inline, {**default, "skills": [], "plugins": [], "capability_directories": [],
                                           "skill_digests": [], "plugin_digests": [], "network": enabled})
            foreign_ref = {"type": "skill_reference", "skill_id": foreign_skill.id}
            api.update(template_id, skills=[foreign_ref])
            reject({**base, "environment": {**base["environment"], "skills": None}}, 404)
            create("excluded-source", {**base, "environment": {**base["environment"], "skills": []}},
                   {**default, "skills": [], "skill_digests": [], "network": enabled})
            owner.skills.versions.create(skill_id=first.id, default=True,
                files=[("proof/SKILL.md", skill_manifest("mutated", marker), "text/markdown")])
            api.update(template_id, skills=[], plugins=[], capability_directories=[], env={}, files=[])
            api.delete(template_id)
            templates.remove(template_id)
            owner.skills.delete(first.id)
            skills.remove((owner, first.id))
            owner.files.delete(source.id)
            sources.remove(source.id)
            for label, (body, key, frozen) in replays.items():
                response = request(body, key, base=settings["recovered"])
                assert response.status_code == 201 and response.json() == frozen, label
                assert projection(reopened, sessions[label], expected[label]) == frozen
            changed = copy.deepcopy(replays["null"][0])
            changed["environment"]["skills"] = []
            assert request(changed, replays["null"][1], base=settings["recovered"]).status_code == 409
            succeeded = True
            print(json.dumps({"sessions": sessions, "expected": expected, "rejected_keys": rejected_keys,
                              "result": "passed", "postgres": True, "native_model_execution": False}))
        finally:
            for template_id in templates:
                api.delete(template_id)
            for source_id in sources:
                owner.files.delete(source_id)
            for client, skill_id in skills:
                client.skills.delete(skill_id)
            if not succeeded:
                for session_id in sessions.values():
                    owner.beta.agents.sessions.delete(session_id)


if __name__ == "__main__":
    main()
