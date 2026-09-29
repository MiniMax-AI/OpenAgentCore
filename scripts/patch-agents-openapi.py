#!/usr/bin/env python3
"""Apply schema constraints that swag cannot express on response fields."""

from pathlib import Path
import sys
import importlib.util
import re


PROVIDER_TYPE_SCHEMA = """      provider_type:
        type: string
"""
BOUNDED_PROVIDER_TYPE_SCHEMA = """      provider_type:
        pattern: '^[a-z][a-z0-9_]{0,31}$'
        type: string
"""



def harness_enums(document):
    # Read the authoring catalog, never another independently maintained enum.
    spec = importlib.util.spec_from_file_location(
        "harness_catalog", Path(__file__).with_name("generate-harness-catalog.py"))
    catalog = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(catalog)
    kinds = [entry["kind"] for entry in catalog.load_catalog(catalog.ROOT / catalog.CATALOG)]
    for definition, member in [
        ("v1.AgentsCore", "harness"),
        ("v1.SavedAgentCoreInput", "harness"),
        ("v1.SavedAgentCore", "harness"),
        ("api.CoreHarness", "id"),
        ("api.HarnessModelConfiguration", "harness"),
    ]:
        pattern = re.compile(r"(^  " + re.escape(definition) + r":\n)(.*?)(?=^  [^ ]|\Z)", re.M | re.S)
        match = pattern.search(document)
        if match is None:
            raise ValueError("Missing Harness schema: " + definition)
        body = match.group(2)
        marker = "      " + member + ":\n"
        if body.count(marker) != 1:
            raise ValueError("Missing Harness field: " + definition + "." + member)
        values = "        enum:\n" + "".join("        - " + kind + "\n" for kind in kinds)
        replacement = match.group(1) + body.replace(marker, marker + values)
        document = document[:match.start()] + replacement + document[match.end():]

    def parameter(match):
        indent = match.group(1)
        return (indent + "enum:\n" + "".join(indent + "- " + kind + "\n" for kind in kinds)
                + match.group(0))

    document, count = re.subn(
        r"^( +)in: path\n\1name: harness\n", parameter, document, flags=re.M)
    if count != 3:
        raise ValueError("Expected the three Harness model-configuration path parameters")
    return document


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: patch-agents-openapi.py OPENAPI_YAML")

    path = Path(sys.argv[1])
    document = harness_enums(path.read_text(encoding="utf-8"))
    series_start = "  v1.RuntimeHistorySeries:\n"
    series_end = "\n  v1.RuntimeHistoryTime:\n"
    if document.count(series_start) != 1 or document.count(series_end) != 1:
        raise SystemExit("expected exactly one Runtime history series schema")
    before, remainder = document.split(series_start)
    series, after = remainder.split(series_end)
    if series.count(PROVIDER_TYPE_SCHEMA) != 1:
        raise SystemExit("expected exactly one Runtime history provider_type field")
    path.write_text(
        before
        + series_start
        + series.replace(PROVIDER_TYPE_SCHEMA, BOUNDED_PROVIDER_TYPE_SCHEMA)
        + series_end
        + after,
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
