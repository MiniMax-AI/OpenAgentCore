# OpenAgentCore documentation

OpenAgentCore runs AI agents on your own infrastructure behind the OpenAI Agents API. The [architecture overview](../architecture.md) explains its parts. Pick the guides for your role.

## Operators

Operators install Core and Web, add sandbox capacity and issue Project API keys.

| Guide | Covers |
| --- | --- |
| [Install Core and Web](install.md) | The default installation, from an empty host to the first Project API key |
| [Installation options](install-options.md) | Installer flags, existing reverse proxies, split and native deployments, offline hosts |
| [Nodes](nodes.md) | Adding, changing, removing and troubleshooting sandbox nodes |
| [Operations](operations.md) | The `oac` command, the Core key, backups, uninstall, upgrades and troubleshooting |
| [Configuration reference](../configuration.md) | Every setting, file and environment variable |
| [Web console](../web/README.md) | What the console shows and manages |

## Application developers

Application developers call the API with a Project API key, and can run Sessions on machines they own.

| Guide | Covers |
| --- | --- |
| [Quickstart](quickstart.md) | From a Project API key to a finished Session |
| [Agents API guide](../api/public-agent-api.md) | Common tasks, harness and model choice, and every resource with SDK and HTTP examples |
| [Self-hosted execution](self-hosted.md) | Running a Session on your own machine |
| [Examples](../examples.md) | Complete applications built on the API |
| [API index](../api/README.md) | All three namespaces and their credentials |

Contributors start with the [developer guide](../development.md) and [CONTRIBUTING.md](../../CONTRIBUTING.md).
