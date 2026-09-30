# Single-host idle microVM suspension

Run the ordinary standalone sandbox node and its microsandbox helper natively on
Linux amd64 with KVM access. Core owns scheduling and durable recovery over the
node protocol; it can run independently in a container or on another host.
PostgreSQL can remain in Docker. Core packaging, container or native, is independent
of the provider.

A hosted Session gets a dedicated microVM with the existing daemon, native harness
and workspace. Core suspends it only after a completed Turn has remained idle and
all admitted work has settled. The next Turn restores the same Session history,
files and configuration. Native harness startup and shutdown retain their existing
behavior. This profile uses microsandbox v0.7.2; the helper performs one finite
operation and exits.

Start with the [installation guide](../../../../docs/getting-started/install.md)
and the [nodes guide](../../../../docs/getting-started/nodes.md). The
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

Same-provider resource and Runtime edits currently require no active reset,
the current generation and verified zero held allocations or pending Environments.
A backend change requires explicit reset and confirmed cleanup, then a new setup.
See the [reset procedure](../../../../docs/getting-started/nodes.md#change-the-sandbox-configuration).
Stopped compute, snapshots and unknown operations remain blockers. Keep the original
node identity, paths and credentials until cleanup is confirmed. Explicit archive
preserves history and persisted Files/Artifacts but discards unpersisted workspace;
ordinary Session deletion has different retention behavior. Existing Sessions never
migrate to another backend.


Core rejects `AGENTS_API_MANAGED_RUNTIMES_FILE`. It does not automatically adopt an
older file-managed database, even after its resources are drained. Keep the
previous release and original backend available to resolve that deployment;
removing an environment variable does not migrate its configuration ownership.

## Host and binaries

Use a dedicated service account with access to `/dev/kvm`. The node's helper
requires glibc and the standard Linux dynamic libraries. Core's container image
does not run the helper; the standalone native node does. Use the matched
distribution's ordinary node installer for an operator installation. The
[maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers)
builds the helper and names the checksum-verified `msb` and `libkrunfw.so.5.6.1`
release archive. `make check` runs the pure-Go provider tests on every supported
host. On Linux it also runs the SDK helper module; other hosts print an explicit
skip for that Linux-only module. A full Linux check is required before publishing
this profile.

For a manual node installation, create a private, short runtime state path, for
example `~/.oac/msb`, with mode 0700. The ordinary installer instead selects
`~/.oac/m/<installation-hash-prefix>/` and stores node identity/configuration
under `~/.oac/nodes/<installation-id>/`, both in the home of the account that runs
the node (`/var/lib/oac-node` for a node added with sudo). Keep these on persistent local storage
reserved for this installation. Unix socket path limits apply. Runtime storage
contains confidential disks, memory snapshots and SDK state; preserve it with the
node identity and Core database when recovering the host.

## Runtime image

Use the immutable Runtime release saved in the deployment specification. The
ordinary node installer verifies the matched distribution manifest and imports
its microsandbox image under the declared digest reference. The image must be
available to the local microsandbox installation before provisioning. For source
builds, the [Runtime image build](../../../../docs/maintainers.md#runtime-images-and-helpers)
remains the image source; produce and select a matching distribution rather than
substituting a local image for an already saved release.

The provider performs the existing Runtime bootstrap, starts the daemon as
uid/gid 1000 and creates `/run/oac` as a private control directory. No model,
Core or tenant credential belongs in the image. The Runtime initializer executes
with that user's existing permissions; the microVM is the isolation boundary.
System dependencies belong in the image: Runtime does not run apt or sudo and
rejects `system_packages`. Snapshot restore does not rerun setup or initial files.

## Deployment and node configuration

Initialize microsandbox through Web, `POST /core/v1/sandbox/deployment` (including
its required `resources` and immutable `runtime` fields) or `install.sh --sandbox
microsandbox`. The standalone node installer reads
`GET /api/v1/sandbox-node/configuration` using an enrollment token, or its retained
node credential on a registered reinstall. It verifies the saved release and
resources before registration. The Core host is added the same way, with Add node,
and needs a guest-reachable, non-loopback HTTPS public URL like any other node.

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
policy. The daemon does not enforce `disabled` or `restricted` native network
modes; combinations without required outer enforcement are unsupported. Never repoint a retained backend namespace or overwrite node identity
to bypass a configuration mismatch.

Use the existing [standalone Core setup](../../README.md) for the database,
migrations, administrator credentials and encryption key. Model providers come from
the Session request, a saved Agent or the deployment default stored in Core; see
[model execution](../../../../contracts/agents-api/model-execution.md#deployment-defaults). The saved public
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
oac-daemon resume --control-file /run/oac/daemon-suspend.json \
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
