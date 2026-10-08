"""Real native-model acceptance helpers for encrypted inline Skill initialization."""
import base64
import io
import json
import secrets
import zipfile


def inline_skill():
    marker = 'skill-private-' + secrets.token_hex(20)
    manifest = """---
name: proof-skill
description: Verify the initialized workspace and publish the Skill proof.
---
Run the packaged `scripts/check.py` by its installed absolute path, with the workspace as the working directory.
Stop on any failed assertion. Report INITIAL_FILES_VERIFIED and SKILL_VERIFIED.
"""
    script = f'''import os, runpy
from pathlib import Path
for name in ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'OPENAI_API_KEY', 'MINIMAX_API_KEY']:
    assert name not in os.environ, 'native credential reached a Skill helper'
runpy.run_path('verify.py')
Path('outputs/skill-proof.txt').write_text({marker!r})
print('SKILL_VERIFIED')
'''
    data = io.BytesIO()
    with zipfile.ZipFile(data, 'w', zipfile.ZIP_DEFLATED) as archive:
        archive.writestr('proof-skill/SKILL.md', manifest)
        archive.writestr('proof-skill/scripts/check.py', script)
    return {
        'type': 'inline', 'name': 'proof-skill',
        'description': 'Verify the initialized workspace and publish the Skill proof.',
        'source': {'type': 'base64', 'media_type': 'application/zip',
                   'data': base64.b64encode(data.getvalue()).decode()},
    }, marker.encode()


def attach_skills(client, foreign, http, environment, skill, template_id=None):
    endpoint = str(client.base_url).rstrip('/')
    if template_id:
        response = client.beta.agents.environments.templates.with_raw_response.update(template_id, skills=[skill])
        metadata = {key: skill[key] for key in ['type', 'name', 'description']}
        assert response.http_response.json()['skills'] == [metadata]
        assert skill['source']['data'] not in json.dumps(response.http_response.json())
        rejected = http.get(endpoint + '/agents/environments/templates/' + template_id,
                            headers={'Authorization': 'Bearer ' + foreign.api_key, 'OpenAI-Beta': 'agents=v1'})
        assert rejected.status_code == 404
        return environment
    return {**environment, 'skills': [skill]}


def verify_skills_metadata(client, session, skill):
    expected = [{key: skill[key] for key in ['type', 'name', 'description']}]
    environment = client.beta.agents.environments.retrieve(session.environment.id).to_dict()
    assert session.environment.to_dict()['skills'] == expected
    assert environment['skills'] == expected
    assert skill['source']['data'] not in json.dumps([session.to_dict(), environment])
