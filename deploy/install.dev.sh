#!/usr/bin/env bash
# Start this checkout. Core, Web and the init image are built here. Node
# metadata still comes from the release named in deploy/compose/smoke-pins.json.
# The published installer is install.sh.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${OAC_INSTALL_DIR_DEFAULT:-$HOME/.oac/local}"
host_address="127.0.0.1"
web_port="8080"

if [[ -x "$HOME/.oac/build/env-docker/docker" ]]; then
  PATH="$HOME/.oac/build/env-docker:$PATH"
fi

usage() {
  cat <<'EOF'
Usage: install.dev.sh [--install-dir DIR] [--host ADDRESS] [--web-port PORT]

Builds Core, Web and the init image from this checkout and starts them.
Open http://localhost:<port> and sign in with the printed Core key.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-dir) install_dir="${2:?}"; shift 2 ;;
    --host) host_address="${2:?}"; shift 2 ;;
    --web-port) web_port="${2:?}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
  esac
done

if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo "Core installs on Linux amd64." >&2
  exit 1
fi
command -v docker >/dev/null || { echo "Docker Engine with Compose is required." >&2; exit 1; }
command -v go >/dev/null || { echo "Go is required to build this checkout." >&2; exit 1; }
if [[ ! -f "$repo_root/apps/web/dist/index.html" ]]; then
  echo "Build the console first: pnpm --dir apps/web build" >&2
  exit 1
fi
if [[ "$install_dir" != /* ]]; then
  echo "--install-dir must be absolute." >&2
  exit 1
fi

mkdir -p "$install_dir/data"
chmod 700 "$install_dir/data"
build="$install_dir/image-build"
rm -rf "$build"
mkdir -p "$build"

revision="$(git -C "$repo_root" rev-parse HEAD)"
protocol="$(sed -n 's/^const Version = "\([^"]*\)".*/\1/p' "$repo_root/internal/agentdaemon/proto/version.go")"
export CGO_ENABLED=0 GOOS=linux GOARCH=amd64

go_build() {
  mkdir -p "$(dirname "$2")"
  go build -trimpath -ldflags "-X main.buildRevision=$revision" -o "$2" "./$1"
}

(
  cd "$repo_root"
  go_build services/core/cmd/server "$build/core/bin/oac-core"
  go_build services/core/cmd/device "$build/core/bin/oac-core-device"
  go_build services/core/cmd/environment-key "$build/core/bin/oac-core-environment-key"
  go_build services/core/cmd/oac "$build/core/bin/oac"
  go_build services/web "$build/web/oac-web"
  go_build services/core/cmd/oac "$build/ingress/oac"
)
mkdir -p "$build/core/e2b" "$build/core/native-installers"
python3 - "$build/core/native-installers/catalog.json" "$revision" "$protocol" <<'PY'
import json, sys
path, revision, protocol = sys.argv[1:]
json.dump({"version": revision, "protocol_version": protocol, "artifacts": {"linux-amd64": {
    "sha256": "0" * 64,
    "url": f"https://example.invalid/oac-native-{revision}-linux-amd64.tar.gz"}}}, open(path, "w"))
PY
cp "$repo_root/deploy/distribution/Dockerfile" "$build/core/Dockerfile"
cp "$repo_root/services/web/Dockerfile" "$build/web/Dockerfile"
cp "$repo_root/deploy/distribution/Ingress.Dockerfile" "$build/ingress/Dockerfile"
cp -a "$repo_root/apps/web/dist" "$build/web/dist"
chmod -R a+rX "$build"

tag="oac-local"
docker build -q --platform linux/amd64 -t "$tag/core:dev" "$build/core" >/dev/null
docker build -q --platform linux/amd64 -t "$tag/web:dev" "$build/web" >/dev/null
docker build -q --platform linux/amd64 -t "$tag/ingress:dev" "$build/ingress" >/dev/null

python3 - "$repo_root" "$install_dir" <<'PY'
import importlib.util, json, sys
from pathlib import Path
root, dest = map(Path, sys.argv[1:])
spec = importlib.util.spec_from_file_location("render", root / "scripts/render-compose.py")
render = importlib.util.module_from_spec(spec)
spec.loader.exec_module(render)
pins = json.loads((root / "deploy/compose/smoke-pins.json").read_text())
(dest / "compose.yaml").write_text(render.render({
    "REVISION": pins["revision"],
    "RELEASE_BASE": pins["release_base"],
    "ARCHIVE_CHECKSUM": pins["archive_checksum"],
}))
PY
cp "$repo_root/deploy/compose/ports.yaml" "$install_dir/ports.yaml"

umask 077
cat >"$install_dir/.env" <<EOF
COMPOSE_PROJECT_NAME=oac-local
COMPOSE_FILE=compose.yaml:ports.yaml
OAC_DATA_DIR=$install_dir/data
OAC_HOST=$host_address
OAC_WEB_PORT=$web_port
OAC_IMAGE_CORE=$tag/core:dev
OAC_IMAGE_WEB=$tag/web:dev
OAC_IMAGE_INGRESS=$tag/ingress:dev
EOF

(
  cd "$install_dir"
  docker compose up -d --wait --wait-timeout 900
  echo
  echo "Open http://localhost:$web_port"
  echo "Core key:"
  docker compose exec -T web /usr/local/bin/oac-web core-key
)
