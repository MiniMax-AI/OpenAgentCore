# Claude Code Runtime

The Claude adapter runs Claude Code through the pinned Claude Agent SDK. The SDK owns the model and tool loop. Two parts make up the adapter: the private TypeScript bridge in [`packages/claude-sdk-adapter`](../../../../packages/claude-sdk-adapter/README.md), which owns the bridge protocol and native SDK configuration, and the Go adapter in [`agent/claudesdk`](../../../../apps/daemon/internal/agent/claudesdk), which owns the bridge process. This page holds the Runtime-level rules and the Claude Runtime image. [Harness onboarding](../../../../contracts/agents-api/harness-onboarding.md) owns the obligations shared by all adapters.

Native tools run with the daemon user's permissions; the outer sandbox provides isolation ([Runtime and outer isolation](../../../../docs/concepts.md#runtime-and-outer-isolation)).

## Native pin and readiness

The bundle pins Claude Agent SDK `0.3.269` ([`package.json`](../../../../packages/claude-sdk-adapter/package.json)), which reports native Claude Code `2.1.269`. The bridge uses protocol 3 for a prepared Executor with separately identified Turns; the Go adapter's readiness and Executor checks reject any other protocol version ([`readiness.go`](../../../../apps/daemon/internal/agent/claudesdk/readiness.go), [`executor.go`](../../../../apps/daemon/internal/agent/claudesdk/executor.go)).

Workspace execution requires the bundle to report the `workspace_tools`, `workspace_prepare`, `workspace_command_observations` and `local_runtime_v2` features; matching SDK versions alone do not establish compatibility. Function tools in workspace execution additionally require `workspace_functions`. The native installer rejects a bundle that lacks the workspace features ([`installation.go`](../../../../apps/daemon/internal/agent/claudesdk/installation.go)).

Claude accepts only the `anthropic` model protocol ([`harnessconfig/claudesdk`](../../../../internal/harnessconfig/claudesdk/configuration.go)). [Model execution](../../../../contracts/agents-api/model-execution.md#deployment-defaults) owns provider selection.

## Native state and recovery

With a workspace binding, the SDK's history, home and scratch directories are `history`, `home` and `scratch` under `$OAC_RUNTIME_HOME/runtime/claude-sdk/` (mode 0700; `OAC_RUNTIME_HOME` defaults to `~/.oac`). The daemon's authentication stays under `$OAC_RUNTIME_HOME/daemon/`. These locations keep Session state apart; they do not restrict the tools.

Recovery uses the SDK's history APIs ([`recovery.ts`](../../../../packages/claude-sdk-adapter/src/recovery.ts)). An explicitly supplied native Session ID must exist. When Core requires existing history but has no recorded ID, the adapter accepts only a single native Session whose recorded cwd equals the bound workspace and which has at least one message. Missing, foreign, ambiguous or empty history is rejected before any model input.

## Native failure classification

The bridge ([`native_failure.ts`](../../../../packages/claude-sdk-adapter/src/native_failure.ts)) classifies a failure only from root assistant messages (no parent tool use) of the same native Session that answer Core-submitted inputs not yet completed; replayed and synthetic messages are ignored. A classification is committed only when the matching native result is an error; a successful result clears it. The [Runtime protocol](../../../../docs/runtime-protocol.md#native-failure-classification) defines the codes.

| SDK assistant error | Code |
| --- | --- |
| `authentication_failed`, `oauth_org_not_allowed`, `account_on_hold`, `verification_required`, `cloud_credential_error` | `authentication_error` |
| `billing_error` | `usage_limit_exceeded` |
| `rate_limit` | `rate_limit_exceeded` |
| `overloaded` | `server_overloaded` |
| `invalid_request` | `invalid_request` |
| `model_not_found` | `resource_not_found` |
| `server_error` | `server_error` |

`unknown`, `max_output_tokens` and other results stay unclassified. The bridge drops the classification when any other error ends the Turn, when cleanup fails or when no native process ran. The Go adapter keeps it only for the reported failed result of the same native Session, and only after it received the Turn settlement ([`executor_turn.go`](../../../../apps/daemon/internal/agent/claudesdk/executor_turn.go)).

## Runtime image

[`Dockerfile`](Dockerfile) builds the Claude Runtime image from a prepared context that holds only `oac-daemon` and the exported SDK bundle; the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds it. Keep the exported bundle unchanged.

| Item | Value |
| --- | --- |
| Base | Digest-pinned `node:22.23.1-bookworm-slim` with `ca-certificates`, `bash`, `git`, `python3`, `python3-pip` and `ripgrep` |
| Programs | `/usr/local/bin/oac-daemon` and the SDK bundle at `/opt/claude-sdk` |
| User | UID/GID 1000 with `HOME=/home/runtime` |
| Environment | `OAC_RUNTIME_HOME=/home/runtime/.oac`, `OAC_RUNTIME_CLAUDE_SDK_NODE=/usr/local/bin/node`, `OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT=/opt/claude-sdk/dist/main.js`, `OAC_RUNTIME_WORKSPACE=/environment/workspace`, `OAC_RUNTIME_INITIALIZATION_DIRECTORY=/environment/initialization`, `OAC_RUNTIME_PACKAGE_DIRECTORY=/environment/packages` |
| Entry point | `oac-daemon connect --profile default`, working directory `/environment/workspace` |

The build runs the bundle's `runtime_check.js` against its entry point. The distribution copies `/opt/claude-sdk` into the combined Runtime image. Sandboxes run the image with the [Docker sandbox settings](../../../../docs/sandbox-provider.md#docker-adapter).
