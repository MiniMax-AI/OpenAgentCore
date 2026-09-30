# microsandbox Sandbox Provider helper

microsandbox runs each hosted Session in its own microVM on a Linux amd64 node with KVM, and it is the Sandbox Provider that supports idle suspension. Core forwards provider operations to the node over the [node protocol](../../../../contracts/agents-api/node-generation-protocol.md); the node's adapter ([`sandbox/microsandbox`](../../internal/sandbox/microsandbox)) runs this helper once per operation. The helper links the microsandbox Go SDK v0.7.2 with its FFI library, so Core and the node program stay CGO-free Go binaries. It implements the provider-neutral `sandbox.CheckpointProvider` with full snapshots. It has no daemon, lifecycle database, scheduler or network control plane.

[Add a Sandbox Provider](../../../../docs/sandbox-provider.md) owns the provider contract. [Sandbox deployment](../../../../contracts/agents-api/sandbox-deployment.md) owns the resources, Runtime release and suspension policy; the [nodes guide](../../../../docs/getting-started/nodes.md) owns node installation, host requirements, the node's directories and its network policy.

## Installation checks

The [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds the helper and packages the checksum-verified `msb` runtime and `libkrunfw` firmware. The node's provider configuration, written by the node installer, supplies the absolute helper, runtime and firmware paths with their SHA-256 values, the runtime home, the Runtime image reference, the saved resources and the host network policy.

Before every operation the helper checks that it was built with the published SDK module v0.7.2 without a replacement, that the runtime and firmware match their hashes, that the runtime's `.msbver` ELF section reports the SDK version and that the SDK resolves exactly those paths with the local backend ([`main.go`](main.go)). It never installs or upgrades these files; keep them unchanged for the lifetime of the provider's backend. Ambient SDK profiles are ignored.

The runtime home must be private (mode 0700), short, on local persistent storage and used by no other installation, profile or manual lifecycle tool. microsandbox uses Unix sockets there, so the node installer refuses a home whose path would exceed their limit. The home holds confidential VM disks, memory snapshots and SDK state; preserve it with the node identity and Core's database when recovering a host. Every managed lifecycle change goes through the provider.

The Runtime image is an immutable `repository@sha256:<64 lowercase hex>` reference that matches the saved Runtime release; bare image IDs and mutable tags are rejected. The node installer imports the distribution's image under that reference. To load an image by hand, `msb image load --tag repository@sha256:<digest>` must register the digest reference explicitly, with the manifest digest from `image inspect`, not the Docker image config ID. The image carries the daemon, Python 3, the native Harnesses and the shared Runtime helpers. The provider installs no registry credentials.

## Create and bootstrap

Create names the VM from a hash of the installation and allocation reference plus the compute generation; a name never serves another incarnation. It creates the VM with the saved CPUs and memory as both initial and maximum, a managed root disk of `root_disk_mib`, an owned ext4 disk of `environment_disk_mib` mounted at `/environment`, user 1000:1000, working directory `/` and the node's network policy ([`bootstrap.go`](bootstrap.go)). The resource checks run before bootstrap.

Workspace, staging and outputs share the `/environment` filesystem, which keeps the Runtime's cross-device and link checks intact; the layered root filesystem can report different device IDs for a directory and its upper-layer files, so it holds no workspace data. Full snapshots and sandbox removal capture, restore and reclaim this disk; there is no host path, external mount or separate storage lifecycle.

VM creation does not run the image's entry point. The bootstrap runs as root through confidential standard input, with a two-minute limit. It creates the Runtime directories and the private control directory `/run/oac` (mode 0700, owned by UID 1000), writes the [Runtime bootstrap](../../../../docs/runtime-bootstrap.md) file to `/home/runtime/runtime-bootstrap.json`, bind-mounts `/environment/workspace` at `/workspace` and starts `oac-daemon connect --bootstrap-file` in the background as UID/GID 1000. `OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE=/run/oac/daemon-suspend.json` enables the daemon's park and wake control. The `io.oac.bootstrap` label then changes from `pending` to `complete` through the SDK's next-start modification policy, because v0.7.2 cannot update the labels of a running VM. That label confirms only these writes and the launch, not authentication or native readiness.

A helper response carries `CreateSettled` with a configuration rejection only after native Create has completed, the first inspection has verified the exact compute ID and ownership, and the resource check has rejected the VM before bootstrap started. The adapter keeps the original error and validates the compute identity before passing the proof to Core. Ordinary inspection, uncertain Create outcomes, timeouts and ownership failures never produce it, and missing compute alone never proves that Create settled.

## Checkpoint lifecycle

Core persists operation IDs, source and target generations, exact identities and snapshot evidence before it depends on them. `Initial` and `NewCompute` only construct references and allocate nothing.

- **Suspend** pauses the exact VM, captures a full snapshot under the persisted operation's derived group and member, verifies the complete checkpoint closure, then force-stops the source. Pausing alone does not release memory. A completed matching artifact is inspected instead of captured again. A full snapshot records a resource proof only after the source's limits match.
- **KillCompute** checks the precise incarnation before it stops the VM and removes its writable disks. Core calls it after it has persisted the verified snapshot, even when Suspend already stopped the source, so no chain of old writable disks grows across suspension cycles.
- **Restore** verifies the exact artifact and creates the precommitted target name. An existing target is adopted only when its immutable ID, if known, and its persisted `snapshot_parent` agree. Upstream restore defaults to public networking, so restore passes the same explicit host policy as creation, and no undeclared host resource or mount is inherited.
- Native restore leaves the managed root size unset because the target inherits the verified full snapshot. The helper accepts that only with a matching snapshot resource proof and the exact source and target identities, and it checks the target's CPU, memory and Environment disk before keeping the inherited proof. A missing root size never counts as unlimited capacity, and retained state is never resized.
- Fresh restore and retry share one completion: verify the original artifact and resource proof, inspect the running target's resources and ancestry, persist its missing derived resource-proof label, then strictly reread the same native ID ([`restore_completion.go`](restore_completion.go)). A conflicting proof is an error. Native restore does not copy the source's ownership labels; ancestry supplies that evidence. There is no ordinary Start, replacement, disk-only restore or cold boot.
- **ResumeCompute** thaws the same resident source after an aborted suspension. The pinned SDK handle method is name-based; the allocation lock and ID checks before and after the call fence every managed replacement. Manual lifecycle changes in the managed namespace are unsupported.
- **DeleteSnapshot** accepts only the derived operation selector and the matching full artifact identity, never an arbitrary path. Core owns retention, consumed snapshot generations and cleanup order. A checkpoint never rolls back work admitted after its first restore.

After a lost response Core uses `ObserveOnly`. It never starts a capture or restore, and observing a suspend operation never kills its source. Observation checks artifact integrity and source ownership independently of resource checks, so resource drift cannot hide a retained artifact from cleanup; Core can persist recovered snapshot evidence before KillCompute. If the artifact is absent but the exact source is still running or paused with a settled bootstrap, observation returns the source without a snapshot and Core can abort the suspension; thawing and further execution still require the resource checks. For an interrupted restore, `ObserveOnly` may finish the missing resource proof on the exact target but never restarts a stopped target, changes resources or restores again. Missing state never authorizes a replay.

GetCompute, commands, cleanup and the next suspension verify restored provenance from the persisted VM configuration after the consumed artifact is deleted.

## Locks and commands

A helper holds a per-allocation lock, under `oac-locks/` in the runtime home, until its SDK call actually settles. Core's response deadline neither kills the helper nor cancels its FFI wait, because cancelling the wait does not prove that the native mutation stopped. On a timeout Core keeps an unknown operation and observes it; a later helper cannot pass the surviving lock holder. A stuck owner needs operator investigation, not lock deletion or another Create.

`RunCommand` and `RunCommandCompute` pass standard input on an anonymous pipe, run as UID 1000 with a deadline and a 1 MiB limit per output stream, and return a result only after the input is fully written and the guest reports its exit. Timeouts, output overflow and missing receipts return `ErrCommandUnconfirmed`; closing an SDK exec handle does not prove that the guest process exited ([`command.go`](command.go)). Core uses `RunCommandCompute` only to run the exact wake command it authorizes:

```sh
oac-daemon resume --control-file /run/oac/daemon-suspend.json \
  --environment-id ENVIRONMENT_ID --suspend-id SUSPENSION_ID
```

The command checks the protected Environment and suspension identities and the parked daemon's PID and start time, then signals it through a Linux pidfd. The daemon reauthenticates and waits for Core's resume confirmation before it admits work.

## Metrics

The read-only metrics operation holds the allocation lock and verifies the exact compute ID through the SDK before and after it runs `msb metrics NAME --format json` with the same pinned runtime binary ([`metrics.go`](metrics.go)). The CLI report keeps the native sample timestamp and fractional-second uptime, so the helper reconstructs one run start consistently across polls and Core restarts, and a new run gets a new start. The Go SDK's projection drops the timestamp and truncates uptime to whole seconds, so it cannot provide this; sandbox creation time is not a run start time. In the pinned source (`v0.7.2`, commit `1c59b8dbf0ad47dda2f807c0214b529aceb81c74`), `crates/metrics/lib/registry.rs` reads `sampled_at_unix_ms` and `started_at_unix_ms` together and subtracts them for uptime, and `crates/cli/lib/commands/metrics.rs` serializes the timestamp and `uptime.as_secs_f64()`.

The helper returns only the native observation time, exact uptime, cumulative vCPU time and guest memory usage and limit. It rejects stale or exited reports and missing or malformed fields, bounds the CLI output and reports failures only as an unavailable code, never native diagnostics. Metrics never connect to the guest, renew activity, resume paused compute or change lifecycle state. [Runtime observability](../../../../contracts/agents-api/runtime-observability.md) owns the mapping to observations.

## Tests

`make check-microsandbox-provider`, part of `make check`, runs the pure-Go adapter tests everywhere and this module's tests on Linux; other hosts print an explicit skip for the Linux-only module.
