---
title: "Examples"
---

Complete applications built on the [Agents API guide](./api/public-agent-api.md), each running against a real Core installation with a Project API key.

## Parsar Agent workbench

A conversation workspace built on Core. Assemble Agents from a resource library and an environment template, then talk to them in a local Web chat or through Feishu bots that act as the Agent's identity. Both entries create ordinary public Sessions.

Source: [`example/parsar`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/README.md).

Run and configure it with the [example README](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/README.md#run), which also owns its supported features, storage and validation.

### What to look at

Each feature maps to one part of the API. Read the code next to the guide section:

| Feature | API used | Guide | Code |
| --- | --- | --- | --- |
| Server-side key and headers | Bearer key, `OpenAI-Beta`, route allowlist | [Before you start](./api/public-agent-api.md#before-you-start) | [`server.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server.mjs) |
| Agents with a Harness and model | `x_agents_core.harness`, `model_provider` | [Core extensions](./api/public-agent-api.md#core-extensions) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs) |
| Web and Feishu conversations | Public Session creation, input events, Turns and Items | [Send input](./api/public-agent-api.md#send-input) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs), [`server/feishu.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/feishu.mjs) |
| Hosted Environments from templates | `openai_hosted` with packages, files, `env` and `setup_commands` | [Create a Session](./api/public-agent-api.md#create-a-session) | [`server/runtime-profile.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/runtime-profile.mjs) |
| Crash-safe Session creation | `Idempotency-Key`, recovery from a lost response | [Idempotency](./api/public-agent-api.md#idempotency) | [`server/sessions.mjs`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/server/sessions.mjs) |
| Live replies | Event stream, text deltas, reconnect and reconcile | [Stream events](./api/public-agent-api.md#stream-events) | [`src/lib/live-session.ts`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/lib/live-session.ts) |
| Follow-ups and cancel | Input events with idempotency keys | [Send input](./api/public-agent-api.md#send-input) | [`src/Composer.tsx`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/Composer.tsx) |
| History | Paging Turns and Items | [Pagination](./api/public-agent-api.md#pagination) | [`src/lib/api.ts`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/lib/api.ts) |
| Skills and versions | `/skills`, default version | [Skills](./api/public-agent-api.md#skills) | [`src/Skills.tsx`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/parsar/src/Skills.tsx) |

To contribute an example, follow the [application example boundary](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#optional-application-example).
