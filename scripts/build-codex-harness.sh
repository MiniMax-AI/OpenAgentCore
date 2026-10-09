#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package="$repo_root/packages/codex-runtime"
source="${CODEX_NATIVE_SOURCE:?Set CODEX_NATIVE_SOURCE to the pinned upstream checkout}"
root="${OAC_DEV_HOME:-$HOME/.oac}/build"
destination="${CODEX_HARNESS_BUILD_DIR:-$root/codex-harness-$(date +%Y%m%d%H%M%S)}"
for directory in "$source" "$root" "$destination"; do
  [[ "$directory" == /* ]] || { printf 'Absolute build directories are required\n' >&2; exit 1; }
done
[[ "$(uname -s):$(uname -m)" == Linux:x86_64 ]] || { printf 'Build Codex on Linux x86_64\n' >&2; exit 1; }
[[ ! -e "$destination" ]] || { printf 'Codex artifact destination already exists\n' >&2; exit 1; }
mkdir -p "$root" "$(dirname "$destination")"
context="$(mktemp -d "$root/codex-build.XXXXXX")"
trap 'rm -rf "$context"' EXIT
python3 "$package/check-source.py" "$source" > "$context/source.json"
revision="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$context/source.json")"
mkdir "$context/upstream"
git -C "$source" archive "$revision" | tar -x -C "$context/upstream"
git -C "$context/upstream" apply "$package/invalidate-mcp.patch"
target=x86_64-unknown-linux-musl
export CODEX_REPO_ROOT="$context/upstream"
# musl-gcc specs can inject an interpreter into Rust's static PIE. Keep the
# self-contained startup code authoritative instead of loading it twice.
export RUSTFLAGS="${RUSTFLAGS:-} -C link-arg=-Wl,--no-dynamic-linker"
(
  cd "$context/upstream/codex-rs"
  # The release tag updates workspace versions but leaves local lock entries at
  # 0.0.0. Resolve that metadata before compiling; external dependencies stay pinned.
  cargo fetch --target "$target"
  python3 - "$source/codex-rs/Cargo.lock" Cargo.lock <<'PY'
import sys, tomllib
before, after = [tomllib.load(open(path, "rb")) for path in sys.argv[1:]]
def external(lock):
    return [entry for entry in lock["package"] if "source" in entry]
assert external(before) == external(after), "Codex external dependency lock changed"
def local(lock):
    return {entry["name"]: {k: v for k, v in entry.items() if k != "version"}
            for entry in lock["package"] if "source" not in entry}
assert local(before) == local(after), "Codex workspace dependencies changed"
PY
  # Match the CLI release feature union, including its vendored OpenSSL.
  cargo test --locked --target "$target" --release -p codex-mcp -p codex-cli --lib targeted_invalidation_
  cargo test --locked --target "$target" --release -p codex-mcp -p codex-cli --lib prepared_call_does_not_reroute_after_captured_connection_closes
  cargo build --locked --target "$target" --release --bin bwrap
  output="${CARGO_TARGET_DIR:-$PWD/target}/$target/release"
  strip --strip-debug --strip-unneeded "$output/bwrap"
  export CODEX_BWRAP_SHA256="$(sha256sum "$output/bwrap" | cut -d' ' -f1)"
  python3 "$context/upstream/scripts/build_codex_package.py" \
    --target "$target" --cargo-profile release --package-dir "$context/canonical" \
    --bwrap-bin "$output/bwrap"
)
mkdir "$context/artifact"
cp "$context/canonical/bin/codex" "$context/canonical/bin/codex-code-mode-host" "$context/artifact/"
strip --strip-debug --strip-unneeded "$context/artifact/codex" "$context/artifact/codex-code-mode-host"
python3 - "$context" <<'PY'
import hashlib, json, pathlib, subprocess, sys
context = pathlib.Path(sys.argv[1])
artifact = context / "artifact"
pin = json.loads((context / "source.json").read_text())
for name in ("codex", "codex-code-mode-host"):
    executable = str(artifact / name)
    headers = subprocess.check_output(["readelf", "-lW", executable], text=True)
    dynamic = subprocess.check_output(["readelf", "-dW", executable], text=True)
    assert not any(line.split()[0] == "INTERP" for line in headers.splitlines() if line.split()), "Codex payload requires an ELF interpreter: " + name
    assert "(NEEDED)" not in dynamic, "Codex payload requires a shared library: " + name
assert subprocess.check_output([str(artifact / "codex"), "--version"], text=True).strip() == "codex-cli " + pin["version"]
pin["target"] = "x86_64-unknown-linux-musl"
pin["cargo_lock_sha256"] = hashlib.sha256((context / "upstream/codex-rs/Cargo.lock").read_bytes()).hexdigest()
pin["files"] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(artifact.iterdir())}
(artifact / "provenance.json").write_text(json.dumps(pin, indent=2) + "\n")
PY
mv "$context/artifact" "$destination"
printf 'Codex Runtime artifact: %s\n' "$destination"
