---
title: Workspace filesystem providers
---

The independent workspace filesystem boundary is defined in [`workspacefs.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/workspacefs/workspacefs.go). This document specifies the required integration contract; it does not establish that every Sandbox Provider or placement implements it. Filesystem adapters own storage objects and native attachment resolution. [Sandbox Providers](sandbox-provider.md) own compute. Core selects and validates their combination before allocating either resource.

## Identity and configuration

`Reference` contains immutable, canonical lowercase, nonzero UUIDs for `TenantID`, `EnvironmentID` and `ObjectID`. Core persists the reference before creation. Object identities are never reused, including after deletion or an unconfirmed mutation. An adapter must verify the entire ownership tuple before exposing or deleting an object.

`Configuration` is an immutable Core database record with its own canonical UUID `ID`, an `Adapter` identifier and adapter-owned JSON object `Parameters`. The database is its only authored home; changing configuration creates a new identity. Core forwards parameters without interpreting native paths. Existing objects remain bound to their original configuration.

`Attachment` binds an adapter-issued JSON object `Native` to the complete reference, configuration ID and attachment kind. It is internal ownership evidence, not an application-provided host path. Shared validation checks the envelope and expected reference/configuration match; the selected adapter must also validate its parameters, receipt and native ownership. Structural validity alone never authorizes filesystem access. Public Session payloads must not supply attachments, configuration parameters or resolved host directories.

`Binding` carries the configuration and attachment transiently to a node resolver. This transfer does not introduce a second persistent node configuration. `Resolver.Resolve` validates the binding and returns `Directory{Path}` for the local compute attachment. Only the adapter resolves native storage into a clean absolute path; Core never constructs one. A structurally valid directory is not itself ownership evidence.

## Operations and outcomes

Every control adapter implements `Declaration`, `Check`, `Create`, `Observe` and `Delete`. Every node attachment adapter implements `Resolve`. There is no optional side interface, implicit adapter substitution or nil implementation fallback.

| Operation | Required meaning |
| --- | --- |
| `Declaration()` | Describe the selected configuration's verified attachment, user xattr and quota behavior. |
| `Check(ctx)` | Validate configuration and availability without creating an object. |
| `Create(ctx, reference)` | Create the owned object or converge on its existing attachment without overwriting data; retries and concurrent calls use the same immutable reference. |
| `Observe(ctx, reference)` | Return an existing object's bound attachment without creating, repairing or preparing it. |
| `Delete(ctx, reference)` | Converge on terminal deletion of the verified owned object after Core has authorized deletion and confirmed compute stop. |
| `Resolve(ctx, binding)` | Validate native ownership and resolve the attachment for local compute. |

Errors support `errors.Is`: `ErrInvalid` for invalid input, `ErrOwnership` for a reference or configuration mismatch, `ErrUnavailable` for unavailable storage, `ErrUnconfirmed` for an unknown mutation outcome, `ErrNotFound` for a missing object and `ErrUnsupported` for an unsupported combination. Adapters preserve these outcomes when wrapping errors. A missing observation does not prove an earlier mutation settled. A timeout or canceled context ends the caller's wait; it does not prove native stop or rollback. Core retains ownership on unknown outcomes. It may retry `Create` or `Delete` with the same persisted immutable reference and configuration, including after `ErrUnconfirmed`; it must never substitute a new object identity to retry an uncertain operation.

Same-reference retry and concurrent convergence are mandatory adapter guarantees. `Create` preserves any existing object data and returns the attachment for that owned object; it never reinitializes a live object. Repeated and concurrent `Delete` calls converge on the same terminal deletion. Once terminal deletion is recorded, concurrent, delayed or retried `Create` calls must not resurrect the object, and `Resolve` must not expose it. Retained deletion metadata and further cleanup of settled in-flight operations are permitted. These guarantees apply to storage lifecycle operations; they do not establish mutation settlement at timeout or distributed compute fencing.

## Capability validation

`Requirements` declares the compute placement's attachment kind and need for `user_xattr`. `Declaration` reports the filesystem adapter's attachment kind, `UserXAttr` support and `CapacityQuota` enforcement. `ValidateCombination` is the shared pre-allocation admission check. The current attachment kind is `host_directory`; unknown kinds are rejected. A required user xattr capability means the mounted filesystem supports user extended attributes used by Environment preparation.

A zero requested capacity means no quota was requested. A positive capacity in MiB is admitted only when the adapter actually enforces that limit; reporting available space or accepting an unchecked number is insufficient. The adapter's immutable configuration must enforce the admitted bound. Adapters cannot claim quota support based only on the Sandbox Provider's compute disk size.

Workspace access is single-writer. Core must serialize compute ownership and confirm the previous writer stopped before attaching a replacement. There is no cross-VM lock capability in this protocol: a guest `flock` is not proof of a host lock or another guest's exclusion. A placement must not advertise concurrent-safe attachment based on guest-local locking.

## Lifetime and filesystem scope

The object contains the whole `/environment` tree, including the workspace, staging files, initialization state, package content and the private native history of a qualified Runtime and Harness. Preparation data remains on the same filesystem so its filesystem operations preserve their semantics. Native history is private state outside the workspace directory; placement and settings are owned by [Runtime resource directories](configuration.md#runtime-resource-directories). Credentials, disposable HOME and other compute-local state have no persistence guarantee. Retained native execution requires the declared capability in the [Core–Runtime protocol](runtime-protocol.md); mounting persistent storage alone does not establish it.

Workspace storage is retained with its Session until explicit Session deletion. Archival, expiration, reset and compute checkpoint deletion do not authorize workspace deletion. Core calls `Delete` only after explicit Session deletion and confirmed compute stop; unresolved compute or storage mutations retain ownership. Failed creation does not authorize deleting storage while its Session is retained. Eligible nonterminal Sessions can survive checkpoint retention expiry and later attach the same object to fresh compute under the [cold replacement lifecycle](sandbox-provider.md#cold-replacement-after-checkpoint-retention). This does not revive archived, reset or deleted execution.

## Kernel NFS adapter

The `nfs` adapter uses an operator-premounted Linux kernel NFS filesystem. The supported setup is NFSv4.2 on trusted AUTH_SYS clients with `root_squash`, using the same export and absolute mount path on Core and nodes. The adapter neither mounts exports nor manages NFS credentials or service accounts. Other operating systems return `ErrUnsupported`. [Configuration](./configuration.md#independent-workspace-storage) owns installation steps and selection.

Parameters contain exactly `root`, `namespace_id` and `uid`. `root` is a clean absolute mount path; `namespace_id` is a canonical nonzero UUID; `uid` is the nonzero effective host UID required on both Core and nodes. The initial deployment uses 65532. The operator prepares the mounted root with mode `0700`, owned by this UID, and a regular `.oac-storage-root` file with mode `0600`, owned by the same UID, containing exactly the namespace UUID followed by one newline. Do this on the export's server-side storage as its administrator; root-squashed clients cannot assume privileged ownership changes. The root marker is an assertion of the selected export's identity, not a credential or distributed lock.

Construction validates and canonicalizes JSON without filesystem I/O. `Check` verifies the opened root is kernel NFS, the effective UID and private ownership/modes match, the namespace marker matches, and real file-descriptor `user.*` xattr set/get/remove operations succeed. Every filesystem operation, including opening the root and checking availability, runs within one process-wide budget of 32 in-flight operations. A timed-out caller receives `ErrUnconfirmed`; its slot remains occupied until the kernel call returns. An exhausted budget returns `ErrUnavailable`. Directory-descriptor-relative operations stay anchored to the opened root. An unavailable mount or a local directory at the configured path fails validation; no local fallback is created.

The declaration is `host_directory`, `user_xattr: true`, `capacity_quota: false`. The native attachment contains only the namespace identity; resolution validates the complete binding against the immutable configuration and filesystem ownership before returning `objects/<ObjectID>/live/data` beneath the root. Only this data directory is exposed to the guest. Neither tenant nor application input supplies a host path.

Each object owns `objects/<ObjectID>/identity`, `staging/`, `live/`, `trash/` and, after deletion begins, `deleted-marker`. The permanent identity binds the tenant, Environment and object UUIDs. Create prepares and syncs a private unique staging envelope before atomically publishing its nonempty directory as `live`; replay cannot overwrite existing live data. Delete first records a durable object-local terminal marker, retires live data to unique object-local trash and removes data before its ownership markers. Concurrent deleters converge. Minimal identity and deletion metadata remain intentionally; deletion does not mean every metadata file disappears. Create and Resolve refuse the retired identity. A late in-flight Create can leave empty controlled metadata requiring a repeated Delete after its syscall settles; it cannot authorize a new writer or reuse the object identity. Cleanup examines only that object's staging and trash, without a namespace-wide sweep or background service.

NFS storage does not supply compute fencing or automatic distributed failover. Guest `flock` is not a cross-VM writer lock. The single-writer and explicit-deletion requirements above remain mandatory. Confirmed release can permit a fresh VM on another compatible node to attach the same object, but an offline node or an unknown Create, Kill or snapshot-cleanup result never proves that the old writer stopped. Native history and database behavior require qualification of the selected Runtime, Harness and filesystem combination; file persistence alone is not a guarantee of native Session recovery, cross-node memory restore or arbitrary crash durability.

Core requires the selected Harness profile to support `retained_native_history` when the selected deployment generation uses external workspace storage. Creation validates that generation while holding the deployment lock, before committing the Session or compute reservation. Existing Sessions use their immutable workspace binding for Runtime admission and the final execution check. Owned workspace storage does not require this capability.
