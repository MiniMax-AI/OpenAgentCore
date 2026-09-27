# OpenAgentCore documentation

OpenAgentCore is self-hosted, open-source Agents API infrastructure: Core serves the
OpenAI Agents API and runs native harnesses (Codex, Claude Code and MiniMax Code) in
sandboxes on your machines. Web is the administrator console; it issues the keys that
applications call Core with.

| Page | For | What it covers |
| --- | --- | --- |
| [Install Core and Web](install.md) | Administrators | Prerequisites, download, the installer and every option, HTTPS and the reverse proxy, a local trial, first sign-in, what is created on disk |
| [Nodes](nodes.md) | Administrators | Adding a node with one command, sudo and no-sudo modes, removal, logs and troubleshooting |
| [Self-hosted executors](self-hosted.md) | Administrators and application owners | Connecting an application's own machine to a `self_hosted` Session, rotating and revoking its credential |
| [Operations](operations.md) | Administrators | The `oac` command, the Core key, backups, upgrades and troubleshooting |
| [Configuration reference](../configuration.md) | Administrators | Every `config.json` setting and every runtime setting in Web |
| [Call the API](quickstart.md) | Application developers | `OPENAI_BASE_URL` and `OPENAI_API_KEY`, running a Session, model providers and `x_agents_core` |
| [API reference](../api/README.md) | Developers | The `/v1`, `/core/v1` and `/api/v1` namespaces and their contracts |

Deeper references: the
[nodes and sandbox backends](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md)
reference for operators, the
[protocol coverage and native differences](../../contracts/agents-api/README.md), and
[maintainers and advanced deployments](../maintainers.md) for building distributions
and running Core without the installer.
