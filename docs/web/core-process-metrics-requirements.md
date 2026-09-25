# Core process CPU and memory: backend requirements

Status: implemented by the Core backend for Monitor > Core metrics, Process.
The page and typed client (`packages/agents-client/src/core-metrics.ts`) consume
the fields below. Unavailable measurements show as missing ("—", "No data").

## Why

Core is one `agents-api` process. Operators size and alert on its CPU and
resident memory, and today the response carries only the Go heap in use
(`process.memory_bytes`, `runtime.MemStats.Alloc`) and the goroutine count, read
when requested. The heap is neither what the operating system
charges the process nor what a container limit is compared against.

## Contract

Reuse `GET /core/v1/metrics?range=1h|6h|24h|7d`: add fields to
`process`; change nothing else. `memory_bytes` and `goroutines` keep their meaning.
Every figure Core cannot measure is `null`, never `0`.
Measured zero remains zero. New current values expire after 60 seconds without
a sample. Go heap and goroutine values continue to be read when requested.

```json
"process": {
  "memory_bytes": 190840832,
  "goroutines": 214,
  "cpu_cores": 0.35,
  "cpu_limit_cores": 2,
  "rss_bytes": 312475648,
  "memory_limit_bytes": 1073741824,
  "series": [ { "start": "RFC 3339", "cpu_cores": 0.41, "rss_bytes": 318767104 } ]
}
```

| Field | Meaning and source |
| --- | --- |
| `cpu_cores` | CPU the process used over the last sample interval (30 s), in cores: the increase in its user plus system CPU time (`getrusage(RUSAGE_SELF)` or `/proc/self/stat`) divided by the elapsed wall time. Null until the first interval completes |
| `cpu_limit_cores` | CPU available to the process: the cgroup v2 `cpu.max` quota divided by its period; without a quota, the CPUs the process may run on (`GOMAXPROCS`) |
| `rss_bytes` | Resident memory (`VmRSS` in `/proc/self/status`) |
| `memory_limit_bytes` | The cgroup v2 `memory.max`; null when it is `max` or unreadable |
| `series` | Per bucket of the response's range, the highest `cpu_cores` and `rss_bytes` observed, recorded by the existing 30-second sampler in the same in-process ring as the queue and database samples. Missing observations stay null; a restart does not backfill |

CPU uses Linux `getrusage(RUSAGE_SELF)`. Missing or invalid readings, counter
resets, nonpositive elapsed time and sampling gaps over 60 seconds reset its
baseline. The next valid interval supplies CPU again. RSS and limits are
independent readings, so a missing CPU interval does not hide measured memory.

Linux resolves cgroup v2 membership using `/proc/self/cgroup` and
`/proc/self/mountinfo`; it reads the process's own cgroup rather than assuming
the mount root. The actual cgroup v2 root has no quota interface and uses
`GOMAXPROCS`; an unreadable interface remains unknown. Limits describe that cgroup's configuration; ancestor-limit
discovery is outside this contract. Unreadable and malformed values stay null.
All procfs and cgroup reads have byte bounds. Non-Linux builds return null for
CPU usage, RSS and memory limit, with `GOMAXPROCS` as the CPU capacity fallback.
Unsupported process measurements do not mark execution or database health as
degraded.

All four existing ranges retain their bucket counts and exclude the active
partial bucket. The process's partial first bucket stays null. Series report
independent observed maxima, so they need not come from the same sample.

Not requested: whole-host CPU, memory or disk (Core may share its host; those
belong to host monitoring), per-request CPU profiles, or garbage-collector detail.
