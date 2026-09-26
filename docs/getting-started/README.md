# Getting started

Parsar Core is self-deployed, open-source Agents API infrastructure. It provides
an execution API and an optional Web console, with native harnesses behind one
Runtime contract. The Parsar product is not required.

- [Install Core and Web](install.md)
- [Configuration reference](../configuration.md)
- [Public API and Web management reference](../api/README.md)
- [Call the API](quickstart.md)
- [Operate the installation](operations.md)
- [Protocol coverage and native differences](https://github.com/MiniMax-AI/parsar-core/blob/main/contracts/agents-api/README.md)

Core and Web ship together. The default installation runs Core, Web and PostgreSQL
and selects Docker sandboxes, with zero execution nodes; `--sandbox microsandbox`,
`--sandbox e2b` or `--sandbox none` choose otherwise. Add nodes, the Core host
included, with **Add node** on the Nodes page in Web; nodes need an HTTPS public URL
such as `--public-url https://core.example`. Core then creates the execution sandbox
when a Session needs it. Installing the service does not require a model key or run
a model request.

The Web console is a client of Core. API users work with Agents, Sessions and
Environments; operators also maintain the host, provider, Runtime images and
durable storage. Those operational needs extend beyond the hosted OpenAI
Platform experience, without redefining its public resource semantics.
