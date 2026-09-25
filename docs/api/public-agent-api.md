# Public Agent API

Applications call Core directly with an API key issued inside a Project. They use
the public API to create and execute Agents, query history, and manage their assets.
They never use the Core key or the console's browser session cookie.

## Authentication and base URL

Set the OpenAI client's `base_url` to the Core origin followed by `/v1`. Send
`Authorization: Bearer <project-api-key>`. The fixed SDK supplies
`OpenAI-Beta: agents=v1` for the Beta Agents resources; raw HTTP callers must add
it. General Files and Skills endpoints do not require that Beta header.

```python
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["PARSAR_PROJECT_API_KEY"],
    base_url=os.environ["PARSAR_CORE_URL"].rstrip("/") + "/v1",
)
page = client.beta.agents.list()
```

This read does not execute a model. See the [real execution quickstart](../getting-started/quickstart.md)
for separate model access configuration and a complete Session example.

All keys in a Project share assets and Session creator identity. Revoking one key
leaves the others valid; archiving the Project revokes all its keys. The optional
`OpenAI-Organization` and `OpenAI-Project` headers must match `core` and
`proj_<project UUID>` respectively. Do not send a key's UUID as the project scope.

## Public resources

Paths are relative to `/v1`. Detailed fields, union types and response shapes are
in the [public OpenAPI](../../contracts/agents-api/openapi.yaml), fixed SDK source
and linked semantic records. The [resource inventory](../../contracts/agents-api/README.md#upstream-resource-inventory)
lists each operation and its coverage.

| Resource | Route family | Semantics |
| --- | --- | --- |
| Agents | `/agents` | Reusable configuration, CRUD and lists |
| Sessions and Turns | `/agents/sessions`, nested `/turns` | Admission, metadata, state and recovery queries |
| Items and events | Session `/items`, `/events` | Persisted history and live SSE |
| Artifacts | Session `/artifacts` | Captured output metadata, immutable content and deletion |
| Subagents | Session `/subagents`, nested `/turns` and `/items` | Scoped child execution reads; [native limits](../../contracts/agents-api/subagents.md) |
| Environments and workspace Files | `/agents/environments`, nested `/files` | [Environment state](../../contracts/agents-api/environments.md) and [workspace access](../../contracts/agents-api/environment-files.md) |
| Environment Templates | `/agents/environments/templates` | [Reusable initialization](../../contracts/agents-api/environment-templates.md) |
| Source Files | `/files` | [Uploaded content resources](../../contracts/agents-api/source-files.md), distinct from live workspace files |
| Skills and versions | `/skills`, nested `/versions` | [Bundles and versions](../../contracts/agents-api/file-resource-semantics.md) |
| Vaults and Credentials | `/vaults`, nested `/credentials` | [Scoped write-only credential storage](../../contracts/agents-api/environments.md) |

SSE is live, not a historical replay service. After a disconnect, query the Session,
Turns and Items to reconcile state. An accepted request is not proof of completed
native execution. Do not automatically replay uncertain tool or file effects.

## Core extension fields

The `/v1` route set is exactly the pinned SDK's operations, listed in
[upstream-routes.json](../../contracts/agents-api/upstream-routes.json); Core adds
no route. It adds fields only inside `x_agents_core`, because it runs several
harnesses and accepts custom model access:

| Field | Where | Reference |
| --- | --- | --- |
| `x_agents_core.harness` | Saved Agent create, update and read; the inline Session `agent`; the Session's effective `agent` | [Harness selection](../../contracts/agents-api/harness-selection.md) |
| `x_agents_core.model_provider` | Saved Agent create, update and read; Session creation. It takes `protocol`, `base_url`, optional `context_window` and `max_output_tokens`, and a write-only `api_key`; reads return `api_key_configured` instead of the key | [Model execution](../../contracts/agents-api/model-execution.md) |

Any other member of `x_agents_core` is rejected with 400. Deployment, placement,
credential issuance and operational reads are not part of `/v1`; they belong to
`/core/v1`, which only the Core key can call (see the [API index](README.md)).

An application reads a Session's model from `agent.model` and an explicitly
selected harness from `agent.x_agents_core.harness`; a Session on the deployment
default harness omits `agent.x_agents_core`. `/v1` has no execution-configuration
read.

`openai_hosted` keeps the official wire name and means Core-managed compute here.
The deployment chooses E2B or its own Docker/microsandbox nodes. A public
`self_hosted` Environment uses caller-owned compute and the same colocated
Runtime. For a `self_hosted` Session, the deployment operator issues the
Environment's executor credential with the Core key; see
[executor credentials](../../contracts/agents-api/environment-executor-credentials.md).
Our daemon transport and the supported native harness differences are
explicit; they do not imply interoperability with OpenAI's stock executor transport.
