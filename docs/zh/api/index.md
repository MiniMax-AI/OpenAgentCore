---
title: "API 命名空间和凭据"
source: docs/api/index.md
source_hash: 2948bfd2f5ed2f86ebef424f6dd45367e3906bc7fe30b57efdc6a4b68cefde89
---

Core 提供三个 API 命名空间，每个面向一类调用方并使用独立凭据：应用使用 `/v1`，管理员使用 `/core/v1`，机器使用 `/api/v1`。凭据只能在其所属命名空间中使用。

| 命名空间 | 调用方 | 凭据 | 内容 | 所有者 |
| --- | --- | --- | --- | --- |
| `/v1` | 应用程序：业务系统和官方 OpenAI SDK | 项目 API 密钥 | 恰好是固定版本 OpenAI SDK 中 Agents API 的路由，列于 [upstream-routes.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream-routes.json)。仅属于 Core 的字段位于 `x_agents_core` 中；参阅 [Core 扩展](public-agent-api.md#core-extensions) | [Agents API 指南](public-agent-api.md) |
| `/core/v1` | Web 的控制台服务器和操作员脚本 | [Core 密钥](../getting-started/operations.md#core-key) | 安装信息、项目和密钥、资源读取和删除、Session 归档、执行器凭据、默认模型、指标、请求日志、审计、沙箱部署和节点、独立工作区存储配置 | [Core 管理 API](../../../contracts/agents-api/zh/admin-api.md) |
| `/api/v1` | 节点、Runtime 守护进程、自托管执行器及其安装程序 | 机器凭据：节点注册令牌和节点凭据、安装授权、执行器凭据和守护进程凭据。每种凭据只能用于其各自的路由 | `/api/v1/sandbox-node/*` 和 `/api/v1/agent-daemon/*` 下的机器初始化与连接（包括 WebSockets），以及公共原生安装程序下载 | [机器连接 API](../../../contracts/agents-api/zh/machine-api.md) |

在其他命名空间中使用凭据会返回 401：在 `/core/v1` 或 `/api/v1` 上使用项目 API 密钥，或者在 `/v1` 或 `/api/v1` 上使用 Core 密钥。有关项目和密钥的行为，请参阅 [项目自有资产](../concepts.md#projects-own-assets)。

**路由。** Web [把 API 路径转发到 Core](../web/console-server.md#request-boundary)。已登录的浏览器通过 Web 访问 `/core/v1`，由 Web 附上 Core 密钥。操作员脚本在 Core 主机上从 Core 的网络命名空间内调用 `/core/v1`（[编写 Core API 脚本](../getting-started/operations.md#script-the-core-api)）。

## API 参考 {#api-reference}

Core 在 `/docs` 提供三个命名空间的只读 Swagger UI，文档位于 `/docs/openapi.yaml`、`/docs/core.openapi.yaml` 和 `/docs/runtime.openapi.yaml`。不需要凭据，页面也不发送 API 请求。在控制台源地址打开，例如 `http://localhost:8080/docs`。浏览器从 `unpkg.com` 加载 Swagger UI。
