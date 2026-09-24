import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { AgentSession, AgentTurn, SessionItem } from "@agents-core-web/agents-client";

import { TraceView } from "./TraceView";

const session: AgentSession = {
  id: "session-1",
  object: "agent.session",
  agent: {
    id: "agent-1",
    model: "fixture/model",
    name: "Fixture Agent",
    instructions: "Use durable evidence.",
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [{
      type: "function",
      name: "lookup",
      description: "Look up a record",
      parameters: { type: "object", properties: { query: { type: "string" } } },
    }],
  },
  environment: { type: "none" },
  status: "idle",
  error: null,
  metadata: {},
  required_actions: [],
  vault_ids: [],
  usage: null,
  created_at: 1,
  last_active_at: 1,
};

function turn(id: string, overrides: Partial<AgentTurn> = {}): AgentTurn {
  return {
    id,
    agent_id: "agent-1",
    session_id: session.id,
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

const items: SessionItem[] = [
  { id: "user", turn_id: "known", type: "message", status: "completed", role: "user", content: [{ type: "input_text", text: "Find Alpha" }] },
  { id: "assistant", turn_id: "known", type: "message", status: "completed", role: "assistant", phase: "final_answer", content: [{ type: "output_text", text: "Alpha found" }] },
  { id: "call", turn_id: "known", type: "function_call", status: "in_progress", name: "lookup", call_id: "call-1", arguments: { query: "Alpha" } },
  { id: "output", turn_id: "known", type: "function_call_output", status: "completed", call_id: "call-1", output: { found: true }, duration_ms: 42 },
  { id: "orphan", turn_id: "missing", type: "future_item" as SessionItem["type"], status: "completed", output: "future" },
];

function render(options: {
  detailState?: "idle" | "loading" | "ready" | "failed";
  detailError?: string | null;
  turnState?: "idle" | "loading" | "ready" | "failed";
  turnError?: string | null;
} = {}) {
  return renderToStaticMarkup(
    <TraceView
      id="trace-panel"
      labelledBy="trace-tab"
      session={session}
      turns={[
        turn("known", { started_at: 10, completed_at: 12, usage: {
          input_tokens: 8,
          output_tokens: 3,
          total_tokens: 11,
          input_tokens_details: { cached_tokens: 2 },
          output_tokens_details: { reasoning_tokens: 1 },
        } }),
        turn("unknown"),
      ]}
      items={items}
      detailState={options.detailState ?? "ready"}
      detailError={options.detailError ?? null}
      turnState={options.turnState ?? "ready"}
      turnError={options.turnError ?? null}
    />,
  );
}

describe("Trace workbench presentation", () => {
  it("renders honest summaries, durable lanes, grouped rows, and search", () => {
    const html = render();

    expect(html).toContain('id="trace-panel"');
    expect(html).toContain('aria-labelledby="trace-tab"');
    expect(html).toContain("Known Turn time");
    expect(html).toContain("2 s · 1 unknown");
    expect(html).toContain("Tool calls");
    expect(html).toContain("Durable order");
    expect(html).toContain("Equal-width sequence · not time-scaled");
    expect(html).toContain("Core reports Turn wall-clock time, but not per-item timing");
    // Turns have their own table on the Session page; the trace no longer embeds them.
    expect(html).not.toContain("Turn diagnostics");
    expect(html).not.toContain("Session aggregate usage");
    expect(html).toContain('data-duration-state="unavailable"');
    expect(html).toContain('title="Core does not provide per-item timing."');
    expect(html).toContain('<span aria-hidden="true">—</span>');
    expect(html).toContain("Per-item timing not provided by Core.");
    expect(html).toContain('data-duration-state="available"');
    expect(html).toContain("Tool-reported duration: 42 ms.");
    expect(html).toContain("Turn 2 s");
    expect(html).toContain("Turn time unknown");
    expect(html).not.toContain("Turn Unavailable");
    expect(html).toContain("Configured instructions");
    expect(html).toContain("Turn 1");
    expect(html).toContain("Turn 2");
    expect(html).toContain("Unassociated Items 1");
    expect(html).toContain("Find Alpha");
    expect(html).toContain("Alpha found");
    expect(html).toContain("lookup");
    expect(html).toContain('type="search"');
    expect(html).not.toContain("TTFT");
    expect(html).not.toContain("Throughput");
  });

  it("retains durable rows while partial Turn and Item reads are reported", () => {
    const html = render({
      detailState: "failed",
      detailError: "Items read unavailable",
      turnState: "failed",
      turnError: "Turns read unavailable",
    });

    expect(html).toContain("Turn history is incomplete");
    expect(html).toContain("Turns read unavailable");
    expect(html).toContain("Item history is incomplete");
    expect(html).toContain("Items read unavailable");
    expect(html).toContain("Alpha found");
  });
});
