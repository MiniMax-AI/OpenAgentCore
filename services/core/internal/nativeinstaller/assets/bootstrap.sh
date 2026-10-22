#!/usr/bin/env bash
set -euo pipefail
base=$1
shift
# Only the installer receives the grant; every other program started below runs without it.
grant=${OAC_INSTALL_GRANT:-}
unset OAC_INSTALL_GRANT
fail() { printf '%s\n' "$*" >&2; exit 1; }
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) fail 'Unsupported operating system.';; esac
case "$(uname -m)" in x86_64) arch=amd64;; arm64|aarch64) arch=arm64;; *) fail 'Unsupported processor architecture.';; esac
for tool in curl tar gzip; do command -v "$tool" >/dev/null || fail "Required command missing: $tool"; done
if command -v sha256sum >/dev/null; then hash=(sha256sum); else hash=(shasum -a 256); command -v shasum >/dev/null || fail 'Required command missing: shasum'; fi
umask 077
root=${OAC_RUNTIME_HOME:-${HOME:?HOME must be set}/.oac}
case "$root" in /*) ;; *) fail 'OAC_RUNTIME_HOME must be an absolute directory.';; esac
mkdir -p "$root" || fail 'Cannot write the Runtime home with the current account.'
root=$(cd "$root" && pwd -P)
cache=$root/native-download
[ ! -L "$cache" ] || fail 'Native download directory must not be a symbolic link.'
mkdir -p "$cache"
[ -d "$cache" ] && [ -O "$cache" ] || fail 'Native download directory must belong to the current account.'
chmod 700 "$cache"
# The unpacked installer lives here; the installer also keeps unfinished component downloads in this directory.
work=$(mktemp -d "$cache/bundle.XXXXXX") || fail 'Cannot write the native download directory.'
active=
cleanup() { rm -rf "$work"; }
interrupt() {
  trap '' INT TERM HUP
  if [ -n "$active" ]; then kill -TERM "$active" 2>/dev/null || :; wait "$active" 2>/dev/null || :; fi
  printf 'Interrupted; rerun the same command to continue.\n' >&2
  exit 130
}
trap cleanup EXIT
trap interrupt INT TERM HUP
run() {
  "$@" <&0 & active=$!
  local result=0
  wait "$active" || result=$?
  active=
  return "$result"
}
progress=(-sS)
if [ -t 2 ]; then progress=(--progress-bar --show-error); fi
fetch() {
  local attempt code status
  for attempt in 1 2 3; do
    code=0
    run curl "${progress[@]}" --fail --location --proto-redir =https --connect-timeout 15 --speed-time 60 --speed-limit 1024 --write-out '%{http_code}' "$1" -o "$2" >"$work/http-status" || code=$?
    [ "$code" -ne 0 ] || return 0
    status=$(cat "$work/http-status")
    [ "$status" != 404 ] || fail 'This Core has no qualified installer for this platform.'
    [ "$code" -ne 23 ] || fail 'Cannot write the installer download. Check disk space, quota and directory permissions.'
    [ "$attempt" -lt 3 ] || break
    printf 'Download interrupted; retrying (%s/2)...\n' "$attempt" >&2
    run sleep "$attempt"
  done
  fail "Installer download failed (HTTP ${status:-unknown}, curl $code). Check the network to Core and GitHub (set HTTPS_PROXY if a proxy is required), then rerun the command."
}
printf 'Downloading the installer matched to Core...\n'
fetch "$base/$os-$arch.sha256" "$work/checksum"
expected=$(cat "$work/checksum")
[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || fail 'Core returned an invalid installer checksum.'
fetch "$base/$os-$arch.tar.gz" "$work/installer.tar.gz"
run "${hash[@]}" "$work/installer.tar.gz" >"$work/digest"
read -r actual rest <"$work/digest"
[ "$actual" = "$expected" ] || fail 'Installer checksum mismatch; the download was discarded. Rerun the command to download it again.'
run tar -xzf "$work/installer.tar.gz" -C "$work" || fail 'Installer extraction failed. Check disk space, quota and filesystem permissions.'
rm -f "$work/installer.tar.gz"
[ -x "$work/oac-daemon" ] || fail 'Cannot start the native installer. Check executable permissions and use a Runtime home that permits execution.'
code=0
OAC_INSTALL_GRANT=$grant run "$work/oac-daemon" install --onboard-url "${base%/install/*}/installation" "$@" || code=$?
case "$code" in
  0) ;;
  126|127) fail 'Cannot start the native installer. Check that the Runtime home permits execution (not a noexec mount) and that this host has the required native libraries.';;
  *) exit "$code";;
esac
