"""Qualified Files and Skills semantics over real Core HTTP and PostgreSQL."""

import importlib.metadata
import io
import json
import secrets
import sys
import zipfile
from contextlib import ExitStack
from pathlib import Path

import httpx2
from openai import BadRequestError, DefaultHttpxClient, OpenAI


def assert_error(response, status, param, code=None):
    assert response.status_code == status, (response.status_code, response.text)
    body = response.json()["error"]
    assert set(body) == {"type", "code", "param", "message"}, body
    assert (body["type"], body["code"], body["param"]) == ("invalid_request_error", code, param), body
    assert isinstance(body["message"], str) and body["message"], body
    return body


def check_files(raw, owner, outsider, recovered, settings, cleanup):
    marker = "private-resource-content-" + secrets.token_hex(16)
    uploaded = owner.files.with_raw_response.create(
        file=("resource-semantics.txt", marker.encode()), purpose="user_data",
    )
    file = uploaded.parse()
    cleanup.callback(owner.files.delete, file.id)
    metadata = uploaded.http_response.json()
    assert uploaded.status_code == 200
    assert metadata["status"] == "processed"
    assert metadata["expires_at"] is None and metadata["status_details"] is None
    assert file.bytes == len(marker.encode()) and file.filename == "resource-semantics.txt"
    assert file.purpose == "user_data" and file.object == "file"
    assert owner.files.retrieve(file.id).to_dict() == file.to_dict()
    assert recovered.files.retrieve(file.id).to_dict() == file.to_dict()
    path = settings["base"] + "/v1/files/" + file.id + "/content"
    headers = {"Authorization": "Bearer " + settings["token"]}
    denied = raw.get(path, headers=headers)
    body = assert_error(denied, 400, None)
    assert body["message"] == "Not allowed to download files of purpose: user_data"
    assert marker not in denied.text
    try:
        owner.files.content(file.id)
    except BadRequestError as error:
        assert error.status_code == 400 and error.body == body
    else:
        raise AssertionError("SDK downloaded a user_data File")
    foreign = raw.get(path, headers={"Authorization": "Bearer " + settings["foreign"]})
    missing = raw.get(settings["base"] + "/v1/files/file-" + secrets.token_hex(24) + "/content", headers=headers)
    assert_error(foreign, 404, "id")
    assert_error(missing, 404, "id")
    assert foreign.json() == missing.json()
    assert all(value not in foreign.text for value in (file.id, file.filename, marker, "user_data"))
    assert list(outsider.files.list()) == []
    assert owner.files.retrieve(file.id).to_dict() == file.to_dict()
    for purpose in (None, "", "user_data", "assistants", "batch", "fine-tune", "vision", "evals",
                    "assistants_output", "batch_output", "fine-tune-results"):
        query = {"after": "file-" + secrets.token_hex(24)}
        if purpose is not None:
            query["purpose"] = purpose
        assert_error(raw.get(settings["base"] + "/v1/files", headers=headers, params=query), 404, "after")
    for purpose in ("invalid-" + secrets.token_hex(12), "USER_DATA"):
        for key, cursor in ((settings["token"], "file-" + secrets.token_hex(24)), (settings["foreign"], file.id)):
            assert_error(raw.get(settings["base"] + "/v1/files",
                                 headers={"Authorization": "Bearer " + key},
                                 params={"purpose": purpose, "after": cursor}), 400, "purpose")
        try:
            owner.files.list(purpose=purpose, after="file-" + secrets.token_hex(24))
        except BadRequestError as error:
            assert error.status_code == 400 and error.code is None and error.param == "purpose"
        else:
            raise AssertionError("SDK accepted an invalid Files purpose")
    # Positive filtering is a Core regression; official evidence qualified only
    # purpose validation before a missing cursor, not positive list contents.
    assert file.id in [item.id for item in owner.files.list(purpose="user_data")]
    assert list(outsider.files.list(purpose="user_data")) == []
    assert owner.files.retrieve(file.id).to_dict() == file.to_dict()


def skill_bundle(name, description, marker):
    manifest = ("---\nname: " + name + "\ndescription: " + description + "\n---\n" + marker).encode()
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("proof/SKILL.md", manifest)
    return output.getvalue(), manifest


