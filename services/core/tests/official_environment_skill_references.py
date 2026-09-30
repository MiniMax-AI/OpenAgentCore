"""Real-model Skill-reference proof using the shared inline installation fixture."""
import base64
import io
import json
import zipfile

from official_environment_skills import inline_skill
from official_session_artifacts import verify_session_artifacts


def upload_reference_skill(client):
    inline, expected = inline_skill()
    with zipfile.ZipFile(io.BytesIO(base64.b64decode(inline["source"]["data"]))) as archive:
        files = [(name, archive.read(name), "application/octet-stream") for name in archive.namelist()]
    skill = client.skills.create(files=files)
    assert skill.default_version == "1"
    return skill, expected


def verify_reference_metadata(client, session, skill, expected):
    metadata = [{"type": "skill_reference", "skill_id": skill.id, "version": "1",
                 "name": skill.name, "description": skill.description}]
    environment = client.beta.agents.environments.retrieve(session.environment.id)
    assert session.environment.to_dict()["skills"] == metadata
    assert environment.to_dict()["skills"] == metadata
    assert expected.decode() not in json.dumps([session.to_dict(), environment.to_dict()])


def verify_reference_output(client, foreign, http, session, artifacts):
    files = client.beta.agents.environments.files.list(session.environment.id, path="/workspace/outputs").data
    assert [(file.path, file.size_bytes) for file in files] == [
        ("/workspace/outputs/skill-proof.txt", len(next(iter(artifacts.values()))["/workspace/outputs/skill-proof.txt"]))]
    verify_session_artifacts(client, foreign, http, session.id, session.environment.id, artifacts)
