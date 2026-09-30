"""Fixtures for real native Plugin MCP acceptance through the pinned public API.

The caller owns Core, Runtime, positive private canaries, model calls, recovery and
cancellation. Reuse official_environment_files.verify_environment_files with
{path: len(body) for path, body in fixture['outputs'].items()}, and
official_session_artifacts.verify_session_artifacts with those same bytes.
These helpers never emulate a model or claim native acceptance from fixture checks.
"""
import base64
import hashlib
import io
import json
import secrets
import shlex
import zipfile

from official_environment_plugins import verify_plugin_resources


_CHECKS = ['selected_user_env', 'unselected_env_absent', 'native_env_absent',
           'package_cwd', 'private_contents_denied', 'installed_package_readonly']
_SERVER = r'''import hashlib, json, os, subprocess, sys
from pathlib import Path

root = Path(__file__).resolve().parent
config = json.loads((root / 'resources/proof.json').read_text())

def verify(marker):
    assert marker == config['marker'], 'wrong invocation marker'
    selected = os.environ.get('PLUGIN_MCP_SELECTED', '')
    assert selected and hashlib.sha256(selected.encode()).hexdigest() == config['selected_sha256'], 'selected user env missing or wrong'
    assert 'PLUGIN_MCP_UNSELECTED' not in os.environ, 'unselected user env present'
    prefixes = ('PARSAR_', 'CODEX_', 'OPENAI_', 'ANTHROPIC_', 'MINIMAX_', 'MOONSHOT_', 'KIMI_')
    assert not any(name.startswith(prefixes) or name == 'NATIVE_MCP_CANARY' for name in os.environ), 'native environment present'
    assert Path.cwd() == root / 'resources', 'relative package cwd not applied'
    assert json.loads(Path('proof.json').read_text()) == config, 'cwd resource differs'
    private_paths = json.loads(Path('/workspace/plugin-isolation-paths.json').read_text())
    assert set(private_paths) == {'staging', 'native_history', 'daemon_auth'}, 'positive private paths missing'
    assert all(isinstance(path, str) and path.startswith('/') for path in private_paths.values()), 'invalid private paths'
    for path in private_paths.values():
        try:
            with open(path, 'rb') as stream:
                exposed = stream.read(1)
        except OSError:
            continue
        assert not exposed, 'private Runtime content readable'
    for path in [root / '.codex-plugin/plugin.json', root / '.mcp.json', root / 'resources/proof.json']:
        try:
            with path.open('r+b'):
                pass
        except OSError:
            continue
        raise AssertionError('installed package writable')
    if config.get('installed_dependencies'):
        import packaging
        assert packaging.__version__ == '26.0', 'installed Python dependency missing'
        assert subprocess.check_output(['semver', '1.2.3']).strip() == b'1.2.3', 'installed npm dependency missing'
        assert subprocess.check_output(['jq', '-r', '.value'], input=b'{"value":42}').strip() == b'42', 'installed system dependency missing'
        try:
            Path('/usr/bin/jq').open('r+b').close()
        except OSError:
            pass
        else:
            raise AssertionError('installed system root writable')
    return {'marker': marker, 'server': config['server'], 'checks': config['checks']}

def invoke(name, arguments):
    proof = verify(arguments.get('marker'))
    if name == 'verify':
        directory = Path('/workspace/outputs')
        directory.mkdir(exist_ok=True)
        (directory / (config['server'] + '.json')).write_text(json.dumps(proof, sort_keys=True) + '\n')
    elif name == 'hold':
        directory = Path('/workspace/plugin-mcp-hold')
        directory.mkdir(exist_ok=True)
        (directory / 'invocation.json').write_text(json.dumps(proof, sort_keys=True) + '\n')
        # Only a real tools/call starts this detached descendant. Its bounded
        # lifetime prevents a failed operator run from leaving an endless probe.
        writer = "import json,sys,time\nfrom pathlib import Path\np=Path(sys.argv[1])\nfor tick in range(720):\n with p.open('a') as f: f.write(json.dumps({'marker':sys.argv[2],'tick':tick})+'\\n')\n time.sleep(0.25)\n"
        subprocess.run([sys.executable, '-I', '-S', '-c', writer,
                        str(directory / 'ticks.jsonl'), proof['marker']],
                       start_new_session=True, check=True)
    else:
        raise AssertionError('unknown tool')
    return {'content': [{'type': 'text', 'text': json.dumps(proof, sort_keys=True)}],
            'structuredContent': proof, 'isError': False}

for line in sys.stdin:
    request = json.loads(line)
    if 'id' not in request:
        continue
    method, params = request.get('method'), request.get('params', {})
    response = {'jsonrpc': '2.0', 'id': request['id']}
    try:
        if method == 'initialize':
            response['result'] = {'protocolVersion': params['protocolVersion'],
                                  'capabilities': {'tools': {}},
                                  'serverInfo': {'name': config['server'], 'version': '1.0.0'}}
        elif method == 'ping':
            response['result'] = {}
        elif method == 'tools/list':
            response['result'] = {'tools': [{'name': name,
                'description': description,
                'inputSchema': {'type': 'object', 'properties': {'marker': {'type': 'string'}},
                                'required': ['marker'], 'additionalProperties': False}}
                for name, description in [('verify', 'Verify the installed package and isolated environment; write workspace proof.'),
                                          ('hold', 'Write invocation proof and keep a descendant writing for cancellation verification.')]]}
        elif method == 'tools/call':
            try:
                response['result'] = invoke(params['name'], params.get('arguments', {}))
            except Exception as error:
                # Exception text, paths and environment values may be private.
                response['result'] = {'content': [{'type': 'text', 'text': 'PLUGIN_MCP_CHECK_FAILED:' + type(error).__name__}], 'isError': True}
        else:
            response['error'] = {'code': -32601, 'message': 'Method not found'}
    except Exception:
        response['error'] = {'code': -32602, 'message': 'Invalid request'}
    print(json.dumps(response), flush=True)
'''


