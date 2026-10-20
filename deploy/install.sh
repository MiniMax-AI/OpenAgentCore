#!/usr/bin/env bash
# The native oac command owns installation on every platform.
# The whole script is one function called on the last line, so a cut-off download never runs partway.
main() {
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
# An unfinished download stays in a per-account cache so a rerun resumes it. It does not depend on ~/.oac, which oac itself reports when it is not writable.
cache="${TMPDIR:-/tmp}/oac-install-$(id -u)"
[[ ! -L "$cache" ]] || { echo "Download cache $cache must not be a symbolic link." >&2; exit 1; }
mkdir -p "$cache"
[[ -d "$cache" && -O "$cache" ]] || { echo "Download cache $cache must belong to the current account." >&2; exit 1; }
chmod 700 "$cache"
trap 'echo "Interrupted; rerun the installer to continue." >&2; exit 130' INT
# Resume after resets, fail only when a download stalls (no total time cap), and give up after five attempts.
download() {
  local name="$1" file="$2" progress="$3" attempt code
  for attempt in 1 2 3 4 5; do
    if code="$(curl --fail "$progress" --location --proto '=https' --proto-redir '=https' --connect-timeout 15 \
      --speed-limit 1024 --speed-time 60 --continue-at - --write-out '%{http_code}' \
      --output "$cache/$file" "$base/$name")"; then return 0; fi
    [[ "$code" != 404 ]] || { echo "Release $version not found." >&2; exit 1; }
    # The partial file already holds the whole download; the checksum decides.
    [[ "$code" != 416 ]] || return 0
    if (( attempt < 5 )); then echo "Download interrupted; retrying ($((attempt + 1))/5)..." >&2; sleep 3; fi
  done
  echo "Could not download $name from GitHub. Check the network (set HTTPS_PROXY if a proxy is required) and rerun the installer." >&2
  exit 1
}
asset="oac-$os-$arch"
progress=--silent
if [[ -t 2 ]]; then progress=--progress-bar; fi
# The checksum is always fetched fresh and names the cached binary, so a moved "latest" never mixes files.
rm -f "$cache/$asset.sha256"
download "$asset.sha256" "$asset.sha256" --silent
expected=
read -r expected _ < "$cache/$asset.sha256" || true
[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { rm -f "$cache/$asset.sha256"; echo 'Installer checksum file is invalid. Rerun the installer to download it again.' >&2; exit 1; }
binary="$cache/$asset.$expected"
for stale in "$cache/$asset".*; do
  [[ "$stale" == "$binary" || "$stale" == "$cache/$asset.sha256" || ! -e "$stale" ]] || rm -f "$stale"
done
verified() {
  local actual=
  if command -v sha256sum >/dev/null; then read -r actual _ < <(sha256sum "$binary"); else read -r actual _ < <(shasum -a 256 "$binary"); fi
  [[ "$actual" == "$expected" ]]
}
# A complete file from an earlier run is used without asking the server for more.
if [[ ! -s "$binary" ]] || ! verified; then
  download "$asset" "$asset.$expected" "$progress"
  verified || { rm -f "$binary"; echo 'Installer checksum mismatch. Rerun the installer to download it again.' >&2; exit 1; }
fi
chmod 700 "$binary"
# oac reports its own interruption.
trap - INT
status=0
"$binary" install "$@" || status=$?
if (( status == 0 )); then rm -rf "$cache"; fi
exit "$status"
}
main "$@"
