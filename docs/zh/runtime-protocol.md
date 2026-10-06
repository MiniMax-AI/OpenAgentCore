---
title: "Core–Runtime 协议"
source: docs/runtime-protocol.md
source_hash: cbc3c6419e2d4991df82d5bbfe3b556d35cccb57d1e4e7437facca7487caabcc
---

此协议在 Runtime daemon 获取机器凭据后连接 Core 与 daemon，定义 daemon 连接上消息的含义和顺序。wire 类型、限制和验证器仅在 [`internal/agentdaemon/proto`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/internal/agentdaemon/proto) 中定义一次；Core 的 [gateway](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/runtimegateway) 与参考 Runtime 的 [dispatcher](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/daemon/internal/dispatch) 都使用它们，因此无需同步第二套 payload schema。签发凭据和打开连接的 HTTP 路由见[机器连接 API](../../contracts/agents-api/zh/machine-api.md)。

托管和自托管 Runtime 使用同一协议。Harness 通过 [Harness adapter 契约](../../contracts/agents-api/zh/harness-onboarding.md)接入，该契约负责 Runtime registry 后的 Executor 和 Turn 生命周期义务。

## 所有权与连接 {#ownership-and-connection}

Core 拥有持久化的 Session、Turn、input 和 Environment 记录、调度与协调。Runtime 拥有原生 Executor、活动 Turn、传输状态和清理责任，直到完成结算。Sandbox Provider 拥有执行位置与外围计算资源。释放执行准入或关闭 Executor 不会删除、暂停或回收沙箱。daemon 不是隔离边界；参见 [Runtime 与外层隔离](concepts.md#runtime-and-outer-isolation)。

Runtime 按以下顺序连接：

1. 获取 daemon 凭据和 device ID。[机器连接 API](../../contracts/agents-api/zh/machine-api.md#credentials) 列出凭据类型；Project API key 和 Core key 都不是 Runtime 凭据。
2. 调用 `POST /api/v1/agent-daemon/bootstrap`，通过 Bearer header 提供凭据，并提供 device ID。使用返回的连接 URL。
3. 连接 `/api/v1/agent-daemon/ws` 的 WebSocket，传入 `device_id`、`version` 查询参数和 Bearer header。凭据不得出现在 URL、payload 日志或 trace 中。
4. 立即发送 heartbeat，之后按 bootstrap 返回的间隔发送。每个 heartbeat 声明 `supported_agent_kinds`、其可用性和[能力](#capability-declarations)。第一个 heartbeat 之前能力未知；heartbeat 中缺失的 kind 视为未声明。两者都不允许推断。
5. 交换有序 JSON [envelope](#envelope-and-identity)。Heartbeat 仅证明存活，不证明执行进度，也不充当此前消息的回执。

wire 版本为 [`proto.Version`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/version.go)，独立于 heartbeat 报告的 Runtime 构建版本。Core 仅接受精确匹配，包括 patch 部分。不匹配时，在任何 dispatch 前返回 HTTP 426 `incompatible_version`；daemon 将其视为永久错误并停止重连。应一起部署版本匹配的两端。

每条物理连接拥有新的路由、admission handle 和传输状态。同一设备的新连接替代旧连接：Core 隔离 owner lease，移除旧 Run 和 interaction 路由，新连接不继承这些状态。有效凭据和连接不授权选择其他 Session 或 Environment 绑定。

## 能力声明 {#capability-declarations}

[`inbound.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/inbound.go) 中的 `AgentKindCapabilities` 描述一个 Runtime 与 Harness 组合，独立于 `available` 和 Core 的 engine profile。每个字段都是 `CapabilitySupport`：支持或不支持。零值表示未指定且无效，即使 Harness 不可用也如此。注册在修改 registry 前验证完整声明；不存在隐含的基础 descriptor。

wire 上每个字段都是 JSON boolean，所有字段都必须出现，包括 `false`。不完整声明编码失败。解码拒绝省略、null、无效和未知字段，以及缺失的 capability 对象。无效 heartbeat 会清空连接的 admission snapshot 并关闭 transport；这不证明原生完成或取消结果。

每个已准入的 Executor 和 Turn 保留准入时的声明。后续 heartbeat 不能给已有 owner 增加操作。可选操作在任何原生调用前检查此快照；存在 Go interface 不代表支持。已声明操作返回 `agent.ErrUnsupportedOperation` 属于契约违规，与不可用、原生调用失败或不确定写入不同。不确定操作保留回执与所有权，绝不自动重放。工作区支持包括公共 Runtime 工作区实现，因此原生 adapter 不支持的 workspace 方法不会禁用该组合。

新增字段要求每个生产声明都作出明确决定。契约测试为注册、wire 往返和持久化 boolean 投影逐一枚举字段；共享测试 fixture 单独列出字段，不为未来字段提供默认值。[Harness 接入](../../contracts/agents-api/zh/harness-onboarding.md)负责各声明的 adapter 侧规则。

声明描述 Runtime 能做什么。Core 仅在 Harness 的 engine profile 也通过资格验证时准入公开功能，并在设备选择及领取 Turn 前的最终检查中检查选定设备的声明：

| 能力 | Core 何时要求 |
| --- | --- |
| `streaming`, `steering`, `durable_turns`, `durable_input_receipts`, `preparation`, `execution_controls`, `tool_observations` | 始终要求，适用于该 Harness 上每次执行（`available` 为 true） |
| `environment_none` | Environment 类型为 `none` |
| `local_environment`, `workspace_read_preparation`, `workspace_output_export` | Environment 类型为 `openai_hosted` 或 `self_hosted` |
| `workspace_read_preparation` | 空闲 Files 目录读取需要只读 preparation |
| `native_session_recovery` | Session 已启动过 Turn，但未记录原生 Session ID |
| `web_search_control`, `text_verbosity` | Harness 的 engine profile 声明该控制 |
| `structured_output` 和 `message_items` | Agent 请求 `json_schema` 输出 |
| `subagent_observations` | `multi_agent.enabled` 为 true |
| `subagent_control` | `multi_agent.enabled` 为 false |
| `tool_search` | Agent 启用 tool search 或延迟 function 加载 |
| `programmatic_tool_calling_disable` | Agent 明确禁用 programmatic tool calling |
| `function_tools` | Agent 声明 function tool |
| `message_images`, `function_result_images` | 消息或 function result 携带图像 |
| `mcp_http_tools`, `mcp_http_required`, `mcp_http_bearer_auth` | Agent 声明 HTTP MCP server；其中一个为 `required`；其中一个选用了 Vault 凭据 |

`permissions` 控制 Runtime 内部权限决定，`workspace_authoring` 控制 daemon 的 authoring 命令。Core 对 `usage` 和 `resume` 没有准入规则。

prompt 请求（`prompt_request` 或 `execution_prepare` 的配置）携带 Core 为各 Run 设置的显式启用项：

| 字段 | Core 设置方式 |
| --- | --- |
| `execution_controls` | 始终设置：web search 为 `disabled`、解析后的 text verbosity（默认 `medium`）、明确禁用 programmatic tool calling，以及任何 `json_schema` 输出格式。原生选项名称由 adapter 负责 |
| `observe_tool_observations` | 始终设置。tool-call frame 随后携带与 engine 无关的 `observation` |
| `observe_messages` | Runtime 声明 `message_items` 时设置。文本 delta 随后携带原生 item ID，`output_message` frame 报告消息开始、完成、phase 和完成文本 |
| `observe_subagent_identities`, `disable_subagents` | 根据 Agent 的 `multi_agent.enabled` 设置 |
| `disable_execution_environment` | Environment 类型为 `none` 时设置 |
| `local_environment` | 为 `openai_hosted` 和 `self_hosted` 设置，包含精确的 Environment 绑定。请求不携带 working directory；Runtime 按自身绑定检查 `workspace_directory` |
| `strict_resume`, `require_existing_native_session` | 始终严格；需要恢复原生 Session 时设置第二项 |
| `prompt_steer` 上的 `durable_receipt` | Core 交付的每个活动输入都设置 |

未携带显式启用项的请求保留未启用时的 frame 和字段。

## Envelope 与身份 {#envelope-and-identity}

每个数据 frame 都是一个 JSON [`Envelope`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/envelope.go)：`type`、随类型变化的 `id`、类型化 `payload` 和可选 W3C `trace`。trace 仅用于诊断关联；trace 数据缺失或无效时创建本地 trace，不改变所有权。不要将 trace ID 用作 request ID。

| 身份 | 范围与含义 |
| --- | --- |
| Device ID 和连接 | 经认证的 Runtime 路由与连接所有权 |
| Session ID / Environment ID | Core 拥有的配置与工作区绑定；payload validator 要求时使用规范 UUID |
| Executor ID | Runtime 拥有的原生资源，可在配置相同的已结算 Turn 之间保留 |
| Preparation request ID | prepare、start、release 和 status 的 `Envelope.id`；与 Run 不同 |
| Admission handle | Runtime 生成的预约，仅在接受它的连接上有效 |
| Run ID | 一次执行尝试；输出、取消、活动输入和 function 的 `Envelope.id` |
| Interaction ID | `permission_request.payload.request_id` 或 `prompt_for_user_choice.payload.ask_id`；这些请求 envelope 仍携带 Run ID |
| Delivery ID / input ID / call ID | 分别标识 resolve 尝试、活动输入回执与原生 function；不能互换 |
| Transfer ID / suspension ID | 连接本地传输关联 / 持久化 suspension 尝试的 fencing |

Decision 和 permission-cancel envelope 使用 interaction ID。取消和 function-result 确认使用 Run ID。每个应用 decision 回执还要匹配 delivery ID。缺少必需关联的回复不能证明已接受。

User-choice decision 携带 `question_answers`：每个已提供回答都有明确的 `question_id` 和 `answers` 数组。ID 必须属于已发出的问题且不能重复。问题顺序和显示 header 不标识回答；省略的问题仍未回答，空数组表示明确未作答。取消携带 `cancelled: true`，不包含回答。共享验证在原生提交前拒绝其他形式。

## 消息族 {#message-families}

链接的源文件定义必需字段、验证器、限制和有限错误类别。

| Core → Runtime | Runtime → Core | 定义 |
| --- | --- | --- |
| `runtime_prepare` | `runtime_prepare_result` | [初始化与能力传输](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/runtime_prepare.go) |
| `execution_prepare`, `execution_start`, `execution_release` | `preparation_status` | [执行准入](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/preparation.go) |
| `prompt_request`, `prompt_cancel`, `device_shutdown` | `delta`, `thinking`, `output_message`, `tool_call`, `usage`, `error`, `done`, `heartbeat` | [请求](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/outbound.go)、[事件与能力](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/inbound.go) |
| `permission_decision`, `prompt_for_user_choice_decision` | `permission_request`, `permission_cancel`, `prompt_for_user_choice`, `interaction_decision_ack` | [请求](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/outbound.go)、[交互](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/inbound.go) |
| `prompt_steer` | `prompt_steer_ack` | [活动输入回执](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/steering.go) |
| `function_result` | `function_call`, `interaction_decision_ack` | [Function 调用](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/functions.go) |
| `workspace_read`, `workspace_write`, `workspace_export` | 对应的 `*_result` | [读取](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/workspace_read.go)、[写入](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/workspace_write.go)、[导出](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/workspace_export.go) |
| `environment_quiesce`, `environment_resume` | `environment_quiesced`, `environment_resumed` | [暂停 fencing](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/suspend.go) |

初始、已准备和活动输入使用同一[有序 MessageInput](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/message_input.go)。Adapter 保留消息与内容顺序，并明确拒绝不支持的内容；仅文本 transport 拒绝图像内容而不丢弃它。[消息输入契约](../../contracts/agents-api/zh/message-content.md)负责公开图像 profile、空白规则和各 Harness 的原生转换。

Usage frame 和最终 usage snapshot 都携带当前执行的累计测量，替换之前的快照；不要相加。缺失的测量表示未知，不是零。

## 准备与执行顺序 {#preparation-and-execution-order}

无论托管还是用户自有环境，每条连接都通过 `runtime_prepare` 初始化 Environment；[Environment 契约](../../contracts/agents-api/zh/environments.md#runtime-capability-preparation)负责准备内容和时机。传输文件或 archive 时，发送 `begin`，等待 `ready`，发送有序 chunk 并等待每个匹配的 `received` offset，再发送 `commit` 并等待 `completed`。初始化和终结阶段使用不含文件数据的类型化 header。使用共享 validator 验证预期结果、offset、size 和有限错误 code。每条连接允许一个 transfer。chunk 回执确认暂存字节，不确认安装；完成的 commit 确认该操作，不证明后续 Turn 已运行。

执行 Turn 分为五步：

1. 订阅 preparation status，再发送 `execution_prepare`，携带不可变 Session 配置，不包含 Run 输入。
2. `preparing` 表示 Runtime 拥有 preparation。`ready` 提供 Executor ID、admission handle、revision 和 expiry。两者都不提交用户输入。
3. 订阅 Run，再发送 `execution_start`，携带该 Executor ID、handle、Run ID 和有序输入。有效 start 将预约转移一次。`started` 确认转移给 Turn，不确认完成；输出可能与 status 竞争，因此必须事先有 subscriber。
4. 消费 Run 事件，直到原生终结结果或观测丢失。执行 error 后跟随 `done`，关闭流；之前的 error 仍属于结果的一部分。
5. start 前放弃时发送 `execution_release`。所有权转给 Run 后使用 `prompt_cancel`；释放旧 handle 不能取消后继 Turn。

preparation 预约每个 Turn 的准入，而不是新 Executor。它携带明确 Session 身份和不可变配置，不包含模型输入或 Run ID。新请求返回连接本地 handle 与所属 Executor ID；复用健康 Executor 时直接返回 `ready`，无需原生 preparation。每个 handle 的 revision 决定 status 观测顺序：忽略旧的或重复的 revision，不将 status 应用于其他 handle。典型状态转移为 `preparing → ready → starting → started`，或由 `released`、`expired`、`failed` 终止。`rejected` 控制操作携带 `operation` 和 error code，不替代 handle 的当前 revision。释放 admission 仅放弃该 admission；不关闭 Session 的空闲 Executor，也不取消后续 Turn。

preparation 和 start 在 receive loop 与 router lock 之外运行。admission 在授予五分钟后到期，重试不延长截止时间；到期不解除 Runtime 完成清理结算的义务。Runtime 分别限制活动 preparation、execution 和保留的空闲资源，关闭中或不确定资源持续计入限制，直到清理成功。明确的 `execution_prepare` 拒绝若为 `preparation_capacity`，会让排队 Turn 保持未领取，供 Worker 重试，包括清理占用容量的情况；其他错误或不确定交付都不授权重放。Runtime 最多保留 64 条 admission 记录，旧 handle 不会消耗替代项的 admission。这些记录仅属于连接，不是持久化输入重放。

Executor 空闲到期属于 Runtime 资源策略，与 Core 的活动 Turn 并发限制独立。关闭时 Runtime 关闭活动和空闲 Executor，保留关闭失败的目标，并允许稍后串行重试。普通断连会关闭失败的 transport 并保留原 router，直到 shutdown 成功；等待超时或清理失败不授权重连，进程 shutdown 继续等待，不丢弃自己拥有的原生资源。工作区操作在跨 Turn 和 Executor 关闭后仍保留绑定与结算规则。

`prompt_request` 不经过 admission handle，直接启动 Run。它不是 prepared start 失败后的回退。

## 活动输入回执 {#active-input-receipts}

Core 通过 `prompt_steer` 交付活动输入，设置 `durable_receipt: true`，每个 Run 一次交付一个输入，并等待回执后再发送下一个：

| 阶段 | 定时器 |
| --- | --- |
| 原生写入 | Runtime 将 adapter 原生写入限制为 10 秒；写入完成后停止计时 |
| `written` 确认 | Core 从交付开始最多等待 30 秒取得 `written`；否则输入结果未知 |
| 回执发送 | 每次发送回执都有独立的 5 秒预算，并感知 shutdown |
| 原生接受 | `accepted` 在 Turn 生命周期内到达，不自动重新交付 |
| Done | `done` 前，Runtime 最多等待 15 秒（写入与发送预算），等待正在处理的输入 |

`written` 和发送失败都不会推进 Core 的 input cursor。取消发出后，即使输入先变为未知，终结结果也由取消回执负责；15 秒内没有取消确认时，Core 记录 `cancel_unconfirmed`。未发出 `done` 时，取消回执携带已停止 Turn 的已确认 continuity snapshot。

## 每类确认所证明的事实 {#what-each-acknowledgement-proves}

| 观测 | 已证明事实 |
| --- | --- |
| Core gateway `Send` 返回 nil | envelope 进入本地发送队列 |
| Runtime transport `Send` 返回 nil | WebSocket 写入在本地完成 |
| Transfer `received`、活动输入 `written` | 定义的接收或写入阶段已发生；原生执行或消费未确认 |
| Preparation `preparing` / `ready` | 准备已接受 / 预约已就绪，尚未提交 Run 输入 |
| Preparation `started` | 准入已转移到指定 Turn |
| 活动输入 `accepted` | adapter 按其声明的回执语义确认原生消费 |
| `interaction_decision_ack.applied=true` | 指定操作已结算；取消还要求原生结算 |
| `done` 和之前的执行事件 | 执行流以观测到的结果完成 |

不存在适用于每个 envelope 的通用回执。发送成功不证明对端已收到、接受或完成请求。进程退出、stop signal 或本地 context 取消不证明取消完成。`done` frame 也可能关闭已结算的取消流；它不覆盖取消回执，也不意味着成功。没有发布 `done` 时，取消回执仍可在 `outcome` 中保留部分内容、原生身份和 usage。无法取得结算结果时，状态继续为失败或未知，不会变为 `applied=true`。

## 故障、重试与清理 {#failures-retries-and-cleanup}

transport 与 execution 结果分开。Core 唯一 Run 订阅入口是 `SubscribeDurable`；事件 channel 关闭时检查 `Subscription.Err()`。断连与 subscriber overflow 以明确 observation error 关闭订阅，不虚构 `error` 或 `done`。Core 保留持久事实，并依据已确认事实协调。Runtime 保留清理所有权，直到原生工作、输入回执、交互和子任务工作都结算完成。

公开 Turn 状态是独立投影。[`execution/delivery.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/execution/delivery.go) 将不成功的编排尝试记录为 `failed`，包括未确认发送后的 `delivery_unknown` 和订阅失败后的 `event_stream_incomplete`；关闭的订阅可以用 `event_stream_incomplete` 替换发送原因。两者都表示原生效果未知：公开 `failed` 状态不证明 Harness 失败、没有产生副作用或清理已完成。应区分观测原因与原生证据。

| 条件 | 责任 |
| --- | --- |
| 不支持的能力或无效绑定 | 在开始操作前拒绝；不选择其他 Harness |
| 已确认准备或执行失败 | 保留有限错误类别和已观测结果；Runtime 结算自己的资源 |
| dispatch 后截止时间到达或连接丢失 | 除非应用回执另有证明，效果未知；不转换为执行失败 |
| 重连 | 重新建立 transport 和能力声明；不重放输入、初始化、传输或未决修改 |
| 重复 preparation 或 start | 应用连接本地身份与 fingerprint 规则；冲突请求被拒绝，旧 handle 不能启动替代工作 |
| 重复 input、function result 或 decision | 应用对应消息族的回执身份和冲突规则；不存在 transport 全局去重或 exactly-once 保证 |
| 清理失败 | 保留资源所有权，报告清理未确认；等待方超时不使资源可复用 |
| 取消回执丢失 | 取消可能已经发生；缺失回执既不证明成功，也不授权另一次执行 |

仅重试其自身契约规定可安全重试的操作。已退出使用的 preparation request ID 最终可能分配新 handle，因此 request ID 不是持久化 idempotency key。恢复原生 Session 是带有已验证身份的显式操作，不是 socket 故障的响应。临时连接错误允许重连；认证或版本拒绝需要运维纠正。Executor 清理与 Sandbox Provider 已确认的计算资源回收独立。

## 原生故障分类 {#native-failure-classification}

adapter 可以给 Run 的 `error` frame 添加 `code` 和 `http_status`。它们是可选的中性元数据，不是终结事件或公开错误契约；frame 文本、Usage、`done`、原生 Session 身份和取消回执保持原顺序与含义。

接受的 code 为 `authentication_error`、`rate_limit_exceeded`、`usage_limit_exceeded`、`server_overloaded`、`server_error`、`invalid_request`、`resource_not_found`、`request_timeout`、`context_length_exceeded`、`cyber_policy` 和 `connection_failed`（[`engine_failure.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/engine_failure.go)）。仅 `connection_failed` 保留 `http_status`，且只能是 100 到 599 的整数；其他 status 都丢弃。值缺失、格式错误或未知时保持未分类错误，不丢弃 Usage 或 `done`。

Core 在 Turn outcome 中将接受的值保存为 `engine_error_code` 和 `engine_http_status`。分类从属于终结状态和 Core 的 `error_code`：不能将已完成或已取消 Turn 变成失败、隐藏不完整事件流，或覆盖持久化或取消回执失败。正常 delivery 和 terminal journal draining 使用同一提取逻辑。[Session 诊断](../../contracts/agents-api/zh/session-diagnostics.md)仅为 Core error 为 `engine_failed` 的失败 Turn 暴露类别。[Codex](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/codex/README.md) 和 [Claude Code](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/claude/README.md) adapter 指南给出各 Harness 的映射；adapter 不依据错误文字分类。

## 工作区操作 {#workspace-operations}

无需运行 Turn 的工作区读取使用只读 preparation profile：带 `workspace_read_only` 的 `execution_prepare`，要求 `workspace_read_preparation` 能力。仅接受绑定的 Environment 和 resource 身份；不包含 execution option、model 与 MCP 凭据、原生 Session continuation、model 或 tool 输入，owner 拒绝 `execution_start`。Runtime 可以从绑定的本地文件系统提供读取，不启动 Harness 进程。profile 仅在本地 close 成功后发布 `released`；清理错误保留所有权并报告 `cleanup_unconfirmed`。factory 失败且清理未确认时，将 resource 与 error 一起返回，wrapper 保留两者。清理重试成功后发布已确认释放；旧 status snapshot 不发布成功。release 请求、HTTP 断连或远端 socket 关闭本身都不确认清理。

`workspace_read` 在同一已认证设备连接上，针对现有 preparation handle 或它已转移给的 Run，使用精确冻结的 Environment 身份；调用方不能提供 socket、凭据或 workspace root。`operation: directory` 列出一个 workspace 相对目录（空路径选择根目录），字节与条目限制互斥。结果最多携带 1024 个单路径组件 UTF-8 名称，每个最多 255 字节，并包含 entry kind、普通文件大小和明确截断信息；仅在目录访问与 handle 清理结算后返回。此层没有快照、递归或分页。字节读取与目录读取共享目标检查、关联、容量和保留的操作等待。

准入前，请求 payload 限制为 8 KiB，correlation ID 限制为 128 字节；过长 ID 不回显，过大的 trace metadata 不进入回复。原始 read result 限制为 1 MiB，位于 4 MiB transport frame 内。这些是私有 transport 限制，不是公开 Files 参数。成功读取要求完整字节或明确截断，并确认原生 close。安全的原生拒绝不携带字节；中断或有歧义的读取保持未知，并停止该 owner 的后续读取。已 dispatch 的读取在 observer 取消以及资源转移或释放后仍保留有限时等待方，资源关闭阻止新准入。gateway 限制订阅，不在重连后重试或重放读取；重复的 pending operation ID 不能启动另一次读取。

Core 在 Worker 的 Session 调度预约上运行空闲目录读取，活动执行时针对精确 Run。它在有限时 read 与 release 期间保留预约，仅在确认 close 后返回数据（不完整读取或不确定清理返回 unavailable，不包含数据），在交付结果前释放预约，并在完成或失败后撤销限定作用域的读取凭据。Runtime 保留不确定清理的所有权和容量。[Environment Files 契约](../../contracts/agents-api/zh/environment-files.md)负责公开授权、路径和分页。

`workspace_write` 在原生 writer 运行前，通过已确认的 64 KiB frame 传输完整且有界的 body，验证声明的 digest，不运行模型。私有 transfer 限制为 50 MiB，与公开 API 在任何 Runtime 工作前检查的 5 MiB decoded inline 限制独立。Runtime 在接收或应用写入时排除执行；格式错误、不完整或到期的 transfer 不会到达 installer。精确的 commit 或拒绝回执释放 mutation owner。缺失或有歧义的回执保留不确定性：observer 取消和本地进程退出不能证明没有改变任何内容。公开准入前，Core 在 Session lock 下持久预约写入，跨重启阻止后继 mutation，直到精确结算；请求不重放。各平台都使用 daemon 的 Go 实现进行有界读取、目录列举、文件创建与输出导出，不使用外部 helper 或 staging directory。

## MCP 连接权限 {#mcp-connection-authority}

prompt request 中每个公开 `MCPHTTPServer` 都携带明确的 `connection_origin`；值缺失或未知时拒绝，不选择默认值，Core 在 dispatch 前冻结公开默认值。Runtime 在选择 factory 前用公共 validator 验证 origin，并将公开和已安装 MCP 解析为临时 effective binding。[Environment 契约](../../contracts/agents-api/zh/environments.md#public-mcp-connection-origin)负责支持的组合、原生限制和故障所有权。

## 契约验证 {#contract-verification}

在仓库根目录运行 `make check-runtime-contract`。它执行共享 wire validator、gateway、transport 和 dispatcher 测试、两端共享 wire scenario、[观测结果回归测试](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/execution/runtime_protocol_test.go)以及 Harness 声明测试。不需要模型凭据或外部沙箱；`make check` 通过 `check-go` 和 `check-core` 运行相同测试。

每个 [wire scenario](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/prototest/wire.go)仅定义一次，按顺序列出 Core 与 Runtime 发送的 frame，以及原生结算、连接丢失和重连。每侧用真实实现对接脚本化 peer，由 peer 通过 WebSocket 重放另一侧 frame：[Core gateway 测试](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/runtimegateway/wire_test.go)和使用受控 Harness adapter 的 [Runtime transport 与 dispatcher 测试](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/wireconformance/wire_test.go)。每侧断言其发送的 frame 和拥有的行为，双方互不导入。Runtime 生成的值（如 Executor ID、admission handle 和 expiry）是占位符，由 Runtime 侧绑定为真实 Runtime 发送的值。

套件覆盖不兼容版本、preparation 失败、取消结算、连接丢失而不虚构终结事件、重连而不重放、陈旧或重复 handle 与回执、清理失败、有界 transfer 验证，以及超时后的资源所有权。详细故障注入测试放在所测 gateway 和 dispatcher 代码旁。

其他 Runtime 对接脚本化 Core 重放同一组 scenario，再对每个声明的能力运行原生验收。受控 adapter 测试证明 transport 契约，不证明原生 Harness 行为、操作系统支持、provider 认证或沙箱隔离。共享类型、本文和契约检查应一起修改。Harness adapter 还需运行 [Harness 接入](../../contracts/agents-api/zh/harness-onboarding.md)中描述的共享文本断言。
