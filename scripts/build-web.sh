#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${OAC_DEV_HOME:-$HOME/.oac}"
output_dir="${OAC_DEV_WEB_BUILD_DIR:-$runtime_root/build/oac-web}"
export GOCACHE="${GOCACHE:-$runtime_root/cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$runtime_root/cache/go-mod}"
for directory in "$runtime_root" "$output_dir" "$GOCACHE" "$GOMODCACHE"; do
  case "$directory" in
    "$HOME/.oac"|"$HOME/.oac/"*) ;;
    *) printf 'Web build directories must be absolute and under ~/.oac\n' >&2; exit 1 ;;
  esac
  case "/$directory/" in
    */../*|*/./*) printf 'Web build directories must not contain dot segments\n' >&2; exit 1 ;;
  esac
done

mkdir -p "$runtime_root/cache/oac-web-builds"
build_context="$(mktemp -d "$runtime_root/cache/oac-web-builds/source.XXXXXX")"
trap 'rm -rf "$build_context"' EXIT
mkdir -p "$build_context/tmp"
export GOTMPDIR="$build_context/tmp"
# Keep the independent console build separate from API and frontend sources.
tar -C "$repo_root" -cf - go.mod go.sum internal/obs/log internal/providerassets services/web \
  | tar -C "$build_context" -xf -
(
  cd "$build_context"
  export GOWORK=off CGO_ENABLED=0
  go build -mod=readonly -trimpath -buildvcs=false \
    -o "$build_context/oac-web" ./services/web
)
mkdir -p "$output_dir"
mv -f "$build_context/oac-web" "$output_dir/oac-web"
printf 'Web binary: %s/oac-web\n' "$output_dir"
