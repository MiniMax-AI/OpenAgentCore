#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
output_dir="${AGENTS_RUNTIME_BUILD_DIR:-$runtime_root/build/codex-runtime}"
package_dir="${CODEX_HARNESS_BUILD_DIR:?Set CODEX_HARNESS_BUILD_DIR to the source-built pinned Codex artifact}"
for directory in "$runtime_root" "$output_dir" "$package_dir"; do
  if [[ "$directory" != /* ]]; then
    printf 'Runtime build directories must be absolute: %s\n' "$directory" >&2
    exit 1
  fi
done
python3 - "$package_dir" "$repo_root" <<'PY'
import hashlib, json, pathlib, sys
artifact, repository = map(pathlib.Path, sys.argv[1:])
package = repository / "packages/codex-runtime"
pin = json.loads((package / "source.json").read_text())
provenance = json.loads((artifact / "provenance.json").read_text())
catalog = json.loads((repository / "internal/harnessconfig/builtin/catalog.json").read_text())
version = next(entry["version"] for entry in catalog if entry["kind"] == "codex")
expected = {**pin, "version": version, "target": "x86_64-unknown-linux-musl",
            "patch_sha256": hashlib.sha256((package / "invalidate-mcp.patch").read_bytes()).hexdigest()}
assert all(provenance.get(key) == value for key, value in expected.items()), "Codex artifact does not match the pinned patched source"
assert set(provenance["files"]) == {"codex", "codex-code-mode-host"}, "Incomplete Codex artifact"
for name, digest in provenance["files"].items():
    assert hashlib.sha256((artifact / name).read_bytes()).hexdigest() == digest, "Codex artifact checksum mismatch: " + name
PY
for executable in "$package_dir/codex" "$package_dir/codex-code-mode-host"; do
  test -x "$executable" || { printf 'Missing executable: %s\n' "$executable" >&2; exit 1; }
done
mkdir -p "$runtime_root/cache/oac-runtime-builds"
context="$(mktemp -d "$runtime_root/cache/oac-runtime-builds/codex.XXXXXX")"
trap 'rm -rf "$context"' EXIT
cp "$package_dir/codex" "$package_dir/codex-code-mode-host" "$package_dir/provenance.json" "$context/"
# Preserve the previous payload if validation failed.
mkdir -p "$output_dir"
rm -f "$output_dir/package.json"
cp -R "$context/." "$output_dir/"
printf 'Codex Harness payload: %s\n' "$output_dir"
