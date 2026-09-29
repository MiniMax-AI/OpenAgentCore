# Contributing to OpenAgentCore

Start with [Develop OpenAgentCore](docs/development.md) for checkout, toolchains,
the repository map and focused checks. This guide owns repository-wide rules only:
documentation ownership, the repository boundary, the decoupling principle,
workflow and review, required checks and naming.

Every other rule has one canonical owner, listed below. Change that owner, not a
copy, when a contract changes.

## Documentation ownership

| Subject | Canonical source |
| --- | --- |
| User concepts and authority | [Design principles](docs/design-principles.md) |
| Architecture overview and diagrams (a map that links to the owners below) | [Architecture](docs/architecture.md) |
| Developer setup, repository map and extension boundaries | [Develop OpenAgentCore](docs/development.md) |
| API callers, credentials and route inventory | [API index](docs/api/README.md) |
| Public wire types and qualified behavior | [Agents API contracts](contracts/agents-api/README.md), [pinned upstream](contracts/agents-api/upstream.json), and linked operation contracts |
| Core service implementation constraints | [Agents API implementation constraints](services/agents-api/IMPLEMENTATION.md) and [service README](services/agents-api/README.md) |
| Provider-to-Runtime startup input | [Runtime bootstrap](docs/runtime-bootstrap.md) and `internal/runtimebootstrap` |
| Runtime messages, Executor/Turn lifetimes, receipts and failure ownership | [Core–Runtime protocol](docs/runtime-protocol.md) and `internal/agentdaemon/proto` |
| Environment ownership and capability preparation (Skills, Plugins, MCP, `packages.system`) | [Environments](contracts/agents-api/environments.md) |
| Adding a Harness (steps) | [Harness onboarding](contracts/agents-api/harness-onboarding.md), `apps/parsar-daemon/internal/agent/harness.go` and `internal/harnessconfig/harness.go` |
| Harness qualification and acceptance | [Harness integration](contracts/agents-api/harnesses.md) |
| Harness selection and Agent defaults | [Harness selection](contracts/agents-api/harness-selection.md) |
| Adding a Sandbox Provider | [Sandbox Provider guide](docs/sandbox-provider.md) and `services/agents-api/internal/sandbox/sandbox_provider.go` |
| Provider selection, sandbox deployment and E2B setup | [Sandbox deployment](contracts/agents-api/sandbox-deployment.md) |
| Hosted sandbox nodes | [Hosted sandbox manager](services/agents-api/HOSTED-SANDBOX-MANAGER.md) |
| Claude private bridge and Runtime artifact | [Claude SDK adapter](packages/claude-sdk-adapter/README.md) |
| MiniMax Code and Claude Runtime adapter rules | [MiniMax Code Runtime](services/agents-api/deploy/mcode/README.md), [Claude Runtime](services/agents-api/deploy/claude/README.md) |
| CI, distribution builds, installer lifecycle and managed HTTPS, release publication | [Maintainer guide](docs/maintainers.md) |
| Operator installation and configuration | [Installation](docs/getting-started/install.md), [installation options](docs/getting-started/install-options.md), [configuration](docs/configuration.md), [operations](docs/getting-started/operations.md) |
| Core Web console server and sign-in | [Web README](apps/web/README.md) |
| Web components, interaction and visual rules | [Web design](apps/web/DESIGN.md) and [Web architecture](docs/web/architecture.md) |
| Documentation website generation | [Docs app](apps/docs/README.md) |

### Documentation rules

- English Markdown in `docs/`, component guides and `contracts/` is authored
  source. The docs app generates guide pages and API references from it. Never
  edit generated copies to establish a different rule.
- Keep documentation and code comments in English. The root README is provided
  in English and Chinese; user-facing product copy may be bilingual.
- Historical acceptance records keep their original revisions and limits. They
  are evidence, not current instructions or authority to restore a retired
  implementation. Keep task chronology and rollout reports out of contributor rules.
- Keep current integration guidance separate from historical qualification evidence.

## Repository boundary

This repository is the standalone execution substrate copied from Parsar at the
revision in `provenance/source.json`. It holds the API and its migrations, the
Runtime protocol and daemon, Harness adapters, shared execution packages, the
standalone Core Web console and build/test tools.

