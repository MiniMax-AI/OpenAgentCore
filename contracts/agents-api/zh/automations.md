---
title: "Automations"
source: contracts/agents-api/automations.md
source_hash: 3408eb6d69fde360c3a22accfc96deab62e4455a27ca31c9ac0cc725061c530c
---

Automation 保存一个计划和一个目标。计划触发时，Core 投递目标：创建新 Session，或向已有 Session 发送指令。锁定版本 SDK 的全部十个操作都在 `/v1/agents/automations` 下实现，并与其他 Beta 路由一样需要项目身份验证和 `OpenAI-Beta: agents=v1`。Automation 属于其密钥所在的项目；不存在的 ID 与其他项目的 ID 都返回 404。

| 操作 | 结果 |
| --- | --- |
| `POST /agents/automations` | 201，一个 active 的 Automation；不会投递任何内容 |
| `GET /agents/automations` | 一页结果；`session_id` 只保留目标为该 Session 的 Automation。列表把 `limit` 截断到 1–100，无法解析的 `after` 返回 404 |
| `GET /agents/automations/{automation_id}` | 该 Automation |
| `POST /agents/automations/{automation_id}` | 替换请求体中的字段；`target` 和 `metadata` 整体替换 |
| `DELETE /agents/automations/{automation_id}` | `agent.automation.deleted`；目标 Session 和它创建的 Session 保留 |
| `POST …/pause`、`POST …/resume` | 该 Automation |
| `POST …/invocations` | 200，一个排队中的手动 invocation |
| `GET …/invocations`、`GET …/invocations/{invocation_id}` | 一页结果，或单个 invocation |

## 资源 {#resource}

`type` 为 `periodic` 或 `once`。

