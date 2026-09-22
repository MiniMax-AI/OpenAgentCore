# Runtime observability and Dashboard design

Status: Phase 1 provider abstraction/Docker sampling and Phase 2 current-snapshot
API/client contract are implemented. Core Web integration, history backend,
additional providers, and lifecycle automation described below are not implemented.

## 1. Problem statement

Operators need one Dashboard that answers four separate questions without
confusing their sources of truth:

1. Which Runtime instances currently belong to which tenant, Session, and
   Environment?
2. What compute is allocated and what is it consuming now?
3. How long has allocation, compute, and model work been active?
4. How many model tokens have been reported for the corresponding Sessions?

The design must work across managed Docker now and later managed Kubernetes,
E2B, and authenticated self-hosted Runtime deployments. Metrics are operational
evidence. They must not become execution or lifecycle authority.

## 2. Goals

- Resolve every sample through durable Core identity before provider access.
- Keep provider-specific collection behind one source interface.
- Preserve observed zero, unavailable measurements, and unsupported modes as
  different states.
- Provide a bounded read-only API suitable for Core Web and other operators.
- Let Web combine Runtime observations with existing Session and Turn usage
  without copying execution truth into the browser.
- Keep current snapshots independent from an optional history backend.
- Define a safe path to future idle shutdown without implementing it implicitly.

## 3. Non-goals

- Redefining the pinned OpenAI Agents resources.
- Adding product users, organizations, billing, or authorization tables to Core.
- Treating a Session, daemon socket, container, pod, native harness Session, or
  Turn as the same identity.
- Estimating missing CPU, memory, token, or duration values.
- Using telemetry, heartbeat age, low CPU, or Prometheus state to stop compute.
- Adding lifecycle actions to the first Dashboard release.
- Storing time-series samples in PostgreSQL.

## 4. Source-of-truth model

| Concern | Authority | Notes |
| --- | --- | --- |
| Tenant and Session ownership | Core database | Every public read is tenant-scoped. |
| Environment placement | Session configuration and Environment row | `none`, `self_hosted`, or `openai_hosted`. |
| Observation resource identity | Session ID | One current observation resource exists per tenant-owned Session. |
| Managed Runtime identity | `runtime_allocations` | Allocation and provider key identify the compute incarnation. |
| Self-hosted Runtime identity | Environment connection generation | Future telemetry must be generation-fenced. |
| Container/pod resource values | Selected provider source | Read-only, point-in-time evidence. |
| Turn state and busy duration | Core Turns | Never inferred from CPU. |
| Token usage | Existing Session/Turn usage | Missing native usage remains unknown. |
| Historical resource series | Optional telemetry backend | Not execution or lifecycle authority. |
| Idle shutdown decision | Future durable Core control state | Separate design and migration. |

## 5. Identity chain

```text
managed
tenant_id -> session_id -> environment_id -> runtime_allocation_id
          -> provider_key -> provider-owned container/pod/instance

self-hosted (future)
tenant_id -> session_id -> environment_id
          -> device_id + connection_generation -> authenticated Runtime report

none
tenant_id -> session_id
          -> no Session-owned Runtime instance
```

Provider-native identifiers are never accepted from browser input. The resolver
starts from the authorized tenant and Session, loads the committed Environment and
allocation, and only then selects the configured source by persisted provider key.
The provider independently verifies its labels or equivalent ownership metadata.

## 6. Component architecture

```mermaid
flowchart LR
    Web[Core Web Dashboard] --> Client[packages/agents-client]
    Client --> API[Agents API read handlers]
    API --> Service[runtimeobs.Service]
    Service --> Resolver[durable identity resolver]
    Resolver --> DB[(Core PostgreSQL)]
    Service --> Registry[provider source registry]
    Registry --> Docker[Docker Inspect and one-shot Stats]
    Registry -. future .-> K8s[Kubernetes Metrics API or cAdvisor]
    Registry -. future .-> E2B[E2B metrics adapter]
    Registry -. future .-> Self[authenticated daemon telemetry]
    Service -. optional export .-> Telemetry[OTLP or Prometheus pipeline]
    Telemetry -. future history reads .-> History[operator history adapter]
```

