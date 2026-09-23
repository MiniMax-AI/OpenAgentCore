#!/usr/bin/env python3
"""Start one user-owned V1 Runtime from the connected Core's distribution."""
import argparse
import fcntl
import getpass
import json
import os
from pathlib import Path
import platform
import re
import stat
import subprocess
import sys
from urllib.parse import urlsplit
import uuid

from distribution import DistributionError, load_manifest, obtain_artifact, runtime_archive


class InstallError(Exception):
    pass


def canonical_uuid(value):
    try:
        return isinstance(value, str) and str(uuid.UUID(value)) == value and uuid.UUID(value).int != 0
    except ValueError:
        return False


def identity(environment, remote):
    try:
        address = urlsplit(remote)
        address.port
        if (not canonical_uuid(environment) or address.scheme != 'wss' or not address.hostname
                or address.username is not None or address.password is not None
                or address.path != '/api/v1/agent-daemon/ws' or any(c in remote for c in '?#\\')
                or any(c.isspace() for c in remote)):
            raise ValueError()
    except (ValueError, TypeError):
        raise InstallError('Use the returned Environment UUID and unchanged reachable wss remote_url') from None
    return {'environment_id': environment, 'remote_url': remote}


def private_read(path):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path:
        raise InstallError('Credential and state files must have absolute paths without symlinks')
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd, 'rb') as stream:
            info = os.fstat(stream.fileno())
            if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
                    or info.st_uid != os.getuid() or info.st_nlink != 1 or info.st_size > 16384):
                raise InstallError('Credential and state files must be private mode-0600 owned regular files')
            return stream.read(16385)
    except OSError:
        raise InstallError('Cannot read private credential or state file') from None


def credential(raw, environment):
    try:
        if len(raw) > 16384:
            raise ValueError()
        value = json.loads(raw)
        if (not isinstance(value, dict) or set(value) != {'key_id', 'environment_id', 'executor_token'}
                or not canonical_uuid(value['key_id']) or value['environment_id'] != environment
                or not isinstance(value['executor_token'], str) or not value['executor_token']
                or any(c.isspace() or c == '\x00' for c in value['executor_token'])):
            raise ValueError()
    except (ValueError, TypeError, KeyError):
        raise InstallError('Invalid restricted executor credential JSON') from None
    return value


def write_private(path, value):
    data = json.dumps(value).encode()
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
    except OSError:
        raise InstallError('Cannot write private Runtime installation state; inspect retained files') from None


def checked(command, message, timeout=30):
    try:
        result = subprocess.run(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, check=False)
        if result.returncode:
            raise InstallError(message)
        return result.stdout.decode()
    except (OSError, subprocess.SubprocessError, UnicodeError):
        raise InstallError(message) from None


def preflight():
    if (sys.version_info < (3, 9) or platform.system() != 'Linux'
            or platform.machine() not in ('x86_64', 'amd64') or os.getuid() == 0):
        raise InstallError('Run Python 3.9+ as a non-root Linux amd64 user with Docker access')
    checked(['docker', '--host', 'unix:///var/run/docker.sock', 'info', '--format', '{{.ServerVersion}}'],
            'Docker access through /var/run/docker.sock is required')


def inspect_prior_launch(root, state):
    docker = 'docker --host unix:///var/run/docker.sock'
    filters = (' --filter label=io.parsar.agents-api.installation=' + state['installation_id']
               + ' --filter label=io.parsar.agents-api.environment=' + state['environment_id'])
    guidance = (' Inspect retained resources with: ' + docker + ' ps -a' + filters
                + '; ' + docker + ' volume ls' + filters
                + '. Do not delete the receipt or create replacement history.')
    receipt = root / 'started.json'
    if not receipt.exists():
        raise InstallError('A Runtime launch was already attempted without confirmed startup.' + guidance)
    prior = json.loads(private_read(receipt))
    name = prior.get('container', '') if isinstance(prior, dict) else ''
    if not re.fullmatch(r'parsar-selfhost-[0-9a-f]{32}', name) or prior.get('status') != 'started':
        raise InstallError('Invalid retained Runtime startup receipt.' + guidance)
    try:
        raw = checked(['docker', '--host', 'unix:///var/run/docker.sock', 'container', 'inspect', name,
                       '--format', '{{json .Config.Labels}} {{.State.Status}}'], 'Cannot inspect retained Runtime')
        labels, status = raw.strip().rsplit(' ', 1)
        labels = json.loads(labels)
    except (InstallError, ValueError):
        raise InstallError('The prior Runtime cannot be confirmed.' + guidance) from None
    expected = {'io.parsar.agents-api.installation': state['installation_id'],
                'io.parsar.agents-api.environment': state['environment_id'],
                'io.parsar.agents-api.user-owned': 'true'}
    if not isinstance(labels, dict) or any(labels.get(key) != value for key, value in expected.items()):
        raise InstallError('The retained container does not match this installation and Environment.' + guidance)
    if status == 'running':
        print('Runtime already running: ' + name)
        print('Read the Session to confirm connection; a running container does not establish it.')
        return
    if status == 'exited':
        raise InstallError('The existing Runtime is stopped. Preserve its history and resume that same container with: '
                           + docker + ' start ' + name + '. Then read the Session to confirm connection.')
    raise InstallError('The retained Runtime requires inspection before continuing.' + guidance)


