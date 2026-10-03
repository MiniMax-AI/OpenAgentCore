---
title: "环境与模板"
source: contracts/agents-api/environments.md
source_hash: a93a477f3de39b2206702995821282404c29ee997ef6b782180d4972bada43f7
---

Environment 是 Session 的执行资源，包括 Harness 运行所在的机器、工作区以及已完成准备的能力。Session 通过其 `environment` 配置创建 Environment；不存在独立的 create 调用。Environment Template 是 Session 创建时解析的可复用准备配置。本契约涵盖这两类资源、两种放置方式、输入接纳、能力准备、Skills、Plugins 和 MCP 连接来源。

相关职责归属：

- [Environment files](environment-files.md)：实时工作区上的 Files API。
- [Executor credentials](environment-executor-credentials.md)：`self_hosted` 机器的注册、安装授权和连接状态。
- [Sandbox deployment](sandbox-deployment.md)：托管 `openai_hosted` Environment 的 Sandbox Provider（E2B、Docker 或 microsandbox）。
- [Core–Runtime protocol](../../../docs/zh/runtime-protocol.md)：`runtime_prepare` 传输及所有其他线上消息。
- [Runtime and outer isolation](../../../docs/zh/concepts.md#runtime-and-outer-isolation)：daemon 使用启动用户的权限运行工具；隔离由外层 Environment 提供。

## 资源与状态 {#resources-and-states}

路径遵循 SDK 资源方法，并位于服务的 `/v1` 前缀之前。链接中的固定版本源码定义了字段和联合类型。

| 资源 | 操作 | 规则 |
| --- | --- | --- |
| [Environment](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/environments.py) | `GET /agents/environments/{id}` | 通过 Session 配置创建。返回 `id`、`type`、`status`，以及 Session 创建时记录的 `files`、`plugins` 和 `skills`。 |
| [Template](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/templates.py) | `POST`、`GET /agents/environments/templates`；`GET`、`POST`、`DELETE /agents/environments/templates/{id}` | 请参阅 [Templates](#templates)。 |
| [Files](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/files.py) | `POST`、`GET /agents/environments/{id}/files` | 请参阅 [Environment files](environment-files.md)。 |

读取 Environment 时，Core 会在调用方的 Project 范围内与其实时所属的 Session 联查，并返回持久化的连接状态。它不需要活跃的 Runtime，不会启动任何原生工作，也不会改变连接状态。对于两种放置方式，安装项数组都会列出由 API 管理的文件、Plugins 和 Skills：文件使用 `{id, type, path, file_id, size_bytes}` 且不含内容，Skills 使用 `{type, name, description, skill_id, version}`，Plugins 使用 `{type, name, description}`。在本地能力目录中发现的 Capabilities 不会列出。

[Session environment input](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/environment_param.py) 与 [output](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/environment.py) 的结构不同：

- `none` 不选择任何 Environment。
- `self_hosted` 输入要求提供 `workspace_directory`；可空的 `capability_directories` 默认为空列表。输出会增加 Environment ID 和仅输出字段 `remote_url`。输出中 `/workspace` 的默认值不会使输入字段变为可选。
- `openai_hosted` 可以引用 Template，并提供 `capability_directories`、`network`、`packages`、`files`、`plugins`、`skills`、`env` 和 `setup_commands`。

| 投影 | 状态 |
| --- | --- |
| [Environment resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environment_info.py) | `pending`、`connected`、`disconnected`、`expired`、`failed` |
| [Session environment event](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agent_session_environment_state.py) | `pending`、`ready`、`connected`、`disconnected`、`failed`，并带可为 null 的 error |

这两套词汇彼此独立；绝不要将其中一套转换为另一套。Session environment 事件携带 Session 和 Environment 的身份标识以及可选的 Turn 身份标识，绝不携带配置、凭据、注册 ID 或修订号。Session 的 `required_actions` 可以包含 `{type: "environment_connection", environment_id}`，这与函数调用相互独立。Environment 连接、Session 状态和 Turn 状态相互独立。

Core 在 Session 创建事务中创建 Environment 记录；Session upsert 会选出重试的胜者，而重试绝不创建或修复 Environment。Environment 从所属 Session 派生其 Project 和不可变配置。其首个状态为 `pending`。Session 删除后，读取会隐藏 Environment，而 Core 仍保留该记录，用于结算和清理。Session 为 `none` 时没有 Environment。

托管的外层 Environment 必须排除权限范围更广的应用程序凭据和其他租户的机密。

## 放置方式 {#placements}

两种放置方式运行相同的 Runtime：daemon、所选 Harness、原生工具和工作区共同在一台机器上运行。二者唯一的差异是由谁拥有该机器。

| 对象 | 责任 |
| --- | --- |
| Session 与 Environment | 持久所有权、配置、待处理交互和连接观察（Core） |
| Provider 分配 | 计算资源和文件系统的生命周期：`openai_hosted` 使用 Core 的 Sandbox Provider，`self_hosted` 使用应用程序 |
| 设备与 daemon 连接 | 经认证的 Runtime 身份和可替换的分派传输 |
| Harness 进程与原生会话 | 原生模型和工具循环、其执行状态及原生历史 |
| 注册 | `self_hosted` 机器的 Environment、设备和 executor key 的精确绑定 |

### 托管（`openai_hosted`） {#hosted-openai-hosted}

部署中配置的 Sandbox Provider（E2B、Docker 或 microsandbox，请参阅 [sandbox deployment](sandbox-deployment.md)）承载 Environment。[Harness capabilities](harness-capabilities.md) 列出了可在其中运行的 Harnesses。

- Session 创建时，无论是否包含初始输入，都会在 Worker 配置计算资源之前提交 Session、Environment 和重试身份。若创建在 bootstrap 前中断，可恢复时不会重复执行 Provider 的 Create。
- 置备无需调用方执行任何操作；在 Turn 启动之前，Session 会保持空闲。
- 省略或传入 null 的 `network` 表示启用；`disabled` 和 `restricted` 会在 Session 创建前被拒绝（[restricted network](#restricted-network)）。
- Core 重启会保留分配、工作区和原生身份，并且绝不重播结果不确定的工作。
- 终止清理会先在一个事务中撤销权限并结算待处理输入，然后 Provider 才会回收计算资源。向处于终止状态的 Environment 提交新输入会被拒绝。
- 删除 Session 会回收其 Environment；删除 Template 则不会。

### 自托管（`self_hosted`） {#self-hosted-self-hosted}

应用程序拥有机器。它使用干净的绝对路径 `workspace_directory` 和可选的绝对本地 `capability_directories` 创建 Session。Core 会返回 Environment ID、`remote_url` 以及 `x_agents_core.installation` 中的一条安装命令；在该机器上运行此命令会安装 daemon 并为其注册（[self-hosted guide](../../../docs/zh/getting-started/self-hosted.md)、[executor credentials](environment-executor-credentials.md)）。

- `remote_url` 是根据 Core 的公共 URL 推导出的 daemon WebSocket URL，绝不根据请求头或 daemon 地址生成。它指定 Core 的私有 daemon 传输通道。
- 注册会将精确的 Session、Environment、设备和 executor key 绑定在一起。它不会创建任何分配，也无法将 Session 迁移到另一台设备。
- Session 的工作区必须等于 `/workspace` 别名，或等于 Runtime 绑定到的精确规范目录。指定某个路径并不会授予对它的访问权限。
- Session 携带自己的模型提供方；部署默认值绝不适用（[model execution](model-execution.md#saved-defaults-and-precedence)）。
- Session 读取、列表和事件会返回带有 Environment ID、工作区及能力目录的 `self_hosted` 输出，但绝不返回私有配置。`capability_directories` 列出调用方选择的内容；Runtime 的安装位置保持私有。
- 计算资源、工作区和文件仍归应用程序所有。删除 Session 或撤销凭据会拒绝后续访问，但不会停止原生进程；机器所有者负责停止和清理。
- 工作区和原生历史必须能在 daemon 重启后继续存在。丢失它们绝不授权进行静默替换或重播。

**应用管理的 E2B。** 应用程序可以在由其使用 E2B SDK 创建、续期和销毁的 E2B sandbox 中运行 Runtime，然后将该 Runtime 注册为 `self_hosted` Environment（[E2B Runtime guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md)）。Core 不为其保留 E2B 分配，也绝不续期或终止它。

### 所有权规则 {#ownership-rules}

- 将 Environment 身份、所有权、配置和生命周期保留在 Core 中，并使其与 Provider 计算资源、设备身份、daemon 套接字和原生会话相分离。将可变连接状态排除在不可变配置之外；替换后的所有者会使过期观察值失效。
- 调用方、设备和 Environment 连接使用彼此不同的凭据。daemon gateway 会对已注册的 executor key 和精确设备进行认证。连接观察保留 generation 和 revision 栅栏。注册和连接都不表示已就绪。
- 轮换、撤销、Session 删除和所有权丧失都会拒绝后续访问；但它们不保证原生效果会立即停止。
- 原生历史保留在绑定的 Runtime 上。替换计算资源时必须保留或以可证明的方式恢复原生历史；绝不能静默移动已绑定的 Session 或重播未知工作。
- 持久元数据读取不需要活跃的 Runtime。实时文件读取需要获得对精确工作区的授权视图和有界操作所有权。写入、替换或撤销工作区所有者的操作也适用变更栅栏。
- 当 Session 已有已启动的 Turn，但没有记录原生 session ID 时，下一个 Turn 必须通过经验证的 Runtime 能力恢复已有历史。已提供的原生 ID 始终具有权威性。Codex 适配器仅通过原生列举和精确 ID 恢复，恢复 Session 私有原生主目录及预期工作目录中唯一且未归档的根项。历史缺失、不完整或存在歧义时会失败且不会创建新根项；记录的启动可能早于原生工作时也会失败。恢复绝不会重播被中断的输入。设备身份和 Environment 范围属于 `ExecutionDevice`；原生会话身份和先前 Turn 状态属于 `SessionExecutionBinding`。
- Runtime 发现会为每个已安装的 Harness 执行一次 15 秒版本探测；缺少二进制文件会立即失败。版本结果仅表示可用性，而非 Environment 就绪状态。

## 输入接纳 {#input-admission}

Session 输入通过 `POST /agents/sessions/{id}/events` 以每次 1–64 个事件的有序批次提交。对于带 Environment 的 Session，Core 会将尚不能启动的输入预留，直到 Environment 完成连接和准备。

### 初始输入 {#initial-input}

创建操作接受字符串形式的初始文本，或由用户消息构成的有序数组。省略或传入 null 输入不会创建 Turn 或连接操作。

- 创建事务会将初始批次存储为预留；对于 `self_hosted`，还会记录连接操作。只有创建胜者会插入它们；重试绝不会再次插入。
- `self_hosted` 创建会同时返回 Session 和 Environment 连接目标，即使机器仍处于离线状态，也不等待接纳。流式 `self_hosted` 创建会将已提交的 JSON 投影作为其 `created` 快照发送，其中已经显示 `requires_action` 和连接操作，随后发送已提交的 `requires_action` 事件。
- 新鲜的创建流会在已接纳 Turn 结束时记录空闲状态，或预留不再处于 pending 时记录空闲状态后立即结束，也会在失败后结束。仅因连接而清除操作不会结束该流。无输入的创建会在发送其 `created` 快照后立即结束；使用相同 key 的流重试会立即结束且不发送任何事件。后续订阅者只能看到未来事件，并通过查询恢复历史。
- 关闭流不会影响已提交的工作；Worker 仍会准备并接纳输入。
- 如果初始预留过期，Session 会变为 `failed`，并带有安全错误且不包含任何操作，同时不会创建 Turn。Environment 本身不会变为 `failed`。
- 创建重试会保留原始身份、截止时间和输入。对于记录了意图的 Saved-Agent，重试会在执行接纳或源解析之前进行恢复。

### 后续输入 {#later-input}

| 批次 | 行为 |
| --- | --- |
| 消息，Turn 处于活动状态 | 通过有序接纳和原生传递追加到当前 Turn。不会创建新 Turn、准备或预留。 |
| 消息，Session 处于空闲状态 | 预留该输入；请求等待 Worker 进行准备、接纳和认领。 |
| 仅取消 | 通过常规持久取消路径接纳。空闲状态下的取消不会创建 Turn。预留处于 pending 时，新的取消会发生冲突；它绝不会取消 Turn 之前的输入。 |
| 仅函数结果 | 使用常规结果回执接纳到指定的 pending 调用，不涉及 Turn 或准备。不能绕过 pending 预留。函数结果绝不会成为 Environment 安装元数据。 |
| 混合类型 | 拒绝。 |

Core 仅在批次已持久接纳后返回 202。对于取消，202 确认的是接纳，而非原生完成或进程退出。在 Session 锁内，重试会先恢复其原始预留或回执，然后由 Core 在活动 Turn 与新预留之间选择，并在后续工作中保持该目标。未加锁的读取或冲突后的重试绝不会选择不同路径。

等待请求使用有界的池化操作，位于事务和执行租约之外；它绝不会准备或启动原生工作。只有此路由会将其响应写入期限延长至六分钟，以涵盖五分钟的数据库期限及响应时间。断开连接会停止等待，但不会取消预留。错误如下：

| 条件 | 响应 |
| --- | --- |
| 预留已过期 | 409 `environment_input_expired` |
| 预留已取消 | 409 `environment_input_cancelled` |
| 向失败的 `openai_hosted` Environment 提交新输入 | 409 `conflict_error`，"the hosted environment failed to provision" |
| 向失败或过期的 `self_hosted` Environment 提交新输入，或 Environment 失败时仍在等待的输入 | 409 `environment_unavailable` |
| 执行所有权丧失 | 503 `execution_unavailable` |
| Session 已删除 | 404 |

### 预留 {#reservations}

预留会在 Turn 接纳之前存储一个规范消息批次，截止时间依据数据库时钟设置为五分钟。

- 它要求 Session 带有 Environment，且没有活动工作。预留和直接输入共享 Session 锁和重试标识；pending 或 settled 的 key 不能通过直接接纳绕过其预留。
- pending 预留会阻止新的直接批次，包括取消；较早成功的重试仍保持可读。
- 提升操作会将原始输入、历史、预留结算和 Turn 认领（`queued` 到 `in_progress`）一并提交。它要求当前持有租约的执行写入方和保留的原生准备状态；准备期间不持有数据库锁。只有首次成功且非重放的回执授权在该准备状态上执行 Start。已接纳的重试会返回原始回执，而不重新获取执行权；读取或不确定的提交绝不复授权另一次 Start。
- 提升之后、Start 之前发生崩溃时，会进入已认领 Turn 的协调流程（`execution_interrupted`），即使 Session 未绑定或已删除也是如此。
- 输入处于 pending 时会拒绝删除 Session，且不会更改任何内容。认领之后删除 Session 会像处理任何活动 Turn 一样被拒绝。
- 截止时间在取得 Session 锁后检查。过期和定向取消会保留其终止身份；终止预留的重试不能影响后续预留或 Turn。
- 初始预留过期时会使 Session 失败；后续预留则会让 Session 回到空闲状态。失败事件会原子地捕获已结算的活动和 Usage。迟到的连接或创建重试不能重置或重播已过期的输入。

当 Runtime 返回 `failed` 结果、带有 `preparation_failed` 且没有 Run 时，会立即以 `runtime_preparation_failed` 结算待处理输入。当当前 `execution_prepare` 在没有 Run 且代码为 `invalid_configuration`、`unsupported_configuration` 或 `unsupported_preparation` 时被拒绝，也同样如此。在任何 Turn 存在之前，Core 会记录安全的 Session 失败并释放输入门控；修复本地原因后即可接受新输入。传输丢失、容量拒绝和未确认的清理在原始期限内保持可重试。Core 使用这些通用控制状态，绝不使用 Harness 特定的错误文本。

### 活动与必需操作 {#activity-and-required-actions}

在 Turn 存在之前，Session 活动来自最近相关的预留和连接状态。离线 `self_hosted` Environment 上的待处理输入会请求 `environment_connection`；连接到达后会将其清除并设为 `idle`，同时 Worker 仍负责原生就绪性和接纳。没有待处理输入的离线 Environment 不会请求任何操作，而 `openai_hosted` 置备也绝不会请求连接。预留或连接变更会在同一事务中提交不可变的 Session 活动和使用量快照；较新的预留或活动 Turn 负责后续活动。已结算的非初始预留会将操作恢复为 `idle`，直至出现较新的工作。

### 过期与调度 {#expiry-and-scheduling}

Worker 在每个 tick 中最多处理 32 个到期预留，处理顺序是在检查其租约之后、检查设备或执行槽位之前。该扫描使用带事务超时的租约 Store 连接，绝不使用池化写入方。部分截止时间索引和 `SKIP LOCKED` Session 锁使无关工作得以继续。截止点采用语句时间，而结算会在取得 Session 锁后重新检查数据库时钟。重启会在正常 tick 中继续处理过期；不存在单独的调度器。失败的 Turn 绝不能代替 Turn 之前的连接失败。

当已完成的托管分配处于暂停或恢复状态且有下一 Turn 输入处于 pending 时，Core 会向托管 Runtime 维护循环发出提示。初始输入、冷创建、运行中或已禁用的计算资源、终止回执、取消和函数结果、历史及文件操作均不会发送提示。提示采用尽力而为的方式合并且不会阻塞；它们在正常的五秒周期中最多增加一次扫描，绝不会绕过容量、繁忙的生命周期门控或所有权检查。持久化工作和正常 ticker 始终具有权威性。

## Runtime 能力准备 {#runtime-capability-preparation}

准备过程会安装 Session 选择的内容：初始文件、工具配置、Skills、Plugins、Environment MCP、npm 和 Python 软件包以及设置命令。两种放置方式使用同一条 Runtime 路径。

### 所有权与生命周期 {#ownership-and-lifetimes}

资源管理负责 Provider 放置、容量和分配，以及 Environment 的创建、续期和回收。经认证的 Runtime 连接承载初始化、能力准备和 executor 操作；它绝不分配或销毁计算资源。Provider 绝不运行 Core 初始化命令。

关闭 Executor、取消 Turn 或传输丢失都会保留已安装的快照、工作区和分配。回收是与活动工作协调的显式操作；断开的套接字不能证明原生效果已经停止。[Harness onboarding](harness-onboarding.md#executor-and-turn-lifetimes) 负责 Executor 和 Turn 的生命周期。

### 准备顺序 {#preparation-order}

Core 会在 Session 创建时冻结资源版本、元数据和源选择。随后初始化按以下顺序运行，每一步都通过 `runtime_prepare` 使用同一个 daemon：

1. 初始文件和工具配置；
2. Skill 和 Plugin bundle 导入；
3. npm 和 Python 软件包，然后按顺序运行设置命令；
4. 能力目录快照。

运行器仅使用中立的 Environment 和 Session 身份以及一个 Runtime 对端，不包含 Provider、部署或操作系统分支。Harness 差异保留在适配器中。

**可移植的准备输入。** `self_hosted` 输入仅携带 `workspace_directory` 和 `capability_directories`。无论 Session 采用哪种工作区放置方式，还可以发送 `x_agents_core.environment`，这是 Core 扩展，包含 `environment_template_id`、`files`、`env`、`packages`、`setup_commands`、`skills`、`plugins` 和 `capability_directories`。它使用与 `openai_hosted` 输入相同的解析器和继承规则。若同一字段同时在 `x_agents_core.environment` 和顶层 `environment` 中提供，则会被拒绝，包括显式 null。`self_hosted` Session 只能通过此扩展指定 Template，并且不能使用 network 不是 `enabled` 的 Template（400，param `x_agents_core.environment.environment_template_id`）。机器位置、规格大小和网络策略不是此扩展的字段。

```json
{
  "agent_id": "agent_example",
  "environment": {"type": "self_hosted", "workspace_directory": "/home/user/project"},
  "x_agents_core": {"environment": {"environment_template_id": "env_template_example"}}
}
```

将 `environment` 改为 `{"type":"openai_hosted"}` 会复用相同的准备输入。资源解析、Project 授权、具体 Skill 版本、加密文件内容和机密工具变量都会在 Session 创建时冻结。重试和重连会复用这些快照；新 Session 会解析新版本。`self_hosted` Environment 从不需要分配记录。

**就绪性。** 传输 `connected` 是一种连接观察，而不代表就绪。执行和实时文件访问都要等待初始化；随后，原生准备所有者会在接纳 Turn 前验证已安装的快照和 Harness。文件读取保留自身的就绪性和授权，不要求能力或原生就绪。部署模型凭据绝不发送到应用程序所有的机器。

**传输。** 初始文件、configure、npm、Python 和设置操作、惰性的 Skill 和 Plugin 归档以及最终确定选择，都会作为带有类型的 `runtime_prepare` 操作传输，并携带规范的 Session 和 Environment 身份；[protocol](../../../docs/zh/runtime-protocol.md#preparation-and-execution-order) 负责分块和回执。文件及设置工作目录使用逻辑 `/workspace` 地址；Runtime 负责选择可执行文件和物理目标位置，Core 不提供任何可执行文件或主机平台字段。源选择接受可移植的 Unix 绝对路径、Windows 驱动器路径和 UNC 路径；Core 绝不会在自己的主机上解析这些路径，而 daemon 会应用其本地路径和访问检查。

### 已安装快照 {#installed-snapshot}

两种来源都使用通用 Runtime 解析器，以及适用于 Linux、macOS 和 Windows 的 `installed.json` 清单。Runtime 操作者负责选择能力根目录（[installer options](../../../docs/zh/getting-started/self-hosted.md#options-for-automation)）；Core 和传输请求无法选择。

- 清单会将 Session 和 Environment 绑定到有序的源选择摘要。准备所有者会在原生执行之前验证或创建该清单，即使选择为空也是如此。
- 设置命令运行后，初始化器会将声明的、位于工作区内的能力目录快照到 Runtime 存储中。目录字节是在设置之后读取，而不是在 Session 创建时读取。
- 文件系统锁可防止并发安装。私有完成记录仅保留操作者的安装根目录，因此已删除的快照绝不会被误认为首次准备，也不会再次被捕获。
- 快照缺失、不完整、冲突或属于外部来源时，会在不删除数据、修复或重播的情况下失败。
- 重连和替换 Executor 会加载已安装的内容，而不会重新读取源。对源的编辑只会影响新 Session。
- 对快照的递归引用，以及会逃逸出快照的目录项，都会被拒绝。
- 只读快照模式只是完整性提示，不能防止启动用户进行修改。

Executor 接纳仅验证已冻结的描述符。准备所有者会在调用原生工厂之前使能力就绪；适配器仅接收已解析的、由 Runtime 所有的 Skill 路径和 MCP 声明。复用的 Executor 会保留其原始配置。

### 系统依赖与 Runtime 目录 {#system-dependencies-and-runtime-directories}

daemon 以启动它的账户身份运行，绝不使用 sudo 或提升权限。准备过程中只会安装用户目录中的依赖项。

- 系统依赖必须预先安装在托管镜像中，或由自托管机器的所有者安装。缺少可执行文件或库时，需要该依赖的操作会失败。
- Templates 和内联配置都会拒绝 `packages.system`，包括 null 或空列表（400，param `packages.system`）。软件包响应仍会包含官方要求的 `system: []`。
- npm 会安装到本地 prefix，Python/pip 会安装到 Runtime 软件包目录下的本地 target；Node/npm 和 Python/pip 必须已经安装。其依赖项可被每个工作目录中的原生工具看到。
- 设置命令使用 Bash 运行；在 Windows 上必须使用 Git Bash，且不能由其他 shell 替代。默认工作目录为 `/workspace`。
- 在 Windows 上，npm 安装以及名为 `npm` 或 `npx` 的 stdio MCP 命令（包括其 `.cmd` shim）会通过 Node 调用 npm 的 JavaScript 入口点运行，而不经过额外的 shell。

初始化目录和软件包目录默认分别是 Runtime 主目录（`OAC_RUNTIME_HOME`）下的 `initialization` 和 `packages`，也可通过 `OAC_RUNTIME_INITIALIZATION_DIRECTORY` 和 `OAC_RUNTIME_PACKAGE_DIRECTORY` 设置；打包的 Linux 镜像使用 `/environment/initialization` 和 `/environment/packages`。这些是资源路径，在 Core 中绝不是 Environment 源或操作系统开关。

每条命令都使用启动用户的权限和主机网络。进程所有权会等待退出及 I/O 结算完成。命令输出会被丢弃；确认失败时只保留一个有界整数退出状态。

### 显式本地工具环境 {#explicit-local-tool-environment}

安装器的 `--tool-env-file`（`OAC_RUNTIME_TOOL_ENV_FILE`）提供 Runtime 操作者的基础工具变量。准备过程会将这些值复制到其私有初始化快照中，并由 Session 的 `env` 键覆盖。Runtime 绝不会重写源文件，也不会继承无关的环境凭据。设置、能力解析和 Harness 执行都会读取同一份已准备快照。即使操作者编辑了文件，重连仍会保留该快照；新 Session 会读取当前文件。Harness profile 可以引用由 Runtime 所有的文件，但不得持久保存其值的副本。配置的文件缺失或无效时，准备过程会失败。

### 初始化状态与失败 {#initialization-state-and-failure}

Environment 的初始化状态为 `pending`、`running`、`complete` 或 `failed`，且独立于任何分配、身份验证和连接发布。

- Worker 的初始化调度器每次扫描 32 个 Environment，在末尾循环回绕，并依据执行并发度限制并发准备，且独立于 Provider 维护。
- 缺少套接字不会消耗一次 pending 尝试。Harness 不可用时，会在安装前失败。每个操作都会重新检查当前权限和原始套接字；完成时还会重新检查精确绑定。
- 每个文件传输、configure、Skill、Plugin、软件包和设置步骤都有两分钟的预算；整个初始化过程有 30 分钟。初始输入仍保留其五分钟接纳期限，因此大型安装应从空闲 Session 开始。
- 正在运行且所有权丧失的初始化，包括跨 Core 重启丧失所有权，会作为未确认而失败；不会重播任何内容。已完成的 Environment 在重连或原生恢复时绝不会重新安装，因此用户后续更改会保留下来。
- 失败对 Session 而言是终止状态，但不会销毁计算资源或文件。

失败时，一个事务会将 Environment 标记为失败，并记录 `agent.session.environment.failed`、一个 `error` 事件和一个 `agent.session.failed`。Session 读取会返回 `failed`、作为 `error` 的原因以及作为 `last_active_at` 的失败时间；实时流会在失败事件后结束。待处理输入以失败状态结算。原因只指明步骤及其退出状态：

| 失败步骤 | 原因 |
| --- | --- |
| 设置命令 `i`，已确认退出 1–255 | `Failed to provision environment: script "setup_commands[i]" failed with exit code N` |
| Python 软件包，已确认退出 1–255 | `Failed to provision environment: script "Python package installation" failed with exit code N` |
| npm 软件包，已确认退出 1–255 | `Failed to provision environment: script "npm package installation" failed with exit code N` |
| 初始文件安装，确认失败 | `Failed to provision environment: initial file installation failed` |
| Skill 准备，确认失败 | `Failed to provision environment: Skill installation failed` |
| Runtime 上未安装 Harness | `Failed to prepare environment: the selected Harness is unavailable. Install the supported Harness version on the Runtime and create a new Session.` |
| 其他情况：超时、未知效果、回执缺失或格式错误、Plugin 安装、快照最终确定、bootstrap 拒绝、Core 重启 | `Failed to provision environment: initialization did not complete` |

每个初始化操作都会返回有类型的 `rejected`、`failed` 或 `unknown` 结果，并且 daemon 会先确认进程退出和 I/O 结算完成。Core 使用固定标签和整数组成原因，因此命令、env 值、软件包名称、路径和进程输出绝不会进入原因、事件、日志或响应。失败步骤不会重试，后续步骤也不会运行。机密 env 和设置快照会与普通元数据分开加密。初始文件在所有平台上都使用原子替换写入器和工作区锚定路径；Files API 创建则保留其自己的不覆盖规则。

## Templates {#templates}

Template 是由 Project 拥有、供 `openai_hosted` Session 和 `x_agents_core.environment` 使用的配置。它不包含运行中的工作区，也与 E2B templates 等 Provider 镜像无关。每个引用它的 Session 都会通过与内联配置相同的准备过程获得自己的 Environment。Template 解析、存储和解析过程绝不会选择 Harness 或 Provider，也不会依赖原生工具名称或 Harness 私有路径，因此新的 Harness 或 Provider 无需修改 Template。

```python
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
template = client.beta.agents.environments.templates.create(
    name="Python workspace", network={"access": "enabled"},
    env={"APP_MODE": "analysis"}, packages={"python": ["packaging==26.0"]},
    setup_commands=[{"command": "mkdir -p /workspace/outputs"}],
)
session = client.beta.agents.sessions.create(
    agent={"model": "your-configured-model"},
    environment={"type": "openai_hosted", "environment_template_id": template.id},
    input="Create /workspace/outputs/report.txt containing the result of 6 * 7.",
)
```

### 操作 {#operations}

- 每个操作都需要 Project API key 和 `OpenAI-Beta: agents=v1`。Template 操作无需执行部署，也不会分配计算资源。
- `name` 可选且可为 null，会逐字保留，长度为 1–256 个 Unicode 字符。
- `network.access` 为 `enabled`、`disabled` 或 `restricted`；省略或传入 null 表示启用。请参阅 [Restricted network](#restricted-network)。
- 响应会携带安全元数据，绝不会包含 `env`、`setup_commands` 正文或内联文件数据。
- 列表操作使用 `after`、`limit`（默认 20；0 按 1 处理，超过 100 的值按 100 处理）和 `order`（默认 `desc`），并按创建时间和 ID 排序。不存在和属于外部 Project 的 Template ID 及游标都会返回相同的 404。
- 更新时，省略字段会保留原值，提供字段则会替换原值。Null 会清除 `name` 和每个列表，并将 `network` 重置为启用。
- 封装或解封机密内容（文件、env、设置命令、Skills、Plugins）的写入操作和 Session 解析需要 Core 的 [credential key](../../../docs/zh/configuration.md#installation-directory)；元数据读取则不需要。
- Session 会在创建时于其 Project 内解析一次 `environment_template_id`，冻结有效配置，并且绝不将 Template ID 传递给 Provider 或 Runtime。更新或删除 Template 绝不会改变现有 Session。创建重试会在读取 Template 之前恢复已记录的调用方意图，即使 Template 已删除也是如此；意图发生变化时会产生冲突。

### 继承 {#inheritance}

Session 会先应用 Template，然后应用自身字段。组合仅在加密 Session 快照写入前执行一次，并使用常规验证器重新验证结果。调用方意图（省略、null 或显式提供）会单独保留，用于重试策略。

| 字段 | Session 中省略或为 null | Session 中提供 |
| --- | --- | --- |
| `network` | 继承整个 Template 策略 | 必须收窄策略：enabled 可以变为 restricted 或 disabled；restricted 可以变为其主机的子集或 disabled；disabled 不能放宽 |
| `env` | 继承 Template 键 | 按键叠加；Session 值优先；`{}` 会保留所有 Template 键 |
| `setup_commands` | 继承该序列 | 替换该序列；`[]` 会清除 |
| `files` | 继承文件集 | 替换整个文件集；`[]` 会清除 |
| `packages` | 继承两个管理器 | 分别解析 `python` 和 `npm`：省略或为 null 的管理器会被继承，列表会替换原值，`[]` 会清除；`system` 会被拒绝 |
| `skills`、`plugins`、`capability_directories` | 继承列表 | 替换列表；`[]` 会清除 |

作为比较，不带 Template 的内联 `openai_hosted` Session 会将省略或为 null 的 `network` 视为启用。

### 受限网络 {#restricted-network}

`restricted` 要求在 `allowed_domains` 中提供 1–100 个精确 ASCII 主机名；子域名和重定向目标需要各自的条目。通配符、URL 或端口语法、IP 字面量、Unicode 和尾随点都会被拒绝。读取操作会返回提供的拼写、顺序和重复项；比较操作使用单独的小写去重副本。

Runtime 不会强制执行 `disabled` 或 `restricted`，也没有 Provider 代为强制执行。因此，Core 会在 Templates 中存储这些策略，但会在分配计算资源之前拒绝任何有效 network 不是 `enabled` 的 Session。初始化和工具使用主机现有的网络。

### Env 与设置命令 {#env-and-setup-commands}

Agent 代码可以读取 env 值，但它们绝不会出现在公开元数据或初始化诊断中。名称必须匹配 `^[A-Za-z_][A-Za-z0-9_]*$`，且值不能包含 NUL。Core 保留 `PATH`、`OPENAI_API_KEY` 以及所有以 `OAC_` 或 `CODEX_` 开头的名称。文件和软件包会在设置命令之前安装；退出状态非零的设置命令会使初始化失败；效果未知时不会重试任何命令；已完成设置在重连时绝不会再次运行。

### 初始文件 {#initial-files}

`files` 条目会将文件放置在 `/workspace` 内的绝对目标路径，来源可以是内联标准 Base64 `data`，也可以是 Project 所有的已上传 `file_id`。

| 限制 | 值 |
| --- | --- |
| 每个配置的文件数 | 50 |
| 内联文件 | 5 MiB |
| 所有内联内容 | 10 MiB |
| 引用文件 | 50 MiB |
| Session 或 Template 请求正文 | 16 MiB |

路径必须规范、互不相同且位于逻辑工作区内部；Runtime 会将每次写入锚定到其绑定的工作区。这是 API 路径范围，不是对以同一用户身份运行的原生工具的限制。Template 元数据会将内联文件显示为 type、path 和 size，将引用显示为 type、path 和 `file_id`；无论内联文件还是引用，每个 Session 都会获得全新的文件 ID 和大小。文件数据不会出现在普通配置、响应、事件或命令参数中。Template 会保留引用；每个 Session 会授权并冻结自己的加密源字节，因此之后删除源文件无法改变这些字节。

### Skills {#skills}

Templates 和内联配置都接受 Project 所有的 Skill 引用及内联 Skill ZIP。通过固定版本 SDK 上传目录，然后引用其默认版本：

```python
skill = client.skills.create(files=[
    ("report/SKILL.md", b"---\nname: report\ndescription: Create the report.\n---\nFollow the report procedure.", "text/markdown"),
])
template = client.beta.agents.environments.templates.create(
    skills=[{"type": "skill_reference", "skill_id": skill.id}],
)
```

固定版本的 `/v1/skills` 资源、版本及内容路由使用 Project API key，且不使用 Agents beta 标头。ZIP 上传使用 `files`，目录上传则重复使用 `files[]`。固定版本 SDK 3.13.0 在多部分内容提取过程中会丢弃单文件 tuple，因此应使用原始 HTTP 上传单个 ZIP。一次上传最多包含 500 个常规文件，并且必须恰好包含一个 `SKILL.md`；压缩后为 5 MiB，展开后为 20 MiB。[File resource semantics](source-files.md#versions-and-metadata) 负责默认版本和删除规则。

省略或传入 null 版本的引用会在 Session 创建时选择默认版本，`"latest"` 会选择最新版本，正版本号字符串则选择该版本。Template 响应会保留未解析的选择器（默认版本为 `version: null`）；Session 元数据会显示 `{type, skill_id, version, name, description}`，其中版本为具体值。Session 会在其创建事务中冻结所选版本的字节和元数据；之后默认版本发生变化、源被删除或 Template 被更新，都无法改变这些内容。

内联 Skill 携带 `name`、`description` 和 Base64 ZIP `source`（`media_type` 为 `application/zip`）。归档包含一个顶层文件夹，其中有 `SKILL.md` 和可选的支持文件；清单中的 name 和 description 必须与请求一致。Frontmatter 可以包含 `name`、`description`、`license`、`compatibility` 和字符串 `metadata`；原生 hooks、权限控制和 subagent 指令都会被拒绝。只允许常规文件：路径遍历、链接、重复目标位置、特殊文件和无效清单都会被拒绝。内容在安装期间保持惰性，并保留可执行位。内联元数据只显示 type、name 和 description。

### Plugins 与能力目录 {#plugins-and-capability-directories}

Plugin 是带有 type、name 和 description 的内联 ZIP，其单个归档根目录包含 `.codex-plugin/plugin.json`。清单中的 `skills` 指定 Skill 目录；整个包布局都会保留。公开 Plugin 元数据只显示 type、name 和 description。

`openai_hosted` 和 Template 的 `capability_directories` 接受 `/workspace` 内的干净绝对路径，初始文件和设置命令可以填充这些路径。`self_hosted` 能力目录是机器上的绝对本地路径。通过目录发现的 Skills 绝不会显示为 `skills` 或 `plugins` 条目。目录缺失、Skill 名称重复、清单不受支持以及存在非常规文件时，初始化会失败。

| 归档与安装限制 | 值 |
| --- | --- |
| 每个归档 | 压缩后 5 MiB，展开后 20 MiB，1,000 个条目 |
| 内联 Skills | 总计 50 个，压缩后共 10 MiB，展开后共 50 MiB |
| Plugins | 总计 50 个，压缩后共 10 MiB，展开后共 50 MiB |
| 已安装快照 | 50 个 Skills、50 个 Plugins、50 MiB |

## Skills、Plugins 与 Environment MCP {#skills-plugins-and-environment-mcp}

Skills 及其不可变版本是 Project 资源，独立于 Session 和原生安装。

- 版本分配和指针变更会在所属 Skill 行上串行化。顶层 name 和 description 在同一事务中跟随默认版本；非默认上传不会更改它们。在并发上传和删除过程中，版本身份始终唯一。
- Bundle 使用服务密码进行加密，并绑定到 Project、Skill 和版本。元数据读取绝不会加载或解密 Bundle。删除 Skill 会回收其版本，但不会影响已冻结的 Session。
- 引用会在 Session 创建事务中、upsert 建立所有权之后解析。被引用的资源按稳定顺序锁定，版本、元数据和字节会一并冻结。公开引用元数据、未解析的 Template 意图和已解析的 Runtime Bundle 始终彼此独立；引用绝不会报告为内联内容。

内联和引用的 Skills 使用相同的机密快照与安装器。Runtime 会在设置和原生执行之前，将它们安装到能力根目录下的 `skills/<name>`。适配器只注册所选的 Skill 根目录：Codex 将其注册为显式额外根目录，MiniMax Code 通过其原生目录进行注册，Claude 则为每个包使用一个受控封装，其中包含实际目录和不可变硬链接。自动原生 MCP 发现保持禁用。Codex 的嵌套 `SKILL.md` 发现、`agents/openai.yaml` 依赖项配置以及 Claude 内联 shell 预处理都会导致适配器准备失败。原生 Harness 配置绝不会整体透传。

### Plugin MCP {#plugin-mcp}

Plugin 可以在 `.codex-plugin/plugin.json` 中通过 `mcpServers: "./.mcp.json"` 声明 MCP 服务器，也可以在省略路径时使用根目录下的 `.mcp.json`。该文件包含以服务器名称为键的 `mcpServers`。将 Plugin 根目录选为能力目录会激活其 MCP 声明；选择父目录则会发现 Skills，但不会激活嵌套 MCP 服务器。

共享解析器接受 HTTP `url`、`bearer_token_env_var` 和字面量 `http_headers`，以及 stdio `command`、`args`、选定的 `env_vars` 和包相对路径 `cwd`。不支持公开的 `env_http_headers`。Runtime 会重新解析已冻结的已安装包，并且只从已初始化的 env 中解析选定值；缺少值时会失败，而不会回退到模型或 daemon 变量。

stdio 服务器通过 daemon 的 stdio helper 启动；该 helper 会解析已安装的声明，并以 Harness 的权限启动命令。在 Unix 上，helper 会将自身替换为服务器；在 Windows 上，它会在所属进程树内部转发 stdio。已初始化的值会覆盖声明中的变量。进程组和 Windows Jobs 负责取消及后代进程清理，而不负责隔离。

[Harness capabilities](harness-capabilities.md#environment-preparation) 负责受支持的 Plugin 传输及每个 Harness 的限制。

Environment MCP 需要启用的网络。重复的服务器身份会被拒绝。Claude 会拒绝字面量标头，因为固定版本客户端会再次展开这些标头，并将自定义标头跨来源转发。MiniMax ACP HTTP 声明会保留在 Session 本地的原生内存中；令牌绝不会进入原生配置文件或进程参数。无法通过 Plugin 清单设置必需初始化和工具允许列表。

### 有效绑定 {#effective-bindings}

Runtime 会在适配器投影之前，通过 `agent.ResolveMCPBindings` 解析公开 HTTP 声明和已安装 Plugin MCP。每个瞬时绑定都会保留其连接来源、传输、可为 null 的工具允许列表、必需标志、凭据权限以及已安装 stdio 身份。绑定绝不会持久化或记录；重复身份和不可用的选定凭据都会被拒绝。MiniMax 会读取其 Session 私有的原生 runtime 名称注册表，以获取精确的首帧身份，并针对两种传输交叉检查已完成的原生结果；适配器绝不会伪造延迟启动事件或猜测身份。

### 公开 MCP 连接来源 {#public-mcp-connection-origin}

Agent 的 HTTP MCP 工具（[declaration](execution-tools.md#http-mcp)）具有 `connection_origin`。省略或传入 null 表示 `service`，在 `self_hosted` Session 中也是如此。该来源会从已保存配置经过 Session 快照一直保留到 Runtime 请求，后者要求精确的线上版本；`service` 请求绝不会变为 `environment` 连接。

| 来源 | 连接发起位置 | 允许的放置方式 |
| --- | --- | --- |
| `service` | Core 的服务端执行主机 | 仅 `none` |
| `environment` | Environment 的工作区 | `openai_hosted` 和启用网络的 `self_hosted` |

Core 的 Harness profile 会声明 `MCPOrigins`；接纳和分派会将来源与放置方式以及 Runtime 公布的 HTTP、bearer 和必需初始化能力进行核对，Runtime 会在调用适配器之前再次验证来源。不存在按 Harness 名称或 Provider 分支选择不同路径的逻辑。

[Harness capabilities](harness-capabilities.md#tools) 负责每个 Harness 的来源支持和策略限制。

两种来源都支持匿名 HTTP 和 HTTPS bearer 凭据。所附加 Vault 的选择会冻结凭据身份，包括唯一的隐式 URL 匹配或匿名选择。只有该凭据经 Project 授权后，才会进入瞬时 Runtime 请求；绝不会搜索 Core 默认值或无关 Vault。解密失败或凭据缺失时，执行会失败且不会回退到匿名。公开 Environment MCP 保留 `project_vault` 权限，Plugin 凭据保留 `environment_configuration` 权限；两者都不会覆盖重复的服务器标签。Bearer 令牌绝不会进入持久化的原生配置或进程参数。

工具允许列表和必需初始化遵循 [HTTP MCP contract](execution-tools.md#http-mcp)。
