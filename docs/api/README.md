# API namespaces and credentials

Core serves three namespaces. Each has one kind of caller and its own credential, and a credential works only in its own namespace.

| Namespace | Caller | Credential | Contents | Owner |
| --- | --- | --- | --- | --- |
| `/v1` | Applications: business systems and the official OpenAI SDK | Project API key | Exactly the 58 method and path pairs of the pinned official Agents API, listed in [upstream-routes.json](../../contracts/agents-api/upstream-routes.json). Core-only fields sit inside `x_agents_core`: `harness`, `model_provider`, `harness_config`, `environment`, and the read-only Session `installation` | [Agents API guide](public-agent-api.md) |
| `/core/v1` | Web's console server and operator scripts | [Core key](../getting-started/operations.md#core-key) | Installation facts, Projects and keys, resource reads and deletion, Session archive, executor credentials, default models, metrics, audit, sandbox deployment and nodes | [Core administration API](../../contracts/agents-api/admin-api.md) |
| `/api/v1` | Nodes, Runtime daemons, self-hosted executors and their installers | Machine credentials: node enrollment tokens and node credentials, installation grants, executor credentials, and daemon credentials. Each works only on its own routes | Machine bootstrap and connections under `/api/v1/sandbox-node/*` and `/api/v1/agent-daemon/*`, including WebSockets, and the public native installer downloads | [Machine connection API](#machine-connection-api) |

A credential used in another namespace gets 401: a Project API key on `/core/v1` or `/api/v1`, the Core key on `/v1` or `/api/v1`. How Projects and keys behave is in [Projects own assets](../design-principles.md#projects-own-assets).

**Routing.** The reverse proxy sends `/v1` and `/api/v1` to Core and everything else to Web ([proxy setup](../getting-started/install-options.md#https-and-the-reverse-proxy)). Browsers reach `/core/v1` only through Web's console server, which adds the Core key after sign-in and answers 404 for `/v1` and `/api/v1` ([console server](../web/console-server.md)). Operator scripts call `/core/v1` on Core's loopback port ([script the Core API](../getting-started/operations.md#script-the-core-api)).

## Machine connection API

These routes are under `/api/v1`. Each accepts only the credential listed, never the Core key or a Project API key. The generated [machine OpenAPI](../../contracts/agents-api/runtime.openapi.yaml) covers the annotated node HTTP and installation grant routes; the node WebSocket `connect` route is described in the node generation protocol.

| Routes | Caller | Credential | Contract |
| --- | --- | --- | --- |
| `POST sandbox-node/enroll` | Node installer | One-use enrollment token from `POST /core/v1/sandbox/enrollment-tokens` | [Node routes](../../contracts/agents-api/sandbox-deployment.md#authority-and-routes) |
| `GET sandbox-node/configuration` | Node installer and node | Enrollment token, or node credential with `X-OAC-Node-ID` | [Node routes](../../contracts/agents-api/sandbox-deployment.md#authority-and-routes) |
| `GET sandbox-node/identity`, WebSocket `GET sandbox-node/connect` | Node | Node credential registered at enrollment | [Node routes](../../contracts/agents-api/sandbox-deployment.md#authority-and-routes), [node generation protocol](../../contracts/agents-api/node-generation-protocol.md) |
| `GET agent-daemon/install/{version}/*` | Self-hosted installer | None: public, immutable release content | [Installation grant](../../contracts/agents-api/environment-executor-credentials.md#installation-grant) |
| `POST agent-daemon/installation`, `POST agent-daemon/installation/claim` | Self-hosted installer | Installation grant: the short-lived authorization in a `self_hosted` Session's `x_agents_core.installation` commands | [Installation grant](../../contracts/agents-api/environment-executor-credentials.md#installation-grant) |
| `POST agent-daemon/enroll`, `GET agent-daemon/connection` | Self-hosted executor and its installer | Executor credential | [Executor credentials](../../contracts/agents-api/environment-executor-credentials.md) |
| WebSocket `GET agent-daemon/ws`, `POST agent-daemon/bootstrap`, `GET agent-daemon/device-status` | Runtime daemons | Daemon credential: Core issues one to each hosted sandbox through the [bootstrap file](../runtime-bootstrap.md); a self-hosted executor uses its executor credential; a device for `none` Sessions uses the credential an operator provisions with `oac-core-device` | [Core–Runtime protocol](../runtime-protocol.md), [device provisioning](../../services/core/README.md#internal-execution-device-connection) |
