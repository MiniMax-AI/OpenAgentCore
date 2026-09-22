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
