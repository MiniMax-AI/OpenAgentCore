#!/usr/bin/env python3
"""Check generated Core queries against their current checked-out bytes."""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parent.parent
generated = root / "services/core/internal/db/sqlc"


def snapshot():
    return {str(p.relative_to(generated)): p.read_bytes() for p in generated.rglob("*") if p.is_file()}


before = snapshot()
subprocess.run(["make", "sqlc-generate"], cwd=root, check=True)
if snapshot() != before:
    raise SystemExit("Core sqlc generated files are out of date; commit make sqlc-generate output")
