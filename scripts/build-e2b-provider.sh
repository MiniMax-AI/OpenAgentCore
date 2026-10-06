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
archive=oac-e2b-provider-linux-amd64.tar.gz
# The helper is a pure function of these files, so a build is reused by their hash.
inputs="$(cd "$repo_root" && {
  find services/core/tools/e2b-provider -type f ! -path '*/__pycache__/*' -print0 | sort -z | xargs -0 sha256sum
  sha256sum LICENSE scripts/build-e2b-provider.sh
} | sha256sum | cut -c1-64)"
cache="${OAC_DEV_HOME:-$HOME/.oac}/cache/e2b-provider/$inputs"
if [[ ! -f "$cache/$archive.sha256" ]]; then
  mkdir -p "${cache%/*}"
  build="$(mktemp -d "${cache%/*}/.build.XXXXXX")"
  image="oac-e2b-provider-build:${inputs:0:12}"
  # Proxy values are build-only operator settings; no account key is needed.
  docker build --platform linux/amd64 --build-arg HTTP_PROXY --build-arg HTTPS_PROXY \
    --build-arg ALL_PROXY --build-arg NO_PROXY \
    --file "$repo_root/services/core/tools/e2b-provider/Build.Dockerfile" \
    --tag "$image" "$repo_root/services/core/tools/e2b-provider"
  docker run --rm --platform linux/amd64 \
    --env HTTP_PROXY --env HTTPS_PROXY --env ALL_PROXY --env NO_PROXY \
    --mount "type=bind,src=$repo_root,dst=/source,readonly" \
    --mount "type=bind,src=$build,dst=/output" "$image"
  mv -T "$build" "$cache" 2>/dev/null || rm -rf "$build"
fi
(cd "$cache" && sha256sum --check --quiet "$archive.sha256")
cp "$cache/$archive" "$cache/$archive.sha256" "$output_dir/"
