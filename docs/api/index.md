---
title: "API namespaces and credentials"
---

Core serves three namespaces. Each has one kind of caller and its own credential, and a credential works only in its own namespace.

| Namespace | Caller | Credential | Contents | Owner |
| --- | --- | --- | --- | --- |
| `/v1` | Applications: business systems and the official OpenAI SDK | Project API key | Exactly the 58 method and path pairs of the pinned official Agents API, listed in [upstream-routes.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream-routes.json). Core-only fields sit inside `x_agents_core`: `harness`, `model_provider`, `harness_config`, `environment`, and the read-only Session `installation` | [Agents API guide](./public-agent-api.md) |
| `/core/v1` | Web's console server and operator scripts | [Core key](../getting-started/operations.md#core-key) | Installation facts, Projects and keys, resource reads and deletion, Session archive, executor credentials, default models, metrics, audit, sandbox deployment and nodes | [Core administration API](../../contracts/agents-api/admin-api.md) |
| `/api/v1` | Nodes, Runtime daemons, self-hosted executors and their installers | Machine credentials: node enrollment tokens and node credentials, installation grants, executor credentials, and daemon credentials. Each works only on its own routes | Machine bootstrap and connections under `/api/v1/sandbox-node/*` and `/api/v1/agent-daemon/*`, including WebSockets, and the public native installer downloads | [Machine connection API](../../contracts/agents-api/machine-api.md) |

A credential used in another namespace gets 401: a Project API key on `/core/v1` or `/api/v1`, the Core key on `/v1` or `/api/v1`. How Projects and keys behave is in [Projects own assets](../concepts.md#projects-own-assets).

**Routing.** The reverse proxy sends `/v1` and `/api/v1` to Core and everything else to Web ([proxy setup](../getting-started/install-options.md#https-and-the-reverse-proxy)). Browsers reach `/core/v1` only through Web's console server, which adds the Core key after sign-in and answers 404 for `/v1` and `/api/v1` ([console server](../web/console-server.md)). Operator scripts call `/core/v1` on Core's loopback port ([script the Core API](../getting-started/operations.md#script-the-core-api)).

**API reference page.** Core serves a read-only Swagger UI of all three namespaces at `/docs`, and the generated documents it renders at `/docs/openapi.yaml`, `/docs/core.openapi.yaml` and `/docs/runtime.openapi.yaml`. These routes need no credential, and the page sends no API requests. The reverse proxy does not route `/docs` to Core, so open it on Core's own address: on the Core host, `http://127.0.0.1:<port>/docs`, where the port is [`ports.core`](../configuration.md#settings), 8091 by default. The browser loads Swagger UI from `unpkg.com`.

## Machine connection API

Nodes, Runtime daemons and the self-hosted installer call `/api/v1` with their own credentials. The [machine connection API](../../contracts/agents-api/machine-api.md) lists every route, caller and credential.
