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

Install Go at the version in [go.mod](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/go.mod), Node 22.13 or newer, pnpm at the version in [package.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/package.json), and Python 3.9 or newer. The complete gate runs on Linux and needs a dedicated PostgreSQL database, OpenSSL development libraries for the microsandbox helper, pigz for distribution compression, and a Playwright browser. Provider and Runtime builds have additional prerequisites in their component guides.

```sh
make node-deps
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
pnpm --filter @oac/web exec playwright install --with-deps chrome
export OAC_TEST_OFFICIAL_SDK_PYTHON="$PWD/.venv/bin/python"
```

### Node dependency boundaries

Each pnpm module owns a `pnpm-lock.yaml` beside its `package.json`. Website and Claude SDK adapter are independent pnpm projects with their own workspace boundaries. The Web/example/client workspace uses `sharedWorkspaceLockfile: false`: it links declared workspace dependencies without sharing dependency resolution or a virtual store. Root scripts only orchestrate module commands and have no installed tool dependencies. Declare build and test tools in the module that imports or executes them. The MiniMax companion uses its own npm manifest and `package-lock.json` and is outside the pnpm workspace.

Install one module with `pnpm --dir website install --frozen-lockfile`, or include its workspace dependencies with `pnpm --filter @oac/web... install --frozen-lockfile`. Add or update dependencies through the same package filter and commit that module's manifest and lockfile. `make node-deps` installs all pnpm modules for full local validation. Web and the example share `packages/agents-client` through explicit `workspace:*` dependencies; their checks install that client too. Component Make targets use filtered installs and checks. CI caches use only the job's dependency locks; the [CI selection policy](./maintainers.md#continuous-integration) owns which checks a change selects.

Set `OAC_TEST_DATABASE_URL` privately to a dedicated PostgreSQL test database. Never point the test suite at an installation or product database. The [test database rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#test-database) list the required role permission.

For native package pin changes, follow the [live acceptance rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#live-acceptance).

## Install a checkout without publishing a release

Use this path to try the complete Core, Web and managed sandbox nodes from a committed checkout. It uses the same distribution builders, Compose services, initialization and node installer as a release. Building requires network access for pinned upstream dependencies; the resulting offline bundle needs no OpenAgentCore release or image registry. Build and install on Linux amd64 with the [distribution prerequisites](./maintainers.md#build-a-distribution). Docker Engine 26+ and Compose 2.26+ are required. The source must be committed, including your local changes.

From the repository root, prepare the pinned Runtime inputs once, then build and install. If `~/.oac/build/release-inputs/inputs.json` already exists, skip the first command and reuse it:

```sh
bash scripts/prepare-release-runtimes.sh
inputs="$HOME/.oac/build/release-inputs/inputs.json"
export CODEX_CLI_DIR="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["codex"])' "$inputs")"
export MCODE_HARNESS_BUILD_DIR="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["mcode"])' "$inputs")"
revision="$(git rev-parse HEAD)"
export CORE_DISTRIBUTION_BUILD_DIR="$HOME/.oac/build/source-$revision"
export CORE_DISTRIBUTION_OFFLINE=1
export CORE_DISTRIBUTION_PLATFORMS=linux/amd64
unset CORE_DISTRIBUTION_RELEASE_BASE_URL
make build-core-distribution
bundle="$CORE_DISTRIBUTION_BUILD_DIR/oac-$revision-linux-amd64"
python3 "$bundle/install.py" \
  --install-dir "$HOME/.oac/local" \
  --host 0.0.0.0 --web-port 8080 \
  --public-url http://YOUR_CORE_HOST:8080
```

Replace `YOUR_CORE_HOST` with an address reachable by your browser and sandbox nodes. Use your HTTPS origin behind a reverse proxy when needed; [installation options](./getting-started/install-options.md) owns ingress configuration. The installer prints the Core key for signing in. It creates the standard Compose installation plus a read-only `node-payload` directory containing the bundle's public node artifacts. Allow disk space for both the extracted bundle and this installed copy. No Docker socket is passed to Core or Web.

The build also creates `oac-<commit>-linux-amd64-offline.tar.gz`. Copy and extract that archive on another Linux amd64 Docker host, then run its `install.py`; no checkout, Go, Node or pnpm is needed on that host, only Python 3.9+ and Docker. Keep the extracted bundle until installation completes. The installed services and node downloads do not depend on its original location afterward.

In Web, configure a model provider, create a sandbox deployment and add nodes with the command shown in [Nodes](./getting-started/nodes.md). Node prerequisites still apply, including KVM for microsandbox. The command downloads the matching execution artifacts directly from this installation. This path builds all three managed Harnesses; optional self-hosted native installers are unavailable unless you supply their [native installer catalog](./maintainers.md#native-installers). It does not create a placeholder catalog.

For a startup failure or a stopped installation, use the saved configuration instead of reinstalling:

```sh
cd "$HOME/.oac/local"
docker compose logs --tail 100
# Retry without downloading release images or replacing saved configuration.
docker compose up -d --wait --pull never
./oac core-key --show
```

`install.py` requires an empty installation directory. Configuration preparation failures before `compose.yaml`, `.env` and `oac` exist require removing that incomplete directory and retrying. After those files exist, use Compose to retry. Use the [operator commands](./getting-started/operations.md) for configuration, keys and stopping services. A different source commit is a fresh installation, not an in-place upgrade: delete managed resources and uninstall nodes first, then remove the old Compose containers and data volume with `docker compose down --volumes`, and remove its installation directory. This permanently deletes that test installation's data. Alternatively choose another installation directory and Web port.

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
| `services/core/internal/persistence`, `services/core/internal/db` and `services/core/migrations` | Core's PostgreSQL adapters, transactions, queries and migrations | [Service guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#database) |
| `services/core/tests/integration` | Tests that drive the HTTP boundaries, the execution Worker and the PostgreSQL adapters together | [Service guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#tests) |
| `services/core/internal/execution` | Durable Turn dispatch and scheduling | [Runtime protocol](./runtime-protocol.md) |
| `services/core/internal/engine` | Pure qualification of Harness operations and placements | [Harness onboarding](../contracts/agents-api/harness-onboarding.md) |
| `internal/agentdaemon/proto` | Core–Runtime wire types and validators | [Runtime protocol](./runtime-protocol.md) |
| `internal/runtimebootstrap` | Provider-to-Runtime startup input | [Runtime bootstrap](./runtime-bootstrap.md) |
| `apps/daemon/internal/dispatch` | Runtime preparation, Executor reuse, Turn and cleanup ownership | [Harness lifecycle](../contracts/agents-api/harness-onboarding.md#required-adapter-interfaces) |
| `apps/daemon/internal/agent` | Native Harness adapters | [Native references](../contracts/agents-api/harness-onboarding.md#native-references) |
| `services/core/internal/sandbox` | Provider interfaces and managed compute lifecycle | [Provider onboarding](./sandbox-provider.md) |
| `services/web` | Console login and the server-side management proxy | [Console server](./web/console-server.md) |
| `apps/web` and `packages/agents-client` | Console UI and typed clients | [Web guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/README.md) |
| `deploy` and `scripts` | Distribution, installation and validation tools | [Maintainers](./maintainers.md) |
| `contracts/agents-api` | Pinned schema, semantic contracts and coverage ledger | [Coverage ledger](../contracts/agents-api/index.md) |

## Choose an extension boundary

Use the [protocol map](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#protocols-at-every-boundary) to find the code and guide for a new Harness, Sandbox Provider, model provider, API operation or Runtime message. The guide owns registration, supported operations and the checks that qualify an implementation. For workspace capabilities such as Skills, Plugins, MCP and system packages, start with [Environments](../contracts/agents-api/environments.md).

## Validate a change

Choose focused checks for the current diff and its directly affected behavior using [Checks for a change](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#checks-for-a-change). The table below lists entry points for each boundary; choose the relevant tests within them.

| Change | Focused validation |
| --- | --- |
| Core handlers, persistence or clients | `make check-core` |
| SQL queries | `make sqlc-generate`, inspect generated files, then `make check-sqlc` |
| Public schema, Core extensions or internal handler annotations | `make openapi`, `make check-openapi`; inspect generated types and all three namespace schemas |
| Shared Runtime protocol | `make check-runtime-contract` |
| Provider integration | `make check-sandbox-provider-contract` and the provider's native checks |
| Claude SDK bridge and artifact | `make check-claude-sdk` |
| Web UI and clients | `make check-web` |
| Distribution or installer | `make check-distribution` |
| Documentation | `make check-names`; `make check-distribution` validates Markdown links and bundled docs |

Fixture browser acceptance uses loopback ports 18092 and 4174. Select unused ports with `AGENTS_FIXTURE_PORT` and `AGENTS_WEB_PORT` when running parallel validation. Keep databases, ports and containers separate between validation workers. Compilation, fixture success and live model/provider acceptance establish different facts; report skipped or unavailable checks explicitly. Follow the [independent blind review workflow](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#review) after validation.

## Change documentation

The [website guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/website/README.md) owns documentation navigation, local preview, validation and GitHub Pages publication.

Use `index.md` for published section indexes. Keep relative Markdown links explicit (`./page.md` or `../page.md`) so they work in the repository and distribution. Links to repository-only guides and source files should point to GitHub. Write literal angle-bracket placeholders inside code spans, and use Markdown reference comments (`[//]: # (comment)`) for generated-region markers so the sources render in both GitHub and VitePress. Change generated content through its generator.

Find the owning source in the [documentation ownership map](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#documentation-ownership) and follow the [documentation rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#documentation). Readers use the authored Markdown in the repository. Generated OpenAPI schemas and the Harness catalog have their own generators; see [Contract and schema rules](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#contract-and-schema-rules).

The distribution has an explicit documentation list in `scripts/core-distribution-manifest.py`. When you move a bundled file or change a heading, update its inbound links and run the [distribution documentation checks](./maintainers.md#build-a-distribution).
