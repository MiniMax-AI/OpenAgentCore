---
title: "OpenAgentCore Web"
source: docs/web/index.md
source_hash: 59ab5c2c95b32a7fb7e6444bccf64be367372212fe6329b44398891cfe8d730a
---

Web 是单个 OpenAgentCore 部署的管理员控制台。管理员用它查看健康状态、容量、用量和失败情况，检查各 Project 的资源与执行历史，并管理 Project、密钥、节点和部署设置。应用不使用 Web；它们通过自己的 Project API 密钥调用 Core 的 Agents API（`/v1`）。

![OpenAgentCore Web 概览](../../assets/console-overview-en.webp)

## 登录 {#sign-in}

使用部署的 [Core 密钥](../getting-started/operations.md#core-key)[登录](../getting-started/install.md#sign-in-to-web)；控制台没有用户账号。浏览器只保留会话 cookie，[控制台服务器](console-server.md)代它向 Core 发送 Core 密钥；[登录](console-server.md#sign-in)说明会话有效期。

登录后打开 Overview。仍有未完成步骤时，**Getting started** 清单引导完成四步，顺序不限：沙箱就绪、默认模型提供商、具有活动密钥的 Project、首个 Session。可从清单启动可选的控制台导览。

## 控制台页面 {#console-pages}

| 分组 | 页面 | 用途 |
| --- | --- | --- |
| Monitor | Overview | 服务状态、运行中的 Session、沙箱槽位和待处理工作；24 小时 Session 活动；以拓扑展示 Core 与节点，各自提供弹出概览；需要关注的 Session；按 Project 统计的用量 |
| Monitor | Core metrics | Core 进程：CPU 与常驻内存及其限制、执行槽位与 Turn 队列、已连接守护进程、数据库延迟与连接池、后台任务 |
| Monitor | Agent metrics | 1 小时、6 小时、24 小时或 7 天的请求、错误、耗时、token、模型、工具、Agent 和 API 密钥 |
| Monitor | Sandbox metrics | 节点容量及各 Project 的托管 Runtime CPU 与内存 |
| Monitor | Session log | 全部 Session；各 Session 的只读对话、跟踪、Turn 和失败分类原因；自托管 Session 页面还管理执行器凭据并提供主机连接命令 |
| Resources | Agents、Environment templates、Skills、Files、Vaults | 检查和允许的删除操作，显示各资源所属 Project 及创建密钥 |
| Platform | Projects and keys | 创建、重命名和归档 Project；签发与撤销密钥；各 Project 的用量、写入历史和 API 调用方法 |
| Platform | Nodes | 添加、编辑和移除 Docker 或 microsandbox 节点；各节点的就绪状态、容量和分配 |
| Platform | System | 安装的公开地址、API 基础 URL、ID 和源码提交；**Domain and HTTPS**；各 Harness 默认模型；**Sandbox configuration**；Core `config.json` 启动设置，只读并注明修改位置 |

缺失数据展示为缺失（—），不会当作零。[控制台 API 使用](console-api-usage.md)列出各页面读取内容及统计边界。

## 管理员在此执行的任务 {#what-administrators-do-here}

| 任务 | 位置 |
| --- | --- |
| 为安装设置 HTTPS 地址 | 托管入口安装的 **System → Domain and HTTPS**；参阅[使 Core 可访问](../getting-started/install.md#configure-the-domain-and-https) |
| 选择沙箱后端（Docker、microsandbox 或 E2B）、规格和 Runtime，或重置后端 | **System → Sandbox configuration**；参阅[修改沙箱配置](../getting-started/nodes.md#change-the-sandbox-configuration) |
| 添加或移除执行节点 | **Nodes**；参阅[节点指南](../getting-started/nodes.md) |
| 设置 Harness 默认模型 | **System → Default model configuration**；参阅[默认模型](../configuration.md#default-models) |
| 创建 Project 并签发 API 密钥 | **Projects and keys**；参阅 [Project 和 API 密钥](../getting-started/operations.md#projects-and-api-keys) |
| 签发、轮换或撤销自托管执行器凭据，或复制安装命令 | **Session log** 中的 Session 页面；参阅[自托管执行器](../getting-started/self-hosted.md) |
| 删除资源，例如泄露的 Credential | 资源列表行或资源页面；Files 从 Files 列表删除。适用公开删除规则 |

安装不创建 Project 或密钥。打开控制台不分配计算资源，也不调用模型，安装可以没有节点。Web 不启动 Session，也不发送输入。归档托管 Session 会请求取消和回收；[管理员权限](../concepts.md#what-administrators-can-and-cannot-do)说明管理员能执行与不能执行的操作。

部署的沙箱后端服务托管 Session。应用的 `self_hosted` Runtime（包括它自己 E2B 账号中的 Runtime）是独立路径，修改沙箱配置不会影响它。

回环公开地址（`local_only`）使节点和远程应用无法访问 Core。控制台仍可通过自己的地址访问，并[展示警告](console-api-usage.md#provenance-and-monitoring)。

## 更多 {#more}

- [控制台服务器](console-server.md)：请求边界、登录、设置和验证。
- [控制台 API 使用](console-api-usage.md)：各页面使用的 Core 路由。
- [Web 包](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/README.md)：开发控制台。
- [管理员 API](../../../contracts/agents-api/zh/admin-api.md)：控制台背后的 `/core/v1` 路由。

OpenAgentCore Web 使用 [MIT 许可证](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/LICENSE)。
