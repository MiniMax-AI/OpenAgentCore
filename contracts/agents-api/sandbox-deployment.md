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
| `PUT /core/v1/sandbox/deployment` | Core key | Advance the same-provider target online while retaining existing ownership |
| `POST /core/v1/sandbox/deployment/reset` | Core key | Start or escalate a durable hosted clear |
| `DELETE /core/v1/sandbox/deployment/reset?expected_generation=N` | Core key | Cancel the remaining clear without restoring archived work |
| `POST /core/v1/sandbox/e2b/templates` | Core Web server or operator script with Core key | List up to 200 templates visible to a transient E2B credential |
| `POST /core/v1/sandbox/e2b/templates/{template_id}/builds` | Core Web server or operator script with Core key | List up to 200 ready builds for one selected template |
| `GET /api/v1/sandbox-node/configuration` | Enrollment token or retained node credential | Read the active node installation configuration without consuming enrollment |

Template discovery posts `{ "api_key": "...", "api_url": "https://sandbox.sandbase.ai", "domain": "sandbox.sandbase.ai" }`.
The official E2B endpoint may omit both endpoint fields. The first response is
`{ "templates": [{ "id": "...", "names": ["..."] }] }`;
the second is `{ "builds": [{ "id": "build-uuid", "cpus": 2, "memory_mib": 2048 }] }`.
Both lists may be empty. The Core key authenticates the caller; the E2B key is
used only for this request, is never stored by discovery, and is never returned.
Discovery uses the pinned SDK helper and its `GET /v2/templates` operation,
makes no allocation, and is capped at 200
results. A limit or provider failure returns 503 with a generic message. The
deployment write separately validates the selected exact ready build.

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
that approval with the token and copies it to the registered node. The response
is `{token, expires_at, enrollment_id}`: `enrollment_id` is a public, non-secret
handle of that command and never a credential. Node enrollment
cannot submit capacity overrides. The existing node configuration/identity reads
expose approved limits for that credential; node updates remain administrator
operations. Local host capacity checks may reject a deployment that cannot run
safely, but never raise its limits.

Each node in `GET /core/v1/sandbox/nodes` and its detail reports `enrollment_id`,
the handle of the command that registered it (null for nodes enrolled before Core
recorded it), and `core_url`: the installation public URL when the node enrolled. A node whose `core_url` differs from
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

### Hosted and application-managed placement

The official `openai_hosted` discriminator means hosting by this independent Core
service, using its configured hosted Provider. Keep the public value unchanged; a product-named hosted value is not
a new API type. Both placements resolve the same Environment Templates: hosted
requests use the official field, and self-hosted requests use
`x_agents_core.environment.environment_template_id`. See
[Environment preparation](environments.md) for the shared snapshot contract.
E2B onboarding follows the application-managed `self_hosted` resource workflow:
the application owns sandbox provisioning and cleanup, and our daemon connects
with the returned Environment ID, unchanged `remote_url` and scoped environment
authorization. Reuse the same Runtime and thin provider components. No OpenAI
executor process or additional execution architecture is required. Qualify
principal/tenant ownership, credentials and connection lifecycle using the pinned
client and actual execution; document our transport boundary explicitly.

## Selection request

POST and PUT take the same complete selection and require `expected_generation`
from a preceding GET. Zero is valid for the initial unconfigured deployment;
omitted or null is invalid. A stale generation is checked before reset, provider,
resource and same-selection conditions, including an identical old request body.

| Field | Meaning |
| --- | --- |
| `expected_generation` | Required nonnegative integer from GET; never automatically refresh and replay |
| `provider` | Exactly one of `docker`, `microsandbox`, `e2b` |
| `resources` | Per-sandbox resource limits described below; required for Docker/microsandbox, optional for E2B |
| `runtime` | Required immutable distribution identity for Docker/microsandbox; absent for E2B |
| `e2b` | Required only for E2B: immutable `template` build selector; write-only `api_key` required on POST, optional on same-provider PUT; optional paired `api_url` and `domain` selectors |

