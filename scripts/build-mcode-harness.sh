#!/usr/bin/env bash
set -euo pipefail

case "$(uname -s):$(uname -m)" in
  Linux:x86_64|Darwin:arm64) native_path() { printf '%s\n' "$1"; }; absolute='/*' ;;
  # Git Bash converts POSIX paths only in arguments, not in environment
  # variables, so Node and Git receive drive-letter paths that MSYS tools also accept.
  MINGW64_NT-*:x86_64) native_path() { cygpath -m "$1"; }; absolute='[A-Za-z]:/*' ;;
  *) printf 'Build the MiniMax Code Runtime artifact on Linux x86_64, macOS arm64 or Windows x86_64\n' >&2; exit 1 ;;
esac
repo_root="$(native_path "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)")"
package="$repo_root/packages/mcode-harness"
source="$(native_path "${MCODE_NATIVE_SOURCE:?Set MCODE_NATIVE_SOURCE to the pinned upstream checkout}")"
native="$(native_path "${MCODE_CLI_DIR:?Set MCODE_CLI_DIR to the pinned CLI package supplying native dependencies}")"
root="$(native_path "${OAC_DEV_HOME:-$HOME/.oac}/build")"
destination="$(native_path "${MCODE_HARNESS_BUILD_DIR:-$root/mcode-harness-$(date +%Y%m%d%H%M%S)}")"
for directory in "$root" "$destination"; do
  [[ "$directory" == $absolute ]] || { printf 'Absolute build directories are required\n' >&2; exit 1; }
done
[[ ! -e "$destination" ]] || { printf 'MiniMax artifact destination already exists\n' >&2; exit 1; }
mkdir -p "$root" "$(dirname "$destination")"
context="$(mktemp -d "$root/mcode-build.XXXXXX")"
trap 'rm -rf "$context"' EXIT
revision="$(node -p 'require(process.argv[1]).revision' "$package/source.json")"
if [[ "$(git -C "$source" rev-parse HEAD)" != "$revision" ]]; then
  printf 'MiniMax Code source revision does not match source.json\n' >&2
  exit 1
fi
mkdir "$context/upstream"
git -C "$source" archive "$revision" | tar -x -C "$context/upstream"
# Upstream validates the cached archive and embedded tool hashes before use.
# Preserve this single optional download cache across fresh source exports.
if [[ -f "$source/.cache/artifacts/code-0.3.11.tgz" ]]; then
  mkdir -p "$context/upstream/.cache/artifacts"
  cp "$source/.cache/artifacts/code-0.3.11.tgz" "$context/upstream/.cache/artifacts/"
fi
printf '%s\n' "$revision" > "$context/upstream/.oac-source-revision"
test "$(node "$native/cli.js" --version)" = 0.4.12
(
  MCODE_SOURCE="$context/upstream" node "$package/patch-native.mjs"
  cd "$context/upstream"
  corepack pnpm install --frozen-lockfile
  node scripts/build.mjs
)
artifact="$context/artifact"
mkdir "$artifact"
cp "$package/check.mjs" "$package/subagent-snapshot.mjs" "$package/source.json" "$artifact/"
mkdir "$artifact/native"
cp -R "$context/upstream/dist/." "$artifact/native/"
cp -R "$native/node_modules" "$artifact/native/"
cp "$context/upstream/.oac-native-patch.json" "$artifact/native-patch.json"
cp "$context/upstream/LICENSE" "$artifact/UPSTREAM_LICENSE"
cp "$context/upstream/third_party/pi-mono/LICENSE" "$artifact/PI_LICENSE"
node - "$artifact" "$package" <<'JS'
const { createHash } = require('node:crypto');
const { readdirSync, readFileSync, statSync, writeFileSync } = require('node:fs');
const { join } = require('node:path');
const [artifact, pkg] = process.argv.slice(2);
const pin = JSON.parse(readFileSync(join(pkg, 'source.json'), 'utf8'));
// Hash every regular file, including file symlinks, without descending into directory symlinks.
const isFile = path => { try { return statSync(path).isFile(); } catch { return false; } };
const walk = relative => readdirSync(join(artifact, relative), { withFileTypes: true }).flatMap(entry => {
  const path = relative ? `${relative}/${entry.name}` : entry.name;
  return entry.isDirectory() ? walk(path) : isFile(join(artifact, path)) ? [path] : [];
});
pin.files = Object.fromEntries(walk('').sort().map(path => [path, createHash('sha256').update(readFileSync(join(artifact, path))).digest('hex')]));
writeFileSync(join(artifact, 'provenance.json'), JSON.stringify(pin, null, 2) + '\n');
JS
mv "$artifact" "$destination"
printf 'MiniMax Code Runtime artifact: %s\n' "$destination"
