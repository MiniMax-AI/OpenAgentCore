#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
image="${OAC_DEV_CORE_IMAGE:-oac-core:dev}"
if [[ "$runtime_root" != /* ]]; then
  printf 'OAC_DEV_HOME must be absolute: %s\n' "$runtime_root" >&2
  exit 1
fi
mkdir -p "$runtime_root/cache/oac-core-builds"
image_context="$(mktemp -d "$runtime_root/cache/oac-core-builds/image.XXXXXX")"
trap 'rm -rf "$image_context"' EXIT

# Reuse the source boundary; never send the repository or runtime keys to Docker.
GOOS=linux GOARCH=amd64 OAC_DEV_CORE_BUILD_DIR="$image_context" \
  "$repo_root/scripts/build-agents-api.sh"
E2B_PROVIDER_BUILD_DIR="$image_context/e2b-build" "$repo_root/scripts/build-e2b-provider.sh"
mkdir -p "$image_context/e2b"
tar -xzf "$image_context/e2b-build/oac-e2b-provider-linux-amd64.tar.gz" \
  --strip-components=1 -C "$image_context/e2b"
rm -rf "$image_context/e2b-build"
cp "$repo_root/services/agents-api/Dockerfile" "$image_context/Dockerfile"
docker build --platform linux/amd64 --tag "$image" "$image_context"
