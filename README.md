<div align="center">

![OpenAgentCore — One core. Many agents.](docs/assets/openagentcore-banner.jpeg)

# OpenAgentCore

The OpenAI Agents API, open source and self-hosted. Run Codex, Claude Code or MiniMax Code on your own machines with your own models, through the official OpenAI SDK.

[![Release](https://img.shields.io/github/v/release/MiniMax-AI/OpenAgentCore)](https://github.com/MiniMax-AI/OpenAgentCore/releases/latest) [![License](https://img.shields.io/github/license/MiniMax-AI/OpenAgentCore)](LICENSE)

[Website](https://openagentcore.dev/) · [Documentation](https://openagentcore.dev/docs/getting-started/) · [Quickstart](#quickstart) · [Contributing](CONTRIBUTING.md)

**English** · [简体中文](README.zh-CN.md) · [日本語](README.ja.md)

</div>

- **Same API as OpenAI.** Set `OPENAI_BASE_URL` and the [official OpenAI SDK](https://developers.openai.com/api/docs/libraries), or plain HTTP, works unchanged.
- **Your agent, your model.** Each Session runs Codex, Claude Code or MiniMax Code natively, with the model provider you configure. See [Harness capabilities](https://openagentcore.dev/contracts/agents-api/harness-capabilities).
- **Compute that sleeps, workspaces that stay.** On microsandbox with independent workspace storage, idle sandboxes release compute and resume on a compatible node with their files intact. E2B, Docker and your own Linux, macOS or Windows machine also run Sessions. See [Sandbox Provider protocol](https://openagentcore.dev/docs/sandbox-provider).
- **Replaceable parts.** Sandbox Providers, workspace filesystems, Harnesses and model providers plug in through [defined protocols](AGENTS.md#protocols-at-every-boundary).

![Deployment overview](docs/assets/console-overview-en.webp)

## Quickstart

Install Core, Web and PostgreSQL on Linux or macOS with [Docker](https://openagentcore.dev/docs/getting-started/install#prerequisites):

```sh
curl -fsSL https://openagentcore.dev/install.sh | bash
```

On Windows, in PowerShell:

```powershell
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

In Web, sign in with the Core key the installer prints, configure the public address, set a default model, issue a Project API key and add sandbox capacity (a node or E2B). [Install Core and Web](https://openagentcore.dev/docs/getting-started/install) covers each step.

Then call it with the OpenAI SDK, with `OPENAI_BASE_URL` set to your API base URL (`https://core.example/v1`) and `OPENAI_API_KEY` to the Project API key:

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

[Run your first Session](https://openagentcore.dev/docs/getting-started/quickstart) waits for the result and reads it back.

## How it works

![OpenAgentCore architecture](docs/assets/architecture.svg)

Applications and operators use these Core APIs:

| API | Path | Used by |
| --- | --- | --- |
| **[Agents API](https://openagentcore.dev/docs/api/public-agent-api)** | `/v1` | Your applications. Same protocol as [OpenAI's Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview) |
| **[Core API](https://openagentcore.dev/contracts/agents-api/admin-api)** | `/core/v1` | Operators, through Web |

Core keeps durable execution state. The Runtime runs the chosen Harness inside the Environment. Each connection is a defined protocol, so any part can be replaced on its own. See [Architecture](https://openagentcore.dev/docs/architecture).

## Documentation

| I want to | Start with |
| --- | --- |
| Install and operate an installation | [Install Core and Web](https://openagentcore.dev/docs/getting-started/install), then [Operations](https://openagentcore.dev/docs/getting-started/operations) |
| Build an application on the API | [Run your first Session](https://openagentcore.dev/docs/getting-started/quickstart), then the [Agents API guide](https://openagentcore.dev/docs/api/public-agent-api) |
| See a complete application | [Examples](https://openagentcore.dev/docs/examples) |
| Run agents on my own machine | [Run self-hosted Sessions](https://openagentcore.dev/docs/getting-started/self-hosted) |
| Check Harness capabilities and limits | [Harness capabilities](https://openagentcore.dev/contracts/agents-api/harness-capabilities) |
| Understand the design | [Architecture](https://openagentcore.dev/docs/architecture) |
| Add a sandbox, Harness or other component | [Develop OpenAgentCore](https://openagentcore.dev/docs/development) |

## Contributing and license

Read [CONTRIBUTING.md](CONTRIBUTING.md) before changing code. Report bugs and ask questions in [GitHub Issues](https://github.com/MiniMax-AI/OpenAgentCore/issues). Released under the [MIT License](LICENSE).
