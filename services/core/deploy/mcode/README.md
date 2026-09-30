# MiniMax Code Runtime

The MiniMax Code adapter ([`agent/mcode`](../../../../apps/daemon/internal/agent/mcode)) runs the native MiniMax Code CLI over ACP inside the daemon. MiniMax Code keeps its own ACP Session, model loop and history. The companion package [`packages/mcode-harness`](../../../../packages/mcode-harness/README.md) owns the workspace tool bridge, the native patch and the Subagent history reader. This page holds the Runtime-level adapter rules and the MiniMax Code Runtime image. [Harness onboarding](../../../../contracts/agents-api/harness-onboarding.md) owns the obligations shared by all adapters.

Native tools run with the daemon user's permissions; the outer sandbox provides isolation ([Runtime and outer isolation](../../../../docs/concepts.md#runtime-and-outer-isolation)).

## Native pin and readiness

The adapter accepts only `@minimax-ai/code` `0.4.12` ([`version.go`](../../../../apps/daemon/internal/agent/mcode/version.go)), built from the upstream source revision in [`source.json`](../../../../packages/mcode-harness/source.json) and run with Node.js 22. The daemon advertises MiniMax execution only when `OAC_RUNTIME_MCODE_AGENTS_API=1` and the version matches ([`execution.go`](../../../../apps/daemon/internal/agent/mcode/execution.go)); the native installer and the Runtime image set it. MiniMax Code runs on Linux and macOS.

Workspace execution also requires the companion's readiness report: private protocol 2, the pinned native version and the pinned source revision ([`workspace_readiness.go`](../../../../apps/daemon/internal/agent/mcode/workspace_readiness.go)). An older companion is rejected even when the upstream version matches.

## Model provider

MiniMax Code accepts the `anthropic`, `responses` and `chat_completions` protocols, and each requires the provider's context window and maximum output tokens ([`harnessconfig/mcode`](../../../../internal/harnessconfig/mcode/configuration.go)). The adapter writes the frozen bundle as the native custom provider for the Session's exact `agent.model`, with those limits, using the native `anthropic-messages`, `openai-responses` or `openai-completions` API ([`model_provider.go`](../../../../apps/daemon/internal/agent/mcode/model_provider.go)). It selects that provider explicitly; there is no native account fallback. [Model execution](../../../../contracts/agents-api/model-execution.md#deployment-defaults) owns provider selection.

## Execution profiles

Each Session has its own native data directory, `$OAC_RUNTIME_HOME/runtime/mcode/state/<state key>/` (`OAC_RUNTIME_HOME` defaults to `~/.oac`), holding the generated native configuration, instructions (`AGENTS.md`, at most 32 KiB), MCP configuration and history. The native process gets `MINIMAX_DATA_DIR`, `HOME` and `USERPROFILE` set to that directory on top of the daemon user's environment.

| Profile | Where it runs | Native configuration |
| --- | --- | --- |
| Text (`environment: none`) | A private working directory inside the Session's data directory | No native tools, Skills, web search, browser tools, `mcode-tools`, thread goals or user questions. With Subagents enabled, the default agent gets only the task tools (`task`, `task_append`, `task_query`, `task_output`, `task_stop`) and delegation |
| Workspace | The Environment's workspace directory, for the native process, the ACP Session and the workspace tools | The text configuration plus permission mode `bypassPermissions`, the native sandbox off, the selected workspace Skills and the `oac_workspace` MCP bridge for file and shell tools |

The text profile rejects workspace, function tools and MCP servers. Workspace execution requires Linux or macOS and an unrestricted Environment network policy ([`workspace.go`](../../../../apps/daemon/internal/agent/mcode/workspace.go)); the adapter enforces no network restriction.

In the workspace profile, the frozen installation's Skills are linked into the Session's `skills` directory under their exact names and loaded through the native `skill` tool; external Skill discovery stays off. Public MCP servers use the Environment origin only, as the [MCP origin contract](../../../../contracts/agents-api/environments.md#public-mcp-connection-origin) describes. With Subagents enabled, the Session's native `mcp.json` holds only the `oac_workspace` server, so child agents get the same workspace tools.

## Turns, cancellation and continuation

- Normal Turns reuse one ACP connection and native Session.
- Active input uses ACP `mcode/session/steer`. Its receipt confirms that the active native Turn accepted the input, not that the model consumed it. Unknown outcomes are never replayed.
- Public input stays a model message: the adapter adds an empty text block so that ACP does not treat slash text as an operator command.
- A Turn that ends with an error, a cancellation, an unknown input outcome, an unfinished tool call or an unanswered permission or question makes the Executor non-reusable. The adapter stops the native process group and waits for it to exit before the Turn settles; later work loads the same native history in a new Executor.
- Continuation requires the exact recorded native Session ID and the same working directory. Recovery without a recorded ID is rejected.
- MiniMax Code reports no usage: ACP context occupancy and cumulative cost are not per-Turn usage.
- Workspace `workspace_bash` calls become command observations with the command text, output text and status; exit code and duration stay unknown. Native task and Skill utilities produce no public Items.
- The adapter attaches no [native failure classification](../../../../docs/runtime-protocol.md#native-failure-classification) to its errors.

## Runtime image

[`Dockerfile`](Dockerfile) builds the MiniMax Code Runtime image from a prepared context that holds `oac-daemon` and the companion artifact; the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds both.

| Item | Value |
| --- | --- |
| Base | Digest-pinned `node:22.23.1-bookworm-slim` with `ca-certificates`, `bash`, `git`, `python3`, `python3-pip` and `ripgrep` |
| Programs | `/usr/local/bin/oac-daemon` and the companion at `/opt/mcode-harness` (native CLI at `native/cli.js`, bridge at `bridge.mjs`) |
| User | UID/GID 1000 with `HOME=/home/runtime` |
| Environment | `OAC_RUNTIME_HOME=/home/runtime/.oac`, `OAC_RUNTIME_MCODE_NODE`, `OAC_RUNTIME_MCODE_BIN`, `OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE`, `OAC_RUNTIME_MCODE_AGENTS_API=1`, `OAC_RUNTIME_WORKSPACE=/environment/workspace`, `OAC_RUNTIME_INITIALIZATION_DIRECTORY=/environment/initialization`, `OAC_RUNTIME_PACKAGE_DIRECTORY=/environment/packages` |
| Entry point | `oac-daemon connect --profile default`, working directory `/environment/workspace` |

The build runs the companion's `check.mjs` and the native `--version`. The combined Runtime image uses this image as its base. Sandboxes run it with the [Docker sandbox settings](../../../../docs/sandbox-provider.md#docker-adapter).
