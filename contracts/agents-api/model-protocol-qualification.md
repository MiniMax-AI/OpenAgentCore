# Model protocol qualification (2026-09-28) — historical, retired

> Historical record of the retired built-in model proxy and converter. The
> implementation, dependency choices, commands and results below describe that
> earlier revision only; they are not current setup instructions, dependencies
> or supported protocol combinations. The current native-only contract is
> [model execution](model-execution.md#saved-defaults-and-precedence). No
> compatibility alias, automatic migration or restoration of this converter is supported.

The pinned converter is CLIProxyAPI v8.0.3. Real execution used MiniMax-M2.7
through the configured MiniMax Chat Completions, Responses and Messages APIs,
Codex 0.153.4, Claude Agent SDK 0.3.269 / Claude Code 2.1.269 and MiniMax Code
0.4.12. The Claude bridge uses Executor protocol 3. Each run creates a fresh
public Session through the pinned official Python client and freezes the provider
through normal deployment-default admission. No test Dispatcher.Options override
supplies model selection.

| Engine | Messages | Responses | Chat Completions |
| --- | --- | --- | --- |
| Claude Code | Native: passed | Converted: passed | Converted: passed |
| Codex | Converted: passed | Native: passed | Converted: passed |
| MiniMax Code | Native: passed | Native: passed | Native: passed |

Each Claude/Codex case covers two successful public function rounds, one failed
function result, incremental text, cancellation, continuation after cancellation
and a Runtime restart followed by exact native Session continuation. Persisted
function-result delivery is checked for loss or duplication. The engine's public
Session response is checked for provider-key exposure. Each MiniMax Code case
covers remembered context and exact continuation after a Runtime restart; public
function calls and cancellation are not claimed by those MiniMax cases.

This is a model-communication acceptance suite using the existing environment:none
profile. Hosted and self-hosted use the same Runtime path in code; this batch does
not repeat a complete Docker/E2B/self-hosted deployment qualification. Native
model reasoning appeared in the real responses. Image payload preservation,
visible reasoning projection, usage, namespaced/custom tools and abnormal stream
termination are additionally checked by controlled converter fixtures. No real
vision-model qualification is claimed. Public token usage can remain null when an
adapter preserves only a partial native breakdown; this task does not add billing
or invent counters to fill that gap.

The endpoint tests cover upstream credential isolation, redirect refusal,
authentication, first-text flushing before completion, cancellation, final cleanup,
non-stream SDK aggregation and interrupted streams. An Executor test verifies that
the endpoint survives completed and cancelled Turns and closes on final teardown.
The console's three affected browser scenarios passed, including protocol choice,
save/edit/cancel, token limits and write-only credentials.

The real entrypoints are TestNativeModelProtocolPublicExecution and
services/core/tests/official_model_protocol_native.py. Private model settings
are supplied by OAC_TEST_MODEL_PROTOCOL_OPTIONS; do not commit them or print their
values. The suite records only controlled checks, IDs and event counts under
OAC_TEST_NATIVE_PROOF_DIR. Library and product performance evidence are separate:
see model-protocol-library-evaluation.md and model-protocol-benchmarks.md.
