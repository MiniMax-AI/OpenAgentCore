#!/usr/bin/env bash
# The native oac command owns installation on every platform.
set -euo pipefail
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) echo 'Use install.ps1 on Windows.' >&2; exit 1;; esac
case "$(uname -m)" in x86_64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Unsupported CPU architecture.' >&2; exit 1;; esac
# Read the release tag without copying "$@": an empty array aborts bash < 4.4 under set -u.
version=latest previous=
for argument in "$@"; do
  case "$previous/$argument" in --version/*) version="$argument";; */--version=*) version="${argument#--version=}";; esac
  previous="$argument"
done
[[ "$previous" != --version ]] || { echo 'Missing release tag after --version.' >&2; exit 1; }
base="https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download"
if [[ "$version" != latest ]]; then base="https://github.com/MiniMax-AI/OpenAgentCore/releases/download/$version"; fi
umask 077
temporary="$(mktemp -d "${TMPDIR:-/tmp}/oac.XXXXXX")"
trap 'rm -rf "$temporary"' EXIT
# Resume after resets, fail only when a download stalls (no total time cap), and give up after five attempts.
download() {
  local name="$1" progress="$2" attempt code
  for attempt in 1 2 3 4 5; do
    if code="$(curl --fail "$progress" --location --proto '=https' --proto-redir '=https' --connect-timeout 15 \
      --speed-limit 1024 --speed-time 60 --continue-at - --write-out '%{http_code}' \
      --output "$temporary/$name" "$base/$name")"; then return 0; fi
    [[ "$code" != 404 ]] || { echo "Release $version not found." >&2; exit 1; }
    sleep 3
  done
  echo "Could not download $name from GitHub. Check the network (set HTTPS_PROXY if a proxy is required) and rerun the installer." >&2
  exit 1
}
asset="oac-$os-$arch"
progress=--silent
if [[ -t 2 ]]; then progress=--progress-bar; fi
download "$asset.sha256" --silent
download "$asset" "$progress"
cd "$temporary"
if command -v sha256sum >/dev/null; then actual="$(sha256sum "$asset")"; else actual="$(shasum -a 256 "$asset")"; fi
[[ "$actual" == "$(cat "$asset.sha256")" ]] || { rm -f "$asset"; echo 'Installer checksum mismatch. Rerun the installer to download it again.' >&2; exit 1; }
chmod 700 "$asset"
"./$asset" install "$@"
