"""Real HTTP/CONNECT downloads across the installer's environment reset."""
import base64
import contextlib
import http.server
import io
import os
from pathlib import Path
import pwd
import select
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.request
from types import SimpleNamespace
from unittest import mock

import distribution
import node_install as installer

PROXIES = ("http_proxy", "https_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY")
PAYLOAD = b"node proxy download fixture"
AUTH = "Basic " + base64.b64encode(b"fixture:private password").decode()


@contextlib.contextmanager
def serve(handler, context=None):
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    if context:
        server.socket = context.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


class Origin(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        # Proxy authentication belongs only to the proxy, never the download origin.
        self.server.leaked_auth |= self.headers.get("Proxy-Authorization") is not None
        self.send_response(200)
        self.send_header("Content-Length", str(len(PAYLOAD)))
        self.end_headers()
        self.wfile.write(PAYLOAD)

    def log_message(self, *args):
        pass


class Proxy(http.server.BaseHTTPRequestHandler):
    def authorized(self):
        self.server.requests.append((self.command, self.path))
        if self.server.authenticate and self.headers.get("Proxy-Authorization") != AUTH:
            self.send_error(407)
            return False
        return True

    def do_GET(self):
        if self.authorized():
            self.send_response(200)
            self.send_header("Content-Length", str(len(PAYLOAD)))
            self.end_headers()
            self.wfile.write(PAYLOAD)

    def do_CONNECT(self):
        if not self.authorized():
            return
        with socket.create_connection(self.server.origin.server_address, timeout=5) as upstream:
            self.send_response(200)
            self.end_headers()
            self.wfile.flush()
            peers = [self.connection, upstream]
            while True:
                ready, _, _ = select.select(peers, [], [], 5)
                if not ready:
                    return
                for peer in ready:
                    data = peer.recv(65536)
                    if not data:
                        return
                    (upstream if peer is self.connection else self.connection).sendall(data)

    def log_message(self, *args):
        pass


@unittest.skipUnless(sys.platform == "linux", "node service-user downloads require Linux")
class NodeProxyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        base = Path.home() / ".oac/tests"
        base.mkdir(parents=True, exist_ok=True)
        cls.temporary = tempfile.TemporaryDirectory(dir=base)
        cls.directory = Path(cls.temporary.name)
        key, certificate = cls.directory / "key.pem", cls.directory / "cert.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                        "-keyout", str(key), "-out", str(certificate), "-days", "1",
                        "-subj", "/CN=downloads.invalid",
                        "-addext", "subjectAltName=DNS:downloads.invalid,IP:127.0.0.1"],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        cls.tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        cls.tls.load_cert_chain(certificate, key)
        cls.trust = ssl.create_default_context(cafile=str(certificate))

    @classmethod
    def tearDownClass(cls):
        cls.temporary.cleanup()

    def child_download(self, env, url, expect_failure=False):
        output, errors = io.StringIO(), io.StringIO()
        account = pwd.getpwnam("nobody") if os.geteuid() == 0 else SimpleNamespace(
            pw_name=pwd.getpwuid(os.getuid()).pw_name, pw_uid=os.getuid(),
            pw_gid=os.getgid(), pw_dir="/")
        # Real setuid when run in the disposable root container. Normal CI also
        # exercises the fork/environment boundary under its existing account.
        account = SimpleNamespace(pw_name=account.pw_name, pw_uid=account.pw_uid,
                                  pw_gid=account.pw_gid, pw_dir="/")
        def download():
            if os.geteuid() == 0:
                raise AssertionError("download must run without root")
            assert "UNRELATED_SECRET" not in os.environ
            assert "PYTHONPATH" not in os.environ
            assert "ALL_PROXY" not in os.environ
            assert os.environ["HOME"] == "/"
            for name in ("http_proxy", "https_proxy", "no_proxy"):
                assert os.environ.get(name) == os.environ.get(name.upper())
            # Both metadata and artifact download openers use the retained proxy.
            assert installer.fetch(url, "manifest.json").read() == PAYLOAD
            with urllib.request.build_opener(distribution.ArtifactRedirect()).open(url, timeout=5) as response:
                assert response.read() == PAYLOAD
            print("download verified")
        with contextlib.ExitStack() as stack:
            stack.enter_context(mock.patch.dict(os.environ, {
                **env, "UNRELATED_SECRET": "not inherited", "PYTHONPATH": "/untrusted",
                "ALL_PROXY": "http://must-not-be-inherited.invalid"}, clear=True))
            stack.enter_context(mock.patch.object(ssl, "_create_default_https_context", return_value=self.trust))
            stack.enter_context(mock.patch.object(installer.sys, "stdout", output))
            stack.enter_context(mock.patch.object(installer.sys, "stderr", errors))
            # Container keyring policy is independent of proxy inheritance.
            stack.enter_context(mock.patch.object(installer, "libc_call"))
            if os.geteuid() != 0:
                stack.enter_context(mock.patch.object(os, "setgroups"))
            if expect_failure:
                with self.assertRaises(installer.ChildFailed):
                    installer.as_service_user(account, download)
            else:
                installer.as_service_user(account, download)
                self.assertIn("download verified", output.getvalue())
        for sensitive in ("private password", "private%20password", "fixture@", AUTH):
            self.assertNotIn(sensitive, output.getvalue() + errors.getvalue())
        return errors.getvalue()

    def test_proxy_only_downloads_lower_upper_and_authentication(self):
        for uppercase in (False, True):
            for authenticate in (False, True):
                with self.subTest(uppercase=uppercase, authenticate=authenticate), serve(Origin, self.tls) as origin, serve(Proxy) as proxy:
                    origin.leaked_auth = False
                    proxy.origin, proxy.authenticate, proxy.requests = origin, authenticate, []
                    value = "http://" + ("fixture:private%20password@" if authenticate else "") + "127.0.0.1:" + str(proxy.server_port)
                    env = {name.upper() if uppercase else name: value for name in ("http_proxy", "https_proxy")}
                    if not uppercase:
                        env.update(HTTP_PROXY="http://unreachable.invalid", HTTPS_PROXY="http://unreachable.invalid",
                                   NO_PROXY="*", no_proxy="")
                    for scheme in ("http", "https"):
                        self.child_download(env, scheme + "://downloads.invalid")
                    self.assertEqual([method for method, _ in proxy.requests], ["GET", "GET", "CONNECT", "CONNECT"])
                    self.assertFalse(origin.leaked_auth)

    def test_direct_and_no_proxy_bypass(self):
        with serve(Origin, self.tls) as origin, serve(Proxy) as proxy:
            origin.leaked_auth = False
            proxy.origin, proxy.authenticate, proxy.requests = origin, True, []
            url = "https://127.0.0.1:" + str(origin.server_port)
            for env in ({}, {"https_proxy": "", "HTTPS_PROXY": "http://unreachable.invalid"},
                        {"HTTPS_PROXY": "http://127.0.0.1:" + str(proxy.server_port), "NO_PROXY": "irrelevant.invalid,127.0.0.1"},
                        {"https_proxy": "http://127.0.0.1:" + str(proxy.server_port), "no_proxy": "127.0.0.1",
                         "HTTPS_PROXY": "http://unreachable.invalid", "NO_PROXY": "wrong.invalid"}):
                self.child_download(env, url)
            self.assertEqual(proxy.requests, [])
            self.assertFalse(origin.leaked_auth)

    def test_rejected_proxy_does_not_reveal_credentials(self):
        with serve(Proxy) as proxy:
            proxy.authenticate, proxy.requests = True, []
            error = self.child_download({"http_proxy": "http://fixture:wrong-private%20password@127.0.0.1:" + str(proxy.server_port)},
                                        "http://downloads.invalid", expect_failure=True)
            self.assertIn("HTTP 407", error)

    def test_installer_proxy_is_not_in_service_unit(self):
        with mock.patch.dict(os.environ, {"https_proxy": "http://fixture:private%20password@proxy.invalid"}):
            unit = installer.system_unit(Path("/var/lib/oac-node/.oac/nodes/fixture"), "docker")
        self.assertIn("User=oac-node", unit)
        self.assertNotIn("proxy", unit.lower())


if __name__ == "__main__":
    unittest.main()
