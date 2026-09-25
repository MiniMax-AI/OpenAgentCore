# Managed sandbox deployment configuration

This deployment-administrator contract selects the provider, per-sandbox resources
and immutable Runtime for Core-managed `openai_hosted` execution. PostgreSQL owns
one active selection per installation. Web and the administrator API write the
same configuration. Node files contain its installed copy and host-specific paths;
they cannot override its resources or Runtime.

The public `/v1` Agent API, Environment Templates and caller-managed `self_hosted`
provisioning are unchanged. A provider selection is independent of the harness.
A deployment can remain unconfigured, with no execution nodes or hosted admission.

See [Hosted Sandbox Manager](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md)
for the operator workflow. Generated schemas cover the
[administrator routes](core.openapi.yaml) and the
[node machine connection routes](runtime.openapi.yaml).

## Authority and routes

| Method and route | Authority | Effect |
| --- | --- | --- |
| `GET /core/v1/sandbox/deployment` | Core key | Read the safe active configuration and retained-resource counts |
| `POST /core/v1/sandbox/deployment` | Core key | Select the initial provider, resources and Runtime |
| `PUT /core/v1/sandbox/deployment` | Core key | Replace a fully drained selection while maintenance is enabled |
| `PATCH /core/v1/sandbox/deployment/maintenance` | Core key | Pause or resume fresh hosted admission at the expected generation |
| `GET /api/v1/sandbox-node/configuration` | Enrollment token or retained node credential | Read the active node installation configuration without consuming enrollment |

The paired console injects the Core key server-side on every signed-in `/core/v1`
request. The browser never receives that key. Node configuration
and enrollment are machine connection routes under `/api/v1`, which the reverse
proxy sends directly to Core; the console does not serve them. They use their own
Bearer credential; a console login, the Core key or a Project key does not grant
node enrollment authority.

Node capacity is separate from the deployment specification. The administrator's
`POST /core/v1/sandbox/enrollment-tokens` accepts optional `max_active` and
`max_retained`, defaulting to 2 and 8. Microsandbox uses both limits. Docker never
suspends, so its `max_retained` always equals `max_active`; Core replaces any
submitted value, here and in `PATCH /core/v1/sandbox/nodes/{node_id}`. Core stores
that approval with the token and copies it to the registered node. Node enrollment
cannot submit capacity overrides. The existing node configuration/identity reads
expose approved limits for that credential; node updates remain administrator
operations. Local host capacity checks may reject a deployment that cannot run
safely, but never raise its limits.

Each node in `GET /core/v1/sandbox/nodes` and its detail reports `core_url`: the
installation public URL when the node enrolled. A node whose `core_url` differs from
the current public URL receives no new placements. Work already placed on it
finishes there: a hosted Environment that was placed but not yet allocated before
the change is still allocated on that node, and its retained sandboxes can still
resume, while the old address reaches Core. Remove it and add it again.

`GET /core/v1/sandbox/nodes/{node_id}/allocations` lists the node's unreleased
allocations. Each item's `compute_phase_changed_at` is the time the allocation
entered its current `compute_phase`, or null when unknown; an allocation that
existed before Core recorded it reports null until its next phase change. A
suspended microsandbox allocation's age, combined with the deployment's snapshot
retention, tells roughly when Core reclaims it.

## Selection request

POST and PUT take the same complete selection. PUT also requires a nonzero
`expected_generation` read from the current deployment.

| Field | Meaning |
| --- | --- |
| `provider` | Exactly one of `docker`, `microsandbox`, `e2b` |
| `resources` | Per-sandbox resource limits described below; required for Docker/microsandbox, optional for E2B |
| `runtime` | Required immutable distribution identity for Docker/microsandbox; absent for E2B |
| `e2b` | Required only for E2B: write-only `api_key` and immutable `template` build selector |

