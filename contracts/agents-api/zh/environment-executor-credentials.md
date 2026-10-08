---
title: "Environment 执行器凭证"
source: contracts/agents-api/environment-executor-credentials.md
source_hash: b1e07476ee5307bb57f58a94547a9ebde1cf377aeb456117c150632108435f7e
---

执行器凭证允许 `oac-daemon` 为一个 `self_hosted` Environment 注册，并通过 [sandbox Link](../../../docs/zh/sandbox-link-protocol.md) 为它提供服务。它只授权该 Environment 的私有 daemon 路由（`/api/v1/agent-daemon/*`），以及注册后在 Link 上 Serve 该 Environment 的 enrollment resource，不授权 `/v1`、`/core/v1`、sandbox node 注册或 Project 资源。Project 的 principal 是其执行 principal。Core 只保存密钥摘要。

凭证有两个来源：

- **安装授权。** `self_hosted` Session 返回安装命令。安装器使用命令中的短期 grant 领取一个凭证，不需要 Web 或 Core key。[自托管指南](../../../docs/zh/getting-started/self-hosted.md)介绍操作步骤。
- **Core-key 路由。** 管理员通过 Web 或 `/core/v1` 签发、轮换和撤销凭证。

Core 不创建、停止或回收机器。断开连接、撤销凭证或删除 Session 都不能证明所有原生进程已停止；机器所有者负责停止并清理自己的计算资源。

## 安装授权 {#installation-grant}

`self_hosted` Session 的创建、查询和更新响应包含 `x_agents_core.installation`，Session 列表不包含。Web 使用 Core key 通过 `GET /core/v1/projects/{project_id}/environments/{environment_id}/installation` 读取同一对象，原样显示命令。

| 字段 | 含义 |
| --- | --- |
| `status` | `available`；当 Core 没有匹配的原生安装器时为 `unavailable`，此时 `message` 说明原因 |
| `version` | 命令安装的 Core 构建版本 |
| `expires_at` | grant 到期的 Unix 时间，为响应生成后 30 分钟 |
| `commands.posix`, `commands.powershell` | Linux/macOS 和 Windows PowerShell 的安装命令 |

grant 绑定 Environment、Session 创建者的 principal 和 Core 构建版本。在到期、Session 被删除、Project 被归档或 Core 运行另一构建版本时失效。重新读取 Session 会获得新 grant。Core 不存储 grant：每个响应重新签名，存储的事件从不包含它。将命令视为临时秘密：它能领取凭证，但不能执行工作或读取文件。

安装器生成密钥，在领取前将其私密保存到安装目录的 `daemon/executor-credential.json`。Core 将摘要保存在 key ID 等于 Environment ID 的记录中。响应丢失后可以安全重试，但必须提交同一个密钥。grant 不替换或恢复凭证。如果 Environment 已有不同、已轮换或已撤销的凭证，领取返回 409 `executor_credential_exists`。

安装器调用 Core 的以下机器路由：

| 路由 | 授权 | 用途 |
| --- | --- | --- |
| `GET /api/v1/agent-daemon/install/{version}/bootstrap.sh`, `bootstrap.ps1` | 无 | 平台 bootstrap 脚本 |
| `GET /api/v1/agent-daemon/install/{version}/{os}-{arch}.sha256`, `{os}-{arch}.tar.gz` | 无 | 安装器校验和与归档；Core 提供本地副本，或以 307 重定向到目录中的版本化发布 URL |
| `POST /api/v1/agent-daemon/installation` | Grant | 固定绑定：`version`、`protocol_version`、`environment_id`、`remote_url`、`workspace_directory`、`harness` |
| `POST /api/v1/agent-daemon/installation/claim` | Grant | `{"executor_token":"SECRET"}`；204 |

