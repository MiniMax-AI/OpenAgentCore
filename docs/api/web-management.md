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

The [Hosted Sandbox Manager reference](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md)
and [generated OpenAPI](../../contracts/agents-api/sandbox-manager.openapi.yaml)
define deployment and node operations:

- `GET/POST/PUT /core/v1/sandbox/deployment` and
  `PATCH /core/v1/sandbox/deployment/maintenance`.
- `GET /core/v1/sandbox/nodes`, `PATCH/DELETE /core/v1/sandbox/nodes/{node_id}`,
  and `GET /core/v1/sandbox/nodes/{node_id}/allocations`.
- `POST /core/v1/sandbox/enrollment-tokens` for a one-time node installation command.

A deployment selects one provider: E2B, Docker or microsandbox. E2B provisions
cloud sandboxes without a node install; own-machine hosting enrolls nodes using
Docker or microsandbox. Switching requires maintenance and verified cleanup of
retained/pending resources. These operations are distinct from public Environment
Templates and caller-owned `self_hosted` provisioning.

Enrollment, node identity and daemon WebSocket routes use their own credentials.
They may pass through the paired console's fixed transport routes, but do not
inherit a browser's administrator session or gain general management authority.

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
