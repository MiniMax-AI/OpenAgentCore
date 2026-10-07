#!/usr/bin/env bash
set -euo pipefail

# The images' files must stay readable by Session uids and the sandbox's user.
umask 022

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
agent_host_image="${OAC_AGENT_HOST_IMAGE:-oac-agent-host:dev}"
sandbox_image="${OAC_SANDBOX_IMAGE:-oac-sandbox:dev}"
[[ "$runtime_root" == /* ]] || { printf 'Expected absolute build directory: %s\n' "$runtime_root" >&2; exit 1; }
mkdir -p "$runtime_root/cache/oac-runtime-builds"
context="$(mktemp -d "$runtime_root/cache/oac-runtime-builds/agent-host.XXXXXX")"
trap 'rm -rf "$context"' EXIT
# The Runtime image builders check their pinned inputs and prepare each Harness's payload.
AGENTS_RUNTIME_BUILD_DIR="$context/codex" bash "$repo_root/scripts/build-codex-runtime.sh"
AGENTS_RUNTIME_BUILD_DIR="$context/claude" bash "$repo_root/scripts/build-claude-runtime.sh"
AGENTS_RUNTIME_BUILD_DIR="$context/mcode" bash "$repo_root/scripts/build-mcode-runtime.sh"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath -o "$context/" \
    ./apps/daemon/cmd/oac-daemon ./apps/daemon/cmd/oac-process-shim ./apps/sandboxio/cmd/oac-sandbox-io
)
# Further arguments, such as --label or --network, go to both builds.
for target in sandbox agent-host; do
  image="$sandbox_image"
  if [[ "$target" == agent-host ]]; then image="$agent_host_image"; fi
  docker build --platform linux/amd64 --target "$target" --tag "$image" \
    --file "$repo_root/deploy/distribution/AgentHost.Dockerfile" "$@" "$context"
done
printf 'Agent-host image: %s\nSandbox image: %s\n' "$agent_host_image" "$sandbox_image"
