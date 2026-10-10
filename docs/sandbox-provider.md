---
title: "Add a Sandbox Provider"
---

A **Sandbox Provider** supplies the outer compute that a Runtime daemon runs in for a Core-managed Environment, and the bounded bootstrap that starts that daemon. This guide is the path for adding one and the reference for how Core drives it. The interface is [`SandboxProvider`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/sandbox/sandbox_provider.go).

| Term | Meaning |
| --- | --- |
| Environment | Durable execution place owned by Core; see [Environments](../contracts/agents-api/environments.md) |
| Allocation | One Core-owned compute lease for an Environment, identified by `Reference` |
| Runtime | The daemon inside the Environment; it prepares capabilities and executes Turns |
| Deployment | The single deployment-wide provider selection; see [Sandbox deployment](../contracts/agents-api/sandbox-deployment.md) |

Core owns durable Environment, allocation, placement and cleanup state; the Provider owns compute and bootstrap only. The Runtime prepares capabilities and runs Turns over the [Core–Runtime protocol](./runtime-protocol.md), and the provider hands it its identity through the [Runtime bootstrap](./runtime-bootstrap.md) file. A provider never runs Environment initialization, Skills, Plugins, MCP setup, initial files, execution or Files; those use the Runtime. Isolation belongs to the provider's infrastructure, not the daemon; see [Runtime and outer isolation](./concepts.md#runtime-and-outer-isolation). Use the vendor's maintained SDK behind a thin adapter.

## Steps

