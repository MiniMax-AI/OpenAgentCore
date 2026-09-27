import assert from "node:assert/strict";
import test from "node:test";
import { projectHistory } from "../dist/subagent_history.js";
import { Subagents } from "../dist/subagents.js";

const timestamp = n => new Date(1700000000000 + n).toISOString();
function fixture() {
  const root = [
    { type: "user", uuid: "root-turn", message: { content: "delegate" } },
    { type: "assistant", message: { content: [{ type: "tool_use", id: "spawn", name: "Agent", input: { prompt: "child input" } }] } },
    { type: "user", message: { content: [{ type: "tool_result", tool_use_id: "spawn", content: "finished" }] }, toolUseResult: { agentId: "child" } },
  ];
  const rows = [
    { type: "user", uuid: "child-turn", parentUuid: null, timestamp: timestamp(10), message: { content: "child input" } },
    { type: "assistant", uuid: "thinking", timestamp: timestamp(20), message: { id: "answer", content: [{ type: "thinking", thinking: "reason" }] } },
    { type: "assistant", uuid: "text", timestamp: timestamp(30), message: { id: "answer", content: [{ type: "text", text: "result" }], stop_reason: "end_turn" } },
  ].map(r => ({ ...r, sessionId: "root", agentId: "child", isSidechain: true }));
  return { root, children: [{ id: "child", metadata: { toolUseId: "spawn", spawnDepth: 1 }, rows }] };
}
test("native child input owns its Turn and stable creation time; completion is not closure", () => {
  const { root, children } = fixture(), facts = projectHistory("root", root, children);
  assert.equal(facts[0].fact.native_created_at, 1700000000);
  assert.equal(facts[0].fact.parent_turn_id, "root-turn");
  assert.equal(facts.filter(e => e.type === "subagent_turn").length, 2);
  assert.deepEqual(facts.filter(e => e.type === "subagent_item").map(e => e.fact.kind), ["message", "reasoning", "output_message"]);
  assert.equal(facts.find(e => e.type === "subagent_turn" && e.fact.status === "completed").fact.completed_at_ms, 1700000000030);
  assert(!facts.some(e => e.type === "subagent_lifecycle"));
  assert.deepEqual(facts, projectHistory("root", structuredClone(root), structuredClone(children)));
});
test("resumed own input preserves original identity and creates a second child Turn", () => {
  const { root, children } = fixture(), rows = children[0].rows;
  rows.push({ ...rows[0], uuid: "continued", parentUuid: "text", isMeta: true, timestamp: timestamp(40), message: { content: "continue" } },
    { ...rows[2], uuid: "continued-output", timestamp: timestamp(50), message: { id: "continued-answer", content: [{ type: "text", text: "continued" }], stop_reason: "end_turn" } });
  const facts = projectHistory("root", root, children);
  assert.deepEqual(facts.filter(e => e.type === "subagent_turn" && e.fact.status === "completed").map(e => e.fact.turn_id), ["child-turn", "continued"]);
  assert.equal(facts[0].fact.native_created_at, 1700000000);
});
test("missing parent evidence, inherited content, foreign ownership and incomplete work fail closed", () => {
  for (const change of [
    f => { f.children[0].metadata.spawnDepth = 2; },
    f => { f.children[0].metadata.toolUseId = "missing"; },
    f => { f.children[0].rows[0].parentUuid = "inherited"; },
    f => { f.children[0].rows[1].agentId = "sibling"; },
    f => { f.children[0].rows[2].message.stop_reason = null; },
    f => { delete f.children[0].rows[0].timestamp; },
  ]) { const f = fixture(); change(f); assert.throws(() => projectHistory("root", f.root, f.children), /invalid native child history/); }
});
test("native admission reserves before start across the whole tree and releases once", async () => {
  const profile = new Subagents("/workspace", 1, undefined), context = { signal: new AbortController().signal };
  profile.consume({ type: "system", subtype: "init", session_id: "root" });
  const before = (id, actor) => profile.beforeTool({ hook_event_name: "PreToolUse", session_id: "root", tool_name: "Agent", tool_use_id: id, agent_id: actor,
    tool_input: { subagent_type: "oac_worker", prompt: "work" } }, id, context);
  assert.equal((await before("one")).hookSpecificOutput.permissionDecision, "allow");
  assert.equal((await before("two")).hookSpecificOutput.permissionDecision, "deny");
  profile.consume({ type: "system", subtype: "task_started", task_type: "local_agent", task_id: "child", session_id: "root", tool_use_id: "one" });
  await profile.childStart({ hook_event_name: "SubagentStart", session_id: "root", agent_id: "child", agent_type: "oac_worker" }, undefined, context);
  assert.equal((await before("nested", "child")).hookSpecificOutput.permissionDecision, "deny");
  const send = await profile.beforeTool({ hook_event_name: "PreToolUse", session_id: "root", tool_name: "SendMessage", tool_use_id: "send",
    tool_input: { to: "child", message: "continue" } }, "send", context);
  assert.equal(send.hookSpecificOutput.permissionDecision, "deny");
  profile.consume({ type: "system", subtype: "task_notification", task_id: "child", session_id: "root", tool_use_id: "one", status: "completed" });
  assert.equal((await before("next", "child")).hookSpecificOutput.permissionDecision, "allow");
  assert.equal((await before("unknown", "foreign")).hookSpecificOutput.permissionDecision, "deny");
  await profile.failedTool({ hook_event_name: "PostToolUseFailure", tool_use_id: "next" }, undefined, context);
  assert.equal((await before("final")).hookSpecificOutput.permissionDecision, "allow");
});