def install(args, root):
    target = identity(args.environment_id, args.remote)
    manifest = load_manifest(source_url=args.source_url, offline_root=args.offline_root)
    revision = manifest.get('source_commit', '')
    runtime_image = manifest.get('images', {}).get('runtime', '')
    if (manifest.get('platform') != 'linux/amd64' or not re.fullmatch(r'[0-9a-f]{40}', revision)
            or not re.fullmatch(r'sha256:[0-9a-f]{64}', runtime_image)):
        raise InstallError('The distribution does not contain a matched Linux amd64 Runtime')
    target.update(source_commit=revision, runtime_image=runtime_image)
    state_file = root / 'installation.json'
    if state_file.exists():
        state = json.loads(private_read(state_file))
        if (not isinstance(state, dict) or set(state) != set(target) | {'installation_id'}
                or any(state.get(key) != value for key, value in target.items())
                or not canonical_uuid(state['installation_id'])):
            raise InstallError('This installation belongs to another Environment or distribution')
    else:
        state = dict(target, installation_id=str(uuid.uuid4()))
        write_private(state_file, state)
    if (root / 'launch.json').exists() or (root / 'launch.json').is_symlink():
        inspect_prior_launch(root, state)
        return
    key_file = root / 'executor-key.json'
    if key_file.exists():
        key = credential(private_read(key_file), args.environment_id)
        if args.credential_file and credential(private_read(Path(args.credential_file)), args.environment_id) != key:
            raise InstallError('Stored executor credential differs; inspect the existing installation')
    else:
        if not args.credential_file and not sys.stdin.isatty():
            raise InstallError('Use --credential-file in noninteractive sessions; credentials are never echoed')
        raw = private_read(Path(args.credential_file)) if args.credential_file else getpass.getpass('Restricted executor credential JSON (hidden): ')
        key = credential(raw, args.environment_id)
        write_private(key_file, key)
    launcher = obtain_artifact(manifest, 'native/bin/parsar-runtime', root / 'native/bin/parsar-runtime', args.offline_root)
    seccomp = obtain_artifact(manifest, 'runtime/seccomp.json', root / 'runtime/seccomp.json', args.offline_root)
    inspect = ['docker', '--host', 'unix:///var/run/docker.sock', 'image', 'inspect', runtime_image,
               '--format', '{{.Id}} {{.Os}}/{{.Architecture}}']
    try:
        image = checked(inspect, 'Runtime image is not installed').strip()
    except InstallError:
        image = None
    if image != runtime_image + ' linux/amd64':
        archive = runtime_archive(manifest, root, args.offline_root)
        checked(['docker', '--host', 'unix:///var/run/docker.sock', 'image', 'load', '--input', str(archive)],
                'Cannot load the matched Runtime image; retry after checking Docker', timeout=600)
        image = checked(inspect, 'Cannot verify the loaded Runtime image').strip()
    if image != runtime_image + ' linux/amd64':
        raise InstallError('Loaded Runtime image does not match the distribution')
    command = [str(launcher), '--installation-id', state['installation_id'],
               '--environment-id', args.environment_id, '--remote', args.remote,
               '--image', runtime_image, '--seccomp-file', str(seccomp), '--credential-file', str(key_file)]
    # Persist the attempt before Docker mutation. Uncertain outcomes require
    # inspection, never another bootstrap or replacement native history.
    write_private(root / 'launch.json', {'installation_id': state['installation_id'], 'environment_id': args.environment_id})
    result = json.loads(checked(command, 'Runtime launch failed or is uncertain. Inspect retained installation state and Docker containers', timeout=150))
    if (not isinstance(result, dict) or result.get('status') != 'started'
            or not re.fullmatch(r'parsar-selfhost-[0-9a-f]{32}', result.get('container', ''))):
        raise InstallError('Runtime launcher returned an invalid result; inspect the retained container')
    write_private(root / 'started.json', result)
    print('Runtime started: ' + result['container'])
    print('Read the Session to confirm connection. Stop this user-owned Runtime with: docker stop ' + result['container'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--source-url', help='Core console HTTPS origin')
    source.add_argument('--offline-root', type=Path, help='matching extracted offline distribution')
    parser.add_argument('--environment-id', required=True)
    parser.add_argument('--remote', required=True, help='unchanged Session environment remote_url')
    parser.add_argument('--credential-file', help='absolute private restricted executor credential JSON')
    parser.add_argument('--install-dir', type=Path)
    args = parser.parse_args()
    preflight()
    identity(args.environment_id, args.remote)
    root = args.install_dir or Path.home() / '.parsar/self-hosted' / args.environment_id
    if not root.is_absolute() or root.resolve() != root:
        raise InstallError('Installation directory must be absolute and have no symlinks')
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = root.stat()
    if not root.is_dir() or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise InstallError('Installation directory must be owned by this user and mode 0700')
    lock = os.open(root / '.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(lock, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o600 or info.st_nlink != 1:
            raise InstallError('Installation lock must be private and owned')
        try:
            fcntl.flock(stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise InstallError('Another installation is already running') from None
        install(args, root)


if __name__ == '__main__':
    try:
        main()
    except (InstallError, DistributionError) as error:
        raise SystemExit(str(error)) from None
    except (OSError, ValueError, KeyError, TypeError):
        raise SystemExit('Runtime installation failed; inspect the retained installation state') from None