### 6.1 `runtimeobs`

Owns provider-neutral identity, mode resolution, source selection, sample
validation, and observation status. It must not import provider SDKs or mutate
Runtime lifecycle.

### 6.2 Provider sources

Each source receives a fully resolved target and returns one normalized sample.
A source must verify target ownership, make only bounded read calls, preserve
missing fields, return cumulative CPU seconds, and never create, renew, restart,
pause, or stop compute.

Docker uses Inspect followed by non-streaming one-shot Stats. Kubernetes should
retain pod UID, container identity, and restart boundaries. E2B must use an
API-supported instance identity rather than display names. Self-hosted metrics
require authenticated daemon messages fenced by the current connection generation.

### 6.3 API composition

The API resolves durable rows first, samples sources with bounded concurrency,
maps only ordinary absence/timeouts to safe unavailable reasons, and fails closed
on ownership or integrity errors. It returns current observations only.

### 6.4 Web composition

Web loads the complete paginated Runtime observation collection before publishing
a new Dashboard snapshot. The collection follows the same Session creation-time
and ID keyset as the Session list, so a Runtime incarnation change cannot invalidate
pagination. It separately uses existing Session/Turn reads for status and tokens
and joins only by exact Session identity. Before publication, the set of Session
IDs from both complete traversals must be identical. Concurrent Session creation
or deletion can make the sets differ because the APIs have no shared snapshot
token; Web then discards the candidate, marks the refresh incomplete, and keeps
the previous successful snapshot visibly stale.

The browser enforces a configured refresh budget for total pages, targets, and
elapsed time. Exhausting that budget is an incomplete refresh: Web retains the
previous complete snapshot and does not relabel partial aggregates as tenant-wide.

## 7. Normalized sample

```go
type Sample struct {
    ObservedAt time.Time
    StartedAt  *time.Time

    CPUUsageSecondsTotal *float64
    CPUCapacityCores      *float64
    MemoryUsageBytes      *uint64
    MemoryLimitBytes      *uint64
}
```

Pointer presence is semantic. `0` means observed zero; `nil` means unavailable.
CPU percentage is derived from the delta between two cumulative samples and their
observation times. A single sample cannot truthfully supply CPU percentage.

The API projection may additionally expose `usage_cores` and `utilization_ratio`
only when the service has two ordered samples for the same Runtime incarnation.
A future process-local observation cache may keep the previous cumulative value
for this calculation. Phase 2 intentionally leaves both derived fields null
because it has only one provider sample per request. Cache loss must make the
derived fields temporarily null; it must never change the cumulative source
measurement or lifecycle state.

## 8. Duration semantics

| UI label | Calculation | Meaning |
| --- | --- | --- |
| Allocation age | allocation `created_at` to `released_at` or now | Age of Core's allocation record. |
| Compute uptime | provider `started_at` to sample `observed_at` | Age of the current compute incarnation. |
| Busy duration | Turn `started_at` to `completed_at` or now | Time model work has been active. |
| Idle duration | future durable `idle_since` | Not available in the current design. |

Container restart resets compute uptime but not allocation age. Dashboard labels
must not collapse these values into one generic Runtime duration.

## 9. Collection behavior

### 9.1 Current snapshot path

- List one current target context for each tenant-owned Session in the same stable
  Session creation-time and ID order used by the Session list. A released managed
  allocation remains attributable but reports `runtime_not_running`; `none` and
  unsupported `self_hosted` remain explicit rows rather than disappearing.
- Default page size 20, maximum 100.
- Sample at most eight providers concurrently.
- Default per-source budget two seconds and whole-request budget ten seconds.
- Do not retry a source call inside the HTTP request.
- An optional process-local singleflight/cache may coalesce identical reads for up
  to five seconds and retain the previous cumulative sample for CPU-rate
  calculation. It is an optimization only and may be lost on restart.
- Do not write samples to the Core database.

### 9.2 Error classification

