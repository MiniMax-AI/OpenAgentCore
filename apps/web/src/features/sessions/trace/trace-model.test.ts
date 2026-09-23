import { describe, expect, it } from "vitest";

import type { AgentTurn, SessionItem } from "@agents-core-web/agents-client";

import { buildTraceModel, filterTraceModel, type TraceAgentSnapshot } from "./trace-model";

function turn(id: string, overrides: Partial<AgentTurn> = {}): AgentTurn {
  return {
    id,
    agent_id: "agent-1",
    session_id: "session-1",
    object: "agent.session.turn",
    status: "completed",
    created_at: 1,
    started_at: null,
    completed_at: null,
    error: null,
    usage: null,
    ...overrides,
  };
}

const agent: TraceAgentSnapshot = {
  instructions: "Answer with source-grounded evidence.",
  tools: [{
    type: "function",
    name: "lookup",
    description: "Look up a record",
    parameters: { type: "object", properties: { query: { type: "string" } } },
  }],
};

describe("trace model grouping", () => {
  it("uses complete ascending Turn order while preserving Item order inside each Turn", () => {
    const items: SessionItem[] = [
      { id: "two-user", turn_id: "two", type: "message", status: "completed", role: "user", content: [{ type: "input_text", text: "second turn" }] },
      { id: "one-user", turn_id: "one", type: "message", status: "completed", role: "user", content: [{ type: "input_text", text: "first question" }] },
      { id: "two-assistant", turn_id: "two", type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: "second answer" }] },
      { id: "one-assistant", turn_id: "one", type: "message", status: "completed", role: "assistant", phase: "final_answer", content: [{ type: "output_text", text: "first answer" }] },
    ];

    const model = buildTraceModel({ turns: [turn("one"), turn("two"), turn("empty")], items, agent });

    expect(model.groups.map((group) => [group.kind, group.title])).toEqual([
      ["configuration", "Agent configuration"],
      ["turn", "Turn 1"],
      ["turn", "Turn 2"],
      ["turn", "Turn 3"],
    ]);
    expect(model.groups[1]?.rows.map((row) => row.sourceItems[0]?.id)).toEqual(["one-user", "one-assistant"]);
    expect(model.groups[2]?.rows.map((row) => row.sourceItems[0]?.id)).toEqual(["two-user", "two-assistant"]);
    expect(model.groups[3]?.rows).toEqual([]);
    expect(model.groups[0]?.rows[0]).toMatchObject({
      kind: "configured_instructions",
      label: "SYSTEM",
      text: { state: "available", value: "Answer with source-grounded evidence." },
    });
  });

  it("appends orphan groups in first-observed order without dropping unknown Items", () => {
    const future = {
      id: "future",
      turn_id: "missing-b",
      type: "future_protocol_item",
      status: "completed",
      future_private_field: "must not enter safe Raw",
      output: { visible: true },
    } as unknown as SessionItem;
    const items: SessionItem[] = [
      future,
      { id: "known", turn_id: "known", type: "message", status: "completed", role: "user", content: [] },
      { id: "orphan-a", turn_id: "missing-a", type: "command_execution", status: "failed", command: "false" },
      { id: "orphan-b", turn_id: "missing-b", type: "message", status: "completed", role: "assistant", content: [] },
    ];

    const model = buildTraceModel({ turns: [turn("known")], items, agent });
    const orphans = model.groups.filter((group) => group.kind === "orphan");

    expect(orphans.map((group) => group.id)).toEqual(["orphan:missing-b", "orphan:missing-a"]);
    expect(orphans[0]?.rows.map((row) => row.sourceItems[0]?.id)).toEqual(["future", "orphan-b"]);
    expect(orphans[0]?.rows[0]).toMatchObject({ kind: "unknown_item", label: "ITEM" });
    expect(orphans[0]?.rows[0]?.safeRaw[0]).toEqual({
      type: "future_protocol_item",
      status: "completed",
      output: { visible: true },
    });
    expect(orphans[0]?.rows[0]?.sourceItems[0]).toBe(future);
    expect(orphans[0]?.turnWallClockDurationMs.state).toBe("unknown");
  });
});

