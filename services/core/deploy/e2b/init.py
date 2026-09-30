#!/usr/bin/env python3
"""One-shot user-owned Runtime startup; never an enrollment or execution service."""
import json
import os
from pathlib import Path
import subprocess
from uuid import UUID
from urllib.parse import urlsplit

ROOT = Path('/root/.oac/e2b')
PROFILE = Path('/home/runtime/.oac/daemon/default')
IMAGE_ENV = Path('/etc/oac-runtime-env.json')


def launch_identity(payload):
    """Reject malformed startup input without including confidential values in errors."""
    if not isinstance(payload, dict) or set(payload) != {
            'launch_id', 'environment_id', 'remote_url', 'executor_key'}:
        raise ValueError('Invalid Runtime startup fields')
    try:
        for field in ['launch_id', 'environment_id']:
            value = payload[field]
            if not isinstance(value, str) or str(UUID(value)) != value or UUID(value).int == 0:
                raise ValueError()
        key = payload['executor_key']
        if (not isinstance(key, dict) or set(key) - {'key_id', 'executor_token', 'environment_id'}
                or str(UUID(key['key_id'])) != key['key_id'] or UUID(key['key_id']).int == 0
                or not isinstance(key['executor_token'], str) or not key['executor_token']
                or any(character.isspace() or character == '\x00' for character in key['executor_token'])
                or key.get('environment_id') not in (None, '', payload['environment_id'])
                or len(json.dumps(key).encode()) > 16384):
            raise ValueError()
        # Keep credentials out of argv/receipts; the daemon owns enrollment validation.
        remote = payload['remote_url']
        address = urlsplit(remote)
        if (not isinstance(remote, str) or address.scheme not in ('ws', 'wss')
                or not address.hostname or address.username is not None
                or address.query or address.fragment or '?' in remote or '#' in remote
                or address.path != '/api/v1/agent-daemon/ws' or remote.strip() != remote
                or len(json.dumps(payload).encode()) > 32768):
            raise ValueError()
    except (KeyError, ValueError, TypeError, AttributeError):
        raise ValueError('Invalid Runtime startup identity or credential') from None
    return {field: payload[field] for field in ['launch_id', 'environment_id', 'remote_url']}


def sync_directory(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def write_private(path, value, owner=None):
    with path.open('x') as stream:
        os.fchmod(stream.fileno(), 0o600)
        if owner is not None:
            os.fchown(stream.fileno(), owner, owner)
        json.dump(value, stream)
        stream.flush()
        os.fsync(stream.fileno())
    sync_directory(path.parent)


def prepare_runtime():
    """Restore the protected image and shared Runtime layout before any daemon starts."""
    # E2B finalization makes /usr/local world-writable after template commands.
    subprocess.run(['chown', '-R', 'root:root', '/usr/local'], check=True)
    subprocess.run(['chmod', '-R', 'go-w', '/usr/local'], check=True)
    for protected in ['/usr/bin/envd', '/etc/inittab', '/etc/init.d/rcS']:
        if protected == '/etc/init.d/rcS' and not Path(protected).exists():
            continue
        os.chown(protected, 0, 0)
        os.chmod(protected, 0o755)
    # Disable E2B's unused passwordless sudo account before unprivileged startup.
    subprocess.run(['usermod', '--lock', '--shell', '/usr/sbin/nologin', 'user'], check=True)
    environment = json.loads(IMAGE_ENV.read_text())
    if (environment.get('OAC_RUNTIME_HOME') != '/home/runtime/.oac'
            or environment.get('OAC_RUNTIME_WORKSPACE') != '/environment/workspace'
            or any(key in environment for key in ['OAC_RUNTIME_ENVIRONMENT_ID',
                                                 'OAC_RUNTIME_SESSION_ID'])):
        raise ValueError('Image must contain an unbound packaged Runtime profile')
    environment['PATH'] = '/usr/local/bin:/usr/bin:/bin'
    subprocess.run(['mount', '--bind', '/environment/workspace', '/workspace'], check=True)
    PROFILE.mkdir(mode=0o700, parents=True, exist_ok=True)
    for directory in [Path('/home/runtime'), Path('/home/runtime/.oac'), PROFILE.parent, PROFILE,
                      Path('/environment/workspace'), Path('/environment/staging'),
                      Path('/environment/initialization'), Path('/environment/packages')]:
        os.chown(directory, 1000, 1000)
        directory.chmod(0o700)
    return environment


def initialize():
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    ROOT.chmod(0o700)
    source = ROOT / 'bootstrap.json'
    receipt = ROOT / 'ready.json'
    if any((ROOT / name).exists() for name in ['ready.json', 'launch.json', 'managed-launch.json', 'managed-ready.json']):
        raise RuntimeError('Runtime startup cannot be replayed; inspect or destroy this sandbox')
    if source.stat().st_size > 32768:
        raise ValueError('Runtime startup input too large')
    payload = json.loads(source.read_text())
    identity = launch_identity(payload)
    # Claim before any side effect. An interrupted attempt must never start twice.
    write_private(ROOT / 'launch.json', identity)
    environment = prepare_runtime()
    # Application-owned startup does not participate in Core-managed suspension.
    environment.pop("OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE", None)
    credential = PROFILE.parent / 'executor-key.json'
    write_private(credential, payload['executor_key'], owner=1000)
    source.unlink()
    with (PROFILE / 'daemon.log').open('xb') as stream:
        os.fchmod(stream.fileno(), 0o600)
        os.fchown(stream.fileno(), 1000, 1000)
        child = subprocess.Popen(
            ['/usr/local/bin/oac-daemon', 'connect', '--profile', 'default',
             '--remote', payload['remote_url'], '--environment-id', payload['environment_id'],
             '--credential-file', str(credential)],
            cwd='/environment/workspace', env=environment, user=1000, group=1000,
            extra_groups=[], start_new_session=True, stdin=subprocess.DEVNULL,
            stdout=stream, stderr=subprocess.STDOUT, umask=0o077)
    # This acknowledges process handoff only. Core owns enrollment and readiness.
    write_private(ROOT / 'ready.tmp', dict(identity, status='daemon_started', daemon_pid=child.pid))
    os.replace(ROOT / 'ready.tmp', receipt)
    sync_directory(ROOT)


if __name__ == '__main__':
    try:
        initialize()
    except Exception:
        raise SystemExit('Runtime startup failed; inspect the retained launch record and sandbox') from None
