"""Compose existing real acceptance fixtures; no model or Runtime emulation."""
import base64
from contextlib import ExitStack
import io
import json
import time
import uuid
import zipfile

from official_environment_initial_files import initial_files, assert_initial_bytes_script
from official_environment_plugin_mcp import plugin_mcp_fixture, verify_plugin_mcp_metadata, verify_plugin_mcp_items
from official_environment_setup import setup_configuration, native_setup_script, verify_setup_metadata
from official_environment_skill_references import upload_reference_skill
from official_environment_skills import inline_skill
from official_session_artifacts import verify_session_artifacts
from session_cleanup import delete_session, delete_source


def composition_fixture(client, foreign, http, agent, model_provider, cleanup):
    """The runner owns source cleanup as well as Session/Template cleanup."""
    fixture = plugin_mcp_fixture(installed_dependencies=True)
    environment, source_id, initial = initial_files(client, foreign, http, agent, model_provider, cleanup)
    skill, expected = upload_reference_skill(client, cleanup)
    setup, marker = setup_configuration()
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
             'caller_env', 'npm_python', 'uploaded_skill']}
    fixture['composition_proof'] = proof
    script = native_setup_script(marker) + assert_initial_bytes_script(initial) + '''
assert Path('plugin-mcp-setup-count').read_text() == '1'
Path('outputs').mkdir(exist_ok=True)
'''
    script += 'import json\nproof = ' + repr(proof) + '''
counter = Path('composition-run-count')
proof['run'] = int(counter.read_text()) + 1 if counter.exists() else 1
counter.write_text(str(proof['run']))
Path('outputs/composition.json').write_text(json.dumps(proof, sort_keys=True) + '\\n')
'''
    fixture['files'].append({'type': 'inline', 'path': '/workspace/verify.py',
                             'data': base64.b64encode(script.encode()).decode()})
    fixture['outputs']['/workspace/outputs/composition.json'] = (json.dumps({**proof, 'run': 1}, sort_keys=True) + '\n').encode()
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
        files = {value['path']: value for value in environment['files']}
        inline = files['/workspace/initial-inline.bin']
        source = files['/workspace/initial-source.bin']
        assert inline['type'] == 'inline' and 'data' not in inline and 'file_id' not in inline
        assert source['type'] == 'file_id' and source['file_id'] == fixture['source_file_id']


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