1. **Read the contract.** Implement every method, support the required operations and declare a decision for each of the others in [Implement the interface](#implement-the-interface).
2. **Write the adapter package** under `services/core/internal/sandbox/<kind>`: native SDK calls, ownership checks, identity translation, private configuration and, for a node adapter, its node-local construction. Assert `var _ sandbox.SandboxProvider = (*YourAdapter)(nil)`. An out-of-process helper lives in `services/core/tools/<kind>-provider`.
3. **Register the kind** once, following [Register the provider kind](#register-the-provider-kind). Registration is explicit construction, not an init-time plugin registry.
4. **Label owned resources** with the provider ownership labels in the [Runtime names](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#openagentcore-runtime-names) table, and never accept older label names as a fallback.
5. **Run the contract suite** with `make check-sandbox-provider-contract`; see [Validate the integration](#validate-the-integration).
6. **Run native acceptance** against real compute, then `make check`.
7. **Document the adapter** next to it, like the [reference adapters](#reference-adapters).

## Implement the interface

`sandbox_provider.go` holds the Core–Sandbox Provider protocol: the `SandboxProvider` interface for allocation, suspension and observation, its request and result types, and the setup-time `ConfigurationAdapter` with its typed errors. Value types that Core also uses beyond this boundary, such as `DeploymentSpec` and `CallFence`, live in their own files of the same package. Every method is required at compile time, and `ProviderOperations()` declares which ones the Provider supports. Five operations are always supported:

| Operation | Purpose |
| --- | --- |
| `Create` | Create compute for a `Reference` and run the bounded daemon bootstrap |
| `GetInfo` | Observe current compute without changing it |
| `Renew` | Extend a native lease, or only observe when the backend has none |
| `Kill` | Reclaim the allocation's compute and retained resources |
| `RunCommand` | Run a bounded command in the allocation's compute |

A backend without a native renewable lease, such as Docker, still keeps Core's hosted expiry and cleanup requirements. Every adapter implements `RunCommand` and its tests exercise it, but Core's orchestration does not call it; only the node transport forwards it. Confidential command input travels in `Command.Stdin`, never in arguments or logs, and the result keeps byte order, bounded output and the actual exit status.

### Explicit operation contracts

Every provider returns a complete `ProviderOperations()` declaration with one entry for each `SandboxProvider` method except `ProviderOperations` itself. The interface's method set is the operation inventory, and `sandbox.ValidateOperations` checks the declaration against it. The groups in the table below are lists in the protocol file, and a test keeps them equal to that method set, so a new method must join one of them.

| Operations | Requirement | Responsibility |
| --- | --- | --- |
| `Create`, `GetInfo`, `Renew`, `Kill`, `RunCommand` | Supported | Allocation lifecycle and bounded commands |
| `Observe` | Explicit decision | Ownership-checked read-only observation of one allocation |
| `Initial`, `NewCompute`, `GetCompute`, `RenewCompute`, `Suspend`, `Resume`, `ResumeCompute`, `KillCompute`, `DeleteRetained`, `RunCommandCompute` | The same decision for all ten suspension methods in `nodes` or `direct` mode | Exact compute incarnations, capture and restore, retained-source resume and cleanup |

`Initial` and `NewCompute` construct compute references without allocating, `ResumeCompute` thaws only the same resident instance after an aborted pause, and `RunCommandCompute` runs a bounded command in one exact compute incarnation. Core uses `RunCommandCompute` to wake a parked daemon after a restore ([`runtime_compute_wake.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/execution/runtime_compute_wake.go)).

Each declaration entry is `state: supported` with no reason, or `state: unsupported` with an authored reason code. Missing, zero, unknown or unsafe entries fail validation. Adding a method to `SandboxProvider` requires an explicit decision and implementation in every adapter; never supply a base type or generate blanket unsupported implementations.

An unsupported method returns `providercontract.UnsupportedError` before any native I/O. The error names the exact operation and a safe code, never a native message, resource identity, endpoint or credential. An empty result, a nil error, `Unavailable` or an unknown mutation outcome never stands in for unsupported, and the five required methods can never return it.

Each adapter owns one `Operations()` function, shared by its instance and its registration. `providers.ValidateBinding` checks both against the interface and each other, and Runtime admission and node generation loading also reject incomplete providers. Callers check the declaration with `providercontract.Require` before they call an operation, never a type assertion.

`Observe` reads one allocation and never renews, starts, prepares or stops compute; see the [observation contract](../contracts/agents-api/runtime-observability.md).

Contract tests call every method declared unsupported with no native client configured, require its matching error and zero result, and reject incomplete or contradictory declarations. Supported behavior still needs native and lifecycle tests.

### Identity and resource ownership

`Reference` is the exact `(TenantID, EnvironmentID, AllocationID)` tuple. Core persists a fresh allocation ID before `Create`; it is not the Environment ID. The adapter also binds resources to its installation, and never locates or authorizes a resource by a bare native ID, display name, guessed path or unverified label. Every mutation, read and cleanup verifies the same ownership.

Core serializes lifecycle operations and keeps the allocation after any uncertain mutation, so the adapter must keep enough native identity and receipts for observation and cleanup. A failed call may return both `Info` and an error: keep reference-bound settlement evidence without turning the failure into success. Never silently create a replacement resource, overwrite credentials or switch to a new allocation after a conflict.

`Info.ProviderID` and `Info.State` describe compute. Core treats compute as `running` only with the matching `Reference` and a native identity; other native states can be observed without claiming readiness. `BootstrapComplete` says the bootstrap reached its final mutating step. `CreateSettled` proves that the original attempt can no longer change resources; it does not mean success:

- `State="absent"` with the matching `Reference` and `CreateSettled=true` is an explicit creation-absence receipt, with no native ID or completed bootstrap.
- `ErrNotFound`, an empty listing, a timeout or a successful `Kill` alone never proves that an in-flight Create cannot appear later.
- Core can also settle creation from a matching running resource with a completed bootstrap. Until it has such evidence or an explicit receipt it keeps the creation unknown, even when a cleanup attempt sees no resources.

Microsandbox closes initial Create admission before returning a creation-absence receipt; its [helper protocol](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/microsandbox-provider/README.md#create-and-bootstrap) defines the durable allocation-lock barrier. Ordinary `GetCompute` absence does not close admission.

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

Suspension support adds `Compute` generation, name and ID and `RetainedState`; persist operation IDs and the provider's retained-state provenance unchanged. `ReconcileOnly` on suspend or resume observes the previous attempt and never starts another capture or restore. `ResumeCompute` only thaws the retained source and never cold-starts a stopped one. Cleanup targets the exact compute incarnation and retained state, not whatever instance now has the same name. Read [`runtime_compute.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/execution/runtime_compute.go) and its failure tests before declaring suspension support.

### Four distinct readiness facts

| Fact | Evidence | Does not establish |
| --- | --- | --- |
| Compute available | Provider observation for the owned allocation | An authenticated Runtime connection or prepared capabilities |
| Runtime connected | Gateway authentication and the exact Environment and device binding | Completed preparation or a usable Harness |
| Capabilities prepared | Successful common Runtime preparation with the fixed configuration | Acceptance or completion of a Turn |
| Execution admitted | Qualified Harness capabilities and the executor and Turn acceptance path | A completed input, cancellation or reclaimed compute |

Hosted and self-hosted Environments use the same Runtime preparation; a provider never implements a competing one.

## Register the provider kind

`sandbox/providers/registry.go` is the only registration table. Each entry binds the adapter's specification and resource validators, its `sandbox.ConfigurationAdapter`, the deployment mode (`nodes` or `direct`), its `sandbox.DeploymentPolicy` (disk limits, the Runtime input and the default size setup proposes), the operation declaration and a node-local (`BuildLocal`) or direct (`BuildDirect`) constructor. `providers.Build` and `providers.BuildDirect` construct adapters without allocating compute. There is no init-time registration or plugin loading.

A new provider takes these steps:

1. Implement the operation contracts in the adapter package, with native contract tests.
2. Add its specification and resource validators.
3. Implement `sandbox.ConfigurationAdapter` over a typed native configuration. `DecodeInput` strictly parses the separate public `configuration` and write-only `credential` objects of a request. `Encode` produces whitelisted public selectors, read-only observations and separate secret bytes, and never passes request JSON through. `Decode` restores stored selectors, and keeps access to owned resources, without remote admission or new template validation. `Normalize` copies its input before changing it. `ResolveChange`, `Equal` and `WithCredential` own inheritance, identity and credential composition. `Requirements` declares whether a credential and a public Core origin are required, and which setup operations are supported: `Discovery` for `DiscoverConfiguration`, `SelectionDiscovery` for `DiscoverSelection` and `CredentialVerification` for `VerifyCredential`. `DiscoverConfiguration` validates the query and returns a safe catalog, never a mutation or an admission decision, while Core keeps authorization, input limits and deadlines. `DiscoverSelection` resolves a candidate's omitted native values before commit, and `VerifyCredential` verifies a credential's access to owned resources without mutation. Both receive the candidate's `sandbox.DirectConfig` and build any native client for that call only. A node provider accepts only an empty public object, rejects credentials and returns Unsupported for every setup operation and for credential replacement.
4. For a node adapter, export from its package the `BuildLocal` constructor, the typed `native` object it decodes and the native files it adds to the shared node artifacts. Node-local settings, such as host paths, live only in that object; resources and the Runtime release are read from the node configuration's `specification`.
5. Register its constructor, policies, configuration adapter and operation declaration in `providers/registry.go`. Its key is the provider kind, which also labels the Provider's observations, and suspension support reads this entry. The generated projections combine each registered mode and deployment policy with the shared field bounds in `sandbox/deployment_contract.go`: the installer reads them from `deploy/node/node_spec.py`, and the TypeScript client and Web from `packages/agents-client/src/deployment-contract.ts`, so Web reads these declarations instead of comparing provider kinds. Regenerate both with `go run ./services/core/cmd/specification-contract -write`.
6. Supply the distribution artifacts for the adapter and its helper, and offer the provider to operators through the registered configuration contract.

**Known design gap:** Web still names providers in the setup wizard's backend choice, the Docker confirmation and E2B's configuration fields wherever Web shows or parses them (the setup step with its service presets, the deployment summary and the client's deployment projection), because the protocol declares no configuration fields yet. Exposing another provider through that surface currently requires a shared Web edit. This coupling does not meet [Complexity stays in the adapter](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#complexity-stays-in-the-adapter); new integrations must express their configuration through the protocol and keep vendor-specific behavior in the adapter. Never add a Session or Turn scheduling path, a vendor column or API field, or a vendor switch in the store.

A node configuration, `sandbox.NodeConfig`, holds `provider`, `generation`, `installation_id`, `core_url`, `specification` and the adapter's opaque `native` object. `providers.Build` validates `provider`, `generation`, `installation_id` and `specification`, and passes the configuration with ephemeral `sandbox.LocalOptions` to `BuildLocal`, which decodes `native` strictly. The caller explicitly selects standalone registration or single-provider execution with `Standalone`, or generation-owned execution with a canonical absolute node state directory in `GenerationStateDirectory`. Missing or mixed contexts are rejected. The adapter owns generation-specific native preparation and readiness checks. Microsandbox binds helper leases to the installation, generation and specification digest, then checks the pinned image after platform, capacity and artifact readiness.

### Registration validation

`providers.ValidateRegistration` is the single wiring check. Lookup, constructor binding and the generated projections run it before any configuration callback or constructor. An unknown provider name stays invalid input; a malformed registration returns a safe `providercontract.ErrContract` that includes no submitted configuration or native diagnostics.

- A `nodes` registration has only `BuildLocal`, and a `direct` registration only `BuildDirect`; missing, mixed or unknown modes are rejected.
- The specification and resource validators, the configuration adapter and a complete operation declaration are mandatory, so an incomplete registration cannot publish a partial projection. A declared default size must pass the adapter's resource validator.
- The Runtime input policy either accepts the pinned Runtime or gives the adapter's fixed reason for rejecting it, never both.
- Suspension is admitted in both `nodes` and `direct` modes when the complete lifecycle is declared supported. A registration carries no suspension values: Core applies its one [suspension policy](#suspension) to every supported Provider.

The configuration adapter must be non-nil, including its concrete value. Every `ConfigurationRequirements` field needs an explicit valid decision: `Credential` and `PublicOrigin` are `Required` or `NotRequired`, and `Discovery`, `SelectionDiscovery` and `CredentialVerification` use the shared supported or unsupported declaration with a safe reason. A new requirement field needs an explicit validation update and never inherits an existing decision. Requiring a credential does not promise the `VerifyCredential` operation. These checks establish complete registration, not correct native SDK behavior; constructor and adapter contract tests still apply.

### Configuration storage and construction

Preview and persistence use `providers.Normalize` and `providers.Describe`. `providers.DiscoverSelection` resolves omitted native values before commit, and the complete specification is validated again at persistence. `providers.ResolveChange` owns configuration inheritance, and comparisons use normalized selectors, so preview, retry and commit share the same defaults. The store owns transactions, credential encryption, generation fencing, resource ownership and generic object storage: only the adapter interprets `provider_config` and `provider_metadata`, and `provider_credential` holds ciphertext bound to the installation and generation. Retained generations keep their original public configuration and metadata and compose the current credential through the adapter, so a credential replacement never rewrites a retained selector. Database constraints check object structure, not the registration list.

A direct adapter with a credential verifies all retained generations and allocation references before a key is replaced. The common `sandbox.CallFence` excludes native calls and waits for helper completion, including calls whose callers timed out; execution invokes the prepared verification and fencing callbacks without branching on a vendor.

Vendor deployment validation and SDK setup stay at the construction boundary, and construction never creates an Environment. For node-local adapters `sandbox.Built` returns the provider, probe, installation identity, backend fingerprint and specification digest, and a `Quiescent` check when a helper can outlive its caller; generation collection waits for it. The factory also returns its close function. `execution.RuntimeProvider` binds the adapter to its kind, installation ID, backend fingerprint, generation, mode and node ownership; the database owns the selection, and the in-memory copy is never another authority. Docker and microsandbox run on nodes, and E2B is constructed directly. The node proxy exposes suspension operations only for a backend whose registered declaration supports them, and common lifecycle code admits suspension through the suspension declaration, never through a provider name.

The backend fingerprint identifies a native resource namespace, not capacity. Core keeps deployment generations so that owned allocations keep resolving to their original backend; never repoint retained allocations at a replacement backend.

### Distribution artifacts and process paths

Each node adapter's registration owns its typed `NodeArtifacts` declaration, the shared node artifacts plus the native files its package exports: logical distribution path, release filename suffix and installation role (`node`, `runtime`, `policy` or `image`). Registration rejects missing declarations, unsafe paths and unknown roles. `go run ./services/core/cmd/provider-artifacts -write` generates the shared Web catalog and Python projection. Run the command without `-write` to check freshness. Distribution packaging, Web availability and node installation read this projection; adding a provider's payload does not add a provider-name branch to those consumers.

The launcher supplies `sandbox.ProcessPaths` from the [derived process environment](./configuration.md). Core reads these paths once and passes them to direct construction and setup operations. They are fixed distribution properties, not deployment settings or user-selectable helper paths. Each adapter resolves its own relative helper and state locations; E2B uses `e2b/oac-e2b-provider` and `e2b/`. Missing or nonabsolute roots fail before helper execution. Provider construction and discovery never read process environment variables.

## Managed lifecycle

This is what Core does around every provider. Adapters implement none of it, but they rely on it.

### Deployment publication

At startup Core claims the stable installation identity and a new owner epoch before it selects a provider. The runtime manager keeps generation-aware provider facades. Initial setup and replacement prepare and validate candidates before any database write, and a rejected candidate leaves the active configuration and workers unchanged. A backend replacement uses the deployment mutation gate: it pauses manager admission, drains old calls and loops, then repeats the resource and generation guards in the commit transaction, where the new selection, its generation and the retirement of old nodes and unused enrollment tokens commit together. After commit, Core publishes the prevalidated configuration and the shared observation and bootstrap cache under the manager mutex, with no further external work or fallible step, so a request cancelled after commit cannot discard it. An interrupted drain stays a barrier for retries. Provider I/O and draining never hold a database transaction or the manager's map mutex.

A locally unavailable provider dependency keeps hosted admission closed while the existing scan waits for repair; administrator recovery stays available, also after a restart. Database and ownership errors stay failures. An unconfigured deployment refuses hosted admission with 503 `execution_unavailable` and creates no Session state. Core derives the Runtime bootstrap and daemon WebSocket addresses from the installation public URL, never from request headers, and reads the current selection from the database, never from a startup file.

Node readiness binds to the exact generation, the current connection and the owner epoch. A durable serving pin is promoted only for readiness of the then-current target, under deployment serialization, so a late report for a superseded target never acquires a pin.

### Allocation lifecycle

Core includes the owning Session’s immutable Harness in `Bootstrap.Harness`; every provider projects it into the [Runtime bootstrap](./runtime-bootstrap.md) without choosing an implementation.

The allocation, its dedicated daemon credential digest and the exact Session binding commit atomically before `Create`, under the execution lease and the Session lock. Only a fresh allocation receipt permits `Create`; retries and a Core restart observe the same reference without replaying it or rotating the credential. An allocation is private compute ownership, separate from public Environment connection and native readiness; adapters qualify bootstrap completion, and Core never infers it from an engine or provider name.

With a configured provider, the Worker scans committed pending hosted Environments that have no allocation, which covers idle Session creation and recovery after an interruption between commit and bootstrap; an existing allocation never re-enters that path. The scan is bounded and serialized by the lifecycle owner and needs no caller action. An initial reservation without a Turn leaves its Session idle, and a daemon connection is never treated as native readiness. The same scan publishes authenticated connection observations with durable generations, after verifying the exact Session and device binding and a settled bootstrap.

Between Turns, Core checks that connected, observed compute is still its Session's running allocation; the check changes nothing and never revives a cleanup request. Running compute never expires: explicit lifecycle cleanup and retained-state retention govern reclamation. A stopped or missing container never authorizes discarding retained workspace or history or selecting replacement compute. Disabling the provider stops new hosted admission and bootstrap but never blocks cancellation, function results or input retry outcomes of existing Sessions.

Terminal cleanup atomically revokes the device's authority, records the Environment's failure or expiry, settles pending input and requests cancellation, and only then calls `Kill`; original input deadlines and retry outcomes are kept. Temporary provider outages, unknown Create results and stopped compute never prove a permanent failure. After public Session deletion Core keeps the allocation and marks it released only after owned compute and volume cleanup and proof that the original Create settled; an unknown creation keeps cleanup ownership even after an absence observation, and bounded scans continue to catch late resources without another `Create`.

### Per-node lifecycle workers

After a hosted Environment commits, Core sends a bounded hint to the lifecycle worker for its placement. Committed pending input also sends a hint to recover a missed creation notification. Hints reuse the serial gate, execution lease, capacity checks and one-shot allocation receipt; admission handlers never create a sandbox directly. Each normal maintenance period admits at most one extra hinted scan, and periodic scans recover missed or coalesced hints. Existing allocations are observed before pending provisioning, so a slow observation on the same node can still delay a fresh Environment.

Each registered node has one serial lifecycle worker that owns its gate, allocation and pending cursors, connections and wake hints; E2B allocations share one serial lifecycle without a node. A thin coordinator discovers nodes and shuts workers down, and never holds its map mutex during database, provider or wait operations. Workers advance independently, so a stuck provider on one online node never stalls another: lifecycle concurrency is one operation per node and grows with the node count. Offline workers stay, so their retained resources remain observable after reconnection.

Allocation scans filter by node before their 32-row page limit, and pending scans join the unreleased committed placement. Each node advances its own cursor, including past failed observations, and wraps once at the end. Direct provisioning resolves the tenant-scoped placement before entering that node's gate, and an active allocation must agree with it. Only confirmed release permits a new placement; a disconnect never moves an active allocation.

Before releasing the execution lease, the coordinator stops accepting work and cancels and drains every node worker and direct caller. Lease loss affects everything; ordinary provider failures stay within their node. A planned deployment drain or node retirement cancels lifecycle contexts synchronously between leased operations, through the lease gate with its five-second bound, including an active manual reconcile, and never cancels an in-flight leased query just to change configuration. A failed cancellation fence closes manager admission and reports owner failure. A failed retirement keeps the original lifecycle identity and gate until owner shutdown, and the drain barrier stays closed. Session locks, deployment capacity transactions and revision-checked receipts stay authoritative, and no external operation holds a database lock.

### Placement and capacity

Placement is automatic: the Environment-to-node placement commits with Session creation and its retry identity, callers cannot choose a node, and an active allocation keeps its original node even while it is offline. After confirmed allocation release, eligible retained Sessions can reserve compatible capacity again, including on another node. Node capacity counts pending reservations and unresolved resources, and new placement and a suspended-to-restoring transition share a database lock. Unknown operations keep their reservations, source teardown must be confirmed before active capacity is released, and confirmed cleanup releases placement capacity. Retained ownership needs exact provider evidence: a socket path, a missing instance or an empty listing never proves cleanup or authorizes a replacement.

The deployment's CPU, memory and disk settings, `max_active`, `max_retained` and the retained-state retention bound each node. Cold replacement after confirmed release does not provide node-level drain, cross-node memory restore, failover from an unreachable writer, concurrent writers, multi-active Core, autoscaling or snapshot replication. Node removal is refused while the node holds allocations, retained states, reservations, unknown results or cleanup, and offline ownership is kept.

### Suspension

The single `SandboxProvider` contract declares `Initial`, `NewCompute`, `GetCompute`, `RenewCompute`, `Suspend`, `Resume`, `KillCompute`, `DeleteRetained`, `RunCommandCompute` and `ResumeCompute` together: all are supported or all are unsupported. No optional suspension interface or provider-name branch selects this lifecycle.

`RetainedState` is an opaque adapter-owned envelope with `Reference`, `ID`, `Data`, `OperationID`, `SourceGeneration`, `SourceName` and `SourceID`. `Data` is nonempty and at most 64 KiB. Core preserves the envelope unchanged and validates operation and source ownership; the immutable allocation provider/generation binding selects the only adapter allowed to interpret it. A retained state need not be an independent snapshot. Microsandbox retains its complete native snapshot identity in `Data`; E2B retains its versioned native pause receipt and cannot promise a separate disk image. Each adapter validates its native payload and ownership before any effect.

`Compute.RestoredFrom`, `ComputeState.Retained`, `ResourcesReleased`, `SuspendSettled` and recovery's `ReconcileOnly` are authoritative settlement evidence. A missing resource is not proof that an unknown suspension settled. `RenewCompute` renews only the exact incarnation; an uncertain result never authorizes a second create, capture or restore. Durable `runtime_compute` uses `protocol_version: "1"`; startup rejects missing or unknown versions. Version-1 envelopes matching the current retained-state field shape remain readable without a rewrite. Snapshot-only durable shapes are rejected even when labeled version 1; equal version numbers do not establish compatibility. Upgrade performs no migration, synthetic replay or retained-state discard.

`ResumeRequest.Workspace` carries the allocation's independently owned filesystem binding. Core obtains a ready binding before restore. Suspension must prove the source released active execution before a target can become a writer. Adapters validate the binding before native effects; an adapter without independent filesystem support rejects a non-null binding. `DeleteRetained` deletes only adapter-owned retained compute state, never the workspace. Explicit Session deletion first settles compute cleanup and then deletes independent storage, preserving a single active writer throughout recovery.

Core suspends the idle work of every provider that declares suspension support, with one fixed policy: it suspends work idle for 5 minutes (300 seconds) and keeps retained state for 24 hours (86400 seconds). The deployment's [`suspension`](../contracts/agents-api/sandbox-deployment.md#safe-response) reports these values. Core suspends initialized Environments, including those with no Turn yet, when no root or Subagent Turn is queued, in progress or waiting, no input, file operation or initialization is pending, and real activity has been idle for that time. The idle clock starts no earlier than the allocation entering the running compute phase, including after a wake. For allocations Core records initialization completion and root or child terminal transitions with the database clock in the same transaction. Candidate filtering and the Session-locked recheck compare elapsed database time with the idle duration, and the initial retained-state retention deadline is anchored to the same database observation, so Core and database host clocks need not agree. Native completion timestamps stay unchanged in public history but never drive idle admission, and heartbeats never reset activity. Before acknowledging a planned suspension, the daemon closes admission and drains native cleanup, output receipts and file work.

The Worker lease, the Session lock and the per-node gates own suspension for every provider. New Turn claims, file-write intents and capture admission serialize under the Session lock and share one compute-phase check; new pending work cancels a capture and wakes the same source. Normal preparation waits for the compute phase to be running, after the authenticated resume handshake, and pending input stays pending when its promotion conflicts with a lifecycle transition. Compute phases and revision-checked receipts live on the allocation. Core persists quiesce, capture and restore intent before the effect, only a fresh receipt performs a capture or restore, and recovery observes the exact attempt without retrying an unknown creation, capture or restore. Consumed retained state never rolls a running generation back. Deletion, revocation and retention expiry win over wake, up to the final database compare-and-swap, and unknown cleanup identities are kept until owned resources are confirmed absent. Consumed artifacts and old compute are deleted, so suspension cycles never build a chain of writable disks.

Queued work and live Environment file access wake a suspended Environment; history and published Artifact reads do not. Live file requests wait for the current Runtime’s credential-authorized connection and Harness declaration before entering the file work queue; a completed compute Create alone is not Runtime readiness. Retained state resumes on its original node. Planned suspension uses an Environment and suspension token on the daemon connection. A PID and start-time fenced local control signal (`RunCommandCompute`) wakes the parked daemon, which authenticates again before admitting work. A transient disconnect before confirmation retries the same armed suspension with bounded attempts and backoff; a permanent authentication or protocol rejection closes it. Core owns the retained state's retention deadline, and the daemon has no timer for it. A lost quiesce acknowledgement may thaw the same source through explicit rollback but never authorizes capturing it.

`Suspend` owns native resource release. Core releases active capacity only after receiving a bound retained handle, suspended state, `ResourcesReleased` and `SuspendSettled`. `ReconcileOnly` prohibits replaying the original capture or pause but permits adapter cleanup justified by durable retained evidence. An outcome without retained state permits rollback only with `SuspendSettled` and an exact source that can resume; other uncertainty retains ownership and closes admission. Core never unconditionally destroys the source after `Suspend`.

`Resume` consumes retained state once into the precommitted target. Recovery observes that same attempt. Core persists `waking`, authenticates and resumes the daemon, deletes consumed retained resources, then commits `running` and admits work. `DeleteRetained` is idempotent cleanup that preserves running compute. Cleanup failure keeps `waking` and cannot trigger another restore. `KillCompute` remains destructive; old-generation cleanup must not kill a newer active incarnation sharing its native ID. Every unreleased allocation, including a running one, consumes `max_retained`. Every reservation records the shared compute protocol version, including allocations whose compute phase is `disabled`. Activation rejects an unreleased allocation with a missing or different version; the old version must complete its normal cleanup before upgrade. Session history remains.

### Coordinated protocol upgrade

Upgrade Core and nodes together with node wire version 7, the E2B helper with private wire version 4 and its rebuilt template containing hosted suspension control, and the microsandbox helper with private wire version 5. All Providers deliver Runtime bootstrap version 2. Each boundary validates its own contract; version numbers are not interchangeable across boundaries. Before activation, use the previous compatible release to complete normal cleanup of incompatible unreleased allocations. The startup fence preserves their stored receipts and Session history and makes no Provider calls to migrate them.

### Cold replacement after checkpoint retention

Retained-state retention bounds compute resources, not the lifetime of an eligible retained Session. After the deadline, Core can clean up the allocation without terminating the Session only when its independent filesystem object is ready, initialization is complete, the Session and Environment remain nonterminal, and the previous authenticated Runtime has a persisted `retained_native_history` declaration. The capability and native recovery obligations are defined by the [Core–Runtime protocol](runtime-protocol.md). Filesystem retention alone is insufficient.

Core confirms the original creation is settled and all owned compute and retained states are cleaned up before releasing allocation and placement ownership. The old device's authority is revoked before replacement admission. Until Create, Kill or DeleteRetained has a confirmed outcome, it keeps ownership and admits no replacement writer. A recoverable Session and Environment remain disconnected with the same filesystem object and initialization result. Archive, reset and deletion take precedence and cannot be undone by a wake request.

New input or live Environment file access reserves a new allocation on a compatible node and bootstraps a fresh Runtime device. Preparation reuses the same filesystem and completed initialization rather than replaying initialization. Execution resumes the same native Session from retained history after validating the replacement Runtime's declaration. Missing or invalid native history fails explicitly; Core never substitutes a new native Session or replays completed input. History and published Artifact reads do not reserve compute. This path does not preserve VM memory, background processes or open connections, and it does not qualify an undeclared Runtime, Harness or filesystem combination.

### Reset and archive

A [reset](../contracts/agents-api/sandbox-deployment.md#reset) is durable execution state that the runtime manager advances outside its counted work. Start, escalation, cancellation, setup, update and finalization serialize through the mutation gate. Idleness is rechecked with the Session lock and then the deployment lock, never in the reverse order during finalization. Work is keyset-paged and bound to the reset's request time and generation, with the absolute deadline and validated audit provenance persisted.

One snapshot and timestamp partition the held resources. Offline ownership comes from the allocation's or active placement's node, with the same 45-second connection and owner-epoch predicate as online presence, independently of provider readiness; no cleanup failure, offline state or empty read authorizes a synthetic release. At zero held resources, Core drains outside database transactions, rechecks under the deployment lock and clears the deployment in one transaction, then publishes a generation-bearing empty provider without fallible work. If the final write or drain fails, it restores the committed provider with a bounded owner context before releasing the mutation gate; if that recovery fails, admission stays fenced and the owner stops.

An administrator [Session archive](../contracts/agents-api/admin-api.md#session-archive) keeps its Project scope and hosted eligibility checks in a Session-first transaction, together with Environment expiry, cancellation, Runtime authority revocation and audit; a reset's background archive reconstructs the actual Project scope from trusted records and keeps the requester's provenance. The ordinary provider lifecycle releases compute and retained states. Archive cancellation keeps a healthy receipt path until the terminal commit: only the archive that first revokes a device records its exact `archive_cancel_turn_id` (ordinary revocation clears it, and repeated cleanup keeps it), and the existing authenticated delivery may drain that cancellation for at most 20 seconds from the Turn's original `cancel_requested_at`. Core tracks the delivery through `done`, the cancellation acknowledgement and the terminal commit, independently of subscription removal, and grants no new connection, input, file or MCP authority or lease renewal. No transaction or lifecycle gate waits for the receipt, and a lost peer, expiry or restart falls back to ordinary failure and cleanup, never a fabricated cancelled outcome.

## Validate the integration

E2B template and endpoint validators in the installer and Go adapter consume the shared [selector fixtures](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/sandbox/e2b/testdata/configuration-selectors.json). Extend these cases with any validation change so both entry points accept the same selectors.

Run `make check-sandbox-provider-contract` while developing. It runs the shared [`contracttest`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/contracttest) suite through real adapter boundaries with controlled native failures, plus the adapter and node transport tests; `make check` includes the same packages. Call the public failure runner with native-side fixtures instead of a fake `SandboxProvider`, and keep tests for foreign ownership, unknown mutation results, cancellation, no automatic replay, failed cleanup and reference-bound settlement.

Node tests separately cover disconnect and reconnect fencing and cleanup after a lost Create response. Helper protocols and the [sandbox node protocol](../contracts/agents-api/node-generation-protocol.md) require an exact version match; direct in-process interfaces have no separate wire version.

Native acceptance proves what fixtures cannot: creation, lease behavior, owned partial cleanup, declared isolation and limits, and snapshots where supported. The opt-in Docker lifecycle and recovery tests use `AGENTS_RUNTIME_DOCKER_TEST_IMAGE`; the SDK helpers use `make check-e2b-provider` and `make check-microsandbox-provider`. Mocked compute proves neither reclamation nor isolation.

## Reference adapters

| Kind | Adapter | Helper and adapter rules | Operator guide |
| --- | --- | --- | --- |
| Docker (node) | [`sandbox/docker`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/docker) | Node proxy in [`sandbox/node`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/node) | [Docker adapter](#docker-adapter) |
| microsandbox (node) | [`sandbox/microsandbox`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/microsandbox) | [`tools/microsandbox-provider`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/microsandbox-provider/README.md) | [Nodes](./getting-started/nodes.md) |
| E2B (direct) | [`sandbox/e2b`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/e2b) | [`tools/e2b-provider`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/e2b-provider/README.md) | [Sandbox deployment](../contracts/agents-api/sandbox-deployment.md#e2b-configuration); application-managed templates in [`deploy/e2b`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md) |

## Docker adapter

The Docker Sandbox Provider ([`sandbox/docker`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/docker)) runs every Runtime image, whichever Harness it serves, with the same container settings ([`container_options.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/sandbox/docker/container_options.go)):

- user 1000:1000, read-only root filesystem, all capabilities dropped, `no-new-privileges`, the [seccomp profile](#seccomp-profile) and AppArmor `unconfined`;
- the node’s configured network and extra hosts ([node configuration](./configuration.md#docker-node-configuration));
- CPU and memory from the deployment specification, a 128-process limit and a 128 MiB `/tmp` tmpfs;
- two named volumes labelled with the installation, tenant, Environment and allocation: `<name>-home` at `/home` and `<name>-environment` at `/environment`, whose `workspace` subdirectory is also mounted at `/workspace`. The Docker Engine must support volume subpath mounts;
- with the configured `nested_sandbox` option, Docker's `/proc` masks are lifted (`/sys/firmware` and `/sys/devices/virtual/powercap` stay masked) and the container runs an init process.

Create refuses to reuse retained volumes that have no container. It copies the [Runtime bootstrap](./runtime-bootstrap.md) file to `/home/runtime/runtime-bootstrap.json` (mode 0600, UID 1000) and the `/environment` workspace, staging, initialization and package directories into the container, then starts `oac-daemon connect --profile default --bootstrap-file /home/runtime/runtime-bootstrap.json`. When the created container does not have the configured CPU, memory and exact image, Create returns the error with `CreateSettled`. Docker has no lease, so Renew only reads the container state. Kill checks the ownership labels of the container and both volumes before removing any of them, then confirms that all three are gone.

The node uses the explicit Unix socket in its [provider configuration](./configuration.md#docker-node-configuration) and ignores `DOCKER_HOST`. No Docker socket, host home or Core credential is mounted into a Runtime.

### Seccomp profile

[`seccomp.json`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/codex/seccomp.json) is the Moby default profile at [revision 65adc7e](https://github.com/moby/profiles/blob/65adc7e022c97f55e45c054ff012988027733b87/seccomp/default.json) (Apache-2.0, see [seccomp.LICENSE](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/codex/seccomp.LICENSE); upstream file SHA-256 `785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334`) with one appended rule that allows `clone`, `unshare`, `setns`, `mount`, `umount2` and `pivot_root`. The distribution ships this file to every Docker node as `runtime/seccomp.json`.

## Independent workspace attachment

`Bootstrap.Workspace` and `ResumeRequest.Workspace` optionally carry a [workspace filesystem binding](workspace-provider.md). Absence selects the Provider's owned Environment disk; presence selects the exact external object and never permits a fallback to an owned disk. Providers validate the tenant and Environment against the binding and its configuration receipt before resolving it. `LocalOptions.Workspace` injects the filesystem resolver; Sandbox Providers never interpret filesystem adapter parameters or native receipts.

`DeploymentPolicy.Workspace` declares supported attachment requirements; absence means external storage is unsupported. The derived immutable `DeploymentSpec.Workspace` receipt selects the generation mode and capabilities; its [deployment workflow](../contracts/agents-api/sandbox-deployment.md#resources) never copies filesystem configuration into the generation. Microsandbox requires `host_directory` and `user_xattr`; Docker and E2B reject external attachments. `ValidateWorkspacePolicy` uses the filesystem protocol's combination validator: an external filesystem without an enforced quota rejects a positive `EnvironmentDiskMiB`, while zero requests no quota. Root disk capacity is still required. Without an external declaration, the existing owned disk bounds apply.

Microsandbox resolves each Create and Resume binding, including observation of an interrupted restore, and passes only the local directory and immutable ObjectID to its private helper. The helper binds the whole `/environment`; its retained contents and private native history follow the [workspace filesystem scope](workspace-provider.md#lifetime-and-filesystem-scope) and [Runtime resource directories](configuration.md#runtime-resource-directories). Private HOME, credentials and the compute root are not a durable history store. Explicit mode, object and path labels qualify native resources; mount shape alone never selects a mode. Same-node checkpoint restore verifies the snapshot's object, remaps `/environment` through SDK `Volumes`, requires strict external mount policy and rejects every restore warning. Partial restore errors retain the exact target identity for cleanup and never establish readiness. Unknown outcomes remain observations of the original operation. Native Bind checkpoint and guest-local flock continuity evidence does not establish cross-VM fencing or cross-node memory restore.
