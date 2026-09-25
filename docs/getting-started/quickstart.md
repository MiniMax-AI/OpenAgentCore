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

The deployment administrator first creates a Project and issues a key within it,
on Web's **Projects and keys** page or with the [Core key](operations.md#core-key)
as Bearer credential: `POST /core/v1/projects` with `{"name":"Default"}`, then
`POST /core/v1/projects/{project_id}/keys` with a descriptive `{"name":"..."}`.

Obtain that key through a private channel and set it as `OPENAI_API_KEY` in your
application's environment. Set `OPENAI_BASE_URL` to the installation's API base
URL: `api_base_url` in `GET /core/v1/installation`, which is the public URL followed
by `/v1`, such as `https://core.example/v1`. The official SDK reads both variables.
The key's plaintext appears only at issuance; Core stores a digest in its database.
All keys in the Project share assets, permissions and the same execution principal,
while write provenance records the actual key. Other Projects remain isolated. The
installer creates neither a Project nor an API key. The API base URL reaches Core
directly; the console does not proxy `/v1`.

```python
from openai import OpenAI

# Reads OPENAI_API_KEY and OPENAI_BASE_URL.
client = OpenAI(default_headers={"OpenAI-Beta": "agents=v1"})
print(client.beta.agents.list().data)
```

Rotate an application key by issuing a replacement in the same Project and revoking
the old key. Assets and the Project principal remain unchanged. Archiving a Project
disables every key while preserving assets and already accepted work. The deployment
administrator credential cannot substitute for an application key on `/v1`.

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

Query execution history from your application:

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
