#!/usr/bin/env bash
set -euo pipefail

runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
output_dir="${AGENTS_RUNTIME_BUILD_DIR:-$runtime_root/build/codex-runtime}"
# Extract the official @openai/codex@0.153.4-linux-x64 npm package here.
package_dir="${CODEX_CLI_DIR:?Set CODEX_CLI_DIR to the extracted pinned platform package}"
for directory in "$runtime_root" "$output_dir" "$package_dir"; do
  if [[ "$directory" != /* ]]; then
    printf 'Runtime build directories must be absolute: %s\n' "$directory" >&2
    exit 1
  fi
done
python3 - "$package_dir/package.json" <<'PY'
import json, sys
package = json.load(open(sys.argv[1]))
assert package['name'] == '@openai/codex' and package['version'] == '0.153.4-linux-x64', 'Expected pinned official Linux x64 package'
PY
native_dir="$package_dir/vendor/x86_64-unknown-linux-musl"
for executable in "$native_dir/bin/codex" "$native_dir/bin/codex-code-mode-host"; do
  test -x "$executable" || { printf 'Missing executable: %s\n' "$executable" >&2; exit 1; }
done
mkdir -p "$runtime_root/cache/oac-runtime-builds"
context="$(mktemp -d "$runtime_root/cache/oac-runtime-builds/codex.XXXXXX")"
trap 'rm -rf "$context"' EXIT
cp "$native_dir/bin/codex" "$native_dir/bin/codex-code-mode-host" "$context/"
# Preserve the previous payload if validation failed.
mkdir -p "$output_dir"
cp -R "$context/." "$output_dir/"
printf 'Codex Harness payload: %s\n' "$output_dir"
