---
title: "运行时可观测性"
source: contracts/agents-api/runtime-observability.md
source_hash: 5da1279a81a6dcb3a85661dbba942c8937f871ae351ed80550db65b2a59156db
---

这是面向贡献者的契约，规定 Core 如何观测 Runtime 并保留其历史。路由和响应字段见 [Runtime telemetry API](runtime-observability-api.md)。代码位于 `services/core/internal/runtimeobs`（解析、源、采样器和导出）、`internal/runtimehistory`（历史查询和 PostgreSQL 存储）以及 `internal/runtimeobs/otlpexporter`。

观测属于遥测数据。它们绝不会创建、续期、唤醒、恢复或停止计算，绝不会影响 Session 活动，也绝不会决定空闲时间、挂起、准入或执行结果。

## 身份与源选择 {#identity-and-source-selection}

在读取 provider 之前，Core 会先把每次观测归属到持久化的 Core 身份：

```text
managed:     tenant_id -> session_id -> environment_id -> runtime_allocation_id
self-hosted: tenant_id -> session_id -> environment_id -> device_id + connection_generation
none:        tenant_id -> session_id (no Session-owned Runtime instance)
```

解析器（`internal/runtimeobs/storeresolver`）从存储中读取 Session、其 Environment、当前分配以及 Session 的实测使用量。Session、守护进程连接、进程、容器和原生 Harness Session 是不同身份，彼此绝不能替代。

托管 Docker、microsandbox 和 E2B 分配均会被观测。`none` 和 `self_hosted` Session 为 `unsupported`；Core 绝不会将共享主机统计信息归属于 `environment:none` Session。

分配中持久化的 `provider_key` 会选择且仅选择一个已配置源；该源在返回数值前会验证分配标签或等效所有权数据。在读取任何 provider 之前，部分行的结果由分配状态决定：处于 `creating` 状态或尚无分配时得到 `allocation_pending`，处于 `cleanup_pending` 或 `released` 状态时得到 `runtime_not_running`，provider key 没有对应源时得到 `source_not_configured`。provider 读取超出截止时间时得到 `sample_timeout`，返回未运行结果时得到 `runtime_not_running`，返回不可用结果时得到 `sample_unavailable`。任何其他错误、所有权不匹配或无效采样都会使读取失败。

观测边界在 `services/core/internal/runtimeobs/source.go` 中声明。每个注册的 `SourceResolver` 都声明支持 `ResolveObservationSource`；注册过程会验证此声明，但不会加载配置或读取数据库。Core 每页只解析每个 provider key 一次，随后验证返回的 `Source`，并在该页上针对此 key 的每次读取中使用同一不可变源。未配置的解析器返回类型化的 `ErrUnavailable`，从而生成不含 provider 类型的 `sample_unavailable`。其他解析错误遵循上述 provider 读取错误规则。

源会声明支持的 `ObservationProviderType`，该类型返回其不可变遥测标识：一个小写字母，后跟最多 31 个小写字母、数字或下划线。空标识无效。在任何采样或导出之前，provider 注册和每个解析后的绑定都会验证标识及操作声明。重新配置会影响后续的源解析，但无法更改正在处理页面所选定的标识或 provider。Generation 路由器仍会通过每个分配记录的部署代次解析该分配。

源实现 `Observe`，并在 provider 操作中声明 `ObserveBatch`。声明支持 `ObserveBatch` 时，一次调用可读取该 provider 的最多 100 个目标；声明不支持时，Core 使用 `Observe` 读取每个目标。批量读取失败后绝不会逐个重试目标。[Sandbox Provider guide](../../../docs/zh/sandbox-provider.md) 说明了这些操作声明。

## 采样语义 {#sample-semantics}

一个采样包含：

- `observed_at`，即 provider 的观测时间；以及 `started_at`，即当前计算实例启动周期的开始时间；
- 累计 CPU 秒数，以及以核心数计的已配置 CPU 容量；
- provider 报告的 CPU 利用率比率，仅适用于不提供累计 CPU 时间的 provider（E2B）；
- 当前内存使用量，以及以字节计的内存限制；
- 当前磁盘使用量和容量，以字节计，且仅在 provider 报告这些值时提供（E2B）。

每项测量都是可选的。存在的零值表示观测到零；缺失值表示不可用，绝不会显示或聚合为零。Core 会拒绝以下采样：`observed_at` 晚于 Core 自身时钟，`started_at` 晚于 `observed_at`，或任一值为负数、非有限值、容量或限制值为零，或超出 JSON 安全整数范围。

`lifecycle_state` 根据 Core 记录中的分配状态和计算阶段派生，绝不会从采样派生：没有分配或状态为 `creating` 时为 `pending`；状态为 `running` 且计算阶段为 `running` 或 `disabled` 时为 `active`；`suspended` 为 `sleeping`；`quiescing`、`suspending`、`restoring` 或 `waking` 为 `transitioning`；`cleanup_pending` 和 `released` 为 `stopped`。

## Provider 映射 {#provider-mapping}

### Docker {#docker}

