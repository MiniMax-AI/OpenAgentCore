# Administrator node host observations

`GET /core/v1/sandbox/nodes/{node_id}?range=1h|6h|24h` requires the Core key.
Core Web forwards it with the Core key after console sign-in; Project, enrollment
and node credentials do not authorize this read.
The omitted range defaults to `1h`. Invalid, repeated or unknown query parameters
return the existing invalid-input response; missing or removed nodes return 404.

The response contains the existing node fields plus these two objects. The node
list response is unchanged. This endpoint does not alter enrollment, administrator
capacity approval, scheduling or any public Agents API contract.

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

Every unavailable measurement is `null`, including unobserved timestamps. `host`
is the last received observation; an offline node retains its original timestamp
and last-known measurements. Clients must use `online` and `observed_at` when
presenting freshness. Reading the endpoint never samples or writes history.

On Linux, CPU utilization is the increase in aggregate busy `/proc/stat` ticks
divided by total ticks between heartbeats, across the whole visible host. Idle
and I/O-wait ticks are not busy; guest counters are not counted twice. The first
observation and a reset or unavailable baseline have null utilization. This is
not node-process CPU or the sum of sandbox utilization. Total memory and available
memory come from `MemTotal` and `MemAvailable`. Effective CPU capacity accounts for
observable process affinity and cgroup limits; it is null if those limits cannot
be established, rather than an invented scheduling limit. Available disk refers
to the node's existing state filesystem, not a sandbox quota. The node should run
on the machine being monitored; namespace visibility limits what it can observe.

The existing Runtime sampling sweep copies fresh, authenticated node heartbeats
into `node_host_history_samples` in the same PostgreSQL database. It uses the
Runtime history cadence (30 seconds by default) and seven-day retention cleanup.
A node observation is keyed by node and its original timestamp, so repeated sweeps
do not manufacture additional observations. Disconnected, stale, future-dated or
removed observations are not copied. The sampler is best effort with a bounded
query timeout; failures do not change execution or admission authority.

Ranges use complete UTC buckets: `1h` has 60-second buckets, `6h` has 300-second
buckets and `24h` has 900-second buckets. Each response includes every bucket in
the range. CPU and memory are maxima of recorded observations; used memory is
`total_memory_bytes - available_memory_bytes` from the same observation. Disk is
the minimum recorded available space. Missing measurements and offline buckets
stay null, independently for each metric. The active partial bucket is excluded.
No interpolation, offline backfill or whole-host process inventory is performed.

History survives Core and node restarts until normal retention expiry. Core
process history remains a separate in-memory series and resets when Core restarts.
Neither series contains credentials, model data, paths or user content.
