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
| Missing required Beta header | HTTP 400 with `type` and `code` equal to `invalid_beta`, before authentication (see [HTTP routing and response headers](#http-routing-and-response-headers--september-23)). |
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
- Files.create cannot tell a file written by an earlier Files.create from any other
  existing file, so both report the untracked-file message; see the
  [write semantics](environment-files.md#write-semantics--september-23-2026).
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
| K3 | Saved `web_search` with mode `live`, `cached`, null or omitted (TV-05) | Resolved by [Saved web_search modes](#saved-web_search-modes--september-23): saved with the official projection; Session admission keeps the K2 rejection. |

Decisions:

- A compact validator walks the raw JSON along the pinned shapes
  (`PersistedAgentToolParam`/`AgentToolParam`, `AgentTextParam`,
  `AgentReasoningParam`, `MultiAgentConfigParam` and the `service_tier` literal)
  and reports the first violation through the typed field error from the
  validation error batch. It runs before the existing parsers, which keep Core's
  local limits and codes, and before harness admission. It is not a JSON Schema
  engine: function and output schemas, `request_metadata` values, MCP `transport`
  members, `metadata` and `x_agents_core` stay with their existing parsers.
- In each object, a union's `type` is checked first. Unknown members are then
  reported in document order, followed by member values in document order and
  missing required members in the pinned order. The whole object is checked
  before the C2/C3 conflicts, and tools before `text`. The official order
  between several errors in one body was not observed.
- Member names match exactly, so a name that differs from a member only by case,
  such as `reasoning.Effort`, is an unknown parameter. This is needed because
  encoding/json matches names case-insensitively. It also merges repeated
  objects into the decoded structs; a key repeated anywhere in the body is now
  rejected earlier by the shared body gate with the official message (see
  [Request body parsing](#request-body-parsing--september-23)), which replaces
  this batch's local `Duplicate parameter: '<path>'.` error. Members left to their
  parsers are checked on the decoded values, so they cannot differ from what is
  stored.
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

TV-05, saving `web_search` with mode `live`, `cached` or omitted, was deferred
here and is resolved by [Saved web_search modes](#saved-web_search-modes--september-23).
Deferred and unchanged: duplicate `programmatic_tool_calling`
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

## Session input conflicts and result targets — September 23

This batch gives every 409 the official conflict type and aligns the conflict and
tool result target errors of `events.create`, from Core main `0035435a`. Evidence
comes from the campaign scans recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-4/errors/findings.json` (ERR-22 and
ERR-27, raw records in `official/results.json`: `sessB-message-while-running`,
`sessA-delete-while-waiting`) and
`~/.parsar/remediation/20260923/campaign-scan-2/events-tools/findings.json`
(EVT-11, EVT-12 and EVT-14, raw records in `official/calls-s2.json`:
`s2-result-unknown-call`, `s2-result-unknown-turn`,
`s2-result-duplicate-after-terminal`, `s2-result-changed-after-terminal` and
`s2-result-after-cancel`). Every observed official 409 has type and code
`conflict_error` and a null param.

| Row | Case | Core behavior |
| --- | --- | --- |
| CF1 | Every 409 response (ERR-27) | Type `conflict_error`. The code stays specific to the case (CF2–CF5). |
| CF2 | `events.create` input that the Session cannot accept in its current state: a tool result after its Turn was cancelled, or ended without a saved result (EVT-12), and any other Turn conflict on this route; a batch while earlier input still waits for admission, such as the reserved initial input of a provisioning hosted Session or of a self-hosted Session awaiting its connection (ERR-22) | 409 with code `conflict_error` and a null param. Core keeps its message "The Turn cannot accept this input in its current state."; pending input reports "Earlier input to this Session is still pending." (official: "session initial input is still pending"). |
| CF3 | A tool result that differs from the call's saved result, before or after its Turn ends (EVT-12) | 409 `conflict_error`, "The tool call already has a different result." |
| CF4 | Idempotency-Key reuse with a different body on Session creation or `events.create` | Unchanged local code `idempotency_conflict` and message, with type `conflict_error`. Request idempotency is a documented Core extension of these operations. |
| CF5 | Other Core-only conflicts: `sandbox_deployment_conflict`, `runtime_node_in_use`, `runtime_local_node_configured`, `environment_unavailable`, `environment_input_expired`, `environment_input_cancelled`, `runtime_history_unsupported`, and `turn_conflict` from an Environment file write while Session work or input is active | Codes and messages unchanged, with type `conflict_error`. |
| CF6 | A tool result whose `call_id` names no function call of the caller's own Session, with any `turn_id` (EVT-11) | 400 with type and code `invalid_request_error`, param null, "Unknown pending tool call." Nothing is written and the pending action is unchanged. |
| CF7 | A tool result for a call of the Session whose `turn_id` names another Turn, an unknown UUID or no UUID at all (EVT-11) | 400 `invalid_request_error`, param null, "The tool call belongs to a different Turn." Nothing is written. |
| CF8 | Any input to a missing, malformed or foreign Session | Unchanged: the byte-identical 404 `not_found_error`, whatever the result target. |
| CF9 | An identical tool result repeated before or after its Turn ends (EVT-14) | Unchanged: 202 without another application or event. The official repeated `item.added` is not copied. |

Decisions:

- The error writer selects type `conflict_error` from the 409 status, so later
  conflicts cannot drift. The Session input writer maps Turn conflicts to code
  `conflict_error`; other routes keep `turn_conflict`, because official conflicts
  there, such as an Environment file write during work, are unsampled.
- Result targets are resolved under the tenant Session lock after the Session
  lookup. A well-formed Turn ID is looked up in that Session and must own the call;
  otherwise the Session's own calls decide between CF6 and CF7. The `turn_id` is
  therefore no longer rejected as a malformed UUID before the Session lookup, and
  malformed, missing and foreign Sessions keep one 404. The decision reads only
  the caller's Session, so it reveals nothing about other Sessions or tenants.
- The official messages name the call or internal executor IDs ("Unknown pending
  tool call: <call_id>", "function call exec-... belongs to a different managed
  agent turn"). Core's messages are fixed and repeat neither caller input nor
  internal identifiers.
- Checks keep their order: request validation, the Session lookup, the retry
  lookup (CF4), for batches with a message the Environment file-write gate (a
  Turn conflict, also CF2 with the Turn message), the pending input gate (CF2),
  then each event in batch order. A
  batch sent while input is pending therefore returns the CF2 409 even when its
  result target is unknown; the official order between these errors is
  unobserved. An empty `turn_id` or a
  blank `call_id` remains the generic 400 `invalid_request`.
- The official pending-input sample is the asynchronous admission window of
  `none` initial input (ERR-22). Core admits `none` input synchronously and does
  not emulate that window; the same fields apply to Core's reserved hosted and
  self-hosted input, initial or later.
- The TypeScript client and Core Web did not branch on the old codes. The client
  documents that `isSessionDeletionConflict` classifies only a `deleteSession`
  failure, since input conflicts now share its code. Cores before this batch
  returned 409 `turn_conflict` or `idempotency_conflict` with type
  `invalid_request_error`, and 404 for unknown result targets; clients that span
  both should treat any 409 as a conflict, and a 400 on new Cores or a 404 on
  older Cores as an unknown result target.

Unchanged: the ERR-22 asynchronous admission window (an architectural difference),
Idempotency-Key semantics, Session-level 404 isolation, admission timing and the
schema. The pinned SDK still retries a 409 by default; the status did not change.

Go API tests pin the exact CF2–CF9 bodies and the conflict type of every Core-only
409 code. A real-PostgreSQL HTTP test replays CF2–CF4 and CF6–CF9 with tenant B
requests, missing and malformed Sessions, a rolled-back mixed batch, a
whole-database digest and Session reads proving that every rejection writes
nothing and keeps the pending action. Store tests cover target classification and
rollback. The pinned-SDK scripts `official_function_inputs.py`,
`official_pending_actions_native.py` and `official_session_creators.py` assert the
new fields, and TypeScript client and Core Web unit tests cover them. Real Core,
daemon and model acceptance is recorded separately by the coordinator.

## Saved web_search modes — September 23

Saved Agents now keep every pinned `web_search` mode, as the official service
does, from Core main `1eb60c27`. Evidence is TV-05 (official W01/W02) in the
campaign scan recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-3/subagents-tools/findings.json`,
and owned probes under `~/.parsar/remediation/20260923/saved-web-search/official/`
(`results.json`, `ledger.jsonl`): four owned Agents with the create records
`type-only`, `mode-null`, `mode-cached` and `mode-cached-full`, the update records
`update-disabled`, `update-omitted-low` and `update-live-domains-empty`, and a
`retrieve`, without a Session or model. All four Agents were deleted. A second
probe created two more Agents, both deleted and confirmed 404 afterwards:
`location-partial-omitted` (`{"city":"Paris","country":"FR"}` saved with null
`region` and `timezone`, `req_db41d2f6261b4abfb69465eafe719ab5`) and `location-empty`
(`{}` saved with all four keys null, `req_165d53b88445490b9146d8272c54134d`).

| Row | Case | Core behavior |
| --- | --- | --- |
| W1 | Agent create with `web_search` mode `live`, `cached`, null or omitted | 201. Omitted or null mode is saved as `live`, and omitted or null `context_size` as `medium`. `allowed_domains` keeps null versus `[]`. `location` stays null, or else includes `city`, `country`, `region` and `timezone`, with null for omitted keys, also for `{}`. |
| W2 | Agent update replacing tools with these forms, including a return to `disabled` | 200 with the same projection. Retrieve and list return the saved form. |
| W3 | Protocol errors in a `web_search` declaration | Unchanged: the C1 and C2 fields. |
| W4 | Session creation from a saved Agent with enabled search, without a Session `tools` replacement | Unchanged K2 rejection: 400 `unsupported_or_invalid_configuration`, "Only disabled web_search is qualified for execution.", writing no Session, Turn, Environment or reservation, for plain, streamed, self-hosted and hosted creation. Protocol errors and the C4 input requirement still come first. |
| W5 | The same creation with a per-Session `tools` replacement | Admitted as before; the saved search is not used. |
| W6 | Inline Session agent with enabled or omitted-mode search | Unchanged K2 rejection. |
| W7 | Explicit `disabled` search, saved or inline, including records saved before this batch | Unchanged, including the frozen Runtime control on all three harnesses. |
| W8 | Another tenant's `agent_id` | Unchanged: the same 404 as a missing Agent. |

Decisions:

- Saved Agents use a separate saved-form parser. Session admission re-resolves the
  effective tools with the unchanged execution parser, which admits only disabled
  search. This is the path saved enabled `programmatic_tool_calling` already takes
  (K1, K2). All creation modes share that admission, and Worker device selection
  and the final preclaim still refuse a non-disabled search control, so enabled
  search cannot reach dispatch.
- Same-key creation retries keep their rules. A retry recovers the earlier Session
  with its frozen disabled control only when that Session recorded its creation
  request hash, as current Sessions do. An older Session without that record falls
  through to admission and, like a new key, receives the 400.
- An inline agent passes the saved-form parser before execution admission, so its
  enabled search is now reported by the execution check, with the same message.
  When one configuration hits several execution limits, another limit, such as
  explicit reasoning, can be reported first, as for saved Agents.
- Saved tools keep the stored key order of other saved configuration; Session
  snapshots keep their own. Clients compare decoded values.
- The TypeScript client types the saved `web_search` declaration with its mode
  union; inline execution types are unchanged. Core Web shows saved search as a
  read-only tool. Its Session admission check mirrors Core: it starts Sessions
  from Agents with one well-formed disabled search and blocks enabled or
  omitted-mode search, which Core does not run.

Unchanged: execution qualification, Runtime controls, the schema and the
configuration validation errors. Enabling search execution needs its own
qualification.

Go handler tests cover the projection table and W4–W7 on every creation mode. A
real-PostgreSQL HTTP test creates, updates, retrieves and lists Agents with exact
tool bytes, rejects W4 on plain, streamed, self-hosted and hosted creation under a
whole-database digest, checks a same-key retry, admits W5 and checks tenant B.
The pinned-SDK scripts `official_agents.py` and `official_agent_update.py` assert
the saved projections and the admission rejection, and `official_tool_policy.py`
adds saved enabled search to its live rejection cases. Real Core acceptance is
recorded separately by the coordinator.

## MCP origin and credential selection — September 23

The minimal pinned-SDK MCP tool `{type, server_label, transport}` now works on
Core, and Session MCP credential selection projects and reports errors as the
official service does. Evidence is MV-01..03 in the campaign scan recorded
privately in `~/.parsar/remediation/20260923/campaign-scan-6/mcp-vaults/`
(`findings.json`, `REPORT.txt`, `official-ledger.jsonl`): owned Agents, three
owned Sessions and two Vaults with four static-bearer Credentials, all deleted
and read back 404. The error records are `ERR-UNATTACHED`
(`req_b687ac760c03451caa5973be8d65a3ae`), `ERR-URL-MISMATCH`
(`req_90009e0ba2e548ad88a30516faeec852`), `ERR-AMBIGUOUS`
(`req_18b4777d35844be9a5549c8e58fc8747`), `ERR-CREDENTIAL-BOGUS`
(`req_5ba08377d4a841c98849cd4649e6fcaa`) and `ERR-VAULT-BOGUS`
(`req_0274192656b549739951de213e16514a`); the origin default is
`req_4a99a59eba2445c4b9ae22e74667f2b8` and `req_108ecc7c779240528efccd5ad55eebed`.

| Row | Case | Core behavior |
| --- | --- | --- |
| M1 | HTTP MCP tool with omitted or null `connection_origin`, on a saved Agent, an inline Session agent or a per-Session replacement | Saved and projected as `"service"`. The stored and frozen configuration equals an explicit `service` declaration, so execution is unchanged. Explicit `"environment"` and other transports keep their rejection. |
| M2 | Session tool without an explicit `credential_id` whose attached credential was selected | Retrieve, list and the created, in-progress and idle event snapshots show the selected credential ID, also after that credential is deleted. Anonymous and unmatched tools stay null; explicit IDs are echoed as sent. |
| M3 | `credential_id` with omitted, null or empty `vault_ids` | 400 `invalid_request_error`, null param: "MCP credential_id requires an attached vault". |
| M4 | `credential_id` not in an attached Vault: missing, foreign tenant, another Vault of the tenant, or malformed | 400 `invalid_request_error`, null param: "MCP credential_id `<id>` was not found in an attached vault". Byte-identical for one ID across the missing, foreign and unattached cases. |
| M5 | Credential in an attached Vault for another URL | 400 `invalid_request_error`, null param: "MCP credential_id `<id>` does not match server_url `<url>`". |
| M6 | Several attached credentials match implicitly | 409 `conflict_error`, null param: "multiple attached vault credentials match MCP server_url `<url>`; specify credential_id". |
| M7 | Unknown or foreign Vault in `vault_ids` | Unchanged 404 `not_found_error`, "Resource not found." (the official message names the ID). |
| M8 | Order and writes | Inline agent protocol errors and the input requirement come first; selection precedes any write, and a rejection writes nothing. |
| M9 | Dispatch | Unchanged: frozen bindings, scoped recheck before decryption, fail-closed on missing keys or decryption, no anonymous fallback. |

Decisions:

- `<id>` and `<url>` are the request's values, repeated only under the shared
  bounded-echo rule (`internal/echotext`: at most 256 bytes of printable UTF-8);
  otherwise the message leaves the value out.
- Selection searches only attached Vaults, which must all belong to the caller.
  An explicit ID is found there by ID alone, so a missing, foreign or unattached
  ID yields one response, and only a credential of an attached Vault can report
  a server_url mismatch. The mismatch message repeats the tool's URL, not the
  credential's.
- The projection reads the frozen private binding of the tool's label and URL,
  and shows it only while the binding's Vault is among the Session's
  attachments. It exposes a credential ID only, never tokens or ciphertext.
  Stored configuration keeps the caller's null, so creation retries, recorded
  caller intent and dispatch are unchanged. Retries recover the original
  projection, also after deletion; a new creation can no longer select a deleted
  credential.
- A same-key retry that omits the origin recovers a Session created with the
  explicit form when the request has no recorded caller intent; with recorded
  intent (attached Vaults or credential references) it remains the local
  `idempotency_conflict`, as for any changed request.
- A deleted, previously selected credential is still admitted at later input and
  fails at dispatch (MV-04); that remains a separate batch.

Go tests cover the origin default, the projection and its private-binding
checks, and the typed store errors. A real-PostgreSQL HTTP test with tenants A
and B covers M1–M8, the byte-identical M4 responses with headers, a
whole-database digest over every rejection, M2 across creation, retrieve, list,
creation-stream and live events, deletion and retries. The pinned-SDK scripts
`official_mcp.py` and `official_mcp_credentials.py` assert the omitted origin,
the projection and the error fields. Real Core acceptance is recorded separately
by the coordinator.

## Hosted initialization failure — September 23

Hosted Environments that fail to provision now surface the failure as the
official service does, from Core main `e1970fd7`. Evidence is HI-01..04 in the
campaign scan recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-6/hosted-init/findings.json`, with
raw official records under `official/`: `006-S2-create-setup-exit3`,
`007-S2-events`, `009-S3-events`, `021-S2-env-after-failed`,
`024-S2-session-after-failed`, `025-S3-session-after-failed`,
`026-S2-input-after-failure`, `027-S3-input-after-failure`, `038-S2-delete` and
`041-S3-delete`. Two owned `openai_hosted` Sessions without a Turn failed, one on a
setup command that echoed a value and exited 3, one on a nonexistent Python
package; both were deleted. The batch plan is
`~/.parsar/remediation/20260924/hosted-init-failure/PLAN.md`.

| Row | Case | Core behavior |
| --- | --- | --- |
| H1 | Hosted initialization fails in any step | One transaction records the Environment failure, `agent.session.environment.failed`, `error` and one `agent.session.failed`. The Session reads `status: failed`, the stored safe reason as `error`, `required_actions: []` and the failure time as `last_active_at`; retrieve, list and the event snapshot agree. Pending input reserved for the Environment settles as failed exactly as before, captured in the same snapshot. |
| H2 | `environment.failed` payload | `error` is `{type: environment_error, code: environment_connection_failed, message: "The environment failed to connect."}`. |
| H3 | `error` event | `{type: environment_error, code: sandbox_error, message: <reason>, param: null}`. |
| H4 | Reason | `Failed to provision environment: script "setup_commands[i]" failed with exit code N`, and `script "Python package installation"` for Python packages; the official Python reason also appends raw pip output, which Core never copies. npm, system package, initial file and Skill labels are unverified. Other failures use `Failed to provision environment: initialization did not complete`; see the [initialization lifecycle](environment-templates.md#initialization-failure--september-23). |
| H5 | Live SSE | GET and creation streams end right after that `agent.session.failed`. |
| H6 | Later `events.create` | 409 `conflict_error`/`conflict_error` "the hosted environment failed to provision", param null. Expired Environments, and input already waiting when the Environment failed, keep 409 `environment_unavailable`. |
| H7 | Delete | 200 `agent.session.deleted`, as officially, then 404. Deletion while provisioning is unchanged (HI-05 awaits a decision). |
| H8 | Unchanged | `self_hosted` and `none` Environments, successful initialization and its timing, the two-minute step limit (HI-06), expiry and tenant isolation. |

Decisions:

- **No output.** The shared Runtime initializer adds only an integer `exit_code`
  to its failed receipt, and only for a step run inside its bwrap isolation;
  Runtime helpers, signals and other errors keep the generic receipt, and the
  exception is never serialized. Core confirms a failed step only when the
  process exits 1 with empty stderr and a version-1 `failed` receipt. The
  decoder is deliberately lenient for older images and ignores other fields; the
  only value ever taken from the receipt is `exit_code`, and only as an integer
  from 1 to 255. The Store composes the reason from a fixed step label and
  integers, so commands, env values, package names, paths and process output
  cannot reach the reason, events, logs or responses.
- **Storage.** The additive migration `000062_environment_failure.sql` adds the
  nullable `environments.failure_reason` and `failed_at`; a check ties them to
  `status = failed` and bounds the reason to 256 characters. Environments that
  failed earlier keep NULL and their previous projection and events; new input
  on them gets the H6 409.
- **Runtime images.** A Runtime image built before this change reports no
  `exit_code`; its failures use the generic reason and otherwise follow H1–H7.
  The Codex, Claude and MiniMax Code images must be rebuilt for exit statuses.
- **Scope of the terminal state.** Only a recorded hosted provisioning failure
  makes the Session terminal. A Turn failure still leaves GET streams open, and
  a GET stream opened after the failure stays open; that case was not observed.
- **Clients.** Official-shaped `error` events carry `param: null`; Core's own
  `stream_interrupted` frame keeps its three-field error without `param`, so
  released clients still report it as an interruption. The TypeScript client
  delivers error events other than Core's `stream_interrupted` to `onEvent`
  before the failed snapshot, instead of raising them. Core Web already renders
  the failed Session, its error and the blocked input.

Go store tests on a dedicated PostgreSQL database drive the managed Worker with a
controlled Provider through setup exit statuses (first and later command),
Python packages, a receipt without `exit_code`, an unknown effect, raw output
instead of a receipt and a failed initial file write. They check the Session
read, list, the exact three events and snapshot, the H6 rejection, pending-input
settlement, tenant B and the absence of a canary. A real-PostgreSQL HTTP test
checks retrieve, list, the live GET stream and its end, the exact 409, tenant B
404s, delete and the canary in every body. Go API and contract tests pin the
projection, stream lifetime, wire shapes and error mapping; Python tests pin the
initializer receipt, and the TypeScript client and Web unit tests pass. Live
Docker acceptance is recorded separately by the coordinator.

## HTTP routing and response headers — September 23

This batch aligns path handling, the Beta check, 401 envelopes and response
headers with campaign scan 6 at Core main `1eb60c27`, recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-6/http-protocol/` (`findings.json`
HP-02..24, raw `official-ledger.jsonl` and `REPORT.txt`): 90 official requests on
one owned Agent, deleted afterwards, without a Session or model. Labels below are
ledger records.

| Row | Case | Core behavior |
| --- | --- | --- |
| RH1 | `//`, `.` or `..` path segments (HP-17: `R10`, `R11`, `R17`, `R18`) | Served on the canonical path, never redirected. Empty and dot segments resolve with ServeMux semantics and a trailing slash is kept, so `/v1/agents/x/../` still reaches the trailing-slash 404. The former 301 made the pinned SDK resend an update as a GET and drop it. |
| RH2 | A percent-encoded unreserved character in the path (HP-18: `R12`) | Decoded before routing, including `%2E` dot segments. The canonical path is built from the request's own path spelling; bytes that are invalid in an escaped path (such as `{`, `"`, a backslash or non-ASCII) are percent-encoded first. Other escapes, such as `%2F`, `%2f`, `%5C` and double encodings, stay encoded and never separate segments. Malformed, missing and foreign IDs keep the single 404. |
| RH3 | HEAD on a GET route (HP-19: `R13`, `R16`) | The GET route runs after the same Beta and authentication checks; 200 with its headers and no body. `Content-Length` is present when Go buffers the whole body (about 2 KiB) and omitted for larger responses. The events stream, the File, Skill, Skill version and Artifact content downloads, and the live Environment Files directory list answer HEAD with Core's 405 instead, so HEAD never holds a stream open or reads content. That exclusion is a documented Core difference; official HEAD on those routes is unobserved. |
| RH4 | Unsupported method (HP-20: `R04`–`R06`) | Unchanged 405 JSON `unsupported_operation`, now with `Allow` listing the route's methods in the observed order, such as `GET,HEAD,POST,DELETE`. Routes outside the Beta group, such as `/healthz` and executor credentials, and methods chi does not know, such as `FOO`, now use the same JSON 405 instead of chi's empty one; an unknown method is answered before the Beta and authentication checks, as before. |
| RH5 | `X-Request-Id` (HP-23) | Every response of the Agents API handler, including 400, 401, 404, 405 and SSE streams, carries a fresh random `req_` plus 32 lowercase hex characters. The ID is attached to the request log context as `request_id` next to the trace carrier. |
| RH6 | No or invalid credentials without OpenAI-Beta on a Beta route (HP-05: `A06`, `A11`) | 400 `invalid_beta`: the constant Beta check now precedes authentication. Files, Skills and Core project extensions still ignore the header. |
| RH7 | Repeated OpenAI-Beta header lines (HP-03: `B08`) | 400 `invalid_beta` unless there is exactly one field value, equal to `agents=v1`. |
| RH8 | 401 (HP-07: `A01`–`A04`, `A07`–`A10`) | Type `invalid_request_error`. Beta routes report a null code for every failure. Files, Skills and Core project extensions report a null code without a Bearer credential (missing, other scheme, empty or repeated header) and `invalid_api_key` for a rejected one, including mismatched scope headers. Core's message and `WWW-Authenticate: Bearer` are kept. |
| RH9 | `invalid_beta` message (HP-02: `B01`) | "To access the Agents API, set the 'OpenAI-Beta' header to 'agents=v1'." |
| RH10 | Optional headers (HP-24) | `OpenAI-Version: 2020-10-01`, `OpenAI-Processing-Ms` and `X-Content-Type-Options: nosniff`. Organization and project headers are not reported: Core's project scope is configured, not account-derived. |
| RH11 | Trailing slashes and unknown sub-routes (HP-21), OPTIONS and CORS (HP-22), `Cache-Control` and `traceparent` (HP-26), `agents=v0` (HP-04) | Unchanged: 404 JSON, no CORS handling, Core's extension headers kept, `agents=v0` still rejected (an upstream anomaly, not copied). |

Decisions:

- One canonicalizing handler wraps the complete server handler in both server
  configurations: around the ServeMux that also serves daemon, enrollment and node
  transport, and around the API router when it is served alone. The ServeMux, the
  router, every middleware, authentication check and handler see only the
  rewritten path. A dirty or encoded path therefore reaches exactly the route
  group and authentication of its canonical path written literally; internal
  daemon, node and sandbox routes keep their own authentication. The input is the
  request's own path spelling (`RawPath` when Go keeps one), never a path
  re-escaped from its decoded form, which would turn `%2F` into a separator when
  the spelling holds a byte Go considers invalid. `Path` and `RawPath` are then
  set consistently, so chi, which prefers `RawPath`, and the ServeMux, which uses
  `EscapedPath`, route on the same string. Decoding only unreserved characters is
  RFC 3986 normalization, so a proxy that normalizes URIs the same way sees the
  same route. The ServeMux still redirects the exact daemon prefix
  `/api/v1/agent-daemon` to `/api/v1/agent-daemon/`; that is daemon transport, not
  an Agents API path.
- The Beta check reads only a constant header and returns no tenant or resource
  data. Moving it first changes only responses that were rejected either way:
  every request that passes it is authenticated before the router reaches any
  Beta handler, 404 or 405.
- Wrong methods and unknown sub-routes below `/v1/files` and `/v1/skills` still
  reach the Beta group's 404 and 405 after its checks, as before; without the Beta
  header they now report `invalid_beta` instead of 401. Official behavior there is
  unobserved.
- Every 401 of the Agents API handler has type `invalid_request_error`, including
  the deployment administrator (`invalid_admin_key`) and sandbox node
  (`invalid_node_credential`) extensions, whose codes are unchanged. Project API
  key management keeps its deployment administrator authentication behind the
  same canonical path; derived project keys authenticate exactly as their static
  parent binding, under the Beta and Files rules above. Daemon,
  enrollment and node transport served beside it keep their own formats.
- The response headers belong to the Agents API handler. Daemon, enrollment and
  node transport routes do not carry them, and the shared log middleware is
  unchanged. A caller-supplied request ID is not echoed; that header is not pinned.
- Core Web recognizes both the current 401 envelope and the older
  `invalid_api_key` code in its connection probe. The TypeScript client already
  exposes status, type and code without branching on them. The Parsar product
  repository has no code branching on these 401 fields, and its Go client refuses
  redirects.

Go handler tests cover RH1–RH11, including a walk over every registered route:
unauthenticated requests, with and without the Beta header and with foreign
credentials, are rejected before any handler, and ten dirty and encoded spellings
of each path, including traversal from the daemon and sandbox prefixes, give the
clean path's exact response. Raw request-line tests over a real listener cover
invalid bytes, non-ASCII, `%2F`, `%2f`, `%5C`, double encoding and absolute-form
URIs in both server configurations, and two fuzz targets assert that any request
path reaches the same handler, route group and response as its canonical form,
with chi, the ServeMux and `Path` agreeing on it. A server test replays the
daemon-enabled composition and checks that no Agents API request is redirected. The pinned-SDK script
`official_http_routing.py` updates an Agent through base URL `/v1//`, checks
`_request_id` and the error `request_id`, and the raw checks in the other official
scripts now expect the Beta check first.

## Request body parsing — September 23

Every Agents API JSON request body now passes one shared gate, before any
route-specific decoding, validation or lookup, with the official parse semantics.
Evidence is HP-09..HP-15 of the HTTP protocol campaign scan, recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-6/http-protocol/` (`findings.json`,
`REPORT.txt`, raw requests in `official-ledger.jsonl`, labels `C01`–`C20`,
`U01`–`U11`, `S1`): one owned Agent, created, updated and deleted (404 confirmed),
without a Session or model. The official records cover Agent create and update;
the other routes are assumed to share the official parser. Two later owned probes
in `~/.parsar/remediation/20260924/http-json-body/official/results.json` created
nothing: a lone high surrogate escape (`req_1a9b7680d615454ca97c816b25e2f401`) and
Agent create with `metadata` and `Metadata` but no `model`
(`req_6ba2a50c71a4410f87a1baac855e82df`).

| Row | Case | Core behavior |
| --- | --- | --- |
| B1 | Malformed JSON, trailing data, two concatenated values, a UTF-8 byte order mark, a whitespace-only body (`C01`, `C06`, `C07`, `C12`, `C20`, `U01`, `U05`, `U06`), or a string escape that forms a lone or mis-paired UTF-16 surrogate, such as `"\ud800"`, in a key or value (`req_1a9b7680d615454ca97c816b25e2f401`) | 400, type and code `invalid_request_error`, param null: "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)". Agent update no longer returns `unsupported_or_invalid_configuration`. Valid surrogate pairs are accepted; lone surrogates were previously stored as U+FFFD. |
| B2 | Invalid UTF-8 anywhere in the body (`C13`) | 400 with the same fields: "Invalid body: encountered a unicode decode error when parsing this JSON value. Please check the value to ensure it is valid unicode." Previously the bytes were stored as U+FFFD. |
| B3 | A repeated object key at any depth (`C08`, `C15`, `C16`, `U07`) | 400 with the same fields: "Invalid body: duplicate JSON key '<key>' at '<path>'. Duplicate JSON keys are not supported." The path joins object keys with `.` and omits array indices: `name`, `metadata.k`, `tools.type`. Keys compare after unescaping and case-sensitively: `metadata` and `Metadata` are distinct keys (`req_6ba2a50c71a4410f87a1baac855e82df`). The first repeat in document order is reported. Previously metadata, Vaults and Templates kept the last value, and Agent configuration returned the local "Duplicate parameter". |
| B4 | A valid root that is not an object (`C05`) | 400 with the same fields: "Invalid type: expected an object, but got <kind> instead." with the existing kind phrases (`a string`, `an integer`, `a number`, `a boolean`). |
| B5 | A zero-length body or `null` (`C02`, `C03`, `U02`, `U03`) | Treated as `{}`: Agent create reports the missing `model`, Agent update is the documented empty update, Session update keeps its "At least one update field is required" rejection, and Vault create creates an unnamed Vault. A whitespace-only body stays B1. |
| B6 | Content-Type missing, `text/plain` or form-encoded, including a bodyless POST without Content-Type (`C09`–`C11`, `C19`, `U08`–`U10`) | 400 with the same fields, "expected request with Content-Type: application/json", checked before the body is read. `application/json` and `application/*+json` are accepted case-insensitively, with parameters (`S1`, `U11`, `C17`, `C18`); a malformed media type, such as `application/foo bar+json` or a conflicting repeated parameter, is rejected the same way. Previously Core ignored the header and applied the update. |
| B7 | Valid bodies | Unchanged, including unknown-member errors, configuration validation, route body limits (413) and Core extensions such as `x_agents_core`. |

Order: authentication and Beta handling as before, then B6, then the route's body
limit, then B2, B1, B3 and B4/B5, then route validation. The gate covers Agent
create and update, Vault create, Credential create and update, Template create
and update, Environment Files create, Session create and update, and Session
events. Environment Files create now reads its body before the Environment
lookup; a missing Environment with a valid body still returns 404.

Decisions:

- An array root is rejected with the B4 message. The official service treats `[]`
  as `{}` (HP-14); that upstream anomaly is not copied.
- The duplicate key and path are repeated only when each is at most 256 bytes of
  printable UTF-8, the shared `echotext` rule; otherwise the message is "Invalid
  body: duplicate JSON key. Duplicate JSON keys are not supported."
- The gate scans each body once in linear time and keeps key positions in the
  body, not copies: objects with more than 16 keys use an open-addressing set of
  8-byte slots, a position and 32 hash bits that skip comparing unequal keys. A
  body of many short keys allocates about twice its size in the gate. Bodies are
  read into a doubling buffer, which allocates two to four times the body in
  total (four near the route limit), against 4.4 to 6.1 times for `io.ReadAll`.
- Member names match exactly on every gated route. encoding/json would match a
  case variant such as `Metadata`, `Input` or a nested `Role` to the field and
  let the last copy win; such a key is now an unknown member at any depth,
  rejected with the route's existing unknown-member error before any write.
  Agent configuration already did this. On Agent create, a body with `Metadata`
  and no `model` still reports the unknown member first, while the official
  service reported the missing `model`; the official order between several
  errors in one body remains unaligned.
- A walk over the body bytes checks member names before a decoder runs and stops
  at the first unknown or case-variant key, and the Session metadata check reads
  only the `metadata` member. For unknown top-level keys this makes rejection
  cheap: a whole 16 MiB Session create allocates about 96 MiB and events about
  six times a 1 MiB body, instead of 482 MiB and 9 MiB before this batch, when
  the decoder formatted an error for every unknown key. Unknown keys nested under
  `agent` or `environment` still pass the existing object decoding of
  `decodeInputObject` and stay linear, at or below main: about 779 and 871 MiB
  for 16 MiB bodies, against 850 and 889 MiB on main.
- Invalid UTF-8 is checked before JSON syntax; the official order for a body with
  both faults was not observed.
- Not gated: DELETE routes, which keep their empty-body rule, the multipart Files
  and Skills uploads, Skills update (a non-Beta API with its own observed error
  fields), the Core extension `/core/v1/*` routes, including executor credential
  issuance, and the internal daemon, sandbox and node routes.

Unchanged: the schema and the route validation and error codes of valid bodies.

Caller check: the TypeScript client sends `application/json` with every JSON body
(an empty Agent update sends `{}`), Core Web uses that client, the Go client uses
the pinned openai-go SDK, the Python acceptance tools send JSON through the pinned
SDK or `json=`, and the documentation has no JSON POST examples. The Parsar
product repository does not call these routes.

Go tests cover the gate on its own (B1–B5, surrogate escapes, the echo bound,
media type parsing, deep bodies, a differential check of the duplicate-key scan
against an encoding/json token walk on small and large objects, and an allocation
bound on many short keys), case-variant members, route-level allocation bounds for
unknown keys on Session create and events, and all eleven route families
(B1–B4, B6, the order against authentication, Beta and the body limit, B5/B7). A
real-PostgreSQL test sends B1–B4, B6 and case-variant members to every route
family as the owner and as tenant B under a whole-database digest, then checks
B5/B7 writes; another exercises the excluded DELETE, Files and Skills upload,
Skills update and executor credential routes with real storage. The pinned-SDK
scripts `official_agents.py`, `official_agent_update.py`, `official_vaults.py`,
`official_credentials.py`, `official_credential_rotation.py` and
`official_session_metadata.py` assert the official messages and recount or reread
the resources to show no writes; `official_session_requests.py` rejects
case-variant members without creating a Session.
Independent acceptance is recorded separately by the coordinator.
