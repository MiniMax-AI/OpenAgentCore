# MiniMax Code workspace bridge

This companion package lets the agent host run MiniMax Code with OpenAgentCore's workspace. MiniMax Code keeps its own ACP Session, model loop and history. The package supplies a trusted MCP server (`bridge.mjs`), which the daemon registers as `oac_workspace` (its MCP server info names it `oac-workspace`). It exposes six native MiniMax Code tools rooted at the Session's workspace: `workspace_read`, `workspace_write`, `workspace_edit`, `workspace_bash`, `workspace_grep` and `workspace_glob`. The bridge and its tools run beside the CLI in the Session's agent-host view, where Bash, `rg` and `git` run in the sandbox; the package adds no inner sandbox. The [maintainer guide](../../docs/maintainers.md#runtime-images-and-helpers) owns the agent-host image build; [Harness qualification](../../contracts/agents-api/harness-onboarding.md#qualify-the-view) owns deployment evidence.

One patch script (`patch-native.mjs`) patches the pinned native CLI source. Native Skill refresh reads at most four independent files per root concurrently, retaining the upstream identity checks, ordered diagnostics, cache keys and precedence. The SQLite task-admission transaction enforces the daemon's Subagent concurrency limit before child work starts; foreground, background, nested and idle-child append admissions share that transaction, and terminal native tasks release capacity. ACP initialization reports `oac/subagents` metadata: its version, the applied workspace tool policy and the admission limit. The native tool catalog applies the `protected-mcp-v1` tool gate described under [Subagents and cancellation](#subagents-and-cancellation). Under the same policy, the CLI ignores the workspace's project `.mcp.json`, so the Session's MCP comes only from the daemon. No second model or scheduling loop is introduced. Hosted public execution is not qualified by this package alone.

## Workspace tools

The native process, its ACP Session and the workspace tools share the Session's workspace as their working directory; native configuration, Skills and history stay in the private Session data directory. Builtin file tools are disabled, so project files are read and written through the bridge's tools. Native Session creation and loading connect the required workspace bridge and validate its tool inventory before preparation succeeds, using the existing Session-scoped connection pool and its normal request timeout. The adapter’s ACP request and Core preparation deadlines continue to bound startup. Turn discovery retains its existing shorter timeout. An unavailable or incomplete bridge fails preparation; optional MCP servers retain native lazy discovery. The expected inventory is generated from the worker's `--describe` output. Only the adapter registers this bridge; callers cannot supply its command, profile, working directory or environment. Native diff/undo capture is not provided by this path. Common Files and Artifacts use the same bound workspace.

The bridge reads the Session's private profile (`workspace-profile.json`, written by the daemon) during initialization. Its tool executor checks that `workspace` is a canonical absolute path and creates the `scratch` directory before accepting calls. Each call starts the worker directly in that workspace with the bridge's environment. An invalid profile rejects bridge initialization.

## Build