def _skill(name, marker, output):
    script = ("from pathlib import Path\n"
              "root = Path(__file__).resolve().parent\n"
              "assert '/initialization/capabilities/' in str(root)\n"
              "Path('/workspace/outputs').mkdir(exist_ok=True)\n"
              "Path(" + repr(output) + ").write_bytes((root / 'proof.txt').read_bytes())\n"
              "print('INSTALLED_PLUGIN_SKILL_VERIFIED')\n")
    manifest = ('---\nname: ' + name + '\ndescription: Execute the installed ' + name + ' proof script.\n---\n'
                'Run `python3 check.py` from this installed Skill directory using native tools. '
                'Use this packaged script; do not recreate it.\n')
    return {'SKILL.md': manifest, 'check.py': script, 'proof.txt': marker + '\n'}


def _package(server, marker, selected, skill=False, *, installed_dependencies=False):
    manifest = {'name': server, 'description': 'Native MCP isolation proof.', 'mcpServers': './.mcp.json'}
    files = {'.mcp.json': json.dumps({'mcpServers': {server: {
        'command': 'python3', 'args': ['../server.py'], 'cwd': 'resources',
        'env_vars': ['PLUGIN_MCP_SELECTED']}}}), 'server.py': _SERVER,
        'resources/proof.json': json.dumps({'server': server, 'marker': marker,
            'selected_sha256': hashlib.sha256(selected.encode()).hexdigest(),
            'installed_dependencies': installed_dependencies,
            'checks': _CHECKS + (['installed_dependencies'] if installed_dependencies else [])})}
    if skill:
        manifest['skills'] = ['./skills']
        files.update({'skills/combined/' + path: body for path, body in _skill(
            'combined-mcp-skill', marker, '/workspace/outputs/combined-skill.txt').items()})
    files['.codex-plugin/plugin.json'] = json.dumps(manifest)
    return files


def _inline_plugin(name, files):
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as output:
        for path, body in files.items():
            output.writestr('proof/' + path, body)
    return {'type': 'inline', 'name': name, 'description': 'Native MCP isolation proof.',
            'source': {'type': 'base64', 'media_type': 'application/zip',
                       'data': base64.b64encode(archive.getvalue()).decode()}}


