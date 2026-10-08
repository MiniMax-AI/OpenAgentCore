---
title: "添加 Harness"
source: contracts/agents-api/harness-onboarding.md
source_hash: 2436c12691cc2f2753f39df73a6f480f081c3ddef1936e40c7da12c09c36a35e
---

**Harness** 是一种运行模型和工具循环的原生代理引擎（Codex、Claude Code、MiniMax Code）。**Harness 适配器**将 Runtime 的 Executor 和 Turn 契约转换到该引擎的 SDK 或协议。本文档定义 Runtime–Harness 协议：适配器接口及其生命周期义务、注册、支持声明和验收。

从两个入口开始：

- [`internal/harnessconfig/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/harnessconfig/harness.go)：共享模型配置契约（声明和准备）。
- [`agent/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/harness.go)：执行生命周期、扩展契约和注册方法。

## 所有权 {#ownership}

```text
Core: Session, Turn, immutable configuration, durable events
                         |
              common Runtime protocol
                         |
Runtime: Executor preparation, reuse, idle expiry, recovery
                         |
               Harness adapter package
                         |
          native SDK, process or connection
```

| 组件 | 职责 | 位置 |
| --- | --- | --- |
| Core | 公共 API、控制权、持久状态、调度和配置快照 | `services/core` |
| Runtime | 经身份验证的连接、共享能力准备以及通用 Executor 和 Turn 生命周期 | `apps/daemon/internal/dispatch` |
| Adapter | 原生配置、资源、API 调用、事件转换和限制 | `apps/daemon/internal/agent/<kind>` |
| Harness | 原生模型和工具循环以及历史记录 | 锁定版本的 SDK 或可执行文件 |
| 声明 | Harness 的支持范围，Core 和 Runtime 据此准入每个选择 | `internal/harnessconfig/<kind>` |
| 注册 | 适配器声明、已安装视图和该安装收窄后的支持 | `apps/daemon/internal/agent/<kind>/declaration.go`；`apps/daemon/internal/cli/agent_host_linux.go` 中的静态列表 |

