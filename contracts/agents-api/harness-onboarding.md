---
title: "Add a native Harness to OpenAgentCore"
---

A **Harness** is a native agent engine (Codex, Claude Code, MiniMax Code) that runs the model and tool loop. A **Harness adapter** translates the Runtime's Executor and Turn contract into that engine's SDK or protocol. This document is the Runtime–Harness protocol: the adapter interfaces and their lifecycle obligations, registration, Core qualification and acceptance. [Harness capabilities](./harness-capabilities.md) records what each current Harness supports.

Start from two entry points:

- [`internal/harnessconfig/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/harnessconfig/harness.go): the shared model configuration contract (declarations and preparation).
- [`agent/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/harness.go): the execution lifecycle, the extension contracts and the registration methods.

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
| Core | Public API, authority, durable state, scheduling and configuration snapshots | `services/core` |
| Runtime | Authenticated connection, shared capability preparation and the common Executor and Turn lifecycle | `apps/daemon/internal/dispatch` |
| Adapter | Native configuration, resources, API calls, event translation and restrictions | `apps/daemon/internal/agent/<kind>` |
| Harness | Native model and tool loop and history | Pinned SDK or executable |
| Service profile | Pure validation of qualified operations and placements | `services/core/internal/engine` |
| Registration | Adapter declarations, installed factories and verified capabilities | `apps/daemon/internal/agent/<kind>/declaration.go`; static list in `apps/daemon/internal/cli/agent_discovery.go` |

