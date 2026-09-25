# Core operational metrics

`GET /core/v1/metrics?range=1h|6h|24h|7d` is a Core-key read. It implements the response shape agreed with Core Web PR #96
(`53dc9d646bc6e3cc2cd9b8bbb353bc53c3513ecf`). It changes neither public `/v1`
resources nor Agent or Sandbox metrics. Project API keys cannot call it.

Only `range` is accepted, once; omission defaults to `1h`. Empty, repeated,
unsupported or other query parameters return `400 invalid_request`. A missing
metrics service returns `503 core_metrics_unavailable`. Partial measurement
failures return the usual `200 core.metrics` envelope with `service.status` set
to `degraded` and unavailable fields set to JSON null. No database or native
error text, credentials, bodies, resource IDs or tenant labels are exposed.

## Time and missing data

The response's `range` uses UTC RFC 3339 boundaries. Its exclusive `end` is the
most recent complete bucket boundary. The included interval is `[start,end)`;
the current partial bucket is excluded from range aggregates and series.

| Range | Bucket size | Buckets |
| --- | --- | --- |
| 1h | 60 seconds | 60 |
| 6h | 300 seconds | 72 |
| 24h | 900 seconds | 96 |
| 7d | 7200 seconds | 84 |

Current gauges and range aggregates are intentionally different: current worker,
connection pool, Go heap and goroutine values are read when requested; process
CPU, RSS and limits, queue gauges, database size and deployment maintenance are
sampled every 30 seconds with bounded I/O.
Samples older than 60 seconds are not reported as current. Queue, running and
pool and process series report the highest **observed** value in each bucket, not a claim
that all intermediate peaks were captured. Missing observations and the process's
partial first bucket stay null. Successful periodic ping samples produce linear
interpolated p50/p95; there is no request-triggered ping.

A fixed-size in-process ring retains seven days of 30-second samples and
rejection counts, plus two hours of padding for complete bucket alignment. Restart loses those measurements: no synthetic backfill occurs.
The `execution.unavailable` count is null if the requested interval starts before
this process's observation began; an entirely observed interval with no rejections
is zero. PostgreSQL Turn history remains queryable across process restarts.
An empty queue has a measured count of zero but no oldest age. No started Turns
or successful ping samples means null percentiles, not zero latency.

## Sources

The envelope contains `object`, `range`, `service`, `execution`, `database`,
`jobs` and `process`, matching the typed client from PR #96. All numeric values
and `service.execution_owner` are nullable; lists of complete buckets are always
present.

- `service.revision` is a full source commit injected into `main.buildRevision`
  by the standalone builder's `-ldflags`. Manual builds without a valid revision
  report null. `started_at` records process initialization. `execution_owner`
  reflects the execution worker's existing lease checks, with unknown ownership
  represented as null. `maintenance` means the saved deployment is in maintenance;
  measurement or job failures take precedence as `degraded`.
- `execution.slots_in_use` is the worker's active Session reservation set. Its
  configured capacity is four; environment input, Turns and file work share it.
  It does not count native harness subprocesses. Worker-disabled installations
  have zero configured execution slots.
- `queued_turns` and `in_progress_turns` count root rows in `turns`, including
  operational state retained for deleted Sessions. Native Subagent views and
  pending Environment input reservations are not extra queued root Turns.
  `waiting_for_daemon` is the queued subset whose Session device binding is
  absent from the actual connected-device registry. `oldest_queued_seconds`
  measures the oldest queued row's `created_at`.
- `queue_wait_ms` uses `started_at - created_at`, in milliseconds, for Turns
  started in the interval. Each bucket uses its own started Turns, with PostgreSQL
  `percentile_cont`. `interrupted` counts failed Turns whose outcome error code is
  `execution_interrupted`, using `completed_at` in the interval.
- `unavailable` counts actual HTTP errors emitted with code
  `execution_unavailable`, once per rejected response. Other 503 codes and errors
  occurring after a stream has already started are not counted. The existing error
  writer reports the code; no response/request body capture is involved.
- `database.ping_ms` measures a periodic pool ping, including connection acquisition.
  Pool `in_use`, `idle` and `max` come from `pgxpool.Stat()`. `size_bytes` is
  `pg_database_size(current_database())`, not host disk usage. Failure to measure
  one value does not turn it into zero.
- `process.memory_bytes` is Go `runtime.MemStats.Alloc` (allocated heap bytes),
  not RSS or container memory. `goroutines` is `runtime.NumGoroutine()`.
- `process.cpu_cores` is the increase in this process's user plus system CPU
  time divided by elapsed sampling time. Linux uses `getrusage(RUSAGE_SELF)`;
  subprocess and whole-host CPU are excluded. The first interval is null.
  Missing/invalid counters, counter resets, nonpositive elapsed time and gaps
  longer than 60 seconds reset the baseline; they never manufacture a zero.
- `process.rss_bytes` is Linux `/proc/self/status` `VmRSS`, converted from KiB
  to bytes. `cpu_limit_cores` is the process's cgroup v2 `cpu.max` quota/period,
  or `GOMAXPROCS` when its quota is `max` or the actual cgroup root has no quota interface. `memory_limit_bytes` is that cgroup's
  finite `memory.max`; `max` is null. Membership and mount information resolve
  the process's cgroup, including nested and subtree mounts. These are that
  cgroup's configured limits, not whole-host metrics or ancestor-limit discovery.
  Unreadable or malformed values remain null. Non-Linux builds report null CPU,
  RSS and memory limit, with `GOMAXPROCS` as CPU capacity.
- `process.series` always contains the range's complete buckets with `start`,
  `cpu_cores` and `rss_bytes`. Each measurement uses its own observed maximum;
  missing samples and the partial first bucket stay null. These samples share
  the existing bounded ring and restart gaps. Unsupported process measurements
  do not by themselves change execution or database health.

## Background jobs

The four bounded IDs are `scheduler`, `runtime_sampler`, `history_cleanup` and
`audit_cleanup`. Each reports `status`, `last_run_at`, `processed` and `failed`.
A not-yet-observed run is unknown; a disabled or stopped loop is stopped. The
last-run time is the completion/observation of the last pass, not its next deadline.

Scheduler processed counts selected Turn/environment work in that poll. Runtime
sampling counts observed and failed targets from its existing sweep result.
Cleanup processed counts confirmed removed rows; an unsuccessful cleanup reports
unknown processed count. Cleanup/scheduler failure counts identify failed passes,
not guessed numbers of lost rows or failed Turns. The actual scheduling, sampling,
retention and execution lifecycles keep their existing owners and timing.

No additional telemetry database, monitoring server, model-provider probe,
message queue, object store, host disk measurement or scheduling mechanism is
introduced. Metrics cannot authorize execution or change resource ownership.