The request has no Core address. Core derives the deployment's `core_url` from the
installation public URL (`public_url` in `config.json`, `AGENTS_API_PUBLIC_URL` for
Core): the HTTPS origin nodes and sandbox guests use to reach Core. A request that
contains `core_url` is rejected with 400 `invalid_request_error` and
`param: "core_url"`. E2B guests reach Core from E2B's cloud, so an E2B selection is
rejected with 409 `sandbox_configuration_error` while the public URL is loopback.
Docker and microsandbox selections do not depend on the address; a loopback public
URL serves local development only, because a guest's loopback address does not
reach its host. Changing the public URL is an installation change, not this
operation: nodes enrolled with the old address receive no new sandboxes and must
be removed and added again.

### Resources

| Field | Accepted value |
| --- | --- |
| `cpus` | Integer, 1 through 255 |
| `memory_mib` | Integer, 512 through 1048576 MiB |
| `root_disk_mib` | Microsandbox: at least 1024 MiB; Docker/E2B: omitted or zero |
| `environment_disk_mib` | Microsandbox: at least 1024 MiB; Docker/E2B: omitted or zero |

These limits describe each sandbox. Node `max_active` and `max_retained` remain
separate reservation limits; host telemetry is an observation, not permission to
exceed either limit. Native providers may reject values that pass these structural
bounds.

Docker applies CPU and memory limits and checks the running container's limits
and exact image identity. It does not provide independent hard root or workspace
disk quotas through this contract. E2B CPU and memory must match the exact ready
template build; Core validates that build through the pinned SDK before saving.
E2B disk capacity remains part of its native template. Neither provider silently
accepts a requested disk quota that it cannot enforce. An E2B selection may omit
`resources`: Core then validates that the build is ready and stores its CPU count
and memory as `cpus` and `memory_mib`, returned in `specification.resources`
without disk fields. The stored specification is complete and validated either way. On restart, Core loads the
committed E2B selection without repeating template-build validation. Existing
resource inspection and cleanup use the original credentials and provider
receipts; new selections still require successful template validation.

Microsandbox configures CPU, memory, managed root disk and a separate owned disk
at `/environment`. Restored root capacity can be inherited from the verified full
snapshot when native restore metadata omits a configured size. That exception
requires the original resource proof and exact snapshot/target identity; it does
not resize a restored root or treat a missing size as arbitrary capacity. Other
native limits must still match. A failed readiness or configuration check retains
ownership and cleanup records.
An interrupted restore can finish the derived proof on its existing, verified
target; it cannot create another instance or change limits. Snapshot observation
for cleanup verifies ownership and artifact identity without requiring that the
source still qualify for execution.

### Runtime release

Docker/microsandbox use all fields of one verified distribution:

| Field | Identity |
| --- | --- |
| `source_commit` | Lowercase 40-character commit SHA |
| `image_id` | Docker image configuration ID, `sha256:` followed by 64 lowercase hex characters |
| `image_manifest_digest` | OCI image manifest digest, in the same `sha256:` form |
| `microsandbox_ref` | `parsar-core-runtime@sha256:` followed by 64 lowercase hex characters |
| `runtime_sha256` | SHA-256 of the native microsandbox Runtime binary |
| `firmware_sha256` | SHA-256 of the matched firmware |

Copy these identities from the matched distribution manifest. Image configuration
IDs and OCI manifest digests identify different objects; do not substitute one
for the other. The node installer verifies the saved release against its payload
before registration and retains the exact local image identity it imports.

E2B instead uses `e2b.template` in `template-id:build-uuid` form. The build UUID must
be canonical and nonzero; a mutable template alias alone is insufficient. Omit
`runtime`. The API key is encrypted in PostgreSQL and never returned in a safe
view, bootstrap configuration, command argument or log. Replacing the key or
build uses the same drained maintenance transition as changing resources.

## Safe response

GET and successful mutations return `installation_id`, `provider`, `core_url`
(read-only: the installation public URL, present before configuration), `mode`,
`generation`, `owner_epoch`, `maintenance`, `suspension` and resource
accounting. A configured deployment also returns `specification` and
`specification_digest`. E2B returns only `e2b.template`,
`e2b.credential_configured` and `e2b.template_build`; the `e2b` object is absent
for Docker and microsandbox.

