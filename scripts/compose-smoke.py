#!/usr/bin/env python3
"""Exercise the Compose installation in an isolated Docker project.

Core, Web and the ingress image are built from this checkout, with node
metadata for this commit. Web serves a placeholder page instead of the console
build. A second phase starts the published release pinned in
deploy/compose/smoke-pins.json and upgrades it in place to this checkout.
"""

import hashlib
import http.cookiejar
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
RELEASES = 'https://github.com/MiniMax-AI/OpenAgentCore/releases/download/'


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


render_compose = load('render_compose', 'scripts/render-compose.py')
distribution = load('distribution', 'scripts/core-distribution-manifest.py')


def prepare_payload(destination, revision, source_time):
    """Node metadata for this commit, built with the distribution's own builders."""
    (destination / 'runtime').mkdir(parents=True)
    with tempfile.TemporaryDirectory(dir=destination.parent) as sources:
        sources = Path(sources)
        for path in (ROOT / 'deploy/node').glob('*.py'):
            shutil.copyfile(path, sources / path.name)
        distribution.bootstraps(sources, source_time, revision)
        shutil.copyfile(sources / 'node-install.pyz', destination / 'node-install.pyz')
    shutil.copyfile(ROOT / 'services/core/deploy/codex/seccomp.json', destination / 'runtime/seccomp.json')
    (destination / 'manifest.json').write_text(json.dumps(
        {'source_commit': revision, 'source_time': int(source_time), 'platform': 'linux/amd64'}))
    distribution.checksums(destination)


def build_images(directory, tag, revision, source_time):
    protocol = re.search(r'const Version = "([^"]+)"', (ROOT / 'internal/agentdaemon/proto/version.go').read_text()).group(1)
    go_env = {**os.environ, 'CGO_ENABLED': '0', 'GOOS': 'linux', 'GOARCH': os.environ.get('GOARCH', 'amd64')}

    def go_build(package, output):
        output.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(['go', 'build', '-trimpath', '-ldflags', '-X main.buildRevision=' + revision,
                        '-o', str(output), './' + package], cwd=ROOT, env=go_env, check=True)

    contexts = {name: directory / ('image-' + name) for name in ('core', 'web', 'ingress')}
    core = contexts['core']
    for name, package in (('oac-core', 'server'), ('oac-core-device', 'device'),
                          ('oac-core-environment-key', 'environment-key'), ('oac', 'oac')):
        go_build('services/core/cmd/' + package, core / 'bin' / name)
    (core / 'e2b').mkdir()
    (core / 'native-installers').mkdir()
    (core / 'native-installers/catalog.json').write_text(json.dumps({
        'version': revision, 'protocol_version': protocol, 'artifacts': {'linux-amd64': {
            'sha256': '0' * 64,
            'url': f'https://example.invalid/oac-native-{revision}-linux-amd64.tar.gz'}}}))
    (core / 'Dockerfile').write_bytes((ROOT / 'deploy/distribution/Dockerfile').read_bytes())
    web = contexts['web']
    go_build('services/web', web / 'oac-web')
    (web / 'dist').mkdir()
    (web / 'dist/index.html').write_text('<!doctype html><html><body>Compose smoke</body></html>\n')
    (web / 'Dockerfile').write_bytes((ROOT / 'services/web/Dockerfile').read_bytes())
    ingress = contexts['ingress']
    prepare_payload(ingress / 'node-payload', revision, source_time)
    go_build('services/core/cmd/oac', ingress / 'oac')
    (ingress / 'Dockerfile').write_bytes((ROOT / 'deploy/distribution/Ingress.Dockerfile').read_bytes())
    for path in directory.glob('image-*/**/*'):
        path.chmod(0o755 if path.is_dir() or os.access(path, os.X_OK) else 0o644)
    images = {}
    for name, context in contexts.items():
        images[name] = f'oac-smoke/{name}:{tag}'
        subprocess.run(['docker', 'build', '-q', '--platform', 'linux/' + go_env['GOARCH'], '-t', images[name], str(context)],
                       check=True, stdout=subprocess.DEVNULL)
    return images


