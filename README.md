# Parsar Core

![One core. Many agents. Open infrastructure for AI agents.](docs/assets/openagentcore-banner.png)

**Open-source Agents API infrastructure, with your choice of native harness.**

Run Codex, Claude Code and MiniMax Code behind one execution API. Parsar Core
owns Sessions, environments, files, credentials and execution history; each
native harness keeps its own model and tool loop. Core runs independently of the
Parsar product.

Core and its administrator Web console ship together. Projects own assets; multiple
API keys in one Project share its assets and execution principal. Projects and API
keys live in the database. Management credentials cannot call the Agent API. The
default installation runs Core, Web
and PostgreSQL with zero execution nodes. Add execution nodes through Web when
you are ready. Core creates each required sandbox from the shared Runtime image. Model
credentials are supplied through the existing write-only API extension.

Hosted deployments select E2B cloud or one provider across their own local/remote
nodes (Docker or microsandbox). The
Hosted Sandbox Manager shows node health, capacity and Session placement. New
Sessions use automatic placement by default or an explicitly selected node;
existing Sessions retain their node across disconnects and resume.

## Start here

The management backend requires the coordinated Web screen switch before a paired
release; see [console integration status](docs/web/README.md).

1. **Install Core and Web.** Obtain and verify a matching Linux amd64 bundle,
   then run its installer. For node access, choose a reachable HTTPS address
   before the first install and configure your DNS/TLS reverse proxy:

   ```sh
   ./install.sh --public-url https://core.example
   ```

   This starts Core, Web and PostgreSQL with zero execution nodes. Release
   bundles are tied to a source revision; an older published bundle does not include
   current management changes. See the [installation guide](docs/getting-started/install.md)
   for obtaining/building a matching bundle and the host/network prerequisites.
2. **Sign in to Web.** Open the console address printed by the installer and
   register your administrator account with a username and password. Keep them safe.
   Existing installations retain their `admin` / `console.password` login.
   The console connects to Core automatically. Until the management screens migrate,
   use the [administrator API](contracts/agents-api/admin-api.md) to create a Project,
   then issue a key within it for your application. Save the one-time plaintext
   response privately; Core stores its digest. Rotate by issuing another key in the
   same Project and revoking the old one.
3. **Add a node.** Open **Hosted Sandbox Manager** and choose E2B cloud or
   your own machines with Docker/microsandbox. E2B needs its account credentials and
   qualified Runtime template, with no node installation. For your own machines,
   initialize the deployment. The paired console address is used by default;
   advanced network settings allow a different reachable HTTPS origin. Select
   **Add node**, then copy and run the command on a prepared Linux host. Web shows when the node is online
   and its provider is ready. All nodes in a deployment use the same provider.

Installation and node enrollment do not call a model. Once a node is ready,
run an optional API example with your own model credentials.

- [Configuration reference](docs/configuration.md)
- [API documentation: public API and Web management](docs/api/README.md)
- [Make your first API request](docs/getting-started/quickstart.md)
- [Service health, data and operations](docs/getting-started/operations.md)
- [Hosted Sandbox Manager](services/agents-api/HOSTED-SANDBOX-MANAGER.md)
- [Protocol coverage and native differences](contracts/agents-api/README.md)
- [Add or select a harness](contracts/agents-api/harness-selection.md)
- [Public landing page source](site/index.html)

### Other installation options

Run these from an extracted distribution. The plain command uses loopback for
local console/API access. For node enrollment, use the reachable origin described
above; the installer does not change an existing installation's public URL.
Installing a local provider is optional, and is not required for adding nodes in Web.

```sh
./install.sh                    # Core + Web + PostgreSQL, loopback access, zero nodes
./install.sh --core-only        # Core + PostgreSQL, zero nodes
./install.sh --sandbox-provider true --provider microsandbox
./install.sh --sandbox-provider true --provider docker
```

Web-only installation connects the console server to an existing Core; see the
installation guide for its URL and private credential-file options. Installation
creates no Project or application key, never creates a sample Session and calls no
model. Configuration files hold deployment settings, not business identities. API
examples are optional.

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
harness, tools and workspace. Caller-owned E2B provisioning uses the official SDK; deployment-managed E2B
uses the configured SandboxProvider. For caller-owned environments, the returned `remote_url` connects our daemon, not `exec-server`.
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
