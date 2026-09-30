#!/usr/bin/env bash
set -euo pipefail
umask 022
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${E2B_PROVIDER_BUILD_DIR:-${OAC_DEV_HOME:-$HOME/.oac}/build/e2b-provider}"
case "$output_dir" in
  /*) ;;
  *) printf 'E2B_PROVIDER_BUILD_DIR must be absolute\n' >&2; exit 1 ;;
esac
mkdir -p "$output_dir"
source_revision="${E2B_SOURCE_REVISION:-$(git -C "$repo_root" rev-parse HEAD)}"
image="oac-e2b-provider-build:${source_revision:0:12}"
# Proxy values are build-only operator settings; no account key is needed.
docker build --platform linux/amd64 --build-arg HTTP_PROXY --build-arg HTTPS_PROXY \
  --build-arg ALL_PROXY --build-arg NO_PROXY \
  --file "$repo_root/services/core/tools/e2b-provider/Build.Dockerfile" \
  --tag "$image" "$repo_root/services/core/tools/e2b-provider"
docker run --rm --platform linux/amd64 \
  --env HTTP_PROXY --env HTTPS_PROXY --env ALL_PROXY --env NO_PROXY \
  --env "E2B_SOURCE_REVISION=$source_revision" \
  --mount "type=bind,src=$repo_root,dst=/source,readonly" \
  --mount "type=bind,src=$output_dir,dst=/output" "$image"
