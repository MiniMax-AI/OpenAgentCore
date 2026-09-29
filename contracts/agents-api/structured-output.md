# Structured output

The pinned Agents API accepts `text.format` with `type: "json_schema"` and a
`schema` object. This is not the Responses API wrapper: do not add `name`, `strict`
or an alternative public output field. The schema is saved and inherited through
the existing Agent/Session configuration resolution and immutable snapshot.

## Qualified execution profile

Claude SDK supports object-root schemas with `environment:none` or Core-managed
Docker `openai_hosted` or user-managed `self_hosted`, medium verbosity, `multi_agent.enabled=false` and optional
ordinary function tools returning text. Hosted execution uses the existing native
workspace tools, preparation, Files and Artifacts. The native SDK remains
responsible for its model/tool loop and schema validation. Codex and MiniMax
structured-output execution, Skills/Plugins/capability
directories (including inherited template contents), HTTP MCP, Subagent/tool-search
combinations and schemas without an explicit object root are unqualified and
explicitly rejected. E2B uses the common execution path but has not been
requalified in this change. These are implementation gaps, not a redefinition of the
official protocol. An explicit non-object root type is an official protocol error
on save and Session creation for every harness
([validation](official-semantics-alignment.md#agent-configuration-validation--september-23)).

The Claude SDK consumes JSON numbers as binary64. Session admission rejects schema
numbers whose values cannot survive that conversion; saved Agent resources still
retain such schemas exactly. This does not claim that the upstream model/harness
preserves arbitrary numeric candidates internally. The adapter never reparses and
serializes a final answer to construct public text.

## Core and Runtime boundary

`ExecutionControls.OutputFormat` carries the schema through the shared execution
request. The selected service profile qualifies the operation; `structured_output`
and message observations are checked only for requests using the option. An
advertised capability alone never enables public support. Other harnesses can
implement this same request and existing Message observations without Core
engine-name branches or a second execution loop.

The Claude adapter passes `outputFormat` to the fixed SDK. Its native
`StructuredOutput` tool is internal and is not an extra caller-defined function.
Successful live root calls are confirmed by their native tool-result receipt and
an attributed successful result containing `structured_output`. The adapter emits
a completed `final_answer` Message with the native tool-use ID and unchanged
`result.result` text. Parent assistant prose retains its own ID. Failed retries
and cancelled candidates cannot become a completed structured answer. No private
history read, output repair, schema coercion or prompt wrapper supplies the result.

Workspace preparation additionally verifies the installed bridge's
`workspace_structured_output` feature and complete local Runtime contract. Native
inventory includes `StructuredOutput` only when output configuration requests it;
root tool identity, abort checks, filesystem and credential protections remain
unchanged. A bundle feature alone does not qualify another public combination.

Recovery uses the existing Session/Turn/Items queries and native continuation.
SSE is still live-only. Frozen schemas apply to both initial and resumed execution;
ordinary text configuration retains its prior behavior.

The public `text.format={type:"json_schema",schema:{...}}` is resolved with saved
Agent overrides and frozen in the existing Session configuration. Core transports
it in `ExecutionControls.OutputFormat`; it does not append prompt instructions,
validate/retry model answers, repair JSON or select native tool names. Public
admission requires the selected profile's structured-output qualification, and
only requests using this option require the Runtime's `structured_output` and
message-observation capabilities. A capability advertisement does not qualify a
new public combination. Claude advertises this operation only when the installed
SDK bridge reports its `structured_output` feature. A workspace Runtime also
requires the complete local Runtime contract and `workspace_structured_output`;
preparation checks that bundle before native launch. These remain adapter readiness
features, not new Core lifecycle or public protocol variants.

The current qualified path is Claude SDK, `environment:none` or Core-managed
Docker `openai_hosted` or `self_hosted`, medium verbosity, single Agent, with optional ordinary
function tools and text results. The workspace uses its existing preparation and
bypass execution profile with the SDK's configured `StructuredOutput` tool added to
inventory and permission checks. Frozen schemas reach preparation before the
input handoff; Start cannot replace them. Skills, Plugins, capability directories,
HTTP MCP, Subagent/tool-discovery combinations and schemas without an explicit
object root remain unqualified; an explicit non-object root type is a protocol
error for every harness. Check resolved template contents as well as inline configuration;
ordinary text requests retain their existing qualifications.
The SDK uses binary64 JSON numbers: reject execution schemas whose numeric values
would change during that conversion, without narrowing saved Agent storage.
Codex and MiniMax structured output remain explicit execution gaps.

The Claude adapter passes `outputFormat` to the maintained native SDK and allows
its native `StructuredOutput` terminal tool. A matching live root tool result and
an attributed successful SDK result confirm the final output. Publish the native
`result.result` string unchanged as a completed `final_answer` Message using the
native tool-use ID; the parent assistant ID can already own a prose Item. Do not
publish unvalidated retry candidates or serialize `structured_output` back to
JSON. Native retries remain harness-owned. Existing input receipts, usage,
cancellation, release and recovery rules apply unchanged. The implementation and
qualification limits are recorded in [the coverage note](structured-output.md).

## Acceptance

`TestNativeStructuredOutputPublicExecution` and
`services/agents-api/tests/official_structured_output.py` exercise the pinned SDK,
raw HTTP, actual PostgreSQL/Worker/gateway/daemon and real model APIs. They require
explicit private operator options and never supply model responses. The workflow
covers a function-only random value, unchanged saved configuration, native result
application receipts, ordered terminal SSE, persisted final JSON, daemon restart
and same-history continuation, cancellation, text override and tenant isolation.

`services/agents-api/tests/official_hosted_structured_native.py` extends public
acceptance to an independently deployed Core, dedicated PostgreSQL and Docker
Runtime using the real Kimi API. It covers initial saved configuration and inline
prepared configuration, function-only random values, active input receipts,
native file writes consistent with final JSON, Files/Artifact reads, unchanged
terminal SSE, result retries/conflicts, cold Core/Runtime continuation, pending
cancellation without a fabricated final, ordinary text and tenant/Session isolation.
The accepted image retains the pinned SDK 0.3.269 and Claude Code 2.1.269. This
qualifies that native/provider combination, not every model or schema dialect.

Focused tests cover native failure/retry projection, exact result bytes, schema
numeric admission, configuration transport and independent service qualification
for another harness. Running only these tests or importing the SDK is not a claim
of complete protocol compatibility.

The retained Codex provider/tool-chain failure is not reopened by Claude's native
qualification. Its next attempt needs a concrete changed prerequisite; do not
weaken assertions, rewrite model output or repeatedly sample until one run passes.

Self-hosted Claude structured output and cold continuation use the same workspace
adapter as managed execution. See [current qualification](environment-capabilities-qualification.md).
