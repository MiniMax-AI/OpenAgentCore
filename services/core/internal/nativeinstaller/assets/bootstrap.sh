#!/usr/bin/env bash
set -euo pipefail
base=$1
authorization=$2
shift 2
fail() { printf '%s\n' "$*" >&2; exit 1; }
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) fail 'Unsupported operating system.';; esac
case "$(uname -m)" in x86_64) arch=amd64;; arm64|aarch64) arch=arm64;; *) fail 'Unsupported processor architecture.';; esac
for tool in curl tar gzip df awk wc; do command -v "$tool" >/dev/null || fail "Required command missing: $tool"; done
if command -v sha256sum >/dev/null; then hash=(sha256sum); else hash=(shasum -a 256); command -v shasum >/dev/null || fail 'Required command missing: shasum'; fi
if [ "$os" = darwin ]; then command -v lockf >/dev/null || fail 'Required command missing: lockf'; else command -v flock >/dev/null || fail 'Required command missing: flock (util-linux)'; fi
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
lock=$cache/download.lock
[ ! -L "$lock" ] && { [ ! -e "$lock" ] || { [ -f "$lock" ] && [ -O "$lock" ]; }; } || fail 'Invalid native download lock.'
# Children inherit this descriptor, so killing only the shell cannot expose an active download.
exec 9>>"$lock"
if [ "$os" = darwin ]; then lockf -s -t 0 9; else flock -n 9; fi || fail 'Another native download is running; wait for it to finish and retry.'
work=$cache/staging
[ ! -L "$work" ] && { [ ! -e "$work" ] || { [ -d "$work" ] && [ -O "$work" ]; }; } || fail 'Invalid native download staging directory.'
rm -rf "$work"
mkdir "$work"
active=
cleanup() { rm -rf "$work"; }
interrupt() {
  trap '' INT TERM HUP
  if [ -n "$active" ]; then kill -TERM "$active" 2>/dev/null || :; wait "$active" 2>/dev/null || :; fi
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
space() {
  local available
  available=$(df -Pk "$work" | awk 'NR==2 {print $4}')
  case "$available" in ''|*[!0-9]*) fail 'Cannot determine free disk space.';; esac
  [ "$available" -ge "$(( ($1 + 67108864 + 1023) / 1024 ))" ] || fail 'Not enough disk space for the native installer. Free space in the Runtime home and retry.'
}
progress=(-sS)
if [ -t 2 ]; then progress=(--progress-bar --show-error); fi
fetch() {
  local url=$1 destination=$2 limit=$3 status code attempt
  for attempt in 1 2 3; do
    code=0
    run curl "${progress[@]}" --fail --location --proto-redir =https --connect-timeout 15 --max-time 1200 --speed-time 60 --speed-limit 1024 --max-filesize "$limit" --write-out '%{http_code}' "$url" -o "$destination" >"$work/http-status" || code=$?
    status=$(cat "$work/http-status")
    [ "$code" -ne 0 ] || return 0
    case "$status" in 404) fail 'This Core has no qualified installer for this platform.';; esac
    case "$code" in
      23) fail 'Cannot write the installer download. Check disk space, quota and directory permissions.';;
      63) fail 'Installer download exceeds the available space or metadata size limit.';;
    esac
    case "$code:$status" in
      6:*|7:*|18:*|28:*|52:*|55:*|56:*|22:408|22:429|22:500|22:502|22:503|22:504)
        if [ "$attempt" -lt 3 ]; then printf 'Download interrupted; retrying (%s/2)...\n' "$attempt" >&2; run sleep "$attempt"; continue; fi;;
    esac
    fail "Installer download failed (HTTP ${status:-unknown}, curl $code). Check network, proxy and certificates, then retry."
  done
}
printf 'Downloading the installer matched to Core...\n'
space 0
fetch "$base/$os-$arch.sha256" "$work/checksum" 1024
expected=$(cat "$work/checksum")
[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || fail 'Core returned an invalid installer checksum.'
available=$(df -Pk "$work" | awk 'NR==2 {print $4}')
fetch "$base/$os-$arch.tar.gz" "$work/bundle.tar.gz" "$((available * 1024 - 67108864))"
printf 'Verifying the installer archive...\n'
run "${hash[@]}" "$work/bundle.tar.gz" >"$work/digest"
read -r actual rest <"$work/digest"
[ "$actual" = "$expected" ] || fail 'Installer checksum mismatch; download again.'
printf 'Checking extraction space...\n'
# Count the verified tar stream without storing another copy, including its headers and padding.
run bash -o pipefail -c 'gzip -dc "$1" | wc -c' -- "$work/bundle.tar.gz" >"$work/unpacked-size" || fail 'Installer archive is incomplete or invalid.'
unpacked=$(cat "$work/unpacked-size")
space "$unpacked"
printf 'Extracting the installer...\n'
mkdir "$work/bundle"
run tar -xzf "$work/bundle.tar.gz" -C "$work/bundle" || fail 'Installer extraction failed. Check disk space, quota and filesystem permissions.'
run "$work/bundle/oac-daemon" install --onboard-url "${base%/install/*}/installation" --authorization "$authorization" "$@"
