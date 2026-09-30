# OpenAgentCore development

This file holds the design rules every change follows.
[Working in this repository](#working-in-this-repository) links to everything else.

## Design principles

OpenAgentCore is protocol-first and modular. Core orchestrates operations that
protocols define. Sandbox Providers, Runtimes, Harnesses and model providers are
replaceable implementations of those protocols; user-owned machines, E2B, Docker
and microsandbox expose the same execution protocol.

### Protocols at every boundary

- Each boundary between components has exactly one protocol: one code file
  (interface, wire types and validators) and one document. Change the code and the
  document together, and update every implementation in the same change.
- Protocols are deterministic. Declare each operation explicitly and type each
  outcome. Declare support; never discover it through optional-interface type
  assertions, name checks or implicit fallbacks. An unsupported operation returns
  an explicit typed error.
- A component joins the system only by implementing a protocol. It gets no private
  entry point, side channel or path selected by its name.

| Boundary | Protocol code | Protocol doc |
| --- | --- | --- |
| Application–Core (`/v1`) | `contracts/agents-api/openapi.yaml`, generated from `contracts/agents-api/v1/*.go` | [Agents API guide](docs/api/public-agent-api.md) |
| Web and operators–Core (`/core/v1`) | `contracts/agents-api/core.openapi.yaml`, generated from `contracts/agents-api/v1/*.go` | [Web management API](docs/api/web-management.md) |
| Nodes and daemons–Core (`/api/v1`) | `contracts/agents-api/runtime.openapi.yaml`, generated from `contracts/agents-api/v1/*.go` | [Machine connection API](docs/api/README.md#machine-connection-api) |
| Core–Sandbox Provider | `sandbox_provider.go`, `suspension.go`, `selection.go` and `operations.go` in `services/agents-api/internal/sandbox/`; `services/agents-api/internal/providercontract/operations.go`; `services/agents-api/internal/runtimeobs/source.go` | [Sandbox Provider guide](docs/sandbox-provider.md) |
| Core–sandbox node | `wire.go`, `generation_wire.go`, `generation_json.go` and `operations.go` in `services/agents-api/internal/sandbox/node/` | [Node generation protocol](contracts/agents-api/node-generation-protocol.md) |
| Provider–Runtime startup | `internal/runtimebootstrap/bootstrap.go` | [Runtime bootstrap](docs/runtime-bootstrap.md) |
| Core–Runtime wire | `internal/agentdaemon/proto/*.go` | [Core–Runtime protocol](docs/runtime-protocol.md) |
| Runtime–Harness | `apps/parsar-daemon/internal/agent/harness.go`, with result types and errors in `apps/parsar-daemon/internal/agent/*.go`; `internal/harnessconfig/harness.go` | [Harness onboarding](contracts/agents-api/harness-onboarding.md) |
| Harness–Model provider | `internal/modelprovider/config.go`, `internal/harnessconfig/provider.go` | [Model execution](contracts/agents-api/model-execution.md) |

`make openapi` generates the three OpenAPI files from the Go types and the handler
annotations in `services/agents-api/`; never edit them by hand. Rows that list more
than one file, a glob or a directory do not meet the single-file rule yet; do not
add files to them.

### Complexity stays in the adapter

New complexity lives in the adapter that needs it and never spreads outward. A new
implementation adds or changes only these files. Directory names differ per
Harness: Claude uses `claude_sdk`, `claudesdk` and `claude`.

- **Sandbox Provider:** its package `services/agents-api/internal/sandbox/<kind>/`,
  its construction in `services/agents-api/internal/sandbox/providers/<kind>.go`
  and its entry in `services/agents-api/internal/sandbox/providers/registry.go`;
  when present, its helper in `services/agents-api/tools/<kind>-provider/` and its
  operator material in `services/agents-api/deploy/<kind>/`.
- **Harness:** its adapter in `apps/parsar-daemon/internal/agent/<harness>/`, its
  native configuration in `internal/harnessconfig/<harness>/`, its entry in
  `internal/harnessconfig/builtin/catalog.json`, its discovery and registration in
  `apps/parsar-daemon/internal/cli/` (`agent_discovery.go`, `agent_registration.go`
  and `native_harness.go`, plus per-Harness files such as `claude_sdk.go`), its
  Runtime image in `services/agents-api/deploy/<harness>/` and, when present, its
  native package (`packages/claude-sdk-adapter/`, `packages/mcode-harness/`).
- **Model provider:** nothing while it speaks a model protocol that
  `internal/modelprovider` defines and a Harness declares; a new model protocol is a
  protocol change.

Rules for every adapter:

- A new Sandbox Provider, Harness, model provider or vendor feature adds no Core
  execution path, store table or column, migration, deployment or configuration
  field, API field or Web UI specific to that vendor or Harness.
- When the protocol cannot express what an adapter needs, change the protocol as a
  change of its own: edit its code and document, update every implementation, and
  have it reviewed on its own. Never add an optional side interface for one
  implementation.
- Example: a vendor that can pause idle compute implements the declared `Suspend`
  and `Resume` operations of `CheckpointProvider` in its own Provider; that is an
  adapter change. A vendor-only pause interface, a Core path for that vendor,
  vendor receipts in the store or a vendor idle setting in the deployment is not.
- Each Harness runs its own native model and tool loop through a maintained
  upstream SDK or native protocol. Never build a second executor, a hand-written
  model/tool loop or a general-purpose compatibility framework to fabricate parity.
  The public API and persistence never depend on one engine's native item types.

Each component owns one part of execution:

| Component | Owns |
| --- | --- |
| Core | Durable Session, Turn, Environment and allocation state; admission, placement and scheduling |
| Sandbox Provider | Compute creation, bootstrap, renewal and reclamation |
| Runtime | Preparation, execution and recovery inside the Environment |
| Harness adapter | Translation of the common execution contract into native operations |

- Executor and compute lifetimes are separate; see
  [Executor and Turn lifetimes](docs/runtime-protocol.md#executor-and-turn-lifetimes).
  Capability preparation follows [Environments](contracts/agents-api/environments.md#runtime-capability-preparation),
  and isolation belongs to the outer Environment; see
  [Runtime and outer isolation](docs/design-principles.md#runtime-and-outer-isolation).
- Fix shared lifecycle, admission, cancellation, reuse and performance problems in
  the common flow, never in branches selected by a Harness, Runtime or vendor name.
- Express compatibility through declared capabilities and validate each selected
  combination explicitly. Reject an unsupported combination with an explicit error.
  Never substitute another implementation or give a capability a different meaning
  per vendor.
- Core preparation and execution never branch on operating system or Environment
  source. Platform support requires native CI builds and automated tests;
  cross-compilation alone is insufficient.
- Each rule has one authored definition. Generate cross-language projections from
  it or check them against shared fixtures.

### One home for each setting and datum

- Write each setting and each piece of data in one place and read it from that
  place. Keep no second copy, no environment-variable or file fallback and no alias.
- Keep configuration files together. Where that is impossible, group them by
  category. Never scatter them.
- A new setting joins one of the categories below and lives beside its peers.
  [Configuration](docs/configuration.md) documents the settings themselves.

| Category | Home | Written by |
| --- | --- | --- |
| Process settings | `<install>/config.json`; the installer defaults `<install>` to `~/.oac/core` | The operator, Web domain setup or `oac domain`; `oac apply` applies them |
| Derived process files | `<install>/generated/` | `oac apply` only |
| Secrets and installation identity | `<install>/secrets/`, one file per secret; `<install>/state.json` | The installer and `oac` commands |
| Provider helper state | `<install>/state/` | Core's provider helpers |
| Managed HTTPS | `<install>/ingress/`: domain status, certificates and control sockets | `oac` and the managed ingress |
| Installed release files | `<install>/oac`, `<install>/native/`, `<install>/node-payload/`; verified bundles in `~/.oac/releases/` | The installer |
| Runtime settings and execution data | Core's PostgreSQL | Web or `/core/v1` for settings; Core, through its APIs, for execution data |
| Node identity, configuration and storage | `~/.oac/nodes/<installation-id>/` and microsandbox storage in `~/.oac/m/<hash>/`, in the node account's home | The node installer and node |
| Daemon settings | `OAC_RUNTIME_*` in the daemon's process environment | The Runtime image, the Sandbox Provider at launch, or `oac-daemon start` from `daemon/installation.json` on a self-hosted machine |
| Runtime state | `~/.oac/daemon/` (Environment binding, installation, executor credential, sessions, scratch and capability installations; connection state per profile in `daemon/<profile>/`) and adapter state in `~/.oac/runtime/<kind>/` | The daemon and its adapters |
| Self-hosted executors | `~/.oac/environments/<environment-id>/`, each its own Runtime home | The native installer and daemon |
| Build output and caches | `~/.oac/build/`; CI caches in `~/.oac/cache/` | `make` targets; CI |
| Test artifacts | Under `~/.oac/` | Tests and acceptance runs |

`OAC_RUNTIME_HOME` replaces `~/.oac` for Runtime state and self-hosted executors;
`OAC_DEV_HOME` replaces it for build output.

### Pre-release: no compatibility layers

OpenAgentCore is pre-release. Replace superseded interfaces, execution paths, files
and documents outright. Keep no version fallback, compatibility shim, alias or
migration for retired behavior unless an explicit upgrade contract requires it.
Keep the pinned official public protocol, valid data and still-used, verified
infrastructure; do not rewrite working infrastructure only to rename it.

### Known gaps

Existing code that breaks these rules is a gap, not a precedent. Do not copy these
patterns; a change that touches one of them moves it toward the rule.

- Multi-file protocols: every row of the boundary table that lists more than one
  file, a glob or a directory.
- Harness support by type assertion: `apps/parsar-daemon/internal/dispatch/workspace_read.go`
  selects workspace file reads and directory listings by asserting `WorkspaceReader`
  and `WorkspaceDirectoryLister` instead of reading a declaration; `dispatch/steering.go`
  reports a declared but missing `Steerer` as unsupported.
- Per-Harness profiles inside Core: `services/agents-api/internal/engine/<harness>.go`
  (`claude.go`, `codex.go`, `mcode.go`).
- Vendor configuration outside the adapter: `sandbox.Selection` has an `E2B` field
  (`services/agents-api/internal/sandbox/selection.go`) with matching store
  columns, API fields and operator-client types, as
  [Register the provider kind](docs/sandbox-provider.md#register-the-provider-kind)
  directs for new Provider fields.

## Documentation

- One fact, one place. Link to the owning document instead of restating it. The
  owner map is [Documentation ownership](CONTRIBUTING.md#documentation-ownership).
- Keep a subject together in one document or section.
- Give each document one audience and one job. Order it for reading: what the
  subject is, how to do the task, then reference detail.
- Write plainly and helpfully. State what the system does and what the reader does.
  Leave out filler, hedging, defensive negations, process history (PR or design
  numbers, "retired", "former", "this candidate") and task chronology.
- Delete obsolete, historical and duplicate documentation outright. Qualification
  evidence stays only while it qualifies current behavior.
- Do not hard-wrap prose. Write each paragraph, list item and blockquote on one line; editors wrap it for display.
- Write documentation and code comments in English. The root README also has a
  Chinese version; user-facing product copy may be bilingual.
- Markdown in `docs/`, component guides and `contracts/` is the authored source.
  Generated copies, such as the `apps/docs` content and generated references, are
  never edited by hand: change the source and regenerate.
- Update the owning document in the same branch as the rule, boundary, workflow or
  generated contract it describes.

## Working in this repository

- [CONTRIBUTING.md](CONTRIBUTING.md): workflow, independent review, required checks
  and naming.
- [Develop OpenAgentCore](docs/development.md): setup, the repository map, focused
  checks and [the guide for each extension boundary](docs/development.md#choose-an-extension-boundary).
- [API index](docs/api/README.md): each route's caller and credential.
