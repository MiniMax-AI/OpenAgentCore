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
absent or null. A child Turn's `agent_id` is the Session's Agent ID and its
`subagent_id` names the child. The Session stream carries root work only: child
Turns and child Items publish no Session events, while `agent.session.subagent.*`
events and root coordination Items remain. Earlier Core releases streamed child
Turns, which could first appear as an already terminal `turn.created` snapshot;
the client still accepts that and must not reinterpret it as newly queued work.
This does not widen the server's Item or interim reasoning-event coverage.
Claude and MiniMax retain their settlement-based child-history boundary. Recover
child state through the Subagent queries.

Use response cursors to page history in the requested direction. Session Turn
lists contain root Turns only; a child Turn ID on the Session Turn routes is not
found. Root Items and child Items have separate query resources; use the Subagent
resources for child Turns and history. See
[Subagent visibility](subagents.md#subagent-visibility--september-23-2026).
Tenant ownership is enforced by Core for both queries and streams.

## Measurement boundary

Adapters publish cumulative measurements for the current execution through the
existing neutral Usage contract. Core replaces a Turn's snapshot atomically;
repeated snapshots, including terminal repeats, do not add consumption. The
Session total sums recorded root-Turn measurements, not Subagent Turn lists.
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

## Creation stream settlement, 2026-09-23

Evidence: the second official-semantics campaign scan compared four owned
`environment:none` official Sessions with Core (private
`~/.parsar/remediation/20260923/campaign-scan-2/events-tools/`, `findings.json`
EVT-01..24 with raw frames under `official/`). All 1091 official events passed
strict validation against the pinned types. Two independent official creation
streams (structured output, and a function call with its result) were closed by
the server; they, a third creation stream that the client closed while a call
was pending, and the 2026-09-22 probe above agree on the snapshot, order and
terminal-event observations below. This batch changes only the four Core-owned
stream differences EVT-01..04; the plan is
`~/.parsar/remediation/20260923/creation-stream-settlement/PLAN.md`.

- **Creation stream lifetime (EVT-01).** The official service closed both creation
  streams right after the first `agent.session.idle`, without `[DONE]` or an error
  frame, and kept the function stream open through `requires_action` and the
  result. A fresh Core creation stream ends right after the first
  `agent.session.idle` recorded when a Turn ends or an input reservation stops
  being pending (expired, cancelled or failed), or any `agent.session.failed`,
  and never sends the events after it. A self-hosted connection that clears
  pending input to idle does not end it. A creation that admitted nothing ends
  right after `created`. Settlements that record no event, such as a reservation
  cancelled while its Session is already idle, use a fallback: after an empty
  drain the stream reads the JSON-path projection and the event cursor in one
  database snapshot and, if settled, sends only events up to that cursor, then
  ends. The settled marker and pending-input flag are Store-internal and add no
  events. A same-key `stream=true` retry of an existing creation returns 201 with
  only the connection comment and ends at once; official same-key requests create
  distinct Sessions, so there is no retry stream to follow, and recovery uses
  `stream=false` or GET. The TypeScript client reports that empty creation stream
  as `CreationStreamRetryError`. Accepted follow-ups: another client's work
  drained before the fallback read can still be sent after a silent settlement;
  idles recorded by an older binary during a rolling deploy carry no settled
  marker and rely on the fallback; and an input reservation made while the ending
  Turn captured Artifacts can start a later Turn that the stream does not follow.
  Four review rounds replaced event-only, projection-only and retry-following
  designs before merge. GET event streams are unchanged: live-only, no replay,
  and they never end on their own. Session deletion still ends both.
- **Created snapshot (EVT-02).** Official `agent.session.created` carried the
  post-admission Session (`in_progress`, no actions, null usage), like the JSON
  201 body. Core now sends the committed projection that JSON 201 returns, read
  after the creation commit, while the stream still starts at the creation
  upsert cursor, so every initial Turn and Item event follows exactly once. The
  snapshot is read after the commit, so it can already show a later state than
  the events that follow it; the JSON 201 body has the same race. For
  self-hosted input the snapshot already requests the Environment connection and
  the committed `requires_action` event follows. Hosted initial input remains
  `idle` while it provisions.
- **Terminal usage (EVT-03).** Official `agent.session.turn.completed` and
  `.cancelled` (8/8) carried a top-level `usage`, null at emission even when later
  reads were measured. Core terminal Turn events (`completed`, `failed`,
  `cancelled`) now carry `usage` copied from the rendered Turn snapshot, with
  explicit null when unknown; other events omit it. This batch applied it to
  root and child Turn events; the
  [Subagent visibility batch](subagents.md#subagent-visibility--september-23-2026)
  later stopped publishing child Turn events on the Session stream. Codex can
  therefore publish measured counters at settlement, while Claude and MiniMax
  stay null; no counter is derived or summed. The TypeScript client accepts the
  field on terminal Turn events only and still accepts older events without it.
- **Turn start order (EVT-04).** Official new Turns published `turn.created`, the
  user `item.added` (`output_index` null), `agent.session.in_progress`, then
  `turn.in_progress`. Core now records the Session activity after the admitting
  input's Items in the same transaction, for creation, events.create and
  reservation promotion. When one batch holds several message events, later
  messages follow that activity; their official order was not observed.

Deferred, with evidence retained in `findings.json`:

- EVT-05: Core emits `turn.in_progress` when a function result resumes a waiting
  Turn and publishes the result Item at native application; the latter is the
  known INTERACTION-PUBLICATION-001 receipt boundary.
- EVT-06: Core emits an interim `agent.session.in_progress` when cancelling a
  Turn that waits on a function result. Statuses match.
- EVT-07: official mid-Turn attach sent catch-up Item snapshots, but not
  deterministically; one more official sample is needed before designing.
- EVT-08: official Items omit in-progress and incomplete output Items; Core keeps
  them under native history ownership.
- EVT-09 and EVT-10: null-valued `output_index`, `phase` and `error` fields and
  the initial assistant content belong to a separate serialization batch.
- EVT-11 and EVT-12: unknown call/Turn result and conflict error codes belong to
  ERROR-PROTOCOL-001.
- EVT-13: official Session usage became null when any root Turn usage was
  unknown; Core sums the known Turns. The in-progress case is unverified.
- EVT-19: the Core terminal sequence for a Turn cancelled mid-text is recorded by
  this batch's live acceptance, not changed by it.

Batch validation used targeted Go API, store and contract tests on a dedicated
PostgreSQL database, including the pinned Python SDK 3.13.0 creation-stream,
initial-input, self-hosted and initial-failure scripts, plus the TypeScript
client and Core Web unit tests. Resource-level replay, live model acceptance and
the full gate are recorded separately; retry, self-hosted, hosted and no-input
creation stream lifetimes have no official observation.
