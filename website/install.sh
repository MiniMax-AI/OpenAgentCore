#!/usr/bin/env bash
# Keep installation behavior in the published release launcher.
set -euo pipefail
umask 077
temporary="$(mktemp -d "${TMPDIR:-/tmp}/oac.XXXXXX")"
trap 'rm -rf "$temporary"' EXIT
for attempt in 1 2 3 4 5; do
  if curl --fail --silent --location --proto '=https' --proto-redir '=https' --connect-timeout 15 \
    --speed-limit 1024 --speed-time 60 --continue-at - \
    https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh \
    --output "$temporary/install.sh"; then
    bash "$temporary/install.sh" "$@"
    exit
  fi
  sleep 3
done
echo 'Could not download the installer from GitHub. Check the network (set HTTPS_PROXY if a proxy is required) and rerun the command.' >&2
exit 1