def check_skills(raw, owner, outsider, recovered, settings, cleanup):
    base = settings["base"] + "/v1/skills"
    headers = {"Authorization": "Bearer " + settings["token"]}
    foreign = {"Authorization": "Bearer " + settings["foreign"]}
    marker = "private-skill-resource-" + secrets.token_hex(16)
    bundles = [skill_bundle("proof-" + label, "Describe " + label + ".", marker + label)
               for label in ("one", "two", "three")]
    created = owner.skills.with_raw_response.create(files=[("proof/SKILL.md", bundles[0][1], "text/markdown")])
    skill = created.parse()
    cleanup.callback(owner.skills.delete, skill.id)
    assert created.status_code == 200 and skill.default_version == skill.latest_version == "1"
    first = owner.skills.versions.retrieve(skill_id=skill.id, version="1")
    first_content = owner.skills.versions.content.retrieve(skill_id=skill.id, version="1").read()
    second = owner.skills.versions.with_raw_response.create(
        skill_id=skill.id, files=[("proof/SKILL.md", bundles[1][1], "text/markdown")], default=False,
    )
    assert second.status_code == 200 and second.parse().version == "2"
    second_content = owner.skills.versions.content.retrieve(skill_id=skill.id, version="2").read()

    def check_default(version, label, content, latest="2"):
        current = owner.skills.retrieve(skill.id)
        assert (current.default_version, current.latest_version, current.name, current.description) == (
            version, latest, "proof-" + label, "Describe " + label + ".",
        )
        assert recovered.skills.retrieve(skill.id).to_dict() == current.to_dict()
        assert next(item for item in owner.skills.list() if item.id == skill.id).to_dict() == current.to_dict()
        response = raw.get(base + "/" + skill.id, headers=headers)
        assert response.status_code == 200 and response.json() == current.to_dict()
        assert owner.skills.content.retrieve(skill.id).read() == content
        assert recovered.skills.content.retrieve(skill.id).read() == content
        return current.to_dict()

    check_default("1", "one", first_content)
    updated = owner.skills.update(skill.id, default_version="2")
    assert updated.name == "proof-two" and updated.description == "Describe two."
    check_default("2", "two", second_content)
    updated = owner.skills.update(skill.id, default_version="1")
    assert updated.name == "proof-one" and updated.description == "Describe one."
    before = check_default("1", "one", first_content)
    versions = [version.to_dict() for version in owner.skills.versions.list(skill.id)]
    denied = raw.delete(base + "/" + skill.id + "/versions/1", headers=headers)
    body = assert_error(denied, 400, "version", "invalid_value")
    try:
        owner.skills.versions.delete(skill_id=skill.id, version="1")
    except BadRequestError as error:
        assert error.status_code == 400 and error.body == body
    else:
        raise AssertionError("SDK deleted a Skill default version")
    for method, suffix, kwargs in (
        ("POST", "", {"json": {"default_version": "2"}}),
        ("DELETE", "", {}),
        ("DELETE", "/versions/2", {}),
        ("POST", "/versions", {"files": {"files": ("proof.zip", bundles[2][0], "application/zip")}}),
    ):
        response = raw.request(method, base + "/" + skill.id + suffix, headers=foreign, **kwargs)
        assert response.status_code == 404, response.text
        assert marker not in response.text
        assert check_default("1", "one", first_content) == before
        assert [version.to_dict() for version in owner.skills.versions.list(skill.id)] == versions
    assert list(outsider.skills.list()) == []
    malformed = raw.post(base + "/" + skill.id + "/versions", headers=headers,
                         files=[("files", ("proof.zip", bundles[2][0])), ("unknown", (None, "reject"))])
    assert malformed.status_code == 400
    assert check_default("1", "one", first_content) == before
    assert [version.to_dict() for version in owner.skills.versions.list(skill.id)] == versions
    assert owner.skills.versions.retrieve(skill_id=skill.id, version="1").to_dict() == first.to_dict()
    assert owner.skills.versions.content.retrieve(skill_id=skill.id, version="2").read() == second_content
    deleted = owner.skills.versions.delete(skill_id=skill.id, version="2")
    assert deleted.deleted and deleted.id == second.parse().id and deleted.version == "2"
    check_default("1", "one", first_content, latest="1")
    # The official deleted-version visibility window is unqualified; do not
    # assert an immediate version GET/list result after successful deletion.
    another = owner.skills.create(files=[("proof/SKILL.md", bundles[0][1], "text/markdown")])
    cleanup.callback(owner.skills.delete, another.id)
    original = owner.skills.versions.content.retrieve(skill_id=another.id, version="1").read()
    third = owner.skills.versions.create(
        skill_id=another.id, files=[("proof/SKILL.md", bundles[2][1], "text/markdown")], default=True,
    )
    current = owner.skills.retrieve(another.id)
    assert (current.name, current.description, current.default_version, current.latest_version) == (
        "proof-three", "Describe three.", third.version, third.version,
    )
    assert recovered.skills.retrieve(another.id).to_dict() == current.to_dict()
    assert next(item for item in owner.skills.list() if item.id == another.id).to_dict() == current.to_dict()
    content = owner.skills.content.retrieve(another.id).read()
    assert content == owner.skills.versions.content.retrieve(skill_id=another.id, version=third.version).read()
    with zipfile.ZipFile(io.BytesIO(content)) as archive:
        assert archive.read("proof/SKILL.md") == bundles[2][1]
    assert owner.skills.versions.content.retrieve(skill_id=another.id, version="1").read() == original


def main():
    settings = json.load(sys.stdin)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    assert distribution.version == pin["sdk_version"] == "3.13.0"
    assert json.loads(distribution.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    with ExitStack() as cleanup:
        raw = cleanup.enter_context(httpx2.Client(trust_env=False, timeout=20))
        clients = [cleanup.enter_context(OpenAI(
            api_key=key, base_url=base + "/v1", max_retries=0,
            _strict_response_validation=True, http_client=DefaultHttpxClient(trust_env=False),
        )) for key, base in (
            (settings["token"], settings["base"]),
            (settings["foreign"], settings["base"]),
            (settings["token"], settings["recovered"]),
        )]
        check_files(raw, *clients, settings, cleanup)
        check_skills(raw, *clients, settings, cleanup)
        print(json.dumps({"result": "passed", "sdk": distribution.version,
                          "tenant_isolation": True, "postgres": True, "native_model": False}))


if __name__ == "__main__":
    main()
