#!/usr/bin/env python3
"""Start an offline source distribution with the production Compose services."""
import argparse
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import uuid

# The bundle ships the node installer's distribution modules beside this file.
BUNDLE = Path(__file__).resolve().parent
sys.path.insert(0, str(BUNDLE))
from distribution import DistributionError, ensure_docker_image, load_manifest
import node_payload


# Matches Core's OAC_PUBLIC_URL rule (publicorigin.New): a canonical http(s) origin with no path or credentials.
PUBLIC_ORIGIN = re.compile(r'https?://(?P<host>(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?|\[(?P<ip6>[0-9a-f:.]+)\])(?::(?P<port>[1-9][0-9]{0,4}))?')


def valid_public_origin(value):
    match = PUBLIC_ORIGIN.fullmatch(value)
    if not match or len(match['host']) > 253 or int(match['port'] or 1) > 65535:
        return False
    try:
        return match['ip6'] is None or bool(ipaddress.IPv6Address(match['ip6']))
    except ValueError:
        return False


def docker(*args, **kwargs):
    """Run a command with its output captured. The installation's .env is the one home of OAC_* and COMPOSE_* settings, so the caller's shell cannot override them."""
    kwargs['env'] = {name: value for name, value in os.environ.items() if not name.upper().startswith(('OAC_', 'COMPOSE_'))}
    return subprocess.run(args, capture_output=True, text=True, **kwargs)


def run(failure, *args, **kwargs):
    """Run a command whose output the user sees; a nonzero exit raises failure."""
    kwargs['env'] = {name: value for name, value in os.environ.items() if not name.upper().startswith(('OAC_', 'COMPOSE_'))}
    if subprocess.run(args, **kwargs).returncode:
        raise ValueError(failure)


def check_docker():
    """Refuse early, with the same words as oac install, when Docker is missing or out of reach."""
    try:
        result = docker('docker', 'info')
    except FileNotFoundError:
        raise ValueError('Docker is not installed; install Docker Engine 26 or newer with the Compose plugin, then rerun the installer') from None
    if 'permission denied while trying to connect to' in result.stderr:
        raise ValueError('This account cannot use Docker: add it to the docker group and log in again, or use rootless Docker. Do not run the installer with sudo.')
    if result.returncode:
        raise ValueError('Docker is not running or not reachable; start it and rerun the installer')


def port_holder_hint(port):
    """Name the OpenAgentCore Compose project that publishes the port, as oac install does."""
    rows = docker('docker', 'ps', '--filter', f'publish={port}', '--format', '{{.Label "com.docker.compose.project"}}\t{{.Label "com.docker.compose.project.working_dir"}}').stdout
    for row in rows.splitlines():
        name, _, directory = row.strip().partition('\t')
        if not name.startswith('oac-'):
            continue
        if not directory:
            return f'\nThe Compose project {name} holds it.'
        if Path(directory).exists():
            return f'\nThe installation in {directory} holds it; upgrade or manage it with --install-dir {directory}.'
        return f'\nThe Compose project {name} holds it, and its installation directory {directory} no longer exists. Remove it with: docker compose -p {name} down --volumes --remove-orphans (this deletes its data)'
    return ''


def check_port(host, port):
    with socket.socket(socket.AF_INET6 if ':' in host else socket.AF_INET) as probe:
        try:
            probe.bind((host, port))
        except OSError:
            raise ValueError(f'Web port {port} is unavailable; choose --web-port.{port_holder_hint(port)}') from None


def read_env(path):
    """The installation's saved settings."""
    pairs = (line.partition('=') for line in path.read_text().splitlines())
    return {name: value.strip("'") for name, _, value in pairs}


def compose_revision(text):
    """Return the release a Compose file starts, from init's OAC_REVISION."""
    return next((line.split(':', 1)[1].strip() for line in text.splitlines() if line.strip().startswith('OAC_REVISION:')), '')


