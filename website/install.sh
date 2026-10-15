#!/usr/bin/env bash
# Keep installation behavior in the published release launcher.
set -euo pipefail
umask 077
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  --connect-timeout 15 --max-time 180 --retry 2 \
  https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh \
  --output "$temporary/install.sh"
bash "$temporary/install.sh" "$@"