Environment 提供执行资源。受管 E2B、Docker 和 microsandbox 机器以及应用自有机器在预配和连接方式上有所不同；它们都把沙箱提供给 Linux agent host，由 agent host 在沙箱的[视图](#run-in-an-agent-host-view)中按同一契约运行每个 Harness。只有在 Runtime 加载绑定的已安装快照后，原生工厂才会收到能力（[capability preparation](environments.md#runtime-capability-preparation)）。模型 Provider 提供模型通信设置，而不负责 Turn 调度或原生进程所有权。

## 步骤 {#steps}

1. **锁定原生来源。** 记录上游包版本和源修订版本，并在适配器旁记录原生入口点。
2. **实现适配器**，位置为 `apps/daemon/internal/agent/<kind>`：实现一个视图，其 `ViewExecutorFactory` 准备 `Executor`，以及 `Turn`（[required interfaces](#required-adapter-interfaces)、[lifetimes](#executor-and-turn-lifetimes)、[view](#run-in-an-agent-host-view)）。复用共享的进程、凭据和配置辅助函数。
3. **声明支持并注册。** 在 `internal/harnessconfig/<kind>` 中声明支持并添加一个目录条目（[declare support](#declare-support)），然后在适配器中声明 kind，并将其添加到 `apps/daemon/internal/cli/agent_host_linux.go` 中 agent host 的静态列表（[register the adapter](#register-the-adapter)）。
4. **打包原生先决条件。** 提供适配器的安装描述，并将 Harness 加入 agent-host 镜像（[native installer participation](#native-installer-participation)）。
5. **启用并选择引擎**，通过 `core.harnesses` 设置和 [Harness selection](model-execution.md#harness-selection) 完成。
6. **认定其资格**（[qualify the adapter](#qualify-the-adapter)），并将每项原生差异记录到[覆盖台账](index.md)。

实现强制的文本生命周期，并明确处理每一种扩展。逐个认定受支持扩展的资格；未认定资格的扩展返回 `agent.ErrUnsupportedOperation`，且不会产生原生副作用。原生取消可能要求退役而非复用：`Reusable=false` 会携带原因，调用方必须确认 `Executor.Close`。不要为了适配测试辅助函数而强制复用，也不要将适配器的原生限制复制到共享 Core 协议中。

## 架构规则 {#architecture-rules}

- Codex、Claude Code 和未来的 Harness 地位平等。通用 Runtime 线协议以及 Executor 和 Turn 接口负责生命周期、输入回执、取消、恢复和资源访问；每个适配器保留其原生实现以及模型和工具循环。
- 新引擎需要提供适配器、其声明、注册以及经过独立验证的部署。它不得在 API 处理程序、持久化、调度、调度器或 Environment Provider 中添加按引擎名称分支的实现，也不得为契约已经涵盖的能力添加处理程序、存储表、调度器、事件投影器或模型循环。
- 将必需的生命周期声明、扩展接口和注册方法保留在 `agent/harness.go` 中。结果类型、错误和 Registry 存储可以保留在聚焦的文件中。
- 使用现有的 `proto.Declaration` 和 `proto.SupportedAgentKind` schema。不要添加第二套能力描述符或组合式可选接口。
- 接入不要求功能完全一致。Harness 不必匹配彼此的可选功能，并且注册时不强制要求 MCP、函数、图像或详细程度控制。验证通用生命周期义务，并对每个声明的操作使用相同的公共断言。缺少声明或扩展实现会阻止接入；原生差异不会。
- 静态声明是支持边界。未知 kind 以关闭方式失败，Core 拒绝扩大声明的心跳。Schema 有效性、声明和可用 Runtime 是相互独立的检查。
- 绝不能将已接受的参数等同于已实际应用的原生行为。

## 必需的适配器接口 {#required-adapter-interfaces}

[`agent/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/harness.go) 是接口入口。必需的生命周期包括 `ViewExecutorFactory`、`Executor`、`Turn` 和 `TurnSettlement`。`Turn` 是一个接口：`Cancel`、`CancellationOutcome`、`SteerWithReceipt`、`SubmitFunctionResult` 和 `AwaitSettlement`。必需方法必须履行其原生义务；返回 Unsupported 并不构成对取消、回执、结算或清理的实现。适配器不支持的操作返回 Unsupported，由能力声明而不是方法决定 Runtime 是否调用它。所有接口都使用中立协议类型。

视图的 `ViewExecutorFactory` 接收一个 `agent.PrepareRequest`：`execution_prepare` 携带的 Session 配置、Registry 按 kind 的声明一次性准备好的模型配置（`Prepared`）、Session 的原生状态键（`StateKey`），以及由 Environment owner 填写的 Environment 工作区和已安装 Capabilities（`WorkspaceRoot`、`CapabilityRoot`、`Skills`、`MCP`）。适配器只从 `Prepared` 获取模型、提供商和原生参数，从不自行解析 `model` 或 `model_provider`。Turn 的 Run ID 和输入通过 `Executor.StartTurn` 传入。

例如，Codex 适配器保留其 app-server 和 thread，Claude 适配器保留一个流式 Query，MiniMax 适配器保留其 ACP 连接和原生 session。它们都公开相同的 Executor 和 Turn 契约。原生回调和资源保留在适配器内部；Runtime 负责准入、空闲过期和替换。取消通过 `Turn.Cancel` 精确定位到目标 Turn，适配器则向 Runtime 提供原生完成证据。

| 接口或契约 | 必需处理 | 义务 |
| --- | --- | --- |
| `ViewExecutorFactory`、`Executor.StartTurn`、`Executor.Close` | 真实实现 | 在没有模型输入的情况下准备；保留失败或不确定资源的所有权；确认清理 |
| `Turn.Cancel`、`CancellationOutcome`、`AwaitSettlement` | 真实实现 | 取消精确的 Turn，保留已观察结果，并独立于取消请求确认结算 |
| `Turn.SteerWithReceipt` | 真实实现 | 区分完整写入与原生应用回执；保留重试身份 |
| `Turn.SubmitFunctionResult` | 真实实现或 Unsupported | 匹配原生调用和结果身份，并确认应用 |
| 图像、MCP、结构化输出和 Subagent 观察 | 明确作出能力决策 | 保持每项操作的协议语义；在提交前拒绝不受支持的输入 |

每个适配器的 `contracts.go` 在编译时断言其实现了 `agent.Executor` 和 `agent.Turn`。不要嵌入会让新方法看起来已经实现的默认实现。通用完整性检查遵循已编写的 Harness 目录，并拒绝 `agent` 中的任何其他导出接口。

对于设计层面的拒绝，请直接实现该方法：

```go
func (s *Session) SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error {
    return fmt.Errorf("%w: native public function tools are not qualified", agent.ErrUnsupportedOperation)
}
```

原因必须是固定的安全字符串，绝不能是已提交内容、凭据或原始原生诊断信息。Unsupported 保证不会产生原生副作用，也不表示操作成功且为空。安装不可用、未知调用 ID、原生失败和不确定结果应保留各自的错误和所有权。nil `Turn` 仍表示没有提交任何输入，并且输出归调用方所有；绝不能将其用作 Unsupported 标记。

线协议请求不携带工作目录。Environment owner 将 `local_environment.workspace_directory` 与 Environment 的工作区进行核对，并通过 `PrepareRequest.WorkspaceRoot` 向 Harness 提供该目录；必须在该目录中运行原生 Harness。

工作区读取、写入、输出导出和只读 preparation 属于 Session 的 [Environment owner](../../../docs/zh/runtime-protocol.md#session-assignments)，不属于 adapter。adapter 不实现其中任何操作。其声明中的 `LocalEnvironment` 和 `EnvironmentNone` 表示其 Executor 能运行的内容，`agent.Registry.Register` 将二者与 Runtime 的 owner 所提供的内容（`agent.EnvironmentSupport`）组合一次，仅在 owner 提供时保留。组合后的 `LocalEnvironment` 同时准入 owner 的工作区读取、只读 preparation 和输出导出。一份声明适用于该安装的每个 Executor。

声明说明 Harness 支持的公共组合，心跳将其收窄到该安装；二者都不能替代 schema 验证或 Project 授权。原生行为测试必须与声明一致。已宣称但返回 Unsupported 的操作属于契约违规，既不是成功，也不能作为重放的依据。

## Executor 和 Turn 生命周期 {#executor-and-turn-lifetimes}

| 生命周期 | 所有者 | 结束条件 |
| --- | --- | --- |
| Environment 分配 | Sandbox Provider | 显式回收，并与 Runtime 执行协调 |
| Runtime 连接 | Runtime 传输层 | 断开连接或被较新的连接替换 |
| 已安装能力快照 | Runtime | 其 Environment 被回收；绝不因 Executor 关闭而结束 |
| Session Executor | Runtime | 空闲过期、关闭或确认失效时执行 `Executor.Close` |
| Turn | 由 Runtime 跟踪的适配器 `Turn` | `AwaitSettlement` 确认结算完成 |

Session 在其已连接的 Runtime 中拥有一个可复用的 Executor；Turn 拥有一次输入执行、其输出流和其取消操作。`agent.ExecutorFactory` 在没有模型输入的情况下准备固定配置，而 `Executor.StartTurn` 创建新的 `agent.Turn`，不替换健康的原生资源。正常完成仅结算 Turn。`Executor.Close` 在空闲过期、Environment 关闭或确认失效时释放原生资源；它既不释放 Environment 分配，也不释放工作区。Core 不保留第二套 Executor 缓存。相同的生命周期适用于托管、自托管和 `none` 放置方式。

**绑定。** Runtime 将其 Executor 记录绑定到 Session、Environment、连接和不可变执行配置。恢复身份和先前 Turn 恢复标志是连续性断言，而不是配置更改。提供的原生身份必须与保留的所有者匹配；当需要现有历史时，恢复绝不能启动新的根。配置冲突属于错误，而不是热切换。连接丢失会让其所有者和句柄退役；旧计时器、输出和取消操作不能影响替代对象。

**每 Turn 状态。** 每个 Turn 都会获得全新的包装器、输出通道和回执状态。引导和函数结果均属于该 Turn。原生回调必须在异步工作开始前捕获来源 Turn，因此迟到事件绝不会被归到当前活动的 Turn 上。原生进程、query 或传输连接、固定能力配置和原生 session 身份均属于 Executor。不要重置已完成的 `sync.Once` 值，也不要复用旧 Turn 对象。

**开始。** `StartTurn` 返回 nil Turn，保证没有提交任何原生输入，也没有保留输出通道；随后由 Runtime 关闭该通道。一旦输入可能已经提交，即使同时返回错误，也必须返回非 nil Turn：该 Turn 拥有恰好一次的输出关闭权，并在结算前持续接受跟踪。未知输入绝不能重放。明确的 `executor_unavailable` Start 拒绝允许进行一次通用恢复尝试，但只能在此前 Executor 已关闭且未提交输入之后进行；Runtime 会重新检查同一物理对端和当前授权。

**取消和结算。** `Turn.Cancel` 仅以目标 Turn 为对象，不会关闭健康的 Executor。`AwaitSettlement` 同时适用于自然完成和取消。成功意味着输出已无法再写入，并且该 Turn 的原生事件、输入、函数和子任务均已结算。原生完成或取消确认独立于资源退役：关闭传输层无法提供缺失的原生终态或操作回执。

- `Reusable=true` 还要确认原生所有者能够接受下一个 Turn。`Reusable=false` 要求提供原因，并在之后确认 Executor 已关闭。
- 错误表示结算尚未确认，既不释放所有权，也不释放容量。调用方截止时间只会停止等待，不会停止受跟踪的清理。必须串行重试同一个清理目标；清理失败会阻止替换并保留其资源槽位。
- `Executor.Close` 独立于 Turn 结果确认资源退役：不可变的 Turn 错误不得阻止在其工作和输出已经停止后关闭原生传输层。
- 结算必须包含所属的后台工作，并在失败后保留精确的原生清理目标。原生终止由适配器负责；仅有批量清理确认并不能证明已达到静默状态。
- 每个 Turn 都实现 `CancellationOutcome`。快照保留已观察到的原生身份和 Usage，并在取消后仍可读取。缺失的证据保持未设置；空的 `DonePayload` 表示未观察到任何内容，而不是表示取消成功或不受支持。读取快照不会等待结算。
- `Turn.Cancel` 请求取消；输出关闭表示拆卸开始。Turn 结算仍需要 `AwaitSettlement` 和所需的任何 `Executor.Close`；取消请求成功或其快照都不能替代这些等待。

**Runtime 在 Turn 前后执行的工作。** 一个输出消费者会在原生 Start 之前启动，耗尽有界的 64 帧通道，并将终态观察保留到 Start 发布、Turn 结算和已准入操作回执完成为止。正常完成绝不调用 Cancel。输入和函数准入会在结算前关闭；已准入的操作会持有其屏障，直至原生回执和出站确认完成。Runtime 会在等待该屏障之前向 Turn 发送取消，因为已写入的输入可能需要原生中断才能生成回执。Runtime 会汇合原生结算、所需的已确认 Executor 关闭、输出耗尽和所有已准入操作，然后应用确认或执行复用，之后才会转发 Done 或已应用的取消回执。Close 失败可以报告失败，同时保留同一 Run 和未完成操作以供重试；已关闭的调用方等待无法凭空生成已应用输入回执。Runtime 会在发布 Done 前提交原生连续性状态并释放旧 Run 的准入，因为接收方可能立即启动另一个 Turn；迟到的终态发送失败属于旧 Run，不能使已拥有 Executor 的后继对象失效。连接关闭负责传输丢失清理。结算等待时间为十秒，回执发送预算为五秒；超时不能证明已达到静默状态。

## 事件、输入和可选能力 {#events-inputs-and-optional-capabilities}

使用 [`internal/agentdaemon/proto`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/internal/agentdaemon/proto) 处理中立请求、事件和回执。每个 Turn 只按顺序发出带有其 Run ID 的自身事件，并产生一个终态结果。原生 ID 和 usage 必须来自观察，绝不能虚构；缺失的度量值表示未知，而不是零。

每条助手消息都携带其原生消息 ID。先发出带该 ID 的 `output_message` `in_progress`，再将每个文本片段作为在 `item_id` 中指向它的 `delta` 发出，最后发出带消息完整文本的 `output_message` `completed`（[消息顺序](../../../docs/zh/runtime-protocol.md#message-families)）。原生协议没有消息结束标记时（例如 ACP），在下一条消息开始或 Turn 结束时完成该消息。`Done` 和 `CancellationOutcome` 不携带回答文本；因取消或失败而未结束的消息保持未结束。

初始输入和引导使用有序的 `proto.MessageInput`。必须保持用户消息顺序和内容顺序。仅支持文本的适配器通过 `TextOnly()` 拒绝图像，而不是丢弃图像；图像适配器在原生环境中转换每个部分，并且只有在其所有消息均已应用后才确认活动批次。成功传输写入与确认原生应用是不同的事件。只能恢复绑定到 Session 的精确历史；缺失、含糊或外部历史会在新的模型输入之前导致失败。设备身份不代表原生 session 所有权。

### 必需操作和扩展操作 {#required-and-extension-operations}

公共文本路径要求持久化 Turn、已应用输入回执、有序观察、取消，以及执行已禁用的执行控制。没有原生工具的引擎可以保证这些工具不存在；具有工具的引擎在收到要求时必须实际禁用它们。接受某项配置不能证明其已得到执行。

MCP、公共函数、延迟函数发现、结构化输出、图像输入、详细程度控制和其他可选操作不必匹配另一个引擎。声明不支持的内容（组合声明为 `Conflicts` 中的一对）并记录差距；绝不能宣称某项能力来绕过选择。

- 结构化输出：读取 `ExecutionControls.OutputFormat`，并通过 Message 契约发布已确认的原生输出（[execution tools](execution-tools.md#structured-output)）。原生 SDK 以 binary64 读取 JSON 数字时，声明 `Binary64OutputSchema`。
- 图像：声明 `MessageImages` 和 `FunctionResultImages`，以及函数结果是否准入图像 URL 和失败结果中的图像（[message input](message-content.md)）。

### MCP 来源和原生限制 {#mcp-origin-and-native-limits}

在 `MCPOrigins` 中声明受支持的公共来源，在能力中声明 HTTP、bearer 和必需初始化支持，并将原生限制声明为数据：`MCPAllowedTools`、`ReservedMCPLabels` 以及 `MCPLabel` 和 `MCPToolName` 模式。`proto.ValidateSelection` 将它们与来源和放置位置一起检查，适配器不再重复检查。

使用 `agent.ResolveMCPBindings` 处理公共声明和已安装声明，并保留来源、凭据权限、`null` 与空允许列表之间的区别以及必需启动过程。不要将令牌复制到原生 profile 中，也不要将服务请求重新解释为 Environment 请求。拒绝不受支持的原生策略，而不是将其丢弃。遵循 [MCP origin contract](environments.md#public-mcp-connection-origin)，并对每个宣称的组合执行公共客户端、失败、取消和冷恢复资格认定。模型能力与 Harness 传输支持相互独立；绝不能从模型名称推断模型能力，也绝不能静默降低输入质量。

### Subagent 观察 {#subagent-observations}

支持 Subagent 读取的 Harness 必须实现[中立观察契约](subagents.md#adapter-contract)。它通过经身份验证的 Run 报告经过验证的子项身份、生命周期影响以及所属的 Turn 和 Item 历史，并通过真实执行认定这些事实，且不使用额外路由、存储分支或 Harness 专用调度器。明确报告不受支持的原生事实；完成子任务并不等于关闭其 Subagent。原生后台工作的所有权必须保持到结算和取消完成为止。

## 注册适配器 {#register-the-adapter}

注册是静态的，并且需要构建。从 `apps/daemon/internal/agent/<kind>/declaration.go` 导出一个 `agent.Declaration`，然后将其添加到 [`cli/agent_host_linux.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_host_linux.go) 的 `harnessDeclarations` 中。声明包含 kind 及共享模型 `Configuration` 的声明中的能力、该 `Configuration` 和 `Discover` 函数。发现过程接收诊断写入器，负责原生配置和可用性检查，并返回已安装的 `agent.Runtime` 及其描述符和视图声明。未配置适配器时返回 nil；已配置的前置条件失败时，返回不带视图的不可用描述符。将版本门控和视图选择条件保留在适配器内部；它们只能清除支持。

[`cli/agent_host_linux.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_host_linux.go) 以 `RegisterKind` 和 `RegisterView` 注册每个带视图的已发现 Runtime。随后 agent host 通过 `Registry.Register` 为 dispatch 注册每个这样的 kind，该方法把声明与 agent host 提供的 Environment 组合，再以 `RegisterExecutor` 安装 agent host 自己的 Executor 工厂；该工厂构建 Session 的视图，并调用视图的 `ViewExecutorFactory`。

| 顺序 | 方法 | 注册内容 |
| --- | --- | --- |
| 1 | `RegisterKind(proto.SupportedAgentKind, harnessconfig.Configuration)` | Kind、可用性、版本、`AgentKindCapabilities` 和模型配置；它将模型配置的声明收窄到这些能力，遇到扩大时 panic。它会重置其他注册项，因此必须首先调用。 |
| 2 | `RegisterView(kind, agent.View)` | 来自 `Runtime.View` 的视图声明。`View.Validate` 失败时以 `ErrInvalidView` panic。其 Executor 工厂接收 agent host 的 Executor 工厂已准备好的请求，并执行[网关规则](#endpoints-and-proxy)。 |
| 3 | `RegisterExecutor(kind, agent.ExecutorFactory)` | 供 dispatch 调用的 agent host Executor 工厂。它只为收窄后的声明所准入、且模型配置能够准备的请求运行，并收到已设置 `Prepared` 的请求。 |

`Runtime.View` 声明 Harness 如何在 agent-host Session 视图中运行，详见[在 agent-host 视图中运行](#run-in-an-agent-host-view)。每个适配器都显式设置它；`View: nil` 表示 agent host 拒绝该 kind，`Registry.ResolveView` 返回包装 `ErrUnsupportedOperation` 的错误。`TestPublicHarnessContractDeclarations` 要求每个声明都包含该字段。

每个 `proto.AgentKindCapabilities` 字段都必须显式设为 `proto.CapabilitySupported` 或 `proto.CapabilityUnsupported`，即使 Harness 不可用也是如此。`proto.CapabilityUnspecified` 无效：零值和省略字段绝不表示 Unsupported。安装探测可以使用 `proto.CapabilityFromBool` 清除单个字段；绝不设置静态声明不具备的支持。可用性通过 `SupportedAgentKind.Available` 单独表示。注册会在更改 registry 之前验证完整声明；线协议会为每个字段携带显式布尔值，因此省略字段和 null 字段均无效。添加新字段时，每个生产声明都必须作出决定。Runtime 使用者应调用 `IsSupported()`，并在原生操作前拒绝不受支持的请求；接口断言用于验证实现，绝不表示支持。每个声明都必须与针对该安装验证的行为一致；[Core–Runtime protocol](../../../docs/zh/runtime-protocol.md#capability-declarations) 负责声明的传输方式和冻结方式。

每个可用 Harness 都无需声明即实现共享 Turn 生命周期（包括持久的 `SteerWithReceipt` 输入和由 `contracttest.TextLifecycle` 检查的 Turn 结算契约）、类型化的 `execution_controls` 和工具观测。在某个平台上无法满足这些要求的 Harness 在该平台报告 `Available` 为 false。`FunctionTools` 准入 `SubmitFunctionResult`。`LocalEnvironment` 准入带 `workspace_read_only` 的 `execution_prepare`，由 Environment owner 就绪并提供读取，不调用 Executor 工厂。

可运行的仅测试示例 [`testdata/onboarding/main.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/testdata/onboarding/main.go) 会以 `mcode` 类型注册一个仅支持文本的合成 Harness，因为 Core 只接纳[目录](harness-catalog.md)中的 Harness。它展示 Session 所有的 Executor、全新的 Turn、持久化引导、取消和历史绑定，并且绝不会发布。

## 声明支持 {#declare-support}

Core 会识别[内置 Harness 注册项](harness-catalog.md)。向 `internal/harnessconfig/builtin/catalog.json` 添加一个条目，包含公共 `kind`、显示 `label` 和 `internal/harnessconfig` 下的模型 `configuration` 包，然后运行 `make generate-harness-catalog`。它会生成模型配置 registry、客户端标识符和显示名称以及注册参考；公共输入验证器读取生成的 registry。`make openapi` 从同一目录派生 Harness 枚举，因此不要在 DTO 标签或路由注解中添加手写枚举。`make check-harness-catalog` 会拒绝过时的投影。

`internal/harnessconfig/<kind>` 中 `Configuration()` 的 `Declaration` 就是 Harness 的支持范围：一个 `proto.Declaration`，包含其 `AgentKindCapabilities`、消息、图像、MCP 和输出 schema 限制，以及 `Conflicts` 中它能单独支持但不能同时支持的功能对。它说明适配器的最大支持范围，并且是唯一来源：Core 通过 `builtin.Registry()` 读取它，适配器的 Runtime 描述符也从它开始。发现过程和 Environment owner 只能清除支持，Core 拒绝扩大该声明的心跳。只声明 Harness 之间的真实差异；对每个 Harness 都成立的规则属于 `proto.ValidateSelection` 中的通用检查。

`proto.ValidateSelection` 是对声明的唯一检查。Core 在创建或更新已保存 Harness 的 Agent、创建 Session 以及准入输入和函数结果时应用静态声明，在设备选择和认领 Turn 之前应用 Runtime 收窄后的声明。Runtime 在准入 `execution_prepare` 时应用它，并在 Environment owner 解析出 Environment 已安装的 MCP 服务器后连同它们再次应用，均早于任何 Executor 工厂运行。拒绝返回 400 `unsupported_or_invalid_configuration`，并以配置路径作为 `param`。Runtime 事实（例如缺少二进制、原生历史或文件系统就绪状态）仍是适配器准备失败。

每个 Runtime 声明都引用同一个 `internal/harnessconfig/<kind>.Configuration()`，并负责其原生工厂和探测。目录不能声明某台机器的可用性，也不存在动态插件加载器。

`internal/harnessconfig/builtin` 中的共享选择夹具为每条声明规则各保存一个接受用例和一个拒绝用例。运行这些夹具，并运行 `services/core/tests/integration` 中的公共接入测试以覆盖准入和 Runtime 调度。

## 原生模型配置 {#native-model-configuration}

[`internal/harnessconfig/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/harnessconfig/harness.go) 负责共享配置声明和纯准备契约。每个适配器在 `internal/harnessconfig/<kind>` 中提供一个 `Configuration`，供 Core 组合和 Runtime 的 `RegisterKind` 使用。Executor 路径和视图路径都会在产生原生副作用之前通过该声明进行验证，而 Registry 包装器会将声明与工厂保留在一起。线协议对象是 `proto.HarnessConfig`。[Model execution](model-execution.md#native-model-parameters) 列出了每个 Harness 接受的字段。

每个请求都指定非空的 `model` 和一个 `model_provider`；缺少任一项的请求，包括显式 null 或空值，都会在产生任何原生副作用之前被准备阶段拒绝。因此空声明不接受任何请求。未知协议格式和重复协议声明会导致注册失败。

声明中的有序 `protocols` 列表是接受协议及默认协议（第一个条目）的唯一来源；它还为 Core 的配置支持描述符提供数据，Core 和 Runtime 通过它拒绝不受支持的组合。适配器通过原生配置和[凭据网关](./model-execution.md#credential-gateway)连接；它们绝不引入自己的模型 API 代理或协议转换器、第二套模型能力 registry，也不会从模型名称推断能力。Claude 的私有 bridge 接收编译后的原生选项，并且只执行结构检查，而不是声明规则的第二份副本。

## 认定适配器资格 {#qualify-the-adapter}

开始前，记录操作集、预期结果、排除项和停止条件。当其声明的操作通过时，资格认定即结束；它不会扩展为匹配另一个 Harness 的功能列表。

1. **契约测试。** 在名为 `TestSharedTextLifecycle` 的测试中，使用适配器准备好的 Executor 和确定性的原生夹具调用 `agent/contracttest.TextLifecycle`；`claudesdk/executor_test.go` 是参考实现。它检查独立的 Turn 流、原生所有者和历史连续性、持久化写入与应用回执、过期取消，以及取消后的健康继续执行。`make check-runtime-contract` 会将它与共享线协议、gateway、传输层和调度器测试、声明完整性检查以及MiniMax Code 的 `TestUnsupportedExtensionsHaveNoNativeEffects` 一起运行。适配器测试还覆盖两个普通 Turn 共享一个原生进程或连接和历史、取消后执行另一个 Turn、过期取消和迟到事件、原生退出、清理失败、输入写入与应用回执、未知结果，以及每 Turn 新鲜的 usage、函数、输入和子项观察状态。必须说明夹具是受控夹具还是真实 Provider。
2. **共享集成。** `TestThirdHarnessPublicOnboarding` 让合成 Harness 通过公共 Session 和输入准入、Worker 设备选择、真实 WebSocket gateway、daemon Registry 和 Router、中立事件以及持久化终态投影运行。它以 `mcode` kind 注册，因此 Core 按 MiniMax Code 的声明准入它，并检查已应用输入回执、已保存原生身份、继续执行、取消、不受支持的可选请求以及缺少强制 Runtime 支持。该夹具没有工作区、MCP 或公共函数，其注册仅保留在测试本地。它证明的是集成路径，而不是原生执行。
3. **真实验收。** 使用锁定的官方 Python SDK 和针对 Core 的原始 HTTP、真实 Provider API、原生 Harness 以及专用数据库。验证初始执行、热后续执行、取消以及带继续执行的重启；记录原生所有者身份以及相同条件下的冷启动和热运行时间。对于工作区放置方式，还要验证 Files 和 Artifacts、工作区身份、公开响应中未出现凭据，以及外部历史会被拒绝。`services/core/tests/official_hosted_functions_native.py` 保存共享函数断言：成功和错误、原生文件输出和公共 Artifact 字节、重启后的同历史继续执行、外部结果拒绝以及待处理调用取消。合成运行或失败运行绝不计入。原生 Harness 的工作区与能力验收使用 [Qualify the view](#qualify-the-view) 中的 agent-host 测试。公共 API 的真实模型验收仍须覆盖模型 Provider 协议、MiniMax Code 文本、消息图像、带图像的函数结果、结构化输出、延迟函数发现、禁用 Web 搜索以及程序化工具调用；仅通过 view 测试不能证明这些公共 API 行为。
4. **回归。** 现有 Harness 必须继续正常工作。先运行定向测试，然后运行 `make check`；API 更改后运行 `make openapi`，查询更改后运行 `make sqlc-generate`。
5. **审查。** 遵循 [blind review workflow](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#review)。

Environment 验收使用 `services/core/tests/official_environment_{templates,setup,skills,plugins,plugin_mcp,composition,initial_files,network,skill_references}.py`。对于组合式准备，请更改 Skill 默认值和 Template，删除源文件，重试并重启；验证冻结字节、一次 setup 执行和 MCP 取消。`official_hosted_structured_native.py` 覆盖托管结构化输出。随每项验收结果记录精确源修订版本、原生版本和命令。

将 Provider 密钥保存在私有操作员文件中，绝不能放入提交或日志。相对于 `apps/daemon/internal/agent` 的现有定向测试如下：

| 边界 | 测试 |
| --- | --- |
| Codex 复用、取消和未确认清理 | `codex/executor_test.go`、`terminal_cleanup_test.go` |
| Codex 输入回执和严格恢复 | `codex/function_write_receipt_test.go`、`function_receipt_test.go`、`resume_test.go`、`recovery_test.go` |
| Claude 输入所有权、取消和准备清理 | `claudesdk/executor_test.go`、`cancellation_test.go`、`preparation_test.go` |
| MiniMax 取消退役、Start 失败和清理重试 | `mcode/executor_test.go`、`executor_backpressure_test.go` |
| MiniMax 原生历史绑定 | `mcode/session_test.go` |
| 无原生副作用或伪造结果的明确拒绝 | `mcode/unsupported_test.go` |

## 原生安装器参与 {#native-installer-participation}

适配器从自身包中的 `installation.go` 提供 `agent.Installation`：已注册 kind 和激活环境。agent host 使用该声明激活打包的 Harness。适配器负责原生布局；必须在 Linux agent host 上验证打包内容和执行。原生内容缺失或不兼容时必须失败；绝不会在 Turn 期间自行安装。自托管安装器不携带 Harness 或 Node.js。

`deploy/distribution/AgentHost.Dockerfile` 把每个 Harness 安装在各自的目录中，并列入镜像清单 `/opt/oac/harnesses.json`。`agent.ManifestEnvironment` 通过 `Installation.Environment` 激活它。将每个新增 Harness 加入该镜像和清单，并使用共享的[镜像构建](../../../docs/zh/maintainers.md#runtime-images-and-helpers)与[视图验收](#qualify-the-view)流程。

## 原生进程所有权 {#native-process-ownership}

daemon 的 `clirunner` 在 Linux 上让每个原生子进程在自己的进程组中启动；其他平台返回其类型化的不支持错误。显式取消和父上下文取消共享 TERM 宽限期（默认为三秒）以及有界的 KILL 升级过程。当直接进程退出时，内部回收器也会清理进程组的剩余成员，即使某个后代进程仍保持 stdout 打开；在取消过程中，主进程退出后，存活的后代进程仍会保留剩余宽限时间。daemon 的 `stop` 命令最多等待十秒以确认关闭，这涵盖该宽限期以及之后的管道和所有者清理。

所属输出管道在主进程退出后仍可读取。消费者在调用 `Wait` 之前耗尽 stdout 和 stderr；`Wait` 会汇合缓存的进程结果并关闭读取器。`Done` 报告主进程回收和进程组清理信号；它不是原生执行回执，也不是历史已持久化的证据。SDK 适配器会结算每个 Turn，并在发布完成状态前耗尽其观察结果；Executor 关闭还会关闭 Query 并等待原生子进程。进程组用于生命周期监管，而不是隔离或遏制离开进程组的后代进程。

适配器以启动用户的权限无人值守运行原生工具，Harness 从不询问人类。Codex 使用批准策略 `never` 和完全访问权限，在该策略下 Codex 自行处理 MCP elicitation，不会发给客户端；适配器还禁用其阻塞式 `request_user_input` 工具。Claude 通过适配器的工具回调运行，回调直接允许或拒绝、不会询问，使用原生 `default` 权限模式并禁用 SDK sandbox。MiniMax 绕过权限并禁用 sandbox；适配器禁用 `askUser`、不声明 elicitation，并以 ACP `cancelled` 结果答复 `session/request_permission`，MiniMax 将其视为拒绝。原生权限或问题请求都不会到达 Core：需要人工介入时通过 function 工具完成，Session 读取 [`requires_action`](./sessions-events.md#session-status)，由应用提交 function 结果。不要添加权限 profile、bubblewrap 包装器或原生 sandbox 设置；每种 Environment 来源都只有一条执行路径。资源路径属于操作员配置，而不是权限边界。

网络准入遵循 [Restricted network](environments.md#restricted-network)。

## 在 agent-host 视图中运行 {#run-in-an-agent-host-view}

agent host 在沙箱之外、在每个 Session 一个的视图中运行 Harness。视图通过[文件访问协议](../../../docs/zh/file-access-protocol.md)在 `/` 呈现沙箱的文件，在 `/.oac` 下呈现 Harness 自己的文件。Harness 未声明为本地的程序通过[进程协议](../../../docs/zh/process-protocol.md)在沙箱中运行。网络只有 loopback，Session 的凭据网关在其上监听。适配器在 `Runtime.View` 中声明其 Harness 所需的内容，agent host 根据该声明和 Session 构建每个视图。支持与否由该声明字段决定：没有 View 的 kind 会以 `ErrUnsupportedOperation` 被拒绝。

### 声明 {#the-declaration}

| 字段 | 声明内容 |
| --- | --- |
| `Closure` | 以只读、可执行方式呈现在 `/.oac/<Name>`（`ViewMount.Path`）的主机目录 |
| `Overlays` | 以只读方式呈现在视图路径上的可信主机文件或目录；`Exec` 使其可执行 |
| `Masks` | 以空且只读方式呈现的视图路径，设置 `Dir` 时呈现为目录 |
| `LocalExec` | Harness 进程树在本地执行的每个视图路径 |
| `Shims` | `/.oac/bin` 上的名称；每个名称在沙箱中运行 Environment 工具 `PATH` 上的同名程序 |
| `ShimPaths` | 绑定 shim 的视图路径；每个路径在沙箱中运行相同路径 |
| `ForwardEnv` | 在沙箱中运行的进程保留的 Harness 变量 |
| `Proxy` | `ViewProxyEnv` 或 `ViewProxyNone` |
| `Executor` | 在 Session 的视图中准备其 Executor 的 `ViewExecutorFactory` |

`View.Validate` 在不访问主机的情况下检查声明：

- 视图路径和主机路径都是干净的绝对路径；
- closure 名称是单个路径分量，且不是 `bin`、`home` 和 `run`，这三个由 agent host 用于 shim、Session home 和进程 relay；
- shim 路径、overlay 和 mask 互不重叠，也不与 `/` 重叠，并且不进入视图自己构建的树：`/.oac`、`/proc` 和 `/dev`（`ViewReserved`）；
- 每个 `LocalExec` 条目都位于某个 closure 目录或某个 `Exec` overlay 中；
- shim 名称和 `ForwardEnv` 名称各自唯一，没有 shim 名为 `oac-process-shim`（该名称属于进程 relay）或以 `oac-mcp-` 开头（该前缀属于 [stdio 别名](#stdio-mcp)），变量名不含 `=`，且 `ForwardEnv` 不指定视图或 broker 设置的变量（[环境](#environment)）；
- `Proxy` 是两个取值之一，且 `Executor` 非 nil。

`harness.go` 只定义一次视图布局，`sessionview` 据此构建视图。agent host 在构建视图时，用声明检查它自己的 overlay，例如 `/etc/passwd`。

### 能力 {#capabilities}

视图运行该 kind 的声明所准入的每个请求，因此 adapter 只声明其视图能运行的内容，dispatch 按该声明检查每个请求。agent host 提供本地 Environment 和 environment none，且每个视图都运行 Environment 已安装的 Skills 和 [stdio MCP](#stdio-mcp)。Environment owner 以沙箱路径填写 `PrepareRequest.Skills` 和 `CapabilityRoot`，adapter 把它们交给 Harness；只有 Harness 通过视图读取它们，adapter 不在 agent host 上打开其中任何路径。agent host 以 `ErrViewHandoff` 拒绝需要凭据的 stdio 绑定。

### Environment none {#environment-none}

设置了 `DisableExecutionEnvironment` 的请求在空根视图中运行：`/` 是只读、noexec 的 tmpfs，只包含 closure、Session home、agent host 运行时文件、`/proc`、`/dev` 和 overlay 的挂载点。它没有沙箱文件、没有 shim、没有 Link 附着，也没有沙箱网络，因此通用代理拒绝每个请求；cgroup、隔离和网关保持不变。请求不携带 `LocalEnvironment`，Harness 在 `/.oac/home/work`（`ViewWorkName`）中运行。请求本身已经表达了这一配置，因此线协议没有对应字段。既没有 `LocalEnvironment` 也没有 `DisableExecutionEnvironment` 的请求是不完整的绑定，agent host 会拒绝它。

### 可执行文件 {#executables}

只有挂载标志授予执行权限。closure、`Exec` overlay 和 shim 是只读的，也是仅有的可执行挂载；沙箱的文件和 home 都是 noexec。`Launch` 和 `Spawn` 只接受 `LocalExec` 路径作为 `Binary`，否则返回 `ErrNotLocalExec`。动态二进制（例如 `node`）需要把它的 ELF 解释器作为 `Exec` overlay 放在其 `PT_INTERP` 路径上，并且它加载的每个库都要在 closure 中，通过 `LD_LIBRARY_PATH` 找到。任何内容都不从沙箱的文件加载。`viewloader.For` 根据二进制的 ELF header 构建这些内容：解释器所在的主机目录作为 `lib` closure 挂载、解释器 overlay、覆盖 `/etc/ld.so.preload` 和 `/etc/ld.so.cache` 的空 mask，以及 `LD_LIBRARY_PATH` 的值。它无法呈现的布局（例如位于解释器目录之外的库）返回 `ErrUnsupportedOperation`。

### Shim {#shims}

agent host 根据声明推导进程 broker 的映射表：`/.oac/bin/<name>` 在沙箱中运行 `<name>`，每个 `ShimPaths` 条目在沙箱中运行相同路径。按名称运行工具的 Harness 通过包含 `/.oac/bin` 的 `PATH` 找到这些工具。视图的 `/etc/passwd` 为 Session 用户设置登录 shell `/bin/bash` 和 home `/.oac/home`。

### 环境 {#environment}

`Launch` 在 `StartOptions.Env` 中接收完整的 Harness 环境，适配器根据自己的安装和请求的类型化字段推导该环境；请求不携带任何环境值。agent host 自身的环境从不传入，因此视图适配器不从 `os.Environ()` 开始构造。在沙箱中运行的进程获得 broker 的环境：来自 Harness 的 `ForwardEnv` 变量、Environment 固定的沙箱值（`HOME`、`PATH` 和 `LANG`）以及 Environment 的工具环境。broker 是工具环境的唯一归属，视图适配器不向 Harness 传递任何工具环境。agent host 只把模型和 MCP 凭据保存在网关受保护的配置中，从不把它们加入 Harness 的环境、Process spec 或视图暴露的能力树。

`ForwardEnv` 从不指定视图或 broker 设置的变量：`HOME`、`PATH`、`TMPDIR`、`LANG`、`LD_LIBRARY_PATH`，以及任意大小写的 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY` 和 `NO_PROXY`。当 Environment 的工具环境也设置了某个转发变量时，以工具环境的值为准。

### 端点与代理 {#endpoints-and-proxy}

调用工厂之前，agent host 将请求的模型提供商，即 `model_provider` 和 `Prepared.Provider`，指向 Session 的[凭据网关](./model-execution.md#credential-gateway)：`base_url` 是不带路径的 `http://127.0.0.1:<port>`，`api_key` 是 `modelprovider.Placeholder`。它把公开声明和已安装的 Environment MCP 一次性解析为 Session 的 MCP，放入 `ViewSession.MCP`，并从请求中移除这两者。只有 HTTP 绑定进入网关：每个 HTTP 绑定指向其网关 URL，不携带 bearer，也不携带 header，网关添加声明的凭据和 header。stdio 绑定在其[别名](#stdio-mcp)下运行。视图 Executor 只从 `ViewSession.MCP` 获取 MCP，从不解析请求。适配器把提供商和绑定渲染为 Harness 的原生配置，从不接触真实凭据。

Registry 在调用工厂之前对每个视图请求检查一次，并在以下情况下以 `ErrViewHandoff` 拒绝：准备好的模型提供商不是带占位凭据的网关、请求在 `ViewSession.MCP` 之外携带 MCP、HTTP 绑定不是不含凭据的 loopback 端点，或 stdio 绑定不是其别名。

使用 `ViewProxyEnv` 时，`ViewSession.Proxy` 是网关的代理 URL。适配器将 `HTTPS_PROXY` 和 `HTTP_PROXY` 设为该值，将 `NO_PROXY` 设为 `127.0.0.1,localhost`，每个变量都设置大写和小写两种形式。只有在确认 Harness 在本地发出的每个请求都遵循这些变量之后，才声明 `ViewProxyEnv`。忽略这些变量的请求会连接失败，因为视图没有出站路由。

使用 `ViewProxyNone` 时，`ViewSession.Proxy` 为空，视图没有通用代理。准入以 `ErrUnsupportedOperation` 拒绝启用了需要代理的功能的请求。由提供商执行的 Web 工具保持提供商来源。

### Stdio MCP {#stdio-mcp}

stdio 绑定在沙箱中以其别名运行。`ViewSession.MCP` 中索引为 `i` 的绑定恰好是 `Stdio: {Server: {Name: ServerLabel, Type: "stdio", Command: agent.ViewAlias(i)}}`，即 `/.oac/bin` 下带 `oac-mcp-` 前缀的名称，Harness 不带参数运行该路径。进程 broker 把别名映射到绑定冻结的 command、args 和 `CWD`（相对 `CWD` 以安装的 package 根目录为基准），并像运行 shim 的进程一样运行它，不使用 Harness 的 argv、工作目录或环境中的任何内容。凭据权限不是 `none` 的 stdio 绑定会以 `ErrViewHandoff` 被拒绝。

### Home {#home}

`ViewSession.Home` 是每个 Session 的原生 home。适配器在 `Home.Host` 写入，Harness 在 `Home.View`（`/.oac/home`）看到同一目录，可读写且 noexec。它在 Session 的各个 Executor 之间保留。调用 `Launch` 之前，在其中布置原生目录并写入配置。`Launch` 在不跟随链接的情况下把该目录树交给 Session 用户；此后读取 home 时也不跟随链接。

### 启动 {#launch}

`ViewSession.Launch` 取代 `clirunner.Start`。每次调用构建一个视图并在其中运行 `Binary`，每个 Session 同一时间至多有一个活动视图。`Dir` 是沙箱中的路径，`Env` 是完整环境。返回的 `clirunner.Process` 遵循[原生进程所有权](#native-process-ownership)：

- Cancel 向视图中的每个进程发送 TERM，并在 `KillTimeout` 后关闭视图。如果 Cancel 发现 Harness 已退出，即使它遗留的进程仍在结束中，也保持其退出结果不变。
- Harness 退出而仍有其他进程时，除非 Cancel 已发送过 TERM，视图会向它们发送 TERM，并在它们退出或自首次 TERM 起经过 `KillTimeout` 后结束。
- `Wait` 关闭 stdio 端；当 Cancel 的 TERM 到达运行中的 Harness 且 Harness 随后以 0 退出时，`Wait` 返回 context 错误；`Done` 关闭后，`ExitCode` 报告退出结果。

### Spawn {#spawn}

`ViewSession.Spawn` 在 Harness 运行期间，把一个 `LocalExec` 二进制作为另一个进程运行在活动视图中，例如读取 Harness 原生历史的程序。读取 Harness 所写数据的 Harness 侧代码在这里运行，从不在视图之外的 agent host 上运行，也从不获得自己的视图。`Spawn` 像 `Launch` 一样接收 `StartOptions`，并返回同样的 `clirunner.Process`。该进程的运行方式与 Harness 相同：同一用户，同一组命名空间、视图 cgroup、world 和网络，没有 capability，设置 `no_new_privs` 并使用同一 seccomp 过滤器，且位于自己的进程组中。

- 视图一次只启动一个 `Spawn`。`Parent` 限定等待轮次和等待启动的时间。它结束后，`Spawn` 返回它的错误，并杀死此后仍然启动的进程。
- Cancel 向它的进程组发送 TERM，并在 `KillTimeout` 后杀死该进程组。进程退出后，Cancel 不再投递任何信号，它遗留的进程像视图中的其他进程一样继续运行。
- 视图结束时它们全部随之结束。Harness 的 Cancel 会到达它们；Harness 退出时，它们属于仍然存在的进程。`Spawn` 返回之后视图才结束的情况，体现在该进程的 `Wait` 中。
- `Binary` 不是 `LocalExec` 路径时，`Spawn` 返回 `ErrNotLocalExec`；没有视图在运行其 Harness 时返回 `ErrNoLiveView`：尚未启动视图，或其 Harness 已退出，或其视图已结束。其他失败（例如二进制无法启动或描述符耗尽）保留各自的错误。

### 认定视图资格 {#qualify-the-view}

在视图中运行适配器的 Turn、取消和续接，然后逐项认定每个声明条目：

| 条目 | 资格认定 |
| --- | --- |
| `Closure`, `Overlays`, `LocalExec` | 每次本地执行都从声明的路径成功。每个动态二进制的解释器 overlay 与其 `PT_INTERP` 一致，`LD_LIBRARY_PATH` 能在 closure 中解析每个库。 |
| `Masks` | Harness 在被 mask 的路径上读不到沙箱的任何文件。 |
| `Shims`, `ShimPaths` | Harness 按名称或路径运行的每个工具都在沙箱中运行，其输出、退出状态和信号都能到达 Harness。 |
| `ForwardEnv` | 在沙箱中运行的进程保留每个声明的变量，且不保留任何其他 Harness 变量。 |
| `Proxy` | 使用 `ViewProxyEnv` 时，每个本地请求（例如网页抓取、下载和更新检查）都经过代理。使用 `ViewProxyNone` 时，启用需要代理的功能的请求会被拒绝。 |
| `Home` | 原生历史和配置保存在 `/.oac/home` 下，同一 Session 中后续的 Executor 从中继续。 |
| 声明 | 每项声明的功能都通过 dispatch 运行一个 Turn：空根视图中的 Environment none、函数调用及其结果、工具搜索、一个已安装的 Skill，以及每个以别名运行的 stdio 绑定。 |

`scripts/qualify-agent-host.sh` 针对 [agent-host 和沙箱镜像](../../../docs/zh/maintainers.md#runtime-images-and-helpers)，通过守护进程的 dispatch 运行每个 Harness 的 Turn。`agenthostqualify` 测试二进制以 [agent-host 容器的参数](../../../docs/zh/configuration.md#agent-host-container)作为 agent host 运行，沙箱镜像提供沙箱。每个 Session 的 Environment 像 Core 那样通过 `runtime_prepare` 准备：configure 步骤冻结工具环境，setup 步骤写入一个文件，内容是工具环境中的一个值，由测试检查；Session 之后的每个 Executor 都重新打开这次准备。第一个 Turn 写入一个文件，并报告一个失败命令的输出和退出状态，这两个值只存在于沙箱的工具环境中。kind 声明函数工具时，第二个 Turn 在新的 Executor 中运行，该 Executor 恢复 Session 的原生历史并调用一个函数；测试通过 dispatch 返回文本、图片、文本组成的结果，回答必须报告两段文本。kind 声明工具搜索时，一个没有原生历史的新 Executor 中的 Turn 用工具搜索找到延迟加载的函数并调用它。kind 声明 environment none 时，一个没有 Environment 的 Session 中的 Turn 通过模型作答，且 Session home 中 Harness 的原生状态必须写明其工作目录 `/.oac/home/work`。在另一个 Session 中，`runtime_prepare` 安装一个 plugin，其中有一个 Skill 和一个 stdio MCP 服务器，即 plugin 中在沙箱里运行的脚本。一个 Turn 使用该 Skill，必须报告只有其 `SKILL.md` 包含的词；新 Executor 中的一个 Turn 调用该服务器的唯一工具，必须报告它返回的代码。Link 通过 WSS 运行，使用测试生成的 CA。测试还会检查 cgroup v2 委派：容器自己的只读 cgroup 以 `ErrUnsupported` 失败；在委派目录中，agent host 用 `cgroup.kill` 结束遗留的 cgroup。将 `OAC_AGENT_HOST_IMAGE` 和 `OAC_SANDBOX_IMAGE` 设为这两个镜像，将 `OAC_QUALIFY_KEY_FILE` 设为模型密钥文件，并为每个要认定的 Harness 将 `OAC_QUALIFY_CLAUDE_SDK`、`OAC_QUALIFY_CODEX` 或 `OAC_QUALIFY_MCODE` 设为其 `model` 和不含 `api_key` 的 `model_provider`。网关直接连接模型提供商，因此在唯一出口是 HTTP 代理的主机上，将 `OAC_QUALIFY_PROXY` 设为该代理，测试会通过它为提供商的主机建立隧道。

## 原生参考 {#native-references}

| Harness | 适配器 | 原生传输方式 |
| --- | --- | --- |
| Codex | [`agent/codex`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/codex/executor.go) | app-server |
| Claude Code | [`agent/claudesdk`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/claudesdk/executor.go) | [TypeScript SDK bridge](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/packages/claude-sdk-adapter/README.md) |
| MiniMax Code | [`agent/mcode`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/daemon/internal/agent/mcode) | ACP 和原生工作区配套组件 |
