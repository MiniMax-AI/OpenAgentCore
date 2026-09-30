# Claude SDK adapter

This package translates the pinned native Claude Agent SDK into OpenAgentCore's
common Executor and Turn lifecycle. It owns the private TypeScript bridge and
native SDK configuration. The [Go adapter](../../apps/parsar-daemon/internal/agent/claudesdk)
owns its subprocess and translates bridge frames into the shared Runtime protocol.

Start with [Harness onboarding](../../contracts/agents-api/harness-onboarding.md)
for shared interfaces, registration and acceptance. This document owns the
Claude-specific bridge and package rules. Public operation qualification stays in
[the harness contract](../../contracts/agents-api/harnesses.md) and its linked
operation contracts; local readiness cannot expand that qualification.

## Develop and verify

From the repository root, install the pinned workspace dependencies and run:

```sh
pnpm install --frozen-lockfile
pnpm --filter @parsar/claude-sdk-adapter test
make check-claude-sdk
```

The package test compiles TypeScript before running its tests. The Make target
also builds and checks the relocatable Runtime artifact. Changes to native
execution require real-provider acceptance through Core and Runtime, including
continuation and cancellation, followed by the repository's required `make check`.
Use [the deployment guide](../../services/agents-api/deploy/claude/README.md)
for the qualified environment and Runtime build.

## Bridge and native lifecycle

`packages/claude-sdk-adapter` privately owns the pinned official TypeScript SDK
and native message translation. The Go `claudesdk.NewExecutorFactory` uses the shared
owned process runner and emits the existing daemon delta/error/Done frames.
The SDK owns the model loop. Its narrow stdio protocol carries Executor preparation and identified Turn starts,
text deltas, function calls/results/receipts, active input/receipts, usage snapshots
and terminal result/error plus settlement; native translation stays inside the adapter.
The private `native_model_options` input contains only the native options compiled
by Go after shared Harness validation. The bridge checks object/field structure
and maps those fields explicitly to SDK options; enum membership, budget ranges
and thinking combinations belong solely to the Go adapter declaration. The
public `harness_config` object does not cross this private boundary.

With `observe_messages`, it also emits the existing neutral `output_message`
start/completion snapshots and tags deltas with the native Messages API message
ID, not the SDK event UUID. Text blocks in one native message share that identity.
The SDK's per-block assistant snapshots replace draft block text; only native
`message_stop` completes the message, without replaying its text as another delta.
Thinking/tool-only messages produce no text Items; interrupted messages retain
their streamed partial text. No phase is inferred from the final result.
Turn-owned native work and output draining precede reuse. Executor close releases
the SDK Query and native process. The private `turn_settled` frame requires
`confirmed` independently of `reusable`: confirmed native Turn/cancellation
settlement, confirmed resource cleanup, and reuse eligibility are separate facts.
Unknown or nonempty interrupt receipts and unsettled input/function/tool work
remain unconfirmed even after successful teardown. Go rejects cancellation and
AwaitSettlement when native confirmation is missing or false, or its own receipt
ledger remains unsettled. A confirmed Turn may be non-reusable after cleanup;
that state alone does not turn a verified cancellation into an error.
Confirmed native cancellation may settle unanswered function calls after result
admission closes and callbacks drain. A submitted function result still requires
its native application receipt, including when the MCP request aborts.

`claudesdk.Config.Workspace` is an operator binding for the selected workspace and
native state. It enables native Bash/Read/Edit and admitted host functions in the
existing SDK loop. Native tools run with the launching user's permissions on all
platforms; there is no inner sandbox, protected-root deny policy or managed shell
wrapper. Managed isolation belongs to the outer Environment, which must exclude
other tenants' and broader application credentials. An ordinary native install
provides no such boundary. Directory selection is not tenant authorization.
The adapter's existing tool callback authorizes unattended execution in native
`default` permission mode. It does not use the CLI permission-bypass flag, which
Claude rejects for root accounts.

`Config.Env` selects readiness and native process variables. Explicit tool env is
applied to tool execution, but this is not a guarantee that same-user tools cannot
read credentials or history from local files. Workspace hooks retain their event,
identity and lifecycle responsibilities, not security enforcement. Process groups
and Windows Jobs provide cancellation and descendant cleanup, not isolation.
Supported MCP and subagent combinations require their own qualification. The
existing `none` profile keeps its tool inventory. Packaged `workspace_tools`
establishes bridge support, not outer host isolation or public API admission.
The dedicated Runtime composes public preparation, placement quotas, command Items
and Files ownership. Real-provider acceptance verifies effects, cancellation and
same-history continuation for the actual platform and outer deployment.

