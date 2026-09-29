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

Core manages Sessions and execution state. Runtime prepares Skills and MCP tools,
then runs the selected harness. Sandbox providers manage environments; model providers
serve inference requests. Web is the administrator console.

```text
Application → Core API → common daemon protocol → Runtime → Harness → Model Provider
                  │
                  └→ Sandbox Provider → create / bootstrap / reclaim Environment
```

Managed Providers supply Linux environments. Self-hosted daemons run on Linux,
macOS and Windows, subject to the selected Harness's
[platform support](docs/self-hosted-native.md#platforms-and-prerequisites). The daemon uses
its starting account's permissions; isolation belongs to an outer sandbox.
Releasing an executor does not destroy its Environment.

See the [coverage record](contracts/agents-api/README.md) for supported API operations
and native harness differences.

## Documentation

| Start here | Purpose |
| --- | --- |
| [Documentation index](docs/getting-started/README.md) | Installation, usage and administration reading paths |
| [User guide](docs/user-guide.md) | Sessions, Skills/Plugins/MCP, files, cancellation and recovery |
| [Configuration](docs/configuration.md) | Operator settings and model configuration |
| [API reference](docs/api/README.md) | Application, administration and machine APIs |
| [Developer guide](docs/development.md) | Repository map, local setup, builds and validation |
| [Add a Harness](contracts/agents-api/harness-onboarding.md) | Start with [agent/harness.go](apps/parsar-daemon/internal/agent/harness.go), then adapter implementation and acceptance |
| [Core–Runtime protocol](docs/runtime-protocol.md) | Lifecycle, capability preparation and execution |
| [Add a Sandbox Provider](docs/sandbox-provider.md) | Environment creation and resource ownership |

Before changing code, read [the contributor rules](CONTRIBUTING.md).
