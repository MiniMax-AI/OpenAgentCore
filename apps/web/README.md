# Core administrator Web frontend

This package contains the React application served by `services/core-console`.
The frontend team owns migration to the implemented administrator API and
`AdminClient`. Current execution pages and the Vite development proxy still reflect
the previous frontend implementation; their builds, screenshots and fixture tests
do not establish acceptance of the new management UI.

## Integration contract

Browser management requests use same-origin `/core/v1/admin` through `AdminClient`,
plus the existing sandbox management client for allowed `/core/v1/sandbox` routes.
The console authenticates the browser and keeps the deployment credential server-side.
Applications use their own Project keys directly against Core's public `/v1` API.
The production console returns 404 for `/v1`, even with an explicit Bearer token.

Management covers Projects and keys, resource inspection and permitted deletion,
independent copies, monitoring and audit. It does not create or edit arbitrary
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

These checks cover the current application source. Management browser acceptance
must run separately after migration through `services/core-console`. Required
repository checks are documented in [CONTRIBUTING.md](../../CONTRIBUTING.md).
