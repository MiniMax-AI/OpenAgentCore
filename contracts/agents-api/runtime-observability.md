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

## Optional history export boundary

An optional server-only OTLP/HTTP exporter can forward validated observation
results to an operator Collector when `AGENTS_API_RUNTIME_HISTORY_FILE` is set.
It is disabled by default and does not change the current API result, execution
ownership, or lifecycle authority. The file may contain endpoint authorization
headers and is never returned to Web. Provider receipts, native identifiers, raw
errors, paths, and credentials are excluded from metric attributes. CPU,
capacity, and memory points require the provider-qualified compute `started_at`
fence; unfenced observations export coverage and read duration only.

The server-only file may also enable a bounded periodic cadence. Only the Core
service holding the execution database lease runs that deployment-wide sampler;
it scans nondeleted managed Sessions with bounded pages and concurrency, gives
each provider read an independent deadline, monitors lease ownership throughout
the sweep, rechecks ownership before export, and never mutates Runtime state.
Exports distinguish `periodic` samples from `on_read` samples.

The Collector, high-cardinality history backend, server-side history query
adapter, retention policy, and durable Web ranges remain separate optional
capabilities in the [full design](runtime-observability-design.md).

## First-phase boundary

The current-snapshot implementation adds no migration, metrics backend, token
duplication, Kubernetes/E2B source, or telemetry-driven lifecycle action. The
internal source interface admits Docker and microsandbox without changing Session
attribution or the existing sandbox lifecycle interface.

The current API and browser-local Web live window are documented in the
[full design](runtime-observability-design.md) and the
[public extension](runtime-observability-api.md). History queries, additional
providers, self-hosted telemetry, and idle-policy authority remain later phases.
