# Web management API

Core Web is an administrator console. Resource inspection, Project/key management,
audit, usage and sandbox operations use management authority. The console has no
Agent execution, copy or arbitrary asset editing operation.

## Browser to console

The browser uses the console's own origin and signs in with the
[Core key](../getting-started/operations.md#core-key):

| Method and route | Request | Result |
| --- | --- | --- |
| `GET /console/auth` | No body | `200 {"mode":"login"}` or `200 {"mode":"authenticated"}` |
| `POST /console/auth/login` | `Content-Type: application/json`, body `{"core_key":"…"}`; other members are rejected | `200 {"mode":"authenticated"}` and an HttpOnly, SameSite=Strict session cookie (Secure over HTTPS) |
| `POST /console/auth/logout` | No credential payload | `200 {"mode":"login"}`; clears the cookie and the server-side session |
| `GET /console/config` | Signed-in session | `node_installer` and `node_installer_sha256` |

Sign-in errors use the console's `{"error": "…"}` envelope: 400 for a malformed
body, 401 for a wrong key, 415 for a non-JSON body, 429 with `Retry-After` when
failed attempts are limited or sign-in is busy, and 503 when the console cannot
start a session. The console compares the submitted key with its configured Core
key in constant time and never logs or returns it. Only failed attempts count
toward the limit; the correct key signs in even while failures are limited. The
console refuses to start with a Core key shorter than 32 characters. Sessions live only in the console's memory; a console restart
or Core key rotation requires signing in again. There are no console accounts,
usernames, passwords, first-run setup or Basic authentication.

Use same-origin browser requests and cookies. Mutations require the same-origin
request checks; never put the Core key in JavaScript or browser storage.

## Console to Core

The console server injects the Core key as its Bearer credential on allowlisted
management requests. It removes browser Authorization and forwarding-sensitive
headers, and sets `X-Core-Console-Actor: console`. Core records the header as the
audit `actor_label`. The label is caller-asserted and display-only: direct Core key
scripts normally send none, which records an empty label, but could set any
value. Never use it for authorization or as proof of origin.

Use [AdminClient](../../packages/agents-client/src/admin-client.ts) for the typed
management client and [the complete administrator reference](../../contracts/agents-api/admin-api.md)
for methods, fields, filters, pagination and response shapes.
Routes below are relative to `/core/v1/admin`:

| Workflow | Routes |
| --- | --- |
| Projects | `GET/POST /projects`, `POST /projects/{id}`, `POST /projects/{id}/archive` |
| Project keys | `GET/POST /projects/{id}/keys`, `DELETE /projects/{id}/keys/{key_id}` |
| Resource lists/details/deletion | `/projects/{id}/agents`, `/sessions`, `/environment-templates`, `/skills`, `/files`, `/vaults`, including the documented nested reads |
| Asset ownership | `GET /projects/{id}/resource-owners` with batched resource IDs |
| Key operation history | `GET /projects/{id}/write-operations` with key/resource/time filters |
| Usage and health | `GET /summary`, `/runtime-observations` |
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

`GET /api/v1/sandbox-node/configuration` uses an enrollment Bearer token, or a
retained node Bearer credential with `X-Parsar-Node-ID`. This read does not consume
enrollment. Retained matching nodes can read their configuration during maintenance.
Installers must verify the returned generation, specification digest and Runtime
before registration; local files cannot override the saved limits. A mismatch
returns `sandbox_specification_mismatch` without replacing node state.

Node configuration, enrollment, identity and connection routes live under
`/api/v1/sandbox-node`, beside the daemon's `/api/v1/agent-daemon`. The reverse
proxy sends `/api/v1` directly to Core; the console returns 404 for it and never
forwards machine traffic. These routes use their own credentials, do not inherit a
browser login and gain no management authority. E2B credentials are absent
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
