# Execution and tools coverage

Assessed against Core `206474a4c10901442b1faa094281bfb5559082b5` on 2026-09-22.
The contract remains [OpenAI Python 3.13.0 at `d7c41ef`](upstream.json), Beta
`agents=v1`. This matrix records the bounded execution/tools milestone. It does not
establish complete protocol compatibility; the remaining limits below stay open.

**Implemented** means current source and controlled checks support the operation.
**Qualified** additionally identifies a real public Core/PostgreSQL/daemon/native
model workflow in the evidence register below. **Unsupported** describes a current
admission/native limitation, not a restriction in the official contract.
**Unverified** means evidence does not establish the stated semantics or profile.
Qualification never extends automatically to another model, placement or combination.

## Operation matrix

All function rows refer to application-defined functions. Native workspace tools
and Environment Plugin MCP have separate inventories and qualification.

| Operation | Implemented behavior and real qualification | Unsupported or unverified boundary |
| --- | --- | --- |
| Initial message input, `sessions.create` | String input and ordered user-message arrays share atomic admission. Codex/Claude text and inline image execution: M1/M2; ordinary MiniMax text: M1/P1. [Input contract](message-input.md), [initial parser](../../services/core/internal/api/session_initial_input.go). | Whitespace-only text is admitted verbatim for Codex and rejected at admission for Claude SDK and MiniMax Code ([text content](message-input.md#text-content)). Empty parts beside text (SES-08) and local size-limit parity need upstream evidence. Inline PNG/JPEG is qualified on `none`, Core-managed Docker `openai_hosted` and `self_hosted`; MiniMax images and remote URLs remain gaps. |
| Prepared and active messages, `sessions.events.create` | Ordered `input_text`/`input_image` arrays retain original content and distinct public user Items. HTTP 202 confirms persistence; native receipts establish application. Initial/prepared/active Docker PNG/JPEG: M2; active PNG on `none`: M1. [Shared admission](../../services/core/internal/api/inputs.go). | Codex flattens native messages with blank-line separators. Claude can fold or queue native turns; it does not promise Codex's same-native-turn behavior. Public durability does not prove native consumption. Claude SDK and MiniMax Code reject messages without an image or non-whitespace text at admission (`unsupported_or_invalid_configuration`); Codex delivers them unchanged. |
| Structured output, `agent.text.format` | Save/inherit/freeze `{type:json_schema,schema:...}`. Claude SDK object-root, single Agent, medium verbosity, ordinary functions returning text: S1 (`none`) and S2 (Docker, including prepared/active input and Files/Artifacts), plus [self-hosted execution and cold continuation](environment-capabilities-qualification.md). Native final text is retained unchanged; its stream follows the official message sequence with the whole text in one `output_text.delta` ([item serialization](history-events-usage.md#item-serialization-2026-09-23)). [Contract](structured-output.md), [profile](../../services/core/internal/engine/claude.go). | An explicit non-object root type is a protocol error for every harness ([validation](official-semantics-alignment.md#agent-configuration-validation--september-23)). Codex/MiniMax, schemas without an object root, schema numbers changed by binary64, Skills/Plugins, MCP, Subagent and discovery combinations reject execution. No output repair, coercion or extra model loop. Arbitrary schema dialects are unverified. |
| Function configuration, saved/inline Agents | Required name/description/schema; `defer_loading` defaults false. Protocol errors, repeated names and explicit non-object root types reject with the official fields ([validation](official-semantics-alignment.md#agent-configuration-validation--september-23)). Saved references resolve into an immutable Session snapshot. Codex/Claude real calls: F1/F2/M2/S1/S2. [Parser](../../services/core/internal/api/function_configuration.go), [saved tools](../../services/core/internal/api/saved_tools.go). | MiniMax public functions reject. Claude requires an explicit object root. Local nonblank/512-byte name and 64-definition Session bounds are compatibility gaps. Saving configuration alone does not qualify execution. |
| Function-result admission, `events.create` | Required `turn_id`, `call_id`, `success`; optional nullable `error` and `output`. Output is string or ordered text/image content. Scoped atomic batches retain field presence, original content and retry identity in storage; public result Items and events always carry `output` and `error`, null when not submitted ([item serialization](history-events-usage.md#item-serialization-2026-09-23)). Same result retries are accepted; changed results and results after cancellation return 409 `conflict_error`, including after terminal state. In the caller's Session, an unknown call or a call of another Turn returns 400 `invalid_request_error` without changing the pending action; missing and foreign Sessions stay 404 ([error rows](official-semantics-alignment.md#session-input-conflicts-and-result-targets--september-23)). F1/F2/M2 plus [controlled SDK/raw checks](../../services/core/tests/official_function_inputs.py). [Parser](../../services/core/internal/api/function_inputs.go), [Store](../../services/core/internal/store/function_inputs.go). | Admission is separate from application and public Item publication. Invalid or unqualified content cannot consume a pending call. Core messages omit the call and executor IDs that official messages name; hosted defaults and publication timing remain unverified. |
| Function text results and native application | Codex waits for a matching live root dynamic-tool completion; Claude waits for a matching live root native tool result. Text/error results, retry/conflict, cancellation and cold continuation: F1/F2. [Receipt contract](function-result-images.md). | Transport writes alone do not confirm application. Confirmation is not provider consumption, crash recovery or exactly-once external effects. No automatic result replay. |
| Function image results | Successful ordered inline PNG/JPEG with text, large PNG and image-only JPEG: Codex/Claude `none` F1/F2; Docker M2. Public Items retain submitted bytes. Codex receipt regression: F2. | Claude rejects failed images and remote references before persistence; native resizing may change its image bytes. Self-hosted Claude image results use the same native path; see the current [qualification record](environment-capabilities-qualification.md). MiniMax functions remain unqualified. Other Codex image/error/reference combinations cannot be inferred from successful-inline evidence. |
| Deferred function discovery | Type-only `tool_search` plus mixed eager/deferred functions: Claude SDK 0.3.269/native 2.1.269, Kimi K3, single Agent, medium, `none`, text results; text and PNG input D1. Native provider observations establish lazy schema loading for D1. [Self-hosted workspace callback, continuation and cancellation](environment-capabilities-qualification.md) use the same native path; they do not add model-request observer evidence. [Contract](tool-search.md). | A repeated `tool_search` is a protocol error. Codex/MiniMax discovery, search-only/missing-search, workspace with Skills/Plugins, MCP, structured-output and Subagent combinations remain gaps. Opaque native policy changes lack a reliable pre-input deferral signal. Saved tools include `tool_search`; the pinned Session response union excludes it. Exact hosted projection is unverified. |
| Explicit disabled search/PTC | Saved and inline `web_search.mode=disabled` and `programmatic_tool_calling.enabled=false`; shared native controls on initial and cold execution. All three harnesses on `none`: P1, including native inventory/control evidence. [Contract](tool-policy.md), [parser](../../services/core/internal/api/disabled_tools.go). | Unsupported explicit enablement rejects at Session admission; saved Agents keep every pinned search mode as resource data (TV-05), and Sessions from such Agents reject unless they replace the tools. A repeated `web_search` is a protocol error. Omission retains approved native behavior, which does not establish official default-on PTC parity. Enabled search and default/error parity remain gaps; unrelated native utilities are not implicitly removed. |
| Agent service-origin MCP | Implemented Codex/Claude HTTP `none` profiles, anonymous/static bearer, scoped Vault selection and native `mcp_call` Items. [Configuration and qualification limits](../../services/core/README.md#http-mcp-execution), [profiles](../../services/core/internal/engine/profile.go). | This closure batch does not requalify MCP/provider combinations. MiniMax, self-hosted/hosted service-origin MCP, OAuth, nonempty inline headers/metadata and other transports reject. Environment origin follows the separate row below. Claude requires a static connected inventory; original MCP-envelope fidelity and continuing server health are unverified. |
| Agent Environment-origin MCP | Public HTTP declarations reuse installed MCP's effective Runtime bindings; attached Vault selection and native observations remain common. [Origin and Harness matrix](environments.md#public-mcp-connection-origin), [real qualification](https://github.com/MiniMax-AI/OpenAgentCore/blob/e974a7f880a2eb799f0dd39e6ba0870462854a53/contracts/agents-api/public-mcp-qualification.md). | Requires a workspace and enabled network. MiniMax rejects every non-null allowlist and required initialization. No service-origin relocation, automatic fallback or credential copy into native profiles. |
| Environment Plugin MCP/native tools | Separate [Docker Plugin transport matrix](environment-templates.md#environment-origin-mcp-plugins) and [V1 deployment evidence](user-managed-runtime-v1.md). Native tools stay within the existing colocated Runtime. | Plugin MCP is not Agent service-origin MCP or a public function action. No new optional cross-product is qualified here. Native utility inventories need not be identical. |
| Required action: `function_call` | Session GET/list and Session SSE expose persisted `{arguments,call_id,name,turn_id,type}`. Actions remain pending until native application or cancellation/terminal settlement. [Projection](../../services/core/internal/store/function_state.go), [confirmation](../../services/core/internal/store/function_results.go); real handling F1/F2/M2/S1/S2/D1; explicit client disconnect/query/reconnect: F3. | Function-call history is not pending-state authority. Client reconnect does not replay events or redo external application effects. |
| Required action: `environment_connection` | Offline waiting input exposes `{environment_id,type}` before Turn creation. Connect the exact Session Environment through our scoped daemon enrollment. Three-harness Docker/E2B execution: E1; explicit initial pending-input/query/connection/native completion: E2; expiry: [controlled initial-input test](../../services/core/tests/official_self_hosted_initial.py). | Idle offline Sessions without pending input request nothing. Registration alone is not connectivity or native readiness. Stock `exec-server`/Noise transport is outside the approved V1 route. Exact upstream registration/action-removal timing remains unverified. |
| Stream disconnect and execution loss | SSE is live-only. Reconnect, buffer events, retrieve Session/Turn/Items and deduplicate Item IDs. Worker recovery fails previously claimed work without replay; queued work can remain. [Recovery contract](README.md#public-execution-admission), [connection reconciliation](../../services/core/internal/store/environment_connection_recovery.go). | Client disconnect and daemon/Core loss are different cases. F3 qualifies client-stream loss and continued pending function handling; F2 qualifies native process loss with an unapplied saved result. Neither promises replay or recovery of unknown external effects. |

## Required-action recovery contract

The fixed [Session union](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agent_session.py)
defines both variants. The official [Functions](https://developers.openai.com/api/docs/guides/agents-api/tools/functions)
and [Manage sessions](https://developers.openai.com/api/docs/guides/agents-api/sessions/manage)
guides, checked on 2026-09-22, direct clients to retrieve `required_actions` after
restart or stream loss. A historical `function_call` Item alone cannot establish
that a result is still pending. The fixed SDK governs field/type compatibility;
current prose does not silently change that pin.

For a pending function, use the returned Session, Turn and call identity. If the
application already performed the function, keep and submit that saved result;
do not run an external effect again merely because an action remains visible.
Core's acknowledgement boundary is native application. For an environment action,
connect its exact Environment using the existing enrollment authorization.
[Connection observations](../../services/core/internal/store/environment_connections.go)
fence stale generations; transport connection can clear pre-Turn activity before
native preparation. Neither registration nor an action disappearing proves that
a model ran successfully. Query the matching Turn and Items for its outcome.

Two timing questions remain open. Repeated `requires_action` notification order
and acknowledgement timing are local implementation choices, not proven upstream
semantics. Also, an admitted result that is cancelled before native observation
can remain internally saved without a public output Item or `item.added`.
[Item publication](../../services/core/internal/store/item_projection.go) and
[the existing coverage record](README.md#public-function-configuration) preserve
this gap. Successful retry cannot manufacture the missing observation or establish
that the model consumed the result.

## Evidence register

Real records below use fixed SDK 3.13.0 and raw HTTP through Core, dedicated
PostgreSQL, daemon and native harness. Evidence is private under `~/.parsar/remediation/` on `zju_a100_2` and the
development host. Paths containing `validated/` or `server-evidence/` identify
downloaded local mirrors; summary files may also live only on the development
host. Locators identify retained evidence without publishing credentials or
private launch configuration. Revisions identify the accepted change,
not a claim that every historical workflow was rerun at the current baseline.

| ID | Exact evidence and accepted scope |
| --- | --- |
| M1 | `20260922/message-image-input/public-codex-reviewed.log` (59.41s), `public-claude-reviewed.log` (79.52s), `public-mcode-network-fixed.log` (48.95s); [PR #12](https://github.com/MiniMax-AI/OpenAgentCore/pull/12), merge `37a2923`. Codex/Claude Kimi K3 PNG initial/active inputs, receipts, retries, cancellation, cold continuation and isolation; MiniMax M2.7 ordinary text regression. |
| M2 | `20260922/workspace-images/validation-summary.json`; Codex `public-run-_lr5uiy_`, Claude `public-run-sb65p9e9`; candidate `2c295116d66ed9aafc808ec49a8cb6dcb5c674c2`, merge `1adb2489bc3415039440224e9a7905c53e68a557` ([#17](https://github.com/MiniMax-AI/OpenAgentCore/pull/17)). Kimi K3, Codex 0.153.4, Claude SDK 0.3.269/native 2.1.269; Docker seven-Turn image/workspace workflow. Codex's receipt conclusion is superseded by F2. |
| F1 | `20260922/function-result-images/validation-summary.json`, `validated/function-image-public-2567456666/public.json`; candidate `d3cdb225bc7628b968b76c7e1b400101894ce8cd`, merge `fd23f169e1ede1b2f1c39a1d9dcce71886e3c006` ([#16](https://github.com/MiniMax-AI/OpenAgentCore/pull/16)). Claude SDK 0.3.269/native 2.1.269 with Kimi K3 on `none`; success PNG/JPEG, failed text, retries, cancellation, history continuation. Earlier Codex transport-only evidence does not qualify current receipts. |
| F2 | `20260922/function-result-receipts/validation-summary.json`, `candidate-public-owner-sdk.log`; hosted `public-run-4fn70foy` (104.13s), `none` `function-image-public-945075091` (83.918s). Candidate `6dceb98e1c56469fcc70cd82025ce77fc870ed3d`, merge `206474a4c10901442b1faa094281bfb5559082b5` ([#20](https://github.com/MiniMax-AI/OpenAgentCore/pull/20)); real Kimi, Codex 0.153.4. Qualifies native receipts, not completed provider consumption. |
| F3 | `20260922/execution-tools-closure/pending-actions/validation-summary.json`; Codex `codex/public-run-8gexf5a8` (65.58s), Claude `claude_sdk/public-run-5f2tr7e_` (74.33s), production source `206474a`. The same [public verifier](../../services/core/tests/official_pending_actions_native.py) checks disconnected-client pending queries, success/error/cancel, original results, target isolation, retry/conflict and no implicit call replay with real Kimi on Docker. No Core restart or native-loss injection is claimed by these runs. |
| S1 | `20260922/structured-output/acceptance-summary.json`, `structured-public-1905281777/public.json`; real candidate `7f533d46c472f3cdf7c16ce9471c225a2ba7eded`, merge `8716e699dbc34904499c94b6b0b03857bbebfaad` ([#11](https://github.com/MiniMax-AI/OpenAgentCore/pull/11)). Claude/Kimi `none` schema/function execution and cold continuation. Schema-specific active steering was not separately qualified here. |
| S2 | `20260922/hosted-structured-output/validation-summary.json`, `validated/public-inline.log`, `public-run-qvq3csf1` (186.09s); candidate `f6d2c3f2a1dc2ff5f177213bd2b20d78496011d0`, merge `aa85d8ecc60e0a8b7f2494b73caa81216ee197e2` ([#18](https://github.com/MiniMax-AI/OpenAgentCore/pull/18)). Claude SDK 0.3.269/native 2.1.269, real Kimi; Docker saved/inline schema, prepared/active input, native files and four completed structured answers. |
| D1 | `20260922/deferred-tools/tool-search-public-2039155387/public.json`, `public-image-isolated-tests.log` (86.71s); merge `178507ae7a63d4068e1e82bef9cd256ba398ae00` ([#13](https://github.com/MiniMax-AI/OpenAgentCore/pull/13)). Claude SDK 0.3.269/native 2.1.269/Kimi K3 `none`; text results with initial/active PNGs, cancellation and cold continuation. `claude-native-1790052750` separately records provider schema inventories and native feasibility. |
| P1 | `20260922/tool-policy/acceptance.json`, `server-evidence/tool-policy-{codex-2495445746,claude_sdk-2539346823,mcode-3747575401}/public.json`; candidate `eeb432c7495265ae3a9309e32f5b4323611c3245`, merge `4b8754a6eda6696d9b18eb8d583953ac16c6800f` ([#14](https://github.com/MiniMax-AI/OpenAgentCore/pull/14)). Codex/Claude Kimi K3, MiniMax M2.7; four `none` Sessions per harness, native disable controls and cold continuation. |
| E1 | `20260921/self-hosted-onboarding/{source-candidate.json,source-verified.json,live,live-e2b}`; candidate `8f0cd2530d7b58cb7fb3ea124a1fc43dacecca36`. [Exact Docker/E2B run paths and limits](user-managed-runtime-v1.md#evidence-and-verification-boundaries): Codex Kimi K3 Responses, Claude Kimi K3 Anthropic-compatible API, MiniMax M2.7, immutable Linux amd64 Runtime builds. Native execution, restart/history, cancellation, Files/Artifacts and isolation; shared credential lifecycle evidence is not a separate live rotation run for every E2B profile. |
| E2 | `20260922/execution-tools-closure/environment-actions/result.json` contains Codex/Claude Kimi passes (13 checks each) and the retained MiniMax direct-network failure; `mcode-relay-retry/result.json` separately passes MiniMax M2.7 (13 checks) after correcting provider routing. Source `206474a`; same shared SDK/raw workflow, isolated Core/PostgreSQL and user-managed Docker. Covers initial creation SSE, client disconnect, exact pending Environment/no Turn or Items, scoped enrollment, one actual completed model Turn and creation retries preserving history. This is connection-action qualification, not another full lifecycle or E2B regression. |

Controlled tests establish parser, projection, atomicity and race behavior; native
feasibility probes establish only feasibility. Neither replaces the real records.
Failed setup, provider and assertion runs remain failures with their original
scope. The unchanged gate requirements and independent review apply to this batch.

## Milestone closure and remaining limits

1. **Principal function recovery:** F3 directly verifies live client-stream loss
   while a function is pending on Codex and Claude, authoritative queries, exact
   targeting, rejection without mutation, original results, retry/conflict,
   application, cancellation and isolation. F2 separately establishes truthful
   native-loss settlement without replay. No active execution recovery is promised.
2. **Principal environment recovery:** E2 explicitly verifies pending
   `environment_connection` recovery and single initial execution on all three
   harnesses. E1 retains broader deployment/history/isolation qualification.
   Registration, connection and preparation remain distinct; no exact upstream
   timing or arbitrary-provider qualification is inferred.
3. **Recorded nonblocking timing:** an accepted result cancelled before native
   application may lack a public result Item. The submitted data remains durable;
   F2 confirms that native loss does not turn unknown delivery into Applied. The
   fixed sources do not establish the publication point for this interleaving.
   Keep it queued rather than inventing a public state or rewriting safe receipt
   ownership. This limited timing question is not accepted protocol parity and
   does not invalidate F3's principal pending-result workflow.

Approved native capability differences (including default PTC), unsupported optional
combinations, arbitrary provider parity and low-frequency error/default edge cases
remain separate limitations. They do not authorize silently relabeling an unproved
principal workflow as deferred. OAuth, full Usage, release publication, new tool
kinds and new execution loops are outside this closure batch.
