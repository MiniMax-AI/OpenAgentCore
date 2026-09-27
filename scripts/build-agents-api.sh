#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
output_dir="${OAC_DEV_CORE_BUILD_DIR:-$runtime_root/build/agents-api}"
for directory in "$runtime_root" "$output_dir"; do
  if [[ "$directory" != /* ]]; then
    printf 'Agents API build directories must be absolute: %s\n' "$directory" >&2
    exit 1
  fi
done

revision="${OAC_DEV_BUILD_REVISION:-$(git -C "$repo_root" rev-parse HEAD 2>/dev/null || true)}"
if [[ -n "$revision" && ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  printf 'Invalid Agents API source revision\n' >&2
  exit 1
fi

mkdir -p "$runtime_root/cache/agents-api-builds"
build_context="$(mktemp -d "$runtime_root/cache/agents-api-builds/source.XXXXXX")"
trap 'rm -rf "$build_context"' EXIT

# This is the release source boundary. Product and other application sources
# must remain physically absent, even when building from the full monorepo.
tar -C "$repo_root" -cf - \
  go.mod go.sum \
  contracts/agents-api/v1 \
  internal/agentdaemon/device internal/agentdaemon/gateway internal/agentdaemon/proto \
  internal/agentnetwork internal/agentbundle internal/agentcapabilities internal/agentplugin internal/agentskill internal/harnessconfig internal/obs/log services/agents-api \
  | tar -C "$build_context" -xf -

(
  cd "$build_context"
  export GOWORK=off CGO_ENABLED=0
  for command in server migrate device environment-key sandbox-node; do
    artifact="oac-core-$command"
    if [[ "$command" == server ]]; then artifact=oac-core; fi
    if [[ "$command" == sandbox-node ]]; then artifact=parsar-sandbox-node; fi
    go build -mod=readonly -trimpath -buildvcs=false -ldflags "-X main.buildRevision=$revision" \
      -o "$build_context/bin/$artifact" "./services/agents-api/cmd/$command"
  done
)

# Publish only after every command builds successfully.
mkdir -p "$output_dir"
for artifact in oac-core oac-core-migrate oac-core-device oac-core-environment-key parsar-sandbox-node; do
  mv -f "$build_context/bin/$artifact" "$output_dir/$artifact"
done
printf 'Standalone Agents API binaries: %s\n' "$output_dir"
