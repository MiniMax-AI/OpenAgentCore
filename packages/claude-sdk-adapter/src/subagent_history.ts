import { readFile, realpath, stat, writeFile, link, unlink } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { dirname, join, relative } from "node:path";

export type Fact = { type: "subagent_identity" | "subagent_turn" | "subagent_item" | "subagent_coordination"; fact: Record<string, unknown> };
export type NativeRow = {
  type: string; uuid?: string; parentUuid?: string | null; sessionId?: string; agentId?: string;
  isSidechain?: boolean; timestamp?: string; toolUseResult?: Record<string, unknown>;
  message?: { id?: string; role?: string; content?: unknown; stop_reason?: string | null };
};
type Call = { id: string; name: string; input: Record<string, unknown>; turn: string; result?: Record<string, unknown>; error?: boolean; finished?: boolean };
export type Cancellation = { root: string; native_id: string; turn_id: string; source_item_id: string; last_item_id: string; completed_at_ms: number };
type Entry = { id: string; metadata: Record<string, unknown>; rows: NativeRow[]; cancellations?: Cancellation[] };
type Child = { id: string; parent: string; call: Call; rows: NativeRow[]; opened: number; cancellations: Cancellation[] };
const identity = /^[A-Za-z0-9_-]{1,160}$/;
function fail(): never { throw new Error("invalid native child history"); }
const blocks = (r: NativeRow): Record<string, unknown>[] => Array.isArray(r.message?.content) ? r.message.content as Record<string, unknown>[] : [];
const input = (r: NativeRow) => r.type === "user" && !blocks(r).some(b => b.type === "tool_result");
const text = (r: NativeRow): string => typeof r.message?.content === "string" ? r.message.content : blocks(r).filter(b => b.type === "text").map(b => b.text).join("");
function time(r: NativeRow): number { const n = typeof r.timestamp === "string" ? Date.parse(r.timestamp) : NaN; return Number.isFinite(n) && n > 0 ? n : fail(); }

function calls(rows: NativeRow[]): Map<string, Call> {
  let turn = ""; const result = new Map<string, Call>();
  for (const row of rows) {
    if (input(row)) turn = row.uuid ?? "";
    for (const b of blocks(row)) {
      if (b.type === "tool_use") {
        if (typeof b.id !== "string" || typeof b.name !== "string" || !b.input || typeof b.input !== "object" || !turn) fail();
        if (result.has(b.id as string)) fail();
        result.set(b.id as string, { id: b.id as string, name: b.name as string, input: b.input as Record<string, unknown>, turn });
      } else if (b.type === "tool_result") {
        const call = result.get(b.tool_use_id as string);
        if (call) { call.finished = true; call.result = row.toolUseResult; call.error = b.is_error === true; }
      }
    }
  }
  return result;
}

export function projectHistory(root: string, rootRows: NativeRow[], entries: Entry[]): Fact[] {
  const facts: Fact[] = [], children = new Map<string, Child>();
  const byParent = new Map<string, Map<string, Call>>([[root, calls(rootRows)]]);
  for (const entry of entries) byParent.set(entry.id, calls(entry.rows));
  for (const { id, metadata, rows, cancellations = [] } of entries) {
    if (!identity.test(id) || !rows.length || rows.some(r => r.sessionId !== root || r.agentId !== id || r.isSidechain !== true)) fail();
    const first = rows.find(r => r.type === "user" || r.type === "assistant");
    if (!first || !input(first) || first.parentUuid !== null || !first.uuid) fail();
    const parent = typeof metadata.parentAgentId === "string" ? metadata.parentAgentId : root;
    if (parent === id || (parent === root && metadata.spawnDepth !== 1)) fail();
    const call = byParent.get(parent)?.get(metadata.toolUseId as string);
    if (!call || !["Agent", "Task"].includes(call.name) || call.input.prompt !== text(first)) fail();
    for (const receipt of cancellations) {
      const own = rows.find(r => input(r) && r.uuid === receipt.turn_id);
      if (!own || receipt.root !== root || receipt.native_id !== id || receipt.source_item_id !== call.id ||
          !rows.some(r => r.uuid === receipt.last_item_id) || !Number.isSafeInteger(receipt.completed_at_ms) || receipt.completed_at_ms < time(own)) fail();
    }
    if (call.result?.agentId !== id && !cancellations.some(r => r.turn_id === first.uuid)) fail();
    children.set(id, { id, parent, call, rows, opened: time(first), cancellations });
  }
  const ordered: Child[] = [], pending = new Map(children);
  while (pending.size) {
    const ready = [...pending.values()].filter(c => c.parent === root || ordered.some(p => p.id === c.parent));
    if (!ready.length) fail();
    for (const child of ready) { ordered.push(child); pending.delete(child.id); }
  }
  for (const child of ordered) facts.push({ type: "subagent_identity", fact: {
    native_id: child.id, parent_native_id: child.parent, native_created_at: Math.floor(child.opened / 1000),
    parent_turn_id: child.call.turn, source_item_id: child.call.id, name: typeof child.call.input.name === "string" ? child.call.input.name : null,
    instructions: null,
  } });
  for (const child of ordered) facts.push(...projectChild(child, children));
  for (const call of byParent.get(root)!.values()) {
    const coordination = coordinationFact(call, "", children);
    if (coordination) facts.push({ type: "subagent_coordination", fact: coordination });
  }
  return facts;
}

