# MiniMax Code Runtime

The MiniMax Code adapter ([`agent/mcode`](../../../../apps/daemon/internal/agent/mcode)) runs the native MiniMax Code CLI over ACP inside the daemon. MiniMax Code keeps its own ACP Session, model loop and history. The companion package [`packages/mcode-harness`](../../../../packages/mcode-harness/README.md) owns the native patch and the Subagent history reader. This page holds the Runtime-level adapter rules and the MiniMax Code Runtime image. [Harness protocol](../../../../contracts/agents-api/harness-onboarding.md) owns the obligations shared by all adapters.

Native tools run with the daemon user's permissions; the outer sandbox provides isolation ([Runtime and outer isolation](../../../../docs/concepts.md#runtime-and-outer-isolation)). The native data directory, including the generated configuration with the provider credential, is readable by those tools, as for [Claude Code](../../../../contracts/agents-api/model-execution.md#session-override).

## Native pin and readiness

The adapter accepts only `@minimax-ai/code` `0.4.12` ([`version.go`](../../../../apps/daemon/internal/agent/mcode/version.go)), built from the upstream source revision in [`source.json`](../../../../packages/mcode-harness/source.json) and run with Node.js 22. MiniMax Code runs on Linux and macOS.

Workspace execution also requires the companion's readiness report: private protocol 3, the pinned native version and the pinned source revision ([`workspace_readiness.go`](../../../../apps/daemon/internal/agent/mcode/workspace_readiness.go)). An older companion is rejected even when the upstream version matches.

## Model provider

MiniMax Code accepts the `anthropic`, `responses` and `chat_completions` protocols, and the provider's context window and maximum output tokens are optional ([`harnessconfig/mcode`](../../../../internal/harnessconfig/mcode/declaration.go)). The adapter writes the frozen bundle as the native custom provider for the Session's exact `agent.model`, with the limits that are set and, when the Agent sets `reasoning.effort`, that effort as the model's only thinking option (the adapter then selects it with ACP `thinkingEffort`), using the native `anthropic-messages`, `openai-responses` or `openai-completions` API ([`model_provider.go`](../../../../apps/daemon/internal/agent/mcode/model_provider.go)). It selects that provider explicitly; there is no native account fallback. A limit that is not set is omitted from the native configuration, so MiniMax Code applies its own defaults of 200000 context tokens and 16384 output tokens. [Model execution](../../../../contracts/agents-api/model-execution.md#deployment-defaults) owns provider selection.

## Execution profiles

Each Session has its own native data directory, `$OAC_RUNTIME_HOME/runtime/mcode/state/<state key>/` (`OAC_RUNTIME_HOME` defaults to `~/.oac`), holding the generated native configuration, instructions (`AGENTS.md`, at most 32 KiB), MCP configuration and history. The native process gets `MINIMAX_DATA_DIR`, `HOME` and `USERPROFILE` set to that directory on top of the daemon user's environment. This directory remains private to the compute instance: the adapter does not declare retained native history across compute replacement.

| Profile | Where it runs | Native configuration |
| --- | --- | --- |
| Text (`environment: none`) | A private working directory inside the Session's data directory | No native file, process or fetch tools, web search, `mcode-tools`, thread goals or user questions. The native task controls (`task_query`, `task_output`, `task_stop`) are always present; with Subagents enabled, the default agent also gets delegation. Permission mode `bypassPermissions`, so MCP tools run without approval prompts |
| Workspace | The Environment's workspace directory, for the native process, the ACP Session and the native tools | The native `read`, `write`, `edit`, `bash`, `grep`, `glob`, `todowrite` and `web_fetch` tools, permission mode `bypassPermissions`, the selected workspace Skills and the frozen tool environment in the native process environment |

