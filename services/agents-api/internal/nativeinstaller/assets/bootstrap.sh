#!/usr/bin/env bash
set -euo pipefail
base=$1
authorization=$2
shift 2
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) echo 'Unsupported operating system.' >&2; exit 1;; esac
case "$(uname -m)" in x86_64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Unsupported processor architecture.' >&2; exit 1;; esac
umask 077
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
echo 'Downloading the installer matched to Core...'
curl -fsS "$base/$os-$arch.sha256" -o "$work/checksum" || { echo 'This Core has no qualified installer for this platform.' >&2; exit 1; }
curl -fsS "$base/$os-$arch.tar.gz" -o "$work/bundle.tar.gz"
if command -v sha256sum >/dev/null; then
  actual=$(sha256sum "$work/bundle.tar.gz"); actual=${actual%% *}
else
  actual=$(shasum -a 256 "$work/bundle.tar.gz"); actual=${actual%% *}
fi
expected=$(cat "$work/checksum")
if [ "$actual" != "$expected" ]; then echo 'Installer checksum mismatch; download again.' >&2; exit 1; fi
mkdir "$work/bundle"
tar -xzf "$work/bundle.tar.gz" -C "$work/bundle"
"$work/bundle/oac-daemon" install --onboard-url "${base%/install/*}/installation" --authorization "$authorization" "$@"