Product users, workspaces, model catalogs, business assets, the Parsar product
Web, product API and product migrations remain in Parsar. Do not import
`server/`, `apps/parsar/`, product CLI/plugin packages or their deployment stack.

Preserve copied Runtime and protocol behavior. Existing Go import paths stay
unchanged and do not require fetching the original repository. The source
snapshot and per-file hashes are an audit trail; future Core development need
not preserve them. Do not automatically sync or delete the original repository's Core.

### Product and execution service separation

Agents API is the primary infrastructure deliverable. Parsar is an ordinary client
and example application; its feature backlog must not dictate the execution
service's public protocol or internal model. Agents API must build, deploy and run
without the Parsar product service, frontend or database. An optional Compose
deployment may install both services with one PostgreSQL instance, but separate
databases, credentials and migrations. The product uses Core exclusively; it has no native daemon or HTTP Agent fallback.

#### Design and compatibility requirements

- The complete pinned `openai/openai-python` `beta/agents` protocol is the target,
  including its referenced resources and types. Match paths, methods, headers,
  field presence, nullability, discriminators, defaults, status transitions,
  pagination, errors and streaming behavior. Engine limitations are implementation
  gaps to solve, not grounds for narrowing or redefining the upstream contract.
- Preserve qualified native capability differences across harnesses. If a material
  difference from the official API has no clear mapping, pause that part and ask
  the user before changing its semantics. Explicit unsupported enablement rejects;
  ordinary requests retain native behavior with any official default discrepancy
  recorded in the coverage ledger. In particular, native programmatic tool calling
  is not currently qualified as the official default-on behavior. Do not build
  a separate executor or model loop to fabricate parity. This does not relax
  authentication, isolation, credential protection or data consistency.
- Pin upstream source and SDK versions in `contracts/agents-api/upstream.json`.
  Use official SDKs for clients and reuse upstream types or schemas where suitable.
  SDK deserialization alone is not server validation or proof of compatibility:
  test raw HTTP payloads and observable workflows as well. Synthetic data and mock
  model responses may support controlled tests; live execution acceptance must
  call a real model API through the service, daemon and harness. A real daemon
  with a synthetic model does not constitute live model validation. Keep provider
  credentials in private test configuration, outside source, logs and task records.
  Record unspecified or unverified behavior explicitly; never invent official
  semantics. When current documentation adds operations or fields absent from the
  fixed baseline, queue a protocol upgrade instead of silently implementing a new
  version. Owned-resource live probes can qualify status codes and wire details
  left unspecified by the SDK; retain request evidence and distinguish observations
  from guaranteed or fully covered behavior. Track partial
  coverage in `contracts/agents-api/README.md` until the complete target is verified.
  Reconcile current coverage summaries with merged routes and recorded acceptance;
  distinguish accepted profiles, partial implementation, missing operations and
  unverified semantics. Retain historical evidence with its original scope. Handler
  counts are not compatibility percentages, and an active provider probe is not
  deployment qualification.
- No legacy Agents API compatibility requirement takes precedence over this
  design. Replace an unsuitable implementation instead of growing compatibility
  branches. Preserve reusable, verified infrastructure rather than rewriting it
  merely for new names or directories. Replacements may retire obsolete private
  interfaces and history backfills in bounded PRs; this does not authorize deleting
  product data or changing unrelated product behavior.
- Every concrete harness interaction goes through the common Runtime contract
  and its adapter. Extend that contract minimally when a current operation cannot
  be expressed; never put native capability logic or transport conversion into
  Core handlers, storage or scheduling. Qualify public workflows through the same
  shared chain; direct native probes establish feasibility only.
- Keep engine-specific types, process management and protocol translation inside
  execution adapters. The public API and persistence/application core must not
  interpret Parsar product payloads or depend on one engine's native item types.
  Prefer maintained upstream SDKs and native execution protocols over a second
  hand-written model/tool loop or a general-purpose compatibility framework.
- Verify an independent official-client workflow before a Parsar integration.
  Parsar uses the same public contract as any other client, with no privileged
  endpoint or direct execution-table access. An OpenAI endpoint is a possible
  client target only where the requested capabilities and credentials support it.

