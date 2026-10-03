"""Check that node setup follows Core's immutable specification and identity."""
import argparse
import copy
import io
import json
from pathlib import Path
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
            "microsandbox_ref": "oac-runtime@sha256:" + "d" * 64,
            "runtime_sha256": "e" * 64, "firmware_sha256": "f" * 64}}
        self.data = {"installation_id": self.args.installation_id, "provider": "docker", "generation": 3,
                     "specification": self.spec, "specification_digest": node_spec.digest("docker", self.spec),
                     "core_url": self.args.core_url, "max_active": 2, "max_retained": 8}
        self.retained = {"core_url": self.args.core_url, "credential": "f" * 64, "identity": {
            "node_id": "634d97be-e54d-40f0-9468-ae6b62be85bf", "installation_id": self.args.installation_id,
            "provider": "docker", "deployment_generation": 3, "specification_digest": self.data["specification_digest"]}}

    def response(self):
        return io.BytesIO(json.dumps(self.data).encode())

    def test_new_node_uses_enrollment_bearer_and_no_local_provider_default(self):
        opener = mock.Mock(return_value=self.response())
        self.assertEqual(node_spec.fetch(self.args, "once", None, opener), self.data)
        req = opener.call_args.args[0]
        self.assertEqual(req.full_url, self.args.core_url + "/api/v1/sandbox-node/configuration")
        self.assertEqual(dict(req.header_items()), {"Authorization": "Bearer once"})
        self.assertEqual(req.get_method(), "GET")

    def test_repeat_uses_retained_credentials_not_a_replacement_token(self):
        opener = mock.Mock(return_value=self.response())
        node_spec.fetch(self.args, "replacement", self.retained, opener)
        headers = dict(opener.call_args.args[0].header_items())
        self.assertEqual(headers["Authorization"], "Bearer " + self.retained["credential"])
        self.assertEqual(headers["X-oac-node-id"], self.retained["identity"]["node_id"])

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

    def test_changed_public_url_says_to_generate_a_new_command(self):
        self.data["core_url"] = "https://core-new.example"
        with self.assertRaisesRegex(node_spec.SpecificationError, "public URL changed"):
            node_spec.fetch(self.args, "once", None, mock.Mock(return_value=self.response()))

    def test_failures_name_their_cause(self):
        for failure, message in ((urllib.error.HTTPError("https://core.example", 404, "", {}, None), "route /api/v1"),
                                 (urllib.error.HTTPError("https://core.example", 409, "", {}, None), "resetting"),
                                 (urllib.error.HTTPError("https://core.example", 401, "", {}, None), "credential"),
                                 (urllib.error.URLError("refused"), "reverse proxy routes /api/v1")):
            with self.assertRaisesRegex(node_spec.SpecificationError, message):
                node_spec.fetch(self.args, "once", None, mock.Mock(side_effect=failure))

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

    def test_capacity_requires_approved_bounded_integers(self):
        for key, value in (("max_active", None), ("max_active", True), ("max_active", 0),
                           ("max_retained", 1), ("max_retained", 1000001)):
            data = copy.deepcopy(self.data)
            data[key] = value
            with self.subTest(key=key, value=value), self.assertRaises(node_spec.SpecificationError):
                node_spec.validate(data, self.args)
        self.data["max_active"], self.data["max_retained"] = 3, 9
        result = node_spec.fetch(self.args, "", self.retained, mock.Mock(return_value=self.response()))
        self.assertEqual((result["max_active"], result["max_retained"]), (3, 9))

    def test_digest_is_independent_of_response_object_key_order(self):
        reordered = copy.deepcopy(self.spec)
        reordered["runtime"] = dict(reversed(list(reordered["runtime"].items())))
        reordered["resources"] = dict(reversed(list(reordered["resources"].items())))
        self.assertEqual(node_spec.digest("docker", reordered), node_spec.digest("docker", self.spec))


class SharedDeploymentContractTests(unittest.TestCase):
    def test_shared_acceptance_and_canonical_bytes(self):
        path = Path(__file__).resolve().parents[2] / "services/core/internal/sandbox/testdata/deployment-contract.json"
        for fixture in json.loads(path.read_text()):
            with self.subTest(name=fixture["name"]):
                if not fixture["valid"]:
                    with self.assertRaises((KeyError, ValueError, TypeError)):
                        node_spec.canonical_spec(fixture["provider"], fixture["specification"])
                    continue
                canonical = json.dumps(node_spec.canonical_spec(fixture["provider"], fixture["specification"]), separators=(",", ":"), ensure_ascii=False)
                self.assertEqual(canonical, fixture["canonical"])
                self.assertEqual(node_spec.digest(fixture["provider"], fixture["specification"]), fixture["digest"])


if __name__ == "__main__":
    unittest.main()
