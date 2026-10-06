---
title: "执行工具"
source: contracts/agents-api/execution-tools.md
source_hash: 5e0b9ab1938ccd0ee7b22b1482a365bd54285c8c09fc4cb1ca172e34ab122ce2
---

Agent 在 `tools` 中声明应用函数、控制项和 MCP 服务器，并可在 `text.format` 中声明输出 schema。本契约说明 Core 如何验证声明、哪些内容跨越 Runtime 边界，以及调用方如何恢复待执行操作。[Harness 能力](harness-capabilities.md)列出各 Harness 在不同部署位置支持的操作。原生工作区工具和 Environment Plugin MCP 属于 [Environment](environments.md#skills-plugins-and-environment-mcp)。

## 准入 {#admission}

- 已保存的 Agent 将所有固定版本工具声明保留为资源数据。保存不代表通过执行资格验证。
- 创建 Session 时，执行解析器将保存的引用与内联声明解析为不可变 Session 快照，再根据 `services/core/internal/engine` 中所选 Harness 的配置检查组合。不支持的组合在任何写入之前返回 400 `unsupported_or_invalid_configuration`。重复 `web_search` 或 `tool_search`、非对象 schema 根等协议错误使用官方错误字段（[验证](wire-semantics.md#configuration-validation)）。
- 分发之前，所选 Runtime 也必须声明该操作的能力。仅有能力声明不会启用操作。
- 原生 Harness 运行模型与工具循环。Core 不添加第二个循环、输出修复、schema 强制转换或提示词包装，也不选择原生工具名称。

## 函数 {#functions}

函数声明要求 `name`、`description` 和 `parameters` 中的 JSON Schema；`defer_loading` 默认为 false，不能为 null。名称必须非空白、唯一且最多 512 字节；每个 Session 最多有 64 个函数定义。保存的引用解析到 Session 快照，定义在原生准备和继续执行期间保持固定。

**结果。** 调用方提交 `agent.session.input.tool_result` 事件，包含 `turn_id`、`call_id`、`success`，以及可选且可空的 `error` 和 `output`。输出为字符串或有序文本与图像部分，受 Harness 支持范围约束。批次原子处理并保留字段存在性、原始内容和重试身份；公开结果 Item 与事件始终包含 `output` 和 `error`，未提交时为 null。

| 情况 | 响应 |
| --- | --- |
| 相同重试，包括 Turn 结束之后 | 接受 |
| 同一调用提交不同结果 | 409 `conflict_error` |
| Turn 取消后首次提交结果 | 409 `conflict_error` |
| 调用方 Session 内未知调用或其他 Turn 的调用 | 400 `invalid_request_error`；待执行操作不变 |
| Session 不存在或属于其他范围 | 404 |

[Session 输入冲突](sessions-events.md#input-errors)记录准确消息。无效或不支持的内容不能消耗待处理调用。准入与应用结果分离：只有匹配的原生工具结果出现在实时根 Turn 中，适配器才确认结果（[回执契约](message-content.md#function-results)）。传输写入本身不确认任何事实，确认也不代表提供方已消费结果或外部副作用恰好发生一次。Core 不自动重放结果。

### 待执行操作与恢复 {#required-actions-and-recovery}

待处理函数在 Session 读取和 Session SSE 中显示为待执行操作 `{arguments, call_id, name, turn_id, type: "function_call"}`，直到原生应用、取消或终态结算。离线且有待处理输入的 `self_hosted` Environment 显示 `{environment_id, type: "environment_connection"}`（[Environment](environments.md#activity-and-required-actions)）。

SSE 仅提供实时事件。重启或流丢失后，读取 Session 的 `required_actions`；历史中的 `function_call` Item 不证明调用仍待处理。对于待处理函数，使用返回的 Session、Turn 与调用身份。如果应用已执行函数，应提交保存的结果，避免再次执行外部副作用。对于 Environment 操作，用其登记信息连接准确的 Environment。注册或操作消失均不证明模型运行过；读取 Turn 及其 Item 查看结果。重新连接不重放事件或外部副作用。

执行丢失后，Worker 将先前已领取的工作标记失败，不进行重放；排队工作可以继续排队。结果在准入后、原生观察之前被取消时，仍在内部保存，但可能没有公开输出 Item 和 `item.added`。

## 结构化输出 {#structured-output}

`text.format` 接受 `{type: "json_schema", schema: {...}}`，即 Agents API 形式：不包含 `name`、`strict` 或其他 Responses API 包装字段。schema 被保存，经 Agent 和 Session 解析继承，并冻结于 Session 快照。对所有 Harness，显式非对象根类型在保存和 Session 创建时均为协议错误。Claude 要求 schema 根显式为 `type: "object"`。Claude SDK 将 JSON 数字读为 binary64，因此 Session 准入拒绝数字在转换中会变化的 schema；已保存 Agent 保留原值。

Core 在 `ExecutionControls.OutputFormat` 中携带 schema，仅对使用该选项的请求要求配置通过结构化输出资格验证，并要求 Runtime 具有 `structured_output` 和消息观察能力。冻结的 schema 在输入之前送达准备阶段，适用于初次和恢复执行；Start 不能替换它。

Claude 适配器将 `outputFormat` 传给固定版本 SDK，并允许原生 `StructuredOutput` 终态工具；该工具属于内部，不是额外的调用方函数。匹配的实时根工具结果和已归属的成功 SDK 结果确认输出。适配器将原生 `result.result` 字符串原样发布为已完成的 `final_answer` 消息，使用原生 tool-use ID；父 assistant 文本保留自己的 ID。未验证重试和已取消候选不会成为答案，适配器不会把 `structured_output` 重新序列化为 JSON。流遵循官方消息顺序，将整段文本放入一个 `output_text.delta`。桥接层仅在报告 `structured_output` 时声明该操作，工作区 Runtime 还需要 `workspace_structured_output`。

## 延迟函数发现 {#deferred-function-discovery}

`tool_search` 工具仅包含 `type`；仅适用于 Responses 的执行字段被拒绝。函数 `defer_loading` 标记延迟加载的定义。发现要求两者同时存在：没有延迟函数的 `tool_search`，或没有 `tool_search` 的延迟函数均被拒绝。已保存 Agent 的工具联合类型保留 `tool_search`；固定版本 Session 响应联合类型省略它，因此 Session 与 SSE 资源投影移除它，冻结配置仍保留它。固定版本 Item 联合类型没有 tool-search Item，Core 不自行创建。

Core 发送 `PromptRequestPayload.ToolSearch` 和每个 `FunctionTool.DeferLoading`，并要求配置通过资格验证、Runtime 具备 `tool_search` 能力。原生搜索与 schema 延迟加载属于适配器。Claude 适配器的 MCP 服务器将立即加载定义标记为 `anthropic/alwaysLoad:true`，延迟定义标记为 false，并启用原生 ToolSearch；函数配置仅允许所声明回调、ToolSearch 与选定的工作区工具。工作区 Runtime 从桥接层的 `workspace_tool_search` 功能推导 `tool_search`。原生 Harness 管理模型与提供方策略；已知冲突模式和 beta 设置在适配器中拒绝，不透明策略改变后，SDK 不提供可靠的输入前信号来确认延迟加载是否生效。

## Web 搜索与程序化工具调用 {#web-search-and-programmatic-tool-calling}

```json
[
  {"type": "web_search", "mode": "disabled"},
  {"type": "programmatic_tool_calling", "enabled": false}
]
```

已保存 Agent 保留固定版本的所有 `web_search` 模式：省略或 null 保存为 `live`，`cached` 和 `live` 原样保存（[保存模式](wire-semantics.md#saved-configuration)）。搜索设置是资源数据：省略或 null 的 `context_size` 解析为 `medium`；省略域名和位置解析为 null；空域名列表保持为空；提供的位置（包括 `{}`）包含 `city`、`country`、`region` 和 `timezone`，省略字段为 null。

执行仅允许 `mode: "disabled"` 和 `enabled: false`。启用或省略模式的搜索、启用或省略 `enabled` 的程序化调用，在 Session 准入时被拒绝，除非 Session 替换了保存的工具。省略程序化配置会保留各 Harness 原生行为，这与官方默认启用行为不同。无关的原生实用工具不会被移除。

`DisableProgrammaticToolCalling` 在初次执行和冷继续执行中携带禁用意图，仅在存在时要求 Runtime 能力。搜索使用已有禁用控制项。

| Harness | 原生执行约束 |
| --- | --- |
| Codex | 禁用 code-mode 功能；在启动或恢复线程前检查原生受管要求，拒绝被强制启用的冲突功能 |
| Claude SDK | 保留受限内置工具清单，并据此验证原生初始化 |
| MiniMax Code | 保留受限原生工具配置、空文本执行工具清单和禁用的 Web 搜索 |

## HTTP MCP {#http-mcp}

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

- `server_label` 非空且在 Session 中唯一。仅接受 `http` 传输；`server_url` 为不带凭据、查询或片段的绝对 HTTP 或 HTTPS URL。非空 `headers` 和 `request_metadata` 被拒绝。
- [公开 MCP 连接来源](environments.md#public-mcp-connection-origin)定义来源默认值、部署位置和凭据权限；[Harness 能力](harness-capabilities.md#tools)定义各 Harness 支持范围。
- 省略或 null 的 `allowed_tools` 允许所有服务器工具；`[]` 不允许任何工具。
- `required: true` 使原生线程创建和冷恢复等待服务器初始化；失败会停止执行，不替换保留历史。它要求 Runtime 的 `mcp_http_required` 能力。等待期间公开工作可被接受或排队。
- Bearer 认证使用附加的静态或 OAuth Vault 凭据。[Vault 凭据](vaults.md)定义选择规则，[MCP 凭据权限](environments.md#public-mcp-connection-origin)定义冻结的 Runtime 绑定。认证执行要求 `mcp_http_bearer_auth`。
- Runtime 必须声明 `mcp_http_tools`。原生 Harness 管理发现、调用和结果；公开 `mcp_call` Item 使用原始服务器与工具名称，保留观察到的原生结果。

Codex 在启动或恢复线程之前验证准确的有效 MCP 配置，排除未声明服务器，禁用原生 apps 和 plugins，拒绝原生保留标签和已存储原生 MCP 凭据。Claude 接受 ASCII 字母、数字、下划线和连字符组成的标签，但不允许 `functions`；工具名称还可包含点，并要求已连接服务器具有静态工具清单。匿名 Claude 请求发送空 Authorization 头以阻止原生 OAuth 注入。不支持原生 OAuth 登录。