function coordinationFact(call: Call, actor: string, children: Map<string, Child>): Record<string, unknown> | undefined {
  const spawn = ["Agent", "Task"].includes(call.name), send = call.name === "SendMessage";
  if (!spawn && !send) return;
  const recipient = spawn ? call.result?.agentId ?? [...children.values()].find(c => c.call.id === call.id)?.id : call.input.to;
  if (!call.error && (typeof recipient !== "string" || !children.has(recipient))) fail();
  return { id: call.id, kind: spawn ? "create_subagent_call" : "send_subagent_input_call", status: call.error || !call.finished ? "failed" : "completed",
    actor_id: actor, recipients: typeof recipient === "string" ? [recipient] : [], text: typeof (spawn ? call.input.prompt : call.input.message) === "string" ? (spawn ? call.input.prompt : call.input.message) : null,
    model: null, reasoning_effort: null };
}

function projectChild(child: Child, children: Map<string, Child>): Fact[] {
  const facts: Fact[] = [], turns: NativeRow[][] = [];
  for (const row of child.rows) {
    if (input(row)) turns.push([]);
    if (turns.length) turns[turns.length - 1].push(row);
  }
  for (let rows of turns) {
    const first = rows[0], turn = first.uuid!, created = time(first);
    const cancellation = child.cancellations.find(r => r.turn_id === turn);
    if (cancellation) {
      const last = rows.findIndex(r => r.uuid === cancellation.last_item_id);
      if (last < 0) fail();
      rows = rows.slice(0, last + 1);
    }
    const state = { native_id: child.id, turn_id: turn, created_at_ms: created, started_at_ms: created };
    facts.push({ type: "subagent_turn", fact: { ...state, status: "in_progress" } });
    let position = 0;
    const item = (id: string, kind: string, payload: Record<string, unknown>) => facts.push({ type: "subagent_item", fact: {
      native_id: child.id, turn_id: turn, item_id: id, position: position++, kind, payload,
    } });
    item(first.uuid!, "message", { input: [{ role: "user", content: [{ type: "input_text", text: text(first) }] }] });
    const messages = new Map<string, { text: string; thinking: string }>();
    const order: { id: string; kind: "text" | "thinking" | "tool" }[] = [], seen = new Set<string>();
    const toolCalls = calls(rows);
    let completed: number | undefined;
    for (const row of rows) {
      if (row.type !== "assistant") continue;
      const id = row.message?.id;
      if (!id) fail();
      let message = messages.get(id);
      if (!message) { message = { text: "", thinking: "" }; messages.set(id, message); }
      for (const block of blocks(row)) {
        if (block.type === "text" && typeof block.text === "string") message.text += block.text;
        else if (block.type === "thinking" && typeof block.thinking === "string") message.thinking += block.thinking;
        const kind = block.type === "text" ? "text" : block.type === "thinking" ? "thinking" : block.type === "tool_use" ? "tool" : undefined;
        if (kind) {
          const key = kind === "tool" ? block.id as string : id;
          if (!seen.has(kind + ":" + key)) { seen.add(kind + ":" + key); order.push({ id: key, kind }); }
        }
      }
      if (row.message?.stop_reason === "end_turn") completed = time(row);
    }
    for (const entry of order) {
      if (entry.kind !== "tool") {
        const message = messages.get(entry.id)!;
        if (entry.kind === "thinking" && message.thinking) item(entry.id + ":thinking", "reasoning", { status: completed ? "completed" : "incomplete", summary: [{ type: "summary_text", text: message.thinking }] });
        if (entry.kind === "text" && message.text) item(entry.id, "output_message", { id: entry.id, status: completed ? "completed" : cancellation ? "incomplete" : "in_progress", text: message.text });
        continue;
      }
      const call = toolCalls.get(entry.id)!;
      const coordination = coordinationFact(call, child.id, children);
      if (coordination) { item(call.id, "subagent_coordination", coordination); continue; }
      if (call.name !== "Bash") continue;
      if (typeof call.input.command !== "string") fail();
      const results = rows.flatMap(row => blocks(row)).filter(b => b.type === "tool_result" && b.tool_use_id === call.id);
      if (results.length > 1 || (!results.length && !cancellation)) fail();
      if (!results.length) {
        item(call.id, "tool_call", { id: call.id, stage: "after", observation: { kind: "command", status: "failed", command: call.input.command, output: "Native execution was cancelled." } });
        continue;
      }
      const result = results[0], content = result.content;
      const output = typeof content === "string" ? content : Array.isArray(content) ? content.filter(b => b.type === "text").map(b => b.text).join("\n") : "";
      item(call.id, "tool_call", { id: call.id, stage: "after", observation: { kind: "command", status: result.is_error ? "failed" : "completed", command: call.input.command, output } });
    }
    if (completed !== undefined && cancellation) fail();
    const ended = completed ?? cancellation?.completed_at_ms;
    if (ended === undefined || ended < created) fail();
    facts.push({ type: "subagent_turn", fact: { ...state, status: cancellation ? "cancelled" : "completed", completed_at_ms: ended } });
  }
  return facts;
}

