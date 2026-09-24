"""Private receipt durability and lifecycle exclusion."""
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import tempfile
import threading
import time
import unittest
from uuid import uuid4
from unittest.mock import patch

from provider import Provider
from state import Failure, Receipt


class StateTest(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.TemporaryDirectory()
        self.addCleanup(self.root.cleanup)
        self.request = {'Version': 1, 'Operation': 'kill',
                        'Config': {'StateDir': self.root.name, 'InstallationID': str(uuid4()), 'APIKey': 'test'},
                        'Reference': {key: str(uuid4()) for key in ['TenantID', 'EnvironmentID', 'AllocationID']},
                        'Deadline': (datetime.now(timezone.utc) + timedelta(seconds=2)).isoformat()}

    def test_existing_helper_holds_lock_beyond_callers_timeout(self):
        finished = []
        with Receipt(self.request, lambda: 1) as first:
            first.save(status='create_pending')
            request = dict(self.request, Deadline=(datetime.now(timezone.utc) + timedelta(milliseconds=50)).isoformat())
            def call():
                try:
                    Provider(request).execute()
                except Failure as error:
                    finished.append(error.code)
            with patch('provider.Sandbox') as sdk:
                worker = threading.Thread(target=call)
                worker.start()
                worker.join(1)
                self.assertFalse(worker.is_alive())
                self.assertEqual(finished, ['unconfirmed'])
                sdk.kill.assert_not_called()
        with Receipt(self.request, lambda: 1) as recovered:
            self.assertEqual(recovered.data['status'], 'create_pending')
            self.assertFalse(recovered.data['settled'])

    def test_receipt_symlink_is_never_followed(self):
        with Receipt(self.request, lambda: 1) as receipt:
            path = receipt.path
        witness = Path(self.root.name, 'witness')
        witness.write_text('unchanged')
        path.symlink_to(witness)
        with self.assertRaises(OSError):
            with Receipt(self.request, lambda: 1):
                pass
        self.assertEqual(witness.read_text(), 'unchanged')

    def test_foreign_identity_and_world_readable_receipt_rejected(self):
        with Receipt(self.request, lambda: 1) as receipt:
            receipt.save(status='create_pending')
            path = receipt.path
        data = json.loads(path.read_text())
        data['identity']['TenantID'] = str(uuid4())
        path.write_text(json.dumps(data))
        with self.assertRaises(Failure):
            with Receipt(self.request, lambda: 1):
                pass
        path.chmod(0o644)
        with self.assertRaises(Failure):
            with Receipt(self.request, lambda: 1):
                pass


if __name__ == '__main__':
    unittest.main()