def main():
    os.umask(0o077)
    project = os.environ.get('COMPOSE_SMOKE_PROJECT', 'oac-smoke-' + uuid.uuid4().hex)
    if not re.fullmatch(r'oac-smoke-[a-f0-9]{32}', project):
        raise ValueError('COMPOSE_SMOKE_PROJECT must contain a unique oac-smoke- UUID hex value')
    artifacts = Path.home() / '.oac/tests'
    artifacts.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix=project + '-', dir=artifacts))
    data = directory / 'data'
    data.mkdir(mode=0o700)
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    source_time = subprocess.check_output(['git', 'show', '-s', '--format=%ct', 'HEAD'], cwd=ROOT, text=True).strip()
    rendered = directory / 'compose.yaml'
    rendered.write_text(render_compose.render({
        'REVISION': revision,
        'INIT_IMAGE': 'ghcr.io/minimax-ai/openagentcore/ingress@sha256:' + '0' * 64,
        'CORE_IMAGE': 'ghcr.io/minimax-ai/openagentcore/core@sha256:' + '0' * 64,
        'WEB_IMAGE': 'ghcr.io/minimax-ai/openagentcore/web@sha256:' + '0' * 64,
    }))
    override = directory / 'offline-init.json'
    images = build_images(directory, project.removeprefix('oac-smoke-'), revision, source_time)

    override.write_text(json.dumps({'services': {'init': {'network_mode': 'none'}}}))
    env = {**os.environ, 'COMPOSE_PROGRESS': 'plain',
           'OAC_HOST': '127.0.0.1', 'OAC_WEB_PORT': '0',
           **{'OAC_IMAGE_' + name.upper(): image for name, image in images.items()}}
    env.pop('OAC_PUBLIC_URL', None)

    def runner(files, environment):
        command = ['docker', 'compose', '--env-file', os.devnull, '-p', project]
        for file in files:
            command += ['-f', str(file)]

        def compose(*args, timeout=120):
            result = subprocess.run(command + list(args), env=environment, cwd=ROOT, capture_output=True, timeout=timeout)
            if result.returncode:
                # Startup/configuration stderr helps diagnose failures before containers exist.
                # Tool output and application logs may contain credentials and stay private.
                detail = '' if args[0] in ('run', 'logs') else result.stderr.decode(errors='replace')[-4096:]
                raise RuntimeError(f'Compose {args[0]} failed with exit code {result.returncode}\n{detail}')
            return result.stdout
        return compose

    compose = runner([rendered, override], env)

    def client():
        return urllib.request.build_opener(urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    browser = client()
    origin = 'http://localhost:8080'
    address = ''

    def request(path, body=None, headers=None, status=200):
        if body is not None and not isinstance(body, bytes):
            body = json.dumps(body).encode()
        req = urllib.request.Request(address + path, data=body, headers={
            'Host': origin.split('://', 1)[1], 'Origin': origin,
            'Content-Type': 'application/json', 'OpenAI-Beta': 'agents=v1', **(headers or {}),
        })
        try:
            response = browser.open(req, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            if response.status != status:
                raise RuntimeError(f'{req.get_method()} {path}: expected {status}, got {response.status}')
            return response.read()

    def get(path, **kwargs):
        return json.loads(request(path, **kwargs))

    def private_logs(*keys):
        logs = compose('logs', '--no-color').decode()
        assert all(key not in logs for key in keys), 'Credentials appeared in container logs'
        return logs

    def upload(api, content):
        boundary = 'oac-compose-smoke'
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="purpose"\r\n\r\nuser_data\r\n'
                f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="smoke.txt"\r\n'
                'Content-Type: text/plain\r\n\r\n').encode() + content + f'\r\n--{boundary}--\r\n'.encode()
        uploaded = get('/v1/files', body=body, headers={**api, 'Content-Type': 'multipart/form-data; boundary=' + boundary})
        assert uploaded['bytes'] == len(content), 'Upload was truncated'
        return uploaded

    def terminate(_signum, _frame):
        raise SystemExit(1)

    signal.signal(signal.SIGTERM, terminate)
    try:
        print('Starting the images with an unset public URL and an empty data directory', flush=True)
        compose('up', '-d', '--wait', '--wait-timeout', '600', timeout=900)
        address = 'http://' + compose('port', 'web', '8080').decode().strip()
        key = compose('exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key').decode().strip()
        assert re.fullmatch(r'oac_admin_[0-9a-f]{64}', key), 'Missing generated sign-in key'
        request('/healthz')
        assert b'<html' in request('/'), 'Console HTML is unavailable'
        request('/', headers={'Host': 'unconfigured.example.invalid'}, status=403)
        request('/v1/agents', status=401)
        request('/console/auth/login', {'core_key': key})
        facts = get('/core/v1/installation')
        assert facts['public_url'] == origin and facts['installation_id'], 'Incorrect installation identity/origin'
        project_data = get('/core/v1/projects', body={'name': 'Compose smoke'}, status=201)
        project_key = get('/core/v1/projects/' + project_data['id'] + '/keys', body={'name': 'smoke'}, status=201)['key']
        api = {'Authorization': 'Bearer ' + project_key}
        assert get('/v1/agents', headers=api)['data'] == [], 'Authenticated API is unavailable'

        installer = request('/node-install/node-install.pyz')
        checksums = dict(line.split('  ', 1)[::-1] for line in request('/node-install/SHA256SUMS').decode().splitlines())
        assert hashlib.sha256(installer).hexdigest() == checksums['node-install.pyz'], 'Node installer checksum mismatch'
        content = b'x' * (5 * 1024 * 1024)
        uploaded = upload(api, content)
        private_logs(key, project_key)

        print('Configuring a reachable URL and recreating containers with the same data directory', flush=True)
        # Retain the assigned port across recreation, without claiming a fixed host port.
        env['OAC_WEB_PORT'] = address.rsplit(':', 1)[1]
        env['OAC_PUBLIC_URL'] = address
        compose('down')
        compose('up', '-d', '--wait', '--wait-timeout', '120', timeout=180)
        origin = address
        browser = client()
        assert compose('exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key').decode().strip() == key, 'Sign-in key changed'
        request('/', headers={'Host': 'localhost:8080'}, status=403)
        request('/console/auth/login', {'core_key': key})
        updated = get('/core/v1/installation')
        assert updated['installation_id'] == facts['installation_id'], 'Installation identity changed'
        assert updated['public_url'] == origin, 'The new public URL did not take effect'
        assert any(p['id'] == project_data['id'] for p in get('/core/v1/projects')['data']), 'Project was lost'
        assert get('/v1/files/' + uploaded['id'], headers=api)['bytes'] == len(content), 'Uploaded file metadata was lost'
        assert 'Bundled node installation metadata verified' not in private_logs(key, project_key), 'Completed initialization recopied metadata'
        compose('run', '--rm', '--no-deps', 'init', '/usr/local/bin/oac', 'rotate-volume-key')
        compose('restart', 'core', 'web')
        compose('up', '-d', '--wait', '--wait-timeout', '120', timeout=180)
        rotated = compose('exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key').decode().strip()
        assert rotated != key, 'Core key was not rotated'
        browser = client()
        request('/console/auth/login', {'core_key': rotated})
        compose('down')
        compose('up', '-d', '--wait', '--wait-timeout', '120', timeout=180)
        assert compose('exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key').decode().strip() == rotated, 'Rotated key was not retained'
        private_logs(key, rotated, project_key)
        print('PASS: startup, origin validation, sign-in, API, upload, node installer, key rotation and persistent installation', flush=True)

        pins = json.loads((ROOT / 'deploy/compose/smoke-pins.json').read_text())
        print(f'Starting the published {pins["tag"]} release in a new installation and upgrading it to this checkout', flush=True)
        compose('down', '--volumes')
        published = directory / 'published-compose.yaml'
        urllib.request.urlretrieve(RELEASES + pins['tag'] + '/compose.yaml', published)
        if hashlib.sha256(published.read_bytes()).hexdigest() != pins['compose_sha256']:
            raise ValueError('Pinned release compose.yaml checksum mismatch')
        env['OAC_WEB_PORT'] = '0'
        env.pop('OAC_PUBLIC_URL')
        released = runner([published], {name: value for name, value in env.items() if not name.startswith('OAC_IMAGE_')})
        released('up', '-d', '--wait', '--wait-timeout', '600', timeout=900)
        address = 'http://' + released('port', 'web', '8080').decode().strip()
        origin = 'http://localhost:8080'
        browser = client()
        key = released('exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key').decode().strip()
        request('/console/auth/login', {'core_key': key})
        facts = get('/core/v1/installation')
        project_data = get('/core/v1/projects', body={'name': 'Upgrade smoke'}, status=201)
        project_key = get('/core/v1/projects/' + project_data['id'] + '/keys', body={'name': 'smoke'}, status=201)['key']
        api = {'Authorization': 'Bearer ' + project_key}
        content = b'upgrade smoke\n'
        uploaded = upload(api, content)

        # The installer's preflight with the new release's images, then its start.
        compose('run', '--rm', '--no-deps', '--entrypoint', '/usr/local/bin/oac-core', 'core', 'check-config')
        compose('run', '--rm', '--no-deps', 'init')
        compose('up', '-d', '--wait', '--wait-timeout', '180', '--pull', 'never', '--remove-orphans', timeout=240)
        address = 'http://' + compose('port', 'web', '8080').decode().strip()
        browser = client()
        assert compose('exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key').decode().strip() == key, 'Upgrade changed the Core key'
        request('/console/auth/login', {'core_key': key})
        upgraded = get('/core/v1/installation')
        assert upgraded['installation_id'] == facts['installation_id'], 'Upgrade changed the installation identity'
        assert upgraded['source_commit'] == revision, 'Core does not run this checkout'
        assert json.loads(request('/node-install/manifest.json'))['source_commit'] == revision, 'Web serves another release'
        assert any(p['id'] == project_data['id'] for p in get('/core/v1/projects')['data']), 'Upgrade lost the Project'
        assert get('/v1/files/' + uploaded['id'], headers=api)['bytes'] == len(content), 'Upgrade lost the Project key or file'
        schema = compose('exec', '-T', 'database', 'psql', '-U', 'agents_api', '-d', 'agents_api', '-Atc',
                         'SELECT max(version_id) FROM agents_api_schema_version').decode().strip()
        latest = max(int(path.name.split('_', 1)[0]) for path in (ROOT / 'services/core/migrations').glob('*.sql'))
        assert int(schema) == latest, 'Upgrade did not migrate the schema'
        private_logs(key, project_key)
        print(f'PASS: in-place upgrade from {pins["tag"]} keeps the installation, Core key, Project, Project key and file', flush=True)
    except BaseException:
        # Service status identifies failed containers without dumping secret-bearing logs.
        status = subprocess.run(['docker', 'compose', '-p', project, 'ps', '--all'], env=env, capture_output=True, timeout=30)
        print(status.stdout.decode(), flush=True)
        raise
    finally:
        compose('down', '--volumes', '--remove-orphans', timeout=60)
        subprocess.run(['docker', 'image', 'rm', '-f', *images.values()], capture_output=True, timeout=60)


if __name__ == '__main__':
    main()
