# Call your Core

Install Core using the [installation guide](install.md). Your Core API credential
authenticates the caller to your installation. It is separate from the model
provider key used by a native harness. Neither key belongs in source control.

Use the fixed client baseline:

```sh
python3 -m venv .venv
. .venv/bin/activate
pip install openai==3.13.0
```

Read the generated caller key from the private installation directory. For an
installation on another machine, use its authenticated HTTPS API endpoint.

```python
from pathlib import Path
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8091/v1",
    api_key=Path.home().joinpath(".parsar/core/config/caller.key").read_text().strip(),
    default_headers={"OpenAI-Beta": "agents=v1"},
)
print(client.beta.agents.list().data)
```

This read verifies API access. It does not invoke a model or create an execution
environment. Installation has no mandatory sample task.

## Run a Session when you are ready

This example requires a connected node whose provider is ready. The default
installation has zero execution nodes: sign in to Web and
[add a Docker or microsandbox node](install.md#add-nodes-after-a-default-installation).
A local provider enabled during installation also satisfies this requirement.
You do not need to reinstall Core or change installer flags to add nodes in Web.

Core creates the sandbox through its Provider using the prepared Runtime image,
then initializes the daemon, native harness and workspace inside it. You do not
install or start a separate daemon for a Core-managed Session. The public discriminator remains
`openai_hosted`; in this deployment it means the sandbox managed by Parsar Core.
The installation's provider can be microsandbox or Docker.

For a Codex-compatible Responses endpoint, provide your actual model name,
endpoint and key through your application's private configuration:

```python
import os

session = client.beta.agents.sessions.create(
    environment={"type": "openai_hosted"},
    input="Create /workspace/hello.txt with a short greeting, then describe it.",
    extra_body={
        "agent": {
            "model": os.environ["MODEL_NAME"],
            "x_agents_core": {"harness": "codex"},
        },
        "x_agents_core": {
            "model_provider": {
                "protocol": "responses",
                "base_url": os.environ["MODEL_BASE_URL"],
                "api_key": os.environ["MODEL_API_KEY"],
            }
        },
    },
)
print(session.id)
```

Running this example makes a real model request and may incur provider charges.
`extra_body` carries existing Core extensions: harness selection and write-only
model configuration. They are not fields in the official SDK 3.13.0 protocol.
The service encrypts model configuration with tenant/Session binding and never
returns the secret through public resource reads. Keep the installation's
credential encryption key and database together across restarts.
Keep the complete `agent` object together: SDK 3.13.0 replaces an ordinary body
field with the corresponding `extra_body` field rather than merging nested fields.

The model must support the selected harness's native protocol:

| Harness selector | Model protocol | Additional input |
| --- | --- | --- |
| `codex` | `responses` | Exact provider model ID |
| `claude_sdk` | `anthropic` | Exact provider model ID |
| `mcode` | `anthropic` | Actual `context_window` and `max_output_tokens` limits |

See [harness selection](https://github.com/MiniMax-AI/parsar-core/blob/main/contracts/agents-api/harness-selection.md) and
[model execution](https://github.com/MiniMax-AI/parsar-core/blob/main/contracts/agents-api/model-execution.md) for the complete
extension contract. Native capabilities differ; selecting an engine does not make
an unsupported model or operation work.

## Observe and recover

Use the existing console or query the same resources from your application:

```python
current = client.beta.agents.sessions.retrieve(session.id)
turns = client.beta.agents.sessions.turns.list(session.id)
items = client.beta.agents.sessions.items.list(session.id)
print(current.id, turns.data, items.data)
```

Wait for the Turn's terminal result before treating a task as complete. After a
client disconnect, recover through Session, Turn and Items queries. SSE is a live
stream, not a historical replay mechanism. Do not automatically submit the same
work as a new Session when a response is lost.

Files, Artifacts, cancellation, credential management and their current limits
are documented in the [coverage ledger](https://github.com/MiniMax-AI/parsar-core/blob/main/contracts/agents-api/README.md).
MCP credentials use the separate Vault API; they are not Core caller keys or
model-provider credentials.
