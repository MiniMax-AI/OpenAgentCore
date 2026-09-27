# Runtime observability contract

This document defines the internal Runtime observation boundary. It does not add
an Agents API resource or change the pinned public protocol.

## Ownership and identity

Runtime telemetry is attributed to durable Core identity before it is sampled:

```text
managed:     tenant_id -> session_id -> environment_id -> runtime_allocation_id
self-hosted: tenant_id -> session_id -> environment_id -> device_id + connection_generation
none:        tenant_id -> session_id (no Session-owned Runtime instance)
```

The managed allocation's persisted `provider_key` selects exactly one configured
observation source. A provider must independently verify the allocation labels or
equivalent ownership data. A Session, daemon connection, process, container, and
native harness Session are different identities and must not be substituted for
one another.

The current implementation supports managed Docker, microsandbox and E2B allocations.
`self_hosted` and `none` are recognized but explicitly unsupported. A future self-hosted source must
use authenticated daemon telemetry fenced by the current connection generation.
Core must not attribute shared host statistics to an `environment:none` Session.

## Sample semantics

One sample contains:

- `observed_at`, the provider observation time;
- `started_at`, the current compute incarnation start time;
- cumulative CPU usage in seconds;
- configured CPU capacity in cores, when known;
- current memory usage in bytes;
- configured memory limit in bytes, when known;
- a provider-reported CPU utilization ratio, only for providers without
  cumulative CPU time (E2B); and
- current disk usage and capacity in bytes, only where the provider reports
  them (E2B). Only the administrator list exposes disk.

Measurements are optional. A present pointer with value zero means the provider
observed zero. An absent measurement means it was unavailable and must never be
rendered or aggregated as zero. A whole observation has one of three states:
`observed`, `unsupported`, or `unavailable`. Provider and permission failures are
errors, not ordinary unavailability.

Managed observations also expose a provider-neutral `lifecycle_state` derived
from Core's allocation and compute lifecycle: `active`, `sleeping`,
`transitioning`, `pending`, or `stopped`. Non-managed modes return `null`.
This field is current control-plane state; it is not inferred from a failed
provider sample.

Docker reports cumulative cgroup CPU time and current cgroup memory usage. CPU and
memory capacity come from the inspected container configuration. Inspect and Stats
are read-only; observation must not renew, restart, create, or stop the container.
The Docker `StartedAt` value defines current compute uptime and resets after a
container restart.

Microsandbox reports cumulative vCPU time, current guest memory usage, its effective
memory limit, and compute uptime through the pinned SDK's point-in-time metrics
operation. Core invokes that SDK only through the existing one-shot Linux helper.
The helper first verifies the allocation labels and exact persisted compute
generation/ID, then reads metrics under the allocation lock. Restored generations
therefore reset compute uptime without resetting allocation age. Paused, stopped,
suspended, metrics-disabled, and no-current-sample states are unavailable, never
observed zero. The SDK also supplies instantaneous CPU percent, host RSS, disk,
network, and overlay values; those are intentionally outside this public sample
until their cross-provider semantics and API fields are designed.
Legacy suspension-disabled allocations without a persisted exact compute receipt
are also unavailable. A deterministic sandbox name is not an incarnation identity
and is never used as a sampling fallback.

E2B reports only the latest point of a current CPU percentage, memory and disk,
through `GET /sandboxes/metrics?sandbox_ids=...`. One helper request reads a whole
page: at most 100 allocations, one metrics request and, concurrently, one listing
of this installation's running sandboxes by their allocation labels. The helper
takes each sandbox ID from its private receipt without the allocation lock; the
listing confirms that exactly that sandbox is running with the allocation's labels
and supplies its `started_at`; it stops paging once every requested sandbox has
been listed, so duplicate-label detection covers only the pages read. A listed
sandbox without a metrics point or with a malformed point, an ambiguous listing
or an E2B API failure (including a rejected key) is unavailable; a malformed
point affects only its own row. A sandbox absent from the running listing is
`runtime_not_running`. Observation
never connects to, renews or changes a sandbox and never writes receipts.

The E2B mapping is: `cpuUsedPct / 100` to `cpu.utilization_ratio`, `cpuCount` to
`cpu.capacity_cores`, `memUsed` and `memTotal` to `memory.usage_bytes` and
`memory.limit_bytes`, and `diskUsed` and `diskTotal` to the administrator
`disk.usage_bytes` and `disk.limit_bytes`. E2B has no cumulative CPU time, so
`usage_seconds_total` and `usage_cores` stay null. A template whose envd predates
E2B disk metrics reports no disk capacity; disk is then null. `observed_at` is the
point's E2B timestamp; a point up to 30 seconds ahead of Core's clock is recorded
at Core's time, and a larger lead is unavailable. Docker disk is null;
microsandbox disk is null until its disk semantics are designed.

## Duration boundaries

These durations answer different questions and must remain separate:

- allocation age: `runtime_allocations.created_at` through `released_at` or now;
- compute uptime: provider `started_at` through `observed_at`; and
- busy Turn duration: `turns.started_at` through `completed_at` or now.

This phase supplies compute uptime evidence and retains the existing durable
allocation and Turn timestamps. It does not infer idle time. CPU quietness,
heartbeat age, connection status, and `kept_at` are not authoritative idle state.

Web projects active Runtime state differently by scope. The Dashboard shows one
summed series of distinct allocation identities: live snapshots count
`lifecycle_state: active`, while retained buckets count successfully observed
allocations because lifecycle state is not retained yet. The single-Session view
collapses the same value to `1` or `0`. Missing or unavailable retained values are
currently rendered as zero, so this presentation intentionally does not yet
distinguish sleeping from collection failure.

Future automatic suspension requires a separate durable control model, including
an activity revision and timestamps such as `idle_since` and
`shutdown_requested_at`. Metrics, an in-memory cache, or a monitoring backend must
not become the lifecycle authority.

## Retained history and optional export

Periodic Runtime observations are persisted asynchronously in the existing Core
PostgreSQL database. The execution owner samples every 30 seconds by default,
using bounded pages, concurrency and source deadlines. Collection never wakes or
mutates compute. The worker lease is checked during the sweep and before each
handoff. Only periodic samples populate durable history; current API reads cannot
inflate cadence coverage. Retention is seven days; history reads span at most
24 hours and have explicit input and output limits.

`OAC_HISTORY_SETTINGS_FILE` optionally changes sampling and adds OTLP/HTTP
export. The local database and external exporter have independent bounded queues.
No Collector is required for the Dashboard. Failures and queue saturation remain
missing observations rather than fabricated zeroes or failed executions. Transport
credentials stay server-only; native identifiers, receipts, raw errors, paths and
credentials are excluded from observations.

The internal `runtimehistory` boundary validates Core scope, bucket coverage,
nullability, time bounds and point limits. One chart series represents an allocation;
CPU deltas reset across compute incarnations or counter regressions. E2B samples
store their reported utilization ratio instead, and a bucket holds the mean of the
ratios sampled in it. Disk is not retained in history. Core's
measured Session usage (recorded root Turn snapshots, active Turns included)
supplies independently sampled token counters. History queries survive
Core restart and browser reload without replaying execution.

See the [design](runtime-observability-design.md),
[current API](runtime-observability-api.md), [history API](runtime-history-api.md)
and [configuration](../../services/agents-api/runtime-history/README.md).
Additional provider telemetry and idle-policy authority remain separate work.

The OTLP resource identifies Core with `service.name=oac-core` and
`service.namespace=oac`. Metric names retain the `agents.*` namespace.
