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

## List query tolerance — September 23, 2026

The pin is unchanged: SDK 3.13.0, commit `d7c41ef`, `agents=v1`. This batch starts
from main `284cbcf` and aligns query-string handling with owned official observations
from campaign scan 1. Findings with request IDs are retained in
`~/.parsar/remediation/20260923/campaign-scan-1/{vaults-agents,sessions,skills-files-templates}/findings.json`;
the batch plan is `~/.parsar/remediation/20260923/list-query-tolerance/PLAN.md`.
Parts of the deferred list above are now qualified; the remaining parts stay
deferred below.

| Row | Case and families | Core behavior | Evidence (finding: request ID) |
| --- | --- | --- | --- |
| A1 | Unknown list key: Agents, Sessions, Turns, Items, Templates, Vaults, Credentials, Skills, Skill versions, Files, Subagent and Artifact lists | Ignored; the page equals the request without the key | VA-02: `req_03f2f965efd54bfab3d22d079121e604`, `req_00357eb8b919413483dba9b59fb18174`; SES-17: `req_8186446b7f68412b8e80e6ec03f8d6d0`; SFT-11: `req_89660f3432624f57afe2c103356fec45` |
| A2 | Unknown key on single-resource GET/POST/DELETE routes | Ignored, including `tenant_id` and `include`; missing and foreign resources still return the same 404 | VA-02: `req_29b59b9bf58a482ca00222b86b542c8a` (deleted Vault read), `req_1aa8acb5602f4a09b7043885d42fca68` (deleted Agent delete) |
| B1 | Repeated supported key on Beta lists, including scalar `status` | 400, code `invalid_request_error`, param null, ``Failed to deserialize query string: duplicate field `<key>` `` | VA-03: `req_83ee26a0b9ba4e84af8996a9346f261b`, `req_de6a02d9a9c547e490e6e53aeb45544d`, `req_92b924b186cf48488cf625638130bb16`; SES-16: `req_86ab2f32451a426399e76b21debeadba`; SFT-24: `req_794a86f4e72946dfb688a2e6f23fff32` |
| B2 | Repeated supported key on Skills and Skill versions | 400, code `duplicate_parameter`, param `<key>`, with the observed message | SFT-10: `req_36e64628c2a44b1198d00949c4ed6cc8` |
| B3 | Repeated key on Files | Unchanged local `unsupported_parameter` rejection | SFT-18: `req_9a38e0628b4e40bd8e7202ac0880209d` accepted one identical repeated `purpose`; a single sample does not define which differing value wins |
| C1, C2 | `limit=0` and `limit>100` on Agents, Sessions, Items, Templates | Clamped to 1 and 100 | VA-01: `req_4954e90768b54c588167333e83f0a958`; VA-18: `req_d43d918872cb40b5b6e545582ea94205`, `req_2efee54ffd6f41179e294870b0e62e02`; SES-10: `req_4f1fb6cb479a4d1cb64486c72d250e9f`; SES-11: `req_5af4e60768b14c9eab284bce8638348a`; SES-12: `req_2f7cab87400348408ab8bb6e68789dc9`; SES-13: `req_b5ebab5691314bfba6ad59c84c42d04e`; SFT-23: `req_f63fef0e08c1462c8158c0ee8523a88d` |
| C3 | `limit` 0 or above 100 on Turns | 400, code `invalid_request_error`, param null, `limit must be between 1 and 100` | SES-14: `req_2793f57b6a454c399a031277b6a02e45` |
| C4 | Negative or non-integer `limit` on Beta lists other than Vaults and Credentials | 400, code `invalid_request_error`, param null, `Failed to deserialize query string: limit: invalid digit found in string` | VA-04: `req_1362046e9d69497da9c23ca69517a026`, `req_c384455192dc4a99a032299a91416a74`; SES-15: `req_918128738f3a47b69203ca091f9cb9ca` |
| C5 | Vault and Credential `limit` | 0, negative and above-100 values keep the pinned clamp; a non-integer uses the C4 error | VA-04: `req_f08cda4e1b9048808be9465b9a56c4a4`, `req_e53033a2e01e4b87aa0bb8d32c932a44`; VA-06 (pin conflict): `req_2ca22663c9414afa920b5509d3574812`, `req_1d088639a3614f6ba545cd36497ff35c`; VA-18: `req_bc47269acf8143f7869908567272e433`, `req_62eaf8fd15e8483a98a9a09ebc4d5e51` |
| C6 | Skills and Skill versions `limit` | `0` returns 200 with empty `data`, null first/last IDs and `has_more` true only if a resource follows the cursor; above 100 is `integer_above_max_value`, below 0 is `integer_below_min_value`, both with param `limit` | SFT-08: `req_b71c126adaf3437d9b01aee3ec913863`, `req_218a5d425b7047a190e2c5dc2e047e81`; SFT-09: `req_0ed2668ecab54253ab40d2655954de2c`, `req_4dd2e453717f4f82b5a7921a34d68b0f` |
| C7 | Files `limit` 0 or 10001 | 400, code null, param null; the local message stays | SFT-17: `req_05ec0b8f86b445b2beb2fdfc595d0879` |
| D1 | Scalar `status` plus `status[]` on Vaults and Credentials | 200, filtering by the union; invalid values still reject | VA-05: `req_1e89a6740ecd49b5b3b42f514a144e71`, `req_6223c9b384a246df848cfb94ffe2144d` |
| D2 | Explicit empty `purpose=` on Files | Same as omission, no filter | SFT-14: `req_af6df1ba19fd428fba6b1a66745ce37f` |

