#!/usr/bin/env python3
"""Verify catalog extension and generated contract freshness without live models."""

import importlib.util
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
    def test_new_declaration_reaches_all_projections(self):
        harnesses = [{"kind": "example", "label": "Example", "package": "example", "component": "example", "version": "1.0.0"}]
        generated = {**catalog.render_registrations(["example"]), **catalog.render_docs(harnesses)}
        generated["client"] = catalog.render_client(harnesses, ["responses"])
        for content in generated.values():
            self.assertIn("example", content)
            for old in ("codex", "claude_sdk", "mcode"):
                self.assertNotIn(old, content)
        self.assertIn("harnessconfig.NewRegistry(example.Declaration)", generated[Path("internal/harnessconfig/builtin/registry.go")])
        self.assertIn("[]agent.Declaration{example.Declaration}", generated[Path("apps/daemon/internal/cli/harness_declarations.go")])

    def test_declaration_directory_is_the_registration(self):
        self.assertEqual(catalog.packages(), ["claudesdk", "codex", "mcode"])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "internal/harnessconfig/bad").mkdir(parents=True)
            (root / "internal/harnessconfig/bad/declaration.go").write_text("package bad\n")
            with self.assertRaises(ValueError):
                catalog.packages(root)

    def test_checked_in_openapi_enums_match_catalog(self):
        expected = [entry["kind"] for entry in catalog.load_declared()["harnesses"]]
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
                    if surface == "openapi.yaml":
                        actual = json.loads(text)["components"]["schemas"][definition]["properties"][member]["enum"]
                        self.assertEqual(actual, expected)
                        continue
                    body = re.search(r"^  " + re.escape(definition) + r":\n(.*?)(?=^  [^ ]|\Z)", text, re.M | re.S)
                    self.assertIsNotNone(body, "missing schema; run make openapi")
                    field = re.search(r"^      " + member + r":\n(.*?)(?=^      [^ ]|^    [^ ]|\Z)", body.group(1), re.M | re.S)
                    self.assertIsNotNone(field)
                    self.assertEqual(re.findall(r"^ +- ([a-z][a-z0-9_]*)$", field.group(1), re.M), expected,
                                     "stale Harness enum; run make openapi")

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
        kinds = catalog.load_declared()["harnesses"]
        for entry in kinds:
            self.assertEqual(result.count("        - " + entry["kind"] + "\n"), 8)
        self.assertIn("type: string", result)
        with self.assertRaises(ValueError):
            schema.harness_enums(document.replace("name: harness", "name: other"))


if __name__ == "__main__":
    unittest.main()
