#!/usr/bin/env python3
"""Regression tests for the official schema projection and extension boundary."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('public_api', Path(__file__).with_name('generate-public-api.py'))
generator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(generator)


class PublicAPITests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source, cls.pin = generator.read_source()
        cls.bindings = json.loads((generator.CONTRACT / 'go-bindings.json').read_text())
        cls.public = json.loads((generator.CONTRACT / 'openapi.yaml').read_text())

    def test_generated_types_are_current(self):
        self.assertEqual(generator.go_types(self.source, self.bindings), (generator.CONTRACT / 'v1/official.gen.go').read_bytes())

    def test_public_projection_is_current_and_keeps_source_immutable(self):
        source = copy.deepcopy(self.source)
        extensions = {k: v for k, v in self.public['components']['schemas'].items() if k.startswith('v1.')}
        actual = generator.public_document(source, extensions, generator.extension_owners(self.bindings), self.pin["beta_header"])
        self.assertEqual(actual, self.public)
        self.assertEqual(source, self.source)

    def test_union_and_nullable_fields_keep_the_official_schema(self):
        schemas = self.public['components']['schemas']
        self.assertIn('anyOf', schemas['CreateAgentSessionParams']['properties']['input'])
        self.assertIn('oneOf', schemas['SessionTurnItemResource'])
        self.assertIn('discriminator', schemas['SessionEvent'])
        self.assertEqual(schemas['AgentResource']['required'], self.source['components']['schemas']['AgentResource']['required'])

    def test_extensions_are_optional_and_reachable(self):
        schemas = self.public['components']['schemas']
        for owner, extension in generator.extension_owners(self.bindings).items():
            self.assertNotIn('x_agents_core', schemas[owner].get('required', []))
            self.assertEqual(schemas[owner]['properties']['x_agents_core'], {'anyOf': [{'$ref': '#/components/schemas/v1.' + extension}, {'type': 'null'}]})
            self.assertIn('v1.' + extension, schemas)
        self.assertIn('model_provider', schemas['v1.SessionExecutionInput']['properties'])
        self.assertNotIn('api.CoreHarness', schemas)

    def test_new_official_fields_reach_go_without_a_second_field_list(self):
        source = copy.deepcopy(self.source)
        source['components']['schemas']['VaultResource']['properties']['future_field'] = {'type': ['string', 'null']}
        generated = generator.go_types(source, self.bindings).decode()
        self.assertIn('FutureField *string', generated)
        self.assertIn('json:"future_field,omitempty"', generated)

    def test_generated_request_shapes_are_current(self):
        owners = generator.extension_owners(self.bindings)
        self.assertEqual(generator.request_shapes(self.source, owners), generator.SHAPES.read_bytes())

    def test_new_official_members_and_values_reach_request_shapes(self):
        source = copy.deepcopy(self.source)
        schemas = source['components']['schemas']
        schemas['McpTransportConfigParamStdio']['properties']['future_field'] = {'type': 'string'}
        schemas['ServiceTierParam']['enum'].append('future_tier')
        generated = generator.request_shapes(source, generator.extension_owners(self.bindings)).decode()
        self.assertIn('{"future_field", shape{kind: stringValue}}', generated)
        self.assertEqual(generated.count('"future_tier"'), 3)

    def test_open_request_objects_are_rejected(self):
        source = copy.deepcopy(self.source)
        del source['components']['schemas']['McpTransportConfigParamStdio']['additionalProperties']
        with self.assertRaisesRegex(ValueError, 'Unsupported open object'):
            generator.request_shapes(source, generator.extension_owners(self.bindings))

    def test_stale_binding_cannot_add_a_public_field(self):
        bindings = copy.deepcopy(self.bindings)
        bindings['Vault']['fields'] = {'private_data': {'type': 'string'}}
        with self.assertRaisesRegex(ValueError, 'missing official fields'):
            generator.go_types(self.source, bindings)

    def test_beta_header_is_only_required_on_beta_resources(self):
        for path, item in self.public['paths'].items():
            for method, operation in item.items():
                if method in generator.HTTP_METHODS:
                    headers = [p for p in operation.get('parameters', []) if p.get('name') == 'OpenAI-Beta']
                    self.assertEqual(bool(headers), path.split('/')[1] in ('agents', 'vaults'))

    def test_optional_nullable_and_union_representation(self):
        self.assertEqual(generator.field_type({}, {'type': ['string', 'null']}, True, {}), '*string')
        self.assertEqual(generator.field_type({}, {'type': 'boolean'}, False, {}), '*bool')
        self.assertEqual(generator.field_type({}, {'type': 'array', 'items': {'type': 'string'}}, True, {}), '[]string')
        self.assertEqual(generator.go_type({}, {'oneOf': [{'type': 'string'}, {'type': 'integer'}]}, {}), 'json.RawMessage')

    def test_generated_client_types_are_current(self):
        actual = generator.ts_module(self.public['components']['schemas'], 'contracts/agents-api/openapi.yaml')
        self.assertEqual(actual, generator.CLIENT_TYPES.read_bytes())

    def test_client_types_project_values_fields_and_unions(self):
        ref = '#/components/schemas/'
        generated = generator.ts_module({
            'Color': {'type': 'string', 'enum': ['red', 'blue']},
            'Paint': {'type': 'object', 'required': ['type', 'color'], 'properties': {
                'type': {'type': 'string', 'enum': ['paint']}, 'color': {'$ref': ref + 'Color'}, 'note': {'type': ['string', 'null']},
                'shade': {'allOf': [{'$ref': '#/definitions/Color'}], 'x-nullable': True},
            }},
            'Item': {'oneOf': [{'$ref': ref + 'Paint'}], 'discriminator': {'propertyName': 'type', 'mapping': {'paint': ref + 'Paint'}}},
        }, 'test').decode()
        for line in (
            'export const colorValues = ["red", "blue"] as const;', 'export type Color = (typeof colorValues)[number];',
            '  color: Color;', '  note?: string | null;', '  shade?: Color | null;',
            'export const paintFields = ["type", "color", "note", "shade"] as const;',
            'export const paintRequired = ["type", "color"] as const;', 'export type Item = Paint;',
            'export const itemFields = {\n  "paint": paintFields,\n} as const;',
        ):
            self.assertIn(line, generated)

    def test_every_public_reference_resolves_and_no_other_apis_leak(self):
        self.assertEqual({p.split('/')[1] for p in self.public['paths']}, {'agents', 'vaults', 'files', 'skills'})
        generator.prune_components(copy.deepcopy(self.public))
        self.assertEqual(self.public['security'], [{'ProjectKey': []}])


if __name__ == '__main__':
    unittest.main()
