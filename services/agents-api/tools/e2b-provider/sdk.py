"""The version-pinned SDK boundary. No handwritten provider HTTP or envd RPC."""
import base64
import shlex

from e2b import Sandbox
from e2b.connection_config import ConnectionConfig
from e2b.exceptions import AuthenticationException, SandboxException, ServiceBusyException
from e2b.sandbox.commands.command_handle import CommandExitException
from packaging.version import Version

from state import Failure

SDK_VERSION = '2.51.0'
MAX_OUTPUT = 1024 * 1024


def connection_material(sandbox):
    # The pinned SDK has no non-mutating attach method; connect can resume a VM.
    # Keep this deprecated constructor boundary isolated and covered by SDK tests.
    return {'sandbox_id': sandbox.sandbox_id, 'sandbox_domain': sandbox.sandbox_domain,
            'envd_version': str(sandbox._envd_version),
            'envd_access_token': sandbox._envd_access_token,
            'traffic_access_token': sandbox.traffic_access_token}


def restore(material, options):
    headers = {'E2b-Sandbox-Id': material['sandbox_id'],
               'E2b-Sandbox-Port': str(ConnectionConfig.envd_port)}
    if material['envd_access_token']:
        headers['X-Access-Token'] = material['envd_access_token']
    return Sandbox(**dict(material, envd_version=Version(material['envd_version']),
                         connection_config=ConnectionConfig(extra_sandbox_headers=headers, **options)))


def definitely_rejected(error):
    # These SDK classes/statuses represent an explicit refused Create. Transport
    # failures and arbitrary 5xx responses can conceal a committed allocation.
    return (isinstance(error, (AuthenticationException, ServiceBusyException)) or
            isinstance(error, SandboxException) and error.status_code in (400, 401, 403, 404, 422, 429))


def run(sandbox, command, remaining, user='runtime'):
    raw = command.get('Stdin')
    data = base64.b64decode(raw, validate=True) if raw is not None else None
    if data is not None and len(data) > 50 * 1024 * 1024 + 32:
        raise Failure('invalid')
    args = command.get('Args')
    if not isinstance(args, list) or not args or any(not isinstance(arg, str) or '\0' in arg for arg in args):
        raise Failure('invalid')
    directory = command.get('Directory') or None
    if directory is not None and not directory.startswith('/'):
        raise Failure('invalid')
    counts = [0, 0]

    def bounded(index, text):
        counts[index] += len(text.encode())
        if counts[index] > MAX_OUTPUT:
            raise Failure('command_unconfirmed')

    try:
        process = sandbox.commands.run(shlex.join(args), user=user, cwd=directory,
                                       background=True, stdin=data is not None,
                                       timeout=remaining(), request_timeout=remaining())
        if data is not None:
            # Avoid an oversized unary SDK message; every chunk is submitted once.
            for offset in range(0, len(data), 64 * 1024):
                sandbox.commands.send_stdin(process.pid, data[offset:offset + 64 * 1024],
                                            request_timeout=remaining())
            sandbox.commands.close_stdin(process.pid, request_timeout=remaining())
        try:
            result = process.wait(on_stdout=lambda text: bounded(0, text),
                                  on_stderr=lambda text: bounded(1, text))
        except CommandExitException as error:
            result = error
        return {'Stdout': result.stdout, 'Stderr': result.stderr, 'ExitCode': result.exit_code}
    except Failure:
        raise
    except Exception:
        raise Failure('command_unconfirmed') from None
