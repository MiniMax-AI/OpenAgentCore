"""Shared producer/consumer fixtures and Provider selector conformance."""
import json
from pathlib import Path
import unittest

from template_manifest import normalize_endpoint, valid_template, validate_manifest

ROOT = Path(__file__).parent


class ManifestTest(unittest.TestCase):
    def test_shared_manifest_cases(self):
        for case in json.loads((ROOT / 'testdata/template-manifests.json').read_text()):
            with self.subTest(case=case['name']):
                if case['valid']:
                    self.assertEqual(validate_manifest(case['value']), case['value'])
                else:
                    with self.assertRaises(ValueError):
                        validate_manifest(case['value'])

    def test_provider_selectors(self):
        fixture = ROOT / '../../internal/sandbox/e2b/testdata/configuration-selectors.json'
        cases = json.loads(fixture.read_text())
        for case in cases['endpoints']:
            with self.subTest(case=case['name']):
                if case['valid']:
                    normalize_endpoint(case['api_url'], case['domain'])
                else:
                    with self.assertRaises(ValueError):
                        normalize_endpoint(case['api_url'], case['domain'])
        for case in cases['templates']:
            with self.subTest(case=case['name']):
                self.assertEqual(valid_template(case['value']), case['valid'])


if __name__ == '__main__':
    unittest.main()
