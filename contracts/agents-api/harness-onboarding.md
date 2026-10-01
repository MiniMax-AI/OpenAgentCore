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
| `PermissionResponder`, `UserChoiceResponder` | Explicit implementation or Unsupported | Respond to exact emitted identities; unknown or expired interactions stay distinct from Unsupported |
| `WorkspaceReader`, `WorkspaceDirectoryLister`, `WorkspaceWriter` | Explicit on Turn, Executor and Prepared owners | Use the authorized workspace, confirm access, commit or close, or return the operation's Unsupported error |
| `Prepared`, `PreparedCancellation` | Real implementation for an executable preparation | Keep resource and output ownership across Start, cancellation and unused cleanup |
| Neutral messages, images, MCP, structured output and Subagent observations | Explicit capability decisions | Keep each operation's protocol semantics; reject unsupported input before submission |

Each adapter's `contracts.go` holds an individual compile-time assertion for each small interface. Do not embed a default implementation that makes future interfaces appear implemented. Adding a contract also requires a classification in the common completeness check and an explicit assertion in every public adapter; the check follows the authored Harness catalog.

For a design-level refusal, implement the method directly:

```go
func (s *Session) SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error {
    return fmt.Errorf("%w: native public function tools are not qualified", agent.ErrUnsupportedOperation)
}
```

The reason is a fixed safe string, never submitted content, a credential or raw native diagnostics. Unsupported guarantees no native side effect and is not a successful empty operation. Installation unavailability, unknown interaction IDs, native failures and uncertain outcomes keep their own errors and ownership. A nil `Turn` still means that no input was submitted and the output stays with the caller; never use it as an Unsupported marker.

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

**Per-Turn state.** Each Turn gets a fresh wrapper, output channel and receipt state. Steering, function, permission and user-choice interfaces belong to that Turn. Native callbacks capture the originating Turn before asynchronous work, so a late event is never attributed to whichever Turn is active. Native processes, query or transport connections, fixed capability configuration and native session identity belong to the Executor. Do not reset completed `sync.Once` values or reuse an old Turn object.

**Start.** A nil Turn from `StartTurn` guarantees that no native input was submitted and the output channel was not retained; the Runtime then closes the channel. Once input may have been submitted, return a non-nil Turn even with an error: that Turn owns exactly-once output closure and stays tracked until settlement. Unknown input is never replayed. A definite `executor_unavailable` Start rejection allows one common recovery attempt, only after the previous Executor has been closed and no input was submitted; the Runtime rechecks the same physical peer and the current authorization.

**Cancellation and settlement.** `Turn.Cancel` targets only that Turn and does not close a healthy Executor. `AwaitSettlement` applies after both natural completion and cancellation. Success means output can no longer be written and the Turn's native events, input, functions, interactions and child work have settled. Native completion or cancellation confirmation is independent of resource retirement: closing a transport cannot supply a missing native terminal or operation receipt.

- `Reusable=true` also confirms that the native owner can accept the next Turn. `Reusable=false` requires a reason and a later confirmed Executor close.
- An error means settlement is unconfirmed and frees neither ownership nor capacity. Caller deadlines stop the wait, not the tracked cleanup. Retry the same cleanup target serially; a failed cleanup blocks replacement and keeps its resource slot.
- `Executor.Close` confirms resource retirement independently of the Turn outcome: an immutable Turn error must not prevent closing the native transport once its work and output have stopped.
- Include owned background work in settlement and keep the exact native cleanup target after a failure. Native termination belongs to the adapter; a bulk cleanup acknowledgement alone does not establish quiescence.
- Every `Session`, including a direct-call factory result, declares `CancellationOutcome`. `Turn` and `PreparedCancellation` inherit it. The snapshot keeps observed native identity, Usage and output and remains readable after cancellation. Missing evidence stays unset; an empty `DonePayload` means nothing has been observed, not that cancellation succeeded or is unsupported. Reading the snapshot does not wait for settlement.
- Direct-call `Session.Cancel` requests cancellation; output closure signals teardown. Executable `PreparedCancellation.Cancel` waits for local cleanup and output writes to stop. Turn settlement still requires `AwaitSettlement` and any required `Executor.Close`; neither a successful cancellation request nor its snapshot replaces those waits.

