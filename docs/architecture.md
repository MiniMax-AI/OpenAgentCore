---
title: "Architecture"
---

OpenAgentCore is an Agent execution platform that brings Harnesses, models, tools, Sessions and execution environments together through replaceable protocol implementations. It separates orchestration, compute and native execution. Core owns the API and durable state. Sandbox Providers manage compute. Independent workspace filesystem adapters manage durable Environment storage through a separate protocol. A Runtime daemon prepares an Environment and runs the selected Harness, whose native SDK or protocol owns the model and tool loop.

The recommended self-managed deployment is microsandbox with kernel NFS workspace storage. E2B supplies managed compute. Docker runs on nodes without checkpoint or external storage. All use the same Core orchestration and capability checks. Compute and filesystem adapters declare their supported combinations; a deployment choice does not introduce a separate Agent execution flow. See [Sandbox Providers](./sandbox-provider.md) and [workspace filesystem providers](./workspace-provider.md) for their contracts and supported capabilities.

```mermaid
flowchart TB
    App["Application"] -->|"Application–Core"| Core
    Web["Web"] -->|"Web–Core"| Core
    Core[("Core + PostgreSQL")]
    Core -->|"Core–Sandbox Provider"| SP["Sandbox Provider"]
    Core -->|"Workspace filesystem"| FS["Workspace storage"]
    SP -->|"Provider–Runtime"| Env
    Core <-->|"Core–Runtime"| Env["Environment:<br/>Runtime + Harness"]
    Env -->|"Harness–Model provider"| Model["Model provider"]
```

The Runtime daemon initiates its authenticated WebSocket connection to Core. The [API index](./api/index.md) describes the application, operator and machine namespaces; [protocol boundaries](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#protocols-at-every-boundary) lists each protocol's code and owning document.

## Component responsibilities

| Component | Responsibility | Reference |
| --- | --- | --- |
| Core | Authenticate callers, resolve and freeze configuration, schedule Turns, handle cancellation and pending interactions, persist resources and execution facts in PostgreSQL | [Core service](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md) |
| Sandbox Provider | Create, observe, renew and reclaim compute; supply Runtime startup input | [Sandbox Provider protocol](./sandbox-provider.md), [Runtime bootstrap](./runtime-bootstrap.md) |
| Workspace filesystem adapter | Create, observe and delete durable Environment storage; validate and resolve its compute attachment | [Workspace filesystem protocol](./workspace-provider.md) |
| Sandbox node | Operate a Docker or microsandbox host and reconcile its assigned generation and allocations | [Sandbox node protocol](../contracts/agents-api/node-generation-protocol.md) |
| Runtime | Prepare the workspace and capabilities, manage Session Executors, execute Turns and report events and receipts | [Core–Runtime protocol](./runtime-protocol.md) |
| Harness adapter | Validate native configuration, invoke the upstream SDK or protocol, translate events and confirm native cleanup | [Harness onboarding](../contracts/agents-api/harness-onboarding.md) |
| Model provider | Serve the model protocol selected for the Harness | [Model execution](../contracts/agents-api/model-execution.md) |
| Web | Let administrators configure and observe the installation through a server-side Core API connection | [Console server](./web/console-server.md) |

The Harness owns the native Agent loop and context management. Applications own business workflows, reviewer policies and quality evaluation; these do not become Core execution paths.

The [repository map](./development.md#repository-map) locates these components. [Concepts](./concepts.md) explains Project boundaries, administrator authority and tool isolation.

## A Session, end to end

An application creates a Session through the Agents API, and Core resolves its configuration and execution location. A managed Session obtains compute from the selected Sandbox Provider. A self-hosted Session waits for the user to run its installation command, which starts the Runtime in place of steps 2 and 3. A Session with `environment: none` uses a connected execution device without a workspace. The [application guide](./api/public-agent-api.md#create-a-session) describes these choices.

```mermaid
%%{init: {"sequence": {"actorMargin": 16, "width": 100}}}%%
sequenceDiagram
    autonumber
    participant App as Application
    participant Core
    participant P as Sandbox Provider
    participant R as Runtime
    participant H as Harness
    App->>Core: Create Session
    Core->>P: Create(Bootstrap)
    P->>R: Start Runtime
    R->>Core: Connect, heartbeat
    Core->>R: runtime_prepare
    R-->>Core: prepare result
    App->>Core: Send input
    Core->>R: execution_prepare
    R-->>Core: status ready
    Core->>R: execution_start
    R->>H: Run the Turn
    H-->>R: Native events
    R-->>Core: delta, tool_call, done
    Core-->>App: Stream events
```

After the daemon connects, Core checks the available Harness and requested capabilities. The Runtime prepares a workspace Environment and its capability snapshot, then prepares or reuses the Session Executor. Core persists the output, tool interactions and receipts for application reads and events. Completion or cancellation settles the Turn; a healthy Executor can serve the next Turn in the same Environment.

For checkpoint-capable compute, Core automatically suspends idle work and releases active compute capacity after confirmed capture and source stop. New work restores retained memory on an eligible compatible node; independent workspace storage stays attached to the same Environment identity. The [checkpoint lifecycle](./sandbox-provider.md#suspension) owns idle admission, wake and retention, and [checkpoint transfer](./sandbox-provider.md#checkpoint-transfer) defines the shared-store and compatibility requirements. An unreachable node alone never authorizes a replacement writer.

Execution, compute and independent workspace storage have separate lifetimes. Closing an Executor does not itself delete storage. The [workspace filesystem contract](./workspace-provider.md#lifetime-and-filesystem-scope) owns persistent storage retention and explicit deletion; compute checkpoint expiry does not authorize its reclamation. Preparation, connection and execution readiness have distinct states. The [Environment contract](../contracts/agents-api/environments.md) owns preparation, and the [Core–Runtime protocol](./runtime-protocol.md) owns ordering, receipts and failure handling.
