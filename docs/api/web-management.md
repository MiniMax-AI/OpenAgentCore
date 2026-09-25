# Web management API

Core Web is an administrator console. Resource inspection, Project/key management,
explicit cross-Project copies, audit, usage and sandbox operations use management
authority. The console has no Agent execution or arbitrary asset editing operation.

## Browser to console

The browser uses the console's own origin. In account mode:

| Method and route | Request | Result |
| --- | --- | --- |
| `GET /console/auth` | No body | `mode: setup`, `login`, or `authenticated`; authenticated mode includes `username` |
| `POST /console/auth/setup` | JSON `username`, `password` | Creates the sole local administrator and signs in; registration closes afterward |
| `POST /console/auth/login` | JSON `username`, `password` | Signs in with an HttpOnly session cookie |
| `POST /console/auth/logout` | No credential payload | Clears the current session |
| `GET /console/config` | Authenticated console session | Safe connection configuration |

Use same-origin browser requests and cookies. Mutations require the same-origin
request checks; do not put deployment credentials in JavaScript or browser storage.
The console's local account is not an Agent API user, Project member or role.
Already-configured Basic-auth deployments retain their existing sign-in mode;
`GET /console/auth` reports `legacy` there. No new Basic-auth flow is required in
frontend work.

## Console to Core

The console server injects its private deployment Bearer credential on allowlisted
management requests. It removes browser Authorization and forwarding-sensitive
headers, and supplies the signed-in account name as `X-Core-Console-Actor`.
Core treats that name as an audit display label, not an authorization input.

Use [AdminClient](../../packages/agents-client/src/admin-client.ts) for the typed
management client and [the complete administrator reference](../../contracts/agents-api/admin-api.md)
for methods, fields, filters, pagination, copy rules and response shapes.
Routes below are relative to `/core/v1/admin`:

| Workflow | Routes |
| --- | --- |
| Projects | `GET/POST /projects`, `POST /projects/{id}`, `POST /projects/{id}/archive` |
| Project keys | `GET/POST /projects/{id}/keys`, `DELETE /projects/{id}/keys/{key_id}` |
| Resource lists/details/deletion | `/projects/{id}/agents`, `/sessions`, `/environment-templates`, `/skills`, `/files`, `/vaults`, including the documented nested reads |
| Asset ownership | `GET /projects/{id}/resource-owners` with batched resource IDs |
| Key operation history | `GET /projects/{id}/write-operations` with key/resource/time filters |
| Copies | `POST /copies`, explicit source/target Project IDs and optional dependencies |
| Usage and health | `GET /summary`, `/runtime-observations`, `/startup-configuration`, `/runtime-history/capabilities` |
| Administrator audit | `GET /audit-log` |

A Project UUID in a management path selects the target; it is not a credential.
API-key plaintext is returned only by successful issuance, so display it once and
never cache it. Reconcile uncertain issuance before explicitly issuing another key.
Deleted/revoked resources retain their audit records. Historical unknown ownership
stays null. Session usage grouped by key belongs to the Session's creation key;
it is operational attribution, not per-key billing.

## Sandbox administration

The [deployment configuration contract](../../contracts/agents-api/sandbox-deployment.md),
[Hosted Sandbox Manager reference](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md)
and [generated OpenAPI](../../contracts/agents-api/sandbox-manager.openapi.yaml)
define deployment and node operations:

- `GET/POST/PUT /core/v1/sandbox/deployment` and
  `PATCH /core/v1/sandbox/deployment/maintenance`.
- `GET /core/v1/sandbox/nodes`, `PATCH/DELETE /core/v1/sandbox/nodes/{node_id}`,
  and `GET /core/v1/sandbox/nodes/{node_id}/allocations`.
- `POST /core/v1/sandbox/enrollment-tokens` for a one-time node installation command.

PostgreSQL owns one provider, per-sandbox resource specification and immutable
Runtime selection. POST initializes it; PUT replaces the complete selection using
`expected_generation`. Requests carry `resources` and, for Docker/microsandbox,
`runtime`; safe responses return `specification` and `specification_digest`.
Response `resources.allocations` and `resources.pending` are cleanup counts, not
CPU, memory or disk settings. E2B accepts a write-only key and exact template build
instead of a node Runtime release, and provisions without a node installation. E2B
may omit `resources` to adopt the validated build's CPU and memory; responses show
the build as read at selection time in `e2b.template_build`. Microsandbox responses
return its idle `suspension` policy; other providers return null.

Provider, resource and Runtime changes all require global maintenance and verified
cleanup of retained/pending resources. Core validates the candidate before commit;
a rejection preserves the previous configuration. A changed commit advances the
generation and retires old nodes and enrollment tokens atomically. Explicitly resume
after success. Neither switching nor editing configuration deletes resources or
migrates existing Sessions. During maintenance, administrators can explicitly
[archive each retained hosted Session](../../contracts/agents-api/admin-api.md#administrative-session-archive)
through the Core administrator API at the current generation, then read its
resource disposition and recheck deployment counts. Archive preserves history
and persisted Files/Artifacts; unpersisted workspace contents are lost and the
original Session cannot resume. This API does not add a console archive control.
Public Environment Templates, the `/v1` contract and
caller-owned `self_hosted` provisioning remain unchanged.

`GET /core/v1/sandbox/node/configuration` uses an enrollment Bearer token, or a
retained node Bearer credential with `X-Parsar-Node-ID`. This read does not consume
enrollment. Retained matching nodes can read their configuration during maintenance.
Installers must verify the returned generation, specification digest and Runtime
before registration; local files cannot override the saved limits. A mismatch
returns `sandbox_specification_mismatch` without replacing node state.

Node configuration, enrollment, identity and daemon WebSocket routes retain their
own credentials through the paired console's fixed transport routes. The console
must not substitute its administrator credential on them. They do not inherit a
browser login or gain general management authority. E2B credentials are absent
from node configuration and safe deployment views.

## Frontend handoff and errors

Frontend screen implementation is owned by the separate frontend task. The
backend provides the management contract, typed client and console allowlist.
Existing React screens that call `/v1` must switch before the paired application
release; backend tests do not qualify those screens.

The console returns 404 for `/v1`, old `/console/api-keys` routes and application
executor-credential routes, even with an explicit application Bearer key. Invalid
Origin/Host requests are rejected; unavailable Core or rejected upstream redirects
return 502. Console authentication uses its own error envelope. Management resource
errors and deletion constraints are documented in the administrator reference and
generated schema; do not interpret every empty or failed read as an absent resource.

## Core metrics

`GET /core/v1/admin/core-metrics?range=1h|6h|24h|7d` returns Core process, execution
queue/slots, PostgreSQL and background-job measurements. It uses deployment
administrator authentication, rejects arbitrary query filters and never grants
Agent execution access. See the [exact measurement contract](../../contracts/agents-api/core-metrics.md)
for complete buckets, null values, units and process-local retention. Frontend
implementation is maintained separately; this backend change does not modify
Agent metrics or the public Agent API.

## Node host history

Deployment administrators can read a node and its host history through
`GET /core/v1/sandbox/nodes/{node_id}?range=1h|6h|24h`. See the
[node host history contract](../../contracts/agents-api/node-host-history.md)
for nullable observations, freshness and aggregation. The node list is unchanged.
