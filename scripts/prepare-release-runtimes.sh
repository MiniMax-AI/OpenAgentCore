#!/usr/bin/env bash
set -euo pipefail

# Prepare pinned upstream inputs once, then reuse the existing Runtime builders.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
catalog="$repo_root/internal/harnessconfig/builtin/catalog.json"
release_root="$HOME/.oac/build/release-inputs"
if [[ -e "$release_root" ]]; then
  printf 'Release input directory already exists; use a fresh build host\n' >&2
  exit 1
fi
mkdir -p "$release_root/mcode-native"
pin="$repo_root/packages/codex-runtime/source.json"
source_repository="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["repository"])' "$pin")"
source_revision="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$pin")"
git init --quiet "$release_root/codex-source"
git -C "$release_root/codex-source" remote add origin "$source_repository"
git -C "$release_root/codex-source" fetch --depth 1 origin "$source_revision"
git -C "$release_root/codex-source" checkout --detach FETCH_HEAD
(
cd "$release_root/codex-source/codex-rs"
# rustup reads the toolchain and components from the pinned upstream checkout.
rustup show active-toolchain
rustup target add x86_64-unknown-linux-musl
TARGET=x86_64-unknown-linux-musl GITHUB_ENV="$release_root/codex-build.env" \
  bash "$release_root/codex-source/.github/scripts/install-musl-build-tools.sh"
while IFS= read -r assignment; do export "$assignment"; done < "$release_root/codex-build.env"
export AWS_LC_SYS_NO_JITTER_ENTROPY=1 AWS_LC_SYS_NO_JITTER_ENTROPY_x86_64_unknown_linux_musl=1
CODEX_NATIVE_SOURCE="$release_root/codex-source" CODEX_HARNESS_BUILD_DIR="$release_root/codex" \
  bash "$repo_root/scripts/build-codex-harness.sh" | tee "$release_root/codex-build.log"
)

pin="$repo_root/packages/mcode-harness/source.json"
source_repository="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["repository"])' "$pin")"
source_revision="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$pin")"
source_version="$(python3 -c 'import json,sys; print(next(entry["version"] for entry in json.load(open(sys.argv[1])) if entry["kind"] == "mcode"))' "$catalog")"
git init --quiet "$release_root/mcode-source"
git -C "$release_root/mcode-source" remote add origin "$source_repository"
git -C "$release_root/mcode-source" fetch --depth 1 origin "$source_revision"
git -C "$release_root/mcode-source" checkout --detach FETCH_HEAD
npm install --prefix "$release_root/mcode-native" --no-audit --no-fund --include=optional \
  --install-strategy=nested "@minimax-ai/code@$source_version"
MCODE_NATIVE_SOURCE="$release_root/mcode-source" \
MCODE_CLI_DIR="$release_root/mcode-native/node_modules/@minimax-ai/code" \
  bash "$repo_root/scripts/build-mcode-harness.sh" | tee "$release_root/mcode-build.log"
companion="$(sed -n 's/^MiniMax Code Runtime artifact: //p' "$release_root/mcode-build.log")"
test -f "$companion/provenance.json"
python3 - "$release_root" "$companion" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
(root / "inputs.json").write_text(json.dumps({
    "codex": str(root / "codex"),
    "mcode": sys.argv[2],
}) + "\n")
PY
printf 'Pinned Runtime build inputs: %s/inputs.json\n' "$release_root"
