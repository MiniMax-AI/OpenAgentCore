# Core administrator Web frontend

This package contains the React application served by `services/core-console`:
the administrator console for monitoring Core, inspecting Project resources, and
managing Projects, keys and sandbox nodes. Its design system is
described in [DESIGN.md](DESIGN.md) and its product scope in [PRODUCT.md](PRODUCT.md).

## Integration contract

Browser management requests use same-origin `/core/v1` through `AdminClient`,
`CoreMetricsClient` (`/core/v1/metrics`) and the sandbox management client
(`/core/v1/sandbox`).
The administrator signs in with the deployment's Core key; the console keeps the key
server-side and gives the browser only a session cookie.
Applications use their own Project keys directly against Core's public `/v1` API.
The production console returns 404 for `/v1`, even with an explicit Bearer token.

Management covers Projects and keys, resource inspection and permitted deletion,
monitoring and audit. It does not create or edit arbitrary
application resources or execute Sessions. Do not add application keys or deployment
credentials to browser configuration, `VITE_*`, storage or logs.

See the [Core Web guide](../../docs/web/README.md),
[connection contract](../../docs/web/core-connection.md),
[frontend handoff](../../docs/web/roadmap.md) and
[administrator API](../../contracts/agents-api/admin-api.md).

The Core Web is an administrator console. Web calls only `/core/v1`, with the Core
key held on its server, and never `/v1` or `/api/v1`. Applications use an API key issued inside a Project. One Project owns one execution
tenant and principal; all its keys share assets and permissions while writes retain
individual key provenance. Projects and keys are database-owned, with no static
business keys or configuration synchronization. Revocation affects one key;
archiving a Project revokes all its keys, retaining assets and admitted execution.
Do not add Core users, roles, memberships or cross-Project sharing. Management
provides safe reads, public deletion preconditions, explicit hosted Session archive,
Project and key operations and credential issuance; it cannot copy, execute or edit
arbitrary assets.
Keep administrator target scope separate from caller principals. See
[design principles](../../docs/design-principles.md) and the
[administrator contract](../../contracts/agents-api/admin-api.md).

### Console structure

Core Web leads with operations: Monitor (Overview, Core metrics, Agent metrics,
Sandbox metrics, Session log), Resources and Platform. Pages use the shared
components in `apps/web/src/components` and the tokens in `apps/web/src/styles`,
described in `apps/web/DESIGN.md`. Keep explanations behind help tips, but keep
errors, warnings and safety notices visible. Browser-derived metrics state their
coverage, keep missing values missing, bound their fan-out and time, report a failed
read as failed and never imply deployment-wide or billing totals.
Sandbox deployment setup, configuration, reset and progress belong to the System
secondary page (`#system?id=sandbox`). Nodes owns node management; Overview and
metrics pages link to these owners instead of repeating their controls or details.
Keep uncommon resource edits and rollout details in dialogs, and avoid repeating
the same information within or across pages. Core responses remain the source of
truth for deployment and connection state.

### Console server and sign-in

