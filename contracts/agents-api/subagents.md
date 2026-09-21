# Subagent resources and Runtime observations

The target is the six read operations in the pinned SDK in `upstream.json`.
Implementation and qualification are separate: the public types and handlers do
not qualify a harness merely because it can deserialize them. Live acceptance for
this batch is pending; record the final exact source and evidence before release.

## Public reads

All paths are under `/v1/agents/sessions/{session_id}` and require the same tenant
authentication and `OpenAI-Beta: agents=v1` as ordinary Session reads.

| Path | Result |
| --- | --- |
| `/subagents` | Direct, nested and closed Subagents |
| `/subagents/{subagent_id}` | One Subagent |
| `/subagents/{subagent_id}/items` | Only that child's own Items |
| `/subagents/{subagent_id}/turns` | Only that child's own Turns |
| `/subagents/{subagent_id}/turns/{turn_id}` | One owned Turn |
| `/subagents/{subagent_id}/turns/{turn_id}/items` | Only that child's Items in that Turn |

Lists use `after`, `limit` (1–100, default 20) and `order` (default `desc`).
Cursors must belong to the requested tenant, Session, child and optional Turn.
Child Turns also appear in Session Turn reads with the same IDs. Their `agent_id`
and `subagent_id` identify the child. Root Turns have `subagent_id: null`. Session
Items remain root-owned; inherited native parent transcripts are not child work.

Active includes idle. Successful close records native time; successful reopen
preserves identity and `opened_at`, clears `closed_at`, and emits `active` once.
An already-active resume is a no-op. Turn completion, interruption and process
release do not close a Subagent. Unknown token measurements remain null.

## Common adapter contract

Use `internal/agentdaemon/proto/subagents.go` through the existing authenticated
Run and execution journal. No new transport, scheduler or model/tool loop exists.

| Fact | Adapter obligation |
| --- | --- |
| Identity | Prove native ID, original parent and creation time; publish parents first |
| Lifecycle effect | Prove a successful close/reopen and its original time; stable effect identity across history reads |
| Child Turn | Supply native-owned ID, state and source timestamps, with known Usage only; a missing native cancellation timestamp requires a durable confirmed-effect receipt |
| Child Item | Supply an ordered complete message/tool snapshot using the existing neutral vocabulary |
| Coordination | Translate the native operation and actor/recipient identities without putting native tool names in Core |

Core supplies public IDs and ownership from the authorized Session binding.
Identity, lifecycle, child history and live event projections commit atomically
under the existing Session lock and execution lease. Repeated observations retain
IDs and do not duplicate lifecycle events. Conflicting effects fail. A failed
coordination request to a nonexistent child preserves its opaque requested target;
it does not create a Subagent or imply that the target exists.

Child Turns have a native writer, so their storage is separate from the Core work
queue. A read-only SQL view joins root and child Turns for public pagination.
Child work never becomes a second queued Core execution. Public GETs read durable
resources; they neither start native processes nor replay execution.

An adapter freezes root output before child settlement, keeps the existing native
owner/reader alive while finite child work completes, and delivers child Items
before their terminal Turn snapshot and Run completion. Cancellation uses the
same owner and must settle child writes before release. A failed observation or
uncertain native effect cannot be converted to a successful empty history.

## Native evidence and remaining qualification

Fixed Codex 0.153.4 real probes established original close/resume/no-op/failure
facts by correlating direct-tool output with the same call's persisted canonical
completion. Cold resume has no raw-result opt-in, so this requires native persisted
receipts. The proven profile excludes result-rewriting hooks, code-mode and
plugins. A packaged immutable Bash PreToolUse hook is allowed only with enforced
managed-only hook discovery and its exact Runtime command and matcher. Native Turn times have second precision; converting to milliseconds does
not create additional precision. A real root-first probe confirmed child file
work can finish in the same owner after the root finishes.

Multi-agent execution with required ToolEnvironment initialization remains an
explicit combination gap until child hook-process failures are handled by the
same execution owner. The managed PreToolUse hook itself does not alter lifecycle
result receipts, but existing hook-failure handling only covers the root Turn. Ordinary single-agent environment/package
execution is unchanged. Public function and MCP tools with enabled multi-agent execution
remain unqualified. These limitations do not redefine the official protocol.

Claude's fixed SDK uses native Agent and idle-child SendMessage calls. Original
private child records establish parentage, the first own input time and subsequent
own Turns; inherited parent context is excluded. The existing query owner admits
children before start and retains their history through settlement. Confirmed
cancellation uses a protected immutable effect receipt because native abort can
leave no terminal record. The receipt preserves the confirmed effect time across
reads without rewriting native history. This profile has no qualified close
operation, and completed or cancelled children remain active. See the
[adapter contract](../../packages/claude-sdk-adapter/SUBAGENTS.md) for restrictions.

MiniMax's fixed ACP supplies native delegation operations. Its Session-private
SQLite records supply original child identity, accepted inputs, terminal times
and own messages. The native task-create transaction enforces the requested
concurrent limit before start; native preparation must acknowledge that applied
limit and the protected tool profile before any model input. Discovery describes
adapter support, not proof that an arbitrary installed CLI applied these controls.
Its child workspace isolation and public integration remain under qualification.
Neither adapter may substitute parent output, task completion, observation time
or an empty list for missing facts.

Still unconfirmed upstream semantics include root-completion child propagation,
complete child-delta ordering and Session Usage aggregation. Unlimited background
work across root Turns, complete multi-agent conformance and business Teams are
not established by these six resource reads.
