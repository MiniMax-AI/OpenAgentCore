# microsandbox provider helper

This Linux-only, one-operation helper links the maintained microsandbox Go SDK
v0.7.2. Core stays a pure-Go binary and uses the provider-neutral
`sandbox.CheckpointProvider` interface. There is no helper daemon, local lifecycle
database, native agent adapter, or additional scheduler.

Managed creation checks the native CPU, memory, root disk and owned Environment
disk configuration before bootstrap. Inspection and restore reject resource or
image drift while ownership-based compute and snapshot deletion remain available.
Full snapshots record a resource proof only after the source's limits match.
The pinned native restore leaves managed root size unspecified in its config;
the verified snapshot ancestry and matching proof establish inherited capacity.
The restored target keeps that proof after its CPU, memory and Environment disk
are checked. This does not claim a new direct root-capacity measurement on restore.

The ordinary standalone sandbox node runs this helper natively on Linux amd64,
under a dedicated service user with KVM access. Core owns lifecycle intent through
the node protocol and can run in a container or on another host. Core's
container image does not execute this helper; only the node does. Core packaging,
container or native, is independent of the provider.

PostgreSQL owns the provider, per-sandbox resources and immutable Runtime release.
The node installs that specification and retains its generation and digest; local
provider files cannot override it. See the
[deployment contract](../../../../contracts/agents-api/sandbox-deployment.md),
[nodes guide](../../../../docs/getting-started/nodes.md) and
[nodes operator reference](../../HOSTED-SANDBOX-MANAGER.md). Core no longer accepts a
file-managed startup selection or automatically adopts an older file-managed
database.

## Build and installation

From this directory, using the repository Go version:

```sh
GOWORK=off CGO_ENABLED=1 go build -mod=readonly -trimpath -o "$HOME/.oac/bin/oac-microsandbox-provider" .
GOWORK=off go test ./...
```

The relative replacement for the parent Core module refers to this checkout.
The SDK is the real published module, pinned in go.mod and go.sum; it has no
local-source replacement. The normal build embeds its matching FFI library.
Do not build production with the SDK's development `microsandbox_ffi_path` tag.

Install the matching v0.7.2 msb runtime and firmware from checksum-verified release
artifacts. Supply absolute helper/runtime/firmware paths and expected SHA256
values in the trusted provider Config. The helper checks the runtime and firmware
hashes, the ELF runtime version section, the SDK module version, and the resolved
local backend. It never auto-installs or upgrades these artifacts. The installed
paths must remain immutable for the lifetime of the provider key.

Use a dedicated, private (0700), short MSB_HOME on local persistent storage.
The upstream runtime uses Unix sockets, so a short path such as
`/var/lib/oac-msb` avoids pathname limits. Never share that home with another
installation, cloud profile, or manual lifecycle controller. Its VM disks,
snapshots, SDK state and credentials are confidential execution-service data.
All managed sandbox lifecycle mutations must go through the provider.

An immutable OCI image must already be available as `repository@sha256:<64 lowercase hex>`. Bare Docker image IDs and mutable tags are rejected.
For offline archives, `msb image load --tag repository@sha256:<digest>` must register
the digest reference explicitly; loading a mutable tag alone does not create it.
Use the manifest digest from `image inspect`, not the Docker image config ID.
The qualified image contains our existing daemon, Python 3, native harness and
shared Runtime helpers. Provider Config carries the saved VM resources and the
node's explicit host network policy. Its Runtime and firmware hashes must match
the database-owned release. This adapter does not install registry credentials.

## Bootstrap and network

The SDK creates a private owned ext4 disk at `/environment`, with explicit
`environment_disk_mib` capacity. Workspace, staging and generated outputs share
that filesystem, preserving the existing cross-device and link checks. The
layered root filesystem can report different device IDs for directories and
upper-layer files and is not used for workspace storage. Native full snapshots
and sandbox removal capture, restore and reclaim the owned disk; no host path or
external volume lifecycle is introduced. Creation starts in `/` until bootstrap
creates the workspace directories.

