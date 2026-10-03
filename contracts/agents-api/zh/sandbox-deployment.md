---
title: "沙箱部署"
source: contracts/agents-api/sandbox-deployment.md
source_hash: e84ced73c64da30f33feba032d7e8e9bc8c33b3062604a47258102460615429b
---

沙箱部署为 Core 管理的 `openai_hosted` 执行选择 Sandbox Provider、每个沙箱的资源以及不可变的 Runtime 发行版。PostgreSQL 为每个安装维护一个当前有效选择；Web 和 Core API 写入同一配置。节点文件保存其已安装副本和特定于主机的路径，且不能覆盖其资源或 Runtime。该选择独立于 Harness；部署可以保持未配置状态，既无节点，也不接受托管准入。

本契约负责下列 Core API 路由及其语义。[节点指南](../../../docs/zh/getting-started/nodes.md)负责操作员工作流，[机器连接 API](machine-api.md#node-routes)负责节点调用的路由，[沙箱节点协议](node-generation-protocol.md)负责节点连接。

## 路由 {#routes}

每个路由都需要 Core 密钥。[Web 的控制台服务器](../../../docs/zh/web/console-server.md)会在服务器端为已登录请求添加该密钥。

| 路由 | 效果 |
| --- | --- |
| `GET /core/v1/sandbox/deployment` | 读取安全的当前配置、推出、重置和资源计数 |
| `POST /core/v1/sandbox/deployment` | 选择初始提供商、资源和 Runtime |
| `PUT /core/v1/sandbox/deployment` | 在线更改同一提供商的目标 |
| `POST /core/v1/sandbox/deployment/reset` | 启动托管资源的持久清除或将其升级为持久清除 |
| `DELETE /core/v1/sandbox/deployment/reset?expected_generation=N` | 取消剩余清除 |
| `POST /core/v1/sandbox/providers/{provider}/discovery` | 使用临时凭据查询提供商配置目录 |
| `POST /core/v1/sandbox/enrollment-tokens` | 签发具有已批准容量的一次性节点注册令牌 |
| `GET /core/v1/sandbox/nodes` | 列出已注册节点 |
| `GET /core/v1/sandbox/nodes/{node_id}` | 读取一个节点及其[主机观测和历史](runtime-observability-api.md#node-host-observations-and-history) |
| `PATCH /core/v1/sandbox/nodes/{node_id}` | 更改节点的名称和容量 |
| `DELETE /core/v1/sandbox/nodes/{node_id}` | 移除节点 |
| `GET /core/v1/sandbox/nodes/{node_id}/allocations` | 列出节点尚未释放的分配 |
| `GET /core/v1/sandbox/runtime-observations` | 当前 Runtime 观测；请参阅 [Runtime 遥测 API](runtime-observability-api.md) |

## 选择请求 {#selection-request}

POST 和 PUT 接受相同的完整选择，并要求提供先前 GET 返回的 `expected_generation`。对于初始未配置部署，零是有效值；省略或 null 无效。检查过期代次早于检查重置、提供商、资源和选择项相同这一条件，即使请求体与先前请求完全相同也是如此。

| 字段 | 含义 |
| --- | --- |
| `expected_generation` | 必填的非负整数，来自 GET；绝不会自动刷新并重放 |
| `provider` | 必须是 `docker`、`microsandbox`、`e2b` 中恰好一个 |
| `resources` | 每个沙箱的限制，见下文；Docker 和 microsandbox 必填，E2B 可选 |
| `runtime` | 不可变的 [Runtime 发行版](#runtime-release)；Docker 和 microsandbox 必填，E2B 必须省略 |
| `configuration` | 提供商的公开选择器。E2B：不可变的 `template` 构建以及可选且配套的 `api_url` 和 `domain`。Docker 和 microsandbox 仅接受 `{}` 或省略 |
| `credential` | 提供商的只写凭据。E2B：`{api_key}`，首次设置时必填，在 PUT 中省略以保留当前密钥；null 或空密钥无效。Docker 和 microsandbox 拒绝该字段 |

请求中没有 Core 地址。Core 根据安装公开 URL（`config.json` 中的 `public_url`，Core 对应 `OAC_PUBLIC_URL`）派生部署的 `core_url`：这是节点和沙箱客户机访问 Core 时使用的源地址。包含 `core_url` 的请求会像包含任何其他未知成员一样被拒绝，并返回 400 `invalid_request`。E2B 客户机从 E2B 云访问 Core，因此当公开 URL 为回环地址时，E2B 选择会被拒绝，并返回 409 `sandbox_configuration_error`。Docker 和 microsandbox 选择接受回环公开 URL，但这仅适用于本地开发，因为客户机的回环地址无法访问其主机。更改公开 URL 属于安装变更：使用旧地址注册的节点不会收到新沙箱，必须移除后重新添加。

### 资源 {#resources}

| 字段 | 可接受值 |
| --- | --- |
| `cpus` | 整数，1 到 255 |
| `memory_mib` | 整数，512 到 1048576 MiB |
| `root_disk_mib` | microsandbox：至少 1024 MiB；Docker 和 E2B：省略或为零 |
| `environment_disk_mib` | microsandbox：至少 1024 MiB；Docker 和 E2B：省略或为零 |

这些限制描述每个沙箱。节点的 `max_active` 和 `max_retained` 是独立的预留限制，主机测量值绝不会允许超过其中任何一个。即使数值满足这些边界，原生提供商仍可能拒绝这些值。

Docker 应用 CPU 和内存限制，并检查运行中容器的限制和精确镜像；它没有硬性的根磁盘或工作区磁盘配额。E2B 的 CPU 和内存必须与精确的就绪模板构建一致，Core 会在保存前通过固定版本 SDK 进行验证；磁盘容量仍属于模板的一部分。两种提供商都不接受其无法强制执行的磁盘配额。E2B 选择可以省略 `resources`：此时 Core 将构建的 CPU 数量和内存存储为 `cpus` 和 `memory_mib`，并通过 `specification.resources` 返回，不带磁盘字段。重启时，Core 会加载已提交的 E2B 选择，而不会再次验证模板构建，因此 E2B 中断绝不会阻碍检查或清理；新选择仍需验证。

microsandbox 会配置 CPU、内存、托管根磁盘，以及位于 `/environment` 的独立自有磁盘。[microsandbox 辅助工具](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/tools/microsandbox-provider/README.md)说明了 restore 如何处理这些限制。

### Runtime 发行版 {#runtime-release}

Docker 和 microsandbox 使用一个经过验证发行包中的每个字段：

| 字段 | 标识 |
| --- | --- |
| `source_commit` | 由 40 个字符组成的小写提交 SHA |
| `image_id` | Docker 镜像配置 ID：`sha256:` 加 64 个小写十六进制字符 |
| `image_manifest_digest` | OCI 镜像清单摘要，格式相同 |
| `microsandbox_ref` | `oac-runtime@sha256:` 加 64 个小写十六进制字符 |
| `runtime_sha256` | 原生 microsandbox Runtime 二进制文件的 SHA-256 |
| `firmware_sha256` | 匹配固件的 SHA-256 |

请从匹配的发行清单中复制这些标识。镜像配置 ID 和 OCI 清单摘要标识不同的对象，绝不能相互替代。节点安装程序会在注册前根据载荷验证已保存的发行版，并保留其导入的精确本地镜像标识。

### E2B 配置 {#e2b-configuration}

E2B 使用 `template-id:build-uuid` 形式的 `configuration.template`；构建 UUID 必须是规范形式且非零，仅提供可变模板别名将被拒绝。API 密钥在 PostgreSQL 中加密，绝不会出现在响应、引导配置、命令参数或日志中。默认情况下，Core 使用 `https://api.e2b.app` 和 `e2b.app`。对于兼容服务，请同时设置 `configuration.api_url`（一个不含路径、端口、查询、片段或凭据的 HTTPS 源地址）和 `configuration.domain`（沙箱数据平面 DNS 后缀）；API 主机必须等于该域名或其子域。Core 会在发送守护进程凭据或使用 envd 之前，拒绝数据平面域名位于所选后缀之外的沙箱。

### 配置发现 {#configuration-discovery}

`POST /core/v1/sandbox/providers/{provider}/discovery` 恰好接受 `configuration`、`credential` 和 `query` 对象，总大小最多为 64 KiB，运行时间最多为 30 秒。未知成员和 null 对象会被拒绝。凭据仅用于该请求，绝不会被存储或返回；发现操作不会保存任何内容、不会更改部署、不会分配任何资源，也绝不会证明某项选择会被准入。Docker 和 microsandbox 会以 400 `sandbox_operation_unsupported` 拒绝该操作。

对于 E2B，发布 `{"configuration": {"api_url": "…", "domain": "…"}, "credential": {"api_key": "…"}, "query": {}}` 可列出模板；添加 `"query": {"template": "template-id"}` 可列出该模板的就绪构建；官方端点可能会省略两个端点字段。结果为 `{"templates": [{"id": "…", "names": ["…"]}]}` 和 `{"builds": [{"id": "build-uuid", "cpus": 2, "memory_mib": 2048}]}`，两者都可能为空。固定版本 SDK 辅助工具读取 `GET /v2/templates` 并返回最多 200 条结果；达到限制或提供商失败会返回 503 和通用消息。部署写入操作会单独验证所选构建。

## 安全响应 {#safe-response}

GET 和成功的写入操作会返回 `installation_id`、`provider`、`core_url`（只读：安装公开 URL，即使配置前也存在）、`mode`、`generation`、`owner_epoch`、`reset`、`rollout`、`suspension`、`resources` 和 `credential_configured`。已配置的部署还会返回 `specification`、`specification_digest`、`configuration` 和 `metadata`：这是适配器对其选择器及其记录的观测结果所作的公开投影，绝不会包含原始存储值或机密。E2B 返回 `configuration.template`、`configuration.api_url`、`configuration.domain`，并在记录后返回 `metadata.template_build`。Docker 和 microsandbox 返回空的 `configuration` 和 `metadata` 对象以及 `credential_configured: false`；未配置的部署则不含这两个对象。

- `metadata.template_build` 为 `{status, resources: {cpus, memory_mib, root_disk_mib}}`：这是保存选择时 Core 通过固定版本 SDK 读取的构建。GET 绝不会调用 E2B，因此 E2B 中断期间该操作仍保持低成本。验证仅接受 CPU 数量和内存与所选值一致的 `ready` 构建；`root_disk_mib` 是构建的原生磁盘大小，Core 不会强制执行该值。未知值为 null，`metadata: {}` 表示未记录任何观测，在不提供凭据的情况下提交完全相同的 PUT 也不会刷新它。
- `suspension` 对 microsandbox 而言为 `{idle_seconds, retention_seconds}`，microsandbox 是 Core 唯一会暂停的提供商（当前为 300 和 86400）；Docker、E2B 和未配置的部署返回 null。
- 请求中的 `resources` 和响应中的 `specification.resources` 是每个沙箱的限制。响应中的 `resources.allocations` 和 `resources.pending` 分别计算尚未释放的分配，以及尚未分配沙箱的待处理托管 Environment。
- 未配置的部署具有空的 provider 且没有 specification。Docker 和 microsandbox 使用 `mode: nodes`；E2B 使用 `mode: direct`，且没有合成节点。
- `generation` 标识已保存的选择。`owner_epoch` 用于对执行所有者和节点连接进行栅栏隔离；它不能替代 `expected_generation`。
- `specification_digest` 是服务器对提供商、限制和 Runtime 发行版的标识；注册过程会原样回显该值。

`packages/agents-client` 中类型化的 `SandboxAdminClient` 会根据准确的这些结构检查部署、节点列表、节点详情和分配响应。未知成员、缺少成员或类型错误都会拒绝整个响应，并产生 502 `invalid_admin_response` 错误。节点的 `diagnostic` 可能不存在，也可能是一个代码，但绝不会为空；客户端会将未知代码读取为 `provider_unavailable`。

## 初始设置与同提供商更改 {#initial-setup-and-same-provider-changes}

POST 会在持久保存候选配置之前对其进行验证，并且不会创建计算资源、Session 或模型请求。在当前代次上，完全相同的选择是无操作；使用旧代次则返回 409 `sandbox_generation_stale`，即使请求体相同也是如此。缺少前置条件或准备失败时，已提交的提供商保持不变。更改后端前必须先重置。POST 还会在重置完成后使用该重置的新代次进行初始化。

仅在没有重置进行时，PUT 才接受同一提供商。只发送一次观测到的代次，绝不自动重放结果不确定的写入操作。新分配使用新提交的 specification，现有分配则保留其不可变的部署代次。同提供商更改不会停用任何节点、令牌或所有者 epoch，也不会排空任何执行：Docker 和 microsandbox 节点在继续提供旧固定版本的同时独立准备新目标，而 E2B 更改会立即生效。

### E2B 密钥替换 {#e2b-key-replacement}

在 PUT 中省略 `credential` 可保留当前密钥；不提供该字段的完全相同选择是无操作。提交密钥时，即使密钥相同，也始终会验证密钥并推进代次；null 或空密钥无效。仅更改密钥时使用相同的完整请求体（provider、现有 configuration、可选 resources、`expected_generation` 和新 `credential`），无需单独路由，也不会隐式重置。

初始设置要求所选模板出现在该密钥所属团队的模板列表中；仅可公开读取并不足够。在线更改之前，Core 会验证已提交密钥拥有当前模板，然后要求候选密钥拥有完全相同的模板，从而确立团队所有权。Core 还会使用候选密钥在其原始端点读取候选构建和每个保留构建，并确认带安装标签的沙箱列表中每个已结算的实时回执。

| 结果 | 含义 |
| --- | --- |
| `409 sandbox_reset_required` | 当前选择没有团队所有权锚点，或已提交密钥不再通过身份验证 |
| `409 sandbox_credential_ownership` | 候选密钥属于另一个团队；初始化另一个团队前必须先重置 |
| `400 sandbox_credential_invalid` | 候选密钥收到 401 或 403 |
| `400 sandbox_configuration_invalid` | 候选构建无效或与资源不匹配 |
| `503 sandbox_verification_unconfirmed` | 回执缺失或未结算，或读取未得到确认 |

不会返回提供商文本或凭据。写入操作及其 `change` 或 `replace_credential` 审计条目共用一个事务。替换操作会短暂对提供商调用设置栅栏，即使调用方已取消，也会等待辅助进程实际退出，并在提交前再次验证；辅助进程退出并不能证明远程 Create 已结算。栅栏和读取均有时间边界，失败时会保留旧密钥和生命周期。成功响应后，所有保留代次管理都使用已提交的密钥；只有在清理完成后，才能在 E2B 中撤销旧密钥，绝不能提前撤销。模板和资源更改不会排空生命周期。

## 代次所有权与推出 {#generation-ownership-and-rollout}

`runtime_deployment` 保存当前 specification。被取代的行仅保留不可变的 specification、构建和端点元数据，绝不保留另一个 E2B 密钥。E2B 分配在预留时绑定其代次；节点放置在 Session 准入时绑定，其分配会复制该代次，即使之后发生更新也是如此。检查、续期、命令和清理使用分配的原始 specification 和端点以及当前密钥；缺失代次绝不会回退到当前 specification。已释放代次的标识仍会保留。

当一个代次为当前代次、被尚未释放的分配或放置引用，或者被尚未移除的节点固定时，该代次会保留。节点的持久服务固定状态可跨离线时段和零资源状态保留，并与当前就绪状态相互独立。回收操作与更新和准入共享部署锁，每轮最多删除 32 个符合条件的代次行。只有确认释放后，重置才会停用固定状态并清除被取代的行。

每个部署响应都包含 `rollout`：

```json
"rollout": {"state": "settled", "previous_generation_sandboxes": 3, "nodes": null}
```

`previous_generation_sandboxes` 使用与资源总数相同的快照，统计旧代次中尚未释放的分配以及没有分配的待处理放置，绝不会重复计算同一资源。E2B 和未配置的部署返回 null `nodes`。节点部署将 `nodes` 返回为计数 `{ready, preparing, failed, update_required, unknown}`，每个尚未移除的节点恰好归入一个类别：

- 在当前连接上离线的节点为 `unknown`；
- 不具备代次管理且在较旧目标上注册的在线节点为 `update_required`；
- 否则，当前连接和所有者 epoch 上的精确目标观测会给出 `ready`、`preparing` 或 `failed`，缺少观测则给出 `unknown`。

节点在当前所有者 epoch 下保持连接，并且最近 45 秒内有心跳时，即为在线。目标推出与服务就绪状态相互独立：`unknown`、`preparing`、`failed` 或 `update_required` 不会移除已独立确认的较旧服务代次。`provider_ready` 要求节点在线，并且在该连接和 epoch 上观测到精确的服务代次；不具备代次管理的节点仅能使其注册时的代次满足此条件。固定状态本身绝不表示就绪。

每个节点会添加 `rollout: {state, ready_generation, diagnostic?}`，其中 `ready_generation` 是可为 null 的持久服务固定状态，`diagnostic` 是目标代次的固定代码；分配项会添加 `deployment_generation`。仅当 `rollout.state` 为 `preparing` 或 `reset` 非 null 时，才每五秒轮询一次；旧 Session 以及失败、需要更新或离线的节点本身均不会使轮询保持活动状态。

新的准入操作会先按在线状态、精确的服务代次就绪情况、地址和共享容量筛选节点，再优先选择最新的合格固定状态，因此最新的节点已满时不会掩盖仍有空闲资源的较旧节点。没有候选项时，准入操作不会创建临时 Session 或放置：在线节点若确实正在准备且有空闲容量，则返回 503 `sandbox_nodes_preparing`；节点集群全部已满或离线，则返回 `runtime_node_unavailable`。

## 重置 {#reset}

要更改后端，请启动重置：

```json
{"expected_generation": 7, "clear": "auto", "deadline_seconds": 3600}
```

`clear` 必填，可取 `auto` 或 `force`。`deadline_seconds` 适用于 `auto`，默认值为 3600，接受 300 到 86400；`force` 必须省略该字段。在关闭新的托管准入之前，Core 会持久保存绝对截止时间和请求方的审计来源信息；请求结束后以及跨重启时，执行所有者会继续推进清除。在 `auto` 模式下，Core 会归档空闲的托管 Session，包括排队或待处理工作以及已暂停沙箱，并等待正在执行或等待的 root Turn 和 Subagent Turn 以及待处理的文件写入完成，同时在 Session 锁下重新检查。到达持久保存的截止时间时，Core 会持久地将操作升级为 `force`。`force` 会对每个符合条件的托管 Session 使用常规归档取消和清理路径。绝不触碰自托管 Session。

以相同代次和模式再次启动时，会保留原始截止时间。`auto` 可以升级为 `force`，但不能反向升级。使用当前 `expected_generation` 发出 DELETE 会取消剩余工作并重新开放准入；它不会撤销归档、恢复已过期 Environment，也不会取消已经请求的清理。没有活动重置时，DELETE 是无操作；请求过期则返回 409。

未激活时 `reset` 为 null，否则为：

```json
{"clear": "auto", "requested_at": "2026-09-27T12:00:00Z",
 "deadline_at": "2026-09-27T13:00:00Z", "forced_at": null,
 "remaining": {"busy": 2, "idle": 1, "cleanup": 3, "on_offline_nodes": 2,
               "offline_nodes": [{"node_id": "node-uuid", "name": "worker", "resources": 2}]}}
```

一个数据库快照会先按 `cleanup`，再按 `busy` 或 `idle`，对每个尚未释放的分配和每个尚未分配沙箱的待处理托管 Environment 进行划分，因此 `busy + idle + cleanup == resources.allocations + resources.pending`。已删除或已过期且具有未释放回执的 Session 计入 cleanup。`offline_nodes` 是通过分配或活动放置持有资源的离线节点的完整列表，并按 ID 排序，其总和等于 `on_offline_nodes`；存在性依据当前所有者 epoch、连接状态以及 45 秒内的心跳判断，而不依据提供商就绪状态。直接 E2B 资源没有节点。离线资源会持续构成阻塞条件，直到清理确认其已释放。

当两个占用计数均达到零时，所有者会排空执行，并原子清除 provider、mode、specification、provider configuration、credential、metadata 和 provider policy，停用节点和未使用的注册令牌，递增代次与所有者 epoch，并记录 `reset_complete`。安装身份和历史会保留。Core 会立即发布未配置状态，即使没有 provider 也保留新代次，因此延迟加载无法恢复旧配置。使用 POST 和返回的代次即可重新配置，无需重启。

重置期间，新的托管准入会返回 503 `sandbox_reset_in_progress`，并且不会留下临时 Session 行；实时输入、回执重试、恢复和清理会继续进行。管理写入和新注册会返回 409 `sandbox_reset_in_progress`，而已注册节点仍可读取其配置，以便恢复并执行清理。每个 Session 的 [archive](admin-api.md#session-archive)仅需要当前代次，无论是否正在重置均可使用。它会保留历史以及已持久化的 Files 和 Artifacts，丢弃未持久化的工作区，并阻止 Session 恢复；轮询 archive GET 以获取实际释放状态。重置绝不会伪造释放回执。

强制归档会立即对凭据和新工作设置栅栏。当 Turn 的托管传递仍处于连接状态时，Core 只会为该传递保留原生的取消和终止回执路径，直到终止提交完成；从最初取消请求起最多持续 20 秒；在取消确认或终止提交仍待处理时，`done` 不会结束此时间限制。该排空过程绝不会授权重新连接、访问 workspace 或 MCP 或继续执行，显式撤销凭据会终止它。回执缺失或失败时会如实保留失败结果；所有者断开连接、过期或重启时，会回退到常规提供商清理。

一个变更操作门会串行化设置、PUT、重置、取消和最终处理。archive 会先锁定 Session，再锁定部署，最终处理绝不会颠倒此顺序。候选操作会绑定到重置请求时间，因此取消后以相同代次启动的新重置无法复用旧工作。绝不重放被拒绝或结果不确定的写入操作：读取当前状态并重新决定。

## 节点与分配 {#nodes-and-allocations}

节点容量由管理员批准，独立于部署 specification。`POST /core/v1/sandbox/enrollment-tokens` 接受可选的 `max_active` 和 `max_retained`，默认分别为 2 和 8，并返回 `{token, expires_at, enrollment_id}`；`enrollment_id` 是该命令的公共句柄，绝不是凭据。microsandbox 会使用两个限制。Docker 绝不暂停，因此 Core 会在此处和 PATCH 中用 `max_active` 替换其 `max_retained`。E2B 没有节点，并返回 409 `sandbox_deployment_conflict`。Core 将批准值与令牌一同存储，并复制到其注册的节点；节点不能提交容量，其本地检查可以拒绝其无法运行的部署，但绝不会提高限制。[节点容量](../../../docs/zh/configuration.md#node-capacity)为操作员说明了这些限制。

`GET /core/v1/sandbox/nodes` 返回 `{data: [...]}`，其中每个节点包含 `id`、`name`、`provider`、`online`、`last_seen_at`、`created_at`、`max_active`、`max_retained`，计数项 `active`、`reserved`、`running`、`retained`、`snapshots` 和 `cleanup_pending`，以及 `provider_ready`、`diagnostic`、主机测量值 `cpu_count`、`available_memory_bytes` 和 `available_disk_bytes`、`rollout`、`enrollment_id` 和 `core_url`。`enrollment_id` 是注册该节点的命令的句柄；如果 Core 没有该句柄，则为 null。`core_url` 是注册时的安装公开 URL；`core_url` 与当前公开 URL 不同的节点不会收到新放置。已放置到该节点的工作会继续在那里完成，包括已放置但尚未获得分配的 Environment；只要旧地址仍能访问 Core，其保留沙箱仍可恢复。请移除该节点并重新添加。

不具备代次管理的节点会自行报告其提供商的就绪状态：`provider_ready`，未就绪时则报告一个固定的 `diagnostic` 代码。通过 Web 的命令添加的节点会管理代次，因此其就绪状态取决于服务代次，而目标代次的固定代码会出现在 `rollout.diagnostic` 中。节点会对首次失败的就绪检查进行分类，并且只发送代码；Core 会将任何其他值存储为 `provider_unavailable`，且绝不存储或返回探测文本或主机路径。代码包括 `docker_unavailable`、`docker_limits_unsupported`、`runtime_download_failed`、`runtime_image_unavailable`、`kvm_unavailable`、`microsandbox_artifacts_unavailable`、`capacity_insufficient` 和 `provider_unavailable`。`runtime_download_failed` 表示无法传输或验证精确的 Runtime 制品；它绝不会包含制品 URL、凭据或传输输出。[就绪代码](../../../docs/zh/getting-started/nodes.md#readiness-codes)给出了原因和操作员应采取的措施。Core 和节点必须来自同一发行包。

`PATCH /core/v1/sandbox/nodes/{node_id}` 接受 `{name, max_active, max_retained}`；降低限制不会停止任何正在运行的沙箱。当节点仍持有分配、快照、预留或待处理清理时，包括节点离线期间，`DELETE /core/v1/sandbox/nodes/{node_id}` 会拒绝操作并返回 409 `runtime_node_in_use`。移除操作不会删除任何计算资源，并且会停用该节点的身份；该主机只能作为新节点重新加入。不存在节点排空过程。

`GET /core/v1/sandbox/nodes/{node_id}/allocations` 列出节点尚未释放的分配。每个项的 `compute_phase_changed_at` 表示该分配进入当前 `compute_phase` 的时间；未知时为 null。对于已暂停的 microsandbox 分配，该时间加上 `suspension.retention_seconds` 可大致确定 Core 回收它的时间。

## 各沙箱提供商中的字段含义 {#what-each-field-means-per-sandbox-provider}

某些字段在不同提供商中保持同一名称，但含义不同或不适用。部署字段来自 `GET /core/v1/sandbox/deployment`，节点和分配字段来自节点路由，Runtime 字段来自 [Runtime 遥测 API](runtime-observability-api.md)；其中 `disk` 仅出现在观测列表中，历史则在 [Session Runtime 历史](runtime-observability-api.md#session-runtime-history)下说明。

| 字段 | E2B | Docker | microsandbox |
| --- | --- | --- | --- |
| 部署 `specification.resources` | `cpus` 和 `memory_mib`，必须等于就绪模板构建中的值；省略时取自该构建；无磁盘字段 | `cpus` 和 `memory_mib`；无磁盘配额 | `cpus`、`memory_mib`、`root_disk_mib` 和 `environment_disk_mib` |
| 部署 `specification.runtime` | 不存在；`configuration.template` 用于选择构建 | 完整的[发行版](#runtime-release)；节点必须与 `image_id` 或 `image_manifest_digest` 匹配 | 完整的[发行版](#runtime-release)；节点必须与 `microsandbox_ref`、`runtime_sha256` 和 `firmware_sha256` 匹配 |
| 部署 `metadata.template_build` | Core 保存选择时读取到的构建 | 不存在：`metadata` 为空 | 不存在：`metadata` 为空 |
| 部署 `suspension` | `null`；Core 不暂停 E2B 沙箱 | `null` | `{idle_seconds, retention_seconds}` |
| 部署 `resources.allocations`、`resources.pending` | Core 中尚未释放的 E2B 沙箱，以及正在等待沙箱的托管 Environment | 所有节点的总数 | 所有节点的总数 |
| 注册令牌 `max_active`、`max_retained` | 先执行 400 容量检查，然后返回 409 `sandbox_deployment_conflict`；E2B 没有节点 | `max_retained` 始终等于 `max_active` | 两个限制均适用 |
| 节点列表和详情 | 空列表；详情返回 404 | 已注册节点 | 已注册节点 |
| 节点 `retained`、`snapshots`、`max_retained` | 不适用 | Docker 绝不暂停：`retained` 等于 `active`，`snapshots` 为 0，`max_retained` 等于 `max_active` | 已暂停沙箱数为 `retained` 减去 `active` |
| 节点 `diagnostic` 代码 | 不适用 | `docker_unavailable`、`docker_limits_unsupported`、`runtime_download_failed`、`runtime_image_unavailable`、`capacity_insufficient` 或 `provider_unavailable` | `kvm_unavailable`、`microsandbox_artifacts_unavailable`、`runtime_download_failed`、`capacity_insufficient` 或 `provider_unavailable` |
| 节点 `host.available_disk_bytes` | 不适用 | 节点状态目录所在文件系统的可用空间，而不是容器的磁盘 | 节点状态目录所在文件系统的可用空间；沙箱磁盘有自己的配额 |
| 分配 `compute_phase`、`compute_phase_changed_at` | 不适用：没有节点分配 | 始终为 `disabled`，在释放前计为运行中；该时间为分配创建时间 | 包含 `suspended`；该时间加上 `suspension.retention_seconds` 可大致确定 Core 回收快照的时间 |
| Runtime 观测 `cpu`、`memory` | 来自 E2B 指标：`cpu.utilization_ratio` 和 `capacity_cores`、内存使用量和限制；无累计 CPU 时间 | 来自 Docker stats：`cpu.usage_seconds_total`、CPU 和内存限制、内存使用量 | 来自 VM：`cpu.usage_seconds_total`、CPU 和内存限制、内存使用量 |
| Runtime 观测 `disk` | E2B `diskUsed` 和 `diskTotal`；模板未报告时为 `null` | `null`：无磁盘配额 | `null` |
| Runtime 观测 `lifecycle_state: sleeping` | 从不 | 从不 | 暂停期间 |
| Runtime 历史 CPU | 每个时间桶中 E2B 所报告使用率比值的平均值 | 根据累计 CPU 时间派生 | 根据累计 CPU 时间派生 |

## 错误 {#errors}

| HTTP | 代码 | 场景 |
| --- | --- | --- |
| 400 | `invalid_request_error` 或 `invalid_request` | 请求格式错误 |
| 400 | `invalid_sandbox_configuration` | 配置验证产生的诊断错误 |
| 409 | `sandbox_generation_stale` | `expected_generation` 不是当前值；`current_generation` 提供当前代次 |
| 409 | `sandbox_reset_required` | 后端不同；详情会指明当前提供商和请求的提供商 |
| 409 | `sandbox_not_configured` | 更改未配置的部署 |
| 409 | `sandbox_reset_in_progress` | 重置期间执行管理写入或新注册 |
| 409 | `sandbox_deployment_conflict` | 更改无法应用于另一种状态 |
| 409 | `runtime_node_in_use` | 节点仍持有资源时移除节点 |
| 503 | `execution_unavailable` | 无法准备提供商 |
| 503 | `credential_storage_unavailable` | Core 没有凭据加密密钥 |

存储和凭据故障始终作为错误处理：读取为空或读取失败绝不能证明清理完成。[机器连接 API](machine-api.md#node-route-errors)列出了节点路由的错误。

## 规范的节点规格 {#canonical-node-specification}

`sandbox/deployment_contract.go`负责资源边界、提供商要求、发行版模式和规范字段顺序；`sandbox/deployment.go`在 Core 中应用这些规则。安装程序会使用 `deploy/install/node_spec.py` 中生成的声明，因此不存在第二套限制或模式。请在仓库根目录运行 `go run ./services/core/cmd/specification-contract -write` 重新生成；作为 `make check` 一部分的沙箱 Go 测试会拒绝过时的投影。

规范摘要是紧凑 UTF-8 JSON 的 SHA-256，其中 `provider` 位于首位，其次是 `resources`，然后在提供商需要时放置 `runtime`。资源和 Runtime 字段遵循契约的声明顺序；值为零的可选磁盘字段会被省略，必填字段则保持存在。发行版标识采用小写 ASCII，摘要绝不会受传入字段顺序或空白字符影响。`services/core/internal/sandbox/testdata/deployment-contract.json`保存共享验收用例、精确的规范字节和摘要，Go 与 Python 测试都会使用这些内容。