The private bridge accepts `executor_prepare` without model input. It freezes
validated configuration and resume identity, checks required history, and retains
one native process and SDK Query across Turns. Preparation requires initialization
and acknowledgement of required hooks while the input iterator remains empty.
An `executor_ready` receipt permits later `turn_start` messages containing only
Turn identity and ordered input; configuration replacement and concurrent starts
are rejected. Every Turn event carries its originating `turn_id`. Native Session
identity and actual tool inventory are checked before `input_ready`.

Each Turn ends with a result/error and `turn_settled`, independently of process
exit. The outer input iterator remains open for later Turns. `turn_cancel` invokes
the native interrupt control for that exact Turn. Unconfirmed input, native child
work or queue state invalidates the Executor and requires close before replacement.
EOF, owner signals and invalid control input close owned resources. Preparation
may write native metadata and perform startup traffic; readiness does not prove
provider authentication, complete sandbox health or placement authorization.

`claudesdk.NewExecutorFactory` binds this bridge to `agent.Executor`. Direct-call
and read-only preparation wrappers delegate to the same implementation. Runtime
execution uses the Executor registry for both none and workspace configurations.
Its owner context spans all Turns; a Turn's caller cannot replace fixed resources.
A failed preparation returns its Executor when cleanup remains unconfirmed.
Installed runtime checks are cached by package/file identity, while capability
and request validation still run for each Executor configuration.

The optional private `agent.WorkspaceReader` on this Executor and its delegated wrappers
requires the packaged `workspace_read` feature. It sends bounded relative
paths to that same SDK Query's native `readFile` control. Only the adapter combines
the path with the frozen workspace root; callers cannot replace the placement.
The pinned native read handler awaits file-handle close before its successful
base64 response. The adapter validates bytes and truncation, bounds each result
to 1 MiB and each request to 8 KiB, and admits one read at a time. Its continuous
bridge output consumer retains read receipts during preparation and across Turns.
Caller cancellation detaches observation without cancelling the Run or discarding
an admitted waiter; its original deadline still applies. Owner closure stops
admission. Native null, malformed receipts, timeout and interrupted delivery remain
uncertain and stop the owner; local reap is not a successful read settlement.
The SDK's nullable result catches all native/control errors, so it cannot distinguish
missing files from denial or transport failure. This does not provide a public
Files endpoint, snapshot consistency, placement registration or idle owner policy.
The qualified live workspace fixture also checks binary, empty and bounded reads
before input and during real execution, plus effects before cancellation and reads
on fresh-process history continuation.

The optional private `agent.WorkspaceDirectoryLister` uses the portable
`workspace_directory` bridge feature. Node reads directory metadata under the
selected workspace; there is no Linux `/proc/self/fd` dependency. Public daemon
Files operations share the Go Binding implementation on all platforms. Their
relative path and result bounds define the Files API, not Harness permissions.

Directory requests are bounded to 8 KiB and 1,000 immediate entries, with explicit
truncation, literal names, kinds, and sizes only for regular files. They do not promise
ordering, snapshots, recursion, or public pagination. Missing and permission errors
are returned only from distinguishable filesystem outcomes; unknown results stop the
owner. Each operation closes its directory before a successful receipt.
Caller cancellation, Turn transitions and owner shutdown retain the existing workspace
read settlement rules. This adapter gap fill alone does not enable public Claude Files; the dedicated
Runtime integration supplies public placement and ownership.

With `ObserveToolObservations`, private workspace execution requires the packaged
`workspace_command_observations` feature and emits the existing neutral command
snapshots. Match root, current-query native Bash call/result identities after input;
ignore historical replay, synthetic and child work. Preserve exact command text and
the native per-call textual result, including native rendering or truncation. This
is final native output, not incremental stdout/stderr or reconstructed interleaving.
Native error results are failed; unambiguous structured interruption is incomplete.
Missing results close as incomplete after the observation drain; query cancellation
does not overwrite an already observed native failure. Do not infer an
exit code from rendered text or supply cwd/duration without qualified native fields.
Preparation alone emits no command. Cold continuation must not reissue historical
observations. This private translation does not enable public workspace admission,
Read/Edit Items or Files ownership.