- `periodic` 带有 `expression` 和 `timezone`。状态为 `active` 或 `paused`。
- `once` 带有 `run_at`，即以秒为单位的 Unix 时间戳，最大为 253402300799。状态为 `active`、`paused` 或 `completed`；completed 表示计划已结束，而不是 agent 的工作已结束。
- `name` 为 1–256 个字符，`expression` 为 1–256，`timezone` 为 1–128。`metadata` 遵循[校验错误](wire-semantics.md#validation-errors)中的规则。
- `created_by` 始终为 `{"type": "developer"}`：不支持 `automations` agent 工具，因此 agent 无法创建 Automation（[Session 准入](wire-semantics.md#session-admission)）。

### 计划 {#schedules}

`expression` 是五字段 crontab：分钟、小时、日、月和星期。每个字段为 `*`，或由 `n`、`a-b`、`*/s` 和 `a-b/s` 组成的逗号列表；月和星期还接受三字母名称，星期 7 表示周日。日和星期两个字段都不以 `*` 开头时，满足任一个即匹配该日。`@daily` 这类描述符、秒字段、`Local` 和空时区会被拒绝，未来五年内没有触发时间的表达式也会被拒绝。

`timezone` 是 IANA 名称，先由系统 zoneinfo（或 `ZONEINFO` 指向的目录）解析，找不到时再用 Core 内嵌的 tzdata。分钟和小时字段都不以 `*` 开头的任务是定点任务。夏令时切换时：

- 向前拨：落在空档里的定点任务在空档结束后的第一个时刻运行。分钟或小时字段为 `*` 的任务只在存在的墙上时间运行。
- 向后拨：落在重复小时里的定点任务只在第一个时刻运行一次。分钟或小时字段为 `*` 的任务在两遍中都运行。

如果 Core 停机期间有多次触发到期，它只记录最近一次。

## 目标 {#targets}

`target.type` 为 `new_session` 或 `existing_session`。更新保持类型不变；另一种类型返回 400，param 为 `target.type`。

`new_session` 采用 Session 创建请求体的字段，外加 `type`：`environment` 和 `input`（用户消息数组）必填，`agent` 与 `agent_id` 必须恰好提供其中一个，`metadata` 和 `vault_ids` 可选。创建和更新会执行 Session 创建在创建任何东西之前执行的检查：请求形状、Environment Template、已保存的 Agent、模型及其 provider、Vault 绑定，以及 Harness 对该配置的支持，因此不受支持的工具（如 `automations`）在创建时返回 400。拒绝规则与 Session 创建相同，param 加上前缀 `target.`：`container_size`、`desktop.enabled`、`packages.system` 和 `blocked_domains` 会被拒绝。`environment.environment_id` 指向一个[独立 Environment](environments.md#standalone-environments)：创建和更新只检查它存在于项目中（404），每次投递都会挂载它，因此在另一个 Session 占用它时投递会因挂载冲突而失败。部署就绪、准入和租约在每次投递时检查。此外：

- 内联 `agent` 与 Session 创建一致：除非适用部署默认模型，否则 `model` 必填。
- 目标中任何位置的 `x_agents_core` 都会被拒绝。
- 保存后的资源展示的内联 agent 不含 `id` 和 `name`，并去掉 MCP headers。`environment` 展示请求提供的成员，其余为 `null`，表示继承；`self_hosted` 环境总会列出 `capability_directories`。`env` 和 `setup_commands` 从不展示。

`existing_session` 采用 `session_id` 和 `instructions`（1 到 1,048,576 个字符）。创建或更新 Automation 时，该 Session 必须存在，Core 保存其规范 ID，无论请求使用哪种形式；之后被删除会让每次投递以输入路径的 404 失败。Core 把 `instructions` 作为一条带有一个 `input_text` 部分的用户消息发送，与 `agent.session.input.message` 完全一样。

创建和更新只保存目标；目标中的引用，如 Skill、File 或 Agent，在每次 invocation 时解析。排队中的 invocation 投递的是它被记录或触发时的目标，因此更新只作用于之后的 invocation。

目标请求可能包含读取时不会返回的值，因此 Core 用凭据密钥将其密封存储，并绑定到项目和 Automation。

## Invocations {#invocations}

每次计划触发和每次手动 invocation 都产生一条 invocation 记录。`source` 为 `manual`，或为 `automation` 并带有触发时的 Automation。

| 状态 | 含义 |
| --- | --- |
| `queued` | 已记录，尚未投递 |
| `completed` | 已投递；`session_id` 是创建的 Session，或目标 Session |
| `failed` | 投递被永久拒绝；`error` 包含相同请求会返回的 `code` 和 `message` |
| `skipped` | `skip_reason` 为 `pending_delivery`：该 Automation 已有排队中的 invocation |

Core 每秒记录到期的触发并投递排队中的 invocation，最多同时运行 16 个投递，每个最长 5 s。投递使用 `POST /v1/agents/sessions` 或 `POST /v1/agents/sessions/{id}/events` 的代码，并以 invocation ID 作为 `Idempotency-Key`，因此崩溃后的重试会找到已经创建的 Session 或输入。投递得到 500 及以上的状态、遇到无法接收输入的 Turn 或超时，Core 会重试；4xx 状态会让 invocation 失败。同一个 Automation 一次只投递一个 invocation，先到先投递，最久未尝试的 Automation 先投递，因此卡住的 invocation 不会占满所有名额。已保存的目标无法打开的 invocation 以 `internal_error` 失败。invocation 保留到 Automation 被删除。

`once` Automation 在其计划 invocation 到达 `completed`、`failed` 或 `skipped` 时完成。手动 invocation 从不改变计划。

## 状态变化 {#state-changes}

| 请求 | 规则 |
| --- | --- |
| 暂停 | 停止计划投递，保留排队中的计划 invocation 直到恢复，不停止任何 Session。手动 invocation 仍会投递。暂停已暂停的 Automation 会原样返回 |
| 恢复 | periodic Automation 从现在起取下一次触发。`once` Automation 在其计划 invocation 待处理或 `run_at` 在未来时恢复；否则返回 409 `conflict_error`。恢复已激活的 Automation 会原样返回 |
| 暂停或恢复已完成的 Automation | 409 `conflict_error` |
| 更新 | `expression` 和 `timezone` 只适用于 periodic Automation，`run_at` 只适用于 `once`；另一类型的字段返回 400 并指明该字段。过去的 `run_at` 返回 400。计划 invocation 产生后修改 `run_at` 返回 409 |
| 手动 invoke | 任何状态下都允许 |

官方来源未说明、由 Core 自行选择的行为列在[清单](index.md#unverified-against-the-official-service)中。创建 invocation 会写入 `automation_invocation` 上的 `create` 审计条目；计划记录不写审计（[写入来源](admin-api.md#write-provenance)）。
