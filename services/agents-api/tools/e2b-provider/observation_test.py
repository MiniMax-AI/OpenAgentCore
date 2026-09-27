"""Read-only batch observation; live metrics qualification remains separate."""
from datetime import datetime, timezone
from types import SimpleNamespace
from unittest.mock import patch
from uuid import uuid4

from provider import Provider
from provider_test import ProviderTest
from state import Failure


class ObservationTest(ProviderTest):
    def observe(self, references):
        request = dict(self.request, Operation='observe', References=references,
                       Reference={key: '' for key in self.reference})
        try:
            return Provider(request).execute()
        except Failure as error:
            return {'Version': 1, 'ErrorCode': error.code}

    def listing(self, *pages):
        pages = [list(page) for page in pages] or [[]]
        paginator = SimpleNamespace(has_next=True, reads=0)

        def next_items(**options):
            paginator.reads += 1
            paginator.has_next = paginator.reads < len(pages)
            return pages[paginator.reads - 1]
        paginator.next_items = next_items
        self.api.list.return_value = paginator
        return paginator

    def test_one_metrics_request_maps_owned_running_sandboxes(self):
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.cloud.started_at = datetime(2026, 9, 25, 10, 31, 34, tzinfo=timezone.utc)
        other = SimpleNamespace(sandbox_id='other-id', metadata={'oac_installationid': self.config['InstallationID']})
        paginator = self.listing([other, self.cloud], [other])
        stopped = {key: str(uuid4()) for key in self.reference}
        point = {'cpuCount': 2, 'cpuUsedPct': 19.55, 'memUsed': 183836672, 'memTotal': 2079141888,
                 'memCache': 1, 'diskUsed': 1593188352, 'diskTotal': 23511863296,
                 'timestamp': '2026-09-25T10:31:36.667115804Z', 'timestampUnix': 1790332296}
        with patch('provider.read_metrics', return_value={'owned-id': point}) as metrics, \
                patch('provider.Receipt') as receipt:
            result = self.observe([self.reference, stopped])
        self.assertEqual(result['ErrorCode'], '')
        metrics.assert_called_once()
        self.assertEqual(metrics.call_args.args[1], ['owned-id'])
        receipt.assert_not_called()
        self.api.connect.assert_not_called()
        self.api.set_timeout.assert_not_called()
        self.assertEqual(self.api.list.call_args.kwargs['query'].metadata,
                         {'oac_installationid': self.config['InstallationID']})
        owned, missing = result['Observations']
        self.assertEqual(owned, dict(self.reference, Status='observed',
                                     ObservedAt='2026-09-25T10:31:36.667115+00:00',
                                     StartedAt='2026-09-25T10:31:34+00:00', CPUCount=2, CPUUsedPct=19.55,
                                     MemUsed=183836672, MemTotal=2079141888,
                                     DiskUsed=1593188352, DiskTotal=23511863296))
        self.assertEqual(missing, dict(stopped, Status='unavailable'))
        self.assertEqual(paginator.reads, 1, 'listing continued after every receipt sandbox was found')

        del point['diskUsed']
        point['memTotal'] = -1
        with patch('provider.read_metrics', return_value={'owned-id': point}):
            self.listing([self.cloud])
            self.assertEqual(self.observe([self.reference])['Observations'], [dict(self.reference, Status='unavailable')])
        point['memTotal'] = 2079141888
        with patch('provider.read_metrics', return_value={'owned-id': point}):
            self.listing([self.cloud])
            row = self.observe([self.reference])['Observations'][0]
        self.assertEqual((row['Status'], row['DiskUsed'], row['DiskTotal']), ('observed', None, None))

    def test_absent_listing_is_not_running_and_rejects_oversized_batches(self):
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.listing([])
        with patch('provider.read_metrics', return_value={}):
            result = self.observe([self.reference])
        self.assertEqual(result['Observations'], [dict(self.reference, Status='not_running')])
        references = [{key: str(uuid4()) for key in self.reference} for _ in range(101)]
        self.assertEqual(self.observe(references)['ErrorCode'], 'invalid')