describe("function call correlation", () => {
  it("links an unambiguous call/output pair by call_id and retains both original snapshots", () => {
    const call: SessionItem = {
      id: "call",
      turn_id: "one",
      type: "function_call",
      status: "in_progress",
      call_id: "call-1",
      name: "lookup",
      arguments: { query: "alpha" },
    };
    const output: SessionItem = {
      id: "output",
      turn_id: "one",
      type: "function_call_output",
      status: "completed",
      call_id: "call-1",
      output: { match: "Alpha" },
      duration_ms: 42,
    };
    const model = buildTraceModel({ turns: [turn("one")], items: [call, output], agent });
    const row = model.groups[1]?.rows[0];

    expect(model.groups[1]?.rows).toHaveLength(1);
    expect(row).toMatchObject({
      kind: "tool_call",
      status: "completed",
      durationMs: { state: "available", value: 42 },
      tool: {
        payload: { state: "available", value: { query: "alpha" } },
        result: {
          state: "available",
          value: { output: { match: "Alpha" }, duration_ms: 42 },
        },
        configuredFunction: {
          state: "available",
          value: { name: "lookup", description: "Look up a record" },
        },
      },
    });
    expect(row?.sourceItems).toEqual([call, output]);
    expect(row?.safeRaw).toHaveLength(2);
    expect(row?.safeRaw[0]).not.toHaveProperty("call_id");
    expect(row?.safeRaw[1]).not.toHaveProperty("id");
  });

  it("does not correlate across Turns or guess among duplicate calls", () => {
    const items: SessionItem[] = [
      { id: "call-a", turn_id: "one", type: "function_call", status: "completed", call_id: "same", name: "lookup", arguments: null },
      { id: "call-b", turn_id: "one", type: "function_call", status: "completed", call_id: "same", name: "lookup", arguments: null },
      { id: "output-a", turn_id: "one", type: "function_call_output", status: "completed", call_id: "same", output: "ambiguous" },
      { id: "output-other", turn_id: "two", type: "function_call_output", status: "completed", call_id: "same", output: "other turn" },
    ];
    const model = buildTraceModel({ turns: [turn("one"), turn("two")], items, agent });

    expect(model.groups[1]?.rows).toHaveLength(3);
    expect(model.groups[1]?.rows.map((row) => row.sourceItems)).toEqual(items.slice(0, 3).map((item) => [item]));
    expect(model.groups[2]?.rows).toHaveLength(1);
    expect(model.summary.toolCallCount).toBe(2);
  });

  it("does not infer a correlation when an output precedes its call", () => {
    const output: SessionItem = {
      id: "output-first",
      turn_id: "one",
      type: "function_call_output",
      status: "completed",
      call_id: "late-call",
      output: "durable result",
    };
    const call: SessionItem = {
      id: "call-later",
      turn_id: "one",
      type: "function_call",
      status: "completed",
      call_id: "late-call",
      name: "lookup",
      arguments: null,
    };

    const rows = buildTraceModel({ turns: [turn("one")], items: [output, call], agent }).groups[1]?.rows;

    expect(rows).toHaveLength(2);
    expect(rows?.map((row) => row.sourceItems)).toEqual([[output], [call]]);
    expect(rows?.map((row) => row.safeRaw[0]?.type)).toEqual(["function_call_output", "function_call"]);
  });

  it("does not infer a correlation when one call_id has multiple outputs", () => {
    const call: SessionItem = {
      id: "call",
      turn_id: "one",
      type: "function_call",
      status: "completed",
      call_id: "ambiguous-results",
      name: "lookup",
      arguments: null,
    };
    const firstOutput: SessionItem = {
      id: "output-one",
      turn_id: "one",
      type: "function_call_output",
      status: "completed",
      call_id: "ambiguous-results",
      output: "first",
    };
    const secondOutput: SessionItem = {
      id: "output-two",
      turn_id: "one",
      type: "function_call_output",
      status: "failed",
      call_id: "ambiguous-results",
      output: "second",
    };

    const rows = buildTraceModel({
      turns: [turn("one")],
      items: [call, firstOutput, secondOutput],
      agent,
    }).groups[1]?.rows;

    expect(rows).toHaveLength(3);
    expect(rows?.map((row) => row.sourceItems)).toEqual([[call], [firstOutput], [secondOutput]]);
  });

  it("only attaches a strictly valid and uniquely named configured function", () => {
    const call: SessionItem = {
      id: "call",
      turn_id: "one",
      type: "function_call",
      status: "completed",
      name: "lookup",
      call_id: "lookup-call",
      arguments: null,
    };
    const duplicateTools: TraceAgentSnapshot = {
      instructions: null,
      tools: [
        agent.tools[0],
        { ...agent.tools[0] as Record<string, unknown> },
        { type: "function", name: "unsafe", description: "", parameters: {}, secret: true },
      ],
    };
    const model = buildTraceModel({ turns: [turn("one")], items: [call], agent: duplicateTools });

    expect(model.groups[0]?.rows[0]?.text).toEqual({ state: "unavailable", value: null });
    expect(model.groups[1]?.rows[0]?.tool?.payload).toEqual({ state: "available", value: null });
    expect(model.groups[1]?.rows[0]?.tool?.configuredFunction).toEqual({ state: "unavailable", value: null });
  });
});

