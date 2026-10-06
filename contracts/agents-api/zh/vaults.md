---
title: "Vault 与 Credential"
source: contracts/agents-api/vaults.md
source_hash: 9e56ee84c782d1f9c0f5506f960a275d9afa3e554634131bf09a74e736135926
---

Vault 是 Project 所有的 Credential 容器。Credential 保存一个 HTTPS MCP server 的秘密：`static_bearer` token 或 `mcp_oauth` grant。Session 在 `vault_ids` 中关联 Vault；Core 在创建 Session 时为每个 HTTP MCP server 选择一个 Credential，只在分派工作时将解密 token 交给 Runtime。秘密只能写入：任何读取都不返回 token、refresh token、client secret 或密文。

应用负责 OAuth 授权与同意、服务方撤销以及审批策略。Core 不提供授权重定向、回调、撤销或公开刷新端点；创建或替换 Credential 不联系 MCP server 或 OAuth provider。

## 存储并使用凭证 {#store-and-use-a-credential}

```python
vault = client.beta.agents.vaults.create(name="internal")
credential = client.beta.agents.vaults.credentials.create(
    vault.id,
    name="Internal MCP",
    auth={
        "type": "static_bearer",
        "mcp_server_url": "https://mcp.example.com/endpoint",
        "token": token_from_private_configuration,
    },
)
session = client.beta.agents.sessions.create(
    environment={"type": "none"},
    vault_ids=[vault.id],
    input="List the open incidents.",
    agent={
        "model": "your-model-id",
        "tools": [{
            "type": "mcp",
            "server_label": "internal",
            "transport": {"type": "http", "server_url": "https://mcp.example.com/endpoint"},
        }],
    },
)
```

