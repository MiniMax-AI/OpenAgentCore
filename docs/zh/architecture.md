---
title: "架构"
source: docs/architecture.md
source_hash: 061593c891cc4dd8b9077311bec8b8b93051c1cba9e387fb7db40a7ffc3a3de5
---

OpenAgentCore 是 Agent 运行平台，通过可替换的协议实现统一组织 Harness、模型、工具、Session 和执行环境。它将编排、计算资源和原生执行分开。Core 负责 API 和持久状态。Sandbox Provider 管理计算资源。独立工作区文件系统 adapter 通过单独的协议管理 Environment 持久存储。Runtime daemon 准备 Environment 并运行选定的 Harness；Harness 的原生 SDK 或协议负责模型与工具循环。

推荐的部署选择是 E2B 托管计算，以及 microsandbox 配合独立持久化工作区存储的自部署方案。两者使用相同的 Core 编排和能力检查。Docker 仍是可用的 Provider。计算和文件系统 adapter 声明其支持的组合；部署选择不会引入独立的 Agent 执行流程。具体契约和支持能力见 [Sandbox Provider](./sandbox-provider.md) 和[工作区文件系统 Provider](./workspace-provider.md)。

```mermaid
flowchart TB
    App["Application / official SDK"] <-->|"Agents API /v1: HTTP and SSE"| Core
    Web["Web administrator console"] <-->|"Core API /core/v1"| Core
    Core["Core: authorization, configuration snapshots,<br/>orchestration and durable state"]
    Core --- DB[("PostgreSQL")]
    Core -->|"Sandbox Provider protocol"| SP["Sandbox Provider: Docker / E2B / microsandbox"]
    SP -.->|"Provision compute and bootstrap Runtime"| R
    User["User-machine installer"] -.->|"Start Runtime"| R
    Core <-->|"Core–Runtime protocol:<br/>preparation, execution, events and receipts"| R
    subgraph Env["Environment: managed sandbox or user-owned machine"]
        R["Runtime daemon"] --> P["Workspace and capability preparation"]
        P -->|"Harness protocol"| A["Harness adapter"]
        A <-->|"Native SDK or protocol"| H["Native Harness: model and tool loop"]
        H <--> F["Workspace, tools and artifacts"]
    end
    H <-->|"Model API"| Model["Model provider"]
    H <-->|"MCP"| MCP["Local or remote MCP servers"]
```

虚线表示资源供应与安装。实线表示组件交互，包括进程内接口。daemon 主动向 Core 发起经过认证的 WebSocket 连接。[API 索引](api/index.md) 说明应用、运维和机器命名空间；[协议边界](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#protocols-at-every-boundary) 列出各协议的代码和所属文档。

## 组件职责 {#component-responsibilities}

| 组件 | 职责 | 参考 |
| --- | --- | --- |
| Core | 认证调用方，解析并冻结配置，调度 Turn，处理取消和待处理交互，将资源与执行事实持久化到 PostgreSQL | [Core 服务](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md) |
| Sandbox Provider | 创建、观察、续期和回收计算资源；提供 Runtime 启动输入 | [Sandbox Provider](sandbox-provider.md)、[Runtime 引导](runtime-bootstrap.md) |
| Workspace filesystem adapter | 创建、观察和删除 Environment 持久存储；验证并解析计算资源所需的挂载 | [工作区文件系统 Provider](workspace-provider.md) |
| Sandbox node | 运行 Docker 或 microsandbox 主机，协调分配给它的 generation 与 allocation | [Sandbox node 协议](../../contracts/agents-api/zh/node-generation-protocol.md) |
| Runtime | 准备工作区和能力，管理 Session Executor，执行 Turn 并报告事件与回执 | [Core–Runtime 协议](runtime-protocol.md) |
| Harness adapter | 验证原生配置，调用上游 SDK 或协议，转换事件并确认原生清理完成 | [Harness 接入](../../contracts/agents-api/zh/harness-onboarding.md) |
| Model provider | 提供 Harness 选定的模型协议 | [模型执行](../../contracts/agents-api/zh/model-execution.md) |
| Web | 让管理员通过服务端 Core API 连接配置与观察安装实例 | [控制台服务端](web/console-server.md) |

[仓库地图](development.md#repository-map) 标出这些组件的位置。[概念](concepts.md) 解释 Project 边界、管理员权限和工具隔离。

## Session 的完整流程 {#a-session-end-to-end}

应用通过 Agents API 创建 Session。Core 解析其配置与执行位置。托管 Session 通过选定的 Sandbox Provider 获取计算资源；自托管 Session 等待用户运行安装命令。`environment: none` 的 Session 使用已连接的执行设备，不提供工作区。[应用指南](api/public-agent-api.md#create-a-session) 说明这些选项。

daemon 连接后，Core 检查可用 Harness 和请求的能力。Runtime 准备工作区 Environment 及其能力快照，然后准备或复用 Session Executor。每个 Turn 通过原生 Harness 运行。Core 持久化输出、工具交互和回执，供应用读取和接收事件。完成或取消使 Turn 结算；健康的 Executor 可以在同一 Environment 中执行下一个 Turn。

执行、计算资源和独立工作区存储拥有不同的生命周期。关闭 Executor 本身不会删除存储。[工作区文件系统契约](workspace-provider.md#lifetime-and-filesystem-scope) 负责持久存储保留及显式删除规则；计算快照到期不授权回收工作区存储。准备、连接和执行就绪具有不同状态。[Environment 契约](../../contracts/agents-api/zh/environments.md) 负责准备规则，[Core–Runtime 协议](runtime-protocol.md) 负责顺序、回执和故障处理。
