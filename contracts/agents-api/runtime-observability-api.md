# Runtime observation API proposal

Status: review proposal. These routes are not implemented and are not yet present
in `openapi.yaml`.

This is an Agents Core extension, not an upstream OpenAI Agents resource. The
implementation must record that status in the coverage ledger and generated
OpenAPI contract.

## Routes

### List current Runtime observations

```http
GET /v1/agents/runtime-observations?after={target_id}&limit=20&order=desc
OpenAI-Beta: agents=v1
Authorization: Bearer ...
```

| Field | Rules |
| --- | --- |
| `after` | Observation ID from the previous page. Optional, supplied once. |
| `limit` | Integer 1–100, default 20. |
| `order` | `asc` or `desc`, default `desc`. |

The list contains one current Runtime context for every Session visible to the
authenticated tenant, including explicit `none`, unsupported `self_hosted`, and
released managed contexts. Ordering uses the same Session creation-time and ID
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
      "id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
      "object": "agent.runtime_observation",
      "session_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
      "environment_id": "env_...",
      "mode": "openai_hosted",
      "provider_type": "docker",
      "instance": {
        "kind": "managed_allocation",
        "allocation_id": "alloc_...",
        "device_id": "device_...",
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
        "usage_cores": 1.42,
        "utilization_ratio": 0.71
      },
      "memory": {
        "usage_bytes": 805306368,
        "limit_bytes": 2147483648
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
GET /v1/agents/sessions/{session_id}/runtime-observation
OpenAI-Beta: agents=v1
Authorization: Bearer ...
```

This returns the same object shape as a list item. It never starts a Turn, creates
an Environment, provisions compute, renews a lease, or changes lifecycle state.

A valid `environment:none` Session returns `200` with status `unsupported`; the
Session exists but has no attributable Runtime instance. A missing or foreign
Session returns the existing indistinguishable not-found error.

## Resource schema

### `RuntimeObservation`

| Field | Type | Required | Semantics |
| --- | --- | --- | --- |
| `id` | string | yes | Session UUID; stable identity of this current-observation resource and its list cursor. |
| `object` | literal | yes | `agent.runtime_observation`. |
| `session_id` | string | yes | Authorized Core Session. |
| `environment_id` | string or null | yes | Null only for mode `none`. |
| `mode` | enum | yes | `none`, `self_hosted`, `openai_hosted`. |
| `provider_type` | string or null | yes | Forward-compatible safe source kind such as `docker`; null when no provider applies. Clients must not treat an unknown nonempty value as an error. |
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
single sample.

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

| HTTP | Code | When |
| --- | --- | --- |
| 400 | `unsupported_parameter` | Unknown or duplicate query fields. |
| 400 | `invalid_request` | Empty or invalid limits, order, or malformed cursor. |
| 401 | `authentication_error` | Missing or invalid API authentication. |
| 404 | `not_found` | Missing or foreign Session/cursor, indistinguishably. |
| 429 | `rate_limit_exceeded` | Runtime sampling read budget exceeded. |
| 500 | `internal_error` | Integrity, ownership, or invalid provider evidence. |
| 503 | `execution_unavailable` | Required Runtime observation service is not configured. |

Errors never include provider raw responses or credentials.

## Freshness and caching

- Return `Cache-Control: no-store`.
- An internal cache may coalesce reads for at most five seconds.
- `observed_at` is authoritative for freshness; HTTP response time is not.
- Clients mark samples stale according to their own explicit threshold.
- `ETag` is not proposed because observations change independently.

## Client contract

`packages/agents-client` should expose:

```ts
type RuntimeObservationStatus = "observed" | "unsupported" | "unavailable";
type RuntimeObservationReason =
  | "runtime_mode_not_observable"
  | "allocation_pending"
  | "runtime_not_running"
  | "source_not_configured"
  | "sample_timeout"
  | "sample_unavailable";

interface RuntimeObservation {
  id: string;
  object: "agent.runtime_observation";
  session_id: string;
  environment_id: string | null;
  mode: "none" | "self_hosted" | "openai_hosted";
  provider_type: string | null;
  instance: {
    kind: "managed_allocation" | "self_hosted_connection" | "none";
    allocation_id: string | null;
    device_id: string | null;
    connection_generation: string | null;
  };
  status: RuntimeObservationStatus;
  reason: RuntimeObservationReason | null;
  allocation_created_at: number | null;
  resolved_at: number;
  observed_at: number | null;
  started_at: number | null;
  cpu: {
    usage_seconds_total: number | null;
    capacity_cores: number | null;
    usage_cores: number | null;
    utilization_ratio: number | null;
  } | null;
  memory: {
    usage_bytes: number | null;
    limit_bytes: number | null;
  } | null;
}

interface RuntimeObservationPage {
  object: "list";
  data: RuntimeObservation[];
  has_more: boolean;
  first_id: string | null;
  last_id: string | null;
}

interface RuntimeObservationClient {
  list(options?: {
    after?: string;
    limit?: number;
    order?: "asc" | "desc";
  }): Promise<RuntimeObservationPage>;

  retrieveForSession(sessionId: string): Promise<RuntimeObservation>;
}
```

The client validates every required field, enum, nullability rule, timestamp, and
finite number. Unknown additive fields are ignored. Malformed data rejects the
whole page; Web does not publish a partial snapshot.

Web also applies a configured whole-refresh budget. If `has_more` remains true
when that budget is exhausted, it retains the prior complete snapshot and marks
the refresh incomplete; it does not publish partial values as global totals.
After both Runtime-observation and Session traversals complete, Web also requires
their Session ID sets to be identical. A mismatch caused by concurrent creation or
deletion makes the candidate incomplete and prevents publication.

## Deliberately excluded

- Token usage: use existing Session/Turn Usage.
- Billing and cost: product/backend concern.
- Historical series: optional later capability with a separate contract.
- Container logs and command output.
- Provider credentials or native configuration.
- Start, stop, pause, resume, restart, renew, or delete operations.
- Idle classification and automatic shutdown.
