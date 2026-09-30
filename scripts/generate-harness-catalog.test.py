#!/usr/bin/env python3
"""Verify catalog extension and generated contract freshness without live models."""

import importlib.util
import copy
import json
from pathlib import Path
import tempfile
import re
import unittest


def module(name, file):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(file))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


catalog = module("catalog", "generate-harness-catalog.py")
schema = module("schema", "patch-agents-openapi.py")


class HarnessCatalogTests(unittest.TestCase):
    def test_installer_schema_membership_and_defaults(self):
        entries = catalog.load_catalog(catalog.ROOT / catalog.CATALOG)
        document = json.loads((catalog.ROOT / "deploy/install/config.schema.json").read_text())
        catalog.check_installer_schema(entries, document)
        for key, field, value in (
            ("harnesses", "items", {"enum": ["unknown"]}),
            ("default_harness", "enum", ["unknown"]),
            ("harnesses", "default", ["unknown"]),
            ("harnesses", "default", []),
            ("harnesses", "default", ["codex", "codex"]),
            ("harnesses", "default", ["claude_sdk"]),
            ("default_harness", "default", "unknown"),
        ):
            changed = copy.deepcopy(document)
            changed["properties"]["core"]["properties"][key][field] = value
            with self.subTest(key=key, field=field, value=value), self.assertRaises(ValueError):
                catalog.check_installer_schema(entries, changed)
        extended = [*entries, {"kind": "additional"}]
        with self.assertRaises(ValueError):
            catalog.check_installer_schema(extended, document)

    def test_installer_projection_includes_every_declaration(self):
        providers = {"additional": {"responses": {"requires_token_limits": True},
                                    "anthropic": {"requires_token_limits": False}}, "native": {}}
        namespace = {}
        exec(catalog.render_installer(providers), namespace)
        self.assertEqual(namespace["PROVIDERS"], providers)

    def test_new_registration_reaches_all_projections(self):
        entries = [{"kind": "example", "label": "Example",
                    "configuration": "example", "profile": "exampleProfile"}]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "catalog.json"
            path.write_text(json.dumps(entries))
            generated = catalog.render(catalog.load_catalog(path))
        for content in generated.values():
            self.assertIn("example", content)
            for old in ("codex", "claude_sdk", "mcode"):
                self.assertNotIn(old, content)
        self.assertIn('"example": example.Configuration()', generated[Path("internal/harnessconfig/builtin/registry.go")])
        self.assertIn('"example": exampleProfile()', generated[Path("services/core/internal/engine/catalog_generated.go")])

    def test_checked_in_openapi_enums_match_catalog(self):
        expected = [entry["kind"] for entry in catalog.load_catalog(catalog.ROOT / catalog.CATALOG)]
        surfaces = {
            "openapi.yaml": [("v1.AgentsCore", "harness"), ("v1.SavedAgentCoreInput", "harness"),
                             ("v1.SavedAgentCore", "harness")],
            "core.openapi.yaml": [("api.CoreHarness", "id"), ("api.HarnessModelConfiguration", "harness"),
                                  ("v1.AgentsCore", "harness"), ("v1.SavedAgentCore", "harness")],
        }
        for surface, fields in surfaces.items():
            text = (catalog.ROOT / "contracts/agents-api" / surface).read_text()
            for definition, member in fields:
                with self.subTest(surface=surface, definition=definition):
                    body = re.search(r"^  " + re.escape(definition) + r":\n(.*?)(?=^  [^ ]|\Z)", text, re.M | re.S)
                    self.assertIsNotNone(body, "missing schema; run make openapi")
                    field = re.search(r"^      " + member + r":\n(.*?)(?=^      [^ ]|^    [^ ]|\Z)", body.group(1), re.M | re.S)
                    self.assertIsNotNone(field)
                    self.assertEqual(re.findall(r"^ +- ([a-z][a-z0-9_]*)$", field.group(1), re.M), expected,
                                     "stale Harness enum; run make openapi")

    def test_invalid_or_duplicate_registration_is_rejected(self):
        entry = {"kind": "example", "label": "Example",
                 "configuration": "example", "profile": "exampleProfile"}
        candidates = [[], [entry, entry], [{**entry, "kind": "bad/kind"}],
                      [{**entry, "configuration": "../example"}],
                      [{**entry, "profile": "bad()"}],
                      [{**entry, "unknown": True}]]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "catalog.json"
            for value in candidates:
                path.write_text(json.dumps(value))
                with self.subTest(value=value), self.assertRaises(ValueError):
                    catalog.load_catalog(path)

    def test_openapi_projection_keeps_every_harness_constraint(self):
        document = "definitions:\n"
        fields = [("v1.AgentsCore", "harness"), ("v1.SavedAgentCoreInput", "harness"),
                  ("v1.SavedAgentCore", "harness"), ("api.CoreHarness", "id"),
                  ("api.HarnessModelConfiguration", "harness")]
        for definition, field in fields:
            document += f"  {definition}:\n    properties:\n      {field}:\n        type: string\n"
        document += "paths:\n"
        for method in ("get", "put", "delete"):
            document += f"  /{method}:\n    parameters:\n      - description: Harness\n        in: path\n        name: harness\n        type: string\n"
        result = schema.harness_enums(document)
        kinds = catalog.load_catalog(catalog.ROOT / catalog.CATALOG)
        for entry in kinds:
            self.assertEqual(result.count("        - " + entry["kind"] + "\n"), 8)
        self.assertIn("type: string", result)
        with self.assertRaises(ValueError):
            schema.harness_enums(document.replace("name: harness", "name: other"))


if __name__ == "__main__":
    unittest.main()
