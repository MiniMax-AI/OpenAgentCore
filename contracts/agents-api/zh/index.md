---
title: "Agents API 覆盖台账"
source: contracts/agents-api/index.md
source_hash: 3e6abc5b98cd47c332ff6f5c12fc8676a35428da02a6caae2648dc8bab6bc486
---

Core 旨在以下方固定版本为准支持完整的 OpenAI Agents API（[public API rule](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#public-api)）。本台账记录 Core 对各项资源实现了哪些内容、哪些契约保存其详细信息，并列出相对于 OpenAI 服务的所有已知差异和所有未解决缺口。[API namespaces and credentials](../../../docs/zh/api/index.md) 说明谁调用哪些 API；[Agents API guide](../../../docs/zh/api/public-agent-api.md) 介绍使用方法。

## 固定基线 {#pinned-baseline}

| 文件 | 内容 |
| --- | --- |
| [upstream.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream.json) | 固定版本：提交 `d7c41ef` 时的 [openai-python](https://github.com/openai/openai-python/tree/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents) 3.13.0；资源位于 `beta/agents` 下；Beta 标头为 `agents=v1` |
| [upstream/openapi.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream/openapi.json) | 未修改的官方 OpenAPI 3.1，固定提交为 `046a2a0f325bf11f97966f2729219f27281ba71e`，发布于 2026-09-10。其中 58 个 Agents、Vaults、Files 和 Skills 操作与固定 SDK 的路由集合一致。`upstream.json` 记录 SHA-256 校验和 |
| [upstream-routes.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream-routes.json) | 从官方 schema 生成的标准化方法和路径清单 |
| [openapi.yaml](../openapi.yaml) | 官方公共契约，并在 Agent 和 Session 请求及响应对象上加入 Core 的 `x_agents_core` 扩展 |
| [go-bindings.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/go-bindings.json) | Go 名称、字段表示、编码顺序和存储投影；不定义官方字段集合、枚举或约束 |

运行 `make openapi` 重新生成公共 Go 类型、Agent 请求结构、路由清单和三个 OpenAPI 文档。`scripts/generate-public-api.py` 读取仓库内经过校验和验证的官方源文件，无需网络。它选择 Agents、Vaults、Files 和 Skills，并跟随 schema 引用，保留联合类型、可空性、必填字段和约束。`services/core/internal/api/official_shapes.gen.go` 中的请求结构投影 `CreateAgentParams`、`UpdateAgentParams` 和 `SessionAgentConfigParam`；Core 在读取 Agent 配置前先按它们检查请求体。Core 在 `v1/` 中的扩展类型继续由 Go 定义，在生成时加入公共 schema。内部 `/core/v1` 和 `/api/v1` 文档由处理函数注解生成。`make check-openapi` 检查生成结果是否最新并测试生成器；`make check-go` 也会运行此检查。

公共契约是官方 API 加上 Core 扩展。标准字段生成到 `v1/official.gen.go`；`go-bindings.json` 只列出 Core 使用的类型，仅在已有存储或自定义 JSON 编码需要时覆盖 Go 表示或字段顺序。未覆盖的字段遵循官方 schema，相同结构复用同一个 Go 类型。部分带判别字段的联合类型也从 schema 生成 JSON 序列化代码，保留每个分支必需的可空字段。其他联合类型序列化、Core 的本地限制、执行准入和状态转换仍由实现代码负责。契约测试验证公共 schema 保留官方定义、扩展位于 `x_agents_core` 中，且所有文档与注册路由一致。官方客户端和原始 HTTP 测试验证行为。生成 schema 不代表某个尚未实现的功能已经得到验证；下方缺口仍然适用。升级上游时，在比对和兼容性测试后一起更新 OpenAPI 和 SDK 固定版本。

官方源文件与已有服务存在以下已记录的差异：Agents 鉴权错误的 `code` 可以为 null；Files 空页的 `first_id` 和 `last_id` 为 null；File 资源的 `expires_at` 和 `status_details` 可以为 null。源文件将这些字段声明为非空。官方客户端响应验证器只对这些指定字段允许 null，其余部分按 OpenAPI 3.1 响应 schema 验证。源文件中的 Files 和 Skills 操作未声明错误响应，因此这些错误体使用上游共享的 `ErrorResponse` schema。[传输语义](wire-semantics.md)和原始 HTTP 测试验证服务行为；发布的 schema 保留官方定义。

Go 输入投影排除 `packages.system`，保留下方记录的明确拒绝行为。生成过程不会启用尚未支持的操作，也不会改变已存储安装配置的验证。


各项状态的证据必须来自固定版本的官方 SDK，以及针对运行中服务发出的原始 HTTP 请求，正如 [CONTRIBUTING](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#compatibility-evidence) 所要求。

## 按资源划分的覆盖情况 {#coverage-by-resource}

**已实现**表示每个操作都支持固定版本中的数据形态；仍然存在的限制列于 [known gaps](#known-gaps)。**部分实现**则说明缺少哪些内容。

| 资源 | 操作 | 状态 | 契约 |
| --- | --- | --- | --- |
| Agents | create, retrieve, update, list, delete | 已实现。所有固定版本设置都会保存；Session 准入只会用到其中一个子集 | [Agents](wire-semantics.md#agents) |
| Sessions | create (JSON 或流式), retrieve, update, list, delete | 已实现。更新仅接受 `metadata`；删除要求 Session 处于空闲或失败状态 | [Sessions](wire-semantics.md#sessions)、[creation streaming](sessions-events.md#creation-streaming) |
| Session events | create, stream | 部分实现：支持包含文本和内嵌图像的消息、取消和函数结果；流仅支持实时模式 | [Sessions, events and history](sessions-events.md)、[message content](message-content.md) |
| Turns | retrieve, list | 已实现；Session Turn 路由仅承载根级 Turns | [Turns and Items](sessions-events.md#turns-and-items) |
| Items | list | 部分实现：支持消息、命令、MCP 调用、functions、web search、reasoning 和 Subagent 协调 Items；其他原生变体不会被投影 | [Turns and Items](sessions-events.md#turns-and-items) |
| Artifacts | retrieve, list, delete, content | 已实现 | [Environment files and Artifacts](environment-files.md) |
| Subagents | retrieve, list; Items; Turns retrieve and list; Turn Items | 部分实现：只读子级工作；不支持实时子级进度或可选原生操作 | [Subagents](subagents.md) |
| Environments | retrieve | 已实现 | [Environments](environments.md) |
| Environment files | create, list | 已实现；列表不会递归 | [Environment files and Artifacts](environment-files.md) |
| Environment Templates | create, retrieve, update, list, delete | 已实现；执行限制列于 [known gaps](#known-gaps) | [Environment Templates](environments.md#templates) |
| Vaults | create, retrieve, list, delete | 已实现；没有归档操作 | [Vaults and Credentials](vaults.md) |
| Vault Credentials | create, retrieve, update, list, delete | 已支持 `static_bearer` 和 `mcp_oauth` | [Vaults and Credentials](vaults.md) |
| Files | create, retrieve, list, delete, content | 已支持 `purpose=user_data`；内容下载会被拒绝 | [Files and Skills](source-files.md) |
| Skills and Skill versions | create, retrieve, update, list, delete, content | 已实现 | [Files and Skills](source-files.md) |

每个 Harness 只在 `internal/harnessconfig/<kind>` 中声明一次自己的支持范围（[声明支持](harness-onboarding.md#declare-support)）。[Core wire behavior](wire-semantics.md) 包含适用于各项资源的通用规则：请求、错误和列表。

Core 自身字段位于 `x_agents_core` 中（[Core extensions](../../../docs/zh/api/public-agent-api.md#core-extensions-x-agents-core)）。Core 管理 API（`/core/v1`）和机器 API（`/api/v1`）不属于 Agents API。

## 与 OpenAI 的差异 {#differences-from-openai}

以下每项都是 Core 有意采用或原生提供的行为，而官方服务的行为有所不同。链接中的规则规定了确切行为。

**请求和错误**（[Core wire behavior](wire-semantics.md)）

- Core 不会发送 `OpenAI-Organization` 或 `OpenAI-Project` 响应标头。
- 对事件流、内容下载和 Environment files 列表执行 `HEAD` 会返回 405。
- JSON 数组请求体会被拒绝；官方服务会将 `[]` 读取为 `{}`。
- 存储的字符串中含有 U+0000 时会返回 400；官方服务会存储该字符。
- 未找到消息从不会指明具体资源；Core 会给完整的元数据键加引号，而官方消息会将其缩略。
- UUID 标识符也可按其他拼写形式解析，例如大写形式或带花括号的形式。
- 对于重复的查询键，Files 路由会保留本地 `unsupported_parameter` 代码。

**列表**（[lists](wire-semantics.md#lists)）

- 将已删除的 Agent 或 Session 用作游标时会返回 404；官方服务仍可从该游标继续分页。
- 使用来自其他 Session 的 Turn 游标、与 Vault ID 相等的 Credential 游标，或不是 Skill ID 的 Skills 游标时，都会返回 404。
- Vault 和 Credential 列表会按照固定版本 SDK 的描述钳制负数 `limit`；官方服务返回 400。

**Agents 和 Sessions**（[Agents](wire-semantics.md#agents)、[Sessions](wire-semantics.md#sessions)）

- 使用相同 `Idempotency-Key` 重复创建 Session 会返回原 Session；官方服务会创建一个新的 Session。
- 未指定程序化工具调用时，会保留 Harness 的原生行为；官方默认值为启用。
- 未指定推理强度时会保持为 null，而不是采用模型的默认值。
- Session 的 `agent.tools` 会省略 `tool_search` 声明。
- 在 events 202 之后立即删除 Session 会返回 409，因为 Core 会在同一事务中准入该 Turn；官方服务返回 200。

**输入、事件和历史**（[Sessions, events and history](sessions-events.md)、[message content](message-content.md)）

- 提交给 `none` Session 的输入会同步完成准入；Core 不会模拟官方异步准入窗口。
- 用于恢复等待中 Turn 的函数结果会发出 `turn.in_progress`；取消正在等待函数结果的 Turn 会发出临时 `agent.session.in_progress`。
- 在 Turn 中途接入流时，不会发送补发 Item 快照。
- Items 列表会包含进行中和未完成的输出 Items，并保留失败函数结果中已提交的 `output`。
- 错误消息会省略官方消息包含的 call 和 executor ID。
- 与其他文本并存的空文本部分可被接受并存储。
- 空输入会返回 400 `invalid_request`，附带通用消息和值为 null 的 param；官方响应为 `invalid_request_error`，param 为 `input`。
- 一旦所有根级 Turn 均已进入终态，Session 用量即可用；官方读取会滞后数秒。

**Files、Skills、Environment files 和 Artifacts**（[Files and Skills](source-files.md)、[Environment files and Artifacts](environment-files.md)）

- 文件上传上限为 512 MiB；官方上限为 512 MB。Files 列表默认最多返回 10,000 个 Files，`purpose` 过滤值不是 `user_data` 时会返回空页面。
- Skill 版本号绝不复用，并且针对某个 Skill 的上传和删除会串行执行。
- Environment files 可用于 `self_hosted` Environments，而官方服务会拒绝。
- 在已有常规文件上创建 Environment file 会返回 "must not traverse symlinks or overwrite existing files" 消息。若父级符号链接仍位于工作区内，则会跟随该链接；若父级逸出工作区或父级本身是常规文件，则会返回通用 400；官方服务会拒绝父级符号链接。
- Artifact ID 均为 UUID。

**Vaults 和 Credentials**（[Vaults and Credentials](vaults.md)）

- Vault 和 Credential 的状态仅在内部保存，默认值为 `active`；由于没有归档操作，未带过滤器的列表会同时包含两种状态。
- `vault_ids` 中包含未知或属于其他账户的 Vault 时会返回 404 "Resource not found."；官方消息会指出该 ID。
- 更新时显式为 OAuth `access_token`、`refresh` 或 `token_endpoint_auth` 指定 `null`，会保留已存储的值。
- 静态令牌要成功运行，必须是 RFC 6750 `b64token`；其他已存储令牌会在派发时失败。
- Vault 元数据上限为 64 KiB，且没有键对或长度限制；名称去除首尾空白后为 1–256 字节。

## 已知缺口 {#known-gaps}

**配置和工具**

- 显式指定推理强度或摘要、使用 `auto` 之外的服务层级、启用 `web_search` 或启用程序化工具调用，这些设置都会被保存，但在 Session 准入时会被拒绝。
- 各 Harness 的支持差异以其[声明](harness-onboarding.md#declare-support)为准。Codex 不支持结构化输出或 `tool_search`。Claude Code 不接受仅含空白的文本，只支持 `medium` 详细程度，函数结果图像只能内联且只能出现在成功结果中；它拒绝结构化输出与子智能体、MCP、已安装能力或 `tool_search` 同时使用，也拒绝 `tool_search` 与 MCP 或已安装能力同时使用。MiniMax Code 不提供公共 functions，没有服务源 MCP，不支持图像输入、仅含空白的文本和必需 MCP，只支持 `medium` 详细程度，且只接受值为 null 的 `allowed_tools`。每个 Harness 都保留一个 MCP `server_label`：Codex 保留 `codex_apps`，Claude Code 保留 `functions`，MiniMax Code 保留 `oac_workspace`。Claude Code 还要求标签匹配 `^[a-zA-Z0-9_-]+$`，`allowed_tools` 中的名称匹配 `^[a-zA-Z0-9_.-]+$`。
- 由模型推导出的推理默认值不会被解析确定。
- MCP 工具仅支持 `http` 传输，`stdio` 会被拒绝，Session MCP 传输中的内联 `authorization` 也会被拒绝（[HTTP MCP](execution-tools.md#http-mcp)）。

**执行和历史**

- 流不会发出 reasoning-summary 事件、Environment 的 `pending` 或 `ready` 事件，也不会覆盖固定版本中的所有临时 tool-output 变体。
- 除 [Turns and Items](sessions-events.md#turns-and-items) 中列出的变体外，其他原生 Item 变体不会被投影，而且 Items 无法修改。
- 如果取消导致函数结果无法应用，该结果将永远不会作为 Item 出现。
- 固定版本的 Codex 可能会丢失在订阅其流之前发出的命令输出。
- 在[凭据网关](./model-execution.md#credential-gateway)之后，固定版本的 Codex 在本地压缩历史，从不调用 `/responses/compact`。
- Claude Code 和 MiniMax Code 都不报告公共用量。
- 对于原生副作用，Core 不提供崩溃安全或恰好一次保证；已认领的工作若不重放，会在重启后失败。
- 消息图像必须是内嵌的 PNG 或 JPEG data URI；远程 URL、`file_id` 和 `detail` 会被拒绝。

**Environments 和 Templates**

- Runtime 不会实施 `disabled` 或 `restricted` 网络，因此需要这些网络的 Session 会被拒绝（[restricted network policy](environments.md#restricted-network)）。
- `packages.system` 会被拒绝；系统软件包必须预先安装。

**Files 和 Environment files**

- Files 仅接受 `purpose=user_data`；不支持其他 `purpose` 值、`expires_after` 和 Uploads API。
- 结果不确定的 Environment file 写入不会自动重试或恢复；它会阻止后续写入以及向 Session 发送消息。

**Vaults 和 Credentials**

- 没有归档生命周期、存储密钥轮换或重新加密。
- OAuth 刷新仅在派发时执行：提供商返回 401 时不会刷新，不会在 Turn 中途替换令牌，也不会撤回已发送给 Runtime 的令牌。
- 发送到所选 Credential 已被删除的 Session 的输入会先通过准入，随后在派发时失败。
- 创建 Credential 时不接受 `Idempotency-Key`。

**Sessions**

- 删除 Session 不会从物理存储中清除已保存的历史记录。
- 对于 `self_hosted`、hosted 和无输入创建的流生命周期，以及创建流重试机制，均由 Core 自行决定。

### 尚未与官方服务核实的内容 {#unverified-against-the-official-service}

- 当一个请求存在多项故障时的错误顺序，以及错误处理、默认值和载荷限制的总体一致性。
- Subagent 子级 Turns 和待处理的 Environment file 写入是否会阻止 Session 删除。
- 官方服务在待处理输入错误与未知结果目标之间的先后顺序。
- npm、initial-file 和 Skill 安装的失败原因没有官方样本。
- Codex 对文本旁的空文本部分、失败函数结果中的图像或远程引用图像的行为；Claude 对混合消息中仅含空白或空文本块的行为。
- 在 Core 返回 405 的路由上，官方服务的 `HEAD` 行为。
- 精确主机名之外的 Environment Template 主机名形式，以及 `disabled` 与域结合使用的情况。
- Files 发生变化时的 purpose 筛选和分页；官方 Skill 上传限制和错误时机。
- Environment file 列表的默认值（limit 为 20、默认路径为工作区根目录、不递归、page-token 失效）、50 MiB 的 `file_id` 复制上限，以及创建时的检查顺序。
- Artifact 对硬链接、特殊文件和链接形式的 `outputs` 目录的捕获，内容字节变化后的重新发布，以及内容标头和范围。
- Vault 和 Credential 的错误与重试语义、并发写入下的分页、删除后的可见性、依据官方规范化处理进行精确 URL 匹配、OAuth 刷新的时机和错误，以及受限密钥范围。
