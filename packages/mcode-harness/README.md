# MiniMax Code workspace bridge

This companion package lets the daemon run MiniMax Code with OpenAgentCore's workspace. MiniMax Code keeps its own ACP Session, model loop and history. The package supplies a trusted MCP server (`bridge.mjs`), which the daemon registers as `oac_workspace` (its MCP server info names it `oac-workspace`). It exposes six native MiniMax Code tools rooted at the Session's workspace: `workspace_read`, `workspace_write`, `workspace_edit`, `workspace_bash`, `workspace_grep` and `workspace_glob`. The tools run as the daemon's user with ordinary permissions; the package adds no inner sandbox. The [MiniMax Code Runtime](../../services/core/deploy/mcode/README.md) guide owns the Runtime image, configuration and qualified deployment.

One patch script (`patch-native.mjs`) makes three edits to the pinned native CLI source. The SQLite task-admission transaction enforces the daemon's Subagent concurrency limit before child work starts; foreground, background, nested and idle-child append admissions share that transaction, and terminal native tasks release capacity. ACP initialization reports `oac/subagents` metadata: its version, the applied workspace tool policy and the admission limit. The native tool catalog applies the `protected-mcp-v1` tool gate described under [Subagents and cancellation](#subagents-and-cancellation). No second model or scheduling loop is introduced. Hosted public execution is not qualified by this package alone.

## Workspace tools

The native process, its ACP Session and the workspace tools share the Session's workspace as their working directory; native configuration, Skills and history stay in the private Session data directory. Builtin file tools are disabled, so project files are read and written through the bridge's tools. Only the adapter registers this bridge; callers cannot supply its command, profile, working directory or environment. Native diff/undo capture is not provided by this path. Common Files and Artifacts use the same bound workspace.

For each tool call, the bridge starts `launch.mjs` with the Session's private profile (`workspace-profile.json`, written by the daemon). The launcher checks that the profile's `workspace` is a canonical absolute path and that `network` is `enabled`, creates the `scratch` directory, and runs the worker in the workspace. An optional `toolEnvFile` supplies the Runtime's frozen tool environment, read only at launch. A present `capabilityRoot` must be a canonical absolute path, and `skills` requires one. A mismatched or invalid profile rejects the call.

## Build

`source.json` pins the CLI and native tool source. The pinned npm package supplies native runtime dependencies; the CLI is built from source into the same artifact. On Linux x86_64 or macOS arm64:

```sh
MCODE_NATIVE_SOURCE=/absolute/upstream/checkout MCODE_CLI_DIR=/absolute/pinned/package bash scripts/build-mcode-harness.sh
```

This standalone companion uses its own npm lock and is excluded from the root pnpm workspace. The build archives the exact source revision, bundles its native tools and installs pinned MCP dependencies. The same source archive builds the CLI with its upstream build script and lockfile; the pinned package supplies only native runtime dependencies. `native-patch.json` in the artifact records the upstream revision and exact patch hashes, and `provenance.json` the hash of every artifact file. The artifact lands in `MCODE_HARNESS_BUILD_DIR`, or a new `${OAC_DEV_HOME:-$HOME/.oac}/build/mcode-harness-<timestamp>` directory. Runtime packaging uses this single CLI artifact and installs it immutably at `/opt/mcode-harness`.

## Subagents and cancellation

`subagent-snapshot.mjs` is a daemon-only reader of the private native SQLite history. It opens a read-only transaction, scopes recursive descendants to the bound root, and fails explicitly if a complete snapshot exceeds its bounds. It never executes a model or exposes native history to workspace tools. The adapter freezes root output and keeps the same ACP owner for bounded child settlement. A completed or idle task remains an active public Subagent; native abort is a Turn cancellation and never implies a closed Subagent.

The bridge owns each launcher until exit. MCP cancellation and transport shutdown stop all owned workers before releasing the bridge. The outer Runtime owns the native process group. Both boundaries require real Docker cancellation tests.

The daemon sets the protected `protected-mcp-v1` tool policy independently of the concurrency limit. The native catalog applies it to root and child profiles, withholding direct native filesystem and process tools. The Session-private `oac_workspace` MCP server supplies the authorized workspace tools to workers. Other MCP servers keep their native selection rules. Native Explore and Verifier profiles keep their stricter native capability ceiling. ACP initialization reports the applied policy and admission limit; enabled Subagents reject an unpatched CLI before accepting model input.

Native tool schemas are retained. Text and image results use standard MCP content; video results are rejected. The pinned native CLI may add task and Skill utility tools, so qualification must inspect the actual inventory rather than assume exactly six.

## Tests

`make check-mcode-harness` runs the package's Node tests and syntax checks. Qualify changes with the [Harness acceptance checklist](../../contracts/agents-api/harnesses.md#acceptance-checklist); synthetic probes and native model runs do not complete public Files/Artifacts or independent Core acceptance.

For the packaged Linux regression, provide an operator-owned private profile and artifact directory, then run `native.test.mjs` inside the qualified Docker Runtime:

```sh
OAC_TEST_MCODE_NATIVE_PROFILE=/absolute/private-profile.json \
OAC_TEST_MCODE_NATIVE_ARTIFACT=/opt/mcode-harness \
node --test packages/mcode-harness/native.test.mjs
```

It verifies a writable native TMPDIR, large Bash output retention and a later native Read. The repository gate skips this case without those inputs; it does not replace Docker cancellation or real-model acceptance.
