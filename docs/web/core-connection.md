# Connecting the administrator console to Core

`services/core-console` serves built Web assets, signs administrators in with the
Core key and forwards every signed-in, same-origin `/core/v1/*` request to Core. The
backend contract and the React screens that use it are implemented; the console has
no execution controls.

## Connection model

The browser calls same-origin `/core/v1` through `AdminClient` and
`CoreMetricsClient`, and the `/core/v1/sandbox` management routes through the
sandbox client. The console server forwards each signed-in `/core/v1/*` request by
prefix to its configured Core upstream, with the Core key (`CORE_CONSOLE_CORE_KEY_FILE`)
as the upstream credential; Core alone decides whether the route exists. Browser
code must never receive that credential.

Applications call Core's `/v1` directly with their own Project API keys and the
public API's route-specific headers. Nodes and Runtime daemons call Core's `/api/v1`
directly with their own machine credentials. The console returns 404 for `/v1` and
`/api/v1`, even with an explicit Bearer token. Deployment routing must send both to Core.
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
| `CORE_CONSOLE_CORE_KEY_FILE` | Absolute path to the private regular file containing the Core key |
| `CORE_CONSOLE_DIST` | Absolute directory containing the built Web assets |

The console exposes `GET /console/auth` and `POST /console/auth/login` and
`/logout`. The administrator signs in with the deployment's Core key, which the
installer writes to `admin/core.key` under the installation directory (by default
`~/.parsar/core/admin/core.key`; see [Core key](../getting-started/operations.md#core-key)).
The server compares it in constant time and answers with a same-origin session
cookie held only in its memory; the key is never logged or returned, and the
browser does not store it. A console restart or a Core key rotation requires
signing in again. There are no console accounts, usernames or setup step, and the
Core key cannot call `/v1`. `GET /console/config` provides safe console
configuration to an authenticated browser.

Use TLS for remote browser access and loopback listeners for local development.
Preserve the host/origin checks and the forwarding rules. The console refuses
requests with an `Upgrade` header, CONNECT and TRACE, and any path that `safePath`
rejects: an encoded `%`, dot segments, empty segments or backslashes. Before
forwarding, it strips the browser's Authorization, Cookie, Origin and Referer headers
and overwrites the actor header (`X-Core-Console-Actor`) with `console`, so a browser
cannot choose the audit actor label the service reports. The label is caller-declared
and display only. Keep deployment, application, node and provider credentials out of
`VITE_*`, browser storage, source files, URLs and logs.

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
2. Console login followed by `GET /core/v1/projects` proves the authenticated
   browser-to-console and console-to-Core path.
3. A Project key must work on its public resources and fail on management routes.
   The Core key must fail on `/v1`; `/v1` through the console stays 404.
4. Cross-origin management writes must be rejected. Audit actor labels must ignore
   a forged browser header.
5. Runtime observations and history report execution state separately from startup
   configuration. Neither a login nor a successful configuration read proves model
   or sandbox readiness.

A console login failure belongs to console authentication. An upstream 401 on a
management request points to the console's Core key or Core connection. A resource
deletion conflict must remain visible; it does not authorize an execution call.
Node and daemon `/api/v1` routes and native Runtime interfaces retain their own authentication.
