"""Read-only observation; live metrics qualification remains separate."""
from datetime import datetime, timezone
from types import SimpleNamespace
from unittest.mock import patch
from uuid import uuid4

from provider import Provider
from provider_test import ProviderTest
from state import Failure


class ObservationTest(ProviderTest):
    def observe(self, reference=None):
        request = dict(self.request, Operation='observe', Reference=reference or self.reference)
        try:
            return Provider(request).execute()
        except Failure as error:
            return {'Version': 2, 'ErrorCode': error.code}

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

    def test_one_metrics_request_maps_the_owned_running_sandbox(self):
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.cloud.started_at = datetime(2026, 9, 25, 10, 31, 34, tzinfo=timezone.utc)
        other = SimpleNamespace(sandbox_id='other-id', metadata={'oac_installationid': self.config['InstallationID']})
        paginator = self.listing([other, self.cloud], [other])
        point = {'cpuCount': 2, 'cpuUsedPct': 19.55, 'memUsed': 183836672, 'memTotal': 2079141888,
                 'memCache': 1, 'diskUsed': 1593188352, 'diskTotal': 23511863296,
                 'timestamp': '2026-09-25T10:31:36.667115804Z', 'timestampUnix': 1790332296}
        with patch('provider.read_metrics', return_value=point) as metrics, \
                patch('provider.Receipt') as receipt:
            result = self.observe()
        self.assertEqual(result['ErrorCode'], '')
        metrics.assert_called_once()
        self.assertEqual(metrics.call_args.args[1], 'owned-id')
        receipt.assert_not_called()
        self.api.connect.assert_not_called()
        self.api.set_timeout.assert_not_called()
        self.assertEqual(self.api.list.call_args.kwargs['query'].metadata, self.cloud.metadata)
        self.assertEqual(result['Observation'], dict(Status='observed',
                                     ObservedAt='2026-09-25T10:31:36.667115+00:00',
                                     StartedAt='2026-09-25T10:31:34+00:00', CPUCount=2, CPUUsedPct=19.55,
                                     MemUsed=183836672, MemTotal=2079141888,
                                     DiskUsed=1593188352, DiskTotal=23511863296))
        self.assertEqual(paginator.reads, 1, 'listing continued after the receipt sandbox was found')
        stopped = {key: str(uuid4()) for key in self.reference}
        self.assertEqual(self.observe(stopped)['Observation'], {'Status': 'unavailable'})

        del point['diskUsed']
        point['memTotal'] = -1
        with patch('provider.read_metrics', return_value=point):
            self.listing([self.cloud])
            self.assertEqual(self.observe()['Observation'], {'Status': 'unavailable'})
        point['memTotal'] = 2079141888
        with patch('provider.read_metrics', return_value=point):
            self.listing([self.cloud])
            row = self.observe()['Observation']
        self.assertEqual((row['Status'], row['DiskUsed'], row['DiskTotal']), ('observed', None, None))

    def test_absent_listing_is_not_running(self):
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.listing([])
        with patch('provider.read_metrics', return_value=None):
            result = self.observe()
        self.assertEqual(result['Observation'], {'Status': 'not_running'})
