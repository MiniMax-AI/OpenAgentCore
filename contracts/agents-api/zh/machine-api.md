---
title: "机器连接 API"
source: contracts/agents-api/machine-api.md
source_hash: 08e8e034ebe1e5a4c70db9143d35f08ceec071701d16e97480220c4ec0ece19f
---

机器通过 `/api/v1` 调用 Core：包括沙箱节点、Runtime daemon、Sandbox I/O 服务和自托管安装器。各路由仅接受所列凭据，不接受 Core 密钥或 Project API 密钥；控制台登录也不授予此处权限。公共源站将 `/api/v1` 转发给 Core，可以直接转发，也可以经过 Web 不改变请求的 HTTP 和 WebSocket 代理。Web 不会给机器请求添加控制台权限。

## 路由 {#routes}

| 路由 | 调用方 | 凭据 | 契约 |
| --- | --- | --- | --- |
| `GET sandbox-node/configuration` | 节点安装器与节点 | 登记 token，或节点凭据加 `X-OAC-Node-ID` | [读取节点配置](#read-the-node-configuration) |
| `POST sandbox-node/enroll` | 节点安装器 | 登记 token | [登记节点](#enroll-a-node) |
| `GET sandbox-node/identity?node_id=` | 节点 | 节点凭据 | [恢复节点身份](#recover-a-node-s-identity) |
| WebSocket `GET sandbox-node/connect?node_id=` | 节点 | 节点凭据 | [节点代际协议](node-generation-protocol.md) |
| `GET` / `HEAD agent-daemon/install/{version}/…` | 自托管安装器 | 无 | [安装授权](environment-executor-credentials.md#installation-grant) |
| `POST agent-daemon/installation`, `POST agent-daemon/installation/claim` | 自托管安装器 | 安装授权 | [安装授权](environment-executor-credentials.md#installation-grant) |
| `POST agent-daemon/enroll` | 自托管 daemon | 执行器凭据 | [登记自托管 daemon](#enroll-a-self-hosted-daemon) |
| `GET agent-daemon/connection?environment_id=` | 自托管安装器 | 执行器凭据 | [私有连接确认](environment-executor-credentials.md#private-connection-confirmation) |
| `POST agent-daemon/bootstrap` | Agent-host Runtime | Agent-host 凭据 | [daemon 引导](#daemon-bootstrap) |
| WebSocket `GET agent-daemon/ws?device_id=&version=` | Agent-host Runtime | Agent-host 凭据 | [Core–Runtime 协议](../../../docs/zh/runtime-protocol.md) |
| WebSocket `GET sandbox-link` | Sandbox I/O 服务（serve peer）和 agent-host Runtime（attach peer） | 资源的 Serve 凭据或 agent host 的 Runtime 凭据，在 Link Hello 中发送 | [Sandbox link 协议](../../../docs/zh/sandbox-link-protocol.md) |

所有凭据通过 `Authorization: Bearer` 头传输；`sandbox-link` 例外，各 peer 在升级之后的 Link Hello 中发送凭据。凭据从不放入 URL。Core 从 `OAC_PUBLIC_URL` 派生 [Link URL](../../../docs/zh/configuration.md#changing-the-public-url)。

生成的 [`runtime.openapi.yaml`](../runtime.openapi.yaml) 描述全部机器 HTTP 操作，包括公共下载和 WebSocket 握手。WebSocket 消息仍由链接的 wire 协议定义。

### HTTP 错误与方法 {#http-errors-and-methods}

所有 HTTP 错误使用 `{"error":{"message":"…","type":"invalid_request_error","code":null,"param":null}}`。`code` 在存在时携带路由定义的原因，`param` 在存在时标识被拒绝字段。409 的类型为 `conflict_error`，5xx 的类型为 `server_error`。升级前的握手失败也使用该封装；升级后的错误属于 wire 协议。调用方使用 HTTP 状态决定重试或永久拒绝，也可显示 `error.message`。

`HEAD` 不打开连接，也不查询 Runtime：daemon WebSocket、Link 和连接观察路由以 405 拒绝；节点连接对所有非 GET 方法返回 503。节点配置与身份读取支持 HEAD，并执行与 GET 相同的凭据检查。安装器下载支持 GET 和 HEAD，包括归档的条件请求与范围响应；下载错误也使用共享封装。登记只接受 POST。不支持的方法保留路由的状态及 `Allow` 头。裸 `/api/v1/agent-daemon` 和 `/api/v1/agent-daemon/install` 前缀以 301 重定向到带尾斜杠的形式，并保留查询。未知机器路由返回使用共享封装的 404。

## 凭据 {#credentials}

| 凭据 | 签发方 | 接受位置 |
| --- | --- | --- |
| 登记 token | `POST /core/v1/sandbox/enrollment-tokens`（Web **Add node**），带节点批准容量。使用一次；在响应 `expires_at` 过期 | 无节点 ID 的 `sandbox-node/configuration`、`sandbox-node/enroll` |
| 节点凭据 | 节点自身：生成 32 至 256 个无空白字符的密钥，在登记时注册 | 带 `X-OAC-Node-ID` 的 `sandbox-node/configuration`、`sandbox-node/identity`、`sandbox-node/connect` |
| 安装授权 | `self_hosted` Session 的 `x_agents_core.installation` 命令；短期有效 | `agent-daemon/installation` 及其 `claim` |
| 执行器凭据 | 安装领取，或 Core 密钥[执行器凭据路由](environment-executor-credentials.md) | `agent-daemon/enroll` 和 `agent-daemon/connection`；登记后也作为该 Environment 的 enrollment 在 `sandbox-link` 上的 Serve 凭据 |
| Agent-host 凭据 | 安装初始化写入 [agent-host 身份](../../../docs/zh/configuration.md#agent-host-container)；Core 在启动时注册它 | `agent-daemon/bootstrap`、`agent-daemon/ws` 和作为 attach peer 的 `sandbox-link` |

Core 对存储的每个 token 和凭据仅保留 SHA-256 摘要；安装授权经签名但不存储。凭据不可互换：各自仅适用于自身路由。

## 节点路由 {#node-routes}

### 读取节点配置 {#read-the-node-configuration}

`GET /api/v1/sandbox-node/configuration` 返回用于节点安装和恢复的活动部署，不消耗登记 token。

- 新节点发送登记 token，不带 `X-OAC-Node-ID`。token 必须有效、未过期、未消费且由此安装签发。活动重置拒绝该读取。
- 已注册节点发送节点凭据，并在 `X-OAC-Node-ID` 放其 UUID。无查询时读取当前目标。`?generation=N` 仅读取此节点仍可能需要的代际：当前目标、服务 pin，或其上未释放分配或 placement 持有的代际；其他代际被拒绝。重置期间仍可读取，以恢复已有归属资源。

响应包含 `installation_id`、`provider`、`core_url`（安装公开 URL）、`generation`、`specification`、`specification_digest`、`max_active` 和 `max_retained`。不包含管理员、Project 或 E2B 凭据，仅适用于节点型提供方。[沙箱部署契约](sandbox-deployment.md#canonical-node-specification)定义 specification 与摘要。

### 登记节点 {#enroll-a-node}

`POST /api/v1/sandbox-node/enroll` 注册节点并消费 token。正文恰好包含以下字段：

| 字段 | 值 |
| --- | --- |
| `node_id` | 节点选定的规范 UUID |
| `credential` | 节点密钥，32 至 256 个无空白字符 |
| `name` | 显示名称 |
| `provider` | 部署提供方 |
| `backend_fingerprint` | 节点后端命名空间摘要 |
| `deployment_generation`, `specification_digest` | 节点读取的配置 |
| `core_url` | 节点存储并连接的 Core origin |

Core 在一个事务中检查 token 有效、部署已初始化且为节点型且未重置、代际与摘要匹配当前 specification、`core_url` 等于安装公开 URL、节点 ID 未使用。仅通过后按 token 批准容量注册节点并消费 token。201 响应为节点身份：`node_id`、`installation_id`、`provider`、`deployment_generation`、`specification_digest`、`max_active` 和 `max_retained`。节点不能提交容量；登记代际和摘要为不可变身份，后续代际使用独立配置。

### 恢复节点身份 {#recover-a-node-s-identity}

`GET /api/v1/sandbox-node/identity?node_id=` 返回同一身份，加 Core 当前观察的 `connected` 和 `provider_ready`。

### 节点路由错误 {#node-route-errors}

| HTTP | 代码 | 时机 |
| --- | --- | --- |
| 400 | `invalid_request_error`, `param: "core_url"` | 登记未提供 `core_url` |
| 400 | `invalid_request` | 正文、节点 ID 或代际格式错误，或提供方与部署不同 |
| 401 | `invalid_node_credential` | token 或节点凭据缺失、无效、过期、已消费或属于其他安装 |
| 409 | `sandbox_specification_mismatch` | 节点代际或摘要不匹配 |
| 409 | `sandbox_node_address_mismatch` | `core_url` 不是安装公开 URL；token 保持未使用 |
| 409 | `idempotency_conflict` | 节点 ID 已注册 |
| 409 | `sandbox_reset_in_progress` | 重置期间登记或新节点读取配置 |
| 503 | `runtime_node_unavailable` | 部署未初始化或存储不可用 |

先检查凭据，再检查部署状态，因此被拒绝凭据（包括其他安装签发的）即使未初始化或使用 E2B 也返回 401。部署初始化前，配置读取与登记对其他方面有效的 token 返回 503，身份读取与节点连接返回 401。

## daemon 路由 {#daemon-routes}

### daemon 引导 {#daemon-bootstrap}

`POST /api/v1/agent-daemon/bootstrap` 携带 agent-host 凭据及 `{"device_id": "…"}`，返回 `device_id`、`workspace_id`（部署范围的主机为空字符串）、`ws_url`（从 `OAC_PUBLIC_URL` 推导，不使用请求头）、`heartbeat_seconds` 和 `protocol_version`。daemon 随后按 [Core–Runtime 协议](../../../docs/zh/runtime-protocol.md#ownership-and-connection)连接 `ws_url`。

引导和 WebSocket 路由报告以下 `error.code` 值：400 `missing_params`、`missing_device_id` 或 `bad_json`；401 `missing_bearer`、`unknown_device` 或 `bad_credential`；403 `wrong_runtime_type`；500 `internal`；WebSocket 的 `version` 不等于 Core 精确 Runtime 协议版本时返回 426 `incompatible_version`。

### 登记自托管 daemon {#enroll-a-self-hosted-daemon}

`POST /api/v1/agent-daemon/enroll` 携带执行器凭据及精确正文 `{"environment_id": "…"}`（无查询），把机器登记为 Environment 的 [Link](../../../docs/zh/sandbox-link-protocol.md) resource，并返回由 Core 提供的[启动输入](../../../docs/zh/sandbox-bootstrap.md#launch-input)字段：`link_url` 和 `resource`（`tenant_id`、`environment_id`、`kind` 为 `enrollment`、`id` 和 `generation`）。不返回其他凭据：执行器凭据就是该 resource 的 Serve 凭据。最先登记该 Environment 的密钥保有它，用该密钥重试返回同一 resource 及其当前 generation。成功响应包含 `Cache-Control: no-store`。

| HTTP | 时机 |
| --- | --- |
| 400 | 正文格式错误或存在任何查询 |
| 401 | 凭据无效、撤销、属于其他范围，Session 已删除，或 Environment 无当前执行器权限 |
| 409 | 其他执行器密钥已登记该 Environment |
| 503 | [公共 URL](../../../docs/zh/configuration.md#changing-the-public-url) 不是 https 时，在验证请求头和正文后、检查执行器权限前返回 `error.code: "no_sandbox_link"` 和 `error.message: "a self_hosted sandbox needs an https public URL"`；否则表示存储不可用 |

登记不创建受管分配，不绑定 Session，也不授予 Session API 访问权限。机器为该 resource 提供服务期间，Core 把 Session 绑定到部署的 agent host（[Session 分配](../../../docs/zh/runtime-protocol.md#session-assignments)）。relay 在每次 Serve 和 Open 时重查凭据权限，因此轮换、撤销和删除 Session 终止后续使用。[自托管指南](../../../docs/zh/getting-started/self-hosted.md)提供操作步骤，[执行器凭据契约](environment-executor-credentials.md#revoked-or-rotated-credential)描述 daemon 如何处理永久拒绝。