对所属容器进行一次非流式 Inspect 和 Stats 读取。累计 CPU 时间来自 cgroup 计数器，内存使用量来自当前 cgroup 使用量，CPU 和内存容量来自容器配置的容量限制。容器的 `StartedAt` 是本次计算实例启动周期的开始时间，因此重启容器会重置计算运行时长。容器缺失或已停止时为 `runtime_not_running`。磁盘为 null。

### microsandbox {#microsandbox}

处于挂起状态的分配为 `runtime_not_running`，且不会调用 helper。否则，Core 会针对分配中记录的确切计算实例向 helper 发送 `metrics` 请求。在分配锁保护下，helper 通过固定版本 SDK 检查 sandbox 身份，运行固定版本的 `msb metrics <name> --format json` CLI 读取，再次检查身份；sandbox 必须处于 running 或 draining 状态。[microsandbox helper](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/microsandbox-provider/README.md) 负责此请求。

累计 vCPU 时间、来宾内存使用量和有效内存限制均来自该次 CLI 采样。CPU 容量为部署中配置的 CPU 数量。`started_at` 等于采样自身的时间戳减去其毫秒级运行时长，绝不能从较晚读取的时钟值中减去已取整的运行时长；因此，恢复后的 generation 会重置计算运行时长，而分配时长会继续累计。不使用瞬时 CPU 百分比、主机内存、磁盘和网络值，磁盘为 null。对于关闭了挂起功能但未记录计算实例身份的分配，结果为 `sample_unavailable`；其他任何未记录计算实例身份的分配都会使读取失败；绝不会改用确定性 sandbox 名称。

### E2B {#e2b}

一次 helper `observe` 请求会读取一页最多 100 个分配：读取私有回执中指定 sandbox 的 E2B 批量指标，并获取该 installation 中运行中 sandbox 的带标签列表，以确认每一个 sandbox。[E2B helper](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/e2b-provider/README.md) 负责此请求。它绝不会连接、续期或更改 sandbox，也不会写入任何回执。

| E2B 值 | 采样字段 |
| --- | --- |
| `cpuUsedPct / 100` | CPU 利用率比率 |
| `cpuCount` | CPU 容量 |
| `memUsed`、`memTotal` | 内存使用量和限制 |
| `diskUsed`、`diskTotal` | 磁盘使用量和容量；仅当两者都存在且总量非零时保留 |

E2B 不报告累计 CPU 时间，因此 CPU 秒数保持为 null。`observed_at` 为 E2B 的时间点；比 Core 时钟最多领先 30 秒的时间点按 Core 时间记录，领先幅度更大时则为 `sample_unavailable`。未出现在运行列表中的 sandbox 为 `runtime_not_running`。时间点缺失或格式错误、列表存在歧义以及 E2B API 失败（包括 key 被拒绝）均为 `sample_unavailable`；格式错误的时间点只影响其所在行。

## 读取预算 {#read-budgets}

当前列表读取处理一页最多 100 个 Session（默认 20 个），provider 读取并发数最多为 8。每次 provider 读取的时限为 2 秒，批量读取至少为 5 秒，整个列表请求为 10 秒；超过这些时限时，列表返回 503。单 Session 读取的时限为 2 秒。一次请求内不会重试任何 provider 调用，Core 也不保留观测缓存。

## 时长 {#durations}

以下时长回答不同问题，彼此保持独立：

- 分配时长：`runtime_allocations.created_at` 到 `released_at`，或到当前时间；
- 计算运行时长：采样的 `started_at` 到 `observed_at`；
- 忙碌 Turn 时长：`turns.started_at` 到 `completed_at`，或到当前时间。

CPU 静默状态、心跳时龄、连接状态和保活时间都不是空闲时间。

## 保留的历史记录与可选导出 {#retained-history-and-optional-export}

### 周期采样 {#periodic-sampling}

