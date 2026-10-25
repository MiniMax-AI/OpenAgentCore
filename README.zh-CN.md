<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/openagentcore-wordmark-dark.svg">
  <img alt="OpenAgentCore" src="docs/assets/openagentcore-wordmark-light.svg" width="440">
</picture>

开源、可自部署的 OpenAI Agents API。用官方 OpenAI SDK，在你自己的机器上用你自己的模型运行 Codex、Claude Code 或 MiniMax Code。

[![Release](https://img.shields.io/github/v/release/MiniMax-AI/OpenAgentCore)](https://github.com/MiniMax-AI/OpenAgentCore/releases/latest) [![License](https://img.shields.io/github/license/MiniMax-AI/OpenAgentCore)](LICENSE)

[官网](https://openagentcore.dev/zh/) · [文档](https://openagentcore.dev/zh/docs/getting-started/) · [快速开始](#快速开始) · [参与贡献](CONTRIBUTING.md)

[English](README.md) · **简体中文**

</div>

- **与 OpenAI 相同的 API。** 设置 `OPENAI_BASE_URL` 后，[官方 OpenAI SDK](https://developers.openai.com/api/docs/libraries)或直接的 HTTP 调用无需改动即可使用。
- **你的 Agent，你的模型。** 每个 Session 原生运行 Codex、Claude Code 或 MiniMax Code，使用你配置的模型提供商，与在本机运行一致。详见 [Harness 能力](https://openagentcore.dev/zh/contracts/agents-api/harness-capabilities)。
- **计算会休眠，工作区会保留。** 在 microsandbox 上（每个工作区都保存在独立存储中），空闲沙箱释放计算资源，并在兼容节点上带着文件恢复。E2B 会就地暂停空闲沙箱。Docker 以及你自己的 Linux、macOS 或 Windows 机器同样可以运行 Session。详见[沙箱提供商协议](https://openagentcore.dev/zh/docs/sandbox-provider)。
- **部件可替换。** 沙箱提供商、工作区文件系统、Harness 和模型提供商都通过[既定协议](AGENTS.md#protocols-at-every-boundary)接入。

## 性能

单个 microsandbox 节点上的沙箱生命周期，并以 E2B 的创建、暂停和恢复作为对照。2026-10-25 测得，每项为 7 到 30 个样本的中位数；每一行都链接到测量方法和原始记录，[`scripts/benchmark.py`](scripts/benchmark.py) 可以复现这些数据。

| 操作 | OpenAgentCore（microsandbox） | E2B | 原始记录 |
| --- | --- | --- | --- |
| [创建沙箱](https://openagentcore.dev/zh/docs/performance#sandbox-start) | 1.22 s | 0.60 s | [core](docs/performance/core.jsonl), [e2b](docs/performance/e2b.jsonl) |
| [沙箱创建完成 → 开始初始化](https://openagentcore.dev/zh/docs/performance#sandbox-start) | 0.98 s | — | [core](docs/performance/core.jsonl) |
| [Environment 就绪，准备缓存命中 / 未命中](https://openagentcore.dev/zh/docs/performance#environment-ready) | 2.50 s / 7.35 s | — | [sessions](docs/performance/sessions.jsonl) |
| [满载节点上 Environment 就绪](https://openagentcore.dev/zh/docs/performance#environment-ready) | 5.90 s | — | [sessions](docs/performance/sessions.jsonl), [core](docs/performance/core.jsonl) |
| [挂起空闲沙箱](https://openagentcore.dev/zh/docs/performance#suspend-and-resume) | 3.33 s | 1.04 s | [core](docs/performance/core.jsonl), [e2b](docs/performance/e2b.jsonl) |
| [恢复沙箱；清空节点页缓存后恢复](https://openagentcore.dev/zh/docs/performance#suspend-and-resume) | 1.75 s / 1.99 s | 0.87 s | [core](docs/performance/core.jsonl), [cold](docs/performance/cold/core.jsonl), [e2b](docs/performance/e2b.jsonl) |
| [Turn 的首个文本，已挂起 / 运行中的 Session](https://openagentcore.dev/zh/docs/performance#suspend-and-resume) | 3.51 s / 1.26 s | — | [sessions](docs/performance/sessions.jsonl) |

E2B 时间为客户端上 SDK 调用的耗时，含 0.20 s 的 API 往返。OpenAgentCore 的沙箱时间由 Core 自己测量，经过 0.2 ms 的本地网络；Environment 就绪和首个文本由 Core 主机上的应用根据事件流计时，Core 以 100 ms 为间隔推送事件流。主机和版本见[测试环境](https://openagentcore.dev/zh/docs/performance#setup)，两边的差异见 [E2B](https://openagentcore.dev/zh/docs/performance#e2b)。

## 适用边界

- **Docker 是兼容选项**，用于可信负载和没有 KVM 的主机。容器共享节点内核，空闲沙箱不会挂起，文件保存在节点磁盘上。详见[选择沙箱提供商](https://openagentcore.dev/zh/docs/getting-started/sandbox-backends)。
- **每个工作区只有一个写入者。** 工作区同一时间只挂载到一个沙箱，存储只保留当前文件，没有文件历史或分叉。详见[工作区文件系统协议](https://openagentcore.dev/zh/docs/workspace-provider)。
- **工作区存储需要 Linux。** NFS 适配器使用 Core 主机和每个节点上的内核 NFS 挂载。详见[设置工作区存储](https://openagentcore.dev/zh/docs/getting-started/workspace-storage)。
- **挂起比 E2B 的暂停慢**：3.33 s 对 1.04 s。节点运行的沙箱已达上限时，新 Session 要等一个空闲沙箱挂起，因此 Environment 在 5.90 s 后才就绪，而不是 2.50 s。详见[挂起](https://openagentcore.dev/zh/docs/sandbox-provider#suspension)。

![部署概览](docs/assets/console-overview-zh.webp)

## 快速开始

在 Linux 或 macOS 上，按[前置条件](https://openagentcore.dev/zh/docs/getting-started/install#prerequisites)准备好 Docker 后，安装 Core、Web 和 PostgreSQL：

```sh
curl -fsSL https://openagentcore.dev/install.sh | bash
```

在 Windows 上，使用 PowerShell：

```powershell
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

在 Web 中，用安装程序输出的 Core 密钥登录，配置公共地址，设置默认模型，签发项目 API 密钥，并添加沙箱容量（节点或 E2B）。每一步见[安装 Core 和 Web](https://openagentcore.dev/zh/docs/getting-started/install)。

然后用 OpenAI SDK 调用，将 `OPENAI_BASE_URL` 设为你的 API 基础地址（`https://core.example/v1`），`OPENAI_API_KEY` 设为项目 API 密钥：

```python
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
session = client.beta.agents.sessions.create(
    environment={"type": "openai_hosted"},
    input="Create /workspace/hello.txt with a short greeting, then describe it.",
    extra_body={"agent": {"x_agents_core": {"harness": "codex"}}},
)
print(session.id)
```

[运行第一个 Session](https://openagentcore.dev/zh/docs/getting-started/quickstart)会等待结果并读回。

## 工作原理

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/architecture-dark.svg">
  <img alt="OpenAgentCore 架构" src="docs/assets/architecture.svg">
</picture>

应用和管理员使用以下 Core API：

| API | 路径 | 调用方 |
| --- | --- | --- |
| **[Agents API](https://openagentcore.dev/zh/docs/api/public-agent-api)** | `/v1` | 你的应用，与 [OpenAI 的 Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview) 协议一致 |
| **[Core API](https://openagentcore.dev/zh/contracts/agents-api/admin-api)** | `/core/v1` | 管理员，通过 Web 调用 |

持久化执行状态由 Core 保存；Runtime 在 Environment 中运行所选 Harness。各部件之间都通过既定协议连接，任何一个都可以单独替换。详见[架构](https://openagentcore.dev/zh/docs/architecture)。

## 文档

| 我想要 | 从这里开始 |
| --- | --- |
| 安装并运维 | [安装 Core 和 Web](https://openagentcore.dev/zh/docs/getting-started/install)，然后看[运维](https://openagentcore.dev/zh/docs/getting-started/operations) |
| 基于 API 开发应用 | [运行第一个 Session](https://openagentcore.dev/zh/docs/getting-started/quickstart)，然后看 [Agents API 指南](https://openagentcore.dev/zh/docs/api/public-agent-api) |
| 看一个完整的应用 | [示例](https://openagentcore.dev/zh/docs/examples) |
| 在自己的机器上运行 Agent | [自托管执行器](https://openagentcore.dev/zh/docs/getting-started/self-hosted) |
| 查看 Harness 能力和限制 | [Harness 能力](https://openagentcore.dev/zh/contracts/agents-api/harness-capabilities) |
| 了解设计 | [架构](https://openagentcore.dev/zh/docs/architecture) |
| 接入新的沙箱、Harness 或其他组件 | [开发 OpenAgentCore](https://openagentcore.dev/zh/docs/development) |

## 参与贡献与许可证

修改代码前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。缺陷和问题请提交到 [GitHub Issues](https://github.com/MiniMax-AI/OpenAgentCore/issues)。基于 [MIT 许可证](LICENSE)发布。