- Parsar owns users, workspaces, business authorization, Agent/Team definitions,
  capabilities, product conversations, IM/sharing, approval decisions and billing.
- Agents API owns protocol saved Agents, execution sessions/turns, effective
  configuration snapshots, dispatch/cancel, environments, vaults, raw usage,
  pending interactions, protocol subagents and durable events. Protocol saved
  Agents/vaults are execution resources, not Parsar marketplace or business roles.
  Neither service reads the other's tables. Parsar uses a versioned client contract.
- A product conversation may map to several execution sessions. An execution
  session is distinct from a live daemon socket, process or sandbox. Native engine
  session identifiers belong to the execution service.
- Establish single-Agent execution, approval, cancellation, idempotent submission,
  persisted recovery queries before Team orchestration. The upstream SSE stream
  is live-only; recover through Session/Turn/Items reads. Any additional product
  cursor replay must be documented as an extension, not upstream semantics.
  Team definitions, management and orchestration belong to Parsar. Agents API
  establishes single-Agent execution first; business Team loops are deferred.
  This does not exclude upstream `multi_agent` configuration or subagent resources
  from protocol coverage. Future business Team orchestration directly depends on
  `openai/openai-agents-python` in Parsar.
- Daemon Skill/SP authoring remains a product operation: forward through a scoped
  product callback with the original requester and workspace checks. A runtime
  credential alone must not grant business write permissions.

### Optional application example

`example/parsar/` is an optional, independently started Agent workbench. Its
[README](example/parsar/README.md) owns its product behavior. The boundary rules are:

- It calls only public `/v1` APIs. Its Project key stays server-side; it never
  holds a Core key or issues machine credentials. Core-only credential issuance
  stays in the operator console.
- It may reuse product UI and keep a small product-owned SQLite database (Node's
  built-in module, Node 22.13+), outside the checkout and isolated by Core origin
  and Project key fingerprint. Provider keys never reach the browser.
- Core owns Skills and all execution and history state. The example stores only
  Session references and pending creation requests with stable idempotency keys.
- Product resources use `/app/` and never become Core API or database conventions.
- It is excluded from Core distributions and cannot become a service dependency.

## Decoupling principle

Core orchestrates protocol-defined operations. Sandbox Providers, Runtime
implementations, Harnesses and model providers are replaceable components.
User-owned machines, E2B, Docker and other Environments expose the same
execution protocol.

Two lifetimes stay separate:

| Component | Owns |
| --- | --- |
| Resource management (Sandbox Provider) | Selecting machines and capacity; creating, bootstrapping, renewing and reclaiming Environments |
| Runtime | Initializing files, tool configuration, packages and capabilities; executing work and recovering inside an Environment |

