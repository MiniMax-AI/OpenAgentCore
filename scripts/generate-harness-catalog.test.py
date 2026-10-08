#!/usr/bin/env python3
"""Verify catalog extension and generated contract freshness without live models."""

import importlib.util
import json
from pathlib import Path
import tempfile
import re
import unittest
from unittest.mock import patch


def module(name, file):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(file))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


catalog = module("catalog", "generate-harness-catalog.py")
schema = module("schema", "patch-agents-openapi.py")


class HarnessCatalogTests(unittest.TestCase):
    def test_installer_projection_includes_every_declaration(self):
        providers = {"additional": {"responses": {"requires_token_limits": True},
                                    "anthropic": {"requires_token_limits": False}}, "native": {}}
        namespace = {}
        exec(catalog.render_installer(providers), namespace)
        self.assertEqual(namespace["PROVIDERS"], providers)

    def test_new_registration_reaches_all_projections(self):
        entries = [{"kind": "example", "label": "Example", "configuration": "example", "version": "1.2.3"}]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "catalog.json"
            path.write_text(json.dumps(entries))
            loaded = catalog.load_catalog(path)
            generated = catalog.render(loaded)
        generated["client"] = catalog.render_client(loaded, ["responses"])
        for content in generated.values():
            self.assertIn("example", content)
            for old in ("codex", "claude_sdk", "mcode"):
                self.assertNotIn(old, content)
        self.assertIn('"example": example.Configuration()', generated[Path("internal/harnessconfig/builtin/registry.go")])
        cli = generated[Path("apps/daemon/internal/cli/harness_catalog_linux.go")]
        self.assertIn("example.Declaration", cli)
        self.assertIn("example.Installation()", cli)
        self.assertTrue(cli.startswith("//go:build linux\n"))
        self.assertIn('const NativeVersion = "1.2.3"', generated[Path("internal/harnessconfig/example/version.go")])

    def test_manifest_projection_preserves_other_owners_and_detects_stale_pins(self):
        entries = catalog.load_catalog(catalog.ROOT / catalog.CATALOG)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            originals = {}
            for kind, relative in (("claude_sdk", "packages/claude-sdk-adapter/package.json"),
                                   ("mcode", "packages/mcode-harness/source.json")):
                original = json.loads((catalog.ROOT / relative).read_text())
                originals[relative] = original
                stale = json.loads(json.dumps(original))
                if kind == "claude_sdk":
                    stale["dependencies"]["@anthropic-ai/claude-agent-sdk"] = "0.0.1"
                else:
                    stale["version"] = "0.0.1"
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(json.dumps(stale, indent=2) + "\n")
            projected = catalog.render_manifests(entries, root)
            for relative, content in projected.items():
                expected = originals[str(relative)]
                self.assertEqual(json.loads(content), expected)
                self.assertNotEqual(content, (root / relative).read_text())
            # Check mode rejects these stale manifests before invoking Go.
            catalog_path = root / catalog.CATALOG
            catalog_path.parent.mkdir(parents=True)
            catalog_path.write_text(json.dumps(entries))
            with patch.object(catalog, "ROOT", root), patch.object(catalog, "render", return_value={}), \
                    patch.object(catalog.subprocess, "run") as run, patch("sys.argv", ["catalog", "--check"]):
                with self.assertRaisesRegex(SystemExit, "package.json.*|source.json"):
                    catalog.main()
                run.assert_not_called()
            for relative, content in projected.items():
                (root / relative).write_text(content)
            self.assertEqual(projected, catalog.render_manifests(entries, root))

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

    def test_invalid_or_duplicate_registration_is_rejected(self):
        entry = {"kind": "example", "label": "Example", "configuration": "example", "version": "1.2.3"}
        candidates = [[], [entry, entry], [entry, {**entry, "kind": "another"}], [{**entry, "kind": "bad/kind"}],
                      [{**entry, "configuration": "../example"}],
                      [{**entry, "unknown": True}], [{**entry, "version": "^1.2.3"}],
                      [{**entry, "version": "1.2.3-linux-x64"}], [{**entry, "version": 123}]]
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
