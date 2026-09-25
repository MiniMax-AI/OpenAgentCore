"""Five bounded SDK operations for an already authorized Core allocation, plus
read-only deployment validation and batch observation."""
from concurrent.futures import ThreadPoolExecutor
import json
import math
import time
from datetime import datetime, timezone
from uuid import UUID

from e2b import Sandbox, SandboxQuery, SandboxState
from e2b.api.client.models.sandbox_metric import SandboxMetric
from e2b.exceptions import FileNotFoundException, SandboxNotFoundException

from sdk import connection_material, definitely_rejected, read_metrics, restore, run, validate_deployment
from state import Failure, Receipt, private_root, read_receipt

PREFIX = 'parsar_'
FIELDS = ('InstallationID', 'TenantID', 'EnvironmentID', 'AllocationID')


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


def observed(reference, cloud, point):
    """Map one metrics point without changing E2B units. A malformed point makes
    only its own row unavailable.

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
        return dict(reference, Status='observed', ObservedAt=utc(metric.timestamp), StartedAt=utc(cloud.started_at),
                    CPUCount=cpu_count, CPUUsedPct=cpu_pct, MemUsed=metric.mem_used, MemTotal=metric.mem_total,
                    DiskUsed=metric.disk_used if disk_known else None, DiskTotal=metric.disk_total if disk_known else None)
    except Exception:
        return dict(reference, Status='unavailable')


class Provider:
    def __init__(self, request):
        self.q = request
        self.config = request['Config']
        self.reference = request['Reference']
        self.references = request.get('References') or []
        if (request['Version'] != 1 or request['Operation'] not in
                ('create', 'inspect', 'renew', 'kill', 'command', 'validate_deployment', 'observe') or
                (request['Operation'] not in ('validate_deployment', 'observe') and
                 not valid_reference(self.reference)) or
                (request['Operation'] == 'observe' and
                 (not 1 <= len(self.references) <= 100 or
                  not all(valid_reference(r) for r in self.references) or
                  len({tuple(sorted(r.items())) for r in self.references}) != len(self.references))) or
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

    def observe(self):
        """Latest metrics of owned running sandboxes, without locks, writes or connect.

        Receipts name each allocation's sandbox so that the one batch metrics
        request runs alongside the labelled listing that confirms it is running."""
        root = private_root(self.config)
        statuses, candidates = [], {}
        for index, reference in enumerate(self.references):
            try:
                record = read_receipt(self.config, root, reference) or {}
            except Failure as error:
                statuses.append(error.code)
                continue
            except Exception:
                statuses.append('unavailable')
                continue
            ids = record.get('ids', [])
            if record.get('status') in ('killed', 'rejected'):
                statuses.append('not_running')
            elif len(ids) == 1 and isinstance(ids[0], str):
                statuses.append('unavailable')
                candidates[index] = ids[0]
            else:
                statuses.append('unavailable')
        if not candidates:
            return [dict(r, Status=status) for r, status in zip(self.references, statuses)]
        installation = self.config['InstallationID']
        # One allocation filters by all its labels; a page lists the installation.
        metadata = {PREFIX + 'installationid': installation}
        if len(self.references) == 1:
            metadata = self.metadata_for(self.references[0])
        wanted, seen = set(candidates.values()), set()
        with ThreadPoolExecutor(max_workers=1) as pool:
            metrics = pool.submit(read_metrics, self.config, sorted(wanted), self.remaining)
            running = {}
            paginator = Sandbox.list(query=SandboxQuery(metadata=metadata, state=[SandboxState.RUNNING]),
                                     limit=100, **self.options())
            # Stop once every receipt's sandbox has been listed. Detection of a
            # second sandbox with the same allocation labels then covers only
            # the pages read; lifecycle discovery remains exhaustive.
            while paginator.has_next and not wanted <= seen:
                for cloud in paginator.next_items(**self.options()):
                    labels = cloud.metadata or {}
                    if labels.get(PREFIX + 'installationid') == installation:
                        key = tuple(labels.get(PREFIX + field.lower()) for field in FIELDS[1:])
                        running.setdefault(key, []).append(cloud)
                        seen.add(cloud.sandbox_id)
            points = metrics.result()
        result = []
        for index, reference in enumerate(self.references):
            if index not in candidates:
                result.append(dict(reference, Status=statuses[index]))
                continue
            found = running.get(tuple(reference[field] for field in FIELDS[1:]), [])
            if not found:
                result.append(dict(reference, Status='not_running'))
            elif len(found) != 1 or found[0].sandbox_id != candidates[index] or not isinstance(points.get(candidates[index]), dict):
                result.append(dict(reference, Status='unavailable'))
            else:
                result.append(observed(reference, found[0], points[candidates[index]]))
        return result

    def metadata_for(self, reference):
        return {PREFIX + field.lower(): value for field, value in
                dict(reference, InstallationID=self.config['InstallationID']).items()}

    def execute(self):
        if self.q['Operation'] in ('validate_deployment', 'observe'):
            try:
                if self.q['Operation'] == 'observe':
                    return {'Version': 1, 'Observations': self.observe(), 'ErrorCode': ''}
                build = validate_deployment(self.config, self.remaining)
                return {'Version': 1, 'DeploymentValid': True, 'TemplateBuild': build, 'ErrorCode': ''}
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
