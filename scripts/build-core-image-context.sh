#!/usr/bin/env bash
set -euo pipefail

# Assemble the build context of the distribution's Core image in CONTEXT_DIR:
# the Core commands in bin/, the E2B helper in e2b/, an empty native-installers/
# and deploy/distribution/Dockerfile. build-core-distribution.sh adds the native
# installer catalog before it builds the image.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
context="${1:?Usage: build-core-image-context.sh CONTEXT_DIR}"
if [[ "$context" != /* ]]; then
  printf 'The Core image context directory must be absolute: %s\n' "$context" >&2
  exit 1
fi

mkdir -p "$context/bin" "$context/e2b" "$context/native-installers"
GOOS=linux GOARCH=amd64 OAC_DEV_CORE_BUILD_DIR="$context/bin" "$repo_root/scripts/build-core.sh"
E2B_PROVIDER_BUILD_DIR="$context/e2b-build" "$repo_root/scripts/build-e2b-provider.sh"
tar -xzf "$context/e2b-build/oac-e2b-provider-linux-amd64.tar.gz" --strip-components=1 -C "$context/e2b"
rm -rf "$context/e2b-build"
cp "$repo_root/deploy/distribution/Dockerfile" "$context/Dockerfile"
