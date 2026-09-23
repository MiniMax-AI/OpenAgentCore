"""Core confirmation is independent of Docker launch and never recreates history."""
import contextlib
import io
import http.server
import json
import signal
import threading
import time
import unittest
from unittest.mock import patch
import urllib.error
import urllib.request

import self_hosted_install as installer


class ConnectionTests(unittest.TestCase):
    environment = '55b5311c-4df9-43ce-b875-faf901e6d10f'
    remote = 'wss://core.example:8443/api/v1/agent-daemon/ws'
    key = {'executor_token': 'synthetic-private-token'}
    container = 'parsar-selfhost-' + 'c' * 32

    def response(self, status='connected', environment=None):
        return io.BytesIO(json.dumps({'status': status, 'environment_id': environment or self.environment}).encode())

    def wait(self):
        installer.wait_connected(self.remote, self.environment, self.key, self.container)

    def test_waits_for_core_and_recovers_transient_failure(self):
        failure = urllib.error.HTTPError('url', 503, 'private body', {}, None)
        with patch.object(installer, 'open_connection', side_effect=[failure, self.response('disconnected'), self.response()]) as request, \
                patch.object(installer.time, 'sleep') as sleep, contextlib.redirect_stdout(io.StringIO()) as output:
            self.wait()
        self.assertEqual(request.call_count, 3)
        self.assertEqual(sleep.call_count, 2)
        sent = request.call_args.args[0]
        self.assertEqual(sent.full_url, 'https://core.example:8443/api/v1/agent-daemon/connection?environment_id=' + self.environment)
        self.assertEqual(sent.get_header('Authorization'), 'Bearer ' + self.key['executor_token'])
        self.assertIn('Runtime connected to Environment', output.getvalue())
        self.assertNotIn(self.key['executor_token'], output.getvalue())

    def test_permanent_denial_does_not_retry_or_echo_server_body(self):
        for status in (401, 403, 404, 409):
            with patch.object(installer, 'open_connection', side_effect=urllib.error.HTTPError('url', status, 'private body', {}, None)) as request:
                with self.assertRaisesRegex(installer.InstallError, 'HTTP ' + str(status)) as failure:
                    self.wait()
            self.assertEqual(request.call_count, 1)
            self.assertIn('logs --tail 100 ' + self.container, str(failure.exception))
            self.assertNotIn('private body', str(failure.exception))

    def test_deadline_retains_container_and_has_retry_guidance(self):
        with patch.object(installer, 'open_connection', return_value=self.response('disconnected')), \
                patch.object(installer.time, 'monotonic', side_effect=[0, 0, 0, 0, 60, 60]), \
                patch.object(installer, 'checked') as mutation:
            with self.assertRaisesRegex(installer.InstallError, 'timed out.*disconnected') as failure:
                self.wait()
        mutation.assert_not_called()
        self.assertIn('rerun the same installation command', str(failure.exception))
        self.assertIn('do not replace history', str(failure.exception))

    def test_wrong_or_malformed_response_cannot_confirm_connection(self):
        for response in (self.response(environment='other'), self.response(status='running'), io.BytesIO(b'{}'), io.BytesIO(b'x' * 4097)):
            with patch.object(installer, 'open_connection', return_value=response):
                with self.assertRaisesRegex(installer.InstallError, 'invalid connection response'):
                    self.wait()

    def test_slow_response_cannot_outlive_deadline_or_report_success(self):
        body = self.response().getvalue()
        class SlowResponse(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                try:
                    for offset in range(0, len(body), 10):
                        self.wfile.write(body[offset:offset + 10])
                        self.wfile.flush()
                        time.sleep(0.07)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def log_message(self, *_args):
                pass

        server = http.server.HTTPServer(('127.0.0.1', 0), SlowResponse)
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        previous_handler = signal.getsignal(signal.SIGALRM)
        try:
            def open_local(request, timeout):
                local = urllib.request.Request('http://127.0.0.1:' + str(server.server_port),
                                               headers=dict(request.header_items()))
                return urllib.request.urlopen(local, timeout=timeout)
            with patch.object(installer, 'open_connection', side_effect=open_local), \
                    contextlib.redirect_stdout(io.StringIO()) as output:
                started = time.monotonic()
                with self.assertRaisesRegex(installer.InstallError, 'connection timed out'):
                    installer.wait_connected(self.remote, self.environment, self.key, self.container, timeout=0.2)
                elapsed = time.monotonic() - started
            self.assertLess(elapsed, 0.5)
            self.assertNotIn('Runtime connected', output.getvalue())
            self.assertEqual(signal.getitimer(signal.ITIMER_REAL), (0, 0))
            self.assertEqual(signal.getsignal(signal.SIGALRM), previous_handler)
        finally:
            server.shutdown()
            server.server_close()
            worker.join(timeout=2)

    def test_timer_and_handler_are_restored_on_success_and_rejection(self):
        previous_handler = signal.getsignal(signal.SIGALRM)
        try:
            signal.setitimer(signal.ITIMER_REAL, 30)
            for response in (self.response(), io.BytesIO(b'{}')):
                with patch.object(installer, 'open_connection', return_value=response), \
                        contextlib.redirect_stdout(io.StringIO()):
                    try:
                        self.wait()
                    except installer.InstallError:
                        pass
                remaining, interval = signal.getitimer(signal.ITIMER_REAL)
                self.assertGreater(remaining, 20)
                self.assertLessEqual(remaining, 30)
                self.assertEqual(interval, 0)
                self.assertEqual(signal.getsignal(signal.SIGALRM), previous_handler)
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)

    def test_redirect_never_forwards_bearer(self):
        for target in ('https://other.example/connection', 'https://core.example/new'):
            with self.assertRaisesRegex(installer.InstallError, 'redirects'):
                installer.NoRedirect().redirect_request(None, None, 302, '', {}, target)


if __name__ == '__main__':
    unittest.main()
