---
title: "Core 运行指标"
source: contracts/agents-api/core-metrics.md
source_hash: 08ac6802f0da2eb5138dc7706a995754a69e7c864e034cf412f75dcc4950c679
---

`GET /core/v1/metrics?range=1h|6h|24h|7d` 报告 Core 自身的健康状况：进程、执行队列与槽位、PostgreSQL 和后台任务。它要求 Core 密钥（[Core 管理 API](admin-api.md)）。

`range` 是唯一参数，最多发送一次，默认为 `1h`。空值、重复或不支持的值以及任何其他参数均返回 400 `invalid_request`。Core 无法读取指标时，路由返回 503 `core_metrics_unavailable`。仅部分测量失败时，响应仍为 `200`，`service.status` 为 `degraded`，每个缺失值为 null。响应不会包含数据库或原生错误文本、凭据、响应体、资源 ID 或租户标签。

## 时间与缺失数据 {#time-and-missing-data}

响应中的 `range` 包含 UTC RFC 3339 格式的 `start` 和 `end`，以及 `resolution_seconds`。`end` 为最近的完整桶边界；区间为 `[start, end)`，因此不包含当前未完成的桶。

| 范围 | 桶大小 | 桶数 |
| --- | --- | --- |
| 1h | 60 秒 | 60 |
| 6h | 300 秒 | 72 |
| 24h | 900 秒 | 96 |
| 7d | 7200 秒 | 84 |

- 执行槽位、已连接 daemon、连接池、Go 堆和 goroutine 在请求到达时读取。进程 CPU、RSS 与限制、队列数量和数据库大小来自 Core 每 30 秒采集的样本；超过 60 秒的样本不会作为当前值报告。
- 每个序列桶报告其中观察到的最高值，而非每一个中间峰值。缺失观测和进程启动时不完整的首个桶为 null。
- 样本和拒绝计数在内存中保留七天，另加两小时用于桶对齐。重启会丢失它们；Core 不回填。Turn 历史来自 PostgreSQL，重启后仍保留。
- 如果区间开始早于该进程开始观察的时间，`execution.unavailable` 为 null；完整观察且没有拒绝的区间为零。
- 空队列的数量为零，最老年龄为 null。没有已开始 Turn 或成功 ping 样本时，百分位数为 null，而非零延迟。周期 ping 的百分位数（p50、p95）采用线性插值；请求不会触发 ping。

## 字段 {#fields}

响应包含 `object: "core.metrics"`、`range`、`service`、`execution`、`database`、`jobs` 和 `process`。所有数值和 `service.execution_owner` 均可为 null；每个 `series` 始终列出范围内的所有完整桶。

| 字段 | 含义 |
| --- | --- |
| `service.status` | 通常为 `running`；测量或任务失败、最新样本缺失或过期、执行所有权未知，或 Core 有执行槽位却未持有执行租约时为 `degraded`。沙箱重置由[部署](sandbox-deployment.md)报告，不在此处报告 |
| `service.revision` | 构建时注入的完整源代码提交；未注入时为 null |
| `service.started_at` | 进程初始化时间 |
| `service.execution_owner` | 此进程是否持有执行 worker 的数据库租约 |
| `execution.slots_in_use`, `execution.slots_total` | 执行 worker 的活动 Session 预留数量及容量：[`core.execution_concurrency`](../../../docs/zh/configuration.md#settings)，默认为 4。Environment 输入、Turn 和文件工作共享槽位；不统计原生 Harness 子进程。没有 worker 时两者均为 0 |
| `execution.queued_turns`, `execution.in_progress_turns` | 处于相应状态的根 Turn，包括已删除 Session 的 Turn。不统计 Subagent Turn 和为准备中 Environment 预留的输入 |
| `execution.waiting_for_daemon` | Session 设备未连接的排队 Turn；无网关时为 null |
| `execution.oldest_queued_seconds` | 最早排队 Turn 自 `created_at` 起的年龄 |
| `execution.connected_daemons` | 连接到 Core 网关的 Runtime daemon 数量；无网关时为 null |
| `execution.queue_wait_ms` | 区间内开始的 Turn 的 `started_at - created_at` 的 p50 和 p95，使用 PostgreSQL `percentile_cont` |
| `execution.interrupted` | 错误代码为 `execution_interrupted` 且 `completed_at` 在区间内的失败 Turn |
| `execution.unavailable` | 使用错误代码 `execution_unavailable` 发送的 HTTP 响应，每个计一次。不统计其他 503 代码或流开始后的错误 |
| `execution.series` | 每桶：`queued`、`in_progress` 和 `queue_wait_p95_ms` |
| `database.ping_ms` | 周期连接池 ping 的 p50 和 p95，包括获取连接的时间 |
| `database.pool` | 连接池的 `in_use`、`idle` 和 `max` 连接数量 |
| `database.size_bytes` | `pg_database_size(current_database())`，不是主机磁盘使用量 |
| `database.series` | 每桶：`ping_p95_ms` 和 `pool_in_use` |
| `process.memory_bytes`, `process.goroutines` | Go `runtime.MemStats.Alloc`（已分配堆，不是 RSS）和 `runtime.NumGoroutine()` |
| `process.cpu_cores` | 进程用户态与系统态 CPU 时间增量除以采样经过时间（Linux `getrusage(RUSAGE_SELF)`），不包括子进程。首个区间为 null；计数器缺失或重置、区间非正或间隔超过 60 秒时重建基线 |
| `process.rss_bytes` | Linux `/proc/self/status` 的 `VmRSS`，单位为字节 |
| `process.cpu_limit_cores` | 进程 cgroup v2 的 `cpu.max` 配额除以周期；配额为 `max` 或 cgroup 无配额接口时为 `GOMAXPROCS` |
| `process.memory_limit_bytes` | 进程 cgroup 的 `memory.max`；为 `max` 时返回 null |
| `process.series` | 每桶：`cpu_cores` 和 `rss_bytes` |

Core 解析自身的 cgroup，包括嵌套与子树挂载，并报告该 cgroup 的限制，而非主机或祖先的限制。无法读取或格式错误的值为 null。非 Linux 构建报告 null CPU、RSS 和内存限制，CPU 限制为 `GOMAXPROCS`。仅缺失进程测量不会使服务变为 `degraded`。

## 后台任务 {#background-jobs}

`jobs` 列出 `scheduler`、`runtime_sampler`、`history_cleanup` 和 `audit_cleanup`。每项包含 `status`（首次运行前为 `unknown`，之后为 `ok`、`failing`，循环禁用或结束时为 `stopped`）、`last_run_at`（上次一轮结束时间）、`processed` 和 `failed`，后两者描述上次一轮。

- scheduler 的 `processed` 统计上次轮询选中的 Turn 和 Environment 工作。
- Runtime sampler 的 `processed` 和 `failed` 统计上次扫描成功观察和未能观察的目标。
- 清理任务的 `processed` 统计删除的行数。
- scheduler 和清理任务失败时，`processed` 为 null，`failed` 为 1；`failed` 不估算丢失行数或失败 Turn 数量。
