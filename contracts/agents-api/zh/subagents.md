---
title: "子智能体"
source: contracts/agents-api/subagents.md
source_hash: 8fed8b73a8403d2ccf80354b2a981f11240eba3c10d5a0257206a39d048b92a7
---

启用 `multi_agent.enabled` 后，Harness 可以启动原生子智能体。Core 通过锁定版本的 SDK（见 [`upstream.json`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream.json)）中的六项 Subagent 读取操作公开它们，并根据适配器观察结果记录它们。当 `multi_agent.enabled=false` 时，Runtime 会移除原生子智能体工具。[Harness capabilities](harness-capabilities.md) 列出各 Harness 在哪些组合中支持子智能体。

## 公开读取 {#public-reads}

所有路径都位于 `/v1/agents/sessions/{session_id}` 下，并且需要与普通 Session 读取相同的 Project 身份验证和 `OpenAI-Beta: agents=v1`。

| 路径 | 结果 |
| --- | --- |
| `/subagents` | 直接子智能体、嵌套子智能体和已关闭的子智能体 |
| `/subagents/{subagent_id}` | 单个子智能体 |
| `/subagents/{subagent_id}/items` | 仅该子智能体自身的 Items |
| `/subagents/{subagent_id}/turns` | 仅该子智能体自身的 Turns |
| `/subagents/{subagent_id}/turns/{turn_id}` | 一个所属 Turn |
| `/subagents/{subagent_id}/turns/{turn_id}/items` | 该 Turn 中仅属于该子智能体的 Items |

列表使用 `after`、`limit`（默认 20）和 `order`（默认 `desc`），并返回 `object: "list"`、`data`、`first_id`、`last_id`（空页面时为 null）和 `has_more`。对于 1–100 范围之外的 `limit`，Subagent 和 Subagent Turn 列表会使用 `limit must be between 1 and 100` 拒绝请求；与 Session Items 一样，两个 Item 列表会将 0 视为 1，并将更大的值视为 100。游标必须属于所请求的 Project、Session、子智能体以及可选的 Turn。

## 子智能体可见性 {#subagent-visibility}

子级工作仅出现在子智能体路由上。

- Session 的 `turns.list` 和 `turns.retrieve` 仅返回根级 Turns。将子级 Turn ID 作为路径参数或列表游标使用时，会返回与不存在的 Turn 相同的 404；Core 的 404 消息与官方消息不同。
- Session 事件流和创建流仅传输根级工作：子级 Turns 及其 Items 不会发布任何 `agent.session.turn.*` 或 Item 内容事件。`agent.session.subagent.*` 事件和根级协调 Items 仍会保留。创建流仍会在根级收敛到 idle 状态时结束。
- 子级 Turn 的 `agent_id` 是 Session 的 Agent ID（即直接子级的 `parent_agent_id`，也是 `create_subagent_call` 的 `agent_id`）；`subagent_id` 标识该子级。嵌套子级遵循相同规则。根级 Turns 带有 `subagent_id: null`。
- Session Items 始终归根级所有；继承的原生父级对话记录不属于子级工作。Session 用量仅汇总根级 Turns。

Active 状态包含 idle 状态。成功关闭会记录原生时间；成功重新打开会保留身份和 `opened_at`、清除 `closed_at`，并且只发出一次 `active`。恢复处于 active 状态的子智能体不会执行任何操作。Turn 完成、中断和进程释放绝不会关闭子智能体。未知的 token 度量值保持为 null。

## 适配器契约 {#adapter-contract}

适配器通过 `internal/agentdaemon/proto/subagents.go`，利用现有的已认证 Run 和执行日志报告子智能体事实。不存在单独的传输机制、调度器或模型与工具循环。

