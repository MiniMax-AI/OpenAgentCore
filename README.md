<div align="center">

![Open AgentCore red pixel wordmark](docs/assets/openagentcore-banner.png)

# OpenAgentCore

Run Codex, Claude Code and MiniMax Code through one API, on infrastructure you control.

[Get started](#quick-start) · [Documentation](#documentation) · [Call the API](docs/getting-started/quickstart.md) · [Contributing](CONTRIBUTING.md)

**English** · [简体中文](README.zh-CN.md)

</div>

Run native Codex, Claude Code and MiniMax Code through one Agents API. Core owns
Sessions and execution state; a daemon prepares capabilities and runs the selected
Harness on a managed sandbox or a machine you connect yourself. Web is the
administrator console.

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

```text
Application → Core API → common daemon protocol → Runtime → native Harness
                  │
                  └→ Sandbox Provider → create / bootstrap / reclaim Environment
```

Managed Providers supply Linux environments. Self-hosted daemons run on Linux,
macOS and Windows, subject to the selected Harness's
[platform support](docs/self-hosted-native.md#platforms-and-prerequisites). The daemon uses
its starting account's permissions; isolation belongs to an outer sandbox.
Releasing an executor does not destroy its Environment.

The public contract follows the pinned OpenAI Agents API. See the
[coverage record](contracts/agents-api/README.md) for supported operations and
native differences. Core is independent of the Parsar product and its database.

## Documentation

All documentation is in English; both README editions use the same sources.

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
