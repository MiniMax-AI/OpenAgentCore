# Native subagent observations

The adapter uses the pinned Claude Agent SDK 0.3.269 and its native Agent and
SendMessage execution. It does not implement a model loop. An explicit Runtime
request enables `oac_worker`; ordinary requests retain their previous tools.
The packaged `subagent_resources` readiness feature gates this request.

A child identity comes from native task admission and persisted child metadata.
The metadata's `toolUseId` must identify the parent's original Agent call;
`parentAgentId` identifies a nested parent, and root children require native
`spawnDepth: 1`. The child history must belong to the same root Session and child
ID. The SDK resolves its persisted conversation chain; the adapter reads the
corresponding private original records for ownership and timestamps that the
SDK's public TypeScript message shape omits.

The first own native user record has a null `parentUuid`. Its timestamp supplies
`opened_at`, in seconds, and the first Turn's creation and start time, in
milliseconds. This is the original input timestamp, not discovery time or a
copied parent record. The fixed native metadata has no separate creation
timestamp. Reopening the Runtime preserves these values. Each subsequent own
input starts a child Turn; native end-turn assistant records supply completion
time. Own messages, reasoning and native Bash receipts retain native IDs and
ordering. Completion leaves the Subagent active and idle: the adapter emits no
closed state because this native profile has no qualified close operation.

The existing native query owns child execution and cleanup. PreToolUse admission
reserves each native Agent or idle-child SendMessage call before execution.
Native `task_started` associates the call and child ID; completion releases that
reservation. The frozen limit applies across the child tree, excludes the root,
and defaults to six. Unknown call associations fail closed. SendMessage to a running child is rejected; only idle continuation is qualified.
Native background
execution, alternate agent types, worktree isolation and per-call model overrides
are not admitted in this profile.

Workspace children use Bash under the existing WorkspaceProfile hooks and native
sandbox. They do not receive unrestricted private-file tools. The parent retains
its existing workspace tool policy. Functions and MCP with subagents are not
qualified combinations and are rejected explicitly. This does not affect the
existing functions/MCP paths without subagents.

The adapter mechanism qualification uses real Kimi calls in the accepted Docker
Runtime, including two child identities, their own histories, workspace writes,
private credential/history/proc-read denial, strict concurrent admission and
same-ID continuation from a new process. These checks are distinct from the
Core public resource, pagination and isolation acceptance performed by the
integration suite.

Cancellation uses an adapter-owned effect receipt only after the existing query
owner confirms native process exit. The fixed native history can end at a tool
call without a cancellation result or timestamp. Each receipt is linked into
the protected native history directory atomically, without overwriting an
earlier receipt, and records the child, own Turn, spawn call and confirmed effect
time. Native records remain unchanged. Replay uses that same timestamp; it
never obtains a new cancellation time by observing an unfinished history. Child
Items and the cancelled Turn precede the root cancellation event. Missing native
exit confirmation or a missing receipt does not imply a terminal child state.
