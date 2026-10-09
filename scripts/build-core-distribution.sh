#!/usr/bin/env bash
set -euo pipefail

# Distribution payloads must remain readable by the non-root service users.
umask 022

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
output_dir="${CORE_DISTRIBUTION_BUILD_DIR:-$runtime_root/build/core-distribution}"
release_base_url="${CORE_DISTRIBUTION_RELEASE_BASE_URL:-}"
offline="${CORE_DISTRIBUTION_OFFLINE:-0}"
if [[ "$offline" != 0 && "$offline" != 1 ]]; then
  printf 'CORE_DISTRIBUTION_OFFLINE must be 0 or 1\n' >&2
  exit 1
fi
if [[ -z "$release_base_url" && "$offline" != 1 ]]; then
  printf 'Set CORE_DISTRIBUTION_RELEASE_BASE_URL, or explicitly select CORE_DISTRIBUTION_OFFLINE=1\n' >&2
  exit 1
fi
python3 "$repo_root/scripts/core-distribution-manifest.py" release-base "$release_base_url"
export GOCACHE="${GOCACHE:-$runtime_root/cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$runtime_root/cache/go-mod}"
export GOOS=linux GOARCH=amd64 GOAMD64=v1 GOTOOLCHAIN=local
export GOFLAGS=-buildvcs=false
python3 - "$HOME/.oac" "$runtime_root" "$output_dir" "$GOCACHE" "$GOMODCACHE" <<'PY'
import pathlib, sys
if sys.version_info < (3, 9):
    sys.exit("Distribution builds require Python 3.9 or newer")
base = pathlib.Path(sys.argv[1]).resolve()
for value in sys.argv[2:]:
    path = pathlib.Path(value)
    if not path.is_absolute() or not path.resolve().is_relative_to(base):
        sys.exit("Distribution build directories must be absolute and under ~/.oac")
PY
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  printf 'Build the distribution on Linux x86_64 with a glibc compatible with Debian 12\n' >&2
  exit 1
fi
for command in docker go node pnpm python3 curl tar sha256sum pigz; do
  command -v "$command" >/dev/null
done
build_network="${CORE_DISTRIBUTION_BUILD_NETWORK:-default}"
case "$build_network" in
  default|host|none) ;;
  *) printf 'CORE_DISTRIBUTION_BUILD_NETWORK must be default, host, or none\n' >&2; exit 1 ;;
esac
# Build one selected Linux architecture image and write the ID the local image store gives it to
# $stage/NAME.id. BuildKit's --iidfile reports the config digest, which is the
# image ID only in Docker's classic store; the containerd store (Docker 29's
# default) uses the manifest digest and cannot resolve the config digest.
# built-image verifies the local identity from the digests the exporter reports.
# Without provenance attestations each image is one platform manifest in both
# stores, as the archive verifier requires.
build_image() {
  local name="$1"
  shift
  # Docker's predefined proxy arguments are build-only; no Dockerfile ARG or ENV
  # declaration persists the operator's network configuration in the images.
  # The metadata file omits build provenance, so it does not record them either.
  BUILDX_METADATA_PROVENANCE=disabled docker build --network "$build_network" \
    --platform "linux/$GOARCH" --provenance=false --metadata-file "$stage/$name.build.json" \
    --label "org.opencontainers.image.revision=$revision" \
    --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg ALL_PROXY --build-arg NO_PROXY \
    --build-arg "http_proxy=${http_proxy:-${HTTP_PROXY:-}}" \
    --build-arg "https_proxy=${https_proxy:-${HTTPS_PROXY:-}}" \
    --build-arg "all_proxy=${all_proxy:-${ALL_PROXY:-}}" \
    --build-arg "no_proxy=${no_proxy:-${NO_PROXY:-}}" "$@"
  python3 scripts/core-distribution-manifest.py built-image "$stage/$name.build.json" "$GOARCH" > "$stage/$name.id"
}

