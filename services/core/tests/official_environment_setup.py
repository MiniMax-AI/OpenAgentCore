"""Shared real-deployment assertions for confidential environment initialization."""
import json
import secrets


def setup_configuration():
    marker = 'setup-private-' + secrets.token_hex(16) + "' $()"
    env = {'SETUP_VALUE': marker}
    first = """test -f initial-inline.bin && mkdir -p setup-sub && python3 - <<'SCRIPT'
import os
from pathlib import Path
import packaging
assert packaging.__version__ == '26.0'
assert os.environ['SETUP_VALUE']
Path('setup-sub/order').write_text('first')
SCRIPT
semver 1.2.3 > setup-version
"""
    second = "test \"$(cat order)\" = first && printf second > order && printf initialized > ../setup-once"
    packages = {'npm': ['semver@7.7.2'], 'python': ['packaging==26.0']}
    return {
        'env': env, 'packages': packages,
        'setup_commands': [{'command': first}, {'command': second, 'cwd': '/workspace/setup-sub'}],
    }, marker


def verify_setup_metadata(client, session, configuration):
    resource = client.beta.agents.environments.retrieve(session.environment.id).to_dict()
    assert session.environment.to_dict()['packages'] == {'system': [], **configuration['packages']}
    for value in [session.to_dict(), resource]:
        assert configuration['env']['SETUP_VALUE'] not in json.dumps(value)
        environment = value.get('environment', value)
        assert 'env' not in environment and 'setup_commands' not in environment


def native_setup_script(marker):
    script = f'''from pathlib import Path
import os, subprocess
import packaging
assert packaging.__version__ == '26.0'
assert os.environ['SETUP_VALUE'] == {marker!r}
assert Path('setup-sub/order').read_text() == 'second'
assert Path('setup-once').read_text() == 'initialized'
for cwd in ['.', 'setup-sub']:
    assert subprocess.check_output(['semver', '1.2.3'], cwd=cwd).strip() == b'1.2.3'
    subprocess.run(['python3', '-c', 'import packaging; assert packaging.__version__ == "26.0"'], cwd=cwd, check=True)
'''
    return script