describe("trace summary and search", () => {
  it("sums only valid per-Turn wall clocks and reports unknown durations separately", () => {
    const turns = [
      turn("one", { started_at: 100, completed_at: 102 }),
      turn("two", { started_at: 200, completed_at: 203 }),
      turn("missing", { started_at: null, completed_at: 999 }),
      turn("backwards", { started_at: 10, completed_at: 9 }),
    ];
    const items: SessionItem[] = [
      { id: "command", turn_id: "one", type: "command_execution", status: "completed", command: "pwd", duration_ms: 99_999 },
      { id: "search", turn_id: "two", type: "web_search_call", status: "completed", action: { type: "search", query: "evidence" } },
      { id: "result", turn_id: "two", type: "function_call_output", status: "completed", output: "not a call" },
    ];

    const summary = buildTraceModel({ turns, items, agent }).summary;

    expect(summary).toEqual({
      turnCount: 4,
      toolCallCount: 2,
      knownTurnWallClockDurationMs: 5_000,
      turnsWithKnownWallClock: 2,
      turnsWithUnknownWallClock: 2,
    });
  });

  it("filters locally across instructions, message text, payload, and result without mutating the model", () => {
    const items: SessionItem[] = [
      { id: "user", turn_id: "one", type: "message", status: "completed", role: "user", content: [{ type: "input_text", text: "Find customer Alpha" }] },
      { id: "call", turn_id: "one", type: "function_call", status: "completed", call_id: "lookup-1", name: "lookup", arguments: { query: "customer-alpha" } },
      { id: "output", turn_id: "one", type: "function_call_output", status: "completed", call_id: "lookup-1", output: { account: "A-42" } },
    ];
    const model = buildTraceModel({ turns: [turn("one")], items, agent });

    expect(filterTraceModel(model, "  ")).toBe(model);
    expect(filterTraceModel(model, "source-grounded").rows.map((row) => row.kind)).toEqual(["configured_instructions"]);
    expect(filterTraceModel(model, "find customer alpha").rows.map((row) => row.kind)).toEqual(["user_message"]);
    expect(filterTraceModel(model, "customer-alpha A-42").rows.map((row) => row.kind)).toEqual(["tool_call"]);
    expect(filterTraceModel(model, "not-present").groups).toEqual([]);
    expect(model.groups[1]?.rows).toHaveLength(2);
    expect(filterTraceModel(model, "A-42").summary).toBe(model.summary);
  });

  it("marks missing snapshot and Item fields unknown or unavailable without inventing values", () => {
    const item = {
      id: "unknown-message",
      turn_id: "one",
      type: "message",
      status: "completed",
      content: [{ type: "input_image", image_url: "https://example.invalid/image" }],
    } as SessionItem;
    const model = buildTraceModel({ turns: [turn("one")], items: [item], agent: null });

    expect(model.groups[0]?.rows[0]?.text).toEqual({ state: "unknown", value: null });
    expect(model.groups[1]?.turnWallClockDurationMs).toEqual({ state: "unknown", value: null });
    expect(model.groups[1]?.rows[0]).toMatchObject({
      kind: "unknown_item",
      lane: "unknown",
      text: { state: "unavailable", value: null },
      durationMs: { state: "unavailable", value: null },
    });
  });

  it("accepts the explicit null phase, output and error fields of current Cores", () => {
    const items: SessionItem[] = [
      { id: "user", turn_id: "one", type: "message", status: "completed", role: "user", phase: null, content: [{ type: "input_text", text: "question" }] },
      { id: "answer", turn_id: "one", type: "message", status: "completed", role: "assistant", phase: null, content: [{ type: "output_text", text: "answer" }] },
      { id: "call", turn_id: "one", type: "function_call", status: "completed", call_id: "call-1", name: "lookup", arguments: {} },
      { id: "result", turn_id: "one", type: "function_call_output", status: "failed", call_id: "call-1", output: null, error: "lookup failed" },
    ];

    const rows = buildTraceModel({ turns: [turn("one")], items, agent }).groups[1]?.rows;

    expect(rows?.map((row) => [row.kind, row.title])).toEqual([
      ["user_message", "User message"],
      ["assistant_message", "Assistant message"],
      ["tool_call", "lookup"],
    ]);
    expect(rows?.[2]?.tool?.result).toEqual({ state: "available", value: { output: null, error: "lookup failed" } });
  });

  it("degrades malformed known variants without throwing or deriving a title from arguments", () => {
    const malformedSearch = {
      id: "search",
      turn_id: "one",
      type: "web_search_call",
      status: "completed",
      action: { type: "search", queries: { secret: "must-not-be-a-title" } },
    } as unknown as SessionItem;
    const malformedFunction = {
      id: "function",
      turn_id: "one",
      type: "function_call",
      status: { unexpected: true },
      call_id: "call",
      name: { unexpected: true },
      arguments: { api_key: "must-not-be-a-title" },
    } as unknown as SessionItem;
    const malformedType = {
      id: "bad-type",
      turn_id: "one",
      type: { toString: null },
      status: "completed",
    } as unknown as SessionItem;

    const model = buildTraceModel({
      turns: [turn("one")],
      items: [malformedSearch, malformedFunction, malformedType],
      agent,
    });
    const rows = model.groups[1]?.rows;

    expect(rows).toHaveLength(3);
    expect(rows?.map((row) => row.kind)).toEqual(["unknown_item", "unknown_item", "unknown_item"]);
    expect(rows?.map((row) => row.title)).toEqual([
      "Unsupported web_search_call Item",
      "Unsupported function_call Item",
      "Unsupported Item",
    ]);
    expect(rows?.[1]?.status).toBeNull();
    expect(rows?.[1]?.title).not.toContain("must-not-be-a-title");
    expect(model.summary.toolCallCount).toBe(0);
  });

  it("bounds each row search index while retaining early payload terms", () => {
    const item: SessionItem = {
      id: "large-call",
      turn_id: "one",
      type: "function_call",
      status: "completed",
      call_id: "large",
      name: "lookup",
      arguments: { query: "needle", output: "x".repeat(100_000) },
    };

    const row = buildTraceModel({ turns: [turn("one")], items: [item], agent }).groups[1]?.rows[0];

    expect(row?.searchText).toContain("needle");
    expect(row?.searchText.length).toBeLessThan(17_000);
  });

  it("bounds search traversal depth without overflowing the call stack", () => {
    let deeplyNested: unknown = "beyond-the-node-budget";
    for (let depth = 0; depth < 10_000; depth += 1) deeplyNested = [deeplyNested];
    const item: SessionItem = {
      id: "deep-call",
      turn_id: "one",
      type: "function_call",
      status: "completed",
      call_id: "deep",
      name: "lookup",
      arguments: deeplyNested,
    };

    expect(() => buildTraceModel({ turns: [turn("one")], items: [item], agent })).not.toThrow();
  });

  it("bounds search traversal breadth for very wide payloads", () => {
    const wide: Record<string, unknown> = {};
    for (let index = 0; index < 10_000; index += 1) wide[`field_${index}`] = index;
    wide.last_unindexed_value = "outside-the-node-budget";
    const item: SessionItem = {
      id: "wide-call",
      turn_id: "one",
      type: "function_call",
      status: "completed",
      call_id: "wide",
      name: "lookup",
      arguments: wide,
    };

    const row = buildTraceModel({ turns: [turn("one")], items: [item], agent }).groups[1]?.rows[0];

    expect(row?.searchText).not.toContain("outside-the-node-budget");
    expect(row?.searchText.length).toBeLessThan(17_000);
  });

  it("keeps a selected row identity stable when an earlier durable Item is inserted", () => {
    const existing: SessionItem = {
      id: "existing",
      turn_id: "one",
      type: "message",
      status: "completed",
      role: "assistant",
      content: [{ type: "output_text", text: "existing" }],
    };
    const inserted: SessionItem = {
      id: "inserted",
      turn_id: "one",
      type: "message",
      status: "completed",
      role: "user",
      content: [{ type: "input_text", text: "inserted" }],
    };

    const before = buildTraceModel({ turns: [turn("one")], items: [existing], agent }).groups[1]?.rows[0]?.id;
    const after = buildTraceModel({ turns: [turn("one")], items: [inserted, existing], agent }).groups[1]?.rows[1]?.id;

    expect(after).toBe(before);
  });
});
