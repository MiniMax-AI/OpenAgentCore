# Administrator console acceptance

Run from the repository root with the pinned Node, pnpm and Go versions:

```sh
pnpm test:web:acceptance
```

Playwright starts a synthetic Core upstream and builds/runs the production
`services/core-console` binary with freshly built Web assets. Login, cookies,
origin checks, the route allowlist and credential forwarding are production code.
The fixture accepts only the test deployment credential and never handles
`/console/auth` or manufactures browser sessions. Other than the sign-in test,
browser contexts reuse a cookie obtained from real login so the suite respects
the production login rate limit. Its data and writes are
synthetic; this suite is not live cluster or model execution acceptance.

`GO` may select an absolute Go executable. Set `AGENTS_FIXTURE_PORT` (default
18611) and `AGENTS_WEB_PORT` (default 19619) to unused loopback ports for concurrent
runs. Existing services are never reused. Account state, the private test token,
installer stub, binary and assets live in a fresh `~/.parsar/tests/console-e2e-*`
directory, removed on shutdown. Browser artifacts go to
`~/.parsar/tests/console-playwright`; `AGENTS_E2E_OUTPUT_DIR` selects an independent
output directory. Chrome is the default Playwright browser; set
`AGENTS_E2E_BROWSER_CHANNEL=chromium` to use the installed bundled Chromium.

The installer stub exercises command creation and the production configuration's
digest only. Tests never execute an enrollment command or install a node.

The suite retains current management behavior: login, Projects/keys, one-time
secrets, uncertain writes, copy/delete, monitoring, read-only Session history,
node management and failure feedback. The previous builder, execution playground,
resource editors and browser connection-key flows were retired with those UI
features. Public Agents API compatibility remains in the Core/client tests.

Project isolation, shared application-key access, revocation enforcement, archive
retention, copy transactions, deletion preconditions and audit persistence require
Core backend tests and real deployment acceptance. Fixture responses cannot prove
these invariants. The repository's required `make check` still applies.
