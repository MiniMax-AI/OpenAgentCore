#!/usr/bin/env python3
"""One-shot managed startup: prepare the sandbox and start only Sandbox I/O."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
from uuid import UUID

# -I excludes the script directory from sys.path; load only its protected sibling.
_contract_spec = importlib.util.spec_from_file_location('helper_contract', Path(__file__).with_name('helper_contract_generated.py'))
contract = importlib.util.module_from_spec(_contract_spec)
_contract_spec.loader.exec_module(contract)

ROOT = Path('/root/.oac/e2b')
HOME = Path('/home/runtime')


def identity(payload):
    if not isinstance(payload, dict) or set(payload) != set(contract.MANAGED_BOOTSTRAP_FIELDS):
        raise ValueError('Invalid managed bootstrap fields')
    for field in contract.MANAGED_IDENTITY_FIELDS:
        value = payload[field]
        if not isinstance(value, str) or str(UUID(value)) != value or UUID(value).int == 0:
            raise ValueError('Invalid managed bootstrap identity')
    # Sandbox I/O validates its own input; this only keeps it a JSON object.
    if not isinstance(payload['SandboxIO'], dict):
        raise ValueError('Invalid managed bootstrap configuration')
    return {field: payload[field] for field in contract.MANAGED_IDENTITY_FIELDS}


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


def prepare_sandbox():
    """Restore the protected image and the Environment layout before Sandbox I/O starts."""
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
    subprocess.run(['mount', '--bind', '/environment/workspace', '/workspace'], check=True)
    for directory in [HOME, Path('/environment/workspace'), Path('/environment/initialization'),
                      Path('/environment/packages')]:
        os.chown(directory, 1000, 1000)
        directory.chmod(0o700)


def initialize():
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    ROOT.chmod(0o700)
    source = ROOT / 'managed-bootstrap.json'
    if any((ROOT / name).exists() for name in ['managed-launch.json', 'managed-ready.json']):
        raise RuntimeError('Sandbox startup cannot be replayed')
    if source.stat().st_size > 65536:
        raise ValueError('Managed bootstrap input too large')
    payload = json.loads(source.read_text())
    binding = identity(payload)
    # Claim before any side effect. An interrupted attempt must never start twice.
    write_private(ROOT / 'managed-launch.json', binding)
    prepare_sandbox()
    bootstrap = HOME / 'sandbox-io-bootstrap.json'
    write_private(bootstrap, payload['SandboxIO'], owner=1000)
    source.unlink()
    # Sandbox I/O is the only process started in the sandbox; its file is its
    # only input.
    with (HOME / 'sandbox-io.log').open('xb') as stream:
        os.fchmod(stream.fileno(), 0o600)
        os.fchown(stream.fileno(), 1000, 1000)
        child = subprocess.Popen(['/usr/local/bin/oac-sandbox-io', '--bootstrap-file', str(bootstrap)],
                                 cwd='/environment/workspace', env={}, user=1000, group=1000,
                                 extra_groups=[], start_new_session=True, stdin=subprocess.DEVNULL,
                                 stdout=stream, stderr=subprocess.STDOUT, umask=0o077)
    # This acknowledges process handoff only. Serving is Core's to observe.
    write_private(ROOT / 'managed-ready.tmp',
                  {'identity': binding, 'status': 'sandbox_io_started', 'sandbox_io_pid': child.pid})
    os.replace(ROOT / 'managed-ready.tmp', ROOT / 'managed-ready.json')
    sync_directory(ROOT)


if __name__ == '__main__':
    try:
        initialize()
    except Exception:
        raise SystemExit('Managed sandbox startup failed; retain and reclaim the owned sandbox') from None
