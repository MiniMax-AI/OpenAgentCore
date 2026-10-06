---
title: "会话、事件和历史"
source: contracts/agents-api/sessions-events.md
source_hash: d5d0928665f38105167e592f9561c3d3852a13fc11b976f15c1bc4cc2ca9cb14
---

本契约涵盖会话（Session）内部发生的事情：发送输入、实时事件流，以及读取轮次（Turn）、条目（Item）和使用量的持久化历史。会话资源本身（创建配置、重试标识、更新、列出和删除）见 [Core 线协议行为](wire-semantics.md)。消息和函数结果内容见[消息内容](message-content.md)。[Agents API 指南](../../../docs/zh/api/public-agent-api.md)展示了使用 SDK 和 HTTP 的调用方式。

## 恢复模型 {#recovery-model}

事件流仅提供实时事件；Turn 和 Item 会持久保存。需要获得每个结果的客户端：

1. 在发送输入前打开 `GET /v1/agents/sessions/{session_id}/events`。
2. 断开连接后，再次订阅并缓冲新事件。
3. 读取会话、其 Turn 和 Item，按 ID 对 Item 去重，并在应用缓冲更新时保留已最终确定的 Item。

流只是一个观察器。关闭流绝不会取消已接纳的工作，而且流绝不会重放未由它发送过的事件。输入响应丢失后，使用相同的 `Idempotency-Key` 重试，然后读取会话、Turn 和 Item。

## 会话状态 {#session-status}

会话的 `status` 和 `last_active_at` 由其最新的根 Turn 以及仍在等待 Environment 的输入推导得出：