**What the Runtime does around a Turn.** One output consumer starts before native Start, drains the bounded 64-frame channel and keeps the terminal observation until Start publication, Turn settlement and admitted operation receipts finish. Natural completion never calls Cancel. Input, function and interaction admission close before settlement; operations already admitted hold their barrier through native receipts and outbound acknowledgement. The Runtime sends cancellation to the Turn before waiting on that barrier, because a written input may need a native interrupt to produce its receipt. It joins native settlement, any required confirmed Executor close, output drain and all admitted operations before an applied acknowledgement or reuse, and only then forwards Done or an applied cancellation receipt. A failed Close can report failure while keeping the same Run and outstanding operations for retry; a closed caller wait cannot manufacture an applied input receipt. The Runtime commits native continuity and releases the old Run's admission before publishing Done, since the receiver may start another Turn at once; a late terminal-send failure belongs to the old Run and cannot invalidate a successor that already owns the Executor. Connection shutdown owns transport-loss cleanup. The settlement wait is ten seconds and the receipt send budget five seconds; a timeout is not proof of quiescence.

## Events, inputs and optional capabilities

Use [`internal/agentdaemon/proto`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/internal/agentdaemon/proto) for neutral requests, events and receipts. Each Turn emits only its own events with its Run ID, in order, and one terminal outcome. Native IDs and usage are observed, never invented; a missing measurement is unknown, not zero.

Initial input and steering use ordered `proto.MessageInput`. Keep user-message and content order. Text-only adapters reject images through `TextOnly()` instead of dropping them; image adapters translate each part natively and acknowledge an active batch only after all its messages are applied. A successful transport write is distinct from confirmed native application. User-choice answers use the emitted question ID and an array of values; the shared `PromptForUserChoiceDecisionPayload.AnswersFor` validates identity before consuming a pending interaction, so never map answers by header or position. Resume only the exact history bound to the Session; missing, ambiguous or foreign history fails before new model input. Device identity is not native session ownership.

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

Registration is static and requires a build. Export one `agent.Declaration` from `apps/daemon/internal/agent/<kind>/declaration.go`, then add it to `harnessDeclarations` in [`cli/agent_discovery.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_discovery.go). The declaration contains the kind and complete capability descriptor, the shared model `Configuration` and a `Discover` function. Discovery receives the profile and diagnostic writers, owns native configuration and availability checks, and returns the installed `agent.Runtime` with its descriptor and session, preparation and Executor factories. Return nil when the adapter is not configured; return an unavailable descriptor with a session factory when configured prerequisites fail. Keep version gates and factory-selection conditions inside the adapter.

`Runtime.SessionCapabilityContext` and `Runtime.ExecutorCapabilityContext` explicitly request capability-download URL resolution and scoped product-upload context for the corresponding execution factory. Preparation never receives those effects. An adapter that supports product workspace authoring declares `WorkspaceAuthoring` itself; common registration does not grant it.

[`cli/agent_registration.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_registration.go) iterates the discovered runtimes and calls `Registry.Register` from `agent/harness.go`. It verifies that discovery retained the declared kind and installs factories in this order:

| Order | Method | Registers |
| --- | --- | --- |
| 1 | `RegisterKind(proto.SupportedAgentKind, harnessconfig.Configuration, agent.Factory)` | Kind, availability, version, `AgentKindCapabilities`, the model configuration declaration and the direct-call factory. It resets the other registrations, so call it first. |
| 2 | `RegisterExecutor(kind, agent.ExecutorFactory)` | The Executor and Turn lifecycle used for execution; derives the `Preparation` capability |
| 3 | `RegisterPreparation(kind, workspaceRead, agent.PreparationFactory)` | Optional: separate read-only workspace preparation for qualified workspace operations |

The direct-call `agent.Factory` delegates to the same Executor implementation.

