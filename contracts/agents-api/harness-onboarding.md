# Add a native Harness to OpenAgentCore

A **Harness** is a native agent engine (Codex, Claude Code, MiniMax Code) that
runs the model/tool loop. A **Harness adapter** translates the common
Core–Runtime execution contract into that engine's SDK or protocol. This guide is
the single numbered path for adding one. Qualification evidence and the
per-operation support table live in [Harness integration](harnesses.md).

Start from two entry points:

- [`internal/harnessconfig/harness.go`](../../internal/harnessconfig/harness.go):
  the shared model configuration contract (declarations and preparation).
- [`agent/harness.go`](../../apps/parsar-daemon/internal/agent/harness.go): the
  execution lifecycle, optional interfaces and registration methods.


## Ownership

```text
Core: Session, Turn, immutable configuration, durable events
                         |
              common Runtime protocol
                         |
Runtime: Executor preparation, reuse, idle expiry, recovery
                         |
               Harness adapter package
                         |
          native SDK, process or connection
```

| Component | Responsibility | Location |
| --- | --- | --- |
| Core | Public API, authority, durable state, scheduling and configuration snapshots | `services/agents-api` |
| Runtime | Authenticated connection, shared capability preparation and common Executor/Turn lifecycle | `apps/parsar-daemon/internal/dispatch` |
| Adapter | Native configuration, resources, API calls, event translation and restrictions | `apps/parsar-daemon/internal/agent/<kind>` |
| Harness | Native model/tool loop and history | Pinned SDK or executable |
| Service profile | Pure validation of qualified operations and placements | `services/agents-api/internal/engine` |
| Registration | Installed factories and verified capability declarations | `apps/parsar-daemon/internal/cli` |

