---
title: "控制台 API 使用"
source: docs/web/console-api-usage.md
source_hash: 0b1593010b41cf81e705ccf5cdb6df3c15e358b6da6b79ab588c552baa903481
---

本页列出各控制台页面读取和写入的 Core 路由，以及控制台如何限定读取范围。[administrator API contract](../../../contracts/agents-api/zh/admin-api.md) 定义了路由、响应结构、分页和审计记录；[API namespaces and credentials](../api/index.md) 定义了本文使用的术语。

## 接口 {#interfaces}

| 接口 | 路径 | 身份验证 | 控制台用途 |
| --- | --- | --- | --- |
| Console server | `/console/auth`、`/console/auth/{login,logout}`、`/console/config`、`/console/installation/domain`、`/node-install/manifest.json` | 登录时使用 Core 密钥，随后使用控制台会话 Cookie；`/node-install/manifest.json` 无需登录 | 登录和退出；Add node 所用的节点安装程序和节点构件；**System → Domain and HTTPS** 上的域设置；用于 Docker 和 microsandbox 设置的发行版 Runtime release。参见 [console server](console-server.md) |
| Administrator API | `/core/v1/**`，不包括 `/core/v1/sandbox` | Core 密钥，由控制台服务器添加 | 项目、密钥、资源读取和删除、诊断、执行器凭据和安装命令、来源信息、汇总、Core 指标、安装信息、默认模型 |
| Sandbox administration | `/core/v1/sandbox/**` | Core 密钥，由控制台服务器添加 | Sandbox 配置；Overview 和 Sandbox metrics 中的 Nodes、机群与容量数据；每个项目的 Runtime observations |
| Agents API | `/v1/**` | 项目 API 密钥 | 不使用。控制台会向开发者说明如何调用它（参见 [Provenance and monitoring](#provenance-and-monitoring)） |

浏览器请求均为同源请求，并且仅携带控制台会话 Cookie。浏览器只在登录请求体中发送一次 Core 密钥，且绝不存储该密钥；它既不持有也不发送 API 密钥或 `OpenAI-Beta` 标头。控制台通过 [`packages/agents-client`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/packages/agents-client/README.md) 中的 `AdminClient`、`SandboxAdminClient` 和 `CoreMetricsClient` 执行读取，这些客户端会验证每个响应：格式错误的值会被报告为失败，或按下文说明标记为无法识别，绝不会用猜测值或零值替代。

## 项目与密钥 {#projects-and-keys}

| 操作 | 路由 | 控制台用途 |
| --- | --- | --- |
| 列出项目 | `GET /core/v1/projects` | 每个项目范围页面上的项目筛选器；Projects 和密钥列表；Overview 的 Getting started（一个拥有活动密钥的项目，以及最新的活动项目，优先选择拥有活动密钥的项目，并打开其调用示例以进入首个 Session 步骤）；归档确认中的 `active_key_count`，当项目的密钥列表显示更多密钥时，该计数也会增加 |
| 创建项目 | `POST /core/v1/projects` | **Create project**，也可从 Getting started 进入 |
| 重命名项目 | `POST /core/v1/projects/{project_id}` | 对活动项目执行 **Rename**；ID 保持不变 |
| 归档项目 | `POST /core/v1/projects/{project_id}/archive` | **Archive**：吊销所有密钥；项目资产仍可读取和删除 |
| 列出密钥 | `GET /core/v1/projects/{project_id}/keys` | 项目密钥表：名称、前缀、状态、创建时间和吊销时间；归档确认所统计的活动密钥 |
| 签发密钥 | `POST /core/v1/projects/{project_id}/keys` | 对活动项目执行 **Issue key**，也可从 Getting started 进入；明文仅显示一次 |
| 吊销密钥 | `DELETE /core/v1/projects/{project_id}/keys/{key_id}` | **Revoke**；当它是项目最后一个活动密钥时会显示警告 |

发送前会检查名称长度（项目为 1–128 个字符，密钥为 1–80 个字符）和控制字符。已签发密钥的明文会保留在组件内存中，直到管理员确认已保存；绝不会写入浏览器存储、URL 或日志。项目不能删除，明文也无法恢复。

## 项目资源 {#project-resources}

以下路由相对于 `/core/v1/projects/{project_id}`，并返回与相应公开 `/v1` 操作相同的对象，因此控制台会应用公开客户端的严格投影。已归档项目仍可读取。

| 资源 | 使用的读取操作 | 删除对象 | 创建者 | 控制台界面 |
| --- | --- | --- | --- | --- |
| Agents | `/agents`、`/agents/{agent_id}` | Agent | `agent` | Agents 列表，含每个 Agent 的用量；Agent 页面，含说明、工具、已保存的模型提供商（绝不显示其密钥）、生成设置和元数据 |
| Environment templates | `/environment-templates`、`/environment-templates/{id}` | Template | `environment_template` | Templates 列表；Template 页面，含所有安全区段 |
| Skills | `/skills`、`/skills/{skill_id}`、`/skills/{skill_id}/versions`、Skill 和版本的 `/content` | Skill 和 Skill version | `skill`（列表） | Skills 列表；Skill 页面，含版本和归档下载 |
| Files | `/files` | File | `file` | Files 列表（仅元数据） |
| Vaults | `/vaults`、`/vaults/{vault_id}`、`/vaults/{vault_id}/credentials` | Vault 和 Credential | `vault`、`credential` | Vaults 列表；Vault 页面，含 Credential 元数据 |
| Sessions | `/sessions`、`/sessions/{session_id}`、`/sessions/{session_id}/items`、`/sessions/{session_id}/turns`、`/sessions/{session_id}/runtime-observation`、`/sessions/{session_id}/runtime-history` | Session | `session` | Session log；Session 页面；Agent 指标；托管 Runtime 行 |
| Diagnostics | `/sessions/{session_id}/diagnostics`、`/sessions/{session_id}/turns/{turn_id}/diagnostics` | — | — | Overview 中失败 Session 或 Turn 状态下的分类原因；Session log 和 Session 页面；跟踪中每个 Item 的 Core 接收时间 |

资源专用规则：

- **Environment templates.** `env` 和安装命令为只写且绝不返回，因此控制台无法判断 Template 是否包含它们。内嵌文件仅报告大小。如果客户端无法识别 Template 的某个区段或字段，则会加以标记；仍会显示可识别的区段，且不会猜测其他内容。
- **Skills.** 版本上传、默认指针更改及所有其他 Skill 写入操作均使用项目的密钥。控制台会将默认版本或指定版本下载为 ZIP、删除版本（只要仍有其他版本，默认版本就无法删除；删除唯一版本会删除该 Skill），并要求输入 Skill 名称后删除该 Skill。
- **Files.** 列表每页读取 100 条，可按最新或最早顺序排列。管理员 API 没有 File 内容路由，因此控制台不提供下载。
- **Vaults.** Credential 令牌绝不返回。控制台显示每个 Credential 的名称、MCP 服务器 URL、身份验证类型和更新时间。
- **Sessions.** 如果某个 Session 格式错误，其所属项目的读取会失败，而不会跳过该 Session。
- **Diagnostics.** 控制台会翻译 Core 分类的原因，绝不从原始日志推断原因。诊断信息不可用或不匹配时，会提供显式的读取重试；重试绝不会重新执行操作。

## 执行器凭据和主机连接 {#executor-credentials-and-host-connection}

仅当 Session 的环境为 `self_hosted` 时，Session 页面才会显示 **Executor credentials** 区段，并针对该 Session 的 `project_id` 和 `environment.id` 读取凭据。[executor credential contract](../../../contracts/agents-api/zh/environment-executor-credentials.md) 定义了路由、404 和 409 响应以及凭据文件。

| 操作 | 路由 | 控制台用途 |
| --- | --- | --- |
| 列出凭据 | `GET /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | 可见时每 5 秒读取一次。表格显示每个凭据的短 `key_id` 及其复制按钮、创建时间（`created_at`，轮换不会改变该值）和状态（Active，或 Revoked 及其时间），并优先显示活动凭据；绝不会列出凭据本身。同一次读取中的 `connection` 观测值用于驱动主机连接面板：never connected、connected、disconnected、bound credential revoked 和 unknown 保持为不同状态，且只有重新读取并得到 connected 才会将主机标记为已连接 |
| 签发或轮换 | `POST …/executor-credentials`，请求体为 `{"key_id", "rotate"}` | Issue 会在提交前保留生成的密钥 ID。轮换前会确认旧凭据将停止工作；轮换已吊销的凭据会恢复该凭据并生成新密钥。私有 JSON 仅显示一次，供下载或复制，绝不会存储在浏览器存储或查询缓存中，并在 Done 时清除 |
| 吊销 | `DELETE …/executor-credentials/{key_id}` | **Revoke**，需确认（执行器会断开连接且不会重试；其守护进程会保持停止状态，直到操作员将其停止），随后再次读取列表，并显示凭据为 Revoked |
| 安装命令 | `GET /core/v1/projects/{project_id}/environments/{environment_id}/installation` | **Connect a host**：按 Core 返回的原始内容显示短效 Linux/macOS 和 PowerShell 命令，并提供平台选择器以及指向 [native installation guide](../getting-started/self-hosted.md) 的链接。这些命令会安装守护进程及其 Harnesses、启动守护进程并检查连接；其授权在 30 分钟后过期，控制台每 20 分钟重新读取一次。如果没有可用且未过期的响应，该区段会说明命令不可用。已归档项目不会读取该信息 |

在已归档项目中，该区段会通过提示隐藏 **Issue credential** 和 **Rotate**，但保留列表和 **Revoke**，因为 Core 仍允许吊销。

## 来源与监控 {#provenance-and-monitoring}

| 操作 | 路由 | 控制台用途 |
| --- | --- | --- |
| 资源所有者 | `GET /core/v1/projects/{project_id}/resource-owners` | 每个资源列表的 Creator 列和详情页的创建者信息，每批最多处理 100 个 ID：创建密钥的名称；来源为 `admin_copy` 的所有者显示 **Admin copy**；Core 无记录时显示 **Unknown** |
| 写入操作 | `GET /core/v1/projects/{project_id}/write-operations` | 项目的写入历史，按最新优先，可按密钥和资源类型筛选，每页 50 条 |
| 汇总 | `GET /core/v1/summary` | Overview（按项目）、Agents 列表（`group_by=agent`）、项目页面（按项目并使用 `group_by=key`）、Agent 指标（跳过空闲项目，并统计从范围开始以来按创建密钥划分的使用量）、Projects 列表（最近活动） |
| 安装 | `GET /core/v1/installation` | System 的 Installation 信息（`public_url`、`api_base_url`、`installation_id`、`source_commit`）和只读 Startup 设置（`path` 下的 `configuration.settings`，以及 `apply_command` 和 `applied_at`；敏感设置仅显示其是否为 `configured`）；调用示例中的 `api_base_url`；作为下载来源以及节点安装和卸载命令中 `--source-url` 的 `public_url`（还包括安装命令中的 `--core-url`）；Core 拒绝的 Sandbox 配置旁的 `path` 和 `apply_command`。如果敏感设置包含值，或存在未知成员，读取会失败；`configuration: null` 会显示一条说明 |
| Core 指标 | `GET /core/v1/metrics?range=` | Core 指标页面；Overview 上的 Core 弹出内容。不存在该路由的 Core（404）会显示为未报告数据，此时弹出内容仅显示 Core 状态。[Core metrics contract](../../../contracts/agents-api/zh/core-metrics.md) 定义了每项度量 |

如果为 `local_only`，或者 `public_url` 不是 HTTPS 来源，Add node 将无法签发命令，Clean up the host 也无法提供命令。随后 Overview、Nodes 和 System 会显示醒目警告，其中 Core 的配置路径和 apply command 为可复制值；当 `configuration` 为 null 时，它们会说明路径和命令不可用。Nodes 会禁用 Add node 并显示明确原因，Getting started 则将 sandbox 步骤保留为待办项。

无论是在显示新密钥时，还是在活动项目页面没有显示任何密钥时，控制台都会提供 `OPENAI_BASE_URL`（安装的 `api_base_url`）和 `OPENAI_API_KEY`（新密钥，或项目密钥的占位符）的 shell 导出变量，以及针对 `GET /v1/agents` 和 `POST /v1/agents/sessions` 的 `curl` 和 Python 示例，但不会发送其中任何调用。当安装为 `local_only` 时，控制台会说明 API 只能在 Core 所在计算机上访问；当缺少 `api_base_url` 时，则会提示设置 `public_url`。

汇总数值按 Session 累计，并不是计费记录。未报告使用量的 Session 会计入覆盖率，但不会计入 token 总量；控制台会将缺失值显示为缺失，绝不会显示为零。

## 默认模型 {#default-models}

| 操作 | 路由 | 控制台用途 |
| --- | --- | --- |
| 列出 Harnesses | `GET /core/v1/harnesses` | System 的 Default model 卡片：每个 Harness 的只读 `enabled` 和 `default`、不含密钥的模型配置，以及来自配置中 `last_used_at`、`last_error_code` 和 `last_error_at` 的 Usage details；Overview 的 Getting started（默认 Harness 上的默认模型；如果没有默认模型，则为任意已启用 Harness 上的默认模型） |
| 设置或替换 | `PUT /core/v1/harnesses/{harness}/model-configuration` | **Set** 或 **Replace**：提交包含只写提供商密钥的完整模型配置；该密钥绝不预填，写入也绝不重试；400 会在表单中显示 Core 的消息，503 `credential_storage_unavailable` 表示 Core 没有凭据加密密钥；随后再次读取列表 |
| 清除 | `DELETE /core/v1/harnesses/{harness}/model-configuration` | **Clear**，需确认，随后再次读取列表 |

列表会返回每个 Harness 的配置，因此控制台不会读取 `GET /core/v1/harnesses/{harness}/model-configuration`。

## Sandbox 管理 {#sandbox-administration}

| 操作 | 路由 | 控制台用途 |
| --- | --- | --- |
| 部署 | `GET`、`POST`、`PUT /core/v1/sandbox/deployment` | 读取提供商、只读 `core_url`（即 config.json 的 `public_url`，会显示在设置审核中且绝不发送）、重置状态、安装 ID 和规范；409 `sandbox_configuration_error`（E2B 搭配回环地址形式的 `public_url`）会在设置向导中显示共享客户端固定的安全地址配置消息，并同时显示安装的配置文件和 apply command，且无需确认；使用 `resources` 以及 Docker 或 microsandbox 的 `runtime` release 初始化部署，或者使用 E2B 账户且不提供 `resources`（Core 采用模板构建的 CPU 和内存）；使用预期的 generation 更改设置。E2B 的 `metadata.template_build`（状态、CPU、内存、磁盘）会显示在 System、Sandbox 配置摘要和 Sandbox metrics 中；当缺少 `specification.resources` 时，它还会确定每个 Sandbox 的大小；microsandbox 的 `suspension`（空闲和保留秒数）会显示在 System 和 Nodes 摘要中 |
| E2B 发现 | `POST /core/v1/sandbox/providers/e2b/discovery` | 设置向导先列出输入的 E2B 密钥可见的模板，再列出所选模板的可用构建。该密钥只会通过这些请求体和部署写入请求传输 |
| 重置 | `POST`、`DELETE /core/v1/sandbox/deployment/reset` | 显式清除托管资源，或在观测到的 generation 处取消剩余清除；显示 Core 的剩余资源和离线预测 |
| Nodes | `GET /core/v1/sandbox/nodes` | Nodes 页面；Overview 上的机群；Sandbox metrics 中的节点容量。在线节点的 `diagnostic`（`docker_unavailable`、`docker_limits_unsupported`、`runtime_image_unavailable`、`kvm_unavailable`、`microsandbox_artifacts_unavailable`、`capacity_insufficient`、`provider_unavailable`；任何其他值均读取为 `provider_unavailable`）会将其标记为降级，并在上述每个页面及节点页面中，紧邻状态的帮助提示里说明原因和修复方法。如果节点的 `core_url`（其注册时使用的地址）与部署的 `core_url` 不同，Nodes 页面会将其标记为绑定到旧地址，需要移除后重新添加；此时它在该页面和节点页面中的状态会显示 Old address，而不是健康状态；如果 `core_url` 为空（Core 未注册该节点），则状态为未知，而不是旧地址。**Add node** 仅跟踪 `enrollment_id` 与其命令所含 `enrollment_id` 相等的节点 |
| 节点详情 | `GET /core/v1/sandbox/nodes/{node_id}?range=1h\|6h\|24h` | Sandbox metrics 节点对话框：主机自最近一次心跳以来的 CPU 忙碌占比和内存使用量，以及页面所选范围内二者的历史记录。**Edit node** 读取 `host.effective_cpu_cores` 和 `host.total_memory_bytes`，用于在每个 Sandbox 大小旁显示主机容量，以及最多可容纳多少个该大小的 Sandbox |
| 分配 | `GET /core/v1/sandbox/nodes/{node_id}/allocations` | Nodes 页面；Sandbox metrics。在 microsandbox 下，节点页面根据 `compute_phase_changed_at` 显示每个分配处于计算阶段的时间，并在分配暂停时估算 Core 回收它的时间（该时间加上部署的 `suspension.retention_seconds`）；时间为 null 时显示短横线 |
| 注册 | `POST /core/v1/sandbox/enrollment-tokens` | **Add node**：管理员先设置节点的 Sandbox 限制（`max_active`；`max_retained` 仅适用于 microsandbox，在 Docker 下等于 `max_active`），然后 Core 才会把一次性令牌放入命令中；该命令会验证安装程序校验和，并包含命令的 `enrollment_id`，节点注册时会报告此 ID。命令使用 sudo 运行安装程序（作为系统服务），并通过标准输入传递令牌；以 root 运行时则直接执行。界面不提供普通用户安装或移除入口，日志提示始终指明系统服务。命令从安装的 `public_url` 下载安装程序。只有成功读取安装信息后才会请求令牌；如果安装信息无法读取、安装为 `local_only`（或其 `public_url` 不是 HTTPS 来源），或者 `/console/config` 列出的 `node_artifacts` 不包含部署的提供商，则不会请求令牌。对话框在打开时和窗口重新获得焦点时，会再次读取这两项信息 |
| 更新节点 | `PATCH /core/v1/sandbox/nodes/{node_id}` | **Edit node**：同时修改名称和 Sandbox 限制（保留数量限制仅适用于 microsandbox；在 Docker 下，Core 会将其设为活动数量限制） |
| 移除节点 | `DELETE /core/v1/sandbox/nodes/{node_id}` | 确认后移除节点；只有 Core 确认删除后该行才会消失，随后 Clean up the host 对话框会提供主机的卸载命令（需要 root 或 sudo；对于使用不同于部署地址的地址注册的节点，还需要使用 `--force`，以跳过安装程序与 Core 的确认） |
| Runtime 观测 | `GET /core/v1/sandbox/runtime-observations` | Sandbox metrics：每个项目的托管 Runtimes，每项均以所属项目为标签；E2B Sandbox 对话框还会将 `observation.disk` 显示为已用量／限制值（其他位置为 null） |

E2B 部署没有节点；其 API 密钥为只写。Overview 和 Sandbox metrics 根据部署的 `resources.allocations` 和 `resources.pending` 统计其正在运行和正在启动的 Sandbox，而托管 Runtime 行来自 Runtime observations。两个来源独立刷新，因此控制台不会根据两者之间的差异推断保留或清理状态。用于 Docker 和 microsandbox 的 Runtime release 来自控制台自身的 `GET /node-install/manifest.json`；如果无法获取该信息，管理员需在高级设置中输入 release。

## 写入 {#writes}

- 删除操作使用管理员 API，并采用与公开删除操作相同的前置条件。每次删除都需要确认。4xx 会让对话框保持打开并显示 Core 给出的原因，404 视为已删除，其他任何失败都会报告为结果不确定，并随后执行一次全新读取。
- 控制台仅允许删除没有必需操作且处于空闲或失败状态的 Session，并且绝不会为了使 Session 可删除而取消正在执行的工作。
- 项目、密钥、执行器凭据、删除和 Sandbox 写入操作，每项明确的用户操作只发送一次，绝不会自动重试。结果不确定时，相关信息会保持可见，直到管理员重新读取状态并作出决定。
- 执行器凭据签发结果未知时（无响应、30 秒超时或 5xx），控制台会打开错误对话框，下一步为 **Refresh list**。如果保留的 `key_id` 随后出现在列表中，则说明凭据已签发但其密钥已经丢失；控制台会提议轮换该凭据（`rotate: true`），以生成仅显示一次的新密钥。如果它未出现在列表中，下一次 Issue 会使用相同的 `key_id` 和 `rotate: false`；如果该请求因第一个请求实际上已成功签发而返回 409，控制台会再次读取列表，并且仅当该凭据在活动项目中列为活动状态时才提供相同的轮换操作，否则会将签发报告为已拒绝。已经列出的保留 `key_id` 绝不会被再次发送；从其所在行执行轮换或吊销后，控制台会清除该 ID，下一次 Issue 将生成新的 `key_id`。

## 读取上限 {#read-bounds}

控制台在浏览器中通过对每个项目的有限读取来组合多项数值。Session 历史记录会分页读取并轮询；不存在管理事件流。

| 页面 | 读取内容 | 上限 |
| --- | --- | --- |
| 资源列表 | 所选项目的所有页面，或并行读取所有项目的所有页面 | 每个项目最多 10,000 个条目；读取失败的项目会被指明，其他项目仍会显示 |
| Session log | 所选项目的所有 Session 页面，按最新优先 | 每个项目最多 10,000 个；按请求刷新 |
| Session 页面 | Session、Items 和 Turns | 最多 10,000 个 Items 和 Turns；Session 进行中或等待时每 5 秒轮询一次，失败后退避至 60 秒 |
| Overview | 汇总；24 小时活动和需要关注的 Sessions 所对应的 Session 列表 | 每个项目最多 1,000 个 Sessions；跳过空闲项目；可见时每 30 秒刷新一次。项目或 Session 读取失败时显示 Retry，而不是合成空结果；任何保留的数据或不完整数据都会附带明确的限定说明 |
| Agent 指标 | 汇总；Session 列表；最近活动的 Sessions 的 Turns 和 Items | 每个项目列出最多 2,000 个 Sessions；每次加载读取 200 个 Sessions，每个 Session 读取 10 个 Turn 页面和 5 个 Item 页面；每个 Session 为 15 秒，每次加载为 45 秒 |
| Sandbox metrics | Nodes 和 allocations；Runtime observations；按 ID 标识的托管 Sessions；Runtime 历史记录 | 每次刷新读取 100 个托管 Sessions；最多读取 24 个 Session 的历史记录；可见时每 30 秒刷新一次 |

Agent 指标具有以下限制：

- 一次请求对应一个根 Agent Turn；Subagent Turns 和已删除的 Sessions 不计入。无法获取 HTTP 请求计数、状态码和 API 延迟。
- 请求所用模型来自 Session 的 Agent 快照，而不是 Session 的执行配置。
- 活跃项目会超出 Session 上限，因此长时间范围的数据可能不完整。
- 按 API 密钥统计使用量时，会根据创建密钥统计该范围内创建的 Sessions；没有创建记录的 Session 记为 Unknown。
- 为允许浏览器与 Core 之间存在时钟差异，范围结束后最多 15 分钟内的 Turn 时间也会被接受。

其帮助提示和覆盖率说明会指出一次请求的定义和不计入的内容、模型的来源、哪些项目的数据被提前截断或哪些 Sessions 被跳过，以及使用量如何按密钥分组。说明中不会提及 HTTP 请求指标或时钟宽限时间。

## 未使用的接口 {#not-consumed}

- 任何 `/v1/**` 路由，包括 Session 创建、Session 事件及其流、消息输入、函数结果和取消操作。
- Agents、Environment templates、Skills、Files、Vaults 或 Credentials 的创建或更新，包括上传和 Credential 令牌替换。
- 单个 Turn 读取、Artifacts、Session 执行配置、Environment Files 和管理端 Session 归档。
- 管理员审计日志（`GET /core/v1/audit-log`）。System 会改为显示安装信息、每个 Harness 的默认模型和 Sandbox 部署。
