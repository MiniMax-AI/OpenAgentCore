"""Five bounded SDK operations for an already authorized Core allocation."""
import json
import time
from datetime import datetime, timezone
from uuid import UUID

from e2b import Sandbox, SandboxQuery, SandboxState
from e2b.exceptions import FileNotFoundException, SandboxNotFoundException

from sdk import connection_material, definitely_rejected, restore, run, validate_deployment
from state import Failure, Receipt

PREFIX = 'parsar_'
FIELDS = ('InstallationID', 'TenantID', 'EnvironmentID', 'AllocationID')


def valid_id(value):
    try:
        return isinstance(value, str) and str(UUID(value)) == value and UUID(value).int != 0
    except (ValueError, AttributeError):
        return False


class Provider:
    def __init__(self, request):
        self.q = request
        self.config = request['Config']
        self.reference = request['Reference']
        if (request['Version'] != 1 or request['Operation'] not in
                ('create', 'inspect', 'renew', 'kill', 'command', 'validate_deployment') or
                (request['Operation'] != 'validate_deployment' and
                 (set(self.reference) != set(FIELDS[1:]) or
                  any(not valid_id(v) for v in self.reference.values()))) or
                not valid_id(self.config['InstallationID'])):
            raise Failure('invalid')
        deadline = datetime.fromisoformat(request['Deadline'].replace('Z', '+00:00'))
        self.deadline = time.monotonic() + (deadline - datetime.now(timezone.utc)).total_seconds()
        self.metadata = {PREFIX + field.lower(): value for field, value in
                         dict(self.reference, InstallationID=self.config['InstallationID']).items()}
        self.receipt = None

    def remaining(self):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise Failure('unconfirmed')
        return remaining

    def options(self):
        return {'api_key': self.config['APIKey'], 'retries': 0, 'debug': False,
                'request_timeout': self.remaining()}

    def info(self, cloud=None, absent=False):
        record = self.receipt.data or {}
        ids = record.get('ids', [])
        absent = absent or self.rejected_absence()
        value = dict(self.reference, ProviderID=ids[0] if len(ids) == 1 else '', State='absent' if absent else 'unknown',
                     BootstrapComplete=False, CreateSettled=record.get('settled', False))
        if cloud is not None:
            value.update(ProviderID=cloud.sandbox_id, State=cloud.state,
                         BootstrapComplete=record.get('bootstrap_complete', False))
        return value

    def rejected_absence(self):
        record = self.receipt.data or {}
        return (record.get('status') == 'rejected' and record.get('settled') is True
                and record.get('ids') == [])

    def owns(self, cloud):
        if any(cloud.metadata.get(key) != value for key, value in self.metadata.items()):
            raise Failure('ownership')
        return cloud

    def qualified(self, cloud):
        resources = self.config.get('Resources')
        if resources is not None:
            template = self.config['Template'].split(':', 1)[0]
            if (cloud.template_id != template or type(cloud.cpu_count) is not int or
                    cloud.cpu_count != resources['cpus'] or type(cloud.memory_mb) is not int or
                    cloud.memory_mb != resources['memory_mib']):
                raise Failure('invalid')
        return cloud

    def discover(self):
        record = self.receipt.data or {}
        ids = record.get('ids', [])
        found = []
        if ids:
            for sandbox_id in ids:
                try:
                    found.append(self.owns(Sandbox.get_info(sandbox_id, **self.options())))
                except SandboxNotFoundException:
                    pass
            return found
        paginator = Sandbox.list(query=SandboxQuery(metadata=self.metadata,
                                                     state=[SandboxState.RUNNING, SandboxState.PAUSED]), **self.options())
        while paginator.has_next:
            self.remaining()
            for cloud in paginator.next_items(**self.options()):
                found.append(self.owns(cloud))
        # Retain every candidate; never pick one of several for initialization.
        if found:
            self.receipt.save(ids=sorted({cloud.sandbox_id for cloud in found}))
        return found

    def client(self, cloud):
        material = (self.receipt.data or {}).get('connection')
        if not material or material['sandbox_id'] != cloud.sandbox_id:
            raise Failure('unconfirmed')
        return restore(material, self.options())

    def inspect(self):
        if self.rejected_absence():
            return None
        found = self.discover()
        if not found:
            if (self.receipt.data or {}).get('settled'):
                return None
            raise Failure('not_found')
        if len(found) != 1:
            raise Failure('unconfirmed')
        cloud = found[0]
        self.qualified(cloud)
        record = self.receipt.data or {}
        if (cloud.state == 'running' and not record.get('bootstrap_complete') and record.get('connection')
                and record.get('status') not in ('bootstrap_failed', 'killed')):
            try:
                receipt = json.loads(self.client(cloud).files.read('/root/.parsar/e2b/managed-ready.json',
                                      user='root', request_timeout=self.remaining()))
            except FileNotFoundException:
                receipt = None
            if receipt is not None:
                expected = record.get('bootstrap_identity')
                if (receipt.get('identity') != expected or receipt.get('status') != 'daemon_started' or
                        type(receipt.get('daemon_pid')) is not int or receipt['daemon_pid'] <= 0):
                    raise Failure('ownership')
                self.receipt.save(settled=True, bootstrap_complete=True)
        return cloud

    def create(self):
        if self.receipt.data is not None:
            raise Failure('exists')
        bootstrap = self.q['Bootstrap']
        if any(bootstrap.get(field) != value for field, value in self.reference.items()):
            raise Failure('invalid')
        identity = dict(self.reference, InstallationID=self.config['InstallationID'],
                        SessionID=bootstrap['SessionID'], DeviceID=bootstrap['DeviceID'])
        self.receipt.save(status='create_pending', bootstrap_identity=identity)
        try:
            cloud = Sandbox.create(template=self.config['Template'], timeout=self.config['TimeoutSeconds'],
                                   metadata=self.metadata, lifecycle={'on_timeout': 'kill', 'auto_resume': False},
                                   **self.options())
        except Exception as error:
            if definitely_rejected(error):
                self.receipt.save(status='rejected', settled=True)
            raise Failure('unconfirmed') from None
        self.receipt.save(status='created', ids=[cloud.sandbox_id], connection=connection_material(cloud))
        # Creation responses do not include resources. Inspect before credentials
        # or bootstrap are written, retaining the allocation for owned cleanup.
        if self.config.get('Resources') is not None:
            try:
                self.qualified(self.owns(Sandbox.get_info(cloud.sandbox_id, **self.options())))
            except Failure:
                self.receipt.save(status='configuration_rejected', settled=True)
                raise
        payload = dict(bootstrap, InstallationID=self.config['InstallationID'])
        cloud.files.write('/root/.parsar/e2b/managed-bootstrap.json', json.dumps(payload),
                          user='root', request_timeout=self.remaining())
        self.receipt.save(status='bootstrap_pending')
        result = run(cloud, {'Args': ['/usr/bin/python3', '-I', '/opt/parsar-e2b/managed_init.py']},
                     self.remaining, user='root')
        if result['ExitCode'] != 0:
            self.receipt.save(status='bootstrap_failed', settled=True)
            raise Failure('unconfirmed')
        self.receipt.save(status='bootstrap_exited', settled=True)
        return self.inspect()

    def renew(self):
        cloud = self.inspect()
        if cloud is None or cloud.state != 'running':
            raise Failure('unconfirmed')
        Sandbox.set_timeout(cloud.sandbox_id, self.config['TimeoutSeconds'], **self.options())
        return self.qualified(self.owns(Sandbox.get_info(cloud.sandbox_id, **self.options())))

    def kill(self):
        if self.rejected_absence():
            return
        found = self.discover()
        # The allocation flock excludes any still-running local Create helper.
        # A matching actual VM proves the original request reached allocation.
        known = bool((self.receipt.data or {}).get('ids'))
        if not known and not (self.receipt.data or {}).get('settled'):
            raise Failure('unconfirmed')
        for cloud in found:
            Sandbox.kill(cloud.sandbox_id, **self.options())
        if self.discover():
            raise Failure('unconfirmed')
        self.receipt.save(status='killed', settled=True, bootstrap_complete=False, connection=None)

    def execute(self):
        if self.q['Operation'] == 'validate_deployment':
            try:
                validate_deployment(self.config, self.remaining)
                return {'Version': 1, 'DeploymentValid': True, 'ErrorCode': ''}
            except Failure as error:
                return {'Version': 1, 'ErrorCode': error.code}
            except Exception:
                return {'Version': 1, 'ErrorCode': 'unconfirmed'}
        with Receipt(self.q, self.remaining) as self.receipt:
            try:
                operation = self.q['Operation']
                if operation == 'kill':
                    self.kill()
                    return {'Version': 1, 'Info': self.info(absent=True), 'ErrorCode': ''}
                if operation == 'command':
                    cloud = self.inspect()
                    if cloud.state != 'running' or not self.receipt.data.get('bootstrap_complete'):
                        raise Failure('unconfirmed')
                    result = run(self.client(cloud), self.q['Command'], self.remaining)
                    return {'Version': 1, 'Command': result, 'ErrorCode': ''}
                cloud = {'create': self.create, 'inspect': self.inspect, 'renew': self.renew}[operation]()
                return {'Version': 1, 'Info': self.info(cloud, absent=cloud is None), 'ErrorCode': ''}
            except Failure as error:
                info = self.info()
                if error.code == 'not_found':
                    info = dict(self.reference, ProviderID='', State='absent', BootstrapComplete=False, CreateSettled=False)
                return {'Version': 1, 'Info': info, 'ErrorCode': error.code}
            except Exception:
                return {'Version': 1, 'Info': self.info(), 'ErrorCode': 'unconfirmed'}
