#!/usr/bin/env python3
"""Application-owned E2B startup example using the maintained SDK."""
import argparse
import json
import os
from pathlib import Path
from uuid import UUID

from e2b import Sandbox
from init import launch_identity, sync_directory, write_private


def launch(payload, template, api_key, record_path, timeout=7200):
    identity = launch_identity(payload)
    try:
        template_id, build_id = template.split(':')
        if not template_id or str(UUID(build_id)) != build_id or UUID(build_id).int == 0 or timeout <= 0:
            raise ValueError()
    except (ValueError, AttributeError):
        raise ValueError('Use a pinned templateID:build_UUID and a positive lease') from None
    record_path = Path(record_path)
    if not record_path.is_absolute():
        raise ValueError('Use an absolute private launch record path')
    record_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    record = dict(identity, template=template, status='create_pending')
    # Exclusive creation prevents accidental retries, including unknown Create outcomes.
    write_private(record_path, record)

    def save(status, **fields):
        record.update(status=status, **fields)
        temporary = record_path.with_name(record_path.name + '.tmp')
        write_private(temporary, record)
        os.replace(temporary, record_path)
        sync_directory(record_path.parent)

    try:
        sandbox = Sandbox.create(
            template=template, timeout=timeout, api_key=api_key,
            metadata={'parsar_launch_id': payload['launch_id'],
                      'parsar_environment_id': payload['environment_id']},
            lifecycle={'on_timeout': 'kill', 'auto_resume': False})
        # Retain the actual provider ID before uploading credentials or starting anything.
        save('created', sandbox_id=sandbox.sandbox_id)
        save('startup_pending')
        sandbox.files.write('/root/.parsar/e2b/bootstrap.json', json.dumps(payload),
                            user='root', request_timeout=30)
        sandbox.commands.run('/usr/bin/python3 /opt/parsar-e2b/init.py', user='root', timeout=60)
        receipt = json.loads(sandbox.files.read('/root/.parsar/e2b/ready.json',
                                               user='root', request_timeout=30))
        if (any(receipt.get(key) != value for key, value in identity.items())
                or receipt.get('status') != 'daemon_started'
                or type(receipt.get('daemon_pid')) is not int or receipt['daemon_pid'] <= 0):
            raise ValueError('Unexpected startup receipt')
        save('daemon_started', receipt=receipt)
        return record
    except Exception:
        # SDK exceptions can contain request/command details. Keep the durable record,
        # do not log credentials, repeat Create/start, or implicitly delete evidence.
        raise RuntimeError('Launch outcome uncertain; inspect the private record and owned sandbox') from None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--remote-url', required=True)
    parser.add_argument('--environment-id', required=True)
    parser.add_argument('--launch-id', required=True, help='Application-generated UUID, retained before Create')
    parser.add_argument('--template', required=True, help='Immutable templateID:build_UUID')
    parser.add_argument('--executor-key-file', required=True, type=Path)
    parser.add_argument('--api-key-file', required=True, type=Path)
    parser.add_argument('--record', required=True, type=Path)
    parser.add_argument('--timeout', type=int, default=7200, help='User-owned lease in seconds')
    args = parser.parse_args()
    try:
        result = launch(
            {'launch_id': args.launch_id, 'environment_id': args.environment_id,
             'remote_url': args.remote_url,
             'executor_key': json.loads(args.executor_key_file.read_text())},
            args.template, args.api_key_file.read_text().strip(), args.record, args.timeout)
    except Exception:
        parser.exit(1, 'Startup not confirmed. Inspect your launch record; do not rerun or replace the sandbox.\n')
    print(json.dumps(result))


if __name__ == '__main__':
    main()
