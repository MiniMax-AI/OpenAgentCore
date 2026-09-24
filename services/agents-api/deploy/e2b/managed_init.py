#!/usr/bin/env python3
"""One-shot managed bootstrap using the existing daemon authentication profile."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
from urllib.parse import urlsplit
from uuid import UUID

# -I excludes the script directory from sys.path; load only its protected sibling.
_spec = importlib.util.spec_from_file_location('runtime_init', Path(__file__).with_name('init.py'))
shared = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(shared)


def identity(payload):
    fields = ['InstallationID', 'TenantID', 'EnvironmentID', 'AllocationID', 'SessionID', 'DeviceID']
    if not isinstance(payload, dict) or set(payload) != set(fields + ['CoreURL', 'Credential', 'NetworkAccess', 'AllowedDomains']):
        raise ValueError('Invalid managed bootstrap fields')
    for field in fields:
        value = payload[field]
        if not isinstance(value, str) or str(UUID(value)) != value or UUID(value).int == 0:
            raise ValueError('Invalid managed bootstrap identity')
    address = urlsplit(payload['CoreURL'])
    if (address.scheme not in ('http', 'https') or not address.hostname or address.username is not None or
            address.query or address.fragment or any(char.isspace() for char in payload['CoreURL'])):
        raise ValueError('Invalid managed bootstrap address')
    if (not isinstance(payload['Credential'], str) or not payload['Credential'] or
            any(char.isspace() or char == '\0' for char in payload['Credential']) or
            payload['NetworkAccess'] not in ('enabled', 'disabled', 'restricted') or
            payload['AllowedDomains'] is not None and
            (not isinstance(payload['AllowedDomains'], list) or
             any(not isinstance(domain, str) for domain in payload['AllowedDomains']))):
        raise ValueError('Invalid managed bootstrap configuration')
    return {field: payload[field] for field in fields}


def initialize():
    root = shared.ROOT
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    root.chmod(0o700)
    source = root / 'managed-bootstrap.json'
    if any((root / name).exists() for name in ['launch.json', 'ready.json', 'managed-launch.json', 'managed-ready.json']):
        raise RuntimeError('Runtime bootstrap cannot be replayed')
    if source.stat().st_size > 65536:
        raise ValueError('Managed bootstrap input too large')
    payload = json.loads(source.read_text())
    binding = identity(payload)
    shared.write_private(root / 'managed-launch.json', binding)
    environment = shared.prepare_runtime()
    environment.update(PARSAR_RUNTIME_ENVIRONMENT_ID=payload['EnvironmentID'],
                       PARSAR_RUNTIME_SESSION_ID=payload['SessionID'],
                       PARSAR_RUNTIME_NETWORK_ACCESS=payload['NetworkAccess'],
                       PARSAR_RUNTIME_ALLOWED_DOMAINS=json.dumps(payload['AllowedDomains'] or []))
    shared.write_private(shared.PROFILE / 'auth.json',
                         {'server_url': payload['CoreURL'], 'runtime_id': payload['DeviceID'],
                          'runner_credential': payload['Credential']}, owner=1000)
    source.unlink()
    with (shared.PROFILE / 'daemon.log').open('xb') as stream:
        os.fchmod(stream.fileno(), 0o600)
        os.fchown(stream.fileno(), 1000, 1000)
        child = subprocess.Popen(['/usr/local/bin/parsar-daemon', 'connect', '--profile', 'default'],
                                 cwd='/environment/workspace', env=environment, user=1000, group=1000,
                                 extra_groups=[], start_new_session=True, stdin=subprocess.DEVNULL,
                                 stdout=stream, stderr=subprocess.STDOUT, umask=0o077)
    shared.write_private(root / 'managed-ready.tmp',
                         {'identity': binding, 'status': 'daemon_started', 'daemon_pid': child.pid})
    os.replace(root / 'managed-ready.tmp', root / 'managed-ready.json')
    shared.sync_directory(root)


if __name__ == '__main__':
    try:
        initialize()
    except Exception:
        raise SystemExit('Managed Runtime startup failed; retain and reclaim the owned sandbox') from None
