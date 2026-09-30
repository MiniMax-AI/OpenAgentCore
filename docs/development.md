# Develop OpenAgentCore

This guide is for contributors changing Core, Runtime, adapters, the Web console
or the documentation site. For using a deployment, start with the
[getting started guide](getting-started/README.md). Read the
[contributor rules](../CONTRIBUTING.md) before changing code.

![OpenAgentCore architecture: applications and the Web console connect to Core through separate APIs; Sandbox Providers, Runtime, Harnesses and model providers connect through shared protocols.](assets/development-architecture.png)

## Set up a checkout

Work from an isolated worktree so experiments and validation do not disturb
another checkout. From an existing clone with an up-to-date `main`:

```sh
git worktree add ../openagentcore-change -b codex/my-change main
cd ../openagentcore-change
```

Install Go at the version in [go.mod](../go.mod), Node 22, pnpm at the version
in [package.json](../package.json), and Python 3.9 or newer. The complete gate
runs on Linux and needs a dedicated PostgreSQL database, OpenSSL development
libraries for the microsandbox helper, and a Playwright browser. Provider and
Runtime builds have additional prerequisites in their component guides.

```sh
pnpm install --frozen-lockfile
python3 -m venv .venv
.venv/bin/python -m pip install -r services/core/tests/requirements.txt
.venv/bin/python - <<'PYTHON'
import json
import subprocess
import sys

pin = json.load(open("contracts/agents-api/upstream.json"))
subprocess.check_call([
    sys.executable, "-m", "pip", "install",
    "git+" + pin["repository"] + "@" + pin["commit"],
])
PYTHON
pnpm exec playwright install --with-deps chrome
export OAC_TEST_OFFICIAL_SDK_PYTHON="$PWD/.venv/bin/python"
```