| `status` | 适用情况 |
| --- | --- |
| `idle` | 尚无 Turn，或最新 Turn 已完成或已取消。为仍在预配的托管 Environment 预留的输入也显示为 `idle` |
| `in_progress` | 最新 Turn 正在排队、运行或等待 |
| `requires_action` | 最新 Turn 正在等待函数结果且未请求取消，或输入正在等待 `self_hosted` 机器连接。`required_actions` 列出 `function_call` 或 `environment_connection` 条目 |
| `failed` | 最新 Turn 失败（`error` 为 "The execution could not complete."）、为 Environment 预留的输入失败、初始输入在接纳前到期，或 Environment 初始化失败（见 [Environment 初始化失败](#environment-initialization-failure)） |

Turn 失败后会话仍可使用：新输入会启动一个新 Turn。后来预留的输入到期时，会话保持为 `idle`。Environment 初始化失败或过期会阻止新工作。

## 发送输入 {#send-input}

`POST /v1/agents/sessions/{session_id}/events` 接收包含 1 到 64 个事件的有序数组：`agent.session.input.message`、`agent.session.input.cancel` 和 `agent.session.input.tool_result`。整个批次以原子方式接纳；批次存储后且在任何 harness 读取它之前，响应为无正文的 202。接纳绝不表示已在原生侧应用。

- **限制。** 请求正文最多为 1 MiB，单次请求存储的输入最多为 512 KiB。
- **空值批次。** 将 `events` 显式设为 `null` 无效。
- **空批次。** `{"events": []}` 会检查会话是否存在并返回 202。它不会创建 Turn、Item 或重试标识。
- **重试。** 最长 128 字节的 `Idempotency-Key` 标识整个有序批次。相同密钥和批次会再次返回 202，而不会重复接纳任何内容；相同密钥搭配另一个批次会返回 409 `idempotency_conflict`。不带密钥的请求始终是新请求。
- **消息。** 在空闲会话上，消息批次会启动一个排队的 Turn。Turn 运行时，消息会加入其中（引导）；它们绝不会启动并行 Turn。每条消息都保留为独立的用户 Item，即使 harness 将多条消息作为同一个提示接收。
- **取消。** 排队的 Turn 无需活动 Runtime 即可取消。正在运行的 Turn 只有在 Runtime 确认后才会取消；完成操作可能赢得该竞争。读取到 `cancelled` 时才表示该 Turn 已停止，而不是请求返回时。在没有待处理输入的情况下，对空闲会话执行的取消会被接受且不产生任何效果；而在输入预留待处理期间，取消会返回 409。
- **函数结果。** `turn_id`、`call_id` 和 `success` 为必填项；`output` 和 `error` 为可选项且可为空（[内容规则](message-content.md#function-results)）。重复提交完全相同的结果会返回 202，不会再次应用或发出事件。harness 应用结果时才会出现结果 Item；如果取消操作导致结果无法应用，结果仍会存储，但不会产生 Item。
- **排队。** 当一个已连接且支持该会话 harness 和配置的 Runtime 接入，并且 Core 的 [`core.execution_concurrency`](../../../docs/zh/configuration.md#settings) 工作槽位有一个空闲时，排队的 Turn 才会启动。会话始终绑定到首次运行它的 Runtime。
- **执行可用性。** 不具备执行能力的服务会返回 503 `execution_unavailable`，失去执行所有权的 Worker 会返回 503。创建时未指定模型提供方的会话会拒绝新消息并返回 400 `model_provider_required`（[模型执行](model-execution.md)）。

### 包含 Environment 的会话 {#sessions-with-an-environment}

在 `openai_hosted` 和 `self_hosted` 会话中，Turn 运行期间发送的消息会立即加入该 Turn。发送给空闲会话的消息会为 Environment 预留该批次：请求等待 Turn 开始，从预留时起最多等待五分钟。预留等待 `self_hosted` 机器连接时，会话显示为 `requires_action`，并带有 `environment_connection` 操作。在这些部署方式下，携带消息的批次只能包含消息。仅取消和仅结果的批次会立即被接纳，并且不会创建 Turn。

当 Turn 启动时，等待中的请求以 202 结束；超过截止时间时，以 409 `environment_input_expired` 结束；管理员归档会话或重置其部署并取消预留时，以 409 `environment_input_cancelled` 结束；Environment 失败或过期时，以 409 `environment_unavailable` 结束。断开等待中的请求不会取消预留，也不会重新开始其截止计时。

### 输入错误 {#input-errors}

检查按以下顺序执行：请求验证、会话查找、重试查找、针对包含消息的批次的 Environment 文件写入门控、待处理输入门控，最后按批次顺序检查每个事件。被拒绝的批次不会写入任何内容，也不会改变任何待处理操作。所有 409 响应的 `type` 都是 `conflict_error`（[错误封装](wire-semantics.md)）。

| 情况 | 状态和代码 | 消息 |
| --- | --- | --- |
| 发往该会话的较早输入仍在等待接纳，例如正在预配的托管会话所预留的初始输入，或离线 `self_hosted` 会话所预留的初始输入 | 409 `conflict_error` | "Earlier input to this Session is still pending." |
| Turn 在当前状态下无法接受的输入，例如取消后提交的结果，或 Turn 已结束且未存储结果后提交的结果 | 409 `conflict_error` | "The Turn cannot accept this input in its current state." |
| 与该调用已存储结果不同的结果，无论其 Turn 结束之前还是之后 | 409 `conflict_error` | "The tool call already has a different result." |
| 相同 `Idempotency-Key` 搭配不同批次 | 409 `idempotency_conflict` | "This idempotency key was used with different input." |
| `call_id` 未标识该会话的任何函数调用的结果 | 400 `invalid_request_error`, param null | "Unknown pending tool call." |
| 为该会话的某个调用提交结果，但 `turn_id` 标识另一个 Turn、未知 Turn ID，或不是 Turn ID 的值 | 400 `invalid_request_error`, param null | "The tool call belongs to a different Turn." |
| 缺失、格式错误或来自其他作用域的会话 | 404 `not_found_error` | "Resource not found." |
| `openai_hosted` Environment 预配失败后提交新输入 | 409 `conflict_error` | "the hosted environment failed to provision" |
| `self_hosted` Environment 失败后提交新输入、Environment 失败时输入已在等待，或 Environment 已过期 | 409 `environment_unavailable` | "The environment is no longer available for new input." |
| 会话 harness 无法处理的消息，例如 Claude Code 上仅含空白的文本 | 400 `unsupported_or_invalid_configuration` | 见[仅含空白的文本](message-content.md#whitespace-only-text) |

`turn_id` 为空或 `call_id` 为空白时，会返回通用的 400 `invalid_request`。错误消息绝不会重复调用方输入或内部标识符。

## 创建会话时的初始输入 {#initial-input-at-session-creation}

`POST /v1/agents/sessions` 接受字符串形式的 `input`（一条文本消息）或用户消息数组，并执行与 events 端点相同的验证和接纳。

- `none` 必须提供初始输入（400 `invalid_request_error`，"conversation-only sessions currently require initial input"）；使用 `stream: true` 时，除 `self_hosted` 外的所有部署方式也必须提供初始输入（400，"streaming session creation requires initial input"）。这些检查在创建重试查找之前执行。
- 会话及其初始工作在一个事务中提交。对于 `none`，这包括首个 Turn 和输入 Item。对于 `openai_hosted`，输入会在 Environment 预配期间保留。对于 `self_hosted`，输入会保留并带有 `environment_connection` 操作，即使机器仍处于离线状态，创建操作也会返回。
- 预留的初始输入与后续输入具有相同的五分钟截止时间。如果截止时间在 Turn 启动前过去，会话将显示为 `failed`，且不会创建 Turn。
- 创建重试会返回原始会话，绝不会再次接纳其输入，即使之后已经产生新的 Turn（[创建重试](wire-semantics.md)）。

## 创建时流式传输 {#creation-streaming}

创建会话时使用 `stream: true` 会返回 201 和事件流，而不是 JSON。

1. 第一个事件是 `agent.session.created`，其中包含提交后读取到的已提交会话，与 JSON 201 正文相同。对于带初始输入的 `none`，其状态已经为 `in_progress`；对于 `self_hosted`，其中已经显示 `environment_connection` 操作。
2. 随后，流会从创建操作自身的位置开始，将创建过程中每个已提交事件恰好发送一次，因此即使执行速度很快，也不会漏掉最初的事件。
3. 流会在首次记录到 `agent.session.idle` 后立即结束；该事件会在 Turn 结束时，或输入预留停止等待时（到期、取消或失败）记录。流也会在任何 `agent.session.failed` 之后结束，并且绝不发送后续事件。`requires_action`、函数结果、恢复的工作和 `self_hosted` 连接会使流保持打开。预留会使流保持打开，直到 Turn 进入最终状态或预留结束。
4. 如果创建未接纳任何内容，流会在 `agent.session.created` 后立即结束。如果某个结束过程未记录任何事件，流会在已提交至该最终状态的事件之后结束；另一个客户端在该时刻之前提交的工作仍可能被发送。

在结束中的 Turn 捕获 Artifacts 期间预留的输入，可以启动创建流不会跟踪的后续 Turn。

使用相同 `Idempotency-Key` 和 `stream: true` 重试会返回 201，流中仅包含连接注释并立即结束：它不会接纳任何内容，也不会跟踪任何工作。要恢复丢失的会话 ID，请使用相同密钥和 `stream: false` 重复请求，然后读取会话、Turn 和 Item。断开连接只会停止观察器。后续 Turn 可通过 GET 流进行观察。

## 实时事件流 {#live-event-stream}

`GET /v1/agents/sessions/{session_id}/events` 从最新已提交事件处开始，只发送此后提交的事件。`Last-Event-ID` 会被忽略。事件会在各自事务提交后发布。

- **生命周期。** 流会跨 Turn 保持打开，并在 Turn 失败后继续存在。流会在会话被删除时，或在 [Environment 初始化失败](#environment-initialization-failure)产生终止 `agent.session.failed` 后结束；在该失败之后打开的流会保持打开。每 15 秒发送一次 keepalive 注释。
- **缓冲区。** Core 为每个会话最多保留 256 个事件和 64 MiB 事件，必要时还会额外容纳一个更大的事件。读取方落后于缓冲区时，会收到一个 `error` 事件，其 type 为 `server_error`、code 为 `stream_interrupted`，随后流会关闭。套接字写入阻塞五秒也会关闭流。执行过程绝不会等待读取方。
- **密钥重新检查。** 打开的流在空闲期间以及发送输出前，每秒最多重新检查一次原始 Project 密钥。撤销密钥或归档 Project 会关闭流，身份验证失败也会关闭流。重新检查使用正常的五秒身份验证超时，并且等待期间不会发送任何会话数据。已经发送的字节无法收回。
- **仅根工作。** 子 Turn 和子 Item 不发布会话事件；`agent.session.subagent.*` 事件和根协调 Item 会发布。请通过 [Subagent 资源](subagents.md)读取子工作。

### 事件规则 {#event-rules}

- 会话事件携带 `event_id`、`type`，以及该次状态转换时的 `session` 快照。Turn 事件携带 `session_id` 和 `turn_id`。不存在 Turn `waiting` 事件。
- 新 Turn 会在一个事务中依次发布 `agent.session.turn.created`、用户条目的 `item.added`、`agent.session.in_progress`，然后发布 `agent.session.turn.in_progress`。当一个批次包含多条消息时，后续消息对应的条目事件会排在会话活动之后。
- Turn 终止事件（`completed`、`failed`、`cancelled`）携带顶层的 `usage`，其值复制自当时的 Turn 快照；未知时为 null。其他事件省略此字段。
- `item.added` 和 `item.done` 始终携带 `output_index`；输入 Item 的该值为 null。函数结果只发出 `item.added`；`item.done` 用于代理输出。
- 助手消息遵循以下单一顺序：先发送 `content` 为空的进行中 `item.added`，再发送文本为空的 `content_part.added`，然后发送 `output_text.delta` 事件、`output_text.done`、`content_part.done` 和 `item.done`。首次观察时已完整的消息（例如结构化输出）会在一个 `output_text.delta` 中逐字节发送全部文本。完整文本会替换此前累积的增量。
- Codex 命令输出以 `agent.output.command_execution_output.delta` 流式传输，并携带命令的 Item ID 和输出索引。原生输出配额和文本转换规则适用，因此这些增量并非逐字节捕获的原始结果；完整 Item 才是权威结果。
- 已取消或失败的 Turn 会将其未完成的 Item 标记为 `incomplete`，并保留其部分内容。
- 函数调用会一直保留在 `required_actions` 中，直到 harness 应用其结果，或取消操作或 Turn 结束将其移除。重复通知不会发出新状态。

## 轮次与条目 {#turns-and-items}

Turn 和 Item 列表接受 `after`、`limit` 和 `order`（[列表规则](wire-semantics.md)）。游标是同一会话内的 ID。

**Turn。** 会话 Turn 路由只包含根 Turn，按创建时间、再按 ID 排序；在该路由中使用子 Turn ID 会返回 404。失败的 Turn 包含 `error: {code: "internal_error", message: "The execution could not complete."}`，绝不包含引擎原始诊断信息。管理员可通过[会话诊断](session-diagnostics.md)读取失败类别。

**Item。** Item 按首次观察时间、再按其在会话中的位置、最后按 ID 排序。更新和重试绝不会移动 Item，也不会更改其 `output_index`；`output_index` 是 Item 在该 Turn 输出 Item 中从零开始的位置，输入 Item 没有此值。读取操作使用已存储的历史索引，绝不会根据原生日志重新构建该索引。Item 列表包含仍在进行中或尚未完成的 Item。

| Item `type` | 内容 |
| --- | --- |
| `message` | 用户或助手内容片段。harness 报告阶段时，`phase` 为其阶段（`commentary`、`final_answer`），否则为 null |
| `command_execution` | 命令、报告的输出、退出代码、持续时间和工作目录 |
| `mcp_call` | 服务器和工具标识、参数、结构化结果或错误 |
| `function_call`、`function_call_output` | 关联的调用及其结果。结果始终携带 `output` 和 `error`；提交时省略这些字段则值为 null。存储的结果会保留提交时字段是否存在的信息。原生文件更改显示为 `apply_patch` 函数调用，更改内容作为参数，不会生成虚构结果 |
| `web_search_call` | 支持的操作字段（`search`、`open_page`、`find_in_page`、`other`） |
| `reasoning`、`agent_message` 和 Subagent 协调调用 | 见 [Subagents](subagents.md)。`agent_message` 没有状态；reasoning 状态可以缺失或为 null |

工具失败不会导致其 Turn 失败。会话的 Project 可以读取工具输出，其中可能包含该工具自己的诊断文本。

## 使用量 {#usage}

Turn 和会话的 `usage` 使用固定的 `TokenUsage` 字段：输入、缓存输入、输出、推理输出和令牌总数。`null` 表示未知，绝不表示零。

- **Turn 使用量**是 harness 为该 Turn 报告的最新完整快照。新快照会替换旧快照；重复快照绝不会累加。存储的快照在取消和 Worker 重启后仍会保留。
- **会话使用量**是所有根 Turn 均已结束（完成、失败或取消）且使用量已知时，各根 Turn 使用量的总和。只要有根 Turn 正在排队、运行或等待，它就为 null；一旦某个根 Turn 在没有使用量的情况下结束，它就会持续保持 null。Subagent Turn 不计入其中。
- **按 harness 划分。** Codex 会在 Turn 运行期间及其结束时报告测量快照；在任何使用量报告之前被中断的 Turn 会保持 null。Claude Code 和 MiniMax Code 不报告完整的公开明细，因此其使用量为 null。

使用量是对已报告测量值进行的尽力核算。它不是账单，Core 绝不会估算缺失的使用量。

## Environment 初始化失败 {#environment-initialization-failure}

当 Environment 初始化失败时，无论是 `openai_hosted` 沙箱还是 `self_hosted` 机器，都会在一个事务中记录该失败和以下三个事件，顺序如下：

| 事件 | 载荷 |
| --- | --- |
| `agent.session.environment.failed` | `error: {type: "environment_error", code: "environment_connection_failed", message: "The environment failed to connect."}` |
| `error` | `error: {type: "environment_error", code: "sandbox_error", message: <reason>, param: null}` |
| `agent.session.failed` | 该会话：`status: "failed"`，失败原因为 `error`，`required_actions: []`，失败时间为 `last_active_at` |

会话读取和列表操作会返回同一个会话，正在等待 Environment 的输入也会在同一快照中结算为失败。GET 流和创建流会在 `agent.session.failed` 之后结束。新输入会返回 409（[输入错误](#input-errors)）；该会话可以被删除。

[Environment 初始化契约](environments.md#initialization-state-and-failure)定义了固定的失败原因。

Core 自身的 `stream_interrupted` 错误事件携带 `type`、`code` 和 `message`，但不携带 `param`；与官方错误事件结构相同的 `error` 事件会携带 `param: null`。
