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
