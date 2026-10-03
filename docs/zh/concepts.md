---
title: "概念与所有权"
source: docs/concepts.md
source_hash: f15758cc223f104393f3eca822dbc585c8c41bb5c7bdbe787e8af4e7bf1a0d95
---

Project 是 OpenAgentCore 的执行租户。应用使用其 API key；运维人员使用独立的 Core key 管理安装实例。[API 索引](api/index.md) 将每类调用方映射到对应命名空间和凭据。

## Project 拥有资产 {#projects-own-assets}

Project 拥有自己的 Agent、Session、Environment、Skill、File 和 Vault。其命名 API key 共用相同的主体、权限和资源。Core 不提供产品用户、角色、成员关系或只读 key。名称是标签；稳定 ID 标识 Project 和 key。

Project 和 key 存储在 PostgreSQL 中。Core 仅在签发时返回一次 key 明文。轮换 key 时，在同一 Project 中签发新 key，再撤销旧 key。撤销保留资产、写入来源和已接受的工作。归档 Project 会撤销所有 key 并禁止签发新 key；管理员仍可检查和删除其资源。[管理契约](../../contracts/agents-api/zh/admin-api.md#projects-and-keys) 定义这些操作。

[Core key](getting-started/operations.md#core-key) 是独立的部署凭据。管理员需要使用应用 API 时，应签发 Project key 并使用该 Project 的权限。产品用户、工作区和业务权限属于应用。

## 资源隔离 {#resource-isolation}

应用的读取、写入和引用限定在 key 所属的 Project 内。其他 Project 中的资源与不存在的资源无法区分。Core 不在 Project 之间共享或复制资产。节点、已配置的模型端点和启动设置属于部署基础设施。

## 管理员可以做什么 {#what-administrators-can-and-cannot-do}

管理员管理 Project 和 key、沙箱部署、节点、executor 凭据及部署默认模型。他们可以检查资源、执行历史、运行计数与用量，并按删除规则删除资源。归档托管 Session 会请求取消和沙箱回收；参见 [Session 归档](../../contracts/agents-api/zh/admin-api.md#session-archive)。

创建或编辑应用资产、启动 Session 和提交输入都需要 Project API key。Core key 不提供应用身份，不能读取已保存的秘密。[管理 API](../../contracts/agents-api/zh/admin-api.md) 定义其操作；[控制台服务端](web/console-server.md) 负责 Web 登录与凭据处理。

## Runtime 与外层隔离 {#runtime-and-outer-isolation}

Runtime daemon 在 Linux、macOS 和 Windows 上运行。原生平台行为由 Runtime 及其 Harness adapter 负责；托管 Sandbox Provider 运行 Linux 环境。

工具以启动 daemon 的账户权限运行。daemon 不增加文件系统、权限或网络隔离。应使用外层 Environment 提供隔离：托管 Docker、E2B 或 microsandbox 环境，或在自托管机器的 Runtime 外使用容器或虚拟机。认证、私有存储、锁和进程清理保护连接与生命周期，但以同一用户运行的工具可以访问 Runtime 数据。

## 秘密与审计 {#secrets-and-audit}

凭据值、模型 key 和机密模板数据仅可写入：应用和管理员读取结果都不包含它们。对话文本、Skill 源码和 Artifact 内容是可读取的资源数据，管理员也可以读取。

公开写入记录执行写入的 API key。管理员写入记录独立的审计身份与目标 Project。Web 的 actor 标签仅用于显示；Core 授权依据是 Core key。审计失败会回滚写入。读取不进行审计，审计记录不包含请求体、秘密或文件内容。[写入来源](../../contracts/agents-api/zh/admin-api.md#write-provenance) 和[审计日志](../../contracts/agents-api/zh/admin-api.md#audit-log) 契约定义存储的记录。
