# Runtime history API

Status: Core-key contract, strict TypeScript client, PostgreSQL history and Core Web
History ranges implemented. Core uses its existing database; the execution owner
samples every 30 seconds by default. API-only processes without an execution worker
collect only on read; their Session history read returns
`503 runtime_history_unavailable` rather than claiming Durable coverage.

This is a read-only, backend-neutral administrator read under `/core/v1`,
authenticated by the Core key. The former project routes
`GET /v1/agents/runtime-history/capabilities` and
`GET /v1/agents/sessions/{session_id}/runtime-history`, and the administrator
capability read, are removed. History is available only with a validated Reader,
retention and query bounds and qualified periodic collection; otherwise, or when
that configuration is malformed, the Session read fails closed with 503. Backend
names, URLs, credentials, table names and tenant data are never returned. The
browser never receives a storage endpoint, OTLP credential, provider-native
identity or tenant selector.

## Session history

```http
GET /core/v1/projects/{project_id}/sessions/{session_id}/runtime-history?start=1789951200&end=1789954800&max_points=120
Authorization: Bearer ...
```

`start` is inclusive and `end` is exclusive, both in whole Unix seconds.
`max_points` defaults to the lower of 120 and the configured maximum. Core
selects an effective whole-second resolution. Unknown parameters, duplicate
parameters, negative timestamps, invalid ranges, and invalid point limits are
rejected before a Reader query.

The caller supplies a Project ID and a Session ID. Core resolves the Project's
tenant, then the Session and Environment from its store, and only then calls the
Reader. Missing Sessions and other Projects' Sessions remain indistinguishable. Allocation IDs,
provider IDs, and backend labels are result identity, never authority-bearing
query inputs.

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

`coverage` describes all resolved samples, including unavailable observations that
cannot safely be attached to one allocation series. `retained_start` is the
latest of the requested start, configured retention boundary, and backend-reported
retention boundary. Expected coverage is calculated only over that retained
period and only from qualified periodic cadence.

Resource `series` are keyed only by `allocation_id`. This keeps one continuous
Dashboard lifecycle when a provider pauses, restores, restarts, or replaces its
underlying compute without replacing the durable allocation. `started_at` remains
the earliest retained provider start estimate; it is
not series identity:

```json
{
  "started_at": {
    "seconds": 1789951210,
    "nanoseconds": 123456789
  }
}
```

The two integers are JSON-safe, nonnegative Unix seconds and a 0–999,999,999
nanosecond remainder. CPU utilization is derived from ordered cumulative counters
inside the allocation. A counter regression resets the baseline, so no interval
is derived across a compute replacement. Successive valid counter intervals are
assigned to the bucket containing their right endpoint and combined by CPU-capacity
time. E2B reports no cumulative CPU time: each periodic E2B sample stores its
reported utilization ratio, and the bucket's `utilization_ratio` is the mean of
the ratios sampled in it, in the same field. Disk observations are not retained. Memory values are the last observed values in a bucket. Every point contains
observation and contributor counts. Missing values are null and gaps remain gaps.
Numeric zero is retained as an observed value.

`token_usage` is Session-scoped rather than allocation-scoped. Each point is the
last cumulative measured Session usage sampled in that bucket and
contains `input_tokens`, `output_tokens`, and `sampled_at`. Web derives throughput
only from adjacent nondecreasing cumulative points. A missing measurement or a
counter regression produces a gap; it is never filled with zero. The sampled
value is Core's measured Session usage, a Core extension that sums every
recorded root Turn snapshot, active Turns included. It differs by design from
public Session usage, which is null while a root Turn runs or after one ends
unmeasured ([item serialization](history-events-usage.md#item-serialization-2026-09-23)). These counters
are measured model usage, not price, cost, or billing records.

Core Web queries each current managed Session through the administrator Session
route with bounded concurrency and an all-or-nothing target budget, offering 1h,
6h, and 24h History ranges. Reloading Web reconstructs the
charts from the backend; Live browser samples and Durable buckets remain explicit
separate sources and are never silently merged.

## Errors and bounds

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `unsupported_parameter` or `invalid_request` | Invalid query shape or range. |
| 401 | `invalid_admin_key` | Missing or invalid Core key. |
| 404 | `not_found` | Missing Project, or a Session outside it. |
| 409 | `runtime_history_unsupported` | The Session has no supported managed Runtime history scope. |
| 503 | `runtime_history_unavailable` | Durable history is unconfigured, timed out, unavailable, or returned malformed data. |

Reader failures and malformed results are sanitized. Raw backend errors are not
logged or returned. Results are bounded by per-series points, series count, and
total response points. Coverage and series points must be ordered, non-overlapping,
inside the half-open request range, and contain valid safe JSON values.

## Client contract

`packages/agents-client` exposes:

```ts
class AdminClient {
  retrieveRuntimeHistory(projectId: string, sessionId: string, query: {
    start: number;
    end: number;
    maxPoints?: number;
    signal?: AbortSignal;
  }): Promise<RuntimeHistory>;
}
```

The client validates exact fields, requested-range echo,
Session identity, half-open bucket ordering, coverage totals, allocation identity,
contributor counts, token usage ordering, nullability, finite numbers, and response size. Unknown fields
or malformed data reject the entire response with a 502 `invalid_admin_response` error.

## Explicit boundaries

- The routes never sample a live provider, provision compute, or mutate lifecycle.
- The contract does not expose a storage backend.
- History availability does not imply current Runtime readiness.
- Current observations and Durable history have separate freshness and retention
  semantics and must remain separately labelled in Web.

Compute uptime is available from current observations only. Retained allocation
series can span compute restarts and unavailable intervals; their earliest start
is not a per-bucket compute start and must not be used to draw an uptime history.
Clients may project a Dashboard active-Sandbox count by counting distinct
allocation identities with `observed_count > 0` in each bucket and deduplicating
the same allocation across Session histories. The single-Session presentation
collapses any positive count to `1`; a missing or unavailable bucket is currently
rendered as `0`. This temporary zero-fill policy does not distinguish a sleeping
Runtime from missing collection coverage.
