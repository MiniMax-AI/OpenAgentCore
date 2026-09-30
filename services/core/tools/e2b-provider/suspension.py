"""E2B's implementation of exact-incarnation suspension under the allocation lock."""
import json

from helper_contract_generated import PROTOCOL_VERSION, COMPUTE_FIELDS, SUSPEND_FIELDS, RESUME_FIELDS
from state import Failure


class Suspension:
    def __init__(self, provider, sdk, material, command):
        self.p, self.sdk, self.material, self.command = provider, sdk, material, command

    @property
    def record(self):
        record = self.p.receipt.data
        if not record or record.get('settled') is not True:
            raise Failure('unconfirmed')
        return record

    def current(self):
        current = self.record.get('compute')
        if not isinstance(current, dict) or not current.get('ID'):
            raise Failure('unconfirmed')
        return current

    def matches(self, wanted, actual, unresolved=False):
        if (not isinstance(wanted, dict) or set(wanted) != set(COMPUTE_FIELDS) or
                type(wanted['Generation']) is not int or wanted['Generation'] < 0 or
                wanted['Name'] != self.p.reference['AllocationID'] or
                any(wanted[k] != actual[k] for k in ('Generation', 'Name', 'RestoredFrom')) or
                (wanted['ID'] != actual['ID'] and not (unresolved and wanted['ID'] == ''))):
            raise Failure('ownership')

    def cloud(self, execution=True):
        found = self.p.discover()
        if len(found) != 1:
            raise Failure('unconfirmed')
        cloud = found[0]
        if cloud.sandbox_id != self.current()['ID']:
            raise Failure('ownership')
        if execution:
            self.p.qualified(cloud)
        return cloud

    def state(self, current, cloud):
        return {'Compute': current, 'Status': cloud.state,
                'BootstrapComplete': self.record.get('bootstrap_complete') is True,
                'Retained': None, 'ResourcesReleased': False, 'SuspendSettled': False}

    def retained(self, operation, current):
        return {'Reference': self.p.reference['AllocationID'], 'ID': operation, 'OperationID': operation,
                'SourceGeneration': current['Generation'], 'SourceName': current['Name'],
                'SourceID': current['ID'],
                'Data': json.dumps({'version': 1, 'sandbox_id': current['ID']}, sort_keys=True, separators=(',', ':'))}

    def request(self, field):
        q = self.p.q.get(field)
        if (not isinstance(q, dict) or set(q) != set(SUSPEND_FIELDS if field == 'Suspend' else RESUME_FIELDS) or
                q.get('Reference') != self.p.reference):
            raise Failure('invalid')
        # UUID parsing is shared with the allocation envelope validator.
        from provider import valid_id
        if not valid_id(q.get('OperationID')) or type(q.get('ReconcileOnly')) is not bool:
            raise Failure('invalid')
        return q

    def inspect(self, operation):
        current = self.current()
        self.matches(self.p.q.get('Compute'), current, unresolved=operation == 'compute_info')
        if self.record.get('status') == 'killed':
            raise Failure('not_found')
        cloud = self.cloud()
        if operation != 'compute_info':
            if cloud.state != 'running' or not self.record.get('bootstrap_complete'):
                raise Failure('unconfirmed')
            pending = self.record.get('suspension')
            if pending and pending['phase'] not in ('resumed', 'consumed'):
                raise Failure('unconfirmed')
        if operation == 'compute_renew':
            self.sdk.set_timeout(cloud.sandbox_id, self.p.config['TimeoutSeconds'], **self.p.options())
            cloud = self.cloud()
        if operation == 'compute_command':
            return {'Command': self.command(self.p.client(cloud), self.p.q['Command'], self.p.remaining)}
        return {'State': self.state(current, cloud)}

    def suspend(self):
        q = self.request('Suspend')
        current = self.current()
        self.matches(q.get('Source'), current)
        handle = self.retained(q['OperationID'], current)
        if q.get('Retained') not in (None, handle):
            raise Failure('ownership')
        entry = self.record.get('suspension')
        if entry and entry['retained'] == handle:
            if entry['phase'] not in ('pause_pending', 'paused'):
                raise Failure('ownership')
        elif q['ReconcileOnly']:
            # No durable native intent: the allocation lock proves this helper
            # never issued pause. Only a qualified running source can roll back.
            if entry and entry['phase'] != 'consumed':
                raise Failure('ownership')
            cloud = self.cloud()
            if cloud.state != 'running':
                raise Failure('unconfirmed')
            state = self.state(current, cloud)
            state['SuspendSettled'] = True
            return {'State': state}
        else:
            if entry and entry['phase'] != 'consumed':
                raise Failure('ownership')
            cloud = self.cloud()
            if cloud.state != 'running' or not self.record.get('bootstrap_complete'):
                raise Failure('unconfirmed')
            entry = {'retained': handle, 'phase': 'pause_pending'}
            self.p.receipt.save(suspension=entry)
            # An error remains pending. A later observation may prove paused;
            # a running observation never proves a timed-out pause cannot land.
            self.sdk.pause(current['ID'], keep_memory=True, **self.p.options())
        cloud = self.cloud()
        if cloud.state != 'paused':
            raise Failure('unconfirmed')
        self.p.receipt.save(suspension=dict(entry, phase='paused'))
        state = self.state(current, cloud)
        state.update(Status='suspended', Retained=handle, ResourcesReleased=True, SuspendSettled=True)
        return {'State': state}

    def resume(self):
        q = self.request('Resume')
        entry = self.record.get('suspension')
        if not entry or entry['retained'] != q.get('Retained'):
            raise Failure('ownership')
        retained = entry['retained']
        target = {'Generation': retained['SourceGeneration'] + 1, 'Name': retained['SourceName'],
                  'ID': retained['SourceID'], 'RestoredFrom': retained}
        self.matches(q.get('Target'), target)
        if entry['phase'] in ('resume_pending', 'resumed', 'consumed'):
            if entry.get('resume_id') != q['OperationID'] or entry.get('target') != target:
                raise Failure('ownership')
        elif entry['phase'] == 'paused' and not q['ReconcileOnly']:
            cloud = self.cloud()
            if cloud.state != 'paused':
                raise Failure('unconfirmed')
            entry = dict(entry, phase='resume_pending', resume_id=q['OperationID'], target=target, connected=False)
            self.p.receipt.save(suspension=entry)
            connected = self.sdk.connect(target['ID'], timeout=self.p.config['TimeoutSeconds'],
                                         on_resume='restore', **self.p.options())
            if connected.sandbox_id != target['ID']:
                raise Failure('ownership')
            self.p.check_domain(connected)
            entry = dict(entry, connected=True)
            self.p.receipt.save(suspension=entry, connection=self.material(connected))
        else:
            raise Failure('unconfirmed')
        # Never repeat connect after an uncertain reply. Fresh connection material
        # must have been durably received before execution can resume.
        if not entry.get('connected'):
            raise Failure('unconfirmed')
        cloud = self.cloud()
        if cloud.state != 'running' or not self.record.get('bootstrap_complete'):
            raise Failure('unconfirmed')
        if entry['phase'] != 'consumed':
            self.p.receipt.save(compute=target, suspension=dict(entry, phase='resumed'))
        return {'State': self.state(target, cloud)}

    def kill(self):
        wanted = self.p.q.get('Compute')
        current = self.current()
        entry = self.record.get('suspension')
        target = (entry or {}).get('target')
        # Target-first cleanup may arrive before Resume was ever dispatched.
        planned = None
        if entry:
            r = entry['retained']
            planned = {'Generation': r['SourceGeneration'] + 1, 'Name': r['SourceName'],
                       'ID': r['SourceID'], 'RestoredFrom': r}
        if wanted == planned and target is None and entry['phase'] == 'paused':
            return {}
        if wanted != current and wanted != target:
            # Once destruction is confirmed, stale source cleanup is harmless.
            if self.record.get('status') == 'killed' and isinstance(wanted, dict):
                if wanted['ID'] == current['ID'] and wanted['Name'] == current['Name'] and wanted['Generation'] < current['Generation']:
                    return {}
            raise Failure('ownership')
        if entry and entry['phase'] == 'resume_pending' and entry.get('connected'):
            # The SDK reply already settled the original connect. Cleanup owns
            # recovery once Core leaves restoring, so reconcile that saved target
            # here without another connect or a resource-qualification gate.
            if wanted != target:
                raise Failure('ownership')
            found = self.p.discover()
            if len(found) > 1 or any(cloud.sandbox_id != target['ID'] for cloud in found):
                raise Failure('ownership')
            entry = dict(entry, phase='resumed')
            self.p.receipt.save(compute=target, suspension=entry)
        if entry and entry['phase'] in ('pause_pending', 'resume_pending'):
            # No successful native reply or settled pause has been observed.
            raise Failure('unconfirmed')
        self.p.kill()
        return {}

    def delete(self):
        wanted = self.p.q.get('Retained')
        entry = self.record.get('suspension')
        if not entry or wanted != entry['retained']:
            raise Failure('ownership')
        if entry['phase'] == 'consumed':
            return {}
        if self.record.get('status') != 'killed':
            if entry['phase'] != 'resumed':
                raise Failure('unconfirmed')
            self.matches(entry['target'], self.current())
            if self.cloud().state != 'running':
                raise Failure('unconfirmed')
        # E2B consumed the pause image on restore. Keep its minimal generation
        # receipt to reject delayed resume/cleanup; never kill running compute.
        self.p.receipt.save(suspension=dict(entry, phase='consumed'))
        return {}

    def execute(self):
        operation = self.p.q['Operation']
        if operation in ('compute_info', 'compute_renew', 'compute_command', 'resume_compute'):
            result = self.inspect(operation)
        else:
            result = {'suspend': self.suspend, 'resume': self.resume,
                      'compute_kill': self.kill, 'delete_retained': self.delete}[operation]()
        return dict(result, Version=PROTOCOL_VERSION, ErrorCode='')
