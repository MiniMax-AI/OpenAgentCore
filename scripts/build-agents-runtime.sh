#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
output_dir="${AGENTS_RUNTIME_BUILD_DIR:-$runtime_root/build/agents-runtime}"
# Extract the official @openai/codex@0.153.4-linux-x64 npm package here.
package_dir="${AGENTS_RUNTIME_CODEX_PACKAGE:?Set AGENTS_RUNTIME_CODEX_PACKAGE to the extracted pinned platform package}"
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
for executable in "$native_dir/bin/codex"; do
  test -x "$executable" || { printf 'Missing executable: %s\n' "$executable" >&2; exit 1; }
done
test -d "$native_dir/codex-resources"
mkdir -p "$runtime_root/cache/oac-runtime-builds"
context="$(mktemp -d "$runtime_root/cache/oac-runtime-builds/bundle.XXXXXX")"
trap 'rm -rf "$context"' EXIT
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath \
    -o "$context/oac-daemon" ./apps/daemon/cmd/oac-daemon
)
cp "$native_dir/bin/codex" "$context/codex"
cp -R "$native_dir/codex-resources" "$context/codex-resources"
cp "$repo_root/services/core/deploy/codex/Dockerfile" "$context/Dockerfile"
# Preserve the previous bundle if compilation or validation failed.
mkdir -p "$output_dir"
cp -R "$context/." "$output_dir/"
printf 'Runtime image context: %s\n' "$output_dir"
printf 'Build with: docker build --platform linux/amd64 -t agents-runtime:local %q\n' "$output_dir"
