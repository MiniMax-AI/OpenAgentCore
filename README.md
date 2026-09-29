<div align="center">

![Open AgentCore red pixel wordmark](docs/assets/openagentcore-banner.png)

# OpenAgentCore

An open-source, self-hosted implementation of the OpenAI Agents API with multiple native harnesses.

[Get started](#quick-start) · [Documentation](#documentation) · [Call the API](docs/getting-started/quickstart.md) · [Contributing](CONTRIBUTING.md)

**English** · [简体中文](README.zh-CN.md)

</div>

Choose Codex, Claude Code or MiniMax Code as your execution engine. Sandbox providers,
model providers and harnesses connect through defined protocols and thin adapters,
so you can add or replace components without changing Core orchestration.

## Quick start

On a Linux amd64 host with Docker and Python 3.9+, install the latest stable release:

```sh
curl -fsSL https://github.com/MiniMax-AI/parsar-core/releases/latest/download/install.sh | bash
```

You can customize the listen address, ports and deployment settings with these [installation options](docs/getting-started/install-options.md).

1. [Install Core and Web](docs/getting-started/install.md) on a Linux host. The guide
   covers prerequisites, release download, local trials and HTTPS setup.
2. Sign in to Web with the installer-created Core key. Configure a model provider,
   create a Project and issue its API key. For managed execution,
   [add a node or configure E2B](docs/getting-started/nodes.md).
3. [Run your first Session](docs/getting-started/quickstart.md) with the pinned Python
   SDK. The walkthrough checks authentication, submits a task and waits for its result.

To execute on your own Linux, macOS or Windows machine, follow the
[self-hosted Runtime guide](docs/getting-started/self-hosted.md).

## How it fits together

![OpenAgentCore architecture](docs/assets/architecture.png)

Core exposes two APIs. Applications use the public **Agents API** (`/v1`), the same
protocol as OpenAI's. Operators use the private **Core API** (`/core/v1`) through
Web. Core manages Sessions and execution state. Runtime prepares Skills and MCP tools,
then runs the selected harness. Sandbox providers manage environments; model providers
serve inference requests. Each connection is a defined protocol, so any component
can be replaced on its own.

Managed Providers supply Linux environments. Self-hosted daemons run on Linux,
macOS and Windows, subject to the selected Harness's
[platform support](docs/self-hosted-native.md#platforms-and-prerequisites). The daemon uses
its starting account's permissions; isolation belongs to an outer sandbox.
Releasing an executor does not destroy its Environment.

See the [coverage record](contracts/agents-api/README.md) for supported API operations
and native harness differences.

## Documentation

For a complete application example using the Parsar product UI, see
[`example/parsar`](example/parsar/README.md): model, Skill, MCP and runtime management, reusable Agents, and independent
Sessions backed by the public API.

| Start here | Purpose |
| --- | --- |
| [Documentation index](docs/getting-started/README.md) | Installation, usage and administration reading paths |
| [Architecture](docs/architecture.md) | Components, the three API namespaces and a Session end to end |
| [User guide](docs/user-guide.md) | Sessions, Skills/Plugins/MCP, files, cancellation and recovery |
| [Configuration](docs/configuration.md) | Operator settings and model configuration |
| [API reference](docs/api/README.md) | Application, administration and machine APIs |
| [Developer guide](docs/development.md) | Repository map, local setup, builds and validation |
| [Add a Harness](contracts/agents-api/harness-onboarding.md) | Start with [agent/harness.go](apps/parsar-daemon/internal/agent/harness.go), then adapter implementation and acceptance |
| [Core–Runtime protocol](docs/runtime-protocol.md) | Lifecycle, capability preparation and execution |
| [Add a Sandbox Provider](docs/sandbox-provider.md) | Environment creation and resource ownership |

Before changing code, read [the contributor rules](CONTRIBUTING.md).
