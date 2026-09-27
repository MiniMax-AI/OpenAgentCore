#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
output_dir="${OAC_DEV_WEB_BUILD_DIR:-$runtime_root/build/core-console}"
export GOCACHE="${GOCACHE:-$runtime_root/cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$runtime_root/cache/go-mod}"
for directory in "$runtime_root" "$output_dir" "$GOCACHE" "$GOMODCACHE"; do
  case "$directory" in
    "$HOME/.oac"|"$HOME/.oac/"*) ;;
    *) printf 'Core console build directories must be absolute and under ~/.oac\n' >&2; exit 1 ;;
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
    -o "$build_context/oac-web" ./services/core-console
)
mkdir -p "$output_dir"
mv -f "$build_context/oac-web" "$output_dir/oac-web"
printf 'Core console binary: %s/oac-web\n' "$output_dir"