`e2b.template_build` is `{status, resources: {cpus, memory_mib, root_disk_mib}}`:
the fixed build as Core read it through the pinned SDK when the selection was
saved. GET does not call E2B, so it stays cheap and cannot fail on an E2B outage;
the values describe the immutable build at selection time. Validation admits only
a `ready` build whose CPU count and memory equal the selected `cpus` and
`memory_mib`. `root_disk_mib` is the build's native disk size, which Core does not
enforce separately. Unknown values are null, including every value of a selection
saved before Core recorded them; re-saving the selection records them.

`suspension` is `{idle_seconds, retention_seconds}` for microsandbox, the only
provider Core suspends (currently 300 and 86400). Docker, E2B and unconfigured
deployments return null.

Two similarly named fields have different purposes:

- Request `resources` and response `specification.resources` contain per-sandbox
  CPU, memory and supported disk limits.
- Response `resources.allocations` and `resources.pending` count unreleased
  allocations and pending hosted Environments without an allocation.

An unconfigured deployment has an empty provider and no specification. Docker and
microsandbox use `mode: nodes`; E2B uses `mode: direct` without a synthetic node.
`generation` identifies the saved selection. `owner_epoch` fences execution-owner
and node connections; it is not a replacement for `expected_generation`.
Treat `specification_digest` as the server-provided identity of the provider,
resource limits and Runtime release. Enrollment echoes it unchanged.

## Initialization and changes

POST validates the candidate before persistence and creates no compute, Session
or model request. An exact retry returns the existing generation. A different
selection conflicts once initialization has succeeded. Missing provider
prerequisites or failed candidate validation leave the previous selection intact.

For a replacement:

1. Read the deployment and PATCH `{"maintenance":true,"expected_generation":N}`.
   Maintenance blocks new hosted Sessions and fresh allocations while preserving
   admitted work, reads, receipt retries and explicit cleanup.
2. Verify both response counts are zero before replacing the selection. Stopped
   compute, snapshots, uncertain operations, pending cleanup and unallocated
   hosted Environments remain blockers. Use the explicit Session archive flow below.
3. PUT the complete replacement selection with `expected_generation: N`, without
   `core_url` (a request that contains it gets 400). Core checks the generation and resources, prepares and
   validates the candidate, then drains the existing manager calls. A short Store
   transaction repeats the guards and commits a changed selection, increments
   its generation and retires old nodes and unused enrollment tokens together.
   The existing manager publishes the prevalidated configuration after commit.
4. Read the returned generation and explicitly PATCH maintenance to `false`.
   Resume requires the committed generation to be active.

