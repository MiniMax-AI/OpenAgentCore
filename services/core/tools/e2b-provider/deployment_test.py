"""Read-only build validation and pre-bootstrap resource rejection."""
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch
from uuid import UUID, uuid4
import unittest

from e2b.api.client.models.template_build import TemplateBuild
from e2b.api.client.models.template_build_status import TemplateBuildStatus
from e2b.api.client.models.template_with_builds import TemplateWithBuilds

from provider_test import ProviderTest
from sdk import read_metrics, sdk_options, validate_deployment
from state import Failure
from e2b.exceptions import SandboxNotFoundException


class ManagedResourcesTest(ProviderTest):
    def setUp(self):
        super().setUp()
        self.config['Resources'] = {'cpus': 3, 'memory_mib': 4096}
        self.cloud.template_id = 'test'
        self.cloud.cpu_count = 3
        self.cloud.memory_mb = 4096

    def test_resource_mismatch_precedes_confidential_bootstrap_and_stays_cleanable(self):
        self.cloud.memory_mb = 2048
        result = self.call('create')
        self.assertEqual(result['ErrorCode'], 'invalid')
        self.assertTrue(result['Info']['CreateSettled'])
        self.assertFalse(result['Info']['BootstrapComplete'])
        self.assertEqual(self.record()['ids'], ['owned-id'])
        self.cloud.files.write.assert_not_called()
        self.assertEqual(self.call('inspect')['ErrorCode'], 'invalid')
        self.api.kill.side_effect = lambda *a, **k: setattr(self.api.get_info, 'side_effect', SandboxNotFoundException())
        self.assertEqual(self.call('kill')['ErrorCode'], '')

    def test_inspection_checks_current_limits(self):
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.cloud.cpu_count = 1
        self.assertEqual(self.call('inspect')['ErrorCode'], 'invalid')

    def test_candidate_validation_never_opens_allocation_receipt(self):
        with patch('provider.verify_team_template') as ownership, patch('provider.validate_deployment') as validate, patch('provider.Receipt') as receipt:
            result = self.call('validate_deployment')
        self.assertTrue(result['DeploymentValid'])
        ownership.assert_called_once()
        validate.assert_called_once()
        receipt.assert_not_called()
        self.assertEqual(list(Path(self.temporary.name).iterdir()), [])
        self.api.create.assert_not_called()


class BuildValidationTest(unittest.TestCase):
    def test_custom_endpoint_is_explicit_in_sdk_options(self):
        options = sdk_options({'APIKey': 'synthetic-key', 'APIURL': 'https://sandbox-test.sandbase.ai',
                               'Domain': 'sandbox-test.sandbase.ai'}, lambda: 5)
        self.assertEqual(options['api_url'], 'https://sandbox-test.sandbase.ai')
        self.assertEqual(options['domain'], 'sandbox-test.sandbase.ai')
        self.assertEqual(options['request_timeout'], 5)

    @patch('sdk.get_api_client')
    @patch('sdk.get_sandboxes_metrics.sync_detailed')
    def test_custom_endpoint_reaches_metrics_client(self, metrics, client):
        self.config.update(APIURL='https://sandbox-test.sandbase.ai', Domain='sandbox-test.sandbase.ai')
        metrics.return_value = SimpleNamespace(status_code=503, parsed=None)
        with self.assertRaises(Failure):
            read_metrics(self.config, ['owned-id'], lambda: 5)
        configuration = client.call_args.args[0]
        self.assertEqual((configuration.api_url, configuration.domain),
                         (self.config['APIURL'], self.config['Domain']))

    def setUp(self):
        self.build_id = uuid4()
        self.config = {'Template': 'test:' + str(self.build_id), 'APIKey': 'synthetic-key',
                       'Resources': {'cpus': 3, 'memory_mib': 4096}}
        now = datetime.now(timezone.utc)
        self.build = TemplateBuild(build_id=self.build_id, status=TemplateBuildStatus.READY,
                                   created_at=now, updated_at=now, cpu_count=3, memory_mb=4096)

    def response(self, builds, cursor=None):
        now = datetime.now(timezone.utc)
        data = TemplateWithBuilds(template_id='test', public=False, aliases=[], names=[],
                                 created_at=now, updated_at=now, last_spawned_at=None, spawn_count=0, builds=builds)
        return SimpleNamespace(status_code=200, parsed=data, headers={'x-next-token': cursor} if cursor else {})

    @patch('sdk.get_api_client')
    @patch('sdk.get_templates_template_id.sync_detailed')
    def test_exact_build_on_later_page_and_bounded_options(self, get, client):
        self.config.update(APIURL='https://sandbox-test.sandbase.ai', Domain='sandbox-test.sandbase.ai')
        get.side_effect = [self.response([], 'next'), self.response([self.build])]
        validate_deployment(self.config, lambda: 5)
        self.assertEqual(get.call_count, 2)
        self.assertEqual(get.call_args.kwargs['next_token'], 'next')
        configuration = client.call_args.args[0]
        self.assertEqual(configuration.retries, 0)
        self.assertEqual(configuration.request_timeout, 5)
        self.assertEqual((configuration.api_url, configuration.domain),
                         (self.config['APIURL'], self.config['Domain']))

    @patch('sdk.get_api_client')
    @patch('sdk.get_templates_template_id.sync_detailed')
    def test_returns_build_and_adopts_it_when_resources_are_omitted(self, get, client):
        self.build.disk_size_mb = 24063
        get.return_value = self.response([self.build])
        expected = {'Status': 'ready', 'CPUs': 3, 'MemoryMiB': 4096, 'RootDiskMiB': 24063}
        self.assertEqual(validate_deployment(self.config, lambda: 5), expected)
        self.config['Resources'] = None
        self.build.cpu_count = 8
        self.assertEqual(validate_deployment(self.config, lambda: 5), dict(expected, CPUs=8))

    @patch('sdk.get_api_client')
    @patch('sdk.get_templates_template_id.sync_detailed')
    def test_rejects_missing_failed_or_mismatched_build(self, get, client):
        for field, value in [('build_id', uuid4()), ('status', TemplateBuildStatus.ERROR),
                             ('cpu_count', 1), ('memory_mb', 2048)]:
            with self.subTest(field=field):
                original = getattr(self.build, field)
                setattr(self.build, field, value)
                get.return_value = self.response([self.build])
                with self.assertRaises(Failure) as error:
                    validate_deployment(self.config, lambda: 5)
                self.assertEqual(error.exception.code, 'invalid')
                setattr(self.build, field, original)

    @patch('sdk.get_api_client')
    @patch('sdk.get_templates_template_id.sync_detailed')
    def test_repeated_cursor_stops_and_unknown_response_stays_unconfirmed(self, get, client):
        get.return_value = self.response([], 'same')
        with self.assertRaises(Failure) as error:
            validate_deployment(self.config, lambda: 5)
        self.assertEqual(error.exception.code, 'unconfirmed')
        self.assertEqual(get.call_count, 2)
        get.return_value = SimpleNamespace(status_code=503, parsed=None)
        with self.assertRaises(Failure) as error:
            validate_deployment(self.config, lambda: 5)
        self.assertEqual(error.exception.code, 'unconfirmed')

    def test_disk_limit_cannot_be_silently_ignored(self):
        self.config['Resources']['root_disk_mib'] = 8192
        with self.assertRaises(Failure) as error:
            validate_deployment(self.config, lambda: 5)
        self.assertEqual(error.exception.code, 'invalid')


if __name__ == '__main__':
    unittest.main()
