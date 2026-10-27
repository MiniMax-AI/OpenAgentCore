"""Bounded SDK operations for an already authorized Core allocation, plus
read-only deployment validation and observation."""
from concurrent.futures import ThreadPoolExecutor
import json
import math
import time
from datetime import datetime, timezone
from urllib.parse import urlsplit
from uuid import UUID

from e2b import Sandbox, SandboxQuery, SandboxState
from e2b.api.client.models.sandbox_metric import SandboxMetric
from e2b.exceptions import AuthenticationException, FileNotFoundException, SandboxNotFoundException

from sdk import connection_material, definitely_rejected, list_builds, list_templates, read_metrics, restore, run, sdk_options, validate_deployment, verify_team_template
from state import Failure, Receipt, private_root, read_receipt
from helper_contract_generated import (PROTOCOL_VERSION, OPERATIONS, REQUEST_FIELDS, REFERENCE_FIELDS,
    MAX_CREDENTIAL_REFERENCES, MANAGED_BOOTSTRAP_FIELDS, MANAGED_BOOTSTRAP_REQUIRED_FIELDS, RUNTIME_BOOTSTRAP_VERSION)

PREFIX = 'oac_'
FIELDS = ('InstallationID', *REFERENCE_FIELDS)
PENDING_PAUSE_SETTLE_SECONDS = 600  # a pause request settles in seconds; one still unpaused after ten minutes is taken as not applied

# Exits 78 unless the template carries the startup script and a contract for this Runtime bootstrap version.
TEMPLATE_CHECK = """import importlib.util, os, sys
root, version = sys.argv[1], int(sys.argv[2])
script = os.path.join(root, 'managed_init.py')
try:
    spec = importlib.util.spec_from_file_location('template_contract', os.path.join(root, 'helper_contract_generated.py'))
    contract = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(contract)
    current = getattr(contract, 'RUNTIME_BOOTSTRAP_VERSION', None)
except Exception:
    current = None
sys.exit(78 if not os.path.isfile(script) or not os.access(script, os.R_OK) or current != version else 0)
"""
COMPUTE_OPERATIONS = ('compute', 'renew_compute', 'resume_compute', 'suspend', 'resume', 'kill_compute', 'delete_snapshot', 'command_compute')


def valid_id(value):
    try:
        return isinstance(value, str) and str(UUID(value)) == value and UUID(value).int != 0
    except (ValueError, AttributeError):
        return False


def valid_reference(reference):
    return (isinstance(reference, dict) and set(reference) == set(FIELDS[1:]) and
            all(valid_id(v) for v in reference.values()))


def utc(value):
    if not isinstance(value, datetime) or value.tzinfo is None:
        raise ValueError('timestamp without zone')
    return value.astimezone(timezone.utc).isoformat()


def observed(cloud, point):
    """Map one metrics point without changing E2B units. A malformed point is
    unavailable.

    Disk metrics need a newer envd; unless E2B reports both integer values and a
    positive total, disk stays unknown rather than an observed zero."""
    try:
        disk_known = (type(point.get('diskUsed')) is int and type(point.get('diskTotal')) is int and
                      point['diskTotal'] > 0)
        # SandboxMetric requires every field; placeholders for unused or unknown
        # fields are never reported.
        metric = SandboxMetric.from_dict({'memCache': 0, 'timestampUnix': 0, 'diskUsed': 0, 'diskTotal': 0, **point})
        cpu_count, cpu_pct = metric.cpu_count, metric.cpu_used_pct
        values = (metric.mem_used, metric.mem_total, metric.disk_used, metric.disk_total)
        if (type(cpu_count) is not int or cpu_count < 1 or type(cpu_pct) not in (int, float) or
                not math.isfinite(cpu_pct) or cpu_pct < 0 or
                any(type(v) is not int or v < 0 for v in values) or metric.mem_total < 1):
            raise ValueError('malformed metrics point')
        return dict(Status='observed', ObservedAt=utc(metric.timestamp), StartedAt=utc(cloud.started_at),
                    CPUCount=cpu_count, CPUUsedPct=cpu_pct, MemUsed=metric.mem_used, MemTotal=metric.mem_total,
                    DiskUsed=metric.disk_used if disk_known else None, DiskTotal=metric.disk_total if disk_known else None)
    except Exception:
        return {'Status': 'unavailable'}


