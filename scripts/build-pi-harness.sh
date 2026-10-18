#!/usr/bin/env bash
# Package the pinned Pi CLI as a native installer component. This does not run on a host.
set -euo pipefail

version=1.3.0
package="@earendil-works/pi-coding-agent"
root="${OAC_DEV_HOME:-$HOME/.oac}/build"
destination="${PI_HARNESS_BUILD_DIR:-$root/pi-harness-$(date +%Y%m%d%H%M%S)}"
[[ "$destination" == /* ]] || { printf 'An absolute build directory is required\n' >&2; exit 1; }
[[ ! -e "$destination" ]] || { printf 'Pi artifact destination already exists\n' >&2; exit 1; }
command -v npm >/dev/null || { printf 'npm is required to package Pi\n' >&2; exit 1; }
mkdir -p "$destination/bin"
npm install --prefix "$destination" --no-audit --no-fund --no-save "$package@$version" >/dev/null
entry="$destination/node_modules/$package"
[[ -f "$entry/package.json" ]] || { printf 'Pi package was not installed\n' >&2; exit 1; }
cat > "$destination/bin/pi" <<'SH'
#!/bin/sh
exec node "$(dirname "$0")/../node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js" "$@"
SH
chmod 0755 "$destination/bin/pi"
observed="$("$destination/bin/pi" --version)"
[[ "$observed" == "$version" ]] || { printf 'Pi version %s does not match the pin %s\n' "$observed" "$version" >&2; exit 1; }
printf '%s\n' "$destination"
