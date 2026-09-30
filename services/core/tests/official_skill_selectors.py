"""Pinned SDK/raw Template selector acceptance on Core and real PostgreSQL.

This verifies resource admission/projection, not Session resolution or native use.
"""
import base64
import json
import secrets
import sys

import httpx2
import openai
from openai import DefaultHttpxClient, OpenAI

from official_skills import bundle


def main():
    assert openai.__version__ == "3.13.0", openai.__version__
    settings = json.load(sys.stdin)
    options = dict(max_retries=0, _strict_response_validation=True)
    client = OpenAI(base_url=settings["base"] + "/v1", api_key=settings["token"], http_client=DefaultHttpxClient(trust_env=False), **options)
    recovered = OpenAI(base_url=settings["recovered"] + "/v1", api_key=settings["token"], http_client=DefaultHttpxClient(trust_env=False), **options)
    api = client.beta.agents.environments.templates
    restarted = recovered.beta.agents.environments.templates
    base = settings["base"] + "/v1"
    path = "/agents/environments/templates"
    headers = {"Authorization": "Bearer " + settings["token"], "OpenAI-Beta": "agents=v1"}
    foreign = {**headers, "Authorization": "Bearer " + settings["foreign"]}
    marker = "private-skill-selector-" + secrets.token_hex(16)
    archive, manifest = bundle(marker)
    private_values = [marker, base64.b64encode(archive).decode(), settings["token"], settings["foreign"]]
    owned = []
    skill_id = None

    def safe(response):
        assert all(value not in response.text for value in private_values), "private content in response"

    def projection(response, selector, status):
        assert response.status_code == status, response.text
        safe(response.http_response)
        body = response.http_response.json()
        assert set(body) == {"id", "object", "created_at", "updated_at", "name", "network", "packages", "capability_directories", "files", "plugins", "skills"}
        assert body["skills"] == [{"type": "skill_reference", "skill_id": skill_id, "version": selector}], body["skills"]
        parsed = response.parse()
        assert parsed.skills[0].version == selector
        assert parsed.skills[0].skill_id == skill_id
        return parsed.id

    with httpx2.Client(timeout=20, trust_env=False) as http:
        try:
            uploaded = client.skills.with_raw_response.create(files=[("proof/SKILL.md", manifest, "text/markdown")])
            skill_id = uploaded.parse().id
            safe(uploaded.http_response)
            for selector_fields in ({}, {"version": None}):
                reference = {"type": "skill_reference", "skill_id": skill_id, **selector_fields}
                created = api.with_raw_response.create(skills=[reference])
                # Register ownership before projection assertions so failure still cleans up.
                template_id = created.http_response.json()["id"]
                owned.append(template_id)
                assert projection(created, None, 201) == template_id
                assert projection(api.with_raw_response.retrieve(template_id), None, 200) == template_id
                for fields, expected in (({}, None), ({"version": None}, None), ({"version": "latest"}, "latest"), ({"version": "1"}, "1")):
                    reference = {"type": "skill_reference", "skill_id": skill_id, **fields}
                    projection(api.with_raw_response.update(template_id, skills=[reference]), expected, 200)
                    projection(restarted.with_raw_response.retrieve(template_id), expected, 200)
                    raw = http.get(base + path + "/" + template_id, headers=headers)
                    assert raw.status_code == 200
                    safe(raw)
                    assert raw.json()["skills"] == [{"type": "skill_reference", "skill_id": skill_id, "version": expected}]
                # Omitting the whole skills field leaves the existing selector intact.
                projection(api.with_raw_response.update(template_id), "1", 200)
                for method, body in (("GET", None), ("POST", {"skills": [{"type": "skill_reference", "skill_id": skill_id, "version": None}]}), ("DELETE", None)):
                    response = http.request(method, base + path + "/" + template_id, headers=foreign, json=body)
                    assert response.status_code == 404
                    safe(response)
                    assert response.json()["error"]["type"] == "not_found_error"
                projection(api.with_raw_response.retrieve(template_id), "1", 200)
            for suffix in ("", "/content", "/versions/1", "/versions/1/content"):
                response = http.get(base + "/skills/" + skill_id + suffix, headers=foreign)
                assert response.status_code == 404
                safe(response)
                assert response.json()["error"] == {"message": "Resource not found.", "type": "invalid_request_error", "code": None, "param": None}
            print(json.dumps({"sdk": openai.__version__, "template_selectors": ["omitted", "null", "latest", "1"], "postgres": True, "sessions": 0, "native_model": False, "result": "passed"}))
        finally:
            for template_id in owned:
                api.delete(template_id)
            if skill_id is not None:
                client.skills.delete(skill_id)
            client.close()
            recovered.close()


if __name__ == "__main__":
    main()
