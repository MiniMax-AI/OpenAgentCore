# Add a Sandbox Provider

A **Sandbox Provider** supplies the outer compute that a Runtime daemon runs in for a Core-managed Environment, and the bounded bootstrap that starts that daemon. This guide is the path for adding one and the reference for how Core drives it. The interface is [`SandboxProvider`](../services/core/internal/sandbox/sandbox_provider.go).

| Term | Meaning |
| --- | --- |
| Environment | Durable execution place owned by Core; see [Environments](../contracts/agents-api/environments.md) |
| Allocation | One Core-owned compute lease for an Environment, identified by `Reference` |
| Runtime | The daemon inside the Environment; it prepares capabilities and executes Turns |
| Deployment | The single deployment-wide provider selection; see [Sandbox deployment](../contracts/agents-api/sandbox-deployment.md) |

Core owns durable Environment, allocation, placement and cleanup state; the Provider owns compute and bootstrap only. The Runtime prepares capabilities and runs Turns over the [Core–Runtime protocol](runtime-protocol.md), and the provider hands it its identity through the [Runtime bootstrap](runtime-bootstrap.md) file. A provider never runs Environment initialization, Skills, Plugins, MCP setup, initial files, execution or Files; those use the Runtime. Isolation belongs to the provider's infrastructure, not the daemon; see [Runtime and outer isolation](concepts.md#runtime-and-outer-isolation). Use the vendor's maintained SDK behind a thin adapter.

## Steps

