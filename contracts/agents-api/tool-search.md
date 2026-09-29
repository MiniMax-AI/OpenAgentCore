# Deferred function discovery

Baseline: Python SDK 3.13.0, upstream
`d7c41efee1b0802b79f3f88a678ef2052b06e9ce`, Beta `agents=v1`.
This is bounded execution coverage, not complete Agents API compatibility.

## Contract and boundary

Saved and inline tools preserve the full function definition and `defer_loading`
(default false). The pinned Agents `tool_search` configuration has only `type`;
Responses-specific execution/parameter fields are not accepted here. The saved-Agent `PersistedAgentTool` union retains `tool_search`, while the
Session `AgentTool` response union omits it. Session/SSE resource projection follows
that pinned distinction; the full frozen configuration still includes it. Exact
hosted response behavior is unverified. The shared
Runtime carries search intent and each deferral flag. Native search and lazy schema
loading belong to the harness adapter. Core retains the frozen definitions and
existing public function calls, results and application receipts. The pinned Items
union contains no tool-search Item; do not invent one.

The implementation is Claude SDK 0.3.269 / native 2.1.269, single Agent,
`environment:none` or a managed/user-owned workspace, medium verbosity, object-root
function schemas and text results. Workspace tool discovery excludes Skills,
Plugins and local capability directories; its ordinary native workspace tools remain available.
It supports a mixture of eager and deferred application functions with text or
previously qualified inline PNG/JPEG message inputs. The native MCP
server marks eager definitions `anthropic/alwaysLoad:true`; deferred definitions use
false and the adapter explicitly enables native ToolSearch. The function profile allows only declared callbacks and ToolSearch in addition
to the selected workspace tools. No search index, callback protocol, provider proxy or
model loop is added to production.

Search-only, missing-search, HTTP MCP, structured-output and Subagent
combinations remain unqualified. A repeated `tool_search` is an official protocol
error on saved and inline configuration. They are implementation/verification
gaps, not claimed upstream restrictions. Codex and MiniMax discovery remain gaps.
Unknown public combinations reject before execution; an actual Runtime must also
advertise the operation. An advertisement alone cannot qualify a public profile.

Public `tool_search` and function `defer_loading` are shared Runtime intent.
Core preserves complete immutable definitions, sends `PromptRequestPayload.ToolSearch`
and each `FunctionTool.DeferLoading`, and requires the operation's existing profile
qualification plus the Runtime `tool_search` capability. It never performs native
search, selects native names, interprets provider policies or implements another
model/tool loop. Additional harnesses implement the same intent in their adapters.
The pinned Session AgentTool response union excludes the tool_search input member;
project it out of Session/SSE resources while preserving saved and frozen input.

Workspace discovery uses the same native ToolSearch and callback path. The
installed bridge must additionally advertise `workspace_tool_search` and the
complete local Runtime/function contract. The daemon derives its ordinary
`tool_search` capability from these verified bridge features; Core does not branch
on environment ownership. Existing callback receipts, cancellation and native cold
continuation retain their semantics. See [current workspace qualification](environment-capabilities-qualification.md).

## Evidence and limitations

Native feasibility passed with Kimi `kimi-k3`: initial provider requests excluded
the deferred target schema; after a real native ToolSearch call that schema became
available, while an unrelated deferred schema stayed unloaded. The model used a
random required argument available only in that schema and returned the exact fresh
callback result. A new native process resumed the same native Session and called
it again with a new callback result. Existing discovered definitions remained in
that native history. Evidence: `~/.parsar/remediation/20260922/deferred-tools/claude-native-1790052750`.

A first test proxy omitted response Content-Encoding and failed after discovery;
the failed evidence is retained. The corrected test forwards unchanged real provider
responses. The proxy is test observation only and is not part of Runtime.

Codex source exposes deferred dynamic functions, but the available Kimi Responses
probe rejected native `tool_search`; the MiniMax sample did not produce discovery.
Neither establishes Codex qualification. These observations do not prove that all
models from either provider lack the feature.

The native harness owns dynamic model/provider policy. Known conflicting modes and
beta/search settings reject in the adapter. Its SDK has no reliable pre-input signal
proving actual deferral after opaque policy changes; init tool inventory is insufficient.
That detection gap and other providers/models remain unverified. No endpoint/model
allowlist or copied native policy evaluator is added to imply a stronger guarantee.

Public-chain real acceptance passed on 2026-09-22 with Kimi `kimi-k3`:
`tool-search-public-2315339108`, 97.83 seconds. The initial strict-SDK attempt
failed on the Session response union before any model request; that failure is
retained as `public-first.log`. Resource projection follows the fixed schema,
without relaxing SDK validation. Run `TestNativeToolSearchPublicExecution`
with the fixed official SDK, a real private model configuration, a real daemon and
PostgreSQL. It checks saved/inline configuration, mixed functions, result retries,
SSE/Items, cancellation, cold daemon continuation and tenant isolation. Independent review is required before release qualification.

Shared Go race checks and 132 Claude SDK checks passed. The required browser gate
passed 73 cases locally with Node22 and real Chrome. The server has no browser;
the remaining `make check` targets passed on zju against a dedicated PostgreSQL.
No database query/schema changed, so explicit sqlc generation is not applicable;
the full gate still verifies generated queries.

The image/discovery combination also passed through the same public chain in
`tool-search-public-2039155387` (`public-image-isolated-tests.log`). A random PNG
was submitted with the opening prompt and a different random PNG was submitted
while the deferred function waited. The answer identified the latest image's band
order and the fresh callback result; cancellation and cold continuation passed in
the same run. The existing PNG generator is shared with the image-input regression.
No new image admission restrictions or native lifecycle were needed.

The full server gate is `make -o check-web check`, with `make check-web` completed
locally on the same changes. Packaging initially caught an outdated expected Runtime
feature list; it was updated and the full server gate rerun successfully. Image
acceptance uses a separate dedicated database from the full gate. Failed setup
attempts (execution-owner lock and test-database naming guard) are retained; neither
reached model execution.
