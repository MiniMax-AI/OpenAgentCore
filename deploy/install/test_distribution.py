"""Exercise artifact transport, integrity and safe retry using real local HTTP."""
import gzip
import hashlib
import http.server
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch

import distribution


class ArtifactTests(unittest.TestCase):
    def setUp(self):
        root = Path.home() / '.parsar/tests/distribution'
        root.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=root)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.data = b'prebuilt artifact' * 1000
        self.requests = []
        self.status = 200
        test = self
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                test.requests.append(self.path)
                self.send_response(test.status)
                self.end_headers()
                if test.status == 200:
                    self.wfile.write(test.data)
            def log_message(self, *args):
                pass
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.stop)
        self.manifest = {'source_commit': 'a' * 40, 'artifact_base_url': f'http://127.0.0.1:{self.server.server_port}', 'artifacts': {}}
        self.entry('native/bin/node', self.data)

    def stop(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def entry(self, name, data):
        value = {'filename': 'parsar-core-' + 'a' * 40 + '-linux-amd64-' + name.replace('/', '-'),
                 'sha256': hashlib.sha256(data).hexdigest(), 'size': len(data)}
        self.manifest['artifacts'][name] = value
        return value

    def test_download_and_verified_warm_cache(self):
        target = self.root / 'node'
        distribution.obtain_artifact(self.manifest, 'native/bin/node', target)
        self.assertEqual(target.read_bytes(), self.data)
        self.assertEqual(target.stat().st_mode & 0o777, 0o700)
        distribution.obtain_artifact(self.manifest, 'native/bin/node', target)
        self.assertEqual(len(self.requests), 1)
        target.write_bytes(b'changed')
        with self.assertRaisesRegex(distribution.DistributionError, 'differs'):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', target)
        self.assertEqual(len(self.requests), 1)

    def test_failed_digest_does_not_install_or_retry(self):
        self.data = b'x' * len(self.data)
        with self.assertRaisesRegex(distribution.DistributionError, 'checksum'):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        self.assertEqual(len(self.requests), 1)
        self.assertEqual(list(self.root.iterdir()), [])

    def test_transient_http_retries_are_bounded(self):
        self.status = 503
        with patch.object(distribution.time, 'sleep'), self.assertRaisesRegex(distribution.DistributionError, 'HTTP 503'):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        self.assertEqual(len(self.requests), 3)
        self.assertFalse((self.root / 'node').exists())

    def test_auth_rejection_is_not_retried(self):
        self.status = 403
        with self.assertRaisesRegex(distribution.DistributionError, 'HTTP 403'):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        self.assertEqual(len(self.requests), 1)

    def test_truncated_transfer_can_be_rerun(self):
        self.data = self.data[:20]
        with patch.object(distribution.time, 'sleep'), self.assertRaisesRegex(distribution.DistributionError, 'interrupted'):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        self.assertEqual(list(self.root.iterdir()), [])
        self.data = b'prebuilt artifact' * 1000
        distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')

    def test_offline_and_runtime_expansion_are_verified(self):
        raw = b'synthetic tar contents' * 1000
        self.data = gzip.compress(raw)
        entry = self.entry('images/runtime.tar.gz', self.data)
        entry.update(unpacked_size=len(raw), unpacked_sha256=hashlib.sha256(raw).hexdigest())
        offline = self.root / 'offline'
        (offline / 'artifacts').mkdir(parents=True)
        (offline / 'artifacts' / entry['filename']).write_bytes(self.data)
        output = distribution.runtime_archive(self.manifest, self.root / 'cache', offline)
        self.assertEqual(output.read_bytes(), raw)
        self.assertEqual(self.requests, [])
        self.assertEqual(distribution.runtime_archive(self.manifest, self.root / 'cache'), output)
        output.write_bytes(b'bad cache')
        with self.assertRaisesRegex(distribution.DistributionError, 'differs'):
            distribution.runtime_archive(self.manifest, self.root / 'cache')

    def test_url_and_path_boundaries(self):
        for value in ('http://example.com/a', 'https://user:secret@example.com/a', 'file:///tmp/a'):
            with self.assertRaises(distribution.DistributionError):
                distribution.safe_url(value)
        self.manifest['artifacts']['native/bin/node']['filename'] = '../escape'
        with self.assertRaises(distribution.DistributionError):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        self.entry('native/bin/node', self.data)
        (self.root / 'link').symlink_to(self.root / 'target')
        with self.assertRaises(distribution.DistributionError):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'link')


if __name__ == '__main__':
    unittest.main()
