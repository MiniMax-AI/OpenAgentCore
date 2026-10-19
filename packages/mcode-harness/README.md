# MiniMax Code companion

This companion package carries what the daemon needs beside the native MiniMax Code CLI: one patch to the pinned CLI source, a readiness check and a read-only Subagent history reader. MiniMax Code keeps its own ACP Session, model loop, tools and history, and the workspace tools are the native ones. The [MiniMax Code Runtime](../../services/core/deploy/mcode/README.md) guide owns the Runtime image, configuration and qualified deployment. The daemon finds the installed package through `OAC_RUNTIME_MCODE_COMPANION`.

## Patch

`patch-native.mjs` edits the pinned native CLI source before its upstream build. `native-patch.json` in the artifact records the upstream revision and exact patch hashes.

- The SQLite task-admission transaction enforces the daemon's Subagent concurrency limit before child work starts. Foreground, background, nested and idle-child append admissions share that transaction, and terminal native tasks release capacity.
- Background Bash is native behavior tied to `task_output`. When `OAC_RUNTIME_MCODE_BACKGROUND_BASH=off`, the executor neither promotes Bash to the background nor accepts `run_in_background`, so no work outlives a Turn.
- ACP initialization reports `oac/subagents` metadata: its version, whether background Bash is on and the admission limit. The daemon rejects an unpatched CLI, or one with background Bash on, before accepting model input.

No second model or scheduling loop is introduced. Hosted public execution is not qualified by this package alone.

## Build

`source.json` pins the CLI source. The pinned npm package supplies native runtime dependencies; the CLI is built from the patched source with its upstream build script and lockfile. On Linux x86_64 or macOS arm64:

```sh
MCODE_NATIVE_SOURCE=/absolute/upstream/checkout MCODE_CLI_DIR=/absolute/pinned/package bash scripts/build-mcode-harness.sh
```

`provenance.json` records the hash of every artifact file. The artifact lands in `MCODE_HARNESS_BUILD_DIR`, or a new `${OAC_DEV_HOME:-$HOME/.oac}/build/mcode-harness-<timestamp>` directory. Runtime packaging uses this single artifact and installs it immutably at `/opt/mcode-harness`.

## Subagent history

`subagent-snapshot.mjs` is a daemon-only reader of the private native SQLite history. It opens a read-only transaction, scopes recursive descendants to the bound root, and fails explicitly if a complete snapshot exceeds its bounds. It never executes a model. The adapter freezes root output and keeps the same ACP owner for bounded child settlement. A completed or idle task remains an active public Subagent; native abort is a Turn cancellation and never implies a closed Subagent. Native Explore and Verifier profiles keep their stricter native capability ceiling.

## Tests

`make check-mcode-harness` runs the package's Node tests and syntax checks. The build applies the patch and fails when the pinned source no longer matches it. Qualify changes with the [Harness acceptance checklist](../../contracts/agents-api/harness-onboarding.md#qualify-the-adapter); synthetic probes and native model runs do not complete public Files/Artifacts or independent Core acceptance.