Set `OAC_TEST_DATABASE_URL` privately to a dedicated PostgreSQL test database.
Never point the test suite at an installation or product database. The
[test database rules](../CONTRIBUTING.md#test-database) list the required role
permission.

Third-party native packages are pinned build inputs; changing a pin requires the
relevant native qualification as well as compilation.

## Build and run components

From the repository root:

```sh
make build-core
make build-daemon
```

These produce the Core commands under `~/.oac/build/oac-core/` and the daemon
under `~/.oac/build/daemon/` by default. `OAC_DEV_HOME` selects another build root;
`OAC_DEV_CORE_BUILD_DIR` selects an absolute Core output directory. Building does
not configure a database, start a deployment or qualify native execution.

Use the [service guide](../services/core/README.md#build-standalone-binaries)
to run the Core migrator and server with a separate development database. The
[configuration appendix](configuration.md#appendix-core-environment-without-the-installer)
owns standalone process settings. For a complete operator installation, use the
[installation guide](getting-started/install.md); building Core alone is a
separate contributor workflow.

The [Web package guide](../apps/web/README.md) describes console development and
its management-only integration. `pnpm dev:web` starts the frontend development
server; it does not install Core, issue keys or start native execution. The
[docs app guide](../apps/docs/README.md) describes the independent documentation
site; `pnpm dev:docs` starts its development server.

## Repository map

| Location | Responsibility | Read next |
| --- | --- | --- |
| `services/core/internal/api` | Public, administrator and machine HTTP boundaries | [API index](api/README.md) |
| `services/core/internal/store` and `internal/db` | Core persistence, transactions, queries and migrations | [Service guide](../services/core/README.md#database-ownership) |
| `services/core/internal/execution` | Durable Turn dispatch and scheduling | [Runtime protocol](runtime-protocol.md) |
| `services/core/internal/engine` | Pure qualification of harness operations and placements | [Harness onboarding](../contracts/agents-api/harness-onboarding.md) |
| `internal/agentdaemon/proto` and `gateway` | Shared wire types, validators and authenticated Runtime connections | [Runtime protocol](runtime-protocol.md) |
| `internal/runtimebootstrap` | Provider-to-Runtime startup input | [Runtime bootstrap](runtime-bootstrap.md) |
| `apps/daemon/internal/dispatch` | Runtime preparation, Executor reuse, Turn and cleanup ownership | [Harness lifecycle](../contracts/agents-api/harness-onboarding.md#required-adapter-interfaces) |
| `apps/daemon/internal/agent` | Native harness adapters | [Native references](../contracts/agents-api/harness-onboarding.md#native-references) |
| `services/core/internal/sandbox` | Provider interfaces and managed compute lifecycle | [Provider onboarding](sandbox-provider.md) |
| `services/web` | Console login and the server-side management proxy | [Console server](web/console-server.md) |
| `apps/web` and `packages/agents-client` | Console UI and typed clients | [Web guide](../apps/web/README.md) |
| `deploy/install` and `scripts` | Distribution, installation and validation tools | [Maintainers](maintainers.md) |
| `contracts/agents-api` | Pinned schema, local semantic contracts and qualification evidence | [Coverage ledger](../contracts/agents-api/README.md) |
| `apps/docs` | Generated guide and API-reference website | [Generation workflow](../apps/docs/README.md) |

Core owns durable execution facts. Runtime owns local execution and cleanup.
Adapters translate native operations. Providers own outer compute. These boundaries
apply to user-owned machines and Core-managed environments; read the
[design principles](design-principles.md) for resource and credential vocabulary.

## Choose an extension boundary

Each boundary has one canonical guide. Read it before changing code; this page
only helps you pick the right one.

| Boundary | You are adding or changing | Canonical guide |
| --- | --- | --- |
| Harness adapter | A native agent engine behind the Runtime | [Harness onboarding](../contracts/agents-api/harness-onboarding.md) |
| Sandbox Provider | Outer compute that creates and reclaims Environments | [Sandbox Provider guide](sandbox-provider.md) |
| Runtime bootstrap | Starting a managed Runtime with its connection identity | [Runtime bootstrap](runtime-bootstrap.md) |
| Core–Runtime protocol | A message, receipt or lifecycle rule between Core and the daemon | [Core–Runtime protocol](runtime-protocol.md) |
| Public API operation | A `/v1`, `/core/v1` or `/api/v1` route | [API index](api/README.md) and [contracts](../contracts/agents-api/README.md) |
| Environment capability | Skills, Plugins, MCP or `packages.system` preparation | [Environments](../contracts/agents-api/environments.md#runtime-capability-preparation) |

### Add a Harness adapter

Implement the shared `ExecutorFactory`, `Executor` and `Turn` interfaces in
[`agent/harness.go`](../apps/daemon/internal/agent/harness.go), register
the adapter and add its profile/configuration entry to the shared catalog. Follow the numbered steps in
[Harness onboarding](../contracts/agents-api/harness-onboarding.md); qualification
evidence belongs in [Harness integration](../contracts/agents-api/harnesses.md).

### Add a Sandbox Provider

Implement the five required `SandboxProvider` operations in
[`sandbox_provider.go`](../services/core/internal/sandbox/sandbox_provider.go),
register the provider kind and pass `make check-sandbox-provider-contract`. Follow
the numbered steps in the [Sandbox Provider guide](sandbox-provider.md).

### Extend the Core–Runtime protocol

Change shared types and validators in `internal/agentdaemon/proto`, both peers
and their contract tests together, keeping the exact wire-version check. The
[protocol guide](runtime-protocol.md) owns message order, receipts and failure
ownership; `make check-runtime-contract` is its focused gate.

## Validate a change

Run checks for the affected boundary while developing. The repository
[required checks](../CONTRIBUTING.md#required-checks) define completion, including
`make check` and any changed native component's real acceptance.

| Change | Focused validation |
| --- | --- |
| Core handlers, persistence or clients | `make check-core` |
| SQL queries | `make sqlc-generate`, inspect generated files, then `make check-sqlc` |
| Handler annotations or API contract | `make openapi`, inspect all three namespace schemas |
| Shared Runtime protocol | `make check-runtime-contract` |
| Provider integration | `make check-sandbox-provider-contract` and the provider's native checks |
| Claude SDK bridge and artifact | `make check-claude-sdk` |
| Web UI and clients | `make check-web` |
| Distribution or installer | `make check-distribution` |
| Docs sources and generated site | `pnpm --dir apps/docs generate`, then `make check-docs` |

When the complete change is ready:

```sh
make check
```

Fixture browser acceptance uses loopback ports 18092 and 4174. Select unused ports
with `AGENTS_FIXTURE_PORT` and `AGENTS_WEB_PORT` when running parallel validation.
Keep databases, ports and containers separate between validation workers.
Compilation, fixture success and live model/provider acceptance establish different
facts; report skipped or unavailable checks explicitly. Follow the
[independent blind review workflow](../CONTRIBUTING.md#review)
after validation.

## Change documentation

Edit the source that owns the subject, using the
[documentation ownership map](../CONTRIBUTING.md#documentation-ownership).
User guides explain a workflow and link to detailed contracts. Contributor rules
explain ownership and required checks. Semantic contracts document exact behavior,
implementation gaps and evidence. Avoid copying the same rule into all three.

Update authored sources before regenerating the docs site. Adding a site page
also requires a source entry and navigation entry; source/output hashes verify
freshness. API references render the namespace-specific generated schemas. Follow
[the docs app workflow](../apps/docs/README.md) for link, type, build and browser
checks. The release bundle has a separate explicit documentation list in
`scripts/core-distribution-manifest.py`; changing a bundled path or heading must
also pass its relative-link and anchor checks.
