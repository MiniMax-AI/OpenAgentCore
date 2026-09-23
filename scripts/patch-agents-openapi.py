#!/usr/bin/env python3
"""Apply schema constraints that swag cannot express on response fields."""

from pathlib import Path
import sys


PROVIDER_TYPE_SCHEMA = """      provider_type:
        type: string
"""
BOUNDED_PROVIDER_TYPE_SCHEMA = """      provider_type:
        pattern: '^[a-z][a-z0-9_]{0,31}$'
        type: string
"""


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: patch-agents-openapi.py OPENAPI_YAML")

    path = Path(sys.argv[1])
    document = path.read_text(encoding="utf-8")
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
