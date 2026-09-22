# Runtime history API

Status: public contract and strict TypeScript client implemented; no production
Reader is configured by default. The capability route therefore advertises
`available=false` until an operator supplies both a validated Reader and qualified
periodic collection.

This is an optional Agents Core extension. It is read-only and backend-neutral.
The browser never receives a ClickHouse endpoint, OTLP credential, provider-native
identity, or tenant selector.

## Capability discovery

```http
GET /v1/agents/runtime-history/capabilities
OpenAI-Beta: agents=v1
Authorization: Bearer ...
```

The route accepts no query parameters. An unconfigured Core returns:

```json
{
  "object": "agent.runtime_history_capabilities",
  "available": false,
  "reason": "not_configured",
  "collection_mode": null,
  "sample_interval_seconds": null,
  "retention_seconds": null,
  "minimum_step_seconds": null,
  "maximum_range_seconds": null,
  "maximum_points": null,
  "metrics": []
}
```

`available=true` requires `collection_mode=periodic`, a qualified positive sample
interval, a validated Reader, retention and query bounds, and at least one of
`cpu` or `memory`. A Reader backed only by request-triggered samples returns
`reason=periodic_collection_required`; Web must not call that data Durable.
Malformed capability configuration fails closed as unconfigured. Backend names,
URLs, credentials, table names, and tenant data are never capability fields.

## Session history

```http
GET /v1/agents/sessions/{session_id}/runtime-history?start=1789951200&end=1789954800&max_points=120
OpenAI-Beta: agents=v1
Authorization: Bearer ...
```

`start` is inclusive and `end` is exclusive, both in whole Unix seconds.
`max_points` defaults to the lower of 120 and the advertised maximum. Core
selects an effective whole-second resolution. Unknown parameters, duplicate
parameters, negative timestamps, invalid ranges, and invalid point limits are
rejected before a Reader query.

The caller supplies only a Session ID. Core obtains the tenant from authentication,
resolves the Session and Environment from its store, and only then calls the
Reader. Missing and foreign Sessions remain indistinguishable. Allocation IDs,
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
  "series": []
}
```

`coverage` describes all resolved samples, including unavailable observations that
cannot safely be attached to one compute incarnation. `retained_start` is the
latest of the requested start, configured retention boundary, and backend-reported
retention boundary. Expected coverage is calculated only over that retained
period and only from qualified periodic cadence.

Resource `series` are split by `allocation_id` plus the lossless compute
`started_at` fence:

```json
{
  "started_at": {
    "seconds": 1789951210,
    "nanoseconds": 123456789
  }
}
```

The two integers are JSON-safe, nonnegative Unix seconds and a 0–999,999,999
nanosecond remainder. They are identity, not merely a display timestamp. CPU
utilization is derived only from ordered cumulative counters inside that fence;
memory values are the last observed values in a bucket. Every point contains
observation and contributor counts. Missing values are null and gaps remain gaps.
Numeric zero is retained as an observed value. The endpoint does not return token
history: token throughput remains sourced from canonical Session Usage and is
Live-only until a separate durable usage contract exists.

## Errors and bounds

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `unsupported_parameter` or `invalid_request` | Invalid query shape or range. |
| 401 | `authentication_error` | Missing or invalid authentication. |
| 404 | `not_found` | Missing or foreign Session. |
| 409 | `runtime_history_unsupported` | The Session has no supported managed Runtime history scope. |
| 503 | `runtime_history_unavailable` | Durable history is unconfigured, timed out, unavailable, or returned malformed data. |

Reader failures and malformed results are sanitized. Raw backend errors are not
logged or returned. Results are bounded by per-series points, series count, and
total response points. Coverage and series points must be ordered, non-overlapping,
inside the half-open request range, and contain valid safe JSON values.

## Client contract

`packages/agents-client` exposes:

```ts
interface AgentCore {
  getRuntimeHistoryCapabilities(options?: ReadOptions): Promise<RuntimeHistoryCapabilities>;
  retrieveRuntimeHistory(sessionId: string, query: {
    start: number;
    end: number;
    maxPoints?: number;
    signal?: AbortSignal;
  }): Promise<RuntimeHistory>;
}
```

The client validates exact fields, capability consistency, requested-range echo,
Session identity, half-open bucket ordering, coverage totals, incarnation identity,
contributor counts, nullability, finite numbers, and response size. Unknown fields
or malformed data reject the entire response with a 502 client projection error.

## Explicit boundaries

- The routes never sample a live provider, provision compute, or mutate lifecycle.
- The contract does not choose ClickHouse, Prometheus, Mimir, or another backend.
- History availability does not imply current Runtime readiness.
- Current observations and Durable history have separate freshness and retention
  semantics and must remain separately labelled in Web.