require_clean_source() {
  if [[ -n "$(git -C "$repo_root" status --porcelain --untracked-files=all)" ]]; then
    printf 'Distribution builds require clean, committed source\n' >&2
    exit 1
  fi
}
require_clean_source
revision="$(git -C "$repo_root" rev-parse HEAD)"
source_tree="$(git -C "$repo_root" rev-parse "$revision^{tree}")"
source_epoch="$(git -C "$repo_root" show -s --format=%ct "$revision")"
mkdir -p "$output_dir"
stage="$(mktemp -d "$output_dir/.build.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
source_dir="$stage/source"
bundle="$stage/oac-$revision-linux-amd64"
mkdir -p "$source_dir" "$bundle/images" "$stage/core/bin" "$stage/core/microsandbox" "$stage/web" "$stage/tmp"
export GOTMPDIR="$stage/tmp"
git -C "$repo_root" archive --format=tar.gz --output="$bundle/source.tar.gz" "$revision"
tar -xzf "$bundle/source.tar.gz" -C "$source_dir"
cd "$source_dir"
required_go="$(awk '$1 == "go" { print "go" $2; exit }' go.mod)"
if [[ "$(go env GOVERSION)" != "$required_go" ]]; then
  printf 'Distribution build requires %s\n' "$required_go" >&2
  exit 1
fi
go run ./services/core/cmd/provider-artifacts
for file in install_display.py node_output.py node_install.py provider_assets.py node_spec.py node_generations.py node_payload.py distribution.py; do
  cp "deploy/node/$file" "$bundle/$file"
done
python3 scripts/core-distribution-manifest.py bootstraps "$bundle" "$source_epoch" "$revision"
# The bundled docs (BUNDLED_DOCS); links that leave them point at this commit on GitHub.
python3 scripts/core-distribution-manifest.py docs . "$bundle" "$revision"
mkdir -p "$bundle/runtime"
cp deploy/distribution/seccomp.json "$bundle/runtime/"
cp LICENSE "$bundle/"

OAC_DEV_BUILD_REVISION="$revision" scripts/build-core-image-context.sh "$stage/core"
(
  cd services/core/tools/microsandbox-provider
  GOWORK=off CGO_ENABLED=1 go build -mod=readonly -trimpath \
    -o "$stage/core/bin/oac-microsandbox-provider" .
)
msb_archive="${CORE_DISTRIBUTION_MICROSANDBOX_ARCHIVE:-$runtime_root/cache/microsandbox-v0.7.2-linux-x86_64.tar.gz}"
if [[ ! -f "$msb_archive" ]]; then
  mkdir -p "$(dirname "$msb_archive")"
  curl --fail --location --proto '=https' --tlsv1.2 \
    https://github.com/superradcompany/microsandbox/releases/download/v0.7.2/microsandbox-linux-x86_64.tar.gz \
    --output "$stage/microsandbox.download"
  python3 scripts/core-distribution-manifest.py extract-runtime "$stage/microsandbox.download" "$stage/core/microsandbox"
  mv "$stage/microsandbox.download" "$msb_archive"
else
  python3 scripts/core-distribution-manifest.py extract-runtime "$msb_archive" "$stage/core/microsandbox"
fi
mkdir -p "$bundle/native/bin" "$bundle/native/microsandbox"
cp "$stage/core/bin/oac-node" "$stage/core/bin/oac-microsandbox-provider" "$bundle/native/bin/"
cp -R "$stage/core/microsandbox/." "$bundle/native/microsandbox/"
if [[ -n "${OAC_NATIVE_INSTALLER_BUILD_DIR:-}" ]]; then
  python3 scripts/core-distribution-manifest.py native-catalog "$bundle" "$stage" "$revision" \
    "$OAC_NATIVE_INSTALLER_BUILD_DIR" "$release_base_url"
