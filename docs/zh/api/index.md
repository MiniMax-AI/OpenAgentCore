---
title: "API 命名空间和凭据"
source: docs/api/index.md
source_hash: d6483deff36f22b113e3d6df256c399238b8f4ffeeda889a1b0e3f0ddbdb6e67
---

Core 提供三个命名空间。每个命名空间都有一种调用方及其独立凭据，凭据只能在其所属命名空间中使用。

| 命名空间 | 调用方 | 凭据 | 内容 | 所有者 |
| --- | --- | --- | --- | --- |
| `/v1` | 应用程序：业务系统和官方 OpenAI SDK | Project API key | 固定版本官方 Agents API 中全部且仅有的 58 个方法和路径对，列于 [upstream-routes.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream-routes.json)。仅属于 Core 的字段位于 `x_agents_core` 中：`harness`、`model_provider`、`harness_config`、`environment`，以及只读的 Session `installation` | [Agents API 指南](public-agent-api.md) |
| `/core/v1` | Web 的控制台服务器和操作员脚本 | [Core key](../getting-started/operations.md#core-key) | 安装信息、Project 和密钥、资源读取和删除、Session 归档、执行器凭据、默认模型、指标、审计、沙箱部署和节点 | [Core 管理 API](../../../contracts/agents-api/zh/admin-api.md) |
| `/api/v1` | 节点、Runtime 守护进程、自托管执行器及其安装程序 | 机器凭据：节点注册令牌和节点凭据、安装授权、执行器凭据和守护进程凭据。每种凭据只能用于其各自的路由 | `/api/v1/sandbox-node/*` 和 `/api/v1/agent-daemon/*` 下的机器初始化与连接（包括 WebSockets），以及公共原生安装程序下载 | [机器连接 API](../../../contracts/agents-api/zh/machine-api.md) |

在其他命名空间中使用凭据会返回 401：在 `/core/v1` 或 `/api/v1` 上使用 Project API key，或者在 `/v1` 或 `/api/v1` 上使用 Core key。有关 Project 和密钥的行为，请参阅 [Project 自有资产](../concepts.md#projects-own-assets)。

**路由。** 反向代理将 `/v1` 和 `/api/v1` 发送到 Core，将其他所有请求发送到 Web（[代理设置](../getting-started/install-options.md#https-and-the-reverse-proxy)）。浏览器只能通过 Web 的控制台服务器访问 `/core/v1`；该服务器会在登录后添加 Core key，并对 `/v1` 和 `/api/v1` 返回 404（[控制台服务器](../web/console-server.md)）。操作员脚本通过 Core 的回环端口调用 `/core/v1`（[编写 Core API 脚本](../getting-started/operations.md#script-the-core-api)）。

**API 参考页面。** Core 在 `/docs` 提供三个命名空间的只读 Swagger UI，并在 `/docs/openapi.yaml`、`/docs/core.openapi.yaml` 和 `/docs/runtime.openapi.yaml` 提供页面所渲染的生成文档。这些路由不需要凭据，页面也不会发送 API 请求。反向代理不会把 `/docs` 发送到 Core，因此请通过 Core 自己的地址打开：在 Core 主机上访问 `http://127.0.0.1:<port>/docs`，其中端口为 [`ports.core`](../configuration.md#settings)，默认是 8091。浏览器从 `unpkg.com` 加载 Swagger UI。

## 机器连接 API {#machine-connection-api}

节点、Runtime 守护进程和自托管安装程序使用各自的凭据调用 `/api/v1`。[机器连接 API](../../../contracts/agents-api/zh/machine-api.md) 列出了每个路由、调用方和凭据。
