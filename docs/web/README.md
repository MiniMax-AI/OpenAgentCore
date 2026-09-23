# Agents Core Web

**English** | [简体中文](README.zh-CN.md)

Agents Core Web is the administrator console for a self-deployed Agent Core. It
shows execution records and manages Agents, Sessions and reusable resources through
Core APIs. Business collaboration belongs in Parsar; credentials and execution stay
outside the browser.

![Agents Core Web Dashboard](images/dashboard.png)

## What you can do

- **Operate from one Dashboard** — see loaded Agents, active Sessions, work that needs
  attention, recent activity, and the two common create flows.
- **Build reusable Agents** — start from a blank Agent or a practical template, then
  configure its model, instructions, text behavior, Functions, and HTTP MCP servers.
- **Run durable conversations** — create a Session, send messages, follow live events,
  reopen previous work, inspect trace history, and continue after a failed Turn.
- **Work with tools safely** — review Function calls, submit requested Function results,
  inspect command and patch activity, and attach write-only MCP credentials through Vaults.
- **Choose an Environment** — use Core's default execution path, connect a caller-managed
  self-hosted executor, or use an operator-enabled managed Runtime.
- **Understand the connection** — view Core reachability, build-supported harnesses,
  safe process startup selections, and whether operator model endpoints are configured,
  without exposing their addresses or credentials.

## Product tour

### Dashboard

Dashboard is the starting point. It summarizes the current Agent and Session results,
highlights Sessions that need attention, and links directly to Agent creation or a new
Session.

### Agents

Agents are reusable working profiles. Create one from scratch or begin with a starter
template for incident response, Slack collaboration, data analysis, GitHub investigation,
or contract review. Existing Agents open directly in the editor and can start a Session
from their card.

![Agent library and starter templates](images/agents.png)

### Sessions

Sessions keep the conversation and work history in Core. The workspace combines Session
navigation, live connection state, conversation output, trace inspection, tool activity,
and follow-up input without losing the durable record.

![Durable Session conversation](images/sessions.png)

### Environment Templates

In managed-Environment builds, open **Templates** to view reusable configuration,
create a basic name/network template, edit those fields, or confirm deletion. The
Session creation picker uses the same refreshed catalog. Saving a template does
not start a Runtime or a model request, and edits do not change existing Sessions.
Advanced template profiles remain outside the current Web client coverage.

### System and connection status

System shows what the connected Core build supports and what this process configured at
startup: harnesses, daemon gateway, self-hosted execution, managed sandbox provider and
operator model endpoint presence. It does not aggregate Runtime/daemon observations, so
configuration is never presented as model execution readiness.

![Core connection and capability status](images/system.png)

## Main capabilities

| Area | User experience |
| --- | --- |
| Dashboard | Agent and Session overview, attention queue, recent activity, quick actions |
| Agents | Create, search, inspect, edit, delete, use templates, and start Sessions |
| Sessions | Durable conversation history, Agent filtering, live events, cancellation, retry and continuation |
| Trace | Turn history, usage when reported by Core, command output, Function and patch activity |
| Functions and MCP | Configure supported tools, inspect calls, submit requested results, attach Vault credentials |
| Vaults | Create project Vaults and manage write-only MCP bearer credentials without reading tokens back |
| Environments | Default execution, optional self-hosted executor connection, and optional managed Runtime views |
| Workspace and Files | Inspect supported Environment files and manage project Source Files when enabled by Core |
| Templates | Basic name/network configuration, full catalog reads, partial updates and confirmed deletion |
| System | Connection status plus safe build support and process startup configuration, clearly separated from Session/Environment runtime state |

Capabilities appear only when the connected Core and the Web operator configuration expose
them. Saving an Agent proves that its definition was stored; actual execution still depends
on the Core's runtime, model provider, credentials, and tool connectivity.

## Quick start

### Requirements

- Node.js 22.12+
- pnpm 10.30.3
- a running [compatible Agent Core](#compatible-cores)
- a caller key issued by that Core's operator

### Start Web

```bash
git clone https://github.com/MiniMax-AI/parsar-core.git
cd parsar-core
pnpm install
cp .env.example .env.local
pnpm dev:web
```

The default development server opens on `http://127.0.0.1:4173` and proxies browser
requests to a Core on `http://127.0.0.1:8091`.

Configure the local proxy in `.env.local`:

```dotenv
AGENTS_API_PROXY_TARGET=http://127.0.0.1:8091
AGENTS_API_PROXY_TOKEN_FILE=/absolute/private/path/to/web-token
```

Only the local Web server reads the caller-key file. Do not place a plaintext key in a
`VITE_*` variable, URL, screenshot, Agent, Session metadata, or Git.

### First workflow

1. Open **Agents** and choose **Create agent** or a starter template.
2. Set a name, model, and instructions; add supported tools only when needed.
3. Select **Start Session**, choose an Environment, and enter the first message for conversation-only execution. Hosted Sessions may be created without input.
4. Continue in **Sessions** while live events and durable history update.
5. Use **Trace**, **Vaults**, **Environment**, or **System** when the workflow needs them.

## Local Core and Docker

When Web cannot reach the local Core, Dashboard opens a connection guide with two paths:

- **Already set up** — start the configured database, Core API, and daemon containers,
  verify `/healthz`, then run **Test connection**.
- **First time on this computer** — build the Core image and follow the current
  in-repository guides to create the database, caller identity, migrations, API container, and daemon
  profile.

Enable the local recovery guide with the non-secret settings documented in
[`.env.example`](../../.env.example). Web displays reviewed commands but never receives the
Docker socket, executes commands, creates credentials, or invents database identities.

For complete setup, see [Connecting Agent Core](core-connection.md). Starting a
daemon can release queued work and trigger model usage; review pending Sessions first.

## Compatible Cores

Agents Core Web uses the tested `/v1/agents/**` HTTP and SSE contract documented in
[Protocol coverage](protocol-coverage.md).

| Core | Status |
| --- | --- |
| [Parsar Core at `0000a0b`](https://github.com/MiniMax-AI/parsar-core/tree/0000a0b32523deb0f7a4d907f31ca4503cff7e9e) | In-repository primary integration |
| Another Core implementing the documented subset | Compatible after contract qualification |
| OpenAI hosted Agents API | Complete compatibility is not claimed |
| OpenAI Agents SDK, Responses API, or Parsar daemon WebSocket | Different interfaces; not direct Core endpoints |

The browser talks to Core. Core owns authentication, durable Agent and Session state,
scheduling, execution, and runtime resources. The browser never connects directly to a
daemon or model provider.

## Documentation

- [Connecting Agent Core](core-connection.md) — local, remote, Docker, daemon and Environment setup
- [Protocol coverage](protocol-coverage.md) — tested resources, events and capability boundaries
- [Architecture](architecture.md) — components, ownership and trust boundaries
- [Roadmap](roadmap.md) — planned product and Core integrations
- [Contributor policy](../../AGENTS.md) — repository scope, security and quality requirements

The screenshots use isolated local fixture data and do not contain production data or
prove model-provider readiness.

Agents Core Web is available under the [MIT License](../../LICENSE).
