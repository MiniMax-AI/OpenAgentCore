"""Native lifecycle effects and durable recovery are tested through the helper."""
import unittest
from uuid import uuid4

from provider import Provider
import provider_test


class SuspensionTest(unittest.TestCase):
    setUp = provider_test.ProviderTest.setUp
    call = provider_test.ProviderTest.call
    record = provider_test.ProviderTest.record

    def prepare(self):
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.api.pause.side_effect = lambda *a, **k: setattr(self.cloud, 'state', 'paused')
        def connect(*args, **kwargs):
            self.cloud.state = 'running'
            return self.cloud
        self.api.connect.side_effect = connect
        return self.record()['compute']

    def invoke(self, operation, **payload):
        return Provider(dict(self.request, Operation=operation, **payload)).execute()

    def pause(self, source):
        q = {'Reference': self.reference, 'OperationID': str(uuid4()), 'Source': source,
             'Retained': None, 'ReconcileOnly': False}
        response = self.invoke('suspend', Suspend=q)
        return q, response

    def resume_request(self, retained):
        target = {'Generation': retained['SourceGeneration'] + 1, 'Name': retained['SourceName'],
                  'ID': retained['SourceID'], 'RestoredFrom': retained}
        return {'Reference': self.reference, 'OperationID': str(uuid4()), 'Target': target,
                'Retained': retained, 'ReconcileOnly': False}

    def test_resume_rejects_external_workspace_before_sdk(self):
        from state import Failure
        with self.assertRaises(Failure) as raised:
            Provider(dict(self.request, Operation='resume', Resume={'Workspace': {}}))
        self.assertEqual(raised.exception.code, 'invalid')
        self.api.connect.assert_not_called()

    def test_resume_accepts_null_workspace(self):
        current = self.prepare()
        _, paused = self.pause(current)
        request = dict(self.resume_request(paused['State']['Retained']), Workspace=None)
        self.assertEqual(self.invoke('resume', Resume=request)['ErrorCode'], '')

    def test_two_cycles_keep_native_id_and_fence_old_generation(self):
        current = self.prepare()
        for generation in range(2):
            old = current
            q, paused = self.pause(current)
            self.assertEqual(paused['ErrorCode'], '')
            self.assertTrue(paused['State']['ResourcesReleased'])
            retained = paused['State']['Retained']
            request = self.resume_request(retained)
            result = self.invoke('resume', Resume=request)
            self.assertEqual(result['ErrorCode'], '')
            current = result['State']['Compute']
            self.assertEqual(current['ID'], old['ID'])
            self.assertEqual(current['Generation'], generation + 1)
            self.assertEqual(self.invoke('compute_kill', Compute=old)['ErrorCode'], 'ownership')
            self.api.kill.assert_not_called()
            # Core wakes the parked daemon before deleting the consumed receipt.
            self.assertEqual(self.invoke('compute_command', Compute=current,
                                        Command={'Args': ['oac-daemon', 'resume']})['ErrorCode'], '')
            self.assertEqual(self.invoke('delete_retained', Retained=retained)['ErrorCode'], '')
            self.assertEqual(self.invoke('delete_retained', Retained=retained)['ErrorCode'], '')
            self.assertEqual(self.invoke('compute_renew', Compute=current)['ErrorCode'], '')
        self.assertEqual(self.api.pause.call_count, 2)
        self.assertEqual(self.api.connect.call_count, 2)
        self.assertEqual(self.api.connect.call_args.kwargs['on_resume'], 'restore')
        self.assertEqual(self.api.connect.call_args.kwargs['timeout'], self.config['TimeoutSeconds'])
        self.assertTrue(self.api.pause.call_args.kwargs['keep_memory'])

    def test_lost_pause_reply_observes_without_replay(self):
        current = self.prepare()
        def lost(*args, **kwargs):
            self.cloud.state = 'paused'
            raise TimeoutError()
        self.api.pause.side_effect = lost
        q, result = self.pause(current)
        self.assertEqual(result['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.invoke('suspend', Suspend=dict(q, ReconcileOnly=True))['ErrorCode'], '')
        self.api.pause.assert_called_once()

    def test_pending_pause_running_cannot_roll_back_or_delete(self):
        current = self.prepare()
        self.api.pause.side_effect = TimeoutError()
        q, result = self.pause(current)
        self.assertEqual(result['ErrorCode'], 'unconfirmed')
        for _ in range(2):
            self.assertEqual(self.invoke('suspend', Suspend=dict(q, ReconcileOnly=True))['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.invoke('compute_kill', Compute=current)['ErrorCode'], 'unconfirmed')
        self.api.pause.assert_called_once()
        self.api.kill.assert_not_called()

    def test_missing_native_pause_intent_can_settle_running_rollback(self):
        current = self.prepare()
        q = {'Reference': self.reference, 'OperationID': str(uuid4()), 'Source': current,
             'Retained': None, 'ReconcileOnly': True}
        state = self.invoke('suspend', Suspend=q)['State']
        self.assertTrue(state['SuspendSettled'])
        self.assertFalse(state['ResourcesReleased'])
        self.assertIsNone(state['Retained'])
        self.api.pause.assert_not_called()

    def test_lost_resume_reply_never_replays_connect(self):
        current = self.prepare()
        _, paused = self.pause(current)
        q = self.resume_request(paused['State']['Retained'])
        def lost(*a, **k):
            self.cloud.state = 'running'
            raise TimeoutError()
        self.api.connect.side_effect = lost
        self.assertEqual(self.invoke('resume', Resume=q)['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.invoke('resume', Resume=dict(q, ReconcileOnly=True))['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.invoke('compute_kill', Compute=q['Target'])['ErrorCode'], 'unconfirmed')
        self.api.connect.assert_called_once()
        self.api.kill.assert_not_called()

    def test_lost_core_resume_reply_adopts_saved_target_without_connect(self):
        current = self.prepare()
        _, paused = self.pause(current)
        q = self.resume_request(paused['State']['Retained'])
        first = self.invoke('resume', Resume=q)
        self.assertEqual(first['ErrorCode'], '')
        second = self.invoke('resume', Resume=dict(q, ReconcileOnly=True))
        self.assertEqual(first, second)
        self.api.connect.assert_called_once()

    def test_foreign_retained_and_incarnation_cannot_mutate(self):
        current = self.prepare()
        _, paused = self.pause(current)
        retained = paused['State']['Retained']
        q = self.resume_request(dict(retained, SourceID='foreign-id'))
        self.assertEqual(self.invoke('resume', Resume=q)['ErrorCode'], 'ownership')
        self.assertEqual(self.invoke('delete_retained', Retained=dict(retained, ID='foreign'))['ErrorCode'], 'ownership')
        self.api.connect.assert_not_called()
        self.api.kill.assert_not_called()

    def test_archive_recovers_saved_connect_reply_before_cleanup(self):
        from e2b.exceptions import SandboxNotFoundException
        current = self.prepare()
        _, paused = self.pause(current)
        retained = paused['State']['Retained']
        q = self.resume_request(retained)
        def connected_then_observation_lost(*args, **kwargs):
            self.cloud.state = 'running'
            self.api.get_info.side_effect = TimeoutError()
            return self.cloud
        self.api.connect.side_effect = connected_then_observation_lost
        self.assertEqual(self.invoke('resume', Resume=q)['ErrorCode'], 'unconfirmed')
        self.assertTrue(self.record()['suspension']['connected'])
        self.assertEqual(self.record()['suspension']['phase'], 'resume_pending')
        self.api.get_info.side_effect = None
        # The old incarnation remains fenced until exact target cleanup settles.
        self.assertEqual(self.invoke('compute_kill', Compute=current)['ErrorCode'], 'ownership')
        def killed(*args, **kwargs):
            self.api.get_info.side_effect = SandboxNotFoundException('gone')
        self.api.kill.side_effect = killed
        self.assertEqual(self.invoke('compute_kill', Compute=q['Target'])['ErrorCode'], '')
        self.assertEqual(self.invoke('compute_kill', Compute=current)['ErrorCode'], '')
        self.assertEqual(self.invoke('delete_retained', Retained=retained)['ErrorCode'], '')
        self.api.connect.assert_called_once()
        self.api.kill.assert_called_once()
        self.assertEqual(self.record()['status'], 'killed')
    def test_paused_delete_reclaims_same_native_id_and_settles_lost_reply(self):
        from e2b.exceptions import SandboxNotFoundException
        source = self.prepare()
        _, paused = self.pause(source)
        retained = paused['State']['Retained']
        def killed_then_lost(*args, **kwargs):
            self.api.get_info.side_effect = SandboxNotFoundException('gone')
            raise TimeoutError()
        self.api.kill.side_effect = killed_then_lost
        self.assertEqual(self.invoke('delete_retained', Retained=retained)['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.record()['suspension']['phase'], 'paused')
        self.assertEqual(self.invoke('delete_retained', Retained=retained)['ErrorCode'], '')
        self.assertEqual(self.record()['status'], 'killed')
        self.assertEqual(self.record()['suspension']['phase'], 'consumed')
        self.api.kill.assert_called_once()
        self.assertEqual(self.api.kill.call_args.args[0], source['ID'])

    def test_paused_delete_rejects_running_source(self):
        source = self.prepare()
        _, paused = self.pause(source)
        self.cloud.state = 'running'
        self.assertEqual(self.invoke('delete_retained', Retained=paused['State']['Retained'])['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.record()['suspension']['phase'], 'paused')
        self.api.kill.assert_not_called()

    def test_never_dispatched_restore_closes_durably_and_rejects_late_request(self):
        source = self.prepare()
        _, paused = self.pause(source)
        q = self.resume_request(paused['State']['Retained'])
        observed = self.invoke('resume', Resume=dict(q, ReconcileOnly=True))
        self.assertEqual(observed['ErrorCode'], '')
        self.assertEqual(observed['State']['RestoreAttemptClosed'], q['OperationID'])
        self.assertEqual(observed['State']['Compute'], q['Target'])
        self.api.connect.assert_not_called()
        self.assertEqual(self.invoke('resume', Resume=q)['ErrorCode'], 'ownership')
        self.assertEqual(self.invoke('resume', Resume=dict(q, ReconcileOnly=True)), observed)
        fresh = dict(q, OperationID=str(uuid4()))
        self.assertEqual(self.invoke('resume', Resume=fresh)['ErrorCode'], '')
        self.assertEqual(self.invoke('resume', Resume=dict(q, ReconcileOnly=True))['ErrorCode'], 'ownership')
        self.api.connect.assert_called_once()

    def test_closed_restore_history_is_bounded_without_eviction(self):
        source = self.prepare()
        _, paused = self.pause(source)
        requests = []
        for _ in range(64):
            q = self.resume_request(paused['State']['Retained'])
            requests.append(q)
            self.assertEqual(self.invoke('resume', Resume=dict(q, ReconcileOnly=True))['ErrorCode'], '')
        extra = self.resume_request(paused['State']['Retained'])
        self.assertEqual(self.invoke('resume', Resume=dict(extra, ReconcileOnly=True))['ErrorCode'], 'unconfirmed')
        self.assertEqual(len(self.record()['suspension']['closed_resume_ids']), 64)
        self.assertEqual(self.invoke('resume', Resume=requests[0])['ErrorCode'], 'ownership')
        self.api.connect.assert_not_called()
        # A fresh, authorized attempt can still proceed; closing unknown attempts
        # never consumes the original native retained image.
        self.assertEqual(self.invoke('resume', Resume=extra)['ErrorCode'], '')
        self.assertEqual(self.invoke('delete_retained', Retained=paused['State']['Retained'])['ErrorCode'], '')
        current = self.record()['compute']
        _, next_pause = self.pause(current)
        self.assertNotIn('closed_resume_ids', self.record()['suspension'])
        self.assertEqual(self.invoke('resume', Resume=requests[0])['ErrorCode'], 'ownership')

    def test_paused_delete_settles_after_killed_receipt_write_fails(self):
        from e2b.exceptions import SandboxNotFoundException
        from state import Receipt
        from unittest.mock import patch
        source = self.prepare()
        _, paused = self.pause(source)
        retained = paused['State']['Retained']
        def killed(*args, **kwargs):
            self.api.get_info.side_effect = SandboxNotFoundException('gone')
        self.api.kill.side_effect = killed
        save = Receipt.save
        def fail_killed(receipt, **values):
            if values.get('status') == 'killed':
                raise OSError('fixture durable write failed')
            return save(receipt, **values)
        with patch.object(Receipt, 'save', fail_killed):
            self.assertEqual(self.invoke('delete_retained', Retained=retained)['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.invoke('delete_retained', Retained=retained)['ErrorCode'], '')
        self.api.kill.assert_called_once()
        self.assertEqual(self.record()['suspension']['phase'], 'consumed')

    def test_rollback_closes_late_suspend_across_helper_reopen(self):
        source = self.prepare()
        q = {'Reference': self.reference, 'OperationID': str(uuid4()), 'Source': source,
             'Retained': None, 'ReconcileOnly': True}
        result = self.invoke('suspend', Suspend=q)
        self.assertTrue(result['State']['SuspendSettled'])
        self.assertIsNone(result['State']['Retained'])
        self.assertEqual(self.invoke('suspend', Suspend=dict(q, ReconcileOnly=False))['ErrorCode'], 'ownership')
        self.assertEqual(self.cloud.state, 'running')
        self.api.pause.assert_not_called()
        self.assertEqual(self.invoke('suspend', Suspend=q), result)

    def test_suspend_fences_bound_and_survive_same_generation_rollback(self):
        source = self.prepare()
        requests = []
        for _ in range(64):
            q = {'Reference': self.reference, 'OperationID': str(uuid4()), 'Source': source,
                 'Retained': None, 'ReconcileOnly': True}
            requests.append(q)
            self.assertTrue(self.invoke('suspend', Suspend=q)['State']['SuspendSettled'])
        extra = dict(q, OperationID=str(uuid4()))
        self.assertEqual(self.invoke('suspend', Suspend=extra)['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.invoke('suspend', Suspend=dict(requests[0], ReconcileOnly=False))['ErrorCode'], 'ownership')
        self.assertEqual(len(self.record()['suspend_fence']['closed']), 64)
        # Only a real resume advances the incarnation; the next observation then
        # replaces the journal while stale source requests remain fenced.
        _, paused = self.pause(source)
        resume = self.resume_request(paused['State']['Retained'])
        current = self.invoke('resume', Resume=resume)['State']['Compute']
        self.assertEqual(self.invoke('delete_retained', Retained=paused['State']['Retained'])['ErrorCode'], '')
        fresh = dict(extra, Source=current, OperationID=str(uuid4()))
        self.assertTrue(self.invoke('suspend', Suspend=fresh)['State']['SuspendSettled'])
        self.assertEqual(len(self.record()['suspend_fence']['closed']), 1)
        self.assertEqual(self.invoke('suspend', Suspend=dict(requests[0], ReconcileOnly=False))['ErrorCode'], 'ownership')