The request has no Core address. Core derives the deployment's `core_url` from the
installation public URL (`public_url` in `config.json`, `OAC_PUBLIC_URL` for
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
| `microsandbox_ref` | `oac-runtime@sha256:` followed by 64 lowercase hex characters |
| `runtime_sha256` | SHA-256 of the native microsandbox Runtime binary |
| `firmware_sha256` | SHA-256 of the matched firmware |

Copy these identities from the matched distribution manifest. Image configuration
IDs and OCI manifest digests identify different objects; do not substitute one
for the other. The node installer verifies the saved release against its payload
before registration and retains the exact local image identity it imports.

E2B instead uses `e2b.template` in `template-id:build-uuid` form. The build UUID must
be canonical and nonzero; a mutable template alias alone is insufficient. Omit
`runtime`. The API key is encrypted in PostgreSQL and never returned in a safe
view, bootstrap configuration, command argument or log. Same-team key, build or endpoint
changes apply online while old sandboxes retain their original specification.
By default Core uses `https://api.e2b.app` and `e2b.app`. For a compatible
service, set both `e2b.api_url` (HTTPS API origin, with no path, port, query,
fragment or credentials) and `e2b.domain` (sandbox data-plane DNS suffix).
The API host must equal the data-plane domain or be its subdomain. Core rejects
a sandbox response whose data-plane domain lies outside the selected suffix
before sending daemon credentials or using envd. Existing sandboxes retain
their original endpoint and credential across online changes.

## Safe response

GET and successful mutations return `installation_id`, `provider`, `core_url`
(read-only: the installation public URL, present before configuration), `mode`,
`generation`, `owner_epoch`, `reset`, `rollout`, `suspension` and resource
accounting. A configured deployment also returns `specification` and
`specification_digest`. E2B returns `e2b.template`, `e2b.api_url`,
`e2b.domain`, `e2b.credential_configured` and `e2b.template_build`; the `e2b` object is absent
for Docker and microsandbox.

`e2b.template_build` is `{status, resources: {cpus, memory_mib, root_disk_mib}}`:
the fixed build as Core read it through the pinned SDK when the selection was
saved. GET does not call E2B, so it stays cheap and cannot fail on an E2B outage;
the values describe the immutable build at selection time. Validation admits only
a `ready` build whose CPU count and memory equal the selected `cpus` and
`memory_mib`. `root_disk_mib` is the build's native disk size, which Core does not
enforce separately. Unknown values are null, including every value of a selection
saved before Core recorded them; a verified write records them. An omitted-key
identical PUT is a no-op and does not refresh provider metadata.

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

The typed `SandboxAdminClient` checks the deployment, node list, node detail and
allocation responses against exactly these shapes. An unknown or missing member,
or a wrong type, rejects the whole response with a 502 `invalid_admin_response`
error. A node's `diagnostic` is absent or a code, never empty; the client reads
an unknown code as `provider_unavailable`.

## Initialization, same-provider changes and reset

POST validates a candidate before persistence and creates no compute, Session or
model request. At the current generation an identical selection is a no-op; an
obsolete generation returns 409 `sandbox_generation_stale`, even for the same body.
Missing prerequisites or failed preparation leave the committed provider intact.
A different backend, or an old node selection without a specification, requires
reset first. POST also initializes after a completed reset, using its new generation.

PUT accepts the same provider and no active reset. Send the observed generation
once; never replay an uncertain mutation automatically. E2B changes apply online:
new allocations use the newly committed specification and existing allocations
retain their immutable deployment generation. No node, token or owner epoch is
retired by a same-provider update. Docker/microsandbox advance only the target;
each node prepares it independently while continuing to serve its qualified old
pin. No execution drain or reenrollment accompanies a target change.

For E2B PUT, omit `e2b.api_key` to preserve the current key. Omitted-key identical
selection is a no-op. Explicit nonempty key submission, including the same key,
always verifies and advances generation. Null or empty keys are invalid. A key-only
change uses the same full DTO: provider, existing template, optional resources and
expected_generation, plus the new api_key. It has no separate route or implicit reset.

Initial setup requires the selected template to appear in the credential's team-owned
template listing; public readability alone is insufficient. Before an online change,
Core verifies that the committed key owns the current template, then requires the
candidate key to own that exact template as a shared ownership anchor. It also reads
the candidate and every retained build at its original endpoint with the candidate key and confirms each
settled live receipt in the installation-labelled sandbox listing.

A legacy public-template selection without this ownership anchor, or a committed key
that no longer authenticates, requires `409 sandbox_reset_required`; Core cannot
establish a safe online replacement from that state. Keep the old key valid until the
successful response. A candidate outside the verified team gives `409 e2b_team_mismatch`;
explicitly reset before initializing another team. Candidate-key 401/403 gives
`400 e2b_api_key_invalid`; an invalid candidate build gives
`400 e2b_template_build_invalid`. Missing or unsettled receipts and unconfirmed reads
fail closed with `503 e2b_request_unconfirmed`. No provider text or credential is
returned. The write and `change` or `replace_credential` audit share one transaction.

A credential replacement briefly fences provider calls, waits for actual helper
process completion even after caller cancellation, and repeats verification before
commit. Helper exit is not evidence that remote Create settled. The fence and reads
are bounded; failure preserves the old key and lifecycles. After the successful
response, all retained-generation management uses the committed key. Only then
revoke the previous key in E2B. Template/resource changes do not drain lifecycles.

### Generation ownership and rollout

`runtime_deployment` owns the current specification. Superseded rows contain only
immutable specification/build/endpoint metadata, never another E2B credential. An E2B
allocation binds its generation at reservation. Node placements bind at Session
admission and allocations copy that generation, even after repeated updates.
Inspection, renewal, command execution and cleanup route the allocation's original
specification and endpoint with the current credential; a missing generation never falls back
to the current specification. Released historical generation identifiers remain.

Retain a generation while it is current, referenced by an unreleased allocation or
placement, or pinned by a nonremoved node. The durable node serving pin survives
offline state and zero resources; it is separate from current connection readiness.
Collection shares the deployment lock with updates and admission and deletes at
most 32 eligible generation rows per pass. Reset retires pins and clears superseded
rows only after confirmed resource release. Downgrade refuses ownership or serving
pins that still need generation routing.

Every deployment response includes:

```json
"rollout": {"state":"settled", "previous_generation_sandboxes":3,
            "nodes":null}
```

The previous count uses the same snapshot as resource totals: old-generation
unreleased allocations plus old-generation pending placements without allocations,
never counting one resource twice. E2B and unconfigured deployments return null
nodes. A node deployment returns counts `{ready,preparing,failed,update_required,unknown}`.
Every nonremoved node belongs to exactly one bucket. Offline current connections
are `unknown` first; an online v1 node enrolled at an older target is
`update_required`. Otherwise the exact target observation on the current
connection and owner epoch supplies `ready`, `preparing` or `failed`; missing
observations are `unknown`. The existing 45-second heartbeat predicate defines
online. Pins alone never imply readiness. Target rollout is independent of serving
readiness: unknown/preparing/failed/update_required does not erase an independently
confirmed old serving provider. `provider_ready` requires online presence and an
exact serving-generation observation on that connection and epoch. A v1 heartbeat
qualifies only its enrolled provider.

Each node adds `rollout: {state, ready_generation, diagnostic?}`. ready_generation is
the nullable durable serving pin. Diagnostic is a fixed node reason; unknown values
project as `provider_unavailable`. Allocation items add `deployment_generation`.
`rollout.state` is the authoritative high-frequency polling signal: poll every five
seconds only while it is `preparing` or reset is nonnull. Old Sessions, failed,
update-required and offline nodes alone do not keep polling active. New admission
filters online, exact serving-generation readiness, address and shared capacity
before choosing the newest qualifying pin. A full newest node does not hide a free
older node. No candidate creates no provisional Session or placement. An online
node actually preparing with available capacity returns 503 `sandbox_nodes_preparing`;
a full or offline fleet returns `runtime_node_unavailable`.

To change backend, explicitly start reset:

```json
{"expected_generation": 7, "clear": "auto", "deadline_seconds": 3600}
```

`clear` is required: `auto` or `force`. Auto's `deadline_seconds` defaults to 3600
and accepts 300–86400; force must omit it. Core persists an absolute deadline and
requester audit provenance before closing fresh hosted admission. The existing
execution owner advances the clear after the request ends and across restarts.
Auto archives idle hosted Sessions, including queued or pending work and suspended
sandboxes, but waits for root/subagent Turns in progress or waiting and pending
file writes. It rechecks this condition under the Session lock. At the persisted
deadline it durably escalates to force; force uses the ordinary archive cancellation
and cleanup path for all eligible hosted Sessions. Self-hosted Sessions are excluded.

Repeated start at the current generation/mode keeps the original deadline. Auto
can escalate to force, but force cannot downgrade to auto. DELETE with the current
`expected_generation` cancels remaining reset work and reopens admission; it does
not undo archives, revive expired Environments or cancel cleanup already requested.
DELETE with no active reset is idempotent. Stale requests still return 409.

Every deployment response includes `reset: null` when inactive, or:

```json
{"clear":"auto","requested_at":"2026-09-27T12:00:00Z",
 "deadline_at":"2026-09-27T13:00:00Z","forced_at":null,
 "remaining":{"busy":2,"idle":1,"cleanup":3,"on_offline_nodes":2,
              "offline_nodes":[{"node_id":"node-uuid","name":"worker","resources":2}]}}
```

Timestamps are explicit nullable fields where applicable. A single database snapshot
partitions every unreleased allocation and every pending hosted Environment with
no allocation into cleanup first, then busy or idle.
`busy + idle + cleanup == resources.allocations + resources.pending`. Deleted or
expired Sessions with unreleased receipts still count as cleanup. `offline_nodes`
is the untruncated, ID-sorted subset attributed to receipt or active-placement node
identity; its resource sum equals `on_offline_nodes`. Presence uses the current
owner epoch, connection and a heartbeat within 45 seconds, not provider readiness.
Direct E2B resources have no node and do not enter that subset. Offline resources
remain blockers until actual cleanup confirms release.

Only after both held counts reach zero does the owner drain and atomically clear
provider/mode/specification, E2B credential/template/build metadata and provider
policy, retire nodes and unused enrollment tokens, increment generation and owner
epoch, and record `reset_complete`. Installation identity and history survive.
The manager publishes an unconfigured state immediately; its cache keeps the new
generation even with no provider, preventing delayed old loads from reviving it.
Configure again with POST using the returned generation; no restart is necessary.

Fresh hosted admission returns 503 `sandbox_reset_in_progress` and leaves no
provisional Session rows. Existing live input, receipt retries, restoration and
cleanup continue. Management writes and new enrollment/configuration return 409
`sandbox_reset_in_progress`; retained matching nodes can recover for cleanup.
Explicit per-Session archive requires the current generation and remains available
without reset. It preserves history and persisted Files/Artifacts but discards
unpersisted workspace and prevents that Session from resuming. Poll its archive
GET for actual release. Reset never manufactures a release receipt.

A force archive fences credentials and new work immediately. If its original
hosted delivery is still connected, Core preserves only that delivery's native
cancellation/terminal receipt path until terminal commit or a fixed 20-second
bound from the original cancellation request. Done does not end this bound while
a cancellation acknowledgment or terminal commit is pending. This internal drain
never authorizes reconnect, workspace/MCP access or renewed execution. Explicit
credential revocation ends the exception; repeats do not extend it. Missing or
failed receipts retain honest failure outcomes, and disconnected, expired or
restarted owners fall back to ordinary provider cleanup. There is no new public
state, request field or model/tool timeout.

One mutation gate serializes setup, PUT, reset, cancel and finalization. Archive
locks Session before deployment; finalization never reverses that order or waits
for itself inside counted manager work. Candidates bind to the reset request time,
so cancel followed by a new reset at the same generation cannot reuse old work.
Never automatically replay a rejected or uncertain write: read current state and
make a new explicit decision. Do not revoke old E2B credentials before cleanup.

## Node configuration and enrollment

The retired `X-Parsar-Node-ID` header is rejected even when empty or accompanied
by its replacement: `400 invalid_request`, with the message
`X-Parsar-Node-ID was renamed to X-OAC-Node-ID; use the node command from this Core's Web`. Header values are never included in this diagnostic.

For a new node, send `Authorization: Bearer <enrollment-token>` to the configuration
GET without `X-OAC-Node-ID`. The token must be valid, unexpired, unconsumed and
belong to this installation. This read does not consume it. An active reset prevents
new enrollment configuration reads.

An already registered node sends its durable node credential as Bearer and its
UUID in `X-OAC-Node-ID`. Its original enrollment identity and installation remain
valid across same-provider target changes. Omitted `generation` reads the current
target; `?generation=N` reads only that node's exact current, serving-pinned or
unreleased-allocation/placement generation. Unknown or unkept history is refused.
This read remains available during reset for owned recovery. The old
enrollment token cannot replace a registered node's credential.

The response contains `installation_id`, `provider`, `core_url` (the installation
public URL), `generation`, `specification` and `specification_digest`. It contains no administrator, Project
or E2B credential. It is available only for node-backed providers.

The installer reads this configuration before preparing local assets. Its provider
file records `generation` and `specification`, alongside the host's socket, paths
and network policy. Enrollment at `POST /api/v1/sandbox-node/enroll` includes
`deployment_generation`, `specification_digest` and `core_url`, the Core origin the
node stores. A specification mismatch rejects with 409
`sandbox_specification_mismatch` and a `core_url` other than the installation public
URL with 409 `sandbox_node_address_mismatch`, both before token consumption. A
missing `core_url` gets 400 `invalid_request_error` with `param: "core_url"`.
The original enrollment identity remains immutable. New generation preparation
uses separate exact configurations, never rewrites that identity or silently
substitutes a local default. See [node generation protocol](node-generation-protocol.md).

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

## Failure and version boundaries

Malformed selections return 400; validated configuration diagnostics use
`invalid_sandbox_configuration`. Stale generation returns 409 `sandbox_generation_stale` with safe `current_generation`;
A different backend returns 409 `sandbox_reset_required` with provider names.
Unconfigured mutations return 409 `sandbox_not_configured`. Other incompatible
deployments return 409 `sandbox_deployment_conflict`. A node
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
`runtime_download_failed`, `runtime_image_unavailable`, `kvm_unavailable`,
`microsandbox_artifacts_unavailable`, `capacity_insufficient` or
`provider_unavailable`. It is absent while the provider is ready. The node
classifies the first failed readiness check and sends only the code; Core stores
any other value as `provider_unavailable` and never stores or returns probe error
text or host paths. Core and nodes must use the same distribution; unknown-code
handling does not establish cross-version compatibility. See
[Node readiness diagnostics](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#node-readiness-diagnostics)
for causes, precedence and operator actions.

Core rejects `AGENTS_API_MANAGED_RUNTIMES_FILE`. A node provider file remains an
installed copy of the database selection, not a Core startup configuration source.
Historical file-managed deployments and selections without a complete specification
are unsupported. Preserve their database, identities, provider receipts, Runtime
resources and history; install the current release separately. Do not clear state,
run a historical service to convert it, or treat removal of an environment variable
as a transfer of ownership. See the
[installation version policy](../../docs/getting-started/operations.md#installation-version-policy).

Landed migrations and their refusal conditions remain historical schema evidence.
They do not establish an operator upgrade, downgrade or conversion procedure.
Fresh database initialization uses the ordinary migration runner.

Unit tests, database tests and provider inspection are separate from live
execution acceptance. This contract does not assert that every resource profile,
provider deployment or host-reboot recovery path has been qualified.


### Current Runtime identity

The immutable microsandbox reference is `oac-runtime@sha256:<64 lowercase hex>`.
Build new E2B templates with the matching current Runtime release. Historical
Runtime names and installations have no supported upgrade, downgrade or conversion
path; preserve their data and resources and install separately. Current Runtime
generation rollout, coexistence and ownership-scoped garbage collection remain
supported and are independent of installed program version changes. See
[generation ownership and rollout](#generation-ownership-and-rollout).

`runtime_download_failed` means the exact Runtime artifacts could not be transferred
or verified. It is distinct from provider probe and image availability failures;
the diagnostic never contains artifact URLs, credentials or transport output.


## Canonical node specification

`sandbox/deployment_contract.go` owns resource bounds, provider requirements,
release patterns and canonical field order. `sandbox/deployment.go` applies those
rules in Core. The installer consumes the generated declaration in
`deploy/install/node_spec.py`; do not maintain a second set of limits or patterns.
Regenerate it from the repository root with
`go run ./services/agents-api/cmd/specification-contract -write`.
The sandbox Go tests, included in `make check`, reject a stale projection.

The specification digest is SHA-256 of UTF-8 compact JSON, with `provider` first,
then `resources`, then `runtime` when required by the provider. Resource and
Runtime fields follow the contract declaration order. Zero optional disk fields
are omitted; required fields remain present. Release identities are lowercase
ASCII; the digest never hashes the incoming JSON field order or whitespace.
`internal/sandbox/testdata/deployment-contract.json` (under `services/agents-api/`)
contains shared acceptance cases, exact canonical bytes and digests consumed by
both Go and Python tests. Cross-language validation is required; distinct peers
must not invent distinct rules.
