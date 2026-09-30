"""Credential verification never mutates receipts or infers absent ownership."""
from types import SimpleNamespace
from unittest.mock import patch
import unittest

from e2b.api.client.models.template import Template
from provider_test import ProviderTest
from sdk import verify_team_template
from state import Failure


class TeamTemplateTest(unittest.TestCase):
    @patch('sdk.get_api_client')
    @patch('e2b.api.client.api.templates.get_v2_templates.sync_detailed')
    def test_paginated_owned_template_and_public_readability_are_distinct(self, listing, client):
        template = object.__new__(Template)
        template.template_id = 'owned'
        config = {'Template': 'owned:build', 'APIKey': 'synthetic'}
        listing.side_effect = [SimpleNamespace(status_code=200, parsed=[], headers={'x-next-token': 'page2'}),
                               SimpleNamespace(status_code=200, parsed=[template], headers={})]
        verify_team_template(config, lambda: 5)
        self.assertEqual(listing.call_args.kwargs['next_token'], 'page2')
        listing.side_effect = None
        listing.return_value = SimpleNamespace(status_code=200, parsed=[], headers={})
        with self.assertRaises(Failure) as error:
            verify_team_template(config, lambda: 5)
        self.assertEqual(error.exception.code, 'team_mismatch')

    @patch('sdk.get_api_client')
    @patch('e2b.api.client.api.templates.get_v2_templates.sync_detailed')
    def test_team_listing_uses_custom_endpoint(self, listing, client):
        template = object.__new__(Template)
        template.template_id = 'owned'
        listing.return_value = SimpleNamespace(status_code=200, parsed=[template], headers={})
        verify_team_template({'Template': 'owned:build', 'APIKey': 'synthetic',
                              'APIURL': 'https://sandbox.example.com', 'Domain': 'sandbox.example.com'}, lambda: 5)
        connection = client.call_args.args[0]
        self.assertEqual(connection.api_url, 'https://sandbox.example.com')
        self.assertEqual(connection.domain, 'sandbox.example.com')

    @patch('sdk.get_api_client')
    @patch('e2b.api.client.api.templates.get_v2_templates.sync_detailed')
    def test_unauthorized_and_repeated_cursor_fail_closed(self, listing, client):
        config = {'Template': 'owned:build', 'APIKey': 'synthetic'}
        for response, code in [(SimpleNamespace(status_code=401), 'unauthorized'),
                               (SimpleNamespace(status_code=503), 'unconfirmed'),
                               (SimpleNamespace(status_code=200, parsed=[], headers={'x-next-token': 'same'}), 'unconfirmed')]:
            listing.return_value = response
            with self.assertRaises(Failure) as error:
                verify_team_template(config, lambda: 5)
            self.assertEqual(error.exception.code, code)


class CredentialReceiptTest(ProviderTest):
    def test_initial_public_template_readability_does_not_establish_team(self):
        with patch('provider.validate_deployment') as readable, patch('provider.verify_team_template', side_effect=Failure('team_mismatch')):
            result = self.call('validate_deployment')
        self.assertEqual(result['ErrorCode'], 'team_mismatch')
        readable.assert_not_called()
        self.api.create.assert_not_called()
        from pathlib import Path
        self.assertEqual(list(Path(self.temporary.name).iterdir()), [])


    def verify(self):
        self.request['References'] = [self.reference]
        with patch('provider.verify_team_template'), patch('provider.validate_deployment'):
            return self.call('verify_credential')

    def test_existing_receipt_is_verified_through_paginated_owned_listing(self):
        self.assertEqual(self.call('create')['ErrorCode'], '')
        before = self.record()
        paginator = SimpleNamespace(has_next=True)
        pages = iter([[], [self.cloud]])
        paginator.next_items = lambda **options: next(pages)
        self.api.list.return_value = paginator
        self.assertEqual(self.verify()['ErrorCode'], '')
        self.assertEqual(self.record(), before)
        self.api.kill.assert_not_called()

    def test_missing_unsettled_and_invisible_receipts_cannot_publish_a_key(self):
        self.assertEqual(self.verify()['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.call('create')['ErrorCode'], '')
        self.assertEqual(self.verify()['ErrorCode'], 'team_mismatch')
        from state import Receipt
        with Receipt(self.request, lambda: 5) as receipt:
            receipt.save(settled=False)
        before = self.record()
        self.assertEqual(self.verify()['ErrorCode'], 'unconfirmed')
        self.assertEqual(self.record(), before)
