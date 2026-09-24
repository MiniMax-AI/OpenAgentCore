# Single-host idle microVM suspension

Run the ordinary standalone sandbox node and its microsandbox helper natively on
Linux amd64 with KVM access. Core owns scheduling and durable recovery over the
node protocol; it can run independently in a container or on another host.
PostgreSQL can remain in Docker. The installer’s local microsandbox opt-in still
runs Core as a native user service, but that packaging choice is not a requirement
of the provider architecture.

A hosted Session gets a dedicated microVM with the existing daemon, native harness
and workspace. Core suspends it only after a completed Turn has remained idle and
all admitted work has settled. The next Turn restores the same Session history,
files and configuration. Native harness startup and shutdown retain their existing
behavior. This profile uses microsandbox v0.7.2; the helper performs one finite
operation and exits.

Start with the [installation guide](../../../../docs/getting-started/install.md)
and [Hosted Sandbox Manager](../../HOSTED-SANDBOX-MANAGER.md). The
[deployment configuration contract](../../../../contracts/agents-api/sandbox-deployment.md)
defines the saved selection, resources, Runtime identity and node authorization.
The [provider contract](../../tools/microsandbox-provider/README.md) describes
snapshot identity, uncertain operations and cleanup. Real-model continuation and
isolation still require deployment acceptance.

## Select and change the deployment

Web or the deployment administrator API selects one provider for the installation.
PostgreSQL owns the provider, per-sandbox CPU/memory/disk limits and immutable Runtime
release. Microsandbox nodes install that selection and must match its generation
and specification digest. Node files hold host paths and an installed copy of the
specification; they cannot select different resources or a different Runtime.
Harness selection is independent. Snapshot suspension is available only on
microsandbox; Docker and E2B retain their own supported lifecycle.

Provider, resource and Runtime changes require global maintenance, the current
generation and verified zero retained allocations or pending hosted Environments.
Use the [maintenance procedure](../../HOSTED-SANDBOX-MANAGER.md#removal-and-maintenance).
Stopped compute, snapshots and unknown operations remain blockers. Maintenance
and configuration changes do not delete resources or migrate Sessions. Keep the
original node identity, backend paths and credentials until cleanup is confirmed.
Session deletion removes its saved artifacts and is not a history-preserving
resource-release operation.

Core rejects `AGENTS_API_MANAGED_RUNTIMES_FILE`. It does not automatically adopt an
older file-managed database, even after its resources are drained. Keep the
previous release and original backend available to resolve that deployment;
removing an environment variable does not migrate its configuration ownership.

## Host and binaries

Use a dedicated service account with access to `/dev/kvm`, a C compiler and the
repository's Go version for source builds. The node's helper requires glibc and
the standard Linux dynamic libraries. Core remains a CGO-disabled build. Its
`distroless/static` image does not run the helper; the standalone native node does.
Use the matched distribution's ordinary node installer for an operator installation.

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

For a manual node installation, create a private, short runtime state path, for
example `~/.parsar/msb`, with mode 0700. The ordinary installer instead selects
`~/.parsar/m/<installation-hash-prefix>/` and stores node identity/configuration
under `~/.parsar/nodes/<installation-id>/`. Keep these on persistent local storage
reserved for this installation. Unix socket path limits apply. Runtime storage
contains confidential disks, memory snapshots and SDK state; preserve it with the
node identity and Core database when recovering the host.

## Runtime image

Use the immutable Runtime release saved in the deployment specification. The
ordinary node installer verifies the matched distribution manifest and imports
its microsandbox image under the declared digest reference. The image must be
available to the local microsandbox installation before provisioning. For source
builds, the existing [Runtime image build](../codex/README.md#managed-runtime-image-and-docker-adapter)
remains the image source; produce and select a matching distribution rather than
substituting a local image for an already saved release.

The provider performs the existing Runtime bootstrap, starts the daemon as
uid/gid 1000 and creates `/run/parsar` as a private control directory. No model,
Core or tenant credential belongs in the image. The ordinary Runtime initializer
and native isolation profile still apply; snapshot restore does not rerun setup
commands or initial file writes.

## Deployment and node configuration

Initialize microsandbox through Web or `POST /core/v1/sandbox/deployment`, including
its required `resources` and immutable `runtime` fields. The standalone node
installer reads `GET /core/v1/sandbox/node/configuration` using an enrollment token,
or its retained node credential on a registered reinstall. It verifies the saved
release and resources before registration. Local opt-in uses this same path and
requires a guest-reachable, non-loopback HTTPS `--public-url`.

Set VM CPU, memory and disk limits in the database-owned specification.
`root_disk_mib` bounds the managed root disk; `environment_disk_mib` separately
bounds the owned ext4 disk at `/environment`, including workspace, staging and
initialization data. Both are required. Budget for both disks and retained full
snapshots. Node `max_active` and `max_retained` are separate reservation limits;
retained allocations include suspended snapshots and uncertain cleanup. The
managed microsandbox policy uses a five-minute idle interval and one-day snapshot
retention.

The private node provider file supplies its absolute helper/runtime/firmware paths,
short Runtime home and explicit host network policy. Permit the required Core,
model and package-registry endpoints. Creation and restore apply the same host
policy. Native tool-network policy remains separate and uses the existing Runtime
controls. Never repoint a retained backend namespace or overwrite node identity
to bypass a configuration mismatch.

Use the existing [standalone Core setup](../../README.md) for the database,
migrations, administrator credentials and encryption key. Model provider settings
retain the private `AGENTS_API_EXECUTION_OPTIONS_FILE` contract. The saved public
Core origin must be reachable from the guest; `localhost` in a microVM refers to
the guest itself. Do not configure a Core-local managed-runtimes file.

## Runtime observations

The configured microsandbox provider uses the same public Runtime observation rows
and Core Web Dashboard as Docker. Each request resolves Session, Environment,
allocation, installation and the persisted current compute generation before
invoking the helper. The helper performs one read-only `SandboxHandle.Metrics`
call; it does not wake suspended compute or extend retention.

The current projection includes cumulative vCPU seconds, configured vCPU capacity,
guest memory usage/limit and compute uptime. Suspended or otherwise non-running
compute reports `unavailable` with `runtime_not_running`; metrics that are disabled
or have no current SDK sample report `sample_unavailable`. The SDK's instantaneous
CPU percentage, host RSS, disk, network and overlay measurements are not yet
exposed. Token usage continues to come from Session/Turn usage, not this provider.
An allocation without a persisted exact compute receipt also reports
`sample_unavailable`; the deterministic sandbox name is not sufficient to identify
one compute incarnation safely.

Observation is operational evidence only. The idle suspension state machine uses
its durable activity and compute-phase records and never consults Dashboard samples.

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
