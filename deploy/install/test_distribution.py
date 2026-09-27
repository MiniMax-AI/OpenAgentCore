"""Exercise artifact transport, integrity and safe retry using real local HTTP."""
import gzip
import hashlib
import http.server
import io
import json
from pathlib import Path
import tempfile
import threading
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import distribution


class ArtifactTests(unittest.TestCase):
    def setUp(self):
        root = Path.home() / '.oac/tests/distribution'
        root.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=root)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.data = b'prebuilt artifact' * 1000
        self.requests = []
        self.ranges = []
        self.if_ranges = []
        self.status = 200
        self.resumable = False
        test = self
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                test.requests.append(self.path)
                test.ranges.append(self.headers.get('Range'))
                test.if_ranges.append(self.headers.get('If-Range'))
                if test.resumable:
                    # The first response breaks off halfway; a Range request gets the rest.
                    start = int(self.headers['Range'][6:-1]) if self.headers.get('Range') else 0
                    self.send_response(206 if start else 200)
                    self.send_header('Last-Modified', 'Sat, 26 Sep 2026 00:00:00 GMT')
                    if start:
                        self.send_header('Content-Range', f'bytes {start}-{len(test.data) - 1}/{len(test.data)}')
                    self.send_header('Content-Length', str(len(test.data) - start))
                    self.end_headers()
                    self.wfile.write(test.data[start:] if start else test.data[:len(test.data) // 2])
                    return
                self.send_response(test.status)
                if test.status == 302:
                    self.send_header('Location', 'https://elsewhere.example' + self.path)
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
        value = {'filename': 'oac-' + 'a' * 40 + '-linux-amd64-' + name.replace('/', '-'),
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
        # Only the private partial file stays, for the rerun to continue.
        self.assertEqual([path.name for path in self.root.iterdir()], ['.node.partial'])
        self.data = b'prebuilt artifact' * 1000
        distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        self.assertEqual([path.name for path in self.root.iterdir()], ['node'])

    def test_interrupted_download_resumes_with_the_missing_bytes(self):
        self.resumable = True
        with patch.object(distribution.time, 'sleep'):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        self.assertEqual((self.root / 'node').read_bytes(), self.data)
        self.assertEqual(self.ranges, [None, f'bytes={len(self.data) // 2}-'])
        # If-Range: a file that changed since the partial began comes back whole instead of mixed.
        self.assertEqual(self.if_ranges, [None, 'Sat, 26 Sep 2026 00:00:00 GMT'])
        self.assertEqual([path.name for path in self.root.iterdir()], ['node'])

    def test_a_stalled_download_stops_and_keeps_its_part(self):
        with patch.object(distribution, 'SLOW_SECONDS', 0), \
                self.assertRaisesRegex(distribution.DistributionError, 'stalled.*rerun the command to resume'):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        self.assertEqual(len(self.requests), 1)  # Not retried as a network error.
        self.assertEqual([path.name for path in self.root.iterdir()], ['.node.partial'])

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


    def test_redirects_are_refused(self):
        # The console never redirects; following one could fetch verified names from another origin.
        self.status = 302
        with self.assertRaisesRegex(distribution.DistributionError, 'do not follow redirects'):
            distribution.obtain_artifact(self.manifest, 'native/bin/node', self.root / 'node')
        with self.assertRaisesRegex(distribution.DistributionError, 'do not follow redirects'):
            distribution.load_manifest(source_url=f'http://127.0.0.1:{self.server.server_port}')
        self.assertEqual(len(self.requests), 2)
        self.assertEqual(list(self.root.iterdir()), [])


class DockerIdentityTests(unittest.TestCase):
    def setUp(self):
        self.config = 'sha256:' + 'a' * 64
        self.oci = 'sha256:' + 'b' * 64
        self.manifest = {'images': {'runtime': self.config},
                         'image_manifest_digests': {'runtime': self.oci}}
        self.archive = Mock(return_value=Path('/verified/runtime.tar'))

    def result(self, identity=None, platform='linux/amd64', code=0):
        return SimpleNamespace(returncode=code, stdout=(identity + ' ' + platform) if identity else '')

    def ensure(self):
        return distribution.ensure_docker_image(self.manifest, 'runtime', self.archive)

    def test_both_stores_use_proven_immutable_cache_without_archive(self):
        for responses, identity in (([self.result(self.config)], self.config),
                                    ([self.result(code=1), self.result(self.oci)], self.oci),
                                    ([self.result(self.oci)], self.oci)):
            with self.subTest(identity=identity), patch.object(distribution, 'docker_command', side_effect=responses) as command:
                self.assertEqual(self.ensure(), identity)
                self.assertTrue(all('load' not in call.args[0] for call in command.call_args_list))
        self.archive.assert_not_called()

    def test_load_is_verified_by_either_digest_on_both_stores(self):
        for identity in (self.config, self.oci):
            replies = [self.result(code=1), self.result(code=1), self.result()]
            if identity == self.oci:
                replies.append(self.result(code=1))
            replies.append(self.result(identity))
            with self.subTest(identity=identity), patch.object(distribution, 'docker_command', side_effect=replies) as command:
                self.assertEqual(self.ensure(), identity)
                self.assertEqual(command.call_args_list[2].args[0], ['docker', 'load', '--input', '/verified/runtime.tar'])
        self.assertEqual(self.archive.call_count, 2)

    def test_wrong_id_platform_or_malformed_inspection_is_never_trusted(self):
        invalid = [self.result('sha256:' + 'c' * 64), self.result(self.oci, 'linux/arm64'),
                   self.result(self.config, 'windows/amd64'), self.result()]
        for reply in invalid:
            for loaded in (False, True):
                responses = ([self.result(code=1), self.result(code=1), self.result()] if loaded else []) + [reply]
                with self.subTest(reply=reply, loaded=loaded), patch.object(distribution, 'docker_command', side_effect=responses), \
                        self.assertRaisesRegex(distribution.DistributionError, 'identity or platform'):
                    self.ensure()

    def test_successful_load_without_inspectable_identity_fails(self):
        with patch.object(distribution, 'docker_command', side_effect=[self.result(code=1), self.result(code=1),
                          self.result(), self.result(code=1), self.result(code=1)]), \
                self.assertRaisesRegex(distribution.DistributionError, 'Cannot verify'):
            self.ensure()

    def test_both_metadata_identities_are_required_before_docker_or_archive(self):
        for field in ('images', 'image_manifest_digests'):
            original = self.manifest[field]
            for invalid in (None, {}, {'runtime': 'mutable:tag'}, {'runtime': 'sha256:' + 'g' * 64}):
                self.manifest[field] = invalid
                with self.subTest(field=field, invalid=invalid), patch.object(distribution, 'docker_command') as command, \
                        self.assertRaisesRegex(distribution.DistributionError, 'immutable image identity'):
                    self.ensure()
                command.assert_not_called()
                self.archive.assert_not_called()
            self.manifest[field] = original


class ManifestSourceTests(unittest.TestCase):
    """A release URL recorded by the build is never an artifact source."""

    def test_console_is_the_only_artifact_source(self):
        manifest = json.dumps({'source_commit': 'a' * 40, 'platform': 'linux/amd64',
                               'artifact_base_url': 'https://github.com/example/releases/download/tag'}).encode()
        sums = (hashlib.sha256(manifest).hexdigest() + '  manifest.json\n').encode()
        root = Path.home() / '.oac/tests/distribution'
        root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=root) as bundle:
            (Path(bundle) / 'manifest.json').write_bytes(manifest)
            (Path(bundle) / 'SHA256SUMS').write_bytes(sums)
            self.assertEqual(distribution.load_manifest(offline_root=bundle)['artifact_base_url'], '')
        files = {'SHA256SUMS': sums, 'manifest.json': manifest}
        opener = Mock(open=lambda url, timeout: io.BytesIO(files[url.rsplit('/', 1)[1]]))
        with patch.object(distribution.urllib.request, 'build_opener', return_value=opener):
            loaded = distribution.load_manifest(source_url='https://console.example')
        self.assertEqual(loaded['artifact_base_url'], 'https://console.example/node-install/artifacts')


if __name__ == '__main__':
    unittest.main()
