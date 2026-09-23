#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
output_dir="${CORE_CONSOLE_BUILD_DIR:-$runtime_root/build/core-console}"
export GOCACHE="${GOCACHE:-$runtime_root/cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$runtime_root/cache/go-mod}"
for directory in "$runtime_root" "$output_dir" "$GOCACHE" "$GOMODCACHE"; do
  case "$directory" in
    "$HOME/.parsar"|"$HOME/.parsar/"*) ;;
    *) printf 'Core console build directories must be absolute and under ~/.parsar\n' >&2; exit 1 ;;
  esac
  case "/$directory/" in
    */../*|*/./*) printf 'Core console build directories must not contain dot segments\n' >&2; exit 1 ;;
  esac
done

mkdir -p "$runtime_root/cache/core-console-builds"
build_context="$(mktemp -d "$runtime_root/cache/core-console-builds/source.XXXXXX")"
trap 'rm -rf "$build_context"' EXIT
mkdir -p "$build_context/tmp"
export GOTMPDIR="$build_context/tmp"
# Keep the independent console build separate from API and frontend sources.
tar -C "$repo_root" -cf - go.mod go.sum internal/obs/log services/core-console \
  | tar -C "$build_context" -xf -
(
  cd "$build_context"
  export GOWORK=off CGO_ENABLED=0
  go build -mod=readonly -trimpath -buildvcs=false \
    -o "$build_context/core-console" ./services/core-console
)
mkdir -p "$output_dir"
mv -f "$build_context/core-console" "$output_dir/core-console"
printf 'Core console binary: %s/core-console\n' "$output_dir"
