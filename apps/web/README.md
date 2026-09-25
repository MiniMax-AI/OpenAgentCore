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