`source.json` owns the upstream repository and revision for the CLI and native tools; its version is generated from the [Harness catalog](../../contracts/agents-api/harness-onboarding.md#native-version-pins). The pinned npm package supplies native runtime dependencies; the CLI is built from source into the same artifact. On Linux x86_64 or macOS arm64:

```sh
MCODE_NATIVE_SOURCE=/absolute/upstream/checkout MCODE_CLI_DIR=/absolute/pinned/package bash scripts/build-mcode-harness.sh
```

This standalone companion uses its own npm lock and is excluded from the root pnpm workspace. The build archives the exact source revision, bundles its native tools and installs pinned MCP dependencies. The build verifies tool guidance with the actual patched native prompt renderer and templates before compiling the CLI with its upstream build script and lockfile; the pinned package supplies only native runtime dependencies. `native-patch.json` in the artifact records the upstream revision and exact patch hashes, and `provenance.json` the hash of every artifact file. The artifact lands in `MCODE_HARNESS_BUILD_DIR`, or a new `${OAC_DEV_HOME:-$HOME/.oac}/build/mcode-harness-<timestamp>` directory. Runtime packaging uses this single CLI artifact and installs it immutably at `/opt/mcode-harness`.

## Subagents and cancellation

`subagent-snapshot.mjs` is a daemon-only reader of the private native SQLite history. It opens a read-only transaction, scopes recursive descendants to the bound root, and fails explicitly if a complete snapshot exceeds its bounds. It never executes a model or exposes native history to workspace tools. The adapter freezes root output and keeps the same ACP owner for bounded child settlement. A completed or idle task remains an active public Subagent; native abort is a Turn cancellation and never implies a closed Subagent.

The bridge owns each worker until exit. MCP cancellation and transport shutdown stop all owned workers before releasing the bridge. The outer Runtime owns the native process group. Both boundaries require real Docker cancellation tests.

For declared stdio MCP calls, the adapter captures the affected server identities before sending cancellation and retains identities from callbacks received while cancellation drains. A local failure remains unconfirmed within its Turn even if native reports the tool as finished. The native tool wrapper sets the private `details.oac_response_received` field only for a result returned by the MCP SDK, including a server's `isError` reply; the adapter validates the result identity before releasing that call's owner. It waits for the Runtime to close every selected server's process scope, including background descendants, then calls the private `oac/session/mcp/disconnect` ACP extension with the Session ID and those server names. The native owner disconnects only the selected Session connections and waits for their old transports' actual close events. Configurations remain installed for lazy reconnection on a later call; other MCP servers and the workspace bridge keep their connections. HTTP servers, unknown names and `oac_workspace` are rejected by this control operation. ACP initialization advertises `oac/mcp-lifecycle` version 3, which the adapter requires for a workspace or declared stdio MCP. The request uses the existing native Session, MCP service and connection pool; it adds no model or tool execution loop.

The daemon sets the protected `protected-mcp-v1` tool policy independently of the concurrency limit. The native catalog applies it to root and child profiles, withholding direct native filesystem and process tools. The Session-private `oac_workspace` MCP server supplies the authorized workspace tools to workers. Other MCP servers keep their native selection rules. The same authored policy filters native prompt capabilities, so the ACP root and child templates name builtin workspace tools only when they are available; MCP tool names and schemas come from their declarations. Native Explore and Verifier profiles keep their stricter native capability ceiling. ACP initialization reports the applied policy and admission limit; enabled Subagents reject an unpatched CLI before accepting model input.

Native tool schemas are retained. Text and image results use standard MCP content; video results are rejected. The pinned native CLI may add task and Skill utility tools, so qualification must inspect the actual inventory rather than assume exactly six.

## Tests

`make check-mcode-harness` runs the package's Node tests and syntax checks. The native prompt regression requires `MCODE_SOURCE` pointing to the pinned, patched native source with upstream dependencies installed; the companion build always runs it. It checks ordinary native guidance, disabled local tools and protected root and child capabilities without a model. Qualify changes with the [Harness acceptance checklist](../../contracts/agents-api/harness-onboarding.md#qualify-the-adapter); synthetic probes and native model runs do not complete public Files/Artifacts or independent Core acceptance.

With the same `MCODE_SOURCE`, `native-mcp-lifecycle.test.mjs` exercises the patched native Session configurations, connection pool and MCP SDK against controlled transports. It checks exact Session/server selection, delayed close events, connection attempts interrupted during initialization, and later lazy reconnection without closing unaffected services. It also checks workspace readiness before preparation completes, rejection of incomplete or failed discovery, reuse of the prepared connection, preparation beyond the shorter Turn discovery timeout, configured connection timeouts and shutdown during required discovery.

For the packaged Linux regression, provide an operator-owned private profile and artifact directory, then run `native.test.mjs` inside the qualified Docker Runtime:

```sh
OAC_TEST_MCODE_NATIVE_PROFILE=/absolute/private-profile.json \
OAC_TEST_MCODE_NATIVE_ARTIFACT=/opt/mcode-harness \
node --test packages/mcode-harness/native.test.mjs
```

It verifies a writable native TMPDIR, large Bash output retention and a later native Read. The repository gate skips this case without those inputs; it does not replace Docker cancellation or real-model acceptance.