VM creation does not implicitly run the OCI ENTRYPOINT. Before admitting native
work, the helper uses confidential stdin to install the existing private
`auth.json` format, create Runtime directories, bind the same workspace at
`/workspace`, and invoke the existing daemon's `connect --profile default -b`
mode as uid/gid 1000. The final provider-owned bootstrap label confirms only
completion of these writes and launch, not authentication or native readiness.
The receipt label uses the supported next-start modification policy to update
persisted metadata without restarting the guest. v0.7.2 cannot update active labels.

A private helper response can carry `CreateSettled` with a configuration rejection.
This proof is emitted only after native Create has positively completed, the first
inspection has verified the exact created compute ID and ownership, and resource
qualification rejects it before bootstrap begins. The adapter preserves the
original error and validates that initial compute identity before passing the
proof to Core. It does not mark the sandbox ready. Ordinary inspection, uncertain
Create outcomes, timeouts and ownership failures cannot acquire this proof. Core
still requires authorized, ownership-checked cleanup before releasing the
allocation; missing compute alone never proves creation settled.

The private daemon control directory is `/run/oac` (0700, uid/gid 1000).
`OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE=/run/oac/daemon-suspend.json` enables the
daemon's separately owned idle park/wake control. RunCommandCompute can execute
the exact daemon resume command authorized by Core; it never uses pkill.

Creation and full restore both receive the same explicit, trusted host policy.
Upstream restore defaults to Public rather than inheriting source host access.
Native tool-network policy remains the existing Runtime responsibility. No
default external mount inheritance, missing-resource allowance, or policy widening
is used.

## Lifecycle contract

Core persists operation IDs, source/target generations, exact identities and
snapshot evidence before depending on them. `Initial` and `NewCompute` only
construct references; they allocate nothing. Names are installation/allocation/
generation-derived and must never be reused for another incarnation.

Suspend pauses the exact VM, captures a full snapshot under the persisted
operation's derived group/member, verifies its complete checkpoint closure, then
force-stops the source. Pausing alone is not suspension or memory reclamation.
A completed matching artifact is inspected rather than captured again.

After a lost response, Core uses `ObserveOnly`. This never starts capture or
restore, and a suspended-operation observation never kills its source. Core can
persist recovered snapshot evidence before KillCompute. Observation verifies
artifact integrity and source ownership independently of execution qualification,
so resource drift cannot hide a retained artifact from cleanup. If the artifact
is absent but the exact source is still running or paused with settled bootstrap,
observation returns that source with no snapshot. Core can then abort suspension;
thawing and subsequent execution still require resource qualification. Missing
state never authorizes replay of the original operation.

Restore verifies the exact artifact and creates the precommitted target name.
Existing targets are adopted only when their immutable ID (if known) and
persisted `snapshot_parent` agree. Fresh restore and retry share completion:
verify the original artifact and resource proof, inspect the running target's
actual resources and ancestry, persist its missing derived resource-proof label,
then strictly reread the same native ID. `ObserveOnly` may finish this receipt
after an interrupted restore; it cannot restart a stopped target, change resources
or issue another Restore. A conflicting proof remains an error. Native restore
does not inherit source ownership labels; ancestry supplies that evidence.
Unfinished restore intent is not bootstrap completion. There is no ordinary Start,
replacement, disk-only restore, or cold-boot fallback.

`ResumeCompute` only thaws the same resident source after an aborted suspension.
The pinned Go handle method is name-based; the allocation flock plus ID checks
before and after the call fence every managed replacement. External manual
lifecycle changes in the managed namespace are unsupported.

A helper holds an allocation flock until its lifecycle SDK call actually settles.
Core's response deadline does not kill that helper or cancel its FFI wait, since
cancelling the wait does not prove the native mutation stopped. On timeout Core
retains an unknown operation and observes it; a subsequent helper cannot race
past the surviving lock holder. A stuck owner needs operator investigation,
not automatic lock deletion or another create.

KillCompute checks the precise incarnation before stopping and removing its writable
disks. Core calls it after persisting the verified snapshot, even if Suspend
already stopped the source. GetCompute, commands, cleanup and the next suspension
verify restored provenance from persisted VM config after the consumed artifact
has been deleted.
DeleteSnapshot accepts only the derived operation selector and matching full
artifact identity, not an arbitrary path. Core owns retention, consumed snapshot
generations and cleanup ordering. A checkpoint must never roll back work admitted
after its first restore.

