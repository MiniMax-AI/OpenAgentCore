# Connecting the administrator console to Core

This guide describes the target connection contract for the management API work.
The existing frontend has not yet migrated, and ownership of that UI change is
pending. Do not interpret the old connection form or execution controls as supported
administrator workflows. See [architecture](architecture.md) for component
boundaries and [design principles](../design-principles.md) for the authority model.

## Connection model

Run Core with its dedicated PostgreSQL database and the console server with its Web
assets. Use the [installation guide](../getting-started/install.md) for deployment
and the [operations guide](../getting-started/operations.md) for storage, upgrades
and node operations. Native Runtime configuration remains separate from connecting
the console; opening the console does not allocate compute or invoke a model.

The browser calls same-origin `/core/v1/admin` through `AdminClient`, plus the
existing `/core/v1/sandbox` management routes through the sandbox management client.
The console server authenticates these requests to Core with its deployment
credential. Browser code must not receive or configure that credential.

The console does not proxy `/v1`, carry a public caller key, or impersonate a selected
Project. An application calls Core's `/v1` endpoint directly using its own API key
and the public API's route-specific headers. The application endpoint and console
endpoint have different purposes even when an operator exposes both on one host.

## Console server configuration

| Setting | Purpose |
| --- | --- |
| `CORE_CONSOLE_UPSTREAM` | Configured HTTP(S) Core server origin, without credentials, query or path |
| `CORE_CONSOLE_ORIGIN` | Browser-facing console origin used for request-origin checks |
| `CORE_CONSOLE_ADMIN_TOKEN_FILE` | Absolute path to a private regular file holding the deployment credential |
| `CORE_CONSOLE_AUTH_MODE=account` | Console account login |
| `CORE_CONSOLE_STATE_DIR` | Private account/session state directory for account login |
| `CORE_CONSOLE_DIST` | Absolute directory containing the built Web assets |

The existing legacy Basic-auth mode uses `CORE_CONSOLE_PASSWORD_FILE` when account
mode is unset. Its password must differ from the deployment credential. These
console login modes do not create API users, roles or application credentials.

Use loopback listeners for local development and TLS for remote browser access.
Preserve the console's origin checks, authenticated proxy allowlist and upstream
header filtering. A development proxy must preserve the same management-only
boundary; restoring `/v1` forwarding is not a migration workaround. Keep deployment,
application, node and provider credentials out of `VITE_*`, source files, browser
storage, URLs and logs.

## Projects and application keys

After installation, an administrator creates a Project through the management API
and issues one or more named keys within it. The Project owns one tenant and one
execution principal; every key in it has equal access to its assets. Core records
the actual key separately for write provenance. There are no API users or roles.

Projects and keys live only in the database. Configuration files hold deployment
settings and credentials, never business Projects or API keys. Issuance returns
plaintext once; Core stores its digest. Deliver the plaintext to the application
through a private channel. Ordinary key reads return safe metadata only.

Rotate by issuing a replacement in the same Project and revoking the old key.
Revocation stops new authentication without removing assets or accepted work.
Renaming a Project preserves its ID and principal. Archiving disables all its keys
and retains resources for administrator inspection, deletion or copying to an
active Project. The management UI is still pending; use the
[administrator API contract](../../contracts/agents-api/admin-api.md) for these
operations and their uncertain-write behavior.

## Verification and diagnosis

Check connection layers separately:

1. `/healthz` establishes Core process liveness only.
2. An authenticated console login and a successful same-origin management read
   establish the browser-to-console and console-to-Core paths. Suitable reads are
   `/core/v1/admin/projects` and `/core/v1/admin/startup-configuration`.
3. An application's own key must work on its permitted `/v1` resources and fail on
   administrator routes. The deployment credential must fail on `/v1`.
4. Runtime observations and history establish the reported execution state. A
   successful configuration read does not prove model or sandbox readiness.

A console login failure belongs to console authentication. A Core 401 on a proxied
management request points to the configured deployment credential or upstream. A
404 for `/v1` through the console is expected; connect the application to the public
Core endpoint instead. Do not resolve a 401 by placing a deployment credential in
browser code or substituting an application key for it.

The console reads Session history without SSE and cannot start or cancel execution.
A deletion conflict must remain visible to the administrator; it does not authorize
an execution call. Resource creation, editing and execution belong to the
application's public API workflow. Existing node transport, Runtime adapters,
provider configuration and durable Session bindings are unchanged by this console
connection model.
