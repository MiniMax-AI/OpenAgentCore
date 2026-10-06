---
title: "Environment 执行器凭证"
source: contracts/agents-api/environment-executor-credentials.md
source_hash: 6c1db305481f9ab2a51bdd0c88243feaab48348b71f5f3ebbc8f00b17744f412
---

执行器凭证允许 `oac-daemon` 为一个 `self_hosted` Environment 注册并连接。它只授权该 Environment 的私有 daemon 传输（`/api/v1/agent-daemon/*`），不授权 `/v1`、`/core/v1`、sandbox node 注册或 Project 资源。Project 的 principal 是其执行 principal。Core 只保存密钥摘要。

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

无效或过期的 grant 返回 401 `installation_authorization_invalid`。没有匹配安装器时，grant 路由返回 503 `installation_unavailable`。Core 用安装的 [`secrets/credential.key`](../../../docs/zh/configuration.md#installation-directory) 签名每个 grant；未配置 key 时，上述 Session 响应、Core-key 查询和 grant 路由返回 503 `credential_storage_unavailable`。格式错误的密钥返回 400。产物路由不携带凭证，grant 只发送给 Core，不发送给产物主机。

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

轮换替换限定于该 Environment 的现有 key 密钥，保留 Environment，立即使旧密钥失效，并恢复已撤销的 key。撤销是幂等操作，每次返回 204，禁止后续注册和连接。

超时等结果不确定的情况下，不要自动重试。先列出凭证，再轮换同一 `key_id`（已签发但密钥丢失），或重新签发（尚未签发）。

签发、轮换和撤销在写入的同一事务中记录管理员审计项：`resource_type:"executor_credential"`，key ID 为 `resource_id`，action 为 `issue`、`rotate` 或 `revoke`。审计不包含密钥。

### 应急命令 {#break-glass-command}

Core API 不可用时，`oac-core-environment-key` 直接在数据库中签发、轮换或撤销凭证。它读取 Core 数据库设置（`OAC_DATABASE_URL`，以及配置时的 `OAC_DATABASE_PASSWORD_FILE`），并需要 Project 的执行 principal：`--tenant`（`projects` 表中 Project 的 tenant UUID）、`--organization core`、`--project proj_<Project UUID>`、`--subject-kind service_account`、`--subject-id project:<Project UUID>` 和 `--key-id`。没有其他标志时签发新凭证，`--environment` 限定到一个 Environment。`--rotate` 替换现有凭证的密钥，包括已撤销凭证；`--revoke` 撤销而不输出密钥。轮换和撤销保留存储的限制，拒绝 `--environment`，两个标志互斥。签发和轮换只在标准输出打印一次凭证文件，应重定向到新建的 0600 文件。命令绕过 Core API，跳过 Project 归档检查且不写审计项，因此 Core 运行时应使用 Core-key 路由。没有 Environment 限制的凭证不能通过上述路由管理。

## 连接状态 {#connection-status}

列表必需的 `connection` 对象包含 `status`（`never_enrolled`、`connected` 或 `disconnected`）、`bound_key_id`、`enrolled_at` 和 `last_seen_at`。注册前，三个绑定字段均为 null。注册后，绑定 key 和注册时间描述已有设备；`last_seen_at` 为 null 表示尚无经过认证的心跳。签发另一 key 不改变绑定。轮换或撤销可能使绑定断开，但历史仍可见。过期 Environment 仍按现有列表规则可读，但不能拥有当前执行器权限。

Connected 表示 Environment 已连接，其设备和执行器 key 仍有当前 Core 权限，且进程内 gateway 有一个用当前 key 认证的开放 peer。Core 观察 peer 后重新检查权限。旧 key 的存活 socket、设备时间戳或看似就绪的 Environment 均不充分；没有 gateway 时 Core 不返回 connected。这些事实是观测结果，不预留连接，也不保证原生执行或模型就绪。`last_seen_at` 可能滞后一个心跳间隔。

列表元数据和绑定事实使用同一个只读数据库快照。快照在实时权限检查前结束，因此已提交的轮换或撤销不会被快照隔离隐藏。已知权限丢失映射为 disconnected；观测和存储失败仍返回错误。设备 ID 和凭证摘要为内部数据，不序列化。公开 `/v1` Environment 形状不变。

### 私有连接确认 {#private-connection-confirmation}

`GET /api/v1/agent-daemon/connection?environment_id=UUID` 使用执行器 bearer，直接发往 Core（反向代理将 `/api/v1` 路由到 Core，console 不提供该接口）。它属于私有 daemon 传输，不属于公开 Agents API。它只读取现有授权和绑定，不注册设备、启动执行或修改资源。no-store 响应只包含请求的 `environment_id` 和 `status`（`connected` 或 `disconnected`）。Connected 要求 Environment 观测、确切的 Session 和设备绑定、当前执行器权限，以及用相同凭证认证的存活 gateway socket。过期观测或携带已轮换 key 的 socket 不能确认连接。

无效、已撤销、其他范围或已删除 Session 的权限返回 401；已绑定 Environment 使用不同 key 返回 409。响应不暴露实际绑定或数据库诊断。

安装器从返回的 `remote_url` 推导此路由，不跟随重定向。启动 daemon 后，每秒轮询一次，最多 45 秒。401 或 409 立即失败。超时后打印 daemon 的 `connect.log` 路径，请求重新执行同一命令；daemon 继续重连，安装、凭证和历史保留。连接确认只证明认证成功，不证明模型访问、Harness 能力或执行完成。

## 已撤销或轮换的凭证 {#revoked-or-rotated-credential}

Core 永久拒绝 daemon 时（注册 401/409、永久 WebSocket 拒绝，或 daemon 版本来自其他 Core 分发），daemon 只打印一次原因，停止发请求直到被停止；随后成功退出，避免按退出重启的 supervisor 循环。再次启动时只尝试一次注册，然后再次停驻。临时故障保持正常重连行为，不重放执行。

机器只能使用绑定的 `key_id` 重连，轮换新密钥后替换配置路径的凭证文件；[自托管指南](../../../docs/zh/getting-started/self-hosted.md#rotate-or-revoke)提供步骤。新 `key_id` 无法重新连接已绑定的 Environment：签发成功，但用它注册返回 409。轮换不重装 Harness、不改变工作区、不替换原生历史；不要为恢复凭证创建新 Session 历史。

## 模型服务 {#model-provider}

`self_hosted` Session 携带自己的模型服务，部署默认值不适用。[模型执行](model-execution.md)负责交付规则。保存的 Agent 的服务 key 会交给 Project 中用该 Agent 创建的每个 `self_hosted` Session 的执行器，因此任何能在该 Project 创建 `self_hosted` Session 并运行执行器的人都能读取它。
