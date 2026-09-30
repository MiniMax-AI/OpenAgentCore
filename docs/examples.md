# Examples

Complete applications built on the [Agents API](api/public-agent-api.md). Each one
runs against a real Core installation with a Project API key.

| Example | What it shows |
| --- | --- |
| [Parsar Agent workbench](#parsar-agent-workbench) | A product UI: models, Skills, MCP, reusable Agents, and Sessions on any runtime |

## Parsar Agent workbench

A small single-user product built on Core. You save model providers, Skills and MCP
servers, combine them into Agents, then start Sessions in a managed sandbox, with
no workspace, or on your own machine.

Source: [`example/parsar`](../example/parsar/README.md).

### Run it

You need Node 22.13+, pnpm 10.30.3, a running Core with a default model, and a Project
API key.

```sh
export OAC_EXAMPLE_CORE_URL='https://core.example'   # origin, without /v1
export OAC_EXAMPLE_PROJECT_KEY='<project-api-key>'
pnpm install --frozen-lockfile
pnpm --filter @oac/parsar-example dev
```

Open <http://127.0.0.1:18180>. The example [README](../example/parsar/README.md) covers
storage, a production build, tests and connecting your own machine.

### What to look at

Each feature maps to one part of the API. Read the code next to the guide section:

| Feature | API used | Guide | Code |
| --- | --- | --- | --- |
| Server-side key and headers | Bearer key, `OpenAI-Beta`, route allowlist | [Before you start](api/public-agent-api.md#before-you-start) | [`server.mjs`](../example/parsar/server.mjs) |
| Agents with a harness and model | `x_agents_core.harness`, `model_provider` | [Core extensions](api/public-agent-api.md#core-extensions-x_agents_core) | [`server/sessions.mjs`](../example/parsar/server/sessions.mjs) |
| Sessions on three runtimes | `openai_hosted`, `none`, `self_hosted` with `workspace_directory` | [Create a Session](api/public-agent-api.md#create-a-session) | [`server/sessions.mjs`](../example/parsar/server/sessions.mjs) |
| Crash-safe Session creation | `Idempotency-Key`, recovery from a lost response | [Idempotency](api/public-agent-api.md#idempotency) | [`server/sessions.mjs`](../example/parsar/server/sessions.mjs) |
| Live replies | Event stream, text deltas, reconnect and reconcile | [Stream events](api/public-agent-api.md#stream-events) | [`src/lib/live-session.ts`](../example/parsar/src/lib/live-session.ts) |
| Follow-ups and cancel | Input events with idempotency keys | [Send input](api/public-agent-api.md#send-input) | [`src/Composer.tsx`](../example/parsar/src/Composer.tsx) |
| History | Paging Turns and Items | [Pagination](api/public-agent-api.md#pagination) | [`src/lib/api.ts`](../example/parsar/src/lib/api.ts) |
| Skills and versions | `/skills`, default version | [Skills](api/public-agent-api.md#skills) | [`src/Skills.tsx`](../example/parsar/src/Skills.tsx) |
| Connect your own machine | Environment status, executor credential | [Self-hosted execution](getting-started/self-hosted.md) | [`src/ConnectMachine.tsx`](../example/parsar/src/ConnectMachine.tsx) |

### Not covered

Vaults and MCP authentication, OAuth, Session deletion, MiniMax Code on your own
machine, and managed Skills or templates on your own machine
(`x_agents_core.environment`).

## Add an example

Put it in its own directory under [`example/`](../example/README.md) with a README
covering how to run it and which API features it shows, then add a row to the
table above. Keep examples on the public API with a Project API key: never the Core
key or private routes. Repository rules for examples are in
[CONTRIBUTING.md](../CONTRIBUTING.md#repository-boundary).
