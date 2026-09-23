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
  these conditions and idle self-hosted creation. Existing blank-text validation
  remains stricter: official whitespace-only string input returned 201. This
  newly found difference is queued rather than expanding the admission batch.
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
  response. Malformed list cursors and request-body references are unchanged:
  Session, Turn, Item, Subagent, Artifact, Agent, Vault and Credential cursors
  still return 400 `invalid_request`, and Template cursors keep their existing
  not-found response.
- Network messages are Core wording; the official prose is not copied.
- Documented message difference for M2: the official message abbreviated a
  65-character key as `'KKK...KKK'`. That single sample of identical characters
  cannot reveal the abbreviation rule, so Core quotes the full key. Status, type,
  code and param match.

Deferred and unchanged: accepting and storing U+0000; hostname forms accepted
officially (SFT-21) and `disabled` with domains, which the official service
accepts (SFT-22); non-canonical UUID spellings such as uppercase, braces or
`urn:uuid:` still resolve to the same resource; Skill sole-version deletion and
number reuse; Session deletion lifecycle; whitespace input; response defaults;
the Environment Files list query parser; and the Files `limit=abc` code.

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
belongs to ERROR-PROTOCOL-001. Subagent lists keep their `data`/`has_more`
envelope until there is official Subagent evidence. Artifact IDs keep the Core
UUID format. Paths removed from the workspace keep their Artifacts.

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
