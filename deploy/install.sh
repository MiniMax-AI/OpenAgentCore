#!/usr/bin/env bash
# Install Core and Web from one release's Compose file. The host needs Docker.
set -euo pipefail

repository="${OAC_REPOSITORY:-MiniMax-AI/OpenAgentCore}"
version="latest"
install_dir="${OAC_INSTALL_DIR_DEFAULT:-$HOME/.oac/core}"
public_url=""
host_address="0.0.0.0"
web_port="8080"
kept=0

usage() {
  cat <<'EOF'
Usage: install.sh [--version TAG] [--install-dir DIR] [--public-url URL]
                  [--host ADDRESS] [--web-port PORT]

Installs Core, Web and PostgreSQL, and publishes Web on --web-port. Without
--public-url, a host published on all addresses with a private-network address
is reached at http://<that address>:<web port>; otherwise only from this host.
HTTPS is terminated by your reverse proxy or hosting platform.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) version="${2:?}"; shift 2 ;;
    --install-dir) install_dir="${2:?}"; shift 2 ;;
    --public-url) public_url="${2:?}"; shift 2 ;;
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
command -v docker >/dev/null || { echo "Docker Engine with Compose 2.26 or newer is required." >&2; exit 1; }
command -v curl >/dev/null || { echo "curl is required." >&2; exit 1; }
compose_version="$(docker compose version --short 2>/dev/null | sed 's/^v//' || true)"
major="${compose_version%%.*}"
minor="${compose_version#*.}"
minor="${minor%%.*}"
if [[ ! "$major" =~ ^[0-9]+$ || ! "$minor" =~ ^[0-9]+$ ]] || (( major < 2 || (major == 2 && minor < 26) )); then
  echo "Docker Compose 2.26 or newer is required (found ${compose_version:-none})." >&2
  exit 1
fi
if [[ "$install_dir" != /* ]]; then
  echo "--install-dir must be absolute." >&2
  exit 1
fi
if [[ -e "$install_dir" ]] && [[ -n "$(ls -A "$install_dir" 2>/dev/null || true)" ]]; then
  echo "Installation directory is not empty: $install_dir" >&2
  exit 1
fi

port_busy() {
  local port="$1"
  if command -v ss >/dev/null; then
    if ss -ltn | awk '{print $4}' | grep -Eq "(^|:|\\])${port}$"; then
      return 0
    fi
    return 1
  fi
  (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1
}
if port_busy "$web_port"; then echo "Port $web_port is already in use." >&2; exit 1; fi
asset_base="https://github.com/${repository}/releases/latest/download"
if [[ "$version" != latest ]]; then
  asset_base="https://github.com/${repository}/releases/download/${version}"
fi

log="$(mktemp)"
cleanup() {
  if [[ "$kept" != 1 && -d "$install_dir" ]]; then
    (
      cd "$install_dir"
      docker compose logs --no-color --tail 50 >&2 || true
      docker compose down --remove-orphans
      # Containers own data/; remove it from a container as well.
      if [[ -d data ]]; then docker compose run --rm --no-deps --volume "$install_dir/data:/data" --entrypoint find database /data -mindepth 1 -delete; fi
    ) >/dev/null 2>&1 || true
    rm -rf "$install_dir"
  fi
  rm -f "$log"
}
trap cleanup EXIT

# step DESCRIPTION COMMAND... prints the command's output only when it fails.
step() {
  printf '%s... ' "$1"
  shift
  if "$@" >"$log" 2>&1; then echo done; else echo failed; cat "$log" >&2; return 1; fi
}

# The source address of this host's default route, when it is a private one.
private_address() {
  ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' |
    grep -E '^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.)' || true
}
local_only=0
if [[ -z "$public_url" && "$host_address" == 0.0.0.0 ]]; then
  address="$(private_address)"
  if [[ -n "$address" ]]; then public_url="http://$address:$web_port"; fi
fi
if [[ -z "$public_url" ]]; then public_url="http://localhost:$web_port"; local_only=1; fi

mkdir -p "$install_dir"
chmod 700 "$install_dir"
curl --fail --silent --show-error --location "$asset_base/compose-sha256sums.txt" --output "$install_dir/compose-sha256sums.txt"
curl --fail --silent --show-error --location "$asset_base/compose.yaml" --output "$install_dir/compose.yaml"
(cd "$install_dir" && sha256sum --check --quiet compose-sha256sums.txt)

umask 077
{
  echo "COMPOSE_PROJECT_NAME=oac-$(od -An -N5 -tx1 /dev/urandom | tr -d ' \n')"
  echo "OAC_INSTALL_DIR=$install_dir"
  echo "OAC_HOST=$host_address"
  echo "OAC_WEB_PORT=$web_port"
  echo "OAC_PUBLIC_URL=$public_url"
} >"$install_dir/.env"

cd "$install_dir"
copy_cli() { docker compose create core && docker compose cp core:/usr/local/bin/oac ./oac; }
step "Pulling images" docker compose pull
step "Installing the oac command" copy_cli
step "Starting services" docker compose up -d --wait
key="$(./oac core-key --show)"
kept=1
trap - EXIT
rm -f "$log"

sudo=""
if [[ "$EUID" == 0 && -n "${SUDO_USER:-}" ]]; then sudo="sudo "; fi
cat <<EOF

OpenAgentCore is running.

  Console   $public_url
  Core key  $key

Sign in to the console with the Core key, the administrator credential.
${sudo}$install_dir/oac core-key --show prints it again.
EOF
if [[ "$local_only" == 1 ]]; then
  cat <<EOF
Only this host can open the console. To serve other machines, set
OAC_PUBLIC_URL in $install_dir/.env, then run ${sudo}$install_dir/oac apply.
EOF
fi
cat <<EOF

Next, on System, set a default model and choose a sandbox backend, then add a
node on Nodes.
EOF
