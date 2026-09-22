# History, events and usage

This milestone covers existing execution resources and client recovery. It does
not establish complete Agents API compatibility. The protocol baseline remains
the SDK and source recorded in [upstream.json](upstream.json).

## Query and live-stream contract

Open the live Session events stream before submitting input. After disconnection,
subscribe again and retrieve Session, Turns and Items to recover persisted state.
An SSE connection is an observer, not the owner of execution. `Last-Event-ID` does
not introduce replay. Query responses and transition events are observations at
different times; a completed Turn can acquire a measured usage snapshot later.
Do not permanently cache `usage: null` as zero or as a final accounting result.

The TypeScript client shares Turn and Item projections between reads and SSE.
It preserves root/child identity and supports the currently implemented reasoning
and coordination Items. `agent_message` has no status; reasoning status can be
absent or null. Child discovery can publish an already terminal native Turn in a
`turn.created` snapshot; clients must not reinterpret it as newly queued work.
This does not widen the server's Item or interim reasoning-event coverage.
Claude and MiniMax retain their settlement-based child-history boundary; accepting
coordination events does not guarantee continuous child progress or a child Turn
event on every execution. Recover child state through the Subagent queries.

Use response cursors to page history in the requested direction. Session Turn
lists can include root and child Turns. Root Items and child Items have separate
query resources; use the Subagent resources for child history. Tenant ownership
is enforced by Core for both queries and streams.

## Measurement boundary

Adapters publish cumulative measurements for the current execution through the
existing neutral Usage contract. Core replaces a Turn's snapshot atomically;
repeated snapshots, including terminal repeats, do not add consumption. The
Session total sums recorded root-Turn measurements, not mixed root/child lists.
It is best-effort accounting, not an invoice or an estimate of missing work.

- Codex publishes observed snapshots while the Turn is active. Exact native
  resume excludes the previous Session total from the new Turn. A complete public
  breakdown requires valid input, cached, output, reasoning and total counters.
- Claude retains native result evidence internally. Its per-turn and cumulative
  query/model counters have different scopes, and reasoning attribution can be
  incomplete. Complete public TokenUsage remains unqualified and null.
- MiniMax ACP context occupancy describes context capacity, not measured resource
  consumption. Complete public TokenUsage remains unqualified and null.

Persisted snapshots survive cancellation and Core worker restart. This does not
recover observations lost before journal commit, stop external tool side effects,
or replay interrupted input. Provider/model reroute attribution, billed costs,
unknown historical counters and unreported partial usage remain outside this
milestone. Native differences must not be hidden with guessed zero counters.

## Core acceptance, 2026-09-22

Three independent PostgreSQL/Core deployments used Docker V1 Runtime images with
the integrated daemon, fixed official SDK 3.13.0, raw HTTP and the final TypeScript
client. Codex and Claude called Kimi K3; MiniMax Code called MiniMax M2.7. Each
performed real native child delegation and an ordinary root Turn. All three passed
history identity/content agreement, both paging directions at limits one and two,
SSE disconnect/reconnect, wrong-tenant denial and stable history after idle Core
restart without another Turn. Captured coordination events passed the TS parser.
Claude's first run had coordination events but no child Turn SSE snapshot; the
final run also captured and parsed child Turn snapshots. Both observations retain
the native publication boundary above, without a continuous-progress guarantee.

Codex additionally exposed measured usage while a tool-separated Turn was still
in progress, then retained it after cancellation, repeated reads and Core restart.
Controlled adapter tests cover ordered publication and cancellation under output
backpressure. PostgreSQL tests cover cumulative replacement, duplicate snapshots
and known usage retained across worker reconciliation after process loss. The
idle real restart does not substitute for active native crash qualification.

Validation included the complete standalone gate split between server
`make -o check-web check` and local `make check-web`, Codex adapter race tests,
286 client tests, 573 Core Web tests and 74 fixture Playwright tests. SQL generation
matched byte-for-byte. No handler annotation, schema, DB query or migration changed.
Real proof and first-failure records are retained privately under
`~/.parsar/remediation/20260922/history-events-usage/live/`. The first Claude probe
incorrectly required a live child Turn event; the bounded follow-up checked the
documented coordination stream and child queries. Invalid historical patch fixtures
were corrected to separate function calls from their result Items, without widening
production validation. E2B and unrelated product flows were not rerun.

## Official-service observation, 2026-09-22

A bounded probe used the fixed Python SDK 3.13.0, raw HTTP, `agents=v1`, and one
owned `environment:none` Session with two tiny real-model text Turns. It did not
read unrelated resources or use tools, workspace execution or Subagents. The
owned Session was deleted after the probe.

- Creation returned HTTP 201 SSE. Input submission returned HTTP 202.
- First-turn events followed queued/in-progress, Item/text output and terminal
  transitions. Its terminal usage was null. Later Session and Turn reads reported
  input 7226, cached 0, output 7, reasoning 0, total 7233.
- Reconnection with an old `Last-Event-ID` produced no historical frames during
  three idle seconds, then delivered new second-Turn frames.
- After closing the second observer stream, queries recovered both completed
  Turns and all four Items. Ascending and descending limit-one pages reversed
  exactly, with exclusive sampled cursors. The Turn completed before the client
  disconnected, so this alone does not prove survival of an active disconnection.
- Session usage after the second Turn was null, as was that Turn's usage in the
  observed pages. No later aggregation behavior was inferred. Twenty-two captured
  events and sampled resources passed strict pinned-type validation.

These are bounded observations, not complete timing or accounting guarantees.
Same-timestamp paging, failures/cancellation and Subagent accounting were not
probed against the official service. The current documentation describes an
Items `turn_id` filter absent from the pinned SDK and requires initial input for
`none` where the pin describes it as optional. Neither difference changes the
repository baseline; the probe supplied initial input and sent no unpinned filter.

Sources: [Sessions](https://developers.openai.com/api/docs/guides/agents-api/sessions),
[Events](https://developers.openai.com/api/docs/guides/agents-api/sessions/events),
[Observability](https://developers.openai.com/api/docs/guides/agents-api/observability).
Sanitized request evidence is retained privately under
`~/.parsar/remediation/20260922/history-events-usage/official/`; credentials are
excluded from source and evidence.