RunCommand/RunCommandCompute carry stdin on anonymous pipes, fix the guest user
to 1000, impose a deadline and 1 MiB per-stream output limits, and require both
successful stdin completion and an explicit guest exit before returning a result.
Timeouts, output overflow and missing receipts return ErrCommandUnconfirmed.
Closing an SDK exec handle alone does not prove the guest process exited.

The read-only metrics operation holds the allocation lock and verifies the exact
compute ID through the SDK before and after `msb metrics NAME --format json`.
The CLI is the same checksum/version-pinned runtime binary. Its registry report
preserves the native sample timestamp and fractional-second uptime; parsing both
at native millisecond precision reconstructs the real run start consistently
across polls and Core restarts, while new runs have a new start. The Go SDK v0.7.2
projection drops that timestamp and truncates uptime to whole seconds, so it
cannot supply this fence. Sandbox creation time is not a run start time.

The helper returns only the native observation time, exact uptime, cumulative
vCPU time and guest memory usage/limit. It rejects stale/exited reports and
malformed or missing fields. CLI output is bounded; command failures expose only
an unavailable code, never native diagnostics. Observation does not connect to
the guest, renew activity, resume paused compute or mutate lifecycle state.

The pinned source evidence is tag `v0.7.2`, commit
`1c59b8dbf0ad47dda2f807c0214b529aceb81c74`: `crates/metrics/lib/registry.rs`
reads `sampled_at_unix_ms` and `started_at_unix_ms` coherently and subtracts them
for uptime; `crates/cli/lib/commands/metrics.rs` serializes the timestamp and
`uptime.as_secs_f64()`. The private native fields and identifiers do not cross
the helper boundary.

## Acceptance boundary

The feature is idle-only: Core must reserve a terminal Session with no pending
work before parking its daemon and suspending compute. The next Turn uses the
same Session history, files and configuration without replaying initialization.
This adapter does not promise that a native agent process persists across Turns.

The following evidence describes earlier bounded single-host qualifications. It
does not qualify the current database-managed node installation or every resource
profile; those require their own acceptance evidence.

SDK feasibility separately qualified exact IDs, full snapshot verification,
snapshot_parent, source memory release, tmpfs/RAM restoration and stale-handle
pause rejection. The published Go module's SDK sources and Linux amd64 FFI were
compared byte-for-byte with that qualification source/runtime. The FFI SHA256 was
`ed04ca4788c1c400e1b67040e964fbfcb3743d31d969d1b426dfb95afe7271d8`.

Production helper qualification completed two full capture/restore cycles, removing
each source before restore and deleting each consumed artifact before the next
cycle. Workspace bind identity, uid 1000, private auth permissions and file content
survived. The real Core/Codex synthetic-model test separately completed two Turns
across idle suspension and a Core restart. Synthetic responses establish the
control flow, not real-model acceptance.

The 2026-09-22 Linux amd64 live acceptance used microsandbox/Go SDK v0.7.2,
libkrunfw 5.6.1, Codex CLI 0.153.4 and the official Kimi K3 Responses API. The
unchanged native harness completed two real Turns in one Session across automatic
idle suspension, source removal and a Core restart. Generation 1 restored the
exact full snapshot; the consumed artifact was removed. The model's shell tool
read the original random file marker, retained environment configuration and one
initialization record, then correctly recalled the first request from history.
Retrying the second public input with the same idempotency key created no extra
Turn or tool side effect. A subsequent public file upload/list woke generation 2
without starting another Turn. Public Session deletion released its allocation;
all qualification VMs and snapshots and temporary credentials were removed.
That run qualified its historical model/profile, not the current installation
path, every provider or broader isolation guarantees.

The source VM was observed at 334304 KiB RSS before suspension and with zero RSS
and no executable after exit. The qualification container's PID 1 left a zombie
PID; it retained no VM memory. The separate backend RAM probe also checked live
anonymous memory and tmpfs contents after full restore. A paused resident VM alone
would not satisfy either memory-release check.

Private qualification evidence is grouped as `ram-03` (backend), `provider-01`
(production helper), `core-01` (synthetic Core) and `live-core-03` (real model).
These are bounded single-host checks. They do not establish cold-image download
latency, tail latency, production capacity, or general native-process persistence
across Turns.