MCP tool 可以通过 `credential_id` 指定 Credential；未指定时，Core 选择已关联 Credential 中 `mcp_server_url` 等于 tool 的 `server_url` 的凭证（[选择规则](#credential-selection-in-a-session)）。能够连接该 server 的 Harness 和 placement 取决于 tool 的 `connection_origin`（[MCP 连接来源](environments.md#public-mcp-connection-origin)）。

## 路由 {#routes}

所有路由位于 `/v1`，使用 Project API key，要求 `OpenAI-Beta: agents=v1`。Project 的 key 共享其 Vault；其他 Project 的 Vault 或 Credential 与不存在的资源一样返回 404。

| 操作 | 路由 | 结果 |
| --- | --- | --- |
| 创建 Vault | `POST /vaults` | 201 Vault |
| 查询 Vault | `GET /vaults/{vault_id}` | Vault |
| 列出 Vault | `GET /vaults` | Vault 列表 |
| 删除 Vault | `DELETE /vaults/{vault_id}` | `{id, object: "vault.deleted", deleted: true}` |
| 创建 Credential | `POST /vaults/{vault_id}/credentials` | 201 Credential |
| 查询 Credential | `GET /vaults/{vault_id}/credentials/{credential_id}` | Credential |
| 列出 Credential | `GET /vaults/{vault_id}/credentials` | Credential 列表 |
| 替换秘密 | `POST /vaults/{vault_id}/credentials/{credential_id}` | Credential |
| 删除 Credential | `DELETE /vaults/{vault_id}/credentials/{credential_id}` | `{id, object: "vault.credential.deleted", deleted: true}` |

格式错误、缺失或其他 Project 的 ID 返回 404 `not_found_error`；通过非所属 Vault 访问 Credential 也一样。两个列表按创建时间、再按 ID 排序，按 `status`（`active`、`archived` 或二者）筛选；[列表规则](wire-semantics.md#lists)说明分页和筛选细节。Core 私有保存 status，默认 `active`，没有归档 Vault 或 Credential 的操作。Credential 的 status 独立于 Vault。

## Vault {#vaults}

| 字段 | 规则 |
| --- | --- |
| `name` | 可选；省略保持 `null`，显式 `null` 被拒绝。字符串去除首尾空白后必须为 1–256 UTF-8 字节 |
| `metadata` | 省略或 `null` 转为 `{}`。值必须为字符串；其他类型返回 400 `invalid_request_error`，param 为 `metadata.<key>`。编码后对象上限 64 KiB，无键值对数量或长度限制 |

Vault 读取包含 `id`、`object: "vault"`、`created_at`、`name` 和 `metadata`。没有更新路由。

删除 Vault 在一个事务内移除 Vault 及所有 Credential，无论它们的 status。无需 storage key，不向服务方发送请求。对 Session 的影响见[删除](#deletion)。

## Credential {#credentials}

创建需要 `name`（去除首尾空白后 1–256 UTF-8 字节）和 `auth` 对象，`type` 为 `static_bearer` 或 `mcp_oauth`。`mcp_server_url` 必须是无 userinfo、无 fragment 的绝对 HTTPS URL；Core 逐字节保留，包括 query，不执行 DNS 或 HTTP 请求。

Credential 读取包含 `id`、`vault_id`、`name`、`object: "vault.credential"`、`created_at`、`updated_at` 和 `auth`。`auth` 包含 `type`、`mcp_server_url`；OAuth Credential 还包含 `expires_at` 和 `refresh`，后者包含 `client_id`、`token_endpoint`、`token_endpoint_auth.type`、`resource` 和 `scope`。读取和列表无需 storage key。

### 静态 bearer {#static-bearer}

`auth` 为 `{type: "static_bearer", mcp_server_url, token}`。`token` 必需且非空。Core 将它作为不透明字节保存，不去除空白。执行时 token 必须符合 RFC 6750 `b64token`；Session 选择了含空白等其他字符的存储 token 时，分派失败。

通过 `POST /vaults/{vault_id}/credentials/{credential_id}` 替换 token，请求必须正好为 `{"auth": {"type": "static_bearer", "token": "…"}}`。token 缺失、null、空值或任何其他字段均被拒绝。仅 token 和 `updated_at` 改变；ID、Vault、name、type、`mcp_server_url`、`created_at` 和所有 Session 绑定保留。替换失败保留旧 token。

### OAuth {#oauth}

`auth` 为 `{type: "mcp_oauth", mcp_server_url, access_token, expires_at, refresh}`：

| 字段 | 规则 |
| --- | --- |
| `access_token` | 必需，非空 |
| `expires_at` | 可选、可为 null 的 RFC 3339 时间戳。创建时允许已过期值 |
| `refresh` | 可选、可为 null；`client_id`、`refresh_token`、HTTPS `token_endpoint` 和 `token_endpoint_auth` 必需；`resource` 和 `scope` 为可选、可为 null 的字符串 |
| `refresh.token_endpoint_auth` | `{type: "none"}`，不含 `client_secret` 成员；或 `client_secret_basic` / `client_secret_post`，含只写 `client_secret` |

使用同一更新路由、`auth.type: "mcp_oauth"` 替换 grant 材料。patch 必须至少修改 `access_token`、`expires_at`、`refresh.refresh_token`、`refresh.token_endpoint_auth.client_secret` 或 `refresh.scope` 之一；空 `access_token` 被拒绝。ID、name、type、`mcp_server_url`、`client_id`、`token_endpoint`、`resource` 和端点认证方法不变；创建时没有 `refresh` 的 Credential 不能添加该块。提交 `token_endpoint_auth` 时必须指定已存储的方法，且为 `client_secret_basic` 或 `client_secret_post`。

| 更新字段 | 省略 | `null` |
| --- | --- | --- |
| `access_token` | 保留 | 保留 |
| `expires_at` | 保留；发送新 `access_token` 时清空 | 清空 |
| `refresh` | 保留 | 保留 |
| `refresh.refresh_token` | 保留 | 保留 |
| `refresh.scope` | 保留 | 停止发送 scope |
| `refresh.token_endpoint_auth` | 保留 | 保留 |
| `refresh.token_endpoint_auth.client_secret` | 保留 | 保留 |

更新时改变 Credential 的 `auth.type` 返回 400。

## Session 中的凭证选择 {#credential-selection-in-a-session}

创建 Session 时，`vault_ids` 列出 Session 可以使用凭证的 Vault。省略、`null` 和 `[]` 均不关联任何 Vault；条目为 `null` 无效；每个 Vault 必须属于 Project，否则创建返回 404 `not_found_error`。Agent 上保存 `credential_id` 不提供授权；只有 Session 的关联提供授权。

创建 Session 时，Core 为每个 HTTP MCP tool 从已关联 Vault 中选择 Credential：

- 指定 `credential_id` 时，该 Credential 必须在已关联 Vault 内，且 `mcp_server_url` 等于 tool 的 `server_url`。
- 未指定时（省略或 `null`），选择唯一一个 `mcp_server_url` 等于 `server_url` 的静态或 OAuth Credential。没有匹配时匿名连接；多个匹配时创建失败，不偏好任何类型。

选择发生在内联 Agent 校验和 input 要求之后、任何写入之前。创建被拒绝时不写入任何内容。失败的 `param` 为 null：

| 情况 | 响应 |
| --- | --- |
| `credential_id` 没有关联 Vault | 400 `invalid_request_error`: `MCP credential_id requires an attached vault` |
| `credential_id` 缺失、格式错误、属于其他 Project 或未关联 Vault | 400 `invalid_request_error`: `MCP credential_id <id> was not found in an attached vault` |
| Credential 在已关联 Vault 内，但 URL 不同 | 400 `invalid_request_error`: `MCP credential_id <id> does not match server_url <url>` |
| 未指定 `credential_id` 时匹配多个 Credential | 409 `conflict_error`: `multiple attached vault credentials match MCP server_url <url>; specify credential_id` |
| `vault_ids` 中的 Vault 未知或属于其他 Project | 404 `not_found_error` |

`<id>` 和 `<url>` 仅在请求值不超过 256 字节且为可打印 UTF-8 时原样返回，否则消息省略它们。缺失、其他范围、未关联和格式错误的情况，对同一 ID 给出逐字节相同响应，因此引用不泄露调用方未关联 Vault 的信息。URL 不匹配消息中的 URL 来自 tool。

Session 固定其关联和每次选择，包括匿名选择。后续 Vault 变化不会重新选择：相同的创建重试返回原 Session 和选择。Session 查询、列表和事件快照在 tool 的 `credential_id` 中显示隐式选择的 Credential ID，即使该 Credential 已删除；匿名 tool 显示 `null`，显式 ID 按提交值读取。存储请求保留调用方值，因此重试比较原请求。

每次分派时，Core 重新检查 Project、已关联 Vault、选中 Credential、type 和确切 URL，然后只在 Runtime 执行请求中解密 token。Session、配置和历史的读取从不包含 token。选中 Credential 的 server 只能运行在声明 `mcp_http_bearer_auth` 的 Runtime 上。Credential 缺失、解密失败或刷新失败会使工作失败；Core 不退回其他 Credential 或匿名连接。

## OAuth 刷新 {#oauth-refresh}

分派时，OAuth Credential 的 `expires_at` 已过期，则在 Core 将工作发送给 Runtime 前刷新。`expires_at` 为 null 的 grant 原样使用，不主动刷新。已过期且没有 `refresh` 的 grant 使工作失败。

刷新只执行一次 `refresh_token` 交换，使用存储的端点认证方法、`scope` 和 `resource`；不跟随重定向、不探测方法、失败后不重试。服务方必须返回 `Bearer` token，其到期时间（若有）必须在未来。Core 先提交新 access token、新到期时间（或无到期时间）和轮换后的 refresh token，再使用 token；省略 refresh token 时保留旧值。PostgreSQL 行锁使刷新与手动替换、删除串行，避免过期刷新恢复已删除或替换的 grant。交换或提交失败时不返回任何内容，也不重试。服务方轮换 grant 后本地提交失败时，应用必须重新授权。服务方错误响应体不返回也不写日志；Harness 只收到 access token，不收到 refresh token 或 client secret。

Token endpoint 必须为 HTTPS。Core 解析主机，拒绝回环、私有、链路本地和共享（`100.64.0.0/10`）地址，连接已检查地址以防第二次 DNS 响应改变目标；TLS 仍验证主机名。它忽略环境中的 HTTP proxy。对于私有 issuer，管理员在 [`core.oauth_trusted_origins`](../../../docs/zh/configuration.md#settings) 中列出确切 HTTPS origin；这只允许该主机与端口使用私有地址，不允许 HTTP、重定向或无效证书。issuer 的 CA 必须在 Core 信任库中。

## 存储密钥 {#storage-key}

Core 使用安装的 [`secrets/credential.key`](../../../docs/zh/configuration.md#installation-directory)，以 AES-256-GCM 加密每个 token、refresh token 和 client secret，绑定 Project、Vault、Credential、auth type 和 `mcp_server_url`。错误密钥、修改的行或移动到其他绑定的行均无法解密。名称是不参与绑定的元数据。key 和明文 token 存在于可信服务内存中；加密保护存储的秘密，不保护已被攻破的服务主机。

未配置 key 时，Credential 创建和替换在写入前返回 503 `credential_storage_unavailable`；读取、列表、删除和 Vault 操作仍可用。key 文件不可读或格式错误会使 Core 启动失败。丢失或替换 key 使全部已存储秘密无法使用；Core 只支持一个 key，不支持轮换或重新加密。

## 删除 {#deletion}

删除 Credential 或其 Vault 会移除凭证行，但不会擦除 PostgreSQL 页面、WAL、备份或原生历史中的秘密。此后查询、更新和重复删除返回 404；列表不再包含它；新 Session 不能选择它；不能在已删除 Vault 中创建新 Credential（缺失 storage key 时仍先返回 503）。

现有 Session 保留固定的关联和选择，历史仍可读。下一次需要已删除 Credential 的分派失败；Core 不选择其他 Credential，也不匿名连接。删除不取消运行中的工作、不收回已发送 Runtime 的 token，也不在服务方撤销 grant；需要时自行取消 Session 并撤销 grant。已撤销或无效 OAuth grant 同样失败，直到被替换。