def plugin_mcp_fixture(*, installed_dependencies=False):
    """Return one hosted configuration and exact expected public proof bytes.

    Before a native Turn, the runner must create nonempty private canary files
    outside tool authority and publish only their paths in plugin-isolation-paths.json.
    Never log the returned env or source bodies. The marker itself is nonsecret.
    """
    marker = 'plugin-mcp-proof-' + secrets.token_hex(20)
    env = {'PLUGIN_MCP_SELECTED': 'selected-' + secrets.token_hex(24),
           'PLUGIN_MCP_UNSELECTED': 'unselected-' + secrets.token_hex(24)}
    servers = ['mcp_only_proof', 'combined_proof', 'generated_proof']
    plugins = [_inline_plugin(name, _package(name, marker, env['PLUGIN_MCP_SELECTED'], skill=index == 1,
                       installed_dependencies=installed_dependencies))
               for index, name in enumerate(servers[:2])]
    exact, parent = '/workspace/generated/mcp-exact', '/workspace/generated/skill-parent'
    generated = {exact + '/' + path: body for path, body in _package(
        servers[2], marker, env['PLUGIN_MCP_SELECTED'],
        installed_dependencies=installed_dependencies).items()}
    # Selecting the parent discovers a nested Skill, but never the child's MCP.
    child = _package('unselected_child_mcp', marker, env['PLUGIN_MCP_SELECTED'])
    child_manifest = json.loads(child['.codex-plugin/plugin.json'])
    child_manifest['skills'] = ['./skills']
    child['.codex-plugin/plugin.json'] = json.dumps(child_manifest)
    child.update({'skills/parent/' + path: body for path, body in _skill(
        'parent-discovered-skill', marker, '/workspace/outputs/parent-skill.txt').items()})
    generated.update({parent + '/child/' + path: body for path, body in child.items()})
    seed = json.dumps(generated)
    initial = [{'type': 'inline', 'path': '/workspace/plugin-mcp-seed.json',
                'data': base64.b64encode(seed.encode()).decode()}]
    setup = ("import json\nfrom pathlib import Path\n"
             "for name, body in json.loads(Path('/workspace/plugin-mcp-seed.json').read_text()).items():\n"
             " p = Path(name)\n p.parent.mkdir(parents=True, exist_ok=True)\n p.write_text(body)\n"
             "p = Path('/workspace/plugin-mcp-setup-count')\n"
             "p.write_text(str(int(p.read_text()) + 1) if p.exists() else '1')\n")
    checks = _CHECKS + (['installed_dependencies'] if installed_dependencies else [])
    outputs = {'/workspace/outputs/' + server + '.json': (json.dumps(
        {'marker': marker, 'server': server, 'checks': checks}, sort_keys=True) + '\n').encode()
        for server in servers}
    outputs.update({path: (marker + '\n').encode() for path in [
        '/workspace/outputs/combined-skill.txt', '/workspace/outputs/parent-skill.txt']})
    prompt = ('Use the installed combined-mcp-skill and parent-discovered-skill Skills and run their packaged '
              'check.py scripts exactly as instructed. Then call the native MCP verify tool on each of '
              + ', '.join(servers) + ' exactly once, in that order, with {"marker": ' + json.dumps(marker) + '}. '
              'Use real native MCP calls; do not recreate servers, scripts or proof files. Do not call hold. '
              'Report only the server names and success or failure, never environment values.')
    return {'plugins': plugins, 'files': initial, 'setup_commands': [{'command': 'python3 -c ' + shlex.quote(setup)}],
            'capability_directories': [exact, parent], 'env': env, 'marker': marker,
            'servers': servers, 'checks': checks, 'forbidden_servers': ['unselected_child_mcp'], 'outputs': outputs,
            'prompt': prompt, 'source_paths': ['/workspace/generated', '/workspace/plugin-mcp-seed.json'],
            'hold_server': servers[0], 'hold_paths': {'invocation': '/workspace/plugin-mcp-hold/invocation.json',
                'ticks': '/workspace/plugin-mcp-hold/ticks.jsonl'},
            'hold_prompt': 'Call the native MCP hold tool on ' + servers[0] + ' exactly once with {"marker": '
                + json.dumps(marker) + '}. Wait for it; do not use shell tools or recreate its effects.'}


def _assert_private_body(value, fixture):
    serialized = json.dumps(value)
    secrets_to_check = list(fixture['env'].values()) + [plugin['source']['data'] for plugin in fixture['plugins']]
    secrets_to_check += [item['data'] for item in fixture['files'] if item['type'] == 'inline']
    assert all(secret not in serialized for secret in secrets_to_check), 'Private initialization body exposed'


def verify_plugin_mcp_metadata(client, session, fixture, *, expected_skills=()):
    expected = [{key: plugin[key] for key in ['type', 'name', 'description']} for plugin in fixture['plugins']]
    resource = client.beta.agents.environments.retrieve(session.environment.id).to_dict()
    assert session.to_dict()['environment']['capability_directories'] == fixture['capability_directories']
    for value in [session.to_dict()['environment'], resource]:
        assert value['plugins'] == expected
        assert value['skills'] == list(expected_skills), 'Configured metadata must not expose discovered Skill inventory'
        assert 'env' not in value and 'setup_commands' not in value
        _assert_private_body(value, fixture)
    return {'plugins': expected, 'capability_directories': fixture['capability_directories']}