| Condition | API result |
| --- | --- |
| Mode `none` or unsupported `self_hosted` | Row status `unsupported`. |
| Managed allocation not created yet | `unavailable`, reason `allocation_pending`. |
| Owned Runtime absent or stopped | `unavailable`, reason `runtime_not_running`. |
| Source not configured | `unavailable`, reason `source_not_configured`. |
| Source deadline | `unavailable`, reason `sample_timeout`. |
| Ownership mismatch or invalid durable identity | Fail the request and log a sanitized integrity error. |
| Database/authentication failure | Existing safe API error mapping. |

Raw Docker, Kubernetes, E2B, daemon, host, credential, or network diagnostics are
never returned to the browser.

## 10. Historical metrics

Current API reads and history are separate capabilities. The initial API does not
provide charts over time. A later operator-configured adapter may query an
OTLP/Prometheus-compatible backend. Core must not make that backend mandatory for
Session execution or current snapshot reads.

Recommended instruments are:

- `agents.runtime.cpu.usage` cumulative seconds;
- `agents.runtime.cpu.capacity` cores;
- `agents.runtime.memory.usage` bytes;
- `agents.runtime.memory.limit` bytes;
- `agents.runtime.sample` success/unavailable count; and
- `agents.runtime.sample.duration` seconds.

Provider type, Runtime mode, and coarse status are safe low-cardinality labels.
High-cardinality identities require tenant-scoped access and retention policies;
they are not global Prometheus labels by default.

## 11. Dashboard information architecture

### 11.1 Overview

- Active managed Runtime count.
- Observed CPU usage and known configured capacity.
- Observed memory usage and known limits.
- Reported Session tokens, together with the reporting Session count.
- Data freshness and source coverage.

Aggregates include only present measurements. Each total states its denominator,
for example, `6.4 / 12 cores across 6 of 8 active Runtimes`. Unknown is never added
as zero.

### 11.2 Runtime table

Each row shows Session, Agent/harness when already available from the Session
snapshot, mode, observation status, CPU, memory, compute uptime, Turn state, and
reported tokens. Rows navigate to the existing Session view. No stop, restart,
pause, or delete actions appear in the first release.

### 11.3 Detail view

The detail surface shows exact Session/Environment/allocation identity, provider
type, observation timestamps, allocation age, compute uptime, and safe unavailable
reason. It displays only the Core-owned identifiers explicitly present in the
public contract. Provider-native container IDs, pod names, instance names, host
paths, and raw labels are never displayed.

### 11.4 States

- **Loading:** no previous complete Runtime snapshot.
- **Fresh:** every page loaded and each row carries its own resolution time; a
  provider sample also carries its independent observation time.
- **Stale:** refresh failed; previous complete snapshot retained.
- **Unavailable row:** identity is valid, measurement is temporarily absent.
- **Unsupported row:** mode is recognized but has no qualified source.
- **Integrity failure:** do not publish a partial replacement snapshot.

### 11.5 Web implementation shape

The Web change belongs in Core Web, not the Core service repository. It uses
`packages/agents-client` as the only Runtime-observation transport and keeps four
seams separate:

1. A client/parser module validates one page and exposes list and Session-scoped
   retrieval methods.
2. A refresh coordinator loads all observation pages plus the canonical Session
   collection, applies page/target/time budgets, requires exact equality of their
   Session ID sets, and atomically swaps only a complete joined snapshot. A set
   mismatch is an incomplete refresh, not a partial success.
3. A feature-local state model retains `last_complete`, current refresh status,
   local filters, and the selected time range. It aborts an overlapping refresh
   and marks old data stale after a failed or incomplete refresh.
4. Presentational components render summary coverage, the Runtime table, and a
   Session detail surface. Trend components are absent unless a later history
   capability and contract are configured.

The initial refresh cadence is an operator-configured value, not an API guarantee.
Web pauses periodic reads when hidden, refreshes when visibility returns, and adds
jitter so multiple browsers do not synchronize. Filtering is local to the last
complete snapshot and never changes tenant authorization or provider selection.

## 12. Token usage boundary

