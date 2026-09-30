# Core operational metrics

`GET /core/v1/metrics?range=1h|6h|24h|7d` reports Core's own health: its process, execution queue and slots, PostgreSQL and background jobs. It requires the Core key ([Core administration API](admin-api.md)).

`range` is the only parameter, sent at most once; it defaults to `1h`. An empty, repeated or unsupported value, or any other parameter, returns 400 `invalid_request`. When Core has no metrics service, or cannot read it, the route returns 503 `core_metrics_unavailable`. When only some measurements fail, the response is still `200` with `service.status` set to `degraded` and each missing value set to null. The response never contains database or native error text, credentials, bodies, resource IDs or tenant labels.

## Time and missing data

`range` in the response has UTC RFC 3339 `start` and `end` and `resolution_seconds`. `end` is the most recent complete bucket boundary; the interval is `[start, end)`, so the current partial bucket is never included.

| Range | Bucket size | Buckets |
| --- | --- | --- |
| 1h | 60 seconds | 60 |
| 6h | 300 seconds | 72 |
| 24h | 900 seconds | 96 |
| 7d | 7200 seconds | 84 |

- Execution slots, connected daemons, the connection pool, Go heap and goroutines are read when the request arrives. Process CPU, RSS and limits, queue counts and database size come from a sample Core takes every 30 seconds; a sample older than 60 seconds is not reported as current.
- Each series bucket reports the highest value observed in it, not every intermediate peak. Missing observations and the process's partial first bucket are null.
- Samples and rejection counts live in memory for seven days, plus two hours of padding for bucket alignment. A restart loses them; Core does not backfill. Turn history comes from PostgreSQL and survives restarts.
- `execution.unavailable` is null when the interval starts before this process began observing, and zero for a fully observed interval without rejections.
- An empty queue has a count of zero and a null oldest age. No started Turns or no successful ping samples give null percentiles, not zero latency. Percentiles of periodic pings (p50, p95) are linearly interpolated; a request never triggers a ping.

## Fields

The response has `object: "core.metrics"`, `range`, `service`, `execution`, `database`, `jobs` and `process`. Every numeric value and `service.execution_owner` is nullable; each `series` always lists every complete bucket of the range.

| Field | Meaning |
| --- | --- |
| `service.status` | `running`, or `degraded` when a measurement or job fails, the latest sample is missing or stale, or execution ownership is unknown or Core has execution slots but does not hold the execution lease. A sandbox reset is reported by the [deployment](sandbox-deployment.md), not here |
| `service.revision` | The full source commit injected at build time; null for builds without one |
| `service.started_at` | When the process initialized |
| `service.execution_owner` | Whether this process holds the execution worker's database lease |
| `execution.slots_in_use`, `execution.slots_total` | Active Session reservations of the execution worker, and its capacity: [`core.execution_concurrency`](../../docs/configuration.md#settings), 4 by default. Environment input, Turns and file work share the slots; native Harness subprocesses are not counted. Without a worker both are 0 |
| `execution.queued_turns`, `execution.in_progress_turns` | Root Turns in those states, including Turns of deleted Sessions. Subagent Turns and input reserved for a preparing Environment are not counted |
| `execution.waiting_for_daemon` | Queued Turns whose Session's device is not connected; null without a gateway |
| `execution.oldest_queued_seconds` | Age of the oldest queued Turn, from its `created_at` |
| `execution.connected_daemons` | Runtime daemons connected to Core's gateway; null without a gateway |
| `execution.queue_wait_ms` | p50 and p95 of `started_at - created_at` for Turns started in the interval, by PostgreSQL `percentile_cont` |
| `execution.interrupted` | Failed Turns with error code `execution_interrupted`, by `completed_at` in the interval |
| `execution.unavailable` | HTTP responses sent with error code `execution_unavailable`, counted once each. Other 503 codes and errors after a stream started are not counted |
| `execution.series` | Per bucket: `queued`, `in_progress` and `queue_wait_p95_ms` |
| `database.ping_ms` | p50 and p95 of the periodic pool ping, including connection acquisition |
| `database.pool` | `in_use`, `idle` and `max` connections of the pool |
| `database.size_bytes` | `pg_database_size(current_database())`, not host disk usage |
| `database.series` | Per bucket: `ping_p95_ms` and `pool_in_use` |
| `process.memory_bytes`, `process.goroutines` | Go `runtime.MemStats.Alloc` (allocated heap, not RSS) and `runtime.NumGoroutine()` |
| `process.cpu_cores` | Increase in the process's user plus system CPU time divided by the elapsed sampling time (Linux `getrusage(RUSAGE_SELF)`), excluding subprocesses. Null for the first interval; a missing or reset counter, a nonpositive interval or a gap over 60 seconds restarts the baseline |
| `process.rss_bytes` | Linux `/proc/self/status` `VmRSS`, in bytes |
| `process.cpu_limit_cores` | The process's cgroup v2 `cpu.max` quota divided by its period, or `GOMAXPROCS` when the quota is `max` or the cgroup has no quota interface |
| `process.memory_limit_bytes` | The process's cgroup `memory.max`; null when it is `max` |
| `process.series` | Per bucket: `cpu_cores` and `rss_bytes` |

Core resolves its own cgroup, including nested and subtree mounts, and reports that cgroup's limits, not the host's or an ancestor's. Unreadable or malformed values are null. Non-Linux builds report null CPU, RSS and memory limit, with `GOMAXPROCS` as the CPU limit. Missing process measurements alone do not make the service `degraded`.

## Background jobs

`jobs` lists `scheduler`, `runtime_sampler`, `history_cleanup` and `audit_cleanup`. Each has `status` (`unknown` before its first run, `ok`, `failing`, or `stopped` when its loop is disabled or ended), `last_run_at` (when the last pass finished), `processed` and `failed`, both describing the last pass.

- The scheduler's `processed` counts the Turn and Environment work it selected in its last poll.
- The Runtime sampler's `processed` and `failed` count the targets its last sweep observed and failed to observe.
- A cleanup job's `processed` counts the rows it removed.
- For the scheduler and the cleanup jobs, a failed pass sets `processed` to null and `failed` to 1; `failed` never estimates lost rows or failed Turns.
