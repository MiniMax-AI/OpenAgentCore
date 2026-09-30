# API documentation

Core serves three namespaces. Each has one kind of caller and its own credential;
no credential works in another namespace. Versioned native installer downloads are
public release content.

| Namespace | Caller | Credential | Contents | Reference |
| --- | --- | --- | --- | --- |
| `/v1` | Applications (business systems, SDKs) | Project API key | Exactly the pinned official Agents API routes. Core-only fields live only in `x_agents_core` (`harness`, `model_provider`, `harness_config`, Session `installation`) | [Agents API guide](public-agent-api.md) |
| `/core/v1` | Core Web's server and operator scripts | [Core key](../getting-started/operations.md#core-key) | Installation facts, Projects and keys, resource reads and deletion, Session archive, credential issuance, metrics, audit, sandbox deployment and nodes, deployment model providers | [Core API](#core-api), [Web API](web-management.md), [Core OpenAPI](../../contracts/agents-api/core.openapi.yaml) |
| `/api/v1` | Nodes, Runtime daemons, self-hosted executors | Machine credentials: short-lived Session installation grants, node enrollment tokens and executor credentials issued through `/core/v1` or claimed by installation, node credentials registered with an enrollment token, and daemon credentials Core issues for hosted sandboxes | Machine bootstrap and connections: `/api/v1/sandbox-node/*` and `/api/v1/agent-daemon/*`, including WebSockets; each credential works only on its own routes | [Node operations](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#register-a-host), [executor credentials](../../contracts/agents-api/environment-executor-credentials.md), [machine OpenAPI](../../contracts/agents-api/runtime.openapi.yaml) |

A credential used in another namespace gets 401: a Project API key on `/core/v1` or
`/api/v1`, the Core key on `/v1` or `/api/v1`. How Projects and keys behave is in
[Projects own assets](../design-principles.md#projects-own-assets).

**Routing.** The reverse proxy sends `/v1` and `/api/v1` to Core and everything else
to Web ([proxy setup](../getting-started/install.md#https-and-the-reverse-proxy)).
Browsers reach `/core/v1` only through Web's server, which adds the Core key after
sign-in; Web returns 404 for `/v1` and `/api/v1`. Operator scripts call `/core/v1`
on Core's loopback port. Details: [Web and Core](web-management.md).

## Public API

Applications call `/v1` with a Project API key. The routes are exactly the 58 pairs
in [upstream-routes.json](../../contracts/agents-api/upstream-routes.json). The
[Agents API guide](public-agent-api.md) explains every resource with SDK and HTTP
examples.

## Core API

All routes are under `/core/v1`. Core Web's server and operator scripts call them
with the Core key. Most administrators use Web instead; call the API directly for
automation.

### Common tasks

Run these on the Core host, against Core's loopback port. The helper reads the key
from its file, keeping it off the command line:

```sh
core() {  # core METHOD PATH [JSON body]
  curl -fsS -X "$1" "http://127.0.0.1:8091/core/v1$2" \
    -H @<(printf 'Authorization: Bearer %s\n' "$(cat ~/.oac/core/secrets/core.key)") \
    -H 'Content-Type: application/json' ${3:+-d "$3"}
}
```

| Task | Command |
| --- | --- |
| List Projects | `core GET /projects` |
| Create a Project | `core POST /projects '{"name": "billing-bot"}'` |
| Issue an API key (shown once, as `key`) | `core POST /projects/$PROJECT_ID/keys '{"name": "prod"}'` |
| Revoke a key | `core DELETE /projects/$PROJECT_ID/keys/$KEY_ID` |
| Archive a Project (revokes all keys) | `core POST /projects/$PROJECT_ID/archive` |
| See harnesses and their default models | `core GET /harnesses` |
| Set Codex's default model | `core PUT /harnesses/codex/model-configuration '{"model": "your-model-id", "model_provider": {"protocol": "responses", "base_url": "https://provider.example/v1", "api_key": "sk-..."}}'` |
| Issue an executor credential | See [executor credentials](../../contracts/agents-api/environment-executor-credentials.md#core-key-routes) |
| Installation facts, including the API base URL | `core GET /installation` |

Errors use the [Core error envelope](../../contracts/agents-api/core-errors.md).

### All routes

| Routes | Contents | Contract |
| --- | --- | --- |
| `projects`, `projects/{project_id}[/archive]`, `projects/{project_id}/keys[/{key_id}]` | Projects and their API keys | [Administrator contract](../../contracts/agents-api/admin-api.md) |
| `projects/{project_id}/{agents,environment-templates,skills,files,vaults,sessions}/**` | Resource reads and deletion, Session history, artifacts and archive | [Administrator contract](../../contracts/agents-api/admin-api.md) |
| `projects/{project_id}/sessions/{session_id}/execution-configuration` | Committed harness and model selection | [Execution configuration](../../contracts/agents-api/execution-configuration.md) |
| `projects/{project_id}/sessions/{session_id}/diagnostics`, `projects/{project_id}/sessions/{session_id}/turns/{turn_id}/diagnostics` | Root failure categories and Item receipt timing | [Session diagnostics](../../contracts/agents-api/session-diagnostics.md) |
| `projects/{project_id}/sessions/{session_id}/runtime-observation`, `sandbox/runtime-observations` | Current Runtime observations | [Runtime observations](../../contracts/agents-api/runtime-observability-api.md) |
| `projects/{project_id}/sessions/{session_id}/runtime-history` | Stored Runtime history | [Runtime history](../../contracts/agents-api/runtime-history-api.md) |
| `projects/{project_id}/{resource-owners,write-operations}`, `audit-log`, `summary` | Provenance, write history, administrator audit and usage summary | [Write audit](../../contracts/agents-api/write-audit.md), [administrator contract](../../contracts/agents-api/admin-api.md) |
| `projects/{project_id}/environments/{environment_id}/executor-credentials[/{key_id}]` | Executor credentials for a self-hosted Environment | [Executor credentials](../../contracts/agents-api/environment-executor-credentials.md) |
| `installation` | Public URL, API base URL, source commit, the installer's process settings and what is bound to the public URL; available before any deployment | [Installation](../../contracts/agents-api/installation.md) |
| `metrics` | Core's own process metrics | [Core metrics](../../contracts/agents-api/core-metrics.md) |
| `sandbox/deployment[/reset]`, `sandbox/e2b/templates[/{template_id}/builds]`, `sandbox/enrollment-tokens`, `sandbox/nodes[/{node_id}[/allocations]]` | Sandbox deployment, read-only E2B template discovery, node enrollment tokens and nodes | [Sandbox deployment](../../contracts/agents-api/sandbox-deployment.md), [node operations](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md), [node host history](../../contracts/agents-api/node-host-history.md) |
| `harnesses`, `harnesses/{harness}/model-configuration` | Supported harnesses and each harness's deployment default model configuration (write-only provider key) | [Model execution](../../contracts/agents-api/model-execution.md#deployment-defaults) |

## Machine connection API

These routes are under `/api/v1`. Each accepts only the credential listed, never
the Core key or a Project API key.

| Routes | Caller | Credential | Contract |
| --- | --- | --- | --- |
| `POST sandbox-node/enroll` | Node installer | One-use enrollment token from `POST /core/v1/sandbox/enrollment-tokens` | [Node operations](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#register-a-host), [machine OpenAPI](../../contracts/agents-api/runtime.openapi.yaml) |
| `GET sandbox-node/configuration` | Node installer and node | Enrollment token, or node credential with `X-OAC-Node-ID` | [Sandbox deployment](../../contracts/agents-api/sandbox-deployment.md), [machine OpenAPI](../../contracts/agents-api/runtime.openapi.yaml) |
| `GET sandbox-node/identity`, WebSocket `GET sandbox-node/connect` | Node | Node credential registered at enrollment | [Node operations](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#register-a-host) |
| `POST agent-daemon/enroll`, `GET agent-daemon/connection` | Self-hosted executor and its installer | Executor credential from `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | [Executor credentials](../../contracts/agents-api/environment-executor-credentials.md) |
| WebSocket `GET agent-daemon/ws`, `POST agent-daemon/bootstrap`, `GET agent-daemon/device-status` | Runtime daemons | Daemon credential: Core writes one into each hosted sandbox it prepares; a self-hosted executor uses its executor credential | [Runtime enrollment](../../services/agents-api/README.md#user-managed-runtime-enrollment) |

## Contract sources

- [Pinned upstream baseline](../../contracts/agents-api/upstream.json): OpenAI
  Python SDK 3.13.0, exact upstream commit and `agents=v1`.
- [Pinned routes](../../contracts/agents-api/upstream-routes.json) and
  [fields](../../contracts/agents-api/upstream-fields.json): extracted from the
  pinned SDK by `scripts/extract-agents-api-upstream.py`. Contract tests require the
  public OpenAPI to have exactly these routes and to keep every other field inside
  `x_agents_core`.
- [Public OpenAPI](../../contracts/agents-api/openapi.yaml): public schema snapshot;
  combine it with the fixed SDK and [operation evidence](../../contracts/agents-api/operation-evidence.md).
- [Core OpenAPI](../../contracts/agents-api/core.openapi.yaml): generated `/core/v1`
  routes, all authenticated by the Core key.
- [Machine connection OpenAPI](../../contracts/agents-api/runtime.openapi.yaml):
  generated `/api/v1` sandbox node routes. The node WebSocket is described in the [generation protocol](../../contracts/agents-api/node-generation-protocol.md); the private
  daemon transport is described in the Runtime credential guide.
- [Administrator contract](../../contracts/agents-api/admin-api.md): Project/key
  lifecycle, resources, explicit hosted Session archive, summary,
  errors/deletion preconditions, audit and historical copy provenance.
- [Web integration](web-management.md): browser, console and Core boundaries and frontend handoff.
- [Design rules](../design-principles.md) and [contributor guide](../../CONTRIBUTING.md):
  ownership, security and change requirements.

Generated schemas do not establish complete compatibility or real execution
support. The [coverage record](../../contracts/agents-api/README.md) identifies
qualified workflows, native differences and unresolved behavior. Update the
relevant contract and this index when adding or moving an API surface.

## Self-hosted installation

Creating or reading a `self_hosted` Session returns short-lived install commands in
`x_agents_core.installation`. Core Web reads the same commands at
`GET /core/v1/projects/{project_id}/environments/{environment_id}/installation`.
Machine installers use `POST /api/v1/agent-daemon/installation` and its `/claim`
subroute with the installation Bearer authorization. Qualified artifacts under
`/api/v1/agent-daemon/install/{version}/` are public, immutable release content. The
[installation grant](../../contracts/agents-api/environment-executor-credentials.md#installation-grant)
owns expiry, retry and credential ownership; the
[self-hosted guide](../getting-started/self-hosted.md#platforms) lists platforms.

The console-local `GET`/`POST /console/installation/domain` surface uses the signed-in
browser session and same-origin checks. It delegates only domain setup to the
installer, with the server-held Core key over a private Unix socket; it is not part
of the Agents API or Core management API. See [Web request boundaries](../web/architecture.md#request-boundaries).
