#!/usr/bin/env bash
set -euo pipefail

# Runs the agent-host qualification (apps/daemon/internal/agenthostqualify)
# against the images scripts/build-agent-host-images.sh builds. The test binary
# runs as the agent host in OAC_AGENT_HOST_IMAGE, and OAC_SANDBOX_IMAGE runs
# oac-sandbox-io with the bootstrap the test writes. OAC_QUALIFY_KEY_FILE is the
# model key's file, each OAC_QUALIFY_<KIND> the Harness's model options without
# api_key, and OAC_QUALIFY_PROXY an HTTP proxy for a host whose only egress it
# is. Further arguments go to the test binary.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
agent_host_image="${OAC_AGENT_HOST_IMAGE:?Set OAC_AGENT_HOST_IMAGE to the agent-host image}"
sandbox_image="${OAC_SANDBOX_IMAGE:?Set OAC_SANDBOX_IMAGE to the sandbox image}"
key="${OAC_QUALIFY_KEY_FILE:?Set OAC_QUALIFY_KEY_FILE to the model key file}"
[[ "$key" == /* && -f "$key" ]] || { printf 'Expected the absolute path of the model key file: %s\n' "$key" >&2; exit 1; }

run="$(mktemp -d)"
label="oac.qualify=$(basename "$run")"
cleanup() {
  docker ps -aq --filter "label=$label" | xargs -r docker rm -f >/dev/null
  rm -rf "$run"
}
trap cleanup EXIT
# The sandbox's user reads the bootstrap and the relay's CA here.
chmod 0755 "$run"
(cd "$repo_root" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$run/qualify.test" ./apps/daemon/internal/agenthostqualify)

# The agent host needs a private cgroup namespace, CAP_SYS_ADMIN and
# CAP_NET_ADMIN, /dev/fuse and no AppArmor profile; Docker's default seccomp
# profile stays. The host network lets the sandbox reach the test's relay.
docker run --rm --label "$label" --network host \
  --cgroupns=private --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
  -v "$run:/run/qualify" -v "$key:/run/model.key:ro" \
  -e OAC_TEST_QUALIFY=1 -e OAC_QUALIFY_KEY_FILE=/run/model.key -e OAC_QUALIFY_PROXY \
  -e OAC_QUALIFY_CLAUDE_SDK -e OAC_QUALIFY_CODEX -e OAC_QUALIFY_MCODE \
  --entrypoint /run/qualify/qualify.test "$agent_host_image" -test.v -test.timeout 40m "$@" &
agent_host=$!
while [[ ! -s "$run/bootstrap.json" ]] && kill -0 "$agent_host" 2>/dev/null; do sleep 1; done
sandbox=""
if [[ -s "$run/bootstrap.json" ]]; then
  sandbox="$(docker run -d --label "$label" --network host --tmpfs /workspace:uid=1000,gid=1000 \
    -v "$run:/run/qualify:ro" -e SSL_CERT_FILE=/run/qualify/ca.pem "$sandbox_image" /run/qualify/bootstrap.json)"
fi
status=0
wait "$agent_host" || status=$?
if [[ "$status" != 0 && -n "$sandbox" ]]; then
  printf -- '--- sandbox log\n'
  docker logs "$sandbox" 2>&1 | tail -20
fi
exit "$status"
