# Core administrator Web frontend

This package contains the React application served by `services/core-console`:
the administrator console for monitoring Core, inspecting Project resources, and
managing Projects, keys and sandbox nodes. Its design system is
described in [DESIGN.md](DESIGN.md) and its product scope in [PRODUCT.md](PRODUCT.md).

## Integration contract

Browser management requests use same-origin `/core/v1/admin` through `AdminClient`,
plus the existing sandbox management client for allowed `/core/v1/sandbox` routes.
The console authenticates the browser and keeps the deployment credential server-side.
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
