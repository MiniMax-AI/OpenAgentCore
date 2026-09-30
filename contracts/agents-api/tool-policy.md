# Explicit disabled tools

Protocol baseline: `upstream.json` (OpenAI Python SDK 3.13.0). This covers a
bounded execution profile, not complete tool or Agents API compatibility.

## Public behavior

Saved Agents and inline Session configuration accept these declarations:

```json
[
  {"type": "web_search", "mode": "disabled"},
  {"type": "programmatic_tool_calling", "enabled": false}
]
```

Saved Agents also keep every other pinned `web_search` mode, as the official
service does: omitted/null mode is saved as `live`, and `cached` and `live` are
saved as sent ([TV-05](official-semantics-alignment.md#saved-web_search-modes--september-23)).
Search settings are resource data in every mode: omitted/null context size
resolves to `medium`; domain and location omission resolves to null; an empty
domain list remains empty; a supplied location, including `{}`, includes `city`,
`country`, `region` and `timezone`, with null for omitted keys (observed
officially: `req_db41d2f6261b4abfb69465eafe719ab5` and `req_165d53b88445490b9146d8272c54134d`). These settings cannot
enable execution. Session admission resolves saved references and inline
declarations with the execution parser into the immutable Session snapshot.
Only explicit `disabled` search is qualified: enabled or omitted-mode search,
inline or saved, rejects with `unsupported_or_invalid_configuration` before any
write unless the Session replaces the saved tools. Protocol errors in these
declarations, such as an unsupported `mode` or `context_size`, a non-boolean
`enabled` or a repeated `web_search`, use the official fields
([validation](official-semantics-alignment.md#agent-configuration-validation--september-23)).
Explicit enabled programmatic execution likewise saves and rejects at Session
admission; saving intent remains separate from execution qualification.

Omitting programmatic configuration preserves each harness's native behavior.
The user approved this difference from the official default-on behavior. Native
feature differences stay in adapters; Core does not supply another executor or
model loop. Unrelated native utility tools are not implicitly removed.

## Runtime boundary

The common `DisableProgrammaticToolCalling` control carries explicit disabled
intent on initial execution and cold continuation. Public qualification and the
operation-specific Runtime capability must both permit this request. Omission
does not require the new capability. Search uses the existing disabled control.

| Adapter | Native enforcement |
| --- | --- |
| Codex | Disable code-mode features; check native managed requirements before thread start/resume and reject a forced conflicting feature. |
| Claude Code | Retain the restricted built-in inventory and verify native initialization against that inventory. |
| MiniMax Code | Retain the protected native tool profile, empty text-execution inventory and disabled web-search feature. |

## Acceptance

The opt-in `TestNativeToolPolicyPublicExecution` fixture and
`services/core/tests/official_tool_policy.py` exercise a real PostgreSQL
database, independent API, Docker daemon and native harness using the pinned
official SDK with strict response validation and raw HTTP. Supply the existing
native-test environment variables plus `OAC_TEST_TOOL_POLICY_ENGINE` and a private
`OAC_TEST_TOOL_POLICY_REAL_OPTIONS` file. Each concurrent execution worker requires
its own dedicated test database.

On 2026-09-22, Codex and Claude with Kimi K3, and MiniMax Code with MiniMax-M2.7,
passed SDK/raw HTTP through both saved and inline configurations. Each of four
Sessions completed a first Turn and continued its random marker after a full
daemon restart with the same native Session. Checks include public configuration,
SSE order, Items, native input receipts, unsupported enablement without Session
persistence, omitted configuration admission and tenant isolation.

Native evidence separately records Codex's disabled feature arguments and search
setting, Claude's empty built-in tool argument and initialization validation, and
MiniMax's restricted native configuration. Successful model text alone does not
prove disabled tools. Controlled tests cover managed requirement conflicts,
invalid fields and the common qualification contract.

Evidence is retained privately under `~/.parsar/remediation/20260922/tool-policy`
on the development host and test server. Live qualification in this batch uses
`environment:none`; workspace provisioning, provider matrices, enabled tools,
native question extensions and complete hosted default/error semantics were not
requalified. Existing workspace mechanisms are unchanged.
