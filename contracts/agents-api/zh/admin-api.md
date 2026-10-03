---
title: "Core 管理 API"
source: contracts/agents-api/admin-api.md
source_hash: 3fc6573b19b9c78ca8a3b275122793b1a99e31f739d83ff25ff56624013dc428
---

Core 管理 API（`/core/v1`）用于管理安装实例：Project 及其 API 密钥、Project 资源的读取和删除、执行器凭据、部署默认模型、沙箱部署及其节点、监控和审计。Web 的[控制台服务器](../../../docs/zh/web/console-server.md#forwarding-to-core)会为已登录的管理员调用它；运维人员则从 Core 主机上的脚本调用它（[编写 Core API 脚本](../../../docs/zh/getting-started/operations.md#script-the-core-api)）。生成的架构是 [core.openapi.yaml](../core.openapi.yaml)，所有错误都使用 [Core 错误封装](core-errors.md)。

应用程序绝不调用 `/core/v1`。它没有任何用于创建或编辑 Agents、Sessions、模板、Files、Skills 或 Vaults，启动或取消工作，读取 Source File 内容或流式传输事件的操作；应用程序通过 [Agents API](../../../docs/zh/api/public-agent-api.md)完成这些操作。

## 身份验证 {#authentication}

每个 `/core/v1` 请求，包括针对未知路径的请求，都必须发送 `Authorization: Bearer <Core key>`。缺少该请求头时，Core 会返回 401 `invalid_admin_key` 和 `WWW-Authenticate: Bearer`；只有在身份验证通过后，未知路径才会返回 404 `not_found`。

- Core 将 bearer 凭据的 SHA-256 与启动时从 `generated/core-key-digests.json` 读取的 Core 密钥摘要进行比较；该摘要由 `oac apply` 根据 `secrets/core.key` 派生（[Core 密钥](../../../docs/zh/getting-started/operations.md#core-key)）。轮换 Core 密钥后，需要重启 Core 才会生效。未配置任何摘要时，Core 不提供 `/core/v1` 路由。
- Core 密钥仅用于 `/core/v1` 身份验证。Project API 密钥和机器凭据在此处会收到 401，而 Core 密钥在 `/v1` 和 `/api/v1` 上会收到 401（[API 命名空间和凭据](../../../docs/zh/api/index.md)）。
- `X-Core-Console-Actor` 是由调用方声明的标签。Core 会将其记录为审计 `actor_label`，但不会验证。控制台服务器发送 `console`；脚本通常不发送该值，因此会记录空标签。切勿将其用于授权或作为来源证明。
- 路径中的 `{project_id}` 用于选择目标 Project，包括已归档的 Project；它不会授予任何权限。

## 路由 {#routes}

路径均相对于 `/core/v1`。

| 路由 | 用途 | 契约 |
| --- | --- | --- |
| `installation` | 公共 URL、API 基础 URL、源代码提交、安装器的进程设置，以及绑定到公共 URL 的内容 | [安装信息](#installation-facts) |
| `projects`、`projects/{project_id}`、`projects/{project_id}/archive`、`projects/{project_id}/keys[/{key_id}]` | Project 及其 API 密钥 | [Project 与密钥](#projects-and-keys) |
| `projects/{project_id}/{agents,environment-templates,skills,files,vaults,sessions}/**` | 资源读取和删除、Session 历史及 Artifact | [资源读取和删除](#resource-reads-and-deletion) |
| `projects/{project_id}/sessions/{session_id}/archive` | 归档一个托管 Session | [Session 归档](#session-archive) |
| `projects/{project_id}/sessions/{session_id}/execution-configuration` | Session 创建时冻结的模型、Harness 和提供商选择 | [执行配置](#execution-configuration) |
| `projects/{project_id}/sessions/{session_id}/diagnostics`、`…/turns/{turn_id}/diagnostics` | 失败类别和 Item 接收时序 | [Session 诊断](session-diagnostics.md) |
| `projects/{project_id}/sessions/{session_id}/runtime-observation`、`sandbox/runtime-observations` | 当前 Runtime 观测值 | [Runtime 观测](runtime-observability-api.md)、[列表中的磁盘字段](#runtime-observations) |
| `projects/{project_id}/sessions/{session_id}/runtime-history` | 已存储的 Runtime 历史 | [Runtime 历史](runtime-observability-api.md#session-runtime-history) |
| `projects/{project_id}/environments/{environment_id}/installation` | `self_hosted` Environment 的安装命令 | [安装授权](environment-executor-credentials.md#installation-grant) |
| `projects/{project_id}/environments/{environment_id}/executor-credentials[/{key_id}]` | `self_hosted` Environment 的执行器凭据 | [执行器凭据](environment-executor-credentials.md#core-key-routes) |
| `projects/{project_id}/resource-owners`、`projects/{project_id}/write-operations` | 创建资源的 API 密钥以及每个密钥执行的写入 | [写入溯源](#write-provenance) |
| `harnesses`、`harnesses/{harness}/model-configuration` | 已启用的 Harness 及各 Harness 的部署默认模型 | [部署默认值](model-execution.md#deployment-defaults) |
| `sandbox/deployment`、`sandbox/deployment/reset`、`sandbox/providers/{provider}/discovery` | 沙箱提供商、资源和 Runtime、重置，以及提供商配置发现，例如 E2B 模板 | [沙箱部署](sandbox-deployment.md#routes) |
| `sandbox/enrollment-tokens`、`sandbox/nodes[/{node_id}[/allocations]]` | 节点注册令牌、节点及其分配和主机历史 | [节点指南](../../../docs/zh/getting-started/nodes.md)、[沙箱部署](sandbox-deployment.md)、[节点主机历史](runtime-observability-api.md#node-host-observations-and-history) |
| `summary` | 按 Project、Agent 或密钥统计的 Session 数量和使用情况 | [汇总](#summary) |
| `metrics` | Core 自身的进程、执行、数据库和作业指标 | [Core 指标](core-metrics.md) |
| `audit-log` | 管理员写入 | [审计日志](#audit-log) |

## Project 与密钥 {#projects-and-keys}

每个 Project 拥有一个执行租户；其密钥共享该租户的主体和资产（[Project 拥有资产](../../../docs/zh/concepts.md#projects-own-assets)）。Web 的 **Projects and keys** 页面使用这些路由。

| 操作 | 路由 | 结果 |
| --- | --- | --- |
| 列出 Project | `GET /projects` | `{data, has_more}` |
| 创建 Project | `POST /projects`，请求体为 `{name}` | 201 和 Project |
| 重命名 Project | `POST /projects/{project_id}`，请求体为 `{name}` | Project |
| 归档 Project | `POST /projects/{project_id}/archive` | Project |
| 列出密钥 | `GET /projects/{project_id}/keys` | `{data, has_more}` |
| 签发密钥 | `POST /projects/{project_id}/keys`，请求体为 `{name}` | 201、密钥元数据和明文 `key` |
| 吊销密钥 | `DELETE /projects/{project_id}/keys/{key_id}` | `{id, deleted: true}` |

- Project 包含 `id`、`name`、`created_at`、可为 null 的 `archived_at` 和 `active_key_count`。密钥包含 `id`、`project_id`、`name`、`prefix`、`created_at` 和可为 null 的 `revoked_at`。ID 是服务器生成的 UUID。
- Project 名称长度为 1–128 个字符，密钥名称长度为 1–80 个字符；名称仅作为标签，可以重复，控制字符会被拒绝。
- 列表按 ID 排序，可使用 `order=asc|desc`（默认 `desc`）、`limit=1..100`（默认 20）和 `after`。
- 只有签发响应会通过 `key` 包含密钥明文；Core 仅存储其摘要。密钥只应显示一次，且绝不能缓存。如果签发响应不确定，请列出密钥，吊销所有无法使用的密钥，然后再签发新密钥。
- 归档操作会在一个事务中将 Project 标记为已归档、吊销其所有密钥并写入审计条目。在已归档的 Project 中签发密钥会返回 409 `project_archived`。不支持删除 Project、取消归档或重置密钥。

[`/v1` 身份验证规则](wire-semantics.md#authentication)定义了密钥查找、吊销可见性、作用域标头和身份验证错误。

## 资源读取和删除 {#resource-reads-and-deletion}

路径均相对于 `/core/v1/projects/{project_id}`。每次读取都会返回与对应 `/v1` 操作相同的对象、分页和错误，每次删除也具有相同的前置条件。

| 资源 | 读取 | 删除 |
| --- | --- | --- |
| Agents | `/agents`、`/agents/{agent_id}` | `/agents/{agent_id}` |
| Environment Templates | `/environment-templates`、`/environment-templates/{environment_template_id}` | 单项路由 |
| Skills | `/skills`、`/skills/{skill_id}`、`/skills/{skill_id}/content`、`/skills/{skill_id}/versions`、`/skills/{skill_id}/versions/{version}` 及其 `/content` | Skill 和版本单项路由 |
| Files | `/files`、`/files/{file_id}` | 单项路由 |
| Vaults | `/vaults`、`/vaults/{vault_id}`、`/vaults/{vault_id}/credentials`、`/vaults/{vault_id}/credentials/{credential_id}` | Vault 和 Credential 单项路由 |
| Sessions | `/sessions`、`/sessions/{session_id}`，以及其下的 `/turns`、`/turns/{turn_id}`、`/items`、`/artifacts`、`/artifacts/{artifact_id}` 及其 `/content` | Session 和 Artifact 单项路由 |

- 管理员删除绝不会取消工作：如果 `/v1` 因 Turn 或输入待处理而无法删除某个 Session，管理员删除也会返回相同的 409。
- 删除 Credential 只会移除 Core 中的副本；不会撤销提供商端的授权。
- 对 Skill 和 Artifact 内容、Runtime 观测值、Runtime 观测值列表以及 Runtime 历史发送 `HEAD` 会返回 405，因此绝不会采样提供商或查询遥测数据。
- 管理员删除会显示在[审计日志](#audit-log)中，而不会显示在密钥写入历史中。

## Session 归档 {#session-archive}

使用 `{"expected_generation": N}` 发送 `POST /projects/{project_id}/sessions/{session_id}/archive`，可在不重置部署的情况下释放一个由 Core 管理的 `openai_hosted` Session 的沙箱。N 是[沙箱部署](sandbox-deployment.md)的当前代次，是一个正整数。必须先在 Web 中配置该部署。

| 情况 | 结果 |
| --- | --- |
| 代次过期 | 409 `sandbox_generation_stale` |
| Environment 类型不是 `openai_hosted` | 400 |
| Session 不存在或属于另一个 Project | 404 |

一个事务会将 Environment 标记为过期（已失败的 Environment 仍保持失败状态）、请求取消正在运行的工作、吊销 Runtime 的权限并写入审计条目。沙箱及其快照随后通过正常生命周期完成清理；如果资源是否已释放尚不确定，在提供商确认之前，该资源仍归 Core 所有。Session 不会被删除：其历史以及已持久化的 Files 和 Artifacts 仍可读取，未持久化的工作区内容会丢失，并且该 Session 无法恢复运行。

`POST` 和 `GET /projects/{project_id}/sessions/{session_id}/archive` 返回 `{session_id, environment_id, state}`。`GET` 是只读操作，不需要代次。`state` 表示资源的当前处置状态：`active`、`cleanup_pending` 或 `released`，无论该资源由什么操作释放。`released` 不表示活动的 Turn 已完成取消；请读取相应的 Turn。

如果 `POST` 响应不确定，请先对归档记录发送 `GET`，然后再进行写入。重复发送 `POST` 会产生相同效果，并为每个被接受的请求记录一个审计条目。要在更改部署前清除所有托管 Session，请使用[部署重置](sandbox-deployment.md#reset)。

## 执行配置 {#execution-configuration}

`GET /projects/{project_id}/sessions/{session_id}/execution-configuration` 报告 Session 在创建时冻结的模型、Harness、原生参数和模型提供商。它仅读取已存储的配置：绝不会联系提供商、启动 Turn 或唤醒沙箱。响应带有 `Cache-Control: no-store`。

```json
{
  "object": "agent.session.execution_configuration",
  "schema_version": 1,
  "session_id": "013773a9-44b9-4f84-baca-b51c04a01201",
  "model": {"value": "requested-model", "source": "session"},
  "harness": {"value": "codex", "source": "agent"},
  "harness_config": {"value": {"model_reasoning_effort": "high"}, "source": "agent"},
  "model_provider": {
    "source": "agent",
    "status": "available",
    "configuration": {"protocol": "responses", "base_url": "https://model.example/v1", "api_key_configured": true}
  }
}
```

每个 `source` 都是 `session`、`agent`、`deployment` 或 `unknown`，并且独立记录：Session 可以覆盖模型，同时保留其 Agent 的 Harness 和提供商。显式内联 Harness 的来源是 `session`；内联 `agent.x_agents_core: null` 会将 Harness 重置为 `deployment`，并保留继承的提供商；Session 提供者为 null 时会正常继承。没有任何原生参数适用时，`harness_config.value` 为 `{}`。[模型执行](model-execution.md)负责定义每个值的解析方式。

| `model_provider.status` | 含义 | `configuration` |
| --- | --- | --- |
| `available` | Core 记录了冻结提供商的安全视图，包括部署默认值（来源 `deployment`） | `protocol`、`base_url`、`api_key_configured`，以及在已设置时包含的 `context_window` 和 `max_output_tokens` |
| `redacted` | 记录了部署选择，但没有安全视图 | null |
| `unavailable` | Core 没有关于该提供商的可靠记录（来源 `unknown`）。执行仍可能已成功 | null |

Core 会在创建 Session 的同一事务中写入此记录。之后的 Agent 编辑或删除、部署默认模型更改、重启以及使用同一密钥重试创建都不会改变该记录。没有该记录的 Session 会报告其已存储的模型和 Harness，来源均为 `unknown`；未存储任何值时对应字段为 null；提供商为 `unavailable`。Session 不存在和 Session 属于另一个 Project 时返回相同的 404。响应绝不会包含密钥、密文、机密引用、原生标头或查询参数。

## 安装信息 {#installation-facts}

`GET /installation` 报告管理员调用和更改此安装实例所需的信息。即使尚未创建任何沙箱部署，该接口也会响应，并且不会调用任何提供商或模型。

| 字段 | 含义 |
| --- | --- |
| `object` | `core.installation` |
| `installation_id` | `state.json` 中的安装 ID（[安装目录](../../../docs/zh/configuration.md#installation-directory)）；Core 在不使用沙箱管理器运行时为 null |
| `public_url` | `public_url` 设置（[设置](../../../docs/zh/configuration.md#settings)）：应用程序、节点、沙箱和自托管执行器使用的源地址。未设置时为 null |
| `api_base_url` | 在 `public_url` 后附加 `/v1`，即 Project API 密钥使用的 `OPENAI_BASE_URL`。当 `public_url` 为 null 时为 null |
| `local_only` | 当 `public_url` 指向回环主机时为 True，该主机只能由 Core 主机访问 |
| `source_commit` | Core 构建所依据的完整源代码提交；开发构建为 null |
| `configuration` | Core 从环境加载的进程设置。`path` 和 `apply_command` 为空，`applied_at` 为 null |
| `address_bindings` | 更改 `public_url` 所影响的内容，每次读取都会重新统计 |

`configuration.settings` 为 Core 加载的每项设置一条记录，包含以点分隔的 `key`、生效的 `value`、`default`、是否 `changeable`、是否 `sensitive`，以及会 `restarts` 的服务（`core`、`web`、`database`）。

敏感设置的 `value` 和 `default` 为 null，并改为包含一个布尔值 `configured`；只有敏感设置具有 `configured`。`oac-core check-config` 校验同一组环境变量，然后退出，不启动 Core，也不打印值。[配置](../../../docs/zh/configuration.md)会说明每个设置。

| `address_bindings` 字段 | 含义 |
| --- | --- |
| `nodes` | 已注册且未移除的节点 |
| `nodes_on_other_address` | 使用了 `public_url` 以外地址注册的节点。它们不会获得新的沙箱；请移除后重新添加。最多为 `nodes` 个 |
| `hosted_sandboxes` | 保留的待处理托管沙箱；它们启动时使用当时有效的地址运行 |
| `self_hosted_executors` | 未吊销的执行器凭据；对应执行器安装时使用的是当时公布的 `remote_url` |

## 写入溯源 {#write-provenance}

Core 会记录是哪个 Project API 密钥完成了每次成功的公共写入；该记录与写入在同一事务中完成，如果记录失败，写入也会失败。读取、被拒绝的请求以及 Core 自身的维护操作（例如 OAuth 令牌刷新和清理）不会被记录。

- 写入在提交时才会被记录。Session 输入一经接纳即记录一次，包括为仍在准备中的 Environment 预留的输入；后续执行失败或响应丢失不会删除该记录。显式提交的空输入批次以及重复的创建或删除操作也会被记录，但不会改变所有权。
- Environment 文件上传会在 Runtime 确认写入时被记录。Core 会在发送文件前保存密钥、请求和追踪信息，并且只记录已确认的上传。
- 创建资源时也会记录其创建者。更新、重试和无效写入绝不会改变创建者。新 Skill 的第一个版本和新 Session 的 Environment 共享创建操作。Artifact 来自 Runtime，没有创建者；删除 Artifact 时会记录该操作。
- 吊销密钥会阻止新的写入，但会保留其历史。删除资源会保留其创建者和操作历史。
- 每条记录包含其 ID、时间、密钥元数据、操作、资源类型和 ID、父 ID、`request_id` 和 `trace_id`。`request_id` 由服务器按请求生成；共享的 `trace_id` 不是幂等密钥。记录绝不会包含请求或响应正文、机密、模型凭据、令牌、文件路径或文件内容。

| 公共写入 | `action` | `resource_type`（父级） |
| --- | --- | --- |
| Agent 创建、更新、删除 | `create`、`update`、`delete` | `agent` |
| Session 创建、更新、删除 | `create`、`update`、`delete` | `session` |
| Session 事件 | `send_events` | `session` |
| Artifact 删除 | `delete` | `artifact`（`session`） |
| Environment 文件上传 | `upload_file` | `environment`（`session`） |
| Environment Template 创建、更新、删除 | `create`、`update`、`delete` | `environment_template` |
| Skill 创建、默认版本更改、删除 | `create`、`update_default_version`、`delete` | `skill` |
| Skill 版本上传、删除 | `upload_version`、`delete` | `skill_version`（`skill`） |
| Source File 上传、删除 | `create`、`delete` | `file` |
| Vault 创建、删除 | `create`、`delete` | `vault` |
| Credential 创建、替换、删除（静态和 OAuth） | `create`、`update`、`delete` | `credential`（`vault`） |

上述两条路由仅接受列出的参数；未知、重复或空参数会返回 400，Project 不存在则返回 404。

`GET /projects/{project_id}/resource-owners?resource_type=agent&resource_ids=id1,id2` 按请求顺序返回同一类型下 1–100 个资源的创建密钥。`resource_type` 可以是 `agent`、`session`、`environment`、`environment_template`、`skill`、`skill_version`、`file`、`vault`、`credential` 或 `artifact`。

```json
{"data":[
  {"resource_id":"id1","api_key":{"id":"key-uuid","name":"SDK","prefix":"pc_example","kind":"issued","revoked_at":null},"source":"api_key","admin_audit_id":null},
  {"resource_id":"id2","api_key":null,"source":null,"admin_audit_id":null}
]}
```

当 Core 没有创建记录时，`api_key` 和 `source` 为 null，这包括另一个 Project 中的资源。带有 `admin_audit_id` 的 `source: "admin_copy"` 表示该资源由审计日志中的 `copy` 条目记录；当前没有路由会写入此类记录。

`GET /projects/{project_id}/write-operations` 按 `(created_at, id)` 从新到旧列出写入记录。过滤条件包括：`key_id`、`resource_type`、`resource_id`、包含起始时间的 `created_after` 和不包含结束时间的 `created_before`（RFC 3339）。`limit` 为 1–100，默认值为 50。在过滤条件不变的情况下，将上一个 `next_cursor` 作为 `after` 传入。响应为 `{data, has_more, next_cursor}`；每个条目包含 `id`、`created_at`、`api_key`、`action`、`resource_type`、`resource_id`、`parent_id`（不存在时为空）、`request_id` 和 `trace_id`。

创建记录会永久保留，即使资源已删除也一样。其他记录会按照 [`core.write_audit_retention`](../../../docs/zh/configuration.md#settings) 保留，默认期限为 90 天；Core 每分钟最多删除 1,000 条过期记录，因此积压记录需要经过多轮处理才能清空。吊销密钥或删除资源绝不会删除记录。

## 汇总 {#summary}

`GET /summary` 统计 Session 数量和使用情况。

| 参数 | 含义 |
| --- | --- |
| `project_id` | 一个 Project；`group_by=agent` 时必填 |
| `group_by` | `project`（默认）、`agent` 或 `key` |
| `created_after`、`created_before` | Session 创建时间的 RFC 3339 下限（含）和上限（不含） |
| `after`、`limit`、`order` | 对 Project 进行分页 |

响应为 `{data, has_more, next_cursor}`。每一行包含 `project_id`、可为 null 的 `agent_id` 和 `key_id`、当前 `assets` 计数（Agent 分组和密钥分组为 null）、`sessions` 计数（`total`、`idle`、`in_progress`、`requires_action`、`failed`）、汇总 `usage`、`coverage`（`measured_sessions`、`total_sessions`、可为 null 的 `ratio`）以及可为 null 的 Unix `last_active_at`。

- 在密钥分组中，每个 Session 都计入创建它的密钥，即使之后由另一个密钥发送输入也是如此。没有记录创建者的 Session 会组成一个 `key_id` 为 null 的分组。
- 公共用量为 null 的 Session 不会增加 token 数量，但仍会计入覆盖率分母。
- 每个 Project 都在单个数据库快照中读取；一页数据并不是整个部署的单一快照。总计属于运营计数，不是计费记录。

## Runtime 观测 {#runtime-observations}

[Runtime 遥测 API](runtime-observability-api.md)负责当前观测值、[仅列表可用的磁盘字段](runtime-observability-api.md#disk)、Session 历史以及节点主机观测值和历史。

## 审计日志 {#audit-log}

`GET /audit-log` 按从新到旧的顺序列出管理员写入。过滤条件包括 `project_id`、`resource_type`、`resource_id`、`action`、包含起始时间的 `created_after` 和不包含结束时间的 `created_before`（RFC 3339）。`limit` 为 1–100，默认值为 50，并使用不透明的 `after` 游标。响应为 `{data, has_more, next_cursor}`。

每个条目包含 `id`、`created_at`、`admin_credential_id`（Core 密钥摘要的前 8 个十六进制字符）、`actor_label`、`action`、`project_id`、`resource_type`、`resource_id`、`result_ids`、`request_id` 和 `trace_id`。除 `copy` 条目外，`result_ids` 都是空数组。部署范围条目为 `project_id: null`，使用 `project_id` 过滤时会排除这些条目。

| `resource_type` | `action` | `resource_id` |
| --- | --- | --- |
| `project` | `create`、`rename`、`archive` | Project ID |
| `api_key` | `create`、`revoke` | 密钥 ID |
| `executor_credential` | `issue`、`rotate`、`revoke` | 密钥 ID |
| `session` | `archive` | Session ID |
| `agent`、`environment_template`、`skill`、`skill_version`、`file`、`vault`、`credential`、`session`、`artifact` | `delete` | 资源 ID |
| `deployment_model_provider`（部署范围） | `set`、`delete` | Harness |
| `sandbox_deployment`（部署范围） | `change`、`replace_credential`（提交了提供商凭据，即使提交的是同一个凭据）、`reset_start`、`reset_force`、`reset_deadline`、`reset_cancel`、`reset_complete` | 安装 ID |

管理员写入及其审计条目会在一个事务中提交；如果条目写入失败，写入也会失败。重置在后台执行的归档操作及其 `reset_deadline` 和 `reset_complete` 条目会携带请求方的凭据、操作者标签、请求和追踪信息，并且每个归档操作都会保留其 Session 的 Project。取消重置不会撤销已经提交的归档。被拒绝的提供商验证和无效更新不会写入条目。条目绝不会包含凭据值、请求正文或提供商响应文本，并且会在其资源被删除和密钥被吊销后继续保留。
