"""Fixed-client helpers for real hosted Plugin and generated-directory acceptance.

Use with the existing standalone Docker/native runner and Files/Artifacts checks.
This module provides fixtures and assertions, never a model or Runtime substitute.
"""
import base64
import io
import json
import secrets
import zipfile


def plugin_fixture():
    marker = 'plugin-proof-' + secrets.token_hex(20)
    files = {'.codex-plugin/plugin.json': json.dumps({
        'name': 'workspace-proof', 'description': 'Two portable verification Skills.',
        'skills': ['./layout', './more']}), 'shared/proof.txt': marker}
    for name, root, relative in [('proof-alpha', 'layout/custom/alpha', '../../../shared/proof.txt'),
                                 ('proof-beta', 'more/beta', '../../shared/proof.txt')]:
        files[root + '/SKILL.md'] = ('---\nname: ' + name + '\ndescription: Execute the packaged ' + name + ' verification script.\n---\n'
            'Use your native tools to run `python3 scripts/check.py` from this installed Skill directory. '
            'Use the provided script; do not recreate it. Report PLUGIN_VERIFIED after success.\n')
        files[root + '/scripts/check.py'] = '''import json, os
from pathlib import Path
root = Path(__file__).parent.parent
for name in ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'OPENAI_API_KEY', 'MINIMAX_API_KEY']:
    assert name not in os.environ, 'native credential reached Plugin script'
private_paths = json.loads(Path('/workspace/plugin-isolation-paths.json').read_text())
assert set(private_paths) == {'staging', 'native_history', 'daemon_auth'}, 'private probe paths missing'
for private in private_paths.values():
    try:
        with open(private, 'rb') as stream:
            exposed = stream.read(1)
    except OSError:
        continue
    # A readable empty mask does not expose the underlying nonempty private file.
    assert not exposed, 'private Runtime content readable'
try:
    (root / 'SKILL.md').write_text('tamper')
except OSError:
    pass
else:
    raise AssertionError('installed Skill writable')
value = (root / RELATIVE).read_text()
Path('/workspace/outputs').mkdir(exist_ok=True)
Path(OUTPUT).write_text(value)
print('PLUGIN_VERIFIED')
'''.replace('RELATIVE', repr(relative)).replace('OUTPUT', repr('/workspace/outputs/' + name + '.txt'))
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as output:
        for path, content in files.items():
            output.writestr('proof/' + path, content)
    plugin = {'type': 'inline', 'name': 'workspace-proof', 'description': 'Two portable verification Skills.',
              'source': {'type': 'base64', 'media_type': 'application/zip',
                         'data': base64.b64encode(archive.getvalue()).decode()}}
    generated = {
        '/workspace/generated/source.md': '---\nname: generated-proof\ndescription: Verify the setup-generated Skill snapshot.\n---\n'
            'Run `python3 check.py` from this installed Skill directory with your native tools. Do not recreate it.\n',
        '/workspace/generated/check.py': "from pathlib import Path\nimport shutil\n"
            "root=Path(__file__).parent\nassert '/initialization/capabilities/' in str(root)\n"
            "Path('/workspace/outputs').mkdir(exist_ok=True)\n"
            "Path('/workspace/outputs/generated-proof.txt').write_text((root/'proof.txt').read_text())\n"
            "shutil.rmtree('/workspace/generated', ignore_errors=True)\nprint('GENERATED_SKILL_VERIFIED')\n",
        '/workspace/generated/proof.txt': marker,
    }
    initial = [{'type': 'inline', 'path': path, 'data': base64.b64encode(content.encode()).decode()}
               for path, content in generated.items()]
    commands = [{'command': 'cp /workspace/generated/source.md /workspace/generated/SKILL.md; printf initialized > /workspace/setup-once'}]
    outputs = {'/workspace/outputs/' + name + '.txt': marker.encode()
               for name in ['proof-alpha', 'proof-beta', 'generated-proof']}
    return plugin, initial, commands, outputs


def verify_plugin_metadata(client, session, plugin):
    expected = [{key: plugin[key] for key in ['type', 'name', 'description']}]
    resource = client.beta.agents.environments.retrieve(session.environment.id).to_dict()
    value = session.to_dict()['environment']
    assert resource['plugins'] == value['plugins'] == expected
    assert value['capability_directories'] == ['/workspace/generated']
    assert resource['skills'] == value['skills'] == []  # Configured resource metadata, not native inventory.
    assert plugin['source']['data'] not in json.dumps([resource, value])


def verify_plugin_resources(client, foreign, http):
    """Run before native creation; resource reads need no allocated Environment."""
    api = client.beta.agents.environments.templates
    plugin, initial, commands, _ = plugin_fixture()
    source = str(client.base_url).rstrip('/') + '/agents/environments/templates'
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    other = {**headers, 'Authorization': 'Bearer ' + foreign.api_key}
    template = api.create(plugins=[plugin], capability_directories=['/workspace/generated'],
                          files=initial, setup_commands=commands)
    try:
        metadata = [{key: plugin[key] for key in ['type', 'name', 'description']}]
        for body in [template.to_dict(), api.retrieve(template.id).to_dict(),
                     http.get(source + '/' + template.id, headers=headers).json()]:
            assert body['plugins'] == metadata
            assert body['capability_directories'] == ['/workspace/generated']
            assert plugin['source']['data'] not in json.dumps(body)
        for method in ['GET', 'POST', 'DELETE']:
            assert http.request(method, source + '/' + template.id, headers=other,
                                **({'json': {'plugins': []}} if method == 'POST' else {})).status_code == 404
        assert api.update(template.id, name='preserve').to_dict()['plugins'] == metadata
        assert api.update(template.id, plugins=[], capability_directories=[]).to_dict()['plugins'] == []
        assert api.update(template.id, plugins=[plugin]).to_dict()['plugins'] == metadata
        assert api.update(template.id, plugins=None, capability_directories=None).to_dict()['plugins'] == []
    finally:
        api.delete(template.id)
