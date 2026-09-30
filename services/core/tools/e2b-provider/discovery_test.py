"""Template discovery through the pinned generated SDK client."""
from datetime import datetime, timezone
from types import SimpleNamespace
from unittest.mock import patch
from uuid import uuid4
import unittest

from e2b.api.client.models.template import Template
from e2b.api.client.models.template_build import TemplateBuild
from e2b.api.client.models.template_build_status import TemplateBuildStatus
from e2b.api.client.models.template_with_builds import TemplateWithBuilds

from sdk import list_builds, list_templates
from state import Failure


class DiscoveryTest(unittest.TestCase):
    def setUp(self):
        self.config = {'APIKey': 'synthetic-key', 'APIURL': 'https://api.e2b.app',
                       'Domain': 'e2b.app', 'Template': 'tpl_123'}
        self.now = datetime.now(timezone.utc)

    def template(self):
        return Template(template_id='tpl_123', build_id=str(uuid4()), cpu_count=2,
                        memory_mb=2048, disk_size_mb=8192, public=False, aliases=[],
                        names=['runtime'], created_at=self.now, updated_at=self.now,
                        created_by=None, last_spawned_at=None, spawn_count=0,
                        build_count=2, envd_version='0.3', build_status=TemplateBuildStatus.READY)

    def build(self, status):
        return TemplateBuild(build_id=uuid4(), status=status, created_at=self.now,
                             updated_at=self.now, cpu_count=2, memory_mb=2048)

    def page(self, builds, next_token=None):
        body = TemplateWithBuilds(template_id='tpl_123', public=False, aliases=[],
                                  names=['runtime'], created_at=self.now, updated_at=self.now,
                                  last_spawned_at=None, spawn_count=0, builds=builds)
        return SimpleNamespace(status_code=200, parsed=body,
                               headers={'x-next-token': next_token} if next_token else {})

    @patch('sdk.get_api_client')
    @patch('sdk.get_v2_templates.sync_detailed')
    def test_visible_templates_and_empty_list(self, get, client):
        get.return_value = SimpleNamespace(status_code=200, parsed=[self.template()], headers={})
        self.assertEqual(list_templates(self.config, lambda: 5),
                         [{'id': 'tpl_123', 'names': ['runtime']}])
        self.assertEqual(client.call_args.args[0].api_url, self.config['APIURL'])
        self.assertEqual(get.call_args.kwargs['limit'], 100)
        get.side_effect = [SimpleNamespace(status_code=200, parsed=[], headers={'x-next-token': 'cursor'}),
                           SimpleNamespace(status_code=200, parsed=[self.template()], headers={})]
        self.assertEqual(len(list_templates(self.config, lambda: 5)), 1)
        self.assertEqual(get.call_args.kwargs['next_token'], 'cursor')
        get.side_effect = None
        get.return_value = SimpleNamespace(status_code=200, parsed=[], headers={})
        self.assertEqual(list_templates(self.config, lambda: 5), [])
        get.return_value = SimpleNamespace(status_code=403, parsed=None)
        with self.assertRaises(Failure) as failure:
            list_templates(self.config, lambda: 5)
        self.assertEqual(failure.exception.code, 'invalid')

    @patch('sdk.get_api_client')
    @patch('sdk.get_templates_template_id.sync_detailed')
    def test_ready_builds_across_pages_and_empty_result(self, get, client):
        ready, failed = self.build(TemplateBuildStatus.READY), self.build(TemplateBuildStatus.ERROR)
        get.side_effect = [self.page([failed], 'cursor'), self.page([ready])]
        self.assertEqual(list_builds(self.config, lambda: 5),
                         [{'id': str(ready.build_id), 'cpus': 2, 'memory_mib': 2048}])
        self.assertEqual(get.call_args.kwargs['next_token'], 'cursor')
        self.assertEqual(get.call_args.kwargs['limit'], 100)
        self.assertEqual(client.call_args.args[0].domain, self.config['Domain'])
        get.side_effect = None
        get.return_value = self.page([])
        self.assertEqual(list_builds(self.config, lambda: 5), [])

    @patch('sdk.get_api_client')
    @patch('sdk.get_templates_template_id.sync_detailed')
    def test_repeated_cursor_and_provider_failure(self, get, _client):
        get.return_value = self.page([], 'cursor')
        with self.assertRaises(Failure) as failure:
            list_builds(self.config, lambda: 5)
        self.assertEqual(failure.exception.code, 'unconfirmed')
        get.return_value = SimpleNamespace(status_code=503, parsed=None)
        with self.assertRaises(Failure) as failure:
            list_builds(self.config, lambda: 5)
        self.assertEqual(failure.exception.code, 'unconfirmed')
