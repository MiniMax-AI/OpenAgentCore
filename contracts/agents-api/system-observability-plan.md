# System observability collection plan

Status: implementation inventory for the operator Dashboard. Runtime history,
request buckets, terminal Turns, tool attempts, and node state have independent
sources. The model-attempt table is reserved but has no writer; its panel must
remain unavailable until native per-attempt events are qualified.

## Existing evidence

| Signal | Source | Current coverage | Dashboard meaning |
| --- | --- | --- | --- |
| Sandbox lifecycle | Core allocation state | Managed Docker and microsandbox | Current control state, not execution success |
| CPU time and capacity | Provider observation | Current and periodic history when sampling is available | Cumulative time; utilization needs two samples from one compute incarnation |
| Memory usage and limit | Provider observation | Current and periodic history when reported | Point-in-time guest/container memory, not host memory |
| Token input/output | Measured Session Usage | Public current Session and periodic Runtime history | Reported canonical usage; absent usage remains unknown |
| Request count, latency, errors | Completed API responses, stored in minute buckets | Fixed route families, methods, outcomes, and latency bins; collector heartbeat tracks quiet intervals | Transport health, not Turn success |
| Terminal Turn outcome and duration | Persisted `turns` terminal timestamps | Completed, failed, and cancelled Turns | Execution outcome, not HTTP success |
| Model distribution and latency | Native per-attempt evidence is not normalized across harnesses | Unavailable | Do not label configured models, cumulative Usage, or Turns as invoked models |
| Tool calls and failures | Sanitized `tool_call` completion events | Attempts with an after event; missing before leaves duration null unless native duration exists | Does not count Agent tool declarations or model selection |
| Sandbox nodes | Deployment administrator node list | Current online, provider-ready, active, retained and capacity fields | A node being online does not prove a Turn can execute |
| Core process, database, queue, and jobs | Core service metrics projection | Current and bounded process-local history | Service health, not Session-scoped execution evidence |

Current Runtime history has a 30-second default periodic collection interval and
seven-day retention; the public Web ranges are 1h, 6h, and 24h. Its table is
renamed in place from `runtime_history_samples` to
`observability_runtime_samples`. Its capability route distinguishes periodic
collection from on-read collection. Missing values and empty intervals are not
observed zeroes.

## Collection boundary and remaining work

The authenticated operator metrics service runs behind Core. It keeps the pinned
Agents API resources unchanged, aggregates sanitized events server-side, and
exposes a bounded read-only extension through
`packages/agents-client`. Browser input may select a fixed range and resolution;
it must not select tenants, storage labels, arbitrary PromQL, endpoints or keys.

1. **Ingress (implemented):** count completed HTTP requests by route family,
   method and coarse outcome (`success`, `client_error`, `server_error`). Record a
   latency histogram after the response completes. Health polling and the metric
   read itself are excluded. The bounded asynchronous queue records drops and
   write failures; failed batches are not replayed onto the request path.
2. **Execution (implemented):** query terminal Turns from durable state with queue
   wait and run duration measured from persisted timestamps. The query is bounded
   to a fixed time window and avoids duplicate terminal events. Active Turns
   remain a current Core gauge, not a counter inferred from sampled CPU.
3. **Model (pending):** observe actual native model invocation attempts at the harness
   boundary, including model identifier, terminal attempt outcome, duration,
   reported input/output tokens and usage coverage. A saved Agent's configured
   model is not proof of which model ran. Keep bounded model label cardinality.
4. **Tools (partial):** observe `tool_call` before/after events at the common
   execution journal boundary. Use a bounded tool category and outcome label;
   raw tool names, arguments, output and credentials stay out of metric labels.
   Record a completed attempt only after the corresponding event batch has been
   persisted. Completion is deduplicated by Turn and call ID. A missing after
   event is not counted as a completed attempt. Some harnesses may not emit
   these events.
5. **Sandbox (partial):** retain allocation identity and compute generation internally.
   Extend the normalized sample only after Docker and microsandbox values have
   matching semantics. Candidates are OOM/exit events, disk usage/limit, network
   bytes, compute restarts, and allocation/compute startup latency. A missing
   provider value remains null; lifecycle state remains Core-owned.
6. **Collector health (partial):** request collection writes a minute heartbeat,
   including quiet minutes. Request and tool queues report drops and write
   failures. Runtime sampler and model-attempt collector health are not yet wired
   into this operator projection; an absent row is unavailable, not zero.

The operator migration adds `observability_request_minute_buckets`,
`observability_model_attempts`, `observability_tool_attempts`, and
`observability_collector_minute_buckets`. Request and collector buckets retain
30 days; model and tool attempts retain seven days. Model attempts remain empty
until a native per-attempt producer is implemented. The deployment administrator
summary supports only 1h, 6h, and 24h and returns no tenant or raw identifiers.
It is served at `/core/v1/admin/observability` through the same administrator
credential and `AdminClient` transport as Core service metrics. The existing
`/core/v1/admin/core-metrics` projection supplies queue, database, job, and
process measurements; this collection does not duplicate those sources.

Use bounded histograms for p50/p95 latency and rates from counters over complete
time buckets. Keep operational cardinality to route family, outcome, provider
type and bounded model family. Session, allocation, tenant, tool name and native
identifiers must not become general metrics labels. Drill-down can use authorized
Core resource IDs through existing tenant-scoped reads. Retention and query limits
must be explicit, with history storage/export optional for execution.

## Dashboard layout and acceptance

The overview keeps a small current snapshot: Agents, Sessions, active Sandboxes,
reported tokens, measured CPU/memory capacity, and ready nodes. The Observability
tab leads with the four existing Runtime trends, followed by request, Turn, tool,
collector, node, and Core service panels. The model panel states that collection is unavailable. The
request rate, error rate, and range latency require complete collector heartbeat
coverage with no recorded drops or export failures; otherwise they are unavailable. Request route tables show counts only
for covered buckets. Every panel must preserve missing measurements and must not equate
a configured Sandbox with a completed execution.

Before shipping those new panels, validate counter deduplication under retries,
provider restarts, missing usage, sampler outages and multi-tenant authorization;
verify that instrumentation has no effect on dispatch, lifecycle or settlement.
