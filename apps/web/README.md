# Web console package

`apps/web` (`@oac/web`) is the React application of the OpenAgentCore administrator console. The [console server](../../docs/web/console-server.md) serves its production build. [DESIGN.md](DESIGN.md) records the visual system and [PRODUCT.md](PRODUCT.md) the product scope and behavior; the [operator guide](../../docs/web/README.md) describes the console for administrators.

## Rules for console code

- Call Core only through `AdminClient`, `SandboxAdminClient` and `CoreMetricsClient` from [`packages/agents-client`](../../packages/agents-client/README.md). The browser calls same-origin `/console/*` and `/core/v1/*` routes and never `/v1` or `/api/v1`. [Console API usage](../../docs/web/console-api-usage.md) lists each page's routes and read bounds; update it with any change to them.
- Keep API keys, the Core key and provider credentials out of `VITE_*` variables, browser storage, URLs, logs and source files.
- Build pages from the shared components in `src/components` and the design tokens: the Beautiful UI base tokens in `src/app/beautifui/foundation.css` and the console's own in `src/styles`, as [DESIGN.md](DESIGN.md) describes.
- Put copy in the i18n resources; see [Web internationalization](src/i18n/README.md).

## Run the console locally

Set up the checkout as described in the [development guide](../../docs/development.md), then start the fixture console and the development server in separate terminals from the repository root:

```sh
node apps/web/e2e/fixture-console.mjs
```

```sh
OAC_WEB_DEV_PROXY_TARGET=http://127.0.0.1:18092 pnpm dev:web
```

Open `http://127.0.0.1:4173` and sign in with the fixture-only key `fixture-core-key-3f9a2c71`.

`pnpm dev:web` runs Vite on `127.0.0.1:4173` and proxies `/console`, `/node-install` and `/core/v1` to `OAC_WEB_DEV_PROXY_TARGET` (default `http://127.0.0.1:8091`). Vite reads the setting from the environment or the repository's `.env` file; it never reaches browser code. The target must serve the console routes. The development server also forwards `/v1` to the same target for local tooling such as `scripts/core-doctor.mjs`, adding a bearer token from `OAC_WEB_DEV_PROXY_TOKEN` or from the private file `OAC_WEB_DEV_PROXY_TOKEN_FILE` (default `~/.oac/dev/web-token`, used when it exists); the console itself never calls `/v1`. `apps/web/e2e/fixture-console.mjs` is a synthetic console service with deterministic data; `AGENTS_FIXTURE_PORT` changes its port (default 18092).

## Checks

From the repository root:

```sh
pnpm --filter @oac/web typecheck
pnpm --filter @oac/web test
pnpm --filter @oac/web build
pnpm test:web:acceptance
```

`pnpm test:web:acceptance` runs the Playwright tests in `apps/web/e2e` in Chrome against the fixture console. After each test, every spec except `domain.spec.ts` checks that the browser sent nothing to `/v1` and no `Authorization` header. The tests do not exercise `services/web` or a real Core; the console server has its own Go tests. `make check-web` runs all of these; [CONTRIBUTING.md](../../CONTRIBUTING.md) lists the repository's required checks.

## README screenshots

The fixture has an opt-in scene for Overview and Agent metrics, including five available nodes. From the repository root, start these in separate terminals:

```sh
OAC_WEB_SCREENSHOT_DEMO=1 AGENTS_FIXTURE_PORT=18394 node apps/web/e2e/fixture-console.mjs
```

```sh
OAC_WEB_DEV_PROXY_TARGET=http://127.0.0.1:18394 pnpm --filter @oac/web exec vite --host 127.0.0.1 --mode test --port 4394
```

Open `http://127.0.0.1:4394` in Chrome and sign in with the fixture key `fixture-core-key-3f9a2c71`. Capture Overview and Agent metrics in light mode, once in English and once in Chinese using the console language menu. Check that all five nodes load and metrics have no partial-data warning before capturing. For a remote preview, forward port 4394 over SSH and capture in local Chrome. Keep the original resolution, crop browser chrome and add a plain macOS-style window bar. Save the four images as `docs/assets/console-*.webp`; the READMEs link them and the distribution manifest includes them. Normal acceptance data and production builds do not enable this scene.
