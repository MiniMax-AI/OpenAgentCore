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

The current implementation supports managed Docker and microsandbox allocations.
`self_hosted` and `none` are recognized but explicitly unsupported. A future self-hosted source must
use authenticated daemon telemetry fenced by the current connection generation.
Core must not attribute shared host statistics to an `environment:none` Session.

## Sample semantics

One sample contains:

- `observed_at`, the provider observation time;
- `started_at`, the current compute incarnation start time;
- cumulative CPU usage in seconds;
- configured CPU capacity in cores, when known;
- current memory usage in bytes; and
- configured memory limit in bytes, when known.

Measurements are optional. A present pointer with value zero means the provider
observed zero. An absent measurement means it was unavailable and must never be
rendered or aggregated as zero. A whole observation has one of three states:
`observed`, `unsupported`, or `unavailable`. Provider and permission failures are
errors, not ordinary unavailability.

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

## Duration boundaries

These durations answer different questions and must remain separate:

- allocation age: `runtime_allocations.created_at` through `released_at` or now;
- compute uptime: provider `started_at` through `observed_at`; and
- busy Turn duration: `turns.started_at` through `completed_at` or now.

This phase supplies compute uptime evidence and retains the existing durable
allocation and Turn timestamps. It does not infer idle time. CPU quietness,
heartbeat age, connection status, and `kept_at` are not authoritative idle state.

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
inflate cadence coverage. Retention is seven days; public queries span at most
24 hours and have explicit input and output limits.

`AGENTS_API_RUNTIME_HISTORY_FILE` optionally changes sampling and adds OTLP/HTTP
export. The local database and external exporter have independent bounded queues.
No Collector is required for the Dashboard. Failures and queue saturation remain
missing observations rather than fabricated zeroes or failed executions. Transport
credentials stay server-only; native identifiers, receipts, raw errors, paths and
credentials are excluded from observations.

The internal `runtimehistory` boundary validates Core scope, bucket coverage,
nullability, time bounds and point limits. One chart series represents an allocation;
CPU deltas reset across compute incarnations or counter regressions. Core's
measured Session usage (recorded root Turn snapshots, active Turns included)
supplies independently sampled token counters. History queries survive
Core restart and browser reload without replaying execution.

See the [design](runtime-observability-design.md),
[current API](runtime-observability-api.md), [history API](runtime-history-api.md)
and [configuration](../../services/agents-api/runtime-history/README.md).
Additional provider telemetry and idle-policy authority remain separate work.
