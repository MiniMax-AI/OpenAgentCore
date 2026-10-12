#!/usr/bin/env python3
"""Start an offline source distribution with the production Compose services."""
import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
from urllib.parse import urlsplit
import uuid


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def install(bundle, destination, host, port, public_url):
    # Use the distribution's own node download and publication implementation.
    sys.path.insert(0, str(bundle))
    from distribution import ensure_docker_image, load_manifest
    from node_payload import prepare_node_payload

    manifest = load_manifest(offline_root=bundle)
    revision = manifest['source_commit']
    if destination.exists() and any(destination.iterdir()):
        raise ValueError('Installation directory is not empty. Use docker compose up for an existing installation; use a fresh directory and data volume for another commit.')
    for name in manifest['artifacts'].values():
        if not (bundle / 'artifacts' / name['filename']).is_file():
            raise ValueError('An offline distribution with all node artifacts is required')
    # Docker validates both daemon access and the production template's required features.
    images = {name: ensure_docker_image(manifest, name, lambda name=name: bundle / 'images' / (name + '.tar'))
              for name in ('core', 'web', 'ingress', 'database')}
    destination.mkdir(parents=True, exist_ok=True, mode=0o700)
    prepare_node_payload(destination, None, bundle)
    payload = destination / 'node-payload'
    # These files contain only public distribution bytes and must be readable by Web.
    for root, dirs, files in os.walk(payload):
        os.chmod(root, 0o755)
        for name in files:
            os.chmod(Path(root) / name, 0o644)
    template = (bundle / 'compose.yaml.template').read_text().replace('__OAC_REVISION__', revision).replace('__OAC_INIT_IMAGE__', images['ingress'])
    override = {'services': {name: {'image': images[image]} for name, image in
                            (('core', 'core'), ('web', 'web'), ('init', 'ingress'), ('database', 'database'))}}
    override['services']['web']['volumes'] = [{'type': 'bind', 'source': str(payload), 'target': '/node-payload', 'read_only': True}]
    (destination / 'compose.yaml').write_text(template)
    (destination / 'compose.override.yaml').write_text(json.dumps(override, indent=2) + '\n')
    (destination / 'compose-sha256sums.txt').write_text(hashlib.sha256(template.encode()).hexdigest() + '  compose.yaml\n')
    (destination / '.env').write_text(f"COMPOSE_PROJECT_NAME=oac-{uuid.uuid4().hex[:10]}\nOAC_HOST='{host}'\nOAC_WEB_PORT='{port}'\nOAC_PUBLIC_URL='{public_url}'\n")
    os.chmod(destination / '.env', 0o600)
    shutil.copyfile(bundle / 'oac', destination / 'oac')
    os.chmod(destination / 'oac', 0o700)
    run('docker', 'compose', 'config', '--quiet', cwd=destination)
    print(f'Configuration ready in {destination}. Starting services…', flush=True)
    run('docker', 'compose', 'up', '-d', '--wait', '--wait-timeout', '180', '--pull', 'never', cwd=destination)
    print(f'Console: {public_url}\nCore key:', flush=True)
    run('docker', 'compose', 'exec', '-T', 'web', '/usr/local/bin/oac-web', 'core-key', cwd=destination)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle-dir', type=Path, default=Path(__file__).resolve().parent)
    parser.add_argument('--install-dir', type=Path, default=Path.home() / '.oac' / 'local')
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--web-port', type=int, default=8080)
    parser.add_argument('--public-url', required=True, help='HTTP(S) origin reachable by browsers and nodes')
    args = parser.parse_args()
    if not args.install_dir.is_absolute() or args.install_dir == Path('/') or not args.bundle_dir.is_absolute():
        parser.error('bundle and installation directories must be absolute; installation cannot be /')
    ipaddress.ip_address(args.host)
    if not 1 <= args.web_port <= 65535:
        parser.error('web port must be between 1 and 65535')
    url = urlsplit(args.public_url)
    if url.scheme not in ('http', 'https') or not url.hostname or url.username or url.password or url.path not in ('', '/') or url.query or url.fragment or any(c in args.public_url for c in "\r\n'\\$"):
        parser.error('public URL must be an HTTP(S) origin without credentials')
    install(args.bundle_dir.resolve(), args.install_dir.resolve(), args.host, args.web_port, args.public_url)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
