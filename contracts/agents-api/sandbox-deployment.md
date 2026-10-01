---
title: "Sandbox deployment"
---

The sandbox deployment selects the Sandbox Provider, the per-sandbox resources and the immutable Runtime release for Core-managed `openai_hosted` execution. PostgreSQL holds one active selection per installation; Web and the Core API write the same configuration. A node's files hold an installed copy of it plus host-specific paths and cannot override its resources or Runtime. The selection is independent of the Harness, and a deployment can stay unconfigured, with no nodes and no hosted admission.

This contract owns the Core API routes below and their semantics. The [nodes guide](../../docs/getting-started/nodes.md) owns the operator workflow, the [machine connection API](./machine-api.md#node-routes) the routes nodes call, and the [sandbox node protocol](./node-generation-protocol.md) the node connection.

## Routes

Every route requires the Core key. [Web's console server](../../docs/web/console-server.md) adds it on the server side for signed-in requests.

| Route | Effect |
| --- | --- |
| `GET /core/v1/sandbox/deployment` | Read the safe active configuration, rollout, reset and resource counts |
| `POST /core/v1/sandbox/deployment` | Select the initial provider, resources and Runtime |
| `PUT /core/v1/sandbox/deployment` | Change the same provider's target online |
| `POST /core/v1/sandbox/deployment/reset` | Start or escalate a durable clear of hosted resources |
| `DELETE /core/v1/sandbox/deployment/reset?expected_generation=N` | Cancel the remaining clear |
| `POST /core/v1/sandbox/providers/{provider}/discovery` | Query a provider's configuration catalog with a transient credential |
| `POST /core/v1/sandbox/enrollment-tokens` | Issue a one-use node enrollment token with approved capacity |
| `GET /core/v1/sandbox/nodes` | List registered nodes |
| `GET /core/v1/sandbox/nodes/{node_id}` | Read one node with its [host observations and history](./runtime-observability-api.md#node-host-observations-and-history) |
| `PATCH /core/v1/sandbox/nodes/{node_id}` | Change a node's name and capacity |
| `DELETE /core/v1/sandbox/nodes/{node_id}` | Remove a node |
| `GET /core/v1/sandbox/nodes/{node_id}/allocations` | List a node's unreleased allocations |
| `GET /core/v1/sandbox/runtime-observations` | Current Runtime observations; see the [Runtime telemetry API](./runtime-observability-api.md) |

## Selection request

POST and PUT take the same complete selection and require `expected_generation` from a preceding GET. Zero is valid for the initial unconfigured deployment; omitted or null is invalid. A stale generation is checked before reset, provider, resource and same-selection conditions, even for a body identical to an earlier request.

| Field | Meaning |
| --- | --- |
| `expected_generation` | Required nonnegative integer from GET; never refresh and replay it automatically |
| `provider` | Exactly one of `docker`, `microsandbox`, `e2b` |
| `resources` | Per-sandbox limits, below; required for Docker and microsandbox, optional for E2B |
| `runtime` | The immutable [Runtime release](#runtime-release); required for Docker and microsandbox, absent for E2B |
| `configuration` | The provider's public selectors. E2B: the immutable `template` build and the optional paired `api_url` and `domain`. Docker and microsandbox accept only `{}` or omission |
| `credential` | The provider's write-only credential. E2B: `{api_key}`, required at first setup and omitted on PUT to keep the current key; a null or empty key is invalid. Docker and microsandbox reject it |

The request has no Core address. Core derives the deployment's `core_url` from the installation public URL (`public_url` in `config.json`, `OAC_PUBLIC_URL` for Core): the origin nodes and sandbox guests use to reach Core. A request that contains `core_url` is rejected with 400 `invalid_request` like any other unknown member. E2B guests reach Core from E2B's cloud, so an E2B selection is rejected with 409 `sandbox_configuration_error` while the public URL is loopback. Docker and microsandbox selections accept a loopback public URL, which serves only local development because a guest's loopback address does not reach its host. Changing the public URL is an installation change: nodes enrolled with the old address receive no new sandboxes and must be removed and added again.

### Resources

| Field | Accepted value |
| --- | --- |
| `cpus` | Integer, 1 through 255 |
| `memory_mib` | Integer, 512 through 1048576 MiB |
| `root_disk_mib` | microsandbox: at least 1024 MiB; Docker and E2B: omitted or zero |
| `environment_disk_mib` | microsandbox: at least 1024 MiB; Docker and E2B: omitted or zero |

These limits describe each sandbox. A node's `max_active` and `max_retained` are separate reservation limits, and host measurements never permit exceeding either. Native providers may reject values that pass these bounds.

Docker applies the CPU and memory limits and checks the running container's limits and exact image; it has no hard root or workspace disk quota. E2B CPU and memory must equal the exact ready template build, which Core validates through the pinned SDK before saving; disk capacity stays part of the template. Neither provider accepts a disk quota it cannot enforce. An E2B selection may omit `resources`: Core then stores the build's CPU count and memory as `cpus` and `memory_mib`, returned in `specification.resources` without disk fields. On restart Core loads the committed E2B selection without validating the template build again, so an E2B outage never blocks inspection or cleanup; new selections still require validation.

microsandbox configures the CPUs, memory, a managed root disk and a separate owned disk at `/environment`. The [microsandbox helper](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/microsandbox-provider/README.md) describes how restore handles these limits.

### Runtime release

Docker and microsandbox use every field of one verified distribution:

| Field | Identity |
| --- | --- |
| `source_commit` | Lowercase 40-character commit SHA |
| `image_id` | Docker image configuration ID: `sha256:` and 64 lowercase hex characters |
| `image_manifest_digest` | OCI image manifest digest, in the same form |
| `microsandbox_ref` | `oac-runtime@sha256:` and 64 lowercase hex characters |
| `runtime_sha256` | SHA-256 of the native microsandbox runtime binary |
| `firmware_sha256` | SHA-256 of the matching firmware |

Copy these identities from the matching distribution manifest. An image configuration ID and an OCI manifest digest identify different objects and never substitute for each other. The node installer verifies the saved release against its payload before registration and keeps the exact local image identity it imports.

### E2B configuration

E2B uses `configuration.template` in `template-id:build-uuid` form; the build UUID must be canonical and nonzero, and a mutable template alias alone is refused. The API key is encrypted in PostgreSQL and never appears in a response, bootstrap configuration, command argument or log. By default Core uses `https://api.e2b.app` and `e2b.app`. For a compatible service, set both `configuration.api_url` (an HTTPS origin without path, port, query, fragment or credentials) and `configuration.domain` (the sandbox data-plane DNS suffix); the API host must equal the domain or be a subdomain of it. Core rejects a sandbox whose data-plane domain lies outside the selected suffix before sending daemon credentials or using envd.

### Configuration discovery

`POST /core/v1/sandbox/providers/{provider}/discovery` takes exactly `configuration`, `credential` and `query` objects, at most 64 KiB, and runs for at most 30 seconds. Unknown members and null objects are rejected. The credential is used only for that request and is never stored or returned; discovery saves nothing, changes no deployment, allocates nothing and never proves that a selection will be admitted. Docker and microsandbox reject it with 400 `sandbox_operation_unsupported`.

For E2B, post `{"configuration": {"api_url": "…", "domain": "…"}, "credential": {"api_key": "…"}, "query": {}}` to list templates, and add `"query": {"template": "template-id"}` to list that template's ready builds; the official endpoint may omit both endpoint fields. The results are `{"templates": [{"id": "…", "names": ["…"]}]}` and `{"builds": [{"id": "build-uuid", "cpus": 2, "memory_mib": 2048}]}`, either possibly empty. The pinned SDK helper reads `GET /v2/templates` and returns at most 200 results; a limit or provider failure returns 503 with a generic message. The deployment write validates the selected build separately.

## Safe response

GET and successful writes return `installation_id`, `provider`, `core_url` (read-only: the installation public URL, present even before configuration), `mode`, `generation`, `owner_epoch`, `reset`, `rollout`, `suspension`, `resources` and `credential_configured`. A configured deployment also returns `specification`, `specification_digest`, `configuration` and `metadata`: the adapter's public projection of its selectors and of the observations it recorded, never raw stored values or secrets. E2B returns `configuration.template`, `configuration.api_url`, `configuration.domain` and, once recorded, `metadata.template_build`. Docker and microsandbox return empty `configuration` and `metadata` objects and `credential_configured: false`; an unconfigured deployment has neither object.

- `metadata.template_build` is `{status, resources: {cpus, memory_mib, root_disk_mib}}`: the build as Core read it through the pinned SDK when the selection was saved. GET never calls E2B, so it stays cheap during an E2B outage. Validation admits only a `ready` build whose CPU count and memory equal the selected values; `root_disk_mib` is the build's native disk size, which Core does not enforce. Unknown values are null, `metadata: {}` means no observation was recorded, and an identical PUT without a credential does not refresh it.
- `suspension` is `{idle_seconds, retention_seconds}` for microsandbox, the only provider Core suspends (currently 300 and 86400); Docker, E2B and unconfigured deployments return null.
- Request `resources` and response `specification.resources` are per-sandbox limits. Response `resources.allocations` and `resources.pending` count unreleased allocations and pending hosted Environments without an allocation.
- An unconfigured deployment has an empty provider and no specification. Docker and microsandbox use `mode: nodes`; E2B uses `mode: direct`, without a synthetic node.
- `generation` identifies the saved selection. `owner_epoch` fences the execution owner and node connections; it does not replace `expected_generation`.
- `specification_digest` is the server's identity of the provider, limits and Runtime release; enrollment echoes it unchanged.

The typed `SandboxAdminClient` in `packages/agents-client` checks the deployment, node list, node detail and allocation responses against exactly these shapes. An unknown or missing member, or a wrong type, rejects the whole response with a 502 `invalid_admin_response` error. A node's `diagnostic` is absent or a code, never empty, and the client reads an unknown code as `provider_unavailable`.

## Initial setup and same-provider changes

POST validates a candidate before persisting it and creates no compute, Session or model request. At the current generation an identical selection is a no-op; an old generation returns 409 `sandbox_generation_stale`, even for the same body. Missing prerequisites or a failed preparation leave the committed provider in place. A different backend requires a reset first. POST also initializes after a completed reset, using the reset's new generation.

PUT accepts the same provider while no reset is active. Send the observed generation once and never replay an uncertain write automatically. New allocations use the newly committed specification, and existing allocations keep their immutable deployment generation. A same-provider change retires no node, token or owner epoch and drains no execution: Docker and microsandbox nodes prepare the new target independently while serving their old pin, and E2B changes apply at once.

### E2B key replacement

Omit `credential` on PUT to keep the current key; an identical selection without it is a no-op. Submitting a key, even the same one, always verifies it and advances the generation; a null or empty key is invalid. A key-only change uses the same full body (provider, existing configuration, optional resources, `expected_generation` and the new `credential`), with no separate route or implicit reset.

Initial setup requires the selected template to appear in the key's team-owned template listing; public readability is not enough. Before an online change, Core verifies that the committed key owns the current template, then requires the candidate key to own that exact template, which anchors team ownership. It also reads the candidate build and every retained build at its original endpoint with the candidate key, and confirms each settled live receipt in the installation-labelled sandbox listing.

| Result | Meaning |
| --- | --- |
| `409 sandbox_reset_required` | The current selection has no team ownership anchor, or the committed key no longer authenticates |
| `409 sandbox_credential_ownership` | The candidate key belongs to another team; reset before initializing another team |
| `400 sandbox_credential_invalid` | The candidate key gets 401 or 403 |
| `400 sandbox_configuration_invalid` | The candidate build is invalid or does not match the resources |
| `503 sandbox_verification_unconfirmed` | A receipt is missing or unsettled, or a read is unconfirmed |

No provider text or credential is returned. The write and its `change` or `replace_credential` audit entry share one transaction. A replacement briefly fences provider calls, waits for helper processes to actually exit even after the caller cancelled, and verifies again before committing; a helper's exit does not prove that a remote Create settled. The fence and reads are bounded, and a failure keeps the old key and lifecycles. After the successful response, all retained-generation management uses the committed key; only then revoke the old key in E2B, never before cleanup. Template and resource changes do not drain lifecycles.

## Generation ownership and rollout

`runtime_deployment` holds the current specification. Superseded rows keep only immutable specification, build and endpoint metadata, never another E2B key. An E2B allocation binds its generation at reservation; a node placement binds at Session admission, and its allocation copies that generation, even across later updates. Inspection, renewal, commands and cleanup use the allocation's original specification and endpoint with the current key; a missing generation never falls back to the current specification. Released generation identifiers stay reserved.

A generation is retained while it is current, referenced by an unreleased allocation or placement, or pinned by a node that is not removed. A node's durable serving pin survives offline periods and zero resources and is separate from its current readiness. Collection shares the deployment lock with updates and admission and deletes at most 32 eligible generation rows per pass. Reset retires pins and clears superseded rows only after confirmed release.

Every deployment response includes `rollout`:

```json
"rollout": {"state": "settled", "previous_generation_sandboxes": 3, "nodes": null}
```

`previous_generation_sandboxes` counts, from the same snapshot as the resource totals, unreleased allocations and allocation-less pending placements of older generations, never one resource twice. E2B and unconfigured deployments return null `nodes`. A node deployment returns `nodes` as counts `{ready, preparing, failed, update_required, unknown}`, and every node that is not removed falls in exactly one bucket:

- a node that is offline on its current connection is `unknown`;
- an online node without generation management that enrolled at an older target is `update_required`;
- otherwise the exact target observation on the current connection and owner epoch gives `ready`, `preparing` or `failed`, and a missing observation gives `unknown`.

A node is online while it is connected under the current owner epoch with a heartbeat in the last 45 seconds. Target rollout is independent of serving readiness: `unknown`, `preparing`, `failed` or `update_required` does not remove an independently confirmed older serving generation. `provider_ready` requires online presence and an exact observation of the serving generation on that connection and epoch; a node without generation management qualifies only its enrolled generation. Pins alone never imply readiness.

Each node adds `rollout: {state, ready_generation, diagnostic?}`, where `ready_generation` is the nullable durable serving pin and `diagnostic` a fixed code for the target generation; allocation items add `deployment_generation`. Poll every five seconds only while `rollout.state` is `preparing` or `reset` is not null; old Sessions and failed, update-required or offline nodes alone do not keep polling active.

New admission filters nodes by online presence, exact serving-generation readiness, address and shared capacity before it prefers the newest qualifying pin, so a full newest node never hides a free older one. Without a candidate, admission creates no provisional Session or placement: an online node that is actually preparing with free capacity gives 503 `sandbox_nodes_preparing`, and a full or offline fleet gives `runtime_node_unavailable`.

## Reset

To change backend, start a reset:

```json
{"expected_generation": 7, "clear": "auto", "deadline_seconds": 3600}
```

`clear` is required: `auto` or `force`. `deadline_seconds` applies to `auto`, defaults to 3600 and accepts 300 to 86400; `force` must omit it. Core persists an absolute deadline and the requester's audit provenance before it closes fresh hosted admission, and the execution owner advances the clear after the request ends and across restarts. `auto` archives idle hosted Sessions, including queued or pending work and suspended sandboxes, and waits for root and Subagent Turns that are in progress or waiting and for pending file writes, rechecking under the Session lock. At the persisted deadline it escalates to `force` durably. `force` uses the ordinary archive cancellation and cleanup path for every eligible hosted Session. Self-hosted Sessions are never touched.

Starting again at the same generation and mode keeps the original deadline. `auto` can escalate to `force`, never the reverse. DELETE with the current `expected_generation` cancels the remaining work and reopens admission; it does not undo archives, revive expired Environments or cancel cleanup already requested. DELETE without an active reset is a no-op, and a stale request returns 409.

`reset` is null when inactive, otherwise:

```json
{"clear": "auto", "requested_at": "2026-09-27T12:00:00Z",
 "deadline_at": "2026-09-27T13:00:00Z", "forced_at": null,
 "remaining": {"busy": 2, "idle": 1, "cleanup": 3, "on_offline_nodes": 2,
               "offline_nodes": [{"node_id": "node-uuid", "name": "worker", "resources": 2}]}}
```

One database snapshot partitions every unreleased allocation and every pending hosted Environment without an allocation into `cleanup` first, then `busy` or `idle`, so `busy + idle + cleanup == resources.allocations + resources.pending`. Deleted or expired Sessions with unreleased receipts count as cleanup. `offline_nodes` is the complete, ID-sorted list of offline nodes that hold resources by allocation or active placement, and its sum equals `on_offline_nodes`; presence uses the current owner epoch, connection and a heartbeat within 45 seconds, not provider readiness. Direct E2B resources have no node. Offline resources stay blockers until cleanup confirms their release.

When both held counts reach zero, the owner drains and atomically clears the provider, mode, specification, provider configuration, credential and metadata and provider policy, retires nodes and unused enrollment tokens, increments the generation and owner epoch, and records `reset_complete`. The installation identity and history remain. Core immediately publishes the unconfigured state and keeps the new generation even with no provider, so a delayed load cannot revive the old one. Configure again with POST and the returned generation; no restart is needed.

During a reset, fresh hosted admission returns 503 `sandbox_reset_in_progress` and leaves no provisional Session rows; live input, receipt retries, restoration and cleanup continue. Management writes and new enrollment return 409 `sandbox_reset_in_progress`, while registered nodes can still read their configuration to recover for cleanup. Per-Session [archive](./admin-api.md#session-archive) needs only the current generation and works with or without a reset. It keeps history and persisted Files and Artifacts, discards the unpersisted workspace and prevents the Session from resuming; poll the archive GET for the actual release. A reset never fabricates a release receipt.

A force archive fences credentials and new work at once. When the hosted delivery of the Turn is still connected, Core keeps only that delivery's native cancellation and terminal receipt path open until the terminal commit, for at most 20 seconds from the original cancellation request; `done` does not end the bound while a cancellation acknowledgement or terminal commit is pending. This drain never authorizes reconnection, workspace or MCP access or further execution, and an explicit credential revocation ends it. Missing or failed receipts keep honest failure outcomes, and a disconnected, expired or restarted owner falls back to ordinary provider cleanup.

One mutation gate serializes setup, PUT, reset, cancellation and finalization. Archive locks the Session before the deployment, and finalization never reverses that order. Candidates bind to the reset request time, so a cancel followed by a new reset at the same generation cannot reuse old work. Never replay a rejected or uncertain write: read the current state and decide again.

## Nodes and allocations

Node capacity is approved by the administrator, separately from the deployment specification. `POST /core/v1/sandbox/enrollment-tokens` accepts optional `max_active` and `max_retained`, default 2 and 8, and returns `{token, expires_at, enrollment_id}`; `enrollment_id` is a public handle of that command, never a credential. microsandbox uses both limits. Docker never suspends, so Core replaces its `max_retained` with `max_active`, here and in PATCH. E2B has no nodes and answers 409 `sandbox_deployment_conflict`. Core stores the approval with the token and copies it to the node it registers; a node cannot submit capacity, and its local checks may refuse a deployment it cannot run but never raise the limits. [Node capacity](../../docs/configuration.md#node-capacity) explains the limits for operators.

`GET /core/v1/sandbox/nodes` returns `{data: [...]}` with, for each node: `id`, `name`, `provider`, `online`, `last_seen_at`, `created_at`, `max_active`, `max_retained`, the counts `active`, `reserved`, `running`, `retained`, `snapshots` and `cleanup_pending`, `provider_ready`, `diagnostic`, the host measurements `cpu_count`, `available_memory_bytes` and `available_disk_bytes`, `rollout`, `enrollment_id` and `core_url`. `enrollment_id` is the handle of the command that registered the node, or null when Core has none. `core_url` is the installation public URL at enrollment; a node whose `core_url` differs from the current public URL receives no new placements. Work already placed on it finishes there, including a placed Environment that has no allocation yet, and its retained sandboxes can still resume while the old address reaches Core. Remove it and add it again.

A node without generation management reports its provider's readiness itself: `provider_ready`, and when it is unready one fixed `diagnostic` code. A node added with Web's command manages generations, so its readiness follows its serving generation and its fixed code for the target generation appears in `rollout.diagnostic`. The node classifies the first failed readiness check and sends only the code; Core stores any other value as `provider_unavailable` and never stores or returns probe text or host paths. The codes are `docker_unavailable`, `docker_limits_unsupported`, `runtime_download_failed`, `runtime_image_unavailable`, `kvm_unavailable`, `microsandbox_artifacts_unavailable`, `capacity_insufficient` and `provider_unavailable`. `runtime_download_failed` means the exact Runtime artifacts could not be transferred or verified; it never contains artifact URLs, credentials or transport output. [Readiness codes](../../docs/getting-started/nodes.md#readiness-codes) gives causes and operator actions. Core and nodes must come from the same distribution.

`PATCH /core/v1/sandbox/nodes/{node_id}` takes `{name, max_active, max_retained}`; lowering a limit stops no running sandbox. `DELETE /core/v1/sandbox/nodes/{node_id}` refuses with 409 `runtime_node_in_use` while the node holds allocations, snapshots, reservations or pending cleanup, including while it is offline. Removal deletes no compute and retires the node's identity; the host can come back only as a new node. There is no node drain.

`GET /core/v1/sandbox/nodes/{node_id}/allocations` lists the node's unreleased allocations. Each item's `compute_phase_changed_at` is when the allocation entered its current `compute_phase`, or null when unknown. For a suspended microsandbox allocation, that time plus `suspension.retention_seconds` tells roughly when Core reclaims it.

## What each field means per sandbox provider

Some fields keep one name across providers but differ in meaning, or do not apply. Deployment fields come from `GET /core/v1/sandbox/deployment`, node and allocation fields from the node routes, and Runtime fields from the [Runtime telemetry API](./runtime-observability-api.md), where `disk` appears only in the observation list and history is described under [Session Runtime history](./runtime-observability-api.md#session-runtime-history).

| Field | E2B | Docker | microsandbox |
| --- | --- | --- | --- |
| Deployment `specification.resources` | `cpus` and `memory_mib`, equal to the ready template build's and taken from it when omitted; no disk fields | `cpus` and `memory_mib`; no disk quota | `cpus`, `memory_mib`, `root_disk_mib` and `environment_disk_mib` |
| Deployment `specification.runtime` | Absent; `configuration.template` selects the build | The full [release](#runtime-release); nodes match `image_id` or `image_manifest_digest` | The full [release](#runtime-release); nodes match `microsandbox_ref`, `runtime_sha256` and `firmware_sha256` |
| Deployment `metadata.template_build` | The build as Core read it when the selection was saved | Absent: `metadata` is empty | Absent: `metadata` is empty |
| Deployment `suspension` | `null`; Core does not suspend E2B sandboxes | `null` | `{idle_seconds, retention_seconds}` |
| Deployment `resources.allocations`, `resources.pending` | Core's unreleased E2B sandboxes, and hosted Environments waiting for one | Totals across all nodes | Totals across all nodes |
| Enrollment-token `max_active`, `max_retained` | 409 `sandbox_deployment_conflict`, after the 400 capacity checks; E2B has no nodes | `max_retained` always equals `max_active` | Both limits apply |
| Node list and detail | Empty list; detail returns 404 | Enrolled nodes | Enrolled nodes |
| Node `retained`, `snapshots`, `max_retained` | Not applicable | Docker never suspends: `retained` equals `active`, `snapshots` is 0 and `max_retained` equals `max_active` | Suspended sandboxes are `retained` minus `active` |
| Node `diagnostic` codes | Not applicable | `docker_unavailable`, `docker_limits_unsupported`, `runtime_download_failed`, `runtime_image_unavailable`, `capacity_insufficient` or `provider_unavailable` | `kvm_unavailable`, `microsandbox_artifacts_unavailable`, `runtime_download_failed`, `capacity_insufficient` or `provider_unavailable` |
| Node `host.available_disk_bytes` | Not applicable | Free space on the filesystem of the node state directory, not a container's disk | Free space on the filesystem of the node state directory; sandbox disks have their own quotas |
| Allocation `compute_phase`, `compute_phase_changed_at` | Not applicable: no node allocations | Always `disabled`, counted as running until release; the time is the allocation's creation | Includes `suspended`; its time plus `suspension.retention_seconds` tells roughly when Core reclaims the snapshot |
| Runtime observation `cpu`, `memory` | From E2B metrics: `cpu.utilization_ratio` and `capacity_cores`, memory usage and limit; no cumulative CPU time | From Docker stats: `cpu.usage_seconds_total`, CPU and memory limits, memory usage | From the VM: `cpu.usage_seconds_total`, CPU and memory limits, memory usage |
| Runtime observation `disk` | E2B `diskUsed` and `diskTotal`; `null` when the template does not report them | `null`: no disk quota | `null` |
| Runtime observation `lifecycle_state: sleeping` | Never | Never | While suspended |
| Runtime history CPU | Mean of the utilization ratios E2B reported in each bucket | Derived from cumulative CPU time | Derived from cumulative CPU time |

## Errors

| HTTP | Code | When |
| --- | --- | --- |
| 400 | `invalid_request_error` or `invalid_request` | A malformed request |
| 400 | `invalid_sandbox_configuration` | A validated configuration diagnostic |
| 409 | `sandbox_generation_stale` | `expected_generation` is not current; `current_generation` gives the current one |
| 409 | `sandbox_reset_required` | A different backend; the details name the current and requested providers |
| 409 | `sandbox_not_configured` | A change to an unconfigured deployment |
| 409 | `sandbox_reset_in_progress` | A management write or new enrollment during a reset |
| 409 | `sandbox_deployment_conflict` | Another state the change cannot apply to |
| 409 | `runtime_node_in_use` | Node removal while it holds resources |
| 503 | `execution_unavailable` | Provider preparation is unavailable |
| 503 | `credential_storage_unavailable` | Core has no credential encryption key |

Storage and credential failures stay errors: an empty or failed read never proves cleanup. The [machine connection API](./machine-api.md#node-route-errors) lists the errors of the node routes.

## Canonical node specification

`sandbox/deployment_contract.go` owns the resource bounds, provider requirements, release patterns and canonical field order; `sandbox/deployment.go` applies them in Core. The installer consumes the generated declaration in `deploy/install/node_spec.py`, so there is no second set of limits or patterns. Regenerate it from the repository root with `go run ./services/core/cmd/specification-contract -write`; the sandbox Go tests, part of `make check`, reject a stale projection.

The specification digest is the SHA-256 of compact UTF-8 JSON with `provider` first, then `resources`, then `runtime` when the provider requires it. Resource and Runtime fields follow the contract's declaration order; zero optional disk fields are omitted and required fields stay present. Release identities are lowercase ASCII, and the digest never depends on the incoming field order or whitespace. `services/core/internal/sandbox/testdata/deployment-contract.json` holds shared acceptance cases, exact canonical bytes and digests that both the Go and Python tests consume.
