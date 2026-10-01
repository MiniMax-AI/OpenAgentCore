---
title: "Agents API guide"
---

Core serves the [OpenAI Agents API](https://platform.openai.com/docs/api-reference) at `/v1`. Use the official OpenAI SDK or plain HTTP. This guide shows both for every common operation, and notes where Core differs from OpenAI.

New to the API? Run the [quickstart](../getting-started/quickstart.md) first.

## Before you start

**Base URL and key.** Your administrator gives you the API base URL, such as `https://core.example/v1`, and a Project API key.

```sh
export OPENAI_BASE_URL=https://core.example/v1
read -rs OPENAI_API_KEY && export OPENAI_API_KEY
```

**SDK.** Use the pinned version:

```sh
pip install openai==3.13.0
```

```python
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
```

**HTTP.** Every request needs a Bearer key. Routes under `/agents` and `/vaults` also need `OpenAI-Beta: agents=v1`; `/files` and `/skills` do not. The SDK sets both. The HTTP examples below use this shell helper:

```sh
oac() {  # oac PATH [curl options]: call /agents or /vaults with the required headers
  curl -sS "$OPENAI_BASE_URL$1" \
    -H "Authorization: Bearer $OPENAI_API_KEY" \
    -H "OpenAI-Beta: agents=v1" \
    -H "Content-Type: application/json" "${@:2}"
}
oac /agents
```

On a shared host, `-H @<(printf 'Authorization: Bearer %s\n' "$OPENAI_API_KEY")` keeps the key out of the process list.

## Resources at a glance

| Resource | Path | What it is |
| --- | --- | --- |
| [Agents](#agents) | `/agents` | Reusable configuration: model, instructions, tools, harness |
| [Sessions](#sessions) | `/agents/sessions` | One agent conversation with its own Environment |
| [Input events](#send-input) | `/agents/sessions/{id}/events` (POST) | Messages, cancellation and tool results |
| [Event stream](#stream-events) | `/agents/sessions/{id}/events` (GET) | Live server-sent events |
| [Turns and Items](#turns-and-items) | `/agents/sessions/{id}/turns`, `/items` | Durable history |
| [Files](#files) | `/files`, `/agents/environments/{id}/files`, `/agents/sessions/{id}/artifacts` | Uploads, workspace files and outputs |
| [Skills](#skills) | `/skills` | Versioned capability bundles |
| [Environment Templates](#environment-templates) | `/agents/environments/templates` | Reusable workspace setup |
| [Vaults](#vaults) | `/vaults` | Write-only credentials for MCP servers |
| Subagents | `/agents/sessions/{id}/subagents` | Read-only child work; see [subagents](../../contracts/agents-api/subagents.md) |

Core has exactly the routes of the pinned SDK, listed in [upstream-routes.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream-routes.json). It adds no route; its additions live in [`x_agents_core`](#core-extensions-x_agents_core).

## Common tasks

| To | Use |
| --- | --- |
| Continue a conversation, or steer a running Turn | [Send a message](#send-a-message) to the same Session. For a different configuration or workspace, create a new Session |
| Watch output live | [Stream events](#stream-events) |
| Stop the current Turn, or recover after a lost response | [Cancel](#cancel), [idempotency](#idempotency) |
| Call your own code from the agent | [Function tools](#function-tools) |
| Give the agent Skills, packages, files and setup commands | [Skills](#skills), [Environment Templates](#environment-templates) |
| Connect an MCP server with credentials | [Vaults](#vaults) and [execution tools](../../contracts/agents-api/execution-tools.md) |
| Use Skill or Plugin directories on your own machine | [Local capability directories](../getting-started/self-hosted.md#local-capability-directories) |
| Put files in the workspace, or download what the agent wrote | [Files](#files) |
| Read the conversation and tool results | [Turns and Items](#turns-and-items) |
| Find out why a Session or Turn failed | [Diagnose a failure](#diagnose-a-failure) |

## Conventions

### Pagination

Lists return:

```json
{"object": "list", "data": [...], "has_more": true, "first_id": "...", "last_id": "..."}
```

| Parameter | Meaning |
| --- | --- |
| `limit` | 1–100, default 20 |
| `order` | `desc` (default) or `asc` |
| `after` | Pass the previous page's `last_id` |

The SDK pages for you:

```python
for session in client.beta.agents.sessions.list(limit=100):
    print(session.id, session.status)
```

```sh
oac "/agents/sessions?limit=100&after=$LAST_ID"
```

Two lists differ: `GET /files` returns up to 10,000 files at once, and workspace files use an opaque `page` token (see [Workspace files](#workspace-files)).

### Idempotency

Send an `Idempotency-Key` (up to 128 bytes) when creating a Session or sending input. A retry with the same key and body returns the original result instead of doing the work twice. The same key with a different body fails with 409 `idempotency_conflict`.

```python
import uuid

key = str(uuid.uuid4())  # store it before sending, reuse it on retry
client.beta.agents.sessions.events.create(session_id, events=[...], idempotency_key=key)
client.beta.agents.sessions.create(environment=..., extra_headers={"Idempotency-Key": key})
```

**After a lost response,** retry with the same key, then read the Session, Turns and Items. Never resend without the key. Core keeps creation keys even after the Session is deleted.

See [creation retries](../../contracts/agents-api/wire-semantics.md#creation-retries) for comparison rules and the difference from OpenAI.

### Errors

```json
{"error": {"type": "invalid_request_error", "message": "...", "code": "model_provider_required", "param": "x_agents_core.model_provider"}}
```

| Status | Common codes | Meaning |
| --- | --- | --- |
| 400 | `invalid_request_error`, `invalid_beta`, `model_provider_required`, `unsupported_or_invalid_configuration` | Fix the request. `invalid_beta` means a missing or wrong `OpenAI-Beta` header |
| 401 | `invalid_api_key` or none | Wrong or missing key, or a key from another namespace |
| 404 | `not_found_error` on Beta routes | Missing, or belongs to another Project. The two look the same |
| 405 | `unsupported_operation` | Core doesn't support this operation |
| 409 | `conflict_error`, `idempotency_conflict` | State conflict, for example deleting a busy Session |
| 413 | `request_too_large` | Body too large |
| 503 | `authentication_unavailable`, `execution_unavailable` | Temporarily uncertain; read state before retrying |

Every response carries `X-Request-Id`; include it when reporting a problem.

### Core extensions: `x_agents_core`

Core runs several harnesses and accepts your own model access. Those settings are the only additions to the OpenAI shapes, and they sit inside `x_agents_core`:

| Field | Where | Value |
| --- | --- | --- |
| `harness` | Agent, or a Session's inline `agent` | `codex`, `claude_sdk` or `mcode` |
| `model_provider` | Agent, or Session creation (top level) | `protocol`, `base_url`, `api_key`, and for `mcode` also `context_window` and `max_output_tokens`. The protocol must be one of the harness's native protocols |
| `harness_config` | Agent, inline `agent`, or Session creation (top level, wins) | The harness's native model parameters, such as Codex's `model_reasoning_effort` |
| `environment` | `openai_hosted` or `self_hosted` Session creation (top level) | Portable preparation: `environment_template_id`, `files`, `env`, `packages`, `setup_commands`, `skills`, `plugins`, `capability_directories`. A field may not also appear in `environment`; see [Environments](../../contracts/agents-api/environments.md#preparation-order) |
| `installation` | Read-only, on `self_hosted` Sessions | Short-lived install commands for your machine; see [self-hosted execution](../getting-started/self-hosted.md) |

Any other member is rejected with 400. `api_key` is write-only: reads return `api_key_configured`. `harness_config` replaces the whole object; `{}` clears it. With the SDK, pass these through `extra_body`.

## Choose a harness and a model

The harness is the agent program that runs a Session: Codex (`codex`), Claude Code (`claude_sdk`) or MiniMax Code (`mcode`). Set `x_agents_core.harness` on the Agent or the inline `agent`; without it, the installation's default harness applies ([`core.default_harness`](../configuration.md#settings), Codex unless the operator changed it).

- **Model.** `model` is the provider's exact model ID. An inline Agent on an `openai_hosted` or `none` Session may omit it to use the default model configuration of its harness. A saved Agent always needs one.
- **Provider.** The harness calls your provider directly, with one of the harness's native protocols; there is no conversion, and a mismatch is rejected when the Session is created. [Model execution](../../contracts/agents-api/model-execution.md#saved-defaults-and-precedence) lists each harness's protocols and which provider a Session uses on each Environment type. A Session freezes its provider at creation.
- **Native parameters.** `harness_config` carries the harness's own model settings; see [native model parameters](../../contracts/agents-api/model-execution.md#native-model-parameters).

Not every combination of harness, placement and operation is supported; the [Harness capabilities](../../contracts/agents-api/harness-capabilities.md) lists them.

## Agents

An Agent is saved configuration. Sessions copy it when they start, so editing an Agent affects only new Sessions.

```python
agent = client.beta.agents.create(
    model="your-model-id",
    name="Reviewer",
    instructions="Review code changes and report problems.",
    extra_body={"x_agents_core": {"harness": "codex"}},
)
```

```sh
oac "/agents" -d '{
  "model": "your-model-id",
  "name": "Reviewer",
  "instructions": "Review code changes and report problems.",
  "x_agents_core": {"harness": "codex"}
}'
```

```json
{"id": "00000000-0000-4000-8000-000000000001", "object": "agent", "model": "your-model-id", "name": "Reviewer",
 "instructions": "...", "tools": [], "metadata": {}, "created_at": 1790000000,
 "x_agents_core": {"harness": "codex"}}
```

| Operation | SDK | HTTP |
| --- | --- | --- |
| Create | `agents.create(...)` | `POST /agents` |
| Read | `agents.retrieve(id)` | `GET /agents/{id}` |
| Update | `agents.update(id, ...)` | `POST /agents/{id}` |
| List | `agents.list()` | `GET /agents` |
| Delete | `agents.delete(id)` | `DELETE /agents/{id}` |

(`agents` is `client.beta.agents` throughout.)

- **Update** changes only the fields you send. `metadata` replaces all pairs; `null` clears `name` or `instructions`.
- **Delete** keeps existing Sessions.
- **Tools** are functions, MCP servers, `tool_search` (Claude) and `web_search` with `mode: "disabled"`. Support depends on harness and Environment; see [execution tools](../../contracts/agents-api/execution-tools.md).

## Sessions

A Session is one conversation with a fixed configuration and its own Environment.

### Create a Session

```python
session = client.beta.agents.sessions.create(
    environment={"type": "openai_hosted"},
    agent_id=agent.id,
    input="Review the files in /workspace and summarize the risks.",
    metadata={"ticket": "T-123"},
)
```

```sh
oac "/agents/sessions" -H "Idempotency-Key: $(uuidgen)" -d '{
  "environment": {"type": "openai_hosted"},
  "agent_id": "00000000-0000-4000-8000-000000000001",
  "input": "Review the files in /workspace and summarize the risks.",
  "metadata": {"ticket": "T-123"}
}'
```

Returns 201 with the Session:

```json
{"id": "00000000-0000-4000-8000-000000000002", "object": "agent.session", "status": "idle",
 "agent": {"model": "...", ...}, "environment": {"id": "env_...", "type": "openai_hosted", ...},
 "metadata": {"ticket": "T-123"}, "required_actions": [], "vault_ids": [],
 "created_at": 1790000000, "last_active_at": 1790000000}
```

| Field | Meaning |
| --- | --- |
| `environment` | Required. Where the agent works; see the table below |
| `agent_id` or `agent` | A saved Agent, or an inline Agent object (same fields as create). An inline Agent on `openai_hosted` or `none` may omit `model` to use the installation default |
| `input` | The first message: a string or a message array. Required on `none`, and with `stream: true` except on `self_hosted` ([initial input](../../contracts/agents-api/sessions-events.md#initial-input-at-session-creation)) |
| `metadata` | Your own string key-value pairs |
| `vault_ids` | [Vaults](#vaults) whose credentials MCP servers may use |
| `stream` | `true` returns [server-sent events](#stream-events) instead of JSON |
| `x_agents_core.model_provider` | This Session's model access, if not from the Agent or the default. Rejected on `none` |

| `environment.type` | Runs on | Notes |
| --- | --- | --- |
| `openai_hosted` | A sandbox Core creates on a node or E2B; the administrator provides the capacity | Optional `network`, `packages`, `files`, `skills`, `plugins`, `env`, `capability_directories`, `setup_commands`, or a template |
| `self_hosted` | Your own Linux, macOS or Windows machine | Requires an absolute `workspace_directory`. Skills, packages, files or a template go in `x_agents_core.environment`. The response carries install commands in `x_agents_core.installation`; see [self-hosted execution](../getting-started/self-hosted.md). The Session brings its own `model_provider` |
| `none` | A device connection an operator registered, with no workspace | `input` required. The model comes from the installation default, or from the device when no default is configured |

A new `openai_hosted` Session reads `idle` while Core prepares its sandbox; its first Turn starts when the Environment is ready. The [Environment contract](../../contracts/agents-api/environments.md) owns placement, expiry and preparation.

### Session status

Read `status`, `error` and `required_actions` to decide whether to send input, return a [function result](#function-tools), connect a machine or diagnose a failure. [Session status](../../contracts/agents-api/sessions-events.md#session-status) defines every state and which failures allow new input.

### Update, list and delete

```python
client.beta.agents.sessions.update(session.id, metadata={"ticket": "T-124"})
for s in client.beta.agents.sessions.list(agent_id=agent.id):
    print(s.id)
client.beta.agents.sessions.delete(session.id)
```

| Operation | HTTP | Notes |
| --- | --- | --- |
| Read | `GET /agents/sessions/{id}` | |
| Update | `POST /agents/sessions/{id}` | Only `metadata` |
| List | `GET /agents/sessions?agent_id=...` | Filter by Agent is optional |
| Delete | `DELETE /agents/sessions/{id}` | Only when `idle` or `failed` with nothing pending; otherwise 409. Cancel first |

## Send input

All input goes to one endpoint as a list of events. It returns 202 after durable admission, before native application. On an idle `openai_hosted` or `self_hosted` Session, a message request can wait up to five minutes for its Turn to start and can end with a 409 expiry, cancellation or Environment error; allow that wait in client timeouts ([Environment input](../../contracts/agents-api/sessions-events.md#sessions-with-an-environment)).

### Send a message

```python
client.beta.agents.sessions.events.create(
    session.id,
    events=[{
        "type": "agent.session.input.message",
        "input": [{"role": "user", "content": [{"type": "input_text", "text": "Now fix the first risk."}]}],
    }],
    idempotency_key=key,
)
```

```sh
oac "/agents/sessions/$SESSION_ID/events" -H "Idempotency-Key: $KEY" -d '{
  "events": [{"type": "agent.session.input.message",
              "input": [{"role": "user", "content": [{"type": "input_text", "text": "Now fix the first risk."}]}]}]
}'
```

- **When idle,** a message starts a new Turn. **While a Turn runs,** it joins that Turn (steering); it does not start a parallel task.
- **Content** is `input_text`, plus `input_image` as an inline PNG or JPEG data URI. Codex and Claude Code accept images; MiniMax Code rejects them. The whole request is limited to 1 MiB.
- Full rules: [message content](../../contracts/agents-api/message-content.md).

### Cancel

```python
client.beta.agents.sessions.events.create(session.id, events=[{"type": "agent.session.input.cancel"}])
```

```sh
oac "/agents/sessions/$SESSION_ID/events" -d '{"events": [{"type": "agent.session.input.cancel"}]}'
```

The Turn is cancelled when it reaches `cancelled`, not when the request returns. A cancel while idle does nothing when no input is pending; a pending Environment input reservation returns 409. A self-hosted machine keeps its workspace and history when you restart the same installation; see [operate the installation](../getting-started/self-hosted.md#operate-the-installation).

## Stream events

`GET /agents/sessions/{id}/events` is a server-sent event stream. It is **live only**: events sent while you were disconnected are not replayed. Open it before sending input, and recover gaps from [Turns and Items](#turns-and-items).

```python
with client.beta.agents.sessions.events.stream(session.id) as stream:
    for event in stream:
        if event.type == "agent.session.turn.output_text.delta":
            print(event.delta, end="", flush=True)
        elif event.type in {"agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled"}:
            break
```

```sh
oac "/agents/sessions/$SESSION_ID/events" -N
```

```text
event: agent.session.turn.output_text.delta
data: {"type": "agent.session.turn.output_text.delta", "item_id": "item_...", "delta": "Hello", ...}
```

| Event | When |
| --- | --- |
| `agent.session.turn.created`, `.in_progress` | A Turn starts |
| `agent.session.turn.item.added`, `.item.done` | An Item (message, tool call, …) starts or finishes |
| `agent.session.turn.output_text.delta`, `.done` | Assistant text, piece by piece |
| `agent.session.turn.completed`, `.failed`, `.cancelled` | A Turn ends. Carries `usage` |
| `agent.session.in_progress`, `.idle`, `.requires_action`, `.failed` | Session status changes |
| `agent.session.subagent.*` | Child work starts or ends |
| `error` | A stream-level error |

The stream stays open across Turns. To stream one Turn and handle [function calls](#function-tools) automatically, the SDK's `sessions.stream` helper does both.

**Stream the creation itself** with `stream=True` on `sessions.create`. You get `agent.session.created` first, and the stream ends at the first `idle` or `failed`.

**Reconnecting:** resubscribe, then read Items and drop any you already have by ID. An open stream closes when its Project key is revoked or its Project archived. Details: [recovery model](../../contracts/agents-api/sessions-events.md#recovery-model).

## Turns and Items

A Turn is one piece of work started by input. Items are its recorded content: messages, reasoning, tool calls and their results. Both are durable; read them to check results or recover after a disconnect.

```python
turns = client.beta.agents.sessions.turns.list(session.id, order="desc")
latest = turns.data[0]
print(latest.status, latest.usage)
for item in client.beta.agents.sessions.items.list(session.id, order="asc"):
    print(item.type)
```

```sh
oac "/agents/sessions/$SESSION_ID/turns?order=desc&limit=1"
oac "/agents/sessions/$SESSION_ID/items?order=asc"
```

| Turn `status` | Meaning |
| --- | --- |
| `queued`, `in_progress` | Not finished |
| `waiting` | Waiting for a function result |
| `completed`, `failed`, `cancelled` | Finished |

- **Usage** (`input_tokens`, `output_tokens`, `total_tokens`, …) is null when unknown, never zero. A Session's usage stays null while a Turn runs; Claude Code and MiniMax Code report none ([usage rules](../../contracts/agents-api/sessions-events.md#usage)).
- Turn lists contain top-level Turns only. Read child work under `/subagents`.

## Function tools

Declare a function on the Agent. When the model calls it, the Session enters `requires_action` and the Turn `waiting` until you return a result.

```python
agent = client.beta.agents.create(
    model="your-model-id",
    tools=[{
        "type": "function",
        "name": "get_weather",
        "description": "Current weather for a city",
        "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]},
    }],
)

def get_weather(args):
    return f"Sunny in {args['city']}"

session = client.beta.agents.sessions.create(
    environment={"type": "openai_hosted"}, agent_id=agent.id,
)

with client.beta.agents.sessions.stream(
    session.id, input="What's the weather in Paris?", tool_handlers={"get_weather": get_weather}
) as stream:
    for event in stream:
        pass  # the helper submits each result and stops when the Turn ends
```

Without the helper, read the call from `session.required_actions`, then send:

```sh
oac "/agents/sessions/$SESSION_ID/events" -d '{
  "events": [{"type": "agent.session.input.tool_result",
              "turn_id": "turn_...", "call_id": "call_...", "success": true, "output": "Sunny in Paris"}]
}'
```

Resending the same result is safe. A different result for the same call, or one sent after cancellation, fails with 409. MiniMax Code doesn't support public functions.

## Files

Three kinds of file serve different purposes:

| Kind | Use it to | Path |
| --- | --- | --- |
| [Source Files](#source-files) | Upload bytes once and reference them by ID | `/files` |
| [Workspace files](#workspace-files) | Put files into a running Session's workspace, or list it | `/agents/environments/{id}/files` |
| [Artifacts](#artifacts) | Download what the agent produced | `/agents/sessions/{id}/artifacts` |

### Source Files

```python
f = client.files.create(file=open("data.csv", "rb"), purpose="user_data")
```

```sh
curl "$OPENAI_BASE_URL/files" -H "Authorization: Bearer $OPENAI_API_KEY" \
  -F purpose=user_data -F file=@data.csv
```

Only `purpose=user_data` is accepted, up to 512 MiB. No Beta header. Their content cannot be downloaded again; use the ID in workspace files or templates.

### Workspace files

Copy a file into the workspace of the Session's Environment (`session.environment.id`):

```python
env_id = session.environment.id
client.beta.agents.environments.files.create(env_id, type="file_id", file_id=f.id, path="/workspace/data.csv")
client.beta.agents.environments.files.create(env_id, type="inline", data="aGVsbG8K", path="/workspace/hello.txt")
page = client.beta.agents.environments.files.list(env_id, path="/workspace")
```

```sh
oac "/agents/environments/$ENV_ID/files" -d '{"type": "file_id", "file_id": "file-...", "path": "/workspace/data.csv"}'
```

- `inline` data is base64, up to 5 MiB decoded; `file_id` up to 50 MiB.
- Parent directories are created. An existing file is never overwritten (400).
- The list shows regular files in one directory, not recursively. It pages with a `page` token and returns `next`.

### Artifacts

When a Turn completes, Core captures the regular files under the workspace's `outputs/` directory. Artifacts stay readable after the Environment is gone.

```python
for a in client.beta.agents.sessions.artifacts.list(session.id):
    data = client.beta.agents.sessions.artifacts.content(a.id, session_id=session.id)
    data.write_to_file(a.path.rsplit("/", 1)[-1])
```

```sh
oac "/agents/sessions/$SESSION_ID/artifacts"
oac "/agents/sessions/$SESSION_ID/artifacts/$ARTIFACT_ID/content" -o report.md
```

Each Artifact has `path`, `size_bytes`, `turn_id` and `environment_id`. Deleting one leaves the workspace file alone.

## Skills

A Skill is a versioned bundle of instructions and files an agent can use. Upload a directory or a ZIP; each upload is a version.

```sh
curl "$OPENAI_BASE_URL/skills" -H "Authorization: Bearer $OPENAI_API_KEY" -F files=@my-skill.zip
```

```python
skill = client.skills.create(files=[("my-skill/SKILL.md", open("my-skill/SKILL.md", "rb"))])
client.skills.versions.create(skill.id, files=[...], default=True)
```

- No Beta header. Select up to 50 Skills per Environment; each archive is at most 5 MiB compressed and 20 MiB expanded.
- SDK 3.13.0 drops a single ZIP file from the upload; use HTTP for a ZIP.
- Attach Skills to a Session through its `environment.skills` or a [template](#environment-templates).

Details: [Files and Skills](../../contracts/agents-api/source-files.md). A Session installs its Skills, Plugins and packages once, when it is prepared; editing the source later doesn't change a running Session. Preparation errors fail the Session before any work runs: fix the cause instead of retrying in a new Session.

## Environment Templates

A template saves workspace setup for reuse. `openai_hosted` Sessions reference it in `environment`; `self_hosted` Sessions in `x_agents_core.environment`:

```python
template = client.beta.agents.environments.templates.create(
    name="python-data",
    packages={"python": ["pandas"]},
    setup_commands=[{"command": "mkdir -p /workspace/outputs"}],
)
```

| Field | Meaning |
| --- | --- |
| `network` | `access`: `enabled` (default), `disabled`, or `restricted` to 1–100 exact hosts in `allowed_domains`. A Session can only narrow it. See execution limits in [restricted network policy](../../contracts/agents-api/environments.md#restricted-network) |
| `packages` | Package setup; see [package admission](../../contracts/agents-api/environments.md#preparation-order) |
| `setup_commands`, `env` | Run and set at preparation. Never returned by reads |
| `files`, `skills`, `plugins` | Initial content. Up to 50 files, 10 MiB inline in total |

A Session freezes the template when it starts. Details: [Environment Templates](../../contracts/agents-api/environments.md#templates).

## Vaults

Vaults hold credentials for HTTP MCP servers: a `static_bearer` token or an `mcp_oauth` token with optional refresh. Tokens are write-only.

```python
vault = client.beta.agents.vaults.create(name="github")
client.beta.agents.vaults.credentials.create(
    vault.id, name="github-token",
    auth={"type": "static_bearer", "token": "ghp_...", "mcp_server_url": "https://api.githubcopilot.com/mcp/"},
)
session = client.beta.agents.sessions.create(environment={"type": "none"}, input="...", vault_ids=[vault.id], agent_id=agent.id)
```

The HTTP path is `/vaults`, with the Beta header. A Session selects credentials from its `vault_ids`, optionally by `credential_id`; the MCP server's URL must match the selected credential's `mcp_server_url` exactly in either case. The [Vaults contract](../../contracts/agents-api/vaults.md) owns selection, errors, OAuth refresh and deletion. An MCP tool's `connection_origin` decides whether Core's side or the workspace connects to the server, and each harness supports a different set: see [MCP connection origin](../../contracts/agents-api/environments.md#public-mcp-connection-origin).

## Diagnose a failure

1. Read the Session's `status` and `error`, and the latest Turn's `error`. A failed Turn reports only a generic `internal_error`.
2. Check that the Environment is connected and its harness is available.
3. Check the harness, model and tool combination in [Harness capabilities](../../contracts/agents-api/harness-capabilities.md).
4. Ask the administrator for the Session's [diagnostics](../../contracts/agents-api/session-diagnostics.md), which name the failure category, and to check [troubleshooting](../getting-started/operations.md#troubleshooting) for service logs, credentials and node readiness.

A 401 usually means a key from another namespace; see [API namespaces and credentials](./index.md).

## Differences from OpenAI

Core differs from the OpenAI service in some behavior, such as Session creation idempotency and harness-specific tool support. The [coverage ledger](../../contracts/agents-api/index.md#differences-from-openai) lists every difference and the per-resource status; the [public OpenAPI](../../contracts/agents-api/openapi.yaml) has the exact schemas.
