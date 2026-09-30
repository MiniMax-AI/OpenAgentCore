# OpenAgentCore documentation

OpenAgentCore runs AI agents on your own infrastructure behind the OpenAI Agents
API. Pick the path that matches your role. New here? Read the
[architecture overview](../architecture.md) first.

## Operators: install and run

1. [Install Core and Web](install.md) and sign in.
2. Add execution capacity: [managed nodes or E2B](nodes.md), or
   [your own machine](self-hosted.md).
3. Issue a Project API key and hand it to the application developer.

| Guide | Covers |
| --- | --- |
| [Installation](install.md) | Prerequisites, HTTPS, first sign-in, advanced options |
| [Configuration](../configuration.md) | `config.json`, default models, sandbox deployment |
| [Nodes](nodes.md) | Adding, checking and removing managed nodes |
| [Self-hosted execution](self-hosted.md) | Connecting your own machine to a Session |
| [Operations](operations.md) | Services, backups, keys, repair and troubleshooting |
| [Web console](../web/README.md) | What the console shows and manages |

## Application developers: build on the API

| Guide | Covers |
| --- | --- |
| [Quickstart](quickstart.md) | From a Project API key to a finished Session |
| [User guide](../user-guide.md) | Common tasks: harness and model choice, follow-ups, capabilities, files, cancel |
| [Agents API guide](../api/public-agent-api.md) | Every resource, with SDK and HTTP examples |
| [Examples](../examples.md) | Complete applications built on the API |
| [API index](../api/README.md) | All three namespaces and their credentials |

## Contributors: extend Core

Start with the [developer guide](../development.md): repository map, local setup,
and how to add a Sandbox Provider, a harness or a Runtime feature. Repository rules
are in [CONTRIBUTING.md](../../CONTRIBUTING.md).
