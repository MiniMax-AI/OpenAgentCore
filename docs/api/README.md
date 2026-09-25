# API documentation

Choose the API by its caller and authority. A route prefix alone does not establish
OpenAI compatibility: Core also has explicitly documented extensions under `/v1`.

| Surface | Caller and credential | Entry point | Reference |
| --- | --- | --- | --- |
| Public Agents API | Applications; a database-issued Project API key | Direct Core `/v1` | [Public API](public-agent-api.md) |
| Project-scoped Core extensions | Applications; the same Project API key | Direct Core; extension-specific paths | [Extension index](public-agent-api.md#core-extensions) |
| Administrator resources | Web's server or administrative automation; [Core key](../getting-started/operations.md#core-key) | Core `/core/v1/admin` | [Management contract](../../contracts/agents-api/admin-api.md) |
| Hosted sandbox administration | Web's server or administrative automation; Core key | Core `/core/v1/sandbox` management routes | [Web API](web-management.md#sandbox-administration) |
| Console authentication | Browser; Core key sign-in, then an HttpOnly session cookie | Console `/console/auth` | [Web API](web-management.md#browser-to-console) |
| Node and daemon transport | Installed node/Runtime; its own enrollment or connection credential | Direct Core `/api/v1`: `/api/v1/sandbox-node/*` and `/api/v1/agent-daemon/*`, including WebSockets | [Runtime credentials](../../contracts/agents-api/environment-executor-credentials.md), [node operations](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#register-a-host), [machine OpenAPI](../../contracts/agents-api/runtime.openapi.yaml) |

Projects own assets. Multiple equally privileged keys share the Project's assets
and execution principal; provenance retains the key that performed each write.
Projects and application keys live in PostgreSQL, never deployment configuration.
The Core key cannot authenticate public Agent API operations, and Project keys
cannot authenticate administrator operations.

The console server stores the Core key privately and proxies only
allowlisted management operations. Browsers do not receive it. Applications and
machines call Core directly; the console returns 404 for `/v1` and `/api/v1`, even
when given an application key or machine credential.

## Contract sources

- [Pinned upstream baseline](../../contracts/agents-api/upstream.json): OpenAI
  Python SDK 3.13.0, exact upstream commit and `agents=v1`.
- [Public OpenAPI](../../contracts/agents-api/openapi.yaml): public schema snapshot;
  combine it with the fixed SDK and [operation evidence](../../contracts/agents-api/operation-evidence.md).
- [Core extension OpenAPI](../../contracts/agents-api/sandbox-manager.openapi.yaml):
  generated management and executor-credential routes. The filename does not mean
  that all its routes are sandbox-administrator operations; use the authority table above.
- [Machine connection OpenAPI](../../contracts/agents-api/runtime.openapi.yaml):
  generated `/api/v1` sandbox node routes. The node WebSocket and the private
  daemon transport are described in the node and Runtime credential guides.
- [Administrator contract](../../contracts/agents-api/admin-api.md): Project/key
  lifecycle, resources, explicit hosted Session archive, summary,
  errors/deletion preconditions, audit and historical copy provenance.
- [Web integration](web-management.md): browser/console/Core boundaries and frontend handoff.
- [Design rules](../design-principles.md) and [contributor guide](../../CONTRIBUTING.md):
  ownership, security and change requirements.

Generated schemas do not establish complete compatibility or real execution
support. The [coverage record](../../contracts/agents-api/README.md) identifies
qualified workflows, native differences and unresolved behavior. Update the
relevant contract and this index when adding or moving an API surface.

Node host observations and retained charts are deployment-administrator reads;
see [node host history](../../contracts/agents-api/node-host-history.md).
