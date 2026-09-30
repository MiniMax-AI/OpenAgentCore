# OpenAgentCore development

This file holds the design rules every change follows. [Working in this repository](#working-in-this-repository) links to everything else.

## Design principles

OpenAgentCore is protocol-first and modular. Core orchestrates operations that protocols define; Sandbox Providers, Runtimes, Harnesses and model providers are replaceable implementations of those protocols. [Architecture](docs/architecture.md) describes each component's responsibilities.

### Protocols at every boundary

- Each boundary between components has exactly one protocol: one code file (interface, wire types and validators) and one document. A protocol change edits both and every implementation in one change, reviewed on its own.
- Protocols are deterministic. Every operation is declared and every outcome is typed. Implementations declare what they support, and callers validate each selected combination against those declarations; they never discover support through type assertions, name checks or implicit fallbacks. An unsupported operation or combination returns a typed error. Core never substitutes another implementation, and a capability means the same for every implementation.
- A component joins the system only by implementing a protocol, never through a private entry point, side channel or path selected by its name.
- Each rule has one authored definition. Generate cross-language projections from it or check them against shared fixtures.

| Boundary | Protocol code | Protocol doc |
| --- | --- | --- |
| Application–Core (`/v1`) | Types in `contracts/agents-api/v1/` and route annotations in `services/agents-api/internal/api/`; `make openapi` generates `contracts/agents-api/openapi.yaml` | [Agents API guide](docs/api/public-agent-api.md) |
| Web and operators–Core (`/core/v1`) | Route annotations in `services/agents-api/internal/api/`; `make openapi` generates `contracts/agents-api/core.openapi.yaml` | [Core API](docs/api/README.md#core-api) |
| Nodes and daemons–Core (`/api/v1` HTTP routes; the node and daemon wire protocols are separate rows) | Route annotations in `services/agents-api/internal/api/`; `make openapi` generates `contracts/agents-api/runtime.openapi.yaml` | [Machine connection API](docs/api/README.md#machine-connection-api) |
| Core–Sandbox Provider | `services/agents-api/internal/sandbox/sandbox_provider.go` | [Sandbox Provider guide](docs/sandbox-provider.md) |
| Core–sandbox node | `services/agents-api/internal/sandbox/node/wire.go` | [Node generation protocol](contracts/agents-api/node-generation-protocol.md) |
| Provider–Runtime startup | `internal/runtimebootstrap/bootstrap.go` | [Runtime bootstrap](docs/runtime-bootstrap.md) |
| Core–Runtime wire | `internal/agentdaemon/proto/` | [Core–Runtime protocol](docs/runtime-protocol.md) |
| Runtime–Harness | `apps/parsar-daemon/internal/agent/harness.go` | [Harness onboarding](contracts/agents-api/harness-onboarding.md) |
| Harness–Model provider | `internal/modelprovider/config.go` | [Model execution](contracts/agents-api/model-execution.md) |

Most boundaries still span several files; the listed file or directory is the entry point. Do not add files to a boundary; this is a [known gap](#known-gaps).

### Complexity stays in the adapter

- New complexity lives in the adapter that needs it and never spreads outward. A new Sandbox Provider, Harness, model provider or vendor feature changes only its adapter. It adds no Core execution path, store table or column, migration, deployment or configuration field, API field or Web UI specific to one vendor or Harness.
- The [Sandbox Provider guide](docs/sandbox-provider.md) and [Harness onboarding](contracts/agents-api/harness-onboarding.md) list the files an adapter touches today. Any step there that edits shared code is a known gap, not a pattern.
- When the protocol cannot express what an adapter needs, change the protocol. Never add an optional side interface for one implementation.
- Example: implementing the complete declared `CheckpointProvider` lifecycle in one vendor's Provider is an adapter change. A vendor-only pause interface, a Core path for that vendor, vendor receipts in the store or a vendor idle setting in the deployment is not.
- Fix shared lifecycle, admission, cancellation, reuse and performance problems in the common flow, never in branches selected by a Harness, Runtime or vendor name. Core preparation and execution never branch on operating system or Environment source; platform support requires native CI builds and automated tests.
- Each Harness runs its own model and tool loop through a maintained upstream SDK or native protocol, in the Environment's declared workspace directory; its native history or configuration directory is never the workspace. Never build a second executor, a hand-written model/tool loop or a general-purpose compatibility framework to fabricate parity. The public API and persistence never depend on one engine's native item types.

### Public API

- The target is the complete OpenAI Agents API (`openai/openai-python` `beta/agents`) as pinned in [`contracts/agents-api/upstream.json`](contracts/agents-api/upstream.json): paths, methods, headers, field presence, nullability, discriminators, defaults, status transitions, pagination, errors and streaming. Engine limitations are gaps to close, never grounds to narrow or redefine the contract. Operations or fields newer than the pinned baseline wait for a protocol upgrade.
- Native differences between Harnesses stay explicit. Record each difference and any unspecified or unverified behavior in the [coverage ledger](contracts/agents-api/README.md), reject explicit enablement of an unsupported feature and never invent official semantics. When a material difference has no clear mapping, stop and ask before changing its semantics. Native differences never relax authentication, isolation, credential protection or data consistency.
- Applications, including the Parsar product, reach Core only through the public contract, with no privileged endpoint and no shared tables, and Core never interprets their product payloads.

### One home for each setting and datum

- Each setting and each piece of data is written in one place and read from that place, with no second copy, no environment-variable or file fallback and no alias.
- Configuration files are grouped by category, never scattered. A new setting joins its category and lives beside its peers.

The categories are [process settings](docs/configuration.md#process-settings-configjson), [derived files](docs/configuration.md#how-oac-apply-works), [secrets](docs/configuration.md#secrets-and-identity), and Core's database for [runtime settings](docs/configuration.md#runtime-settings-web) and execution data. [Configuration](docs/configuration.md) owns the installation layout and the settings themselves.

### Pre-release: no compatibility layers

OpenAgentCore is pre-release. Replace superseded interfaces, execution paths and files outright. Keep no version fallback, compatibility shim or migration for superseded behavior unless an explicit upgrade contract requires it. Keep the pinned official public protocol, valid data and still-used, verified infrastructure; do not rewrite working infrastructure only to rename it.

### Known gaps

Existing code still breaks these rules in places. The bullets below are examples, not a complete list. Do not copy these patterns. Until a gap is closed, follow the extension guide; a change that touches a gap moves it toward the rule.

- Protocol definitions spread over several files, such as the Sandbox Provider contract across `services/agents-api/internal/sandbox/` and `services/agents-api/internal/providercontract/`.
- Support discovered by type assertion, such as daemon workspace reads in `apps/parsar-daemon/internal/dispatch/workspace_read.go` and Core's observation source selection in `services/agents-api/cmd/server/main.go`.
- Harness-specific code in shared places, such as Core engine profiles in `services/agents-api/internal/engine/<harness>.go`, daemon discovery and registration, the installer's Harness list and default in `deploy/install/config.schema.json`, and Core's own default Harness when `OAC_DEFAULT_HARNESS` is unset.
- Vendor-specific configuration, routes and UI outside the adapter, such as the E2B selection and store fields (`services/agents-api/internal/sandbox/selection.go`), the `/core/v1/sandbox/e2b/*` routes, E2B credential hooks in `providers.Adapter` and the E2B Web views.
- Host-local state spread over several `~/.oac/` directories, such as the Runtime's `~/.oac/daemon/`, `~/.oac/runtime/<kind>/` and `~/.oac/environments/<environment-id>/`.
- Persistence and vendor types in the Core and machine OpenAPI documents, such as the `store.*` and `e2b.*` definitions in `contracts/agents-api/core.openapi.yaml`.

## Documentation

- One fact, one place. Link to the owning document instead of restating it. The owner map is [Documentation ownership](CONTRIBUTING.md#documentation-ownership).
- Keep a subject together in one document or section.
- Give each document one audience and one job. Order it for reading: what the subject is, how to do the task, then reference detail.
- Write plainly and helpfully. State what the system does and what the reader does. Leave out filler, hedging, defensive negations, process history (PR or design numbers, "retired", "former", "this candidate") and task chronology.
- Delete obsolete, historical and duplicate documentation outright. Qualification evidence stays only while it qualifies current behavior.
- Do not hard-wrap prose. Write each paragraph, list item and blockquote on one line; editors wrap it for display.
- User-facing documentation uses Web's exact page and action names.
- Application examples read the endpoint and key from `OPENAI_BASE_URL` and `OPENAI_API_KEY`.
- Write documentation and code comments in English. The root README also has a Chinese version; user-facing product copy may be bilingual.
- Markdown in `docs/`, component guides and `contracts/` is the authored source. Generated files, such as the OpenAPI documents and the [Harness catalog reference](contracts/agents-api/harness-catalog.md), are never edited by hand: change the source and regenerate.
- Update the owning document in the same branch as the rule, workflow or generated contract it describes.

## Working in this repository

- [CONTRIBUTING.md](CONTRIBUTING.md): read before changing code. Documentation ownership, repository boundary, workflow, independent review, required checks and naming.
- [Develop OpenAgentCore](docs/development.md): setup, the repository map, focused checks and [the guide for each extension boundary](docs/development.md#choose-an-extension-boundary).
- [API index](docs/api/README.md): each route's caller and credential.
