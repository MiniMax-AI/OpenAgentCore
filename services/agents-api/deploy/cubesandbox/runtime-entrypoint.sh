#!/bin/sh
# Managed Runtime entrypoint for the CubeSandbox image.
#
# The vendor base image entrypoint (docker/cube-entrypoint.sh from the pinned
# CubeSandbox revision) starts envd in the background on ${ENVD_PORT:-49983} with
# -isnotfc, then execs this script as the foreground process. This script keeps
# that contract and adds what a Core-managed Runtime needs:
#
#   1. start the Runtime readiness endpoint, which is also the Cube template
#      probe (apps/parsar-runtime-readiness);
#   2. wait for the managed bootstrap to write the daemon auth profile, because a
#      sandbox boots from a template snapshot before Core has injected
#      credentials, and starting the daemon earlier would only fail;
#   3. exec the daemon as the foreground process so tini reaps it.
#
# During a template build no profile ever appears: the readiness endpoint reports
# image readiness on its own, Cube probes it, takes the snapshot, and this wait
# continues from that snapshot inside a real sandbox.
set -eu

PARSAR_HOME="${PARSAR_HOME:-/home/runtime/.parsar}"
DAEMON_PROFILE="${PARSAR_DAEMON_PROFILE:-default}"
READINESS_BIN="${PARSAR_RUNTIME_READINESS_BIN:-/usr/local/bin/parsar-runtime-readiness}"
DAEMON_BIN="${PARSAR_RUNTIME_DAEMON_BIN:-/usr/local/bin/parsar-daemon}"
PROFILE_DIR="${PARSAR_HOME}/parsar-daemon/${DAEMON_PROFILE}"
PROFILE_FILE="${PROFILE_DIR}/auth.json"
# Bounded so a sandbox whose bootstrap never happens does not wait forever; the
# platform TTL reclaims it either way.
WAIT_SECONDS="${PARSAR_DAEMON_PROFILE_WAIT_SECONDS:-1800}"

start_readiness() {
    if [ ! -x "${READINESS_BIN}" ]; then
        echo "runtime-entrypoint: readiness endpoint missing at ${READINESS_BIN}" >&2
        exit 127
    fi
    "${READINESS_BIN}" &
    echo "runtime-entrypoint: readiness endpoint started (pid=$!)" >&2
}

wait_for_profile() {
    waited=0
    while [ ! -f "${PROFILE_FILE}" ]; do
        if [ "${waited}" -ge "${WAIT_SECONDS}" ]; then
            echo "runtime-entrypoint: no daemon profile after ${WAIT_SECONDS}s" >&2
            return 1
        fi
        sleep 1
        waited=$((waited + 1))
    done
    echo "runtime-entrypoint: daemon profile present after ${waited}s" >&2
}

start_readiness
wait_for_profile

if [ ! -x "${DAEMON_BIN}" ]; then
    echo "runtime-entrypoint: daemon missing at ${DAEMON_BIN}" >&2
    exit 127
fi
echo "runtime-entrypoint: starting daemon profile ${DAEMON_PROFILE}" >&2
exec "${DAEMON_BIN}" connect --profile "${DAEMON_PROFILE}"
