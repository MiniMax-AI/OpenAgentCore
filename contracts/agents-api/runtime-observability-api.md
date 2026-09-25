# Runtime observation API

Status: Phase 2 and initial Core Web consumption implemented. The current-snapshot routes, strict
`packages/agents-client` projection, and generated `core.openapi.yaml` contract are
implemented and consumed by the Dashboard through complete Session/observation
identity joins. Durable history uses the separate optional
[Runtime history API](runtime-history-api.md); lifecycle controls remain outside
this phase.

These are administrator reads under `/core/v1`, authenticated by the Core key,
not upstream OpenAI Agents resources. The
former project routes `GET /v1/agents/runtime-observations` and
`GET /v1/agents/sessions/{session_id}/runtime-observation` are removed.

## Routes

### List current Runtime observations

```http
GET /core/v1/sandbox/runtime-observations?after={session_id}&limit=20&order=desc
Authorization: Bearer ...
```

| Field | Rules |
| --- | --- |
| `after` | Observation ID from the previous page. Optional, supplied once. |
| `limit` | Integer 1–100, default 20. |
| `order` | `asc` or `desc`, default `desc`. |

The list contains one current Runtime context for every Session of every managed
Project, labelled with its owning `project_id`, including explicit `none`,
unsupported `self_hosted`, and released managed contexts. Ordering uses the same Session creation-time and ID
keyset as the Session list. An observation ID is the Session UUID, so pagination
does not change when the underlying Runtime incarnation changes. Pages are not an
atomic telemetry snapshot; every row has its own `resolved_at`, and a successful
provider sample has its own `observed_at`. A client completes the entire page chain
before publishing a new Dashboard snapshot.

```json
{
  "object": "list",
  "data": [
    {
      "project_id": "3f0c2a9e-2b7d-4d0f-9a51-1c8e4b6d7a20",
      "observation": {
        "id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
        "object": "agent.runtime_observation",
        "session_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
        "environment_id": "6c02fb71-5fa8-4298-93e8-57c6625a3fc2",
        "mode": "openai_hosted",
        "provider_type": "docker",
        "instance": {
          "kind": "managed_allocation",
          "allocation_id": "d23ab94e-e40b-45bd-93a2-444f1f74642b",
          "device_id": "2e434f4f-76aa-4e54-a707-4757036d90ef",
          "connection_generation": null
        },
        "status": "observed",
        "reason": null,
        "allocation_created_at": 1789951200,
        "resolved_at": 1789953021,
        "observed_at": 1789953020,
        "started_at": 1789951220,
        "cpu": {
          "usage_seconds_total": 482.75,
          "capacity_cores": 2.0,
          "usage_cores": null,
          "utilization_ratio": null
        },
        "memory": {
          "usage_bytes": 805306368,
          "limit_bytes": 2147483648
        },
        "disk": null
      }
    }
  ],
  "has_more": false,
  "first_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
  "last_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3"
}
```

### Retrieve one Session's current Runtime observation

```http
GET /core/v1/projects/{project_id}/sessions/{session_id}/runtime-observation
Authorization: Bearer ...
```

This returns the same object shape as a list item's `observation`, without the
administrator list's `disk` (see the [administrator contract](admin-api.md)). It never starts a Turn, creates
an Environment, provisions compute, renews a lease, or changes lifecycle state.

A valid `environment:none` Session returns `200` with status `unsupported`; the
Session exists but has no attributable Runtime instance. A missing Session, or one
outside the Project, returns the existing indistinguishable not-found error.

## Resource schema

### `RuntimeObservation`

| Field | Type | Required | Semantics |
| --- | --- | --- | --- |
| `id` | string | yes | Session UUID; stable identity of this current-observation resource and its list cursor. |
| `object` | literal | yes | `agent.runtime_observation`. |
| `session_id` | string | yes | Authorized Core Session. |
| `environment_id` | string or null | yes | Null only for mode `none`. |
| `mode` | enum | yes | `none`, `self_hosted`, `openai_hosted`. |
| `provider_type` | string or null | yes | Forward-compatible safe source kind such as `docker` or `microsandbox`; null when no provider applies. Clients must not treat an unknown nonempty value as an error. |
| `instance` | object | yes | Provider-neutral current incarnation identity; explicit `kind=none` when no compute applies. |
| `status` | enum | yes | `observed`, `unsupported`, `unavailable`. |
| `reason` | enum or null | yes | Safe reason when status is not `observed`. |
| `allocation_created_at` | integer or null | yes | Unix seconds for managed allocation age. |
| `resolved_at` | integer | yes | Unix seconds when Core resolved identity and status for this row. |
| `observed_at` | integer or null | yes | Provider sample time; null without a sample. |
| `started_at` | integer or null | yes | Current compute incarnation start time. |
| `cpu` | object or null | yes | Null when no CPU fields were observed. |
| `memory` | object or null | yes | Null when no memory fields were observed. |

### `RuntimeInstance`

```json
{
  "kind": "managed_allocation",
  "allocation_id": "alloc_...",
  "device_id": "device_...",
  "connection_generation": null
}
```

`kind` is `managed_allocation`, `self_hosted_connection`, or `none`. For a managed
context, `allocation_id` is the incarnation key; for self-hosted, the current
`connection_generation` is the incarnation key. Fields that do not apply are
explicit nulls. Provider-native container IDs, pod names, host paths, credentials,
and raw labels are not public fields.

### `RuntimeCPUObservation`

