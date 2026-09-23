# OpenAgentCore

![OpenAgentCore: One core. Many agents. Open infrastructure for AI agents.](docs/assets/openagentcore-banner.png)

**Open-source Agents API infrastructure, with your choice of native harness.**

Run Codex, Claude Code and MiniMax Code behind one execution API. OpenAgentCore
owns Sessions, environments, files, credentials and execution history; each
native harness keeps its own model and tool loop. Core runs independently of the
Parsar product.

Core and its Web console ship together. The default installation runs Core, Web
and PostgreSQL with zero execution nodes. A local sandbox provider is optional:
enable microsandbox or Docker explicitly when installing. With a provider enabled,
Core creates each required sandbox from the colocated Runtime image. Model
credentials are supplied through the existing write-only API extension.

Hosted deployments use one selected provider across local or remote nodes. The
Hosted Sandbox Manager shows node health, capacity and Session placement. New
Sessions use automatic placement by default or an explicitly selected node;
existing Sessions retain their node across disconnects and resume.

## Start here

- [Install Core and Web](docs/getting-started/install.md)
- [Make your first API request](docs/getting-started/quickstart.md)
- [Service health, data and operations](docs/getting-started/operations.md)
- [Hosted Sandbox Manager](services/agents-api/HOSTED-SANDBOX-MANAGER.md)
- [Protocol coverage and native differences](contracts/agents-api/README.md)
- [Add or select a harness](contracts/agents-api/harness-selection.md)
- [Public landing page source](site/index.html)

After verifying and extracting a matching Linux amd64 distribution:

```sh
./install.sh                    # Core + Web + PostgreSQL, no sandbox provider
./install.sh --core-only        # Core + PostgreSQL, no sandbox provider
./install.sh --sandbox-provider true --provider microsandbox
./install.sh --sandbox-provider true --provider docker
```

Web-only installation connects the unchanged console to an existing Core; see the
installation guide for its URL and private credential-file options. Installation
never creates a sample Session or calls a model. API examples are optional.

The protocol baseline is `openai-python` 3.13.0 and `agents=v1`. Harness selection,
model execution configuration and our daemon transport are documented differences.
A passing workflow does not establish complete OpenAI Agents API compatibility.

## Develop and build

The repository includes the API, its independent PostgreSQL migrations, daemon,
Runtime/provider adapters, clients, Web console and distribution tools. It has no
Parsar product service, product database or business-user dependency.

```sh
make build-agents-api
make build-daemon
pnpm dev:web
```

Use the toolchain pinned in `go.mod`, Node 22 and pnpm 10.30.3. Build output goes
under `~/.parsar/build/`. For advanced deployment, see the
[service guide](services/agents-api/README.md),
[Docker Runtime](services/agents-api/deploy/codex/README.md),
[microsandbox provider](services/agents-api/deploy/microsandbox/README.md), and
[Web development guide](docs/web/README.md).

Core-managed and user-managed environments reuse the colocated daemon, native
harness, tools and workspace. E2B uses caller-managed provisioning through the
official SDK; the returned `remote_url` connects our daemon, not `exec-server`.
See the [Runtime enrollment guide](services/agents-api/README.md#user-managed-runtime-enrollment).

Read [CONTRIBUTING.md](CONTRIBUTING.md) before developing. Historical source-copy
provenance is retained in [provenance/README.md](provenance/README.md). Existing Go
import paths resolve inside this repository and do not require the product repo.
Third-party native packages remain pinned build dependencies.

## Validate

On Linux with Go, Node 22, pnpm 10.30.3, Python 3.9+, Rust 1.95.0 (including
rustfmt/Clippy), OpenSSL development libraries, Chrome for Playwright, and a
dedicated test PostgreSQL:

```sh
export PARSAR_AGENTS_API_TEST_DATABASE_URL='postgres://.../parsar_agents_api_core_tests?sslmode=disable'
make check
```

Install the pinned Playwright browser on a fresh validation host with
`pnpm exec playwright install --with-deps chrome`. Fixture acceptance uses
loopback ports `18092` and `4174`; set `AGENTS_FIXTURE_PORT` and
`AGENTS_WEB_PORT` to unused ports when either default is occupied.

The full gate requires the test database rather than silently skipping persistence
tests. Native model/provider fixtures remain explicit, credential-dependent
acceptance checks; see the [coverage ledger](contracts/agents-api/README.md).
The Rust gate covers only the retained directory/write/export helpers; the former
separate Codex harness gate and remote probe suite are retired.
Importing existing implementations does not establish additional protocol coverage.
