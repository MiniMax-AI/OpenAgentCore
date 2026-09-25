# Administrator metrics: backend requirements

Status: proposal for discussion with Core owners. Nothing in this document is
implemented beyond the Web API routes it names as existing.
[简体中文](admin-metrics-backend-requirements.zh-CN.md)

The console is a management tool: Monitor (Overview, Agent metrics, Sandbox
metrics, Session log) leads. It reads only the Web API (`/core/v1/**`,
including the sandbox administration routes under `/core/v1/sandbox/**`); it
never calls `/v1`. Some figures come from Web API aggregates, others are still assembled
in the browser from bounded reads of each project. This document records what
that costs, where it is incomplete, and which Web API endpoints would replace
the browser work.

## Two classes of Core interface

1. **Public Agents API** (`/v1/**`): serves only the pinned official OpenAI
   Agents API routes; Core additions appear only as `x_agents_core` fields.
   Metrics work must not add fields, routes or behavior here.
2. **Web API** (`/core/v1/**`, including `/core/v1/sandbox/**` for sandbox
   administration): called by the console server and operator scripts with the
   Core key. Every endpoint proposed below belongs here; `services/core-console`
   forwards `/core/v1/*` by prefix, so a new endpoint needs no proxy change.

## What the console computes today

| Page | Source | Limit |
| --- | --- | --- |
| Overview | `GET /summary` (per project: asset counts, Sessions by status, usage, coverage, last activity); `/core/v1/sandbox` deployment and nodes; each project's Session list for the 24-hour activity chart and the Sessions needing attention | Session lists stop once they pass the 24-hour window and have found every Session the summary counts as needing attention, at most 1,000 per project; projects idle since before the window are not read |
| Agent metrics | `GET /summary` to skip idle projects; each project's Session list, then Turns and Items of the most recently active Sessions through the project's scope; `GET /summary?group_by=key` for usage by API key | 2,000 Sessions listed per project; 200 Sessions read per load, 10 Turn pages and 5 Item pages per Session, 15 s per Session and 45 s per load |
| Sandbox metrics | `/core/v1/sandbox` nodes and allocations; `GET /core/v1/sandbox/runtime-observations` (every project); each hosted Session read by ID through its project; Runtime history per hosted Session | 100 hosted Sessions read per refresh; history covers at most 24 hosted Sessions; host figures are free memory/disk and CPU count only |

Consequences the console states in its help tips and warnings:

- A "request" is an Agent Turn. HTTP-level request counts, status codes and API
  latency are not available anywhere.
- Model attribution uses the Session's Agent snapshot, not the effective
  execution configuration.
- Busy projects exceed the Session caps, so long ranges and old Sessions that
  need attention can be partial; the page says which projects were cut short.
- Usage by API key counts Sessions created in the range by their creating key
  (#87 provenance); Sessions without a record are shown as "unknown".

Other limits: browser and Core clocks can differ (the console tolerates 15
minutes of skew without saying so), and Subagent Turns and deleted Sessions are
not counted.

## Proposed endpoints

All endpoints are read-only, deployment-scoped with an optional `project_id`
filter, bounded like Runtime history (`start`, `end`, `step`, a maximum range and
point count), and return `null` for unavailable values instead of zero. They are
operational evidence, not billing.

### P0 — Agent run aggregates

`GET /core/v1/metrics/agent-runs?start=&end=&step=&group_by=model|agent|harness|project|key&project_id=`

Per bucket, and per group when `group_by` is set:

- Turns created, completed, failed, cancelled, still running.
- Duration from `started_at` to `completed_at`: average, p50, p95.
- Queue wait from `created_at` to `started_at`: average, p95.
- Tokens: input, output, cached input, reasoning.
- The effective model and harness from the execution configuration.
- Top-N groups plus an `other` bucket, with the total group count.

This replaces the Turn fan-out on Agent metrics and makes long ranges complete.

### P0 — Tool call aggregates

`GET /core/v1/metrics/tool-calls?start=&end=&step=&group_by=tool|kind|agent&project_id=`

Per bucket and tool (`function` name, MCP `server_label` + name, shell command,
web search, Subagent): calls, failures, and duration where the Item reports one.
This replaces the Item fan-out.

### P1 — Core process and host status

The Overview shows Core itself beside its sandbox hosts. Core runs no
sandboxes, so it has no slots; the operator asked for its CPU and memory
instead. Nothing reports them, so the console shows the Web API's reachability
and maintenance state and "Not reported" for CPU and memory.

`GET /core/v1/core-status`

- Core version and process uptime.
- Process CPU utilization (ratio of one core, averaged over a short window) and
  the host's CPU cores and utilization.
- Process resident memory, host memory total and available.
- Database reachability and connection-pool use.
- `null` for any figure the platform cannot read (for example inside a
  restricted container), never zero.

### P1 — Session activity and attention

`GET /summary` already gives Session counts by status per project, Agent and
key. Two Overview parts still read Session lists:

- Sessions created and failed per bucket:
  `GET /core/v1/metrics/sessions?start=&end=&step=&project_id=`.
- Sessions needing attention across projects:
  `GET /core/v1/sessions?status=failed,requires_action&order=last_active_desc&limit=`
  (each entry labelled with its project), or a `status` filter on the per-project
  Session list.

### P1 — Agents API request metrics

`GET /core/v1/metrics/api-requests?start=&end=&step=&group_by=route|status_class|project|key`

Recorded by the API router middleware: request count, 4xx/5xx counts and latency
percentiles per route family (Sessions, events stream, Turns, Items, files …).
This is what hosted consoles call "requests" and "error rate". Store rollups in
the existing PostgreSQL database with bounded retention, following the Runtime
history pattern, with the optional OTLP exporter as a secondary sink.

### P1 — Node utilization and history

- Extend `GET /core/v1/sandbox/nodes` with host CPU utilization, memory used and
  total, disk used and total, node software version and process uptime.
- `GET /core/v1/sandbox/nodes/{id}/history?start=&end=&step=` for active,
  retained and reserved sandboxes and host utilization over time.

### P2 — Hosted Runtime rows with their Sessions

`GET /core/v1/sandbox/runtime-observations` labels each observation with its
project but not its Session's title, Agent, status or usage, so Sandbox metrics
reads every hosted Session by ID (bounded at 100 per refresh). An
`expand=session` option returning those fields would remove the reads.

### P2 — Keys

- API key `last_used_at` and per-key request counts (with the API request
  metrics above).

## Open questions

1. Should aggregates be computed on read from existing tables (simpler, slower on
   large deployments) or from periodic rollups (like Runtime history)?
2. Retention and maximum range for each family; is 7 days enough for the console?
3. Which percentile method and bucket alignment should all metric families share?
4. Should API request metrics exclude the console's own polling traffic?
5. Should Core's own status include the executor gateway and model endpoint
   reachability, or stay limited to the process and host?
