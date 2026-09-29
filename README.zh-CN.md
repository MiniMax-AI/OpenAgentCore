<div align="center">

![Open AgentCore 红色像素字标](docs/assets/openagentcore-banner.png)

# OpenAgentCore

通过同一套 API，在你自己的基础设施上运行 Codex、Claude Code 和 MiniMax Code。

[快速开始](#快速开始) · [文档](#文档) · [调用 API](docs/getting-started/quickstart.md) · [参与贡献](CONTRIBUTING.md)

[English](README.md) · **简体中文**

</div>

通过一套 Agents API 运行原生 Codex、Claude Code 和 MiniMax Code。Core 管理 Session
和执行状态；daemon 在托管沙箱或用户连接的机器上准备能力、运行所选 Harness。
Web 提供管理员控制台。

## 快速开始

1. 在 Linux 主机上[安装 Core 和 Web](docs/getting-started/install.md)。安装指南包含环境要求、
   发行包下载、本地试用和 HTTPS 配置。
2. 用安装器生成的 Core key 登录 Web，配置模型提供方，创建 Project 并签发应用 API key。
   使用托管环境时，先[添加节点或配置 E2B](docs/getting-started/nodes.md)。
3. 跟随[第一个 Session 示例](docs/getting-started/quickstart.md)，安装指定版本的 Python SDK，
   验证访问、提交任务并等待执行结果。

要在自己的 Linux、macOS 或 Windows 机器上执行任务，请阅读
[自托管 Runtime 指南](docs/getting-started/self-hosted.md)。

## 组件关系

```text
应用 → Core API → 统一 daemon 协议 → Runtime → 原生 Harness
          │
          └→ Sandbox Provider → 创建 / 引导 / 回收 Environment
```

托管 Provider 提供 Linux 环境；自托管 daemon 支持 Linux、macOS 和 Windows，
具体组合见 [Harness 平台支持表](docs/self-hosted-native.md#platforms-and-prerequisites)。
daemon 使用启动账户的权限，隔离由外层沙箱负责。释放执行器不会销毁 Environment。

公开接口遵循固定版本的 OpenAI Agents API，已支持的操作和原生引擎差异见
[协议覆盖记录](contracts/agents-api/README.md)。

## 文档

正文统一使用英文；中英文 README 指向同一套文档。

| 入口 | 内容 |
| --- | --- |
| [文档目录](docs/getting-started/README.md) | 安装、使用和运维的阅读路径 |
| [使用指南](docs/user-guide.md) | Session、Skill/Plugin/MCP、文件、取消与恢复 |
| [配置参考](docs/configuration.md) | 部署设置和模型配置 |
| [API 参考](docs/api/README.md) | 应用、管理与机器接口 |
| [开发指南](docs/development.md) | 仓库结构、开发环境、构建与验证 |
| [接入 Harness](contracts/agents-api/harness-onboarding.md) | 适配器实现与验收 |
| [Core–Runtime 协议](docs/runtime-protocol.md) | 生命周期、能力准备与执行 |
| [接入 Sandbox Provider](docs/sandbox-provider.md) | 环境创建与资源管理 |

修改代码前请阅读[贡献规范](CONTRIBUTING.md)。
