# Core–Runtime protocol

This protocol connects Core to a Runtime daemon after the daemon has its machine credential. It defines the meaning and order of the messages on the daemon connection. The wire types, limits and validators live once in [`internal/agentdaemon/proto`](../internal/agentdaemon/proto); Core's [gateway](../internal/agentdaemon/gateway) and the reference Runtime's [dispatcher](../apps/daemon/internal/dispatch) both use them, so there is no second payload schema to keep in sync. The HTTP routes that issue credentials and open the connection are in the [machine connection API](../contracts/agents-api/machine-api.md).

Hosted and self-hosted Runtimes use the same protocol. A Harness joins through the [Harness adapter contract](../contracts/agents-api/harness-onboarding.md), which owns the Executor and Turn lifecycle obligations behind the Runtime registry.

## Ownership and connection

Core owns durable Session, Turn, input and Environment records, scheduling and reconciliation. Runtime owns native Executors, active Turns, transfer state and cleanup until settlement. A Sandbox Provider owns placement and the surrounding compute. Releasing an execution admission or closing an Executor never deletes, suspends or reclaims a sandbox. The daemon is not an isolation boundary; see [Runtime and outer isolation](design-principles.md#runtime-and-outer-isolation).

A Runtime connects in this order:

1. Obtain a daemon credential and device ID. The [machine connection API](../contracts/agents-api/machine-api.md#credentials) lists the credential kinds; Project API keys and the Core key are never Runtime credentials.
2. Call `POST /api/v1/agent-daemon/bootstrap` with the credential as a Bearer header and the device ID. Use the connection URL it returns.
3. Dial the WebSocket at `/api/v1/agent-daemon/ws` with `device_id` and `version` query parameters and the Bearer header. Never put a credential in a URL, a payload log or a trace.
4. Send a heartbeat at once, then at the interval bootstrap returned. Each heartbeat declares `supported_agent_kinds`, their availability and their [capabilities](#capability-declarations). Before the first heartbeat, capabilities are unknown; a kind missing from a heartbeat is not advertised. Neither permits inference.
5. Exchange ordered JSON [envelopes](#envelope-and-identity). Heartbeats establish liveness only, never execution progress or a receipt for an earlier message.

The wire version is [`proto.Version`](../internal/agentdaemon/proto/version.go), independent of the Runtime build version that heartbeats report. Core accepts only an exact match, including the patch component. A mismatch returns HTTP 426 `incompatible_version` before any dispatch; the daemon treats it as permanent and stops reconnecting. Deploy matching peers together.

Each physical connection has fresh routing, admission handles and transfer state. A newer connection for the same device replaces the previous one: Core fences the owner lease and evicts the old Run and interaction routes, and the new connection inherits none of them. A valid credential and connection are never authority to choose another Session or Environment binding.

## Capability declarations

`AgentKindCapabilities` in [`inbound.go`](../internal/agentdaemon/proto/inbound.go) describes one composed Runtime and Harness, independently of `available` and of Core's engine profile. Every field is a `CapabilitySupport`: supported or unsupported. The zero value is unspecified and invalid, even for an unavailable Harness. Registration validates the complete declaration before changing the registry; there is no implicit basic descriptor.

On the wire each field is a JSON boolean, and every field is present, including `false`. Encoding an incomplete declaration fails. Decoding rejects omitted, null, invalid and unknown fields, and a missing capability object. An invalid heartbeat clears the connection's admission snapshot and closes its transport; that establishes no native completion or cancellation result.

Each admitted Executor and Turn keeps the declaration it was admitted with. A later heartbeat cannot add operations to an existing owner. Optional operations check this snapshot before any native call; the presence of a Go interface never grants support. A declared operation that returns `agent.ErrUnsupportedOperation` is a contract violation, distinct from unavailability, a failed native call or an uncertain write. Uncertain operations keep their receipts and ownership and are never replayed automatically. Workspace support includes the common Runtime workspace implementation, so a native adapter's unsupported workspace method does not disable that composition.

A new field requires an explicit decision in every production declaration. Contract tests enumerate every field for registration, wire round trips and the persisted boolean projection; the shared test fixture lists fields individually and supplies no defaults for future ones. [Harness onboarding](../contracts/agents-api/harness-onboarding.md) owns the adapter side of each declaration.

A declaration describes what the Runtime can do. Core admits a public feature only when the Harness's engine profile also qualifies it, and checks the declaration of the selected device during device selection and again at the final check before it claims a Turn:

| Capability | Core requires it when |
| --- | --- |
| `streaming`, `steering`, `durable_turns`, `durable_input_receipts`, `preparation`, `execution_controls`, `tool_observations` | Always, for every execution on that Harness (with `available` true) |
| `environment_none` | The Environment type is `none` |
| `local_environment`, `workspace_read_preparation`, `workspace_output_export` | The Environment type is `openai_hosted` or `self_hosted` |
| `workspace_read_preparation` | An idle Files directory read needs a read-only preparation |
| `native_session_recovery` | A Session with a started Turn has no recorded native Session ID |
| `web_search_control`, `text_verbosity` | The Harness's engine profile declares that control |
| `structured_output` and `message_items` | The Agent requests `json_schema` output |
| `subagent_observations` | `multi_agent.enabled` is true |
| `subagent_control` | `multi_agent.enabled` is false |
| `tool_search` | The Agent enables tool search or defers function loading |
| `programmatic_tool_calling_disable` | The Agent explicitly disables programmatic tool calling |
| `function_tools` | The Agent declares function tools |
| `message_images`, `function_result_images` | A message, or a function result, carries an image |
| `mcp_http_tools`, `mcp_http_required`, `mcp_http_bearer_auth` | The Agent declares HTTP MCP servers; one is `required`; a Vault credential is selected for one |

`permissions` gates permission decisions inside the Runtime, and `workspace_authoring` gates the daemon's authoring command. Core has no admission rule for `usage` and `resume`.

The prompt request (`prompt_request`, or the configuration of `execution_prepare`) carries the opt-ins Core sets for each Run:

| Field | Set by Core |
| --- | --- |
| `execution_controls` | Always: web search `disabled`, the resolved text verbosity (default `medium`), an explicit programmatic-tool-calling disable and any `json_schema` output format. Native option names belong to the adapter |
| `observe_tool_observations` | Always. Tool-call frames then carry the engine-neutral `observation` |
| `observe_messages` | When the Runtime declares `message_items`. Text deltas then carry the native item ID, and `output_message` frames report message start, completion, phase and the completion text |
| `observe_subagent_identities`, `disable_subagents` | From the Agent's `multi_agent.enabled` |
| `disable_execution_environment` | For an Environment of type `none` |
| `local_environment` | For `openai_hosted` and `self_hosted`, with the exact Environment binding |
| `strict_resume`, `require_existing_native_session` | Always strict; the second when a native Session must be recovered |
| `durable_receipt` on `prompt_steer` | For every active input Core delivers |

Requests without an opt-in keep the frames and fields they had without it.

## Envelope and identity

Every data frame is one JSON [`Envelope`](../internal/agentdaemon/proto/envelope.go): `type`, a type-dependent `id`, a typed `payload` and an optional W3C `trace`. The trace is diagnostic correlation only; missing or invalid trace data creates a local trace and never changes ownership. Never use a trace ID as a request ID.

| Identity | Scope and meaning |
| --- | --- |
| Device ID and connection | Authenticated Runtime routing and connection ownership |
| Session ID / Environment ID | Core-owned configuration and workspace binding; canonical UUIDs where the payload validator requires them |
| Executor ID | Runtime-owned native resource, possibly retained across settled Turns with identical configuration |
| Preparation request ID | `Envelope.id` for prepare, start, release and status; distinct from a Run |
| Admission handle | Runtime-generated reservation, valid only on the connection that accepted it |
| Run ID | One execution attempt; `Envelope.id` for output, cancellation, active input and functions |
| Interaction ID | `permission_request.payload.request_id` or `prompt_for_user_choice.payload.ask_id`; these request envelopes still carry the Run ID |
| Delivery ID / input ID / call ID | Resolve attempt, active-input receipt and native function identity; never interchangeable |
| Transfer ID / suspension ID | Connection-local transfer correlation / persisted suspension-attempt fencing |

Decision and permission-cancel envelopes use the interaction ID. Cancellation and function-result acknowledgements use the Run ID. Every application decision receipt also matches the delivery ID. A reply without the required correlation cannot establish acceptance.

User-choice decisions carry `question_answers`: an explicit `question_id` and an `answers` array for each provided answer. The IDs must belong to the emitted questions and cannot repeat. Question order and display headers do not identify answers; an omitted question stays unanswered, and an empty array is an explicit non-answer. Cancellation carries `cancelled: true` without answers. Shared validation rejects other shapes before native submission.

## Message families

The linked source files define the required fields, validators, limits and finite error categories.

| Core → Runtime | Runtime → Core | Definition |
| --- | --- | --- |
| `runtime_prepare` | `runtime_prepare_result` | [Initialization and capability transfer](../internal/agentdaemon/proto/runtime_prepare.go) |
| `execution_prepare`, `execution_start`, `execution_release` | `preparation_status` | [Execution admission](../internal/agentdaemon/proto/preparation.go) |
| `prompt_request`, `prompt_cancel`, `device_shutdown` | `delta`, `thinking`, `output_message`, `tool_call`, `usage`, `error`, `done`, `heartbeat` | [Requests](../internal/agentdaemon/proto/outbound.go), [events and capabilities](../internal/agentdaemon/proto/inbound.go) |
| `permission_decision`, `prompt_for_user_choice_decision` | `permission_request`, `permission_cancel`, `prompt_for_user_choice`, `interaction_decision_ack` | [Requests](../internal/agentdaemon/proto/outbound.go), [interactions](../internal/agentdaemon/proto/inbound.go) |
| `prompt_steer` | `prompt_steer_ack` | [Active input receipts](../internal/agentdaemon/proto/steering.go) |
| `function_result` | `function_call`, `interaction_decision_ack` | [Function calls](../internal/agentdaemon/proto/functions.go) |
| `workspace_read`, `workspace_write`, `workspace_export` | Matching `*_result` | [Read](../internal/agentdaemon/proto/workspace_read.go), [write](../internal/agentdaemon/proto/workspace_write.go), [export](../internal/agentdaemon/proto/workspace_export.go) |
| `environment_quiesce`, `environment_resume` | `environment_quiesced`, `environment_resumed` | [Suspension fencing](../internal/agentdaemon/proto/suspend.go) |

Initial, prepared and active input use the same [ordered MessageInput](../internal/agentdaemon/proto/message_input.go). Adapters keep message and content order and reject unsupported content explicitly; a text-only transport rejects image content rather than dropping it. The [message input contract](../contracts/agents-api/message-content.md) owns the public image profile, whitespace rules and each Harness's native conversion.

Usage frames and the final usage snapshot each carry the cumulative measurement of the current execution and replace the previous snapshot; never add them. An absent measurement is unknown, not zero.

## Preparation and execution order

Environment initialization uses `runtime_prepare` on every connection, managed or user-owned; the [Environment contract](../contracts/agents-api/environments.md#runtime-capability-preparation) owns what is prepared and when. For a file or archive, send `begin`, wait for `ready`, send ordered chunks and await each matching `received` offset, then send `commit` and await `completed`. Initialization and finalization have typed headers without file data. Validate the expected outcome, offset, size and finite error code with the shared validator. One transfer is allowed per connection. A chunk receipt confirms staged bytes, not installation; a completed commit confirms that operation, not that a later Turn ran.

An execution Turn runs in five steps:

1. Subscribe to preparation status, then send `execution_prepare` with the immutable Session configuration and no Run input.
2. `preparing` means the Runtime owns preparation. `ready` supplies the Executor ID, admission handle, revision and expiry. Neither submits user input.
3. Subscribe to the Run, then send `execution_start` with that Executor ID, handle, Run ID and ordered input. A valid start transfers the reservation once. `started` confirms the transfer to the Turn, not its completion; output can race the status, so it must already have a subscriber.
4. Consume Run events until a native terminal outcome or loss of observation. An execution error is followed by `done`, which closes the stream; the preceding errors remain part of its outcome.
5. To abandon before start, send `execution_release`. After ownership passes to a Run, use `prompt_cancel`; releasing the old handle cannot cancel its successor.

A preparation reserves a per-Turn admission, not a new Executor. It carries an explicit Session identity and immutable configuration without model input or a Run ID. A fresh request returns a connection-local handle and the owning Executor ID; a reused healthy Executor returns `ready` without native preparation. Per-handle revisions order status observations: ignore older or repeated revisions and never apply a status to another handle. Typical transitions are `preparing → ready → starting → started`, or termination by `released`, `expired` or `failed`. A `rejected` control operation carries an `operation` and error code and does not replace the handle's current revision. Releasing an admission abandons only that admission; it does not close the Session's idle Executor or cancel a later Turn.

Preparation and start run outside the receive loop and router lock. An admission expires five minutes after it is granted, and retries do not extend that deadline; expiry does not remove the Runtime's obligation to settle cleanup. The Runtime bounds active preparation and execution separately from idle retained resources and counts closing or uncertain resources until their cleanup succeeds. A definite `execution_prepare` rejection with `preparation_capacity` leaves the queued Turn unclaimed for the Worker to retry, including when cleanup holds the capacity; any other error or uncertain delivery authorizes no replay. The Runtime retains at most 64 admission records, and an old handle never consumes a replacement's admission. These records are connection-local, not durable input replay.

Idle expiry of an Executor is a Runtime resource policy, separate from Core's active-Turn concurrency. On shutdown the Runtime closes active and idle Executors, keeps any target whose close failed and allows a later serialized retry. An ordinary disconnection closes the failed transport and keeps the exact router until shutdown succeeds; a wait timeout or failed cleanup never authorizes reconnection, and process shutdown keeps waiting rather than discarding owned native resources. Workspace operations keep their binding and settlement rules across Turn boundaries and Executor closure.

`prompt_request` starts a Run directly, without an admission handle. It is not a fallback after a failed prepared start.

## Active input receipts

Core delivers active input as `prompt_steer` with `durable_receipt: true`, one input at a time per Run, and waits for its receipt before sending the next:

| Phase | Timer |
| --- | --- |
| Native write | The Runtime bounds the adapter's native write at 10 seconds; the timer stops once the write is complete |
| `written` acknowledgement | Core waits at most 30 seconds from delivery for `written`; otherwise the input outcome is unknown |
| Receipt send | Each receipt send has its own 5-second, shutdown-aware budget |
| Native acceptance | `accepted` arrives under the Turn lifetime, with no automatic redelivery |
| Done | Before `done`, the Runtime waits at most 15 seconds (the write and send budgets) for an in-flight input |

Neither `written` nor a send failure advances Core's input cursor. Once cancellation is sent, its receipt owns the terminal outcome even if an input becomes unknown first; Core records `cancel_unconfirmed` when no cancellation confirmation arrives within 15 seconds. A cancellation receipt carries the stopped Turn's confirmed continuity snapshot when no `done` is emitted.

## What each acknowledgement proves

| Observation | Proven fact |
| --- | --- |
| Core gateway `Send` returns nil | The envelope entered the local send queue |
| Runtime transport `Send` returns nil | The WebSocket write completed locally |
| Transfer `received`, active-input `written` | The defined receive or write phase occurred; native execution or consumption is unconfirmed |
| Preparation `preparing` / `ready` | Preparation accepted / reservation ready, with no Run input submitted |
| Preparation `started` | Admission transferred to the identified Turn |
| Active-input `accepted` | The adapter confirmed native consumption under its declared receipt semantics |
| `interaction_decision_ack.applied=true` | The identified operation settled; a cancellation also requires native settlement |
| `done` and the preceding execution events | The execution stream completed with its observed outcome |

No generic receipt exists for every envelope. A successful send does not prove that the peer received, accepted or completed a request. Process exit, a stop signal or a canceled local context does not prove cancellation. A `done` frame may also close a settled cancellation stream; it does not override the cancellation receipt or imply success. A cancellation receipt may keep partial content, native identity and usage in `outcome` even when no `done` is published. A failure to obtain settlement stays failed or unknown; it never becomes `applied=true`.

## Failures, retries and cleanup

Transport and execution outcomes are separate. Core's only Run subscription entry point is `SubscribeDurable`; inspect `Subscription.Err()` when its event channel closes. Disconnection and subscriber overflow close it with an explicit observation error and fabricate no `error` or `done`. Core keeps the durable truth and reconciles from confirmed facts. The Runtime keeps cleanup ownership until native work, input receipts, interactions and child work have settled.

The public Turn status is a separate projection. [`execution/delivery.go`](../services/core/internal/execution/delivery.go) records an unsuccessful orchestration attempt as `failed`, including `delivery_unknown` after an unconfirmed send and `event_stream_incomplete` after a subscription failure; a closed subscription can replace the send reason with `event_stream_incomplete`. Both mean the native effect is unknown: a public `failed` status does not prove that the Harness failed, that no side effect occurred or that cleanup completed. Keep the observation reason and any native evidence distinct.

| Condition | Responsibility |
| --- | --- |
| Unsupported capability or invalid binding | Reject before starting the operation; never select another Harness |
| Confirmed preparation or execution failure | Keep the finite error category and any observed result; the Runtime settles its resources |
| Deadline or connection loss after dispatch | The effect is unknown unless an application receipt proves otherwise; do not convert it to an execution failure |
| Reconnection | Reestablish the transport and the capability declaration; never replay input, initialization, transfers or unresolved mutations |
| Duplicate preparation or start | Connection-local identity and fingerprint rules apply; a conflicting request rejects, and an old handle cannot start replacement work |
| Duplicate input, function result or decision | That family's receipt identity and conflict rules apply; there is no transport-wide deduplication or exactly-once promise |
| Cleanup failure | Keep resource ownership and report unconfirmed cleanup; a waiter's timeout does not make a resource reusable |
| Lost cancellation receipt | Cancellation may have happened; the missing receipt neither establishes success nor authorizes another execution |

Retry only an operation whose own contract makes retry safe. A retired preparation request ID may eventually allocate a new handle, so request IDs are not durable idempotency keys. Resuming a native Session is an explicit operation with verified identity, never a response to a socket failure. A transient connection error permits reconnecting; an authentication or version rejection requires operator correction. Executor cleanup is separate from the Sandbox Provider's confirmed reclamation of compute.

## Native failure classification

An adapter may add `code` and `http_status` to a Run's `error` frame. They are optional neutral metadata, not a terminal event or a public error contract; the frame's text, Usage, `done`, native Session identity and cancellation receipts keep their order and meaning.

The accepted codes are `authentication_error`, `rate_limit_exceeded`, `usage_limit_exceeded`, `server_overloaded`, `server_error`, `invalid_request`, `resource_not_found`, `request_timeout`, `context_length_exceeded`, `cyber_policy` and `connection_failed` ([`engine_failure.go`](../internal/agentdaemon/proto/engine_failure.go)). Only `connection_failed` keeps `http_status`, and only an integer from 100 to 599; every other status is discarded. A missing, malformed or unknown value leaves the error unclassified without discarding Usage or `done`.

Core stores accepted values in the Turn outcome as `engine_error_code` and `engine_http_status`. The classification is subordinate to the terminal status and Core's `error_code`: it cannot turn a completed or cancelled Turn into a failure, hide an incomplete event stream, or override a persistence or cancellation-receipt failure. Normal delivery and terminal journal draining use the same extraction. [Session diagnostics](../contracts/agents-api/session-diagnostics.md) expose the category only for a failed Turn whose Core error is `engine_failed`. The [Codex](../services/core/deploy/codex/README.md) and [Claude Code](../services/core/deploy/claude/README.md) adapter guides give each Harness's mapping; an adapter never classifies error prose.

## Workspace operations

A workspace read that needs no running Turn uses the read-only preparation profile: `execution_prepare` with `workspace_read_only`, which requires the `workspace_read_preparation` capability. It accepts only the bound Environment and resource identity; execution options, model and MCP credentials, native Session continuation and model or tool input are excluded, and the owner rejects `execution_start`. A Runtime may serve it from its bound local filesystem without starting a Harness process. The profile publishes `released` only after local close succeeds; a cleanup error keeps ownership and reports `cleanup_unconfirmed`. A failed factory returns its resource with the error while cleanup is unconfirmed, and wrappers keep both values. A successful cleanup retry publishes the confirmed release; a stale status snapshot never publishes success. A release request, HTTP disconnect or remote socket closure alone does not confirm cleanup.

`workspace_read` targets an existing preparation handle, or the Run it was transferred to, on the same authenticated device connection, with the exact frozen Environment identity; callers cannot supply sockets, credentials or workspace roots. `operation: directory` lists one workspace-relative directory (an empty path selects the root) with mutually exclusive byte and entry limits. A result carries at most 1024 single-component UTF-8 names of at most 255 bytes each, the entry kind, regular-file sizes and explicit truncation, and is returned only after directory access and handle cleanup settle. There is no snapshot, recursion or pagination at this layer. Byte and directory reads share target checks, correlation, capacity and retained operation waits.

Request payloads are bounded at 8 KiB and correlation IDs at 128 bytes before admission; oversized IDs are not echoed, and oversized trace metadata is omitted from replies. Raw read results are bounded at 1 MiB within the 4 MiB transport frame. These are private transport limits, not public Files parameters. A successful read requires complete bytes or explicit truncation and an acknowledged native close. A safe native rejection carries no bytes; an interrupted or ambiguous read stays unknown and stops further reads on that owner. A dispatched read keeps its bounded waiter across observer cancellation and resource transfer or release, and resource closure stops new admission. The gateway bounds subscriptions and never retries or replays a read on reconnect; a duplicate pending operation ID cannot start another read.

Core runs an idle directory read on the Worker's Session scheduling reservation and targets the exact Run during active execution. It keeps the reservation through the bounded read and release, returns data only after a confirmed close (an incomplete read or uncertain cleanup returns unavailable without data), releases the reservation before delivering the result, and revokes the scoped read credential on completion or failure. The Runtime keeps uncertain cleanup ownership and capacity. The [Environment Files contract](../contracts/agents-api/environment-files.md) owns public authorization, paths and pagination.

`workspace_write` transfers a complete bounded body in acknowledged 64 KiB frames before the native writer runs, verifies the declared digest and runs no model. The private transfer bound is 50 MiB, separate from the public 5 MiB decoded inline bound that the API checks before any Runtime work. The Runtime excludes execution while it receives or applies a write; a malformed, incomplete or expired transfer never reaches the installer. An exact commit or rejection receipt releases the mutation owner. A missing or ambiguous receipt keeps the uncertainty: observer cancellation and local process exit cannot prove that nothing changed. Before public admission Core durably reserves the write under the Session lock and blocks successor mutations across restarts until exact settlement; the request is never replayed. Every platform uses the daemon's Go implementation for bounded reads, directory listing, file creation and output export, with no external helper or staging directory.

## MCP connection authority

Every public `MCPHTTPServer` in a prompt request carries an explicit `connection_origin`; a missing or unknown value rejects rather than selecting a default, and Core freezes the public default before dispatch. The Runtime validates the origin with the common validator before selecting a factory and resolves public and installed MCP into transient effective bindings. The [Environment contract](../contracts/agents-api/environments.md#public-mcp-connection-origin) owns the supported combinations, native limits and failure ownership.

## Contract verification

Run `make check-runtime-contract` from the repository root. It exercises the shared wire validators, gateway, transport and dispatcher, the [real WebSocket contract scenarios](../apps/daemon/internal/contracttest/wire_test.go) with a controlled Harness adapter, the [observation-result regression](../services/core/internal/execution/runtime_protocol_test.go) and the Harness declaration tests. It needs no model credentials or external sandbox, and `make check` runs the same tests through `check-go` and `check-core`.

The suite covers incompatible versions, preparation failure, cancellation settlement, connection loss without invented terminal events, reconnection without replay, stale or duplicate handles and receipts, cleanup failures, bounded transfer validation and resource ownership after a timeout. Detailed fault injection stays next to the gateway and dispatcher code it tests.

Another Runtime reuses these protocol sequences and assertions, then runs native acceptance for every capability it declares. A controlled-adapter test establishes the transport contract, not native Harness behavior, operating-system support, provider authentication or sandbox isolation. Change the shared types, this document and the contract checks together. Harness adapters also run the shared text assertions described in [Harness onboarding](../contracts/agents-api/harness-onboarding.md).
