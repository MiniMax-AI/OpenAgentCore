<div align="center">

![OpenAgentCore — One core. Many agents.](docs/assets/openagentcore-banner.jpeg)

# OpenAgentCore

An open-source, self-hosted implementation of the OpenAI Agents API with multiple native harnesses.

[Website](https://openagentcore.dev/) · [Install](#install) · [Call the API](https://openagentcore.dev/docs/getting-started/quickstart) · [Documentation](https://openagentcore.dev/docs/getting-started/) · [Contributing](CONTRIBUTING.md)

**English** · [简体中文](README.zh-CN.md)

</div>

## What it is

OpenAgentCore runs AI agents on your own infrastructure behind the [OpenAI Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview).

- **Same API as OpenAI.** Point the [OpenAI Agent API](https://developers.openai.com/api/docs/guides/agents/sdk), or plain HTTP, at your installation. No new client to learn.
- **Your choice of agent.** Each [Session](https://openagentcore.dev/docs/api/public-agent-api) runs a [native harness](https://openagentcore.dev/contracts/agents-api/harness-onboarding): [Codex](https://github.com/openai/codex), [Claude Code](https://code.claude.com/docs/en/overview) or [MiniMax Code](https://github.com/MiniMax-AI/minimax-code), with the [model provider you configure](https://openagentcore.dev/contracts/agents-api/model-execution).
- **Your choice of machine.** Agents work in a [managed sandbox](https://openagentcore.dev/contracts/agents-api/sandbox-deployment) ([Docker](https://www.docker.com/), [microsandbox](https://github.com/zerocore-ai/microsandbox) or [E2B](https://e2b.dev/)), or on your own Linux, macOS or Windows machine.
- **Every part is replaceable.** [Sandboxes](https://openagentcore.dev/docs/sandbox-provider), [harnesses](https://openagentcore.dev/contracts/agents-api/harness-onboarding) and [model providers](https://openagentcore.dev/contracts/agents-api/model-execution) plug in through [defined protocols](AGENTS.md#protocols-at-every-boundary).

## How it fits together

![OpenAgentCore architecture](docs/assets/architecture.png)

Applications and operators use these Core APIs:

| API | Path | Used by |
| --- | --- | --- |
| **[Agents API](https://openagentcore.dev/docs/api/public-agent-api)** | `/v1` | Your applications. Same protocol as [OpenAI's Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview) |
| **[Core API](https://openagentcore.dev/contracts/agents-api/admin-api)** | `/core/v1` | Operators, through Web |

Core keeps durable execution state. The Runtime runs the chosen harness inside the Environment. Each connection is a defined protocol, so any part can be replaced on its own. See the [architecture guide](https://openagentcore.dev/docs/architecture).

## Screenshots

| Overview | Agent metrics |
| --- | --- |
| ![Deployment overview](docs/assets/console-overview-en.webp) | ![Agent metrics](docs/assets/console-agent-metrics-en.webp) |

## Install

On Linux or macOS with [Docker configured](https://openagentcore.dev/docs/getting-started/install#prerequisites):

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

Then:

1. **Sign in to Web**, the admin console, with the Core key the installer created, and **configure the domain and HTTPS**.
2. **Set a default model** and **issue a Project API key**.
3. **Add execution capacity:** a node, E2B, or your own machine.
4. **[Run your first Session](https://openagentcore.dev/docs/getting-started/quickstart)** with the OpenAI SDK.

The [installation guide](https://openagentcore.dev/docs/getting-started/install) covers each step, HTTPS and a quick local trial. Listen addresses, ports and other options: [installation options](https://openagentcore.dev/docs/getting-started/install-options).

## Documentation

| I want to | Start with |
| --- | --- |
| Install and operate an installation | [Installation](https://openagentcore.dev/docs/getting-started/install), then [operations](https://openagentcore.dev/docs/getting-started/operations) |
| Build an application on the API | [Quickstart](https://openagentcore.dev/docs/getting-started/quickstart), then the [Agents API guide](https://openagentcore.dev/docs/api/public-agent-api) |
| See a complete application | [Examples](https://openagentcore.dev/docs/examples) |
| Run agents on my own machine | [Self-hosted execution](https://openagentcore.dev/docs/getting-started/self-hosted) |
| Check Harness capabilities and limits | [Harness capabilities](https://openagentcore.dev/contracts/agents-api/harness-capabilities) |
| Understand the design | [Architecture](https://openagentcore.dev/docs/architecture) |
| Add a sandbox, harness or other component | [Developer guide](https://openagentcore.dev/docs/development) |

All pages: [documentation index](https://openagentcore.dev/docs/getting-started/). Before changing code, read the [contributor rules](CONTRIBUTING.md).
