# OpenAgentCore development

## Design principles

OpenAgentCore is protocol-first and modular. Core orchestrates operations that protocols define; Sandbox Providers, Runtimes, Harnesses and model providers are replaceable implementations of those protocols. [Architecture](docs/architecture.md) describes each component's responsibilities.

### Infrastructure standard

OpenAgentCore is infrastructure. Change a boundary only when the existing protocol cannot express the behavior, and make that the smallest change that leaves the design intact. Hold the code to the standard of a careful, widely used open-source service.

Keep it concise. Write elegant code that reuses existing code and standard SDKs as far as possible, and avoid redundant code. Expose nothing that does not need to be exposed: no port, route, command or setting without a caller.

### Protocols at every boundary

- Each boundary between components has exactly one protocol: one code file (interface, wire types and validators) and one document. A protocol change edits both and every implementation in one change, reviewed on its own.
- Protocols are deterministic. Every operation is declared and every outcome is typed. Implementations declare what they support, and callers validate each selected combination against those declarations. Support is never discovered through type assertions, name checks or implicit fallbacks. An unsupported operation or combination returns a typed error. Core never substitutes another implementation, and a capability means the same for every implementation.
- A component joins the system only by implementing a protocol, never through a private entry point, side channel or path selected by its name.
- Each rule has one authored definition. Generate cross-language projections from it or check them against shared fixtures.

| Boundary | Protocol code | Protocol doc |
| --- | --- | --- |
| Application–Core (`/v1`) | Types in `contracts/agents-api/v1/` and route annotations in `services/core/internal/api/`; `make openapi` generates `contracts/agents-api/openapi.yaml` | [Agents API guide](docs/api/public-agent-api.md) |
| Web and operators–Core (`/core/v1`) | Route annotations in `services/core/internal/api/`; `make openapi` generates `contracts/agents-api/core.openapi.yaml` | [Core administration API](contracts/agents-api/admin-api.md) |
| Nodes and daemons–Core (`/api/v1` HTTP routes; the node and daemon wire protocols are separate rows) | Route annotations in `services/core/internal/api/`; `make openapi` generates `contracts/agents-api/runtime.openapi.yaml` | [Machine connection API](contracts/agents-api/machine-api.md) |
| Core–Sandbox Provider | `services/core/internal/sandbox/sandbox_provider.go` | [Sandbox Provider guide](docs/sandbox-provider.md) |
| Core–sandbox node | `services/core/internal/sandbox/node/wire.go` | [Sandbox node protocol](contracts/agents-api/node-generation-protocol.md) |
| Provider–Runtime startup | `internal/runtimebootstrap/bootstrap.go` | [Runtime bootstrap](docs/runtime-bootstrap.md) |
| Core–Runtime wire | `internal/agentdaemon/proto/` | [Core–Runtime protocol](docs/runtime-protocol.md) |
| Runtime–Harness | `apps/daemon/internal/agent/harness.go` | [Harness onboarding](contracts/agents-api/harness-onboarding.md) |
| Harness–Model provider | `internal/modelprovider/config.go` | [Model execution](contracts/agents-api/model-execution.md) |

### Complexity stays in the adapter

- A new Sandbox Provider, Harness, model provider or vendor feature changes only its adapter. It adds no Core execution path, store table or column, migration, deployment or configuration field, API field or Web UI specific to one vendor or Harness. The [Sandbox Provider guide](docs/sandbox-provider.md) and [Harness onboarding](contracts/agents-api/harness-onboarding.md) describe how to add an adapter.
- When the protocol cannot express what an adapter needs, change the protocol. Never add an optional side interface for one implementation.
- Implementing the declared `CheckpointProvider` lifecycle in one vendor's Provider is an adapter change. A vendor-only pause interface, a Core path for that vendor, vendor receipts in the store or a vendor idle setting in the deployment is not.
- Fix shared lifecycle, admission, cancellation, reuse and performance problems in the common flow, never in a branch selected by Harness, Runtime or vendor name. Core preparation and execution never branch on operating system or Environment source; platform support requires native CI builds and automated tests.
- Each Harness runs its own model and tool loop through a maintained upstream SDK or native protocol, in the Environment's declared workspace directory. Its native history or configuration directory is never the workspace. Never build a second executor, a hand-written model/tool loop or a compatibility framework to fabricate parity. The public API and persistence never depend on one engine's native item types.

