---
title: "Add a Harness"
---

A **Harness** is a native agent engine (Codex, Claude Code, MiniMax Code) that runs the model and tool loop. A **Harness adapter** translates the Runtime's Executor and Turn contract into that engine's SDK or protocol. This document is the Runtime–Harness protocol: the adapter interfaces and their lifecycle obligations, registration, the support declaration and acceptance.

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
| Declaration | The Harness's support, against which Core and the Runtime admit each selection | `internal/harnessconfig/<kind>` |
| Registration | Adapter declarations, installed views and the installation's narrowed support | `apps/daemon/internal/agent/<kind>/declaration.go`; catalog-generated list in `apps/daemon/internal/cli/harness_catalog_linux.go` |

An Environment supplies execution resources. Managed E2B, Docker and microsandbox machines and application-owned machines differ in provisioning and connection; each serves the sandbox to the Linux agent host, which runs every Harness in a [view](#run-in-an-agent-host-view) of it under this same contract. Native factories receive capabilities only after the Runtime has loaded the bound installed snapshot ([capability preparation](./environments.md#runtime-capability-preparation)). Model providers supply model communication settings, not Turn scheduling or native process ownership.

## Steps

1. **Pin the native source.** Add the upstream package version to the [Harness catalog](./harness-catalog.md), keep any source revision in one authored owner ([native version pins](#native-version-pins)), and document the native entry point next to the adapter.
2. **Implement the adapter** in `apps/daemon/internal/agent/<kind>`: a view whose `ViewExecutorFactory` prepares an `Executor`, and a `Turn` ([required interfaces](#required-adapter-interfaces), [lifetimes](#executor-and-turn-lifetimes), [view](#run-in-an-agent-host-view)). Reuse the shared process, credential and configuration helpers.
3. **Declare its support and register it.** Declare the support in `internal/harnessconfig/<kind>` with one catalog entry ([declare support](#declare-support)), then export the adapter declaration for generated agent-host registration ([register the adapter](#register-the-adapter)).
4. **Package native prerequisites.** Supply the adapter's installation and add the Harness to the agent-host image ([native Harness packaging](#native-harness-packaging)).
5. **Enable and select the engine** with the `core.harnesses` setting and [Harness selection](./model-execution.md#harness-selection).
6. **Qualify it** ([qualify the adapter](#qualify-the-adapter)) and record each native difference in the [coverage ledger](./index.md).

Implement the mandatory text lifecycle and handle every extension explicitly. Qualify supported extensions one at a time; an unqualified extension returns `agent.ErrUnsupportedOperation` without native effects. A native cancellation may require retirement instead of reuse: `Reusable=false` carries a reason and the caller must confirm `Executor.Close`. Do not force reuse to fit a test helper, and do not copy an adapter's native limitations into the shared Core protocol.

## Architecture rules

- Codex, Claude Code and future Harnesses have equal standing. The common Runtime wire protocol and Executor and Turn interfaces own lifecycle, input receipts, cancellation, recovery and resource access; each adapter keeps its native implementation and model and tool loop.
- A new engine supplies an adapter, its declaration, registration and an independently verified deployment. It adds no engine-name branches to API handlers, persistence, dispatch, scheduling or Environment providers, and no handler, store table, scheduler, event projector or model loop for capabilities the contract already represents.
- Keep required lifecycle declarations, extension interfaces and registration methods in `agent/harness.go`. Result types, errors and Registry storage may stay in focused files.
- Use the existing `proto.Declaration` and `proto.SupportedAgentKind` schema. Do not add a second capability descriptor or a combined optional interface.
- Onboarding does not require feature equality. Harnesses need not match each other's optional features, and MCP, functions, images or verbosity control are not required to register. Verify the common lifecycle obligations and use the same public assertions for each declared operation. An omitted declaration or a missing extension implementation blocks onboarding; a native difference does not.
- The static declaration is the support boundary. Unknown kinds fail closed, and Core rejects a heartbeat that widens a declaration. Schema validity, the declaration and the available Runtime are independent checks.
- Never equate accepted parameters with applied native behavior.

## Required adapter interfaces

[`agent/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/harness.go) is the interface entry point. The required lifecycle is `ViewExecutorFactory`, `Executor`, `Turn` and `TurnSettlement`. `Turn` is one interface: `Cancel`, `CancellationOutcome`, `SteerWithReceipt`, `SubmitFunctionResult` and `AwaitSettlement`. Required methods perform their native obligations; returning Unsupported is not an implementation of cancellation, receipts, settlement or cleanup. An operation the adapter does not support returns Unsupported, and the capability declaration, not the method, decides whether the Runtime calls it. All use the neutral protocol types.

The view's `ViewExecutorFactory` takes one `agent.PrepareRequest`: the Session's configuration as `execution_prepare` carries it, the model configuration that the Registry prepared once from the kind's declaration (`Prepared`), the Session's native state key (`StateKey`), and the Environment's workspace and installed Capabilities (`WorkspaceRoot`, `CapabilityRoot`, `Skills`, `MCP`), which its owner fills. The adapter takes its model, provider and native parameters only from `Prepared` and never parses `model` or `model_provider` itself. A Turn's Run ID and input arrive in `Executor.StartTurn`.

For example, the Codex adapter keeps its app-server and thread, the Claude adapter one streaming Query, and the MiniMax adapter its ACP connection and native session. All expose the same Executor and Turn contract. Native callbacks and resources stay inside the adapter; the Runtime owns admission, idle expiry and replacement. Cancellation targets the exact Turn through `Turn.Cancel`, and the adapter supplies native completion evidence to the Runtime.

| Interface or contract | Required handling | Obligation |
| --- | --- | --- |
| `ViewExecutorFactory`, `Executor.StartTurn`, `Executor.Close` | Real implementation | Prepare without model input; keep ownership of failed or uncertain resources; confirm cleanup |
| `Turn.Cancel`, `CancellationOutcome`, `AwaitSettlement` | Real implementation | Cancel the exact Turn, keep observed results and confirm settlement independently of cancellation requests |
| `Turn.SteerWithReceipt` | Real implementation | Distinguish a complete write from the native application receipt; keep retry identity |
| `Turn.SubmitFunctionResult` | Real implementation or Unsupported | Match native call and result identity and acknowledge application |
| Images, MCP, structured output and Subagent observations | Explicit capability decisions | Keep each operation's protocol semantics; reject unsupported input before submission |

Each adapter's `contracts.go` asserts at compile time that it implements `agent.Executor` and `agent.Turn`. Do not embed a default implementation that makes a new method appear implemented. The common completeness check follows the authored Harness catalog and rejects any other exported interface in `agent`.

For a design-level refusal, implement the method directly:

```go
func (s *Session) SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error {
    return fmt.Errorf("%w: native public function tools are not qualified", agent.ErrUnsupportedOperation)
}
```

The reason is a fixed safe string, never submitted content, a credential or raw native diagnostics. Unsupported guarantees no native side effect and is not a successful empty operation. Installation unavailability, unknown call IDs, native failures and uncertain outcomes keep their own errors and ownership. A nil `Turn` still means that no input was submitted and the output stays with the caller; never use it as an Unsupported marker.

The execution request carries no working directory. The Environment owner freezes the workspace from `assignment_bind.workspace_directory` before preparation and gives the Harness that directory in `PrepareRequest.WorkspaceRoot`; run the native Harness there.

Workspace reads, writes, output export and read-only preparation belong to the Session's [Environment owner](../../docs/runtime-protocol.md#session-assignments), not the adapter. An adapter implements none of them. Its declaration's `LocalEnvironment` and `EnvironmentNone` state what its Executors run, and `agent.Registry.Register` composes them once with what the Runtime's owner serves (`agent.EnvironmentSupport`), keeping each only where the owner serves it. The composed `LocalEnvironment` also admits the owner's workspace reads, read-only preparation and output export. One declaration holds for every Executor of the install.

The declaration states the public combinations the Harness supports and the heartbeat narrows it to the installation; neither replaces schema validation or Project authorization. Native behavior tests must agree with the declarations. An advertised operation that returns Unsupported is a contract violation, never success or grounds for replay.

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

**Per-Turn state.** Each Turn gets a fresh wrapper, output channel and receipt state. Steering and function results belong to that Turn. Native callbacks capture the originating Turn before asynchronous work, so a late event is never attributed to whichever Turn is active. Native processes, query or transport connections, fixed capability configuration and native session identity belong to the Executor. Do not reset completed `sync.Once` values or reuse an old Turn object.

**Start.** A nil Turn from `StartTurn` guarantees that no native input was submitted and the output channel was not retained; the Runtime then closes the channel. Once input may have been submitted, return a non-nil Turn even with an error: that Turn owns exactly-once output closure and stays tracked until settlement. Unknown input is never replayed. A definite `executor_unavailable` Start rejection allows one common recovery attempt, only after the previous Executor has been closed and no input was submitted; the Runtime rechecks the same physical peer and the current authorization.

**Cancellation and settlement.** `Turn.Cancel` targets only that Turn and does not close a healthy Executor. `AwaitSettlement` applies after both natural completion and cancellation. Success means output can no longer be written and the Turn's native events, input, functions and child work have settled. Native completion or cancellation confirmation is independent of resource retirement: closing a transport cannot supply a missing native terminal or operation receipt.

- `Reusable=true` also confirms that the native owner can accept the next Turn. `Reusable=false` requires a reason and a later confirmed Executor close.
- An error means settlement is unconfirmed and frees neither ownership nor capacity. Caller deadlines stop the wait, not the tracked cleanup. Retry the same cleanup target serially; a failed cleanup blocks replacement and keeps its resource slot.
- `Executor.Close` confirms resource retirement independently of the Turn outcome: an immutable Turn error must not prevent closing the native transport once its work and output have stopped.
- Include owned background work in settlement and keep the exact native cleanup target after a failure. Native termination belongs to the adapter; a bulk cleanup acknowledgement alone does not establish quiescence.
- Every Turn implements `CancellationOutcome`. The snapshot keeps observed native identity and Usage and remains readable after cancellation. Missing evidence stays unset; an empty `DonePayload` means nothing has been observed, not that cancellation succeeded or is unsupported. Reading the snapshot does not wait for settlement.
- `Turn.Cancel` requests cancellation; output closure signals teardown. Turn settlement still requires `AwaitSettlement` and any required `Executor.Close`; neither a successful cancellation request nor its snapshot replaces those waits.

**What the Runtime does around a Turn.** One output consumer starts before native Start, drains the bounded 64-frame channel and keeps the terminal observation until Start publication, Turn settlement and admitted operation receipts finish. Natural completion never calls Cancel. Input and function admission close before settlement; operations already admitted hold their barrier through native receipts and outbound acknowledgement. The Runtime sends cancellation to the Turn before waiting on that barrier, because a written input may need a native interrupt to produce its receipt. It joins native settlement, any required confirmed Executor close, output drain and all admitted operations before an applied acknowledgement or reuse, and only then forwards Done or an applied cancellation receipt. A failed Close can report failure while keeping the same Run and outstanding operations for retry; a closed caller wait cannot manufacture an applied input receipt. The Runtime commits native continuity and releases the old Run's admission before publishing Done, since the receiver may start another Turn at once; a late terminal-send failure belongs to the old Run and cannot invalidate a successor that already owns the Executor. Connection shutdown owns transport-loss cleanup. The settlement wait is ten seconds and the receipt send budget five seconds; a timeout is not proof of quiescence.

## Events, inputs and optional capabilities

Use [`internal/agentdaemon/proto`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/internal/agentdaemon/proto) for neutral requests, events and receipts. Each Turn emits only its own events with its Run ID, in order, and one terminal outcome. Native IDs and usage are observed, never invented; a missing measurement is unknown, not zero.

Every assistant message carries its native message ID. Emit `output_message` `in_progress` with that ID, each text fragment as a `delta` naming it in `item_id`, then `output_message` `completed` with the message's full text ([message order](../../docs/runtime-protocol.md#message-families)). A native protocol without a message end, such as ACP, completes a message when the next one starts or the Turn ends. `Done` and `CancellationOutcome` carry no answer text; a message left open by cancellation or failure stays open.

Initial input and steering use ordered `proto.MessageInput`. Keep user-message and content order. Text-only adapters reject images through `TextOnly()` instead of dropping them; image adapters translate each part natively and acknowledge an active batch only after all its messages are applied. A successful transport write is distinct from confirmed native application. Resume only the exact history bound to the Session; missing, ambiguous or foreign history fails before new model input. Device identity is not native session ownership.

### Required and extension operations

The public text path requires durable Turns, applied input receipts, ordered observations, cancellation and enforcement of disabled execution controls. An engine without native tools can guarantee their absence; an engine with tools must actually disable them when asked. Accepting a configuration is not proof of enforcement.

MCP, public functions, deferred function discovery, structured output, image input, verbosity controls and other optional operations need not match another engine. Declare what is unsupported, a combination as a `Conflicts` pair, and record the gap; never advertise a capability to bypass selection.

- Structured output: consume `ExecutionControls.OutputFormat` and publish confirmed native output through the Message contract ([execution tools](./execution-tools.md#structured-output)). Declare `Binary64OutputSchema` when the native SDK reads JSON numbers as binary64.
- Images: declare `MessageImages` and `FunctionResultImages`, and whether function results admit image URLs and images in a failed result ([message input](./message-content.md)).

### MCP origin and native limits

Declare the supported public origins in `MCPOrigins`, HTTP, bearer and required-initialization support in the capabilities, and native limits as data: `MCPAllowedTools`, `ReservedMCPLabels` and the `MCPLabel` and `MCPToolName` patterns. `proto.ValidateSelection` checks them with the origin and placement, and the adapter does not check them again.

Consume `agent.ResolveMCPBindings` for public and installed declarations, and keep origin, credential authority, null versus empty allowlists and required startup. Do not copy tokens into native profiles or reinterpret a service request as an Environment request. Reject unsupported native policies instead of dropping them. Follow the [MCP origin contract](./environments.md#public-mcp-connection-origin) and run public-client, failure, cancellation and cold-recovery qualification for each advertised combination. Model capability is separate from Harness transport support; never infer it from model names or silently degrade input.

### Subagent observations

A Harness that supports the Subagent reads implements the [neutral observation contract](./subagents.md#adapter-contract). It reports verified child identity, lifecycle effects and owned Turn and Item history through the authenticated Run and qualifies those facts with real execution, without routes, storage branches or a Harness-specific scheduler. Report unsupported native facts explicitly; completing a child task is not closing its Subagent. Native background work stays owned through settlement and cancellation.

## Register the adapter

Registration is static and requires a build. Export one `agent.Declaration` from `apps/daemon/internal/agent/<configuration>/declaration.go`. `make generate-harness-catalog` generates the agent host's declaration and `Installation()` lists in `apps/daemon/internal/cli/harness_catalog_linux.go` from each catalog entry's `configuration` package. The declaration contains the kind with the capabilities of the shared model `Configuration`'s declaration, that `Configuration` and a `Discover` function. Discovery receives the diagnostic writers, owns native configuration and availability checks, and returns the installed `agent.Runtime` with its descriptor and view declaration. Return nil when the adapter is not configured; return an unavailable descriptor without a view when configured prerequisites fail. Keep version gates and view-selection conditions inside the adapter; they only clear support.

[`cli/agent_host_linux.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_host_linux.go) registers each discovered Runtime that has a view with `RegisterKind` and `RegisterView`. The agent host then registers each such kind for dispatch through `Registry.Register`, which composes the declaration with the Environments it serves, and installs its own Executor factory with `RegisterExecutor`; that factory builds the Session's view and calls the view's `ViewExecutorFactory`.

| Order | Method | Registers |
| --- | --- | --- |
| 1 | `RegisterKind(proto.SupportedAgentKind, harnessconfig.Configuration)` | Kind, availability, version, `AgentKindCapabilities` and the model configuration, whose declaration it narrows to those capabilities; it panics on a widening. It resets the other registrations, so call it first. |
| 2 | `RegisterView(kind, agent.View)` | The view declaration from `Runtime.View`. It panics with `ErrInvalidView` when `View.Validate` fails. Its Executor factory receives the request that the agent host's Executor factory prepared and enforces the [gateway rule](#endpoints-and-proxy). |
| 3 | `RegisterExecutor(kind, agent.ExecutorFactory)` | The agent host's Executor factory, which dispatch calls. It runs only for a request whose selection the narrowed declaration admits and whose model configuration prepares, and receives it with `Prepared` set. |

`Runtime.View` declares how the Harness runs in an agent-host Session view, described in [Run in an agent-host view](#run-in-an-agent-host-view). Every adapter sets it explicitly; `View: nil` means the agent host rejects the kind, and `Registry.ResolveView` returns an error wrapping `ErrUnsupportedOperation`. `TestPublicHarnessContractDeclarations` requires the field in each declaration.

Every `proto.AgentKindCapabilities` field must be explicitly `proto.CapabilitySupported` or `proto.CapabilityUnsupported`, even for an unavailable Harness. `proto.CapabilityUnspecified` is invalid: zero values and omitted fields never mean Unsupported. An installation probe may clear an individual field with `proto.CapabilityFromBool`; it never sets support the static declaration lacks. Availability stays separate in `SupportedAgentKind.Available`. Registration validates the complete declaration before changing the registry, and the wire carries an explicit boolean for every field, so omitted and null fields are invalid. A new field requires a decision in every production declaration. Runtime consumers use `IsSupported()` and reject unsupported requests before native operations; an interface assertion verifies implementation, never support. Every declaration must match the behavior verified for that installation; the [Core–Runtime protocol](../../docs/runtime-protocol.md#capability-declarations) owns how declarations travel and are frozen.

Every available Harness implements, without a declaration, the shared Turn lifecycle, including durable `SteerWithReceipt` input and the Turn settlement contract that `contracttest.TextLifecycle` checks, typed `execution_controls` and tool observations. A Harness that cannot meet them on a platform reports `Available` false there. `FunctionTools` admits `SubmitFunctionResult`. `LocalEnvironment` admits `execution_prepare` with `workspace_read_only`, which the Environment owner readies and serves without calling the Executor factory.

The runnable test-only example [`testdata/onboarding/main.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/testdata/onboarding/main.go) registers a text-only synthetic Harness under the `mcode` kind, because Core admits only [catalog](./harness-catalog.md) Harnesses. It shows a Session-owned Executor, fresh Turns, durable steering, cancellation and history binding, and is never shipped.

## Declare support

Core recognizes the [built-in Harness registrations](./harness-catalog.md). Add one entry to `internal/harnessconfig/builtin/catalog.json` with the public `kind`, the display `label`, the pinned upstream `version` and the model `configuration` package under `internal/harnessconfig`, then run `make generate-harness-catalog`. It generates the model configuration registry, agent-host declaration and installation lists, version pin projections, client identifiers and display names and the registration reference; public input validators read the generated registry. `make openapi` derives Harness enums from the same catalog, so do not add handwritten enums to DTO tags or route annotations. `make check-harness-catalog` rejects stale projections.

The `Declaration` of `Configuration()` in `internal/harnessconfig/<kind>` is the Harness's support: a `proto.Declaration` with its `AgentKindCapabilities`, its message, image, MCP and output-schema limits, and in `Conflicts` the feature pairs it supports alone but not together. It states the adapter's maximum support and is the only source: Core reads it through `builtin.Registry()`, and the adapter's Runtime descriptor starts from it. Discovery and the Environment owner only clear support, and Core rejects a heartbeat that widens it. Declare only real differences between Harnesses; a rule that holds for every Harness is a common check in `proto.ValidateSelection`.

`proto.ValidateSelection` is the only check of a declaration. Core applies the static declaration when an Agent with a saved Harness is created or updated, at Session creation and at input and function-result admission, and the Runtime's narrowed declaration at device selection and before it claims a Turn. The Runtime applies it when it admits an `execution_prepare`, and again with the Environment's installed MCP servers once the Environment owner has resolved them, before any Executor factory runs. A rejection is 400 `unsupported_or_invalid_configuration` with the configuration path as `param`. Runtime facts, such as a missing binary, native history or filesystem readiness, stay adapter preparation failures.

Each Runtime declaration references the same `internal/harnessconfig/<kind>.Configuration()` and owns its native factories and probes. The catalog cannot declare a machine's availability, and there is no dynamic plugin loader.

The shared selection fixtures in `internal/harnessconfig/builtin` hold one accepted and one rejected case for every declared rule. Run them, and the public onboarding tests in `services/core/tests/integration` for admission and Runtime dispatch.

## Native model configuration

[`internal/harnessconfig/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/harnessconfig/harness.go) owns the shared configuration declaration and pure preparation contract. Each adapter supplies one `Configuration`, in `internal/harnessconfig/<kind>`, to Core's composition and to the Runtime's `RegisterKind`. The Executor and view paths both validate through that declaration before native side effects, and Registry wrappers keep the declaration with the factory. The wire object is `proto.HarnessConfig`. [Model execution](./model-execution.md#native-model-parameters) lists each Harness's accepted fields.

Every request names a nonempty `model` and a `model_provider`; preparation rejects a request without either, including an explicit null or empty value, before any native side effect. An empty declaration therefore accepts no request. Unknown protocol formats and duplicate protocol declarations fail at registration.

The declaration's ordered `protocols` list is the only source of accepted protocols and the default (the first entry); it also feeds Core's configuration-support descriptor, and Core and Runtime reject unsupported combinations through it. Adapters connect through native configuration and the [credential gateway](./model-execution.md#credential-gateway); they never introduce their own model API proxy or protocol converter, a second model capability registry, or capabilities inferred from model names. Claude's private bridge receives compiled native options and performs structural checks only, not a second copy of the declaration's rules.

## Qualify the adapter

Before starting, record the operation set, expected results, exclusions and stopping conditions. A qualification ends when its declared operations pass; it does not expand to match another Harness's feature list.

1. **Contract tests.** Call `agent/contracttest.TextLifecycle` from a test named `TestSharedTextLifecycle` with the adapter's prepared Executor and a deterministic native fixture; `claudesdk/executor_test.go` is the reference. It checks independent Turn streams, native owner and history continuity, durable write and application receipts, stale cancellation and healthy continuation after cancellation. `make check-runtime-contract` runs it together with the shared wire, gateway, transport and dispatcher tests, the declaration completeness check and MiniMax Code's `TestUnsupportedExtensionsHaveNoNativeEffects`. Adapter tests also cover two ordinary Turns sharing one native process or connection and history, cancellation followed by another Turn, stale cancellation and late events, native exit, cleanup failure, input write and application receipts, unknown outcomes and fresh per-Turn usage, function, input and child-observation state. State whether a fixture is controlled or a real provider.
2. **Shared integration.** `TestThirdHarnessPublicOnboarding` runs the synthetic Harness through public Session and input admission, Worker device selection, the real WebSocket gateway, the daemon Registry and Router, neutral events and durable terminal projection. It registers under the `mcode` kind, so Core admits it against MiniMax Code's declaration, and checks applied input receipts, saved native identity, continuation, cancellation, unsupported optional requests and missing mandatory Runtime support. The fixture has no workspace, MCP or public functions, and its registration stays local to the test. It proves the integration path, not native execution.
3. **Real acceptance.** Use the pinned official Python SDK and raw HTTP against Core, a real provider API, the native Harness and a dedicated database. Verify initial execution, a warm follow-up, cancellation and restart with continuation; record native owner identity and same-condition cold and warm timing. For workspace placements also verify Files and Artifacts, workspace identity, that no credentials appear in public responses and that foreign history is rejected. `services/core/tests/official_hosted_functions_native.py` holds the shared function assertions: success and error, native file output and public Artifact bytes, same-history continuation after restart, foreign result rejection and pending-call cancellation. Synthetic or failed runs never count. Use the agent-host tests in [Qualify the view](#qualify-the-view) for native workspace and capability acceptance. Real-model public API acceptance must also cover model provider protocols, MiniMax Code text, message images, function results with images, structured output, deferred function discovery, disabled web search and programmatic tool calling; passing view tests alone does not qualify those public API behaviors.
4. **Regression.** Existing Harnesses keep working. Run targeted tests, then `make check`; run `make openapi` after API changes and `make sqlc-generate` after query changes.
5. **Review.** Follow the [blind review workflow](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#review).

Environment acceptance uses `services/core/tests/official_environment_{templates,setup,skills,plugins,plugin_mcp,composition,initial_files,network,skill_references}.py`. Run composed preparation through the [public qualifier](#qualify-the-public-path)'s `composition` suite. `official_hosted_structured_native.py` covers hosted structured output. Record exact source revisions, native versions and commands with each acceptance result.

Keep provider keys in private operator files, never in commits or logs. Existing focused tests, relative to `apps/daemon/internal/agent`:

| Boundary | Tests |
| --- | --- |
| Codex reuse, cancellation and unconfirmed cleanup | `codex/executor_test.go`, `terminal_cleanup_test.go` |
| Codex input receipts and strict recovery | `codex/function_write_receipt_test.go`, `function_receipt_test.go`, `resume_test.go`, `recovery_test.go` |
| Claude input ownership, cancellation and preparation cleanup | `claudesdk/executor_test.go`, `cancellation_test.go`, `preparation_test.go` |
| MiniMax cancellation retirement, failed Start and cleanup retry | `mcode/executor_test.go`, `executor_backpressure_test.go` |
| MiniMax native history binding | `mcode/session_test.go` |
| Explicit refusals without native effects or fabricated results | `mcode/unsupported_test.go` |

## Qualify the public path

`services/core/tests/qualify_public_native.py` runs the pinned official SDK and raw HTTP assertions against an already installed, isolated Core deployment with its real agent host. It creates and deletes its own Sessions and uses the selected real model; it does not provision a deployment or replace the executor. Set `OPENAI_BASE_URL` to the deployment's `/v1` endpoint and `OPENAI_API_KEY` to its Project key. Supply a second Project's key in a private file. [Installation](../../docs/getting-started/install.md) owns deployment setup; [Projects and keys](./admin-api.md#projects-and-keys) owns credential issuance.

The private settings JSON has exactly `agent`, `model_provider` and `environment`. `agent` contains `model` and an explicit `x_agents_core.harness`, with optional `harness_config` inside that extension. `model_provider` is the complete [provider bundle](./model-execution.md#session-override); `environment` is the public Session Environment input. Keep settings and foreign-key files absolute and mode 0600, outside the repository. Evidence must be a new absolute path under `~/.oac`; it contains public responses and check names, never the settings. The runner refuses to write evidence containing any of its three supplied credentials.

```bash
python services/core/tests/qualify_public_native.py \
  --settings "$HOME/.oac/qualification/codex-none.json" \
  --foreign-key-file "$HOME/.oac/qualification/foreign-project.key" \
  --suite none \
  --evidence "$HOME/.oac/qualification/codex-none-result.json"
```

| Suite | Operations | Placement |
| --- | --- | --- |
| `none` | Initial-input creation/retry/conflict with one Turn, foreign history rejection, two native text Turns, history recall, SSE ordering, SDK/raw schema parity and nullable usage accounting | `none` |
| `pending-actions` | Query/reconnect pending calls, success/error results, cancellation, exact target rejection, retries and durable Items | Any declared placement with function tools |
| `functions` | SDK handlers, success/error, native file and Artifact bytes, continuation, pending cancellation and tenant isolation | Workspace |
| `images` | Initial and active images, image results, retry/atomic rejection, native files/Artifacts, isolation and continuation | Workspace with declared image and function support |
| `structured` | Saved and inline schema, function-assisted native files, exact JSON/SSE, cancellation and text override | Workspace with declared structured output and function support |
| `composition` | Frozen source snapshots, initial bytes, ordered setup, packages, Skills, stdio MCP, continuation and cancellation | `openai_hosted` |

For `composition`, settings must supply exactly `{"type":"openai_hosted"}` as `environment`; the suite creates its own Template, file and Skill sources. It checks initial binary bytes, ordered setup, npm and Python packages, uploaded, plugin and directory Skills, and three real stdio MCP tool identities, Items and results. After changing or deleting source resources, it verifies frozen preparation through warm continuation and, when selected, the existing Compose-verified agent-host restart. Cancellation must stop the MCP descendant's file effects. The suite cleans up all resources it creates.

Private-owner and credential isolation remain `unverified`; positive canary evidence requires separate proof using operator-owned resources. The suite does not request `packages.system` or stdio MCP `env_vars`, which the current contracts reject.

For `self_hosted`, choose a custom absolute `workspace_directory`. When the runner prints each new Session ID, connect a separate isolated machine or container using that Session's public installation command; the runner waits up to five minutes. Multiple Sessions must not share a workspace. The separate `official_environment_files_native.py` check accepts two already connected self-hosted Sessions and checks Files.list sorting, pagination and isolation through the Environment owner.

Warm continuation is the default and records cold recovery as `unverified`. To qualify cold continuation, additionally pass `--compose-directory` with the owned installation's absolute directory and `--compose-project` with its exact project name. The runner restarts only that project's `agent-host`, confirms its container start time changed, and then runs the unchanged history assertions. This does not qualify a Core restart or a sandbox checkpoint restore. The `pending-actions` suite reconnects the public client, not the agent-host process, and rejects those restart options. Select cold recovery only where the [declaration and coverage ledger](./index.md#known-gaps) support it; unsupported recovery remains a gap, never a successful skipped check. An API rejection fails the selected suite.

The `none` suite validates required and nullable Session, Turn, Item and event fields against the pinned schema. When native Turns supply measured usage, it checks the terminal event against the stored Turn and sums the measurements into Session totals. Unknown usage remains `null` and each affected Turn is listed in the evidence’s `proof.unverified`; a passing suite does not claim native measurement support for those Turns.

Run `python -m unittest discover -s services/core/tests -p qualify_public_native_test.py` with the pinned SDK to check credential handling and the owned restart boundary without a model. Existing deterministic Core integration tests remain the authority for schema validation, atomic admission, durable receipts and rejection semantics. Real-model results qualify only the selected suite, protocol, Harness and placement. Provider lifecycle, native identity, credential isolation, deferred discovery and unselected suites need separate evidence; view-only results do not qualify the public path.

## Native version pins

`internal/harnessconfig/builtin/catalog.json` owns each built-in Harness's upstream version. Build scripts read that catalog; adapter constants and package version fields are generated projections. Update the catalog and run `make generate-harness-catalog` before building, then use `make check-harness-catalog` to verify freshness. A version change requires [native qualification](#qualify-the-adapter).

For Claude, `version` pins the official Agent SDK dependency in `packages/claude-sdk-adapter/package.json`; its pnpm lock must resolve that exact version and installs stay frozen. Update the lock through pnpm when changing the pin. Native Claude Code's version comes from the installed SDK's `claudeCodeVersion` metadata and is checked against its runtime report. Do not author a separate native Claude Code pin.

For MiniMax, `packages/mcode-harness/source.json` owns the upstream repository and source revision; its `version` is projected from the catalog. The companion artifact carries `source.json`, so its source validation and provenance work independently of the checkout.

## Native Harness packaging

An adapter supplies `agent.Installation` from `installation.go` in its own package: its registered `AgentKind` and activation `Environment`. The generated registration passes these declarations to `agent.ManifestEnvironment`, which activates the packaged Harness from the image manifest. Adapters own native layout; validate the packaged content and execution on the Linux agent host. Missing or incompatible native content fails; it never installs itself during a Turn. Self-hosted installers carry no Harness or Node.js.

`deploy/distribution/AgentHost.Dockerfile` installs each Harness in its own directory and lists it in the image's manifest, `/opt/oac/harnesses.json`. `agent.ManifestEnvironment` activates it through `Installation.Environment`. Add each new Harness to that image and manifest; use the shared [image build](../../docs/maintainers.md#runtime-images-and-helpers) and [view qualification](#qualify-the-view) workflow.

## Native process ownership

The daemon's `clirunner` starts every native child in its own process group on Linux; other platforms return its typed unsupported error. Explicit and parent-context cancellation share a TERM grace period (three seconds by default) and a bounded KILL escalation. An internal reaper also cleans remaining group members when the direct process exits, even if a descendant still holds stdout open; during cancellation, surviving descendants keep the remaining grace after the leader exits. The daemon's `stop` command waits up to ten seconds for confirmed shutdown, which covers that grace period and the pipe and owner cleanup after it.

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
| `Executor` | The `ViewExecutorFactory` that prepares the Session's Executor in its view |

`View.Validate` checks the declaration without touching the host:

- view and host paths are absolute and clean;
- closure names are single path components other than `bin`, `home` and `run`, which the agent host uses for the shims, the Session home and the process relay;
- shim paths, overlays and masks do not overlap each other or `/`, and stay out of the trees the view builds itself: `/.oac`, `/proc` and `/dev` (`ViewReserved`);
- each `LocalExec` entry lies in a closure directory or an `Exec` overlay;
- shim names and `ForwardEnv` names are unique, no shim is named `oac-process-shim`, which is the process relay's, or starts with `oac-mcp-`, which [stdio aliases](#stdio-mcp) use, a variable name contains no `=`, and `ForwardEnv` names no variable the view or the broker sets ([Environment](#environment));
- `Proxy` is one of the two values and `Executor` is non-nil.

`harness.go` defines the view layout once, and `sessionview` builds views from it. The agent host checks its own overlays, such as `/etc/passwd`, against the declaration when it builds the view. A workspace must remain entirely in the sandbox world: binding rejects overlap with the common reserved trees or agent-host overlays before any Environment effect, and Harness admission rejects overlap with its declared overlays, masks or shim paths before any native effect. Neither operation substitutes a private home or another directory. At initial launch with a sandbox world, the launcher opens the working directory beneath that world without following symlinks or crossing mounts, then enters the opened directory before forking; changing the path cannot redirect startup into a view-owned mount. An empty-root launch and `Spawn` retain their own directory rules, including native-history helpers in the private home. This startup check does not restrict where the native process may later change directory.

### Capabilities

A view runs every request that the kind's declaration admits, so the adapter declares only what its view runs, and dispatch checks each request against that declaration. The agent host serves a local Environment and environment none, and every view runs the Environment's installed Skills and [stdio MCP](#stdio-mcp). The Environment owner fills `PrepareRequest.Skills` and `CapabilityRoot` as sandbox paths, and the adapter hands them to its Harness; only the Harness reads them, through the view, and the adapter opens none of them on the agent host. The agent host rejects a stdio binding that needs a credential with `ErrViewHandoff`.

### Environment none

A request with `DisableExecutionEnvironment` runs in an empty-root view: a read-only, noexec tmpfs at `/` that holds only the mountpoints for the closure, the Session home, the agent host's runtime files, `/proc`, `/dev` and the overlays. It has no sandbox files, no shims, no Link attachment and no sandbox network, so the generic proxy refuses every request; the cgroup, the isolation and the gateway stay. The request carries no `LocalEnvironment`, and the Harness runs in `/.oac/home/work` (`ViewWorkName`). The request already expresses the profile, so the wire has no field for it. A request with neither `LocalEnvironment` nor `DisableExecutionEnvironment` is an incomplete binding, and the agent host rejects it.

### Executables

Only mount flags grant execution. The closure, `Exec` overlays and the shim are read-only and are the only executable mounts; the sandbox's files and the home are noexec. `Launch` and `Spawn` accept only a `LocalExec` path as `Binary` and otherwise return `ErrNotLocalExec`. A dynamic binary, such as `node`, needs its ELF interpreter as an `Exec` overlay at its `PT_INTERP` path, and every library it loads in the closure, reached through `LD_LIBRARY_PATH`. Nothing loads from the sandbox's files. `viewloader.For` builds this from the binaries' ELF headers: the interpreter's host directory as the `lib` closure mount, the interpreter overlay, empty masks over `/etc/ld.so.preload` and `/etc/ld.so.cache`, and the `LD_LIBRARY_PATH` value. A layout it cannot present, such as a library outside the interpreter's directory, returns `ErrUnsupportedOperation`.

### Shims

The agent host derives the process broker's table from the declaration: `/.oac/bin/<name>` runs `<name>` in the sandbox, and each `ShimPaths` entry runs the same path there. A Harness that runs tools by name finds them through a `PATH` that lists `/.oac/bin`. The view's `/etc/passwd` gives the Session user the login shell `/bin/bash` and the home `/.oac/home`.

### Environment

`Launch` takes the complete Harness environment in `StartOptions.Env`, which the adapter derives from its installation and the request's typed fields; the request carries no environment values. The agent host's own environment never passes through, so a view adapter does not start from `os.Environ()`. A process run in the sandbox gets the broker's environment: the `ForwardEnv` variables from the Harness, the Environment's fixed sandbox values (`HOME`, `PATH` and `LANG`) and the Environment's tool environment. The broker is the only home of the tool environment, and a view adapter passes none of it to the Harness. The agent host keeps model and MCP credentials only in the gateway's protected configuration and adds none to the Harness's environment, a Process spec or a capability tree the view exposes.

`ForwardEnv` never names a variable the view or the broker sets: `HOME`, `PATH`, `TMPDIR`, `LANG`, `LD_LIBRARY_PATH`, or `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` and `NO_PROXY` in any case. When the Environment's tool environment also sets a forwarded variable, the tool environment's value wins.

### Endpoints and proxy

Before it calls the factory, the agent host points the request's model provider, `model_provider` and `Prepared.Provider`, at the Session's [credential gateway](./model-execution.md#credential-gateway): `base_url` is `http://127.0.0.1:<port>` with no path and `api_key` is `modelprovider.Placeholder`. It resolves the Session's MCP once, from the public declarations and the installed Environment MCP, into `ViewSession.MCP`, and removes both from the request. Only HTTP bindings go to the gateway: each points at its gateway URL and carries no bearer and no headers, and the gateway adds the declared credential and headers. A stdio binding runs under its [alias](#stdio-mcp). A view Executor takes MCP only from `ViewSession.MCP` and never resolves the request. The adapter renders the provider and the bindings into the Harness's native configuration and never sees a real credential.

The Registry checks each view request once, before the factory, and rejects it with `ErrViewHandoff` when its prepared model provider is not the gateway with the placeholder, when it carries MCP outside `ViewSession.MCP`, when an HTTP binding is not a credential-free loopback endpoint, or when a stdio binding is not its alias.

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
| Declaration | Each declared feature runs a Turn through dispatch: environment none in the empty-root view, function calls and results, tool search, an installed Skill, and each stdio binding under its alias. |

`scripts/qualify-agent-host.sh` runs each Harness's Turns through the daemon's dispatch against the [agent-host and sandbox images](../../docs/maintainers.md#runtime-images-and-helpers). The `agenthostqualify` test binary runs as the agent host with the [agent-host container's flags](../../docs/configuration.md#agent-host-container), and the sandbox image serves the sandbox. Each Session's Environment is prepared through `runtime_prepare` as Core prepares it: a configure step freezes the tool environment, and a setup step writes a file with one of its values, which the test checks; every later Executor of the Session reopens that preparation. The first Turn writes a file and reports the output and exit status of a failing command whose values only the sandbox's tool environment holds. When the kind declares function tools, a second Turn runs in a new Executor that resumes the Session's native history and calls a function; the test returns a text, image and text result through dispatch, and the answer must report both texts. When the kind declares tool search, a Turn in a new Executor without native history finds the deferred function with tool search and calls it. When the kind declares environment none, a Turn in a Session without an Environment answers through the model, and the Harness's native state in the Session home must name its working directory, `/.oac/home/work`. In another Session, `runtime_prepare` installs a plugin with one Skill and one stdio MCP server, a script in the plugin that runs in the sandbox. One Turn uses the Skill and must report the word that only its `SKILL.md` holds, and a Turn in a new Executor calls the server's one tool and must report the code it returns. The Link runs over WSS with a CA the test generates. The test also checks the cgroup v2 delegation: the container's own read-only cgroup fails with `ErrUnsupported`, and in a delegated directory the agent host ends a cgroup left behind with `cgroup.kill`. Set `OAC_AGENT_HOST_IMAGE` and `OAC_SANDBOX_IMAGE` to the two images, `OAC_QUALIFY_KEY_FILE` to the model key's file and, for each Harness to qualify, `OAC_QUALIFY_CLAUDE_SDK`, `OAC_QUALIFY_CODEX` or `OAC_QUALIFY_MCODE` to its `model` and `model_provider` without `api_key`. The gateway dials model providers directly, so on a host whose only egress is an HTTP proxy, set `OAC_QUALIFY_PROXY` to it and the test tunnels the providers' hosts through it.

## Native references

| Harness | Adapter | Native transport |
| --- | --- | --- |
| Codex | [`agent/codex`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/codex/executor.go) | app-server |
| Claude Code | [`agent/claudesdk`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/claudesdk/executor.go) | [TypeScript SDK bridge](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/packages/claude-sdk-adapter/README.md) |
| MiniMax Code | [`agent/mcode`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/daemon/internal/agent/mcode) | ACP and native workspace companion |
