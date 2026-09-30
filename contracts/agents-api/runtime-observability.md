# Runtime observability

This is the contributor contract for how Core observes Runtimes and keeps their history. The routes and response fields are in the [Runtime telemetry API](runtime-observability-api.md). The code lives in `services/core/internal/runtimeobs` (resolution, sources, sampler and export), `internal/runtimehistory` (history queries and the PostgreSQL store) and `internal/runtimeobs/otlpexporter`.

Observations are telemetry. They never create, renew, wake, restore or stop compute, never touch Session activity, and never decide idle time, suspension, admission or execution outcomes.

## Identity and source selection

Core attributes every observation to durable Core identity before it reads a provider:

```text
managed:     tenant_id -> session_id -> environment_id -> runtime_allocation_id
self-hosted: tenant_id -> session_id -> environment_id -> device_id + connection_generation
none:        tenant_id -> session_id (no Session-owned Runtime instance)
```

The resolver (`internal/runtimeobs/storeresolver`) reads the Session, its Environment, the current allocation and the Session's measured usage from the store. A Session, daemon connection, process, container and native Harness Session are different identities and never stand in for one another.

Managed Docker, microsandbox and E2B allocations are observed. `none` and `self_hosted` Sessions are `unsupported`; Core never attributes shared host statistics to an `environment:none` Session.

The allocation's persisted `provider_key` selects exactly one configured source, which verifies the allocation's labels or equivalent ownership data before it returns values. Before any provider read, the allocation state decides some rows: `creating` or no allocation yet gives `allocation_pending`, `cleanup_pending` or `released` gives `runtime_not_running`, and a provider key without a source gives `source_not_configured`. A provider read that exceeds its deadline gives `sample_timeout`, a not-running result `runtime_not_running`, and an unavailable result `sample_unavailable`. Any other error, an ownership mismatch or an invalid sample fails the read.

A source implements `Observe` and declares `ObserveBatch` in its provider operations. When `ObserveBatch` is declared supported, one call reads up to 100 targets of that provider; when it is declared unsupported, Core reads each target with `Observe`. A failed batch read is never retried target by target. The [Sandbox Provider guide](../../docs/sandbox-provider.md) describes the operation declarations.

## Sample semantics

A sample carries:

- `observed_at`, the provider's observation time, and `started_at`, the start of the current compute incarnation;
- cumulative CPU seconds and the configured CPU capacity in cores;
- a provider-reported CPU utilization ratio, only from providers without cumulative CPU time (E2B);
- current memory usage and the memory limit in bytes;
- current disk usage and capacity in bytes, only where the provider reports them (E2B).

Every measurement is optional. A present zero is an observed zero; an absent value is unavailable and is never shown or aggregated as zero. Core rejects a sample whose `observed_at` is later than its own clock, whose `started_at` is later than `observed_at`, or whose values are negative, non-finite, a zero capacity or limit, or beyond the JSON safe-integer range.

`lifecycle_state` is derived from the allocation state and compute phase in Core's records, never from a sample: no allocation or `creating` is `pending`; `running` with compute phase `running` or `disabled` is `active`, `suspended` is `sleeping`, and `quiescing`, `suspending`, `restoring` or `waking` is `transitioning`; `cleanup_pending` and `released` are `stopped`.

## Provider mapping

### Docker

One non-streaming Inspect and Stats read of the owned container. Cumulative CPU time comes from the cgroup counter, memory usage from the current cgroup usage, and CPU and memory capacity from the container's configured limits. The container's `StartedAt` is the incarnation start, so a container restart resets compute uptime. A missing or stopped container is `runtime_not_running`. Disk is null.

### microsandbox

A suspended allocation is `runtime_not_running` without calling the helper. Otherwise Core sends the helper a `metrics` request for the exact compute recorded on the allocation. Under the allocation lock, the helper checks the sandbox's identity through the pinned SDK, runs the pinned `msb metrics <name> --format json` CLI read, and checks the identity again; the sandbox must be running or draining. The [microsandbox helper](../../services/core/tools/microsandbox-provider/README.md) owns that request.

Cumulative vCPU time, guest memory usage and the effective memory limit come from that one CLI sample. CPU capacity is the deployment's configured CPU count. `started_at` is the sample's own timestamp minus its millisecond uptime, never rounded uptime subtracted from a later clock reading, so a restored generation restarts compute uptime while the allocation age continues. Instantaneous CPU percent, host memory, disk and network values are not used, and disk is null. A suspension-disabled allocation without a recorded compute identity is `sample_unavailable`, and any other allocation without one fails the read; a deterministic sandbox name is never used instead.

### E2B

One helper `observe` request reads a page of at most 100 allocations: E2B's batch metrics for the sandboxes named in the private receipts, and a labelled listing of the installation's running sandboxes that confirms each one. The [E2B helper](../../services/core/tools/e2b-provider/README.md) owns that request. It never connects to, renews or changes a sandbox and writes no receipts.

| E2B value | Sample field |
| --- | --- |
| `cpuUsedPct / 100` | CPU utilization ratio |
| `cpuCount` | CPU capacity |
| `memUsed`, `memTotal` | Memory usage and limit |
| `diskUsed`, `diskTotal` | Disk usage and capacity, kept only when both are present and the total is nonzero |

E2B reports no cumulative CPU time, so CPU seconds stay null. `observed_at` is E2B's point time; a point up to 30 seconds ahead of Core's clock is recorded at Core's time, and a larger lead is `sample_unavailable`. A sandbox missing from the running listing is `runtime_not_running`. A missing or malformed point, an ambiguous listing and an E2B API failure, a rejected key included, are `sample_unavailable`; a malformed point affects only its own row.

## Read budgets

