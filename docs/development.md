---
title: "Develop OpenAgentCore"
---

Set up a checkout, build a component and validate your changes. To use an installation, start with the [getting started guide](./getting-started/index.md). Read the [contributor rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md) before changing code.

For component responsibilities and execution flow, read [Architecture](./architecture.md).

## Set up a checkout

Work from an isolated worktree so experiments and validation do not disturb another checkout. From an existing clone with an up-to-date `main`:

```sh
git worktree add ../openagentcore-change -b codex/my-change main
cd ../openagentcore-change
```

Install Go at the version in [go.mod](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/go.mod), Node 22.13 or newer, pnpm at the version in [package.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/package.json), and Python 3.9 or newer. The complete gate runs on Linux and needs a dedicated PostgreSQL database, OpenSSL development libraries for the microsandbox helper, and a Playwright browser. Provider and Runtime builds have additional prerequisites in their component guides.

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

Set `OAC_TEST_DATABASE_URL` privately to a dedicated PostgreSQL test database. Never point the test suite at an installation or product database. The [test database rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#test-database) list the required role permission.

For native package pin changes, follow the [live acceptance rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#live-acceptance).

## Build and run components

From the repository root:

```sh
make build-core
make build-daemon
```

Core build outputs and output-directory settings are in [Standalone Core builds](./maintainers.md#standalone-core-builds). The daemon is written to `${OAC_DEV_HOME:-$HOME/.oac}/build/daemon/oac-daemon`.

Use the [service guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#run-from-source) to run the Core migrator and server with a separate development database. The [configuration appendix](./configuration.md#appendix-core-environment-without-the-installer) owns standalone process settings. For a complete operator installation, use the [installation guide](./getting-started/install.md); building Core alone is a separate contributor workflow.

For frontend development, run `pnpm dev:web` using the fixture or Core connection in the [Web package guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/README.md).

## Repository map

| Location | Responsibility | Read next |
| --- | --- | --- |
| `services/core/internal/api` | Public, administrator and machine HTTP boundaries | [API index](./api/index.md) |
| `services/core/internal/store` and `services/core/internal/db` | Core persistence, transactions, queries and migrations | [Service guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#database) |
| `services/core/internal/execution` | Durable Turn dispatch and scheduling | [Runtime protocol](./runtime-protocol.md) |
| `services/core/internal/engine` | Pure qualification of harness operations and placements | [Harness onboarding](../contracts/agents-api/harness-onboarding.md) |
| `internal/agentdaemon/proto` | Core–Runtime wire types and validators | [Runtime protocol](./runtime-protocol.md) |
| `internal/runtimebootstrap` | Provider-to-Runtime startup input | [Runtime bootstrap](./runtime-bootstrap.md) |
| `apps/daemon/internal/dispatch` | Runtime preparation, Executor reuse, Turn and cleanup ownership | [Harness lifecycle](../contracts/agents-api/harness-onboarding.md#required-adapter-interfaces) |
| `apps/daemon/internal/agent` | Native harness adapters | [Native references](../contracts/agents-api/harness-onboarding.md#native-references) |
| `services/core/internal/sandbox` | Provider interfaces and managed compute lifecycle | [Provider onboarding](./sandbox-provider.md) |
| `services/web` | Console login and the server-side management proxy | [Console server](./web/console-server.md) |
| `apps/web` and `packages/agents-client` | Console UI and typed clients | [Web guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/README.md) |
| `deploy/install` and `scripts` | Distribution, installation and validation tools | [Maintainers](./maintainers.md) |
| `contracts/agents-api` | Pinned schema, semantic contracts and coverage ledger | [Coverage ledger](../contracts/agents-api/index.md) |

## Choose an extension boundary

Use the [protocol map](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#protocols-at-every-boundary) to find the code and guide for a new Harness, Sandbox Provider, model provider, API operation or Runtime message. The guide owns registration, supported operations and the checks that qualify an implementation. For workspace capabilities such as Skills, Plugins, MCP and system packages, start with [Environments](../contracts/agents-api/environments.md).

## Validate a change

Run checks for the affected boundary while developing. The repository [required checks](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#required-checks) define completion, including `make check` and any changed native component's real acceptance.

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
| Documentation | `make check-names`; `make check-distribution` validates Markdown links and bundled docs |

Fixture browser acceptance uses loopback ports 18092 and 4174. Select unused ports with `AGENTS_FIXTURE_PORT` and `AGENTS_WEB_PORT` when running parallel validation. Keep databases, ports and containers separate between validation workers. Compilation, fixture success and live model/provider acceptance establish different facts; report skipped or unavailable checks explicitly. Follow the [independent blind review workflow](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#review) after validation.

## Change documentation

The repository-root `docs.json` configures the Mintlify site using the Almond theme, a dark graphite background, green accents and monospace headings. It references the existing Markdown sources; `.mintignore` excludes application code and styles from the site. Connect Mintlify to the repository root on the default branch. Use Node 22 LTS, install the CLI with `npm install -g mint`, and run `mint dev` from the repository root to preview the site. Before publishing, run `mint validate` and inspect its output for parsing errors as well as its exit status, then run `mint broken-links`.

Use `index.md` for published section indexes: Mintlify excludes `README.md` and `CONTRIBUTING.md` from pages. Keep relative Markdown links explicit (`./page.md` or `../page.md`) so they work in the repository and distribution. Each published Markdown page needs a matching `.md`-to-page redirect in `docs.json` for website navigation. Links to repository-only guides and source files should point to GitHub. Write literal angle-bracket placeholders inside code spans, and use Markdown reference comments (`[//]: # (comment)`) for generated-region markers so the sources render in both GitHub and Mintlify. Change generated content through its generator.

Find the owning source in the [documentation ownership map](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#documentation-ownership) and follow the [documentation rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#documentation). Readers use the authored Markdown in the repository. Generated OpenAPI schemas and the Harness catalog have their own generators; see [Contract and schema rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#contract-and-schema-rules).

The distribution has an explicit documentation list in `scripts/core-distribution-manifest.py`. When you move a bundled file or change a heading, update its inbound links and run the [distribution documentation checks](./maintainers.md#build-a-distribution).