The text profile rejects workspace tools; it accepts service-origin HTTP MCP servers, which the adapter passes in the ACP `mcpServers` of the native Session. In both profiles public functions run as native MCP tools: the daemon serves them from a loopback HTTP MCP server named `functions` that exists for the Session, and passes it in `mcpServers` with a per-Session bearer token. Native fails a call after 120 s ([difference](../../../../contracts/agents-api/index.md#differences-from-openai)). Functions and `multi_agent` cannot be combined. Workspace execution requires Linux or macOS ([`workspace.go`](../../../../apps/daemon/internal/agent/mcode/workspace.go)).

In the workspace profile, the frozen installation's Skills are linked into the Session's `skills` directory under their exact names and loaded through the native `skill` tool. Native discovery also reads the workspace's `.minimax`, `.claude` and `.agents` Skills, as a local run does. Public MCP servers use the Environment origin on a workspace and the service origin on the text profile, as the [MCP origin contract](../../../../contracts/agents-api/environments.md#public-mcp-connection-origin) describes. With Subagents enabled, child agents use the same native tools. In both profiles the adapter turns off what needs a MiniMax account or outlives a Turn or asks the user: `askUser`, `beta.mcodeTools`, `beta.threadGoal`, `features.mavis` (its tool carries cron, session send, agent create and MCP create) and `features.webSearch`, and it leaves `website_deploy` out of `tools`. `web_search: disabled` also removes `web_fetch`. Background Bash is off: the patched CLI gates it on `OAC_RUNTIME_MCODE_BACKGROUND_BASH=off`, and the adapter rejects a CLI that does not report it off.

## Turns, cancellation and continuation

- Normal Turns reuse one ACP connection and native Session.
- Active input uses ACP `mcode/session/steer`. Its receipt confirms that the active native Turn accepted the input, not that the model consumed it. Unknown outcomes are never replayed.
- Public input stays a model message: ACP joins text blocks with a blank line, so the adapter sends one block, and adds an empty second block only when the text starts with `/`, so that ACP does not treat slash text as an operator command.
- A Turn that ends with an error, a cancellation, an unknown input outcome, an unfinished tool call or an unanswered permission or question makes the Executor non-reusable. The adapter stops the native process group and waits for it to exit before the Turn settles; later work loads the same native history in a new Executor.
- Continuation requires the exact recorded native Session ID and the same working directory. Recovery without a recorded ID is rejected.
- MiniMax Code reports no usage: ACP context occupancy and cumulative cost are not per-Turn usage.
- Native `bash` calls become command observations with the command, the Session's workspace as the working directory, the output and status; the exit code is reported only when the native result carries one, and duration stays unknown. Native `web_fetch` calls become `web_search` observations with an `open_page` action. Native task and Skill utilities produce no public Items.
- The adapter attaches no [native failure classification](../../../../docs/runtime-protocol.md#native-failure-classification) to its errors: MiniMax Code `0.4.12` reports a model provider rejection over ACP as JSON-RPC internal error `-32603` whose only detail is prose, so these failures stay unclassified.
- MiniMax Code `0.4.12` retries a failed model request up to 5 times and stops retrying after 120 seconds, so a model host that does not resolve fails the Turn, unclassified, in about two minutes. Its retries stay inside MiniMax Code and never reach ACP.

## Runtime image

[`Dockerfile`](Dockerfile) builds the MiniMax Code Runtime image from a prepared context that holds `oac-daemon` and the companion artifact; the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds both.

| Item | Value |
| --- | --- |
| Base | The [shared Runtime base](../../../../docs/maintainers.md#runtime-images-and-helpers) |
| Programs | `/usr/local/bin/oac-daemon` and the companion at `/opt/mcode-harness` (native CLI at `native/cli.js`) |
| User | UID/GID 1000 with `HOME=/home/runtime` |
| Environment | `OAC_RUNTIME_HOME=/home/runtime/.oac`, `OAC_RUNTIME_MCODE_NODE`, `OAC_RUNTIME_MCODE_BIN`, `OAC_RUNTIME_MCODE_COMPANION`, `OAC_RUNTIME_INITIALIZATION_DIRECTORY=/environment/initialization`, `OAC_RUNTIME_PACKAGE_DIRECTORY=/environment/packages`, `OAC_RUNTIME_STATE_DIRECTORY=/environment/runtime-state` |
| Entry point | `oac-daemon connect --profile default`, working directory `/environment/workspace` |

The build runs the companion's `check.mjs` and the native `--version`. The combined Runtime image uses this image as its base. Sandboxes run it with the [Docker sandbox settings](../../../../docs/sandbox-provider.md#docker-adapter).
