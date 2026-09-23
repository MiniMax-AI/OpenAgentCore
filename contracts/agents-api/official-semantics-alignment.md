# Official wire semantics: September 22, 2026

This batch compares owned-resource requests to the official Agents API with Core
main `692e32daafb19521e0919915c7685eef78b813fa`. The protocol remains Python SDK
3.13.0, upstream `d7c41efee1b0802b79f3f88a678ef2052b06e9ce`, `agents=v1`.
The current [Session reference](https://developers.openai.com/api/reference/python/resources/beta/subresources/agents/subresources/sessions)
and [overview](https://developers.openai.com/api/docs/guides/agents-api/overview)
were consulted alongside the pinned source. Current documentation is not a
replacement baseline. A successful SDK parse alone is not conformance evidence.

## Selected common behavior

| Operation | Official observation and Core behavior in this batch |
| --- | --- |
| Create Agent, Vault, Credential, EnvironmentTemplate or Session | HTTP 201. Session JSON and live SSE creation use the same status. Non-creation successes retain their operation-specific status. |
| Submit Session events | HTTP 202 with an empty response body. An empty array is an authenticated no-op; null is invalid. No-op requests do not create a Turn, Item or execution retry reservation. |
| Session, Turn and Item lists | `object: list`, `data`, `has_more`, `first_id` and `last_id`. Empty pages contain null first/last IDs. |
| Empty Agent/Template update | Advance `updated_at` through the existing atomic update, preserving IDs, content, ownership and frozen Session snapshots. Timestamp precision is seconds; immediate updates may have the same serialized timestamp. |
| Static bearer create/replacement | Reject an explicitly empty token before mutation. Preserve valid opaque token bytes without trimming. |
| OAuth grant create/replacement | Reject an explicitly empty access token and a replacement without mutable grant fields. Preserve previously qualified refresh/expiry/null handling. |
| Input message discriminator | Omission remains valid; a supplied `type` must be `message`. Explicit null or empty strings reject through the shared initial/event decoder, matching the pinned literal type. |
| Missing beta resource | HTTP 404 with `type` and `code` equal to `not_found_error`. Missing and foreign resources remain indistinguishable. |
| Missing required Beta header | HTTP 400 with `type` and `code` equal to `invalid_beta`, after authentication. |
| Missing non-beta File or Skill | HTTP 404 with `type: invalid_request_error`, `code: null`. Exact message, File `param` and additional detail payload remain outside this batch. |

The resource comparison made 40 raw requests over six newly owned resources
(one Agent, two Vaults, two Credentials and one Template), including actual
rejected replacements and post-delete reads. All six resources were deleted.
Session probes used real `gpt-6-astra` executions and compared event admission,
query envelopes and accumulated usage; their owned Sessions and Agent were also
deleted. Separate missing File/Skill probes used randomly generated IDs.
Private request/status/body evidence and cleanup results are retained under
`~/.parsar/remediation/20260922/official-semantics-alignment/`; no API keys or
credential values belong in the repository or task board.

## Explicit remaining differences

- Current documentation supports Session Agent configuration updates; the fixed
  `SessionUpdateParams` exposes only metadata. New fields and newer Environment
  status/configuration shapes are a queued baseline upgrade, as approved by the user.
- The Session admission batch rejects missing/null input for `none` and for
  streaming creation outside `self_hosted`. September 23 official probes confirmed
  these conditions and idle self-hosted creation. Official whitespace-only string
  input returned 201; the
  [whitespace batch](#whitespace-only-message-text--september-23) admits it.
- Two otherwise identical official creates with the same `Idempotency-Key`
  returned 201 and distinct Session IDs. Core retains its durable creation retry
  guarantee. This is a local behavior, not evidence of official idempotency parity.
- Empty Session update now returns the observed 400 error; explicit metadata null
  and empty-object clearing remain supported. Generic validation codes/field `param`,
  malformed queries, page limits and overlapping mutation behavior need qualification.
  The error mapping above must not be extrapolated to every status or resource.
- Template references with inline installation overrides, optional Skill version
  semantics and the other active board entries remain outstanding.
- Native model defaults, tool combinations and unavailable usage counters retain
  their documented multi-harness differences. Core does not reconstruct model
  output, guess counters or introduce a second tool loop to manufacture equality.

These observations establish a bounded comparison, not complete official protocol
compatibility. Core regression and real-model validation are recorded with the
implementation acceptance before merge.

## Core acceptance

The deployed server/runtime at `7c80d604` passed seven real-PostgreSQL resource and
safety groups, including unchanged credential-row hashes after rejected writes
and restart. Codex and Claude Code used Kimi K3; MiniMax Code used MiniMax M2.7.
Each completed three real Turns covering JSON creation, live SSE creation and
event continuation, with history paging, empty no-op requests and tenant isolation.
Four earlier attempts were interrupted by failed test-network relays and retained
as unsuccessful evidence. After end-to-end TLS checks, the controlled rerun passed.

The server `make check` gate passed with Web checks run separately: type checks,
production build, 287 client tests, 573 Web tests and 74 fixture browser cases
(the corrected Beta-error fixture was rerun separately). Full standalone fixed
Python SDK and Go-client acceptance passed. A fresh Astra high full-diff review
found no blockers and independently ran API/contract tests. Rebase onto main
`c96ea82` preserved every batch patch; the combined tree passed API/execution and
three PostgreSQL scheduling regressions. Test resources were scoped to this batch.
E2B, OAuth provider refresh and new native capability combinations were not requalified.

## Session admission batch — September 23

The conditional input requirements are checked before creation lookup, credential
binding or execution. No idle-none legacy creation exception is retained; existing
Session GET and events remain available. Valid creation requests keep the local
same-key guarantee. An empty metadata update is rejected after authentication and
before resource lookup; supplied metadata still uses the existing tenant-scoped
update path.

Core Web requires initial input for conversation-only creation. Hosted creation
without input uses JSON; input-bearing creation retains SSE. If a creation stream
fails before revealing the Session ID, the next user-initiated retry sends the
same draft and key as JSON to recover that creation. It does not replay an input
or introduce an automatic retry loop.

The official probe made nine bounded create requests and created two owned
Sessions (self-hosted without input and none with whitespace). Both were deleted
successfully; no hosted environment was created. Metadata observations are reused
from September 22. Evidence: `~/.parsar/remediation/20260923/session-admission-alignment/official/`.
See [operation evidence](operation-evidence.md) for the wider 58-operation audit.
Real Core/daemon/native-model acceptance ran on production source `7ccc636`:
Codex and Claude used Kimi K3; MiniMax Code used MiniMax M2.7. Each completed one
JSON string-input Turn and one SSE ordered-message Turn, six total with no failed
attempt or rerun. Same-key JSON recovery retained each Session and exactly one
Turn. Twenty-one initial invalid creates plus three missing-input retries rejected
without adding rows to the eight checked execution tables. Empty updates preserved
metadata; null/empty clearing and foreign-tenant reads/valid updates were checked.
Hosted JSON no-input admission was retained for all three configured profiles;
self-hosted idle admission was checked on Codex only. Neither check claims a new
hosted or self-hosted execution capability.

Evidence is retained under
`~/.parsar/remediation/20260923/session-admission-alignment/live/`, including raw
HTTP, fixed-SDK responses, SSE, history, native outputs, exact source/image hashes
and cleanup. Six assistant results matched the requested markers. Claude emitted
two assistant Items for its two-message input within one Turn; native Item counts
were preserved. All 39 evidence hashes and known-secret scans passed. Owned
Runtime/Core processes, three databases, three derived images and the dedicated
network forward were removed, and temporary devices were revoked.

The integrated Web gate passed 287 client and 583 Web unit tests, type checks,
builds and all 76 fixture browser cases. These controlled UI checks include
empty-input prevention and same-key JSON recovery after a lost creation response;
they are separate from the real-model evidence. The server `make -o check-web check`
passed at `01f9356` with dedicated PostgreSQL, generated-query checks, Go service
and adapter tests/builds, and Rust tests/format/Clippy. The optional packaged
MiniMax native-tools probe was skipped; the separate real-model evidence above
qualifies this batch, not every native capability. Fixed Python SDK and Go-client
service acceptance also passed. The subsequent tool-policy evidence-count test
change compiled; its opt-in native run was not repeated.

A fresh independent Astra high reviewer inspected all 66 changed files and found
no grounded in-scope blockers; API/contract tests, 39 focused Web tests and whitespace
checks passed independently. Rebase onto main `6a3131e` preserved every batch patch.
The combined tree at `4981580` passed API, execution, contract and dedicated-PostgreSQL
Environment scheduling/initial-input/creation-stream regressions. No new E2B,
OAuth provider or native capability combination was qualified.

## Validation error fields — September 23

This batch aligns validation failures that Core already rejected with the
official `code` and `param` fields. It does not change any limit. Evidence comes
from the campaign scan at main `284cbcf`, recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-1/{vaults-agents,sessions,skills-files-templates}/findings.json`
(VA-07, VA-08, VA-09, VA-10, SES-28 and SFT-20), plus the September 22 Session
observation that `{"metadata":{"a":null}}` returns param `metadata.a`.

| Row | Case | Core behavior |
| --- | --- | --- |
| M1–M3 | More than 16 metadata pairs, a key over 64 characters, a value over 512 characters (Agent create/update, Session create/update) | 400 with type and code `invalid_request_error`, param `metadata` or `metadata.<key>`, and the observed official message with the actual count or length. Pairs are checked before keys and values, and keys in sorted order. |
| M4 | A non-string metadata value: integer, number, boolean, object, array or null (Agent create/update, Session create/update and Vault create; neither Core nor the pinned SDK has a Vault update) | 400 `invalid_request_error`, param `metadata.<key>`, message `Invalid type for 'metadata.<key>': expected a string, but got <kind> instead.` The first such value in document order is reported before the generic whole-body error. Templates accept no metadata. |
| M5 | Vault metadata size | Unchanged: no pair or length limits, only the local 64 KiB storage bound. |
| N1 | Agent `name` over 128 characters | 400 `invalid_request_error`, param `name`, observed message. Empty and untrimmed names stay accepted. |
| U1 | U+0000 in a stored string | Never 500 and nothing is written. Metadata keys and values report `metadata.<key>`; other strings return 400 `invalid_request_error` with a null param. This is a local limit: PostgreSQL text and jsonb cannot store U+0000, while the official service accepts and echoes it. |
| I1/I2 | A malformed path identifier on any Beta resource route, and on Files, Skills and Skill versions | Byte-for-byte the response of a well-formed missing identifier on that route, including invalid bodies and queries, and a deployment without credential encryption. Foreign, missing and malformed identifiers stay indistinguishable. |
| T1 | Template network rejections (wildcard, port, scheme, IPv6, empty host, empty/null/omitted list with `restricted`, more than 100 domains, domains with another access) and the shared inline Session network | 400 `invalid_request_error` with a null param. Accepted hostname forms are unchanged; other unsupported installation fields keep `unsupported_or_invalid_configuration`. |

Decisions:

- A typed field error carries the param and message through the existing error
  writer. Metadata type errors are found by reading the metadata object in
  document order before generic decoding; limit checks keep their previous
  position, so validation order relative to lookups (SES-33) is unchanged.
- U+0000 is checked explicitly in metadata, so the param is exact. All other
  stored strings rely on mapping PostgreSQL `22021` (U+0000 or invalid UTF-8 in
  a text parameter) and `22P05` (`\u0000` in jsonb) to 400 with the generic
  message "Request text contains characters this service cannot store or compare,
  such as U+0000 or invalid UTF-8." The same mapping covers query filters, for
  example `agent_id=%ff` on the Session list. The persisted string fields are too
  many to check one by one, and the database is the single place that knows which
  strings are stored. The failing statement aborts its transaction; real-PostgreSQL
  tests compare every public table before and after the rejected requests.
- A malformed path identifier resolves to the maximum UUID, which Core never
  assigns because it only generates version 4 and 5 UUIDs. The request then
  follows exactly the missing-identifier path, including body, query and storage
  checks. Routes whose lookup is the next check keep their direct not-found
  response. Request-body references are unchanged. Malformed list cursors were
  later aligned by the [list cursor error batch](list-query-semantics.md#list-cursor-errors--september-23-2026):
  Agent, Session, Turn, Template, Vault and Credential cursors take the same
  missing-cursor path, and Item, Subagent, Artifact and Skill version cursors
  return their list's official cursor error.
- Network messages are Core wording; the official prose is not copied.
- Documented message difference for M2: the official message abbreviated a
  65-character key as `'KKK...KKK'`. That single sample of identical characters
  cannot reveal the abbreviation rule, so Core quotes the full key. Status, type,
  code and param match.

Deferred and unchanged: accepting and storing U+0000; hostname forms accepted
officially (SFT-21) and `disabled` with domains, which the official service
accepts (SFT-22); non-canonical UUID spellings such as uppercase, braces or
`urn:uuid:` still resolve to the same resource; Skill sole-version deletion and
number reuse (since resolved or recorded in
[file resource semantics](file-resource-semantics.md#sole-version-deletion--september-23-2026));
Session deletion lifecycle; whitespace input (since addressed by the
[whitespace batch](#whitespace-only-message-text--september-23)); response defaults;
and the Files `limit=abc` code. The Environment Files list query parser is aligned
for unknown and repeated keys by the [Environment Files wire batch](environment-files.md#wire-alignment--september-23-2026);
it still rejects malformed query encoding locally.

Go handler tests cover every row. Real-PostgreSQL tests replay every path-ID
route for malformed, missing and foreign identifiers (tenant B), with valid and
invalid bodies and queries, and replay U+0000 on every create/update family with
a database digest proving no writes. The pinned-SDK acceptance scripts assert the
new codes, params and messages. Independent real-Core acceptance is recorded
separately by the coordinator.

## Artifact capture and listing — September 23

This batch aligns Session Artifact capture and listing with the first official
Artifact observations. Evidence comes from the hosted-environment campaign scan
recorded privately in `~/.parsar/remediation/20260923/campaign-scan-2/hosted-env/`
(`findings.json` HE-50..62, raw records under `official/` and `run1/`). The probe
used three owned Sessions and two tiny `gpt-6-astra` Turns; all three Sessions
were deleted. Official Turn 1 created regular, nested and empty outputs plus
`outputs/link.txt -> a.txt`; Turn 2 only wrote `outputs/c.txt` after one Artifact
was deleted.

| Row | Case | Core behavior |
| --- | --- | --- |
| A1 | A symlink below `outputs/` at Turn completion: to a file or directory, dangling, or pointing outside the workspace (HE-51) | Skipped by its `lstat` type: never followed, opened or resolved, and no Artifact. Every regular file is still captured and the Turn completes. |
| A2 | Later Turns in the same Session (HE-52) | A path is published again only when it has no remaining published Artifact in the Session, or its bytes (sha256) differ from the newest remaining one. Unchanged paths keep their existing Artifact IDs. The first Turn is unchanged. |
| A3 | List envelope (HE-53) | `object: list`, `data`, `first_id`, `last_id`, `has_more`, with null first/last IDs on an empty page, like the Session, Turn and Item lists. Paging and cursors are unchanged. |
| A4 | Malformed `environment_id` filter (HE-56) | 200 with an empty page, as for another existing Environment. Session lookup still runs first, so foreign and missing Sessions remain 404; cursor and limit errors are unchanged. |

Decisions:

- The Rust export helper handles every link kind the same way. Official evidence
  shows one relative link to a file; telling the other kinds apart would require
  resolving the link, which the confinement rules forbid. A link still counts as
  a directory entry, so creating or removing one during export is a concurrent
  change.
- The republication decision runs in the Turn's terminal transaction, not in the
  private capture transaction. Capture commits and releases the Session lock
  before the Turn completes, so an Artifact deletion can commit in between. The
  terminal transaction holds the Session lock that also orders Artifact deletion,
  and only one Turn per Session can be active, so the decision sees exactly the
  Artifacts that remain at completion. Unchanged staged rows are deleted and their
  private large objects unlinked in that transaction; published rows are never
  modified.
- "Newest" follows the producing Turn's database creation time, then its ID.
  Publication time can come from the Runtime's reported completion and is not a
  reliable order between Turns.
- Known difference from the batch plan's wording, accepted as a local decision:
  the plan republishes a path whose newest Artifact was deleted, but Core compares
  against the newest *remaining* published Artifact. Deletion is physical and
  leaves no record, and adding one would need a schema change outside this batch.
  Example: Turn 1 publishes `b.txt` as `bravo`, Turn 2 publishes `bravo-v2`, and
  the Turn 2 Artifact is then deleted. A later Turn whose `b.txt` is `bravo-v2`
  republishes it, because the remaining Turn 1 version differs. A later Turn whose
  `b.txt` is `bravo` publishes nothing, because the remaining Turn 1 Artifact
  already has those bytes. The official behavior for this case is unobserved.
- A malformed filter resolves to the never-assigned maximum UUID, as for
  malformed path identifiers, so it matches nothing without a database text
  comparison. An empty `environment_id=` still means no filter.

Deferred and unchanged: a linked `outputs` root, hard links, FIFOs, sockets,
devices and device crossings still reject the whole capture and fail the Turn
with `artifact_capture_failed`; there is no official evidence for them yet.
Republication after changed bytes is inferred rather than observed, and the
deleted-newest case above is unobserved. The unknown `after` cursor (HE-57)
was later aligned by the
[list cursor error batch](list-query-semantics.md#list-cursor-errors--september-23-2026).
Subagent lists keep their `data`/`has_more` envelope until there is official
Subagent evidence. Artifact IDs keep the Core UUID format. Paths removed from
the workspace keep their Artifacts.

Rust tests cover every link kind, including absolute links to a secret outside
the workspace and a relative link to a workspace file outside `outputs/`; an
inotify watch proves no target is opened or read, with a positive control. They
also keep the hard-link, socket, FIFO, linked-root and concurrent-change
rejections. Real-PostgreSQL store tests cover new, unchanged, changed,
changed-back, deleted-then-unchanged and deleted-during-capture paths, a deletion
that holds the Session lock while Turn completion waits, Turn-ordered newest
versions with inverted publication times, Session scoping and private object
accounting. Handler and real-PostgreSQL HTTP tests
cover the envelope, other, foreign and malformed filters, and foreign or missing
Sessions. The pinned-SDK and raw HTTP verifier used by live acceptance runs
against PostgreSQL across three Turns. Real Core, daemon and model acceptance is
recorded separately by the coordinator.

## Session deletion lifecycle — September 23

This batch aligns Session deletion with the observed official lifecycle rules.
Evidence comes from the campaign scan at main `beb18fd`, recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-1/sessions/findings.json` (SES-29
and SES-30) with raw records under `official/`: `q5-delete-repeat.json`,
`q5b-delete-while-in-progress.json`, `q5-delete-never-existed.json` and
`q5-delete-while-running.json`, plus the September 22 retry-session cleanup that
first returned 409.

| Row | Case | Core behavior |
| --- | --- | --- |
| D1 | DELETE of the caller's own Session that is already publicly deleted (SES-29) | 200 `{id, object: "agent.session.deleted", deleted: true}`, identical to the first confirmation, with no database write. GET, update, events, Turns and Items stay 404. |
| D2 | DELETE of a never-existing, malformed or foreign Session, including a foreign deleted one | Unchanged: the byte-identical 404 `not_found_error` of a missing Session. |
| D3 | DELETE while a root Turn is queued, in progress (including a requested cancellation) or waiting on required actions or function results, or while an input reservation is pending: a queued later input, self-hosted input awaiting a connection, or hosted initial input while provisioning (SES-30). Subagent child Turns and pending Environment file writes are not checked (see follow-ups) | 409 with type and code `conflict_error`, param null and message "session must be durably idle or failed without required actions before deletion". Nothing changes: no cancellation, marker, event, Artifact removal or Runtime cleanup. |
| D4 | DELETE of an idle Session, including an idle hosted Session still provisioning without input, and of a failed Session without required actions, including expired initial input | 200 with the existing public deletion and managed Runtime cleanup. |
| D5 | Callers that need to delete running work | Cancel first with `agent.session.input.cancel`, wait until the Session is idle, then delete. The Core Web offers this as an explicit action after a 409. |

Decisions:

- The rule is the one the creation stream already uses to settle: the Session is
  idle or failed, no root Turn is queued, running or waiting, and the latest input
  reservation is not pending. Deletion reuses the Store's active-Turn query and
  reservation state, so a pending reservation blocks deletion even while the
  public status projects idle.
- The decision and the marker commit in one transaction under the tenant Session
  row lock that also orders Turn and input admission. Either admission commits
  first and deletion returns 409 without mutation, or deletion commits first and
  admission returns 404. A rejected deletion rolls its transaction back.
- A repeated deletion locks the owner's deleted row and returns the confirmation
  without a write. Foreign and missing rows are never locked, so they stay
  indistinguishable. Physical purge, when implemented, may end this idempotency.
- Documented stricter local behavior: official DELETE immediately after an
  `events.create` 202 on an idle Session returned 200 (`q5-delete-while-running`);
  its Turn was apparently not yet durably in progress. Core admits the Turn
  synchronously in the 202 transaction, so Core returns 409 in that window.
- A self-hosted or hosted Session whose reserved input waits for its Environment
  cannot be cancelled publicly (the pending reservation rejects new batches), so
  it stays undeletable until the input starts, its five-minute deadline expires
  or its Environment fails. The official behavior of that window is unobserved.
- Capacity change: previously, deleting a provisioning hosted Session with
  reserved input released its sandbox node placement immediately. Now the
  deletion returns 409, and the placement keeps counting toward the node's
  retained and reserved capacity until the input is admitted or its five-minute
  deadline expires. A later allowed deletion releases an unallocated placement.
- Earlier releases deleted busy Sessions after requesting cancellation. Their
  markers can remain in upgraded databases; hidden-work settlement, restart
  reconciliation and Runtime cleanup keep handling them unchanged.
- The Core Web keeps the plain delete action. When Core returns the busy 409, the
  dialog reads the Session once. If a Turn is still busy it replaces the action
  with Cancel work and delete, which sends one cancellation, reads the Session
  until it is idle or failed without required actions (a 30-second bound checked
  between reads) and sends one deletion. A rejected or uncertain cancellation, a
  timeout, a connection change or another 409 stops without retrying. If the
  Session reads idle or failed, or only awaits its Environment connection, only
  pending input blocks deletion; Core rejects its cancellation, so the dialog
  explains that the input must start, expire or fail first and offers no
  cancellation.

Follow-up: deletion checks only root Turns and input reservations. A subagent child
Turn that is still running and a pending Environment file write do not block it,
which matches the permissive behavior before this batch. The official behavior for
both is unobserved; decide whether they should return 409 once it is sampled.

Unchanged: physical retention and purge (SESSION-CLEANUP-001 remainder), 404 for
reads of deleted Sessions, Artifact retention rules after deletion, managed Runtime
cleanup once deletion is allowed, and caller-owned self-hosted compute, which is
never reclaimed. No schema change.

Real-PostgreSQL HTTP tests replay D1–D4 across every busy and settled state with
exact bodies, tenant B requests and a whole-database digest proving that a 409
and a repeated deletion write nothing. Store tests race deletion against Turn and
input admission on one real row lock in both commit orders and concurrently, and
a Worker test cancels a waiting Turn through the daemon protocol before deleting.
Handler, pinned-SDK, TypeScript client and Web unit tests cover the error fields
and the cancel-then-delete flow. Real Core, daemon and model acceptance is
recorded separately by the coordinator.

## Agent configuration validation — September 23

This batch moves protocol validation of Agent configuration into Core with the
official error fields: saved Agent create and update bodies and the inline
`agent` on Session create. Evidence comes from the campaign scan at main
`beb18fd`, recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-3/subagents-tools/findings.json`
(TV-01..07) with raw official records in `official/validation-{B1,B2A,B2B,B3}.json`
and the Core replay under `core/`. The official probe used one owned Agent and 44
requests without a model: Agent updates, two Agent creates and nine Session
creates without input on `none`, so no Session or Turn could start. The Agent was
deleted and a read confirmed 404.

| Row | Case | Core behavior |
| --- | --- | --- |
| C1 | A missing required member, an unknown member, a wrong JSON type or an unsupported enum value in `tools[]`, `text`, `reasoning`, `service_tier`, `multi_agent`, `model`, `name` or `instructions`, including the unpinned `tool_choice` (TV-01) | 400 with type and code `invalid_request_error`, param set to the JSON path (`tools[0].parameters`; `agent.tools[0].parameters` on Session create) and the observed messages: `Missing required parameter: '<path>'.`, `Unknown parameter: '<path>'.`, `Invalid type for '<path>': expected <kind>, but got <kind> instead.`, `Invalid value: '<v>'. Supported values are: ...` with the pinned literals, and `Invalid '<path>': integer below minimum value. Expected a value >= 1, but got <n> instead.` |
| C2 | Repeated function name, more than one `web_search` or more than one `tool_search` (TV-02) | 400 `invalid_request_error`, param null: `duplicate function tool name: <name>`, `duplicate web_search tool`, `duplicate tool_search tool`. |
| C3 | Function `parameters` or `text.format` json_schema with an explicit string root `type` other than `object` (TV-03) | 400 `invalid_request_error`, param null: `Invalid schema for function '<name>': schema must be a JSON Schema of 'type: "object"', got 'type: "<t>"'.` and `agent.text.format.schema must have top-level type "object"; got "<t>"`, for every harness and before harness admission. |
| C4 | Session create on `none` without input and with an invalid inline agent (TV-04) | The configuration error first. Valid configurations, including enabled `web_search` or programmatic tool calling, still receive the input requirement. |
| K1 | Function names with any characters or over 64 characters, programmatic tool calling enabled on a saved Agent, reasoning effort `max`, service tier `flex` (TV-07) | Unchanged: saved and echoed. |
| K2 | Harness and execution admission limits: enabled `web_search` or programmatic tool calling, structured output on an unqualified harness, explicit reasoning or a non-`auto` service tier on Session create (TV-06) | Unchanged: `unsupported_or_invalid_configuration` with the existing messages, after protocol validation. |
| K3 | Saved `web_search` with mode `live`, `cached`, null or omitted (TV-05) | Unchanged: `unsupported_or_invalid_configuration`, "Only disabled web_search is qualified for execution." |

Decisions:

- A compact validator walks the raw JSON along the pinned shapes
  (`PersistedAgentToolParam`/`AgentToolParam`, `AgentTextParam`,
  `AgentReasoningParam`, `MultiAgentConfigParam` and the `service_tier` literal)
  and reports the first violation through the typed field error from the
  validation error batch. It runs before the existing parsers, which keep Core's
  local limits and codes, and before harness admission. It is not a JSON Schema
  engine: function and output schemas, `request_metadata` values, MCP `transport`
  members, `metadata` and `x_agents_core` stay with their existing parsers.
- In each object, a union's `type` is checked first. Unknown and repeated members
  are then reported in document order, followed by member values in document
  order and missing required members in the pinned order. The whole object is
  checked before the C2/C3 conflicts, and tools before `text`. The official order
  between several errors in one body was not observed.
- Member names match exactly, so a name that differs from a member only by case,
  such as `reasoning.Effort`, is an unknown parameter. A member repeated anywhere
  in the checked tree returns 400 `invalid_request_error` with its path as param
  and the local message `Duplicate parameter: '<path>'.`; the official response
  is unobserved. Both are needed because encoding/json matches names
  case-insensitively and merges repeated objects into the decoded structs:
  checking only the last copy let `"text"` given twice store an array-root schema,
  and `{"reasoning":{"Effort":"high"},"reasoning":{}}` store `effort: high`.
  Members left to their parsers are checked on the decoded values, so they cannot
  differ from what is stored.
- Observed expected-kind phrases are `an object`, `a boolean` and
  `an object with string keys and unknown value values`. At unsampled positions
  Core uses `a string` (also for enum members), `an integer` and `an array`, and
  reports a missing Agent create `model` and a non-object Session `agent` in the
  same forms.
- Caller-supplied member names, enum values, function names and schema root types
  are repeated only when they are at most 256 bytes of printable UTF-8, the
  Environment Files rule. Otherwise the error keeps its code and path param, or
  a null param for an unknown member, and drops the value: `Unknown parameter.`,
  `Invalid value. Supported values are: ...`, `duplicate function tool name`, or
  the schema message without the name or `got` clause.
- C3 rejects only an explicit string root type. Schemas without a root type, or
  with a non-string `type` such as an array, are unchanged; neither was sampled.
  The output schema message names `agent.text.format.schema` on Agent requests as
  well, as observed on Agent update; Agent create was not sampled.
- Session admission also applies C2 and C3 to the resolved saved configuration, so
  Agents saved before this batch cannot execute with such tools or schemas;
  replacing the field in the Session override admits them. An invalid inline
  override is reported before the saved-Agent lookup, so owned, foreign and missing
  Agents give the same response. Otherwise the lookup order (SES-33) is unchanged.
- Validation of the update body precedes the Agent lookup, so owned, foreign,
  missing and malformed Agent IDs give the same response.
- Inline agent validation runs before same-key creation recovery. A same-key
  retry of an inline Session created before this batch therefore returns the new
  400 if its original configuration is now invalid, instead of the original
  Session. Saved-Agent retries still recover, because the resolved-configuration
  checks run after recovery. Core is pre-release, so the order is not changed for
  such retries.

Deferred and unchanged: TV-05, saving `web_search` with mode `live`, `cached` or
omitted (officially saved, omitted stored as `live`), needs a separate decision
about saved-but-unqualified settings. Duplicate `programmatic_tool_calling`
declarations and MCP server labels were not sampled: saved Agents accept them and
Session admission keeps "Execution requires distinct tool controls." and
"Execution requires distinct MCP server labels.". A missing model without
`agent_id`, unknown top-level Session members, `max_concurrent_subagents` above
4294967295, nonblank and 512-byte function names and the 64-function Session bound
keep their local codes.

Go handler tests cover every C and K row on Agent create and update and on Session
create with and without input, the echo bounds and saved records from before this
batch. A real-PostgreSQL test replays the TV-01..03 rows on Agent create by two
tenants, on updates of owned, foreign, missing and malformed Agents, and on inline
and saved-override Session creates, with a database digest proving no writes; it
then saves and reads back the K1 values and checks tenant isolation. The
pinned-SDK acceptance scripts assert the new codes, params and messages. Real
Core, daemon and model acceptance is recorded separately by the coordinator.

## Whitespace-only message text — September 23

Core admitted user text only when it had a non-whitespace character; the official
service admits any non-empty text and stores it unchanged. Evidence comes from the
campaign scan recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-1/sessions/findings.json`
(SES-01..08) with raw official records under `official/`: `s1-create-string-spaces`,
`s4-create-string-newline-tab`, `s2-create-message-part-newline-tab`,
`e1-events-two-whitespace-messages`, `q2-items-l100` (user Item text `"   "`),
`p1a-create-missingagent-empty-string`, `e2-events-empty-content`,
`e3-events-text-emptystring` and `e4-events-empty-input`. The probed Sessions were
deleted.

| Row | Case | Core behavior |
| --- | --- | --- |
| W1 | Session create with string input `"   "` or `"\n\t"` (SES-01/02) | 201; the user Item keeps the exact text. |
| W2 | Session create with a message whose only `input_text` part is whitespace-only (SES-03) | 201; stored verbatim. |
| W3 | `events.create` message with whitespace-only `input_text` parts, including two such messages in one event (SES-04) | 202; one Turn, Items verbatim. String `content` or string `input` stay type errors, as observed officially. |
| W4 | Empty string, empty `content`, empty `input`, or a message whose text parts are all empty (SES-05..07) | Unchanged 400 `invalid_request` with the generic message and null param, without writes. The official responses use code `invalid_request_error`, specific messages and, for the empty create string, param `input`; aligning them is outside this batch. |
| W5 | A message with parts `["", "real text"]` (SES-08) | Unchanged: accepted and stored with the empty part. Official behavior is unobserved; its per-part error message suggests it may reject. |
| W6 | Native execution of a whitespace-only Turn on Codex, Claude SDK and MiniMax Code | Declared per harness through the engine profile. Codex admits and delivers the text unchanged; live acceptance at `898b197a` completed its whitespace-only Turns. Claude SDK and MiniMax Code are not qualified: a message without an image or non-whitespace text returns 400 `unsupported_or_invalid_configuration` at Session creation (including streaming and self-hosted creation) and `events.create`, before any write, reservation or promotion. Live evidence for MiniMax Code: after admission its native runtime refused the prompt with "Local message content or attachments are required." and the Turn failed with `engine_failed` (public `internal_error`). The Claude SDK admission rejection was confirmed live at `d88ffba6`. |

Decisions:

- `MessageInput.Validate` treats any non-empty text part as content and no longer
  trims. Image reference checks are unchanged. The rule applies wherever the
  validator runs: Core admission for create and events, Worker delivery, daemon
  steering and prepared start, and the Codex and MiniMax adapters.
- W6 reuses the engine profile that declares image placements: a
  `WhitespaceOnlyText` qualification checked with the other input profile rules
  during Worker admission, without engine-name branches in handlers. The Claude
  bridge and Anthropic-compatible providers reject text blocks without
  non-whitespace characters, and the MiniMax Code native runtime refuses such a
  prompt, so Core declares both combinations instead of failing the Turn or
  rewriting input. Whitespace beside non-whitespace text in one
  message stays admitted for every harness. Whitespace is one explicit set, the
  union of Go `unicode.IsSpace` and ECMAScript `String.prototype.trim`, used by
  both Core admission and the Claude bridge; a shared table test keeps them
  equal. The MiniMax native check is covered only by live evidence.
  Admission makes the bridge's own check unreachable. If such a steering message
  still reached the bridge it would report `input_rejected`, and Core would end
  the running Turn as before; the delivery lifetime is unchanged.
- The TypeScript client mirrored the old rule for event batches; it now rejects
  only messages whose text is empty. Core Web keeps its local nonblank composer
  and Start Session rules; they are a UI choice, not protocol validation.
- No schema, model output or image rule changes.

Follow-ups:

- Codex omits the `text` field of an empty text part (`omitempty` on its native
  input), so a W5 message `["", "text"]` may be rejected natively. It is recorded
  rather than changed here, because removing the tag would also add empty text to
  image parts.
- On Claude SDK, a mixed message such as `["   ", "text"]` is admitted and sends
  a whitespace-only native text block, and `["", "text"]` sends an empty block;
  the provider's behavior for such blocks is unverified.
- The Core Web composer trims leading and trailing whitespace from all sent text,
  not only blank sends. This is a UI choice; other clients' text is unchanged.

Go proto, dispatch, Codex and API handler tests cover W1–W5. Profile, error
mapping and real-PostgreSQL Worker tests cover W6 admission: Codex admits and
stores the text, and Claude SDK and MiniMax Code reject at none, streaming and
self-hosted creation and at events.create without writes. A real-PostgreSQL
test creates Sessions and submits events over HTTP, reads back the exact user
Item text, and proves the W4 rejections write nothing; the pinned-SDK initial
input script asserts the same. Real Core, daemon and model acceptance is recorded
separately by the coordinator.
