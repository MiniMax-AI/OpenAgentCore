# OpenAgentCore development

This file holds the design rules every change follows. [CONTRIBUTING.md](CONTRIBUTING.md)
holds workflow, review, checks and naming; [docs/development.md](docs/development.md)
holds setup and the repository map.

## Design principles

OpenAgentCore is protocol-first and modular. Core orchestrates operations that
protocols define. Sandbox Providers, Runtimes, Harnesses and model providers are
replaceable implementations of those protocols; user-owned machines, E2B, Docker
and microsandbox expose the same execution protocol.

Existing code that breaks a rule below is a gap, not a precedent. Do not copy or
extend it.

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
| Application–Core (`/v1`) | `contracts/agents-api/openapi.yaml` | [Agents API guide](docs/api/public-agent-api.md) |
| Web and operators–Core (`/core/v1`) | `contracts/agents-api/core.openapi.yaml` | [Web management API](docs/api/web-management.md) |
| Nodes and daemons–Core (`/api/v1`) | `contracts/agents-api/runtime.openapi.yaml` | [Machine connection API](docs/api/README.md#machine-connection-api) |
| Core–Sandbox Provider | `services/agents-api/internal/sandbox/sandbox_provider.go`, `services/agents-api/internal/sandbox/operations.go`, `services/agents-api/internal/providercontract/operations.go` | [Sandbox Provider guide](docs/sandbox-provider.md) |
| Core–sandbox node | `services/agents-api/internal/sandbox/node/wire.go`, `services/agents-api/internal/sandbox/node/generation_wire.go` | [Node generation protocol](contracts/agents-api/node-generation-protocol.md) |
| Provider–Runtime startup | `internal/runtimebootstrap/bootstrap.go` | [Runtime bootstrap](docs/runtime-bootstrap.md) |
| Core–Runtime wire | `internal/agentdaemon/proto/*.go` | [Core–Runtime protocol](docs/runtime-protocol.md) |
| Runtime–Harness | `apps/parsar-daemon/internal/agent/harness.go`, `internal/harnessconfig/harness.go` | [Harness onboarding](contracts/agents-api/harness-onboarding.md) |
| Harness–Model provider | `internal/modelprovider/config.go` | [Model execution](contracts/agents-api/model-execution.md) |

A boundary listed with more than one code file does not meet this rule yet; do not
add files to it.

### Complexity stays in the adapter

New complexity lives in the adapter that needs it and never spreads outward.

| Component | Adapter |
| --- | --- |
| Sandbox Provider | `services/agents-api/internal/sandbox/<kind>/` and its entry in `services/agents-api/internal/sandbox/providers/registry.go`; when present, its helper in `services/agents-api/tools/<kind>-provider/` and its operator material in `services/agents-api/deploy/<kind>/` |
| Harness | `apps/parsar-daemon/internal/agent/<harness>/`, its native configuration in `internal/harnessconfig/<harness>/`, its entry in `internal/harnessconfig/builtin/catalog.json` and its Runtime image in `services/agents-api/deploy/<harness>/` |
| Model provider | The Harness adapters that declare its model protocol; a new model protocol is a change to `internal/modelprovider` |

- A new Sandbox Provider, Harness, model provider or vendor feature changes only its
  adapter. It adds no Core execution path, store table or column, migration,
  deployment or configuration field, API field or Web UI specific to that vendor or
  Harness.
- When the protocol cannot express what an adapter needs, change the protocol as a
  change of its own: edit its code and document, update every implementation, and
  have it reviewed on its own. Never add an optional side interface for one
  implementation.
- Example: making one sandbox vendor pause idle compute is that vendor's Provider's
  job, done behind the existing lifecycle operations. A vendor-specific pause
  interface, a new Core resume path, pause receipts in the store and a vendor idle
  setting in the deployment go beyond an adapter change.

Each component owns one part of execution:

| Component | Owns |
| --- | --- |
| Core | Durable Session and Turn state, admission and scheduling |
| Sandbox Provider | Compute selection, creation, bootstrap, renewal and reclamation |
| Runtime | Preparation inside the Environment (files, tool configuration, packages, capabilities) and execution |
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
| Process settings | `<install>/config.json`; the installer defaults `<install>` to `~/.oac/core` | The operator, applied with `oac apply` |
| Derived process files | `<install>/generated/` | `oac apply` only |
| Secrets | `<install>/secrets/`, one file per secret | The installer and `oac` commands |
| Installation identity and local provider receipts | `<install>/state.json` and `<install>/state/` | The installer tools and Core's provider helpers |
| Runtime settings | Core's PostgreSQL | Web or `/core/v1` |
| Execution data | Core's PostgreSQL | Core, through its APIs |
| Node configuration and identity | `~/.oac/nodes/<installation-id>/` in the node account's home | The node installer and node |
| Self-hosted executor | `~/.oac/environments/<environment-id>/` | The native installer and daemon |
| Runtime daemon state | `~/.oac/daemon/<profile>/`; `OAC_RUNTIME_HOME` replaces `~/.oac` | The daemon |
| Build output | `~/.oac/build/`; `OAC_DEV_HOME` replaces `~/.oac` | `make` targets |
| Test artifacts | Under `~/.oac/` | Tests and acceptance runs |

### Pre-release: no compatibility layers

OpenAgentCore is pre-release. Replace superseded interfaces, execution paths, files
and documents outright. Keep no version fallback, compatibility shim, alias or
migration for retired behavior unless an explicit upgrade contract requires it.
Keep the pinned official public protocol, valid data and still-used, verified
infrastructure; do not rewrite working infrastructure only to rename it.

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
- [docs/development.md](docs/development.md): setup, the repository map and
  [the guide for each extension boundary](docs/development.md#choose-an-extension-boundary).
- [API index](docs/api/README.md): each route's caller and credential.
- Run `make sqlc-generate` after query changes and `make openapi` after handler
  changes. Review and commit the generated contracts with their source.
- Run `make check` before reporting completion.