def verify_composition(client, foreign, http, agent_options, session_options, ready, restart, record):
    """Qualify installed composition through the public Core path, without a deployment rig."""
    assert session_options['environment'] == {'type': 'openai_hosted'}, 'Composition requires a fresh hosted Environment'
    sessions = client.beta.agents.sessions
    templates = client.beta.agents.environments.templates
    proof = {'checks': [], 'runs': [], 'unverified': ['Private-owner and credential isolation require positive operator evidence.'],
             'unsupported': ['packages.system', 'Plugin stdio env_vars']}
    with ExitStack() as cleanup:
        fixture = composition_fixture(client, foreign, http, agent_options,
            session_options['extra_body']['x_agents_core']['model_provider'], cleanup)
        template = templates.create(**fixture['configuration'])
        cleanup.callback(delete_source, templates, template.id)
        session = sessions.create(agent=agent_options, **{**session_options, 'environment': {
            'type': 'openai_hosted', 'environment_template_id': template.id}})
        cleanup.callback(delete_session, sessions, session.id)
        proof['session'] = session.id
        try:
            ready(session)
            expected_artifacts = {}
            stages = ['initial', 'warm'] + (['cold'] if restart is not None else [])
            for run_number, stage in enumerate(stages, 1):
                if stage == 'warm':
                    change_and_delete_sources(client, fixture, template.id)
                    templates.delete(template.id)
                elif stage == 'cold':
                    restart()
                    ready(session)
                current = sessions.retrieve(session.id)
                verify_composition_metadata(client, http, current, fixture)
                prompt = fixture['prompt'] + ' Run all checks again even if proof files already exist; do not reuse an earlier answer.'
                with sessions.stream(session.id, input=prompt, idempotency_key=uuid.uuid4().hex, timeout=600) as stream:
                    events = [event.to_dict() for event in stream]
                proof['runs'].append({'stage': stage, 'events': events})
                record(proof)
                terminal = [event for event in events if event['type'] in {
                    'agent.session.turn.completed', 'agent.session.turn.failed', 'agent.session.turn.cancelled'}]
                assert len(terminal) == 1 and terminal[0]['type'] == 'agent.session.turn.completed'
                assert events[-1]['type'] == 'agent.session.idle'
                turn = terminal[0]['turn']['id']
                run = verify_plugin_mcp_items(client, http, session.id, turn, fixture, events=events)
                proof['runs'][-1]['mcp'] = run
                if stage == 'initial':
                    expected_artifacts[turn] = dict(fixture['outputs'])
                else:
                    expected_artifacts[turn] = {'/workspace/outputs/composition.json': (json.dumps(
                        {**fixture['composition_proof'], 'run': run_number}, sort_keys=True) + '\n').encode()}
                verify_session_artifacts(client, foreign, http, session.id, session.environment.id, expected_artifacts)
                rows = client.beta.agents.environments.files.list(session.environment.id, path='/workspace/outputs')
                assert {row.path: row.size_bytes for row in rows} == {path: len(body) for path, body in fixture['outputs'].items()}
                assert sessions.retrieve(session.id).required_actions == []
                proof['checks'].append(stage + '_composition_bytes_mcp_and_frozen_sources')
                record(proof)

            before = {turn.id for turn in sessions.turns.list(session.id)}
            sessions.events.create(session.id, events=[{'type': 'agent.session.input.message', 'input': [
                {'role': 'user', 'content': [{'type': 'input_text', 'text': fixture['hold_prompt']}]}]}],
                                   idempotency_key=uuid.uuid4().hex)
            deadline = time.monotonic() + 180
            observed = None
            while time.monotonic() < deadline:
                turns = [turn for turn in sessions.turns.list(session.id) if turn.id not in before]
                assert len(turns) <= 1 and not any(turn.status in {'completed', 'failed', 'cancelled'} for turn in turns)
                rows = client.beta.agents.environments.files.list(session.environment.id, path='/workspace/plugin-mcp-hold')
                sizes = {row.path: row.size_bytes for row in rows}
                size = sizes.get(fixture['hold_paths']['ticks'], 0)
                if observed is not None and size > observed and fixture['hold_paths']['invocation'] in sizes:
                    break
                if size:
                    observed = size
                time.sleep(0.5)
            else:
                raise AssertionError('Native MCP descendant did not produce growing effects')
            turn_id = turns[0].id
            key = uuid.uuid4().hex
            for _ in range(2):
                sessions.events.create(session.id, events=[{'type': 'agent.session.input.cancel'}], idempotency_key=key)
            deadline = time.monotonic() + 120
            while time.monotonic() < deadline:
                turn = sessions.turns.retrieve(turn_id, session_id=session.id)
                if turn.status == 'cancelled' and sessions.retrieve(session.id).status == 'idle':
                    break
                assert turn.status not in {'completed', 'failed'}
                time.sleep(0.5)
            else:
                raise AssertionError('MCP cancellation did not settle')
            settled = {row.path: row.size_bytes for row in client.beta.agents.environments.files.list(
                session.environment.id, path='/workspace/plugin-mcp-hold')}
            assert settled[fixture['hold_paths']['ticks']] >= size
            time.sleep(3)
            assert {row.path: row.size_bytes for row in client.beta.agents.environments.files.list(
                session.environment.id, path='/workspace/plugin-mcp-hold')} == settled, 'MCP effects continued after cancellation'
            proof['release'] = verify_plugin_mcp_items(client, http, session.id, turn_id, fixture,
                tool='hold', servers=[fixture['hold_server']], expected_status='incomplete', expected_turn_status='cancelled')
            proof['release']['stable_seconds'] = 3
            proof['release']['ticks_before_cancel_bytes'] = size
            proof['release']['ticks_after_cancel_bytes'] = settled[fixture['hold_paths']['ticks']]
            proof['checks'].append('native_mcp_cancel_retry_stops_descendant_effects')
            return proof['checks']
        finally:
            record(proof)
