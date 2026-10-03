# Codex Runtime

The Codex adapter ([`agent/codex`](../../../../apps/daemon/internal/agent/codex)) runs the native Codex CLI through its app-server protocol inside the daemon. This page holds the Codex-specific adapter rules, the Codex Runtime image and the Docker sandbox settings that every Docker Runtime uses. [Harness onboarding](../../../../contracts/agents-api/harness-onboarding.md) owns the obligations shared by all adapters.

Native tools run with the daemon user's permissions; the outer sandbox provides isolation ([Runtime and outer isolation](../../../../docs/concepts.md#runtime-and-outer-isolation)).

## Native pin

The adapter accepts only Codex `0.153.4`: installation and recovery checks require `codex --version` to report `codex-cli 0.153.4` ([`installation.go`](../../../../apps/daemon/internal/agent/codex/installation.go), [`recovery.go`](../../../../apps/daemon/internal/agent/codex/recovery.go)). The Runtime image carries the official Linux amd64 package of that version with its matching `codex-resources`; the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds it.

## Model provider and native state

Codex accepts only the `responses` model protocol ([`harnessconfig/codex`](../../../../internal/harnessconfig/codex/configuration.go)). A Chat Completions or Anthropic provider is rejected; nothing converts between protocols. [Model execution](../../../../contracts/agents-api/model-execution.md#deployment-defaults) owns provider selection, including the per-Harness deployment default.

Each Session has its own `CODEX_HOME` at `$OAC_RUNTIME_HOME/daemon/agent-sessions/<state key>/` (`OAC_RUNTIME_HOME` defaults to `~/.oac`). Native history stays there. The adapter regenerates that directory's `config.toml` on every prompt: the Session's frozen provider bundle becomes the `[model_providers.oac]` block, with `wire_api` `responses`, and the thread is pinned to that provider, so Codex never falls back to its built-in `openai` provider.

## Execution controls

The adapter applies Core's typed controls and opt-ins, described in the [Runtime protocol](../../../../docs/runtime-protocol.md#capability-declarations), through native Codex settings on both new and resumed Turns. Explicit controls take precedence over adapter options without changing them.

- **`environment: none`.** The daemon forces `CODEX_EXEC_SERVER_URL=none` after the caller's environment options and confirms that Codex reports its `local` and `remote` environments as unknown before it starts or resumes a thread; a binary that cannot do this fails closed. The engine process runs on the bound device, which is not a user execution environment.
- **Web search.** The control becomes Codex's `web_search` option (`disabled`, `cached` or `live`); Core sends `disabled`.
- **Programmatic tool calling.** An explicit disable turns off the native `code_mode`, `code_mode_only` and `code_mode_prewarm` features and checks managed requirements before a thread starts or resumes, rejecting a conflicting requirement.
- **Text verbosity.** The adapter reads the model catalog with `codex debug models`, checks the model's support and pins that catalog snapshot for the execution. The probe needs Unix process-group cancellation, so other hosts do not declare `text_verbosity`. For a model without declared verbosity support, including Codex's unknown-model fallback, `medium` omits the override and keeps native default text; a supported model receives an explicit `medium`. Unsupported `low` or `high` and an unreadable catalog fail before model execution.
- **Subagents.** Disabling Subagents turns off both native multi-agent feature generations, `multi_agent` and `multi_agent_v2`, overriding the operator's feature preferences.

## MCP servers

A typed MCP declaration replaces operator MCP options. The adapter renders it with the native renderer and the original tool names for `enabled_tools`, including `[]`. Before creating or resuming a thread it reads native `config/read` with the exact cwd and rejects additional servers or any difference in the effective configuration. It disables native plugins and apps, selects file-only MCP credentials, and rejects existing credentials in the private native home without deleting them or native history. Reserved native labels are an adapter restriction, not a rule of the saved resource. The check is a snapshot, not a barrier against concurrent operator configuration changes, and discovery of a deny-all server can still contact it.

With `required: true`, which needs `mcp_http_required`, root thread creation and cold resume wait for required servers to initialize and send no native Turn until they do; a failed strict resume is never replaced with a new thread. Public work may already be accepted while initialization waits.

A selected bearer credential (`mcp_http_bearer_auth`) becomes a fresh daemon-owned `bearer_token_env_var` reference for each server and native process. The secret enters only that app-server child's environment, after auxiliary launch probes, and never global environment, arguments, configuration, history, snapshots or logs. Preflight accepts only the expected server and reference pairing and keeps rejecting ambient credential sources. Empty values and bytes outside RFC 6750 `b64token` syntax are rejected with generic errors, and tokens are never trimmed. Core-managed OAuth delivers access tokens through the same path.

## Function results and command output

A function result counts as applied only when Codex reports a matching live dynamic-tool completion: root thread, Turn and call identity, function, success and ordered content. Writing the JSON-RPC response is not application. The adapter owns pending receipts without holding their lock across I/O or waits; terminal state, cancellation and native loss settle unconfirmed submissions before release, and an uncertain receipt timeout ends that native execution without resending the result. It records native confirmation before any potentially blocking observation publication, so output backpressure cannot turn a known application into an unknown one.

With neutral tool observations, the adapter emits `command_output` fragments carrying the native command identity, filtered to the root thread and Turn. Codex `0.153.4` can miss early process output in both its notifications and its final aggregate; that output is lost, and Core never reconstructs it from model text.

## Native failure classification

The adapter classifies a failed Turn only from the exact root terminal Turn's `codexErrorInfo` ([`error_classification.go`](../../../../apps/daemon/internal/agent/codex/error_classification.go)); error notifications, including retry notifications, never classify it. The [Runtime protocol](../../../../docs/runtime-protocol.md#native-failure-classification) defines the codes.

| `codexErrorInfo` | Code |
| --- | --- |
| `unauthorized` | `authentication_error` |
| `usageLimitExceeded` | `usage_limit_exceeded` |
| `rateLimitExceeded` | `rate_limit_exceeded` |
| `contextWindowExceeded` | `context_length_exceeded` |
| `serverOverloaded` | `server_overloaded` |
| `internalServerError` | `server_error` |
| `badRequest` | `invalid_request` |
| `cyberPolicy` | `cyber_policy` |
| `httpConnectionFailed`, `responseStreamConnectionFailed`, `responseStreamDisconnected` | `connection_failed`, with the upstream `httpStatusCode` |
| `responseTooManyFailedAttempts` | `rate_limit_exceeded` when `httpStatusCode` is 429, otherwise `connection_failed` |

Every other variant stays unclassified.

## Runtime image

[`Dockerfile`](Dockerfile) builds the Codex Runtime image from a prepared context that holds only `oac-daemon`, the unmodified `codex` executable and `codex-resources`.

| Item | Value |
| --- | --- |
| Base | Digest-pinned `node:22.23.1-bookworm-slim` with `ca-certificates`, `bash`, `git`, `python3`, `python3-pip` and `ripgrep`, the same base and package layer as the Claude and MiniMax images |
| Programs | `/usr/local/bin/oac-daemon`, `/usr/local/bin/codex` (mode 0555) and `/usr/local/codex-resources` |
| User | UID/GID 1000 with `HOME=/home/runtime` |
| Environment | `OAC_RUNTIME_HOME=/home/runtime/.oac`, `OAC_RUNTIME_CODEX_BIN=/usr/local/bin/codex`, `OAC_RUNTIME_WORKSPACE=/environment/workspace`, `OAC_RUNTIME_INITIALIZATION_DIRECTORY=/environment/initialization`, `OAC_RUNTIME_PACKAGE_DIRECTORY=/environment/packages` |
| Entry point | `oac-daemon connect --profile default`, working directory `/environment/workspace` |

The build fails unless `codex --version` reports the pinned version. The image holds no credentials, workspace data or product software. The distribution copies the Codex executable and resources into the combined Runtime image; see the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers).

The [Docker adapter](../../../../docs/sandbox-provider.md#docker-adapter) owns the shared container lifecycle and isolation settings.