To release a retained hosted Session, explicitly POST
`/core/v1/projects/{project_id}/sessions/{session_id}/archive` with
`{"expected_generation": N}` while maintenance is enabled. Poll GET on the same
path until its resource state is `released`, then recheck the deployment counts.
The [administrator archive contract](admin-api.md#administrative-session-archive)
preserves public history and persisted Files/Artifacts, but discards unpersisted
workspace contents and prevents the original Session from resuming. An active
Turn may still be finalizing after resources are released. Ordinary Session
deletion remains separate. Maintenance and PUT do not clear resources; snapshots,
pending Environments and unknown cleanup remain blockers until the existing
lifecycle confirms release.

Provider validation, cancellation and draining hold no database transaction or
manager map mutex. One mutation gate, the existing execution lease and the Store
checks serialize changes; there is no second configuration owner. A rejected
candidate does not replace the previous provider or drain its workers. A later
commit failure leaves maintenance enabled and the previous selection retained.
After an interrupted drain, retries and resume must wait for that drain to finish.

An exact PUT retry at the still-current generation is a no-op when the clean
maintenance guards hold. A successful changed PUT makes the old expected
generation stale. After an uncertain write response, GET the deployment before
choosing another mutation; do not automatically replay writes. No step deletes
compute automatically, migrates an existing Session or rewrites historical
allocation ownership. Revoking an E2B key before cleanup can leave unverifiable
resources that block replacement.

## Node configuration and enrollment

For a new node, send `Authorization: Bearer <enrollment-token>` to the configuration
GET without `X-Parsar-Node-ID`. The token must be valid, unexpired, unconsumed and
belong to this installation. This read does not consume it. Maintenance prevents
new enrollment configuration reads.

An already registered node sends its durable node credential as Bearer and its
UUID in `X-Parsar-Node-ID`. Its installation, saved generation and specification
digest must match the active deployment. This read remains available in
maintenance so the retained node can recover its exact configuration. The old
enrollment token cannot replace a registered node's credential.

The response contains `installation_id`, `provider`, `core_url` (the installation
public URL), `generation`, `specification` and `specification_digest`. It contains no administrator, Project
or E2B credential. It is available only for node-backed providers.

The installer reads this configuration before preparing local assets. Its provider
file records `generation` and `specification`, alongside the host's socket, paths
and network policy. Enrollment at `POST /api/v1/sandbox-node/enroll` includes
`deployment_generation` and `specification_digest`. A mismatch rejects before
token consumption. Retained node authentication checks the same generation and
digest. A changed local resource setting, Runtime or generation must fail rather
than rewrite the retained identity or silently use a local default.

## What each field means per sandbox provider

Some fields keep one name across providers but differ in meaning, or do not apply.
Deployment fields come from `GET /core/v1/sandbox/deployment`; node and allocation
fields from the administrator node routes; runtime fields from the
[Runtime observation API](runtime-observability-api.md), with `disk` only in the
`GET /core/v1/sandbox/runtime-observations`, and the
[Runtime history API](runtime-history-api.md).

| Field | E2B | Docker | microsandbox |
| --- | --- | --- | --- |
| Deployment `specification.resources` | `cpus` and `memory_mib`, equal to the ready template build's and taken from it when omitted; no disk fields | `cpus` and `memory_mib`; no disk quota | `cpus`, `memory_mib`, `root_disk_mib` and `environment_disk_mib` |
| Deployment `specification.runtime` | Absent; the build is selected by `e2b.template` | The full [release](#runtime-release); nodes match `image_id` or `image_manifest_digest` | The full [release](#runtime-release); nodes match `microsandbox_ref`, `runtime_sha256` and `firmware_sha256` |
| Deployment `e2b.template_build` | The build as Core read it when the selection was saved | Absent, with the whole `e2b` object | Absent, with the whole `e2b` object |
| Deployment `suspension` | `null`; Core does not suspend E2B sandboxes | `null` | `{idle_seconds, retention_seconds}` |
| Deployment `resources.allocations`, `resources.pending` | Core's unreleased E2B sandboxes, and hosted Environments waiting for one | Totals across all nodes | Totals across all nodes |
| Enrollment-token `max_active`, `max_retained` | 409 `sandbox_deployment_conflict`, after the 400 capacity checks; E2B has no nodes | `max_retained` always equals `max_active` | Both limits apply |
| Node list and detail | Empty list; detail returns 404 | Enrolled nodes | Enrolled nodes |
| Node `retained`, `snapshots`, `max_retained` | Not applicable | Docker never suspends: `retained` equals `active`, `snapshots` is 0 and `max_retained` equals `max_active` | Suspended sandboxes are `retained` minus `active` |
| Node `diagnostic` | Not applicable | `docker_unavailable`, `docker_limits_unsupported`, `runtime_image_unavailable`, `capacity_insufficient` or `provider_unavailable` | `kvm_unavailable`, `microsandbox_artifacts_unavailable`, `capacity_insufficient` or `provider_unavailable` |
| Node `host.available_disk_bytes` | Not applicable | Free space on the filesystem of the node state directory, not a container's disk | Free space on the filesystem of the node state directory; sandbox disks have their own quotas |
| Allocation `compute_phase`, `compute_phase_changed_at` | Not applicable: no node allocations | Always `disabled`, counted as running until release; the time is the allocation's creation | Includes `suspended`; its time plus `suspension.retention_seconds` tells roughly when Core reclaims the snapshot |
| Runtime observation `cpu`, `memory` | From E2B metrics: `cpu.utilization_ratio` and `capacity_cores`, memory usage and limit; no cumulative CPU time | From Docker stats: `cpu.usage_seconds_total`, CPU and memory limits, memory usage | From the VM: `cpu.usage_seconds_total`, CPU and memory limits, memory usage |
| Runtime observation `disk` | E2B `diskUsed` and `diskTotal`; `null` when the template does not report them | `null`: no disk quota | `null` for now |
| Runtime observation `lifecycle_state: sleeping` | Never | Never | While suspended |
| Runtime history CPU | Mean of the utilization ratios E2B reported in each bucket | Derived from cumulative CPU time | Derived from cumulative CPU time |

## Failure and upgrade boundaries

Malformed selections return 400; validated configuration diagnostics use
`invalid_sandbox_configuration`. Stale generations, retained resources or an
incompatible deployment return 409 `sandbox_deployment_conflict`. A node
configuration mismatch returns 409 `sandbox_specification_mismatch`; rejected
node credentials return 401 `invalid_node_credential`. Node machine routes check
the credential before any deployment state, so a missing or rejected credential,
including one issued for another installation, gets that 401 even before
initialization or under E2B. Until the deployment is initialized,
`GET /api/v1/sandbox-node/configuration` and `POST /api/v1/sandbox-node/enroll`
answer an otherwise accepted credential with 503 `runtime_node_unavailable`;
`GET /api/v1/sandbox-node/identity` and the node connection answer 401 for any
credential. Unavailable provider preparation returns 503 `execution_unavailable`.
Storage and credential failures remain errors; an empty or failed read is not
evidence of cleanup.

The administrator node list and node detail report an unready provider with one
fixed `diagnostic` code: `docker_unavailable`, `docker_limits_unsupported`,
`runtime_image_unavailable`, `kvm_unavailable`,
`microsandbox_artifacts_unavailable`, `capacity_insufficient` or
`provider_unavailable`. It is absent while the provider is ready. The node
classifies the first failed readiness check and sends only the code; Core stores
any other value as `provider_unavailable` and never stores or returns probe error
text or host paths. Older nodes remain compatible, but an older Core rejects the
new codes, so upgrade Core first. See
[Node readiness diagnostics](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#node-readiness-diagnostics)
for causes, precedence and operator actions.

Core rejects `AGENTS_API_MANAGED_RUNTIMES_FILE`. A node provider file remains an
installed copy of the database selection, not a Core startup configuration source.
For an older file-managed deployment, use the previous release and original
backend to settle work and confirm cleanup before retiring its file configuration.
Preserve identities, provider receipts, history and storage. This version does not
automatically adopt the old database or delete its resources; removing an
environment variable alone does not complete that migration.

A Web-managed Docker or microsandbox selection saved before specifications has
the empty migration default. Core loads it for draining only. GET omits
`specification` and `specification_digest`. Fresh hosted sandboxes are refused
with the same error as during maintenance. Enrollment tokens return 409
`sandbox_deployment_conflict`; node configuration reads and enrollment are refused.
Retained nodes without a recorded digest or generation authenticate while the
deployment is in this state, but those nodes use the removed `/core/v1/sandbox`
node paths. They can reach only a Core that has this drain mode (pull request
#114) and still serves those paths. No such release is published: build one from
main commit `7b66be236a627246c85658722314285e6b39d9b8`, or another commit with
#114 but without `/api/v1/sandbox-node`. Run the maintenance and archive steps there. On this
release, the drain mode matters only for the PUT that records a specification after
draining elsewhere; that PUT retires those nodes. See the
[operator upgrade notes](../../docs/getting-started/operations.md#data-and-upgrades).

Unit tests, database tests and provider inspection are separate from live
execution acceptance. This contract does not assert that every resource profile,
provider deployment or host-reboot recovery path has been qualified.
