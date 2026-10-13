import { describe, expect, it, vi } from "vitest";
import { OpenAIAgentsClient } from "./client";
import type { AgentTurn, SessionEvent } from "./types";

const usage = {
  input_tokens: 7226, output_tokens: 7, total_tokens: 7233,
  input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 },
};

function turn(overrides: Record<string, unknown> = {}) {
  return {
    id: "turn", agent_id: "root", subagent_id: null, session_id: "session",
    object: "agent.session.turn", status: "completed", created_at: 1,
    started_at: 1, completed_at: 2, error: null, usage: null, ...overrides,
  };
}

function session() {
  return {
    id: "session", object: "agent.session", status: "idle", error: null,
    metadata: {}, required_actions: [], vault_ids: [], usage: null, created_at: 1, last_active_at: 1,
    environment: { type: "none" },
    agent: {
      id: "root", model: "model", name: null, instructions: null,
      multi_agent: { enabled: true, max_concurrent_subagents: null }, reasoning: { effort: null, summary: null },
      service_tier: "auto", text: { format: { type: "text" }, verbosity: "medium" }, tools: [],
    },
  };
}

function json(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } });
}

function page(data: Record<string, unknown>[], fields: Record<string, unknown> = {}) {
  return {
    object: "list", data, first_id: data[0]?.id ?? null, last_id: data[data.length - 1]?.id ?? null, has_more: false, ...fields,
  };
}

function sse(events: unknown[]) {
  return new Response(events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join(""), {
    headers: { "Content-Type": "text/event-stream" },
  });
}

function turnEvent(value = turn(), type = "agent.session.turn.completed") {
  const terminal = /\.(completed|failed|cancelled)$/.test(type) ? { usage: value.usage } : {};
  return { type, event_id: "event", session_id: "session", turn_id: value.id, turn: value, ...terminal };
}

function itemEvent(item: Record<string, unknown>) {
  return {
    type: "agent.session.turn.item.added", event_id: `event-${item.id}`, session_id: "session",
    turn_id: item.turn_id, output_index: 0, item,
  };
}

// Exact wire examples also exercised by contracts/agents-api/v1/subagents_test.go.
// The pinned SDK is openai-python d7c41ef, types/beta/agent_session_item.py.
const items: Record<string, unknown>[] = [
  { id: "create", turn_id: "turn", type: "create_subagent_call", status: "completed", agent_id: "root", content: [], model: null, reasoning_effort: null },
  { id: "create-model", turn_id: "turn", type: "create_subagent_call", status: "failed", agent_id: "root", content: [{ type: "output_text", text: "" }], model: "requested-model", reasoning_effort: "high" },
  { id: "send", turn_id: "turn", type: "send_subagent_input_call", status: "completed", sender_agent_id: "root", recipient_agent_id: "child", content: [{ type: "encrypted_content", encrypted_content: "opaque" }] },
  { id: "wait", turn_id: "turn", type: "wait_for_subagents_call", status: "in_progress", sender_agent_id: "root", recipient_agent_ids: ["child", "other"] },
  { id: "resume", turn_id: "turn", type: "resume_subagent_call", status: "completed", sender_agent_id: "root", recipient_agent_id: "child" },
  { id: "interrupt", turn_id: "turn", type: "interrupt_subagent_call", status: "incomplete", sender_agent_id: "root", recipient_agent_id: "child" },
  { id: "close", turn_id: "turn", type: "close_subagent_call", status: "failed", sender_agent_id: "root", recipient_agent_id: "child" },
  { id: "message", turn_id: "turn", type: "agent_message", sender_agent_id: "child", recipient_agent_id: "root", content: [{ type: "output_text", text: "Result." }] },
  { id: "reasoning", turn_id: "turn", type: "reasoning", status: null, summary: [] },
  { id: "reasoning-completed", turn_id: "turn", type: "reasoning", status: "completed", summary: [{ type: "summary_text", text: "Checked." }] },
];