test("an unadmitted native start cannot authorize a child tool actor", async () => {
  const profile = new Subagents("/workspace", 1, undefined);
  profile.consume({ type: "system", subtype: "init", session_id: "root" });
  await assert.rejects(profile.childStart({ hook_event_name: "SubagentStart", session_id: "root", agent_id: "foreign", agent_type: "oac_worker" }), /invalid native child start/);
  assert.equal(profile.permitsActor("foreign"), false);
});

test("confirmed cancellation retains own partial history and a stable effect time", () => {
  const { root, children } = fixture();
  root.pop();
  children[0].rows.pop();
  children[0].cancellations = [{ root: "root", native_id: "child", turn_id: "child-turn", source_item_id: "spawn", last_item_id: "thinking", completed_at_ms: 1700000000100 }];
  const facts = projectHistory("root", root, children);
  const terminal = facts.filter(e => e.type === "subagent_turn").at(-1).fact;
  assert.equal(terminal.status, "cancelled");
  assert.equal(terminal.completed_at_ms, 1700000000100);
  assert.equal(facts.at(-1).fact.status, "failed");
  assert.deepEqual(projectHistory("root", structuredClone(root), structuredClone(children)), facts);
  children[0].rows.push({ ...children[0].rows[1], uuid: "late-recovery-record", message: { id: "late", content: [{ type: "text", text: "late native record" }], stop_reason: "end_turn" } });
  assert.deepEqual(projectHistory("root", root, children), facts);
  children[0].cancellations[0].native_id = "foreign";
  assert.throws(() => projectHistory("root", root, children), /invalid native child history/);
});

test("cancellation publication is atomic and cannot replace an earlier effect", async () => {
  const { mkdtemp, readFile, rm } = await import("node:fs/promises");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const { storeCancellation } = await import("../dist/subagent_history.js");
  const dir = await mkdtemp(join(tmpdir(), "claude-child-cancel-")), path = join(dir, "effect.json");
  const receipt = { root: "root", native_id: "child", turn_id: "turn", source_item_id: "spawn", last_item_id: "thinking", completed_at_ms: 1700000000100 };
  try {
    await storeCancellation(path, receipt);
    await assert.rejects(storeCancellation(path, { ...receipt, completed_at_ms: 1700000000200 }), { code: "EEXIST" });
    assert.deepEqual(JSON.parse(await readFile(path, "utf8")), receipt);
  } finally { await rm(dir, { recursive: true, force: true }); }
});

test("simultaneous continuations reserve the idle recipient before native task start", async () => {
  const profile = new Subagents("/workspace", 6, undefined), context = { signal: new AbortController().signal };
  profile.consume({ type: "system", subtype: "init", session_id: "root" });
  profile.known.add("child");
  const send = id => profile.beforeTool({ hook_event_name: "PreToolUse", session_id: "root", tool_name: "SendMessage", tool_use_id: id,
    tool_input: { to: "child", message: "continue" } }, id, context);
  const result = await Promise.all([send("first"), send("second")]);
  assert.deepEqual(result.map(r => r.hookSpecificOutput.permissionDecision), ["allow", "deny"]);
  await profile.failedTool({ hook_event_name: "PostToolUseFailure", tool_use_id: "first" });
  assert.equal((await send("retry")).hookSpecificOutput.permissionDecision, "allow");
  assert.throws(() => profile.consume({ type: "system", subtype: "task_started", task_type: "local_agent", task_id: "foreign", session_id: "root", tool_use_id: "retry" }), /mismatched native continuation target/);
});

test("child input uses the shared neutral input envelope", () => {
  const { root, children } = fixture();
  const input = projectHistory("root", root, children).find(e => e.type === "subagent_item" && e.fact.kind === "message");
  assert.deepEqual(input.fact.payload, { input: [{ role: "user", content: [{ type: "input_text", text: "child input" }] }] });
  assert.equal(input.fact.item_id, "child-turn");
  assert.equal(input.fact.position, 0);
});
