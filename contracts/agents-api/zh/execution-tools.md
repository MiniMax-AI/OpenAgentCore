---
title: "执行工具"
source: contracts/agents-api/execution-tools.md
source_hash: 7783010b0968214d235d349fd60d1ee6096f0ab9d33d58397d90bb0fd935cded
---

Agent 在 `tools` 中声明应用函数、控制项和 MCP 服务器，并可在 `text.format` 中声明输出 schema。本契约说明 Core 如何验证声明、哪些内容跨越 Runtime 边界，以及调用方如何恢复待执行操作。[Harness 能力](harness-capabilities.md)列出各 Harness 在不同部署位置支持的操作。原生工作区工具和 Environment Plugin MCP 属于 [Environment](environments.md#skills-plugins-and-environment-mcp)。

## 准入 {#admission}

- 已保存的 Agent 将所有固定版本工具声明保留为资源数据。保存不代表通过执行资格验证。
- 创建 Session 时，执行解析器将保存的引用与内联声明解析为不可变 Session 快照，再根据 `internal/harnessconfig` 中所选 Harness 的声明检查组合。不支持的组合在任何写入之前返回 400 `unsupported_or_invalid_configuration`。重复 `web_search` 或 `tool_search`、非对象 schema 根等协议错误使用官方错误字段（[验证](wire-semantics.md#configuration-validation)）。
- 分发之前，所选 Runtime 也必须声明该操作的能力。仅有能力声明不会启用操作。
- 原生 Harness 运行模型与工具循环。Core 不添加第二个循环、输出修复、schema 强制转换或提示词包装，也不选择原生工具名称。

## 函数 {#functions}

函数声明要求 `name`、`description` 和 `parameters` 中的 JSON Schema；`defer_loading` 默认为 false，不能为 null。名称必须非空白且唯一。保存的引用解析到 Session 快照，定义在原生准备和继续执行期间保持固定。

**结果。** 调用方提交 `agent.session.input.tool_result` 事件，包含 `turn_id`、`call_id`、`success`，以及可选且可空的 `error` 和 `output`。输出为字符串或有序文本与图像部分，受 Harness 支持范围约束。批次原子处理并保留字段存在性、原始内容和重试身份；公开结果 Item 与事件始终包含 `output` 和 `error`，未提交时为 null。

| 情况 | 响应 |
| --- | --- |
| 相同重试，包括 Turn 结束或调用被放弃之后 | 接受 |
| 同一调用提交不同结果 | 409 `conflict_error` |
| Turn 取消后首次提交结果 | 409 `conflict_error` |
| 为原生 Harness 已放弃的调用首次提交结果 | 409 `conflict_error` |
| 调用方 Session 内未知调用或其他 Turn 的调用 | 400 `invalid_request_error`；待执行操作不变 |
| Session 不存在或属于其他范围 | 404 |

[Session 输入冲突](sessions-events.md#input-errors)记录准确消息。无效或不支持的内容不能消耗待处理调用。准入与应用结果分离：只有匹配的原生工具结果出现在发起调用的实时 Turn 中，适配器才确认结果（[回执契约](message-content.md#function-results)）。传输写入本身不确认任何事实，确认也不代表提供商已消费结果或外部副作用恰好发生一次。Core 不自动重放结果。

原生 Harness 可能在应用结果之前放弃调用，例如根 Turn 关闭或中断了发起该调用的 Subagent，或 MiniMax Code 的 MCP 客户端使该调用超时。该调用随即不再是待执行操作，Turn 继续运行，并在没有待执行操作时回到 `in_progress`；之后为它提交的 `function_call_output` 会像取消后的结果一样被拒绝。被放弃的调用没有应用回执。

Subagent 发起的函数调用是运行该子级的根 Turn 的待执行操作：`turn_id` 是该根 Turn，`call_id` 等于 [Subagent 路由](subagents.md#public-reads)上该子级 `function_call` Item 的 `call_id`。使用该 `call_id`，以及根 Turn ID 或该调用的 `agent.session.turn.item.added` 事件所标明的子级 Turn ID（官方 SDK 发送的即是后者），以 `function_call_output` / `tool_result` 作答；其他 Turn ID 会得到“另一个 Turn 的调用”的 400。Subagent 的 HTTP MCP 调用是该子级历史中的 `mcp_call` Item。

### 待执行操作与恢复 {#required-actions-and-recovery}

待处理函数在 Session 读取和 Session SSE 中显示为待执行操作 `{arguments, call_id, name, turn_id, type: "function_call"}`，直到原生应用、取消或终态结算。离线且有待处理输入的 `self_hosted` Environment 显示 `{environment_id, type: "environment_connection"}`（[Environment](environments.md#activity-and-required-actions)）。

SSE 仅提供实时事件。重启或流丢失后，读取 Session 的 `required_actions`；历史中的 `function_call` Item 不证明调用仍待处理。对于待处理函数，使用返回的 Session、Turn 与调用身份。如果应用已执行函数，应提交保存的结果，避免再次执行外部副作用。对于 Environment 操作，用其登记信息连接准确的 Environment。注册或操作消失均不证明模型运行过；读取 Turn 及其 Item 查看结果。重新连接不重放事件或外部副作用。

执行丢失后，Worker 将先前已领取的工作标记失败，不进行重放；排队工作可以继续排队。结果在准入后、原生观察之前被取消时，仍在内部保存，但可能没有公开输出 Item 和 `item.added`。

## 结构化输出 {#structured-output}

`text.format` 接受 `{type: "json_schema", schema: {...}}`，即 Agents API 形式：不包含 `name`、`strict` 或其他 Responses API 包装字段。schema 被保存，经 Agent 和 Session 解析继承，并冻结于 Session 快照。对所有 Harness，显式非对象根类型在保存和 Session 创建时均为协议错误。Claude 要求 schema 根显式为 `type: "object"`。Claude SDK 将 JSON 数字读为 binary64，因此 Session 准入拒绝数字在转换中会变化的 schema；已保存 Agent 保留原值。

Core 在 `ExecutionControls.OutputFormat` 中携带 schema，仅对使用该选项的请求要求 Harness 声明支持结构化输出，并要求 Runtime 具有 `structured_output` 和消息观察能力。冻结的 schema 在输入之前送达准备阶段，适用于初次和恢复执行；Start 不能替换它。

Claude 适配器将 `outputFormat` 传给固定版本 SDK，并允许原生 `StructuredOutput` 终态工具；该工具属于内部，不是额外的调用方函数。原生会在 Subagent、Skill、MCP 和 ToolSearch 之外添加该工具；适配器的清单检查将它和 ToolSearch 交给原生决定，缺少结构化调用会使结果失败。匹配的实时根工具结果和已归属的成功 SDK 结果确认输出。适配器将原生 `result.result` 字符串原样发布为已完成的 `final_answer` 消息，使用原生 tool-use ID；父 assistant 文本保留自己的 ID。未验证重试和已取消候选不会成为答案，适配器不会把 `structured_output` 重新序列化为 JSON。流遵循官方消息顺序，将整段文本放入一个 `output_text.delta`。桥接层仅在报告 `structured_output` 时声明该操作，工作区 Runtime 还需要 `workspace_structured_output`。

Codex 适配器在每个根 Turn（包括 resume 或恢复后的 Turn）上将 schema 原样作为原生 `outputSchema` 发送，并原样发布原生最终 agent 消息文本。Codex 无损读取 JSON 数字，因此 Session 准入除通用规则外不附加数字或根类型限制。引导（steer）输入并入正在运行的 Turn 并沿用其 schema，Subagent 线程不接收该 schema。Codex 将 schema 作为 strict 的 Responses `text.format` 传给模型提供商；无法运行它的提供商原样返回自己的错误，忽略它的提供商可能返回与 schema 不符的文本。

## 延迟函数发现 {#deferred-function-discovery}

`tool_search` 工具仅包含 `type`；仅适用于 Responses 的执行字段被拒绝。函数 `defer_loading` 标记延迟加载的定义。发现要求两者同时存在：没有延迟函数的 `tool_search`，或没有 `tool_search` 的延迟函数均被拒绝。已保存 Agent 的工具联合类型保留 `tool_search`；固定版本 Session 响应联合类型省略它，因此 Session 与 SSE 资源投影移除它，冻结配置仍保留它。固定版本 Item 联合类型没有 tool-search Item，Core 不自行创建。

Core 发送 `PromptRequestPayload.ToolSearch` 和每个 `FunctionTool.DeferLoading`，并要求 Harness 声明和 Runtime 均支持 `tool_search`。原生搜索与 schema 延迟加载属于适配器。Claude 适配器的 MCP 服务器将立即加载定义标记为 `anthropic/alwaysLoad:true`，延迟定义标记为 false，并设置 `ENABLE_TOOL_SEARCH=true`；`none` 配置只允许所声明回调、所声明 MCP 工具和 ToolSearch。工作区 Runtime 从桥接层的 `workspace_tool_search` 功能推导 `tool_search`。原生 Harness 管理模型、提供商与 beta 设置策略；适配器不拒绝其中任何一项，SDK 也不提供可靠的输入前信号来确认延迟加载是否生效。

## Web 搜索与程序化工具调用 {#web-search-and-programmatic-tool-calling}

```json
[
  {"type": "web_search", "mode": "disabled"},
  {"type": "programmatic_tool_calling", "enabled": false}
]
```

已保存 Agent 保留固定版本的所有 `web_search` 模式：省略或 null 保存为 `live`，`cached` 和 `live` 原样保存（[保存模式](wire-semantics.md#saved-configuration)）。搜索设置是资源数据：省略或 null 的 `context_size` 解析为 `medium`；省略域名和位置解析为 null；空域名列表保持为空；提供的位置（包括 `{}`）包含 `city`、`country`、`region` 和 `timezone`，省略字段为 null。

执行把解析后的值交给 Harness，并按 `internal/harnessconfig` 中的声明（`Execution`）校验：支持的 Web 搜索模式以及是否接受搜索设置、reasoning effort 与 summary、service tier、程序化工具调用的取值和 text verbosity。声明之外的值会在 Session 准入时被拒绝，消息会指出字段，除非 Session 替换了保存的工具。Core 仅在 Agent 设置了选项或适用固定版本的默认值时才发送该选项，因此省略 `web_search` 或 `programmatic_tool_calling` 会保留各 Harness 的原生行为。对程序化调用而言，这与官方默认启用的行为不同。无关的原生实用工具不会被移除。省略该控制项时，工作区位置上的 Claude Code 也保留 `WebSearch` 和 `WebFetch`；其 `web_search_call` 操作见 [Turn 与 Item](./sessions-events.md#turns-and-items)。

`ExecutionControls` 携带这些选项（[Runtime 协议](../../../docs/zh/runtime-protocol.md#capability-declarations)）。Runtime 只需要 `execution_controls` 能力。

| Harness | Web 搜索 | 程序化工具调用 |
| --- | --- | --- |
| Codex | `disabled`、`cached` 和 `live`，以及 `context_size`、`allowed_domains` 和 `location`，对应原生 `web_search` 与 `tools.web_search` 设置 | `enabled: true` 开启原生 `code_mode`；`false` 关闭 `code_mode`、`code_mode_only` 和 `code_mode_prewarm`，并在启动或恢复线程前检查原生受管要求 |
| Claude Code | `disabled` 和 `live`，需使用 `context_size: "medium"`，且不支持 `allowed_domains` 或 `location`。在工作区位置运行原生 `claude_code` 工具集，但移除向用户提问、定时、工作流、后台和 `ListAgents` 工具；`disabled` 会移除 `WebSearch` 和 `WebFetch`，`live` 保留它们。`environment: none` 不保留内置工具，`live` 只增加提供方侧工具 `WebSearch`。`WebFetch` 在此保持关闭，因为没有 Environment 时它会从 Runtime 主机发起抓取 | 仅 `false`：原生 REPL 在模型编写的代码中调用工具，不经过函数回执和工具观察所读取的工具记录 |
| MiniMax Code | 仅 `disabled`，通过原生 `features.webSearch=false` 实现，因为原生搜索需要 MiniMax 账号凭据。在工作区位置上运行原生工具，`disabled` 还会移除 `web_fetch`，省略该控制项则保留。`environment: none` 没有内置工具 | 仅 `false` |

## MCP {#mcp}

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

- `server_label` 只含字母、数字、下划线和连字符，且在各工具之间唯一；违反时返回 400 `invalid_request_error`，并指出出错工具的 `server_label`。被拒绝的传输成员以同样方式指出，例如 `tools[0].transport.cwd`，Session 内联 Agent 的路径以 `agent.` 开头。接受 `http` 和 `stdio` 传输；`server_url` 为不带凭据或片段的绝对 HTTP 或 HTTPS URL。非空 `request_metadata` 被拒绝，消息为 "No engine sends MCP request_metadata."；内联 `authorization` 被拒绝，消息为 "Inline MCP authorization is not supported; use credential_id."。`headers` 为非机密标头，名称和值须为有效的 HTTP 名称和值，且名称不区分大小写唯一，会与 bearer 一同原样传给原生 Harness；`Authorization` 标头被拒绝，因为凭据只通过 Vault 凭据进入。Claude 会拒绝 `server_url` 或标头值中的 `${`（[原生差异](index.md#differences-from-openai)）。
- [公开 MCP 连接来源](environments.md#public-mcp-connection-origin)定义来源默认值、部署位置和凭据权限；[Harness 能力](harness-capabilities.md#tools)定义各 Harness 支持范围。
- 省略或 null 的 `allowed_tools` 允许所有服务器工具；`[]` 不允许任何工具。
- `required: true` 使原生线程创建和冷恢复等待服务器初始化；失败会停止执行，不替换保留历史。它要求 Runtime 的 `mcp_http_required` 能力。等待期间公开工作可被接受或排队。
- Bearer 认证使用附加的静态或 OAuth Vault 凭据。[Vault 凭据](vaults.md)定义选择规则，[MCP 凭据权限](environments.md#public-mcp-connection-origin)定义冻结的 Runtime 绑定。认证执行要求 `mcp_http_bearer_auth`。
- Runtime 必须声明 `mcp_http_tools`。原生 Harness 管理发现、调用和结果；公开 `mcp_call` Item 使用原始服务器与工具名称，保留观察到的原生结果。

`stdio` 服务器携带 `command`、`args`、`cwd` 和 `env_vars`，作为 Environment 的进程运行，需要工作区部署位置（`openai_hosted` 或 `self_hosted`）；在 `environment: none` 上，准入返回 400。其 `connection_origin` 默认且只能为 `environment`，不带 `credential_id`。非空的 Session `env` 被拒绝，消息为 "Inline MCP env is not supported; declare env_vars and set the values in the Environment."；`command` 非空，`cwd` 为绝对路径，`env_vars` 只含变量名，且任何部分都不含 NUL。`env_vars` 指定 Environment 中的变量名，其值不会到达 Core；与 [Plugin MCP](environments.md#plugin-mcp) 相同，Environment 未设置其中某个变量时启动失败。Runtime 必须声明 `mcp_http_tools`，它涵盖所有公开 MCP 服务器。

- Codex 把 `command`、`args`、`cwd` 和 `env_vars` 写入其 `[mcp_servers]` 表，并在任何原生连接之前与其余已声明配置一并验证。
- Claude Code 以 `command` 和 `args` 在其自身进程所在目录（即工作区目录）启动服务器，因此准入以 "The configured engine starts MCP stdio servers in the workspace directory; set cwd to it." 拒绝其他 `cwd`。Core 拒绝 stdio 服务器的非 null `allowed_tools`，因为适配器只能在 HTTP 服务器上隐藏未选工具。
- MiniMax Code 在其私有主目录中启动会话 stdio 服务器，任何 `cwd` 都无法指向该目录，因此准入拒绝所有 stdio 服务器，消息为 "The configured engine cannot start MCP stdio servers in the declared cwd."。
- Codex 把 `env_vars` 交给其受限的原生环境。Claude Code 像启动 Bash 一样以整个进程环境启动服务器，因此服务器也能读到模型提供方密钥和其他 MCP 服务器的 Vault bearer 令牌，适配器设置的变量（如 `HOME`）保持适配器的值。

Codex 以原生方式信任工作区，因此工作区的项目配置在启动和冷恢复时加载，未声明的服务器、apps 和 plugins 保持原生行为。在任何携带 Core 凭据的原生连接之前，它验证每个已声明服务器保持其 URL 或命令、bearer 引用和请求头，否则启动或恢复以错误失败。它拒绝原生保留标签和已存储原生 MCP 凭据。Claude 接受 ASCII 字母、数字、下划线和连字符组成的标签，但不允许 `functions`；接受任意工具名称（原生客户端会将其规范化，适配器拒绝规范化后的冲突），并要求静态工具清单。与本地一致，可选服务器未连接时 Claude 不带其工具继续运行；必需服务器未连接则启动失败。MiniMax Code 以 MCP 服务器 `functions` 提供公共函数，因此声明了函数的 Agent 不能让公共或 Plugin 服务器使用该标签。匿名 Claude 请求发送空 Authorization 头以阻止原生 OAuth 注入。不支持原生 OAuth 登录。
