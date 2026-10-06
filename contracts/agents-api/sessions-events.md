---
title: "Sessions, events and history"
---

This contract covers what happens inside a Session: sending input, the live event stream, and reading the durable history of Turns, Items and usage. The Session resource itself (creation configuration, retry identity, update, list and deletion) is in [Core wire behavior](./wire-semantics.md). Message and function-result content is in [message content](./message-content.md). The [Agents API guide](../../docs/api/public-agent-api.md) shows the calls with the SDK and HTTP.

## Recovery model

The event stream is live only; Turns and Items are durable. A client that needs every result:

1. Opens `GET /v1/agents/sessions/{session_id}/events` before sending input.
2. After a disconnect, subscribes again and buffers new events.
3. Reads the Session, its Turns and its Items, deduplicates Items by ID and keeps finalized Items when it applies the buffered updates.

A stream is an observer. Closing it never cancels admitted work, and a stream never replays events it did not send. After a lost input response, retry with the same `Idempotency-Key`, then read the Session, Turns and Items.

## Session status

A Session's `status` and `last_active_at` derive from its latest root Turn and from input still waiting for its Environment:

| `status` | When |
| --- | --- |
| `idle` | No Turn yet, or the latest Turn completed or was cancelled. Input reserved for a hosted Environment that is still provisioning also reads `idle` |
| `in_progress` | The latest Turn is queued, running or waiting |
| `requires_action` | The latest Turn waits for a function result and no cancellation was requested, or input waits for a `self_hosted` machine to connect. `required_actions` lists `function_call` or `environment_connection` entries |
| `failed` | The latest Turn failed (`error` is "The execution could not complete."), input reserved for the Environment failed, initial input expired before admission, or the Environment failed to initialize (see [Environment initialization failure](#environment-initialization-failure)) |

A Session stays usable after a Turn fails: new input starts a new Turn. Later reserved input that expires leaves the Session idle. An Environment initialization failure or expiry prevents new work.

## Send input

`POST /v1/agents/sessions/{session_id}/events` takes an ordered array of 1 to 64 events: `agent.session.input.message`, `agent.session.input.cancel` and `agent.session.input.tool_result`. The whole batch is admitted atomically, and the response is 202 with no body once the batch is stored, before any harness reads it. Admission never confirms native application.

- **Limits.** The request body is at most 1 MiB, and the stored input of one request at most 512 KiB.
- **Null batch.** An explicit `null` for `events` is invalid.
- **Empty batch.** `{"events": []}` checks that the Session exists and returns 202. It creates no Turn, Item or retry identity.
- **Retries.** An `Idempotency-Key` of up to 128 bytes identifies the whole ordered batch. The same key and batch return 202 again without admitting anything twice; the same key with another batch returns 409 `idempotency_conflict`. A request without a key is always new.
- **Messages.** On an idle Session a message batch starts a queued Turn. While a Turn runs, messages join it (steering); they never start a parallel Turn. Each message stays its own user Item, even when the harness receives several as one prompt.
- **Cancellation.** A queued Turn is cancelled without a live Runtime. A running Turn is cancelled when the Runtime confirms it; completion can win that race. The Turn has stopped when it reads `cancelled`, not when the request returns. A cancellation on an idle Session with no pending input is accepted and has no effect; while an input reservation is pending, it returns 409.
- **Function results.** `turn_id`, `call_id` and `success` are required; `output` and `error` are optional and nullable ([content rules](./message-content.md#function-results)). An identical repeated result returns 202 without another application or event. The result Item appears when the harness applies the result; a result that cancellation prevents from being applied stays stored but produces no Item.
- **Queueing.** A queued Turn starts when a Runtime that supports the Session's harness and configuration is connected and one of Core's [`core.execution_concurrency`](../../docs/configuration.md#settings) work slots is free. A Session stays bound to the Runtime that first ran it.
- **Execution availability.** A service without execution returns 503 `execution_unavailable`, and a Worker that loses execution ownership returns 503. A Session created without a model provider rejects new messages with 400 `model_provider_required` ([model execution](./model-execution.md)).

### Sessions with an Environment

On `openai_hosted` and `self_hosted` Sessions, messages sent while a Turn runs join it at once. Messages sent to an idle Session reserve the batch for the Environment: the request waits until a Turn starts, for at most five minutes from the reservation. While the reservation waits for a `self_hosted` machine, the Session reads `requires_action` with an `environment_connection` action. A batch that carries messages on these placements may contain only messages. Cancellation-only and result-only batches are admitted at once and create no Turn.

The waiting request ends with 202 when the Turn starts, or with 409 `environment_input_expired` when the deadline passes, 409 `environment_input_cancelled` when an administrator archives the Session or resets its deployment and cancels the reservation, or 409 `environment_unavailable` when the Environment fails or expires. Disconnecting the waiting request does not cancel the reservation or restart its deadline.

### Input errors

Checks run in this order: request validation, Session lookup, retry lookup, the Environment file-write gate for batches with a message, the pending-input gate, then each event in batch order. A rejected batch writes nothing and leaves any pending action unchanged. Every 409 has type `conflict_error` ([error envelope](./wire-semantics.md)).

| Case | Status and code | Message |
| --- | --- | --- |
| Earlier input to the Session still waits for admission, such as the reserved initial input of a provisioning hosted Session or of an offline `self_hosted` Session | 409 `conflict_error` | "Earlier input to this Session is still pending." |
| Input the Turn cannot accept in its state, such as a result after cancellation or after its Turn ended without a stored result | 409 `conflict_error` | "The Turn cannot accept this input in its current state." |
| A result that differs from the call's stored result, before or after its Turn ends | 409 `conflict_error` | "The tool call already has a different result." |
| The same `Idempotency-Key` with a different batch | 409 `idempotency_conflict` | "This idempotency key was used with different input." |
| A result whose `call_id` names no function call of this Session | 400 `invalid_request_error`, param null | "Unknown pending tool call." |
| A result for a call of this Session whose `turn_id` names another Turn, an unknown Turn ID or a value that is not a Turn ID | 400 `invalid_request_error`, param null | "The tool call belongs to a different Turn." |
| A missing, malformed or foreign Session | 404 `not_found_error` | "Resource not found." |
| New input after an `openai_hosted` Environment failed to provision | 409 `conflict_error` | "the hosted environment failed to provision" |
| New input after a `self_hosted` Environment failed, input already waiting when the Environment failed, or an expired Environment | 409 `environment_unavailable` | "The environment is no longer available for new input." |
| A message the Session's harness cannot take, such as whitespace-only text on Claude Code | 400 `unsupported_or_invalid_configuration` | See [whitespace-only text](./message-content.md#whitespace-only-text) |

An empty `turn_id` or blank `call_id` is the generic 400 `invalid_request`. Error messages never repeat caller input or internal identifiers.

## Initial input at Session creation

`POST /v1/agents/sessions` accepts `input` as a string (one text message) or an array of user messages, with the same validation and admission as the events endpoint.

- Initial input is required on `none` (400 `invalid_request_error`, "conversation-only sessions currently require initial input") and for `stream: true` on every placement except `self_hosted` (400, "streaming session creation requires initial input"). These checks run before the creation retry lookup.
- The Session and its initial work commit in one transaction. On `none` that includes the first Turn and the input Items. On `openai_hosted` the input is reserved while the Environment provisions. On `self_hosted` it is reserved with an `environment_connection` action, and creation returns while the machine is offline.
- Reserved initial input has the same five-minute deadline as later input. When it passes before a Turn starts, the Session reads `failed` without a Turn.
- A creation retry returns the original Session and never admits its input again, including after later Turns ([creation retries](./wire-semantics.md)).

## Creation streaming

`stream: true` on Session creation returns 201 with an event stream instead of JSON.

1. The first event is `agent.session.created` with the same committed Session as the JSON 201 body, read after the commit. With initial input on `none` it already reads `in_progress`; on `self_hosted` it already shows the `environment_connection` action.
2. The stream then sends every committed event of the creation exactly once, starting at the creation's own position, so fast execution cannot skip its first events.
3. It ends right after the first `agent.session.idle` recorded when a Turn ends or an input reservation stops waiting (expired, cancelled or failed), or after any `agent.session.failed`, and never sends later events. `requires_action`, function results, resumed work and a `self_hosted` connection keep it open. A reservation keeps it open until a Turn settles or the reservation ends.
4. A creation that admitted nothing ends right after `agent.session.created`. When a settlement records no event, the stream ends after the events committed up to the settled state; another client's work committed before that point can still be sent.

Input reserved while the ending Turn captures Artifacts can start a later Turn that the creation stream does not follow.

A retry with the same `Idempotency-Key` and `stream: true` returns 201 with only the connection comment and ends at once: it admits nothing and follows no work. To recover a lost Session ID, repeat the request with the same key and `stream: false`, then read the Session, Turns and Items. Disconnecting stops only the observer. Observe later Turns with the GET stream.

## Live event stream

`GET /v1/agents/sessions/{session_id}/events` starts at the latest committed event and sends only events committed after that. `Last-Event-ID` is ignored. Events publish after their transaction commits.

- **Lifetime.** The stream stays open across Turns and after a Turn fails. It ends when the Session is deleted or after the terminal `agent.session.failed` of an [Environment initialization failure](#environment-initialization-failure); a stream opened after that failure stays open. A keepalive comment is sent every 15 seconds.
- **Buffer.** Core keeps at most 256 events and 64 MiB of events per Session, plus one larger event when needed. A reader that falls behind the buffer receives an `error` event with type `server_error` and code `stream_interrupted`, and the stream closes. A socket write that blocks for five seconds also closes it. Execution never waits for a reader.
- **Key recheck.** An open stream checks the original Project key at most once per second while idle and before sending output. Revoking the key or archiving the Project closes the stream, and so does an authentication failure. A recheck uses the normal five-second authentication timeout and sends no Session data while it waits. Bytes already sent cannot be recalled.
- **Root work only.** Child Turns and child Items publish no Session events; `agent.session.subagent.*` events and root coordination Items do. Read child work through the [Subagent resources](./subagents.md).

### Event rules

- Session events carry `event_id`, `type` and the `session` snapshot at that transition. Turn events carry `session_id` and `turn_id`. There is no Turn `waiting` event.
- A new Turn publishes `agent.session.turn.created`, the user `item.added`, `agent.session.in_progress`, then `agent.session.turn.in_progress`, all from one transaction. When a batch holds several messages, the later messages follow the Session activity.
- Terminal Turn events (`completed`, `failed`, `cancelled`) carry top-level `usage` copied from the Turn snapshot at that moment, null when unknown. Other events omit it.
- `item.added` and `item.done` always carry `output_index`, null for input Items. Function results emit `item.added` only; `item.done` is for agent output.
- An assistant message follows one sequence: `item.added` in progress with empty `content`, `content_part.added` with empty text, `output_text.delta` events, `output_text.done`, `content_part.done` and `item.done`. A message first observed complete, such as structured output, sends its whole text in one `output_text.delta`, byte for byte. The completed text replaces the accumulated deltas.
- Codex command output streams as `agent.output.command_execution_output.delta` with the command's Item ID and output index. Native output quotas and text conversion apply, so the deltas are not a byte-exact capture; the completed Item is authoritative.
- A cancelled or failed Turn marks its unfinished Items `incomplete` and keeps their partial content.
- A function call stays in `required_actions` until the harness applies its result, or cancellation or the Turn's end removes it. A repeated notification emits no new state.

## Turns and Items

Turn and Item lists take `after`, `limit` and `order` ([list rules](./wire-semantics.md)). Cursors are IDs within the same Session.

**Turns.** Session Turn routes hold root Turns only, ordered by creation time then ID; a child Turn ID returns 404 there. A failed Turn has `error: {code: "internal_error", message: "The execution could not complete."}` and never raw engine diagnostics. Administrators read the failure category through [Session diagnostics](./session-diagnostics.md).

**Items.** Items are ordered by the time they were first observed, then by their position in the Session, then by ID. Updates and retries never move an Item or change its `output_index`, a zero-based position among the Turn's output Items; input Items have none. Reads use the stored history index and never rebuild it from native journals. The Items list includes Items that are still in progress or incomplete.

| Item `type` | Content |
| --- | --- |
| `message` | User or assistant content parts. `phase` is the harness's phase when it reports one (`commentary`, `final_answer`), otherwise null |
| `command_execution` | Command, reported output, exit code, duration and working directory |
| `mcp_call` | Server and tool identity, arguments, structured result or error |
| `function_call`, `function_call_output` | A linked call and its result. The result always carries `output` and `error`, null when the submission omitted them; stored results keep the submitted field presence. Native file changes appear as an `apply_patch` function call with the changes as arguments and no invented result |
| `web_search_call` | The supported action fields (`search`, `open_page`, `find_in_page`, `other`) |
| `reasoning`, `agent_message` and the Subagent coordination calls | See [Subagents](./subagents.md). `agent_message` has no status; reasoning status can be absent or null |

A failed tool does not fail its Turn. Tool output is readable by the Session's Project and can contain the tool's own diagnostic text.

## Usage

Turn and Session `usage` uses the pinned `TokenUsage` fields: input, cached input, output, reasoning output and total tokens. Null means unknown, never zero.

- **Turn usage** is the latest complete snapshot the harness reported for that Turn. A new snapshot replaces the previous one; repeats never add. Stored snapshots survive cancellation and Worker restarts.
- **Session usage** is the sum of root Turn usage when every root Turn has ended (completed, failed or cancelled) with known usage. It is null while any root Turn is queued, running or waiting, and stays null once a root Turn ends without usage. Subagent Turns do not count.
- **By harness.** Codex reports measured snapshots while a Turn runs and at its end; a Turn interrupted before any usage report stays null. Claude Code and MiniMax Code report no complete public breakdown, so their usage is null.

Usage is best-effort accounting of reported measurements. It is not an invoice, and Core never estimates missing usage.

## Environment initialization failure

When an Environment fails to initialize, whether an `openai_hosted` sandbox or a `self_hosted` machine, one transaction records the failure and three events, in this order:

| Event | Payload |
| --- | --- |
| `agent.session.environment.failed` | `error: {type: "environment_error", code: "environment_connection_failed", message: "The environment failed to connect."}` |
| `error` | `error: {type: "environment_error", code: "sandbox_error", message: <reason>, param: null}` |
| `agent.session.failed` | The Session: `status: "failed"`, the reason as `error`, `required_actions: []` and the failure time as `last_active_at` |

Session reads and lists return the same Session, and input that was waiting for the Environment settles as failed in the same snapshot. The GET stream and the creation stream end after `agent.session.failed`. New input returns 409 ([input errors](#input-errors)); the Session can be deleted.

The [Environment initialization contract](./environments.md#initialization-state-and-failure) defines the fixed failure reasons.

Core's own `stream_interrupted` error event carries `type`, `code` and `message` without `param`; `error` events shaped like the official ones carry `param: null`.
