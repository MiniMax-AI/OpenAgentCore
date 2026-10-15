#!/usr/bin/env python3
"""Start an offline source distribution with the production Compose services."""
import argparse
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit
import uuid


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def compose_revision(text):
    """Return the release a Compose file starts, from init's OAC_REVISION."""
    return next((line.split(':', 1)[1].strip() for line in text.splitlines() if line.strip().startswith('OAC_REVISION:')), '')


def install(bundle, destination, host, port, public_url, upgrade):
    # Use the distribution's own node download and publication implementation.
    sys.path.insert(0, str(bundle))
    from distribution import ensure_docker_image, load_manifest
    import node_payload

    manifest = load_manifest(offline_root=bundle)
    revision = manifest['source_commit']
    for name in manifest['artifacts'].values():
        if not (bundle / 'artifacts' / name['filename']).is_file():
            raise ValueError('An offline distribution with all node artifacts is required')
    lock = Path(str(destination) + '.lock')
    lock.mkdir(parents=True, exist_ok=True, mode=0o700)
    descriptor = os.open(lock, os.O_RDONLY | os.O_DIRECTORY)
    try:
        # The same lock as oac apply and the other mutating oac commands.
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('Another operation is using this installation') from None
        # A staging directory left by a killed run is never published.
        for leftover in destination.glob('.staging-*'):
            shutil.rmtree(leftover)
        existing = destination.exists() and any(destination.iterdir())
        if existing and not all((destination / name).is_file() for name in ('compose.yaml', '.env')):
            raise ValueError(f'Incomplete installation: preserve {destination} and choose another directory')
        previous = compose_revision((destination / 'compose.yaml').read_text()) if existing else ''
        if existing and not previous:
            raise ValueError(f'Incomplete installation: preserve {destination} and choose another directory')
        if previous and previous != revision and not upgrade:
            raise ValueError(f'This installation runs release {previous[:12]}; this bundle is {revision[:12]}. Back up the installation (Upgrade in the operations guide), then rerun with --upgrade.')
        if not existing and public_url is None:
            raise ValueError('--public-url is required for a new installation')
        # Docker validates both daemon access and the production template's required features.
        images = {name: ensure_docker_image(manifest, name, lambda name=name: bundle / 'images' / (name + '.tar'))
                  for name in ('core', 'web', 'ingress', 'database')}
        destination.mkdir(parents=True, exist_ok=True, mode=0o700)
        payload = destination / 'node-payload'
        node_payload.publish(payload, bundle)
        # These files contain only public distribution bytes and must be readable by Web.
        for root, dirs, files in os.walk(payload):
            os.chmod(root, 0o755)
            for name in files:
                os.chmod(Path(root) / name, 0o644)
        if not existing:
            (destination / '.env').write_text(f"COMPOSE_PROJECT_NAME=oac-{uuid.uuid4().hex[:10]}\nOAC_HOST='{host}'\nOAC_WEB_PORT='{port}'\nOAC_PUBLIC_URL='{public_url}'\n")
            os.chmod(destination / '.env', 0o600)
        with tempfile.TemporaryDirectory(prefix='.staging-', dir=destination) as stage:
            stage = Path(stage)
            template = (bundle / 'compose.yaml.template').read_text().replace('__OAC_REVISION__', revision).replace('__OAC_INIT_IMAGE__', images['ingress'])
            for name in ('core', 'web'):
                template = template.replace('__OAC_' + name.upper() + '_IMAGE__', images[name])
            (stage / 'compose.yaml').write_text(template)
            # The installer owns only the service images and Web's payload bind; other entries are the operator's.
            saved = destination / 'compose.override.yaml'
            try:
                override = json.loads(saved.read_text()) if saved.is_file() else {}
            except ValueError:
                raise ValueError('compose.override.yaml must stay JSON so the installer can replace its own entries') from None
            services = override.setdefault('services', {})
            for name, image in (('core', 'core'), ('web', 'web'), ('init', 'ingress'), ('database', 'database')):
                services.setdefault(name, {})['image'] = images[image]
            web = services['web']
            web['volumes'] = [volume for volume in web.get('volumes', []) if not (isinstance(volume, dict) and volume.get('target') == '/node-payload')]
            web['volumes'].append({'type': 'bind', 'source': str(payload), 'target': '/node-payload', 'read_only': True})
            (stage / 'compose.override.yaml').write_text(json.dumps(override, indent=2) + '\n')
            os.chmod(stage / 'compose.override.yaml', 0o600)
            shutil.copyfile(bundle / 'oac', stage / 'oac')
            os.chmod(stage / 'oac', 0o700)
            compose = ('docker', 'compose', '-f', str(stage / 'compose.yaml'), '-f', str(stage / 'compose.override.yaml'), '--project-directory', str(destination))
            if existing:
                # Init upgrades the data volume or refuses; until then the running release is untouched.
                # On the same release init comes first: it finishes a first start that stopped before it,
                # so check-config finds the secrets.
                checks = [('run', '--rm', '--no-deps', '--entrypoint', '/usr/local/bin/oac-core', 'core', 'check-config'),
                          ('run', '--rm', '--no-deps', 'init')]
                if previous == revision:
                    checks.reverse()
                try:
                    for check in checks:
                        run(*compose, *check, cwd=destination)
                except subprocess.CalledProcessError:
                    raise ValueError('Upgrade stopped; the running services are unchanged') from None
            else:
                run(*compose, 'config', '--quiet', cwd=destination)
            node_payload.activate(payload, revision)
            for name in ('oac', 'compose.override.yaml', 'compose.yaml'):
                os.replace(stage / name, destination / name)
        # Installations from earlier releases kept a checksum that no longer describes compose.yaml.
        (destination / 'compose-sha256sums.txt').unlink(missing_ok=True)
        print(f'Configuration ready in {destination}. Starting services…', flush=True)
        run('docker', 'compose', 'up', '-d', '--wait', '--wait-timeout', '180', '--pull', 'never', '--remove-orphans', cwd=destination)
        if not existing:
            print(f'Console: {public_url}')
        if previous and previous != revision:
            print(f"Upgraded from {previous[:12]} to {revision[:12]}. Settings → Sandbox and scheduling offers this release's Runtime; sandboxes keep their current Runtime until you apply it.")
        print('Core key:', flush=True)
        run('docker', 'compose', 'exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key', cwd=destination)
    finally:
        os.close(descriptor)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle-dir', type=Path, default=Path(__file__).resolve().parent)
    parser.add_argument('--install-dir', type=Path, default=Path.home() / '.oac' / 'local')
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--web-port', type=int, default=8080)
    parser.add_argument('--public-url', help='HTTP(S) origin reachable by browsers and nodes; required for a new installation')
    parser.add_argument('--upgrade', action='store_true', help='upgrade an existing installation to this release')
    args = parser.parse_args()
    if not args.install_dir.is_absolute() or args.install_dir == Path('/') or not args.bundle_dir.is_absolute():
        parser.error('bundle and installation directories must be absolute; installation cannot be /')
    ipaddress.ip_address(args.host)
    if not 1 <= args.web_port <= 65535:
        parser.error('web port must be between 1 and 65535')
    if args.public_url is not None:
        url = urlsplit(args.public_url)
        if url.scheme not in ('http', 'https') or not url.hostname or url.username or url.password or url.path not in ('', '/') or url.query or url.fragment or any(c in args.public_url for c in "\r\n'\\$"):
            parser.error('public URL must be an HTTP(S) origin without credentials')
    install(args.bundle_dir.resolve(), args.install_dir.resolve(), args.host, args.web_port, args.public_url, args.upgrade)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
