# Add a Sandbox Provider

A **Sandbox Provider** supplies the outer compute (an *Environment*) that a Core
Runtime daemon runs in, plus the bounded bootstrap that starts it. This guide is
the single start-to-finish path for adding one. The canonical interface is
[`SandboxProvider`](../services/agents-api/internal/sandbox/sandbox_provider.go).

## Before you start

| Term | Meaning |
| --- | --- |
| Environment | Durable execution place owned by Core; see [Environments](../contracts/agents-api/environments.md) |
| Allocation | One Core-owned compute lease for an Environment, identified by `Reference` |
| Runtime | The daemon inside the Environment; it prepares capabilities and executes Turns |
| Deployment | The single deployment-wide provider selection; see [Sandbox deployment](../contracts/agents-api/sandbox-deployment.md) |

Core owns durable Environment, allocation, placement and cleanup state. The
Provider supplies compute and bootstrap only. Runtime prepares capabilities and
workspaces through the common [daemon protocol](runtime-protocol.md); Harness
adapters translate execution. A user-owned machine uses the same Runtime
contract but has no Core-owned allocation to create or destroy.

Use maintained provider SDKs behind thin adapters. Hosted deployments select one
deployment-wide Provider: E2B cloud, or Docker/microsandbox on
administrator-owned nodes. Providers never execute Core initialization commands;
initialization, daily execution and Files use the daemon's typed Runtime operations.

