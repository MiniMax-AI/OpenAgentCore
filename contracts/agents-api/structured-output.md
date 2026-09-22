# Structured output

The pinned Agents API accepts `text.format` with `type: "json_schema"` and a
`schema` object. This is not the Responses API wrapper: do not add `name`, `strict`
or an alternative public output field. The schema is saved and inherited through
the existing Agent/Session configuration resolution and immutable snapshot.

## Qualified execution profile

Claude SDK supports object-root schemas with `environment:none`, medium verbosity,
`multi_agent.enabled=false` and optional ordinary function tools returning text.
The native SDK remains responsible for its model/tool loop and schema validation.
Codex and MiniMax structured-output execution, workspace/HTTP MCP/Subagent
combinations and other root types are unqualified and explicitly rejected. These
are implementation gaps, not a redefinition of the official protocol.

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

Recovery uses the existing Session/Turn/Items queries and native continuation.
SSE is still live-only. Frozen schemas apply to both initial and resumed execution;
ordinary text configuration retains its prior behavior.

## Acceptance

`TestNativeStructuredOutputPublicExecution` and
`services/agents-api/tests/official_structured_output.py` exercise the pinned SDK,
raw HTTP, actual PostgreSQL/Worker/gateway/daemon and real model APIs. They require
explicit private operator options and never supply model responses. The workflow
covers a function-only random value, unchanged saved configuration, native result
application receipts, ordered terminal SSE, persisted final JSON, daemon restart
and same-history continuation, cancellation, text override and tenant isolation.

Focused tests cover native failure/retry projection, exact result bytes, schema
numeric admission, configuration transport and independent service qualification
for another harness. Running only these tests or importing the SDK is not a claim
of complete protocol compatibility.

The retained Codex provider/tool-chain failure is not reopened by Claude's native
qualification. Its next attempt needs a concrete changed prerequisite; do not
weaken assertions, rewrite model output or repeatedly sample until one run passes.