`services/core-console` serves the production Web build and, after console login
and same-origin checks, forwards every `/core/v1` request with the Core key; Core
decides whether the route exists. It requires the private Core key file named by
`OAC_WEB_CORE_KEY_FILE` and holds no project caller credential. Every `/v1`
and `/api/v1` request returns 404, including explicit Bearer and WebSocket
requests; Web forwards no node or daemon transport. The installer mounts only the
Core key into Web and only its digest (`OAC_CORE_KEY_DIGESTS_FILE`) into
Core. The browser receives safe configuration, never that key. The deployment's
TLS reverse proxy routes `/v1` (applications) and `/api/v1` (nodes and Runtime
daemons, with their own credentials) directly to Core and everything else,
including `/core/v1`, to Web. Operator scripts call `/core/v1` on Core's loopback
port.
Nodes and Core come from one distribution. Runtime generation rollout has a
separate resource lifecycle; see the
[installation version policy](../../docs/getting-started/operations.md#installation-version-policy).

The Web manager offers no manual Core key entry outside sign-in, and Web refuses
to start without its Core key file. It holds no Project API key and never calls
`/v1`.
Chinese/English sandbox text, status and diagnostic formatting live in the shared
`apps/web/src/lib/` locale modules. A persisted explicit language preference wins
before the first browser language; unrelated product surfaces are outside this
translation scope. Preserve zero-node setup and node installation behavior when
localizing their controls. The sandbox manager centers node readiness and capacity in a desktop topology,
with Core surrounded by actual node buttons. Connection animation represents
liveness only, never invented traffic or work; offline/stale connections are
static and reduced-motion preferences disable decorative animation. Node selection
reveals inspection details. Installation identifiers, provider metadata and
allocation records are secondary content. Node enrollment is an explicit Add node action in a focused
dialog, using the deployment's `core_url` (the installation public URL).
Do not expose routine network wiring or manual runtime setup as the primary flow.
Generate a one-time command only on user intent, never retry enrollment writes
automatically, and discard credentials and late responses when the dialog closes
or the Core connection changes. Core reports the command's `enrollment_id` on the
node it registered (null for nodes enrolled before Core recorded it), and Web follows
the added node by an exact match on it; an existing node reconnecting is not a new
enrollment.
The command verifies the installer checksum before execution, retains normal TLS
verification, and passes the enrollment credential only to the installer process,
on standard input.


The `/core/v1` proxy retains fixed-origin, cross-site, safe-path, redirect and Upgrade
restrictions through the standard Go reverse proxy with streaming/cancellation;
literal or encoded dot segments can never move a request out of `/core/v1`.
During managed HTTP bootstrap, the console accepts a literal IP host and requires
writes to match that request's origin; domain hosts still require the configured
origin.
The console implements no product identity, resource semantics, Runtime discovery
or execution loop. Signing in with the Core key grants the complete console
surface; do not introduce Web accounts, roles, invitations or per-project Web
identities. Agent API caller keys remain independent of the Core key and cookie.

Web signs in only with the Core key (`POST /console/auth/login` with
`{"core_key":"…"}`), compared in constant time with the console's configured key
and never logged or echoed. There are no accounts, passwords, first-run setup or
Basic authentication. The retired authentication-mode, state-directory,
password-file and admin-token-file settings fail startup; their names are listed
in [`config.go`](../../services/core-console/config.go). Cookie sessions are in memory, bounded, HttpOnly, SameSite Strict and
Secure for HTTPS origins; a restart or Core key rotation requires sign-in again.
Unauthenticated access is limited to the static login UI, finite console
authentication routes and the static node installation payload. Sign-in uses
same-origin JSON POSTs with bounded bodies and bounded concurrent work. Only
failed attempts are rate limited, so the correct key always signs in; Web and the
installer therefore require Core keys of at least 32 characters. See the
[Core key operations guide](../../docs/getting-started/operations.md#core-key).

Projects and application API keys live in Core PostgreSQL. Project creation owns
its scope and shared principal; key issuance, revocation and Project archive share
a transaction with audit. Issuance stores only a digest and metadata and returns
plaintext once. Keys cannot be read back or reset in place; rotate by issuing a
new key in the same Project and revoking the old key. Authentication checks the
key and Project on every request, without a credential cache, and fails closed on
database errors. Deployment credentials cannot authenticate to the public API.
Configuration defines no Projects or business API keys. Fresh installation starts
with no Projects; an administrator creates a Project and then issues a key.

Administrator onboarding covers console login, Project creation, key issuance and
optional node enrollment. Model execution belongs in an external API example using an issued
key. Keep secrets out of browser persistence, generated examples and URLs. Observe
confirmed resources through the management API; do not infer Agent-to-node ownership
or execution readiness from a host connection. Preserve keyboard focus, reduced
motion and the existing node enrollment/topology contract.
The console has neither KVM nor Docker authority; its static root contains no
secrets. A default container installation uses a managed gateway with its Web
bootstrap port bound to `0.0.0.0`, so operators can sign in through the server's
IP address and configure a domain under System → Domain and HTTPS. Core's direct
host port remains on loopback and PostgreSQL stays on the private container
network. After HTTPS setup, the gateway routes application and node traffic to
Core and redirects the bootstrap Web entry to the configured HTTPS address.
Native, Core-only, Web-only and explicit external-ingress installations keep an
operator-managed HTTPS boundary. Web-only mode can connect to a loopback existing
Core on the same Linux host or a remote HTTPS Core.

## Local checks

From the repository root, with Node 22 and pnpm 10.30.3:

```sh
pnpm --filter @agents-core-web/web typecheck
pnpm --filter @agents-core-web/web test
pnpm --filter @agents-core-web/web build
```

These checks cover the application source. Browser acceptance through
`services/core-console` is tracked in the [frontend roadmap](../../docs/web/roadmap.md).
Required repository checks are documented in [CONTRIBUTING.md](../../CONTRIBUTING.md).

### Domain setup

System → Domain and HTTPS uses the authenticated, same-origin
`/console/installation/domain` installation manager. It is not a Core API route.
The form accepts one hostname, submits once, and polls backend-reported status.
A changed public address requires explicit confirmation when requested by the
manager. Failed or interrupted writes are not retried automatically; refresh
status before retrying. During a console restart the new HTTPS address remains
available as a sign-in link, including when the old session ends. Only the
manager's `ready` state confirms HTTPS; the browser does not probe another origin.

The Web bootstrap listener and Core's machine-facing public address are separate.
A `local_only` Core address requires HTTPS setup for external clients; it does
not mean the Web console is restricted to the local machine. Domain settings
belong to their System subpage, not the read-only startup settings table.

### README screenshots

The browser fixture has an opt-in scene for Overview and Agent metrics, including
five available nodes. From the repository root, start these in separate terminals:

```sh
OAC_WEB_SCREENSHOT_DEMO=1 AGENTS_FIXTURE_PORT=18394 node apps/web/e2e/fixture-console.mjs
```

```sh
OAC_WEB_DEV_PROXY_TARGET=http://127.0.0.1:18394 pnpm --filter @agents-core-web/web exec vite --host 127.0.0.1 --mode test --port 4394
```

Open `http://127.0.0.1:4394` in Chrome and sign in with the fixture-only key
`fixture-core-key-3f9a2c71`. Capture Overview and Agent metrics in light mode,
once in English and once in Chinese using the console language menu. Check that
all five nodes load and metrics have no partial-data warning before capturing.
For a remote preview, forward port 4394 over SSH and capture in local Chrome.
Keep the original resolution, crop browser chrome and add a plain macOS-style
window bar. The four WebP images in `docs/assets/console-*.webp` are linked by the matching
README and included in the distribution manifest. Normal acceptance data and
production builds do not enable this scene.
