# Single-host idle microVM suspension

Run Core and its microsandbox helper natively on one Linux amd64 host. PostgreSQL
can remain in Docker. A hosted Session gets a dedicated microVM with the existing
daemon, native harness and workspace. Core suspends it only after a completed Turn
has remained idle and all admitted work has settled. The next Turn restores the
same Session history, files and configuration. Native harness startup and shutdown
keep their existing behavior.

This profile uses microsandbox v0.7.2. The helper runs one bounded operation at a
time and exits; Core owns scheduling and durable recovery. See the
[provider contract](../../tools/microsandbox-provider/README.md) for snapshot
identity, uncertain operations and cleanup rules. This document specifies setup;
real-model continuation and isolation still require deployment acceptance.

## Choose a hosted provider

Choose either the [Docker deployment](../codex/README.md#standalone-operator-configuration)
or this microsandbox deployment during setup. Both adapters implement the same
Core Provider contract. One Core deployment uses one provider, one installation
identity and one backend configuration. Snapshot suspension is available only on
microsandbox. Docker remains supported with its ordinary lifecycle.

Set `provider` to `docker` or `microsandbox` and include only that configuration
object. Provider selection applies to the entire deployment, independently of
harness selection. Legacy provider maps, `default_provider` and `engine_providers`
are rejected. The setup remains a private configuration file and a Core restart;
there is no separate setup service or automatic migration.

## Change the deployment provider

1. Keep the old provider, installation ID and backend path configured. Set
   `maintenance: true` and restart Core. Maintenance blocks new compute; the old
   adapter remains available for observing and explicitly cleaning up resources.
2. Handle or delete the old hosted Sessions and resources explicitly. Confirm all
   retained allocations, including snapshots and pending hosted Sessions, are
   gone. A public deletion acknowledgement alone does not prove physical cleanup.
3. Configure the new provider with a fresh `installation_id`, its backend object
   and `maintenance: true`, then restart Core. Core validates that the old
   deployment is empty before accepting the new identity.
4. Keep the new identity unchanged, set `maintenance: false` and restart Core to
   allow new compute.

The installation ID and backend namespace are persisted. Changing the Docker
socket or microsandbox `runtime_home` counts as a provider switch even if the UUID
is reused. Image, resource limits and idle policy do not change that identity.
Maintenance itself never initiates deletion; ordinary expiry, revocation and
explicit deletion keep their existing cleanup behavior. A switch requires both
the persisted old configuration and incoming configuration to be in maintenance.
Switching never deletes resources automatically or migrates Sessions between
providers. Do not repoint a configured backend path to another installation.

When upgrading a database that has retained allocations but no recorded provider
identity, Core cannot verify their original backend and refuses initial adoption.
Keep the previous Core and its configuration available to finish cleanup before
starting this profile. An empty legacy deployment can select its first provider;
previously unallocated Sessions have no provider ownership to migrate.

## Host and binaries

Use a dedicated service account with access to `/dev/kvm`, a C compiler and the
repository's Go version. The helper requires glibc and the standard Linux dynamic
libraries. The existing Core binary remains a CGO-disabled build. The current
Core `distroless/static` image cannot run this helper.

Build from the repository root:

```sh
make build-agents-api build-daemon build-microsandbox-provider
make check-microsandbox-provider
```

The helper is written to
`~/.parsar/build/microsandbox-provider/agents-api-microsandbox-provider`.
Its separate Go module pins the published SDK and embeds its matching FFI library.
`make check` runs the pure-Go provider tests on every supported host. On Linux it
also runs the SDK helper module; other hosts print an explicit skip for that
Linux-only module. A full Linux check is required before publishing this profile.

Install the Linux x86_64 archive from the official
[v0.7.2 release](https://github.com/superradcompany/microsandbox/releases/tag/v0.7.2)
into a fresh private directory under `~/.parsar/runtime/`. Verify the release
checksum before extraction. The qualified archive is
`microsandbox-linux-x86_64.tar.gz`, SHA256
`47c223e3ef5298abf05f47ed9f87981106e400d99bb3f1d042d4d6881346b18b`.
It supplies `msb` and `libkrunfw.so.5.6.1`. Record each extracted file's SHA256 in
the provider configuration. The helper verifies both files on every invocation;
it does not install or upgrade them.

Create a private, short runtime state path, for example
`~/.parsar/msb`, with mode 0700. Keep it on persistent local storage and reserve it
for this installation. Unix socket path limits apply. The directory holds
confidential disks, memory snapshots and SDK state; retain it together with the
Core database when recovering the host.

## Runtime image

Build the existing [Codex Runtime](../codex/README.md#managed-runtime-image-and-docker-adapter)
with the daemon from this branch, then publish it to an operator-controlled OCI
registry. Configure its immutable `repository@sha256:...` digest. The image must
be available to the local microsandbox installation before provisioning.

The provider performs the existing Runtime bootstrap, starts the daemon as
uid/gid 1000 and creates `/run/parsar` as a private control directory. No model,
Core or tenant credential belongs in the image. The ordinary Runtime initializer
and native isolation profile still apply; snapshot restore does not rerun setup
commands or initial file writes.

## Core configuration

Copy [managed-runtimes.example.json](managed-runtimes.example.json) to a private
file under the service account's `~/.parsar/` directory and set mode 0600. Replace
all placeholder paths, hashes, image digest and hostnames. Generate a fresh
installation UUID for `installation_id`. Keep that identity and its original
backend while any allocation needs cleanup. Set `provider: "microsandbox"` and
include the single `microsandbox` object; do not include a `docker` object.

Set VM memory, CPU and disk limits explicitly. `max_active` bounds active compute.
`max_retained` bounds all retained allocations, including suspended snapshots, and
must be at least `max_active`. `idle_seconds` is the sustained idle interval before
suspension.
`retention_seconds` bounds retained snapshots. Each value must be positive.
Choose capacity and retention for the host's RAM and disk budget.

The example denies network access except HTTPS to the named Core and model
endpoints. Adapt those explicit rules to the deployment, including required
package registries if initialization uses them. Both creation and restore apply
the same host network policy. Native tool-network policy remains separate and
continues to use the existing Runtime controls.

Use the existing [standalone Core setup](../../README.md) for the database,
migrations, API keys and encryption key. Add these variables to its private
service environment:

```sh
export AGENTS_API_MANAGED_RUNTIMES_FILE="$HOME/.parsar/managed-runtimes.json"
export AGENTS_API_DAEMON_WS_URL='wss://core.example/api/v1/agent-daemon/ws'
export AGENTS_API_ENGINE=codex
"$HOME/.parsar/build/agents-api/agents-api"
```

The outward Core URL must be reachable from the guest. `localhost` inside the
microVM refers to the guest. Model provider settings continue to use the existing
private `AGENTS_API_EXECUTION_OPTIONS_FILE` contract.

## Park, restore and verification

Core first reserves the idle allocation. The daemon rejects suspension while
Turns, preparations, input receipts or file operations remain unsettled. After
its quiesce acknowledgement, it closes the old socket and parks. Core captures
and verifies the full snapshot before stopping the source VM and reclaiming RAM.

Restore creates the precommitted next compute generation. Core invokes the
existing daemon binary inside that exact VM:

```sh
parsar-daemon resume --control-file /run/parsar/daemon-suspend.json \
  --environment-id ENVIRONMENT_ID --suspend-id SUSPENSION_ID
```

This is Core's recovery action, not a second service. The command validates the
protected Environment/suspension identity and the daemon's PID/start time before
signalling through a Linux pidfd. The daemon reauthenticates and requires Core's
matching resume confirmation before admitting work. Each reconnect attempt is
limited to 30 seconds; transient failures retry the same suspension with backoff.
Authentication or protocol rejection terminates recovery. Ordinary disconnects
after resume retain the existing cleanup behavior. A failed capture may roll back the still-running
source through the same controlled wake path.

For acceptance, complete a real-model Turn that writes a known file, wait for
suspension and confirm the source VM has stopped and released RAM. Submit the next
Turn in the same Session and verify the prior history, exact file bytes and frozen
configuration. Confirm setup ran once and no prior input or tool operation was
replayed. Exercise Core restart during capture/restore, pending-work exclusion,
credential revocation and Session deletion with both live and suspended compute.
Keep the resulting evidence separate from unit-test results.
