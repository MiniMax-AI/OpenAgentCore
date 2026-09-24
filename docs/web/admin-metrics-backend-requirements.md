# Administrator metrics: backend requirements

Status: proposal for discussion with Core owners. Nothing in this document is
implemented. [简体中文](admin-metrics-backend-requirements.zh-CN.md)

The redesigned console leads with operations: Overview, Agent metrics, Sandbox
metrics and the Session log. Today it builds every figure in the browser from
existing reads. This document records what that costs, where it is incomplete,
and which Core endpoints would replace the browser work.

## Two classes of Core interface

1. **Public Agents API** (`/v1/agents/**`, `/v1/vaults/**`, `/v1/files**`,
   `/v1/skills/**`): must stay identical to the pinned OpenAI Agents API and its
   documented Core extensions. Metrics work must not add fields, routes or
   behavior here.
2. **Console-only administration** (`/core/v1/**`): deployment-level routes that
   only the paired console calls, after console sign-in, with the server-side
   deployment administrator token. `sandbox` and `project-api-keys` already live
   here. Every endpoint proposed below belongs to this class and needs an entry
   in the console allowlist (`services/core-console`).

Existing Web-facing Core extensions under `/v1` (runtime observations, runtime
history, startup configuration, execution configuration, sandbox placement) stay
where they are; this proposal does not move them.

## What the console computes today

| Page | Source | Limit |
| --- | --- | --- |
| Overview, Session log, Agents | Complete Session list plus Runtime observations, re-read every 30 s | About 10,000 Sessions; project scope of the console credential only |
| Agent metrics | Turns and Items of the most recently active Sessions in the range | 200 Sessions, 10 Turn pages and 5 Item pages per Session per load, 15 s per Session and 45 s per load; 1 to 15 requests per Session (about 3,000 in the worst case) on each refresh |
| Sandbox metrics | `/core/v1/sandbox` nodes and allocations; Runtime history per hosted Session | Host figures are free memory/disk and CPU count only; history covers at most 24 hosted Sessions |

Consequences the console states in its help tips and warnings:

- Totals cover one project (the console's project credential), not the whole
  deployment.
- A "request" is an Agent Turn. HTTP-level request counts, status codes and API
  latency are not available anywhere.
- Model attribution uses the Session's Agent snapshot, not the effective
  execution configuration.
- Busy deployments exceed the Session cap, so long ranges are partial.

Other limits: browser and Core clocks can differ (the console tolerates 15
minutes of skew without saying so), and Subagent Turns and deleted Sessions are
not counted.

## Proposed endpoints

All endpoints are read-only, deployment-scoped with an optional `project_id`
filter, bounded like Runtime history (`start`, `end`, `step`, a maximum range and
point count), and return `null` for unavailable values instead of zero. They are
operational evidence, not billing.

### P0 — Agent run aggregates

`GET /core/v1/metrics/agent-runs?start=&end=&step=&group_by=model|agent|harness|project&project_id=`

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

### P1 — Session summary

`GET /core/v1/metrics/sessions?project_id=`

Counts by status, environment type and Agent, plus Sessions created and active
per bucket. Lets the Overview stop reading the full Session list.

### P1 — Agents API request metrics

`GET /core/v1/metrics/api-requests?start=&end=&step=&group_by=route|status_class|project`

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

### P2 — Deployment configuration writes

The console can only read whether each harness has a model endpoint configured
at startup. A console-only write API (for example
`PUT /core/v1/deployment/model-providers/{harness}`, write-only credentials)
would let operators set deployment defaults in Web. It must keep the precedence
Session → Agent → deployment and never return secrets.

### P2 — Keys and projects

- API key `last_used_at` and per-key request counts.
- A project list (`GET /core/v1/projects`) if a deployment serves more than one
  project, so metrics can be filtered by project in Web.

## Open questions

1. Should aggregates be computed on read from existing tables (simpler, slower on
   large deployments) or from periodic rollups (like Runtime history)?
2. Retention and maximum range for each family; is 7 days enough for the console?
3. Which percentile method and bucket alignment should all metric families share?
4. Is a deployment ever multi-project in practice, and who may see other
   projects' Session identifiers in node allocations?
5. Should API request metrics exclude the console's own polling traffic?
