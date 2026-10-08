"""Real-deployment checks for confidential initial files and frozen metadata."""
import base64
import secrets

from session_cleanup import delete_source


def initial_files(client, foreign, http, agent, model_provider, cleanup):
    inline = secrets.token_bytes(40)
    source_body = bytes(range(256))
    source = client.files.create(file=('initial.bin', source_body), purpose='user_data')
    cleanup.callback(delete_source, client.files, source.id)
    files = [{'type': 'inline', 'path': '/workspace/initial-inline.bin',
              'data': base64.b64encode(inline).decode()},
             {'type': 'file_id', 'path': '/workspace/initial-source.bin', 'file_id': source.id}]
    endpoint = str(client.base_url).rstrip('/')
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    foreign_headers = {**headers, 'Authorization': 'Bearer ' + foreign.api_key}
    attempted = http.post(endpoint + '/agents/sessions', headers=foreign_headers,
                          json={'agent': agent, 'x_agents_core': {'model_provider': model_provider}, 'environment': {'type': 'openai_hosted', 'files': [files[1]]}})
    assert attempted.status_code == 404 and source.id not in attempted.text
    environment = {'type': 'openai_hosted', 'network': {'access': 'enabled'}, 'files': files}
    return environment, source.id, {files[0]['path']: inline, files[1]['path']: source_body}


def assert_initial_bytes_script(expected):
    return ''.join('assert Path(' + repr(path.removeprefix('/workspace/')) + ').read_bytes() == bytes.fromhex(' + repr(body.hex()) + ')\n'
                   for path, body in expected.items())
