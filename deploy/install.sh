#!/usr/bin/env bash
# Install Core and Web from one release's Compose file. The host needs Docker.
set -euo pipefail

repository="${OAC_REPOSITORY:-MiniMax-AI/OpenAgentCore}"
version="latest"
install_dir="${OAC_INSTALL_DIR_DEFAULT:-$HOME/.oac/core}"
public_url=""
host_address="0.0.0.0"
web_port="8080"
stage=""
published=0

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
fail() { printf '%s\n' "$*" >&2; exit 1; }
for tool in docker curl sha256sum flock od sed awk grep; do
  command -v "$tool" >/dev/null || fail "Required command missing: $tool. Install it and rerun this command. Docker needs Compose 2.26 or newer."
done
compose_version="$(docker compose version --short 2>/dev/null | sed 's/^v//' || true)"
major="${compose_version%%.*}"
minor="${compose_version#*.}"
minor="${minor%%.*}"
if [[ ! "$major" =~ ^[0-9]+$ || ! "$minor" =~ ^[0-9]+$ ]] || (( major < 2 || (major == 2 && minor < 26) )); then
  fail "Docker Compose 2.26 or newer is required (found ${compose_version:-none})."
fi
docker info >/dev/null 2>&1 || fail "Cannot reach Docker. Start Docker and check this account's access, then rerun."
[[ "$install_dir" == /* && "$install_dir" != / ]] || fail "--install-dir must be an absolute directory other than /."
# These values are written as literal dotenv strings, never shell commands.
for value in "$install_dir" "$public_url" "$host_address" "$version"; do
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* && "$value" != *"'"* && "$value" != *\\* ]] || fail "Installation options cannot contain newlines, quotes or backslashes."
done
[[ "$web_port" =~ ^[0-9]{1,5}$ ]] && (( 10#$web_port >= 1 && 10#$web_port <= 65535 )) || fail "--web-port must be between 1 and 65535."
[[ ! -L "$install_dir" ]] || fail "Installation directory must not be a symbolic link."
[[ ! -e "$install_dir" || -d "$install_dir" ]] || fail "Installation path must be a directory."
case "$(basename "$install_dir")" in .|..) fail "--install-dir must name the installation directory, not . or ...";; esac
umask 077
parent="$(dirname "$install_dir")"
mkdir -p "$parent"
parent="$(cd "$parent" && pwd -P)"
install_dir="$parent/$(basename "$install_dir")"
lock="$install_dir.install.lock"
[[ ! -L "$lock" && ( ! -e "$lock" || ( -f "$lock" && -O "$lock" ) ) ]] || fail "Invalid installation lock: $lock"
exec 9>>"$lock"
flock -n 9 || fail "Another installation is using this directory. Wait for it to finish and rerun."
stage="$install_dir.staging"
[[ ! -L "$stage" && ( ! -e "$stage" || ( -d "$stage" && -O "$stage" ) ) ]] || fail "Invalid installation staging directory: $stage"
if [[ -d "$stage" && -n "$(ls -A "$stage")" ]]; then
  [[ -f "$stage/.oac-installer" && ! -L "$stage/.oac-installer" && "$(cat "$stage/.oac-installer")" == "OpenAgentCore staging for $install_dir" ]] || fail "Unrecognized staging directory; preserve it and choose another --install-dir: $stage"
fi
rm -rf "$stage"
mkdir "$stage"
printf 'OpenAgentCore staging for %s\n' "$install_dir" >"$stage/.oac-installer"
log="$stage/install.log"
: >"$log"
cleanup() {
  local status=$?
  trap - EXIT
  if [[ "$status" != 0 && "$published" == 1 ]]; then
    printf '\nInstallation and data retained at %s.\n' "$install_dir" >&2
    (cd "$install_dir" && docker compose ps --all && docker compose logs --no-color --tail 50) >&2 || true
    printf 'Fix the reported problem, then rerun install.sh --install-dir %q.\n' "$install_dir" >&2
  fi
  [[ -z "$stage" ]] || rm -rf "$stage"
  rm -f "$log"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

# step DESCRIPTION COMMAND... prints the command's output only when it fails.
step() {
  printf '%s... ' "$1"
  shift
  if "$@" >"$log" 2>&1; then echo done; else echo failed; cat "$log" >&2; return 1; fi
}

resume=0
if [[ -e "$install_dir" && -n "$(ls -A "$install_dir")" ]]; then
  for file in compose.yaml compose-sha256sums.txt .env; do
    [[ -f "$install_dir/$file" && ! -L "$install_dir/$file" ]] || fail "Directory is not a complete Core installation: $install_dir. Preserve it and choose another directory."
  done
  resume=1
  published=1
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
if [[ "$resume" == 0 ]] && port_busy "$web_port"; then fail "Port $web_port is already in use. Choose another with --web-port."; fi
asset_base="https://github.com/${repository}/releases/latest/download"
if [[ "$version" != latest ]]; then
  asset_base="https://github.com/${repository}/releases/download/${version}"
fi

# The source address of this host's default route, when it is a private one.
private_address() {
  command -v ip >/dev/null || return 0
  ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' |
    grep -E '^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.)' || true
}
local_only=0
if [[ -z "$public_url" && "$host_address" == 0.0.0.0 ]]; then
  address="$(private_address)"
  if [[ -n "$address" ]]; then public_url="http://$address:$web_port"; fi
fi
if [[ -z "$public_url" ]]; then public_url="http://localhost:$web_port"; local_only=1; fi

if [[ "$resume" == 0 ]]; then
  # Publish configuration only after both downloads and Compose validation succeed.
  # Before publication only this invocation's private staging directory is removed.
  download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
      --connect-timeout 15 --max-time 120 --retry 2 --retry-connrefused --retry-delay 1 \
      --max-filesize 1048576 "$asset_base/$1" --output "$stage/$1"
  }
  step "Downloading release checksums" download compose-sha256sums.txt
  step "Downloading Compose configuration" download compose.yaml
  (cd "$stage" && sha256sum --check --quiet compose-sha256sums.txt)
  {
    echo "COMPOSE_PROJECT_NAME=oac-$(od -An -N5 -tx1 /dev/urandom | tr -d ' \n')"
    printf "OAC_INSTALL_DIR='%s'\nOAC_HOST='%s'\nOAC_WEB_PORT='%s'\nOAC_PUBLIC_URL='%s'\n" "$install_dir" "$host_address" "$web_port" "$public_url"
  } >"$stage/.env"
  (cd "$stage" && step "Checking Compose configuration" docker compose config --quiet)
  # An existing empty directory may be replaced, never a directory with user data.
  if [[ -e "$install_dir" ]]; then rmdir "$install_dir"; fi
  mv "$stage" "$install_dir"
  stage=""
  log="$install_dir/install.log"
  rm -f "$install_dir/.oac-installer"
  published=1
fi

cd "$install_dir"
[[ ! -L .oac.lock && ( ! -e .oac.lock || ( -f .oac.lock && -O .oac.lock ) ) ]] || fail "Invalid installation lock: $install_dir/.oac.lock"
exec 8>>.oac.lock
flock -n 8 || fail "Another oac command is using this installation. Wait for it to finish and rerun."
[[ ! -L install.log && ( ! -e install.log || ( -f install.log && -O install.log ) ) ]] || fail "Invalid installation log: $install_dir/install.log"
log="$install_dir/install.log"
: >"$log"
step "Verifying saved configuration" sha256sum --check --quiet compose-sha256sums.txt
step "Checking Compose configuration" docker compose config --quiet
if [[ "$resume" == 0 ]]; then
  step "Pulling images" docker compose pull
else
  printf 'Using saved settings from .env; installation flags only apply to new directories. Existing data is preserved.\n'
  public_url="$(docker compose config --environment | sed -n 's/^OAC_PUBLIC_URL=//p')"
  local_only=0
  [[ "$public_url" != http://localhost:* && "$public_url" != http://127.0.0.1:* ]] || local_only=1
fi
copy_cli() (
  [[ ! -L ./oac.download ]] || fail "Temporary oac command must not be a symbolic link."
  trap 'rm -f ./oac.download' EXIT
  docker compose create core &&
    docker compose cp core:/usr/local/bin/oac ./oac.download &&
    mv ./oac.download ./oac
)
if [[ ! -x ./oac ]]; then step "Installing the oac command" copy_cli; fi
step "Starting services" docker compose up -d --wait --wait-timeout 180
if ! key="$(./oac core-key --show)"; then
  fail "Services started, but the Core key could not be read. Inspect Web's logs and retry; data is preserved."
fi

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
