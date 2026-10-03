"""Validate acceptance inputs without contacting Core or model providers."""
import copy
import json
from types import SimpleNamespace
import unittest
from unittest import mock

import acceptance


class AcceptanceSettingsTests(unittest.TestCase):
    def setUp(self):
        self.models = {
            harness: {"model": "fixture", "model_provider": {
                "protocol": protocol, "base_url": "https://model.example/v1", "api_key": "private-key",
                "context_window": 64000, "max_output_tokens": 4096,
            }} for harness, protocol in acceptance.CAMPAIGN.items()
        }

    def settings(self, models=None):
        models = self.models if models is None else models
        with mock.patch.object(acceptance, "private_file", side_effect=["caller-key", json.dumps(models)]):
            return acceptance.settings(SimpleNamespace(caller_key_file=None, model_config_file=None))

    def test_campaign_is_explicit_and_valid(self):
        self.assertEqual(acceptance.CAMPAIGN, {
            "codex": "responses", "claude_sdk": "anthropic", "mcode": "anthropic"})
        token, models, secrets = self.settings()
        self.assertEqual(token, "caller-key")
        self.assertEqual(models, self.models)
        self.assertEqual(secrets, ["caller-key"] + ["private-key"] * 3)

    def test_catalog_support_does_not_expand_campaign(self):
        for protocol in ("responses", "chat_completions"):
            self.assertIn(protocol, acceptance.PROVIDERS["mcode"])
            self.models["mcode"]["model_provider"]["protocol"] = protocol
            with self.subTest(protocol=protocol), self.assertRaisesRegex(
                    acceptance.Failure, "provider_protocol_does_not_match_campaign"):
                self.settings()
        expanded = {**acceptance.PROVIDERS, "additional": {"responses": {"requires_token_limits": False}}}
        with mock.patch.object(acceptance, "PROVIDERS", expanded):
            self.models["mcode"]["model_provider"]["protocol"] = "anthropic"
            self.settings()
            self.models["additional"] = copy.deepcopy(self.models["codex"])
            with self.assertRaisesRegex(acceptance.Failure, "require_all_three_harness_configs"):
                self.settings()

    def test_unsupported_campaign_is_rejected_before_reading_secrets(self):
        for campaign in ({}, {"unknown": "responses"}, {"codex": "anthropic"}):
            with self.subTest(campaign=campaign), mock.patch.object(acceptance, "CAMPAIGN", campaign), \
                    mock.patch.object(acceptance, "private_file") as private_file:
                with self.assertRaisesRegex(acceptance.Failure, "unsupported_acceptance_campaign"):
                    acceptance.settings(SimpleNamespace())
                private_file.assert_not_called()

    def test_token_limit_requirements_follow_declarations(self):
        for harness in acceptance.CAMPAIGN:
            models = copy.deepcopy(self.models)
            del models[harness]["model_provider"]["context_window"]
            del models[harness]["model_provider"]["max_output_tokens"]
            if harness == "mcode":
                with self.assertRaisesRegex(acceptance.Failure, "harness_requires_positive_model_limits"):
                    self.settings(models)
            else:
                self.settings(models)
        providers = copy.deepcopy(acceptance.PROVIDERS)
        providers["codex"]["responses"]["requires_token_limits"] = True
        del self.models["codex"]["model_provider"]["context_window"]
        with mock.patch.object(acceptance, "PROVIDERS", providers):
            with self.assertRaisesRegex(acceptance.Failure, "harness_requires_positive_model_limits"):
                self.settings()

    def test_model_limit_validation(self):
        for field in ("context_window", "max_output_tokens"):
            for value in (False, -1, "100", 1.5, None):
                models = copy.deepcopy(self.models)
                models["codex"]["model_provider"][field] = value
                with self.subTest(field=field, value=value), self.assertRaisesRegex(
                        acceptance.Failure, "invalid_model_limits"):
                    self.settings(models)
        for field in ("context_window", "max_output_tokens"):
            models = copy.deepcopy(self.models)
            models["mcode"]["model_provider"][field] = 0
            with self.assertRaisesRegex(acceptance.Failure, "harness_requires_positive_model_limits"):
                self.settings(models)
        self.models["codex"]["model_provider"]["max_output_tokens"] = 64001
        with self.assertRaisesRegex(acceptance.Failure, "invalid_model_limits"):
            self.settings()

    def test_provider_validation_is_preserved(self):
        for patch, label in (
            ({"unknown": "value"}, "invalid_model_provider_fields"),
            ({"api_key": "secret\nvalue"}, "invalid_model_provider_key"),
            ({"api_key": ""}, "invalid_model_provider_key"),
            ({"base_url": "http://model.example"}, "model_origin_requires_https"),
            ({"base_url": "https://api.openai.com"}, "official_openai_origin_refused"),
            ({"protocol": "anthropic"}, "provider_protocol_does_not_match_campaign"),
        ):
            models = copy.deepcopy(self.models)
            models["codex"]["model_provider"].update(patch)
            with self.subTest(label=label), self.assertRaisesRegex(acceptance.Failure, label):
                self.settings(models)


if __name__ == "__main__":
    unittest.main()
