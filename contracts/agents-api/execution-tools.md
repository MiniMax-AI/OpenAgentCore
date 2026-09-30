# Execution tools

An Agent declares application functions, controls and MCP servers in `tools`, and an optional output schema in `text.format`. This contract states how Core validates each declaration, what crosses the Runtime boundary and how callers recover required actions. [Harness capabilities](harness-capabilities.md) lists which Harness supports each operation on which placement. Native workspace tools and Environment Plugin MCP are part of the [Environment](environments.md#skills-plugins-and-environment-mcp).

## Admission

- Saved Agents keep every pinned tool declaration as resource data. Saving never qualifies execution.
- Session creation resolves saved references and inline declarations with the execution parser into the immutable Session snapshot, then checks the combination against the selected Harness's profile in `services/core/internal/engine`. An unsupported combination returns 400 `unsupported_or_invalid_configuration` before anything is written. Protocol errors, such as a repeated `web_search` or `tool_search` or a non-object schema root, use the official error fields ([validation](official-semantics-alignment.md#agent-configuration-validation--september-23)).
- Before dispatch, the selected Runtime must also advertise the operation's capability. An advertisement alone never enables an operation.
- The native Harness runs the model and tool loop. Core adds no second loop, output repair, schema coercion or prompt wrapper, and selects no native tool names.

## Functions

A function declaration requires `name`, `description` and a JSON Schema in `parameters`; `defer_loading` defaults to false and cannot be null. Names are nonblank, unique and at most 512 bytes; a Session holds at most 64 function definitions. Saved references resolve into the Session snapshot, and the definitions stay fixed through native preparation and continuation.

**Results.** A caller submits `agent.session.input.tool_result` events with `turn_id`, `call_id` and `success`, and optional nullable `error` and `output`. Output is a string or ordered text and image parts, subject to the Harness. Batches are atomic and keep field presence, original content and retry identity; public result Items and events always carry `output` and `error`, null when not submitted.

| Case | Response |
| --- | --- |
| Identical retry, also after the Turn ends | Accepted |
| Different result for the same call | 409 `conflict_error` |
| First result after the Turn was cancelled | 409 `conflict_error` |
| Unknown call, or a call of another Turn, in the caller's Session | 400 `invalid_request_error`; the pending action is unchanged |
| Missing or foreign Session | 404 |

[Session input conflicts](official-semantics-alignment.md#session-input-conflicts-and-result-targets--september-23) records the exact messages. Invalid or unsupported content cannot consume a pending call. Admission is separate from application: the adapter confirms a result only when the matching native tool result appears in the live root Turn ([receipt contract](function-result-images.md)). A transport write alone confirms nothing, and a confirmation says nothing about provider consumption or exactly-once external effects. Core never replays a result automatically.

### Required actions and recovery

A pending function appears in Session reads and Session SSE as a required action `{arguments, call_id, name, turn_id, type: "function_call"}` until native application, cancellation or terminal settlement. An offline `self_hosted` Environment with pending input shows `{environment_id, type: "environment_connection"}` ([Environments](environments.md#activity-and-required-actions)).

SSE is live only. After a restart or a lost stream, read the Session's `required_actions`; a `function_call` Item in history does not prove the call is still pending. For a pending function, use the returned Session, Turn and call identity. If the application already ran the function, submit the saved result instead of running the external effect again. For an environment action, connect that exact Environment with its enrollment. Neither a registration nor a disappearing action proves that the model ran; read the Turn and its Items for the outcome. Reconnecting never replays events or external effects.

The Worker fails previously claimed work without replay after execution loss; queued work can remain queued. A result admitted and then cancelled before native observation stays saved internally but can lack a public output Item and `item.added`.

## Structured output

`text.format` accepts `{type: "json_schema", schema: {...}}`, the Agents API form: it has no `name`, `strict` or other Responses API wrapper fields. The schema is saved, inherited through Agent and Session resolution and frozen in the Session snapshot. An explicit non-object root type is a protocol error on save and Session creation for every Harness. Claude requires an explicit `type: "object"` at the schema root. The Claude SDK reads JSON numbers as binary64, so Session admission rejects schemas whose numbers would change in that conversion; saved Agents keep them unchanged.

Core carries the schema in `ExecutionControls.OutputFormat` and requires the profile's structured-output qualification plus the Runtime's `structured_output` and message-observation capabilities, only for requests that use the option. Frozen schemas reach preparation before input and apply to initial and resumed execution; Start cannot replace them.

The Claude adapter passes `outputFormat` to the pinned SDK and allows its native `StructuredOutput` terminal tool, which is internal and never an extra caller function. A matching live root tool result and an attributed successful SDK result confirm the output. The adapter publishes the native `result.result` string unchanged as a completed `final_answer` message with the native tool-use ID; parent assistant prose keeps its own ID. Unvalidated retries and cancelled candidates never become the answer, and the adapter never serializes `structured_output` back to JSON. The stream follows the official message sequence with the whole text in one `output_text.delta`. The bridge advertises the operation only when it reports `structured_output`, and a workspace Runtime also needs `workspace_structured_output`.

## Deferred function discovery

A `tool_search` tool has only `type`; Responses-only execution fields are rejected. Function `defer_loading` marks which definitions load lazily. Discovery requires both: `tool_search` without a deferred function, or deferred functions without `tool_search`, is rejected. The saved-Agent tool union keeps `tool_search`; the pinned Session response union omits it, so Session and SSE resources project it out while the frozen configuration keeps it. The pinned Items union has no tool-search Item, and Core invents none.

Core sends `PromptRequestPayload.ToolSearch` and each `FunctionTool.DeferLoading`, and requires the profile's qualification plus the Runtime's `tool_search` capability. Native search and lazy schema loading belong to the adapter. The Claude adapter's MCP server marks eager definitions `anthropic/alwaysLoad:true` and deferred ones false, and enables native ToolSearch; the function profile allows only declared callbacks and ToolSearch besides the selected workspace tools. A workspace Runtime derives `tool_search` from the bridge's `workspace_tool_search` feature. The native Harness owns model and provider policy; known conflicting modes and beta settings reject in the adapter, and the SDK gives no reliable pre-input signal that deferral took effect after an opaque policy change.

## Web search and programmatic tool calling

```json
[
  {"type": "web_search", "mode": "disabled"},
  {"type": "programmatic_tool_calling", "enabled": false}
]
```

Saved Agents keep every pinned `web_search` mode: omitted or null is saved as `live`, and `cached` and `live` as sent ([saved modes](official-semantics-alignment.md#saved-web_search-modes--september-23)). Search settings are resource data: omitted or null `context_size` resolves to `medium`; omitted domains and location resolve to null; an empty domain list stays empty; a supplied location, including `{}`, has `city`, `country`, `region` and `timezone`, null where omitted.

Execution admits only `mode: "disabled"` and `enabled: false`. Enabled or omitted-mode search and enabled or omitted-`enabled` programmatic calling are rejected at Session admission unless the Session replaces the saved tools. Omitting programmatic configuration keeps each Harness's native behavior, which differs from the official default-on behavior. Unrelated native utility tools are not removed.

`DisableProgrammaticToolCalling` carries the disabled intent on initial execution and cold continuation and requires the Runtime capability only when present. Search uses the existing disabled control.

| Harness | Native enforcement |
| --- | --- |
| Codex | Disables code-mode features; checks native managed requirements before thread start or resume and rejects a forced conflicting feature |
| Claude SDK | Keeps the restricted built-in inventory and verifies native initialization against it |
| MiniMax Code | Keeps the restricted native tool profile, an empty text-execution inventory and disabled web search |

## HTTP MCP

```json
{
  "type": "mcp",
  "server_label": "tickets",
  "transport": {"type": "http", "server_url": "https://mcp.example.com/mcp"},
  "connection_origin": "service",
  "allowed_tools": ["lookup_ticket"],
  "required": false
}
```

- `server_label` is nonempty and unique within the Session. Only the `http` transport is accepted; `server_url` is an absolute HTTP or HTTPS URL without credentials, query or fragment. Nonempty `headers` and `request_metadata` are rejected.
- [Public MCP connection origin](environments.md#public-mcp-connection-origin) owns origin defaults, placement and credential authority; [Harness capabilities](harness-capabilities.md#tools) owns per-Harness support.
- Omitted or null `allowed_tools` permits every server tool; `[]` permits none.
- `required: true` makes native thread creation and cold resume wait for the server to initialize; a failure stops execution without replacing retained history. It needs the Runtime's `mcp_http_required` capability. Public work can be accepted or queued during the wait.
- Bearer authentication uses an attached static or OAuth Vault credential. [Vault credentials](../../services/core/credentials.md) owns selection, and [MCP credential authority](environments.md#public-mcp-connection-origin) owns the frozen Runtime binding. Authenticated execution requires `mcp_http_bearer_auth`.
- The Runtime must advertise `mcp_http_tools`. The native Harness owns discovery, calls and results; public `mcp_call` Items use the original server and tool names and keep the observed native result.

Codex verifies the exact effective MCP configuration before starting or resuming a thread, excludes undeclared servers, disables native apps and plugins, and rejects reserved native labels and stored native MCP credentials. Claude accepts labels of ASCII letters, digits, underscore and hyphen except `functions`, tool names that may also contain dots, and requires connected servers with static inventories. Anonymous Claude requests send a blank Authorization header to suppress native OAuth injection. Native OAuth login is not supported.