def verify_plugin_mcp_resources(client, foreign, http, fixture=None):
    """Reuse Skill Plugin CRUD/isolation checks; add MCP-only and combined bodies.

    Public absence is not proof of encryption at rest. The runner must separately
    inspect its task-owned database rows using the existing encrypted-body checks.
    """
    verify_plugin_resources(client, foreign, http)
    fixture = fixture or plugin_mcp_fixture()
    api = client.beta.agents.environments.templates
    configuration = {key: fixture[key] for key in ['plugins', 'files', 'setup_commands', 'capability_directories', 'env']}
    template = api.create(**configuration)
    endpoint = str(client.base_url).rstrip('/') + '/agents/environments/templates/' + template.id
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    expected = [{key: plugin[key] for key in ['type', 'name', 'description']} for plugin in fixture['plugins']]
    try:
        response = http.get(endpoint, headers=headers)
        assert response.status_code == 200
        bodies = [template.to_dict(), api.retrieve(template.id).to_dict(), response.json()]
        bodies.append(api.update(template.id, name='Preserved MCP packages').to_dict())
        for body in bodies:
            assert body['plugins'] == expected
            assert body['capability_directories'] == fixture['capability_directories']
            assert 'env' not in body and 'setup_commands' not in body
            _assert_private_body(body, fixture)
        for method in ['GET', 'POST', 'DELETE']:
            response = http.request(method, endpoint,
                headers={**headers, 'Authorization': 'Bearer ' + foreign.api_key},
                **({'json': {'plugins': []}} if method == 'POST' else {}))
            assert response.status_code == 404
            _assert_private_body(response.json(), fixture)
        assert api.retrieve(template.id).to_dict()['plugins'] == expected
        return {'plugins': expected, 'public_bodies_private': True, 'foreign_status': 404}
    finally:
        api.delete(template.id)


def _tool_proof(output):
    # Claude retains the native content list; Codex/MiniMax retain the MCP object.
    # Assert the same fixture result without rewriting the persisted native output.
    assert isinstance(output, (dict, list)), 'Unexpected native MCP result shape'
    if isinstance(output, dict):
        assert output.get('isError') is not True, 'Native MCP reported a tool error'
    content = output if isinstance(output, list) else output.get('content')
    if isinstance(content, str):
        # Claude SDK preserves this native result as text plus structuredContent.
        proof = json.loads(content)
        assert output.get('structuredContent') == proof, 'Native structured and text results differ'
        return proof
    assert isinstance(content, list) and len(content) == 1 and content[0]['type'] == 'text'
    proof = json.loads(content[0]['text'])
    if isinstance(output, dict) and 'structuredContent' in output:
        assert output['structuredContent'] == proof, 'Native structured and text results differ'
    return proof


