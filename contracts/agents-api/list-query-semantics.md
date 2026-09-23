# List query semantics — September 23, 2026

The fixed target remains openai-python 3.13.0, commit
`d7c41efee1b0802b79f3f88a678ef2052b06e9ce`, with `agents=v1` on beta resources.
This batch starts from Core main `c86b5bb` and addresses list order validation and
the error fields actually observed for those requests. It does not change page
limits, cursor lookup order, execution or native harness behavior.

## Official observations

Exactly 35 new read-only official GET requests were made, with no retries, resource
writes or model execution. Nested requests used random missing Session/Vault IDs;
top-level requests used missing cursors or a nonexistent Agent filter. Every
successful list was empty. Evidence, request IDs, fixed type copies and the full
matrix are retained under
`~/.parsar/remediation/20260923/error-query-survey/`. Earlier owned Session/Turn
observations are separately identified in that directory, not counted as new calls.

The nine sampled collections rejected explicit `order=`. The fixed enum permits
only `asc` or `desc`; omission and an explicitly empty string are different inputs.

| Measured request | Status | Error type | Code | Param |
| --- | --- | --- | --- | --- |
| Empty order on Agents, Sessions, Turns, Items, Templates, Vaults, Credentials | 400 | invalid_request_error | invalid_request_error | null |
| Empty order on Files | 400 | invalid_request_error | null | null |
| Empty order on Skills | 400 | invalid_request_error | invalid_value | order |
| Invalid status on Vaults/Credentials | 400 | invalid_request_error | invalid_request_error | null |

These are operation-specific observations. Do not apply the Skills code to all
validation errors or make every Files error share one parameter. Other endpoints
using the same order enum may share the parser, but were not independently probed
here. Authentication and ownership checks retain their existing boundaries.

The fixed Python SDK's query serializer drops empty string values. Consequently,
`list(order="")` does not send `order=` and follows omission/default behavior.
Explicit-empty rejection requires raw HTTP evidence; nonempty invalid SDK order
values exercise the rejected path normally. Do not alter Core or the SDK to hide
that request-serialization distinction.

## Deferred differences and uncertainty

- Query limits require separate qualification. Missing cursors or parents can mask
  a later numeric validation error. A successful filtered-empty request does not
  establish useful zero-sized pagination or an actual maximum page capacity.
- An owned official Item list accepted 101 although its pinned type documents
  1–100; an owned Turn list rejected 101. Keep the fixed range until the conflict
  is resolved explicitly. No general range change follows from this survey.
- Vault/Credential negative limits rejected on the official service, while the
  pinned description broadly says values clamp to 1–100. Core's clamping policy
  is unchanged in this batch.
- Files invalid purpose and missing cursor expose different `param` values;
  purpose typing and cursor behavior need a separate bounded decision. The
  verbose Files validation message and additional `detail` object are not a
  reason to recreate an upstream validation framework.
- Two malformed Skills cursor observations returned 500, while a shaped missing
  cursor returned 404. Retain the evidence; do not deliberately reproduce an
  upstream failure or weaken safe missing-resource handling.
- Unknown/repeated query keys, whitespace cursors, numeric overflow, all status
  combinations and concurrent-page mutation were not newly qualified.

Current documentation was consulted alongside the pin, including the
[Session list reference](https://developers.openai.com/api/reference/go/resources/beta/subresources/agents/subresources/sessions/methods/list)
and [Vault list reference](https://developers.openai.com/api/reference/typescript/resources/beta/subresources/agents/subresources/vaults/methods/list).
New documentation and sampled tolerance do not silently replace the fixed SDK.

## Acceptance boundary

Acceptance must exercise fixed-SDK and raw HTTP requests against the actual Core
service and a dedicated PostgreSQL database: rejected queries, omitted/valid order,
scoped history and pagination, authentication, tenant isolation and no mutation
from rejected GETs. Controlled tests support the same contract. This read-only
change requires no new native-model capability qualification; existing real-model
evidence retains its original scope. Record completed checks and independent review
before merging; neither a deserializable response nor a route inventory proves
complete compatibility.

## Completed acceptance

The nine-family fixed-SDK/raw-HTTP regression passed against actual Core HTTP,
PostgreSQL and Worker admission with dispatch paused. It checked 35 primary
rejections, nine SDK empty-query omission cases, successful ascending/descending
and default pagination, authentication and foreign-tenant masking. Resource
snapshots and the original three cancelled Turns/three Items remained unchanged.
The baseline failed at raw empty-order admission; the implementation passed.
This is resource/query acceptance, not native or model execution. Logs, exact
SDK serializer source and hashes are retained in the survey directory's
`ACCEPTANCE.md`; its owned database was removed.

Server `make -o check-web check` passed at `e4cb8a5`, including the dedicated
PostgreSQL regression, sqlc regeneration, service/adapter Go tests and builds,
Claude/MiniMax checks and Rust tests/format/Clippy. Web/client source and dependencies
are unchanged from PR #35: its 287 client tests, 583 Web tests and 76 browser cases
remain the applicable exact-source evidence; no new Web run is claimed. The
optional packaged MiniMax native-tools probe was skipped. This batch changes no
Runtime/provider/model behavior and does not requalify their combinations.

A fresh independent GPT-6 Astra high reviewer found no grounded in-scope findings
after inspecting the full diff and evidence; API tests and `git diff --check`
passed independently. Server logs are retained under
`~/.parsar/remediation/20260923/list-query-alignment/`. The remaining limits, cursor,
lookup and Files verbose error differences above are not declared compatible.
