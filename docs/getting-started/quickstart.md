---
title: "Run your first Session"
---

Run an agent through Core with the official OpenAI Python SDK: connect with a Project API key, start a Session that writes a file, and read the result. You need the key and the API base URL from your administrator.

Before you start, have:

- a Project API key and the API base URL ([Issue a Project API key](./install.md#issue-a-project-api-key));
- a ready node or E2B backend, so Core has somewhere to run the agent ([Add and manage nodes](./nodes.md));
- Python 3.9 or newer;
- a model: the installation's default model, or your own provider's model ID, base URL and API key. [Model execution](../../contracts/agents-api/model-execution.md#saved-defaults-and-precedence) says which one a Session uses and which protocols each Harness speaks.

The example uses Codex, whose model provider must speak the OpenAI Responses API.

## 1. Connect

Core serves the OpenAI Agents API, so the official OpenAI SDK works unchanged. It reads two environment variables:

| Variable | Value |
| --- | --- |
| `OPENAI_BASE_URL` | The API base URL: the public URL plus `/v1`, such as `https://core.example/v1`. Web's **Settings → Service and deployment** page shows it |
| `OPENAI_API_KEY` | Your Project API key |

```sh
python3 -m venv .venv
. .venv/bin/activate
pip install openai==3.13.0
export OPENAI_BASE_URL=https://core.example/v1
read -rs OPENAI_API_KEY && export OPENAI_API_KEY   # paste the key; it is not echoed
```

Check access. This runs no model and creates no sandbox:

::: code-group

```python [Python]
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
print(client.beta.agents.list().data)
```

```sh [curl]
curl "$OPENAI_BASE_URL/agents" -H "OpenAI-Beta: agents=v1" \
  -H @<(printf 'Authorization: Bearer %s\n' "$OPENAI_API_KEY")
```

:::

An empty list (`[]`) means you are connected.

## 2. Start a Session

A Session is one agent conversation with its own workspace. With `openai_hosted`, Core creates a sandbox for it on a node or E2B and starts the Harness inside.

To use your own provider instead of the installation's default model, export its settings and run the second tab:

```sh
export MODEL_NAME='your-model-id'
export MODEL_BASE_URL='https://your-provider.example/v1'
read -rs MODEL_API_KEY && export MODEL_API_KEY
```

::: code-group

```python [Default model]
session = client.beta.agents.sessions.create(
    environment={"type": "openai_hosted"},
    input="Create /workspace/hello.txt with a short greeting, then describe it.",
    extra_body={"agent": {"x_agents_core": {"harness": "codex"}}},
)
print(session.id)
```

```python [Your own provider]
import os

session = client.beta.agents.sessions.create(
    environment={"type": "openai_hosted"},
    input="Create /workspace/hello.txt with a short greeting, then describe it.",
    extra_body={
        "agent": {"model": os.environ["MODEL_NAME"], "x_agents_core": {"harness": "codex"}},
        "x_agents_core": {"model_provider": {
            "protocol": "responses",
            "base_url": os.environ["MODEL_BASE_URL"],
            "api_key": os.environ["MODEL_API_KEY"],
        }},
    },
)
print(session.id)
```

:::

This makes a real model request and may incur charges.

- `x_agents_core` holds Core's additions to the OpenAI API; see [Core extensions](../api/public-agent-api.md#core-extensions).
- Keep the whole `agent` object in `extra_body`. SDK 3.13.0 replaces a body field with the matching `extra_body` field instead of merging them.

## 3. Wait for the result

A Session ID confirms creation, not success. Poll durable state; never resubmit to "retry":

```python
import time

for _ in range(120):
    turns = client.beta.agents.sessions.turns.list(session.id, order="desc").data
    if turns and turns[0].status in {"completed", "failed", "cancelled"}:
        turn = turns[0]
        print("Turn:", turn.id, turn.status)
        print(client.beta.agents.sessions.items.list(session.id).data)
        break
    if client.beta.agents.sessions.retrieve(session.id).status == "failed":
        raise RuntimeError("Session preparation failed; inspect its Environment")
    time.sleep(1)
else:
    raise TimeoutError(f"Session {session.id} is still running; inspect it before retrying")
```

It prints:

```text
Turn: 00000000-0000-4000-8000-000000000003 completed
[…the Turn's Items, including the assistant message that describes /workspace/hello.txt…]
```

A `completed` Turn whose Items describe the new file means the agent ran. A timeout neither cancels the work nor proves it failed.

## Next steps

| To | Read |
| --- | --- |
| Stream output, send follow-up messages, upload files, add Skills or MCP, cancel | [Agents API guide](../api/public-agent-api.md#common-tasks) |
| See every resource with request and response examples | [Agents API guide](../api/public-agent-api.md) |
| Run the agent on your own machine | [Run self-hosted Sessions](./self-hosted.md) |
| See a complete application | [Examples](../examples.md) |
