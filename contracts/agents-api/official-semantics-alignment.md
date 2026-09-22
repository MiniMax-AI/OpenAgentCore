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
- Official `none` creation rejected omitted, null and empty initial input. Core
  still permits idle `none` Sessions. Changing this requires a coordinated client
  and acceptance-flow migration and is separately queued.
- Two otherwise identical official creates with the same `Idempotency-Key`
  returned 201 and distinct Session IDs. Core retains its durable creation retry
  guarantee. This is a local behavior, not evidence of official idempotency parity.
- An empty Session update body, generic validation codes/field `param`, malformed
  queries, page limits and overlapping mutation behavior need separate qualification.
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