An Environment supplies execution resources. Managed Docker, E2B and user-managed
machines differ in provisioning and connection; their connected Runtime uses this
same contract. Operating-system support belongs in the implementation and its
qualification. The native daemon supports Linux, macOS and Windows; each adapter
declares its qualified platform scope. Managed Providers remain Linux-only.
A platform-neutral interface alone does not qualify a harness on another platform.
See [native Runtime validation](../../docs/self-hosted-native.md) for the current
acceptance limits. Runtime connection, installed capability snapshot, Session Executor and Turn
each have their own lifetime; see
[Executor and Turn lifetimes](../../docs/runtime-protocol.md#executor-and-turn-lifetimes).
Native factories receive
capabilities only after the common Runtime has loaded its bound installed snapshot;
see [capability preparation](environments.md#runtime-capability-preparation).
Model providers supply
model communication configuration, not Turn scheduling or native process ownership.

## Steps

1. **Pin the native source.** Record the upstream package version and source
   revision and document the native entry point next to the adapter.
2. **Implement the adapter** in `apps/parsar-daemon/internal/agent/<kind>`: an
   `ExecutorFactory`, an `Executor` and a `Turn`. See
   [Required adapter interfaces](#required-adapter-interfaces). Reuse shared
   process, credential/configuration and local workspace helpers.
3. **Register the kind in the Runtime** in `apps/parsar-daemon/internal/cli`.
   See [Register the adapter](#register-the-adapter).
4. **Add the service profile and one catalog entry.** See
   [Add the engine to Core](#add-the-engine-to-core).
5. **Package native prerequisites.** Add a Runtime image under
   `services/agents-api/deploy/<kind>` and, optionally,
   [native installer participation](#native-installer-participation).
6. **Enable and select the engine** through
   [engine selection](#engine-selection).
7. **Qualify it.** Run the synthetic integration test, then the real acceptance in
   [Harness integration](harnesses.md#acceptance-checklist). Record results in
   the [qualification table](harnesses.md#current-qualified-operations).

Start with the mandatory text lifecycle, then qualify optional operations one at
a time. Call the reusable `agent/contracttest.TextLifecycle` assertions with the
adapter's prepared Executor and deterministic native fixture. These assertions
cover healthy reuse, durable input and cancellation; keep native fault and live
acceptance separate. Name the entry test `TestSharedTextLifecycle` so
`make check-runtime-contract` includes it. Do not copy an adapter's native
limitations into the shared Core protocol.

## Architecture rules

- Codex, Claude Code and future Harnesses have equal architectural status. The
  common Runtime wire protocol and Executor/Turn interfaces own lifecycle, input
  receipts, cancellation, recovery and resource access. Each adapter keeps its
  native implementation and model/tool loop.
- A new engine supplies an adapter, a qualified profile, registration and an
  independently verified deployment. It adds no engine-name branches to API
  handlers, persistence, dispatch, scheduling or Environment providers.
- Keep required lifecycle declarations, optional interfaces and registration
  methods in `agent/harness.go`. Result types, errors and Registry storage may
  stay in focused files. Keep this guide linked to that entry point.
- Use the existing `proto.SupportedAgentKind` and `AgentKindCapabilities`
  schema. Do not add a second capability descriptor or a combined optional
  interface.
- Onboarding does not require feature equality. Verify common lifecycle
  obligations and use the same public assertions for each declared operation.
  Optional native differences are separate capability work, not onboarding blockers.
- Never equate accepted parameters with applied native behavior.

## Native model configuration

[`internal/harnessconfig/harness.go`](../../internal/harnessconfig/harness.go) owns
the shared configuration declaration and pure preparation contract. Each adapter
supplies one `Configuration` to Core's composition and Runtime's `RegisterKind`.
All three Runtime entry paths validate through that declaration before native
side effects: direct factory, preparation and Executor. Registry wrappers retain
the declaration alongside the factory. Lifecycle and cleanup ownership remain in
[`agent/harness.go`](../../apps/parsar-daemon/internal/agent/harness.go).
The shared wire object is `proto.HarnessConfig`.

A supplied `model` must be a nonempty string, and an explicit `model_provider`
requires it. The native-owned connection path may omit both; explicit null is
invalid. An explicitly empty adapter declaration accepts no provider or nonempty
native parameters. It does not advertise provider support. Unknown protocol
formats and duplicate protocol declarations fail at registration.
Adapter declarations in
`internal/harnessconfig/<kind>` own these native fields:

| Harness | Accepted native fields | Application |
| --- | --- | --- |
| Codex | `model_reasoning_effort`: `none`, `minimal`, `low`, `medium`, `high`, `xhigh` | Native app-server `-c model_reasoning_effort=...` and each Turn's `collaborationMode.settings.reasoning_effort` |
| Claude SDK | `effort`: `low`, `medium`, `high`, `xhigh`, `max`; `thinking`: the SDK's `adaptive`, `enabled` or `disabled` object | SDK `Options.effort` and `Options.thinking` |
| MiniMax Code | Empty object only | Existing provider token limits remain required; additional native generation parameters are not qualified |

Claude `thinking` accepts `display` (`summarized` or `omitted`) for adaptive or
enabled thinking, and an optional positive integer `budgetTokens` only for
`enabled`. The deprecated `maxThinkingTokens` alternative is rejected.
These are native settings, not a shared reasoning vocabulary; model availability
and provider support remain the selected harness's responsibility.

Native protocol and parameter declarations also feed Core administration's small
configuration-support descriptor. Its ordered `protocols` list is the sole source
for accepted protocols and the default (the first entry). Core and Runtime reject
unsupported combinations through the same declaration. The current protocol
matrix belongs to [model execution](model-execution.md#saved-defaults-and-precedence).
Adapters connect directly through native configuration; they must not introduce
a model API proxy or protocol converter. Follow the per-input capability
requirements in the execution lifecycle contract; do not add a second model capability registry
or infer capabilities from model names. Native protocol acceptance and remote
model support remain separate facts.

Claude's private bridge receives compiled native options and performs structural
checks only, not a second copy of the declaration's rules. Planned fields and
ownership are in the [unified model configuration design](model-configuration-design.md).


## Required adapter interfaces

[`agent/harness.go`](../../apps/parsar-daemon/internal/agent/harness.go) is the
canonical interface entry point. Its required lifecycle is `ExecutorFactory`,
`Executor`, `Turn` and `TurnSettlement`. Optional Turn and workspace interfaces
remain separate; their result types and error values stay in the corresponding
operation files in the same package. All use the existing neutral protocol types.

The [Core–Runtime lifecycle contract](../../docs/runtime-protocol.md#executor-and-turn-lifetimes)
owns preparation failure, partial StartTurn results, output closure, settlement,
reuse and cleanup. Implement those obligations through the interfaces above.
Native callbacks and resources stay inside the adapter; Runtime owns admission,
idle expiry and replacement.

For example, a Codex adapter retains its app-server and thread, a Claude adapter
retains one streaming Query, and a MiniMax adapter retains its ACP connection and
native Session. Their implementations expose the same Executor and Turn contract.

### Cancellation ownership

Implement cancellation on the exact Turn through `agent.Session`. Follow the
[lifecycle and settlement rules](../../docs/runtime-protocol.md#executor-and-turn-lifetimes);
the adapter must supply native completion evidence to the shared Runtime.
Permission and user-choice responses use the optional interfaces below.

## Events, inputs and optional capabilities

Use [`internal/agentdaemon/proto`](../../internal/agentdaemon/proto) for neutral
requests, events and receipts. Each Turn emits only its own events with its Run ID,
in order, and one terminal outcome. Native IDs and usage must be observed rather
than invented. Capture the originating Turn before asynchronous native callbacks;
never attribute a late result to whichever Turn is currently active.

Initial input and steering use ordered `proto.MessageInput`. Preserve user-message
and content order. Text-only adapters reject images through `TextOnly()` instead
of dropping them. A successful transport write is distinct from confirmed native
application. User-choice answers use the emitted question ID and an array of
values; shared `PromptForUserChoiceDecisionPayload.AnswersFor` validates identity
before consuming a pending interaction. Do not map answers by header or position.
Resume only the exact history bound to the Session; missing or ambiguous required
history fails before new model input.

| Interface or contract | When required | Obligation |
| --- | --- | --- |
| `agent.DurableSteerer` | Current public text execution | Distinguish write and application receipts; preserve retry identity; independent of the optional non-durable `Steerer` |
| `agent.FunctionResultSubmitter` | Public function tools | Match call/result identity and acknowledge native application |
| `agent.PermissionResponder`, `agent.UserChoiceResponder` | When emitting these interactions | Route exact identities and settle receipts |
| `agent.WorkspaceReader`, `agent.WorkspaceDirectoryLister`, `agent.WorkspaceWriter` | Qualified workspace operations | Use the fixed authorized workspace and retain accepted operations through close |
| Neutral message, image, MCP, structured-output and Subagent observations | Only when qualified and advertised | Preserve the operation-specific contract and reject unsupported combinations |

Optional features need not match another harness. The service profile qualifies
public combinations; the Runtime advertises this installation's available support.
Neither replaces schema validation or tenant authorization. Declaring a capability
without implementing its semantics is an error. Workspace reads may use separate
read-only preparations; those do not start model work or provide another execution
lifecycle.

## Register the adapter

Registration is static and requires a build; dynamic plugins are outside this
contract. The methods live in `agent/harness.go`, and built-in adapters call them
from [`cli/agent_registration.go`](../../apps/parsar-daemon/internal/cli/agent_registration.go)
(Codex, MiniMax Code) and [`cli/claude_sdk.go`](../../apps/parsar-daemon/internal/cli/claude_sdk.go)
(Claude Code).

| Order | Method | Registers |
| --- | --- | --- |
| 1 | `RegisterKind(proto.SupportedAgentKind, harnessconfig.Configuration, agent.Factory)` | Kind, availability, version, `AgentKindCapabilities`, the adapter's model configuration declaration and the direct-call factory. Resets the other registrations, so call it first. |
| 2 | `RegisterExecutor(kind, agent.ExecutorFactory)` | The shared Executor/Turn lifecycle used for execution. Derives the `Preparation` capability. |
| 3 | `RegisterPreparation(kind, workspaceRead, agent.PreparationFactory)` | Optional. Separate read-only workspace preparation when qualified workspace operations need it. |

The direct-call `agent.Factory` should delegate to the same Executor
implementation. Every other capability declaration must match behavior verified
for that installation. Runtime registration does not grant Core qualification;
that belongs to the service profile.

The runnable test-only example is
[`testdata/onboarding/main.go`](../../apps/parsar-daemon/testdata/onboarding/main.go).
It registers a text-only synthetic Harness, demonstrates a Session-owned
Executor, fresh Turns, durable steering, cancellation and history binding, and is
never shipped as a real engine.

## Add the engine to Core

Core recognizes the [built-in Harness registrations](harness-catalog.md).
Add one entry to `internal/harnessconfig/builtin/catalog.json` with:

- the public `kind` and display `label`;
- the model `configuration` package under `internal/harnessconfig`;
- the `profile` constructor under `services/agents-api/internal/engine`.

Implement the profile constructor, then run `make generate-harness-catalog`.
This generates the model configuration registry, Core profile catalog, client
identifiers/display names and registration reference. Public input validators read
the generated registry. `make openapi` derives Harness enums from the same authored
catalog; do not add handwritten enums to DTO tags or route annotations.
`make check-harness-catalog` rejects stale projections.

Runtime registration uses `builtin.Configuration(kind)` for public Harnesses and
separately registers native factories, probes and installed capability evidence.
The catalog cannot declare a machine's availability. Adapter discovery and
packaging still require their own implementation and qualification; no dynamic
plugin loader is introduced.

The profile is pure: it declares supported placements, public configuration and
result limits and required Runtime controls, using existing public/protocol
types. Profile callbacks cannot query business data, decrypt credentials or
control native processes. Public schema validation, qualified engine support and
actual Runtime capabilities remain separate checks; Runtime advertisements alone
never enable public operations. Shared dispatch checks capability combinations,
not a whitelist of engine names.

`execution.Policy` supplies immutable service qualification to HTTP admission,
Worker device selection and final dispatch. Custom composition gives the same
Policy to `api.WithExecutionPolicy` and `Dispatcher.Policy`. The zero value uses
built-in profiles; an explicitly empty catalog authorizes none. There is no
mutable global registration or compatibility fallback.

## Engine selection

The [Harness selection contract](harness-selection.md) owns the public selector,
deployment enablement, defaults and immutable Session binding. This guide adds no
second selector or fallback rule.

## Required versus optional operations

The current public text path requires durable turns, applied input receipts,
ordered observations, cancellation and enforcement of disabled execution controls.
Check `execution.Policy.engineCapabilities` for the exact current requirements.
An engine without native tools can guarantee their absence; an engine with tools
must actually disable them when requested. Configuration acceptance is not proof
of enforcement.

MCP, public function calls, deferred function discovery, structured output, image inputs, verbosity controls and other optional
operations do not need to match another engine. Reject unqualified combinations
explicitly and record the gap. Never advertise a capability to bypass selection.

Structured-output adapters consume `ExecutionControls.OutputFormat` and publish
confirmed native output through the existing Message contract. Register public
qualification separately from the Runtime capability; see the
[structured-output boundary](structured-output.md). No Core engine-name branch is required.

Initial requests, `Executor.StartTurn` and steering consume the same ordered
`proto.MessageInput`. Text-only adapters use `TextOnly()` to reject images without
discarding content. Image adapters translate each part natively and acknowledge
an active batch only after all its messages are applied. Register
Runtime `MessageImages` and qualify the engine profile’s `MessageImages` separately; see the
[message-input contract and real acceptance](message-input.md).

Hosted workspace execution additionally requires verified preparation, workspace
reads/output export, network behavior and credential/history isolation. Reuse the
same dedicated Runtime binding and shared Files helpers. A native Bash sandbox
alone does not establish isolation for other native file tools. Enable a placement
only after its required security and lifecycle behavior is demonstrated.

### MCP origin and native limits

Declare supported public origins in the existing engine profile's `MCPOrigins`
and bearer support in `MCPBearer`. Runtime advertises actual HTTP, bearer and
required-initialization capabilities. Shared admission validates origin and
placement; adapter validation retains native label, allowlist and initialization
limits. These are separate checks, not a second MCP executor.

Consume `agent.ResolveMCPBindings` for public and installed declarations; preserve
origin, credential authority, null versus empty allowlists and required startup.
Do not copy tokens into native profiles or reinterpret a service request as an
Environment request. Reject unsupported native policies instead of dropping them.
Follow [the MCP origin contract](environments.md#public-mcp-connection-origin)
and run public-client, failure, cancellation and cold-recovery qualification for
each advertised combination. Model capability remains separate from Harness
transport support; never infer it from model names or silently degrade input.

## Optional Subagent observations

A harness that supports the Subagent resource reads implements the existing
[neutral observation contract](subagents.md#common-adapter-contract). It reports
verified child identity, lifecycle effects and owned Turn/Item history through
the authenticated Run, then qualifies those facts with real execution. It does
not add routes, storage branches or a harness-specific Core scheduler. Report
unsupported native facts explicitly; completing a child task is not closing its
Subagent. Native background work must remain owned through settlement and cancel.

## Native installer participation

An adapter may supply `agent.Installation` from `installation.go` in its own
package: registered agent kind, pinned version, supported platforms, activation
environment and a bounded
readiness probe. Register it in `cli/native_harness.go` and add its pinned component
to the native distribution builder. This optional contract does not change
Executor/Turn semantics. Runtime owns checksums, copying, locks and additive
installation; adapters own native layout and probes. Validate installation and
actual execution on each advertised platform. Missing or incompatible native
content must fail, never install itself during a Turn.

## Native process ownership

The shared daemon `clirunner` offers opt-in Unix process-group ownership for
adapters whose SDK launches a native child. Existing callers keep their current
process policy. Explicit cancellation and parent-context cancellation share a
TERM grace period and bounded KILL escalation. An internal reaper also cleans
remaining group members when the direct process exits, even if a descendant
still holds stdout open. During cancellation, surviving descendants keep the
remaining TERM grace after the leader exits. Unsupported hosts reject this mode before launch.

Owned output pipes remain readable after the leader exits. Consumers must drain
stdout and stderr before calling `Wait`, which joins the cached process result
and closes the readers. `Done` reports leader reaping and group cleanup signals;
it is not a native execution receipt or proof of persisted history. SDK adapters
must settle each Turn and drain its observations before publishing completion.
Executor close additionally closes the query and awaits the native child. Process groups are lifecycle supervision, not OS isolation
or containment of descendants that deliberately leave the group.

All Harness adapters use bypass execution. Do not restore named Codex permission
profiles, bubblewrap wrappers, native Claude sandbox settings or MiniMax
SandboxManager branches. There is one execution path for every Environment origin.
Resource paths are ordinary operator configuration, not a permission boundary.

The daemon does not enforce disabled or restricted network policies. Such a
combination must be rejected unless its outer Environment implementation provides
and qualifies the requested behavior. Do not advertise daemon-level network
isolation or silently run a restricted request with unrestricted semantics.
The normal self-hosted combination uses the host's existing network access.

## Native references

Use these adapters as implementation references after choosing a native API:

| Harness | Adapter | Native transport | Runtime guide |
| --- | --- | --- | --- |
| Codex | [`agent/codex`](../../apps/parsar-daemon/internal/agent/codex/executor.go) | app-server | [Codex Runtime](../../services/agents-api/deploy/codex/README.md) |
| Claude Code | [`agent/claudesdk`](../../apps/parsar-daemon/internal/agent/claudesdk/executor.go) | [TypeScript SDK bridge](../../packages/claude-sdk-adapter/README.md) | [Claude Runtime](../../services/agents-api/deploy/claude/README.md) |
| MiniMax Code | [`agent/mcode`](../../apps/parsar-daemon/internal/agent/mcode) | ACP and native workspace companion | [MiniMax Code Runtime](../../services/agents-api/deploy/mcode/README.md) |
