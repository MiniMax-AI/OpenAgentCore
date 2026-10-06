---
title: "Runtime 遥测 API"
source: contracts/agents-api/runtime-observability-api.md
source_hash: d6111447def530b07c392d457cce0cd553f92dafb2e7cc7f481da47f816fcab2
---

Core 通过 `/core/v1` 下的只读管理员路由报告托管 Runtime 和沙箱节点所使用的信息：当前 Runtime 观测值、单个 Session 的已存储 Runtime 历史记录，以及沙箱节点的主机观测值和历史记录。读取操作绝不创建、唤醒、续期或更改计算资源，也绝不向历史记录添加样本。[Runtime observability](runtime-observability.md) 定义了 Core 如何采集和保留这些值；[Console API usage](../../../docs/zh/web/console-api-usage.md) 列出了读取这些值的 Web 页面。

每个路由都要求以 Core 密钥作为 Bearer 凭证；缺少或无效的密钥会返回 401 `invalid_admin_key`。路径中的 Project ID 用于选择目标 Project，不承担身份验证作用。响应带有 `Cache-Control: no-store`，采用 Core 错误封装格式，且绝不包含提供方响应、原生标识符、路径或凭证。

## 当前 Runtime 观测值 {#current-runtime-observations}

### 列出所有 Project 的观测值 {#list-observations-of-every-project}

```http
GET /core/v1/sandbox/runtime-observations?after={session_id}&limit=20&order=desc
Authorization: Bearer <Core key>
```

| 参数 | 规则 |
| --- | --- |
| `after` | 上一页末尾的观测 ID（一个 Session ID）。 |
| `limit` | 1 到 100，默认 20。 |
| `order` | 按 Session 创建时间采用 `asc` 或 `desc`，默认 `desc`。 |

