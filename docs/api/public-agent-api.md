# Public Agent API

Applications call Core directly with an API key issued inside a Project. They use
the public API to create and execute Agents, query history, and manage their assets.
They do not use the console's administrator credential or browser session cookie.

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

## Core extensions

These are application-facing Core capabilities, not official OpenAI operations.
They retain Project authentication and scope. Web administrators use the separate
management equivalents where provided.

| Extension | Reference |
| --- | --- |
| Harness selection and write-only model access | [Harness selection](../../contracts/agents-api/harness-selection.md), [model execution](../../contracts/agents-api/model-execution.md) |
| Effective Session execution configuration | [Configuration query](../../contracts/agents-api/execution-configuration.md) |
| Startup support and process configuration | [Startup configuration](../../contracts/agents-api/startup-configuration.md) |
| Current Runtime observations and stored telemetry | [Observation API](../../contracts/agents-api/runtime-observability-api.md), [history API](../../contracts/agents-api/runtime-history-api.md) |
| Environment-bound Runtime credential issuance/revocation | [Executor credentials](../../contracts/agents-api/environment-executor-credentials.md); `/core/v1/environments/{environment_id}/executor-credentials` is Project-authenticated despite its `/core` prefix |

`openai_hosted` keeps the official wire name and means Core-managed compute here.
The deployment chooses E2B or its own Docker/microsandbox nodes. A public
`self_hosted` Environment uses caller-owned compute and the same colocated
Runtime. Our daemon transport and the supported native harness differences are
explicit; they do not imply interoperability with OpenAI's stock executor transport.
