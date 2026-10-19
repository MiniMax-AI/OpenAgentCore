#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package="$repo_root/packages/mcode-harness"
source="${MCODE_NATIVE_SOURCE:?Set MCODE_NATIVE_SOURCE to the pinned upstream checkout}"
native="${MCODE_CLI_DIR:?Set MCODE_CLI_DIR to the pinned CLI package supplying native dependencies}"
case "$(uname -s):$(uname -m)" in
  Linux:x86_64|Darwin:arm64) ;;
  *) printf 'Build the MiniMax Code Runtime artifact on Linux x86_64 or macOS arm64\n' >&2; exit 1 ;;
esac
root="${OAC_DEV_HOME:-$HOME/.oac}/build"
destination="${MCODE_HARNESS_BUILD_DIR:-$root/mcode-harness-$(date +%Y%m%d%H%M%S)}"
for directory in "$root" "$destination"; do
  [[ "$directory" == /* ]] || { printf 'Absolute build directories are required\n' >&2; exit 1; }
done
[[ ! -e "$destination" ]] || { printf 'MiniMax artifact destination already exists\n' >&2; exit 1; }
mkdir -p "$root" "$(dirname "$destination")"
context="$(mktemp -d "$root/mcode-build.XXXXXX")"
trap 'rm -rf "$context"' EXIT
revision="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$package/source.json")"
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
python3 - "$artifact" "$package" <<'PY'
import hashlib,json,pathlib,sys
artifact,package=map(pathlib.Path,sys.argv[1:])
pin=json.loads((package/'source.json').read_text())
pin['files']={str(p.relative_to(artifact)):hashlib.sha256(p.read_bytes()).hexdigest()
              for p in sorted(artifact.rglob('*')) if p.is_file()}
(artifact/'provenance.json').write_text(json.dumps(pin,indent=2)+'\n')
PY
mv "$artifact" "$destination"
printf 'MiniMax Code Runtime artifact: %s\n' "$destination"
