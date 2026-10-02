---
title: "添加 Sandbox Provider"
source: docs/sandbox-provider.md
source_hash: 36ed532c778ec8c1c4c596a011a37613ed2aa0e6ffdf20854ea30ef9a8e0c953
---

**Sandbox Provider** 为 Core 管理的 Environment 提供 Runtime daemon 运行所需的外层计算资源，以及启动 daemon 的有界引导流程。本指南说明如何添加 Provider，并作为 Core 驱动 Provider 的参考。接口为 [`SandboxProvider`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/sandbox/sandbox_provider.go)。

| 术语 | 含义 |
| --- | --- |
| Environment | Core 拥有的持久执行场所；参见 [Environment](../../contracts/agents-api/zh/environments.md) |
| Allocation | Core 为 Environment 拥有的一份计算资源租约，由 `Reference` 标识 |
| Runtime | Environment 内的 daemon；准备能力并执行 Turn |
| Deployment | 整个部署唯一的 provider 选择；参见[沙箱部署](../../contracts/agents-api/zh/sandbox-deployment.md) |

Core 拥有持久 Environment、allocation、placement 和 cleanup 状态；Provider 仅负责计算资源与引导。Runtime 通过 [Core–Runtime 协议](runtime-protocol.md)准备能力并运行 Turn，provider 通过 [Runtime 引导](runtime-bootstrap.md)文件交付身份。provider 不运行 Environment 初始化、Skill、Plugin、MCP 设置、初始文件、execution 或 Files；这些操作使用 Runtime。隔离属于 provider 基础设施，不属于 daemon；参见 [Runtime 与外层隔离](concepts.md#runtime-and-outer-isolation)。使用薄 adapter 封装厂商维护的 SDK。

## 步骤 {#steps}

1. **阅读契约。** 实现五项必需操作，并对[实现接口](#implement-the-interface)中的每个扩展接口作出明确决定。
2. **编写 adapter 包**，放在 `services/core/internal/sandbox/<kind>`：包括原生 SDK 调用、所有权检查、身份转换和私有配置。声明 `var _ sandbox.SandboxProvider = (*YourAdapter)(nil)`。进程外 helper 放在 `services/core/tools/<kind>-provider`。
3. **注册 kind** 一次，遵循[注册 provider kind](#register-the-provider-kind)。注册是明确构造，不是 init 时 plugin registry。
4. **标记所属资源**，使用 [Runtime 名称](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#openagentcore-runtime-names)表中的 provider ownership label，不接受旧 label 名称作为回退。
5. **运行契约套件** `make check-sandbox-provider-contract`；参见[验证集成](#validate-the-integration)。
6. **对真实计算资源运行原生验收**，再运行 `make check`。
7. **在 adapter 旁编写文档**，参考[参考 adapter](#reference-adapters)。

## 实现接口 {#implement-the-interface}

`SandboxProvider` 有五项必需操作：

| 操作 | 用途 |
| --- | --- |
| `Create` | 为 `Reference` 创建计算资源，并运行有界 daemon 引导 |
| `GetInfo` | 观察当前计算资源，不修改它 |
| `Renew` | 延长原生租约；backend 没有租约时仅观察 |
| `Kill` | 回收 allocation 的计算资源与保留资源 |
| `RunCommand` | 在 allocation 的计算资源中运行有界命令 |

Docker 等没有原生可续期租约的 backend 仍遵守 Core 的 hosted expiry 和 cleanup 要求。每个 adapter 都实现 `RunCommand`，测试也执行它，但 Core 编排不调用它；只有 node transport 转发。机密命令输入通过 `Command.Stdin` 传输，不放在参数或日志中；结果保留字节顺序、有界输出和实际退出状态。

### 明确的操作契约 {#explicit-operation-contracts}

每个 provider 实现以下各接口的方法，并返回完整的 `ProviderOperations()` 声明。接口方法就是操作清单；`sandbox.ValidateOperations` 根据接口检查声明，无需第二份手动维护的清单。

| 契约 | 要求 | 职责 |
| --- | --- | --- |
| `sandbox.SandboxProvider` | 支持全部五项操作 | Allocation 生命周期与有界命令 |
| `sandbox.CheckpointProvider` | 对每个方法明确决定，所有方法一致 | 精确计算实例、捕获与恢复、保留源恢复和清理 |
| `runtimeobs.Source` | 明确决定 | 检查所有权的只读观测 |
| `runtimeobs.BatchSource` | 明确决定；要求 `Source` | 按输入顺序提供有界观测，包含每目标错误 |
| `sandbox.SelectionDiscoverer` | 明确决定 | 提交前只读原生配置发现 |
| `sandbox.CredentialVerifier` | 明确决定 | 验证对所属资源的访问，不修改资源 |

`CheckpointProvider` 增加 `Initial` 和 `NewCompute`（构造 compute reference，不分配资源）、`GetCompute`、`Suspend`、`Resume`、`ResumeCompute`（暂停中止后仅解冻同一驻留实例）、`KillCompute`、`DeleteSnapshot` 和 `RunCommandCompute`，后者在一个精确 compute incarnation 中运行有界命令。Core 使用 `RunCommandCompute` 在恢复后唤醒 parked daemon（[`runtime_compute_wake.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/execution/runtime_compute_wake.go)）。

每个声明项为不带 reason 的 `state: supported`，或带 authored reason code 的 `state: unsupported`。缺失、零值、未知或不安全项以及缺失方法都会验证失败。给接口添加方法时，必须在每个 adapter 中明确决定并实现；不提供 base type，也不生成笼统的不支持实现。

不支持的方法在任何原生 I/O 前返回 `providercontract.UnsupportedError`。错误指明精确操作和安全 code，不包含原生消息、资源身份、endpoint 或凭据。空结果、nil error、`Unavailable` 或未知 mutation 结果都不能代替 unsupported，五项必需方法不能返回 unsupported。

每个 adapter 拥有一个 `Operations()` 函数，由实例和注册共享。`providers.ValidateBinding` 根据接口并相互对照检查两者，Runtime admission 与 node generation 加载也拒绝不完整 provider。interface assertion 仅证明方法形态；调用方使用声明决定支持情况。

`ObserveBatch` 返回 error，不返回有歧义的 boolean。仅 `ObserveBatch` 的类型化 `UnsupportedError` 允许逐目标调用 `Observe`；服务不可用、超时或其他失败都不允许。观测不续期、启动、准备或停止计算资源；参见[观测契约](../../contracts/agents-api/zh/runtime-observability.md)。

契约测试在未配置原生 client 时调用每个声明不支持的方法，要求匹配错误和零值结果，并拒绝不完整或矛盾的声明。支持的行为仍需要原生和生命周期测试。

### 身份与资源所有权 {#identity-and-resource-ownership}

`Reference` 是精确的 `(TenantID, EnvironmentID, AllocationID)` 元组。Core 在 `Create` 前持久化新的 allocation ID；它不是 Environment ID。adapter 还将资源绑定到安装实例，不按裸原生 ID、display name、猜测路径或未验证 label 定位或授权资源。每次修改、读取和清理验证相同所有权。

Core 串行化生命周期操作，并在任何不确定 mutation 后保留 allocation，因此 adapter 必须保留足够的原生身份和回执供观测与清理。失败调用可以同时返回 `Info` 和 error：保留与 reference 绑定的结算证据，不把失败变成成功。冲突后不得悄悄创建替代资源、覆盖凭据或切换新 allocation。

`Info.ProviderID` 和 `Info.State` 描述计算资源。仅在有匹配 `Reference` 和原生身份时，Core 将计算资源视为 `running`；可以观察其他原生状态，但不声明就绪。`BootstrapComplete` 表示引导到达最终修改步骤。`CreateSettled` 证明原始尝试不能再修改资源，不表示成功：

- `State="absent"` 携带匹配 `Reference` 和 `CreateSettled=true`，是明确的创建不存在回执，不含原生 ID 或已完成引导。
- `ErrNotFound`、空列表、超时或单独成功的 `Kill` 都不能证明进行中的 Create 不会稍后出现。
- Core 也可以依据匹配的 running resource 和已完成 bootstrap 结算创建。取得此类证据或明确回执前，创建保持未知，即使清理尝试看不到资源。

`Kill` 负责清理 allocation 的计算资源和保留资源，包括部分 bootstrap storage；名称冲突时不删除其他租户资源。Core 仅在确认清理和创建已结算后释放持久所有权。关闭 Executor 或取消 Harness 不删除 Environment、工作区或 allocation。

### 操作结果与重试 {#operation-outcomes-and-retries}

每次调用接收有界 context。到期或取消结束调用方等待，不证明回滚、停止、清理或不存在。adapter 或 transport 不得脱离跟踪执行 mutation，也不重放超时命令。

| 操作 | 已确认结果 | 失败或未知结果 | 恢复 |
| --- | --- | --- | --- |
| `Create` | 匹配计算资源与 bootstrap 证据；执行仍需要 Runtime preparation | 无效或外来配置被拒绝；重复返回 `ErrExists`；transport 失败可能隐藏已创建资源 | 观察原 `Reference`。不重放 `Create`，即使使用新凭据。保留部分资源用于所属清理 |
| `GetInfo` | 不修改的当前计算资源观测 | `ErrNotFound` 仅表示缺少观测；error 不是不存在的证明 | 重复有界读取；不将其变成 create、start 或 renew |
| `Renew` | 原生租约已延长，或无租约 provider 的观测 | 超时可能隐藏延期；停止或缺失计算资源仍保持原样 | 先观察，再由 reconciler 续期同一 allocation。不复活计算资源或虚构 lease expiry |
| `Kill` | 所属计算资源与保留存储已移除；重复已确认不存在时成功 | error 保留所有权与清理意图；所有权不匹配不删除外来资源 | 未决创建或 mutation 已隔离后重试同一 `Reference` 的清理；不提前释放 owner |
| `RunCommand`, `RunCommandCompute` | 收集到输出和实际 exit code；非零退出是已结算命令失败 | 缺失原生完成为 `ErrCommandUnconfirmed`；部分输出不是成功 | 不重放。无法证明完成时保留 owner，并在复用前回收 |

`ErrInvalid`、`ErrOwnership`、`ErrExists`、`ErrNotFound`、`ErrComputeUnconfirmed` 和 `ErrCommandUnconfirmed` 保持其定义含义。未分类原生或 transport error 表示未知，不授权重试 mutation。Core 不将 provider diagnostics 读作生命周期事实，也不暴露原生错误文本或凭据；node transport 将错误映射为固定 code，直接 SDK 细节保持私有。

Checkpoint 支持增加 `Compute` generation、name、ID 和 `SnapshotIdentity`；原样持久化 operation ID 与 provider snapshot provenance。suspend 或 resume 的 `ObserveOnly` 仅观察上次尝试，不启动另一 capture 或 restore。`ResumeCompute` 仅解冻保留源，不冷启动已停止源。清理针对精确 compute incarnation 和 snapshot，不针对当前同名实例。声明 checkpoint 支持前阅读 [`runtime_compute.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/execution/runtime_compute.go) 及其失败测试。

### 四个独立就绪事实 {#four-distinct-readiness-facts}

| 事实 | 证据 | 不证明 |
| --- | --- | --- |
| 计算资源可用 | Provider 对所属 allocation 的观测 | 已认证 Runtime 连接或已准备能力 |
| Runtime 已连接 | Gateway 认证和精确 Environment、device 绑定 | preparation 已完成或 Harness 可用 |
| 能力已准备 | 使用固定配置完成公共 Runtime preparation | Turn 已接受或完成 |
| 执行已准入 | 已验证 Harness 能力及 executor、Turn 接受路径 | 输入已完成、取消已完成或计算资源已回收 |

托管和自托管 Environment 使用相同 Runtime preparation；provider 不实现另一套竞争流程。

## 注册 provider kind {#register-the-provider-kind}

`sandbox/providers/registry.go` 是唯一注册表。每项绑定 adapter 的 specification 与 resource validator、`sandbox.ConfigurationAdapter`、部署模式（`nodes` 或 `direct`）、suspension 默认值、operation 声明，以及 node-local（`BuildLocal`）或 direct（`BuildDirect`）constructor。`providers.Build` 和 `providers.BuildDirect` 构造 adapter，不分配计算资源。没有 init 时注册或 plugin 加载。

新 provider 执行以下步骤：

1. 在 adapter 包中实现 operation 契约，并编写原生契约测试。
2. 添加 specification 和 resource validator；提交前需要原生资源发现时，添加可选只读 `SelectionDiscoverer`。原生凭据验证放在 `CredentialVerifier` 后。
3. 基于类型化原生配置实现 `sandbox.ConfigurationAdapter`。`DecodeInput` 严格解析请求中独立的公开 `configuration` 与只写 `credential` 对象。`Encode` 生成白名单公开 selector、只读观测和独立 secret bytes，不透传请求 JSON。`Decode` 恢复已存储 selector 并保留对所属资源的访问，不做远程 admission 或新模板验证。`Normalize` 修改前复制输入。`ResolveChange`、`Equal` 和 `WithCredential` 负责继承、身份与凭据组合。`Requirements` 声明是否需要凭据和公开 Core origin，以及是否支持配置发现。即使不支持 discovery，也实现 `ConfigurationDiscoverer`：验证 query 并返回安全 catalog，不做 mutation 或 admission decision；Core 保留授权、输入限制与 deadline。node provider 仅接受空公开对象，拒绝凭据，对 discovery 和 credential replacement 返回 Unsupported。
4. 在 `providers/registry.go` 中注册 constructor、policy、configuration adapter、operation 声明和默认值。Node proxy identity 和 checkpoint 支持读取此项。installer 投影组合已注册 policy 与 `sandbox/deployment_contract.go` 中的共享 field bound；通过 `go run ./services/core/cmd/specification-contract -write` 重新生成。
5. 提供 adapter 和 helper 的发行产物，通过已注册 configuration 契约向运维人员提供 provider。

**已知设计缺口：** Web 的 setup view 携带 provider 专有选项，如 E2B 的 view。通过该界面提供另一 provider 目前需要修改共享的 Web。此耦合不符合[复杂性留在 adapter 内](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#complexity-stays-in-the-adapter)；新集成必须通过协议表达配置，把厂商专有行为留在 adapter。不得添加 Session 或 Turn 调度路径、厂商专有 column 或 API field，或 store 中的厂商 switch。

`providers.Build` 将持久化 node 配置与临时 `LocalOptions` 传给 `BuildLocal`。调用方通过 `Standalone` 明确选择独立注册或单 provider 执行，或通过 `GenerationStateDirectory` 中的规范绝对 node state directory 选择 generation 拥有的执行。context 缺失或混合时拒绝。adapter 负责 generation 特有的原生 preparation 和 readiness 检查。Microsandbox 将 helper lease 绑定到安装实例、generation 和 specification digest，然后在平台、容量和 artifact readiness 后检查固定 image。

### 注册验证 {#registration-validation}

`providers.ValidateRegistration` 是唯一 wiring 检查。lookup、constructor binding 和 installer projection 在任何 configuration callback 或 constructor 前运行它。未知 provider name 保持为无效输入；格式错误的注册返回安全 `providercontract.ErrContract`，不包含提交的配置或原生诊断。

- `nodes` 注册仅有 `BuildLocal`，`direct` 注册仅有 `BuildDirect`；缺失、混合或未知 mode 被拒绝。
- specification 和 resource validator、configuration adapter 与完整 operation 声明都是必需项，因此不完整注册不能发布部分 installer projection。
- Runtime input policy 要么接受固定 Runtime，要么给出 adapter 拒绝它的固定原因，不能两者兼有。
- Checkpoint 支持要求 node mode 和适合 Runtime duration 的正 idle、retention 默认值；不支持 checkpoint 的 provider 不配置 suspension 默认值。

configuration adapter 必须非 nil，包括其具体值。每个 `ConfigurationRequirements` 字段都需要明确有效的决定：`Credential` 和 `PublicOrigin` 为 `Required` 或 `NotRequired`，`Discovery` 使用共享 supported 或 unsupported 声明并携带安全 reason。新增 requirement field 或 discovery method 需要明确更新验证，不继承已有决定。configuration discovery 与 resource selection discovery 不同，要求凭据也不承诺支持 `VerifyCredential` 操作。这些检查证明注册完整，不证明原生 SDK 行为正确；constructor 和 adapter 契约测试仍然适用。

### 配置存储与构造 {#configuration-storage-and-construction}

预览和持久化使用 `providers.Normalize` 与 `providers.Describe`。`SelectionDiscoverer` 在提交前解析省略的原生值，持久化时再次验证完整 specification。`providers.ResolveChange` 负责配置继承，比较使用 normalized selector，使预览、重试和提交共享默认值。store 负责事务、凭据加密、generation fencing、资源所有权和通用对象存储：仅 adapter 解释 `provider_config` 与 `provider_metadata`，`provider_credential` 保存绑定到安装实例与 generation 的密文。保留 generation 保持原公开配置和 metadata，通过 adapter 组合当前凭据，因此替换凭据不重写保留 selector。数据库约束检查对象结构，不检查注册列表。

具有凭据的 direct adapter 在替换 key 前验证全部保留 generation 与 allocation reference。公共 `sandbox.CallFence` 排除原生调用并等待 helper 完成，包括调用方已超时的调用；execution 调用已准备的 verification 和 fencing callback，不按厂商分支。

厂商部署验证和 SDK setup 留在构造边界，构造不创建 Environment。node-local adapter 的 `providers.Built` 返回 provider、probe、installation identity、backend fingerprint 和 specification digest，factory 还返回 close 函数。`execution.RuntimeProvider` 将 adapter 绑定到 kind、installation ID、backend fingerprint、generation、mode 和 node ownership；选择由数据库负责，内存副本不构成另一权限来源。Docker 与 microsandbox 在 node 上运行，E2B 直接构造。node proxy 仅对注册声明支持 checkpoint 的 backend 暴露 checkpoint 操作，公共 lifecycle 通过 `CheckpointProvider` 准入 suspension，不通过 provider name。

backend fingerprint 标识原生资源命名空间，不表示容量。Core 保留部署 generation，使所属 allocation 继续解析到原 backend；不要将保留 allocation 重新指向替代 backend。

### 发行产物与进程路径 {#distribution-artifacts-and-process-paths}

每个 node adapter 注册负责其类型化 `NodeArtifacts` 声明：逻辑发行路径、release filename suffix 和安装角色（`node`、`runtime`、`policy` 或 `image`）。注册拒绝缺失声明、不安全路径和未知角色。`go run ./services/core/cmd/provider-artifacts -write` 生成共享 Web catalog 和 Python projection。不带 `-write` 运行可检查是否最新。发行打包、Web availability 和 node 安装读取此投影；添加 provider payload 不在这些消费者中增加 provider-name 分支。

launcher 从[派生进程环境](configuration.md)提供 `sandbox.ProcessPaths`。Core 读取这些路径一次，并传给 direct construction 和 configuration discovery。它们是固定发行属性，不是部署设置或用户可选 helper 路径。每个 adapter 解析自己的相对 helper 和 state 位置；E2B 使用 `e2b/oac-e2b-provider` 和 `e2b/`。root 缺失或不是绝对路径时，在执行 helper 前失败。Provider 构造与发现不读取进程环境变量。

## 托管生命周期 {#managed-lifecycle}

这是 Core 对每个 provider 执行的公共流程。Adapter 不实现这些流程，但依赖它们。

### 部署发布 {#deployment-publication}

启动时 Core 在选择 provider 前领取稳定 installation identity 和新的 owner epoch。runtime manager 保留了解 generation 的 provider facade。初始 setup 与 replacement 在任何数据库写入前准备并验证 candidate；candidate 被拒绝时不改变活动配置或 worker。backend replacement 使用 deployment mutation gate：暂停 manager admission，排空旧调用与循环，再在 commit transaction 中重复 resource 与 generation guard；新选择、其 generation、旧 node 和未使用 enrollment token 的退役一起提交。提交后，Core 在 manager mutex 下发布预验证配置和共享 observation、bootstrap cache，没有进一步外部工作或可失败步骤，因此提交后取消的请求不能丢弃配置。中断的 drain 保留为重试 barrier。Provider I/O 和 drain 不持有数据库事务或 manager map mutex。

本地 provider 依赖不可用时，现有 scan 等待修复，hosted admission 保持关闭；管理员恢复仍可使用，重启后也如此。数据库和所有权错误仍是失败，未配置 hosted admission 不创建 Session 状态。Core 从 installation public URL 派生 Runtime bootstrap 和 daemon WebSocket 地址，不使用请求 header；从数据库读取当前选择，不使用 startup file。

Node readiness 绑定到精确 generation、当前连接和 owner epoch。持久 serving pin 仅在部署串行化下为当时目标的 readiness 提升，因此已被替代目标的延迟报告不获得 pin。

### Allocation 生命周期 {#allocation-lifecycle}

allocation、专用 daemon credential digest 和精确 Session binding 在 `Create` 前、execution lease 与 Session lock 下原子提交。只有新 allocation receipt 允许 `Create`；重试和 Core 重启观察同一 reference，不重放或轮换凭据。allocation 是私有计算资源所有权，与公开 Environment connection 和原生 readiness 独立；adapter 验证 bootstrap completion，Core 不从 engine 或 provider name 推断。

配置 provider 后，Worker 扫描已提交且没有 allocation 的 pending hosted Environment，涵盖空闲 Session 创建以及 commit 与 bootstrap 之间中断后的恢复；已有 allocation 不重新进入此路径。scan 有界，由 lifecycle owner 串行化，不需要调用方操作。没有 Turn 的初始预约让 Session 保持空闲，daemon 连接不被当作原生 readiness。同一 scan 在验证精确 Session、device binding 和已结算 bootstrap 后，发布带持久 generation 的认证连接观测。

已连接且已观察的计算资源在 Turn 之间接收 service keepalive。keepalive 不复活一小时的中断或 cleanup 请求。node allocation 不仅因 keepalive 已过一小时而到期；显式删除和 snapshot retention 仍授权其清理。停止或缺失 container 不授权丢弃保留工作区或历史。禁用 provider 停止新 hosted admission 与 bootstrap，但不阻止现有 Session 的取消、function result 或 input retry outcome。

终结清理原子撤销 device authority、记录 Environment 失败或到期、结算 pending input 并请求取消，然后才调用 `Kill`；原 input deadline 与 retry outcome 保留。临时 provider outage、未知 Create result 和停止的计算资源不证明永久失败。公开 Session 删除后 Core 保留 allocation，仅在所属 compute 与 volume 清理完成且原 Create 已结算的证明成立后标记 released；未知创建即使观察到不存在也保留 cleanup ownership，有界 scan 继续捕捉延迟资源，不再调用 `Create`。

### 每节点生命周期 worker {#per-node-lifecycle-workers}

每个注册 node 有一个串行 lifecycle worker，负责 gate、allocation 与 pending cursor、connection 和 wake hint；E2B allocation 共享一个没有 node 的串行 lifecycle。薄 coordinator 发现 node 并关闭 worker，数据库、provider 或等待操作期间不持有 map mutex。worker 独立推进，因此一个在线 node 的 provider 卡住不会阻塞其他 node：lifecycle 并发为每 node 一项操作，随 node 数量增长。离线 worker 保留，因此其保留资源在重连后仍可观察。

allocation scan 在应用 32 行分页限制前按 node 过滤，pending scan join 尚未释放的已提交 placement。每个 node 推进自己的 cursor，包括越过失败观测，并在末尾回绕一次。direct provisioning 在进入该 node gate 前解析 tenant 范围内 placement，已有 allocation 必须与其一致；Core 不选择另一 node。

释放 execution lease 前，coordinator 停止接受工作，取消并排空每个 node worker 和 direct caller。lease 丢失影响全部；普通 provider failure 限于所属 node。计划 deployment drain 或 node retirement 通过五秒有界 lease gate，在 leased operation 之间同步取消 lifecycle context，包括活动 manual reconcile，不仅为了改变配置就取消进行中的 leased query。失败的 cancellation fence 关闭 manager admission 并报告 owner failure。失败的 retirement 保留原 lifecycle identity 和 gate，直到 owner shutdown，drain barrier 保持关闭。Session lock、deployment capacity transaction 和 revision-checked receipt 仍是权威依据，外部操作不持有数据库锁。

### Placement 与容量 {#placement-and-capacity}

placement 自动完成：environment-to-node placement 与 Session 创建及其 retry identity 一起提交，调用方不能选择 node，重试即使 node 离线也保留原 node。Node capacity 计入 pending reservation 和未决资源，新 placement 与 suspended-to-restoring 转移共享数据库锁。未知操作保留预约，source teardown 必须确认后才能释放 active capacity，确认 cleanup 后释放 placement capacity。保留所有权需要精确 provider evidence：socket path、缺失 instance 或空列表都不证明 cleanup，也不授权 replacement。

部署的 CPU、memory、disk 设置、`max_active`、`max_retained` 和 snapshot retention 限制每个 node。不提供 node-level drain、跨 node Session migration、multi-active Core、autoscaling 或 snapshot replication。node 持有 allocation、snapshot、reservation、unknown result 或 cleanup 时拒绝移除 node，离线 ownership 保留。

### 暂停 {#suspension}

支持 checkpoint 的 provider 可以暂停空闲工作；部署 [`suspension`](../../contracts/agents-api/zh/sandbox-deployment.md#safe-response) policy 设置 idle time 和 snapshot retention。Core 仅在至少一个 Turn 已终结、没有 root 或 Subagent Turn 排队、进行中或等待、没有 pending input、file operation 或 initialization，且真实 activity 已空闲达到配置间隔后暂停。对于 node allocation，Core 在同一事务中用数据库时钟记录首个 root 或 child terminal transition。candidate filter 和 Session-locked recheck 比较数据库已过时间与 idle duration，初始 snapshot retention deadline 也锚定同一数据库观测，因此 Core 与数据库主机时钟无需一致。原生 completion timestamp 在公开历史中保持不变，但不驱动 idle admission，heartbeat 不重置 activity。确认计划暂停前，daemon 关闭 admission 并排空 native cleanup、output receipt 和 file work。

Worker lease、Session lock 与 per-node gate 对每个 provider 负责 suspension。新 Turn claim、file-write intent 和 capture admission 在 Session lock 下串行化，共享一个 compute-phase 检查；新 pending work 取消 capture 并唤醒同一 source。正常 preparation 在经过认证的 resume handshake 后等待 compute phase 为 running；pending input 的 promotion 与 lifecycle transition 冲突时保持 pending。compute phase 和 revision-checked receipt 位于 allocation。Core 在 effect 前持久化 quiesce、capture 和 restore intent，仅新 receipt 执行 capture 或 restore，恢复观察精确 attempt，不重试未知 creation、capture 或 restore。已消费 snapshot 不让 running generation 回滚。删除、撤销和 retention expiry 优先于 wake，一直持续到最终数据库 compare-and-swap；未知 cleanup identity 保留，直到确认所属资源不存在。已消费 artifact 和旧 compute 被删除，因此暂停循环不累积可写磁盘链。

排队工作和实时 Environment file access 唤醒 suspended Environment；history 和已发布 Artifact read 不唤醒。计划暂停在 daemon 连接上使用 Environment 和 suspension token。受 PID 与 start-time fencing 的本地 control signal（`RunCommandCompute`）唤醒 parked daemon，daemon 在准入工作前重新认证。确认前临时断连通过有界 attempt 和 backoff 重试同一已 armed suspension；永久认证或协议拒绝则关闭。Core 负责 snapshot retention deadline，daemon 没有相应 timer。quiesce 确认丢失时可以通过明确 rollback 解冻同一 source，但不授权 capture。

### 重置与归档 {#reset-and-archive}

[reset](../../contracts/agents-api/zh/sandbox-deployment.md#reset) 是 runtime manager 在计数工作之外推进的持久 execution state。start、escalation、cancellation、setup、update 和 finalization 通过 mutation gate 串行化。空闲状态先在 Session lock 再在 deployment lock 下复查，finalization 时不反向加锁。工作使用 keyset paging，绑定 reset request time 和 generation，并持久化 absolute deadline 与已验证 audit provenance。

一个 snapshot 和 timestamp 对持有资源分区。离线 ownership 来自 allocation 或 active placement 的 node，与 online presence 使用同一 45 秒 connection 和 owner-epoch predicate，独立于 provider readiness；cleanup failure、offline state 或空 read 都不授权合成 release。held resource 为零时，Core 在数据库事务外 drain，在 deployment lock 下复查，并在一个事务中清空 deployment，再发布携带 generation 的空 provider，没有可失败工作。最终 write 或 drain 失败时，在释放 mutation gate 前使用有界 owner context 恢复已提交 provider；恢复失败时 admission 保持 fenced，owner 停止。

管理员 [Session archive](../../contracts/agents-api/zh/admin-api.md#session-archive) 在 Session-first transaction 中保留 Project scope 与 hosted eligibility 检查，并一起处理 Environment expiry、cancellation、Runtime authority revocation 和 audit；reset 的后台 archive 从可信记录重建实际 Project scope，保留请求方 provenance。普通 provider lifecycle 释放 compute 和 snapshot。archive cancellation 在 terminal commit 前保留健康 receipt path：仅首次撤销 device 的 archive 记录精确 `archive_cancel_turn_id`（普通 revocation 清空它，重复 cleanup 保留它），现有已认证 delivery 可从 Turn 原 `cancel_requested_at` 起最多 20 秒排空该 cancellation。Core 独立于 subscription removal，通过 `done`、cancellation acknowledgement 和 terminal commit 跟踪 delivery，不授予新 connection、input、file 或 MCP authority，也不续期 lease。事务或 lifecycle gate 不等待 receipt，peer 丢失、到期或重启回到普通 failure 与 cleanup，不虚构 cancelled outcome。

## 验证集成 {#validate-the-integration}

installer 与 Go adapter 的 E2B template 和 endpoint validator 消费共享 [selector fixture](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/sandbox/e2b/testdata/configuration-selectors.json)。任何验证修改都扩展这些 case，使两处入口接受相同 selector。

开发时运行 `make check-sandbox-provider-contract`。它通过真实 adapter 边界和受控原生失败运行共享 [`contracttest`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/contracttest) 套件，以及 adapter 和 node transport 测试；`make check` 包含相同 package。使用 native-side fixture 调用公开 failure runner，不使用假的 `SandboxProvider`，保留外来 ownership、未知 mutation result、cancellation、不自动 replay、failed cleanup 和 reference-bound settlement 测试。

node 测试单独覆盖 disconnect、reconnect fencing，以及 Create response 丢失后的 cleanup。helper protocol 和 [sandbox node 协议](../../contracts/agents-api/zh/node-generation-protocol.md)要求精确版本匹配；直接进程内接口没有独立 wire version。

原生验收证明 fixture 无法证明的事实：creation、lease 行为、所属 partial cleanup、声明的 isolation 与 limit，以及支持时的 snapshot。显式启用的 Docker lifecycle 和 recovery 测试使用 `AGENTS_RUNTIME_DOCKER_TEST_IMAGE`；SDK helper 使用 `make check-e2b-provider` 和 `make check-microsandbox-provider`。mock compute 不能证明 reclamation 或 isolation。

## 参考 adapter {#reference-adapters}

| Kind | Adapter | Helper 与 adapter 规则 | 运维指南 |
| --- | --- | --- | --- |
| Docker (node) | [`sandbox/docker`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/docker) | [`sandbox/node`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/node) 中的 node proxy | [Docker adapter](#docker-adapter) |
| microsandbox (node) | [`sandbox/microsandbox`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/microsandbox) | [`tools/microsandbox-provider`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/microsandbox-provider/README.md) | [Node](getting-started/nodes.md) |
| E2B (direct) | [`sandbox/e2b`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/e2b) | [`tools/e2b-provider`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/e2b-provider/README.md) | [沙箱部署](../../contracts/agents-api/zh/sandbox-deployment.md#e2b-configuration)；应用管理的模板见 [`deploy/e2b`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md) |

## Docker adapter {#docker-adapter}

Docker Sandbox Provider（[`sandbox/docker`](https://github.com/MiniMax-AI/OpenAgentCore/tree/main/services/core/internal/sandbox/docker)）对所有 Runtime image 使用相同 container setting，无论服务哪个 Harness（[`container_options.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/sandbox/docker/container_options.go)）：

- user 1000:1000、只读 root filesystem、移除全部 capability、`no-new-privileges`、[seccomp profile](#seccomp-profile) 和 AppArmor `unconfined`；
- node 配置的 network 和 extra host（[node 配置](configuration.md#docker-node-configuration)）；
- deployment specification 中的 CPU 和 memory，128-process limit 和 128 MiB `/tmp` tmpfs；
- 两个 named volume，label 包含 installation、tenant、Environment 和 allocation：`<name>-home` 挂载到 `/home`，`<name>-environment` 挂载到 `/environment`，后者的 `workspace` 子目录也挂载到 `/workspace`。Docker Engine 必须支持 volume subpath mount；
- 配置 `nested_sandbox` option 时，解除 Docker `/proc` mask（`/sys/firmware` 和 `/sys/devices/virtual/powercap` 保持 mask），container 运行 init process。

Create 拒绝复用没有 container 的保留 volume。它将 [Runtime 引导](runtime-bootstrap.md)文件复制到 `/home/runtime/runtime-bootstrap.json`（mode 0600、UID 1000），并将 `/environment` workspace、staging、initialization 和 package directory 放入 container，然后启动 `oac-daemon connect --profile default --bootstrap-file /home/runtime/runtime-bootstrap.json`。创建的 container 不具备配置的 CPU、memory 和精确 image 时，Create 返回 error 和 `CreateSettled`。Docker 没有 lease，因此 Renew 仅读取 container state。Kill 在删除前检查 container 和两个 volume 的 ownership label，再确认三者都已不存在。

node 使用 [provider 配置](configuration.md#docker-node-configuration)中的明确 Unix socket，忽略 `DOCKER_HOST`。不将 Docker socket、host home 或 Core credential 挂载进 Runtime。

### Seccomp profile {#seccomp-profile}

[`seccomp.json`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/codex/seccomp.json) 是 [revision 65adc7e](https://github.com/moby/profiles/blob/65adc7e022c97f55e45c054ff012988027733b87/seccomp/default.json) 的 Moby default profile（Apache-2.0，参见 [seccomp.LICENSE](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/codex/seccomp.LICENSE)；上游文件 SHA-256 为 `785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334`），追加一条允许 `clone`、`unshare`、`setns`、`mount`、`umount2` 和 `pivot_root` 的规则。发行包将此文件作为 `runtime/seccomp.json` 交付每个 Docker node。