describe("history and live event projections", () => {
  // Official child Turns keep the Session's Agent ID and name the child in subagent_id.
  it.each([null, "child"])("preserves %s subagent identity in reads and SSE", async (subagentId) => {
    const value = turn({ agent_id: "root", subagent_id: subagentId, usage });
    const client = new OpenAIAgentsClient({ fetch: vi.fn()
      .mockResolvedValueOnce(json(value))
      .mockResolvedValueOnce(json(page([value])))
      .mockResolvedValueOnce(sse([turnEvent(value)])) });
    expect(await client.retrieveTurn("session", "turn")).toEqual(value);
    expect((await client.listTurns("session")).data).toEqual([value]);
    const received: SessionEvent[] = [];
    await client.streamEvents("session", { onEvent: (event) => received.push(event) });
    expect(received[0]?.turn).toEqual(value);
  });

  it("does not weaken root creation snapshots or immutable root identity", async () => {
    for (const value of [turn(), turn({ status: "queued", agent_id: "other" })]) {
      const client = new OpenAIAgentsClient({ fetch: async () => sse([
        { type: "agent.session.created", event_id: "created", session: session() },
        turnEvent(value, "agent.session.turn.created"),
      ]) });
      await expect(client.createSessionStream({ environment: { type: "none" } }, "key", {
        onSession: () => undefined, onEvent: () => undefined,
      })).rejects.toMatchObject({ code: "invalid_stream_event" });
    }
  });

  it("projects supported coordination/reasoning Items identically through history and SSE", async () => {
    const client = new OpenAIAgentsClient({ fetch: vi.fn()
      .mockResolvedValueOnce(json(page(items)))
      .mockResolvedValueOnce(sse(items.map(itemEvent))) });
    const saved = (await client.listItems("session")).data;
    const live: SessionEvent[] = [];
    await client.streamEvents("session", { onEvent: (event) => live.push(event) });
    expect(saved).toEqual(items);
    expect(live.map((event) => event.item)).toEqual(saved);
    expect(saved.find((item) => item.type === "agent_message")).not.toHaveProperty("status");
    expect(saved.find((item) => item.id === "reasoning")?.status).toBeNull();
  });

  it.each([
    { ...items[0], agent_id: "" },
    { ...items[2], content: [{ type: "encrypted_content", text: "wrong" }] },
    { ...items[3], recipient_agent_ids: ["child", 123] },
    { ...items[7], status: "completed" },
    { ...items[8], status: "failed" },
    { ...items[9], summary: [{ type: "summary_text", text: 123 }] },
    { ...items[0], native_session_id: "private" },
  ])("rejects malformed or private Item fields in both paths: $type", async (item) => {
    const onEvent = vi.fn();
    const client = new OpenAIAgentsClient({ fetch: vi.fn()
      .mockResolvedValueOnce(json(page([item])))
      .mockResolvedValueOnce(sse([itemEvent(item)])) });
    await expect(client.listItems("session")).rejects.toMatchObject({ code: "invalid_history_resource" });
    await expect(client.streamEvents("session", { onEvent })).rejects.toMatchObject({ code: "invalid_stream_event" });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it.each([
    turn({ session_id: "foreign" }), turn({ id: "other" }),
    turn({ agent_id: "", subagent_id: "child" }), turn({ subagent_id: "" }), turn({ subagent_id: 1 }),
    turn({ usage: { input_tokens: 1 } }), turn({ native_session_id: "private" }),
    (({ subagent_id: _omitted, ...value }) => value)(turn()),
  ])("rejects malformed or mismatched Turn retrieval", async (value) => {
    const client = new OpenAIAgentsClient({ fetch: async () => json(value) });
    await expect(client.retrieveTurn("session", "turn")).rejects.toMatchObject({ code: "invalid_history_resource" });
  });

  it("preserves a caller's cancellation signal on Turn retrieval", async () => {
    const controller = new AbortController();
    const fetch = vi.fn(async () => json(turn()));
    const client = new OpenAIAgentsClient({ fetch });
    await client.retrieveTurn("session", "turn", { signal: controller.signal });
    expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/sessions/session/turns/turn"), expect.objectContaining({ signal: controller.signal }));
  });

  it("recovers measured usage after a completed event with unknown usage", async () => {
    const completed = turn();
    const measured = turn({ usage });
    const requests: string[] = [];
    const responses = [sse([turnEvent(completed)]), sse([turnEvent(measured)]), json(measured), json(page([measured]))];
    const client = new OpenAIAgentsClient({ fetch: async (input, init) => {
      requests.push(String(input));
      expect(new Headers(init?.headers).has("Last-Event-ID")).toBe(false);
      return responses.shift()!;
    } });
    const observed: AgentTurn[] = [];
    await client.streamEvents("session", { onEvent: (event) => { if (event.turn) observed.push(event.turn); } });
    expect(observed[0]?.usage).toBeNull();
    let opened = false;
    await client.streamEvents("session", {
      onOpen: () => { opened = true; },
      onEvent: (event) => { expect(opened).toBe(true); if (event.turn) observed.push(event.turn); },
    });
    expect((await client.retrieveTurn("session", "turn")).usage).toEqual(usage);
    expect((await client.listTurns("session")).data[0]?.usage).toEqual(usage);
    expect(observed.map((entry) => entry.usage)).toEqual([null, usage]);
    expect(requests).toHaveLength(4);
  });
});

describe("history pagination", () => {
  it.each(["asc", "desc"] as const)("recovers every saved Item across %s pages without changing snapshots", async (order) => {
    const snapshots = [
      { id: "input", turn_id: "turn", type: "message", status: "completed", role: "user", phase: null, content: [{ type: "input_text", text: "Question" }] },
      { id: "answer", turn_id: "turn", type: "message", status: "incomplete", role: "assistant", phase: "commentary", content: [{ type: "output_text", text: "Retained partial answer" }] },
      { id: "command", turn_id: "turn", type: "command_execution", status: "incomplete", command: "work", cwd: null, output: "Retained output", exit_code: null, duration_ms: null },
    ];
    const expected = order === "asc" ? snapshots : [...snapshots].reverse();
    const client = new OpenAIAgentsClient({ fetch: async (input) => {
      const query = new URL(String(input), "http://core.test").searchParams;
      expect(query.get("order")).toBe(order);
      const after = query.get("after");
      const index = after === null ? 0 : expected.findIndex((item) => item.id === after) + 1;
      return json(page(expected.slice(index, index + 1), { has_more: index + 1 < expected.length }));
    } });
    const recovered = [];
    let after: string | undefined;
    for (;;) {
      const page = await client.listItems("session", { order, limit: 1, after });
      recovered.push(...page.data);
      if (!page.has_more) break;
      after = page.data[page.data.length - 1]!.id;
    }
    expect(recovered).toEqual(expected);
  });

  it.each(["asc", "desc"] as const)("keeps the %s cursor and page metadata", async (order) => {
    const calls: string[] = [];
    const client = new OpenAIAgentsClient({ fetch: async (input) => {
      calls.push(String(input));
      return json({ data: [turn()], has_more: true, object: "list", first_id: "turn", last_id: "turn" });
    } });
    expect(await client.listTurns("session", { after: "previous/turn", limit: 1, order })).toEqual({
      data: [turn()], has_more: true, object: "list", first_id: "turn", last_id: "turn",
    });
    expect(calls[0]).toContain(`after=previous%2Fturn&limit=1&order=${order}`);
  });

  it.each([
    {}, page([], { has_more: true }), page([], { has_more: "false" }),
    page([turn(), turn()]),
    page([turn()], { first_id: "wrong" }),
    page([turn()], { last_id: null }),
    page([], { object: "turn.list" }),
    page([turn({ session_id: "foreign" })]),
    page([turn()], { internal_cursor: "private" }),
  ])("rejects malformed Turn pages", async (page) => {
    const client = new OpenAIAgentsClient({ fetch: async () => json(page) });
    await expect(client.listTurns("session")).rejects.toMatchObject({ code: "invalid_history_resource" });
  });

  it("rejects cursor repetition, overfull pages and invalid Item envelopes", async () => {
    const client = new OpenAIAgentsClient({ fetch: vi.fn()
      .mockResolvedValueOnce(json(page([turn()], { has_more: true })))
      .mockResolvedValueOnce(json(page([turn(), turn({ id: "turn-2" })])))
      .mockResolvedValueOnce(json(page([items[0]!], { last_id: "other" }))) });
    await expect(client.listTurns("session", { after: "turn" })).rejects.toMatchObject({ code: "invalid_history_resource" });
    await expect(client.listTurns("session", { limit: 1 })).rejects.toMatchObject({ code: "invalid_history_resource" });
    await expect(client.listItems("session")).rejects.toMatchObject({ code: "invalid_history_resource" });
  });
});