Every `proto.AgentKindCapabilities` field must be explicitly `proto.CapabilitySupported` or `proto.CapabilityUnsupported`, even for an unavailable Harness. `proto.CapabilityUnspecified` is invalid: zero values and omitted fields never mean Unsupported. An installation probe may set an individual field with `proto.CapabilityFromBool`; it must not populate unmentioned or future fields. Availability stays separate in `SupportedAgentKind.Available`. Registration validates the complete declaration before changing the registry, and the wire carries an explicit boolean for every field, so omitted and null fields are invalid. A new field requires a decision in every production declaration. Runtime consumers use `IsSupported()` and reject unsupported requests before native operations; an interface assertion verifies implementation, never support. Every declaration must match the behavior verified for that installation; the [Core–Runtime protocol](../../docs/runtime-protocol.md#capability-declarations) owns how declarations travel and are frozen.

The admission mapping is explicit. `Steering` controls non-durable `Steerer` input. `DurableInputReceipts` controls `DurableSteerer` input and also requires the Turn settlement contract; neither implies the other, and Core's public text profile requires both. `Permissions` qualifies permission and user-choice responses together and requires both native response paths. Workspace declarations describe the authorized resource owner, including the common Runtime workspace implementation. Runtime registration does not grant Core qualification; the service profile does.

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

Run the `engine` and `execution` tests for omission, policy, combination and error precedence coverage, and the public onboarding/store tests for admission and Runtime dispatch. Test fixtures use `engine/enginetest`, whose exhaustive literal also requires a decision when a field is added; it is not a production profile.

`execution.Policy` supplies immutable service qualification to HTTP admission, Worker device selection and final dispatch. Custom composition gives the same Policy to `api.Dependencies.Policy` and the Core dispatcher's `Policy`. The zero value uses the built-in profiles; an explicitly empty catalog authorizes none. There is no mutable global registration.

## Native model configuration

[`internal/harnessconfig/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/harnessconfig/harness.go) owns the shared configuration declaration and pure preparation contract. Each adapter supplies one `Configuration`, in `internal/harnessconfig/<kind>`, to Core's composition and to the Runtime's `RegisterKind`. The direct factory, preparation and Executor paths all validate through that declaration before native side effects, and Registry wrappers keep the declaration with the factory. The wire object is `proto.HarnessConfig`. [Model execution](./model-execution.md#native-model-parameters) lists each Harness's accepted fields.

A supplied `model` must be a nonempty string, and an explicit `model_provider` requires it. The native-owned connection path may omit both; explicit null is invalid. An explicitly empty declaration accepts no provider or nonempty native parameters and advertises no provider support. Unknown protocol formats and duplicate protocol declarations fail at registration.

The declaration's ordered `protocols` list is the only source of accepted protocols and the default (the first entry); it also feeds Core's configuration-support descriptor, and Core and Runtime reject unsupported combinations through it. Adapters connect directly through native configuration; they never introduce a model API proxy or protocol converter, a second model capability registry, or capabilities inferred from model names. Claude's private bridge receives compiled native options and performs structural checks only, not a second copy of the declaration's rules.

## Qualify the adapter

Before starting, record the operation set, expected results, exclusions and stopping conditions. A qualification ends when its declared operations pass; it does not expand to match another Harness's feature list.

1. **Contract tests.** Call `agent/contracttest.TextLifecycle` from a test named `TestSharedTextLifecycle` with the adapter's prepared Executor and a deterministic native fixture; `claudesdk/executor_test.go` is the reference. It checks independent Turn streams, native owner and history continuity, durable write and application receipts, stale cancellation and healthy continuation after cancellation. `make check-runtime-contract` runs it together with the shared wire, gateway, transport and dispatcher tests, the declaration completeness check and each adapter's `TestUnsupportedExtensionsHaveNoNativeEffects`. Adapter tests also cover two ordinary Turns sharing one native process or connection and history, cancellation followed by another Turn, stale cancellation and late events, native exit, cleanup failure, input write and application receipts, unknown outcomes and fresh per-Turn usage, function, input and child-observation state. State whether a fixture is controlled or a real provider.
2. **Shared integration.** `TestThirdHarnessPublicOnboarding` runs the synthetic Harness through public Session and input admission, Worker device selection, the real WebSocket gateway, the daemon Registry and Router, neutral events and durable terminal projection. It uses a custom immutable `engine.Catalog` in the same `execution.Policy` given to the API handler and the dispatcher, and checks applied input receipts, saved native identity, continuation, cancellation, unsupported optional requests and missing mandatory Runtime support. The fixture has no workspace, MCP, public functions, permissions or user-choice handlers, and its registration stays local to the test. It proves the integration path, not native execution.
3. **Real acceptance.** Use the pinned official Python SDK and raw HTTP against Core, a real provider API, the native Harness and a dedicated database. Verify initial execution, a warm follow-up, cancellation and restart with continuation; record native owner identity and same-condition cold and warm timing. For workspace placements also verify Files and Artifacts, workspace identity, that no credentials appear in public responses and that foreign history is rejected. `services/core/tests/official_hosted_functions_native.py` holds the shared function assertions: success and error, native file output and public Artifact bytes, same-history continuation after restart, foreign result rejection and pending-call cancellation. Synthetic or failed runs never count. The opt-in tests below run the pinned-SDK fixtures in `services/core/tests` against a real daemon and model; each runs when `OAC_TEST_OFFICIAL_SDK_PYTHON`, `OAC_TEST_NATIVE_DAEMON_BIN`, `OAC_TEST_NATIVE_PROOF_DIR` and its private options file are set. The options file is a JSON object with exactly `model` and `model_provider` (the fields of `x_agents_core.model_provider`); the test sets it as the deployment default model provider, which the fixtures' `environment: none` Sessions freeze at creation.
4. **Regression.** Existing Harnesses keep working. Run targeted tests, then `make check`; run `make openapi` after API changes and `make sqlc-generate` after query changes.
5. **Review.** Follow the [blind review workflow](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#review).

| Operation | Test in `services/core/internal/store` | Options file variable, and Harness variable where the test takes one |
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
| Codex reuse, cancellation and unconfirmed cleanup | `codex/executor_test.go`, `terminal_cleanup_test.go`, `prepared_cancel_test.go` |
| Codex input receipts and strict recovery | `codex/function_write_receipt_test.go`, `function_receipt_test.go`, `resume_test.go`, `recovery_test.go` |
| Claude input ownership, cancellation and preparation cleanup | `claudesdk/executor_test.go`, `cancellation_test.go`, `preparation_test.go` |
| MiniMax cancellation retirement, failed Start and cleanup retry | `mcode/executor_test.go`, `executor_backpressure_test.go` |
| MiniMax native history binding | `mcode/session_test.go` |
| Explicit refusals without native effects or fabricated results | Each adapter's `unsupported_test.go` |

## Native installer participation

An adapter may supply `agent.Installation` from `installation.go` in its own package: registered kind, pinned version, supported platforms, activation environment and a bounded readiness probe. Register it in `cli/native_harness.go` and add its pinned component to the native distribution builder. This optional contract does not change Executor and Turn semantics. The Runtime owns checksums, copying, locks and additive installation; adapters own native layout and probes. Validate installation and execution on each advertised platform. Missing or incompatible native content fails; it never installs itself during a Turn.

## Native process ownership

The daemon's `clirunner` offers opt-in Unix process-group ownership for adapters whose SDK launches a native child; unsupported hosts reject this mode before launch. Explicit and parent-context cancellation share a TERM grace period (three seconds by default) and a bounded KILL escalation. An internal reaper also cleans remaining group members when the direct process exits, even if a descendant still holds stdout open; during cancellation, surviving descendants keep the remaining grace after the leader exits. The daemon's `stop` command waits up to ten seconds for confirmed shutdown, which covers that grace period and the pipe and owner cleanup after it.

Owned output pipes stay readable after the leader exits. Consumers drain stdout and stderr before calling `Wait`, which joins the cached process result and closes the readers. `Done` reports leader reaping and group cleanup signals; it is not a native execution receipt or proof of persisted history. SDK adapters settle each Turn and drain its observations before publishing completion, and Executor close also closes the query and awaits the native child. Process groups are lifecycle supervision, not isolation or containment of descendants that leave the group.

Adapters run native tools unattended with the launching user's permissions: Codex with approval policy `never` and full access, Claude through the adapter's tool callback in native `default` permission mode with the SDK sandbox disabled, and MiniMax with bypassed permissions and its sandbox disabled. Do not add permission profiles, bubblewrap wrappers or native sandbox settings; there is one execution path for every Environment origin. Resource paths are operator configuration, not a permission boundary.

Network admission follows [Restricted network](./environments.md#restricted-network).

## Native references

| Harness | Adapter | Native transport | Runtime guide |
| --- | --- | --- | --- |
| Codex | [`agent/codex`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/codex/executor.go) | app-server | [Codex Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/codex/README.md) |
| Claude Code | [`agent/claudesdk`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/claudesdk/executor.go) | [TypeScript SDK bridge](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/packages/claude-sdk-adapter/README.md) | [Claude Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/claude/README.md) |
| MiniMax Code | [`agent/mcode`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/daemon/internal/agent/mcode) | ACP and native workspace companion | [MiniMax Code Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/mcode/README.md) |
