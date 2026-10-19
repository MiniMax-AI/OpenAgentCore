---
title: "Execution tools"
---

An Agent declares application functions, controls and MCP servers in `tools`, and an optional output schema in `text.format`. This contract states how Core validates each declaration, what crosses the Runtime boundary and how callers recover required actions. [Harness capabilities](./harness-capabilities.md) lists which Harness supports each operation on which placement. Native workspace tools and Environment Plugin MCP are part of the [Environment](./environments.md#skills-plugins-and-environment-mcp).

## Admission

- Saved Agents keep every pinned tool declaration as resource data. Saving never qualifies execution.
- Session creation resolves saved references and inline declarations with the execution parser into the immutable Session snapshot, then checks the combination against the selected Harness's declaration in `internal/harnessconfig`. An unsupported combination returns 400 `unsupported_or_invalid_configuration` before anything is written. Protocol errors, such as a repeated `web_search` or `tool_search` or a non-object schema root, use the official error fields ([validation](./wire-semantics.md#configuration-validation)).
- Before dispatch, the selected Runtime must also advertise the operation's capability. An advertisement alone never enables an operation.
- The native Harness runs the model and tool loop. Core adds no second loop, output repair, schema coercion or prompt wrapper, and selects no native tool names.

## Functions

A function declaration requires `name`, `description` and a JSON Schema in `parameters`; `defer_loading` defaults to false and cannot be null. Names are nonblank and unique. Saved references resolve into the Session snapshot, and the definitions stay fixed through native preparation and continuation.

**Results.** A caller submits `agent.session.input.tool_result` events with `turn_id`, `call_id` and `success`, and optional nullable `error` and `output`. Output is a string or ordered text and image parts, subject to the Harness. Batches are atomic and keep field presence, original content and retry identity; public result Items and events always carry `output` and `error`, null when not submitted.

| Case | Response |
| --- | --- |
| Identical retry, also after the Turn ends or the call is abandoned | Accepted |
| Different result for the same call | 409 `conflict_error` |
| First result after the Turn was cancelled | 409 `conflict_error` |
| First result for a call the native Harness abandoned | 409 `conflict_error` |
| Unknown call, or a call of another Turn, in the caller's Session | 400 `invalid_request_error`; the pending action is unchanged |
| Missing or foreign Session | 404 |

[Session input conflicts](./sessions-events.md#input-errors) records the exact messages. Invalid or unsupported content cannot consume a pending call. Admission is separate from application: the adapter confirms a result only when the matching native tool result appears in the live Turn that made the call ([receipt contract](./message-content.md#function-results)). A transport write alone confirms nothing, and a confirmation says nothing about provider consumption or exactly-once external effects. Core never replays a result automatically.

The native Harness can abandon a call before it applies a result, for example when the root Turn closes or interrupts the Subagent that made it, or when MiniMax Code's MCP client times the call out. The call then stops being a required action, the Turn continues and returns to `in_progress` when no required action is left, and a later `function_call_output` for it is rejected like a result after cancellation. An abandoned call has no application receipt.

A function call made by a Subagent is a required action of the root Turn that runs the child. Its `turn_id` is that root Turn, and its `call_id` equals the `call_id` of the child's `function_call` Item on the [Subagent routes](./subagents.md#public-reads). Answer it with `function_call_output` / `tool_result` under that `call_id` and the root Turn ID; the child's Turn ID is not a Session Turn and gets the 400 for a call of another Turn. A Subagent's HTTP MCP calls are `mcp_call` Items in the child's history.

### Required actions and recovery

A pending function appears in Session reads and Session SSE as a required action `{arguments, call_id, name, turn_id, type: "function_call"}` until native application, cancellation or terminal settlement. An offline `self_hosted` Environment with pending input shows `{environment_id, type: "environment_connection"}` ([Environments](./environments.md#activity-and-required-actions)).

SSE is live only. After a restart or a lost stream, read the Session's `required_actions`; a `function_call` Item in history does not prove the call is still pending. For a pending function, use the returned Session, Turn and call identity. If the application already ran the function, submit the saved result instead of running the external effect again. For an environment action, connect that exact Environment with its enrollment. Neither a registration nor a disappearing action proves that the model ran; read the Turn and its Items for the outcome. Reconnecting never replays events or external effects.

The Worker fails previously claimed work without replay after execution loss; queued work can remain queued. A result admitted and then cancelled before native observation stays saved internally but can lack a public output Item and `item.added`.

## Structured output

`text.format` accepts `{type: "json_schema", schema: {...}}`, the Agents API form: it has no `name`, `strict` or other Responses API wrapper fields. The schema is saved, inherited through Agent and Session resolution and frozen in the Session snapshot. An explicit non-object root type is a protocol error on save and Session creation for every Harness. Claude requires an explicit `type: "object"` at the schema root. The Claude SDK reads JSON numbers as binary64, so Session admission rejects schemas whose numbers would change in that conversion; saved Agents keep them unchanged.

Core carries the schema in `ExecutionControls.OutputFormat` and requires structured-output support in the Harness declaration plus the Runtime's `structured_output` and message-observation capabilities, only for requests that use the option. Frozen schemas reach preparation before input and apply to initial and resumed execution; Start cannot replace them.

The Claude adapter passes `outputFormat` to the pinned SDK and allows its native `StructuredOutput` terminal tool, which is internal and never an extra caller function. Natively the tool is added beside Subagents, Skills, MCP and ToolSearch; the adapter's inventory check leaves it and ToolSearch to native, and a missing structured call fails the result. A matching live root tool result and an attributed successful SDK result confirm the output. The adapter publishes the native `result.result` string unchanged as a completed `final_answer` message with the native tool-use ID; parent assistant prose keeps its own ID. Unvalidated retries and cancelled candidates never become the answer, and the adapter never serializes `structured_output` back to JSON. The stream follows the official message sequence with the whole text in one `output_text.delta`. The bridge advertises the operation only when it reports `structured_output`, and a workspace Runtime also needs `workspace_structured_output`.

The Codex adapter sends the schema unchanged as the native `outputSchema` on every root Turn, including Turns after resume or recovery, and publishes the native final agent message text unchanged. Codex reads JSON numbers losslessly, so Session admission applies no number or root-type limit beyond the common rules. A steered input joins the running Turn under its schema, and Subagent threads do not receive the schema. Codex passes the schema to the model provider as a strict Responses `text.format`; a provider that cannot run it returns its own error unchanged, and a provider that ignores it can return text that does not match the schema.

## Deferred function discovery

A `tool_search` tool has only `type`; Responses-only execution fields are rejected. Function `defer_loading` marks which definitions load lazily. Discovery requires both: `tool_search` without a deferred function, or deferred functions without `tool_search`, is rejected. The saved-Agent tool union keeps `tool_search`; the pinned Session response union omits it, so Session and SSE resources project it out while the frozen configuration keeps it. The pinned Items union has no tool-search Item, and Core invents none.

Core sends `PromptRequestPayload.ToolSearch` and each `FunctionTool.DeferLoading`, and requires `tool_search` support in both the Harness declaration and the Runtime. Native search and lazy schema loading belong to the adapter. The Claude adapter's MCP server marks eager definitions `anthropic/alwaysLoad:true` and deferred ones false, and sets `ENABLE_TOOL_SEARCH=true`; the `none` profile allows only declared callbacks, declared MCP tools and ToolSearch. A workspace Runtime derives `tool_search` from the bridge's `workspace_tool_search` feature. The native Harness owns model, provider and beta-setting policy; the adapter rejects none of them, and the SDK gives no reliable pre-input signal that deferral took effect.

## Web search and programmatic tool calling

```json
[
  {"type": "web_search", "mode": "disabled"},
  {"type": "programmatic_tool_calling", "enabled": false}
]
```

Saved Agents keep every pinned `web_search` mode: omitted or null is saved as `live`, and `cached` and `live` as sent ([saved modes](./wire-semantics.md#saved-configuration)). Search settings are resource data: omitted or null `context_size` resolves to `medium`; omitted domains and location resolve to null; an empty domain list stays empty; a supplied location, including `{}`, has `city`, `country`, `region` and `timezone`, null where omitted.

Execution carries the resolved values to the Harness and validates them against its declaration in `internal/harnessconfig` (`Execution`): the supported web search modes and whether search settings are accepted, reasoning efforts and summaries, service tiers, programmatic-tool-calling values and text verbosities. A value outside the declaration is rejected at Session admission with a message naming the field, unless the Session replaces the saved tools. Core sends an option only when the Agent sets it or the pinned default applies, so an omitted `web_search` or `programmatic_tool_calling` keeps each Harness's native behavior. For programmatic calling this differs from the official default-on behavior. Unrelated native utility tools are not removed. Claude Code on a workspace placement also keeps `WebSearch` and `WebFetch` when the control is omitted; [Turns and Items](./sessions-events.md#turns-and-items) gives their `web_search_call` actions.

`ExecutionControls` carries the options ([Runtime protocol](../../docs/runtime-protocol.md#capability-declarations)). The Runtime needs only the `execution_controls` capability.

| Harness | Web search | Programmatic tool calling |
| --- | --- | --- |
| Codex | `disabled`, `cached` and `live`, with `context_size`, `allowed_domains` and `location`, as native `web_search` and `tools.web_search` settings | `enabled: true` turns on native `code_mode`; `false` turns off `code_mode`, `code_mode_only` and `code_mode_prewarm` and checks native managed requirements before a thread starts or resumes |
| Claude Code | `disabled` and `live`, with `context_size: "medium"` and no `allowed_domains` or `location`. On a workspace placement the native `claude_code` tool set runs without ask-user, scheduling, workflow, background and `ListAgents` tools; `disabled` removes `WebSearch` and `WebFetch`, and `live` keeps them. `environment: none` keeps no built-in tools, and `live` adds only `WebSearch`, a provider-side tool. `WebFetch` stays off there because it would fetch from the Runtime host without an Environment | `false` only: native REPL calls tools from model-written code, outside the tool records that function receipts and tool observations read |
| MiniMax Code | `disabled` only, through the native `features.webSearch=false` because native search needs a MiniMax account credential. On a workspace placement the native tools run, and `disabled` also removes `web_fetch` while an omitted control keeps it. `environment: none` has no built-in tools | `false` only |

## MCP

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

- `server_label` is nonempty and unique within the Session. The `http` and `stdio` transports are accepted; `server_url` is an absolute HTTP or HTTPS URL without credentials or fragment. Nonempty `request_metadata` is rejected with "Nonempty MCP request_metadata is not supported yet." and an inline `authorization` with "Inline MCP authorization is not supported; use credential_id.". `headers` are non-secret, need valid HTTP names and values with names unique ignoring case, and reach the native Harness unchanged beside any bearer; an `Authorization` header is rejected, because credentials enter only through a Vault credential. Claude rejects `${` in `server_url` or a header value ([native differences](./index.md#differences-from-openai)).
- [Public MCP connection origin](./environments.md#public-mcp-connection-origin) owns origin defaults, placement and credential authority; [Harness capabilities](./harness-capabilities.md#tools) owns per-Harness support.
- Omitted or null `allowed_tools` permits every server tool; `[]` permits none.
- `required: true` makes native thread creation and cold resume wait for the server to initialize; a failure stops execution without replacing retained history. It needs the Runtime's `mcp_http_required` capability. Public work can be accepted or queued during the wait.
- Bearer authentication uses an attached static or OAuth Vault credential. [Vault credentials](./vaults.md) owns selection, and [MCP credential authority](./environments.md#public-mcp-connection-origin) owns the frozen Runtime binding. Authenticated execution requires `mcp_http_bearer_auth`.
- The Runtime must advertise `mcp_http_tools`. The native Harness owns discovery, calls and results; public `mcp_call` Items use the original server and tool names and keep the observed native result.

A `stdio` server carries `command`, `args`, `cwd` and `env_vars`, runs as a process of the Environment and needs a workspace placement (`openai_hosted` or `self_hosted`); on `environment: none` admission returns a 400. Its `connection_origin` defaults to and must be `environment`, and it takes no `credential_id`. A nonempty Session `env` is rejected with "Inline MCP env is not supported; declare env_vars and set the values in the Environment."; `command` is nonempty, `cwd` is an absolute path, `env_vars` holds variable names and no part contains NUL. `env_vars` names variables of the Environment, whose values never reach Core; as for [Plugin MCP](./environments.md#plugin-mcp), the start fails when the Environment does not set one. The Runtime must advertise `mcp_http_tools`, which covers every public MCP server.

- Codex writes `command`, `args`, `cwd` and `env_vars` to its `[mcp_servers]` table and verifies them with the rest of the declared configuration before any native connection.
- Claude Code starts the server with `command` and `args` in the directory of its own process, which is the workspace directory, so admission rejects any other `cwd` with "The configured engine starts MCP stdio servers in the workspace directory; set cwd to it." Core rejects non-null `allowed_tools` for a stdio server, because the adapter can hide unselected tools only on HTTP servers.
- MiniMax Code starts session stdio servers in its private home directory, which no `cwd` can name, so admission rejects every stdio server with "The configured engine cannot start MCP stdio servers in the declared cwd."
- Codex passes `env_vars` to its restricted native environment. Claude Code starts the server with its whole process environment, as it starts Bash, so the server also sees the model provider key and the Vault bearer tokens of other MCP servers, and a variable the adapter sets, such as `HOME`, keeps the adapter's value.

Codex trusts the workspace natively, so its project configuration loads on start and cold resume, and undeclared servers, apps and plugins keep their native behavior. Before any native connection that carries a Core credential, it verifies that each declared server keeps its URL or command, bearer reference and headers, and fails the start or resume with an error otherwise. It rejects reserved native labels and stored native MCP credentials. Claude accepts labels of ASCII letters, digits, underscore and hyphen except `functions`, any tool name (the native client normalizes it, and the adapter rejects normalized collisions), and requires static inventories. As locally, Claude runs on without the tools of an optional server that did not connect; a required one fails the start. MiniMax Code serves public functions as the MCP server `functions`, so an Agent with functions cannot use that label for a public or Plugin server. Anonymous Claude requests send a blank Authorization header to suppress native OAuth injection. Native OAuth login is not supported.