# E2B sandboxes cannot reach private networks; the deny list closes the ranges
# a sandbox host could still route to.
BLOCKED_RANGES = ('127.0.0.0/8', '10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', '100.64.0.0/10', '169.254.0.0/16',
                  '::1/128', 'fc00::/7', 'fe80::/10')
ALL_TRAFFIC = '0.0.0.0/0'


def network_options(bootstrap):
    """The SDK network option for Core's egress: public access, or only Core and the model provider."""
    egress = bootstrap['Egress']
    if egress['public']:
        return {'deny_out': list(BLOCKED_RANGES)}
    allowed = [urlsplit(bootstrap['CoreURL']).hostname, *egress['endpoints']]
    return {'deny_out': [ALL_TRAFFIC], 'allow_out': sorted(set(allowed))}


class Provider:
    def __init__(self, request):
        if (not isinstance(request, dict) or
                any(key not in request for key in ('Version', 'Operation', 'Config', 'Reference', 'Deadline')) or
                not isinstance(request['Config'], dict) or not isinstance(request['Reference'], dict)):
            raise Failure('invalid')
        self.q = request
        self.config = request['Config']
        self.reference = request['Reference']
        self.references = request.get('References')
        if self.references is None:
            self.references = []
        if not isinstance(self.references, list):
            raise Failure('invalid')
        if (type(request['Version']) is not int or request['Version'] != PROTOCOL_VERSION or
                not isinstance(request['Operation'], str) or request['Operation'] not in OPERATIONS or
                set(request) - set(REQUEST_FIELDS) or
                (request['Operation'] not in ('validate_deployment', 'list_templates', 'list_builds', 'verify_credential') and
                 not valid_reference(self.reference)) or
                (request['Operation'] == 'verify_credential' and
                 (len(self.references) > MAX_CREDENTIAL_REFERENCES or
                  not all(valid_reference(r) for r in self.references))) or
                (request['Operation'] not in ('list_templates', 'list_builds') and
                 not valid_id(self.config.get('InstallationID')))):
            raise Failure('invalid')
        bootstrap = request.get('Bootstrap')
        if bootstrap is not None and (not isinstance(bootstrap, dict) or bootstrap.get('Workspace') is not None):
            raise Failure('invalid')
        deadline = datetime.fromisoformat(request['Deadline'].replace('Z', '+00:00'))
        self.deadline = time.monotonic() + (deadline - datetime.now(timezone.utc)).total_seconds()
        self.metadata = {PREFIX + field.lower(): value for field, value in
                         dict(self.reference, InstallationID=self.config.get('InstallationID', '')).items()}
        self.receipt = None

    def remaining(self):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise Failure('unconfirmed')
        return remaining

    def options(self):
        return sdk_options(self.config, self.remaining)

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

    def check_domain(self, cloud):
        domain = self.options()['domain']
        # The API may omit the domain of a sandbox on the configured one: SDK
        # Sandbox objects then fill in that domain, SandboxInfo leaves None.
        sandbox_domain = domain if cloud.sandbox_domain is None else cloud.sandbox_domain
        if not isinstance(sandbox_domain, str) or not (
                sandbox_domain == domain or sandbox_domain.endswith('.' + domain)):
            raise Failure('ownership')

    def qualified(self, cloud):
        self.check_domain(cloud)
        template = self.config['Template'].split(':', 1)[0]
        if cloud.template_id not in (template, self.config['Template']):
            raise Failure('invalid')
        resources = self.config.get('Resources')
        if resources is not None:
            if (type(cloud.cpu_count) is not int or
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

    def inspect(self, qualify=True):
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
        if qualify:
            self.qualified(cloud)
        record = self.receipt.data or {}
        if (cloud.state == 'running' and not record.get('bootstrap_complete') and record.get('connection')
                and record.get('status') not in ('bootstrap_failed', 'killed')):
            try:
                receipt = json.loads(self.client(cloud).files.read('/root/.oac/e2b/managed-ready.json',
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
                                   network=network_options(bootstrap), **self.options())
        except Exception as error:
            if definitely_rejected(error):
                self.receipt.save(status='rejected', settled=True)
            raise Failure('unconfirmed') from None
        self.receipt.save(status='created', ids=[cloud.sandbox_id], connection=connection_material(cloud))
        # A create response must not steer envd traffic to an unrelated host.
        self.check_domain(cloud)
        # SDK Create returns connection material, but no metadata or resources.
        # Read its exact ID before writing credentials, even when Core adopts the
        # template's resources and does not supply explicit limits.
        try:
            detail = self.owns(Sandbox.get_info(cloud.sandbox_id, **self.options()))
            self.qualified(detail)
        except Failure:
            self.receipt.save(status='configuration_rejected', settled=True)
            raise
        # Validate the current template entry point before writing any credential.
        check = run(cloud, {'Args': ['/usr/bin/python3', '-I', '-c', TEMPLATE_CHECK, '/opt/oac-e2b', str(RUNTIME_BOOTSTRAP_VERSION)]},
                    self.remaining, user='root')
        if check['ExitCode'] != 0:
            self.receipt.save(status='bootstrap_failed', settled=True)
            raise Failure('template_invalid' if check['ExitCode'] == 78 else 'unconfirmed')
        payload = dict(bootstrap, InstallationID=self.config['InstallationID'],
                       RuntimeBootstrap=self.q['RuntimeBootstrap'])
        del payload['CoreURL'], payload['Credential'], payload['SuspendControl'], payload['Egress']
        if (set(payload) - set(MANAGED_BOOTSTRAP_FIELDS) or
                not set(MANAGED_BOOTSTRAP_REQUIRED_FIELDS) <= set(payload)):
            raise Failure('invalid')
        cloud.files.write('/root/.oac/e2b/managed-bootstrap.json', json.dumps(payload),
                          user='root', request_timeout=self.remaining())
        self.receipt.save(status='bootstrap_pending')
        result = run(cloud, {'Args': ['/usr/bin/python3', '-I', '/opt/oac-e2b/managed_init.py']},
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

    def observe(self):
        """Latest metrics of the owned running sandbox, without locks, writes or connect.

        The receipt names the allocation's sandbox so that the metrics request
        runs alongside the labelled listing that confirms it is running."""
        try:
            record = read_receipt(self.config, private_root(self.config), self.reference) or {}
        except Failure as error:
            return {'Status': error.code}
        except Exception:
            return {'Status': 'unavailable'}
        ids = record.get('ids', [])
        if record.get('status') in ('killed', 'rejected'):
            return {'Status': 'not_running'}
        if len(ids) != 1 or not isinstance(ids[0], str):
            return {'Status': 'unavailable'}
        found = []
        with ThreadPoolExecutor(max_workers=1) as pool:
            metrics = pool.submit(read_metrics, self.config, ids[0], self.remaining)
            paginator = Sandbox.list(query=SandboxQuery(metadata=self.metadata, state=[SandboxState.RUNNING]),
                                     limit=100, **self.options())
            # Stop once the receipt's sandbox has been listed. Detection of a
            # second sandbox with the same allocation labels then covers only
            # the pages read; lifecycle discovery remains exhaustive.
            while paginator.has_next and not any(cloud.sandbox_id == ids[0] for cloud in found):
                found += [cloud for cloud in paginator.next_items(**self.options())
                          if all((cloud.metadata or {}).get(k) == v for k, v in self.metadata.items())]
            point = metrics.result()
        if not found:
            return {'Status': 'not_running'}
        if len(found) != 1 or found[0].sandbox_id != ids[0] or not isinstance(point, dict):
            return {'Status': 'unavailable'}
        return observed(found[0], point)

    # One sandbox spans every generation. The receipt's generation says which
    # incarnation owns it: pause leaves it as the snapshot and resume advances it.
    def generation(self):
        return (self.receipt.data or {}).get('generation', 0)

    def current(self, compute):
        """The sandbox of the exact current incarnation; any other generation is absent."""
        if compute['Generation'] != self.generation():
            raise Failure('not_found')
        cloud = self.inspect()
        if cloud is None:
            raise Failure('not_found')
        if compute.get('ID') and compute['ID'] != cloud.sandbox_id:
            raise Failure('ownership')
        return cloud

    def state(self, compute, cloud, **extra):
        status = getattr(cloud.state, 'value', cloud.state)
        return dict(Compute=dict(compute, ID=cloud.sandbox_id), Status=status,
                    BootstrapComplete=bool((self.receipt.data or {}).get('bootstrap_complete')), **extra)

    def lease(self, cloud):
        Sandbox.set_timeout(cloud.sandbox_id, self.config['TimeoutSeconds'], **self.options())
        return self.qualified(self.owns(Sandbox.get_info(cloud.sandbox_id, **self.options())))

    def get_compute(self):
        compute = self.q['Compute']
        return self.state(compute, self.current(compute))

    def renew_compute(self):
        compute = self.q['Compute']
        cloud = self.current(compute)
        if cloud.state == 'running':
            cloud = self.lease(cloud)
        return self.state(compute, cloud)

    def resume_compute(self):
        """Thaw only a running source; a paused sandbox is a snapshot, not a source."""
        compute = self.q['Compute']
        cloud = self.current(compute)
        if cloud.state != 'running':
            raise Failure('unconfirmed')
        return self.state(compute, self.lease(cloud))

    def command_compute(self):
        cloud = self.current(self.q['Compute'])
        if cloud.state != 'running' or not self.receipt.data.get('bootstrap_complete'):
            raise Failure('unconfirmed')
        return run(self.client(cloud), self.q['Command'], self.remaining)

    def snapshot(self, operation, source, cloud):
        return {'Compatibility': self.config['Compatibility'], 'Reference': source['Name'] + ':s-' + operation,
                'ID': cloud.sandbox_id, 'Evidence': '',
                'OperationID': operation, 'SourceGeneration': source['Generation'], 'SourceName': source['Name'],
                'SourceID': cloud.sandbox_id}

    def suspend(self):
        request = self.q['Suspend']
        source, operation = request['Source'], request['OperationID']
        cloud = self.current(source)
        if cloud.state == 'running' and not request['ObserveOnly']:
            # Record the attempt before the native call; pause of a paused sandbox is a no-op.
            self.receipt.save(pause={'operation_id': operation, 'status': 'pending', 'at': time.time()})
            Sandbox.pause(cloud.sandbox_id, **self.options())
            cloud = self.qualified(self.owns(Sandbox.get_info(cloud.sandbox_id, **self.options())))
        pause = (self.receipt.data or {}).get('pause') or {}
        if cloud.state != 'paused' or pause.get('operation_id') != operation:
            # A dispatched pause may still take effect, so running is never reported for it.
            pending = pause.get('operation_id') == operation and pause.get('status') == 'pending'
            if pending and request['ObserveOnly'] and cloud.state == 'running' and time.time() - pause.get('at', time.time()) > PENDING_PAUSE_SETTLE_SECONDS:
                # Observation proves the long-dispatched pause did not take effect; close it as not taken.
                self.receipt.save(pause={'operation_id': operation, 'status': 'not_taken'})
                return self.state(source, cloud)
            if request['ObserveOnly'] and not pending:
                return self.state(source, cloud)
            raise Failure('unconfirmed')
        self.receipt.save(pause={'operation_id': operation, 'status': 'paused'})
        return self.state(source, cloud, Snapshot=self.snapshot(operation, source, cloud), SourceStopped=True)

    def close_resume(self, operation, kind):
        data = self.receipt.data or {}
        closed = dict(data.get('closed', {}), **{operation: kind})
        self.receipt.save(closed=dict(list(closed.items())[-32:]), resume=None)

    def closed_state(self, target, operation):
        return {'Compute': target, 'Status': 'absent', 'BootstrapComplete': False, 'RestoreAttemptClosed': operation}

    def finish_resume(self, target, operation, cloud, material=None):
        values = {'generation': target['Generation'], 'resume': {'operation_id': operation, 'status': 'resumed'}}
        if material is not None:
            values['connection'] = material
        self.receipt.save(**values)
        return self.state(target, cloud)

    def resume(self):
        request = self.q['Resume']
        operation, snapshot, target = request['OperationID'], request['Snapshot'], request['Target']
        data = self.receipt.data or {}
        kind = data.get('closed', {}).get(operation)
        if kind == 'unusable':
            raise Failure('checkpoint_unusable')
        if kind:
            return self.closed_state(target, operation)
        record = data.get('resume') or {}
        if record.get('operation_id') == operation:
            # Native restoration was dispatched: observe it, never dispatch again.
            cloud = self.inspect()
            if cloud is None:
                raise Failure('not_found')
            if cloud.state != 'running':
                raise Failure('unconfirmed')
            return self.finish_resume(target, operation, cloud)
        if request['ObserveOnly']:
            # No dispatch is recorded, so this attempt can still be closed durably.
            self.close_resume(operation, 'attempt')
            return self.closed_state(target, operation)
        pause = data.get('pause') or {}
        # Drift is judged below, before dispatch, as an unusable checkpoint.
        cloud = self.inspect(qualify=False)
        usable = (cloud is not None and cloud.state == 'paused' and pause.get('status') == 'paused' and
                  pause.get('operation_id') == snapshot['OperationID'] and
                  snapshot['SourceGeneration'] == self.generation() < target['Generation'] and
                  snapshot['SourceID'] == cloud.sandbox_id)
        if usable:
            try:
                self.qualified(cloud)
            except Failure:
                usable = False
        if not usable:
            self.close_resume(operation, 'unusable')
            raise Failure('checkpoint_unusable')
        self.receipt.save(resume={'operation_id': operation, 'target': target['Generation'], 'status': 'dispatched'})
        try:
            connected = Sandbox.connect(cloud.sandbox_id, timeout=self.config['TimeoutSeconds'], **self.options())
        except SandboxNotFoundException:
            # Dispatched: the outcome is unknown and the attempt stays open.
            raise Failure('unconfirmed') from None
        cloud = self.qualified(self.owns(Sandbox.get_info(cloud.sandbox_id, **self.options())))
        if cloud.state != 'running':
            raise Failure('unconfirmed')
        return self.finish_resume(target, operation, cloud, connection_material(connected))

    def kill_compute(self):
        """Kill only the sandbox's owning incarnation; a superseded one owns nothing."""
        compute = self.q['Compute']
        data = self.receipt.data or {}
        record = data.get('resume') or {}
        if compute['Generation'] == self.generation() or (record.get('status') == 'dispatched' and record.get('target') == compute['Generation']):
            self.kill_owned(compute.get('ID'))

    def delete_snapshot(self):
        """A snapshot is the paused sandbox itself; one consumed by resume owns nothing."""
        snapshot = self.q['Snapshot']
        if snapshot['SourceGeneration'] == self.generation():
            self.kill_owned(snapshot['SourceID'])

    def kill_owned(self, sandbox_id):
        ids = (self.receipt.data or {}).get('ids', [])
        if sandbox_id and ids and sandbox_id not in ids:
            raise Failure('ownership')
        self.kill()

    def metadata_for(self, reference):
        return {PREFIX + field.lower(): value for field, value in
                dict(reference, InstallationID=self.config['InstallationID']).items()}

    def verify_credential(self):
        verify_team_template(self.config, self.remaining)
        validate_deployment(self.config, self.remaining)
        root = private_root(self.config)
        wanted = {}
        for reference in self.references:
            record = read_receipt(self.config, root, reference)
            if record is None:
                # Missing durable evidence is not proof that Create never ran.
                raise Failure('unconfirmed')
            if record.get('status') in ('killed', 'rejected') and record.get('settled') is True:
                continue
            if record.get('settled') is not True:
                raise Failure('unconfirmed')
            ids = record.get('ids')
            if not isinstance(ids, list) or not ids or any(not isinstance(i, str) or not i for i in ids):
                raise Failure('unconfirmed')
            for identity in ids:
                if identity in wanted:
                    raise Failure('unconfirmed')
                wanted[identity] = self.metadata_for(reference)
        if not wanted:
            return
        paginator = Sandbox.list(query=SandboxQuery(metadata={PREFIX + 'installationid': self.config['InstallationID']},
                                 state=[SandboxState.RUNNING, SandboxState.PAUSED]), limit=100, **self.options())
        for _ in range(100):
            self.remaining()
            if not paginator.has_next:
                raise Failure('team_mismatch')
            page = paginator.next_items(**self.options())
            if len(page) > 100:
                raise Failure('unconfirmed')
            for cloud in page:
                expected = wanted.get(cloud.sandbox_id)
                if expected is not None:
                    if any(cloud.metadata.get(k) != v for k, v in expected.items()):
                        raise Failure('team_mismatch')
                    del wanted[cloud.sandbox_id]
            if not wanted:
                return
        raise Failure('unconfirmed')

    def execute(self):
        if self.q['Operation'] in ('validate_deployment', 'observe', 'verify_credential', 'list_templates', 'list_builds'):
            try:
                if self.q['Operation'] == 'list_templates':
                    return {'Version': PROTOCOL_VERSION, 'Templates': list_templates(self.config, self.remaining), 'ErrorCode': ''}
                if self.q['Operation'] == 'list_builds':
                    return {'Version': PROTOCOL_VERSION, 'Builds': list_builds(self.config, self.remaining), 'ErrorCode': ''}
                if self.q['Operation'] == 'verify_credential':
                    self.verify_credential()
                    return {'Version': PROTOCOL_VERSION, 'DeploymentValid': True, 'ErrorCode': ''}
                if self.q['Operation'] == 'observe':
                    return {'Version': PROTOCOL_VERSION, 'Observation': self.observe(), 'ErrorCode': ''}
                verify_team_template(self.config, self.remaining)
                build = validate_deployment(self.config, self.remaining)
                return {'Version': PROTOCOL_VERSION, 'DeploymentValid': True, 'TemplateBuild': build, 'ErrorCode': ''}
            except AuthenticationException:
                return {'Version': PROTOCOL_VERSION, 'ErrorCode': 'unauthorized'}
            except Failure as error:
                return {'Version': PROTOCOL_VERSION, 'ErrorCode': error.code}
            except Exception:
                return {'Version': PROTOCOL_VERSION, 'ErrorCode': 'unconfirmed'}
        with Receipt(self.q, self.remaining) as self.receipt:
            try:
                operation = self.q['Operation']
                if operation == 'kill':
                    self.kill()
                    return {'Version': PROTOCOL_VERSION, 'Info': self.info(absent=True), 'ErrorCode': ''}
                if operation == 'command':
                    cloud = self.inspect()
                    if cloud.state != 'running' or not self.receipt.data.get('bootstrap_complete'):
                        raise Failure('unconfirmed')
                    result = run(self.client(cloud), self.q['Command'], self.remaining)
                    return {'Version': PROTOCOL_VERSION, 'Command': result, 'ErrorCode': ''}
                if operation in ('compute', 'renew_compute', 'resume_compute', 'suspend', 'resume'):
                    handler = {'compute': self.get_compute, 'renew_compute': self.renew_compute, 'resume_compute': self.resume_compute,
                               'suspend': self.suspend, 'resume': self.resume}[operation]
                    return {'Version': PROTOCOL_VERSION, 'State': handler(), 'ErrorCode': ''}
                if operation == 'command_compute':
                    return {'Version': PROTOCOL_VERSION, 'Command': self.command_compute(), 'ErrorCode': ''}
                if operation in ('kill_compute', 'delete_snapshot'):
                    {'kill_compute': self.kill_compute, 'delete_snapshot': self.delete_snapshot}[operation]()
                    return {'Version': PROTOCOL_VERSION, 'ErrorCode': ''}
                cloud = {'create': self.create, 'inspect': self.inspect, 'renew': self.renew}[operation]()
                return {'Version': PROTOCOL_VERSION, 'Info': self.info(cloud, absent=cloud is None), 'ErrorCode': ''}
            except Failure as error:
                if self.q['Operation'] in COMPUTE_OPERATIONS:
                    return {'Version': PROTOCOL_VERSION, 'ErrorCode': error.code}
                info = self.info()
                if error.code == 'not_found':
                    info = dict(self.reference, ProviderID='', State='absent', BootstrapComplete=False, CreateSettled=False)
                return {'Version': PROTOCOL_VERSION, 'Info': info, 'ErrorCode': error.code}
            except Exception:
                if self.q['Operation'] in COMPUTE_OPERATIONS:
                    return {'Version': PROTOCOL_VERSION, 'ErrorCode': 'unconfirmed'}
                return {'Version': PROTOCOL_VERSION, 'Info': self.info(), 'ErrorCode': 'unconfirmed'}