无效或过期的 grant 返回 401 `installation_authorization_invalid`。没有匹配安装器时，grant 路由返回 503 `installation_unavailable`。Core 用安装的 [`secrets/core/credential.key`](../../../docs/zh/configuration.md#compose-installations) 签名每个 grant。格式错误的密钥返回 400。产物路由不携带凭证，grant 只发送给 Core，不发送给产物主机。

## Core-key 路由 {#core-key-routes}

所有路由位于 `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials`，要求 Core key。它们仅适用于该 Project 内 Session 仍存在（未删除）的 `self_hosted` Environment；其他 Project、Environment 类型、缺失 Environment 或已删除 Session 均返回 404。Project API key 无权调用。

| 操作 | 请求 | 结果 |
| --- | --- | --- |
| 列表 | `GET …/executor-credentials` | `data` 中的凭证元数据，以及必需的 `connection` 对象 |
| 签发或轮换 | `POST …/executor-credentials`，内容为 `{"key_id":"UUID","rotate":false}` | 201 凭证文件，只返回一次 |
| 撤销 | `DELETE …/executor-credentials/{key_id}` | 204 |

列表只包含限定于此 Environment 的凭证元数据，按最早创建优先排序。有效凭证的 `revoked_at` 为 null。列表不包含密钥。

`key_id` 是请求前选定并保留的规范非零 UUID。`rotate` 可选，默认 false。201 响应使用 daemon 凭证文件格式：

```json
{"key_id":"UUID","environment_id":"ENVIRONMENT_UUID","executor_token":"ONE_TIME_SECRET"}
```

响应使用 `Cache-Control: no-store`。直接保存到自己拥有、权限为 0600 的文件，不放入 shell 参数、日志、工作区或源码。

写入有两种 409 冲突。`executor_credential_exists`：签发时 `key_id` 已存在且没有 `rotate:true`，即使已撤销也一样。`project_archived`：Project 已归档，不能签发或轮换凭证；列表和撤销仍可用，因为必须始终能够撤销。

签发或轮换按以下顺序检查，返回第一个失败：请求体（400）；目标 Environment（404）；已归档 Project（409 `project_archived`）；key 本身（未设置 `rotate` 时为 409 `executor_credential_exists`，轮换从未签发的 `key_id` 时为 404）。

轮换替换限定于该 Environment 的现有 key 密钥，保留 Environment 和 `key_id`，立即使旧密钥失效，并恢复已撤销的 key。它还会推进该 Environment 的 enrollment generation 及其 Session 分配的 epoch，因此机器必须用新密钥重新注册后才能再次提供服务，正在运行的 Turn 也会失去该 Environment（[Session 分配](../../../docs/zh/runtime-protocol.md#session-assignments)）。撤销是幂等操作，每次返回 204，禁止后续注册，并关闭机器的 Serve。

超时等结果不确定的情况下，不要自动重试。先列出凭证，再轮换同一 `key_id`（已签发但密钥丢失），或重新签发（尚未签发）。

签发、轮换和撤销在写入的同一事务中记录管理员审计项：`resource_type:"executor_credential"`，key ID 为 `resource_id`，action 为 `issue`、`rotate` 或 `revoke`。审计不包含密钥。

### 应急命令 {#break-glass-command}

Core API 不可用时，`oac-core-environment-key` 直接在数据库中签发、轮换或撤销凭证。它读取 Core 数据库设置（`OAC_DATABASE_URL`，以及配置时的 `OAC_DATABASE_PASSWORD_FILE`），并需要 Project 的执行 principal：`--tenant`（`projects` 表中 Project 的 tenant UUID）、`--organization core`、`--project proj_<Project UUID>`、`--subject-kind service_account`、`--subject-id project:<Project UUID>` 和 `--key-id`。没有其他标志时签发新凭证，`--environment` 限定到一个 Environment。`--rotate` 替换现有凭证的密钥，包括已撤销凭证；`--revoke` 撤销而不输出密钥。轮换和撤销保留存储的限制，拒绝 `--environment`，两个标志互斥。签发和轮换只在标准输出打印一次凭证文件，应重定向到新建的 0600 文件。命令绕过 Core API，跳过 Project 归档检查且不写审计项，因此 Core 运行时应使用 Core-key 路由。没有 Environment 限制的凭证不能通过上述路由管理。

## 连接状态 {#connection-status}

列表必需的 `connection` 对象包含 `status`（`never_enrolled`、`connected` 或 `disconnected`）、`bound_key_id` 和 `enrolled_at`。注册前，两个绑定字段均为 null。注册后，`bound_key_id` 是为该 Environment 注册的 key，`enrolled_at` 是首次注册的时间；两者都不表示就绪。签发另一 key 不改变绑定。轮换或撤销可能使绑定断开，但历史仍可见。过期 Environment 仍按现有列表规则可读，但不能拥有当前执行器权限。

Connected 表示已注册的 key 仍有当前 Core 权限，且 Environment 的 live Link resource 正是该 key 的 enrollment，并以当前 generation Serve：relay 持有机器上 Sandbox I/O 服务的 serve peer。Core 观察 serve peer 后重新检查权限，因此仍用旧密钥提供服务的 peer 不算已连接。该规则与托管 Environment 相同（[就绪事实](../../../docs/zh/sandbox-provider.md#four-distinct-readiness-facts)）。它是观测结果，不预留连接，也不保证原生执行或模型就绪。

列表元数据和绑定事实使用同一个只读数据库快照。快照在实时权限检查前结束，因此已提交的轮换或撤销不会被快照隔离隐藏。已知权限丢失映射为 disconnected；观测和存储失败仍返回错误。Enrollment ID 和凭证摘要为内部数据，不序列化。公开 `/v1` Environment 形状不变。

### 私有连接确认 {#private-connection-confirmation}

`GET /api/v1/agent-daemon/connection?environment_id=UUID` 使用执行器 bearer，直接发往 Core（反向代理将 `/api/v1` 路由到 Core，console 不提供该接口）。它属于私有 daemon 传输，不属于公开 Agents API。它只读取现有授权和注册，不注册、不启动执行，也不修改资源。no-store 响应只包含请求的 `environment_id` 和 `status`（`connected` 或 `disconnected`）。`connected` 对所出示的凭证遵循[连接状态](#connection-status)规则：Environment 的 live Link resource 是同一凭证的 enrollment，且正在 Serve。过期观测或使用已轮换密钥的 serve peer 不能确认连接。

无效、已撤销、其他范围或已删除 Session 的权限，以及失败或已过期的 Environment，返回 401；已注册 Environment 使用不同 key 返回 409。响应不暴露实际绑定或数据库诊断。

安装器从返回的 `remote_url` 推导此路由，不跟随重定向。启动 daemon 后，每秒轮询一次，最多 45 秒。401 或 409 立即失败。超时后打印 daemon 的 `connect.log` 路径，请求重新执行同一命令；daemon 继续重连，安装、凭证和历史保留。连接确认证明机器正在为该 Environment 提供服务，不证明模型访问、Harness 能力或执行完成。

## 已撤销或轮换的凭证 {#revoked-or-rotated-credential}

Core 永久拒绝机器注册时（401 或 409），daemon 只打印一次原因，停止发请求直到被停止；随后成功退出，避免按退出重启的 supervisor 循环。再次启动时只尝试一次注册，然后再次停驻。临时故障（包括 Sandbox I/O 服务退出）按退避重试，不重放执行。

机器只能使用绑定的 `key_id` 重连，轮换新密钥后替换配置路径的凭证文件；[自托管指南](../../../docs/zh/getting-started/self-hosted.md#rotate-or-revoke)提供步骤。新 `key_id` 无法重新连接已绑定的 Environment：签发成功，但用它注册返回 409。轮换不改变工作区或 Session 的原生历史；不要为恢复凭证创建新 Session。
