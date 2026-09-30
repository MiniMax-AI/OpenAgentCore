# Native harness contract and qualification

Codex, Claude Code and future harnesses are equal execution engines. Core owns
public protocol, authority and durable state. Each adapter owns native
configuration, transport and process translation;
the native harness owns the model/tool loop. Supporting this contract
means implementing its observable semantics. Harnesses do not need identical
feature sets. Optional native limitations are separate capability work and do not
block completion of otherwise qualified onboarding.

For implementation steps and interface obligations, see
[Add a native Harness](harness-onboarding.md).

## Integration surface

Follow the numbered steps in [Add a native Harness](harness-onboarding.md#steps)
to implement, register and select an adapter. This page owns what counts as
qualified.

The service profile catalog is the explicit qualification boundary; a Runtime
heartbeat cannot authorize new public functionality. Unknown profiles fail
closed. Schema validity, qualified service support and the available Runtime
remain independent checks. Additional capability combinations require evidence,
not an engine-name exception. No new handler, store table, scheduler, event
projector or model loop is needed for capabilities the contract already represents.

Do not require MCP, functions, images, verbosity control or another engine's
optional features simply to register a Harness. The default engine is a
deployment convenience, not a different contract or authority level; see
[engine selection](harness-onboarding.md#engine-selection).

## Shared behavioral obligations

- Preparation holds resources without consuming input. Normal Turn completion
  retains a settled healthy Executor. Cancellation targets one Turn; idle expiry,
  environment shutdown and invalidation close the Executor. Failed cleanup retains
  ownership and capacity, and never authorizes replay of uncertain input.
- Confirm accepted/applied inputs separately. Preserve ordered public Items and
  events, stable call identity and one terminal outcome. Never replay uncertain
  work merely because a connection closed.
- Resume only the bound Session's native history. Missing, ambiguous or foreign
  history fails closed. Device identity is not native Session ownership.
- Native tools and public Files use the same bound workspace. Public Files retains
  tenant and path authorization. Native tools run with the starting account's
  permissions; daemon/model credentials are not isolated from that same user.
  Managed outer Environments must exclude other tenants' resources.
- Emit verified measurements; absence of native usage detail is not a zero value.
  Explicit unsupported operations remain implementation gaps in protocol coverage.

## Current qualified operations

The baseline is actual supported behavior on main, not everything Codex accepts
syntactically or everything an upstream Harness can theoretically perform. The
MiniMax Code column summarizes the `mcode` row of the
[public engine profiles](README.md#public-engine-profiles) and the linked operation
contracts.

| Operation | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| Docker hosted text execution, native local tools | Qualified | Qualified | Qualified (Docker `openai_hosted`) |
| Files, immutable Artifacts, cancellation, restart/history recovery | Qualified | Qualified | Qualified on the dedicated Docker profile; see [MiniMax Code Runtime](../../services/core/deploy/mcode/README.md) |
| Public functions in `none` | Qualified | Qualified; object-root schemas; text or successful inline PNG/JPEG results | Unsupported |
| Public functions alongside hosted workspace tools | Qualified | Qualified; object-root schemas and text or successful inline PNG/JPEG results | Unsupported |
| HTTP MCP and static-bearer Vault credentials in `none` | Qualified | Qualified subset | Unsupported |
| Required MCP initialization | Qualified | Qualified on `none`; native readiness before initial input | Unsupported |
| Hosted HTTP MCP | Gap | Gap | Gap |
| Function image results | Supported subset; early acknowledgement is transport-only | Successful inline PNG/JPEG on `none` and Docker `openai_hosted`; native resizing allowed, error images rejected | Unsupported |
| Non-default verbosity | Native/model-dependent support | No equivalent qualified; medium only | Medium only |
| Public detailed Usage | Supported native counters | Native raw usage retained; public breakdown gap | Public breakdown unsupported |
| V1 `self_hosted` daemon enrollment at `/workspace` | [Qualified deployment scope](user-managed-runtime-v1.md) | [Qualified deployment scope](user-managed-runtime-v1.md) | [Qualified deployment scope](user-managed-runtime-v1.md) |
| Deferred function discovery | Unqualified; explicit rejection | [Single-agent text/function profile](tool-search.md) | Gap; see [tool search](tool-search.md) |
| Structured output | Unqualified; explicit rejection | [Qualified single-agent function profile](structured-output.md) | Gap; see [structured output](structured-output.md) |
| Message images | Inline PNG/JPEG on `none`, Docker `openai_hosted` and `self_hosted` | Inline PNG/JPEG on `none`, Docker `openai_hosted` and `self_hosted` | Unsupported; see [message input](message-input.md) |
| Explicit reasoning | Shared service gap | Shared service gap | Shared service gap |
| Six Subagent reads | [Qualified scope](subagents.md) | [Qualified scope](subagents.md) | [Qualified scope](subagents.md) |

This inventory records supported combinations, not a feature-equality checklist.
Do not silently drop options, fabricate measurements, weaken isolation or remove
working features. Unsupported operations stay explicit; implementing them is a
separately prioritized decision, not an onboarding prerequisite. MiniMax also implements the same colocated Runtime enrollment. This V1 decision
uses our daemon as executor and explicitly does not claim stock `exec-server`
interoperability. The old service-side harness/remote executor route is retired.
Service-origin HTTP MCP remains unsupported on `self_hosted`; `none` MCP and
qualified hosted Template Plugin MCP retain their separate scopes.

## Acceptance checklist

Before implementation, record the operation set, expected results, exclusions and
stopping conditions. A batch ends when its declared operations pass; it does not
expand to match another Harness's feature list. A small adapter does not remove
the need for native qualification.

- Adapter tests: reuse `agent/contracttest.TextLifecycle` with a controlled native
  fixture or real provider. Record which one was used. Two ordinary Turns share
  one native process/connection and history;
  cancellation followed by another Turn; stale cancellation and late events; native
  exit, cleanup failure, input write/application receipts and unknown outcomes.
  Verify fresh per-Turn usage, function, input and child-observation state.
- Shared integration: public admission, actual Worker/device selection, gateway,
  daemon registration/dispatch and durable terminal projection. See
  `TestThirdHarnessPublicOnboarding` for a synthetic example, not native evidence.
- Real acceptance: pinned official Python SDK and raw HTTP against our Agents API,
  real provider API, native harness and independent execution database. Verify
  initial execution, warm follow-up, cancellation and restart/continuation.
  Record native owner identity and same-condition cold/warm timing; mock results
  cannot establish native reuse or performance gains.
  For hosted qualification also verify Files/Artifacts, workspace identity,
  credential protection and foreign-history rejection.
- Regression: existing qualified engines keep working. Run targeted tests during
  development, then `make check` and applicable real regressions. API changes
  require `make openapi`; query changes require `make sqlc-generate`.
- Review: follow the repository
  [blind review workflow](../../CONTRIBUTING.md#review).

Record exact revisions, image/package versions, commands, results and limits.
Keep keys in private operator files; never commit them or include them in logs.
Failed or synthetic runs cannot be counted as real acceptance. Acceptance of one
engine is not complete public protocol compatibility.

## Common contract acceptance

The synthetic [third-harness fixture](../../apps/daemon/testdata/onboarding/main.go)
implements only the current text execution contract: cancellation, durable active
input receipts and strict bound-history continuation. It has no workspace, MCP,
public functions, permissions or user-choice handlers. Its registration is local
to the fixture; production builds never register it.

`TestThirdHarnessPublicOnboarding` uses a custom immutable `engine.Catalog` in the
same `execution.Policy` supplied to both the API handler and Dispatcher. The zero
policy selects built-ins; an explicitly empty catalog authorizes no engines.
The test runs public Session/input admission, Worker device selection, the real
WebSocket gateway, daemon Registry/Router, neutral events and durable terminal
projection. It checks applied input receipts, saved native identity, continuation,
cancellation, unsupported optional requests and missing mandatory Runtime support.
This proves the integration path, not real native execution or sandbox security.
The existing internal registration functions suffice for this fixture; no dynamic
registry or global mutable test registration is required.

The current text execution contract still requires durable input/Turn semantics,
ordered observations and enforcement of disabled execution controls. An adapter
without tools or subagents can guarantee their absence; it must not pretend to
apply unsupported requested behavior. Hosted qualification has additional workspace
and isolation obligations. These guarantees are independent of feature equality.

## Native operation acceptance

Use the pinned official Python SDK, raw HTTP and real model APIs. The common
`services/core/tests/official_hosted_functions_native.py` assertions exercise
function success/error, native file output and public artifact bytes, same-history
continuation after restart, foreign result rejection and pending-call cancellation.
The operator fixture supplies only deployment/restart and model configuration;
public assertions are shared by adapters that support this operation. Existing workflow, file, artifact
and interruption fixtures remain applicable. Native isolation canaries supplement
these tests; synthetic responses alone do not establish live qualification.

The 2026-09-19 candidate passed the same hosted-function assertions with Codex
0.153.4 and Claude SDK 0.3.269/native 2.1.269 using real Kimi K3. Each run used
an independent Core, dedicated Agents API database and Docker Runtime. Cold
restart acceptance restores Core before restarting Runtime; daemon startup while
Core is unavailable is not qualified by this test. Evidence is retained under
`~/.parsar/remediation/20260919/harness-parity/` on the validation server.

The broader protocol inventory remains in [README.md](README.md). Passing one
profile or these shared assertions does not establish complete compatibility.
