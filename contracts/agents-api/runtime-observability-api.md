---
title: "Runtime telemetry API"
---

Core reports what hosted Runtimes and sandbox nodes consume through read-only administrator routes under `/core/v1`: current Runtime observations, the stored Runtime history of one Session, and the host observations and history of a sandbox node. Reads never create, wake, renew or change compute and never add samples to history. [Runtime observability](./runtime-observability.md) defines how Core collects and keeps these values; [Console API usage](../../docs/web/console-api-usage.md) lists the Web pages that read them.

Every route requires the Core key as the bearer credential; a missing or invalid key returns 401 `invalid_admin_key`. A Project ID in a path selects the target Project and does not authenticate. Responses carry `Cache-Control: no-store`, use the Core error envelope and never contain provider responses, native identifiers, paths or credentials.

## Current Runtime observations

### List observations of every Project

```http
GET /core/v1/sandbox/runtime-observations?after={session_id}&limit=20&order=desc
Authorization: Bearer <Core key>
```

| Parameter | Rules |
| --- | --- |
| `after` | Observation ID (a Session ID) that ended the previous page. |
| `limit` | 1 to 100, default 20. |
| `order` | `asc` or `desc` by Session creation time, default `desc`. |

The list has one row for every Session of every Project that is not deleted, including `none`, `self_hosted` and released managed Sessions. Each row carries the owning `project_id` and an `observation`: the [`RuntimeObservation`](#runtimeobservation) plus [`disk`](#disk). Pages use the Session list's creation-time and ID keyset. The observation ID is the Session ID, so page boundaries do not move when the Runtime behind a Session changes. A page is not an atomic snapshot: each row has its own `resolved_at` and, when sampled, `observed_at`. Unknown query keys are ignored.

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
        "lifecycle_state": "active",
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

### Retrieve one Session's observation

```http
GET /core/v1/projects/{project_id}/sessions/{session_id}/runtime-observation
Authorization: Bearer <Core key>
```

This returns one `RuntimeObservation`, without `disk`. It accepts no query parameters. An `environment:none` Session returns 200 with status `unsupported`.

### `RuntimeObservation`

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | The Session ID; the stable identity of this resource and its list cursor. |
| `object` | string | `agent.runtime_observation`. |
| `session_id` | string | The Session. |
| `environment_id` | string or null | Null only for mode `none`. |
| `mode` | enum | `none`, `self_hosted` or `openai_hosted`. |
| `provider_type` | string or null | Source kind, such as `docker`, `microsandbox` or `e2b`; null when no provider was read. Treat an unknown value as a new kind, not an error. |
| `instance` | object | The current compute identity; see [`RuntimeInstance`](#runtimeinstance). |
| `lifecycle_state` | enum or null | Core's own lifecycle view of a managed allocation; null for `none` and `self_hosted`. See below. |
| `status` | enum | `observed`, `unsupported` or `unavailable`. |
| `reason` | enum or null | Why the row has no sample; see [Status and reason](#status-and-reason). |
| `allocation_created_at` | integer or null | Unix seconds when the managed allocation was created. |
| `resolved_at` | integer | Unix seconds when Core resolved this row. |
| `observed_at` | integer or null | Unix seconds of the provider sample; null without a sample. |
| `started_at` | integer or null | Unix seconds when the current compute incarnation started. |
| `cpu` | object or null | Null when no CPU value was observed. |
| `memory` | object or null | Null when no memory value was observed. |

`lifecycle_state` comes from Core's allocation records, never from the sample:

| Value | Allocation |
| --- | --- |
| `pending` | Not created yet, or being created |
| `active` | Running |
| `sleeping` | Suspended |
| `transitioning` | Quiescing, suspending, restoring or waking |
| `stopped` | Cleanup pending, or released |

### `RuntimeInstance`

| Field | Meaning |
| --- | --- |
| `kind` | `managed_allocation`, `self_hosted_connection` or `none`. |
| `allocation_id` | The managed allocation, which identifies the compute of a managed Session; null otherwise. |
| `device_id` | The Runtime device bound to the managed allocation, when there is one; null otherwise. |
| `connection_generation` | Always null: Core does not observe self-hosted connections. |

### `cpu`

All fields are finite nonnegative numbers or null. Zero is an observed zero; null is unavailable.

| Field | Meaning |
| --- | --- |
| `usage_seconds_total` | Cumulative CPU seconds of the current compute incarnation (Docker, microsandbox). |
| `capacity_cores` | Configured CPU capacity, greater than zero. |
| `usage_cores` | Always null. |
| `utilization_ratio` | Provider-reported share of `capacity_cores` (E2B), not clamped; null for providers that report cumulative CPU time. |

### `memory`

`usage_bytes` and `limit_bytes` are safe JSON integers or null. Zero usage is observed zero; `limit_bytes` is at least 1, and an unknown or unlimited limit is null.

### `disk`

Only list rows carry `disk`: null, or `{usage_bytes, limit_bytes}` with the rules of `memory`. E2B fills it when the sandbox reports both its disk usage and a nonzero capacity. Docker and microsandbox return null. A non-null `disk` appears only on an `observed` row.

### Status and reason

| Status | Reason | When |
| --- | --- | --- |
| `observed` | null | The provider returned a sample. |
| `unsupported` | `runtime_mode_not_observable` | `none` and `self_hosted` Sessions. |
| `unavailable` | `allocation_pending` | The managed allocation does not exist yet or is being created. |
| `unavailable` | `runtime_not_running` | The allocation is being cleaned up or is released, or the provider reports the Runtime absent, stopped or suspended. |
| `unavailable` | `source_not_configured` | No observation source serves the allocation's provider. |
| `unavailable` | `sample_timeout` | The provider read exceeded its deadline. |
| `unavailable` | `sample_unavailable` | The provider could not produce a current sample. |

An ownership mismatch, malformed durable identity or invalid provider evidence fails the request instead of becoming an `unavailable` row. The generated `core.openapi.yaml` records each field's type, nullability and enum but cannot express which combinations of status, mode and fields are valid; this table and the field rules above are normative.

### Errors

| HTTP | Code | When |
| --- | --- | --- |
| 400 | `invalid_request_error` | List: a repeated `after`, `limit` or `order`, or an invalid `limit` or `order`. |
| 400 | `unsupported_parameter` | Single read: any query parameter. |
| 404 | `not_found_error` | A missing Project; a missing, malformed or foreign Session or list cursor. |
| 500 | `internal_error` | Inconsistent identity or invalid provider evidence. |
| 503 | `execution_unavailable` | The list exceeded its collection budget. |

### Client

`packages/agents-client` exposes `AdminClient.listRuntimeObservations({after, limit, order})` and `AdminClient.retrieveRuntimeObservation(projectId, sessionId, options)`. `RuntimeObservation` in `src/types.ts` is a union discriminated by `status` and `mode`; `AdminRuntimeObservation` adds `disk`. The client checks every field, enum, nullability rule, timestamp and number and rejects unknown fields. A malformed observation rejects with a 502 `invalid_runtime_observation` error and a malformed page with `invalid_admin_response`; one bad row rejects the whole page.

## Session Runtime history

```http
GET /core/v1/projects/{project_id}/sessions/{session_id}/runtime-history?start=1789951200&end=1789954800&max_points=120
Authorization: Bearer <Core key>
```

| Parameter | Rules |
| --- | --- |
| `start` | Required. Inclusive Unix second, 0 or more. |
| `end` | Required. Exclusive Unix second, after `start`, at most 24 hours after it and at most one second in the future. |
| `max_points` | Optional. Buckets per array, 2 to 1000; default 120. |

Each parameter may appear once. Core chooses the bucket width: the range divided by `max_points`, rounded up to whole seconds, and at least 30 seconds or the sampling interval, whichever is longer. Buckets start at `start`; the last one ends at `end`.

Core resolves the Project, then the Session and its Environment, before it reads storage; allocation and provider identities are results, never query inputs. History exists only for `openai_hosted` Sessions.

```json
{
  "object": "agent.runtime_history",
  "source": "durable",
  "session_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
  "requested_range": { "start": 1789951200, "end": 1789954800 },
  "resolution_seconds": 60,
  "generated_at": 1789954801,
  "coverage": {
    "retained_start": 1789951200,
    "first_sample_at": 1789951210,
    "last_sample_at": 1789954750,
    "sample_count": 118,
    "expected_sample_count": 120,
    "buckets": []
  },
  "series": [],
  "token_usage": []
}
```

`source` is always `durable`. `resolution_seconds` is the bucket width and `generated_at` the read time. Only buckets that hold at least one sample appear in `coverage.buckets`, `series[].points` and `token_usage`; a gap stays a gap, never a zero.

### Coverage

`coverage` counts every stored sample of the Session in the range, including unavailable ones that belong to no allocation. `retained_start` is the later of `start` and seven days before `generated_at`. `expected_sample_count` is the number of sampling intervals between `retained_start` and `end`, rounded up. Each bucket has `start`, `end`, `first_observed_at`, `last_observed_at`, `observation_count`, `observed_count` and `unavailable_count`.

### Series

There is one series per managed allocation, keyed by `allocation_id`, so a provider that pauses, restores or replaces compute under the same allocation keeps one series. `environment_id` and `provider_type` identify its source. `started_at` is the earliest retained start of the allocation's compute, as JSON-safe `{seconds, nanoseconds}` with nanoseconds 0 to 999,999,999; it is not a per-bucket start, and compute uptime comes only from current observations.

Each point has the bucket bounds, the coverage counts of the allocation's samples, and nullable `cpu` and `memory` objects with a `contributor_count` of at least 1:

- `cpu.utilization_ratio` comes from consecutive cumulative CPU counters of one compute incarnation: the CPU seconds consumed divided by the elapsed time multiplied by the capacity, over the intervals that end in the bucket. The baseline resets when the incarnation changes or a counter decreases. E2B reports no cumulative CPU time; its bucket value is the mean of the ratios sampled in it. `cpu.capacity_cores` is the last capacity in the bucket.
- `memory.usage_bytes` and `memory.limit_bytes` are the last values observed in the bucket.

Disk is not kept in history.

### Token usage

`token_usage` belongs to the Session, not to an allocation. Each point holds the last cumulative measured Session usage sampled in its bucket: `start`, `end`, `sampled_at`, `input_tokens` and `output_tokens`. Measured Session usage is a Core extension that sums every recorded root Turn snapshot, active Turns included. It differs from [public Session usage](./sessions-events.md#usage), which is null while a root Turn runs or after one ends unmeasured. These counters are measured model tokens, not prices or billing records.

### Errors and bounds

| HTTP | Code | When |
| --- | --- | --- |
| 400 | `unsupported_parameter` | A parameter other than `start`, `end` and `max_points`, or one supplied twice. |
| 400 | `invalid_request` | An invalid range or `max_points`. |
| 404 | `not_found_error` | A missing Project, or a Session missing from it. |
| 409 | `runtime_history_unsupported` | The Session is not `openai_hosted`. |
| 500 | `internal_error` | Inconsistent stored identity. |
| 503 | `runtime_history_unavailable` | Core collects no periodic history (it runs without the execution worker), or the read failed, timed out or produced a result outside the bounds. |

A response holds at most `max_points` buckets per array, 64 series and 10,000 coverage and series points in total. Storage error text is neither returned nor logged.

### Client

`AdminClient.retrieveRuntimeHistory(projectId, sessionId, {start, end, maxPoints, signal})` validates the query before sending it. It then checks the exact fields, the echoed range and Session, bucket order within the range, coverage totals, allocation identity, contributor counts, token usage order, nullability, numbers and response size. Any violation rejects the whole response with a 502 `invalid_admin_response` error.

## Node host observations and history

```http
GET /core/v1/sandbox/nodes/{node_id}?range=1h
Authorization: Bearer <Core key>
```

`range` is `1h` (the default), `6h` or `24h`. Another parameter, a repeated or invalid `range`, or a malformed node ID returns 400 `invalid_request`; a missing or removed node returns 404 `not_found_error`. The response is the node object of the [node list](./sandbox-deployment.md) plus `host` and `history`:

```json
{
  "host": {
    "effective_cpu_cores": 4,
    "cpu_utilization": 0.35,
    "total_memory_bytes": 17179869184,
    "available_memory_bytes": 8589934592,
    "available_disk_bytes": 107374182400,
    "observed_at": "2026-09-25T09:00:00Z"
  },
  "history": {
    "resolution_seconds": 60,
    "points": [{
      "start": "2026-09-25T08:59:00Z",
      "cpu_utilization_max": 0.4,
      "memory_used_bytes_max": 8589934592,
      "available_disk_bytes_min": 107374182400
    }]
  }
}
```

`host` is the node's last received heartbeat observation; every unavailable value, including an unobserved `observed_at`, is null. An offline node keeps its last values and their original time, so judge freshness by the node's `online` and `host.observed_at`.

- `cpu_utilization` is the share of busy ticks in the host's aggregate `/proc/stat` counters between two heartbeats, 0 to 1. Idle and I/O-wait ticks are not busy, and guest time is not counted twice. The first heartbeat of a connection, a counter reset and an unreadable baseline give null. It measures the whole visible host, not the node process or its sandboxes.
- `effective_cpu_cores` accounts for the node process's CPU affinity and cgroup limits; null when those cannot be established.
- `total_memory_bytes` and `available_memory_bytes` are `MemTotal` and `MemAvailable`.
- `available_disk_bytes` is the free space of the node's state filesystem, not a sandbox quota.

The node measures what its namespaces can see, so run it on the host it reports on.

`history` covers the range in complete UTC buckets: 60 seconds for `1h`, 300 for `6h` and 900 for `24h`. Every bucket of the range is present, and the bucket in progress is left out. `cpu_utilization_max` and `memory_used_bytes_max` are the maxima of the recorded observations, where used memory is total minus available memory of the same observation; `available_disk_bytes_min` is the minimum. Each metric is null for a bucket without a recorded value, including offline periods. Core never interpolates or backfills.

`SandboxAdminClient.retrieveNode(nodeId, range, options)` in `packages/agents-client` reads this route and validates the response.
