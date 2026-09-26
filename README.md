# Parsar Core

![One core. Many agents. Open infrastructure for AI agents.](docs/assets/openagentcore-banner.png)

**Open-source Agents API infrastructure, with your choice of native harness.**

Run Codex, Claude Code and MiniMax Code behind the OpenAI Agents API, on machines you
control. Core serves the API and runs each Session in a sandbox; Web is the
administrator console that issues the keys applications call Core with.

## Get started

1. **Install Core and Web with one command.** On a Linux amd64 host with Docker,
   download a release bundle and run its installer. The repository is internal for
   now, so sign in to GitHub first. Pick `<tag>` from
   `gh release list --repo MiniMax-AI/parsar-core`:

   ```sh
   gh auth login
   gh release download <tag> --repo MiniMax-AI/parsar-core --pattern '*-linux-amd64-offline.tar.gz*'
   sha256sum -c parsar-core-<commit>-linux-amd64-offline.tar.gz.sha256
   tar -xzf parsar-core-<commit>-linux-amd64-offline.tar.gz
   cd parsar-core-<commit>-linux-amd64
   ./install.sh --public-url https://core.example
   ```

   Put your HTTPS reverse proxy in front first, or install without `--public-url` for
   a local trial and set it later. See the
   [installation guide](docs/getting-started/install.md). If you're reading this on
   GitHub, follow the docs inside the downloaded bundle (`README.md` and `docs/`)
   instead: they match its installer, while GitHub shows the current source.
2. **Sign in to Web with the Core key**, from `~/.parsar/core/secrets/core.key`. On
   **System**, set a default model; on **Projects and keys**, create a project and
   issue a key. See [Sign in to Web](docs/getting-started/install.md#sign-in-to-web).
3. **Add a node by pasting one command.** On **Nodes**, choose **Add node**, then
   **Generate command**, and run the command on a Linux host with sudo. See
   [Nodes](docs/getting-started/nodes.md).

Web's **Overview** tracks these steps, and the first Session, in its **Getting started**
checklist; they can be done in any order.

Applications then set `OPENAI_BASE_URL` to `https://core.example/v1` and
`OPENAI_API_KEY` to the Project API key, and use the official OpenAI SDK; see
[Call the API](docs/getting-started/quickstart.md).

## What you get

- **One API, several harnesses.** Core implements part of the pinned OpenAI Agents API
  (`openai-python` 3.13.0, `agents=v1`). Each Session runs a native harness, Codex,
  Claude Code or MiniMax Code, which keeps its own model and tool loop. Core's additions
  live only in `x_agents_core`: the harness and the model provider.
- **Durable execution.** Core owns Agents, Sessions, Turns, Items, environments, files
  and credentials, in its own PostgreSQL database.
- **Your sandboxes.** Core-hosted Sessions run in sandboxes on your nodes (Docker or
  microsandbox microVMs) or on E2B. Applications can also connect their own machines as
  self-hosted executors.
- **A console for administrators.** Web signs in with the Core key, issues Project API
  keys, adds nodes, sets default models and shows metrics and Session history. It
  never runs Agents on anyone's behalf.
- **Independent of the Parsar product.** Core needs no product service or database.

A passing workflow does not establish complete OpenAI Agents API compatibility; the
[protocol coverage](contracts/agents-api/README.md) records what is qualified and the
native differences.

## Documentation

| Page | Covers |
| --- | --- |
| [Install Core and Web](docs/getting-started/install.md) | Prerequisites, download, installer options, HTTPS and the reverse proxy, first sign-in |
| [Nodes](docs/getting-started/nodes.md) | Adding, removing and troubleshooting nodes |
| [Self-hosted executors](docs/getting-started/self-hosted.md) | Connecting an application's own machine to a Session |
| [Operations](docs/getting-started/operations.md) | The `parsar` command, the Core key, backups, upgrades, troubleshooting |
| [Configuration reference](docs/configuration.md) | Every setting in `config.json` and in Web |
| [Call the API](docs/getting-started/quickstart.md) | The application developer's quickstart |
| [API reference](docs/api/README.md) | The `/v1`, `/core/v1` and `/api/v1` namespaces |
| [Nodes and sandbox backends: operator reference](services/agents-api/HOSTED-SANDBOX-MANAGER.md) | Node protocol, manual registration, placement, maintenance |
| [Protocol coverage](contracts/agents-api/README.md) and [harness selection](contracts/agents-api/harness-selection.md) | Supported operations and native differences |
| [Landing page source](site/index.html) | The public site |

### Maintainers and advanced deployments

Building distributions, producing releases and running Core alone, without Web or the
installer, are described in [Maintainers and advanced deployments](docs/maintainers.md).
The standalone Core archive and container are not an installation path for new users.

## Develop and build

The repository includes the API, its independent PostgreSQL migrations, daemon,
Runtime and provider adapters, clients, Web console and distribution tools. It has no
Parsar product service, product database or business-user dependency.

```sh
make build-agents-api
make build-daemon
pnpm dev:web
```

Use the toolchain pinned in `go.mod`, Node 22 and pnpm 10.30.3. Build output goes under
`~/.oac/build/`. See the [service guide](services/agents-api/README.md),
[Docker Runtime](services/agents-api/deploy/codex/README.md),
[microsandbox provider](services/agents-api/deploy/microsandbox/README.md) and
[Web development guide](docs/web/README.md).

Core-managed and user-managed environments reuse the colocated daemon, native harness,
tools and workspace. Caller-owned E2B provisioning uses the official SDK;
deployment-managed E2B uses the configured sandbox provider. For caller-owned
environments, the returned `remote_url` connects our daemon, not `exec-server`. See the
[Runtime enrollment guide](services/agents-api/README.md#user-managed-runtime-enrollment).

Read [CONTRIBUTING.md](CONTRIBUTING.md) before developing. Historical source-copy
provenance is retained in [provenance/README.md](provenance/README.md). Existing Go
import paths resolve inside this repository and do not require the product repo.
Third-party native packages remain pinned build dependencies.

## Validate

On Linux with Go, Node 22, pnpm 10.30.3, Python 3.9+, Rust 1.95.0 (including
rustfmt/Clippy), OpenSSL development libraries, Chrome for Playwright, and a
dedicated test PostgreSQL:

```sh
export OAC_TEST_DATABASE_URL='postgres://.../oac_core_tests?sslmode=disable'
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
