#!/usr/bin/env bash
# The native oac command owns installation on every platform.
set -euo pipefail
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) echo 'Use install.ps1 on Windows.' >&2; exit 1;; esac
case "$(uname -m)" in x86_64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Unsupported CPU architecture.' >&2; exit 1;; esac
version=latest
args=("$@")
while [[ $# -gt 0 ]]; do
  case "$1" in --version) version="${2:?Missing release tag}"; shift 2;; *) shift;; esac
done
base="https://github.com/${OAC_REPOSITORY:-MiniMax-AI/OpenAgentCore}/releases/latest/download"
if [[ "$version" != latest ]]; then base="https://github.com/${OAC_REPOSITORY:-MiniMax-AI/OpenAgentCore}/releases/download/$version"; fi
umask 077
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
asset="oac-$os-$arch"
for name in "$asset" "$asset.sha256"; do
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 15 --max-time 180 --retry 2 "$base/$name" --output "$temporary/$name"
done
cd "$temporary"
if command -v sha256sum >/dev/null; then actual="$(sha256sum "$asset")"; else actual="$(shasum -a 256 "$asset")"; fi
[[ "$actual" == "$(cat "$asset.sha256")" ]] || { echo 'Installer checksum mismatch.' >&2; exit 1; }
chmod 700 "$asset"
"./$asset" install "${args[@]}"
