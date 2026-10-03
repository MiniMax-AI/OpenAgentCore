---
title: "机器连接 API"
source: contracts/agents-api/machine-api.md
source_hash: e9a16774d2924db9dde18385be315d3a0317c616b029b419165e442fa34fb08d
---

机器通过 `/api/v1` 调用 Core：包括沙箱节点、Runtime daemon 和自托管安装器。各路由仅接受所列凭据，不接受 Core 密钥或 Project API 密钥；控制台登录也不授予此处权限。反向代理将 `/api/v1` 直接发送给 Core；Web 不提供这些路由。

## 路由 {#routes}

| 路由 | 调用方 | 凭据 | 契约 |
| --- | --- | --- | --- |
| `GET sandbox-node/configuration` | 节点安装器与节点 | 登记 token，或节点凭据加 `X-OAC-Node-ID` | [读取节点配置](#read-the-node-configuration) |
| `POST sandbox-node/enroll` | 节点安装器 | 登记 token | [登记节点](#enroll-a-node) |
| `GET sandbox-node/identity?node_id=` | 节点 | 节点凭据 | [恢复节点身份](#recover-a-node-s-identity) |
| WebSocket `GET sandbox-node/connect?node_id=` | 节点 | 节点凭据 | [节点代际协议](node-generation-protocol.md) |
| `GET agent-daemon/install/{version}/…` | 自托管安装器 | 无 | [安装授权](environment-executor-credentials.md#installation-grant) |
| `POST agent-daemon/installation`, `POST agent-daemon/installation/claim` | 自托管安装器 | 安装授权 | [安装授权](environment-executor-credentials.md#installation-grant) |
| `POST agent-daemon/enroll` | 自托管 daemon | 执行器凭据 | [登记自托管 daemon](#enroll-a-self-hosted-daemon) |
| `GET agent-daemon/connection?environment_id=` | 自托管安装器 | 执行器凭据 | [私有连接确认](environment-executor-credentials.md#private-connection-confirmation) |
| `POST agent-daemon/bootstrap` | Runtime daemon | daemon 凭据 | [daemon 引导](#daemon-bootstrap) |
| `GET agent-daemon/device-status?device_id=` | Runtime daemon | daemon 凭据 | [设备状态](#device-status) |
| WebSocket `GET agent-daemon/ws?device_id=&version=` | Runtime daemon | daemon 凭据 | [Core–Runtime 协议](../../../docs/zh/runtime-protocol.md) |

所有凭据通过 `Authorization: Bearer` 头传输，不放入 URL。

生成的 [`runtime.openapi.yaml`](../runtime.openapi.yaml) 仅描述 sandbox-node 配置、登记、身份路由和两个安装路由。两个 WebSocket 及 daemon 引导、设备状态、登记和连接路由在 API 路由器外提供，无生成 schema；本文及所链接契约是它们唯一的定义。

## 凭据 {#credentials}

| 凭据 | 签发方 | 接受位置 |
| --- | --- | --- |
| 登记 token | `POST /core/v1/sandbox/enrollment-tokens`（Web **Add node**），带节点批准容量。使用一次；在响应 `expires_at` 过期 | 无节点 ID 的 `sandbox-node/configuration`、`sandbox-node/enroll` |
| 节点凭据 | 节点自身：生成 32 至 256 个无空白字符的密钥，在登记时注册 | 带 `X-OAC-Node-ID` 的 `sandbox-node/configuration`、`sandbox-node/identity`、`sandbox-node/connect` |
| 安装授权 | `self_hosted` Session 的 `x_agents_core.installation` 命令；短期有效 | `agent-daemon/installation` 及其 `claim` |
| 执行器凭据 | 安装领取，或 Core 密钥[执行器凭据路由](environment-executor-credentials.md) | `agent-daemon/enroll` 和 `agent-daemon/connection`；登记后也作为绑定设备的 daemon 凭据 |
| 托管沙箱 daemon 凭据 | Core 为每个受管分配签发，通过[引导文件](../../../docs/zh/runtime-bootstrap.md)交付 | `agent-daemon/bootstrap`、`device-status` 和 `ws` |
| 操作者设备配置 | 具有数据库访问权限的操作者运行 `oac-core-device` | `agent-daemon/bootstrap`、`device-status` 和 `ws` |

Core 对存储的每个 token 和凭据仅保留 SHA-256 摘要；安装授权经签名但不存储。凭据不可互换：各自仅适用于自身路由。

### 操作者设备配置 {#operator-device-profile}

`environment: none` Session 的引擎主机使用操作者直接在数据库创建的设备配置连接：

```sh
umask 077
mkdir -p ~/.oac/daemon/default
OAC_DATABASE_URL=... oac-core-device --tenant <tenant-uuid> --name 'engine host' --url https://core.example > ~/.oac/daemon/default/auth.json
oac-daemon connect --profile default
```

`--tenant` 为 Project 执行租户 UUID，`--url` 为不带路径的 Core origin。命令打印配置一次：`server_url`（origin 加 `/api/v1`）、`runtime_id`（设备 ID）、`runner_credential` 和 `device_name`。使用新配置，不覆盖其他设备文件；私密复制到远程主机相同路径。`oac-core-device --tenant <tenant-uuid> --revoke <device-uuid>` 撤销设备：立即拒绝新连接，已有连接在下一次心跳关闭。Worker 将每个 `none` Session 绑定到其租户内声明所需能力的已连接设备，重试和重启保留绑定；自托管 Session 不使用此路径。

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

`POST /api/v1/agent-daemon/bootstrap` 携带 daemon 凭据及 `{"device_id": "…"}`，返回 `device_id`、`workspace_id`、`ws_url`（从 `OAC_PUBLIC_URL` 推导，不使用请求头）、`heartbeat_seconds` 和 `protocol_version`。daemon 随后按 [Core–Runtime 协议](../../../docs/zh/runtime-protocol.md#ownership-and-connection)连接 `ws_url`。

### 设备状态 {#device-status}

`GET /api/v1/agent-daemon/device-status?device_id=` 携带 daemon 凭据，返回 `device_id`、`online` 和 `owner`：当前连接所有者的 `owner_pod_id`、`owner_url`、`generation`、`status` 和 `lease_expires_at`，或 null。

引导、设备状态和 WebSocket 路由共享错误体 `{"error": code, "detail": text}`：400 `missing_params`、`missing_device_id` 或 `bad_json`；401 `missing_bearer`、`unknown_device` 或 `bad_credential`；403 `wrong_runtime_type`；500 `internal`；WebSocket 的 `version` 不等于 Core 精确 Runtime 协议版本时返回 426 `incompatible_version`。

### 登记自托管 daemon {#enroll-a-self-hosted-daemon}

`POST /api/v1/agent-daemon/enroll` 携带执行器凭据及精确正文 `{"environment_id": "…"}`（无查询），将一个专用设备绑定到 Environment 的 Session，返回 `device_id`、`session_id`、`environment_id` 和 `workspace_directory`。不返回其他凭据：执行器凭据成为该设备的 daemon 凭据。相同凭据重试返回相同绑定。成功响应包含 `Cache-Control: no-store`。

| HTTP | 时机 |
| --- | --- |
| 400 | 正文格式错误或存在任何查询 |
| 401 | 凭据无效、撤销、属于其他范围，Session 已删除，或 Environment 无当前执行器权限 |
| 409 | Environment 已绑定到不同密钥或设备 |
| 503 | 存储不可用 |

登记不创建受管分配，也不授予 Session API 访问权限。daemon 在凭据旁保存绑定，拒绝其他 Environment 的原生历史。网关与 Worker 在每次连接和分发时重查凭据权限，因此轮换、撤销和删除 Session 终止后续使用。[自托管指南](../../../docs/zh/getting-started/self-hosted.md)提供操作步骤，[执行器凭据契约](environment-executor-credentials.md#revoked-or-rotated-credential)描述 daemon 如何处理永久拒绝。