A current list read handles one page of up to 100 Sessions (default 20) with at most eight concurrent provider reads. Each provider read has two seconds, a batch read at least five, and the whole list request ten; beyond that the list returns 503. A single-Session read has two seconds. No provider call is retried within a request, and Core keeps no observation cache.

## Durations

These durations answer different questions and stay separate:

- allocation age: `runtime_allocations.created_at` to `released_at`, or now;
- compute uptime: the sample's `started_at` to `observed_at`;
- busy Turn duration: `turns.started_at` to `completed_at`, or now.

CPU quietness, heartbeat age, connection state and keepalive time are not idle time.

## Retained history and optional export

### Periodic sampling

Periodic collection runs only with the execution worker (Core started with `OAC_PUBLIC_URL`; see the [Core environment](../../docs/configuration.md#appendix-core-environment-without-the-installer)) and under the worker's database lease. A Core without it stores no history and answers every history read with 503; current reads work on either.

The sampler sweeps once at startup and again each sampling interval after the previous sweep ends. A sweep is a keyset scan, in Session ID order, of the Sessions that are not deleted, are `openai_hosted` and have no released allocation. It reads pages of 32 Sessions through the same resolver and sources as current reads, with eight concurrent reads and two seconds per source. The sampler checks the lease before each page and every 100 ms during a sweep, cancels in-flight reads when ownership is lost, and checks it again before handing each record to export. A failed row does not stop the sweep, and an incomplete sweep is repeated at the next interval.

Every observation, current or periodic, is marked with its collection source, `on_read` or `periodic`, and handed to each exporter's bounded queue. A full queue drops the record, which becomes a missing sample, never a zero. The PostgreSQL history store and the optional OTLP exporter have independent queues, so an exporter outage cannot delay local history or execution. The [`core.runtime_history` settings](../../docs/configuration.md#settings) set the interval, queue capacity, timeout and OTLP destination.

### Stored history

The PostgreSQL store keeps only periodic `openai_hosted` records, so API reads cannot inflate coverage. Each goes into one `runtime_history_samples` row keyed by tenant, Session, Environment and resolution time: the allocation, provider type, status, observation and start times, CPU seconds, capacity and utilization ratio, memory usage and limit, and the measured token counters. Disk is not stored. A sample without `started_at` keeps its coverage and drops its resource values, since they cannot be tied to an incarnation.

The history service resolves the Project, Session and Environment before it queries; the query always carries that scope and bounded times, never provider identity. The store keeps seven days. A read covers at most 24 hours, reads at most 20,000 raw samples, starts two sampling intervals before the range to find CPU baselines, and returns at most 1,000 buckets per array, 64 series and 10,000 points in total. Results outside the requested scope, range or limits fail the read. The API's [Series](runtime-observability-api.md#series) section describes the aggregation.

`runtimehistory.Capabilities` states the collection mode, interval, seven-day retention, minimum bucket width (30 seconds or the interval, whichever is longer), 24-hour range and point limits; the history route answers 503 unless they are valid and periodic.

A cleanup loop runs every minute, even without active Runtimes. Each pass has at most two seconds and deletes expired Runtime and node host rows in batches of 256 per table, at most 16 batches. Reads never return rows older than the retention.

### Node host history

After each sweep, within two seconds and after a lease check, Core copies every fresh node heartbeat into `node_host_history_samples`: the host CPU utilization, used memory and available disk, keyed by node and the heartbeat's own time, so repeated sweeps add nothing. A heartbeat is fresh when its node is not removed, is connected under the current owner epoch, was seen within 45 seconds and reported a host time within the last 45 seconds and not in the future. The same seven-day cleanup applies. Node host history is telemetry, never scheduling or capacity truth, and reads never sample or backfill it.

### Token usage

Each observation carries the Session's measured usage from `MeasuredSessionUsage`: the sum of every recorded root Turn snapshot, active Turns included. It is not the public Session usage rule, and Core never derives tokens from provider counters, context occupancy or costs.

### OTLP export

With an OTLP endpoint configured, Core exports every record, both `on_read` and `periodic`, as OTLP/HTTP metrics. The resource has `service.name=oac-core` and `service.namespace=oac`.

| Instrument | Aggregation | Source |
| --- | --- | --- |
| `agents.runtime.cpu.usage` | Monotonic cumulative sum, seconds | Cumulative CPU counter |
| `agents.runtime.cpu.capacity` | Gauge, cores | Configured CPU capacity |
| `agents.runtime.cpu.utilization` | Gauge, ratio | Provider-reported CPU share (E2B) |
| `agents.runtime.memory.usage` | Gauge, bytes | Memory usage |
| `agents.runtime.memory.limit` | Gauge, bytes | Memory limit |
| `agents.session.tokens.input` | Gauge, tokens | Measured Session input tokens |
| `agents.session.tokens.output` | Gauge, tokens | Measured Session output tokens |
| `agents.runtime.sample` | Monotonic delta sum | One per validated result, unavailable and unsupported included |
| `agents.runtime.sample.duration` | Delta histogram, seconds | Provider read duration; a batch read counts once |

CPU and memory points are exported only when the sample has `started_at`; a missing measurement produces no point. Attributes are `agents.tenant.id`, `agents.session.id`, `agents.environment.id`, `agents.runtime.allocation.id`, `agents.runtime.mode`, `agents.runtime.provider.type`, `agents.runtime.status`, `agents.runtime.reason`, `agents.runtime.collection.source` and nanosecond `agents.runtime.resolved_at_unix_nano`, `agents.runtime.observed_at_unix_nano` and `agents.runtime.compute.started_at_unix_nano`. The nanosecond times keep records joinable when a backend stores event time at lower precision. Provider keys, receipts, native identifiers, raw errors, paths and credentials are never attributes.

Web reads history only through Core; neither a Collector nor another metrics store is needed for its charts.
