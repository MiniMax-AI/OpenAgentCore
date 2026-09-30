#!/usr/bin/env python3
"""Audit the initial source import against its recorded original hashes.

This is an import acceptance command, not a gate on future Core development.
"""
import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parent.parent
manifest = json.loads((root / "provenance/source.json").read_text())
adaptations = {
    "go.mod": "Remove unused product dependencies without changing module identity.",
    "go.sum": "Regenerate checksums for the standalone dependency closure.",
    "pnpm-lock.yaml": "Keep only the standalone Claude adapter workspace and its dependencies.",
    "services/agents-api/RELEASE.md": "Resolve new release revisions in parsar-core.",
    "services/agents-api/HOSTED-RELEASE.md": "Resolve new release revisions in parsar-core.",
}
# Records keep their upstream paths; these copied roots now live under new names.
moved = {
    "apps/parsar-daemon/cmd/parsar-daemon/": "apps/daemon/cmd/oac-daemon/",
    "apps/parsar-daemon/": "apps/daemon/",
    "services/agents-api/": "services/core/",
}


def destination(name):
    for source, target in moved.items():
        if name.startswith(source):
            return target + name[len(source):]
    return name


errors = []
unchanged = 0
for name, expected in manifest["files"].items():
    path = root / destination(name)
    if not path.is_file():
        errors.append("missing: " + name)
    elif hashlib.sha256(path.read_bytes()).hexdigest() == expected:
        unchanged += 1
    elif name not in adaptations:
        errors.append("unexpected modification: " + name)
for name in ("server", "apps/web", "apps/parsar", "packages/cli", "packages/opencode-plugin", "infra"):
    if (root / name).exists():
        errors.append("product tree included: " + name)
if errors:
    raise SystemExit("\n".join(errors))
print(f"Source import verified: {len(manifest['files'])} files, {unchanged} unchanged")
print("Permitted packaging adaptations: " + ", ".join(adaptations))
