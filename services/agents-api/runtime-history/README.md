# Runtime history

Core stores recent Runtime observations in its existing PostgreSQL database.
No additional database or Collector is required. Apply Core migrations normally;
the server enables 30-second periodic sampling when an execution worker is present.
Only the execution lease owner samples, using the same provider-neutral current
observation sources. Observation never changes Runtime lifecycle.

History retains seven days and queries at most 24 hours. Writes use a bounded
asynchronous queue; failures or overflow create gaps, never execution failures.
CPU and memory unknowns remain null. CPU counter deltas never bridge compute
restarts or counter resets. Providers without cumulative CPU time (E2B) store
their reported utilization ratio, and a bucket holds the mean of those ratios. Token snapshots come from canonical Session Usage;
the history table does not become billing or execution authority.

Tenant, Session and Environment scope are mandatory on reads and writes. The
schema stores sanitized measurements and Core identifiers, not credentials,
provider receipts, native process identifiers, paths or raw provider errors.
Nanosecond identity timestamps are lossless integers. Retention cleanup runs in
bounded batches once per minute even when there are no active Runtimes. Queries
exclude expired records before physical cleanup completes.

## Optional configuration

Set `OAC_HISTORY_SETTINGS_FILE` to an absolute server-only JSON file to change
sampling or export to an existing OTLP receiver. Sampling-only example:

```json
{"sample_interval_seconds": 60}
```

Sampling accepts 5–300 seconds; omitted or zero selects 30. Queue capacity defaults
to 256 (maximum 4096); write/export timeout defaults to two seconds (maximum 30).
A server without an execution worker advertises on-read collection and does not
claim periodic coverage. Retained queries remain available through Core.

Optional external export:

```json
{
  "transport": "otlp_http",
  "endpoint": "https://collector.example.com/v1/metrics",
  "headers": {"Authorization": "Bearer operator-secret"},
  "sample_interval_seconds": 30,
  "queue_capacity": 256,
  "timeout_seconds": 2
}
```

HTTP requires explicit `insecure: true`. Transport credentials stay server-only;
the browser uses only Core. External export has a separate bounded queue, so its
outage cannot starve local history. Do not commit this private configuration.

Apply the standard `make check` gate with a dedicated PostgreSQL database. Real
Runtime acceptance must include provider observations during real model execution,
tenant isolation and retained history across Core restart; injected rows alone do
not qualify provider sampling.