列表中，每个未删除 Project 下的每个 Session 都有一行，包括模式为 `none`、`self_hosted` 的 Session 以及已释放的托管 Session。每行都包含所属 `project_id` 和一个 `observation`：[`RuntimeObservation`](#runtimeobservation) 与 [`disk`](#disk)。分页采用 Session 列表的创建时间与 ID 键集。观测 ID 就是 Session ID，因此即使 Session 背后的 Runtime 发生变化，分页边界也不会移动。页面不是原子快照：每行都有自己的 `resolved_at`，并在进行了采样时拥有 `observed_at`。未知的查询键会被忽略。

```json
{
  "object": "list",
  "data": [
    {
      "project_id": "3f0c2a9e-2b7d-4d0f-9a51-1c8e4b6d7a20",
      "observation": {
        "id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
        "object": "agent.runtime_observation",
        "session_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
        "environment_id": "6c02fb71-5fa8-4298-93e8-57c6625a3fc2",
        "mode": "openai_hosted",
        "provider_type": "docker",
        "instance": {
          "kind": "managed_allocation",
          "allocation_id": "d23ab94e-e40b-45bd-93a2-444f1f74642b",
          "device_id": "2e434f4f-76aa-4e54-a707-4757036d90ef",
          "connection_generation": null
        },
        "lifecycle_state": "active",
        "status": "observed",
        "reason": null,
        "allocation_created_at": 1789951200,
        "resolved_at": 1789953021,
        "observed_at": 1789953020,
        "started_at": 1789951220,
        "cpu": {
          "usage_seconds_total": 482.75,
          "capacity_cores": 2.0,
          "usage_cores": null,
          "utilization_ratio": null
        },
        "memory": {
          "usage_bytes": 805306368,
          "limit_bytes": 2147483648
        },
        "disk": null
      }
    }
  ],
  "has_more": false,
  "first_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
  "last_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3"
}
```

### 获取一个 Session 的观测值 {#retrieve-one-session-s-observation}

```http
GET /core/v1/projects/{project_id}/sessions/{session_id}/runtime-observation
Authorization: Bearer <Core key>
```

该接口返回一个不含 `disk` 的 `RuntimeObservation`。它不接受任何查询参数。`environment:none` Session 会返回 200，且 status 为 `unsupported`。

### `RuntimeObservation` {#runtimeobservation}

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `id` | string | Session ID；此资源及其列表游标的稳定标识。 |
| `object` | string | `agent.runtime_observation`。 |
| `session_id` | string | 对应的 Session。 |
| `environment_id` | string 或 null | 仅当 mode 为 `none` 时为 null。 |
| `mode` | enum | `none`、`self_hosted` 或 `openai_hosted`。 |
| `provider_type` | string 或 null | 来源类型，例如 `docker`、`microsandbox` 或 `e2b`；未读取任何提供方数据时为 null。未知值应视为新类型，而不是错误。 |
| `instance` | object | 当前计算资源标识；请参阅 [`RuntimeInstance`](#runtimeinstance)。 |
| `lifecycle_state` | enum 或 null | Core 自身对托管分配的生命周期视图；对于 `none` 和 `self_hosted` 为 null。请参阅下文。 |
| `status` | enum | `observed`、`unsupported` 或 `unavailable`。 |
| `reason` | enum 或 null | 该行没有样本的原因；请参阅 [Status and reason](#status-and-reason)。 |
| `allocation_created_at` | integer 或 null | 创建托管分配时的 Unix 秒数。 |
| `resolved_at` | integer | Core 解析此行时的 Unix 秒数。 |
| `observed_at` | integer 或 null | 提供方样本的 Unix 秒数；无样本时为 null。 |
| `started_at` | integer 或 null | 当前计算实例代次启动时的 Unix 秒数。 |
| `cpu` | object 或 null | 未观测到 CPU 值时为 null。 |
| `memory` | object 或 null | 未观测到内存值时为 null。 |

`lifecycle_state` 来自 Core 的分配记录，绝不来自样本：

| 值 | 分配 |
| --- | --- |
| `pending` | 尚未创建，或正在创建 |
| `active` | 正在运行 |
| `sleeping` | 已挂起 |
| `transitioning` | 正在静默处理、挂起、恢复或唤醒 |
| `stopped` | 等待清理，或已释放 |

### `RuntimeInstance` {#runtimeinstance}

| 字段 | 含义 |
| --- | --- |
| `kind` | `managed_allocation`、`self_hosted_connection` 或 `none`。 |
| `allocation_id` | 用于标识托管 Session 计算资源的托管分配；其他情况下为 null。 |
| `device_id` | 存在时，为绑定到托管分配的 Runtime 设备；其他情况下为 null。 |
| `connection_generation` | 始终为 null：Core 不观测自托管连接。 |

### `cpu` {#cpu}

所有字段均为有限非负数或 null。0 表示观测到的零；null 表示不可用。

| 字段 | 含义 |
| --- | --- |
| `usage_seconds_total` | 当前计算实例代次的累计 CPU 秒数（Docker、microsandbox）。 |
| `capacity_cores` | 已配置的 CPU 容量，大于零。 |
| `usage_cores` | 始终为 null。 |
| `utilization_ratio` | 提供方报告的 `capacity_cores` 占比（E2B），不进行钳制；对于报告累计 CPU 时间的提供方为 null。 |

### `memory` {#memory}

`usage_bytes` 和 `limit_bytes` 是 JSON 安全整数或 null。使用量为零表示观测到零；`limit_bytes` 至少为 1，未知或无限制的限额为 null。

### `disk` {#disk}

只有列表行包含 `disk`：其值为 null，或为遵循 `memory` 规则的 `{usage_bytes, limit_bytes}`。当沙箱同时报告磁盘使用量和非零容量时，E2B 会填充此对象。Docker 和 microsandbox 返回 null。非 null 的 `disk` 只出现在状态为 `observed` 的行中。

### 状态和原因 {#status-and-reason}

| 状态 | 原因 | 适用情况 |
| --- | --- | --- |
| `observed` | null | 提供方返回了样本。 |
| `unsupported` | `runtime_mode_not_observable` | `none` 和 `self_hosted` Session。 |
| `unavailable` | `allocation_pending` | 托管分配尚不存在或正在创建。 |
| `unavailable` | `runtime_not_running` | 分配正在清理或已释放，或者提供方报告 Runtime 不存在、已停止或已暂停。 |
| `unavailable` | `source_not_configured` | 该分配的提供方未配置任何观测源。 |
| `unavailable` | `sample_timeout` | 提供方读取超过其截止时间。 |
| `unavailable` | `sample_unavailable` | 提供方无法生成当前样本。 |

所有权不匹配、格式错误的持久身份或无效的提供方证据会使请求失败，而不会转换为 `unavailable` 行。生成的 `core.openapi.yaml` 会记录每个字段的类型、可空性和枚举，但无法表达 status、mode 与字段之间哪些组合有效；上表和上述字段规则具有规范效力。

### 错误 {#errors}

| HTTP | 代码 | 适用情况 |
| --- | --- | --- |
| 400 | `invalid_request_error` | 列表：重复提供 `after`、`limit` 或 `order`，或者 `limit` 或 `order` 无效。 |
| 400 | `unsupported_parameter` | 单项读取：提供任何查询参数。 |
| 404 | `not_found_error` | Project 不存在；Session 或列表游标不存在、格式错误或属于其他 Project。 |
| 500 | `internal_error` | 标识不一致或提供方证据无效。 |
| 503 | `execution_unavailable` | 列表读取超过其采集预算。 |

### 客户端 {#client}

`packages/agents-client` 公开了 `AdminClient.listRuntimeObservations({after, limit, order})` 和 `AdminClient.retrieveRuntimeObservation(projectId, sessionId, options)`。`src/types.ts` 中的 `RuntimeObservation` 是以 `status` 和 `mode` 为判别字段的联合类型；`AdminRuntimeObservation` 增加了 `disk`。客户端会检查每个字段、枚举、可空性规则、时间戳和数值，并拒绝未知字段。格式错误的观测值会触发 502 `invalid_runtime_observation` 错误，格式错误的页面会触发 `invalid_admin_response`；任意一行有误都会拒绝整个页面。

## Session Runtime 历史记录 {#session-runtime-history}

```http
GET /core/v1/projects/{project_id}/sessions/{session_id}/runtime-history?start=1789951200&end=1789954800&max_points=120
Authorization: Bearer <Core key>
```

| 参数 | 规则 |
| --- | --- |
| `start` | 必填。包含端点的 Unix 秒值，必须大于或等于 0。 |
| `end` | 必填。排除端点的 Unix 秒值，必须晚于 `start`，最多比 `start` 晚 24 小时，并且最多只能比当前时间晚 1 秒。 |
| `max_points` | 可选。每个数组的桶数，2 到 1000；默认 120。 |

每个参数最多只能出现一次。Core 会选择桶宽：将范围除以 `max_points`，向上取整为整数秒，并取 30 秒或采样间隔中的较长者作为最小值。桶从 `start` 开始；最后一个桶在 `end` 结束。

Core 先解析 Project，然后解析 Session 及其 Environment，之后才读取存储；分配和提供方标识都是结果，绝不能作为查询输入。只有 `openai_hosted` Session 存在历史记录。

```json
{
  "object": "agent.runtime_history",
  "source": "durable",
  "session_id": "6c77d3a2-71d6-4ed5-884f-687aecda02a3",
  "requested_range": { "start": 1789951200, "end": 1789954800 },
  "resolution_seconds": 60,
  "generated_at": 1789954801,
  "coverage": {
    "retained_start": 1789951200,
    "first_sample_at": 1789951210,
    "last_sample_at": 1789954750,
    "sample_count": 118,
    "expected_sample_count": 120,
    "buckets": []
  },
  "series": [],
  "token_usage": []
}
```

`source` 始终为 `durable`。`resolution_seconds` 是桶宽，`generated_at` 是读取时间。只有至少包含一个样本的桶才会出现在 `coverage.buckets`、`series[].points` 和 `token_usage` 中；缺口仍然是缺口，绝不会变成零值。

### 覆盖范围 {#coverage}

`coverage` 会对范围内 Session 的每个已存储样本进行计数，包括不属于任何分配的不可用样本。`retained_start` 是 `start` 与 `generated_at` 前七天两者中较晚的时间。`expected_sample_count` 是 `retained_start` 与 `end` 之间的采样间隔数，并向上取整。每个桶都包含 `start`、`end`、`first_observed_at`、`last_observed_at`、`observation_count`、`observed_count` 和 `unavailable_count`。

### 数据系列 {#series}

每个托管分配都有一条以 `allocation_id` 为键的 series，因此，即使提供方在同一分配下暂停、恢复或替换计算资源，也只会保留一条 series。`environment_id` 和 `provider_type` 用于标识其来源。`started_at` 是该分配的计算资源在保留范围内最早的 start，以 JSON 安全的 `{seconds, nanoseconds}` 表示，其中纳秒值为 0 到 999,999,999；它不是各个桶的开始时间，而且计算运行时长只能来自当前观测值。

每个点都包含该桶的边界、该分配样本的覆盖范围计数，以及可空的 `cpu` 和 `memory` 对象；其 `contributor_count` 至少为 1：

- 对于结束于该桶的所有区间，`cpu.utilization_ratio` 根据同一计算实例代次的连续累计 CPU 计数器得出：消耗的 CPU 秒数除以（经过时间乘以容量）。计算实例代次发生变化或计数器数值减小时，基线会重置。E2B 不报告累计 CPU 时间；其桶值是桶内采样比率的平均值。`cpu.capacity_cores` 是桶内最后一次容量值。
- `memory.usage_bytes` 和 `memory.limit_bytes` 是桶内最后观测到的值。

历史记录中不保存磁盘数据。

### Token 用量 {#token-usage}

`token_usage` 属于 Session，而不属于分配。每个点都保存其所在桶中最后采样到的累计 Session 已测量用量：`start`、`end`、`sampled_at`、`input_tokens` 和 `output_tokens`。已测量的 Session 用量是一项 Core 扩展功能，会对每个已记录的根 Turn 快照求和，其中包括活跃 Turn。它不同于 [public Session usage](sessions-events.md#usage)；在根 Turn 运行期间，或根 Turn 结束后未进行测量时，后者的值为 null。这些计数器衡量的是模型 token 数，而不是价格或计费记录。

### 错误和界限 {#errors-and-bounds}

| HTTP | 代码 | 适用情况 |
| --- | --- | --- |
| 400 | `unsupported_parameter` | 提供了 `start`、`end` 和 `max_points` 以外的参数，或同一参数提供了两次。 |
| 400 | `invalid_request` | 范围或 `max_points` 无效。 |
| 404 | `not_found_error` | Project 不存在，或 Session 不属于该 Project。 |
| 409 | `runtime_history_unsupported` | Session 不是 `openai_hosted`。 |
| 500 | `internal_error` | 已存储的标识不一致。 |
| 503 | `runtime_history_unavailable` | Core 未收集周期性历史（即在不使用 execution worker 的情况下运行），或读取失败、超时或产生了超出界限的结果。 |

每个数组最多包含 `max_points` 个桶，响应最多包含 64 条 series，并且 coverage 和 series 中的点总计最多为 10,000 个。存储错误文本既不会返回，也不会记录到日志中。

### 客户端 {#client-1}

`AdminClient.retrieveRuntimeHistory(projectId, sessionId, {start, end, maxPoints, signal})` 会在发送查询前验证查询。随后，它会检查精确字段、回显的范围和 Session、范围内的桶顺序、coverage 总计、分配标识、贡献者计数、token 用量顺序、可空性、数值和响应大小。任何违规都会使整个响应被拒绝，并返回 502 `invalid_admin_response` 错误。

## 节点主机观测值和历史记录 {#node-host-observations-and-history}

```http
GET /core/v1/sandbox/nodes/{node_id}?range=1h
Authorization: Bearer <Core key>
```

`range` 可以是 `1h`（默认值）、`6h` 或 `24h`。提供其他参数、重复或无效的 `range`，或提供格式错误的节点 ID，都会返回 400 `invalid_request`；节点不存在或已被移除会返回 404 `not_found_error`。响应是 [node list](sandbox-deployment.md) 中的节点对象，并附加 `host` 和 `history`：

```json
{
  "host": {
    "effective_cpu_cores": 4,
    "cpu_utilization": 0.35,
    "total_memory_bytes": 17179869184,
    "available_memory_bytes": 8589934592,
    "available_disk_bytes": 107374182400,
    "observed_at": "2026-09-25T09:00:00Z"
  },
  "history": {
    "resolution_seconds": 60,
    "points": [{
      "start": "2026-09-25T08:59:00Z",
      "cpu_utilization_max": 0.4,
      "memory_used_bytes_max": 8589934592,
      "available_disk_bytes_min": 107374182400
    }]
  }
}
```

`host` 是节点最近收到的心跳观测值；每个不可用值，包括未观测到的 `observed_at`，均为 null。离线节点会保留其最后值及原始时间，因此应根据节点的 `online` 和 `host.observed_at` 判断数据时效性。

- `cpu_utilization` 是主机聚合 `/proc/stat` 计数器在两次心跳之间的忙碌 tick 占比，范围为 0 到 1。空闲和 I/O 等待 tick 不属于忙碌状态，来宾时间不会被重复计算。连接的首个心跳、计数器重置或无法读取基线时，该值为 null。它衡量整个可见主机，而不是节点进程或其沙箱。
- `effective_cpu_cores` 会同时考虑节点进程的 CPU 亲和性和 cgroup 限制；无法确定这些值时为 null。
- `total_memory_bytes` 和 `available_memory_bytes` 分别对应 `MemTotal` 和 `MemAvailable`。
- `available_disk_bytes` 是节点状态文件系统的可用空间，而不是沙箱配额。

节点会测量其命名空间能够看到的内容，因此应让节点运行在其所报告的宿主机上。

`history` 按完整的 UTC 桶覆盖指定范围：`1h` 使用 60 秒，`6h` 使用 300 秒，`24h` 使用 900 秒。范围内的每个桶都会存在，但正在进行中的桶除外。`cpu_utilization_max` 和 `memory_used_bytes_max` 是已记录观测值的最大值，其中已用内存按同一观测值中的总内存减去可用内存计算；`available_disk_bytes_min` 是最小值。对于没有记录值的桶，包括离线时段，每个指标均为 null。Core 绝不进行插值或回填。

`packages/agents-client` 中的 `SandboxAdminClient.retrieveNode(nodeId, range, options)` 会读取此路由并验证响应。
