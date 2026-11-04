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

## RL and evaluation rollouts

A Python script that runs batches of tasks as hosted Sessions, with any Harness and model, and grades each answer outside the sandbox. It shows Core as a rollout engine for RL training and evaluation. Each rollout is one Session with no network, task files as initial files and the answer as an Artifact.

Source: [`example/rl-eval`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/README.md).

Run and configure it with the [example README](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/README.md#run), which also owns its output format, task format and training setup.

### What to look at

| Feature | API used | Guide | Code |
| --- | --- | --- | --- |
| One Session per rollout | Inline `input`, `metadata` and `openai_hosted` | [Create a Session](./api/public-agent-api.md#create-a-session) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| Task files without network | Inline `files` and `network.access: disabled` | [Initial files](../contracts/agents-api/environments.md#initial-files) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| Harness and model matrix | `x_agents_core.harness`, `model`, `model_provider` | [Core extensions](./api/public-agent-api.md#core-extensions) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| Wait, time out and cancel | Turn list polling, `agent.session.input.cancel` | [Cancel](./api/public-agent-api.md#cancel) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| Answer outside the sandbox | Artifacts under `outputs/` | [Artifacts](./api/public-agent-api.md#artifacts) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |
| Trajectories and usage | Items and Turn `usage` | [Turns and Items](./api/public-agent-api.md#turns-and-items) | [`rl_eval.py`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/example/rl-eval/rl_eval.py) |

To contribute an example, follow the [application example boundary](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#optional-application-example).
