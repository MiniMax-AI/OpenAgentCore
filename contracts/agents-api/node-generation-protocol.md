# Node generation protocol

This is the internal, authenticated Core-to-node Provider protocol. It does not
change the pinned public Agents API. `sandbox/node.ProtocolVersion` is the only
accepted wire version; both peers reject historical versions. The current hello
explicitly advertises `generation_management` when the node can prepare and retain
multiple deployment generations. Fixed-configuration manual nodes omit that
capability and serve only their enrolled generation using the same wire protocol.
Every Provider request carries its exact allocation-owned deployment generation;
Core never strips it for an older peer. Automatic preparation and retention frames
are sent only to nodes advertising generation management.

## Provider operation outcomes

Node startup and generation loading validate complete Provider operation
declarations before accepting work. Proxies use the same registered declaration
for admission; unsupported operations reject before node resolution or native I/O.
The Provider [operation contract](../../docs/sandbox-provider.md#explicit-operation-contracts)
owns the inventory and support rules.

A Provider response with `error_code: unsupported` carries an `unsupported` object
containing the exact method `operation` and an authored safe `reason` code. The
proxy checks both against the request. Missing, malformed or mismatched evidence
is an unconfirmed result, never proof that a mutation was rejected. Unsupported
remains distinct from observation unavailability and unknown compute/command
results; it does not settle resource ownership or authorize replay. The current
private wire version requires both peers to understand this outcome.

## Bounded control

A generation-managing node's hello or heartbeat contains at most eight generation observations.
Each names a positive signed-64-bit generation, its lowercase SHA-256 specification
digest, a `ready`, `preparing` or `failed` state, and an optional fixed diagnostic.
The target and serving generation take priority; other records rotate fairly.
Eight bounds one message, not the number of generations a node may retain.
Omitted observations never authorize deletion or imply absence.

Welcome and heartbeat acknowledgements carry the Core-owned target generation,
its specification digest, and an explicitly nullable durable serving generation.
Target preparation is independent of the readiness of a retained serving provider.
A provider request carries its exact immutable `deployment_generation` separately
from the allocation's compute generation.

Retention exchanges use an independent bounded control path. A request identifies
at most eight local `(generation, specification_digest)` references, a UUID request
ID, a monotonically increasing sequence, the current connection UUID and owner
epoch. Its acknowledgement must match the complete pending request, including
entry order and identity, and must explicitly supply a boolean `keep` for every
entry. Only one pending exchange exists per connection. A disconnect discards it;
an unsolicited, replayed, stale, partial or mixed acknowledgement deletes nothing.

Control envelopes are at most 32 KiB. Their member names are exact and unique;
unknown members, case aliases, duplicate members and unexpected nulls are rejected.
The nullable serving pin and host measurements preserve unknown values. Provider
request/response frames retain the existing global size bound; control traffic
does not increase that bound or consume the provider-operation queue.

## Local retention and helper lifetime

A Core drop grant is necessary but insufficient for collection. Queued and running
provider calls, preparation, the local target and serving pin retain references.
Collection rechecks those references and refuses an already canceled connection.
A canceled provider caller does not establish that its native helper has stopped:
the parent counts that helper through its actual `Wait` completion.

Every generation also owns a permanent private lease file at
`state/node/generations/<generation>.lease`. Before starting a native helper, the
node acquires its shared flock and validates the durable lease identity under
that lock. It also requires the matching published final provider configuration
and absence of preparing, collecting and dropped journals. The helper inherits the descriptor. The node closes its own descriptor
only after `Wait`; it never explicitly unlocks the shared open-file description.
Thus caller cancellation or node exit does not release a live helper's reference.
New native helpers set this descriptor close-on-exec before calling the SDK so VM
and daemon descendants do not inherit a helper reference.

Collection acquires the exclusive nonblocking flock before inspecting references,
removing shared images or release files, or publishing the dropped tombstone. It
keeps the lock through those changes. Lock files belong to stable node state and
are never removed or atomically replaced during collection. Symlinks, multiply
linked files, foreign ownership, unsafe permissions and replaced lock paths are
refused. Before the first helper can start, the installer exclusively creates the lease and
fsyncs it, then atomically persists and fsyncs a private `.lease-identity` record and
its parent directory. The record binds installation, generation, specification
digest, device and inode. Python exclusive collection/repair and Go shared helper
openers validate the same record on every open, including after node restart.
Neither opener adopts a missing identity, replaces its inode, or erases
it after GC. Initialization interrupted before the identity is durable refuses
re-adoption; preserve the installation for inspection. A removed identity or an
owned replacement 0600 lease still refuses, even when its current fstat/lstat agree.

A dropped generation cannot be prepared or used again; a future rollback
would require a new generation and a separate policy.

An immutable older native helper may pass its inherited descriptor to descendants.
That conservatively retains its generation's bytes until those descriptors close;
collection must not kill historical VMs or a host daemon merely to reclaim disk.
A helper's exit is local file-lifetime evidence, not proof that a remote mutation
or an uncertain provider receipt has been released. Core's durable allocation and
placement retention requirements remain independent.

## Matched fresh installation

The host program release and Core's selected Runtime release are independent.
A fresh node gets its executable and private preparer from the current console
release. It reads the exact Runtime source, image identities and native runtime /
firmware digests from the authenticated Core configuration. If that Runtime is
older, the console must still serve its immutable `releases/<source>/` manifest,
checksums and allowlisted artifacts. Runtime helper, firmware, seccomp and image
bytes come from that selected release; the enrolled specification records it.
Artifact URLs are pinned to their verified manifest source even if the console's
current release changes during download.

A missing retained release refuses installation rather than substituting the
current Runtime. A local bundle that contains only a different Runtime also
refuses with guidance to use the console origin retaining the selected release.
These refusals occur before writing the installation identity, importing the
Runtime, registering the node or starting its service.

## Restart recovery

Missing retained Runtime bytes do not switch a pinned placement to the current
Runtime. The node retains the original generation and specification digest as
unready state, and asks Core's authenticated configuration endpoint for that exact
kept generation before recovery. Missing seccomp bytes may leave an unready
provider placeholder; missing image or native artifacts discovered by a provider
probe queue repair without advertising readiness.

Preparation and repair are serialized. Target and serving generations take
priority, with bounded progress over other retained generations. Each attempt has
a 30-minute deadline; failures back off for 1, 2, 5, 10 and then at most 30 minutes.
No connection-established deployment facts means no preparation starts. Repair
preserves existing configurations and paths, verifies the selected release and all
existing sibling checksums, and downloads only absent immutable files. Conflicting
bytes or a different retained specification refuse repair. A missing complete
provider configuration without an exact durable preparation plan remains a refusal rather than a guessed reconstruction.

Repair takes the same exclusive generation lease and installation lock used by
collection. A live helper or concurrent collector therefore retains ownership;
repair retries later without replacing in-use files. Once bytes are restored, the
node still runs the actual provider readiness probe. File presence and executable
capability alone never establish serving readiness.

## Interrupted local collection

Before native or release deletion, the node persists a private collection journal
bound to its installation, generation and specification digest. Restart loads an
unfinished journal only as a retention-exchange candidate: it cannot prepare,
probe, acquire or advertise that generation. A fresh correlated Core drop grant
is required to resume; the journal itself never authorizes deletion.

For an installation-private microsandbox store, a successful complete native image
inventory distinguishes absence from a CLI failure. Failed queries, malformed inventories, native in-use refusals and
unknown ownership retain the local bytes. Native completion is persisted before
release-file cleanup, so a retry can finish a partly removed release without
executing an already removed helper. Shared microsandbox images and private releases remain until their last local
reference. Docker imported images belong to the shared host daemon and are retained,
including when another installation has only an idle serving pin. Automatic node
GC never runs Docker image removal or pruning. The host administrator may remove
those images only after confirming that no installation on the host needs them;
local generation collection does not claim physical Docker image GC. The final digest-bound dropped marker follows durable file
cleanup and permanently prevents re-adoption. The small immutable configuration
and ownership journals remain as local identity records.

Fresh installations also persist the verified Runtime file checksums separately
from the host program. Collection of the original generation removes only those
exact private Runtime helper, executable, firmware, seccomp and import-cache files
that no retained configuration references. All remaining files are checked before
the first deletion; unknown hashes, changed bytes, links or missing ownership
metadata refuse cleanup. Interruption resumes under the same collection journal.
The current node executable, preparer, identity, base provider configuration and
manifests remain, so restart can read its enrolled identity and construct a newer
retained provider after the original Runtime bytes have gone. Shared native paths
are compared across all retained configurations before removal.

New preparation has two distinct records. Before downloads, `.preparing` holds the
immutable installation/generation/specification identity, private provider paths
and `import_started:false`; it is a recovery/collection plan, not a published
provider. Before invoking the importer, the same plan records `import_started:true`.
Both Python retention discovery and Go restart recovery recognize pending-only
plans, but never build, probe or acquire a provider from them. Current-connection
Core authorization is still required for recovery or collection.

Only successful preparation publishes the final `.json` provider configuration.
Docker records the actual immutable local ID returned by the resolver; either
builder-proven config or manifest identity can be valid for the same specification.
The final configuration is write-once. Publication is durable before clearing the
preparation journal. Interruption between those steps revalidates the same plan
and existing final identity; it does not permit editing a published configuration.
Plan/spec/path drift refuses. A canceled or failed import remains visible to fresh
Core retention exchange without becoming a serving generation.

An interrupted download repairs only missing bytes at the original paths. If
collection precedes any import attempt, the preparation journal proves that this
generation has no imported native image. An older or interrupted generation whose
native executable is missing and whose import may have started remains retained;
missing files do not prove native absence. Receipt/store history is never
erased using an empty native inventory.

Preparation diagnostics preserve fixed typed causes. Only artifact transfer,
checksum or release-provenance failures report `runtime_download_failed`. A private
preparer exit category communicates that class without parsing stderr; provider,
ownership, cancellation and unclassified failures remain their existing typed
code or `provider_unavailable`. No raw provider text crosses the node protocol.

Published console releases keep immutable metadata and existing artifact bytes.
A rerun first validates all published metadata and every existing declared artifact,
then may atomically add only absent, checksum-matched declared artifacts. This
supports thin-release completion and retained HTTP artifact repair. Any existing
conflict prevents all additions; repair never overwrites a conflicting artifact.

## Node program version policy

Node program upgrades, historical conversion and adoption are not supported.
`node-install.pyz --update` refuses before changing the installation. Preserve old
installation files, credentials, Runtime stores, provider resources and history;
install the current program separately through the ordinary fresh-node enrollment
flow. Reinstallation does not automatically delete or migrate existing data.

Current Runtime generation changes operate within an installation of the current
node program. They do not upgrade that program or establish compatibility with
historical node installations.

## Qualification boundary

Protocol and process tests do not qualify Runtime readiness, VM coexistence,
or native image/store locking. Current-version Runtime behavior requires the
separate exact-artifact KVM and Docker acceptance matrix. Historical helper
behavior remains historical evidence, not a supported upgrade workflow; absence
of local test coverage is not evidence of successful garbage collection.
