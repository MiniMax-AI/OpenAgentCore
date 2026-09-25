# Core process CPU and memory: backend requirements

Status: requested by Core Web (Monitor › Core metrics, the Process section). The
page and the typed client (`packages/agents-client/src/core-metrics.ts`) read the
fields below; until Core serves them they show as missing ("—", "No data").

## Why

Core is one `agents-api` process. Operators size and alert on its CPU and
resident memory, and today the response carries only the Go heap in use
(`process.memory_bytes`, `runtime.MemStats.Alloc`) and the goroutine count, read
when requested, with no history. The heap is neither what the operating system
charges the process nor what a container limit is compared against.

## Contract

Reuse `GET /core/v1/admin/core-metrics?range=1h|6h|24h|7d`: add fields to
`process`; change nothing else. `memory_bytes` and `goroutines` keep their meaning.
Every figure Core cannot measure is `null`, never `0`.

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

Not requested: whole-host CPU, memory or disk (Core may share its host; those
belong to host monitoring), per-request CPU profiles, or garbage-collector detail.
