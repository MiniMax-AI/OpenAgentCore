"""The version-pinned SDK boundary. No handwritten provider HTTP or envd RPC."""
import base64
import shlex

from e2b import Sandbox
from e2b.connection_config import ConnectionConfig
from e2b.exceptions import AuthenticationException, SandboxException, ServiceBusyException
from e2b.sandbox.commands.command_handle import CommandExitException
from packaging.version import Version

from e2b.api.client_sync import get_api_client
from e2b.api.client.api.sandboxes import get_sandboxes_metrics
from e2b.api.client.api.templates import get_v2_templates, get_templates_template_id
from e2b.api.client.models.template import Template
from e2b.api.client.models.sandboxes_with_metrics import SandboxesWithMetrics
from e2b.api.client.models.template_with_builds import TemplateWithBuilds
from e2b.api.client.types import UNSET

from state import Failure

SDK_VERSION = '2.51.0'
MAX_OUTPUT = 1024 * 1024


def list_templates(config, remaining):
    cursor, seen, result = UNSET, set(), []
    for _ in range(20):
        client = get_api_client(ConnectionConfig(**sdk_options(config, remaining)))
        response = get_v2_templates.sync_detailed(client=client, next_token=cursor, limit=100)
        if response.status_code != 200 or not isinstance(response.parsed, list):
            raise Failure('invalid' if response.status_code in (400, 401, 403, 422) else 'unconfirmed')
        for item in response.parsed:
            if not isinstance(item, Template) or not isinstance(item.template_id, str):
                raise Failure('unconfirmed')
            names = [name for name in item.names if isinstance(name, str) and len(name) <= 128][:20]
            result.append({'id': item.template_id, 'names': names})
        if len(result) > 200:
            raise Failure('unconfirmed')
        cursor = response.headers.get('x-next-token')
        if not cursor:
            return result
        if len(cursor) > 4096 or cursor in seen:
            raise Failure('unconfirmed')
        seen.add(cursor)
    raise Failure('unconfirmed')


def list_builds(config, remaining):
    template = config['Template']
    cursor, seen, result = UNSET, set(), []
    for _ in range(20):
        client = get_api_client(ConnectionConfig(**sdk_options(config, remaining)))
        response = get_templates_template_id.sync_detailed(template_id=template, client=client,
                                                          next_token=cursor, limit=100)
        if response.status_code != 200 or not isinstance(response.parsed, TemplateWithBuilds):
            raise Failure('invalid' if response.status_code in (400, 401, 403, 404, 422) else 'unconfirmed')
        if response.parsed.template_id != template:
            raise Failure('unconfirmed')
        for build in response.parsed.builds:
            if build.status.value == 'ready' and type(build.cpu_count) is int and build.cpu_count > 0 and type(build.memory_mb) is int and build.memory_mb > 0:
                result.append({'id': str(build.build_id), 'cpus': build.cpu_count, 'memory_mib': build.memory_mb})
        if len(result) > 200:
            raise Failure('unconfirmed')
        cursor = response.headers.get('x-next-token')
        if not cursor:
            return result
        if len(cursor) > 4096 or cursor in seen:
            raise Failure('unconfirmed')
        seen.add(cursor)
    raise Failure('unconfirmed')


def sdk_options(config, remaining):
    # Explicit selectors survive the helper's removal of ambient E2B_* values.
    return {'api_key': config['APIKey'], 'api_url': config.get('APIURL') or 'https://api.e2b.app',
            'domain': config.get('Domain') or 'e2b.app', 'retries': 0, 'debug': False,
            'request_timeout': remaining()}


def validate_deployment(config, remaining):
    """Read the exact ready build through the pinned SDK, without allocating.

    Returns the build as read, for Core to record with the selection. Without
    Resources, Core adopts the ready build's CPU and memory as the selection."""
    resources = config.get('Resources')
    if resources is not None and (
            not isinstance(resources, dict) or type(resources.get('cpus')) is not int or
            not 1 <= resources['cpus'] <= 255 or
            type(resources.get('memory_mib')) is not int or
            not 512 <= resources['memory_mib'] <= 1048576 or
            resources.get('root_disk_mib', 0) != 0 or resources.get('environment_disk_mib', 0) != 0):
        raise Failure('invalid')
    template, build_id = config['Template'].split(':', 1)
    cursor, seen = UNSET, set()
    for _ in range(100):
        client = get_api_client(ConnectionConfig(**sdk_options(config, remaining)))
        response = get_templates_template_id.sync_detailed(template_id=template, client=client,
                                                          next_token=cursor, limit=100)
        if response.status_code != 200 or not isinstance(response.parsed, TemplateWithBuilds):
            raise Failure('unauthorized' if response.status_code in (401, 403) else 'invalid' if response.status_code in (400, 404, 422) else 'unconfirmed')
        result = response.parsed
        if result.template_id != template:
            raise Failure('invalid')
        matches = [build for build in result.builds if str(build.build_id) == build_id]
        if matches:
            build = matches[0]
            if (len(matches) != 1 or build.status.value != 'ready' or
                    type(build.cpu_count) is not int or build.cpu_count < 1 or
                    type(build.memory_mb) is not int or build.memory_mb < 1 or
                    resources is not None and (build.cpu_count != resources['cpus'] or
                                               build.memory_mb != resources['memory_mib'])):
                raise Failure('invalid')
            disk = build.disk_size_mb if type(build.disk_size_mb) is int and 0 < build.disk_size_mb < 2 ** 31 else None
            return {'Status': build.status.value, 'CPUs': build.cpu_count, 'MemoryMiB': build.memory_mb, 'RootDiskMiB': disk}
        cursor = response.headers.get('x-next-token')
        if not cursor:
            raise Failure('invalid')
        if len(cursor) > 4096 or cursor in seen:
            raise Failure('unconfirmed')
        seen.add(cursor)
    raise Failure('unconfirmed')


def read_metrics(config, sandbox_ids, remaining):
    """Latest metrics point per sandbox from one batch request of at most 100 IDs."""
    if not 1 <= len(sandbox_ids) <= 100:
        raise Failure('invalid')
    client = get_api_client(ConnectionConfig(**sdk_options(config, remaining)))
    response = get_sandboxes_metrics.sync_detailed(client=client, sandbox_ids=sandbox_ids)
    if (response.status_code != 200 or not isinstance(response.parsed, SandboxesWithMetrics) or
            not isinstance(response.parsed.sandboxes, dict)):
        raise Failure('unconfirmed')
    return response.parsed.sandboxes


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


def verify_team_template(config, remaining):
    """The pinned SDK's paginated team listing, not public template readability."""
    from e2b.api.client.api.templates import get_v2_templates
    from e2b.api.client.models.template import Template
    wanted = config['Template'].split(':', 1)[0]
    cursor, seen = UNSET, set()
    for _ in range(100):
        client = get_api_client(ConnectionConfig(**sdk_options(config, remaining)))
        response = get_v2_templates.sync_detailed(client=client, next_token=cursor, limit=100)
        if response.status_code in (401, 403):
            raise Failure('unauthorized')
        if response.status_code != 200 or not isinstance(response.parsed, list):
            raise Failure('unconfirmed')
        if len(response.parsed) > 100 or any(not isinstance(item, Template) for item in response.parsed):
            raise Failure('unconfirmed')
        if any(item.template_id == wanted for item in response.parsed):
            return
        cursor = response.headers.get('x-next-token')
        if not cursor:
            raise Failure('team_mismatch')
        if len(cursor) > 4096 or cursor in seen:
            raise Failure('unconfirmed')
        seen.add(cursor)
    raise Failure('unconfirmed')
