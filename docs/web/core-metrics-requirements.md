# Core metrics: backend requirements

Status: requested by Core Web (Monitor › Core metrics). The page and the typed
client (`packages/agents-client/src/core-metrics.ts`) are built against the
contract below; Core does not serve it yet, and the page shows "Core does not
report its own metrics yet" until it does. It implements the "System" section of
`contracts/agents-api/system-observability-plan.md` (PR #93).

## Scope

Core metrics answer what no other console page does: is Core serving requests,
is its Turn scheduling keeping up, are its dependencies healthy, and is its
process within limits. Agent outcomes, durations, models and tools stay on Agent
metrics; sandbox capacity and hosted Runtimes stay on Sandbox metrics. The two
must not be duplicated here.

## Endpoint

`GET /core/v1/admin/core-metrics?range=1h|6h|24h|7d`

- Deployment administrator only (the paired console's Web API). No tenant,
  project, key or free-form query parameters.
- Aggregated server-side over complete buckets. Suggested resolution: 60 s (1h),
  300 s (6h), 900 s (24h), 7200 s (7d).
- Every figure Core cannot measure is `null`, never `0`. An empty bucket is not an
  observed zero.
- Health polling and the metrics request itself are excluded from ingress counts.

## Response

```json
{
  "object": "core.metrics",
  "range": { "start": "RFC 3339", "end": "RFC 3339", "resolution_seconds": 60 },
  "service": {
    "status": "running | maintenance | degraded",
    "version": "string | null",
    "started_at": "RFC 3339 | null",
    "instances": "integer | null"
  },
  "ingress": {
    "requests": "integer | null",
    "client_errors": "integer | null",
    "server_errors": "integer | null",
    "latency_ms": { "p50": "number | null", "p95": "number | null" },
    "series": [
      { "start": "RFC 3339", "success": 0, "client_error": 0, "server_error": 0, "p50_ms": 0, "p95_ms": 0 }
    ],
    "routes": [
      { "family": "agents_api | web_api | sandbox_admin | node_channel | other", "requests": 0, "client_errors": 0, "server_errors": 0, "p95_ms": 0 }
    ]
  },
  "execution": {
    "active_turns": "integer | null",
    "queued_turns": "integer | null",
    "queue_wait_ms": { "p50": "number | null", "p95": "number | null" },
    "series": [ { "start": "RFC 3339", "queued": "integer | null" } ]
  },
  "dependencies": [
    {
      "id": "string",
      "kind": "database | queue | object_storage | model_provider | runtime_sampler | other",
      "name": "string",
      "status": "ok | degraded | down | unknown",
      "latency_p95_ms": "number | null",
      "error_rate": "number 0..1 | null",
      "checked_at": "RFC 3339 | null"
    }
  ],
  "process": {
    "cpu_cores": "number | null",
    "cpu_limit_cores": "number | null",
    "memory_bytes": "integer | null",
    "memory_limit_bytes": "integer | null",
    "open_connections": "integer | null",
    "disk_used_bytes": "integer | null",
    "disk_total_bytes": "integer | null",
    "series": [ { "start": "RFC 3339", "cpu_cores": "number | null", "memory_bytes": "integer | null" } ]
  }
}
```

The client also accepts `execution.completed`, `failed`, `cancelled` and
`run_duration_ms` for forward compatibility, but the page does not show them:
Turn outcomes and durations belong to Agent metrics.

## Semantics

- **Ingress:** completed HTTP requests by bounded route family and coarse outcome
  (2xx/3xx success, 4xx client error, 5xx server error), with a latency histogram
  recorded after the response. Transport health, not Turn success.
- **Queue:** `active_turns` and `queued_turns` are current gauges from Core's
  scheduling ownership; `queued` per bucket is the highest queued count seen;
  queue wait is measured from persisted enqueue and start timestamps.
- **Dependencies:** status from Core's own checks and real calls. Model providers
  are the configured provider endpoints (one row each), not model names.
  `runtime_sampler` reports collector health: failed or timed-out samples over
  attempted samples.
- **Process:** all Core instances together. Disk is the volume that holds Core's
  data directory.
- Metric labels stay bounded: no Session, allocation, tenant, key, tool name or
  native identifiers.