Isolation belongs to the outer infrastructure. The daemon runs with its
launching user's authority and adds no filesystem, tool or network sandbox.
Qualify provider isolation and network enforcement independently of daemon
connectivity; see [Runtime and outer isolation](design-principles.md#runtime-and-outer-isolation).

## Steps

1. **Read the contract.** Implement the five required operations and explicitly handle all
   extension interfaces in [Implement the interface](#implement-the-interface).
2. **Write the adapter package** under `services/agents-api/internal/sandbox/<kind>`
   (native SDK calls, ownership checks, identity translation, private config).
   Assert `var _ sandbox.SandboxProvider = (*YourAdapter)(nil)` at compile time.
   Out-of-process helpers live in `services/agents-api/tools/<kind>-provider`.
3. **Register the kind** once using
   [Register the provider kind](#register-the-provider-kind). Registration is
   explicit construction, not an init-time plugin registry.
4. **Label owned resources** with `io.oac.*` labels, or `oac_*` metadata keys
   where the vendor rejects dotted keys. See [Label naming](#label-naming).
5. **Run the contract suite** with `make check-sandbox-provider-contract`. See
   [Validate the integration](#validate-the-integration).
6. **Run native acceptance** against real compute, then `make check`. Record
   evidence and limits in [Sandbox deployment](../contracts/agents-api/sandbox-deployment.md).
7. **Document operator setup** next to the adapter, like the
   [reference adapters](#reference-adapters).

## Implement the interface

`SandboxProvider` has **five required operations**:

| Operation | Purpose |
| --- | --- |
| `Create` | Create compute for a `Reference` and run the bounded daemon bootstrap |
| `GetInfo` | Observe current compute without mutation |
| `Renew` | Extend a native lease, or observe when the backend has none |
| `Kill` | Reclaim the allocation's compute and retained resources |
| `RunCommand` | Bounded administrative bootstrap/diagnostic command |

Use the existing types; do not introduce another lifecycle protocol or a
vendor-specific execution path. A backend without a native renewable lease
(Docker) still keeps service-owned hosted expiry and cleanup requirements.

### Explicit operation contracts

Keep the existing small interfaces. Every Provider must implement their methods
and return a complete `ProviderOperations()` declaration. The interface methods
are the operation inventory; `sandbox.ValidateOperations` checks it without a
second hand-maintained list.

| Contract | Requirement | Responsibility |
| --- | --- | --- |
| `sandbox.SandboxProvider` | Five operations must be supported | Allocation lifecycle and bounded administrative commands |
| `sandbox.CheckpointProvider` | Explicit supported or unsupported decision for every method | Exact compute incarnations, capture/restore, retained-source resume and cleanup |
| `runtimeobs.Source` | Explicit decision | Ownership-checked read-only observations |
| `runtimeobs.BatchSource` | Explicit decision | Bounded observations in input order, with per-target errors |
| `sandbox.SelectionDiscoverer` | Explicit decision | Read-only native configuration discovery before commit |
| `sandbox.CredentialVerifier` | Explicit decision | Verify access to owned resources without mutation |

Each declaration entry has `state: supported` with no reason, or
`state: unsupported` with an authored reason code. Missing entries, zero states,
unknown entries, missing methods and unsafe reasons fail validation. The entire
checkpoint lifecycle must agree on support; batch observation requires single
observation. Adding a method to an existing interface requires an explicit
decision and implementation in every adapter. Never supply a default base class
or generate blanket unsupported implementations for future methods.

Unsupported methods return `providercontract.UnsupportedError` before native
I/O. The error identifies the exact operation and a safe code, not a native
message, resource identity, endpoint or credential. Empty results, nil errors,
`Unavailable`, and unknown mutation outcomes cannot substitute for unsupported.
The five required methods cannot return unsupported; a backend without a native
lease preserves the existing read-only `Renew` semantics.

Each adapter owns one `Operations()` function, shared by its concrete instance
and registration. `providers.ValidateBinding` checks both against the existing
interfaces and against each other. Runtime admission and node generation loading
also reject incomplete providers. Interface assertions establish method shape
only; callers use the declaration to decide whether an operation is supported.

`ObserveBatch` returns an error instead of an ambiguous boolean. Only a typed,
safe `UnsupportedError` for `ObserveBatch` permits per-target `Observe` calls.
An unavailable service, timeout or other failure never triggers that fallback.
Observation never renews, starts, prepares or stops compute; see the
[observation contract](../contracts/agents-api/runtime-observability.md).

Contract tests call every declared unsupported native method with no configured
native client, require its matching error and zero result, and reject incomplete
or contradictory declarations. Supported behavior still requires native and
lifecycle tests; declaration validation alone cannot prove SDK semantics.

`RunCommand` is an existing administrative bootstrap/diagnostic facility, not an
alternate route for Skills, Plugins, MCP setup, initial files, Session execution or
Files. Those use Runtime. Confidential command input travels in `Command.Stdin`,
not argv or logs. Preserve its byte order, bounded output and actual exit status.

### Identity and resource ownership

`Reference` is the exact `(TenantID, EnvironmentID, AllocationID)` tuple. Core
persists a fresh allocation ID **before** `Create`; it is not the Environment ID.
The adapter also binds resources to its installation. Do not locate or authorize
resources by a bare native ID, display name, guessed path or an unverified label.
All mutations, reads and cleanup must verify the same ownership.

Core serializes lifecycle operations and retains the allocation after any
uncertain mutation. An adapter must preserve enough native identity/receipts for
observation and cleanup. A failed call may return both `Info` and an error:
preserve reference-bound settlement evidence without converting failure to success.
Adapters must not silently create replacement resources, overwrite credentials or
switch to a new allocation after a conflict.

`Info.ProviderID` and `Info.State` describe compute. Core recognizes `running`
only with the matching Reference and a native identity. Other native states can
be observed without claiming readiness. `BootstrapComplete` says the bootstrap
reached its final mutating step. `CreateSettled` proves the original attempt can
no longer mutate resources; it does not mean success. In particular:

- `State="absent"` plus matching Reference and `CreateSettled=true` is an explicit
  creation-absence receipt, with no native ID or completed bootstrap.
- `ErrNotFound`, an empty listing, a timeout or a successful `Kill` alone is not
  proof that an in-flight create cannot appear later.
- Core can also settle creation from a matching running resource with completed
  bootstrap. It must retain unknown creation until it has such evidence or an
  explicit receipt, even if a cleanup attempt currently sees no resources.

`Kill` owns cleanup of the allocation's compute and retained resources, including
partial bootstrap storage. It must not remove another tenant's resource on a
name collision. Core releases durable ownership only after confirmed cleanup
**and** settled creation. Executor release or Harness cancellation does not delete
an Environment, its workspace or its Sandbox Provider allocation.

### Operation outcomes and retries

All calls receive bounded contexts. Expiry or cancellation ends the caller's
wait; it does not prove rollback, native stop, cleanup or absence. An adapter or
transport must not detach untracked mutations or replay a timed-out command.

| Operation | Confirmed result | Failure or unknown result | Recovery |
| --- | --- | --- | --- |
| `Create` | Matching compute and bootstrap evidence; execution still needs Runtime preparation | Invalid/foreign configuration rejects; duplicate returns `ErrExists`; transport failure may hide created resources | Observe the original Reference. Never replay `Create`, even with a new credential. Preserve partial resources for owned cleanup. |
| `GetInfo` | Current compute observation without mutation | `ErrNotFound` is only a missing observation; an error is not absence proof | Repeat a bounded read; never turn it into create/start/renew. |
| `Renew` | Existing native lease extended, or an observation for a provider without leases | Timeout may hide a lease extension; stopped/missing compute stays stopped/missing | Observe then let the normal reconciler renew the same allocation. Never revive it or fabricate a lease expiry. |
| `Kill` | Owned compute and retained storage removed; repeated confirmed absence succeeds | Error retains ownership and cleanup intent; ownership mismatch must not delete foreign resources | Retry cleanup of the same Reference after outstanding creation/mutation is fenced. Do not release the durable owner early. |
| `RunCommand` | Collected output and actual exit code; nonzero exit is a settled command failure | Missing native completion is `ErrCommandUnconfirmed`; partial output is not success | Do not replay. Preserve the owner and reclaim before reuse when completion cannot be proved. |

Use `ErrInvalid`, `ErrOwnership`, `ErrExists`, `ErrNotFound`,
`ErrComputeUnconfirmed` and `ErrCommandUnconfirmed` for their existing meanings.
An unclassified native/transport error is conservatively unknown, not permission
to retry a mutation. Core must not interpret provider diagnostics as lifecycle
truth or expose native error text/credentials. The node boundary maps errors to
fixed codes; direct SDK details stay private.

Checkpoint support adds `Compute` generation/name/ID and `SnapshotIdentity`.
Persist operation IDs and provider-returned snapshot provenance unchanged.
`ObserveOnly` on suspend/resume observes the previous attempt and must not start
another capture or restore. `ResumeCompute` only thaws the retained source; it
must not cold-start a stopped one. Cleanup targets the exact compute incarnation
and snapshot, not whichever instance currently has the same display name. See
[the lifecycle implementation](../services/agents-api/internal/execution/runtime_compute.go)
and its failure tests before advertising this capability.

### Four distinct readiness facts

| Fact | Evidence | Does not establish |
| --- | --- | --- |
| Compute available | Provider observation for the owned allocation | Authenticated Runtime connection or prepared capabilities |
| Runtime connected | Gateway authentication and exact Environment/device binding | Completed Runtime preparation or a usable Harness |
| Capabilities prepared | Successful common Runtime preparation with the fixed configuration/snapshot | Acceptance or completion of a Turn |
| Execution admitted | Qualified Harness capabilities and the existing executor/Turn acceptance path | A completed input, cancellation or reclaimed compute |

For both hosted and self-hosted environments, Core uses the daemon preparation
and execution protocol. Core resolves configuration and supplies resources;
Runtime installs/reads local capabilities and freezes the result for the Session.
Reconnection can reuse that prepared content; a new Session takes a new snapshot.
A provider must not implement a competing preparation path.

## Register the provider kind

`sandbox/providers/registry.go` is the sole registration table. Each entry binds
an adapter's specification/resource validators, selection normalization, deployment
mode, defaults, the adapter-owned operation declaration, and local or direct constructor.
`providers.Build` constructs node-local adapters; `providers.BuildDirect` constructs
direct adapters. Neither allocates compute. There is no init-time registration or
runtime plugin loading.

For a new implementation:

1. Implement the operation contracts above in the adapter package and add native contract tests.
2. Add its configuration validators and optional read-only `SelectionDiscoverer`
   for native resource discovery. Normalization must copy input before changing it.
   `RestoreSelection` must retain access to owned resources without requiring new
   template validation. Put native credential verification behind
   `CredentialVerifier` when needed.
3. Register its constructor, policies, operation declaration and defaults in `providers/registry.go`.
   Node proxy identity and checkpoint advertisement consume this same entry.
   The installer projection uses those registered policies and the common field
   bounds in `sandbox/deployment_contract.go`; regenerate it with
   `go run ./services/agents-api/cmd/specification-contract -write`.
4. If new configuration fields are necessary, extend the typed `sandbox.Selection`
   envelope and its dedicated encrypted persistence fields, API DTO and operator
   client. Do not replace typed configuration with unrestricted JSON. Field codecs
   may map columns; Store must not parse native endpoints, templates or defaults.
5. Supply required installer/distribution artifacts and operator labels. A new
   provider must not add a Session/Turn scheduling path or a Store vendor switch.

Preview and persistence use `providers.Normalize` and `providers.Describe`.
`SelectionDiscoverer` resolves omitted native resource values before commit; the
complete specification is validated again at persistence. Store owns transactions,
credential encryption, generation fencing, resource ownership and typed column
mapping. Database constraints validate structure, not the registration list.
`providers.ResolveChange` owns configuration inheritance and comparison uses
normalized selectors, so preview, retry and commit share the same defaults.

A direct adapter with credentials verifies all retained generations and allocation
references before replacing a key. The common `sandbox.CallFence` excludes native
calls and waits for helper completion, including calls whose callers timed out.
Execution invokes prepared verification/fencing callbacks without branching on a
vendor. A transport wrapper advertises only capabilities that its adapter supports;
new optional capabilities need forwarding and qualification before registration.

Keep vendor-specific deployment validation and SDK setup at the construction
boundary. Construction must not create an Environment. For node-local adapters
the `providers.Built` result returns the provider, probe, installation identity, backend
fingerprint and specification digest; the factory also returns its close function.

Preserve the `execution.RuntimeProvider` deployment binding: `ProviderKind`,
installation ID, backend fingerprint, generation, mode and node ownership
identify the backend. The database owns the selection; the in-memory selection
is not an alternate authority. Register optional interfaces consistently on
direct adapters and their transport wrappers. A new public capability needs its
own protocol change.

Today Docker and microsandbox use nodes; E2B is constructed directly. The node
proxy exposes checkpoint operations only for its registered checkpoint-capable
backend. A new node backend must register both construction and the corresponding
proxy capability; otherwise that capability is unavailable. Provider names belong
in this adapter/configuration wiring, not Session scheduling, capability
preparation or Turn execution. Common lifecycle code uses `CheckpointProvider`
to admit suspension, independently of a provider name.

The backend fingerprint identifies a native resource namespace, not mutable
capacity. Core retains deployment generations so old owned allocations continue
to resolve to their original backend. This is resource ownership, not historical
binary compatibility. Do not repoint retained allocations at a replacement backend.

## Label naming

Every owned native resource carries ownership labels so mutations, reads and
cleanup can verify the `Reference` and installation.

- Use the `io.oac.` prefix, for example `io.oac.installation` and `io.oac.tenant`.
- Where the vendor rejects dotted keys, use `oac_` metadata keys (E2B).
- Never accept older label names as a fallback; see
  [Runtime names](../CONTRIBUTING.md#openagentcore-runtime-names).

## Validate the integration

Run `make check-sandbox-provider-contract` while developing. It runs the shared
[`contracttest`](../services/agents-api/internal/sandbox/contracttest) suite through
real adapter boundaries using controlled native failures, plus existing adapter
and node transport tests. The same packages are included in `make check`.
New adapters should call the public failure runner with native-side fixtures,
not substitute a fake implementation of `SandboxProvider` for the adapter under
test. Preserve tests for foreign ownership, unknown mutation results, cancellation,
no automatic replay, failed cleanup and reference-bound settlement.

Node tests separately exercise disconnect/reconnect fencing and cleanup after a
lost create response. Provider helper protocols and the node protocol require an
exact version match and reject mismatches; do not add fallback decoders or old
binary migration. Direct in-process interfaces have no independent wire version.
Node generation management is an explicit current hello capability, not another
wire version. Fixed-configuration manual nodes use the same protocol and only
serve their enrolled deployment generation. See the
[node contract](../contracts/agents-api/node-generation-protocol.md).

Run `make check` with its dedicated database before completion. Retain native
acceptance for SDK behavior that fixtures cannot prove: creation, lease behavior,
owned partial cleanup, declared isolation/limits, and snapshots where supported.
The opt-in Docker lifecycle/recovery tests use `AGENTS_RUNTIME_DOCKER_TEST_IMAGE`;
SDK helper tests use `make check-e2b-provider` and
`make check-microsandbox-provider`. Passing controlled contract tests is not a
claim of live cloud or model acceptance.

Mocked compute cannot prove that resources were reclaimed or that the outer
Environment provides isolation.

## Reference adapters

| Kind | Adapter | Helper | Operator guide |
| --- | --- | --- | --- |
| Docker (node) | [`sandbox/docker`](../services/agents-api/internal/sandbox/docker) | Node proxy in [`sandbox/node`](../services/agents-api/internal/sandbox/node) | [Hosted sandbox manager](../services/agents-api/HOSTED-SANDBOX-MANAGER.md) |
| microsandbox (node) | [`sandbox/microsandbox`](../services/agents-api/internal/sandbox/microsandbox) | [`tools/microsandbox-provider`](../services/agents-api/tools/microsandbox-provider) | [`deploy/microsandbox`](../services/agents-api/deploy/microsandbox/README.md) |
| E2B (direct) | [`sandbox/e2b`](../services/agents-api/internal/sandbox/e2b) | [`tools/e2b-provider`](../services/agents-api/tools/e2b-provider/README.md) | [`deploy/e2b`](../services/agents-api/deploy/e2b/README.md) |

Provider selection and E2B setup are owned by
[Sandbox deployment](../contracts/agents-api/sandbox-deployment.md).
