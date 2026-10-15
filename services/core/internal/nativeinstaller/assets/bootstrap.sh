#!/usr/bin/env bash
set -euo pipefail
base=$1
shift
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
# Resume after resets and fail only when a transfer stalls, with no total time cap. A partial file is kept in the download directory for the next run.
fetch() {
  local url=$1 destination=$2 limit=$3 status code attempt
  for attempt in 1 2 3 4 5; do
    code=0
    run curl "${progress[@]}" --fail --location --proto-redir =https --connect-timeout 15 --speed-time 60 --speed-limit 1024 --continue-at - --max-filesize "$limit" --write-out '%{http_code}' "$url" -o "$destination" >"$work/http-status" || code=$?
    status=$(cat "$work/http-status")
    [ "$code" -ne 0 ] || return 0
    case "$status" in 404) fail 'This Core has no qualified installer for this platform.';; esac
    case "$code" in
      23) fail 'Cannot write the installer download. Check disk space, quota and directory permissions.';;
      63) fail 'Installer download exceeds the available space or metadata size limit.';;
      33) rm -f "$destination";; # The server cannot resume; start the file again.
    esac
    case "$code:$status" in
      6:*|7:*|16:*|18:*|28:*|33:*|35:*|52:*|55:*|56:*|92:*|22:408|22:429|22:500|22:502|22:503|22:504)
        if [ "$attempt" -lt 5 ]; then printf 'Download interrupted; retrying (%s/4)...\n' "$attempt" >&2; run sleep "$attempt"; continue; fi;;
    esac
    fail "Installer download failed (HTTP ${status:-unknown}, curl $code). Check the network to Core and GitHub (set HTTPS_PROXY if a proxy is required), then rerun the command; downloaded data is kept and resumed."
  done
}
# Reject a rejected grant before any large download. Only a 401 stops here; any other answer is reported by the installer.
check_grant() {
  [ -n "${OAC_INSTALL_GRANT:-}" ] || return 0
  local status
  printf 'header = "Authorization: Bearer %s"\n' "$OAC_INSTALL_GRANT" | run curl -sS --config - --request POST --connect-timeout 15 --max-time 30 --write-out '%{http_code}' --output /dev/null "${base%/install/*}/installation" >"$work/http-status" 2>/dev/null || :
  status=$(cat "$work/http-status")
  [ "$status" != 401 ] || fail 'Installation authorization rejected or expired; copy a fresh command from the Session.'
}
verified() {
  run "${hash[@]}" "$1" >"$work/digest" || return 1
  local actual rest
  read -r actual rest <"$work/digest"
  [ "$actual" = "$expected" ]
}
space 0
check_grant
printf 'Downloading the installer matched to Core...\n'
fetch "$base/$os-$arch.sha256" "$work/checksum" 1024
expected=$(cat "$work/checksum")
[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || fail 'Core returned an invalid installer checksum.'
# The verified archive outlives this run so a later failure, such as a wrong option, needs no new download; it is removed once the installation completes.
archive=$cache/$expected.tar.gz
for stale in "$cache"/*.tar.gz "$cache"/*.tar.gz.part; do
  case "$stale" in "$archive"|"$archive.part") ;; *) rm -f "$stale";; esac
done
if [ -f "$archive" ]; then
  printf 'Verifying the installer archive from an earlier run...\n'
  verified "$archive" || { rm -f "$archive"; printf 'The earlier archive failed verification and was discarded.\n' >&2; }
fi
if [ ! -f "$archive" ]; then
  [ ! -s "$archive.part" ] || printf 'Resuming the earlier download...\n'
  available=$(df -Pk "$work" | awk 'NR==2 {print $4}')
  fetch "$base/$os-$arch.tar.gz" "$archive.part" "$((available * 1024 - 67108864))"
  printf 'Verifying the installer archive...\n'
  verified "$archive.part" || { rm -f "$archive.part"; fail 'Installer checksum mismatch; the download was discarded. Rerun the command to download it again.'; }
  mv "$archive.part" "$archive"
fi
printf 'Checking extraction space...\n'
# Count the verified tar stream without storing another copy, including its headers and padding.
run bash -o pipefail -c 'gzip -dc "$1" | wc -c' -- "$archive" >"$work/unpacked-size" || { rm -f "$archive"; fail 'Installer archive is incomplete or invalid; it was discarded. Rerun the command to download it again.'; }
unpacked=$(cat "$work/unpacked-size")
space "$unpacked"
printf 'Extracting the installer...\n'
mkdir "$work/bundle"
run tar -xzf "$archive" -C "$work/bundle" || fail 'Installer extraction failed. Check disk space, quota and filesystem permissions.'
[ -x "$work/bundle/oac-daemon" ] || fail 'Cannot start the native installer. Check executable permissions and use a Runtime home that permits execution.'
if run "$work/bundle/oac-daemon" install --onboard-url "${base%/install/*}/installation" "$@"; then
  rm -f "$archive"
  exit 0
else
  code=$?
  case "$code" in
    126|127) fail 'Cannot start the native installer. Check that the Runtime home permits execution (not a noexec mount) and that this host has the required native libraries.';;
    *) exit "$code";;
  esac
fi