fi
build_image core "$stage/core"
core_image="$(cat "$stage/core.id")"
# Fail at packaging time if the helper or runtime requires unavailable host libraries.
docker run --rm --network none --entrypoint /bin/sh \
  --mount "type=bind,src=$stage/core/microsandbox,dst=/opt/microsandbox,readonly" \
  --mount "type=bind,src=$stage/core/bin/oac-microsandbox-provider,dst=/opt/provider,readonly" \
  "$core_image" -ec \
  'for p in /opt/provider /opt/microsandbox/msb /opt/microsandbox/libkrunfw.so.5.6.1; do ! ldd "$p" | grep "not found"; done; /opt/microsandbox/msb --version'

OAC_DEV_WEB_BUILD_DIR="$stage/web" scripts/build-web.sh
pnpm --filter @oac/web... install --frozen-lockfile
pnpm build:web
cp -R apps/web/dist "$stage/web/dist"
cp services/web/Dockerfile "$stage/web/Dockerfile"
build_image web "$stage/web"

CGO_ENABLED=0 go build -mod=readonly -trimpath -o "$stage/" \
  ./apps/daemon/cmd/oac-daemon ./apps/daemon/cmd/oac-process-shim ./apps/sandboxio/cmd/oac-sandbox-io
: "${CODEX_HARNESS_BUILD_DIR:?Set the existing source-built pinned Codex artifact directory}"
: "${MCODE_HARNESS_BUILD_DIR:?Set the existing built pinned MiniMax Code companion directory}"
export CLAUDE_SDK_BUILD_DIR="$stage/claude-sdk"
scripts/build-claude-sdk-runtime.sh
# Share the agent-host build context with the local qualification builder.
for harness in codex claude mcode; do
  AGENTS_RUNTIME_BUILD_DIR="$stage/agent-host/$harness" bash "scripts/build-$harness-runtime.sh"
done
cp "$stage/oac-daemon" "$stage/oac-process-shim" "$stage/oac-sandbox-io" "$stage/agent-host/"
build_image agent-host --target agent-host --file deploy/distribution/AgentHost.Dockerfile "$stage/agent-host"
build_image runtime --target sandbox --file deploy/distribution/AgentHost.Dockerfile "$stage/agent-host"
python3 scripts/core-distribution-manifest.py verify-runtime "$(cat "$stage/runtime.id")" "$stage/oac-sandbox-io"