Closing a Session Executor does not release its allocation, destroy its
Environment or delete its workspace; see
[Executor and Turn lifetimes](docs/runtime-protocol.md#executor-and-turn-lifetimes).
Capability preparation follows [Environments](contracts/agents-api/environments.md#runtime-capability-preparation).
The Runtime is not a sandbox; see
[Runtime and outer isolation](docs/design-principles.md#runtime-and-outer-isolation).

- Keep component boundaries explicit through shared interfaces and versioned
  protocols. Register implementations behind those interfaces. Adding an
  implementation must not require a new orchestration path selected by its name.
- Core owns durable Session/Turn state and scheduling. Runtime owns local
  execution resources. Harness adapters translate the common execution contract
  into native operations; model and sandbox provider details stay behind their
  interfaces.
- Fix shared lifecycle, admission, cancellation, reuse and performance problems
  in the common protocol or flow, not with Harness-, Runtime- or vendor-specific
  branches in Core. Adapters may differ natively but keep shared semantics.
- Express compatibility through declared capabilities and validate selected
  combinations explicitly. Replaceability does not mean every model, Harness and
  Environment combination is supported. Never silently substitute another
  implementation or give a capability different meanings per vendor.
- Core preparation and execution never branch on operating system or
  Environment source. Platform support requires native CI builds and automated
  tests; cross-compilation alone is insufficient.
- Evolve shared contracts and their implementations together, document
  ownership and validate the same contract across implementations. Each rule has
  one authored definition; cross-language projections are generated from it or
  checked against common fixtures. Bootstrap credentials use the Runtime-owned
  launch input, never a Provider-authored private auth file. This rule
  does not claim every implementation already meets every target, change the
  pinned public API, or authorize unrelated refactors.

### Extension contracts

| Boundary | Canonical guide | Code entry point |
| --- | --- | --- |
| Provider–Runtime startup | [Runtime bootstrap](docs/runtime-bootstrap.md) | `internal/runtimebootstrap` |
| Core–Runtime wire | [Core–Runtime protocol](docs/runtime-protocol.md) | `internal/agentdaemon/proto` |
| Harness | [Harness onboarding](contracts/agents-api/harness-onboarding.md) | `apps/parsar-daemon/internal/agent/harness.go`, `internal/harnessconfig/harness.go` |
| Sandbox Provider | [Sandbox Provider guide](docs/sandbox-provider.md) | `services/agents-api/internal/sandbox/sandbox_provider.go` |

Shared wire types and validators live only in `internal/agentdaemon/proto`.
Change both peers together with an exact wire-version check; do not add a
parallel schema or a historical wire fallback.

### Pre-release policy

OpenAgentCore is pre-release. Replace superseded internal interfaces and
execution paths cleanly; do not retain version fallbacks, compatibility shims,
aliases or migrations without an explicit upgrade contract. Preserve the pinned
official public protocol, valid data and still-used infrastructure.

## Workflow and review

### Before you start

1. Record the requirements, acceptance criteria and scope.
2. Work in an isolated Git worktree on a feature branch and submit a PR. Do not
   edit or commit implementation directly on `main`.
3. Make only the changes that scope needs; keep unrelated refactors separate.

When documents conflict, apply the latest explicit user decision and update the
affected current guidance. Historical evidence does not override it.

If requirements are unresolved, object ownership is unclear, or a design would
need parallel compatibility paths, raise the issue with a concrete
recommendation and tradeoffs before implementing it. Continue independent work
meanwhile. Do not silently preserve obsolete private designs.

Record unrelated findings without automatically starting them. Do not claim a
broader compatibility target is complete from one merged batch.

### Implementation conventions

- Search for existing formatters, parsers, validation and error mappers before
  adding one. Keep one error mapper per API surface.
- Share frontend formatting and labels in `apps/web/src/lib/`; reuse components
  and tokens.
- Split growing files at an existing ownership boundary instead of adding
  unrelated responsibilities.
- Use `internal/obs/log` for logs. Keep credentials out of source and logs.
- Require absolute user-supplied working directories.
- Keep test artifacts under `~/.oac/` and build output under
  `${OAC_DEV_HOME:-$HOME/.oac}`. Runtime state uses `${OAC_RUNTIME_HOME:-$HOME/.oac}`.
- New or changed routes identify their caller and credential in the
  [API index](docs/api/README.md) and link their detailed contract.
- Update the owning guide when architecture, ownership or generated contracts change.

### Review

1. After implementation and validation, have a fresh independent subagent review
   the complete diff.
2. Give it only the requirements, acceptance criteria, boundaries, repository
   path and comparison baseline. Do not give an implementation summary,
   self-assessment or earlier findings. Explain these criteria to the user.
3. Fix substantiated in-scope findings, validate, then use another fresh reviewer.
4. If the cycle repeats, reassess design and scope before adding changes. Report
   an unresolved blocker instead of broadening the task.

Do not use `codex exec` as a substitute reviewer.

## Required checks

Toolchain setup and focused commands are in
[Develop OpenAgentCore](docs/development.md#set-up-a-checkout). CI coverage,
caches and release publication are owned by the
[maintainer guide](docs/maintainers.md#publish-a-version).

### Full gate

Run `make check` before completion. It includes:

- all daemon and shared Go tests, including native daemon filesystem tests;
- Core contract, client and service tests and standalone API builds;
- a real dedicated PostgreSQL test database and byte-for-byte sqlc checks;
- Core Web and TypeScript client checks, including fixture-only Playwright acceptance;
- Claude SDK tests and packaging, and MiniMax companion checks;
- the Core distribution and installer gates;
- `make check-example` for the optional application example (TypeScript,
  proxy/persistence tests, build and fixture browser acceptance). Its synthetic
  responses are not live model qualification.

It excludes Parsar product Web and server gates.

### Test database

| Variable | Value |
| --- | --- |
| `OAC_TEST_DATABASE_URL` | A dedicated test database. The full gate fails when it is missing. |
| `OAC_TEST_OFFICIAL_SDK_PYTHON` | The pinned official SDK interpreter |

The role needs `CREATE DATABASE`: managed-provider tests create and drop
isolated `oac_*_tests` databases because provider identity is deployment-wide.
`PARSAR_AGENTS_API_TEST_DATABASE_URL` is retired; `make check-database` reports
its replacement when only the old name is set. Tests must not bypass the
production provider-switch guard.

### Contract and schema rules

- `make sqlc-generate` owns only `services/agents-api/internal/db/sqlc`
  (sqlc v1.29.0). Do not rewrite landed migrations.
- The public protocol schema is `contracts/agents-api/openapi.yaml`. Preserve
  its pinned types, coverage ledgers and official SDK/raw HTTP tests when
  changing API behavior. Core changes keep the independent build and
  official-client workflow.
- `make check-runtime-contract` is the focused Core–Runtime contract entry
  point; see [Contract verification](docs/runtime-protocol.md#contract-verification).
  It also runs through `check-go` and `check-agents-api`.

### Live acceptance

Native adapter changes require their build/check targets and live provider
acceptance. Real execution checks need real models; omitted prerequisites or
mocked responses do not count as live acceptance. Historical remote native
probes are not current validation entry points.

## OpenAgentCore Runtime names

| Surface | Current name |
| --- | --- |
| Runtime binary | `oac-daemon` |
| Filesystem and initialization helpers | `oac-*` |
| Runtime settings | `OAC_RUNTIME_*` |
| Runtime state | `~/.oac/daemon` |
| Reserved Environment `env` prefix | `OAC_` |
| Provider ownership labels | `io.oac.*` |
| E2B metadata | `oac_*` |

Provider bootstrap, Runtime images and Harness adapters must agree on these
names. Daemon startup rejects renamed settings before any subcommand and
reports replacements without values; the separate Parsar product integration
settings remain unchanged. No old label is accepted as a fallback.

Historical Runtime and project-version upgrades are not supported. Preserve
older installations, Runtime files, provider resources and Session history;
install the current release separately. Use this release's template builder for
new E2B templates. Landed migrations and historical evidence stay as repository
history; ordinary current-version database initialization uses the migration runner.

The dormant Pi adapter keeps its `parsar` provider slug because the separate
Parsar product pins model selections to that identity. This is a product
boundary exception for the name guard, like the skill-upload integration.

Build the MiniMax companion from this revision's pinned patched native sources.

## Branding

Public project branding uses OpenAgentCore. The canonical vector mark is
`docs/assets/openagentcore-logo.svg`; Core Web, docs and landing-page assets use
the same outline, with transparent margins cropped, theme-aware favicon colors
and dark-surface inversion. The canonical SVG preserves the reference PNG canvas.
The README hero uses the supplied `docs/assets/openagentcore-banner.jpeg`.
The `example/parsar/` workbench retains its own name, logo and favicon.
Historical provenance, external repository URLs, import paths and existing data
identifiers retain their original spelling; do not rename those as display copy.

## OpenAgentCore name guard

`make check-names` scans tracked text for retired branding, settings and
installed command names. Each exception in `scripts/name-allowlist.json` names a
path glob, a regular expression and a reason.

- An exception covers only its matched text: an allowed repository import cannot
  hide a retired setting elsewhere on the line.
- Keep exceptions narrow and explain the preserved contract or historical input.

These identities stay unchanged:

- Go module and source directory paths, npm and Cargo package identities;
- public `AgentCoreError`, upstream contract fields and the separate Parsar product;
- persisted credential encryption domains and native-session resume keys, so
  existing data can be decrypted and Sessions can resume.

Conversion inputs, retirement diagnostics and historical evidence must still
name the identifiers they reject. Historical migration files keep their original
identifiers; current examples use the new names.