| 事实 | 适配器义务 |
| --- | --- |
| 身份 | 证明原生 ID、初始父级和创建时间；先发布父级 |
| 生命周期影响 | 证明成功的关闭或重新打开及其实际发生时间；在读取历史记录时保持该影响的标识稳定 |
| 子级 Turn | 提供由原生端拥有的 ID、状态和来源时间戳，并且仅在已知时提供 Usage；如果缺少原生取消时间戳，则必须提供持久化的已确认操作回执 |
| 子级 Item | 使用中立词汇提供有序且完整的消息或工具快照 |
| 协调 | 转换原生操作以及操作者和接收者身份，但不得在 Core 中放入原生工具名称 |

Core 根据已授权的 Session 绑定关系分配合公开 ID 和所有权。身份、生命周期、子级历史和实时事件投影在 Session 锁和执行租约下原子提交。重复观察会保留其 ID，且不会重复生成生命周期事件；检测到冲突效果时操作失败。对不存在子级的失败协调请求会保留其不透明的请求目标，并且不会创建子智能体。

子级 Turns 由原生写入方负责，因此它们与 Core 的工作队列分开存储，绝不会成为另一项排队执行。Session Turn 读取直接查询根级 Turns。公开 GET 读取持久化资源；它们绝不会启动原生进程或重放执行。

适配器会在子级工作收敛前冻结根级输出，在有限子级工作完成期间保持原生所有者和读取器存活，并在子级终态 Turn 快照和 Run 完成之前交付子级 Items。取消操作使用相同所有者，并在释放前确保子级写入已经完成。失败的观察或不确定的原生操作结果绝不会转化为成功的空历史；父级输出、任务完成、观察时间或空列表均不能替代缺失的事实。

## 原生配置 {#native-profiles}

[Harness capabilities](harness-capabilities.md#tools) 列出被拒绝的工具组合。

**Codex.** 适配器会启用原生 `multi_agent` 特性，将嵌套深度设为 64，并把并发限制映射到 `agents.max_threads`。它会禁用原生钩子、插件、代码模式和 `multi_agent_v2`；如果原生钩子列表非空，或托管配置要求强制启用冲突特性，则拒绝启动。关闭和重新打开的事实来自直接工具输出，并与同一次调用的持久化完成记录相关联，因此需要原生持久化回执。原生 Turn 时间具有秒级精度。根级 Turn 完成后，子级文件工作可以在同一所有者下完成。超过调用方截止时间后，取消操作仍会在同一所有者下继续；后续调用可以确认已经收敛，而无需重复执行原生中断。

**Claude SDK.** 锁定版本 SDK 的原生 Agent 和 SendMessage 调用会运行唯一的子级类型 `oac_worker`；该类型继承模型，并可使用工作区中的 Bash、Agent 和 SendMessage；bridge 的 `subagent_resources` 特性控制是否启用它。子级使用原生 Bash，权限继承自父级的启动用户。私有子级记录用于确定父子关系、首次自身输入时间及后续自身 Turns；继承的父级上下文会被排除。查询所有者会在启动前接纳子级，并将子级历史保留至工作收敛。已确认的取消操作会写入不可变的操作回执，因为原生中止可能不会留下终态记录。不存在关闭操作：已完成或已取消的子级保持 active 状态。向运行中的子级发送消息、使用后台工作、采用其他子级配置以及按次覆盖模型都会被拒绝。[Claude SDK adapter](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/packages/claude-sdk-adapter/README.md#subagents) 记录了详细信息。

**MiniMax Code.** 原生 ACP 委派（`task`、`task_append`、`task_stop`）会创建子级。Session 私有 SQLite 记录提供子级身份、已接受的输入、终态时间和自身消息。原生 task-create 事务会在启动前强制执行并发限制，并且原生准备过程必须在任何模型输入前确认该限制和受限工具配置。不存在关闭或重新打开操作，原生工作器也不会委派嵌套工作。

Claude 和 MiniMax 在子级工作收敛时发布经验证的子级历史，而不是持续发布子级进度。向子级传播根级完成状态、完整的子级增量排序以及跨根级 Turns 的无界后台工作均尚未得到支持。
