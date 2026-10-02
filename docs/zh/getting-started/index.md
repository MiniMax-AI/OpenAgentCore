---
title: "OpenAgentCore 文档"
source: docs/getting-started/index.md
source_hash: b00e7833d6dc323fc53a9e6dd145bd0e08069a421346ee48c9454397494aa5a8
---

OpenAgentCore 在你自己的基础设施上运行 AI Agent，并提供 OpenAI Agents API。[架构概览](../architecture.md)介绍各个组成部分。根据你的角色选择指南。

## 运维人员 {#operators}

运维人员安装 Core 和 Web、添加沙箱容量并签发 Project API 密钥。

| 指南 | 内容 |
| --- | --- |
| [安装 Core 和 Web](install.md) | 默认安装流程：从空白主机到第一个 Project API 密钥 |
| [安装选项](install-options.md) | 安装参数、已有反向代理、离线主机 |
| [节点](nodes.md) | 添加、修改、移除沙箱节点及排查问题 |
| [运维](operations.md) | `oac` 命令、Core 密钥、备份、卸载、升级及问题排查 |
| [配置参考](../configuration.md) | 所有设置、文件和环境变量 |
| [Web 控制台](../web/index.md) | 控制台展示和管理的内容 |

## 应用开发者 {#application-developers}

应用开发者使用 Project API 密钥调用 API，也可以在自己的机器上运行 Session。

| 指南 | 内容 |
| --- | --- |
| [快速开始](quickstart.md) | 从 Project API 密钥到完成一个 Session |
| [Agents API 指南](../api/public-agent-api.md) | 常见任务、Harness 与模型选择，以及各资源的 SDK 和 HTTP 示例 |
| [自托管执行](self-hosted.md) | 在自己的机器上运行 Session |
| [示例](../examples.md) | 基于 API 构建的完整应用 |
| [API 索引](../api/index.md) | 三个命名空间及其凭据 |

贡献者从[开发指南](../development.md)和 [CONTRIBUTING.md](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md)开始。
