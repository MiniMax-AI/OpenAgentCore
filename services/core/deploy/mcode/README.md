# MiniMax Code Runtime

MiniMax Code uses the same Core/Runtime execution contract as the other engines.
The text profile supports `environment:none`; the dedicated Docker profile adds
workspace execution and the shared Files/Artifacts path. The historical
workspace qualification (evidence under `~/.parsar/remediation/20260919/mcode-workspace/`)
does not describe the current bypass profile.
Public MCP/functions, image input and native Subagent execution remain outside
this batch.

## Pinned prerequisite

Use the official [`@minimax-ai/code`](https://github.com/MiniMax-AI/minimax-code)
package version **0.4.12**, with Node.js 22.x and its native SQLite dependency. The Docker image pins Node.js
22.23.1; the text fixture used 22.22.0.
The inspected upstream source is `33b259bbbeb1c16433390869938191d09bdb0680`.
Install outside the checkout, under a private operator directory in `~/.oac/`.
Check the native install succeeds and `mcode --version` reports exactly 0.4.12.
This profile runs on a trusted execution host.

Set `OAC_RUNTIME_MCODE_BIN` to that absolute executable and `OAC_RUNTIME_MCODE_AGENTS_API=1`
for the daemon. The opt-in only advertises the profile for the qualified version.
Use the existing authenticated daemon connection and operator device enrollment;
native self-hosted installation uses the same Runtime protocol. See the
[self-hosted guide](../../../../docs/getting-started/self-hosted.md#platforms); MiniMax on Windows remains
unsupported.
Set `OAC_DEFAULT_HARNESS=mcode` in the independent Core deployment. Existing Sessions
retain their engine. Do not expose a new public harness selector.

## Docker workspace

The [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers)
builds the companion from the pinned native source and the Runtime image.

Configure Core's existing managed Docker provider with the immutable image ID,
`deploy/codex/seccomp.json` and `nested_sandbox: true`. Core, database ownership,
enrollment and the public protocol remain shared. The image supplies the private
`OAC_RUNTIME_MCODE_WORKSPACE=managed` and companion paths; caller Agent options cannot
change them. Public Files and Artifacts use the common bound workspace helpers.

The native process, ACP Session and six original native tools use the declared
Environment workspace (`/workspace` in the Docker image). The tools connect
through one trusted MCP bridge with the daemon user's ordinary permissions and
no inner sandbox. MCP is internal transport here; its presence alone does not
enable caller-supplied public MCP servers. Native project discovery uses that
same workspace; adapter-owned configuration and history remain in the private
Session data directory. Exact native-ID continuation remains required; recovery
without a recorded ID fails closed. Native Bash observations become public
`command_execution` items after command arguments arrive. Their text output and
status are retained; absent native exit code/duration stay unknown. The private
MCP server and file/skill/task utilities are not invented public MCP or function
calls.

## Provider configuration

Supply an Anthropic-compatible bundle with the model's context and output limits,
either per Session as `x_agents_core.model_provider` or as the deployment default,
set with the Core key in Web or through Core's API:

```sh
curl -fsS -X PUT http://127.0.0.1:8091/core/v1/harnesses/mcode/model-provider \
  -H "Authorization: Bearer $CORE_KEY" -H "Content-Type: application/json" \
  -d '{"protocol":"anthropic","base_url":"https://api.moonshot.cn/anthropic","api_key":"<private provider key>","context_window":64000,"max_output_tokens":4096}'
```

Core maps the bundle to the native custom provider for the Session's exact
`agent.model`, with these limits. The adapter selects it explicitly; it does not
use a native account fallback.
Use the real provider endpoint for the selected credential. Test proxies may
forward requests unchanged and capture tool names/status, but must not synthesize
model responses or log keys.

MiniMax M2 Chat Completions may return inline `<think>` content. The pinned native
Harness does not expose the provider's `reasoning_split` option; the adapter does
not guess which response text to remove. Anthropic Messages supplies distinct
thinking blocks. File read/write utilities also have no qualified public Item
mapping; only actual Bash calls become `command_execution`.

## Execution boundaries

Each API Session uses a separate native state directory. Workspace execution
uses the declared Environment directory; text-only execution uses a private cwd.
The execution child inherits only process and model-network essentials; it does
not inherit Core/daemon tokens or arbitrary Node startup configuration. The
adapter owns its native config, instructions and home and disables external
skills, delegated work, web search, builtin file/shell tools, browser tools,
mcode-tools and native goals. The workspace profile uses its tool bridge without
sandbox isolation. Tools can read any local state available to the launching
user. The outer Environment owns managed isolation; daemon network modes do not
add another network boundary.

Provide system dependencies at image/template build time or on the self-hosted
machine before execution. Runtime does not run apt or sudo or elevate daemon
permissions. `system_packages` is unsupported; missing dependencies fail the
operation requiring them. npm/Python packages and setup retain direct execution
with the user's existing permissions.

Workspace Skills use the frozen installation's exact names and native `skill`
loader. Session-local links resolve to the installed package directories;
external discovery stays disabled. The text-only profile has no selected Skills.
Task utilities such as `task_query`, `task_output` and `task_stop` cannot create
work and enforce native Session ownership. Auxiliary native title requests are
internal bookkeeping. Do not equate these with public function or workspace tools.

ACP `mcode/session/steer` acknowledges acceptance for the active native Turn,
separately from the transport write. It is not a promise that the model consumed
that input before cancellation. Unknown outcomes are never automatically replayed.
Cancellation waits for process-group exit and output settlement. Public slash
text remains a model message instead of invoking native ACP operator commands.

Cold continuation requires the exact persisted native Session ID and matching
workspace cwd (or private cwd for text-only execution). Missing/foreign history
fails; recovery by guessing an ID from native session listings is not qualified.
Public usage breakdown is unavailable because native ACP context occupancy and
cumulative cost are not per-Turn usage.

### Adapter rules

MiniMax Code's opt-in Agents API profile qualifies native ACP 0.4.12 for
`environment:none` text execution. It reuses the shared lifecycle without public
workspace, functions or MCP. Enabled Subagents use the separately qualified
common observation path below. Native configuration disables
file/shell authority and external
capability discovery; the child receives a private Session home and a restricted
environment. Active-input application requires a native ACP receipt. Normal
Turns retain one ACP connection and native Session. Cancellation requires native
root and child completion evidence before reuse. Without a qualified root-state
reader, the ACP cancellation response is insufficient: stop the invalid native
process and wait for its exit before settling the Turn as non-reusable. Common
Runtime recovery then loads the exact owned native history in a new Executor. Do not infer history IDs or qualify hosted execution from this text
profile. See [Acceptance](#acceptance).

MiniMax companion readiness uses private protocol 2; old companions are rejected
even when the upstream version matches. Cancellation retires the executor and
settles its native workers and detached Bash groups before acknowledgement;
later work recovers the same native history in a new owner without replay.

The MiniMax workspace profile builds one CLI from the fixed upstream source and
lockfile through the existing companion packaging path, and connects native
workspace tools through its standard MCP client. The process and native Session
share the declared workspace directory, including for cold continuation. A
trusted adapter-owned bridge runs the original six tool implementations with the
launching user's ordinary permissions. It adds no inner sandbox on any platform.
Keep native history in the Session data directory and native execution and
Files/Artifacts bound to the same Environment workspace. Core and shared file
helpers remain engine neutral. This internal MCP transport does not admit public
MCP configuration. Record the upstream revision, native admission patch hashes
and worker-source provenance. The bounded patch checks the shared
descendant-task limit inside the existing native SQLite admission transaction
before start, without another scheduler. ACP initialization must acknowledge the
applied limit before input. The native tool catalog selects the same admitted
workspace tools for every child, not only the root's configured profile; this is
tool selection, not filesystem isolation. Only the Session's authorized internal
workspace MCP entry crosses the native child selector; this does not grant
external MCP access. Initialization must acknowledge the admitted tool inventory
before input as well. Subagent reads use the Session's native database; the
daemon does not protect it from other tools running as the same user.
Multi-agent workspace execution installs only the existing authorized workspace
MCP entry in that private native configuration so children inherit the same
tools; public MCP and Environment-origin MCP combinations remain separately
qualified. Complete the hosted
[acceptance checklist](../../../../contracts/agents-api/harness-onboarding.md#qualify-the-adapter)
before enabling hosted execution. The standalone companion uses its own npm
lock; `make check` runs its lifecycle tests and script checks, while its
exact-source Linux build and native qualification for the actual supported
platform and outer deployment are required when the companion changes.
Historical tests of both inner network policies do not qualify the current
bypass implementation.

## Acceptance

Recorded results below are historical evidence for their exact binaries and
profiles, not qualification of the current native platforms or bypass behavior.

The opt-in `TestNativeMCodePublicExecution` uses the fixed official Python SDK,
raw HTTP, actual daemon/gateway/Worker and a dedicated PostgreSQL test database.
Provide private `OAC_TEST_MCODE_REAL_OPTIONS` (the provider object above plus the
`model` string), `OAC_RUNTIME_MCODE_BIN`, `OAC_TEST_NATIVE_DAEMON_BIN`,
`OAC_TEST_NATIVE_PROOF_DIR`, `OAC_TEST_OFFICIAL_SDK_PYTHON` and
`OAC_TEST_DATABASE_URL`, then run:

```sh
go test ./services/core/internal/store \
  -run '^TestNativeMCodePublicExecution$' -count=1 -v -timeout=15m
```

It verifies real text execution, same-Turn steering and durable input receipt,
daemon cold restart with the same native ID, ordinary slash text, foreign-tenant
rejection, cancellation and subsequent continuation. Run independently with real
Kimi and MiniMax options. This fixture creates only operator device credentials
privately; all tested Sessions and inputs enter through public HTTP.

`TestNativeMCodeHistoryIsolation` additionally takes
`OAC_TEST_MCODE_FOREIGN_NATIVE_ID` from a successful public run and verifies rejection
in another private native home, plus rejection of a nonexistent history ID. Run it
in the daemon's `internal/agent/mcode` package with the same private native/provider
options. It must fail before model input, without a replacement native Session.

On 2026-09-19, the public fixture passed independently with real Kimi K3 and
MiniMax M2.7 APIs. Both exercised execution, native steering receipts, daemon
restart, history continuation, tenant rejection and cancel/continue. Native
missing/foreign history rejection passed separately. Credential scans found the
provider key only in each private mode-0600 native config, not in logs, public
results or native history. Product ACP regression uses the same 0.4.12 package
and covers new/resume, model/instruction refresh, Skill discovery and MCP using
its existing synthetic provider fixture.

That text acceptance used an in-process Core HTTP server and a separate real
daemon. The subsequent Docker workspace qualification
(`~/.parsar/remediation/20260919/mcode-workspace/`) used independently built
Core binaries, its own database and managed Runtime.
It covers public Files/Artifacts, cancellation, reconnect, exact-history recovery
and isolation with real Kimi and MiniMax APIs. Read its recorded Kimi continuation
limitation: new model-issued commands are not automatic recovery replay. Neither
acceptance establishes complete official protocol compatibility.
