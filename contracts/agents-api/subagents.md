# Subagents

With `multi_agent.enabled`, a Harness may start native child agents. Core exposes them through the six Subagent read operations of the pinned SDK in [`upstream.json`](upstream.json) and records them from adapter observations. With `multi_agent.enabled=false`, the Runtime removes native child tools. [Harness capabilities](harness-capabilities.md) lists which Harness supports Subagents in which combinations.

## Public reads

All paths are under `/v1/agents/sessions/{session_id}` and need the same Project authentication and `OpenAI-Beta: agents=v1` as ordinary Session reads.

| Path | Result |
| --- | --- |
| `/subagents` | Direct, nested and closed Subagents |
| `/subagents/{subagent_id}` | One Subagent |
| `/subagents/{subagent_id}/items` | Only that child's own Items |
| `/subagents/{subagent_id}/turns` | Only that child's own Turns |
| `/subagents/{subagent_id}/turns/{turn_id}` | One owned Turn |
| `/subagents/{subagent_id}/turns/{turn_id}/items` | Only that child's Items in that Turn |

Lists use `after`, `limit` (default 20) and `order` (default `desc`) and return `object: "list"`, `data`, `first_id`, `last_id` (null on an empty page) and `has_more`. The Subagent and Subagent Turn lists reject a limit outside 1–100 with `limit must be between 1 and 100`; the two Item lists treat 0 as 1 and larger values as 100, like Session Items. Cursors must belong to the requested Project, Session, child and optional Turn.

## Subagent visibility

Child work appears only on the Subagent routes.

- Session `turns.list` and `turns.retrieve` return root Turns only. A child Turn ID, as a path or a list cursor, returns the same 404 as a missing Turn; Core's 404 message differs from the official one.
- The Session event stream and the creation stream carry root work only: child Turns and their Items publish no `agent.session.turn.*` or Item content events. `agent.session.subagent.*` events and root coordination Items stay. The creation stream still ends on the root's settled idle state.
- A child Turn's `agent_id` is the Session's Agent ID (a direct child's `parent_agent_id` and the `create_subagent_call` `agent_id`); `subagent_id` identifies the child. Nested children follow the same rule. Root Turns have `subagent_id: null`.
- Session Items stay root-owned; inherited native parent transcripts are not child work. Session usage sums root Turns only.

Active includes idle. A successful close records the native time; a successful reopen keeps the identity and `opened_at`, clears `closed_at` and emits `active` once. Resuming an active Subagent does nothing. Turn completion, interruption and process release never close a Subagent. Unknown token measurements stay null.

## Adapter contract

Adapters report Subagent facts through `internal/agentdaemon/proto/subagents.go` over the existing authenticated Run and execution journal. There is no separate transport, scheduler or model and tool loop.

| Fact | Adapter obligation |
| --- | --- |
| Identity | Prove the native ID, original parent and creation time; publish parents first |
| Lifecycle effect | Prove a successful close or reopen and its original time; keep the effect identity stable across history reads |
| Child Turn | Supply the native-owned ID, state and source timestamps, with known Usage only; a missing native cancellation timestamp requires a durable confirmed-effect receipt |
| Child Item | Supply an ordered, complete message or tool snapshot in the neutral vocabulary |
| Coordination | Translate the native operation and actor and recipient identities without putting native tool names in Core |

Core assigns public IDs and ownership from the authorized Session binding. Identity, lifecycle, child history and live event projections commit atomically under the Session lock and execution lease. Repeated observations keep their IDs and never duplicate lifecycle events; conflicting effects fail. A failed coordination request to a nonexistent child keeps its opaque requested target and creates no Subagent.

Child Turns have a native writer, so they are stored apart from Core's work queue and never become a second queued execution. Session Turn reads query root Turns directly. Public GETs read durable resources; they never start native processes or replay execution.

An adapter freezes root output before child settlement, keeps its native owner and reader alive while finite child work completes, and delivers child Items before their terminal Turn snapshot and the Run's completion. Cancellation uses the same owner and settles child writes before release. A failed observation or uncertain native effect never becomes a successful empty history; parent output, task completion, observation time or an empty list never substitutes for a missing fact.

## Native profiles

[Harness capabilities](harness-capabilities.md#tools) lists rejected tool combinations.

**Codex.** The adapter enables the native `multi_agent` feature with a nesting depth of 64 and maps the concurrency limit to `agents.max_threads`. It disables native hooks, plugins, code mode and `multi_agent_v2`, and refuses to start if the native hook list is not empty or managed requirements force a conflicting feature. Close and reopen facts come from direct tool output correlated with the same call's persisted completion, so they need native persisted receipts. Native Turn times have second precision. Child file work can finish under the same owner after the root Turn finishes. Cancellation continues under the same owner after a caller deadline; a later call can confirm settlement without repeating the native interrupt.

**Claude SDK.** The pinned SDK's native Agent and SendMessage calls run the single child type `oac_worker`, which inherits the model and has workspace Bash, Agent and SendMessage; the bridge's `subagent_resources` feature gates it. Children use native Bash with the parent's launching-user permissions. Private child records establish parentage, the first own input time and later own Turns; inherited parent context is excluded. The query owner admits children before start and keeps their history through settlement. Confirmed cancellation writes an immutable effect receipt because a native abort can leave no terminal record. There is no close operation: completed or cancelled children stay active. Messages to running children, background work, other child profiles and per-call model overrides are rejected. The [Claude SDK adapter](../../packages/claude-sdk-adapter/README.md#subagents) documents the details.

**MiniMax Code.** Native ACP delegation (`task`, `task_append`, `task_stop`) creates children. Session-private SQLite records supply child identity, accepted inputs, terminal times and own messages. The native task-create transaction enforces the concurrency limit before start, and native preparation must acknowledge that limit and the restricted tool profile before any model input. There is no close or reopen, and native workers do not delegate nested work.

Claude and MiniMax publish verified child history at settlement, not as continuous child progress. Root-completion propagation to children, complete child-delta ordering and unbounded background work across root Turns are not established.