export async function readHistory(root: string, transcript: string, cwd: string, cancelled?: { at: number; admitted: ReadonlyMap<string, string> }): Promise<{ facts: Fact[]; ids: string[] }> {
  if (!identity.test(root) || !process.env.CLAUDE_CONFIG_DIR) fail();
  const state = await realpath(process.env.CLAUDE_CONFIG_DIR!);
  const path = await realpath(transcript);
  if (relative(state, path).startsWith("..") || !path.endsWith("/" + root + ".jsonl")) fail();
  async function read(path: string): Promise<string> {
    const resolved = await realpath(path);
    if (relative(state, resolved).startsWith("..") || resolved !== path || (await stat(path)).size > 16 * 1024 * 1024) fail();
    return readFile(path, "utf8");
  }
  async function rows(path: string): Promise<NativeRow[]> {
    const value = await read(path);
    if (!value.endsWith("\n")) fail();
    const lines = value.trimEnd().split("\n");
    if (lines.length > 20000) fail();
    return lines.map(line => JSON.parse(line) as NativeRow);
  }
  const { getSubagentMessages, listSubagents } = await import("@anthropic-ai/claude-agent-sdk");
  const ids = await listSubagents(root, { dir: cwd });
  if (ids.length > 64 || new Set(ids).size !== ids.length) fail();
  const entries: Entry[] = [];
  for (const id of ids) {
    if (!identity.test(id)) fail();
    const base = join(dirname(path), root, "subagents", "agent-" + id);
    const raw = await rows(base + ".jsonl"), metadata = JSON.parse(await read(base + ".meta.json"));
    // The SDK resolves the persisted conversation chain; raw records retain the
    // native ownership and timestamps omitted by its public TypeScript shape.
    const messages = await getSubagentMessages(root, id, { dir: cwd });
    const own = new Map(raw.filter(r => r.uuid).map(r => [r.uuid, r]));
    if (!messages.length || messages.some(m => !own.has(m.uuid))) fail();
    const selected = messages.map(m => own.get(m.uuid)!);
    const cancellations: Cancellation[] = [];
    const inputs = selected.filter(input);
    for (let index = 0; index < inputs.length; index++) {
      const own = inputs[index];
      if (!own.uuid || !identity.test(own.uuid)) fail();
      const receiptPath = base + ".cancel-" + own.uuid + ".json";
      let receipt: Cancellation | undefined;
      try { receipt = JSON.parse(await read(receiptPath)); }
      catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; }
      const start = selected.indexOf(own), end = index + 1 < inputs.length ? selected.indexOf(inputs[index + 1]) : selected.length;
      const finished = selected.slice(start, end).some(r => r.type === "assistant" && r.message?.stop_reason === "end_turn");
      if (!receipt && cancelled && index === inputs.length - 1 && !finished && cancelled.admitted.has(id)) {
        receipt = { root, native_id: id, turn_id: own.uuid, source_item_id: metadata.toolUseId, last_item_id: selected[end - 1].uuid!, completed_at_ms: cancelled.at };
        await storeCancellation(receiptPath, receipt);
      }
      if (receipt) cancellations.push(receipt);
    }
    entries.push({ id, metadata, rows: selected, cancellations });
  }
  return { ids, facts: projectHistory(root, await rows(path), entries) };
}

// Link publishes a complete receipt without replacing an earlier effect. Native
// history remains untouched; replay must use the first confirmed cancellation.
export async function storeCancellation(path: string, receipt: Cancellation): Promise<void> {
  const temporary = path + "." + randomUUID() + ".tmp";
  try {
    await writeFile(temporary, JSON.stringify(receipt), { flag: "wx", mode: 0o600 });
    await link(temporary, path);
  } finally { await unlink(temporary).catch(() => {}); }
}
