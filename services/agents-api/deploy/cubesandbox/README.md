# CubeSandbox managed Runtime profile

This directory holds the operator packaging for the opt-in CubeSandbox backend:
a Runtime image built on the vendor's base image (which supplies `envd`), the
readiness endpoint that doubles as the Cube template probe, and the template
build steps. The provider itself lives in
`services/agents-api/internal/sandbox/cubesandbox/`; the pinned vendor contract
and its reconciliation with the implementation specification live in
`services/agents-api/internal/sandbox/cubesandbox/contract/README.md`.

Provider choice stays operator-owned. Nothing here changes the public API, the
OpenAPI document, Core Web or the pinned protocol: callers still see
`openai_hosted`.

## What the image adds to the Docker profile

| Item | Purpose |
| --- | --- |
| `envd` from the vendor base image on `49983` | Command and file transport the adapter uses during trusted initialization |
| `parsar-runtime-readiness` on `49984` | The Runtime readiness endpoint, the Cube template probe, and the evidence behind `Info.BootstrapComplete` |
| `parsar-runtime-entrypoint` | Starts the readiness endpoint, waits for the managed daemon profile, then execs the daemon |
| The same Runtime bundle as the Docker image | `parsar-daemon`, the Rust helpers, `codex` and its resources, the initialization scripts |

The base image is
`ghcr.io/tencentcloud/cubesandbox-base:2026.16@sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472`.
Re-resolve that digest and update the contract record when the tag moves.

Two deliberate divergences from `deploy/codex/Dockerfile`:

- `sudo` is purged and the base image's passwordless sudoers entry is removed, so
  native tools cannot become root inside the sandbox (specification §8.7, R9).
- The base image's uid/gid 1000 account is renamed to `runtime` with home
  `/home/runtime`, keeping every daemon, helper and profile path byte compatible
  with the Docker profile.

## Build the image

The build context is the prepared Runtime bundle, never the product checkout:

```sh
export AGENTS_RUNTIME_BUILD_DIR=/absolute/path/to/agents-runtime
scripts/build-agents-runtime.sh            # writes parsar-daemon and parsar-runtime-readiness
docker build -f services/agents-api/deploy/cubesandbox/Dockerfile \
  -t agents-runtime-cube:<release> "$AGENTS_RUNTIME_BUILD_DIR"
```

`scripts/build-agents-runtime.sh` requires `AGENTS_RUNTIME_CODEX_PACKAGE` (the
extracted pinned `@openai/codex` platform package) and
`AGENTS_EXECUTOR_BUILD_DIR`, and it builds the readiness endpoint together with
the daemon so both backends share one bundle.

## Local image self-check

Before any template build, confirm inside the image:

```sh
docker run --rm -p 49983:49983 -p 49984:49984 agents-runtime-cube:<release> &
curl -fsS  http://127.0.0.1:49983/health        # envd is up (HTTP 204)
curl -fsS  http://127.0.0.1:49984/healthz       # unprovisioned image is ready
codex --version                                 # codex-cli 0.153.4
```

`/healthz` returning 2xx here is the *unprovisioned* state: the image is intact
and there is no daemon profile to wait for, which is what a template build sees.
Once Core injects a profile in a real sandbox, `/healthz` returns 503 until the
daemon reports an authenticated connection.

## Create the template

Single node (recommended first deployment; CubeMaster reads the local image, so
no registry is involved):

```sh
cubemastercli tpl create-from-image \
  --image agents-runtime-cube:<release> \
  --writable-layer-size 2G \
  --expose-port 49983 \
  --expose-port 49984 \
  --probe 49984 \
  --probe-path /healthz
cubemastercli tpl watch --job-id <job_id>
```

Only the readiness port is probed, so the snapshot is taken when the Runtime
reports readiness rather than when `envd` merely answers. Multi-node deployments
push the same image to a registry the nodes can reach and pass
`registry/ns/image@sha256:...`.

Record in the deployment record: CubeSandbox version and the pinned revision, the
base image digest, the built image digest, the template id and the
`artifact_sha256` reported by `tpl watch`, and the exact create command.

## Prerequisites and limits

- **Host storage.** Every allocation gets one directory,
  `<host_mount_root>/<installation>/<tenant>/<environment>/<allocation>`, mounted
  at `/environment` and its `workspace` subdirectory at `/workspace`. The prefix
  must be allowed in CubeMaster's `allowed_host_mount_prefixes` (default
  `/data/shared/`), must exist on the node, and must be owned by uid 1000. Host
  mounts are node-local, are not copied into snapshots, pin pause/resume to the
  origin node, and are **not** deleted with the sandbox: define an operator
  retention and purge policy.
- **Node capacity.** Idle sandboxes terminate in this slice (there is no pause
  support), so size for concurrent live Sessions and measure density on the target
  hardware. The Docker baseline is 2 vCPU and 2 GiB.
- **Egress.** A Session with network access disabled is created with
  `allow_internet_access=false`, which denies ordinary public egress, while the
  Core host and the `platform_egress` entries stay reachable because the daemon's
  WebSocket to Core and the harness's model calls share that interface. List the
  model endpoints, and any MCP server hosts, in `platform_egress`; otherwise a
  disabled Session cannot execute. Domain-restricted policy is still rejected
  before create.
- **Authentication.** CubeAPI allows anonymous access by default. Configure the
  auth callback before exposing it beyond the operator network; Core sends
  `Authorization: Bearer <key>` from a private file regardless.
- **Isolation.** Native tools must not be able to read the executor credential,
  the private staging area or Core's history, and must be denied `envd`, `sudo`
  and privileged accounts. The vendor base image ships `envd` inside the sandbox
  by design; the isolation probe in the acceptance plan is what proves it is not
  reachable by native tools.

## Not yet verified

This section exists so no reader mistakes packaging for acceptance. On a host
without KVM, or without a CubeSandbox cluster, the following remain unverified
and must be completed before the profile is used for real Sessions:

- the image build itself, the local self-check above, and the version assertion;
- template creation reaching `READY`, and the recorded deployment record;
- the opt-in real-cluster provider checks (`AGENTS_RUNTIME_CUBE_*`, Tier B);
- the public end-to-end acceptance (Tier C), including the native isolation probe;
- Node's major version differs by base OS (Debian bookworm 18, Ubuntu 22.04 12).
  The Codex Runtime does not invoke node directly, but any dependency that does
  must be qualified against the packaged version.