1. **Read the contract.** Implement the five required operations and give an explicit decision for every extension interface in [Implement the interface](#implement-the-interface).
2. **Write the adapter package** under `services/core/internal/sandbox/<kind>`: native SDK calls, ownership checks, identity translation and private configuration. Assert `var _ sandbox.SandboxProvider = (*YourAdapter)(nil)`. An out-of-process helper lives in `services/core/tools/<kind>-provider`.
3. **Register the kind** once, following [Register the provider kind](#register-the-provider-kind). Registration is explicit construction, not an init-time plugin registry.
4. **Label owned resources** with the provider ownership labels in the [Runtime names](../CONTRIBUTING.md#openagentcore-runtime-names) table, and never accept older label names as a fallback.
5. **Run the contract suite** with `make check-sandbox-provider-contract`; see [Validate the integration](#validate-the-integration).
6. **Run native acceptance** against real compute, then `make check`.
7. **Document the adapter** next to it, like the [reference adapters](#reference-adapters).

## Implement the interface

`SandboxProvider` has five required operations:

| Operation | Purpose |
| --- | --- |
| `Create` | Create compute for a `Reference` and run the bounded daemon bootstrap |
| `GetInfo` | Observe current compute without changing it |
| `Renew` | Extend a native lease, or only observe when the backend has none |
| `Kill` | Reclaim the allocation's compute and retained resources |
| `RunCommand` | Run a bounded command in the allocation's compute |

A backend without a native renewable lease, such as Docker, still keeps Core's hosted expiry and cleanup requirements. Every adapter implements `RunCommand` and its tests exercise it, but Core's orchestration does not call it; only the node transport forwards it. Confidential command input travels in `Command.Stdin`, never in arguments or logs, and the result keeps byte order, bounded output and the actual exit status.

### Explicit operation contracts

Every provider implements the methods of each interface below and returns a complete `ProviderOperations()` declaration. The interface methods are the operation inventory; `sandbox.ValidateOperations` checks the declaration against it without a second hand-kept list.

| Contract | Requirement | Responsibility |
| --- | --- | --- |
| `sandbox.SandboxProvider` | All five operations supported | Allocation lifecycle and bounded commands |
| `sandbox.CheckpointProvider` | Explicit decision for every method, the same for all of them | Exact compute incarnations, capture and restore, retained-source resume and cleanup |
| `runtimeobs.Source` | Explicit decision | Ownership-checked read-only observations |
| `runtimeobs.BatchSource` | Explicit decision; requires `Source` | Bounded observations in input order, with per-target errors |
| `sandbox.SelectionDiscoverer` | Explicit decision | Read-only native configuration discovery before commit |
| `sandbox.CredentialVerifier` | Explicit decision | Verify access to owned resources without mutation |

`CheckpointProvider` adds `Initial` and `NewCompute` (construct compute references without allocating), `GetCompute`, `Suspend`, `Resume`, `ResumeCompute` (thaw only the same resident instance after an aborted pause), `KillCompute`, `DeleteSnapshot` and `RunCommandCompute`, which runs a bounded command in one exact compute incarnation. Core uses `RunCommandCompute` to wake a parked daemon after a restore ([`runtime_compute_wake.go`](../services/core/internal/execution/runtime_compute_wake.go)).

Each declaration entry is `state: supported` with no reason, or `state: unsupported` with an authored reason code. Missing, zero, unknown or unsafe entries and missing methods fail validation. Adding a method to an interface requires an explicit decision and implementation in every adapter; never supply a base type or generate blanket unsupported implementations.

An unsupported method returns `providercontract.UnsupportedError` before any native I/O. The error names the exact operation and a safe code, never a native message, resource identity, endpoint or credential. An empty result, a nil error, `Unavailable` or an unknown mutation outcome never stands in for unsupported, and the five required methods can never return it.

Each adapter owns one `Operations()` function, shared by its instance and its registration. `providers.ValidateBinding` checks both against the interfaces and each other, and Runtime admission and node generation loading also reject incomplete providers. An interface assertion establishes only method shape; callers use the declaration to decide support.

`ObserveBatch` returns an error, not an ambiguous boolean. Only a typed `UnsupportedError` for `ObserveBatch` permits per-target `Observe` calls; an unavailable service, timeout or other failure never does. Observation never renews, starts, prepares or stops compute; see the [observation contract](../contracts/agents-api/runtime-observability.md).

Contract tests call every method declared unsupported with no native client configured, require its matching error and zero result, and reject incomplete or contradictory declarations. Supported behavior still needs native and lifecycle tests.

### Identity and resource ownership

`Reference` is the exact `(TenantID, EnvironmentID, AllocationID)` tuple. Core persists a fresh allocation ID before `Create`; it is not the Environment ID. The adapter also binds resources to its installation, and never locates or authorizes a resource by a bare native ID, display name, guessed path or unverified label. Every mutation, read and cleanup verifies the same ownership.

Core serializes lifecycle operations and keeps the allocation after any uncertain mutation, so the adapter must keep enough native identity and receipts for observation and cleanup. A failed call may return both `Info` and an error: keep reference-bound settlement evidence without turning the failure into success. Never silently create a replacement resource, overwrite credentials or switch to a new allocation after a conflict.

`Info.ProviderID` and `Info.State` describe compute. Core treats compute as `running` only with the matching `Reference` and a native identity; other native states can be observed without claiming readiness. `BootstrapComplete` says the bootstrap reached its final mutating step. `CreateSettled` proves that the original attempt can no longer change resources; it does not mean success:

- `State="absent"` with the matching `Reference` and `CreateSettled=true` is an explicit creation-absence receipt, with no native ID or completed bootstrap.
- `ErrNotFound`, an empty listing, a timeout or a successful `Kill` alone never proves that an in-flight Create cannot appear later.
- Core can also settle creation from a matching running resource with a completed bootstrap. Until it has such evidence or an explicit receipt it keeps the creation unknown, even when a cleanup attempt sees no resources.

`Kill` owns cleanup of the allocation's compute and retained resources, including partial bootstrap storage, and never removes another tenant's resource on a name collision. Core releases durable ownership only after confirmed cleanup and settled creation. Closing an Executor or cancelling a Harness never deletes an Environment, its workspace or its allocation.

### Operation outcomes and retries

Every call receives a bounded context. Expiry or cancellation ends the caller's wait; it proves no rollback, stop, cleanup or absence. An adapter or transport never detaches untracked mutations or replays a timed-out command.

| Operation | Confirmed result | Failure or unknown result | Recovery |
| --- | --- | --- | --- |
| `Create` | Matching compute and bootstrap evidence; execution still needs Runtime preparation | Invalid or foreign configuration rejects; a duplicate returns `ErrExists`; a transport failure may hide created resources | Observe the original `Reference`. Never replay `Create`, even with a new credential. Keep partial resources for owned cleanup |
| `GetInfo` | Current compute observation without change | `ErrNotFound` is only a missing observation; an error is not proof of absence | Repeat a bounded read; never turn it into create, start or renew |
| `Renew` | The native lease extended, or an observation for a provider without leases | A timeout may hide an extension; stopped or missing compute stays so | Observe, then let the reconciler renew the same allocation. Never revive compute or fabricate a lease expiry |
| `Kill` | Owned compute and retained storage removed; repeated confirmed absence succeeds | An error keeps ownership and cleanup intent; an ownership mismatch never deletes foreign resources | Retry cleanup of the same `Reference` after outstanding creation or mutation is fenced; never release the owner early |
| `RunCommand`, `RunCommandCompute` | Collected output and actual exit code; a nonzero exit is a settled command failure | Missing native completion is `ErrCommandUnconfirmed`; partial output is not success | Never replay. Keep the owner and reclaim before reuse when completion cannot be proved |

`ErrInvalid`, `ErrOwnership`, `ErrExists`, `ErrNotFound`, `ErrComputeUnconfirmed` and `ErrCommandUnconfirmed` keep their defined meanings. An unclassified native or transport error is unknown, never permission to retry a mutation. Core never reads provider diagnostics as lifecycle truth or exposes native error text or credentials; the node transport maps errors to fixed codes, and direct SDK details stay private.

Checkpoint support adds `Compute` generation, name and ID and `SnapshotIdentity`; persist operation IDs and the provider's snapshot provenance unchanged. `ObserveOnly` on suspend or resume observes the previous attempt and never starts another capture or restore. `ResumeCompute` only thaws the retained source and never cold-starts a stopped one. Cleanup targets the exact compute incarnation and snapshot, not whatever instance now has the same name. Read [`runtime_compute.go`](../services/core/internal/execution/runtime_compute.go) and its failure tests before declaring checkpoint support.

### Four distinct readiness facts

| Fact | Evidence | Does not establish |
| --- | --- | --- |
| Compute available | Provider observation for the owned allocation | An authenticated Runtime connection or prepared capabilities |
| Runtime connected | Gateway authentication and the exact Environment and device binding | Completed preparation or a usable Harness |
| Capabilities prepared | Successful common Runtime preparation with the fixed configuration | Acceptance or completion of a Turn |
| Execution admitted | Qualified Harness capabilities and the executor and Turn acceptance path | A completed input, cancellation or reclaimed compute |

Hosted and self-hosted Environments use the same Runtime preparation; a provider never implements a competing one.

## Register the provider kind

`sandbox/providers/registry.go` is the only registration table. Each entry binds the adapter's specification and resource validators, its `sandbox.ConfigurationAdapter`, the deployment mode (`nodes` or `direct`), suspension defaults, the operation declaration and a node-local (`BuildLocal`) or direct (`BuildDirect`) constructor. `providers.Build` and `providers.BuildDirect` construct adapters without allocating compute. There is no init-time registration or plugin loading.

A new provider takes these steps:

1. Implement the operation contracts in the adapter package, with native contract tests.
2. Add its specification and resource validators and, for native resource discovery before commit, an optional read-only `SelectionDiscoverer`. Put native credential verification behind `CredentialVerifier`.
3. Implement `sandbox.ConfigurationAdapter` over a typed native configuration. `DecodeInput` strictly parses the separate public `configuration` and write-only `credential` objects of a request. `Encode` produces whitelisted public selectors, read-only observations and separate secret bytes, and never passes request JSON through. `Decode` restores stored selectors, and keeps access to owned resources, without remote admission or new template validation. `Normalize` copies its input before changing it. `ResolveChange`, `Equal` and `WithCredential` own inheritance, identity and credential composition. `Requirements` declares whether a credential and a public Core origin are required and whether configuration discovery is supported. Also implement `ConfigurationDiscoverer`, even when discovery is unsupported: it validates the query and returns a safe catalog, never a mutation or an admission decision, while Core keeps authorization, input limits and deadlines. A node provider accepts only an empty public object, rejects credentials and returns Unsupported for discovery and credential replacement.
4. Register its constructor, policies, configuration adapter, operation declaration and defaults in `providers/registry.go`. Node proxy identity and checkpoint support read this entry. The installer's projection combines the registered policies with the shared field bounds in `sandbox/deployment_contract.go`; regenerate it with `go run ./services/core/cmd/specification-contract -write`.
5. Supply the distribution artifacts for the adapter and its helper, and offer the provider to operators through the registered configuration contract.

**Known design gap:** the installer's `--sandbox` choices and Web's setup views carry provider-specific options, such as E2B's installer flags and Web views. Exposing another provider through these surfaces currently requires shared installer and Web edits. This coupling does not meet [Complexity stays in the adapter](../AGENTS.md#complexity-stays-in-the-adapter); new integrations must express their configuration through the protocol and keep vendor-specific behavior in the adapter. Never add a Session or Turn scheduling path, a vendor column or API field, or a vendor switch in the store.

### Registration validation

`providers.ValidateRegistration` is the single wiring check. Lookup, constructor binding and the installer projection run it before any configuration callback or constructor. An unknown provider name stays invalid input; a malformed registration returns a safe `providercontract.ErrContract` that includes no submitted configuration or native diagnostics.

- A `nodes` registration has only `BuildLocal`, and a `direct` registration only `BuildDirect`; missing, mixed or unknown modes are rejected.
- The specification and resource validators, the configuration adapter and a complete operation declaration are mandatory, so an incomplete registration cannot publish a partial installer projection.
- The Runtime input policy either accepts the pinned Runtime or gives the adapter's fixed reason for rejecting it, never both.
- Checkpoint support requires node mode and positive idle and retention defaults that fit Runtime durations; a provider without checkpoint support configures no suspension defaults.

The configuration adapter must be non-nil, including its concrete value. Every `ConfigurationRequirements` field needs an explicit valid decision: `Credential` and `PublicOrigin` are `Required` or `NotRequired`, and `Discovery` uses the shared supported or unsupported declaration with a safe reason. A new requirement field or discovery method needs an explicit validation update and never inherits an existing decision. Configuration discovery is distinct from resource selection discovery, and requiring a credential does not promise the `VerifyCredential` operation. These checks establish complete registration, not correct native SDK behavior; constructor and adapter contract tests still apply.

### Configuration storage and construction

Preview and persistence use `providers.Normalize` and `providers.Describe`. `SelectionDiscoverer` resolves omitted native values before commit, and the complete specification is validated again at persistence. `providers.ResolveChange` owns configuration inheritance, and comparisons use normalized selectors, so preview, retry and commit share the same defaults. The store owns transactions, credential encryption, generation fencing, resource ownership and generic object storage: only the adapter interprets `provider_config` and `provider_metadata`, and `provider_credential` holds ciphertext bound to the installation and generation. Retained generations keep their original public configuration and metadata and compose the current credential through the adapter, so a credential replacement never rewrites a retained selector. Database constraints check object structure, not the registration list.

A direct adapter with a credential verifies all retained generations and allocation references before a key is replaced. The common `sandbox.CallFence` excludes native calls and waits for helper completion, including calls whose callers timed out; execution invokes the prepared verification and fencing callbacks without branching on a vendor.

Vendor deployment validation and SDK setup stay at the construction boundary, and construction never creates an Environment. For node-local adapters `providers.Built` returns the provider, probe, installation identity, backend fingerprint and specification digest, and the factory also returns its close function. `execution.RuntimeProvider` binds the adapter to its kind, installation ID, backend fingerprint, generation, mode and node ownership; the database owns the selection, and the in-memory copy is never another authority. Docker and microsandbox run on nodes, and E2B is constructed directly. The node proxy exposes checkpoint operations only for a backend whose registered declaration supports them, and common lifecycle code admits suspension through `CheckpointProvider`, never through a provider name.

The backend fingerprint identifies a native resource namespace, not capacity. Core keeps deployment generations so that owned allocations keep resolving to their original backend; never repoint retained allocations at a replacement backend.

### Distribution artifacts and process paths

Each node adapter's registration owns its typed `NodeArtifacts` declaration: logical distribution path, release filename suffix and installation role (`node`, `runtime`, `policy` or `image`). Registration rejects missing declarations, unsafe paths and unknown roles. `go run ./services/core/cmd/provider-artifacts -write` generates the shared Web catalog and Python projection. Run the command without `-write` to check freshness. Distribution packaging, Web availability and node installation read this projection; adding a provider's payload does not add a provider-name branch to those consumers.

The launcher supplies `sandbox.ProcessPaths` from the [derived process environment](configuration.md). Core reads these paths once and passes them to direct construction and configuration discovery. They are fixed distribution properties, not deployment settings or user-selectable helper paths. Each adapter resolves its own relative helper and state locations; E2B uses `e2b/oac-e2b-provider` and `e2b/`. Missing or nonabsolute roots fail before helper execution. Provider construction and discovery never read process environment variables.

## Managed lifecycle

This is what Core does around every provider. Adapters implement none of it, but they rely on it.

### Deployment publication

At startup Core claims the stable installation identity and a new owner epoch before it selects a provider. The runtime manager keeps generation-aware provider facades. Initial setup and replacement prepare and validate candidates before any database write, and a rejected candidate leaves the active configuration and workers unchanged. A backend replacement uses the deployment mutation gate: it pauses manager admission, drains old calls and loops, then repeats the resource and generation guards in the commit transaction, where the new selection, its generation and the retirement of old nodes and unused enrollment tokens commit together. After commit, Core publishes the prevalidated configuration and the shared observation and bootstrap cache under the manager mutex, with no further external work or fallible step, so a request cancelled after commit cannot discard it. An interrupted drain stays a barrier for retries. Provider I/O and draining never hold a database transaction or the manager's map mutex.

A locally unavailable provider dependency keeps hosted admission closed while the existing scan waits for repair; administrator recovery stays available, also after a restart. Database and ownership errors stay failures, and unconfigured hosted admission creates no Session state. Core derives the Runtime bootstrap and daemon WebSocket addresses from the installation public URL, never from request headers, and reads the current selection from the database, never from a startup file.

Node readiness binds to the exact generation, the current connection and the owner epoch. A durable serving pin is promoted only for readiness of the then-current target, under deployment serialization, so a late report for a superseded target never acquires a pin.

### Allocation lifecycle

The allocation, its dedicated daemon credential digest and the exact Session binding commit atomically before `Create`, under the execution lease and the Session lock. Only a fresh allocation receipt permits `Create`; retries and a Core restart observe the same reference without replaying it or rotating the credential. An allocation is private compute ownership, separate from public Environment connection and native readiness; adapters qualify bootstrap completion, and Core never infers it from an engine or provider name.

With a configured provider, the Worker scans committed pending hosted Environments that have no allocation, which covers idle Session creation and recovery after an interruption between commit and bootstrap; an existing allocation never re-enters that path. The scan is bounded and serialized by the lifecycle owner and needs no caller action. An initial reservation without a Turn leaves its Session idle, and a daemon connection is never treated as native readiness. The same scan publishes authenticated connection observations with durable generations, after verifying the exact Session and device binding and a settled bootstrap.

Connected, observed compute receives service keepalives between Turns. Keepalives never revive a lapse of one hour or a cleanup request. A node allocation never expires only because its keepalive is an hour old; explicit deletion and the snapshot retention still authorize its cleanup. A stopped or missing container never authorizes discarding retained workspace or history. Disabling the provider stops new hosted admission and bootstrap but never blocks cancellation, function results or input retry outcomes of existing Sessions.

Terminal cleanup atomically revokes the device's authority, records the Environment's failure or expiry, settles pending input and requests cancellation, and only then calls `Kill`; original input deadlines and retry outcomes are kept. Temporary provider outages, unknown Create results and stopped compute never prove a permanent failure. After public Session deletion Core keeps the allocation and marks it released only after owned compute and volume cleanup and proof that the original Create settled; an unknown creation keeps cleanup ownership even after an absence observation, and bounded scans continue to catch late resources without another `Create`.

### Per-node lifecycle workers

Each registered node has one serial lifecycle worker that owns its gate, allocation and pending cursors, connections and wake hints; E2B allocations share one serial lifecycle without a node. A thin coordinator discovers nodes and shuts workers down, and never holds its map mutex during database, provider or wait operations. Workers advance independently, so a stuck provider on one online node never stalls another: lifecycle concurrency is one operation per node and grows with the node count. Offline workers stay, so their retained resources remain observable after reconnection.

Allocation scans filter by node before their 32-row page limit, and pending scans join the unreleased committed placement. Each node advances its own cursor, including past failed observations, and wraps once at the end. Direct provisioning resolves the tenant-scoped placement before entering that node's gate, and an existing allocation must agree with it; Core never chooses another node.

Before releasing the execution lease, the coordinator stops accepting work and cancels and drains every node worker and direct caller. Lease loss affects everything; ordinary provider failures stay within their node. A planned deployment drain or node retirement cancels lifecycle contexts synchronously between leased operations, through the lease gate with its five-second bound, including an active manual reconcile, and never cancels an in-flight leased query just to change configuration. A failed cancellation fence closes manager admission and reports owner failure. A failed retirement keeps the original lifecycle identity and gate until owner shutdown, and the drain barrier stays closed. Session locks, deployment capacity transactions and revision-checked receipts stay authoritative, and no external operation holds a database lock.

### Placement and capacity

Placement is automatic: the environment-to-node placement commits with Session creation and its retry identity, callers cannot choose a node, and a retry keeps its original node even while it is offline. Node capacity counts pending reservations and unresolved resources, and new placement and a suspended-to-restoring transition share a database lock. Unknown operations keep their reservations, source teardown must be confirmed before active capacity is released, and confirmed cleanup releases placement capacity. Retained ownership needs exact provider evidence: a socket path, a missing instance or an empty listing never proves cleanup or authorizes a replacement.

The deployment's CPU, memory and disk settings, `max_active`, `max_retained` and the snapshot retention bound each node. There is no node-level drain, cross-node Session migration, multi-active Core, autoscaling or snapshot replication. Node removal is refused while the node holds allocations, snapshots, reservations, unknown results or cleanup, and offline ownership is kept.

### Suspension

A provider with checkpoint support can suspend idle work; the deployment's [`suspension`](../contracts/agents-api/sandbox-deployment.md#safe-response) policy sets the idle time and snapshot retention. Core suspends only after at least one Turn is terminal, when no root or Subagent Turn is queued, in progress or waiting, no input, file operation or initialization is pending, and real activity has been idle for the configured interval. For node allocations Core records the first root or child terminal transition with the database clock in the same transaction. Candidate filtering and the Session-locked recheck compare elapsed database time with the idle duration, and the initial snapshot retention deadline is anchored to the same database observation, so Core and database host clocks need not agree. Native completion timestamps stay unchanged in public history but never drive idle admission, and heartbeats never reset activity. Before acknowledging a planned suspension, the daemon closes admission and drains native cleanup, output receipts and file work.

The Worker lease, the Session lock and the per-node gates own suspension for every provider. New Turn claims, file-write intents and capture admission serialize under the Session lock and share one compute-phase check; new pending work cancels a capture and wakes the same source. Normal preparation waits for the compute phase to be running, after the authenticated resume handshake, and pending input stays pending when its promotion conflicts with a lifecycle transition. Compute phases and revision-checked receipts live on the allocation. Core persists quiesce, capture and restore intent before the effect, only a fresh receipt performs a capture or restore, and recovery observes the exact attempt without retrying an unknown creation, capture or restore. A consumed snapshot never rolls a running generation back. Deletion, revocation and retention expiry win over wake, up to the final database compare-and-swap, and unknown cleanup identities are kept until owned resources are confirmed absent. Consumed artifacts and old compute are deleted, so suspension cycles never build a chain of writable disks.

Queued work and live Environment file access wake a suspended Environment; history and published Artifact reads do not. Planned suspension uses an Environment and suspension token on the daemon connection. A PID and start-time fenced local control signal (`RunCommandCompute`) wakes the parked daemon, which authenticates again before admitting work. A transient disconnect before confirmation retries the same armed suspension with bounded attempts and backoff; a permanent authentication or protocol rejection closes it. Core owns the snapshot's retention deadline, and the daemon has no timer for it. A lost quiesce acknowledgement may thaw the same source through explicit rollback but never authorizes capturing it.

### Reset and archive

A [reset](../contracts/agents-api/sandbox-deployment.md#reset) is durable execution state that the runtime manager advances outside its counted work. Start, escalation, cancellation, setup, update and finalization serialize through the mutation gate. Idleness is rechecked with the Session lock and then the deployment lock, never in the reverse order during finalization. Work is keyset-paged and bound to the reset's request time and generation, with the absolute deadline and validated audit provenance persisted.

One snapshot and timestamp partition the held resources. Offline ownership comes from the allocation's or active placement's node, with the same 45-second connection and owner-epoch predicate as online presence, independently of provider readiness; no cleanup failure, offline state or empty read authorizes a synthetic release. At zero held resources, Core drains outside database transactions, rechecks under the deployment lock and clears the deployment in one transaction, then publishes a generation-bearing empty provider without fallible work. If the final write or drain fails, it restores the committed provider with a bounded owner context before releasing the mutation gate; if that recovery fails, admission stays fenced and the owner stops.

An administrator [Session archive](../contracts/agents-api/admin-api.md#session-archive) keeps its Project scope and hosted eligibility checks in a Session-first transaction, together with Environment expiry, cancellation, Runtime authority revocation and audit; a reset's background archive reconstructs the actual Project scope from trusted records and keeps the requester's provenance. The ordinary provider lifecycle releases compute and snapshots. Archive cancellation keeps a healthy receipt path until the terminal commit: only the archive that first revokes a device records its exact `archive_cancel_turn_id` (ordinary revocation clears it, and repeated cleanup keeps it), and the existing authenticated delivery may drain that cancellation for at most 20 seconds from the Turn's original `cancel_requested_at`. Core tracks the delivery through `done`, the cancellation acknowledgement and the terminal commit, independently of subscription removal, and grants no new connection, input, file or MCP authority or lease renewal. No transaction or lifecycle gate waits for the receipt, and a lost peer, expiry or restart falls back to ordinary failure and cleanup, never a fabricated cancelled outcome.

## Validate the integration

Run `make check-sandbox-provider-contract` while developing. It runs the shared [`contracttest`](../services/core/internal/sandbox/contracttest) suite through real adapter boundaries with controlled native failures, plus the adapter and node transport tests; `make check` includes the same packages. Call the public failure runner with native-side fixtures instead of a fake `SandboxProvider`, and keep tests for foreign ownership, unknown mutation results, cancellation, no automatic replay, failed cleanup and reference-bound settlement.

Node tests separately cover disconnect and reconnect fencing and cleanup after a lost Create response. Helper protocols and the [sandbox node protocol](../contracts/agents-api/node-generation-protocol.md) require an exact version match; direct in-process interfaces have no separate wire version.

Native acceptance proves what fixtures cannot: creation, lease behavior, owned partial cleanup, declared isolation and limits, and snapshots where supported. The opt-in Docker lifecycle and recovery tests use `AGENTS_RUNTIME_DOCKER_TEST_IMAGE`; the SDK helpers use `make check-e2b-provider` and `make check-microsandbox-provider`. Mocked compute proves neither reclamation nor isolation.

## Reference adapters

| Kind | Adapter | Helper and adapter rules | Operator guide |
| --- | --- | --- | --- |
| Docker (node) | [`sandbox/docker`](../services/core/internal/sandbox/docker) | Node proxy in [`sandbox/node`](../services/core/internal/sandbox/node) | [Docker adapter](#docker-adapter) |
| microsandbox (node) | [`sandbox/microsandbox`](../services/core/internal/sandbox/microsandbox) | [`tools/microsandbox-provider`](../services/core/tools/microsandbox-provider/README.md) | [Nodes](getting-started/nodes.md) |
| E2B (direct) | [`sandbox/e2b`](../services/core/internal/sandbox/e2b) | [`tools/e2b-provider`](../services/core/tools/e2b-provider/README.md) | [Sandbox deployment](../contracts/agents-api/sandbox-deployment.md#e2b-configuration); application-managed templates in [`deploy/e2b`](../services/core/deploy/e2b/README.md) |

## Docker adapter

The Docker Sandbox Provider ([`sandbox/docker`](../services/core/internal/sandbox/docker)) runs every Runtime image, whichever Harness it serves, with the same container settings ([`container_options.go`](../services/core/internal/sandbox/docker/container_options.go)):

- user 1000:1000, read-only root filesystem, all capabilities dropped, `no-new-privileges`, the [seccomp profile](#seccomp-profile) and AppArmor `unconfined`;
- the node’s configured network and extra hosts ([node configuration](configuration.md#docker-node-configuration));
- CPU and memory from the deployment specification, a 128-process limit and a 128 MiB `/tmp` tmpfs;
- two named volumes labelled with the installation, tenant, Environment and allocation: `<name>-home` at `/home` and `<name>-environment` at `/environment`, whose `workspace` subdirectory is also mounted at `/workspace`. The Docker Engine must support volume subpath mounts;
- with the configured `nested_sandbox` option, Docker's `/proc` masks are lifted (`/sys/firmware` and `/sys/devices/virtual/powercap` stay masked) and the container runs an init process.

Create refuses to reuse retained volumes that have no container. It copies the [Runtime bootstrap](runtime-bootstrap.md) file to `/home/runtime/runtime-bootstrap.json` (mode 0600, UID 1000) and the `/environment` workspace, staging, initialization and package directories into the container, then starts `oac-daemon connect --profile default --bootstrap-file /home/runtime/runtime-bootstrap.json`. When the created container does not have the configured CPU, memory and exact image, Create returns the error with `CreateSettled`. Docker has no lease, so Renew only reads the container state. Kill checks the ownership labels of the container and both volumes before removing any of them, then confirms that all three are gone.

The node uses the explicit Unix socket in its [provider configuration](configuration.md#docker-node-configuration) and ignores `DOCKER_HOST`. No Docker socket, host home or Core credential is mounted into a Runtime.

### Seccomp profile

[`seccomp.json`](../services/core/deploy/codex/seccomp.json) is the Moby default profile at [revision 65adc7e](https://github.com/moby/profiles/blob/65adc7e022c97f55e45c054ff012988027733b87/seccomp/default.json) (Apache-2.0, see [seccomp.LICENSE](../services/core/deploy/codex/seccomp.LICENSE); upstream file SHA-256 `785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334`) with one appended rule that allows `clone`, `unshare`, `setns`, `mount`, `umount2` and `pivot_root`. The distribution ships this file to every Docker node as `runtime/seccomp.json`.