# Pin the linux/amd64 platform manifest, not the multi-platform tag: the
# containerd store keeps a pulled tag's whole index, whose export holds every
# platform. A platform manifest stays one image in both stores.
database_image="${CORE_DISTRIBUTION_DATABASE_IMAGE:-postgres:16-alpine@sha256:1a66d744c1b459e13b05a8fca341da84cb63383e99ce262210efee5a319d4551}"
if [[ ! "$database_image" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  docker pull --platform linux/amd64 "$database_image"
fi
docker image inspect --format '{{.Id}}' "$database_image" > "$stage/database.id"
docker run --rm --network none --entrypoint postgres "$(cat "$stage/database.id")" --version \
  | python3 -c 'import sys; value=sys.stdin.read(); assert value.startswith("postgres (PostgreSQL) 16."), "Distribution requires PostgreSQL 16"'
for name in core web runtime database agent-host; do
  image="$(cat "$stage/$name.id")"
  python3 scripts/core-distribution-manifest.py verify-image "$image"
  docker image save --output "$bundle/images/$name.tar" "$image"
done

# OCI manifest digests differ from Docker config IDs. Import the exact offline
# archive through the pinned runtime in a temporary cache; no VM is started.
mkdir -m 0700 "$stage/msb-cache"
msb=(docker run --rm --network none --user "$(id -u):$(id -g)" \
  --mount "type=bind,src=$stage/msb-cache,dst=/cache" \
  --mount "type=bind,src=$stage/core/microsandbox,dst=/opt/microsandbox,readonly" \
  --mount "type=bind,src=$bundle/images/runtime.tar,dst=/runtime.tar,readonly" \
  --env MSB_HOME=/cache --env MSB_BACKEND=local --env MSB_PATH=/opt/microsandbox/msb \
  --env MSB_LIBKRUNFW_PATH=/opt/microsandbox/libkrunfw.so.5.6.1 \
  --entrypoint /opt/microsandbox/msb "$core_image")
"${msb[@]}" image load --input /runtime.tar --tag oac-runtime:distribution --quiet
"${msb[@]}" image inspect oac-runtime:distribution --format json > "$stage/runtime-inspect.json"
python3 scripts/core-distribution-manifest.py node-payload "$bundle" "$stage" "$revision" "$source_tree" "$release_base_url" "$offline"
mkdir -p "$stage/ingress"
cp deploy/distribution/Ingress.Dockerfile "$stage/ingress/Dockerfile"
cp "$stage/core/bin/oac" "$stage/ingress/oac"
build_image ingress "$stage/ingress"

docker run --rm --network none --entrypoint /usr/local/bin/oac \
  --env "OAC_REVISION=$revision" --tmpfs /data "$(cat "$stage/ingress.id")" init
python3 scripts/core-distribution-manifest.py verify-image "$(cat "$stage/ingress.id")"
docker image save --output "$bundle/images/ingress.tar" "$(cat "$stage/ingress.id")"
python3 scripts/core-distribution-manifest.py manifest "$bundle" "$stage"
require_clean_source
if [[ "$(git -C "$repo_root" rev-parse HEAD)" != "$revision" ]]; then
  printf 'Source changed during distribution build\n' >&2
  exit 1
fi
archive_name="$(basename "$bundle").tar.gz"
if [[ -e "$output_dir/$(basename "$bundle")" || -e "$output_dir/$archive_name" || -e "$output_dir/${archive_name%.tar.gz}-offline.tar.gz" ]]; then
  printf 'A distribution already exists for this revision; choose a fresh output directory\n' >&2
  exit 1
fi
if [[ -n "$release_base_url" ]]; then
  python3 scripts/core-distribution-manifest.py archive "$bundle" "$source_epoch"
  mv "$stage/$archive_name" "$stage/$archive_name.sha256" "$output_dir/"
  printf 'Core distribution: %s/%s\n' "$output_dir" "$archive_name"
fi
if [[ "$offline" == 1 ]]; then
  mkdir "$bundle/artifacts"
  # Hard links keep the optional archive from requiring another Runtime-sized copy.
  for asset in "$stage/artifacts/"*; do ln "$asset" "$bundle/artifacts/"; done
  python3 scripts/core-distribution-manifest.py native-offline "$bundle" "$stage"
  python3 scripts/core-distribution-manifest.py archive "$bundle" "$source_epoch" offline
  offline_name="${archive_name%.tar.gz}-offline.tar.gz"
  mv "$stage/$offline_name" "$stage/$offline_name.sha256" "$output_dir/"
  printf 'Offline Core distribution: %s/%s\n' "$output_dir" "$offline_name"
fi
mv "$stage/artifacts/"* "$output_dir/"
if [[ -d "$stage/native-artifacts" ]]; then mv "$stage/native-artifacts/"* "$output_dir/"; fi
mv "$bundle" "$output_dir/"

# The launchers and operator commands use this same portable implementation.
for platform in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64; do
  extension=""
  if [[ "$platform" == windows-* ]]; then extension=.exe; fi
  asset="oac-$platform$extension"
  GOOS="${platform%-*}" GOARCH="${platform#*-}" CGO_ENABLED=0 go build -mod=readonly -trimpath \
    -ldflags "-X main.buildRevision=$revision" -o "$output_dir/$asset" ./services/core/cmd/oac
  (cd "$output_dir" && sha256sum "$asset" > "$asset.sha256")
done