An Environment supplies execution resources. Managed E2B, Docker and microsandbox machines and application-owned machines differ in provisioning and connection; the connected Runtime uses this same contract. The daemon runs on Linux, macOS and Windows, managed Providers are Linux-only, and each adapter qualifies its own platforms ([self-hosted platforms](../../docs/getting-started/self-hosted.md#platforms)). Native factories receive capabilities only after the Runtime has loaded the bound installed snapshot ([capability preparation](./environments.md#runtime-capability-preparation)). Model providers supply model communication settings, not Turn scheduling or native process ownership.

## Steps

1. **Pin the native source.** Record the upstream package version and source revision and document the native entry point next to the adapter.
2. **Implement the adapter** in `apps/daemon/internal/agent/<kind>`: an `ExecutorFactory`, an `Executor` and a `Turn` ([required interfaces](#required-adapter-interfaces), [lifetimes](#executor-and-turn-lifetimes)). Reuse the shared process, credential, configuration and local workspace helpers.
3. **Declare the kind in the adapter** and add its declaration to the Runtime’s static list in `apps/daemon/internal/cli/agent_discovery.go` ([register the adapter](#register-the-adapter)).
4. **Add the service profile and one catalog entry** ([add the engine to Core](#add-the-engine-to-core)).
5. **Package native prerequisites.** Add a Runtime image under `services/core/deploy/<kind>` and, optionally, [native installer participation](#native-installer-participation).
6. **Enable and select the engine** with the `core.harnesses` setting and [Harness selection](./model-execution.md#harness-selection).
7. **Qualify it** ([qualify the adapter](#qualify-the-adapter)) and record the result in [Harness capabilities](./harness-capabilities.md).

Implement the mandatory text lifecycle and handle every extension explicitly. Qualify supported extensions one at a time; an unqualified extension returns `agent.ErrUnsupportedOperation` without native effects. A native cancellation may require retirement instead of reuse: `Reusable=false` carries a reason and the caller must confirm `Executor.Close`. Do not force reuse to fit a test helper, and do not copy an adapter's native limitations into the shared Core protocol.

## Architecture rules

- Codex, Claude Code and future Harnesses have equal standing. The common Runtime wire protocol and Executor and Turn interfaces own lifecycle, input receipts, cancellation, recovery and resource access; each adapter keeps its native implementation and model and tool loop.
- A new engine supplies an adapter, a qualified profile, registration and an independently verified deployment. It adds no engine-name branches to API handlers, persistence, dispatch, scheduling or Environment providers, and no handler, store table, scheduler, event projector or model loop for capabilities the contract already represents.
- Keep required lifecycle declarations, extension interfaces and registration methods in `agent/harness.go`. Result types, errors and Registry storage may stay in focused files.
- Use the existing `proto.SupportedAgentKind` and `AgentKindCapabilities` schema. Do not add a second capability descriptor or a combined optional interface.
- Onboarding does not require feature equality. Harnesses need not match each other's optional features, and MCP, functions, images or verbosity control are not required to register. Verify the common lifecycle obligations and use the same public assertions for each declared operation. An omitted declaration or a missing extension implementation blocks onboarding; a native difference does not.
- The service profile catalog is the qualification boundary. Unknown profiles fail closed, and a Runtime heartbeat cannot authorize new public functionality. Schema validity, service qualification and the available Runtime are independent checks.
- Never equate accepted parameters with applied native behavior.

## Required adapter interfaces

[`agent/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/harness.go) is the interface entry point. The required lifecycle is `ExecutorFactory`, `Executor`, `Turn` (including `DurableSteerer`) and `TurnSettlement`. Required methods perform their native obligations; returning Unsupported is not an implementation of cancellation, receipts, settlement or cleanup. Turn and workspace extension interfaces stay small and separate, but every public adapter implements each one explicitly. All use the neutral protocol types.

For example, the Codex adapter keeps its app-server and thread, the Claude adapter one streaming Query, and the MiniMax adapter its ACP connection and native session. All expose the same Executor and Turn contract. Native callbacks and resources stay inside the adapter; the Runtime owns admission, idle expiry and replacement. Cancellation targets the exact Turn through `agent.Session`, and the adapter supplies native completion evidence to the Runtime.

| Interface or contract | Required handling | Obligation |
| --- | --- | --- |
| `ExecutorFactory`, `Executor.StartTurn`, `Executor.Close` | Real implementation | Prepare without model input; keep ownership of failed or uncertain resources; confirm cleanup |
| `Session`, `Turn`, `CancellationOutcome`, `AwaitSettlement` | Real implementation | Cancel the exact Turn, keep observed results and confirm settlement independently of cancellation requests |
| `DurableSteerer` | Real implementation on every Turn | Distinguish a complete write from the native application receipt; keep retry identity |
| `Steerer` | Explicit implementation or Unsupported | Additional non-durable active-Turn input |
| `FunctionResultSubmitter` | Explicit implementation or Unsupported | Match native call and result identity and acknowledge application |
| `WorkspaceReader`, `WorkspaceDirectoryLister`, `WorkspaceWriter` | Explicit on Turn and Executor owners | Use the authorized workspace, confirm access, commit or close, or return the operation's Unsupported error |
| Neutral messages, images, MCP, structured output and Subagent observations | Explicit capability decisions | Keep each operation's protocol semantics; reject unsupported input before submission |

Each adapter's `contracts.go` holds an individual compile-time assertion for each small interface. Do not embed a default implementation that makes future interfaces appear implemented. Adding a contract also requires a classification in the common completeness check and an explicit assertion in every public adapter; the check follows the authored Harness catalog.

For a design-level refusal, implement the method directly:

```go
func (s *Session) SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error {
    return fmt.Errorf("%w: native public function tools are not qualified", agent.ErrUnsupportedOperation)
}
```

The reason is a fixed safe string, never submitted content, a credential or raw native diagnostics. Unsupported guarantees no native side effect and is not a successful empty operation. Installation unavailability, unknown call IDs, native failures and uncertain outcomes keep their own errors and ownership. A nil `Turn` still means that no input was submitted and the output stays with the caller; never use it as an Unsupported marker.

The wire request carries no working directory. The Runtime checks `local_environment.workspace_directory` against its binding and gives the Harness its bound workspace directory in `LocalEnvironment.WorkspaceRoot`; run the native Harness there.

Workspace capability describes the actual Runtime and resource-owner combination. The Codex and MiniMax resource objects reject native workspace access while the common authorized `localworkspace` owner provides it; Claude can expose native read and list access, and the common owner provides writes. Interface presence alone never selects a resource or advertises support.

The service profile qualifies public combinations and the Runtime advertises the installed combination; neither replaces schema validation or Project authorization. Native behavior tests must agree with the declarations. An advertised operation that returns Unsupported is a contract violation, never success or grounds for replay.

## Executor and Turn lifetimes

| Lifetime | Owner | Ends when |
| --- | --- | --- |
| Environment allocation | Sandbox Provider | Explicit reclamation, coordinated with Runtime execution |
| Runtime connection | Runtime transport | Disconnection or replacement by a newer connection |
| Installed capability snapshot | Runtime | Its Environment is reclaimed; never on Executor close |
| Session Executor | Runtime | `Executor.Close` on idle expiry, shutdown or confirmed invalidation |
| Turn | Adapter `Turn`, tracked by the Runtime | `AwaitSettlement` confirms settlement |

A Session owns one reusable Executor in its connected Runtime; a Turn owns one input execution, its output stream and its cancellation. `agent.ExecutorFactory` prepares the fixed configuration without model input, and `Executor.StartTurn` creates a new `agent.Turn` without replacing healthy native resources. Normal completion settles only the Turn. `Executor.Close` releases native resources on idle expiry, Environment shutdown or confirmed invalidation; it releases neither the Environment allocation nor the workspace. Core keeps no second Executor cache. The same lifecycle applies to hosted, self-hosted and `none` placements.

**Binding.** The Runtime binds its Executor record to the Session, Environment, connection and immutable execution configuration. Resume identity and prior-Turn recovery flags are continuity assertions, not configuration changes. A supplied native identity must match the retained owner, and when existing history is required, recovery never starts a new root. A configuration conflict is an error, not a hot switch. A lost connection retires its owners and handles; old timers, output and cancellation cannot affect their replacements.

**Per-Turn state.** Each Turn gets a fresh wrapper, output channel and receipt state. Steering and function interfaces belong to that Turn. Native callbacks capture the originating Turn before asynchronous work, so a late event is never attributed to whichever Turn is active. Native processes, query or transport connections, fixed capability configuration and native session identity belong to the Executor. Do not reset completed `sync.Once` values or reuse an old Turn object.

**Start.** A nil Turn from `StartTurn` guarantees that no native input was submitted and the output channel was not retained; the Runtime then closes the channel. Once input may have been submitted, return a non-nil Turn even with an error: that Turn owns exactly-once output closure and stays tracked until settlement. Unknown input is never replayed. A definite `executor_unavailable` Start rejection allows one common recovery attempt, only after the previous Executor has been closed and no input was submitted; the Runtime rechecks the same physical peer and the current authorization.

**Cancellation and settlement.** `Turn.Cancel` targets only that Turn and does not close a healthy Executor. `AwaitSettlement` applies after both natural completion and cancellation. Success means output can no longer be written and the Turn's native events, input, functions and child work have settled. Native completion or cancellation confirmation is independent of resource retirement: closing a transport cannot supply a missing native terminal or operation receipt.

- `Reusable=true` also confirms that the native owner can accept the next Turn. `Reusable=false` requires a reason and a later confirmed Executor close.
- An error means settlement is unconfirmed and frees neither ownership nor capacity. Caller deadlines stop the wait, not the tracked cleanup. Retry the same cleanup target serially; a failed cleanup blocks replacement and keeps its resource slot.
- `Executor.Close` confirms resource retirement independently of the Turn outcome: an immutable Turn error must not prevent closing the native transport once its work and output have stopped.
- Include owned background work in settlement and keep the exact native cleanup target after a failure. Native termination belongs to the adapter; a bulk cleanup acknowledgement alone does not establish quiescence.
- Every `Session` declares `CancellationOutcome`. `Turn` inherits it. The snapshot keeps observed native identity, Usage and output and remains readable after cancellation. Missing evidence stays unset; an empty `DonePayload` means nothing has been observed, not that cancellation succeeded or is unsupported. Reading the snapshot does not wait for settlement.
- `Session.Cancel` requests cancellation; output closure signals teardown. Turn settlement still requires `AwaitSettlement` and any required `Executor.Close`; neither a successful cancellation request nor its snapshot replaces those waits.

**What the Runtime does around a Turn.** One output consumer starts before native Start, drains the bounded 64-frame channel and keeps the terminal observation until Start publication, Turn settlement and admitted operation receipts finish. Natural completion never calls Cancel. Input and function admission close before settlement; operations already admitted hold their barrier through native receipts and outbound acknowledgement. The Runtime sends cancellation to the Turn before waiting on that barrier, because a written input may need a native interrupt to produce its receipt. It joins native settlement, any required confirmed Executor close, output drain and all admitted operations before an applied acknowledgement or reuse, and only then forwards Done or an applied cancellation receipt. A failed Close can report failure while keeping the same Run and outstanding operations for retry; a closed caller wait cannot manufacture an applied input receipt. The Runtime commits native continuity and releases the old Run's admission before publishing Done, since the receiver may start another Turn at once; a late terminal-send failure belongs to the old Run and cannot invalidate a successor that already owns the Executor. Connection shutdown owns transport-loss cleanup. The settlement wait is ten seconds and the receipt send budget five seconds; a timeout is not proof of quiescence.

## Events, inputs and optional capabilities

Use [`internal/agentdaemon/proto`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/internal/agentdaemon/proto) for neutral requests, events and receipts. Each Turn emits only its own events with its Run ID, in order, and one terminal outcome. Native IDs and usage are observed, never invented; a missing measurement is unknown, not zero.

Initial input and steering use ordered `proto.MessageInput`. Keep user-message and content order. Text-only adapters reject images through `TextOnly()` instead of dropping them; image adapters translate each part natively and acknowledge an active batch only after all its messages are applied. A successful transport write is distinct from confirmed native application. Resume only the exact history bound to the Session; missing, ambiguous or foreign history fails before new model input. Device identity is not native session ownership.

### Required and extension operations

The public text path requires durable Turns, applied input receipts, ordered observations, cancellation and enforcement of disabled execution controls; `execution.Policy.engineCapabilities` holds the exact requirements. An engine without native tools can guarantee their absence; an engine with tools must actually disable them when asked. Accepting a configuration is not proof of enforcement.

MCP, public functions, deferred function discovery, structured output, image input, verbosity controls and other optional operations need not match another engine. Reject an unqualified combination with Unsupported and record the gap; never advertise a capability to bypass selection.

- Structured output: consume `ExecutionControls.OutputFormat` and publish confirmed native output through the Message contract ([execution tools](./execution-tools.md#structured-output)). Register the public qualification separately from the Runtime capability.
- Images: register the Runtime's `MessageImages` and qualify the profile's `MessageImages` separately ([message input](./message-content.md)).
- Workspace placements additionally need verified preparation, workspace reads and output export and the dedicated Runtime binding with the shared Files helpers. Enable a placement only after its lifecycle behavior is demonstrated.

### MCP origin and native limits

Declare supported public origins in the engine profile's `MCPOrigins` and bearer support in `MCPBearer`. The Runtime advertises its actual HTTP, bearer and required-initialization capabilities. Shared admission validates origin and placement; adapter validation keeps native label, allowlist and initialization limits.

Consume `agent.ResolveMCPBindings` for public and installed declarations, and keep origin, credential authority, null versus empty allowlists and required startup. Do not copy tokens into native profiles or reinterpret a service request as an Environment request. Reject unsupported native policies instead of dropping them. Follow the [MCP origin contract](./environments.md#public-mcp-connection-origin) and run public-client, failure, cancellation and cold-recovery qualification for each advertised combination. Model capability is separate from Harness transport support; never infer it from model names or silently degrade input.

### Subagent observations

A Harness that supports the Subagent reads implements the [neutral observation contract](./subagents.md#adapter-contract). It reports verified child identity, lifecycle effects and owned Turn and Item history through the authenticated Run and qualifies those facts with real execution, without routes, storage branches or a Harness-specific scheduler. Report unsupported native facts explicitly; completing a child task is not closing its Subagent. Native background work stays owned through settlement and cancellation.

## Register the adapter

Registration is static and requires a build. Export one `agent.Declaration` from `apps/daemon/internal/agent/<kind>/declaration.go`, then add it to `harnessDeclarations` in [`cli/agent_discovery.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_discovery.go). The declaration contains the kind and complete capability descriptor, the shared model `Configuration` and a `Discover` function. Discovery receives the profile and diagnostic writers, owns native configuration and availability checks, and returns the installed `agent.Runtime` with its descriptor, Executor factory and view declaration. Return nil when the adapter is not configured; return an unavailable descriptor without an Executor factory or view when configured prerequisites fail. Keep version gates and factory-selection conditions inside the adapter.

[`cli/agent_registration.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_registration.go) iterates the discovered runtimes and calls `Registry.Register` from `agent/harness.go`. It verifies that discovery retained the declared kind and registers the Runtime in this order:

| Order | Method | Registers |
| --- | --- | --- |
| 1 | `RegisterKind(proto.SupportedAgentKind, harnessconfig.Configuration)` | Kind, availability, version, `AgentKindCapabilities` and the model configuration declaration. It resets the other registrations, so call it first. |
| 2 | `RegisterExecutor(kind, agent.ExecutorFactory)` | The Executor and Turn lifecycle used for execution; derives the `Preparation` capability |
| 3 | `RegisterView(kind, agent.View)` | Optional: the agent-host view declaration from `Runtime.View`. It panics with `ErrInvalidView` when `View.Validate` fails. Its Executor factory validates the model configuration like `RegisterExecutor` and enforces the [gateway rule](#endpoints-and-proxy). |

`Runtime.View` declares how the Harness runs in an agent-host Session view, described in [Run in an agent-host view](#run-in-an-agent-host-view). Every adapter sets it explicitly; `View: nil` means the agent host rejects the kind, and `Registry.ResolveView` returns an error wrapping `ErrUnsupportedOperation`. `TestPublicHarnessContractDeclarations` requires the field in each declaration.

Every `proto.AgentKindCapabilities` field must be explicitly `proto.CapabilitySupported` or `proto.CapabilityUnsupported`, even for an unavailable Harness. `proto.CapabilityUnspecified` is invalid: zero values and omitted fields never mean Unsupported. An installation probe may set an individual field with `proto.CapabilityFromBool`; it must not populate unmentioned or future fields. Availability stays separate in `SupportedAgentKind.Available`. Registration validates the complete declaration before changing the registry, and the wire carries an explicit boolean for every field, so omitted and null fields are invalid. A new field requires a decision in every production declaration. Runtime consumers use `IsSupported()` and reject unsupported requests before native operations; an interface assertion verifies implementation, never support. Every declaration must match the behavior verified for that installation; the [Core–Runtime protocol](../../docs/runtime-protocol.md#capability-declarations) owns how declarations travel and are frozen.

The admission mapping is explicit. `Steering` controls non-durable `Steerer` input. `DurableInputReceipts` controls `DurableSteerer` input and also requires the Turn settlement contract; neither implies the other, and Core's public text profile requires both. Workspace declarations describe the authorized resource owner, including the common Runtime workspace implementation. Runtime registration does not grant Core qualification; the service profile does.

The runnable test-only example [`testdata/onboarding/main.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/testdata/onboarding/main.go) registers a text-only synthetic Harness. It shows a Session-owned Executor, fresh Turns, durable steering, cancellation and history binding, and is never shipped.

## Add the engine to Core

Core recognizes the [built-in Harness registrations](./harness-catalog.md). Add one entry to `internal/harnessconfig/builtin/catalog.json` with:

- the public `kind` and display `label`;
- the model `configuration` package under `internal/harnessconfig`;
- the `profile` constructor under `services/core/internal/engine`.

Implement the profile constructor, then run `make generate-harness-catalog`. It generates the model configuration registry, Core profile catalog, client identifiers and display names and the registration reference; public input validators read the generated registry. `make openapi` derives Harness enums from the same catalog, so do not add handwritten enums to DTO tags or route annotations. `make check-harness-catalog` rejects stale projections.

Each Runtime declaration references the same `internal/harnessconfig/<kind>.Configuration()` used by the generated catalog and owns its native factories, probes and installed capability evidence. The catalog cannot declare a machine's availability, and there is no dynamic plugin loader.

The profile is pure: it declares supported placements, public configuration and result limits and required Runtime controls, using existing public and protocol types. Profile callbacks cannot query business data, decrypt credentials or control native processes. Shared dispatch checks capability combinations, not a whitelist of engine names.

### Explicit service qualification

`engine.Profile` is the service's qualification declaration, separate from the Runtime's `AgentKindCapabilities`. Its capability fields reuse the small `proto.CapabilitySupport` value type: every field must explicitly select `CapabilitySupported` or `CapabilityUnsupported`. `CapabilityUnspecified`, including an omitted field, is rejected. Sharing this value type does not let a Runtime advertisement grant service authorization.

Each of `ConfigurationValidation`, `ToolsValidation` and `FunctionResultValidation` chooses one of two strategies:

- `CommonValidationOnly`: common schema and admission checks are sufficient. The corresponding callback must be nil; no successful placeholder callback is needed.
- `AdditionalValidation`: the matching `ValidateConfiguration`, `ValidateTools` or `ValidateFunctionResult` callback is mandatory and adds pure Harness restrictions.

An omitted or unknown policy, missing required callback, or callback paired with common-only policy is invalid. Admission follows the declared policy, never method presence. Preserve existing error precedence: configuration restrictions run first; when additional configuration validation is selected, tool-decoding errors precede tool restrictions. With common-only configuration validation, additional tool restrictions retain their existing precedence over a decoding error. Common-only function-result validation adds no native result restriction.

`engine.NewCatalog` validates every entry before publishing its immutable snapshot and panics with `engine.ErrInvalidDeclaration` for invalid static registrations. Kinds must be nonempty without surrounding whitespace. Placements must explicitly list at least one supported placement; MCP origins must be a non-nil list (an empty list qualifies none). Unknown or duplicate choices, origins without a corresponding placement and bearer support without an MCP origin are rejected. Errors identify authored fields without echoing declaration values. Future profile fields must be classified by the completeness validator and explicitly decided by every profile; there is no production default-filling constructor.

Run the `engine` and `execution` tests for omission, policy, combination and error precedence coverage, and the public onboarding tests in `services/core/tests/integration` for admission and Runtime dispatch. Test fixtures use `engine/enginetest`, whose exhaustive literal also requires a decision when a field is added; it is not a production profile.

`execution.Policy` supplies immutable service qualification to HTTP admission, Worker device selection and final dispatch. Custom composition gives the same Policy to `api.Dependencies.Policy` and the Core dispatcher's `Policy`. The zero value uses the built-in profiles; an explicitly empty catalog authorizes none. There is no mutable global registration.

## Native model configuration

[`internal/harnessconfig/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/harnessconfig/harness.go) owns the shared configuration declaration and pure preparation contract. Each adapter supplies one `Configuration`, in `internal/harnessconfig/<kind>`, to Core's composition and to the Runtime's `RegisterKind`. The Executor and view paths both validate through that declaration before native side effects, and Registry wrappers keep the declaration with the factory. The wire object is `proto.HarnessConfig`. [Model execution](./model-execution.md#native-model-parameters) lists each Harness's accepted fields.

A supplied `model` must be a nonempty string, and an explicit `model_provider` requires it. The native-owned connection path may omit both; explicit null is invalid. An explicitly empty declaration accepts no provider or nonempty native parameters and advertises no provider support. Unknown protocol formats and duplicate protocol declarations fail at registration.

The declaration's ordered `protocols` list is the only source of accepted protocols and the default (the first entry); it also feeds Core's configuration-support descriptor, and Core and Runtime reject unsupported combinations through it. Adapters connect through native configuration and the [credential gateway](./model-execution.md#credential-gateway); they never introduce their own model API proxy or protocol converter, a second model capability registry, or capabilities inferred from model names. Claude's private bridge receives compiled native options and performs structural checks only, not a second copy of the declaration's rules.

## Qualify the adapter

Before starting, record the operation set, expected results, exclusions and stopping conditions. A qualification ends when its declared operations pass; it does not expand to match another Harness's feature list.

1. **Contract tests.** Call `agent/contracttest.TextLifecycle` from a test named `TestSharedTextLifecycle` with the adapter's prepared Executor and a deterministic native fixture; `claudesdk/executor_test.go` is the reference. It checks independent Turn streams, native owner and history continuity, durable write and application receipts, stale cancellation and healthy continuation after cancellation. `make check-runtime-contract` runs it together with the shared wire, gateway, transport and dispatcher tests, the declaration completeness check and each adapter's `TestUnsupportedExtensionsHaveNoNativeEffects`. Adapter tests also cover two ordinary Turns sharing one native process or connection and history, cancellation followed by another Turn, stale cancellation and late events, native exit, cleanup failure, input write and application receipts, unknown outcomes and fresh per-Turn usage, function, input and child-observation state. State whether a fixture is controlled or a real provider.
2. **Shared integration.** `TestThirdHarnessPublicOnboarding` runs the synthetic Harness through public Session and input admission, Worker device selection, the real WebSocket gateway, the daemon Registry and Router, neutral events and durable terminal projection. It uses a custom immutable `engine.Catalog` in the same `execution.Policy` given to the API handler and the dispatcher, and checks applied input receipts, saved native identity, continuation, cancellation, unsupported optional requests and missing mandatory Runtime support. The fixture has no workspace, MCP or public functions, and its registration stays local to the test. It proves the integration path, not native execution.
3. **Real acceptance.** Use the pinned official Python SDK and raw HTTP against Core, a real provider API, the native Harness and a dedicated database. Verify initial execution, a warm follow-up, cancellation and restart with continuation; record native owner identity and same-condition cold and warm timing. For workspace placements also verify Files and Artifacts, workspace identity, that no credentials appear in public responses and that foreign history is rejected. `services/core/tests/official_hosted_functions_native.py` holds the shared function assertions: success and error, native file output and public Artifact bytes, same-history continuation after restart, foreign result rejection and pending-call cancellation. Synthetic or failed runs never count. The opt-in tests below run the pinned-SDK fixtures in `services/core/tests` against a real daemon and model; each runs when `OAC_TEST_OFFICIAL_SDK_PYTHON`, `OAC_TEST_NATIVE_DAEMON_BIN`, `OAC_TEST_NATIVE_PROOF_DIR` and its private options file are set. The options file is a JSON object with exactly `model` and `model_provider` (the fields of `x_agents_core.model_provider`); the test sets it as the deployment default model provider, which the fixtures' `environment: none` Sessions freeze at creation.
4. **Regression.** Existing Harnesses keep working. Run targeted tests, then `make check`; run `make openapi` after API changes and `make sqlc-generate` after query changes.
5. **Review.** Follow the [blind review workflow](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#review).

| Operation | Test in `services/core/tests/integration` | Options file variable, and Harness variable where the test takes one |
| --- | --- | --- |
| Model provider protocols | `TestNativeModelProtocolPublicExecution` | [Model execution](./model-execution.md#acceptance) |
| MiniMax Code text | `TestNativeMCodePublicExecution` | `OAC_TEST_MCODE_REAL_OPTIONS` |
| Message images | `TestNativeMessageImagePublicExecution` | `OAC_TEST_MESSAGE_IMAGE_REAL_OPTIONS`, `OAC_TEST_MESSAGE_IMAGE_ENGINE` |
| Function results with images | `TestNativeFunctionImagePublicExecution` | `OAC_TEST_FUNCTION_IMAGE_REAL_OPTIONS`, `OAC_TEST_FUNCTION_IMAGE_ENGINE` |
| Structured output | `TestNativeStructuredOutputPublicExecution` | `OAC_TEST_STRUCTURED_OUTPUT_REAL_OPTIONS` |
| Deferred function discovery | `TestNativeToolSearchPublicExecution` | `OAC_TEST_TOOL_SEARCH_REAL_OPTIONS` |
| Disabled web search and programmatic tool calling | `TestNativeToolPolicyPublicExecution` | `OAC_TEST_TOOL_POLICY_REAL_OPTIONS`, `OAC_TEST_TOOL_POLICY_ENGINE` |

Environment acceptance uses `services/core/tests/official_environment_{templates,setup,skills,plugins,plugin_mcp,composition,initial_files,network,skill_references}.py`. For composed preparation, change the Skill default and Template, delete the sources, retry and restart; verify frozen bytes, one setup execution and MCP cancellation. `official_hosted_structured_native.py` covers hosted structured output. Record exact source revisions, native versions and commands with each acceptance result.

Keep provider keys in private operator files, never in commits or logs. Existing focused tests, relative to `apps/daemon/internal/agent`:

| Boundary | Tests |
| --- | --- |
| Codex reuse, cancellation and unconfirmed cleanup | `codex/executor_test.go`, `terminal_cleanup_test.go` |
| Codex input receipts and strict recovery | `codex/function_write_receipt_test.go`, `function_receipt_test.go`, `resume_test.go`, `recovery_test.go` |
| Claude input ownership, cancellation and preparation cleanup | `claudesdk/executor_test.go`, `cancellation_test.go`, `preparation_test.go` |
| MiniMax cancellation retirement, failed Start and cleanup retry | `mcode/executor_test.go`, `executor_backpressure_test.go` |
| MiniMax native history binding | `mcode/session_test.go` |
| Explicit refusals without native effects or fabricated results | Each adapter's `unsupported_test.go` |

## Native installer participation

An adapter may supply `agent.Installation` from `installation.go` in its own package: registered kind, pinned version, supported platforms, activation environment and a bounded readiness probe. Register it in `cli/native_harness.go` and add its pinned component to the native distribution builder. This optional contract does not change Executor and Turn semantics. The Runtime owns checksums, copying, locks and additive installation; adapters own native layout and probes. Validate installation and execution on each advertised platform. Missing or incompatible native content fails; it never installs itself during a Turn.

The agent-host image uses the same contract. `deploy/distribution/AgentHost.Dockerfile` installs each Harness in its own directory and lists it in the image's manifest, `/opt/oac/harnesses.json`, and `agent.ManifestEnvironment` activates it from there through `Installation.Environment`. A Harness the agent host runs is added there too.

## Native process ownership

The daemon's `clirunner` starts every native child in its own Unix process group (a Job object on Windows); other hosts reject the launch. Explicit and parent-context cancellation share a TERM grace period (three seconds by default) and a bounded KILL escalation. An internal reaper also cleans remaining group members when the direct process exits, even if a descendant still holds stdout open; during cancellation, surviving descendants keep the remaining grace after the leader exits. The daemon's `stop` command waits up to ten seconds for confirmed shutdown, which covers that grace period and the pipe and owner cleanup after it.

Owned output pipes stay readable after the leader exits. Consumers drain stdout and stderr before calling `Wait`, which joins the cached process result and closes the readers. `Done` reports leader reaping and group cleanup signals; it is not a native execution receipt or proof of persisted history. SDK adapters settle each Turn and drain its observations before publishing completion, and Executor close also closes the query and awaits the native child. Process groups are lifecycle supervision, not isolation or containment of descendants that leave the group.

Adapters run native tools unattended with the launching user's permissions, and a Harness never asks a human. Codex runs with approval policy `never`, under which it settles MCP elicitation itself without reaching the client, and with full access; the adapter also disables its blocking `request_user_input` tool. Claude runs through the adapter's tool callback, which allows or denies without asking, in native `default` permission mode with the SDK sandbox disabled. MiniMax runs with bypassed permissions and its sandbox disabled; the adapter disables `askUser`, advertises no elicitation and answers `session/request_permission` with the ACP `cancelled` outcome, which MiniMax treats as a denial. No native permission or question request reaches Core: a human in the loop goes through a function tool, the Session reads [`requires_action`](./sessions-events.md#session-status) and the application submits the function result. Do not add permission profiles, bubblewrap wrappers or native sandbox settings; there is one execution path for every Environment origin. Resource paths are operator configuration, not a permission boundary.

Network admission follows [Restricted network](./environments.md#restricted-network).

## Run in an agent-host view

An agent host runs the Harness outside the sandbox, in a per-Session view. The view shows the sandbox's files at `/` over the [File access protocol](../../docs/file-access-protocol.md), and the Harness's own files under `/.oac`. Programs the Harness does not declare local run in the sandbox over the [Process protocol](../../docs/process-protocol.md). The network has loopback only, where the Session's credential gateway listens. The adapter declares what its Harness needs in `Runtime.View`, and the agent host builds each view from that declaration and the Session. Support is the declared field: a kind without a View is rejected with `ErrUnsupportedOperation`.

### The declaration

| Field | Declares |
| --- | --- |
| `Closure` | Host directories presented read-only and executable at `/.oac/<Name>` (`ViewMount.Path`) |
| `Overlays` | Trusted host files or directories presented read-only at view paths; `Exec` makes one executable |
| `Masks` | View paths presented empty and read-only, as a directory when `Dir` is set |
| `LocalExec` | Every view path the Harness process tree executes locally |
| `Shims` | Names on `/.oac/bin`; each runs that name on the Environment's tool `PATH` in the sandbox |
| `ShimPaths` | View paths the shim is bound over; each runs the same path in the sandbox |
| `ForwardEnv` | Harness variables that a process run in the sandbox keeps |
| `Proxy` | `ViewProxyEnv` or `ViewProxyNone` |
| `Capabilities` | What the view runs ([Capabilities](#capabilities)) |
| `Executor` | The `ViewExecutorFactory` that prepares the Session's Executor in its view |

`View.Validate` checks the declaration without touching the host:

- view and host paths are absolute and clean;
- closure names are single path components other than `bin`, `home` and `run`, which the agent host uses for the shims, the Session home and the process relay;
- shim paths, overlays and masks do not overlap each other or `/`, and stay out of the trees the view builds itself: `/.oac`, `/proc` and `/dev` (`ViewReserved`);
- each `LocalExec` entry lies in a closure directory or an `Exec` overlay;
- shim names and `ForwardEnv` names are unique, no shim is named `oac-process-shim`, which is the process relay's, or starts with `oac-mcp-`, which [stdio aliases](#stdio-mcp) use, a variable name contains no `=`, and `ForwardEnv` names no variable the view or the broker sets ([Environment](#environment));
- every `Capabilities` field is `proto.CapabilitySupported` or `proto.CapabilityUnsupported`;
- `Proxy` is one of the two values and `Executor` is non-nil.

`harness.go` defines the view layout once, and `sessionview` builds views from it. The agent host checks its own overlays, such as `/etc/passwd`, against the declaration when it builds the view.

### Capabilities

`View.Capabilities` declares each feature the view runs, and the agent host admits a request before any effect only when the view supports each feature the request uses. The registry the agent host gives dispatch derives `EnvironmentNone`, `FunctionTools`, `FunctionResultImages` and `ToolSearch` from it.

| Field | A request that uses it |
| --- | --- |
| `EnvironmentNone` | Sets `DisableExecutionEnvironment` ([Environment none](#environment-none)) |
| `Skills` | Has resolved Skills (`LocalEnvironment.Skills`) |
| `FunctionTools`, `FunctionResultImages`, `ToolSearch` | Uses the feature of the same `AgentKindCapabilities` name |
| `StdioMCP` | Has a stdio MCP binding ([Stdio MCP](#stdio-mcp)) |

Whatever the view declares, the agent host rejects with `ErrUnsupportedOperation` a request without strict resume, one whose installed Capabilities no preparation resolved, and one with a restricted network, because only the Provider's workload network boundary can contain a process's own sockets. It rejects a stdio binding that needs a credential with `ErrViewHandoff`.

### Environment none

A request with `DisableExecutionEnvironment` runs in an empty-root view: a read-only, noexec tmpfs at `/` that holds only the mountpoints for the closure, the Session home, the agent host's runtime files, `/proc`, `/dev` and the overlays. It has no sandbox files, no shims, no Link attachment and no sandbox network, so the generic proxy refuses every request; the cgroup, the isolation and the gateway stay. The request carries no `LocalEnvironment`, and the Harness runs in `/.oac/home/work` (`ViewWorkName`). The request already expresses the profile, so the wire has no field for it. A request with neither `LocalEnvironment` nor `DisableExecutionEnvironment` is an incomplete binding, and the agent host rejects it.

### Executables

Only mount flags grant execution. The closure, `Exec` overlays and the shim are read-only and are the only executable mounts; the sandbox's files and the home are noexec. `Launch` and `Spawn` accept only a `LocalExec` path as `Binary` and otherwise return `ErrNotLocalExec`. A dynamic binary, such as `node`, needs its ELF interpreter as an `Exec` overlay at its `PT_INTERP` path, and every library it loads in the closure, reached through `LD_LIBRARY_PATH`. Nothing loads from the sandbox's files. `viewloader.For` builds this from the binaries' ELF headers: the interpreter's host directory as the `lib` closure mount, the interpreter overlay, empty masks over `/etc/ld.so.preload` and `/etc/ld.so.cache`, and the `LD_LIBRARY_PATH` value. A layout it cannot present, such as a library outside the interpreter's directory, returns `ErrUnsupportedOperation`.

### Shims

The agent host derives the process broker's table from the declaration: `/.oac/bin/<name>` runs `<name>` in the sandbox, and each `ShimPaths` entry runs the same path there. A Harness that runs tools by name finds them through a `PATH` that lists `/.oac/bin`. The view's `/etc/passwd` gives the Session user the login shell `/bin/bash` and the home `/.oac/home`.

### Environment

`Launch` takes the complete Harness environment in `StartOptions.Env`, which the adapter derives from its installation and the request's typed fields; the request carries no environment values. The agent host's own environment never passes through, so a view adapter does not start from `os.Environ()`. A process run in the sandbox gets the broker's environment: the `ForwardEnv` variables from the Harness, the Environment's fixed sandbox values (`HOME`, `PATH`, `TMPDIR` and `LANG`) and the Environment's tool environment. The broker is the only home of the tool environment, and a view adapter passes none of it to the Harness. The agent host keeps model and MCP credentials only in the gateway's protected configuration and adds none to the Harness's environment, a Process spec or a capability tree the view exposes.

`ForwardEnv` never names a variable the view or the broker sets: `HOME`, `PATH`, `TMPDIR`, `LANG`, `LD_LIBRARY_PATH`, or `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` and `NO_PROXY` in any case. When the Environment's tool environment also sets a forwarded variable, the tool environment's value wins.

### Endpoints and proxy

Before it calls the factory, the agent host points the request's `model_provider` at the Session's [credential gateway](./model-execution.md#credential-gateway): `base_url` is `http://127.0.0.1:<port>` with no path and `api_key` is `modelprovider.Placeholder`. It resolves the Session's MCP once, from the public declarations and the installed Environment MCP, into `ViewSession.MCP`, and removes both from the request. Only HTTP bindings go to the gateway: each points at its gateway URL and carries no bearer and no headers, and the gateway adds the declared credential and headers. A stdio binding runs under its [alias](#stdio-mcp). A view Executor takes MCP only from `ViewSession.MCP` and never resolves the request. The adapter renders the provider and the bindings as it does for a local Harness and never sees a real credential.

The Registry checks each view request once, before the factory, and rejects it with `ErrViewHandoff` when its model provider is missing or is not the gateway with the placeholder, when it carries MCP outside `ViewSession.MCP`, when an HTTP binding is not a credential-free loopback endpoint, or when a stdio binding is not its alias.

With `ViewProxyEnv`, `ViewSession.Proxy` is the gateway's proxy URL. The adapter sets `HTTPS_PROXY` and `HTTP_PROXY` to it and `NO_PROXY` to `127.0.0.1,localhost`, each in upper and lower case. Declare `ViewProxyEnv` only after qualifying that every request the Harness makes locally honours these variables. A request that ignores them fails to connect, because the view has no route out.

With `ViewProxyNone`, `ViewSession.Proxy` is empty and the view has no generic proxy. Admission rejects a request that enables a feature needing one with `ErrUnsupportedOperation`. Web tools that the provider executes keep provider origin.

### Stdio MCP

A stdio binding runs in the sandbox under its alias. The binding at index `i` of `ViewSession.MCP` has exactly `Stdio: {Server: {Name: ServerLabel, Type: "stdio", Command: agent.ViewAlias(i)}}`, a name under `/.oac/bin` with the `oac-mcp-` prefix, and the Harness runs that path without arguments. The process broker maps the alias to the binding's frozen command, args and `CWD`, a relative `CWD` resolving against the installation's package root, and runs it as it runs a shim's process, with nothing from the Harness's argv, working directory or environment. A stdio binding whose credential authority is not `none` is rejected with `ErrViewHandoff`.

### Home

`ViewSession.Home` is the per-Session native home. The adapter writes at `Home.Host`, and the Harness sees the same directory at `Home.View` (`/.oac/home`), read-write and noexec. It persists across the Session's Executors. Lay out native directories and write configuration under it before calling `Launch`. `Launch` gives the tree to the Session user without following links; after that, read the home without following links.

### Launch

`ViewSession.Launch` replaces `clirunner.Start`. Each call builds one view and runs `Binary` in it, and at most one view per Session is live at a time. `Dir` is a path in the sandbox and `Env` is the complete environment. The returned `clirunner.Process` follows [Native process ownership](#native-process-ownership):

- Cancel sends TERM to every process in the view and closes the view after `KillTimeout`. A Cancel that finds the Harness exited leaves its exit as it was, even while the processes it left still end.
- When the Harness exits while other processes remain, the view sends them TERM unless Cancel already did, and ends once they exit or `KillTimeout` passes from the first TERM.
- `Wait` closes the stdio ends, returns the context error when Cancel's TERM reached the running Harness and it then exited 0, and `ExitCode` reports the exit once `Done` closes.

### Spawn

`ViewSession.Spawn` runs a `LocalExec` binary as another process in the live view while the Harness runs, such as a reader of the Harness's native history. Harness-side code that reads Harness-written data runs here, never on the agent host outside the view and never in a view of its own. `Spawn` takes `StartOptions` as `Launch` does and returns the same `clirunner.Process`. The process runs as the Harness does: as the same user, in the same namespaces, view cgroup, world and network, with no capabilities, `no_new_privs` and the same seccomp filter, in a process group of its own.

- The view starts one `Spawn` at a time. `Parent` bounds the wait for its turn and for the start. Once it ends, `Spawn` returns its error and kills a process that starts after all.
- Cancel sends TERM to its process group and kills the group after `KillTimeout`. Once the process has exited, Cancel delivers nothing, and what it left runs on as the view's other processes do.
- The view's end ends them all. A Cancel of the Harness reaches them, and when the Harness exits they are among the processes that remain. A view that ends after `Spawn` returned shows in the process's `Wait`.
- `Spawn` returns `ErrNotLocalExec` when `Binary` is not a `LocalExec` path, and `ErrNoLiveView` when no view runs its Harness: none was launched yet, or its Harness has exited or its view has ended. Any other failure, such as a binary that does not start or no descriptors left, keeps its own error.

### Qualify the view

Run the adapter's Turns, cancellation and continuation in a view, then qualify each declared entry:

| Entry | Qualification |
| --- | --- |
| `Closure`, `Overlays`, `LocalExec` | Every local exec succeeds from a declared path. Each dynamic binary's interpreter overlay matches its `PT_INTERP`, and `LD_LIBRARY_PATH` resolves every library in the closure. |
| `Masks` | The Harness reads none of the sandbox's files at the masked paths. |
| `Shims`, `ShimPaths` | Each tool the Harness runs by name or path runs in the sandbox, and its output, exit status and signals reach the Harness. |
| `ForwardEnv` | A process run in the sandbox keeps each declared variable and no other Harness variable. |
| `Proxy` | With `ViewProxyEnv`, every local request, such as web fetches, downloads and update checks, goes through the proxy. With `ViewProxyNone`, a request enabling a feature that needs it is rejected. |
| `Home` | Native history and configuration stay under `/.oac/home`, and a later Executor in the same Session continues from them. |
| `Capabilities` | Each supported feature runs a Turn through dispatch: environment none in the empty-root view, Skills, function calls and results, tool search, and each stdio binding under its alias. |

`scripts/qualify-agent-host.sh` runs each Harness's Turns through the daemon's dispatch against the [agent-host and sandbox images](../../docs/maintainers.md#runtime-images-and-helpers). The `agenthostqualify` test binary runs as the agent host with the [agent-host container's flags](../../docs/configuration.md#agent-host-container), and the sandbox image serves the sandbox. The first Turn writes a file and reports the output and exit status of a failing command whose values only the sandbox's tool environment holds. When the view declares function tools, a second Turn runs in a new Executor that resumes the Session's native history and calls a function; the test returns a text, image and text result through dispatch, and the answer must report both texts. When the view declares tool search, a Turn in another Session finds the deferred function with tool search and calls it. When the view declares environment none, a Turn in a Session without an Environment answers through the model, and the Harness's native state in the Session home must name its working directory, `/.oac/home/work`. When the view declares stdio MCP, the test gives a Session's Environment one installed stdio MCP server, a script that runs in the sandbox, and the answer must report the code its one tool returns. The Link runs over WSS with a CA the test generates. The test also checks the cgroup v2 delegation: the container's own read-only cgroup fails with `ErrUnsupported`, and in a delegated directory the agent host ends a cgroup left behind with `cgroup.kill`. Set `OAC_AGENT_HOST_IMAGE` and `OAC_SANDBOX_IMAGE` to the two images, `OAC_QUALIFY_KEY_FILE` to the model key's file and, for each Harness to qualify, `OAC_QUALIFY_CLAUDE_SDK`, `OAC_QUALIFY_CODEX` or `OAC_QUALIFY_MCODE` to its `model` and `model_provider` without `api_key`. The gateway dials model providers directly, so on a host whose only egress is an HTTP proxy, set `OAC_QUALIFY_PROXY` to it and the test tunnels the providers' hosts through it.

## Native references

| Harness | Adapter | Native transport | Runtime guide |
| --- | --- | --- | --- |
| Codex | [`agent/codex`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/codex/executor.go) | app-server | [Codex Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/codex/README.md) |
| Claude Code | [`agent/claudesdk`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/claudesdk/executor.go) | [TypeScript SDK bridge](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/packages/claude-sdk-adapter/README.md) | [Claude Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/claude/README.md) |
| MiniMax Code | [`agent/mcode`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/daemon/internal/agent/mcode) | ACP and native workspace companion | [MiniMax Code Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/mcode/README.md) |