Runtime observations do not duplicate token usage. Web uses the existing canonical
Session Usage snapshot and joins it to Runtime rows by `session_id`. The Dashboard
shows both total and coverage, such as `1.84M reported by 7/8 Sessions`. Missing or
incomplete native usage remains unknown.

Cost and billing stay outside this Core API. A product may join billing in its own
authorized backend, never by exposing product credentials to Core Web.

## 13. Security and tenancy

- Authenticate with the existing Agents API mechanism.
- Scope resolution to the authenticated tenant before provider access.
- Do not accept provider key, allocation ID, container ID, pod UID, or device ID
  as an authority-bearing query parameter.
- Bound per-request list size, concurrency, response bytes, and source deadlines;
  Web separately bounds a complete multi-page refresh.
- Sanitize logs through `internal/obs/log`.
- Never return credentials, environment variables, Docker raw JSON, daemon status
  payloads, host paths, image registry credentials, or backend credentials.
- Rate-limit collection separately from ordinary Session reads.

## 14. Data model impact

Current snapshot and Dashboard work require no migration. Existing
`runtime_allocations`, `environments`, Sessions, Turns, and Usage are sufficient.
No time-series table is proposed.

Automatic idle shutdown is a separate feature. It requires durable fields such as
`activity_revision`, `idle_since`, and `shutdown_requested_at` with fenced state
transitions. That migration cannot read a monitoring backend as authority.

## 15. Delivery plan

### Phase 1: provider-neutral foundation

Implemented in the Docker observability foundation.

- `runtimeobs` identity, resolver, source, sample, and service.
- Managed Docker Inspect/Stats source.
- CPU, memory, and current compute start time.
- Explicit unsupported and unavailable states.

### Phase 2: current snapshot API

Implemented by the Runtime Observation extension routes and
`packages/agents-client`. The generated OpenAPI contract records the extension;
this does not add an upstream OpenAI operation.

- Add extension types under `contracts/agents-api/v1`.
- Add collection and Session-scoped handlers.
- Add `packages/agents-client` methods and raw HTTP/client coverage.
- Add bounded concurrency, timeout, authorization, and error tests.
- Regenerate the public OpenAPI contract.

### Phase 3: Web Dashboard

- Add Runtime observations as a third independent Dashboard collection.
- Publish only complete traversals and retain the previous snapshot on failure.
- Join existing Session Usage and Turn status by exact Session ID.
- Add responsive, keyboard-accessible current-resource views.

### Phase 4: optional history

- Add telemetry exporter and qualified operator backend.
- Define a separate history query adapter and retention/security policy.
- Add trend charts only when this capability is advertised.

### Phase 5: additional sources

- Kubernetes, E2B, and generation-fenced self-hosted telemetry.
- Each source requires independent mechanism and deployment acceptance.

### Phase 6: idle policy

- Separate durable activity and shutdown state machine.
- No automatic action until race, fencing, recovery, and operator-control
  acceptance is complete.

## 16. Acceptance criteria

- Every observation proves tenant, Session, Environment, and Runtime-instance
  association.
- Managed Docker emits correct present/absent semantics and never mutates compute.
- Unsupported modes never look like zero usage.
- Collection calls are bounded and one ordinary unavailable source invents no data.
- Ownership/integrity mismatch fails closed.
- Web never publishes a partial page traversal as a current snapshot.
- Web rejects cross-collection Session membership skew, including concurrent
  Session create/delete cases, before publishing tenant-wide aggregates.
- Token totals report coverage and do not estimate missing usage.
- No lifecycle action is reachable from the first Dashboard.
- No new database table is required for current snapshots or history export.

## 17. Recorded design decisions

1. The API is a documented Core extension rather than an upstream OpenAI resource.
2. Current snapshots have no atomic cross-row time semantics; every row
   exposes its own `observed_at`.
3. History is optional and external, not a PostgreSQL sample table.
4. The first Web release has no lifecycle controls.
5. `self_hosted` remains visibly unsupported until authenticated,
   generation-fenced telemetry is qualified.
