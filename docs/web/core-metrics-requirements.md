# Core metrics: backend requirements

Status: requested by Core Web (Monitor › Core metrics). The page and the typed
client (`packages/agents-client/src/core-metrics.ts`) are built against the
contract below; Core does not serve it yet, and the page says "Core does not
report its own metrics yet" until it does.

## What Core is, and what the page measures

Core is one `agents-api` process with one execution owner per database
(`pg_try_advisory_lock`). There is no message queue: a queued Turn is a row in
`turns`, and the execution worker polls it into one of four execution slots
(Turns, environment input and file reads and writes share them) and hands it to
the Session's connected daemon. PostgreSQL is the only store (files are large
objects). Core never calls model providers; harnesses in the Runtime do.

So the page shows only what this process owns: execution slots, the Turn queue
and connected daemons; the database; the periodic background jobs; and the
process. Agent outcomes and durations stay on Agent metrics; sandbox capacity
stays on Sandbox metrics. Instance counts, message queues, object storage,
model-provider health and data-disk usage do not apply and are not requested.

## Endpoint

`GET /core/v1/admin/core-metrics?range=1h|6h|24h|7d`

- Deployment administrator only (the paired console's Web API). No tenant,
  project, key or free-form query parameters.
- Series are aggregated over complete buckets. Suggested resolution: 60 s (1h),
  300 s (6h), 900 s (24h), 7200 s (7d).
- Every figure Core cannot measure is `null`, never `0`.

## Response

```json
{
  "object": "core.metrics",
  "range": { "start": "RFC 3339", "end": "RFC 3339", "resolution_seconds": 60 },
  "service": {
    "status": "running | maintenance | degraded",
    "revision": "source commit | null",
    "started_at": "RFC 3339 | null",
    "execution_owner": "boolean | null"
  },
  "execution": {
    "slots_in_use": 3, "slots_total": 4,
    "queued_turns": 2, "waiting_for_daemon": 1, "in_progress_turns": 3,
    "oldest_queued_seconds": 130,
    "connected_daemons": 9,
    "interrupted": 0, "unavailable": 3,
    "queue_wait_ms": { "p50": 420, "p95": 2600 },
    "series": [ { "start": "RFC 3339", "queued": 1, "in_progress": 3, "queue_wait_p95_ms": 1600 } ]
  },
  "database": {
    "ping_ms": { "p50": 1.8, "p95": 3.4 },
    "pool": { "in_use": 6, "idle": 4, "max": 20 },
    "size_bytes": 1331439861,
    "series": [ { "start": "RFC 3339", "ping_p95_ms": 3.1, "pool_in_use": 7 } ]
  },
  "jobs": [
    { "id": "scheduler | runtime_sampler | history_cleanup | audit_cleanup", "status": "ok | failing | stopped | unknown", "last_run_at": "RFC 3339 | null", "processed": 12, "failed": 1 }
  ],
  "process": { "memory_bytes": 190840832, "goroutines": 214 }
}
```

## Where each figure comes from

| Figure | Source in Core |
| --- | --- |
| `revision`, `started_at` | Build revision via `-ldflags` at build time; process start time |
| `execution_owner` | The execution lease (`lease.Ping`) |
| `slots_in_use`, `slots_total` | The worker's active set and its fixed limit of 4 |
| `queued_turns`, `in_progress_turns`, `oldest_queued_seconds` | `turns` by status; oldest `created_at` of queued Turns |
| `waiting_for_daemon` | Queued Turns whose Session has no connected daemon |
| `connected_daemons` | The daemon registry (`Registry.Devices()`) |
| `queue_wait_ms`, `series[].queue_wait_p95_ms` | `started_at − created_at` of Turns started in the bucket |
| `interrupted`, `unavailable` | Turns failed with `execution_interrupted`; requests refused with `execution_unavailable` in the range |
| `database.ping_ms` | A periodic ping (for example every 30 s) |
| `database.pool` | `pgxpool.Stat()` |
| `database.size_bytes` | `pg_database_size(current_database())` |
| `jobs` | Last result of the worker poll, the Runtime sampler sweep (listed/observed/failed, today only logged), history cleanup and audit cleanup |
| `process` | Go runtime memory statistics and goroutine count |

Series need a small in-memory ring buffer (or the existing history store) sampled
once per resolution step. HTTP request rate and latency are not requested now;
they would need a new middleware and can follow later.