### Public API

- The target is the complete OpenAI Agents API (`openai/openai-python` `beta/agents`) as pinned in [`contracts/agents-api/upstream.json`](contracts/agents-api/upstream.json): paths, methods, headers, field presence, nullability, discriminators, defaults, status transitions, pagination, errors and streaming. Engine limitations are gaps to close, never grounds to narrow or redefine the contract. Operations or fields newer than the pinned baseline wait for a protocol upgrade.
- Record each native Harness difference, and any unspecified or unverified behavior, in the [coverage ledger](contracts/agents-api/index.md). Reject explicit enablement of an unsupported feature and never invent official semantics. When a material difference has no clear mapping, stop and ask before changing its semantics. Native differences never relax authentication, isolation, credential protection or data consistency.
- Applications, including the Parsar product, reach Core only through the public contract, with no privileged endpoint and no shared tables. Core never interprets their product payloads.

### One home for each setting and datum

Each setting and each piece of data is written in one place and read from that place: no second copy, no environment-variable or file fallback and no alias. A new setting joins its category and lives beside its peers.

The categories are [process settings](docs/configuration.md#process-settings-configjson), [derived files](docs/configuration.md#how-oac-apply-works), [secrets](docs/configuration.md#installation-directory), and Core's database for [runtime settings](docs/configuration.md#runtime-settings-web) and execution data. [Configuration](docs/configuration.md) owns the installation layout and the settings themselves.

### Pre-release: no compatibility layers

OpenAgentCore is pre-release. Replace superseded interfaces, execution paths and files outright. Keep no version fallback, compatibility shim or migration for superseded behavior unless an explicit upgrade contract requires it. Keep the pinned official public protocol, valid data and still-used, verified infrastructure. Do not rewrite working infrastructure only to rename it.

## Documentation

- One fact, one place. Link to the owning document instead of restating it. The owner map is [Documentation ownership](CONTRIBUTING.md#documentation-ownership).
- Leave out filler, hedging, process history (PR or design numbers, "retired", "former", "this candidate") and task chronology. Delete obsolete and duplicate documentation. Qualification evidence stays only while it qualifies current behavior.
- Do not hard-wrap prose. Write each paragraph, list item and blockquote on one line.
- User-facing documentation uses Web's exact page and action names.
- Application examples read the endpoint and key from `OPENAI_BASE_URL` and `OPENAI_API_KEY`.
- Maintain authored documentation under `docs/` and `contracts/agents-api/` in English and Simplified Chinese in the same change. English files keep their paths; Chinese translations mirror them under `docs/zh/` and `contracts/agents-api/zh/`. Preserve protocol identifiers, executable examples and English heading anchors. Each translation records its English `source` and SHA-256 `source_hash` in frontmatter; website checks reject missing or stale translations. Generated references remain owned by their generators and are excluded from manual translation. Keep code comments in English. The root README also has a Chinese version; user-facing product copy may be bilingual.
- Markdown in `docs/`, component guides and `contracts/` is the authored source. Generated files, such as the OpenAPI documents and the [Harness catalog reference](contracts/agents-api/harness-catalog.md), are never edited by hand: change the source and regenerate.
- Update the owning document in the same change as the rule, workflow or generated contract it describes.

## Working in this repository

- For each new task, create a new Git worktree. Name its directory after that change's commit subject, in kebab-case, beside the checkout. Run `git pull --ff-only` on the base branch, and create the feature branch in that worktree before development.
- After every push to a pull request, wait 60 seconds, then run `gh pr checks` and confirm CI passes. Fix any failure before reporting the work as done.
- [CONTRIBUTING.md](CONTRIBUTING.md): documentation ownership, repository boundary, workflow, independent review, required checks and naming.
- [Develop OpenAgentCore](docs/development.md): setup, the repository map, focused checks and [each extension boundary](docs/development.md#choose-an-extension-boundary).
- [API index](docs/api/index.md): each route's caller and credential.