The private adapter also accepts typed anonymous HTTP and static-bearer HTTPS MCP
declarations on the trusted `environment:none` harness host. The packaged readiness
report must include `mcp_http_tools`; discovery advertises that feature only when
present, and execution
rechecks the installed bundle before dispatching an MCP request. An unchanged SDK
version alone cannot qualify an older bridge. Authenticated private requests also
require the packaged `mcp_http_bearer_auth` feature at discovery and dispatch.
The daemon generates a separate environment reference for each server and launch;
only those references enter the bridge request and native SDK configuration.
The native HTTP client expands them from its owned process environment. Literal
bearers must never enter SDK MCP headers because that configuration enters argv.
Readiness probes receive no per-request bearer environment. Token validation is
shared with the Codex adapter; credential storage remains an opaque-string contract.
Public Claude MCP admission reuses the shared resolver, immutable Session snapshots
and neutral Item/event projection.
The API checks the supported profile before persistence, during device selection
and again before claiming execution; a missing runtime capability leaves work queued.

MCP queries use the SDK's main-thread Agent definition to restrict model-visible
tools, in addition to empty built-ins, strict MCP configuration, empty setting
sources and default-deny permissions. Permission allowlists alone do not restrict
the native model inventory. Null selects all tools from a declared server; an empty
list selects none. Host functions compose with those selections. Native server
status supplies original tool identities; map their normalized native aliases while
preserving the original names in observations. Native status deduplicates aliases,
so it does not prove a complete original server inventory. A native PreToolUse hook
waits for inventory verification before admitting root calls and denies unverified,
mismatched or cancelled calls. The native Agent restriction controls model-visible
tools; inventory verification is not a barrier before the model request.
Anonymous HTTP declarations explicitly set an empty Authorization header to disable
native OAuth and automatic credential injection. Preserve that header; do not erase
native history or credentials to enforce this boundary. Servers that reject a blank
Authorization header, normalized name collisions and inventory changes during a
query require separate validation; this profile covers static inventories.
Private SDK status/control objects can contain expanded authentication headers.
Read only connection and tool identity fields; never retain, log or publish raw
status/configuration or control responses. Diagnostic projections must whitelist
safe fields. This does not permit filtering actual model/tool output to hide a leak.
The bounded adapter profile currently requires connected servers, reserves the
`functions` label, accepts alphanumeric/underscore/hyphen server labels and
alphanumeric/underscore/hyphen/dot selected tool names, and excludes remote
environments. Required startup is separately qualified by `mcp_http_required`.
All HTTP MCP queries use native SDK startup and an empty input iterator to
confirm initialization hooks. Required declarations additionally check connected
server status before the initial prompt is released exactly once. Pending, failed,
missing or ambiguous required status rejects before input; native startup timeouts are retained without
an adapter retry loop. Normal system/init still verifies Session identity and the
complete inventory before input readiness/tool authority. Optional servers retain
their existing inventory checks without a new pre-input connection requirement.
A Runtime must advertise the concrete required-initialization capability; there
is no fallback to an older execution path.

Public Claude static-bearer HTTPS MCP reuses the shared
Vault attachment, frozen selection and scoped decryption path. Selection and final
preclaim require the existing bearer capability; shared authentication dispatch
uses capability/placement checks rather than a Codex-name restriction. Missing keys
or failed lookup/decryption never fall back to anonymous execution; an attached
Vault with no matching credential may remain anonymous. These are execution limits,
not saved-Agent schema restrictions or changes to the official protocol.

Root assistant tool calls and live root user results produce the existing neutral
MCP observations. Correlate actual Session/call identities; exclude replay,
synthetic and subagent work and keep host function receipts separate. Preserve
the exact native `tool_use_result` when one result is unambiguous, otherwise the
per-call result content. Native errors remain observed native errors. The SDK can
replace annotated MCP content with rendered structuredContent and flatten MCP
errors; these observations do not claim original MCP envelope fidelity or hosted
output parity. Do not reconstruct lost fields or infer output from model prose.
Unfinished observed calls become incomplete on shutdown, without claiming that
remote tool effects were cancelled. Rich content, native truncation and asynchronous
MCP task results remain unverified.