def install(bundle, destination, host, port, public_url, upgrade):
    manifest = load_manifest(offline_root=bundle)
    revision = manifest['source_commit']
    for name in manifest['artifacts'].values():
        if not (bundle / 'artifacts' / name['filename']).is_file():
            raise ValueError('An offline distribution with all node artifacts is required')
    lock = Path(str(destination) + '.lock')
    lock.mkdir(parents=True, exist_ok=True, mode=0o700)
    descriptor = os.open(lock, os.O_RDONLY | os.O_DIRECTORY)
    created = committed = False
    try:
        # The same lock as oac apply and the other mutating oac commands.
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('Another operation is using this installation') from None
        # A staging directory left by a killed run is never published.
        for leftover in destination.glob('.staging-*'):
            shutil.rmtree(leftover)
        created = not destination.exists()
        existing = not created and any(destination.iterdir())
        if existing and not all((destination / name).is_file() for name in ('compose.yaml', '.env')):
            raise ValueError(f'Incomplete installation: preserve {destination} and choose another directory')
        previous = compose_revision((destination / 'compose.yaml').read_text()) if existing else ''
        if existing and not previous:
            raise ValueError(f'Incomplete installation: preserve {destination} and choose another directory')
        if previous and previous != revision and not upgrade:
            raise ValueError(f'This installation runs release {previous[:12]}; this bundle is {revision[:12]}. Back up the installation (Upgrade in the operations guide), then rerun with --upgrade.')
        if not existing and public_url is None:
            raise ValueError('--public-url is required for a new installation')
        check_docker()
        if not existing:
            check_port(host, port)
        # Docker validates both daemon access and the production template's required features.
        images = {name: ensure_docker_image(manifest, name, lambda name=name: bundle / 'images' / (name + '.tar'))
                  for name in ('core', 'web', 'ingress', 'database')}
        destination.mkdir(parents=True, exist_ok=True, mode=0o700)
        with tempfile.TemporaryDirectory(prefix='.staging-', dir=destination) as stage:
            stage = Path(stage)
            # A new installation is assembled in the stage and moved in only after its check.
            root = destination if existing else stage
            payload = root / 'node-payload'
            node_payload.publish(payload, bundle)
            # These files contain only public distribution bytes and must be readable by Web.
            for directory, dirs, files in os.walk(payload):
                os.chmod(directory, 0o755)
                for name in files:
                    os.chmod(Path(directory) / name, 0o644)
            if not existing:
                (stage / '.env').write_text(f"COMPOSE_PROJECT_NAME=oac-{uuid.uuid4().hex[:10]}\nOAC_HOST='{host}'\nOAC_WEB_PORT='{port}'\nOAC_PUBLIC_URL='{public_url}'\n")
                os.chmod(stage / '.env', 0o600)
            template = (bundle / 'compose.yaml.template').read_text().replace('__OAC_REVISION__', revision).replace('__OAC_INIT_IMAGE__', images['ingress'])
            for name in ('core', 'web', 'database'):
                template = template.replace('__OAC_' + name.upper() + '_IMAGE__', images[name])
            (stage / 'compose.yaml').write_text(template)
            # The installer owns only the service images and Web's payload bind; other entries are the operator's.
            saved = destination / 'compose.override.yaml'
            try:
                override = json.loads(saved.read_text()) if saved.is_file() else {}
            except ValueError:
                raise ValueError('compose.override.yaml must stay JSON so the installer can replace its own entries') from None
            services = override.setdefault('services', {})
            # compose.yaml is the one home of the image references; earlier installers also wrote them here.
            for name in ('core', 'web', 'init', 'database'):
                services.setdefault(name, {}).pop('image', None)
            web = services['web']
            web['volumes'] = [volume for volume in web.get('volumes', []) if not (isinstance(volume, dict) and volume.get('target') == '/node-payload')]
            web['volumes'].append({'type': 'bind', 'source': str(destination / 'node-payload'), 'target': '/node-payload', 'read_only': True})
            (stage / 'compose.override.yaml').write_text(json.dumps(override, indent=2) + '\n')
            os.chmod(stage / 'compose.override.yaml', 0o600)
            shutil.copyfile(bundle / 'oac', stage / 'oac')
            os.chmod(stage / 'oac', 0o700)
            compose = ('docker', 'compose', '-f', str(stage / 'compose.yaml'), '-f', str(stage / 'compose.override.yaml'), '--project-directory', str(root))
            if existing:
                # Init upgrades the data volume or refuses; until then the running release is untouched.
                # On the same release init comes first: it finishes a first start that stopped before it,
                # so check-config finds the secrets.
                checks = [('Configuration check', ('run', '--rm', '--no-deps', '--entrypoint', '/usr/local/bin/oac-core', 'core', 'check-config')),
                          ('Data volume check', ('run', '--rm', '--no-deps', 'init'))]
                if previous == revision:
                    checks.reverse()
                for step, check in checks:
                    failure = (f'{step} failed; no service was changed. See the output above.' if previous == revision
                               else f'Upgrade stopped; the running services are unchanged. {step} failed; see the output above.')
                    run(failure, *compose, *check, cwd=destination)
            else:
                run('Compose configuration check failed; see the output above. Nothing was installed.', *compose, 'config', '--quiet', cwd=destination)
            node_payload.activate(payload, revision)
            # compose.yaml goes last: it marks the directory as an installation.
            for name in ('oac', 'compose.override.yaml', 'compose.yaml') if existing else ('node-payload', '.env', 'oac', 'compose.override.yaml', 'compose.yaml'):
                os.replace(stage / name, destination / name)
            committed = True
        # Installations from earlier releases kept a checksum that no longer describes compose.yaml.
        (destination / 'compose-sha256sums.txt').unlink(missing_ok=True)
        print(f'Configuration ready in {destination}. Starting services…', flush=True)
        run('Starting services failed; see the output above. Settings and data are kept: fix the cause and rerun the same command.',
            'docker', 'compose', 'up', '-d', '--wait', '--wait-timeout', '180', '--pull', 'never', '--remove-orphans', cwd=destination)
        # A rerun of a start that lost the port must not report success: the console would be another installation's.
        settings = read_env(destination / '.env')
        # compose.yaml's defaults apply when .env leaves a setting empty.
        web_port = settings.get('OAC_WEB_PORT') or '8080'
        published = docker('docker', 'compose', 'port', 'web', '8080', cwd=destination)
        if published.returncode or not published.stdout.strip():
            raise ValueError(f"Web is running without its published port {web_port}; something else holds it.{port_holder_hint(web_port)}\nFree the port, then rerun the same command.")
        print(f"Console: {settings.get('OAC_PUBLIC_URL') or 'http://localhost:' + web_port}")
        if previous and previous != revision:
            print(f"Upgraded from {previous[:12]} to {revision[:12]}. Settings → Sandbox and scheduling offers this release's Runtime; sandboxes keep their current Runtime until you apply it.")
        if existing:
            print(f'Core key: {destination / "oac"} core-key --show')
        else:
            print('Core key:', flush=True)
            run(f'Reading the Core key failed; see the output above. Show it with {destination / "oac"} core-key --show.',
                'docker', 'compose', 'exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key', cwd=destination)
    finally:
        # A new installation that failed before compose.yaml was in place leaves nothing behind.
        if created and not committed:
            shutil.rmtree(destination, ignore_errors=True)
        os.close(descriptor)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--install-dir', type=Path, default=Path.home() / '.oac' / 'local')
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--web-port', type=int, default=8080)
    parser.add_argument('--public-url', help='HTTP(S) origin reachable by browsers and nodes; required for a new installation')
    parser.add_argument('--upgrade', action='store_true', help='upgrade an existing installation to this release')
    args = parser.parse_args()
    if not args.install_dir.is_absolute() or args.install_dir == Path('/'):
        parser.error('installation directory must be absolute and cannot be /')
    try:
        ipaddress.ip_address(args.host)
    except ValueError:
        parser.error('host must be an IP address')
    if not 1 <= args.web_port <= 65535:
        parser.error('web port must be between 1 and 65535')
    if args.public_url is not None:
        if not valid_public_origin(args.public_url):
            parser.error('public URL must be a canonical http or https origin without a path, such as https://core.example')
    destination = args.install_dir.resolve()
    try:
        install(BUNDLE, destination, args.host, args.web_port, args.public_url, args.upgrade)
    except KeyboardInterrupt:
        sys.exit('Interrupted; rerun the same command to continue.')
    except PermissionError as error:
        # Typically left by an earlier run under sudo. A path that could not be created names its parent.
        path = Path(error.filename or destination)
        if destination in path.parents:
            path = destination
        elif not path.exists():
            path = path.parent
        sys.exit(f'{path} is not writable by this account; if an earlier run used sudo, return it with: sudo chown -R "$(id -un)" {path}')
    except OSError as error:
        sys.exit(f'{error.strerror}: {error.filename}' if error.strerror and error.filename else str(error))
    except (ValueError, DistributionError, node_payload.PayloadError) as error:
        sys.exit(str(error))


if __name__ == '__main__':
    main()
