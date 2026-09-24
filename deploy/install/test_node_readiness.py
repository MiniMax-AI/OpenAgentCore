"""Verify bounded bootstrap reads and authenticated provider readiness."""
import argparse
import io
import http.client
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.error

import node_install as installer


class ReadinessTests(unittest.TestCase):
    def setUp(self):
        base = Path.home() / ".parsar/tests/node-readiness"
        base.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.args = argparse.Namespace(core_url="https://core.example", provider="docker",
                                       installation_id="94be54a1-138c-4f30-bc87-b13686272dbe")
        self.identity = {"node_id": "634d97be-e54d-40f0-9468-ae6b62be85bf", "installation_id": self.args.installation_id,
                         "provider": self.args.provider, "deployment_generation": 1, "specification_digest": "b" * 64}
        self.path = self.root / "state/node/identity.json"
        self.path.parent.mkdir(parents=True)
        self.path.write_text(json.dumps({"identity": self.identity, "credential": "a" * 64, "core_url": self.args.core_url}))
        self.path.chmod(0o600)

    def response(self, **fields):
        return io.BytesIO(json.dumps(dict(self.identity, **fields)).encode())

    def test_waits_for_both_connection_and_provider_without_restarting_service(self):
        replies = [self.response(connected=False, provider_ready=True), self.response(connected=True, provider_ready=False),
                   self.response(connected=True, provider_ready=True)]
        with mock.patch.object(installer, "open_request", side_effect=replies) as request, \
             mock.patch.object(installer.time, "sleep") as sleep:
            installer.wait_ready(self.root, self.args)
        self.assertEqual(request.call_count, 3)
        self.assertEqual(sleep.call_count, 2)
        self.assertEqual(request.call_args.args[0].get_header("Authorization"), "Bearer " + "a" * 64)
        self.assertNotIn("a" * 64, request.call_args.args[0].full_url)

    def test_transient_readiness_failure_recovers_with_same_identity(self):
        original = self.path.read_bytes()
        failure = urllib.error.HTTPError("https://core.example", 503, "unavailable", {}, None)
        with mock.patch.object(installer, "open_request", side_effect=[failure, self.response(connected=True, provider_ready=True)]), \
             mock.patch.object(installer.time, "sleep"):
            installer.wait_ready(self.root, self.args)
        self.assertEqual(self.path.read_bytes(), original)

    def test_permanent_authorization_failure_is_not_retried(self):
        failure = urllib.error.HTTPError("https://core.example", 401, "private failure text", {}, None)
        with mock.patch.object(installer, "open_request", side_effect=failure) as request, \
             mock.patch.object(installer.time, "sleep") as sleep:
            with self.assertRaisesRegex(installer.InstallError, "HTTP 401"):
                installer.wait_ready(self.root, self.args)
        self.assertEqual(request.call_count, 1)
        sleep.assert_not_called()

    def test_deadline_reports_provider_and_retains_identity(self):
        original = self.path.read_bytes()
        with mock.patch.object(installer, "open_request", return_value=self.response(connected=True, provider_ready=False)), \
             mock.patch.object(installer.time, "monotonic", side_effect=[0, 0, 0, 60, 60]):
            with self.assertRaisesRegex(installer.InstallError, "provider is not ready.*journalctl"):
                installer.wait_ready(self.root, self.args)
        self.assertEqual(self.path.read_bytes(), original)

    def test_private_or_mismatched_identity_is_not_sent(self):
        self.path.chmod(0o644)
        with mock.patch.object(installer, "open_request") as request:
            with self.assertRaises(installer.InstallError):
                installer.wait_ready(self.root, self.args)
            self.path.chmod(0o600)
            self.args.core_url = "https://other.example"
            with self.assertRaisesRegex(installer.InstallError, "identity differs"):
                installer.wait_ready(self.root, self.args)
        request.assert_not_called()

    def test_response_cannot_substitute_another_node(self):
        with mock.patch.object(installer, "open_request", return_value=self.response(node_id="other", connected=True, provider_ready=True)):
            with self.assertRaisesRegex(installer.InstallError, "different node identity"):
                installer.wait_ready(self.root, self.args)


class MetadataRetryTests(unittest.TestCase):
    def test_fetch_retries_transient_failure_with_bounded_delays(self):
        failure = urllib.error.HTTPError("https://console.example", 503, "unavailable", {}, None)
        with mock.patch.object(installer, "open_request", side_effect=[failure, failure, io.BytesIO(b"ok")]) as request, \
             mock.patch.object(installer.time, "sleep") as sleep:
            with installer.fetch("https://console.example", "manifest.json") as response:
                self.assertEqual(response.read(), b"ok")
        self.assertEqual(request.call_count, 3)
        self.assertEqual(sleep.call_args_list, [mock.call(1), mock.call(2)])

    def test_fetch_retries_a_truncated_response_before_using_metadata(self):
        truncated = mock.MagicMock()
        truncated.__enter__.return_value.read.side_effect = http.client.IncompleteRead(b"partial", 100)
        with mock.patch.object(installer, "open_request", side_effect=[truncated, io.BytesIO(b"complete")]) as request, \
             mock.patch.object(installer.time, "sleep"):
            with installer.fetch("https://console.example", "manifest.json") as response:
                self.assertEqual(response.read(), b"complete")
        self.assertEqual(request.call_count, 2)

    def test_fetch_rejects_permanent_status_without_retry_or_response_body(self):
        failure = urllib.error.HTTPError("https://console.example", 404, "private response body", {}, None)
        with mock.patch.object(installer, "open_request", side_effect=failure) as request:
            with self.assertRaisesRegex(installer.InstallError, "manifest.json.*HTTP 404") as caught:
                installer.fetch("https://console.example", "manifest.json")
        self.assertEqual(request.call_count, 1)
        self.assertNotIn("private response body", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
