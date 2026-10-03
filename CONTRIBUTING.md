# Contributing to OpenAgentCore

This guide owns how to work in the repository: documentation ownership, the repository boundary, workflow and review, required checks and naming. Every other subject has one canonical owner, listed below.

## Documentation ownership

| Subject | Canonical source |
| --- | --- |
| Design principles, public API fidelity, settings and data ownership, documentation rules | [AGENTS.md](AGENTS.md) |
| Protocol code and documents at each component boundary | [Protocol map](AGENTS.md#protocols-at-every-boundary) |
| Projects, keys, resource isolation, administrator authority, secrets and audit concepts | [Concepts and ownership](docs/concepts.md) |
| Component responsibilities and Session flow | [Architecture](docs/architecture.md) |
| Developer setup, repository map and focused checks | [Develop OpenAgentCore](docs/development.md) |
| API callers, credentials and route inventory | [API index](docs/api/index.md) |
| Public wire semantics and protocol coverage | [Agents API contracts](contracts/agents-api/index.md) |
| Machine connection routes | [Machine connection API](contracts/agents-api/machine-api.md) |
| Core service setup, tests and generation | [Core service guide](services/core/README.md) |
| Core implementation constraints beyond the public contracts | [Implementation constraints](services/core/IMPLEMENTATION.md) |
| Environment ownership, preparation, Skills, Plugins, packages and MCP bindings | [Environments](contracts/agents-api/environments.md) |
| Built-in Harness identifiers and display names | `internal/harnessconfig/builtin/catalog.json` and its [generated reference](contracts/agents-api/harness-catalog.md) |
| Harness registration, service qualification and acceptance | [Harness onboarding](contracts/agents-api/harness-onboarding.md) |
| Harness capabilities by placement | [Harness capabilities](contracts/agents-api/harness-capabilities.md) |
| Harness selection, model providers and native parameters | [Model execution](contracts/agents-api/model-execution.md) |
| Provider registration and lifecycle | [Sandbox Provider guide](docs/sandbox-provider.md) |
| Sandbox deployment, selection and administrative transitions | [Sandbox deployment](contracts/agents-api/sandbox-deployment.md) |
| Operator node tasks | [Nodes guide](docs/getting-started/nodes.md) |
| Codex, Claude and MiniMax Runtime adapters and images | [Codex](services/core/deploy/codex/README.md), [Claude](services/core/deploy/claude/README.md), [MiniMax](services/core/deploy/mcode/README.md) |
| Claude private SDK bridge | [Claude SDK adapter](packages/claude-sdk-adapter/README.md) |
| E2B template construction | [E2B template builder](services/core/deploy/e2b/README.md) |
| E2B and microsandbox Provider helper implementation | [E2B helper](services/core/tools/e2b-provider/README.md), [microsandbox helper](services/core/tools/microsandbox-provider/README.md) |
| Runtime telemetry responses | [Runtime telemetry API](contracts/agents-api/runtime-observability-api.md) |
| Runtime observation, sampling, retention and export | [Runtime observability](contracts/agents-api/runtime-observability.md) |
| Distribution builds, Runtime image builds, CI and publication | [Maintainer guide](docs/maintainers.md) |
| Website: landing page, bilingual documentation maintenance, documentation site build and GitHub Pages publication | [Website guide](website/README.md) |
| Self-hosted Runtime installation, recovery and local operation | [Self-hosted execution](docs/getting-started/self-hosted.md) |
| Installer lifecycle, locking, generated state, managed HTTPS and downloads | [Deployment](deploy/README.md) and [Node installer](deploy/node/README.md) |
| Operator installation and alternatives | [Installation](docs/getting-started/install.md), [installation options](docs/getting-started/install-options.md) |
| Settings, defaults, files and installation layout | [Configuration](docs/configuration.md) |
| Operator commands, keys, backup and version policy | [Operations](docs/getting-started/operations.md) |
| Web console request boundary and sign-in | [Console server](docs/web/console-server.md) |
| Web page behavior and visual rules | [Web product](apps/web/PRODUCT.md), [Web design](apps/web/DESIGN.md) |
| Application example behavior and local operation | [Application example](example/parsar/README.md) |

## Repository boundary

This repository contains the Core API and database, Runtime daemon, Harness and Sandbox Provider adapters, shared protocol packages, Web administrator console and their build and test tools. Product applications stay outside that service boundary. Keep the external Parsar product's `server/`, `apps/parsar/`, CLI, plugins and deployment stack in its own repository; do not automatically sync or delete its Core copy. Go imports resolve through this repository's module.

### Product and execution service separation

Core must build, deploy and run independently of product services, frontends and databases. Applications follow the [public API boundary](AGENTS.md#public-api); their feature backlogs do not define Core's public protocol or storage model. Applications that share a PostgreSQL server with Core must use separate databases, credentials and migrations.

- Parsar owns users, workspaces, business authorization, Agent/Team definitions, capabilities, product conversations, IM/sharing, approval decisions and billing. It uses Core for execution.
- A product conversation may reference several execution Sessions. Core owns native engine session identities; an execution Session has its own lifetime, separate from a daemon connection, process or sandbox.
- Build application orchestration on the [public Session and event contract](docs/api/public-agent-api.md). Product cursor replay must be an explicit product extension. Business Team orchestration belongs to the application; Core's pinned `multi_agent` and Subagent resources remain part of the public contract.
- Daemon Skill/SP authoring is a product operation: forward it through a scoped product callback that checks the original requester and workspace. A Runtime credential alone must not authorize business writes.

### Optional application example

`example/parsar/` is an optional Agent workbench in this repository. Its [README](example/parsar/README.md) owns its product behavior.

- It calls only public `/v1` APIs. Its Project key stays server-side; it never holds a Core key or issues machine credentials. Self-hosted connection displays the public Session installation command unchanged; Core owns bootstrap authorization and machine credential issuance.
- It may keep a small product-owned SQLite database (Node's built-in module, Node 22.13+), outside the checkout and isolated by Core origin and Project key fingerprint. Provider keys never reach the browser.
- Core owns Skills and all execution and history state. The example stores only Session references and pending creation requests with stable idempotency keys.
- Product resources use `/app/` and never become Core API or database conventions.
- It is excluded from Core distributions and cannot become a service dependency.

## Workflow and review

### Before you start

Follow the [worktree rule](AGENTS.md#working-in-this-repository). Do not edit or commit implementation directly on `main`.

When documents conflict, apply the latest explicit user decision and update the affected current guidance. Recorded evidence does not override it.

If requirements are unresolved, object ownership is unclear, or a design would need parallel compatibility paths, raise it with a concrete recommendation and tradeoffs before implementing. Continue independent work meanwhile. Do not silently preserve obsolete private designs.

Record unrelated findings without starting them. Scope compatibility claims to the operations and placements verified.

### Implementation conventions

- Keep one formatter, parser, validator and error mapper per job, and one error mapper per API surface.
- Share frontend formatting and labels in `apps/web/src/lib/`.
- Use `internal/obs/log` for logs. Keep credentials out of source and logs. Harness profiles must not copy Runtime tool environment values; see the [environment contract](contracts/agents-api/environments.md#explicit-local-tool-environment).
- Require absolute user-supplied working directories.
- Keep test artifacts under `~/.oac/`.
- New or changed routes identify their caller and credential in the [API index](docs/api/index.md) and link their detailed contract.

### Review

1. After implementation and validation, have a fresh independent subagent review the complete diff.
2. Give it only the requirements, acceptance criteria, boundaries, repository path and comparison baseline. Do not give an implementation summary, self-assessment or earlier findings. Explain these criteria to the user.
3. Fix substantiated in-scope findings, validate, then use another fresh reviewer.
4. If the cycle repeats, reassess design and scope before adding changes. Report an unresolved blocker instead of broadening the task.

Do not use `codex exec` as a substitute reviewer.

## Required checks

Toolchain setup and focused commands are in [Develop OpenAgentCore](docs/development.md#set-up-a-checkout). CI coverage, caches and release publication are owned by the [maintainer guide](docs/maintainers.md#publish-a-version).

### Checks for a change

Validate the current diff and the behavior it directly affects, with the smallest checks that establish correctness. Include migration or cross-component tests only when those behaviors change. A review, documentation edit or CI configuration change does not need a full repository test run. After a follow-up edit, rerun only the checks that edit affects, and record what passed and any limits.

The [CI selection policy](docs/maintainers.md#continuous-integration) names affected groups. A narrower check is enough when it covers the change. `make check` is the full gate for an explicitly requested full validation and for releases. [Live acceptance](#live-acceptance) applies when native execution behavior changes.

### Test database

| Variable | Value |
| --- | --- |
| `OAC_TEST_DATABASE_URL` | A dedicated test database. The full gate fails when it is missing. |
| `OAC_TEST_OFFICIAL_SDK_PYTHON` | The pinned official SDK interpreter |

The role needs `CREATE DATABASE`: tests of database-wide state, such as the execution lease and the provider identity, create and drop isolated `oac_*_tests` databases. Tests must not bypass the production provider-switch guard.

### Contract and schema rules

- `internal/harnessconfig/builtin/catalog.json` is the single authored public Harness registration list. `make generate-harness-catalog` generates Go configuration/profile registration, client identifiers/names and the reference; `make openapi` derives the matching enums. `make check-harness-catalog` verifies freshness in the full gate. Native configuration rules stay in their adapter declarations; Core qualification and Runtime availability stay separate.
- `make sqlc-generate` owns only `services/core/internal/db/sqlc` (sqlc v1.29.0). Do not rewrite landed migrations.
- `make check-runtime-contract` is the focused Core–Runtime contract entry point; see [Contract verification](docs/runtime-protocol.md#contract-verification). It also runs through `check-go` and `check-core`.

### Compatibility evidence

Use official SDKs and upstream types or schemas. Validate raw HTTP payloads and observable workflows alongside SDK behavior. API changes preserve the pinned contracts, coverage ledger, official-client tests and Core's independent build. Verify the official-client workflow before application integration. An OpenAI endpoint is a test target only when the required capabilities and credentials are available.

Controlled fixtures and synthetic model responses qualify deterministic behavior. Live acceptance calls a real model API through Core, the daemon and the Harness adapter. Direct native probes establish feasibility only. Keep provider credentials in private test configuration, outside source, logs and task records.

For wire details the pinned SDK does not specify, probe resources you own and retain the request evidence. In the [coverage ledger](contracts/agents-api/index.md), keep observed behavior distinct from guarantees, accepted profiles distinct from complete coverage, and provider connectivity distinct from deployment qualification.

### Live acceptance

Native adapter changes require their build/check targets and live provider acceptance. Follow [Harness qualification](contracts/agents-api/harness-onboarding.md#qualify-the-adapter) for native model execution. State which checks ran, which used fixtures and which lacked prerequisites. Changes to native package pins require the same qualification; build the MiniMax companion from this revision's pinned patched sources.

## OpenAgentCore Runtime names

| Surface | Current name |
| --- | --- |
| Runtime binary | `oac-daemon` |
| Filesystem and initialization helpers | `oac-*` |
| Runtime settings | `OAC_RUNTIME_*` |
| Reserved Environment `env` prefix | `OAC_` |
| Provider ownership labels | `io.oac.*` |
| E2B metadata | `oac_*` |

Provider bootstrap, Runtime images and Harness adapters must agree on these names. The separate Parsar product integration settings keep their own names.

The [installation version policy](docs/getting-started/operations.md#installation-version-policy) owns release changes and preservation of installed data and resources.

## Branding

Use OpenAgentCore for public project branding. The canonical mark is `docs/assets/openagentcore-logo.svg`; Web uses its outline with cropped transparent margins, theme-aware favicon colors and dark-surface inversion. The README banner is `docs/assets/openagentcore-banner.jpeg`. The `example/parsar/` workbench keeps its own name, logo and favicon. Preserve external repository URLs and data identifiers when changing display copy.

## OpenAgentCore name guard

`make check-names` scans tracked text for retired branding, GitHub organization, settings and installed command names. Each exception in `scripts/name-allowlist.json` names a path glob, a regular expression and a reason.

- An exception covers only its matched text: an allowed repository import cannot hide a retired setting elsewhere on the line.
- Keep exceptions narrow and explain the preserved contract or detection input.
- The guard fails on an exception that excuses no retired identifier. Remove an exception together with the last text it covers.

These identities stay unchanged:

- public `AgentCoreError`, upstream contract fields and the separate Parsar product;
- persisted credential encryption domains and native-session resume keys, so existing data can be decrypted and Sessions can resume.

Detection inputs name the identifiers they reject. Landed migrations keep their original identifiers; application and operator examples use the current names.