### Decisions

The pinned Turn, Item and Template docstrings say "between 1 and 100". A server
that clamps out-of-range values keeps each effective page inside that documented
range, so clamping Items and Templates to match the live service does not contradict
the pin. Turns keep rejecting because the live service rejects. Vault and Credential
negative limits keep clamping because their pinned description says values are
"clamped between 1 and 100"; the official 400 for `-1` (VA-06) remains a recorded
pin conflict.

No pinned single-resource operation sends a typed query parameter, including
Environment retrieval, so no single-resource route keeps a query rejection.

Unchanged: every default (20, Files 10000), the maximum page capacity (100, Files
10000), `order` handling, cursor lookup order, `after` trimming, authentication,
tenant scoping and missing/foreign masking. A `tenant_id` query key is an ignored
unknown key; only authentication selects the tenant. Shared-parser list rejections
happen before any resource lookup, so tenant B receives the same error. The
Environment Files list validates its query only after the Environment lookup, so a
foreign or missing Environment returns 404 first. No rejected request writes.

Families are still selected by path, as for order errors. Local choices for
unsampled inputs:

- Subagent and Artifact lists keep rejecting 0 and values above 100, now with the
  Turn error fields.
- Beta limits above the signed 64-bit range still reject, with code
  `invalid_request_error` and `...limit: number too large to fit in target type`.
  A leading sign or any other non-digit input, including an empty value, uses the
  C4 message.
- A non-integer Skills limit keeps the local `invalid_request` code; its message now
  names the 0–100 range. A non-integer Files limit keeps `invalid_request`. The
  hosted Files schema message and `detail` member are not copied.
- Duplicate keys are reported before value errors, and limit errors before order
  errors. Vault status validation still precedes limit and order. Precedence
  between simultaneous errors was not sampled.

### Deferred

These remain registered differences and are not changed here: repeated Files
`purpose` values (SFT-18); unsampled overflowing limits; unknown and repeated keys
on the Environment Files list, which keeps its own strict parser (its limit range
errors now use the Beta code through the shared limit reader); malformed non-UUID
path IDs (SES-28); metadata and name error envelopes (VA-07/08/09) and U+0000
(VA-10); Skill sole-version deletion and number reuse (SFT-01/02); Session deletion
lifecycle (SES-29/30); whitespace input (SES-01..04); Template network forms and
codes (SFT-20/21/22); and response defaults (VA-11, SES-23/25).

### Acceptance boundary

Go handler and parser tests cover every row, including tenant isolation, and the
Skills store test covers the zero page against PostgreSQL. `official_list_query.py`
replays rows A1–D2 through raw HTTP and the pinned SDK against Core and a dedicated
PostgreSQL database as tenant A and tenant B, and confirms that rejected requests
change no resource. It also checks that single-resource reads, event admission,
streams, updates and deletions ignore unknown keys. This is resource and query
acceptance with no model execution: query parsing does not affect execution, so
live model acceptance does not apply. The independent batch acceptance, server gate
and review are recorded separately when complete.
