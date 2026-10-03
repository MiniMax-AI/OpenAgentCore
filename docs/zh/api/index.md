---
title: "API 命名空间和凭据"
source: docs/api/index.md
source_hash: 11df05084e85f1bc05650e11c2c744318b92ba7ac90280207956fce712335122
---

Core 提供三个命名空间。每个命名空间都有一种调用方及其独立凭据，凭据只能在其所属命名空间中使用。

| 命名空间 | 调用方 | 凭据 | 内容 | 所有者 |
| --- | --- | --- | --- | --- |
| `/v1` | 应用程序：业务系统和官方 OpenAI SDK | Project API key | 固定版本官方 Agents API 中全部且仅有的 58 个方法和路径对，列于 [upstream-routes.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream-routes.json)。仅属于 Core 的字段位于 `x_agents_core` 中：`harness`、`model_provider`、`harness_config`、`environment`，以及只读的 Session `installation` | [Agents API 指南](public-agent-api.md) |
| `/core/v1` | Web 的控制台服务器和操作员脚本 | [Core key](../getting-started/operations.md#core-key) | 安装信息、Project 和密钥、资源读取和删除、Session 归档、执行器凭据、默认模型、指标、审计、沙箱部署和节点 | [Core 管理 API](../../../contracts/agents-api/zh/admin-api.md) |
| `/api/v1` | 节点、Runtime 守护进程、自托管执行器及其安装程序 | 机器凭据：节点注册令牌和节点凭据、安装授权、执行器凭据和守护进程凭据。每种凭据只能用于其各自的路由 | `/api/v1/sandbox-node/*` 和 `/api/v1/agent-daemon/*` 下的机器初始化与连接（包括 WebSockets），以及公共原生安装程序下载 | [机器连接 API](../../../contracts/agents-api/zh/machine-api.md) |

在其他命名空间中使用凭据会返回 401：在 `/core/v1` 或 `/api/v1` 上使用 Project API key，或者在 `/v1` 或 `/api/v1` 上使用 Core key。有关 Project 和密钥的行为，请参阅 [Project 自有资产](../concepts.md#projects-own-assets)。

**路由。** Web 把 `/v1`、`/api/v1` 和 `/docs` 原样转发到 Core（[控制台服务器](../web/console-server.md)）。已登录的浏览器通过 Web 访问 `/core/v1`，由 Web 附上 Core key。操作员脚本在 Core 主机上从 Core 的网络命名空间内调用 `/core/v1`（[编写 Core API 脚本](../getting-started/operations.md#script-the-core-api)）。

**API 参考。** Core 在 `/docs` 提供三个命名空间的只读 Swagger UI，文档位于 `/docs/openapi.yaml`、`/docs/core.openapi.yaml` 和 `/docs/runtime.openapi.yaml`。不需要凭据，页面也不发送 API 请求。在控制台源地址打开，例如 `http://localhost:8080/docs`。浏览器从 `unpkg.com` 加载 Swagger UI。

## 机器连接 API {#machine-connection-api}

节点、Runtime 守护进程和自托管安装程序使用各自的凭据调用 `/api/v1`。[机器连接 API](../../../contracts/agents-api/zh/machine-api.md) 列出了每个路由、调用方和凭据。
