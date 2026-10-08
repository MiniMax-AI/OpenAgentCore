---
title: "Core–Runtime 协议"
source: docs/runtime-protocol.md
source_hash: b823b0a4d564b9191687005326939a7db10803ef99667d088336427b906d06d5
---

此协议在 Runtime daemon 获取机器凭据后连接 Core 与 daemon，定义 daemon 连接上消息的含义和顺序。wire 类型、限制和验证器仅在 [`internal/agentdaemon/proto`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/internal/agentdaemon/proto) 中定义一次；Core 的 [gateway](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/runtimegateway) 与参考 Runtime 的 [dispatcher](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/daemon/internal/dispatch) 都使用它们，因此无需同步第二套 payload schema。签发凭据和打开连接的 HTTP 路由见[机器连接 API](../../contracts/agents-api/zh/machine-api.md)。

托管和自托管 Runtime 使用同一协议。Harness 通过 [Harness adapter 契约](../../contracts/agents-api/zh/harness-onboarding.md)接入，该契约负责 Runtime registry 后的 Executor 和 Turn 生命周期义务。

## 所有权与连接 {#ownership-and-connection}

Core 拥有持久化的 Session、Turn、input 和 Environment 记录、调度与协调。Runtime 拥有原生 Executor、活动 Turn、传输状态和清理责任，直到完成结算。Sandbox Provider 拥有执行位置与外围计算资源。释放执行准入或关闭 Executor 不会删除、暂停或回收沙箱。daemon 不是隔离边界；参见 [Runtime 与外层隔离](concepts.md#runtime-and-outer-isolation)。

Runtime 按以下顺序连接：

1. 获取 daemon 凭据和 device ID。[机器连接 API](../../contracts/agents-api/zh/machine-api.md#credentials) 列出凭据类型；Project API key 和 Core key 都不是 Runtime 凭据。
2. 调用 `POST /api/v1/agent-daemon/bootstrap`，通过 Bearer header 提供凭据，并提供 device ID。使用返回的连接 URL。
3. 连接 `/api/v1/agent-daemon/ws` 的 WebSocket，传入 `device_id`、`version` 查询参数和 Bearer header。凭据不得出现在 URL、payload 日志或 trace 中。
4. 立即发送 heartbeat，之后按 bootstrap 返回的间隔发送。每个 heartbeat 声明 `supported_agent_kinds`、其可用性和[能力](#capability-declarations)，以及 Runtime 是否在释放时删除 Session 的原生 home（`home_removal`，见 [Session 分配](#session-assignments)）。第一个 heartbeat 之前能力未知；heartbeat 中缺失的 kind 视为未声明。两者都不允许推断。
5. 交换有序 JSON [envelope](#envelope-and-identity)。Heartbeat 仅证明存活，不证明执行进度，也不充当此前消息的回执。

wire 版本为 [`proto.Version`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/version.go)。Core 仅接受精确匹配，包括 patch 部分。不匹配时，在任何 dispatch 前返回 HTTP 426 `incompatible_version`；daemon 将其视为永久错误并停止重连。应一起部署版本匹配的两端。

每条物理连接拥有新的路由、admission handle 和传输状态。同一设备的新连接替代旧连接：Core 关闭旧连接并移除其 Run 路由，新连接不继承这些状态。有效凭据和连接不授权选择其他 Session 或 Environment 绑定；[分配](#session-assignments)约束 frame 可代表哪个 Session 行事。

## 能力声明 {#capability-declarations}

[`inbound.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/inbound.go) 中的 `AgentKindCapabilities` 描述一个 Runtime 与 Harness 组合，独立于 `available`。它是 Harness [声明](../../contracts/agents-api/zh/harness-onboarding.md#declare-support)中由安装收窄的部分。每个字段都是 `CapabilitySupport`：支持或不支持。零值表示未指定且无效，即使 Harness 不可用也如此。注册在修改 registry 前验证完整声明；不存在隐含的基础 descriptor。

wire 上每个字段都是 JSON boolean，所有字段都必须出现，包括 `false`。不完整声明编码失败。解码拒绝省略、null、无效和未知字段，以及缺失的 capability 对象。无效 heartbeat 会清空连接的 admission snapshot 并关闭 transport；这不证明原生完成或取消结果。

每个已准入的 Executor 和 Turn 保留准入时的声明。后续 heartbeat 不能给已有 owner 增加操作。可选操作在任何原生调用前检查此快照；存在 Go interface 不代表支持。已声明操作返回 `agent.ErrUnsupportedOperation` 属于契约违规，与不可用、原生调用失败或不确定写入不同。不确定操作保留回执与所有权，绝不自动重放。Runtime 仅在 Harness 及其 [Environment owner](#session-assignments) 都支持时声明 `local_environment` 和 `environment_none`；`local_environment` 同时涵盖 owner 的工作区读取、只读 preparation 和输出导出。

新增字段要求每个生产声明都作出明确决定。契约测试为注册和 wire 往返逐一枚举字段；共享测试 fixture 单独列出字段，不为未来字段提供默认值。[Harness 接入](../../contracts/agents-api/zh/harness-onboarding.md)负责各声明的 adapter 侧规则。

heartbeat 只能收窄 Harness 的静态声明。某个 kind 没有内置声明，或宣称了其静态声明不具备的支持时，Core 将该 heartbeat 视为无效。在设备选择及领取 Turn 前的最终检查中，Core 要求选定设备报告该 Harness 为 `available`，并用 `proto.ValidateSelection` 按收窄到该 heartbeat 的静态声明检查 Session，它要求：

| 能力 | Core 何时要求 |
| --- | --- |
| `environment_none` | Environment 类型为 `none` |
| `local_environment` | Environment 类型为 `openai_hosted` 或 `self_hosted` |
| `native_session_recovery` | Session 已启动过 Turn，但未记录原生 Session ID |
| `text_verbosity` | Agent 请求 `medium` 以外的 verbosity |
| `structured_output` | Agent 请求 `json_schema` 输出 |
| `subagent_observations` | `multi_agent.enabled` 为 true |
| `tool_search` | Agent 启用 tool search 或延迟 function 加载 |
| `function_tools` | Agent 声明 function tool，或交付 function result |
| `message_images`, `function_result_images` | 消息或 function result 携带图像 |
| `mcp_http_tools`, `mcp_http_required`, `mcp_http_bearer_auth` | Agent 声明 HTTP MCP server；其中一个为 `required`；其中一个选用了 Vault 凭据 |

流式输出、带持久输入回执的 steering、持久 Turn、preparation、执行控制和工具观测不作声明：每个可用 Harness 都实现它们，该义务由 [Harness 接入](../../contracts/agents-api/zh/harness-onboarding.md#register-the-adapter)负责。

`execution_prepare` 的配置携带 Session 的模型配置和 Core 为各 Run 设置的显式启用项：

| 字段 | Core 设置方式 |
| --- | --- |
| `model`, `system_prompt`, `model_provider`, `harness_config` | 来自 Session 冻结的配置：Agent 的 model 和 instructions、Session 的 provider bundle，以及[原生模型参数](../../contracts/agents-api/zh/model-execution.md#native-model-parameters)。Harness 在产生任何原生效果之前验证它们 |
| `execution_controls` | 始终设置：解析后的 text verbosity（默认 `medium`）、明确禁用 programmatic tool calling，以及任何 `json_schema` 输出格式。原生选项名称由 adapter 负责 |
| `observe_subagent_identities`, `disable_subagents` | 根据 Agent 的 `multi_agent.enabled` 设置 |
| `disable_execution_environment` | Environment 类型为 `none` 时设置 |
| `local_environment` | 为 `openai_hosted` 和 `self_hosted` 设置，包含精确的 Environment 绑定。请求不携带 working directory；Runtime 按自身绑定检查 `workspace_directory`。请求不携带网络策略，因为 Core 只准入[启用的网络](../../contracts/agents-api/zh/environments.md#restricted-network) |
| `require_existing_native_session` | 需要恢复原生 Session 时设置 |

执行配置必须且只能包含 `local_environment` 和 `disable_execution_environment` 之一；两者都缺失或同时存在时，`execution_prepare` 以 `unsupported_configuration` 拒绝。

未携带显式启用项的请求保留未启用时的 frame 和字段。只要 adapter 映射了原生工具，`tool_call` frame 就携带与 engine 无关的 `observation`。

## Envelope 与身份 {#envelope-and-identity}

每个数据 frame 都是一个 JSON [`Envelope`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/envelope.go)：`type`、随类型变化的 `id`、类型化 `payload`、Session frame 上的 Session `assignment`，以及可选 W3C `trace`。trace 仅用于诊断关联；trace 数据缺失或无效时创建本地 trace，不改变所有权。不要将 trace ID 用作 request ID。

| 身份 | 范围与含义 |
| --- | --- |
| Device ID 和连接 | 经认证的 Runtime 路由与连接所有权 |
| Assignment | `Envelope.assignment`：约束 frame 的 Session、assignment ID 和 epoch；见 [Session 分配](#session-assignments) |
| Session ID / Environment ID | Core 拥有的配置与工作区绑定；payload validator 要求时使用规范 UUID |
| Executor ID | Runtime 拥有的原生资源，可在配置相同的已结算 Turn 之间保留 |
| Preparation request ID | prepare、start、release 和 status 的 `Envelope.id`；与 Run 不同 |
| Admission handle | Runtime 生成的预约，仅在接受它的连接上有效 |
| Run ID | 一次执行尝试；输出、取消、活动输入和 function 的 `Envelope.id` |
| Delivery ID / input ID / call ID | 分别标识 resolve 尝试、活动输入回执与原生 function；不能互换 |
| Transfer ID / suspension ID | 连接本地传输关联 / 持久化 suspension 尝试的 fencing |

取消和 function-result 确认使用 Run ID，并匹配 delivery ID。缺少必需关联的回复不能证明已接受。

## 消息族 {#message-families}

链接的源文件定义必需字段、验证器、限制和有限错误类别。Runtime 严格解码每个 Core → Runtime payload：任意层级出现其类型未声明的成员都会拒绝该请求。

| Core → Runtime | Runtime → Core | 定义 |
| --- | --- | --- |
| `assignment_bind`, `assignment_release` | `assignment_status` | [Session 分配](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/assignment.go) |
| Runtime 无法路由的任意 frame | `protocol_error` | [Session 分配](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/assignment.go) |
| `runtime_prepare` | `runtime_prepare_result` | [初始化与能力传输](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/runtime_prepare.go) |
| `execution_prepare`, `execution_start`, `execution_release` | `preparation_status` | [执行准入](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/preparation.go) |
| `prompt_cancel` | `delta`, `thinking`, `output_message`, `tool_call`, `usage`, `error`, `done`, `heartbeat`, `interaction_decision_ack` | [请求](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/outbound.go)、[事件与能力](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/inbound.go) |
| `prompt_steer` | `prompt_steer_ack` | [活动输入回执](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/steering.go) |
| `function_result` | `function_call`, `interaction_decision_ack` | [Function 调用](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/functions.go) |
| `workspace_read`, `workspace_write`, `workspace_export` | 对应的 `*_result` | [读取](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/workspace_read.go)、[写入](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/workspace_write.go)、[导出](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/workspace_export.go) |
| `environment_quiesce`, `environment_resume` | `environment_quiesced`, `environment_resumed` | [暂停 fencing](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/suspend.go) |

初始、已准备和活动输入使用同一[有序 MessageInput](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/message_input.go)。Adapter 保留消息与内容顺序，并明确拒绝不支持的内容；仅文本 transport 拒绝图像内容而不丢弃它。[消息输入契约](../../contracts/agents-api/zh/message-content.md)负责公开图像 profile、空白规则和各 Harness 的原生转换。

助手文本只以带身份的消息到达。一条消息以状态为 `in_progress`、带其 ID 的 `output_message` 开始，该 frame 可以指明 `phase`；每个文本片段是一个 `delta`，其 `item_id` 指向该消息；状态为 `completed` 的 `output_message` 携带消息的完整文本，并替换其片段。共享校验器拒绝没有 `item_id` 的 `delta`，`done` 不携带回答文本。Turn 结束时仍未结束的消息标记为 `incomplete`。

Usage frame 和最终 usage snapshot 都携带当前执行的累计测量，替换之前的快照；不要相加。缺失的测量表示未知，不是零。

## Session 分配 {#session-assignments}

分配（assignment）把一个 Session 绑定到运行它的 Runtime。`Envelope.assignment` 以 `session_id`、`assignment_id` 和 `epoch` 命名它，这是 frame 携带分配的唯一位置。Core 每次改变分配的期望状态时推进 epoch，因此较低的 epoch 是陈旧的。

每个 Session frame 都携带分配：`execution_prepare`、`execution_start` 和 `execution_release`；`prompt_cancel`、`prompt_steer` 和 `function_result`；`runtime_prepare`、`workspace_read`、`workspace_write` 和 `workspace_export` 的每个 frame；以及 `environment_quiesce` 和 `environment_resume`。回复回显请求的分配，Run 的 frame 携带启动它的分配；Core 拒绝指明其他分配的回复或 Run frame。heartbeat 不携带分配。

在一条连接上执行 Session 的第一个操作之前，包括没有 Turn 的 Environment 初始化和文件操作，Core 发送带 Session 的 Environment ID 的 `assignment_bind`，并等待 `assignment_status` `bound`。当 Runtime 是 agent host 且 Environment 有存活的 [Link](./sandbox-link-protocol.md) resource 时，绑定还携带 `resource` 和 `attach_grant`：前者是该 resource，形式与[引导输入](./sandbox-bootstrap.md#launch-input)中的相同；后者是 base64 编码的 grant，agent host 凭它在此分配和 epoch 下打开该 resource generation 上的服务。若 Environment 没有存活的 resource，Core 不向 agent host 发送绑定，需要该绑定的操作失败。grant 是机密。Core 不向其他任何 Runtime 发送这两个字段。只带其中一个字段、或带其他 Environment 的 resource 的绑定以 `invalid_request` 失败。以相同的 epoch、Environment、resource 和 grant 重复绑定同一分配仍得到 `bound`；其他字段不同的同 epoch 绑定以 `assignment_conflict` 失败。以更高 epoch 绑定已绑定的分配会取代较早的 epoch，resource 和 grant 均可不同：Runtime 像释放那样 fence 并清理较早 epoch 的工作，但保留 home，然后绑定新 epoch 并回复 `bound`。Session 的 Environment 从不改变，因此这样的绑定若指定其他 Environment，会在任何 fence 之前以 `assignment_conflict` 失败。清理未完成时回复 `failed` 和 `cleanup_unconfirmed`，以同一 epoch 重试会重复清理；期间到达的释放或更晚的绑定使其以 `assignment_stale` 失败。Runtime 只在其已绑定的分配下准入 Session frame：较旧的 epoch 或已释放的分配以 `assignment_stale` 失败；其他分配、Session 或 Environment 以 `assignment_conflict` 失败。已启动 Run 的 frame，包括其取消回执，在释放前仍可在启动它的分配下准入。Runtime 已记录回执的重复函数结果或决策只在应用它的分配下得到回答；其他分配以 `assignment_conflict` 失败。

Session 的第一次绑定确定其 Environment owner，此后不再改变；owner 持有 Environment 的资源，并执行对这些资源的每个作用。owner 根据 Environment 检查每个 `execution_prepare` 配置（包括只读 profile），并在 Executor 启动前填入已安装的能力。它应用 `runtime_prepare`，为 `workspace_read` 列举目录，为 `workspace_write` 写入文件，为 `workspace_export` 导出输出。Runtime 的 dispatcher 保留准入、传输分帧和 fencing，从不替换为其他实现。self-hosted Runtime 的 owner 是其绑定的本地工作区，该工作区比每个分配存续得更久；Runtime 以 `assignment_conflict` 拒绝任何其他 Session 的绑定。agent host 的 owner 通过自己的一个 [Link](./sandbox-link-protocol.md) attachment，用 File 和 Process 在 Session 的沙箱中工作；该 attachment 在首次使用时凭绑定的 grant 打开。owner 从 Session 的第一次绑定存续到其 home 被删除，比 Session 的 Executor 和连接存续得更久。其 Environment 被 quiesce、分配被释放或其 epoch 被取代时关闭其 attachment；之后的分配或 epoch 下的绑定在该 attachment 关闭后接管 owner，它无法接管的绑定（包括其他 Environment 的绑定）以 `assignment_conflict` 失败。它把每个 setup 步骤作为 Process 操作运行，操作 ID 即 `runtime_prepare` 的 envelope ID。在产生任何作用前无法连到沙箱的 `workspace_write` 或 `runtime_prepare` 以 `rejected` 和 `resource_unavailable` 结束。若 Plugin 的 MCP server 声明了字面量 `http_headers`，或是带 `env_vars` 的 stdio server，owner 会在暂存其任何内容之前使其失败。无法观察到结果的文件变更或 setup 步骤会隔离 owner，直到 home 被删除：它不再发送任何变更，之后每个 `workspace_write` 和 `runtime_prepare` 都以 `unknown` 结束。没有 owner 的 Session 不支持上述任何操作，Runtime 以各自的类型化错误码拒绝：只读 preparation 为 `unsupported_read_preparation`；带 `local_environment` 的 Executor 配置为 `invalid_configuration`；`runtime_prepare` 为 `runtime_preparation_unsupported`；`workspace_write` 为 `write_unsupported`；`workspace_read` 和 `workspace_export` 为 `read_unsupported`。

Core 先记录释放并推进 epoch，再发送任何消息；记录即撤回该分配的 attach grant。随后 Core 让 relay 吊销 Environment 的 Link resource 的当前 generation，使凭该 grant 打开的 attachment 在 Runtime 收到释放之前关闭。删除 Session 以 `remove_home: true` 释放其分配；释放其 Environment 发送 `false`。删除从不吊销共享的 Runtime 凭据。`assignment_release` 立即约束该分配。随后 Runtime 停止 Session 的工作：仍在接收内容、或已提交但尚未应用的传输以 `assignment_stale` 结束；它释放只读准备，并等待每个 workspace 读取、写入、导出和 Runtime 准备发送结果。它关闭 Session 的 Executor，随后释放其 Environment owner 持有的资源，并在要求时删除原生 home；此后才回复 `released` 或 `home_removed`。未完成的清理回复 `failed` 和 `cleanup_unconfirmed`，同一 epoch 的重试会重复清理。声明 `home_removal` 不支持的 Runtime 以 `unsupported_operation` 回答 `remove_home: true`，Core 只要求它释放。Core 根据匹配的 `released` 或 `home_removed` 记录释放已应用；没有 Runtime 能处理该释放时立即记录：发给无授权 Runtime 的释放在记录时即结清，吊销 Runtime 会结清它的释放。Core 在 Runtime 连接时重发所有未确认的释放；失败的释放退避重试，等待最久的释放先发送，因此失败的释放不会拖延其他释放。`environment_quiesce` 只作用于指定的 Environment：该 Environment 的某个 Session 仍有进行中的工作时，它以 `resource_busy` 失败；否则 Runtime 关闭该 Environment 的 Executor，让其 owner 释放所持有的资源，并回复 `environment_quiesced`。在匹配的 `environment_resume`（携带使其 quiesce 的分配）到达之前，Runtime 对该 Environment 只准入其 Session 的释放，并以 `protocol_error` `resource_unavailable` 回答它的其他任何 frame（包括绑定）；其他 Environment 的 Session 继续运行。

Runtime 对无法路由的 Core frame 回复 `protocol_error`，回显请求 ID，并携带其类型和错误码。

## 准备与执行顺序 {#preparation-and-execution-order}

无论托管还是用户自有环境，每条连接都通过 `runtime_prepare` 初始化 Environment；[Environment 契约](../../contracts/agents-api/zh/environments.md#runtime-capability-preparation)负责准备内容和时机。传输文件或 archive 时，发送 `begin`，等待 `ready`，发送有序 chunk 并等待每个匹配的 `received` offset，再发送 `commit` 并等待 `completed`。初始化和终结阶段使用不含文件数据的类型化 header。使用共享 validator 验证预期结果、offset、size 和有限错误 code。每个 Session 同一时间每种 transfer 至多一个；无论结果如何，一个 transfer 在发送结果之前只阻塞其所属 Session 的 `execution_start` 和 transfer，以及其 Environment 的 quiesce。一条连接上所有 Session 同时暂存的 body 总量以 `workspace_write` 与 `runtime_prepare` 限制之和为上限；超出上限的 `begin` 以 `write_capacity` 或 `runtime_preparation_capacity` 被拒绝。chunk 回执确认暂存字节，不确认安装；完成的 commit 确认该操作，不证明后续 Turn 已运行。

执行 Turn 分为五步：

1. 订阅 preparation status，再发送 `execution_prepare`，携带不可变 Session 配置，不包含 Run 输入。
2. `preparing` 表示 Runtime 拥有 preparation。`ready` 提供 Executor ID、admission handle、revision 和 expiry。两者都不提交用户输入。
3. 订阅 Run，再发送 `execution_start`，携带该 Executor ID、handle、Run ID 和有序输入。有效 start 将预约转移一次。`started` 确认转移给 Turn，不确认完成；输出可能与 status 竞争，因此必须事先有 subscriber。
4. 消费 Run 事件，直到原生终结结果或观测丢失。执行 error 后跟随 `done`，关闭流；之前的 error 仍属于结果的一部分。
5. start 前放弃时发送 `execution_release`。所有权转给 Run 后使用 `prompt_cancel`；释放旧 handle 不能取消后继 Turn。

preparation 预约每个 Turn 的准入，而不是新 Executor。它携带明确 Session 身份和不可变配置，不包含模型输入或 Run ID；Runtime 从该身份推导 Session 的原生状态键。新请求返回连接本地 handle 与所属 Executor ID；复用健康 Executor 时直接返回 `ready`，无需原生 preparation。每个 handle 的 revision 决定 status 观测顺序：忽略旧的或重复的 revision，不将 status 应用于其他 handle。典型状态转移为 `preparing → ready → starting → started`，或由 `released`、`expired`、`failed` 终止。`rejected` 控制操作携带 `operation` 和 error code，不替代 handle 的当前 revision。释放 admission 仅放弃该 admission；不关闭 Session 的空闲 Executor，也不取消后续 Turn。

preparation 和 start 在 receive loop 与 router lock 之外运行。admission 在授予五分钟后到期，重试不延长截止时间；到期不解除 Runtime 完成清理结算的义务。Runtime 分别限制活动 preparation、execution 和保留的空闲资源，关闭中或不确定资源持续计入限制，直到清理成功。明确的 `execution_prepare` 拒绝若为 `preparation_capacity`，会让排队 Turn 保持未领取，供 Worker 重试，包括清理占用容量的情况；其他错误或不确定交付都不授权重放。Runtime 最多保留 64 条 admission 记录，旧 handle 不会消耗替代项的 admission。这些记录仅属于连接，不是持久化输入重放。

Executor 空闲到期属于 Runtime 资源策略，与 Core 的活动 Turn 并发限制独立。关闭时 Runtime 关闭活动和空闲 Executor，保留关闭失败的目标，并允许稍后串行重试。普通断连会关闭失败的 transport 并保留原 router，直到 shutdown 成功；等待超时或清理失败不授权重连，进程 shutdown 继续等待，不丢弃自己拥有的原生资源。工作区操作在跨 Turn 和 Executor 关闭后仍保留绑定与结算规则。

## 活动输入回执 {#active-input-receipts}

Core 通过 `prompt_steer` 交付活动输入，每个 Run 一次交付一个输入，并等待回执后再发送下一个。每个输入都是持久化的，经历以下阶段：

| 阶段 | 定时器 |
| --- | --- |
| 原生写入 | Runtime 将 adapter 原生写入限制为 10 秒；写入完成后停止计时 |
| `written` 确认 | Core 从交付开始最多等待 30 秒取得 `written`；否则输入结果未知 |
| 回执发送 | 每次发送回执都有独立的 5 秒预算，并感知 shutdown |
| 原生接受 | `accepted` 在 Turn 生命周期内到达，不自动重新交付 |
| Done | Runtime 在原生 Turn 结算完成、且正在处理的输入的回执发送结束后才发送 `done`；该输入受原生写入与回执发送预算限制 |

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

不存在适用于每个 envelope 的通用回执。发送成功不证明对端已收到、接受或完成请求。进程退出、stop signal 或本地 context 取消不证明取消完成。`done` frame 也可能关闭已结算的取消流；它不覆盖取消回执，也不意味着成功。没有发布 `done` 时，取消回执仍可在 `outcome` 中保留原生身份和 usage。无法取得结算结果时，状态继续为失败或未知，不会变为 `applied=true`。

## 故障、重试与清理 {#failures-retries-and-cleanup}

transport 与 execution 结果分开。Core 唯一 Run 订阅入口是 `SubscribeDurable`；事件 channel 关闭时检查 `Subscription.Err()`。断连与 subscriber overflow 以明确 observation error 关闭订阅，不虚构 `error` 或 `done`。Core 保留持久事实，并依据已确认事实协调。Runtime 保留清理所有权，直到原生工作、输入回执、function 结果和子任务工作都结算完成。

公开 Turn 状态是独立投影。[`execution/delivery.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/execution/delivery.go) 将不成功的编排尝试记录为 `failed`，包括未确认发送后的 `delivery_unknown` 和订阅失败后的 `event_stream_incomplete`；关闭的订阅可以用 `event_stream_incomplete` 替换发送原因。两者都表示原生效果未知：公开 `failed` 状态不证明 Harness 失败、没有产生副作用或清理已完成。应区分观测原因与原生证据。

| 条件 | 责任 |
| --- | --- |
| 不支持的能力或无效绑定 | 在开始操作前拒绝；不选择其他 Harness |
| 已确认准备或执行失败 | 保留有限错误类别和已观测结果；Runtime 结算自己的资源 |
| dispatch 后截止时间到达或连接丢失 | 除非应用回执另有证明，效果未知；不转换为执行失败 |
| 重连 | 重新建立 transport 和能力声明，在下一次 Session 操作前重新绑定，并重发未确认的释放；不重放输入、初始化、传输或未决修改 |
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

无需运行 Turn 的工作区读取使用只读 preparation profile：带 `workspace_read_only` 的 `execution_prepare`，要求 `local_environment` 能力。仅接受绑定的 Environment 和 resource 身份；不包含 execution option、model 与 MCP 凭据、原生 Session continuation、model 或 tool 输入，owner 拒绝 `execution_start`。Session 的 Environment owner 提供读取，不启动 Harness 进程。profile 在 Runtime 放弃该 preparation 的所有权后发布 `released`；旧 status snapshot 不发布成功。release 请求、HTTP 断连或远端 socket 关闭本身都不确认释放。

`workspace_read` 在同一已认证设备连接上，针对现有 preparation handle 或它已转移给的 Run，使用精确冻结的 Environment 身份，列出一个 workspace 相对目录（空路径选择根目录）；调用方不能提供 socket、凭据或 workspace root。结果最多携带 `max_entries`（1 到 1024）个单路径组件 UTF-8 名称，每个最多 255 字节，并包含 entry kind、普通文件大小和明确截断信息；仅在目录访问与 handle 清理结算后返回。此层没有快照、递归或分页。

准入前，请求 payload 限制为 8 KiB，correlation ID 限制为 128 字节；过长 ID 不回显，过大的 trace metadata 不进入回复。这些是私有 transport 限制，不是公开 Files 参数。安全的原生拒绝不携带目录；中断或有歧义的读取保持未知，并停止该 owner 的后续读取。已 dispatch 的读取在 observer 取消以及资源转移或释放后仍保留有限时等待方，资源关闭阻止新准入。gateway 限制订阅，不在重连后重试或重放读取；重复的 pending operation ID 不能启动另一次读取。

Core 在 Worker 的 Session 调度预约上运行空闲目录读取，活动执行时针对精确 Run。它在有限时 read 与 release 期间保留预约，仅在确认 close 后返回数据（不完整读取或不确定清理返回 unavailable，不包含数据），在交付结果前释放预约，并在完成或失败后撤销限定作用域的读取凭据。Runtime 保留不确定清理的所有权和容量。[Environment Files 契约](../../contracts/agents-api/zh/environment-files.md)负责公开授权、路径和分页。

`workspace_write` 在原生 writer 运行前，通过已确认的 64 KiB frame 传输完整且有界的 body，验证声明的 digest，不运行模型。私有 transfer 限制为 50 MiB，与公开 API 在任何 Runtime 工作前检查的 5 MiB decoded inline 限制独立。Runtime 在接收或应用写入时排除该 Session 的执行；格式错误、不完整或到期的 transfer 不会到达 installer。精确的 commit 或拒绝回执结算该写入。缺失或有歧义的回执把不确定性留给 [Environment owner](#session-assignments)：observer 取消和本地进程退出不能证明没有改变任何内容。公开准入前，Core 在 Session lock 下持久预约写入，跨重启阻止后继 mutation，直到精确结算；请求不重放。在各平台上，owner 都在 daemon 内列举目录、创建文件和导出输出，不使用外部 helper 或 staging directory。

## MCP 连接权限 {#mcp-connection-authority}

`execution_prepare` 配置中每个公开 `MCPHTTPServer` 都携带明确的 `connection_origin`；值缺失或未知时拒绝，不选择默认值，Core 在 dispatch 前冻结公开默认值。Runtime 在选择 factory 前用公共 validator 验证 origin，并将公开和已安装 MCP 解析为临时 effective binding。[Environment 契约](../../contracts/agents-api/zh/environments.md#public-mcp-connection-origin)负责支持的组合、原生限制和故障所有权。

## 契约验证 {#contract-verification}

在仓库根目录运行 `make check-runtime-contract`。它执行共享 wire validator、gateway、transport 和 dispatcher 测试、两端共享 wire scenario、[观测结果回归测试](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/execution/runtime_protocol_test.go)以及 Harness 声明测试。不需要模型凭据或外部沙箱；`make check` 通过 `check-go` 和 `check-core` 运行相同测试。

每个 [wire scenario](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/agentdaemon/proto/prototest/wire.go)仅定义一次，按顺序列出 Core 与 Runtime 发送的 frame，以及原生结算、连接丢失和重连。每侧用真实实现对接脚本化 peer，由 peer 通过 WebSocket 重放另一侧 frame：[Core gateway 测试](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/runtimegateway/wire_test.go)和使用受控 Harness adapter 的 [Runtime transport 与 dispatcher 测试](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/wireconformance/wire_test.go)。每侧断言其发送的 frame 和拥有的行为，双方互不导入。Runtime 生成的值（如 Executor ID、admission handle 和 expiry）是占位符，由 Runtime 侧绑定为真实 Runtime 发送的值。

套件覆盖不兼容版本、preparation 失败、取消结算、连接丢失而不虚构终结事件、重连而不重放、陈旧或重复 handle 与回执、清理失败、有界 transfer 验证，以及超时后的资源所有权。详细故障注入测试放在所测 gateway 和 dispatcher 代码旁。

其他 Runtime 对接脚本化 Core 重放同一组 scenario，再对每个声明的能力运行原生验收。受控 adapter 测试证明 transport 契约，不证明原生 Harness 行为、操作系统支持、provider 认证或沙箱隔离。共享类型、本文和契约检查应一起修改。Harness adapter 还需运行 [Harness 接入](../../contracts/agents-api/zh/harness-onboarding.md)中描述的共享文本断言。