For unmanaged bootstrap, daemon `connect` optionally registers this factory as
`claude_sdk` when the operator sets `OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT` to the absolute packaged `dist/main.js`.
`OAC_RUNTIME_CLAUDE_SDK_NODE` selects Node (default: `node` on PATH). Discovery resolves
Node once and checks that exact configuration before pairing; the SDK's bounded
runtime check is independent of legacy CLI version probes. A ready SDK alone is
sufficient to start the daemon. No configuration means no SDK probe or descriptor;
failed readiness reports an unavailable descriptor with a rejecting factory.
Runtime checks establish local readiness, not provider authentication. Installed
daemons use `start` and their verified installation manifest for adapter selection
and activation; ambient activation variables cannot extend that selection. See
[the native installation contract](../../deploy/install/README.md#native-daemon-installer).

SDK state lives under `paths.ProfileDir(profile)/runtime/claude-sdk`, independently
of the replaceable runtime bundle. Both the entrypoint and managed state root must
be absolute. Background re-execution inherits operator configuration; it does not
persist provider credentials in pairing profiles. Product `claude_code` remains
unchanged. Product registration explicitly opts existing engines into
`WorkspaceAuthoring`; the authoring registry wraps only that opt-in. SDK registration
bypasses product capability-download, skill-upload and workspace-authoring wrappers.
It does not accept caller-supplied environment variables or business write authority.

The SDK descriptor advertises the validated daemon subset, including durable
Turns/input receipts, text observations, function tools, raw usage and restrictive
execution controls. It does not advertise permissions, product authoring, legacy
raw tool Items, general web-search control or text-verbosity levels. Router admission
for `environment:none` uses the available engine capability, not an engine name.
The independent API selects new Session engines through `OAC_DEFAULT_HARNESS`
(`codex` by default, `claude_sdk` or `mcode`); existing Sessions keep their stored engine.
This remains the deployment default; the optional Core harness extension selects
an enabled engine for one saved or inline Agent configuration. API admission,
device selection and the final preclaim check share the execution service's narrow
engine policy without importing native adapters. Selected engines require the common
durable execution capabilities. Codex retains its general search/verbosity checks;
Claude uses its restrictive profile without claiming those general capabilities.
Idle and initial-input Session creation qualify the resolved configuration before
persistence; saved Agent resources remain independent of engine restrictions.
Claude additionally requires medium verbosity and explicit object-root function
schemas. Function-result batches normalize through the existing shared parser. Claude accepts
text results and, on `none` and Core-managed Docker `openai_hosted`, successful
ordered inline PNG/JPEG results. Unqualified placements, failed image results and
invalid/remote references reject before any batch write, preserving pending calls
and retry identity. Public qualification receives the full neutral result so
success-dependent limitations remain in the profile. Image-bearing delivery alone
requires Runtime function-result image support; text results and function
declarations do not acquire that requirement. These are implementation limits, not changes to the upstream contract.
Do not bypass them by dropping fields, changing model identity or fabricating usage.
Operators may configure the daemon provider environment or the deployment default
model provider for `claude_sdk` (HTTPS `base_url` and a write-only key), which Core
freezes in the Session's encrypted snapshot and delivers as the adapter-owned
`model_provider`; it never enters public Session configuration. The adapter exclusively selects the
provider environment and removes credentials from native tool environments. Product `claude_code` and product execution are unchanged.
The `none` public profile accepts only
text, explicit model/system instructions, managed state, exact native resume and
declared functions with ordered text or successful inline PNG/JPEG results, and the HTTP MCP subset
described above. It rejects unsupported request
options and disables built-in tools and undeclared MCP discovery.
`DisableExecutionEnvironment` and `DisableSubagents` are accepted assertions about
the single-Agent restrictive profile. Omission does not enable built-in tools.
Explicit Subagent observation enables only its qualified native delegation tools,
with admission before start and verified child identity before workspace authority.
Public function/MCP combinations remain unqualified with Subagents. Single-Agent
new and resumed queries use the SDK's empty built-in tool set, explicit function MCP
configuration and allowlist, strict MCP configuration and empty user/project/local
setting sources. Without HTTP MCP declarations, native initialization and real
provider request inventories must contain only the declared host functions. Managed operator policy may further
restrict execution; it must not widen the profile. This limits model tool access,
not native state files or filesystem access by an explicitly supplied host function;
it is not sandbox/file isolation. The private factory accepts typed execution
controls only for disabled search and medium text verbosity. Search remains excluded
by the native tool inventory; medium retains the SDK's default text generation,
without adding instructions or changing caller input. The pinned SDK has no native
verbosity-level option: low/high and enabled search remain explicit implementation
gaps. Missing/invalid fields in a supplied control block fail before native setup;
omitting the block keeps the same restrictive profile. Public engine admission is
qualified separately by the API policy described above.
Use the SDK's history lookup before explicit resume; never fall back to a new
Session. Native files remain device-affine under a caller-selected managed
runtime directory. The launch configuration supplies trusted provider environment;
request options cannot supply environment variables or business write authority.
Omitted, null and empty `system_prompt` map to empty SDK instructions only at this
adapter boundary; null model values and unsupported options remain rejected.

The internal SDK function-server helper uses the maintained MCP server's public
request handlers and standard Tool/CallToolResult types. It snapshots definitions
and forwards JSON Schema without a JSON Schema-to-Zod conversion; supplied tools
are always loaded. Native call identity comes from the pinned harness's
`claudecode/toolUseId` MCP metadata, independently of request IDs, names or arrival
order. Missing identities and undeclared tools fail before invoking the host.
Return content/error fields unchanged over MCP and forward its per-request abort
signal. The private Go factory connects declared functions through this helper and
reuses the daemon function-call/result interface and opt-in neutral observations.
The native function-server registry must contain exactly those functions. SDK allowlisting admits
only these host callbacks; the host still owns result decisions and any business
permission checks. It grants no runtime-token business authority.

Function results remain pending after stdin/MCP delivery. A matching live, root
native user tool_result confirms application only when its Session/call identity,
error flag and ordered content match the submission. Text matches exactly; each
submitted image position must remain a valid native base64 image. Native resizing
or re-encoding may change image bytes. This acknowledges incorporation into native
history, not byte/pixel fidelity or completed provider consumption. Public Items
retain the original caller content; real image-dependent model responses separately
qualify usability. Ignore replayed, synthetic and
subagent messages. Native error text joins the submitted text parts with newlines;
neutral observations retain their original order and separate failure status.
Missing/mismatched receipts fail the execution; do not replay unknown delivery.
Result submission waits at most ten seconds for a receipt and cancels uncertain
execution on timeout. Invalid or unsupported image results fail before consuming
a pending call. Function state belongs to one live Run and ends with it; the
existing router owns receipt retry/conflict handling. This does not establish
crash recovery or exactly-once effects. Public schemas outside MCP's object-root
contract, failed image results and remote image references remain admission/execution gaps.

Each SDK result supplies one native usage snapshot, including reported failures.
`Usage.Raw.claude_sdk_result` holds the latest; queries with multiple native results
also retain all snapshots in order under `claude_sdk_results`. Main-loop `usage`
is per native turn, while query-pipeline `modelUsage` and estimated `total_cost_usd`
are cumulative within the query. Retain subtype/error provenance and earlier
snapshots even when a later failure reports zero counters. Reuse the latest full
snapshot set in Usage and Done; never sum cumulative measurements. Each Executor owns one SDK query, including cold resume. Raw snapshots explicitly
identify per-native-turn usage versus query-cumulative model usage and cost. A new
Turn has a fresh snapshot list, but query totals may include earlier Turns; never
represent those totals as consumption by the current Turn. Missing native results do not imply zero consumption. SDK estimates stay
in raw evidence, outside the billed cost field; do not select an arbitrary model
or invent missing public token breakdowns. The API does not parse native counters.
Precise public usage projection, unreported costs and crash/partial accounting
remain gaps; the native snapshot alone is not complete protocol Usage compatibility.

Active text uses the SDK's `AsyncIterable<SDKUserMessage>` input, with a fresh
native UUID mapped to each daemon input ID. A native query may fold text into its
current native turn or queue another; one daemon Run can therefore contain several
native turns. Never promise Codex's same-native-turn semantics. Writes, queued
notifications and user-message echoes do not confirm consumption. Only matching
root assistant/partial/result `user_message_uuids` (or the singular fallback)
confirm applied input. Typed mid-turn folds may appear only on the native result.
Preserve that receipt even when the result reports failure. Check pending functions
after the query drains: the SDK may dispatch later-turn callbacks before the
earlier result handler finishes.
Keep Turn input admission open until every submitted input has a consuming result,
even when an earlier result reports an empty native queue. Close admission before
releasing final receipt waiters. The outer SDK iterator remains open across Turns.
Cancellation resolves unconfirmed pending receipts as unknown and interrupts the
exact native Turn. Successful Cancel requires confirmed Turn settlement; process
exit alone cannot establish a successful cancellation. Reuse also requires an
empty confirmed native queue and settled child work. Otherwise close the Executor.
The settled CancellationOutcome retains verified native identity, partial text
and observed Usage; a requested resume identity alone is not evidence. Caller
wait expiry reports failure/unknown while cleanup retains ownership. Output
backpressure cannot turn missing native facts into confirmed settlement. Closed
Turn output precedes successful AwaitSettlement; the settled outcome remains
readable when connection loss prevents publication.
A receipt timeout after a full write preserves the process and pending identity
without redelivery; a blocked write is cancelled and released.
The private adapter permits one input awaiting consumption and at most 63 extra
inputs per Run, preserving the native 64-UUID receipt bound. Durable receipt opt-in
separates bounded writes from native consumption waits; calls without it retain
the router's ten-second deadline. Larger input capacity and interrupted-input
recovery remain separate work. Daemon registration alone does not establish public acceptance.

Current public qualification is recorded in the
[harness contract](../../contracts/agents-api/harnesses.md) and its linked operation
contracts. Keep native execution evidence separate from local registration and
packaging checks. Live adapter acceptance uses a real provider with private
credentials; fixture tests cannot substitute for it.

## Runtime artifact

`make build-claude-sdk-runtime` exports the compiled bridge and pinned production
SDK/MCP dependencies, including the native package for the build host, into a
platform/architecture/libc-specific `.tar.gz` and SHA256 file under
`${OAC_DEV_HOME:-$HOME/.oac}/build/claude-sdk-runtime`. `CLAUDE_SDK_BUILD_DIR`
may select another absolute output directory. The production dependency closure
requires Node20 or newer; Node22 is the tested version. This standalone archive
requires operator-supplied Node and is independent of product sources, services
and databases.
It does not add Node or SDK assets to the Agents API binaries/image.

The build validates source manifests with the repository-pinned pnpm frozen
install and compiles into fresh managed staging, never exporting incremental
checkout output. It then uses modern `pnpm deploy` with command-scoped workspace injection
and its dedicated frozen lock. The adapter has no workspace dependencies; keep
that boundary explicit. Do not enable injection globally or replace this with a
custom dependency copier. Export only compiled `dist` and production dependencies;
retain their package metadata, lockfile and licenses. Check dependency links stay
inside the export, pinned SDK/MCP/native versions, native `--version`, and bridge
startup before publishing the archive. Startup with stdin EOF is an import check,
not model execution acceptance. `make check-claude-sdk` includes this artifact check.

Extract the archive into a fresh managed runtime directory on a matching host
and use its absolute `dist/main.js` as the private factory entrypoint. Validate
relocation and real provider cancellation/continuation before accepting an
artifact. Linux x64/glibc with Node22 is the currently exercised platform;
other hosts require their own native acceptance. Do not reuse a bundle across
platforms or libc variants. The native installer bundles Node and owns
user-managed activation; release publication remains separate. Operator-configured
discovery and registration remain available for unmanaged bootstrap as specified
above.

The exported `dist/runtime_check.js` companion is the local readiness contract.
It checks Node20+, installed SDK/MCP/native versions against the package manifest,
contained dependency resolution, native startup, and the exact `dist/main.js` bridge
with stdin EOF. It emits one versioned JSON report without calling a model or
creating Session state. The artifact check reuses this companion and separately
checks all exported links, the lockfile and source pins. `claudesdk.CheckRuntime`
uses the same Node, entrypoint and environment as execution,
with shared process-group ownership, bounded output and a 15-second deadline
plus bounded cleanup. Both native and bridge probes have five-second limits.
Return unavailable on failed or malformed probes; never forward native diagnostics
or treat local readiness as provider authentication, public capability acceptance
or filesystem isolation. The native installer reuses this readiness check after
copying its release components.


Workspace deferred-function discovery uses native ToolSearch alongside the normal
workspace tool profile. Its readiness feature is `workspace_tool_search`, in
addition to `tool_search` and the existing workspace/function features. Qualification,
combination limits and model-policy limitations are owned by
[Deferred function discovery](../../contracts/agents-api/tool-search.md).
