---
title: "将原生 Harness 添加到 OpenAgentCore"
source: contracts/agents-api/harness-onboarding.md
source_hash: 526356d82bd6b69e0c848b570d4944cd4a5872f8bb85eee0fb99124da0f1b8bd
---

**Harness** 是一种运行模型和工具循环的原生代理引擎（Codex、Claude Code、MiniMax Code）。**Harness 适配器**将 Runtime 的 Executor 和 Turn 契约转换到该引擎的 SDK 或协议。本文档定义 Runtime–Harness 协议：适配器接口及其生命周期义务、注册、Core 资格认定和验收。[Harness capabilities](harness-capabilities.md) 记录了当前每个 Harness 支持的功能。

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
| 服务 profile | 对已认定合格的操作和放置位置进行纯验证 | `services/core/internal/engine` |
| 注册 | 适配器声明、已安装工厂和已验证能力 | `apps/daemon/internal/agent/<kind>/declaration.go`；`apps/daemon/internal/cli/agent_discovery.go` 中的静态列表 |

Environment 提供执行资源。受管 E2B、Docker 和 microsandbox 机器以及应用自有机器在预配和连接方式上有所不同；已连接的 Runtime 使用同一契约。daemon 运行于 Linux、macOS 和 Windows，受管 Provider 仅支持 Linux，并且每个适配器自行认定其支持的平台（[self-hosted platforms](../../../docs/zh/getting-started/self-hosted.md#platforms)）。只有在 Runtime 加载绑定的已安装快照后，原生工厂才会收到能力（[capability preparation](environments.md#runtime-capability-preparation)）。模型 Provider 提供模型通信设置，而不负责 Turn 调度或原生进程所有权。

## 步骤 {#steps}

1. **锁定原生来源。** 记录上游包版本和源修订版本，并在适配器旁记录原生入口点。
2. **实现适配器**，位置为 `apps/daemon/internal/agent/<kind>`：实现 `ExecutorFactory`、`Executor` 和 `Turn`（[required interfaces](#required-adapter-interfaces)、[lifetimes](#executor-and-turn-lifetimes)）。复用共享的进程、凭据、配置和本地工作区辅助函数。
3. **在适配器中声明 kind**，并将其声明添加到 `apps/daemon/internal/cli/agent_discovery.go` 中 Runtime 的静态列表（[register the adapter](#register-the-adapter)）。
4. **添加服务 profile 和一个目录条目**（[add the engine to Core](#add-the-engine-to-core)）。
5. **打包原生先决条件。** 在 `services/core/deploy/<kind>` 下添加 Runtime 镜像，并可选添加 [native installer participation](#native-installer-participation)。
6. **启用并选择引擎**，通过 `core.harnesses` 设置和 [Harness selection](model-execution.md#harness-selection) 完成。
7. **认定其资格**（[qualify the adapter](#qualify-the-adapter)），并将结果记录到 [Harness capabilities](harness-capabilities.md)。

实现强制的文本生命周期，并明确处理每一种扩展。逐个认定受支持扩展的资格；未认定资格的扩展返回 `agent.ErrUnsupportedOperation`，且不会产生原生副作用。原生取消可能要求退役而非复用：`Reusable=false` 会携带原因，调用方必须确认 `Executor.Close`。不要为了适配测试辅助函数而强制复用，也不要将适配器的原生限制复制到共享 Core 协议中。

## 架构规则 {#architecture-rules}

- Codex、Claude Code 和未来的 Harness 地位平等。通用 Runtime 线协议以及 Executor 和 Turn 接口负责生命周期、输入回执、取消、恢复和资源访问；每个适配器保留其原生实现以及模型和工具循环。
- 新引擎需要提供适配器、已认定合格的 profile、注册以及经过独立验证的部署。它不得在 API 处理程序、持久化、调度、调度器或 Environment Provider 中添加按引擎名称分支的实现，也不得为契约已经涵盖的能力添加处理程序、存储表、调度器、事件投影器或模型循环。
- 将必需的生命周期声明、扩展接口和注册方法保留在 `agent/harness.go` 中。结果类型、错误和 Registry 存储可以保留在聚焦的文件中。
- 使用现有的 `proto.SupportedAgentKind` 和 `AgentKindCapabilities` schema。不要添加第二套能力描述符或组合式可选接口。
- 接入不要求功能完全一致。Harness 不必匹配彼此的可选功能，并且注册时不强制要求 MCP、函数、图像或详细程度控制。验证通用生命周期义务，并对每个声明的操作使用相同的公共断言。缺少声明或扩展实现会阻止接入；原生差异不会。
- 服务 profile 目录是资格认定边界。未知 profile 以关闭方式失败，并且 Runtime 心跳无法授权新的公共功能。Schema 有效性、服务资格认定和可用 Runtime 是相互独立的检查。
- 绝不能将已接受的参数等同于已实际应用的原生行为。

## 必需的适配器接口 {#required-adapter-interfaces}

[`agent/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/harness.go) 是接口入口。必需的生命周期包括 `ExecutorFactory`、`Executor`、`Turn`（包括 `DurableSteerer`）和 `TurnSettlement`。必需方法必须履行其原生义务；返回 Unsupported 并不构成对取消、回执、结算或清理的实现。Turn 和工作区扩展接口应保持小而独立，但每个公共适配器都必须明确实现每一个接口。所有接口都使用中立协议类型。

例如，Codex 适配器保留其 app-server 和 thread，Claude 适配器保留一个流式 Query，MiniMax 适配器保留其 ACP 连接和原生 session。它们都公开相同的 Executor 和 Turn 契约。原生回调和资源保留在适配器内部；Runtime 负责准入、空闲过期和替换。取消通过 `agent.Session` 精确定位到目标 Turn，适配器则向 Runtime 提供原生完成证据。

| 接口或契约 | 必需处理 | 义务 |
| --- | --- | --- |
| `ExecutorFactory`、`Executor.StartTurn`、`Executor.Close` | 真实实现 | 在没有模型输入的情况下准备；保留失败或不确定资源的所有权；确认清理 |
| `Session`、`Turn`、`CancellationOutcome`、`AwaitSettlement` | 真实实现 | 取消精确的 Turn，保留已观察结果，并独立于取消请求确认结算 |
| `DurableSteerer` | 每个 Turn 上真实实现 | 区分完整写入与原生应用回执；保留重试身份 |
| `Steerer` | 明确实现或 Unsupported | 额外的非持久化活动 Turn 输入 |
| `FunctionResultSubmitter` | 明确实现或 Unsupported | 匹配原生调用和结果身份，并确认应用 |
| `PermissionResponder`、`UserChoiceResponder` | 明确实现或 Unsupported | 响应精确发出的身份；未知或已过期的交互与 Unsupported 保持区分 |
| `WorkspaceReader`、`WorkspaceDirectoryLister`、`WorkspaceWriter` | 在 Turn、Executor 和 Prepared 所有者上明确实现 | 使用授权工作区，确认访问，提交或关闭，或者返回该操作的 Unsupported 错误 |
| `Prepared`、`PreparedCancellation` | 对可执行准备进行真实实现 | 在 Start、取消和未使用清理之间保持资源和输出的所有权 |
| 中立消息、图像、MCP、结构化输出和 Subagent 观察 | 明确作出能力决策 | 保持每项操作的协议语义；在提交前拒绝不受支持的输入 |

每个适配器的 `contracts.go` 都包含针对每个小型接口的单项编译时断言。不要嵌入会让未来接口看起来已经实现的默认实现。添加契约时，还必须在通用完整性检查中进行分类，并在每个公共适配器中添加明确断言；该检查遵循已编写的 Harness 目录。

对于设计层面的拒绝，请直接实现该方法：

```go
func (s *Session) SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error {
    return fmt.Errorf("%w: native public function tools are not qualified", agent.ErrUnsupportedOperation)
}
```

原因必须是固定的安全字符串，绝不能是已提交内容、凭据或原始原生诊断信息。Unsupported 保证不会产生原生副作用，也不表示操作成功且为空。安装不可用、未知交互 ID、原生失败和不确定结果应保留各自的错误和所有权。nil `Turn` 仍表示没有提交任何输入，并且输出归调用方所有；绝不能将其用作 Unsupported 标记。

线协议请求不携带工作目录。Runtime 将 `local_environment.workspace_directory` 与其绑定进行核对，并通过 `LocalEnvironment.WorkspaceRoot` 向 Harness 提供其绑定的工作区目录；必须在该目录中运行原生 Harness。

工作区能力描述实际 Runtime 与资源所有者的组合。Codex 和 MiniMax 资源对象拒绝原生工作区访问，而通用的授权 `localworkspace` 所有者提供该访问；Claude 可以公开原生读取和列举访问，通用所有者提供写入。仅仅存在相应接口绝不会选择某个资源或宣称支持。

服务 profile 对公共组合进行资格认定，Runtime 宣称已安装的组合；二者都不能替代 schema 验证或 Project 授权。原生行为测试必须与声明一致。已宣称但返回 Unsupported 的操作属于契约违规，既不是成功，也不能作为重放的依据。

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

**每 Turn 状态。** 每个 Turn 都会获得全新的包装器、输出通道和回执状态。引导、函数、权限和用户选择接口均属于该 Turn。原生回调必须在异步工作开始前捕获来源 Turn，因此迟到事件绝不会被归到当前活动的 Turn 上。原生进程、query 或传输连接、固定能力配置和原生 session 身份均属于 Executor。不要重置已完成的 `sync.Once` 值，也不要复用旧 Turn 对象。

**开始。** `StartTurn` 返回 nil Turn，保证没有提交任何原生输入，也没有保留输出通道；随后由 Runtime 关闭该通道。一旦输入可能已经提交，即使同时返回错误，也必须返回非 nil Turn：该 Turn 拥有恰好一次的输出关闭权，并在结算前持续接受跟踪。未知输入绝不能重放。明确的 `executor_unavailable` Start 拒绝允许进行一次通用恢复尝试，但只能在此前 Executor 已关闭且未提交输入之后进行；Runtime 会重新检查同一物理对端和当前授权。

**取消和结算。** `Turn.Cancel` 仅以目标 Turn 为对象，不会关闭健康的 Executor。`AwaitSettlement` 同时适用于自然完成和取消。成功意味着输出已无法再写入，并且该 Turn 的原生事件、输入、函数、交互和子任务均已结算。原生完成或取消确认独立于资源退役：关闭传输层无法提供缺失的原生终态或操作回执。

- `Reusable=true` 还要确认原生所有者能够接受下一个 Turn。`Reusable=false` 要求提供原因，并在之后确认 Executor 已关闭。
- 错误表示结算尚未确认，既不释放所有权，也不释放容量。调用方截止时间只会停止等待，不会停止受跟踪的清理。必须串行重试同一个清理目标；清理失败会阻止替换并保留其资源槽位。
- `Executor.Close` 独立于 Turn 结果确认资源退役：不可变的 Turn 错误不得阻止在其工作和输出已经停止后关闭原生传输层。
- 结算必须包含所属的后台工作，并在失败后保留精确的原生清理目标。原生终止由适配器负责；仅有批量清理确认并不能证明已达到静默状态。
- 每个 `Session`（包括直接调用工厂的结果）都要声明 `CancellationOutcome`。`Turn` 和 `PreparedCancellation` 继承该声明。快照保留已观察到的原生身份、Usage 和输出，并在取消后仍可读取。缺失的证据保持未设置；空的 `DonePayload` 表示未观察到任何内容，而不是表示取消成功或不受支持。读取快照不会等待结算。
- 直接调用的 `Session.Cancel` 请求取消；输出关闭表示拆卸开始。可执行准备中的 `PreparedCancellation.Cancel` 会等待本地清理和输出写入停止。Turn 结算仍需要 `AwaitSettlement` 和所需的任何 `Executor.Close`；取消请求成功或其快照都不能替代这些等待。

**Runtime 在 Turn 前后执行的工作。** 一个输出消费者会在原生 Start 之前启动，耗尽有界的 64 帧通道，并将终态观察保留到 Start 发布、Turn 结算和已准入操作回执完成为止。正常完成绝不调用 Cancel。输入、函数和交互准入会在结算前关闭；已准入的操作会持有其屏障，直至原生回执和出站确认完成。Runtime 会在等待该屏障之前向 Turn 发送取消，因为已写入的输入可能需要原生中断才能生成回执。Runtime 会汇合原生结算、所需的已确认 Executor 关闭、输出耗尽和所有已准入操作，然后应用确认或执行复用，之后才会转发 Done 或已应用的取消回执。Close 失败可以报告失败，同时保留同一 Run 和未完成操作以供重试；已关闭的调用方等待无法凭空生成已应用输入回执。Runtime 会在发布 Done 前提交原生连续性状态并释放旧 Run 的准入，因为接收方可能立即启动另一个 Turn；迟到的终态发送失败属于旧 Run，不能使已拥有 Executor 的后继对象失效。连接关闭负责传输丢失清理。结算等待时间为十秒，回执发送预算为五秒；超时不能证明已达到静默状态。

## 事件、输入和可选能力 {#events-inputs-and-optional-capabilities}

使用 [`internal/agentdaemon/proto`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/internal/agentdaemon/proto) 处理中立请求、事件和回执。每个 Turn 只按顺序发出带有其 Run ID 的自身事件，并产生一个终态结果。原生 ID 和 usage 必须来自观察，绝不能虚构；缺失的度量值表示未知，而不是零。

初始输入和引导使用有序的 `proto.MessageInput`。必须保持用户消息顺序和内容顺序。仅支持文本的适配器通过 `TextOnly()` 拒绝图像，而不是丢弃图像；图像适配器在原生环境中转换每个部分，并且只有在其所有消息均已应用后才确认活动批次。成功传输写入与确认原生应用是不同的事件。用户选择答案使用发出的问题 ID 和值数组；共享的 `PromptForUserChoiceDecisionPayload.AnswersFor` 会在消费待处理交互之前验证身份，因此绝不能按标头或位置映射答案。只能恢复绑定到 Session 的精确历史；缺失、含糊或外部历史会在新的模型输入之前导致失败。设备身份不代表原生 session 所有权。

### 必需操作和扩展操作 {#required-and-extension-operations}

公共文本路径要求持久化 Turn、已应用输入回执、有序观察、取消，以及执行已禁用的执行控制；`execution.Policy.engineCapabilities` 保存精确要求。没有原生工具的引擎可以保证这些工具不存在；具有工具的引擎在收到要求时必须实际禁用它们。接受某项配置不能证明其已得到执行。

MCP、公共函数、延迟函数发现、结构化输出、图像输入、详细程度控制和其他可选操作不必匹配另一个引擎。使用 Unsupported 拒绝未认定资格的组合并记录差距；绝不能宣称某项能力来绕过选择。

- 结构化输出：读取 `ExecutionControls.OutputFormat`，并通过 Message 契约发布已确认的原生输出（[execution tools](execution-tools.md#structured-output)）。公共资格认定与 Runtime 能力分别注册。
- 图像：分别注册 Runtime 的 `MessageImages` 并认定 profile 的 `MessageImages` 资格（[message input](message-content.md)）。
- 工作区放置方式还需要经过验证的准备过程、工作区读取和输出导出，以及使用共享 Files 辅助函数的专用 Runtime 绑定。只有在展示其生命周期行为后才能启用放置方式。

### MCP 来源和原生限制 {#mcp-origin-and-native-limits}

在引擎 profile 的 `MCPOrigins` 中声明受支持的公共来源，并在 `MCPBearer` 中声明 bearer 支持。Runtime 宣称其实际 HTTP、bearer 和必需初始化能力。共享准入负责验证来源和放置位置；适配器验证负责保留原生标签、允许列表和初始化限制。

使用 `agent.ResolveMCPBindings` 处理公共声明和已安装声明，并保留来源、凭据权限、`null` 与空允许列表之间的区别以及必需启动过程。不要将令牌复制到原生 profile 中，也不要将服务请求重新解释为 Environment 请求。拒绝不受支持的原生策略，而不是将其丢弃。遵循 [MCP origin contract](environments.md#public-mcp-connection-origin)，并对每个宣称的组合执行公共客户端、失败、取消和冷恢复资格认定。模型能力与 Harness 传输支持相互独立；绝不能从模型名称推断模型能力，也绝不能静默降低输入质量。

### Subagent 观察 {#subagent-observations}

支持 Subagent 读取的 Harness 必须实现[中立观察契约](subagents.md#adapter-contract)。它通过经身份验证的 Run 报告经过验证的子项身份、生命周期影响以及所属的 Turn 和 Item 历史，并通过真实执行认定这些事实，且不使用额外路由、存储分支或 Harness 专用调度器。明确报告不受支持的原生事实；完成子任务并不等于关闭其 Subagent。原生后台工作的所有权必须保持到结算和取消完成为止。

## 注册适配器 {#register-the-adapter}

注册是静态的，并且需要构建。从 `apps/daemon/internal/agent/<kind>/declaration.go` 导出一个 `agent.Declaration`，然后将其添加到 [`cli/agent_discovery.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_discovery.go) 的 `harnessDeclarations` 中。声明包含 kind、完整能力描述符、共享模型 `Configuration` 和 `Discover` 函数。发现过程接收 profile 和诊断写入器，负责原生配置和可用性检查，并返回已安装的 `agent.Runtime` 及其描述符、session 工厂、准备工厂和 Executor 工厂。未配置适配器时返回 nil；已配置的前置条件失败时，返回不可用描述符和 session 工厂。将版本门控和工厂选择条件保留在适配器内部。

`Runtime.SessionCapabilityContext` 和 `Runtime.ExecutorCapabilityContext` 会为相应的执行工厂显式请求能力下载 URL 解析和限定范围的产品上传上下文。准备过程绝不会收到这些影响。支持产品工作区创作功能的适配器自行声明 `WorkspaceAuthoring`；通用注册不会授予该能力。

[`cli/agent_registration.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/cli/agent_registration.go) 遍历已发现的 Runtime，并调用 `agent/harness.go` 中的 `Registry.Register`。它验证发现过程是否保留了声明的 kind，并按以下顺序安装工厂：

| 顺序 | 方法 | 注册内容 |
| --- | --- | --- |
| 1 | `RegisterKind(proto.SupportedAgentKind, harnessconfig.Configuration, agent.Factory)` | Kind、可用性、版本、`AgentKindCapabilities`、模型配置声明和直接调用工厂。它会重置其他注册项，因此必须首先调用。 |
| 2 | `RegisterExecutor(kind, agent.ExecutorFactory)` | 执行所用的 Executor 和 Turn 生命周期；据此派生 `Preparation` 能力 |
| 3 | `RegisterPreparation(kind, workspaceRead, agent.PreparationFactory)` | 可选：针对已认定合格的工作区操作的独立只读工作区准备 |

直接调用的 `agent.Factory` 委托给同一个 Executor 实现。

每个 `proto.AgentKindCapabilities` 字段都必须显式设为 `proto.CapabilitySupported` 或 `proto.CapabilityUnsupported`，即使 Harness 不可用也是如此。`proto.CapabilityUnspecified` 无效：零值和省略字段绝不表示 Unsupported。安装探测可以使用 `proto.CapabilityFromBool` 设置单个字段；但不得填充未提及字段或未来字段。可用性通过 `SupportedAgentKind.Available` 单独表示。注册会在更改 registry 之前验证完整声明；线协议会为每个字段携带显式布尔值，因此省略字段和 null 字段均无效。添加新字段时，每个生产声明都必须作出决定。Runtime 使用者应调用 `IsSupported()`，并在原生操作前拒绝不受支持的请求；接口断言用于验证实现，绝不表示支持。每个声明都必须与针对该安装验证的行为一致；[Core–Runtime protocol](../../../docs/zh/runtime-protocol.md#capability-declarations) 负责声明的传输方式和冻结方式。

准入映射是显式的。`Steering` 控制非持久化 `Steerer` 输入。`DurableInputReceipts` 控制 `DurableSteerer` 输入，并且还要求 Turn 结算契约；二者互不隐含，而且 Core 的公共文本 profile 要求同时具备二者。`Permissions` 一起认定权限响应和用户选择响应的资格，并要求两条原生响应路径均存在。工作区声明描述授权资源所有者，包括通用 Runtime 工作区实现。Runtime 注册不会授予 Core 资格；服务 profile 才会授予。

可运行的仅测试示例 [`testdata/onboarding/main.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/testdata/onboarding/main.go) 会注册一个仅支持文本的合成 Harness。它展示 Session 所有的 Executor、全新的 Turn、持久化引导、取消和历史绑定，并且绝不会发布。

## 将引擎添加到 Core {#add-the-engine-to-core}

Core 会识别[内置 Harness 注册项](harness-catalog.md)。向 `internal/harnessconfig/builtin/catalog.json` 添加一个条目，其中包含：

- 公共 `kind` 和显示 `label`；
- `internal/harnessconfig` 下的模型 `configuration` 包；
- `services/core/internal/engine` 下的 `profile` 构造函数。

实现 profile 构造函数，然后运行 `make generate-harness-catalog`。它会生成模型配置 registry、Core profile 目录、客户端标识符和显示名称以及注册参考；公共输入验证器读取生成的 registry。`make openapi` 从同一目录派生 Harness 枚举，因此不要在 DTO 标签或路由注解中添加手写枚举。`make check-harness-catalog` 会拒绝过时的投影。

每个 Runtime 声明都引用生成的目录所使用的同一个 `internal/harnessconfig/<kind>.Configuration()`，并负责其原生工厂、探测和已安装能力证据。目录不能声明某台机器的可用性，也不存在动态插件加载器。

profile 是纯逻辑：它使用现有的公共类型和协议类型，声明受支持的放置方式、公共配置、结果限制和必需的 Runtime 控制。Profile 回调不能查询业务数据、解密凭据或控制原生进程。共享调度检查能力组合，而不是引擎名称允许列表。

### 显式服务资格认定 {#explicit-service-qualification}

`engine.Profile` 是服务的资格声明，独立于 Runtime 的 `AgentKindCapabilities`。其能力字段复用小型 `proto.CapabilitySupport` 值类型：每个字段都必须显式选择 `CapabilitySupported` 或 `CapabilityUnsupported`。`CapabilityUnspecified`（包括省略字段）会被拒绝。复用此值类型并不意味着 Runtime 的宣称可以授予服务授权。

`ConfigurationValidation`、`ToolsValidation` 和 `FunctionResultValidation` 分别选择以下两种策略之一：

- `CommonValidationOnly`：通用 schema 和准入检查已足够。相应回调必须为 nil；不需要提供成功的占位回调。
- `AdditionalValidation`：相应的 `ValidateConfiguration`、`ValidateTools` 或 `ValidateFunctionResult` 回调为必需项，并添加纯 Harness 限制。

省略或未知策略、缺少必需回调，或者将回调与仅通用策略搭配，均无效。准入遵循声明的策略，而不依据方法是否存在。保留现有错误优先级：配置限制最先执行；选择额外配置验证时，工具解码错误先于工具限制。对于仅通用的配置验证，额外工具限制仍保持其相对于解码错误的现有优先级。仅通用的函数结果验证不会添加原生结果限制。

`engine.NewCatalog` 会在发布不可变快照之前验证每个条目，并对无效静态注册触发 `engine.ErrInvalidDeclaration` panic。Kind 必须非空且前后不得包含空白字符。放置方式必须显式列出至少一个受支持的放置方式；MCP 来源必须是非 nil 列表（空列表表示不认定任何来源合格）。未知或重复选项、没有对应放置方式的来源，以及没有 MCP 来源的 bearer 支持均会被拒绝。错误应标识已编写的字段，但不回显声明值。未来 profile 字段必须由完整性验证器分类，并由每个 profile 显式决定；不存在生产用默认填充构造函数。

运行 `engine` 和 `execution` 测试以覆盖遗漏、策略、组合和错误优先级，并运行公共接入测试和存储测试以覆盖准入和 Runtime 调度。测试夹具使用 `engine/enginetest`，其穷尽式字面量在添加字段时也要求作出决定；它不是生产 profile。

`execution.Policy` 向 HTTP 准入、Worker 设备选择和最终调度提供不可变服务资格认定。自定义组合将同一个 Policy 提供给 `api.Dependencies.Policy` 和 Core 调度器的 `Policy`。零值使用内置 profile；显式空目录不授权任何内容。不存在可变全局注册。

## 原生模型配置 {#native-model-configuration}

[`internal/harnessconfig/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/harnessconfig/harness.go) 负责共享配置声明和纯准备契约。每个适配器在 `internal/harnessconfig/<kind>` 中提供一个 `Configuration`，供 Core 组合和 Runtime 的 `RegisterKind` 使用。直接调用工厂、准备路径和 Executor 路径都会在产生原生副作用之前通过该声明进行验证，而 Registry 包装器会将声明与工厂保留在一起。线协议对象是 `proto.HarnessConfig`。[Model execution](model-execution.md#native-model-parameters) 列出了每个 Harness 接受的字段。

提供的 `model` 必须是非空字符串，并且显式指定 `model_provider` 时必须提供它。原生所有权连接路径可以省略二者；显式 null 无效。显式为空的声明不接受任何 Provider 或非空原生参数，也不宣称支持 Provider。未知协议格式和重复协议声明会导致注册失败。

声明中的有序 `protocols` 列表是接受协议及默认协议（第一个条目）的唯一来源；它还为 Core 的配置支持描述符提供数据，Core 和 Runtime 通过它拒绝不受支持的组合。适配器通过原生配置直接连接；它们绝不引入模型 API 代理或协议转换器、第二套模型能力 registry，也不会从模型名称推断能力。Claude 的私有 bridge 接收编译后的原生选项，并且只执行结构检查，而不是声明规则的第二份副本。

## 认定适配器资格 {#qualify-the-adapter}

开始前，记录操作集、预期结果、排除项和停止条件。当其声明的操作通过时，资格认定即结束；它不会扩展为匹配另一个 Harness 的功能列表。

1. **契约测试。** 在名为 `TestSharedTextLifecycle` 的测试中，使用适配器准备好的 Executor 和确定性的原生夹具调用 `agent/contracttest.TextLifecycle`；`claudesdk/executor_test.go` 是参考实现。它检查独立的 Turn 流、原生所有者和历史连续性、持久化写入与应用回执、过期取消，以及取消后的健康继续执行。`make check-runtime-contract` 会将它与共享线协议、gateway、传输层和调度器测试、声明完整性检查以及每个适配器的 `TestUnsupportedExtensionsHaveNoNativeEffects` 一起运行。适配器测试还覆盖两个普通 Turn 共享一个原生进程或连接和历史、取消后执行另一个 Turn、过期取消和迟到事件、原生退出、清理失败、输入写入与应用回执、未知结果，以及每 Turn 新鲜的 usage、函数、输入和子项观察状态。必须说明夹具是受控夹具还是真实 Provider。
2. **共享集成。** `TestThirdHarnessPublicOnboarding` 让合成 Harness 通过公共 Session 和输入准入、Worker 设备选择、真实 WebSocket gateway、daemon Registry 和 Router、中立事件以及持久化终态投影运行。它在与 API 处理程序和调度器相同的 `execution.Policy` 中使用自定义不可变 `engine.Catalog`，并检查已应用输入回执、已保存原生身份、继续执行、取消、不受支持的可选请求以及缺少强制 Runtime 支持。该夹具没有工作区、MCP、公共函数、权限或用户选择处理器，其注册仅保留在测试本地。它证明的是集成路径，而不是原生执行。
3. **真实验收。** 使用锁定的官方 Python SDK 和针对 Core 的原始 HTTP、真实 Provider API、原生 Harness 以及专用数据库。验证初始执行、热后续执行、取消以及带继续执行的重启；记录原生所有者身份以及相同条件下的冷启动和热运行时间。对于工作区放置方式，还要验证 Files 和 Artifacts、工作区身份、公开响应中未出现凭据，以及外部历史会被拒绝。`services/core/tests/official_hosted_functions_native.py` 保存共享函数断言：成功和错误、原生文件输出和公共 Artifact 字节、重启后的同历史继续执行、外部结果拒绝以及待处理调用取消。合成运行或失败运行绝不计入。下面的选择性测试会在 `services/core/tests` 中针对真实 daemon 和模型运行锁定 SDK 夹具；设置 `OAC_TEST_OFFICIAL_SDK_PYTHON`、`OAC_TEST_NATIVE_DAEMON_BIN`、`OAC_TEST_NATIVE_PROOF_DIR` 及其私有选项文件后，每项测试才会运行。选项文件是一个 JSON 对象，恰好包含 `model` 和 `model_provider`（即 `x_agents_core.model_provider` 的字段）；测试会将其设置为部署默认模型 Provider，而夹具的 `environment: none` Session 会在创建时将其冻结。
4. **回归。** 现有 Harness 必须继续正常工作。先运行定向测试，然后运行 `make check`；API 更改后运行 `make openapi`，查询更改后运行 `make sqlc-generate`。
5. **审查。** 遵循 [blind review workflow](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#review)。

| 操作 | `services/core/internal/store` 中的测试 | 选项文件变量；测试采用 Harness 变量时也列出该变量 |
| --- | --- | --- |
| 模型 Provider 协议 | `TestNativeModelProtocolPublicExecution` | [Model execution](model-execution.md#acceptance) |
| MiniMax Code 文本 | `TestNativeMCodePublicExecution` | `OAC_TEST_MCODE_REAL_OPTIONS` |
| 消息图像 | `TestNativeMessageImagePublicExecution` | `OAC_TEST_MESSAGE_IMAGE_REAL_OPTIONS`、`OAC_TEST_MESSAGE_IMAGE_ENGINE` |
| 带图像的函数结果 | `TestNativeFunctionImagePublicExecution` | `OAC_TEST_FUNCTION_IMAGE_REAL_OPTIONS`、`OAC_TEST_FUNCTION_IMAGE_ENGINE` |
| 结构化输出 | `TestNativeStructuredOutputPublicExecution` | `OAC_TEST_STRUCTURED_OUTPUT_REAL_OPTIONS` |
| 延迟函数发现 | `TestNativeToolSearchPublicExecution` | `OAC_TEST_TOOL_SEARCH_REAL_OPTIONS` |
| 禁用 Web 搜索和程序化工具调用 | `TestNativeToolPolicyPublicExecution` | `OAC_TEST_TOOL_POLICY_REAL_OPTIONS`、`OAC_TEST_TOOL_POLICY_ENGINE` |

Environment 验收使用 `services/core/tests/official_environment_{templates,setup,skills,plugins,plugin_mcp,composition,initial_files,network,skill_references}.py`。对于组合式准备，请更改 Skill 默认值和 Template，删除源文件，重试并重启；验证冻结字节、一次 setup 执行和 MCP 取消。`official_hosted_structured_native.py` 覆盖托管结构化输出。随每项验收结果记录精确源修订版本、原生版本和命令。

将 Provider 密钥保存在私有操作员文件中，绝不能放入提交或日志。相对于 `apps/daemon/internal/agent` 的现有定向测试如下：

| 边界 | 测试 |
| --- | --- |
| Codex 复用、取消和未确认清理 | `codex/executor_test.go`、`terminal_cleanup_test.go`、`prepared_cancel_test.go` |
| Codex 输入回执和严格恢复 | `codex/function_write_receipt_test.go`、`function_receipt_test.go`、`resume_test.go`、`recovery_test.go` |
| Claude 输入所有权、取消和准备清理 | `claudesdk/executor_test.go`、`cancellation_test.go`、`preparation_test.go` |
| MiniMax 取消退役、Start 失败和清理重试 | `mcode/executor_test.go`、`executor_backpressure_test.go` |
| MiniMax 原生历史绑定 | `mcode/session_test.go` |
| 无原生副作用或伪造结果的明确拒绝 | 每个适配器的 `unsupported_test.go` |

## 原生安装器参与 {#native-installer-participation}

适配器可以从自身包中的 `installation.go` 提供 `agent.Installation`：已注册 kind、锁定版本、受支持平台、激活环境和有界就绪探测。在 `cli/native_harness.go` 中注册它，并将其锁定组件添加到原生分发构建器。此可选契约不会改变 Executor 和 Turn 语义。Runtime 负责校验和、复制、锁和增量安装；适配器负责原生布局和探测。必须在每个宣称的平台上验证安装和执行。原生内容缺失或不兼容时必须失败；绝不会在 Turn 期间自行安装。

## 原生进程所有权 {#native-process-ownership}

daemon 的 `clirunner` 为 SDK 会启动原生子进程的适配器提供可选的 Unix 进程组所有权；不支持的主机会在启动前拒绝此模式。显式取消和父上下文取消共享 TERM 宽限期（默认为三秒）以及有界的 KILL 升级过程。当直接进程退出时，内部回收器也会清理进程组的剩余成员，即使某个后代进程仍保持 stdout 打开；在取消过程中，主进程退出后，存活的后代进程仍会保留剩余宽限时间。daemon 的 `stop` 命令最多等待十秒以确认关闭，这涵盖该宽限期以及之后的管道和所有者清理。

所属输出管道在主进程退出后仍可读取。消费者在调用 `Wait` 之前耗尽 stdout 和 stderr；`Wait` 会汇合缓存的进程结果并关闭读取器。`Done` 报告主进程回收和进程组清理信号；它不是原生执行回执，也不是历史已持久化的证据。SDK 适配器会结算每个 Turn，并在发布完成状态前耗尽其观察结果；Executor 关闭还会关闭 Query 并等待原生子进程。进程组用于生命周期监管，而不是隔离或遏制离开进程组的后代进程。

适配器以启动用户的权限无人值守运行原生工具：Codex 使用批准策略 `never` 和完全访问权限；Claude 通过适配器的工具回调，以原生 `default` 权限模式运行，并禁用 SDK sandbox；MiniMax 绕过权限并禁用 sandbox。不要添加权限 profile、bubblewrap 包装器或原生 sandbox 设置；每种 Environment 来源都只有一条执行路径。资源路径属于操作员配置，而不是权限边界。

网络准入遵循 [Restricted network](environments.md#restricted-network)。

## 原生参考 {#native-references}

| Harness | 适配器 | 原生传输方式 | Runtime 指南 |
| --- | --- | --- | --- |
| Codex | [`agent/codex`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/codex/executor.go) | app-server | [Codex Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/codex/README.md) |
| Claude Code | [`agent/claudesdk`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/daemon/internal/agent/claudesdk/executor.go) | [TypeScript SDK bridge](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/packages/claude-sdk-adapter/README.md) | [Claude Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/claude/README.md) |
| MiniMax Code | [`agent/mcode`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/apps/daemon/internal/agent/mcode) | ACP 和原生工作区配套组件 | [MiniMax Code Runtime](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/mcode/README.md) |
