# MiniMax Code workspace bridge

This adapter companion keeps MiniMax Code ACP, model loop and history. Its trusted
MCP server exposes six original native tools inside the upstream-vendored Linux
sandbox. The pinned native CLI has one source patch: the original SQLite task
admission transaction enforces the daemon's Subagent concurrency limit before
child work starts. Foreground, background, nested and idle-child append admissions
share that transaction; terminal native tasks release capacity. No second model
or scheduling loop is introduced.
Hosted public execution is not qualified by this package alone.

The harness process and ACP Session use a private control directory. Builtin file
tools are disabled. Only the adapter registers this bridge; callers cannot supply
its command, profile, working directory or environment. Workspace project files
are read through sandboxed tools rather than imported by the privileged harness.
Native diff/undo capture is not provided by this path. Common Files/Artifacts use
the bound public workspace independently of the native control directory.

`source.json` pins the CLI, native tool and sandbox source. The pinned npm package
supplies native runtime dependencies; the CLI is built from source into the same
qualified artifact. On Linux x86_64:

```sh
MCODE_NATIVE_SOURCE=/absolute/upstream/checkout MCODE_CLI_DIR=/absolute/pinned/package bash scripts/build-mcode-harness.sh
```

This standalone companion uses its own npm lock and is excluded from the root
pnpm workspace. The build archives the exact source revision, bundles its native tools and sandbox
and installs pinned MCP dependencies. The same source archive builds the CLI with
its upstream build script and lockfile; the pinned package supplies only native
runtime dependencies. `native-patch.json` records the upstream revision and exact
patch hashes. Runtime packaging uses this single CLI artifact. Install
the artifact immutably at `/opt/mcode-harness`. The private profile supplies
`workspace`, `scratch`, `protectedDirs` and `network`. Only the
isolated worker receives the real workspace as its tool root. Missing or mismatched
profiles reject; there is no unsandboxed fallback.

`subagent-snapshot.mjs` is a daemon-only reader of the private native SQLite
history. It opens a read-only transaction, scopes recursive descendants to the
bound root, and fails explicitly if a complete snapshot exceeds its bounds.
It never executes a model or exposes native history to workspace tools. The
adapter freezes root output and retains the same ACP owner for bounded child
settlement. A completed/idle task remains an active public Subagent; native abort
is a Turn cancellation and never implies a closed Subagent.

The bridge owns each launcher until exit. MCP cancellation and transport shutdown
stop all owned workers before releasing the bridge. The outer Runtime owns the
native process group. Both boundaries require real Docker cancellation tests.

The daemon sets the protected `protected-mcp-v1` tool policy independently of
the concurrency limit. The native catalog applies it to root and child profiles,
withholding direct native filesystem and process tools. The Session-private
`oac_workspace` MCP server supplies the already authorized workspace tools to
workers. Other MCP servers retain their existing native selection rules. Native
Explore/Verifier profiles retain their stricter native capability ceiling. ACP
initialization reports the applied policy and admission limit; enabled Subagents
reject an unpatched CLI before accepting model input.

Native tool schemas are retained. Text and image results use standard MCP content;
video results reject explicitly. The pinned native CLI may add task/skill utility tools;
qualification must inspect the actual inventory rather than assume exactly six.

See [workspace qualification](../../contracts/agents-api/mcode-workspace-v1.md) for
the required tests and stopping conditions. Synthetic isolation probes and native
model runs do not complete public Files/Artifacts or independent Core acceptance.

For the packaged Linux regression, provide an operator-owned private profile and
artifact directory, then run `native.test.mjs` inside the qualified Docker Runtime:

```sh
OAC_TEST_MCODE_NATIVE_PROFILE=/absolute/private-profile.json \
OAC_TEST_MCODE_NATIVE_ARTIFACT=/opt/mcode-harness \
node --test packages/mcode-harness/native.test.mjs
```

Run once for each supported network policy. It verifies writable native TMPDIR,
large Bash output retention and subsequent native Read. The ordinary repository
gate skips this case without those explicit inputs; it cannot replace Docker
isolation, cancellation or real-model acceptance.
