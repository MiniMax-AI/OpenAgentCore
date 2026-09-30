#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
output_dir="${AGENTS_RUNTIME_BUILD_DIR:-$runtime_root/build/claude-runtime}"
sdk_dir="${CLAUDE_SDK_BUILD_DIR:-$runtime_root/build/claude-sdk-runtime}"
for directory in "$runtime_root" "$output_dir" "$sdk_dir"; do
  [[ "$directory" == /* ]] || { printf 'Expected absolute build directory: %s\n' "$directory" >&2; exit 1; }
done
mkdir -p "$runtime_root/cache/oac-runtime-builds"
context="$(mktemp -d "$runtime_root/cache/oac-runtime-builds/claude.XXXXXX")"
trap 'rm -rf "$context"' EXIT
archive=claude-sdk-runtime-linux-x64-glibc.tar.gz
(cd "$sdk_dir" && sha256sum -c "$archive.sha256")
mkdir "$context/claude-sdk"
tar -xzf "$sdk_dir/$archive" -C "$context/claude-sdk"
node "$repo_root/scripts/check-claude-sdk-runtime.mjs" "$context/claude-sdk"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath \
    -o "$context/oac-daemon" ./apps/daemon/cmd/oac-daemon
)
cp "$repo_root/services/core/deploy/claude/Dockerfile" "$context/Dockerfile"
mkdir -p "$output_dir"
cp -R "$context/." "$output_dir/"
printf 'Claude Runtime image context: %s\n' "$output_dir"
