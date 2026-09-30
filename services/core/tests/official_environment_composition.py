"""Compose existing real acceptance fixtures; no model or Runtime emulation."""
import base64
import io
import json
import zipfile

from official_environment_initial_files import initial_files, assert_initial_bytes_script
from official_environment_plugin_mcp import plugin_mcp_fixture, verify_plugin_mcp_metadata
from official_environment_setup import setup_configuration, native_setup_script, verify_setup_metadata
from official_environment_skill_references import upload_reference_skill
from official_environment_skills import inline_skill


def composition_fixture(client, foreign, http, agent, proxy):
    """The runner owns source cleanup as well as Session/Template cleanup."""
    fixture = plugin_mcp_fixture(installed_dependencies=True)
    environment, source_id, initial = initial_files(client, foreign, http, agent)
    skill, expected = upload_reference_skill(client)
    setup, marker = setup_configuration(proxy)
    setup['packages']['system'] = ['jq']
    fixture['env'].update(setup['env'])
    fixture['packages'] = setup['packages']
    fixture['setup_commands'] = setup['setup_commands'] + fixture['setup_commands']
    fixture['files'] += environment['files']
    fixture['skills'] = [{'type': 'skill_reference', 'skill_id': skill.id}]
    fixture['expected_skills'] = [{'type': 'skill_reference', 'skill_id': skill.id,
                                  'version': '1', 'name': skill.name,
                                  'description': skill.description}]
    fixture['source_file_id'] = source_id
    fixture['source_skill_id'] = skill.id
    fixture['setup_configuration'] = setup
    proof = {'marker': fixture['marker'], 'checks': ['initial_files', 'ordered_setup',
             'caller_env', 'system_npm_python', 'uploaded_skill', 'readonly_system_root', 'private_paths_denied']}
    script = native_setup_script(marker) + assert_initial_bytes_script(initial) + '''
import json
assert subprocess.check_output(['jq', '-r', '.value'], input=b'{"value":42}').strip() == b'42'
private_paths = json.loads(Path('/workspace/plugin-isolation-paths.json').read_text())
assert set(private_paths) == {'staging', 'native_history', 'daemon_auth'}
for path in private_paths.values():
    try:
        with open(path, 'rb') as stream:
            exposed = stream.read(1)
    except OSError:
        continue
    assert not exposed, 'private Runtime data readable by uploaded Skill'
try:
    Path('/usr/bin/jq').open('r+b').close()
except OSError:
    pass
else:
    raise AssertionError('system package root is writable')
Path('/workspace/outputs').mkdir(exist_ok=True)
'''
    script += 'Path("/workspace/outputs/composition.json").write_text(' + repr(
        json.dumps(proof, sort_keys=True) + '\n') + ')\n'
    fixture['files'].append({'type': 'inline', 'path': '/workspace/verify.py',
                             'data': base64.b64encode(script.encode()).decode()})
    fixture['outputs']['/workspace/outputs/composition.json'] = (json.dumps(proof, sort_keys=True) + '\n').encode()
    fixture['outputs']['/workspace/outputs/skill-proof.txt'] = expected
    fixture['prompt'] = ('Use the installed proof-skill Skill and run its packaged script. '
                         'It must verify initialized files, environment, setup and installed dependencies. '
                         'Do not recreate or print scripts or environment values. ' + fixture['prompt'])
    fixture['initial_sizes'] = {item['path']: (len(base64.b64decode(item['data']))
                               if item['type'] == 'inline' else len(initial[item['path']]))
                              for item in fixture['files']}
    fixture['configuration'] = {key: fixture[key] for key in ['files', 'env', 'packages',
                                'setup_commands', 'skills', 'plugins', 'capability_directories']}
    fixture['configuration']['network'] = {'access': 'enabled'}
    return fixture


def verify_composition_metadata(client, http, session, fixture):
    verify_plugin_mcp_metadata(client, session, fixture, expected_skills=fixture['expected_skills'])
    verify_setup_metadata(client, session, fixture['setup_configuration'])
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    root = str(client.base_url).rstrip('/')
    for suffix, expected in [('/agents/sessions/' + session.id,
                              client.beta.agents.sessions.retrieve(session.id).to_dict()),
                             ('/agents/environments/' + session.environment.id,
                              client.beta.agents.environments.retrieve(session.environment.id).to_dict())]:
        response = http.get(root + suffix, headers=headers)
        assert response.status_code == 200 and response.json() == expected
        environment = expected.get('environment', expected)
        assert environment['skills'] == fixture['expected_skills']
        assert {value['path']: value['size_bytes'] for value in environment['files']} == fixture['initial_sizes']
        assert len({value['id'] for value in environment['files']}) == len(fixture['files'])
        assert 'env' not in environment and 'setup_commands' not in environment


def change_and_delete_sources(client, fixture, template_id=None):
    """Existing Session must retain v1 despite changed source pointers and bytes."""
    replacement, replacement_output = inline_skill()
    assert replacement_output != fixture['outputs']['/workspace/outputs/skill-proof.txt']
    with zipfile.ZipFile(io.BytesIO(base64.b64decode(replacement['source']['data']))) as archive:
        files = [(name, archive.read(name), 'application/octet-stream') for name in archive.namelist()]
    version = client.skills.versions.create(skill_id=fixture['source_skill_id'], files=files, default=True)
    assert version.version == '2'
    assert client.skills.retrieve(fixture['source_skill_id']).default_version == '2'
    if template_id:
        updated = client.beta.agents.environments.templates.update(template_id, env={'CHANGED': 'replacement'},
                     files=[], skills=[], plugins=[], capability_directories=[], setup_commands=[], packages=None)
        assert updated.skills == [] and updated.plugins == [] and updated.files == []
    client.files.delete(fixture['source_file_id'])
    client.skills.delete(fixture['source_skill_id'])
