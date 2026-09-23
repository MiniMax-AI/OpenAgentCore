"""Pinned official SDK and raw HTTP acceptance for Core-owned Skill resources.

This exercises a real API and PostgreSQL; native model/installation acceptance is
separate and must not be inferred from these resource checks.
"""
import io
import json
import secrets
import sys
import zipfile

import httpx
import openai
from openai import DefaultHttpxClient, OpenAI


def bundle(marker):
    content = ("---\nname: proof\ndescription: Verify a versioned Skill.\n---\n" + marker).encode()
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("proof/SKILL.md", content)
        archive.writestr("proof/scripts/proof.py", b"print('proof')")
    return output.getvalue(), content


def main():
    assert openai.__version__ == "3.13.0", openai.__version__
    settings = json.load(sys.stdin)
    client = OpenAI(base_url=settings["base"] + "/v1", api_key=settings["token"], max_retries=0, http_client=DefaultHttpxClient(trust_env=False), _strict_response_validation=True)
    recovered = OpenAI(base_url=settings["recovered"] + "/v1", api_key=settings["token"], max_retries=0, http_client=DefaultHttpxClient(trust_env=False), _strict_response_validation=True)
    headers = {"Authorization": "Bearer " + settings["token"]}
    foreign = {"Authorization": "Bearer " + settings["foreign"]}
    base = settings["base"] + "/v1"
    marker = "confidential-skill-" + secrets.token_hex(20)
    data, manifest = bundle(marker)
    owned = []
    with httpx.Client(timeout=20, trust_env=False) as http:
        try:
            raw = client.skills.with_raw_response.create(files=[("proof/SKILL.md", manifest, "text/markdown"), ("proof/scripts/proof.py", b"print('proof')", "text/plain")])
            skill = raw.parse()
            owned.append(skill.id)
            assert "OpenAI-Beta" not in raw.http_response.request.headers
            assert set(raw.http_response.json()) == {"id", "object", "name", "description", "created_at", "default_version", "latest_version"}
            assert skill.object == "skill" and skill.default_version == skill.latest_version == "1"
            assert marker not in raw.http_response.text
            data = client.skills.content.retrieve(skill.id).read()
            with zipfile.ZipFile(io.BytesIO(data)) as archive:
                assert archive.read("proof/SKILL.md") == manifest
            first = client.skills.versions.retrieve(version="1", skill_id=skill.id)
            assert first.version == "1" and first.object == "skill.version"
            assert client.skills.versions.content.retrieve(version="1", skill_id=skill.id).read() == data
            _, second_manifest = bundle("second-" + marker)
            second = client.skills.versions.create(skill_id=skill.id, files=[("proof/SKILL.md", second_manifest, "text/markdown")], default=False)
            second_data = client.skills.versions.content.retrieve(version=second.version, skill_id=skill.id).read()
            assert second.version == "2"
            assert client.skills.retrieve(skill.id).default_version == "1"
            assert client.skills.retrieve(skill.id).latest_version == "2"
            assert client.skills.update(skill.id, default_version="2").default_version == "2"
            assert recovered.skills.content.retrieve(skill.id).read() == second_data
            # Raw HTTP covers the single-ZIP form; the pinned SDK loses a single
            # FileTypes value during array extraction before sending its request.
            zip_data, _ = bundle("raw-" + marker)
            uploaded = http.post(base + "/skills", headers=headers, files={"files": ("proof.zip", zip_data, "application/zip")})
            assert uploaded.status_code == 200
            directory = client.skills.retrieve(uploaded.json()["id"])
            owned.append(directory.id)
            downloaded = client.skills.versions.content.retrieve(version="1", skill_id=directory.id).read()
            with zipfile.ZipFile(io.BytesIO(downloaded)) as archive:
                assert downloaded == zip_data
                assert archive.read("proof/scripts/proof.py") == b"print('proof')"
            page = client.skills.list(limit=1, order="asc")
            assert page.data[0].id == skill.id and page.has_more
            assert client.skills.list(limit=1, order="asc", after=skill.id).data[0].id == directory.id
            versions = client.skills.versions.list(skill.id, limit=1, order="asc")
            assert versions.data[0].id == first.id and versions.has_more
            assert client.skills.versions.list(skill.id, limit=1, order="asc", after=first.id).data[0].id == second.id
            endpoints = ["", "/content", "/versions", "/versions/1", "/versions/1/content"]
            for suffix in endpoints:
                assert http.get(base + "/skills/" + skill.id + suffix, headers=foreign).status_code == 404
            assert http.get(base + "/skills", headers=foreign, params={"after": skill.id}).status_code == 404
            assert http.post(base + "/skills/" + skill.id, headers=foreign, json={"default_version": "1"}).status_code == 404
            assert http.delete(base + "/skills/" + skill.id, headers=foreign).status_code == 404
            assert http.delete(base + "/skills/" + skill.id + "/versions/1", headers=foreign).status_code == 404
            assert http.post(base + "/skills/" + skill.id + "/versions", headers=foreign, files={"files": ("proof.zip", data)}).status_code == 404
            assert http.get(base + "/skills").status_code == 401
            assert http.post(base + "/skills/" + skill.id, headers=headers, json={"default_version": 1}).status_code == 400
            # Verify a malformed trailing field cannot commit a partial upload.
            before = [item.id for item in client.skills.list()]
            malformed = http.post(base + "/skills", headers=headers, files=[("files", ("proof.zip", data)), ("unknown", (None, "reject"))])
            assert malformed.status_code == 400
            assert [item.id for item in client.skills.list()] == before
            deleted = client.skills.versions.delete(version="1", skill_id=skill.id)
            assert deleted.id == first.id and deleted.version == "1" and deleted.object == "skill.version.deleted" and deleted.deleted
            # Deleting the only remaining version also deletes the Skill.
            sole = client.skills.create(files=[("proof/SKILL.md", manifest, "text/markdown")])
            owned.append(sole.id)
            sole_path = base + "/skills/" + sole.id
            assert http.delete(sole_path + "/versions/1", headers=foreign).status_code == 404
            sole_version = client.skills.versions.retrieve(version="1", skill_id=sole.id)
            removed = client.skills.versions.with_raw_response.delete(version="1", skill_id=sole.id)
            assert removed.http_response.json() == {"id": sole_version.id, "object": "skill.version.deleted", "deleted": True, "version": "1"}
            assert removed.parse().deleted
            owned.remove(sole.id)
            for suffix in ("", "/content", "/versions", "/versions/1"):
                assert http.get(sole_path + suffix, headers=headers).status_code == 404
            try:
                client.skills.retrieve(sole.id)
            except openai.NotFoundError:
                pass
            else:
                raise AssertionError("Skill survived deletion of its only version")
            assert sole.id not in [item.id for item in client.skills.list()]
            assert client.skills.delete(directory.id).deleted
            owned.remove(directory.id)
            assert http.get(base + "/skills/" + directory.id + "/versions/1/content", headers=headers).status_code == 404
            print(json.dumps({"sdk": openai.__version__, "resource_operations": 11, "uploads": ["zip", "directory"], "postgres": True, "native_model": False, "result": "passed"}))
        finally:
            for skill_id in owned:
                client.skills.delete(skill_id)


if __name__ == "__main__":
    main()
