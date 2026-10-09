#!/usr/bin/env python3
"""Check the pinned native build input without modifying it or running a model."""

import hashlib
import json
import pathlib
import re
import subprocess
import sys


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: check-source.py UPSTREAM_SOURCE_DIRECTORY")
    package = pathlib.Path(__file__).resolve().parent
    source = pathlib.Path(sys.argv[1]).resolve()
    pin = json.loads((package / "source.json").read_text())
    catalog = json.loads((package.parents[1] / "internal/harnessconfig/builtin/catalog.json").read_text())
    version = next(item["version"] for item in catalog if item["kind"] == "codex")

    def git(*args):
        return subprocess.check_output(["git", "-C", str(source), *args], text=True).strip()

    if git("rev-parse", "HEAD") != pin["revision"]:
        raise SystemExit("Codex source revision does not match source.json")
    if git("status", "--porcelain", "--untracked-files=no"):
        raise SystemExit("Codex source has tracked modifications")
    manifest = (source / "codex-rs/Cargo.toml").read_text()
    package_section = re.search(r"(?ms)^\[workspace\.package\]\s*\n(.*?)(?=^\[|\Z)", manifest)
    source_version = re.search(r'^version = "([^"]+)"$', package_section[1], re.M) if package_section else None
    if source_version is None or source_version[1] != version:
        raise SystemExit("Codex source version does not match the Harness catalog")
    patch = package / "invalidate-mcp.patch"
    subprocess.run(["git", "-C", str(source), "apply", "--check", str(patch)], check=True)
    print(json.dumps({"repository": pin["repository"], "revision": pin["revision"], "version": version,
                      "patch_sha256": hashlib.sha256(patch.read_bytes()).hexdigest()}, sort_keys=True))


if __name__ == "__main__":
    main()