```json
{
  "usage_seconds_total": 482.75,
  "capacity_cores": 2.0,
  "usage_cores": 1.42,
  "utilization_ratio": 0.71
}
```

All fields are `number | null`. Values are finite and nonnegative;
`capacity_cores`, when present, is greater than zero. Numeric zero is observed
zero. Null is unavailable. `usage_cores` is the cumulative CPU delta divided by
the observation-time delta for two ordered samples of the same incarnation.
`utilization_ratio` is `usage_cores / capacity_cores`. It is not clamped: a value
above 1 is retained as provider/accounting evidence and is not interpreted as a
lifecycle signal. Both derived fields are null after a cache restart or whenever
either source sample is absent or invalid. The API never derives CPU rate from a
single sample of cumulative time. A provider that reports only a current share
of its CPU capacity (E2B) fills `utilization_ratio` with that report and leaves
`usage_seconds_total` and `usage_cores` null.

### `RuntimeMemoryObservation`

```json
{
  "usage_bytes": 805306368,
  "limit_bytes": 2147483648
}
```

Both fields are `integer | null`. Values are nonnegative and safe JSON integers.
Zero usage is observed zero. A missing or unlimited provider limit is null.

## Status and reason matrix

| Status | Allowed reason |
| --- | --- |
| `observed` | null |
| `unsupported` | `runtime_mode_not_observable` |
| `unavailable` | `allocation_pending`, `runtime_not_running`, `source_not_configured`, `sample_timeout`, `sample_unavailable` |

Ownership mismatch, malformed durable identity, corrupt provider evidence, and
authorization failure are not downgraded to unavailable rows.

## Error responses

Use the existing Agents API error envelope.

| HTTP | Type / code | When |
| --- | --- | --- |
| 400 | `invalid_request_error` / `invalid_request_error` | List: a repeated supported query key, or an empty or invalid limit or order, with the shared Beta list messages. Unknown list query keys are ignored. |
| 400 | `invalid_request_error` / `unsupported_parameter` | Single-Session retrieval with any query parameter. |
| 401 | `invalid_request_error` / `invalid_admin_key` | Missing or invalid Core key. |
| 404 | `not_found_error` / `not_found_error` | Missing, malformed or foreign Session/cursor, indistinguishably, as for the [Session list cursor](list-query-semantics.md#list-cursor-errors--september-23-2026). |
| 500 | `server_error` / `internal_error` | Integrity, ownership, or invalid provider evidence. |
| 503 | `server_error` / `execution_unavailable` | Required Runtime observation service is not configured, or list collection exceeded its request budget. |

Errors never include provider raw responses or credentials.

## Freshness and caching

- Return `Cache-Control: no-store`.
- The Phase 2 implementation performs bounded direct reads and has no observation
  cache. A later internal cache may coalesce reads for at most five seconds.
- `observed_at` is authoritative for freshness; HTTP response time is not.
- Clients mark samples stale according to their own explicit threshold.
- `ETag` is not proposed because observations change independently.

## Client contract

`packages/agents-client` exposes:

```ts
type RuntimeObservationStatus = "observed" | "unsupported" | "unavailable";
type RuntimeObservationReason =
  | "runtime_mode_not_observable"
  | "allocation_pending"
  | "runtime_not_running"
  | "source_not_configured"
  | "sample_timeout"
  | "sample_unavailable";

type RuntimeObservation =
  | RuntimeObservedObservation
  | RuntimeUnavailableObservation
  | RuntimeNoneObservation
  | RuntimeSelfHostedObservation;

interface AdminRuntimeObservation {
  project_id: string;
  observation: RuntimeObservation & { disk: RuntimeDiskObservation | null };
}

class AdminClient {
  listRuntimeObservations(options?: {
    after?: string;
    limit?: number;
    order?: "asc" | "desc";
  }): Promise<ListPage<AdminRuntimeObservation>>;

  retrieveRuntimeObservation(projectId: string, sessionId: string): Promise<RuntimeObservation>;
}
```

These exported variants discriminate on `status` and `mode`; their instance,
reason, timestamps, CPU, and memory fields narrow accordingly. The exact variant
definitions live in `packages/agents-client/src/types.ts` and mirror the status
and reason matrix above.

The client validates every required field, enum, nullability rule, timestamp, and
finite number. The current pinned contract rejects unknown additive fields so an
unreviewed server expansion cannot silently cross the browser boundary. Malformed
data rejects the whole page; Web does not publish a partial snapshot.

The generated OpenAPI 2 schema records field-level required/nullability rules,
UUID formats, reason enums, and numeric minima. OpenAPI 2
cannot encode the complete cross-field discriminated union. The matrix above is
normative for wire consumers; the server projection and strict TypeScript
projector enforce it, and the exported TypeScript type prevents invalid
status/mode combinations in typed consumers.

Web also applies a configured whole-refresh budget. If `has_more` remains true
when that budget is exhausted, it retains the prior complete snapshot and marks
the refresh incomplete; it does not publish partial values as global totals.
After both Runtime-observation and Session traversals complete, Web also requires
their Session ID sets to be identical. A mismatch caused by concurrent creation or
deletion makes the candidate incomplete and prevents publication.

## Deliberately excluded

- Token usage: use existing Session/Turn Usage.
- Billing and cost: product/backend concern.
- Historical series in these routes: the optional capability uses the separate
  [Runtime history API](runtime-history-api.md).
- Container logs and command output.
- Provider credentials or native configuration.
- Start, stop, pause, resume, restart, renew, or delete operations.
- Idle classification and automatic shutdown.
