# Architecture

OpenAgentCore separates control, runtime and execution. Core owns durable state
and the API; the Runtime daemon runs work inside an Environment; the native harness
keeps its own model and tool loop. Each connection between them is a defined
protocol, so any part can be replaced without changing Core orchestration.

This page is a map. Each section names a component, its boundary and the document
that owns its rules.

![OpenAgentCore architecture](assets/architecture-overview.png)

The diagram has four tiers:

1. **Callers.** Applications, including your product and the official OpenAI SDK,
   call the Agents API. Operators use Core Web, which calls the Core API.
2. **Core.** The control plane: public and administrator APIs, resources,
   orchestration, PostgreSQL, the Runtime gateway and the Sandbox Provider
   interface.
3. **Environment.** Where the agent works: a Core-managed sandbox or your own
   machine. The Runtime daemon prepares capabilities and starts the native harness,
   which works on the workspace and tools.
4. **Outside Core.** The model API and remote MCP servers, called by the harness
   with the Session's model provider.

## Two APIs, and a machine channel

![Three namespaces and their credentials](assets/architecture-api-surfaces.png)

Core serves three namespaces: the Agents API (`/v1`) for applications, the Core
API (`/core/v1`) for operators, and a machine API (`/api/v1`) for nodes and Runtime
daemons. Each has its own credential; one used elsewhere gets 401. The
[API index](api/README.md) owns the full matrix of callers, credentials and routes.

## Core

Core is the only owner of durable execution facts: Projects and keys, Agents,
Sessions, Turns, Items, Environments, files and audit records, all in PostgreSQL.
It schedules Turns, handles cancellation and pending interactions, and checks that
a requested harness, Environment and capability combination is supported before
starting work.

Core does not isolate tools, run a model or talk to a vendor SDK directly. It
selects implementations through interfaces and never branches on a harness,
operating system or provider name. See
[the decoupling principle](../CONTRIBUTING.md#decoupling-principle) and the
[repository map](development.md#repository-map).

## Replaceable parts

| Part | Responsibility | Connects through | Current implementations | Add one |
| --- | --- | --- | --- | --- |
| Sandbox Provider | Creates, bootstraps, renews and reclaims the outer Environment | `SandboxProvider` interface | Docker, microsandbox, E2B, sandbox nodes | [Sandbox Provider guide](sandbox-provider.md) |
| Runtime | Prepares Skills, MCP and files, runs executors, owns local cleanup | Core–Runtime protocol over `/api/v1` | `oac-daemon`: managed Linux; self-hosted Linux, macOS and Windows | [Core–Runtime protocol](runtime-protocol.md) |
| Harness | Runs the native model and tool loop | Harness adapter (`Executor` and `Turn`) | Codex, Claude Code, MiniMax Code | [Harness onboarding](../contracts/agents-api/harness-onboarding.md) |
| Model Provider | Serves inference for the harness | Responses, Anthropic or Chat Completions protocol | Any endpoint speaking one of those protocols | [Model execution](../contracts/agents-api/model-execution.md) |

Replaceability does not mean every combination works. Supported combinations are
declared as capabilities and validated explicitly; see
[Harness selection](../contracts/agents-api/harness-selection.md) and the
[coverage record](../contracts/agents-api/README.md).

## A Session, end to end

![A managed Session from creation to result](assets/architecture-session-flow.png)

For a Core-managed (`openai_hosted`) Session:

1. The application creates a Session through the Agents API.
2. Core asks the Sandbox Provider for an Environment.
3. The provider starts the Runtime using the [bootstrap contract](runtime-bootstrap.md).
4. The daemon dials into Core and advertises its capabilities.
5. Core sends the preparation request; Runtime prepares Skills, MCP declarations
   and initial files inside the Environment.
6. The application sends input.
7. Core prepares and starts execution on the daemon.
8. The daemon's harness adapter starts a native Turn.
9. The harness runs its model and tool loop against the model provider.
10. The daemon streams events, output and usage back to Core, then `done`.
11. The application reads Items and events from Core.

A `self_hosted` Session skips steps 2 and 3: an administrator issues an executor
credential and you start the daemon on your own machine
([self-hosted guide](getting-started/self-hosted.md)). A `none` Session uses an
existing device connection. Everything from step 4 onward is the same protocol.
The [Environment contract](../contracts/agents-api/environments.md) covers
placement and expiry; the [Core–Runtime protocol](runtime-protocol.md) defines
message order, receipts and failure ownership.

## Boundaries to keep in mind

- **Isolation belongs to the outer Environment.** The daemon is not a sandbox
  ([Runtime and outer isolation](design-principles.md#runtime-and-outer-isolation)).
- **Execution and compute have separate lifetimes.** Closing an executor does not
  release its allocation, destroy its Environment or delete its workspace.
  Reclamation is an explicit Sandbox Provider operation.
- **Model keys stay with the compute that owns them.** A self-hosted Session brings
  its own model provider ([why](user-guide.md#which-model-provider-a-session-uses)).
- **Core Web is an administrator console.** It calls only `/core/v1` and cannot
  start Sessions or send input ([Web architecture](web/architecture.md)).