def verify_plugin_mcp_items(client, http, session_id, turn_id, fixture, *,
                            expected_status='completed', tool='verify', servers=None,
                            events=None, expected_turn_status='completed'):
    """Check exact native identities, saved result bodies, and SDK/raw ordering.

    For cancellation, pass tool='hold', servers=[fixture['hold_server']] and the
    precise expected settled status. Observe hold_paths growth before cancellation
    and stability afterwards in the runner; Items alone cannot prove effect cleanup.
    Pass recorded event.to_dict() values to verify item.added < item.done < the
    terminal Turn event. A cancellation run must set expected_turn_status='cancelled'.
    """
    assert tool in ['verify', 'hold']
    assert expected_status in ['completed', 'failed', 'incomplete', 'in_progress']
    servers = fixture['servers'] if servers is None else servers
    assert servers and len(set(servers)) == len(servers)
    items = client.beta.agents.sessions.items
    saved = [item.to_dict() for item in items.list(session_id, limit=3, order='asc')]
    assert len({item['id'] for item in saved}) == len(saved)
    assert [item.to_dict() for item in items.list(session_id, limit=5)] == list(reversed(saved))
    endpoint = str(client.base_url).rstrip('/') + '/agents/sessions/' + session_id + '/items'
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    raw, params = [], {'limit': 3, 'order': 'asc'}
    for _ in range(len(saved) + 1):
        response = http.get(endpoint, headers=headers, params=params)
        assert response.status_code == 200
        page = response.json()
        raw.extend(page['data'])
        if not page['has_more']:
            break
        assert page['data'] and page['data'][-1]['id'] != params.get('after'), 'Items cursor did not advance'
        params['after'] = page['data'][-1]['id']
    else:
        raise AssertionError('Items pagination did not terminate')
    assert raw == saved, 'SDK and raw saved Items differ'
    _assert_private_body(saved, fixture)
    assert not any(item.get('server_label') in fixture['forbidden_servers'] for item in saved)
    group = [item for item in saved if item['turn_id'] == turn_id]
    assert group and group[0]['type'] == 'message' and group[0]['role'] == 'user'
    calls = [item for item in group if item['type'] == 'mcp_call']
    assert [item['server_label'] for item in calls] == servers, 'Native server identities or requested invocation order differ'
    for item in calls:
        assert item['id'] and item['turn_id'] == turn_id
        assert item['name'] == tool and item['status'] == expected_status
        assert item['arguments'] == {'marker': fixture['marker']}
        if expected_status == 'completed':
            assert item.get('error') is None
            assert _tool_proof(item['output']) == {
                'marker': fixture['marker'], 'server': item['server_label'], 'checks': fixture['checks']}
    if expected_status == 'completed':
        answers = [item for item in group if item['type'] == 'message' and item.get('role') == 'assistant']
        assert answers
        assert answers[-1]['status'] == 'completed'
    evidence = {'session_id': session_id, 'turn_id': turn_id, 'calls': calls,
                'item_ids_asc': [item['id'] for item in saved], 'sdk_raw_equal': True}
    if events is not None:
        assert expected_turn_status in ['completed', 'cancelled', 'failed']
        assert expected_status != 'in_progress', 'Pass settled Items for terminal stream assertions'
        _assert_private_body(events, fixture)
        assert len({event['event_id'] for event in events}) == len(events), 'Duplicate SSE event identity'
        terminals = [(index, event) for index, event in enumerate(events)
                     if event['type'] in ['agent.session.turn.' + status for status in ['completed', 'cancelled', 'failed']]
                     and event['turn']['id'] == turn_id]
        assert len(terminals) == 1 and terminals[0][1]['type'] == 'agent.session.turn.' + expected_turn_status
        transitions, completed_call_positions = [], []
        for call in calls:
            added = [(index, event) for index, event in enumerate(events)
                     if event['type'] == 'agent.session.turn.item.added' and event['item']['id'] == call['id']]
            done = [(index, event) for index, event in enumerate(events)
                    if event['type'] == 'agent.session.turn.item.done' and event['item']['id'] == call['id']]
            assert len(added) == len(done) == 1, 'Missing or duplicate native MCP transitions'
            start, end = added[0], done[0]
            assert start[0] < end[0] < terminals[0][0], 'Native MCP and terminal Turn ordering differs'
            assert start[1]['item']['status'] == 'in_progress', 'No observable native MCP start'
            # Native ACP can identify a call before its arguments arrive.
            assert start[1]['item']['arguments'] in [None, {}, {'marker': fixture['marker']}], 'Unexpected initial native MCP arguments'
            for event in [start[1], end[1]]:
                assert event['turn_id'] == turn_id
                assert event['item']['type'] == 'mcp_call'
                assert event['item']['server_label'] == call['server_label'] and event['item']['name'] == tool
            assert end[1]['item'] == call, 'Saved MCP item differs from its completed SSE item'
            assert type(start[1]['output_index']) is int and start[1]['output_index'] == end[1]['output_index']
            completed_call_positions.append(end[0])
            transitions.append({'item_id': call['id'], 'added_event_id': start[1]['event_id'],
                                'done_event_id': end[1]['event_id'], 'output_index': start[1]['output_index']})
        if expected_status == 'completed':
            # Items retain their first-observation position while streaming.
            # A native assistant may start before tools and complete after them.
            completed_answers = [(index, event) for index, event in enumerate(events)
                                 if event['type'] == 'agent.session.turn.item.done'
                                 and event['turn_id'] == turn_id
                                 and event['item']['type'] == 'message'
                                 and event['item'].get('role') == 'assistant'
                                 and event['item']['status'] == 'completed']
            assert completed_answers, 'Missing completed assistant SSE item'
            answer_position, answer_event = completed_answers[-1]
            assert max(completed_call_positions) < answer_position < terminals[0][0], 'Assistant completion must follow MCP results and precede the terminal Turn'
            assert answer_event['item'] in answers, 'Completed assistant SSE item differs from saved Items'
            evidence['assistant_done_event_id'] = answer_event['event_id']
        evidence['transitions'] = transitions
        evidence['terminal_event_id'] = terminals[0][1]['event_id']
    return evidence
