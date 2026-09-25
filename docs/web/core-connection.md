# Connecting the administrator console to Core

`services/core-console` serves built Web assets, authenticates administrators and
proxies an explicit management allowlist to Core. This backend contract is
implemented. React screens and the Vite development proxy still need migration by
the frontend team; their current execution controls are not supported management
workflows.

## Connection model

The browser calls same-origin `/core/v1/admin` through `AdminClient` and existing
`/core/v1/sandbox` management routes through the sandbox client. The console server
supplies the deployment administrator credential to its configured Core upstream.
Browser code must never receive that credential.

Applications call Core's `/v1` directly with their own Project API keys and the
public API's route-specific headers. The console returns 404 for `/v1`, even with
an explicit Bearer token. Deployment routing must send application traffic to Core.
The console endpoint and the public application endpoint serve different purposes,
even if they share a host.

Use the [installation guide](../getting-started/install.md) for deployment and the
[operations guide](../getting-started/operations.md) for storage, upgrades and node
management. A Core, Web and PostgreSQL installation may have zero execution nodes.
Opening the console neither allocates compute nor invokes a model. Deployment
sandbox management selects E2B, Docker or microsandbox independently of an
application's caller-managed `self_hosted` Runtime, including its own E2B setup.

## Server configuration and login

| Setting | Purpose |
| --- | --- |
| `CORE_CONSOLE_ADDR` | Console listener address |
| `CORE_CONSOLE_ORIGIN` | Exact browser-facing origin used for host and origin checks |
| `CORE_CONSOLE_UPSTREAM` | Core HTTP(S) origin, without credentials, query or resource path |
| `CORE_CONSOLE_ADMIN_TOKEN_FILE` | Absolute path to a private regular file containing the deployment credential |
| `CORE_CONSOLE_AUTH_MODE=account` | Enables console account login |
| `CORE_CONSOLE_STATE_DIR` | Private account state directory (login sessions are process-local) |
| `CORE_CONSOLE_DIST` | Absolute directory containing the built Web assets |

Account mode exposes `GET /console/auth` and `POST /console/auth/setup`, `/login`
and `/logout`. Initial setup registers the console account; subsequent login uses
a same-origin session cookie. Console accounts do not create Core API users,
Projects, roles or application keys. `GET /console/config` provides safe console
configuration to an authenticated browser.

Use TLS for remote browser access and loopback listeners for local development.
Preserve host/origin checks and the management route allowlist. Browser authorization,
cookies and actor headers are replaced or removed before forwarding to Core.
The service reports the authenticated console account as an audit label; a browser
cannot choose that label. Keep deployment, application, node and provider credentials
out of `VITE_*`, browser storage, source files, URLs and logs.

## Projects and application keys

Installation creates no Project or application key. An administrator creates a
Project and issues named keys using the [administrator API](../../contracts/agents-api/admin-api.md).
The Project owns one tenant and one principal; its keys share assets and permissions.
Writes retain each key's provenance. All Projects and application keys live in
PostgreSQL, independently of deployment configuration.

Issuance returns plaintext once; Core stores its digest. Deliver the new key to the
application privately. Rotate by issuing a replacement in the same Project and
revoking the old key. Archive disables every key while retaining assets and admitted
work. Ordinary metadata reads cannot recover plaintext.

## Verification and diagnosis

1. Core `/healthz` proves process liveness only.
2. Console login followed by `GET /core/v1/admin/projects` proves the authenticated
   browser-to-console and console-to-Core path.
3. A Project key must work on its public resources and fail on management routes.
   The deployment credential must fail on `/v1`; `/v1` through the console stays 404.
4. Cross-origin management writes must be rejected. Audit actor labels must reflect
   the signed-in console account despite a forged browser header.
5. Runtime observations and history report execution state separately from startup
   configuration. Neither a login nor a successful configuration read proves model
   or sandbox readiness.

A console login failure belongs to console authentication. An upstream 401 on a
management request points to the deployment credential or Core connection. A resource
deletion conflict must remain visible; it does not authorize an execution call.
Fixed node transports and native Runtime interfaces retain their own authentication.
