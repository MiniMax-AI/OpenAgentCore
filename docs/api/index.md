---
title: "API namespaces and credentials"
---

Core serves three API namespaces, each for one kind of caller with its own credential: applications on `/v1`, administrators on `/core/v1`, and machines on `/api/v1`. A credential works only in its own namespace.

| Namespace | Caller | Credential | Contents | Owner |
| --- | --- | --- | --- | --- |
| `/v1` | Applications: business systems and the official OpenAI SDK | Project API key | Exactly the Agents API routes of the pinned OpenAI SDK, listed in [upstream-routes.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream-routes.json). Core-only fields sit inside `x_agents_core`; see [Core extensions](./public-agent-api.md#core-extensions) | [Agents API guide](./public-agent-api.md) |
| `/core/v1` | Web's console server and operator scripts | [Core key](../getting-started/operations.md#core-key) | Installation facts, Projects and keys, resource reads and deletion, Session archive, executor credentials, default models, metrics, request logs, audit, sandbox deployment and nodes, independent workspace storage configuration | [Core administration API](../../contracts/agents-api/admin-api.md) |
| `/api/v1` | Nodes, Runtime daemons, self-hosted executors and their installers | Machine credentials: node enrollment tokens and node credentials, installation grants, executor credentials, and daemon credentials. Each works only on its own routes | Machine bootstrap and connections under `/api/v1/sandbox-node/*` and `/api/v1/agent-daemon/*`, including WebSockets, and the public native installer downloads | [Machine connection API](../../contracts/agents-api/machine-api.md) |

A credential used in another namespace gets 401: a Project API key on `/core/v1` or `/api/v1`, the Core key on `/v1` or `/api/v1`. How Projects and keys behave is in [Projects own assets](../concepts.md#projects-own-assets).

**Routing.** Web [forwards the API paths to Core](../web/console-server.md#request-boundary). A signed-in browser reaches `/core/v1` through Web, which adds the Core key. Operator scripts call `/core/v1` inside Core's network namespace on the Core host ([script the Core API](../getting-started/operations.md#script-the-core-api)).

## API reference

Core serves a read-only Swagger UI of the three namespaces at `/docs`, and the documents at `/docs/openapi.yaml`, `/docs/core.openapi.yaml` and `/docs/runtime.openapi.yaml`. No credential is required, and the page sends no API requests. Open it on the console origin, for example `http://localhost:8080/docs`. The browser loads Swagger UI from `unpkg.com`.
