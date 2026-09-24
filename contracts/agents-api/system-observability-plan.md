# System observability collection plan

Status: collection inventory and next-phase design. The current Dashboard release
uses existing Runtime observations and Session Usage only. Request, model, and tool
panels must not be shown as measured until the instruments below are implemented.

## Existing evidence

| Signal | Source | Current coverage | Dashboard meaning |
| --- | --- | --- | --- |
| Sandbox lifecycle | Core allocation state | Managed Docker and microsandbox | Current control state, not execution success |
| CPU time and capacity | Provider observation | Current and periodic history when sampling is available | Cumulative time; utilization needs two samples from one compute incarnation |
| Memory usage and limit | Provider observation | Current and periodic history when reported | Point-in-time guest/container memory, not host memory |
| Token input/output | Measured Session Usage | Public current Session and periodic Runtime history | Reported canonical usage; absent usage remains unknown |
| Request count, latency, errors | No common metric | Not available | Do not derive from Session count or HTTP page loads |
| Model distribution and latency | Saved Agent model is available, terminal call records are not aggregated | Configuration only | Do not label configured models as invoked models |
| Tool calls and failures | Durable Turn events exist, no bounded aggregate read | Partial event evidence | Do not count tools by Agent declarations |
| Host, database, queue health | No tenant-safe operator projection | Not available | Keep out of Session-scoped charts |

Current Runtime history has a 30-second default periodic collection interval and
seven-day retention; the public Web ranges are 1h, 6h, and 24h. Its capability
route distinguishes periodic collection from on-read collection. The Dashboard
must show the source, age, coverage and missing values. An empty interval is not
an observed zero.

## Next collection boundary

Add a separate, authenticated operator metrics service behind Core. Keep the
pinned Agents API resources unchanged. The service should aggregate sanitized
events server-side and expose a bounded read-only extension through
`packages/agents-client`. Browser input may select a fixed range and resolution;
it must not select tenants, storage labels, arbitrary PromQL, endpoints or keys.

1. **Ingress:** count completed HTTP requests by route family, method and coarse
   outcome (`success`, `client_error`, `server_error`). Record a latency histogram
   after the response completes. Exclude health polling or show it separately.
   This is transport health, never terminal Turn success.
2. **Execution:** emit one terminal Turn outcome from the durable state transition
   winner, with queue wait and run duration measured from persisted timestamps.
   Active Turns are a current gauge from Core ownership, not a counter inferred
   from sampled CPU. Retries and recovery must not double count terminal outcomes.
3. **Model:** observe actual native model invocation attempts at the harness
   boundary, including model identifier, terminal attempt outcome, duration,
   reported input/output tokens and usage coverage. A saved Agent's configured
   model is not proof of which model ran. Keep bounded model label cardinality.
4. **Tools:** observe actual tool attempt start and terminal result at the common
   Runtime adapter boundary. Use a bounded tool category and outcome label;
   raw tool names, arguments, output and credentials stay out of metric labels.
   Distinguish model tool selection from completed tool execution.
5. **Sandbox:** retain allocation identity and compute generation internally.
   Extend the normalized sample only after Docker and microsandbox values have
   matching semantics. Candidates are OOM/exit events, disk usage/limit, network
   bytes, compute restarts, and allocation/compute startup latency. A missing
   provider value remains null; lifecycle state remains Core-owned.
6. **Collector health:** count attempted, successful, timed-out and unavailable
   samples, queue drops and export failures. Display coverage per interval so
   a quiet chart cannot hide collector failure.

Use bounded histograms for p50/p95 latency and rates from counters over complete
time buckets. Keep operational cardinality to route family, outcome, provider
type and bounded model family. Session, allocation, tenant, tool name and native
identifiers must not become general metrics labels. Drill-down can use authorized
Core resource IDs through existing tenant-scoped reads. Retention and query limits
must be explicit, with history storage/export optional for execution.

## Dashboard layout and acceptance

The overview keeps loaded Agent/Session counts and current attention. The Runtime
section shows allocation-deduplicated lifecycle, coverage, sample gaps, memory
pressure and existing Live/History trends. A later System section can add request
rate/error rate/p95, terminal Turn outcomes and durations, actual model usage and
tool attempts once their collection is qualified. Every panel needs a source and
freshness label, a coverage denominator, and an unavailable state. It must never
convert missing measurements to zero or equate a configured Sandbox with a
completed execution.

Before shipping those new panels, validate counter deduplication under retries,
provider restarts, missing usage, sampler outages and multi-tenant authorization;
verify that instrumentation has no effect on dispatch, lifecycle or settlement.
