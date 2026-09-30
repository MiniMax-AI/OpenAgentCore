# Contributing to OpenAgentCore

This guide owns how to work in the repository: documentation ownership, the repository boundary, workflow and review, required checks and naming. Every other subject has one canonical owner, listed below.

## Documentation ownership

| Subject | Canonical source |
| --- | --- |
| Design principles, including public API fidelity and the storage rule, and documentation rules | [AGENTS.md](AGENTS.md) |
| Protocol boundaries: each boundary's protocol code and document | [AGENTS.md](AGENTS.md#protocols-at-every-boundary) |
| User concepts and authority | [Concepts and ownership](docs/design-principles.md) |
| Architecture overview, component responsibilities and diagrams | [Architecture](docs/architecture.md) |
| Developer setup, repository map and focused checks | [Develop OpenAgentCore](docs/development.md) |
| API callers, credentials and route inventory | [API index](docs/api/README.md) |
| Public wire types and qualified behavior | [Agents API contracts](contracts/agents-api/README.md), [pinned upstream](contracts/agents-api/upstream.json), and linked operation contracts |
| Core service implementation constraints | [Agents API implementation constraints](services/core/IMPLEMENTATION.md) and [service README](services/core/README.md) |
| Environment ownership and capability preparation (Skills, Plugins, MCP, `packages.system`) | [Environments](contracts/agents-api/environments.md) |
| Built-in Harness identifiers, configuration/profile bindings and display names | `internal/harnessconfig/builtin/catalog.json` and its [generated reference](contracts/agents-api/harness-catalog.md) |
| Effective MCP bindings and credential authority | [Environment MCP](contracts/agents-api/environments.md#skills-plugins-and-environment-mcp) and `apps/daemon/internal/agent/mcp_binding.go` |
| Harness qualification and acceptance | [Harness integration](contracts/agents-api/harnesses.md) |
| Harness selection and Agent defaults | [Harness selection](contracts/agents-api/harness-selection.md) |
| Provider selection, sandbox deployment and E2B setup | [Sandbox deployment](contracts/agents-api/sandbox-deployment.md) |
| Hosted sandbox nodes | [Nodes guide](docs/getting-started/nodes.md) and [sandbox deployment contract](contracts/agents-api/sandbox-deployment.md) |
| Claude private bridge and Runtime artifact | [Claude SDK adapter](packages/claude-sdk-adapter/README.md) |
| MiniMax Code and Claude Runtime adapter rules | [MiniMax Code Runtime](services/core/deploy/mcode/README.md), [Claude Runtime](services/core/deploy/claude/README.md) |
| CI, distribution builds, installer lifecycle and managed HTTPS, release publication | [Maintainer guide](docs/maintainers.md) |
| Operator installation, installation layout and configuration | [Installation](docs/getting-started/install.md), [installation options](docs/getting-started/install-options.md), [configuration](docs/configuration.md), [operations](docs/getting-started/operations.md) |
| Core Web console server and sign-in | [Console server](docs/web/console-server.md) |
| Web components, interaction and visual rules | [Web design](apps/web/DESIGN.md) and [Web product](apps/web/PRODUCT.md) |
| Documentation website generation | [Docs app](apps/docs/README.md) |

## Repository boundary

This repository is the standalone execution substrate copied from Parsar at the revision in `provenance/source.json`. It holds the API and its migrations, the Runtime protocol and daemon, Harness adapters, shared execution packages, the standalone Core Web console and build/test tools.

Product users, workspaces, model catalogs, business assets, the Parsar product Web, product API and product migrations remain in Parsar. Do not import `server/`, `apps/parsar/`, product CLI/plugin packages or their deployment stack.

Preserve copied Runtime and protocol behavior. Go import paths use this repository's module and do not require fetching the original repository. The source snapshot and per-file hashes are an audit trail; future Core development need not preserve them. Do not automatically sync or delete the original repository's Core.

### Product and execution service separation

Agents API is the primary infrastructure deliverable. Parsar is an ordinary client and example application; its feature backlog must not dictate the execution service's public protocol or internal model. Agents API must build, deploy and run without the Parsar product service, frontend or database. An optional Compose deployment may install both services with one PostgreSQL instance, but separate databases, credentials and migrations. The product uses Core exclusively; it has no native daemon or HTTP Agent fallback.

- Parsar owns users, workspaces, business authorization, Agent/Team definitions, capabilities, product conversations, IM/sharing, approval decisions and billing.
- A product conversation may map to several execution sessions. An execution session is distinct from a live daemon socket, process or sandbox. Native engine session identifiers belong to the execution service.
- Establish single-Agent execution, approval, cancellation, idempotent submission, persisted recovery queries before Team orchestration. The upstream SSE stream is live-only; recover through Session/Turn/Items reads. Any additional product cursor replay must be documented as an extension, not upstream semantics. Agents API establishes single-Agent execution first; business Team loops are deferred. This does not exclude upstream `multi_agent` configuration or subagent resources from protocol coverage. Future business Team orchestration directly depends on `openai/openai-agents-python` in Parsar.
- Daemon Skill/SP authoring remains a product operation: forward through a scoped product callback with the original requester and workspace checks. A runtime credential alone must not grant business write permissions.

### Optional application example

`example/parsar/` is an optional, independently started Agent workbench. Its [README](example/parsar/README.md) owns its product behavior. The boundary rules are:

- It calls only public `/v1` APIs. Its Project key stays server-side; it never holds a Core key or issues machine credentials. Core-only credential issuance stays in the operator console.
- It may reuse product UI and keep a small product-owned SQLite database (Node's built-in module, Node 22.13+), outside the checkout and isolated by Core origin and Project key fingerprint. Provider keys never reach the browser.
- Core owns Skills and all execution and history state. The example stores only Session references and pending creation requests with stable idempotency keys.
- Product resources use `/app/` and never become Core API or database conventions.
- It is excluded from Core distributions and cannot become a service dependency.

## Workflow and review

### Before you start

1. Record the requirements, acceptance criteria and scope.
2. Work in an isolated Git worktree on a feature branch and submit a PR. Do not edit or commit implementation directly on `main`.
3. Make only the changes that scope needs; keep unrelated refactors separate.

When documents conflict, apply the latest explicit user decision and update the affected current guidance. Recorded evidence does not override it.

If requirements are unresolved, object ownership is unclear, or a design would need parallel compatibility paths, raise the issue with a concrete recommendation and tradeoffs before implementing it. Continue independent work meanwhile. Do not silently preserve obsolete private designs.

Record unrelated findings without automatically starting them. Do not claim a broader compatibility target is complete from one merged batch.

### Implementation conventions

- Search for existing formatters, parsers, validation and error mappers before adding one. Keep one error mapper per API surface.
- Share frontend formatting and labels in `apps/web/src/lib/`; reuse components and tokens.
- Split growing files at an existing ownership boundary instead of adding unrelated responsibilities.
- Use `internal/obs/log` for logs. Keep credentials out of source and logs. Harness profiles must not copy Runtime tool environment values; see the [environment contract](contracts/agents-api/environments.md#explicit-local-tool-environment).
- Require absolute user-supplied working directories.
- Keep test artifacts under `~/.oac/`.
- New or changed routes identify their caller and credential in the [API index](docs/api/README.md) and link their detailed contract.

### Review

1. After implementation and validation, have a fresh independent subagent review the complete diff.
2. Give it only the requirements, acceptance criteria, boundaries, repository path and comparison baseline. Do not give an implementation summary, self-assessment or earlier findings. Explain these criteria to the user.
3. Fix substantiated in-scope findings, validate, then use another fresh reviewer.
4. If the cycle repeats, reassess design and scope before adding changes. Report an unresolved blocker instead of broadening the task.

Do not use `codex exec` as a substitute reviewer.

## Required checks

Toolchain setup and focused commands are in [Develop OpenAgentCore](docs/development.md#set-up-a-checkout). CI coverage, caches and release publication are owned by the [maintainer guide](docs/maintainers.md#publish-a-version).

### Full gate

Run `make check` before completion. It includes:

- all daemon and shared Go tests, including native daemon filesystem tests;
- Core contract, client and service tests and standalone API builds;
- a real dedicated PostgreSQL test database and byte-for-byte sqlc checks;
- Core Web and TypeScript client checks, including fixture-only Playwright acceptance;
- Claude SDK tests and packaging, and MiniMax companion checks;
- the Core distribution and installer gates;
- `make check-example` for the optional application example (TypeScript, proxy/persistence tests, build and fixture browser acceptance). Its synthetic responses are not live model qualification.

It excludes Parsar product Web and server gates.

### Test database

| Variable | Value |
| --- | --- |
| `OAC_TEST_DATABASE_URL` | A dedicated test database. The full gate fails when it is missing. |
| `OAC_TEST_OFFICIAL_SDK_PYTHON` | The pinned official SDK interpreter |

The role needs `CREATE DATABASE`: managed-provider tests create and drop isolated `oac_*_tests` databases because provider identity is deployment-wide. `PARSAR_AGENTS_API_TEST_DATABASE_URL` is retired; `make check-database` reports its replacement when only the old name is set. Tests must not bypass the production provider-switch guard.

### Contract and schema rules

- `internal/harnessconfig/builtin/catalog.json` is the single authored public Harness registration list. `make generate-harness-catalog` generates Go configuration/profile registration, client identifiers/names and the reference; `make openapi` derives the matching enums. `make check-harness-catalog` verifies freshness in the full gate. Native configuration rules stay in their adapter declarations; Core qualification and Runtime availability stay separate.

- `make sqlc-generate` owns only `services/core/internal/db/sqlc` (sqlc v1.29.0). Do not rewrite landed migrations.
- `make check-runtime-contract` is the focused Core–Runtime contract entry point; see [Contract verification](docs/runtime-protocol.md#contract-verification). It also runs through `check-go` and `check-core`.

### Compatibility evidence

- Use official SDKs for clients and reuse upstream types or schemas where suitable. SDK deserialization alone is not server validation or proof of compatibility: test raw HTTP payloads and observable workflows as well.
- When changing API behavior, preserve the pinned types, coverage ledgers and official SDK and raw HTTP tests. Core changes keep the independent build and official-client workflow.
- Verify an independent official-client workflow before a Parsar integration. An OpenAI endpoint is a possible client target only where the requested capabilities and credentials support it.
- Qualify public workflows through the common Runtime contract and Harness adapter; direct native probes establish feasibility only.
- Synthetic data and mock model responses may support controlled tests; live execution acceptance must call a real model API through the service, daemon and harness. A real daemon with a synthetic model does not constitute live model validation. Keep provider credentials in private test configuration, outside source, logs and task records.
- Owned-resource live probes can qualify status codes and wire details left unspecified by the SDK; retain request evidence and distinguish observations from guaranteed or fully covered behavior.
- Track partial coverage in `contracts/agents-api/README.md` until the complete target is verified. Reconcile current coverage summaries with merged routes and recorded acceptance; distinguish accepted profiles, partial implementation, missing operations and unverified semantics. Handler counts are not compatibility percentages, and an active provider probe is not deployment qualification.

### Live acceptance

Native adapter changes require their build/check targets and live provider acceptance. Real execution checks need real models; omitted prerequisites or mocked responses do not count as live acceptance. Historical remote native probes are not current validation entry points.

## OpenAgentCore Runtime names

| Surface | Current name |
| --- | --- |
| Runtime binary | `oac-daemon` |
| Filesystem and initialization helpers | `oac-*` |
| Runtime settings | `OAC_RUNTIME_*` |
| Reserved Environment `env` prefix | `OAC_` |
| Provider ownership labels | `io.oac.*` |
| E2B metadata | `oac_*` |

Provider bootstrap, Runtime images and Harness adapters must agree on these names. Daemon startup rejects renamed settings before any subcommand and reports replacements without values; the separate Parsar product integration settings remain unchanged. No old label is accepted as a fallback.

Historical Runtime and project-version upgrades are not supported. Do not ship retired installer conversion implementations; preserve rejection guards under the [installer lifecycle contract](deploy/install/README.md#versions-and-the-lock). Preserve older installations, Runtime files, provider resources and Session history; install the current release separately. Startup never verifies and rebinds historical allocations or accepts node deployments without a valid specification. Keep the original Core responsible for unresolved resources; see the [installation version policy](docs/getting-started/operations.md#installation-version-policy). Use this release's template builder for new E2B templates. Ordinary current-version database initialization uses the migration runner.

The dormant Pi adapter keeps its `parsar` provider slug because the separate Parsar product pins model selections to that identity. This is a product boundary exception for the name guard, like the skill-upload integration.

Build the MiniMax companion from this revision's pinned patched native sources.

## Branding

Public project branding uses OpenAgentCore. The canonical vector mark is `docs/assets/openagentcore-logo.svg`; Core Web, docs and landing-page assets use the same outline, with transparent margins cropped, theme-aware favicon colors and dark-surface inversion. The canonical SVG preserves the reference PNG canvas. The README hero uses the supplied `docs/assets/openagentcore-banner.jpeg`. The `example/parsar/` workbench retains its own name, logo and favicon. Historical provenance, external repository URLs and existing data identifiers retain their original spelling; do not rename those as display copy.

## OpenAgentCore name guard

`make check-names` scans tracked text for retired branding, settings and installed command names. Each exception in `scripts/name-allowlist.json` names a path glob, a regular expression and a reason.

- An exception covers only its matched text: an allowed repository import cannot hide a retired setting elsewhere on the line.
- Keep exceptions narrow and explain the preserved contract or historical input.

These identities stay unchanged:

- public `AgentCoreError`, upstream contract fields and the separate Parsar product;
- persisted credential encryption domains and native-session resume keys, so existing data can be decrypted and Sessions can resume.

Conversion inputs, retirement diagnostics and evidence records must still name the identifiers they reject. Landed migrations keep their original identifiers; current examples use the new names.
