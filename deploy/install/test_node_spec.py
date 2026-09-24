"""Check that node setup follows Core's immutable specification and identity."""
import argparse
import copy
import io
import json
import unittest
import urllib.error
from unittest import mock

import node_spec


class SpecificationTests(unittest.TestCase):
    def setUp(self):
        self.args = argparse.Namespace(core_url="https://core.example", installation_id="94be54a1-138c-4f30-bc87-b13686272dbe", provider=None)
        self.spec = {"resources": {"cpus": 2, "memory_mib": 4096}, "runtime": {
            "source_commit": "a" * 40, "image_id": "sha256:" + "b" * 64,
            "image_manifest_digest": "sha256:" + "c" * 64,
            "microsandbox_ref": "parsar-core-runtime@sha256:" + "d" * 64,
            "runtime_sha256": "e" * 64, "firmware_sha256": "f" * 64}}
        self.data = {"installation_id": self.args.installation_id, "provider": "docker", "generation": 3,
                     "specification": self.spec, "specification_digest": node_spec.digest("docker", self.spec),
                     "core_url": self.args.core_url}
        self.retained = {"core_url": self.args.core_url, "credential": "f" * 64, "identity": {
            "node_id": "634d97be-e54d-40f0-9468-ae6b62be85bf", "installation_id": self.args.installation_id,
            "provider": "docker", "deployment_generation": 3, "specification_digest": self.data["specification_digest"]}}

    def response(self):
        return io.BytesIO(json.dumps(self.data).encode())

    def test_new_node_uses_enrollment_bearer_and_no_local_provider_default(self):
        opener = mock.Mock(return_value=self.response())
        self.assertEqual(node_spec.fetch(self.args, "once", None, opener), self.data)
        req = opener.call_args.args[0]
        self.assertEqual(req.full_url, self.args.core_url + "/core/v1/sandbox/node/configuration")
        self.assertEqual(dict(req.header_items()), {"Authorization": "Bearer once"})
        self.assertEqual(req.get_method(), "GET")

    def test_repeat_uses_retained_credentials_not_a_replacement_token(self):
        opener = mock.Mock(return_value=self.response())
        node_spec.fetch(self.args, "replacement", self.retained, opener)
        headers = dict(opener.call_args.args[0].header_items())
        self.assertEqual(headers["Authorization"], "Bearer " + self.retained["credential"])
        self.assertEqual(headers["X-parsar-node-id"], self.retained["identity"]["node_id"])

    def test_partial_registration_can_use_enrollment_after_unauthenticated_identity(self):
        rejected = urllib.error.HTTPError("https://core.example", 401, "private details", {}, None)
        opener = mock.Mock(side_effect=[rejected, self.response()])
        node_spec.fetch(self.args, "once", self.retained, opener, allow_enrollment=True)
        self.assertEqual(opener.call_count, 2)
        self.assertEqual(dict(opener.call_args.args[0].header_items()), {"Authorization": "Bearer once"})
        for allowed, token in ((False, "once"), (True, "")):
            opener = mock.Mock(side_effect=rejected)
            with self.assertRaises(node_spec.SpecificationError):
                node_spec.fetch(self.args, token, self.retained, opener, allow_enrollment=allowed)
            self.assertEqual(opener.call_count, 1)

    def test_changed_generation_or_specification_rejects_retained_node(self):
        for change in (lambda: self.data.update(generation=4), lambda: self.spec["resources"].update(cpus=8)):
            original = copy.deepcopy(self.data)
            change()
            self.data["specification_digest"] = node_spec.digest("docker", self.data["specification"])
            with self.assertRaisesRegex(node_spec.SpecificationError, "Retained node specification differs"):
                node_spec.fetch(self.args, "once", self.retained, mock.Mock(return_value=self.response()))
            self.data = original
            self.spec = self.data["specification"]

    def test_invalid_limits_digests_and_provider_assertions_are_rejected(self):
        for field, value in (("cpus", True), ("memory_mib", 1), ("root_disk_mib", 8192), ("custom", 1)):
            data = copy.deepcopy(self.data)
            data["specification"]["resources"][field] = value
            with self.subTest(field=field), self.assertRaises(node_spec.SpecificationError):
                node_spec.validate(data, self.args)
        self.data["specification_digest"] = "0" * 64
        with self.assertRaises(node_spec.SpecificationError):
            node_spec.validate(self.data, self.args)
        self.data["specification_digest"] = node_spec.digest("docker", self.spec)
        self.args.provider = "microsandbox"
        with self.assertRaises(node_spec.SpecificationError):
            node_spec.validate(self.data, self.args)

    def test_digest_is_independent_of_response_object_key_order(self):
        reordered = copy.deepcopy(self.spec)
        reordered["runtime"] = dict(reversed(list(reordered["runtime"].items())))
        reordered["resources"] = dict(reversed(list(reordered["resources"].items())))
        self.assertEqual(node_spec.digest("docker", reordered), node_spec.digest("docker", self.spec))


if __name__ == "__main__":
    unittest.main()