周期采集仅随执行工作器运行而执行（Core 需使用 `OAC_PUBLIC_URL` 启动；见 [Core environment](../../../docs/zh/configuration.md#appendix-core-environment-without-the-installer)），并受该工作器的数据库租约保护。未运行该工作器的 Core 不存储历史记录，所有历史读取都返回 503；当前读取在两种情况下均可正常工作。

采样器在启动时扫描一次，此后每次扫描结束后再经过一个采样间隔再次扫描。一次扫描按 Session ID 顺序，对未删除、状态为 `openai_hosted` 且没有已释放分配的 Session 执行 keyset 扫描。它通过与当前读取相同的解析器和源，以 32 个 Session 为一页进行读取，并发数为 8，每个源时限为 2 秒。采样器在每页之前以及扫描期间每 100 ms 检查租约；失去所有权时取消进行中的读取；将每条记录交给导出之前再次检查租约。失败的行不会停止扫描；未完成的扫描会在下一个间隔重复。

每次观测，无论来自当前读取还是周期采集，都会标记采集源 `on_read` 或 `periodic`，并放入每个导出器的有界队列。队列已满时会丢弃记录；该记录将成为缺失采样，而绝不会成为零。PostgreSQL 历史存储和可选 OTLP 导出器使用彼此独立的队列，因此导出器故障不会延迟本地历史记录或执行。[`core.runtime_history` settings](../../../docs/zh/configuration.md#settings) 用于设置采样间隔、队列容量、超时和 OTLP 目标。

### 存储的历史记录 {#stored-history}

PostgreSQL 存储仅保留周期性的 `openai_hosted` 记录，因此 API 读取无法虚增覆盖率。每条记录都会写入一行 `runtime_history_samples`，以租户、Session、Environment 和解析时间为键，包括分配信息、provider 类型、状态、观测时间与启动时间、CPU 秒数、容量和利用率比率、内存使用量和限制，以及实测令牌计数器。磁盘数据不存储。缺少 `started_at` 的采样仍计入覆盖范围，但会丢弃资源值，因为这些值无法关联到某次计算实例启动周期。

历史服务在查询前解析 Project、Session 和 Environment；查询始终携带该作用域和有界时间范围，但绝不携带 provider 身份。存储保留 7 天。单次读取最多覆盖 24 小时，最多读取 20,000 条原始采样，并从范围起点之前两个采样间隔处开始读取，以查找 CPU 基线；每个数组最多返回 1,000 个桶，最多返回 64 个序列，总点数最多 10,000 个。结果超出请求的作用域、时间范围或限制时，读取失败。API 的 [Series](runtime-observability-api.md#series) 部分说明了聚合方式。

`runtimehistory.Capabilities` 声明采集模式、间隔、7 天保留期、最小桶宽度（30 秒或采样间隔，取较长者）、24 小时范围和点数限制；历史路由仅在所有这些值有效且采集模式为 `periodic` 时响应，否则返回 503。

清理循环每分钟运行一次，即使没有活跃 Runtime 也会运行。每轮最多耗时 2 秒，按每表 256 行的批次删除过期 Runtime 行和节点主机行，每张表最多 16 批。读取绝不会返回超过保留期的行。

### 节点主机历史记录 {#node-host-history}

每次扫描结束后，Core 会在 2 秒内且在检查租约后，将每个新鲜节点心跳复制到 `node_host_history_samples`，其中包含主机 CPU 利用率、已用内存和可用磁盘，并以节点和心跳自身时间为键，因此重复扫描不会增加数据。心跳新鲜的判定条件是：其节点未被移除、在当前所有者 epoch 下处于连接状态、在过去 45 秒内被观察到，并且所报告的主机时间位于过去 45 秒内且不在未来。同样适用 7 天清理期。节点主机历史属于遥测数据，绝不作为调度或容量的权威依据，读取也绝不会对其进行采样或回填。

### 令牌使用量 {#token-usage}

每次观测都携带来自 `MeasuredSessionUsage` 的 Session 实测使用量，即所有已记录根 Turn 快照之和，其中包含活跃 Turn。这不是公开的 Session 使用量规则，并且 Core 绝不会从 provider 计数器、上下文占用量或成本推导令牌数。

### OTLP 导出 {#otlp-export}

配置 OTLP 端点后，Core 会将每条记录，包括 `on_read` 和 `periodic`，都导出为 OTLP/HTTP 指标。资源具有 `service.name=oac-core` 和 `service.namespace=oac`。

| 指标 | 聚合方式 | 来源 |
| --- | --- | --- |
| `agents.runtime.cpu.usage` | 单调累积和，秒 | 累计 CPU 计数器 |
| `agents.runtime.cpu.capacity` | Gauge，核心数 | 已配置的 CPU 容量 |
| `agents.runtime.cpu.utilization` | Gauge，比率 | Provider 报告的 CPU 占比（E2B） |
| `agents.runtime.memory.usage` | Gauge，字节 | 内存使用量 |
| `agents.runtime.memory.limit` | Gauge，字节 | 内存限制 |
| `agents.session.tokens.input` | Gauge，令牌 | 实测 Session 输入令牌 |
| `agents.session.tokens.output` | Gauge，令牌 | 实测 Session 输出令牌 |
| `agents.runtime.sample` | 单调差值和 | 每个经验证的结果一条，包括 unavailable 和 unsupported |
| `agents.runtime.sample.duration` | Delta 直方图，秒 | Provider 读取时长；批量读取仅计一次 |

仅当采样包含 `started_at` 时才导出 CPU 和内存数据点；缺少测量值不会产生数据点。属性包括 `agents.tenant.id`、`agents.session.id`、`agents.environment.id`、`agents.runtime.allocation.id`、`agents.runtime.mode`、`agents.runtime.provider.type`、`agents.runtime.status`、`agents.runtime.reason`、`agents.runtime.collection.source`，以及以纳秒为单位的 `agents.runtime.resolved_at_unix_nano`、`agents.runtime.observed_at_unix_nano` 和 `agents.runtime.compute.started_at_unix_nano`。这些纳秒时间使记录在后端以较低精度存储事件时间时仍可关联。Provider key、回执、原生标识符、原始错误、路径和凭据绝不会作为属性。

Web 仅通过 Core 读取历史记录；其图表不需要 Collector 或其他指标存储。
