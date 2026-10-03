---
title: "根 Session 与 Turn 诊断"
source: contracts/agents-api/session-diagnostics.md
source_hash: d84a678de2a21f66e2090afff19aea5cff5d3f11d5c32306ebd4387b015cb416
---

这些只读路由要求 Core 密钥。Project ID 选择目标 Project，不用于认证。两者都返回 `Cache-Control: no-store`：

- `GET /core/v1/projects/{project_id}/sessions/{session_id}/diagnostics`
- `GET /core/v1/projects/{project_id}/sessions/{session_id}/turns/{turn_id}/diagnostics`

不存在、格式错误、属于其他范围或已删除的资源遵循 Session 与 Turn 的未找到规则。Subagent Turn 不是根 Turn，返回 404。读取不会联系执行器、配置 Environment、修复历史或改变执行。

## Session 快照 {#session-snapshot}

对象为 `core.session_diagnostics`，包含 `session_id`、官方 Session `status` 和可空的 `failure`。每次 Session 或 Turn 读取使用一个可重复读数据库快照和公开状态投影，时间预算为五秒，调用方更早的截止时间会缩短该预算。读取后关闭事务，取消时也一样。除非投影为 `failed`，否则 `failure` 为 null。其字段为：

| 字段 | 值 |
| --- | --- |
| `source` | `turn`、`environment` 或 `environment_input` |
| `turn_id` | 仅在 `source: turn` 时存在 |
| `code` | [诊断目录](core-errors.md#diagnostic-failure-categories)中的固定分类 |
| `params` | 包含固定安全值的对象；无参数的分类使用 `{}` |
| `failed_at` | RFC 3339 时间戳；时间未知时为 null |

托管配置失败优先于输入活动，输入活动优先于最新根 Turn；公开 Session 响应使用相同优先级。不会包含私有结果文本、原生消息、提供方响应体、命令文本、路径或凭据。未知结果代码变为 `internal_error`；不使用前缀匹配或原因文本解析。

配置参数来自已确认的结构化回执，在结算 Environment 及其输入并记录事件的失败事务中持久化。`step` 为 `setup`、`python`、`npm`、`system`、`file`、`skill` 或 null。`index` 仅对 setup 为 JSON 安全的非负整数，其他情况为 null。`exit_code` 对脚本步骤为 1 至 255，其他情况为 null。未记录的详情保持 null。这些私有字段不会改变公开失败原因或 SSE。

## Turn 快照与 Item 回执时间 {#turn-snapshot-and-item-receipt-timing}

对象为 `core.turn_diagnostics`，包含 `session_id`、`turn_id`、官方根 Turn `status`、可空的 `failure`、`items` 和 `items_truncated`。Turn 失败包含 `code`、`params` 和可空的 `failed_at`，不包含来源或 Turn ID。只有失败的 Turn 才有失败详情；已取消或已完成的 Turn 不会继承私有结果中的分类。

原生分类仅适用于结果为 `engine_failed` 的失败 Turn，来自[原生失败分类](../../../docs/zh/runtime-protocol.md#native-failure-classification)中定义的有限结果元数据。`connection_failed` 的 params 包含 `http_status`，值为 100 至 599 或 null；其他原生分类使用空 params。

`items` 最多包含 1000 个根 Item，与公开 Item 列表一样，按 `(created_at, position, id)` 升序排列。存储最多读取 1001 行以检测截断。每项包含 `item_id`、`started_at`、可空的 `completed_at` 和可空整数 `observed_duration_ms`。

- `started_at` 是 Core 首次持久化该 Item 输入或事件回执的时间。
- `completed_at` 是首次终态输入或事件回执的时间。首次观察即为终态的 Item 在同一回执处结算，观察时长为零。
- 根 Turn 终止时仍在进行的 Item，使用 Session 锁和终态投影之后采样的一次数据库 `clock_timestamp()` 结算，同一事务中的所有此类 Item 共享该值。既不使用事务开始时的 `now()`，也不使用原生 Turn 完成时间。
- 重复终态投影保留首次结算。已存储但没有结算记录的终态 Item 保持 null；Core 不回填或估算。
- 当两个回执时间均已知时，`observed_duration_ms` 为整数毫秒差，否则为 null。它不是原生执行时间：日志批处理（事件日志约每 100 ms 刷新）、传输、持久化和数据库时钟行为都会影响它。值不会被钳制。

成功 Turn 的公开完成时间可能来自原生执行器，不会因这些读取而改变。

## 客户端 {#client}

`packages/agents-client` 中的 `AdminClient.retrieveSessionDiagnostics(projectId, sessionId, options)` 和 `AdminClient.retrieveTurnDiagnostics(projectId, sessionId, turnId, options)` 支持请求取消，并验证范围、分类、可空性和安全参数值。
