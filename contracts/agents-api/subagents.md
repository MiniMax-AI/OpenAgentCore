# Subagent resources and Runtime observations

The target is the six read operations in the pinned SDK in `upstream.json`.
Implementation and qualification are separate: the public types and handlers do
not qualify a harness merely because it can deserialize them. The Docker V1
workflows below have real execution evidence; they do not establish complete
multi-agent or protocol compatibility.

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

Lists use `after`, `limit` (default 20) and `order` (default `desc`) and return
`object: "list"`, `data`, `first_id`, `last_id` (null on an empty page) and
`has_more`. The Subagent and Subagent Turn lists reject a limit outside 1–100;
the two Item lists treat 0 as 1 and larger values as 100, like Session Items.
Cursors must belong to the requested tenant, Session, child and optional Turn.

Child work appears only on these routes. Session Turn list and retrieve return
root Turns only; a child Turn ID there, including as a list cursor, gets the same
404 as a missing Turn. A child Turn's `agent_id` is the Session's Agent ID (a
direct child's `parent_agent_id` and the `create_subagent_call` `agent_id`), and
its `subagent_id` identifies the child; nested children use the same rule, which
is not observed officially. Root Turns have `subagent_id: null`. Session Items
remain root-owned; inherited native parent transcripts are not child work. The
Session event stream carries root work only: child Turns and child Items publish
no `agent.session.turn.*` events. `agent.session.subagent.*` events and root
coordination Items are unchanged. See [Subagent visibility](#subagent-visibility--september-23-2026).

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
queue. Session Turn reads query root Turns directly; the read-only SQL view from
migration 000051 that joins root and child Turns stays in the schema without a
public reader. Child work never becomes a second queued Core execution. Public GETs read durable
resources; they neither start native processes nor replay execution.

An adapter freezes root output before child settlement, keeps the existing native
owner/reader alive while finite child work completes, and delivers child Items
before their terminal Turn snapshot and Run completion. Cancellation uses the
same owner and must settle child writes before release. A failed observation or
uncertain native effect cannot be converted to a successful empty history.
Codex cancellation continues under the same owner after a caller deadline; a
later call can confirm settlement without repeating the native interrupt. Actual
observation failures remain fail-closed, independently of local process cleanup.

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
Neither adapter may substitute parent output, task completion, observation time
or an empty list for missing facts.

## Qualified Docker workflows

The three harnesses passed the same six GET checks with Python SDK 3.13.0 and
raw HTTP against the independent Core, dedicated PostgreSQL and colocated Runtime.
Checks include two real children with their own model output, ascending/descending
pagination, scoped cursors, root/child Item separation, Session/child Turn identity
(under the earlier contract that listed child Turns in Session Turn reads) and
cross-project denial. A new native process continued the same child without
changing old IDs, timestamps or history. Public cancellation stopped actual child
workspace writes, persisted cancelled Turns and left the Subagent active. Core
restart preserved all previously captured resources byte-for-byte after JSON
normalization.

| Harness | Native execution | Verified optional behavior | Explicit limits |
| --- | --- | --- | --- |
| Codex 0.153.4 | Native app-server, Kimi K3 Responses | Nested children, successful close and reopen, same-child continuation | Required ToolEnvironment and public function/MCP combinations are not qualified |
| Claude Agent SDK 0.3.269 | Native Agent/SendMessage, Kimi K3 Anthropic endpoint | Foreground `oac_worker`, idle-child continuation, protected Bash | No qualified close; running-child messages, background work, alternate child profiles and per-call model overrides are rejected |
| MiniMax Code 0.4.12 | Fixed source `33b259bbbeb1c16433390869938191d09bdb0680` and recorded bounded patch, MiniMax M2.7 | Native task/task_append/task_stop, protected workspace tools | No qualified close/reopen; native workers do not delegate nested work; public function/MCP combinations remain unsupported |

Native probes separately verified concurrency admission, child credential/history
protection and cancellation settlement under each supported profile. MiniMax's
initial child-tool isolation failure and Claude's initial input-projection failure
remain failed evidence; subsequent fixed executions supply the acceptance proof.
The MiniMax M3/Codex empty-tool-argument failure remains a separate model-profile
investigation; Core does not repair model output.

Evidence root on `zju_a100_2`:
`~/.parsar/remediation/20260922/subagent-contract/`. Public proofs are in
`public-codex-kimi1`, `public-claude_sdk-2` and `public-mcode-1`; native mechanism
proofs are in `native-proof`, `claude-native` and `mcode-native`. The shared script
`scripts/agents-api-subagents-acceptance.py --phase spawn-direct` validates common
reads; its `resources_passed` and `requested_phase_passed` fields qualify that
phase. Since the visibility batch, `resources_passed` also requires every named
check recorded under `visibility`. Its aggregate `passed` field additionally
requires the optional Codex close/reopen scenario. Do not require unsupported native close operations merely
to make that separate aggregate flag true.

This batch does not rerun the E2B deployment matrix or establish live child-delta
timing equivalence. Claude and MiniMax publish verified child history at settlement;
the accepted read/recovery workflow must not be advertised as continuous native
child progress streaming. Existing single-Agent deployment evidence retains its
original scope.

Still unconfirmed upstream semantics include root-completion child propagation,
complete child-delta ordering and Session Usage aggregation. Unlimited background
work across root Turns, complete multi-agent conformance and business Teams are
not established by these six resource reads.

## Subagent visibility — September 23, 2026

The pin is unchanged: SDK 3.13.0, commit `d7c41ef`, `agents=v1`. This batch is
based on main `73ecc152`. Its plan is
`~/.parsar/remediation/20260923/subagent-visibility/PLAN.md`. The first owned
official Subagent evidence is
`~/.parsar/remediation/20260923/campaign-scan-3/subagents-tools/findings.json`
(SAT-01, 02, 07, 08, 09, with SAT-03 for the kept rejections), with raw records
under `official/`. It covers two owned
Sessions and two child Turns, all deleted.

| Row | Case | Core behavior | Evidence (finding: request ID) |
| --- | --- | --- | --- |
| A1 | Session `turns.list` and `turns.retrieve` | Root Turns only. A child Turn ID, as a path or a list cursor, returns the same 404 as a missing Turn. Subagent Turn list/retrieve and their Item lists still serve child Turns | SAT-07: `req_4bb89ada3457444f994e7a90374d114e` (root-only list), `req_85e7eb58da8e402c8103379ff5bb11d2` (child list), `req_8a599dc455014b0398d884dfa5cc289c` (child ID 404) |
| A2 | GET events and the creation stream | No `agent.session.turn.*` event for a child Turn, including its Item and content events. `agent.session.subagent.*` events and root coordination Items stay. The creation stream still ends on the root's settled idle | SAT-09: `req_e0f7fb0ca13f4eb98b4d677be046e1da`, `req_7a68fa8c18e344cfa0ed202df92a875e` (S1 20 and S2 43 frames, no child Turn or Item event) |
| A3 | Child Turn `agent_id` | The Session's Agent ID; `subagent_id` unchanged. Nested children follow the same rule (not observed) | SAT-08: `req_85e7eb58da8e402c8103379ff5bb11d2`, `req_fc10f0d1a2e84bd086f006c01aa7ee54` |
| A4 | Subagent list envelope | `object`, `data`, `first_id`, `last_id`, `has_more`; null IDs on an empty page | SAT-01: `req_089f86e8088d441380a22de2723e6179`, `req_5f79af4eaea44cb7ab4e92920e0f88c8` |
| A5 | `limit` 0 or above 100 | Subagent Item and Subagent Turn Item lists clamp to 1 and 100. The Subagent and Subagent Turn lists keep rejecting with `limit must be between 1 and 100` | SAT-02: `req_6179ae6c1d1640d899ee4798e7f9fa57`, `req_7436104afbae4e73a0eb43b00ec9e660`, `req_32899313414b4031849a22cd2927f0ad`; SAT-03 rejections: `req_7df58d9579be4ee3ab7fdab55286aa05`, `req_b4321de4480c4a8e96b9ea285ff63a46`, `req_0f437ad4713d47a8af1f61a88636bf79`, `req_e9d476dd2a69472694cffc0851d0574c` |

### Decisions

- Session Turn reads use a new root-only query instead of changing the view, so no
  migration is needed. Child data is not deleted or rewritten.
- The Session event log has one reader: the public GET and creation streams.
  Creation-stream settlement reads the settled idle and the latest root Turn, and
  Session usage sums root Turns, so neither used child Turn events. The Core Web
  timeline had no Subagent view; it only showed child Turns as ordinary Turn rows
  and now keeps its timeline root-only even against an earlier Core, hiding the
  Items of Subagent Turns that Core listed or streamed. Recovery
  continues through Session, Turn and Item reads plus the Subagent routes. No
  internal signal had to be kept.
- The scan recorded child Item events as already absent. They were not: every
  child Item recorded `turn.item.*` and content events with the child Turn ID.
  They are removed with the child Turn events, since the official parent stream
  carried neither.
- The official 404 message for a child Turn ID (`No managed agent resource found:
  …`) and the Subagent 404 messages (SAT-06) differ from Core's local text. Only
  the status, error fields and "same as missing" behavior are aligned here.
- Core may expose documented extensions beyond the official API. This batch
  removes only the mixed Session Turn pages and child Session events, which were
  not documented extensions. Root-only extensions such as
  `agent.output.command_execution_output.delta` and the Web's handling of older
  Core releases are unchanged or additive.

Unchanged: Subagent retrieve fields and statuses, child history contents (SAT-12
remains unknown; Core keeps the child input Item), the hidden task text, the
single `subagent.created` emission, cursor error semantics (SAT-04, HE-57, since
aligned by the [list cursor error batch](list-query-semantics.md#list-cursor-errors--september-23-2026)), native
history ownership, cancellation, cold continuation and tenant isolation.

### Acceptance boundary

Go store and API tests cover each row, including tenant isolation. A real-PostgreSQL
HTTP test also checks creation-stream settlement with Subagent facts. TypeScript
client and Core Web unit tests cover the official child Turn shape and the
root-only timeline. `scripts/agents-api-subagents-acceptance.py` records A1–A5
as named `visibility` checks per phase, including the observed stream. Its
inspect phase, run against a controlled local fixture without a model or stream,
reported all six read differences on baseline main and passed on this branch. Live model acceptance and the server gate are recorded with the
batch when complete.
