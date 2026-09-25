import { describe, expect, it } from "vitest";

import type { AgentSession, AgentTurn, ListPage, PageOptions, SessionItem } from "@agents-core-web/agents-client";

import {
  aggregateAgentMetrics,
  agentMetricsOutcome,
  INLINE_AGENT_ID,
  metricsWindow,
  OTHER_SERIES_ID,
  percentile,
  toolIdentity,
  topSeries,
  type MetricsCoverage,
} from "./agent-metrics";
import { candidateSessions, loadAgentMetricsActivity, loadProjectAgentMetrics, projectMayHaveActivity, projectMetricsSource, type AgentMetricsSource } from "./agent-metrics-loader";
import { project, sessionLister, summary } from "../overview/test-fixtures";

const NOW = 1_800_000_000;

function session(id: string, overrides: Partial<AgentSession> = {}): AgentSession {
  return {
    id,
    object: "agent.session",
    agent: { id: `agent_${id}`, name: `Agent ${id}`, model: "model-a" } as AgentSession["agent"],
    environment: { type: "none" } as AgentSession["environment"],
    status: "idle",
    error: null,
    metadata: {},
    required_actions: [],
    vault_ids: [],
    usage: null,
    created_at: NOW - 7_200,
    last_active_at: NOW - 60,
    ...overrides,
  };
}

function turn(id: string, sessionId: string, overrides: Partial<AgentTurn> = {}): AgentTurn {
  return {
    id,
    agent_id: "agent",
    session_id: sessionId,
    object: "agent.session.turn",
    status: "completed",
    created_at: NOW - 600,
    started_at: NOW - 598,
    completed_at: NOW - 588,
    error: null,
    usage: { input_tokens: 100, output_tokens: 20, total_tokens: 120, input_tokens_details: { cached_tokens: 40 }, output_tokens_details: { reasoning_tokens: 5 } },
    ...overrides,
  };
}

const coverage: MetricsCoverage = { candidateSessions: 2, loadedSessions: 2, skippedSessions: 0, failedSessions: 0, truncatedSessions: 0, itemFailedSessions: 0 };

describe("metrics window", () => {
  it("aligns buckets to the bucket size and ends at the current bucket", () => {
    const window = metricsWindow("1h", NOW + 30);
    expect(window.buckets).toHaveLength(30);
    expect(window.buckets[29]).toBe(Math.floor((NOW + 30) / 120) * 120);
    expect(window.start).toBe(window.buckets[0]);
    expect(window.end).toBe(NOW + 30);
  });
});

describe("aggregateAgentMetrics", () => {
  it("counts Turns as requests and derives error rate, latency and tokens", () => {
    const window = metricsWindow("1h", NOW);
    const first = session("s1");
    const second = session("s2", { agent: { id: "agent_b", name: "Reviewer", model: "model-b" } as AgentSession["agent"] });
    const metrics = aggregateAgentMetrics(window, [
      { session: first, turns: [turn("t1", "s1"), turn("t2", "s1", { status: "failed", completed_at: NOW - 590, usage: null })], items: [], truncated: false },
      { session: second, turns: [turn("t3", "s2", { status: "in_progress", completed_at: null }), turn("t4", "s2", { created_at: NOW - 5 * 3_600 })], items: [], truncated: false },
    ], coverage);

    expect(metrics.totals.requests).toBe(3);
    expect(metrics.totals.completed).toBe(1);
    expect(metrics.totals.failed).toBe(1);
    expect(metrics.totals.unfinished).toBe(1);
    expect(metrics.totals.errorRate).toBe(0.5);
    expect(metrics.totals.averageLatencySeconds).toBe(9);
    expect(metrics.totals.p95LatencySeconds).toBe(10);
    expect(metrics.totals.tokens).toEqual({ input: 200, output: 40, cached: 80, reasoning: 10, total: 240, reportedTurns: 2 });
    expect(metrics.byModel.map((entry) => [entry.id, entry.requests, entry.failed, entry.tokens])).toEqual([
      ["model-a", 2, 1, 120],
      ["model-b", 1, 0, 120],
    ]);
    expect(metrics.byAgent.find((entry) => entry.id === "agent_b")).toMatchObject({ label: "Reviewer", requests: 1, sessions: 1 });
    expect(metrics.series.requests.reduce((sum, value) => sum + value, 0)).toBe(3);
    expect(metrics.series.failed.reduce((sum, value) => sum + value, 0)).toBe(1);
  });

  it("reports no error rate or latency when no Turn finished", () => {
    const metrics = aggregateAgentMetrics(metricsWindow("1h", NOW), [
      { session: session("s1"), turns: [turn("t1", "s1", { status: "queued", started_at: null, completed_at: null, usage: null })], items: null, truncated: false },
    ], coverage);
    expect(metrics.totals.errorRate).toBeNull();
    expect(metrics.totals.averageLatencySeconds).toBeNull();
    expect(metrics.byTool).toBeNull();
    expect(metrics.totals.toolCalls).toBeNull();
  });

  it("attributes tool calls to Turns in the window and counts failures", () => {
    const items: SessionItem[] = [
      { id: "i1", turn_id: "t1", type: "function_call", name: "apply_patch", status: "completed" },
      { id: "i2", turn_id: "t1", type: "command_execution", command: "ls", exit_code: 2, status: "completed" },
      { id: "i3", turn_id: "t1", type: "mcp_call", server_label: "github", name: "search", status: "failed" },
      { id: "i4", turn_id: "old", type: "function_call", name: "apply_patch", status: "completed" },
      { id: "i5", turn_id: "t1", type: "message", role: "assistant", content: [] },
    ];
    const metrics = aggregateAgentMetrics(metricsWindow("1h", NOW), [
      { session: session("s1"), turns: [turn("t1", "s1")], items, truncated: false },
    ], coverage);
    expect(metrics.byTool).toEqual([
      { id: "command", kind: "command", name: null, calls: 1, failed: 1 },
      { id: "function:apply_patch", kind: "function", name: "apply_patch", calls: 1, failed: 0 },
      { id: "mcp:github · search", kind: "mcp", name: "github · search", calls: 1, failed: 1 },
    ]);
    expect(metrics.totals.toolCalls).toBe(3);
    expect(metrics.totals.toolFailures).toBe(2);
  });
});

describe("helpers", () => {
  it("identifies tool kinds", () => {
    expect(toolIdentity({ id: "a", turn_id: "t", type: "web_search_call" })).toEqual({ id: "web_search", kind: "web_search", name: null });
    expect(toolIdentity({ id: "a", turn_id: "t", type: "create_subagent_call" })?.kind).toBe("subagent");
    expect(toolIdentity({ id: "a", turn_id: "t", type: "reasoning" })).toBeNull();
  });

  it("folds the tail into an other series", () => {
    const totals = new Map(Array.from({ length: 7 }, (_, index) => [`m${index}`, 10 - index] as [string, number]));
    const perBucket = new Map([...totals.keys()].map((id) => [id, [1, 2]] as [string, number[]]));
    const series = topSeries(totals, perBucket, 2);
    expect(series.map((entry) => entry.id)).toEqual(["m0", "m1", "m2", "m3", "m4", OTHER_SERIES_ID]);
    expect(series[5]?.values).toEqual([2, 4]);
  });

  it("computes nearest-rank percentiles", () => {
    expect(percentile([1, 2, 3, 4], 0.5)).toBe(2);
    expect(percentile([], 0.95)).toBeNull();
  });
});

function page<T extends { id: string }>(data: T[], hasMore: boolean): ListPage<T> {
  return { data, has_more: hasMore, last_id: data[data.length - 1]?.id ?? null };
}

describe("loadAgentMetricsActivity", () => {
  it("selects recently active Sessions and stops reading at the window start", async () => {
    const window = metricsWindow("1h", NOW);
    const turnCalls: Array<[string, PageOptions | undefined]> = [];
    const itemCalls: string[] = [];
    const source: AgentMetricsSource = {
      async listTurns(sessionId, options) {
        turnCalls.push([sessionId, options]);
        return page([turn("t-new", sessionId), turn("t-old", sessionId, { created_at: window.start - 10 })], true);
      },
      async listItems(sessionId) {
        itemCalls.push(sessionId);
        return page<SessionItem>([
          { id: "i-new", turn_id: "t-new", type: "function_call", name: "lookup" },
          { id: "i-old", turn_id: "t-old", type: "function_call", name: "lookup" },
        ], true);
      },
    };
    const sessions = [
      session("recent"),
      session("stale", { last_active_at: window.start - 1 }),
      session("running", { last_active_at: window.start - 1, status: "in_progress" }),
    ];
    expect(candidateSessions(sessions, window).map((entry) => entry.id)).toEqual(["recent", "running"]);

    const result = await loadAgentMetricsActivity(source, sessions, window, new AbortController().signal, { includeTools: true });
    expect(result.coverage).toEqual({ candidateSessions: 2, loadedSessions: 2, skippedSessions: 0, failedSessions: 0, truncatedSessions: 0, itemFailedSessions: 0 });
    expect(turnCalls).toHaveLength(2);
    expect(turnCalls[0]?.[1]).toMatchObject({ order: "desc", limit: 100 });
    expect(itemCalls).toHaveLength(2);
    const activity = result.activities.find((entry) => entry.session.id === "recent")!;
    expect(activity.turns.map((entry) => entry.id)).toEqual(["t-new"]);
    expect(activity.items?.map((entry) => entry.id)).toEqual(["i-new"]);
  });

  it("caps Sessions, marks truncated history and counts failed reads", async () => {
    const window = metricsWindow("1h", NOW);
    const source: AgentMetricsSource = {
      async listTurns(sessionId, options) {
        if (sessionId === "broken") throw new Error("boom");
        return page([turn(`t-${options?.after ?? "first"}`, sessionId)], true);
      },
      async listItems() {
        return page<SessionItem>([], false);
      },
    };
    const sessions = [session("a"), session("broken", { last_active_at: NOW - 120 }), session("c", { last_active_at: NOW - 3_000 })];
    const result = await loadAgentMetricsActivity(source, sessions, window, new AbortController().signal, {
      includeTools: false,
      limits: { maxSessions: 2, concurrency: 1, maxTurnPages: 2, maxItemPages: 1, sessionTimeoutMs: 5_000, loadBudgetMs: 5_000 },
    });
    expect(result.coverage).toEqual({ candidateSessions: 3, loadedSessions: 1, skippedSessions: 1, failedSessions: 1, truncatedSessions: 1, itemFailedSessions: 0 });
    expect(result.activities[0]?.items).toBeNull();
  });

  it("propagates aborts", async () => {
    const controller = new AbortController();
    const source: AgentMetricsSource = {
      async listTurns() {
        controller.abort();
        throw new DOMException("aborted", "AbortError");
      },
      async listItems() {
        return page<SessionItem>([], false);
      },
    };
    await expect(loadAgentMetricsActivity(source, [session("a")], metricsWindow("1h", NOW), controller.signal, { includeTools: false }))
      .rejects.toMatchObject({ name: "AbortError" });
  });
});

describe("review regressions", () => {
  const limits = { maxSessions: 10, concurrency: 2, maxTurnPages: 2, maxItemPages: 2, sessionTimeoutMs: 50, loadBudgetMs: 5_000 };

  it("reports a failed load instead of claiming no runs when every read fails", async () => {
    const source: AgentMetricsSource = {
      async listTurns() { throw new Error("HTTP 503"); },
      async listItems() { return page<SessionItem>([], false); },
    };
    const window = metricsWindow("1h", NOW);
    const load = await loadAgentMetricsActivity(source, [session("a"), session("b")], window, new AbortController().signal, { includeTools: true, limits });
    const metrics = aggregateAgentMetrics(window, load.activities, load.coverage);
    expect(load.coverage.failedSessions).toBe(2);
    expect(agentMetricsOutcome(metrics)).toBe("failed");
    expect(agentMetricsOutcome(aggregateAgentMetrics(window, [], { ...coverage, loadedSessions: 0, failedSessions: 0 }))).toBe("empty");
    // A failed Session list means runs may be missing: never claim "no runs".
    expect(agentMetricsOutcome(aggregateAgentMetrics(window, [], { ...coverage, loadedSessions: 0, failedSessions: 0 }), 1)).toBe("failed");
  });

  it("times out a stalled Session read and keeps loading the others", async () => {
    const source: AgentMetricsSource = {
      listTurns(sessionId, options) {
        if (sessionId === "stalled") {
          return new Promise((_, reject) => options?.signal?.addEventListener("abort", () => reject(options.signal?.reason)));
        }
        return Promise.resolve(page([turn(`t-${sessionId}`, sessionId)], false));
      },
      async listItems() { return page<SessionItem>([], false); },
    };
    const load = await loadAgentMetricsActivity(source, [session("stalled"), session("ok")], metricsWindow("1h", NOW), new AbortController().signal, { includeTools: false, limits });
    expect(load.coverage).toMatchObject({ loadedSessions: 1, failedSessions: 1 });
  });

  it("works where AbortSignal.any is unavailable", async () => {
    const original = AbortSignal.any;
    Object.defineProperty(AbortSignal, "any", { value: undefined, configurable: true });
    try {
      const source: AgentMetricsSource = {
        async listTurns(sessionId) { return page([turn("t1", sessionId)], false); },
        async listItems() { return page<SessionItem>([], false); },
      };
      const load = await loadAgentMetricsActivity(source, [session("a")], metricsWindow("1h", NOW), new AbortController().signal, { includeTools: true, limits });
      expect(load.coverage.loadedSessions).toBe(1);
    } finally {
      Object.defineProperty(AbortSignal, "any", { value: original, configurable: true });
    }
  });

  it("keeps a Session's Turns when only its Item read fails", async () => {
    const source: AgentMetricsSource = {
      async listTurns(sessionId) { return page([turn("t1", sessionId)], false); },
      async listItems() { throw new Error("HTTP 500"); },
    };
    const load = await loadAgentMetricsActivity(source, [session("a")], metricsWindow("1h", NOW), new AbortController().signal, { includeTools: true, limits });
    expect(load.coverage).toMatchObject({ loadedSessions: 1, failedSessions: 0, truncatedSessions: 0, itemFailedSessions: 1 });
    expect(load.activities[0]?.turns).toHaveLength(1);
    expect(load.activities[0]?.items).toBeNull();
  });

  it("keeps tokens unknown when no Turn reported usage and separates inline Agents", () => {
    const window = metricsWindow("1h", NOW);
    const inline = (name: string | null) => session(`s-${name}`, { agent: { name, model: "model-a" } as unknown as AgentSession["agent"] });
    const metrics = aggregateAgentMetrics(window, [
      { session: inline("Triage"), turns: [turn("t1", "s-Triage", { usage: null })], items: null, truncated: false },
      { session: inline("Review"), turns: [turn("t2", "s-Review", { usage: null })], items: null, truncated: false },
      { session: inline(null), turns: [turn("t3", "s-null", { usage: null })], items: null, truncated: false },
    ], coverage);
    expect(metrics.totals.tokens.reportedTurns).toBe(0);
    expect(metrics.byAgent.map((entry) => entry.id).sort()).toEqual([INLINE_AGENT_ID, `${INLINE_AGENT_ID}:Review`, `${INLINE_AGENT_ID}:Triage`]);
    expect(metrics.byAgent.every((entry) => entry.reportedTurns === 0)).toBe(true);
  });

  it("uses one model ranking for both model charts", () => {
    const window = metricsWindow("1h", NOW);
    const activities = Array.from({ length: 7 }, (_, index) => {
      const id = `s${index}`;
      const tokens = (index + 1) * 100;
      return {
        session: session(id, { agent: { id: `agent_${index}`, name: `A${index}`, model: `model-${index}` } as AgentSession["agent"] }),
        turns: Array.from({ length: 7 - index }, (_, n) => turn(`${id}-t${n}`, id, { usage: { input_tokens: tokens, output_tokens: 0, total_tokens: tokens, input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } } })),
        items: null,
        truncated: false,
      };
    });
    const metrics = aggregateAgentMetrics(window, activities, coverage);
    const tokenIds = metrics.series.tokensByModel.map((entry) => entry.id);
    const requestIds = metrics.series.requestsByModel.map((entry) => entry.id);
    expect(requestIds).toEqual(tokenIds);
    expect(requestIds).toHaveLength(6);
    expect(requestIds.at(-1)).toBe(OTHER_SERIES_ID);
  });
});

describe("Agent metrics across projects", () => {
  const window = metricsWindow("1h", NOW);

  it("keeps the same Agent of two projects apart and remembers each row's project", () => {
    const shared = { id: "agent_same", name: "Reviewer", model: "model-a" } as AgentSession["agent"];
    const metrics = aggregateAgentMetrics(window, [
      { projectId: "p1", session: session("s1", { agent: shared }), turns: [turn("t1", "s1")], items: null, truncated: false },
      { projectId: "p2", session: session("s2", { agent: shared }), turns: [turn("t2", "s2"), turn("t3", "s2")], items: null, truncated: false },
    ], coverage);
    expect(metrics.byAgent.map((entry) => [entry.id, entry.agentId, entry.projectId, entry.requests])).toEqual([
      ["p2/agent_same", "agent_same", "p2", 2],
      ["p1/agent_same", "agent_same", "p1", 1],
    ]);
  });

  it("routes each Session's reads to its project", async () => {
    const calls: string[] = [];
    const reader = (name: string): AgentMetricsSource => ({
      async listTurns(sessionId) { calls.push(`${name}:turns:${sessionId}`); return page([], false); },
      async listItems(sessionId) { calls.push(`${name}:items:${sessionId}`); return page<SessionItem>([], false); },
    });
    const source = projectMetricsSource([
      { project: project("p1"), value: session("a") },
      { project: project("p2"), value: session("b") },
    ], (target) => reader(target.id));
    await source.listTurns("b");
    await source.listItems("a");
    expect(calls).toEqual(["p2:turns:b", "p1:items:a"]);
    await expect(async () => source.listTurns("unknown")).rejects.toThrow();
  });

  it("skips projects the summary shows as inactive in the range", () => {
    const idle = { total: 3, idle: 3, in_progress: 0, requires_action: 0, failed: 0 };
    expect(projectMayHaveActivity(undefined, window)).toBe(true);
    expect(projectMayHaveActivity(summary("p", { sessions: idle, last_active_at: window.start - 1 }), window)).toBe(false);
    expect(projectMayHaveActivity(summary("p", { sessions: idle, last_active_at: window.start }), window)).toBe(true);
    expect(projectMayHaveActivity(summary("p", { sessions: { ...idle, in_progress: 1 }, last_active_at: window.start - 1 }), window)).toBe(true);
  });

  it("lists active projects, tags activities with their project and reports capped lists and failures", async () => {
    const reads: string[] = [];
    const client = (id: string, sessions: AgentSession[]) => ({
      ...sessionLister(sessions),
      async listTurns(sessionId: string) { reads.push(`${id}:${sessionId}`); return page([turn(`t-${sessionId}`, sessionId)], false); },
      async listItems() { return page<SessionItem>([], false); },
    });
    const clients: Record<string, ReturnType<typeof client>> = {
      busy: client("busy", [session("b1"), session("b2"), session("b3")]),
      idle: client("idle", [session("i1")]),
    };
    const load = await loadProjectAgentMetrics(
      [project("busy"), project("idle"), project("down")],
      window,
      {
        clientFor: (target) => {
          if (target.id === "down") return { ...clients.busy!, listSessionsTolerant: async () => { throw new Error("HTTP 502"); } };
          return clients[target.id]!;
        },
        summary: [
          summary("busy", { sessions: { total: 3, idle: 3, in_progress: 0, requires_action: 0, failed: 0 }, last_active_at: NOW }),
          summary("idle", { sessions: { total: 1, idle: 1, in_progress: 0, requires_action: 0, failed: 0 }, last_active_at: window.start - 10 }),
          summary("down", { sessions: { total: 1, idle: 1, in_progress: 0, requires_action: 0, failed: 0 }, last_active_at: NOW }),
        ],
      },
      new AbortController().signal,
      { includeTools: false, listCap: 2 },
    );
    expect(clients.idle!.calls).toEqual([]);
    expect(reads.sort()).toEqual(["busy:b1", "busy:b2"]);
    expect(load.activities.every((activity) => activity.projectId === "busy")).toBe(true);
    expect(load.truncatedLists.map((entry) => entry.id)).toEqual(["busy"]);
    expect(load.listFailures.map((failure) => failure.project.id)).toEqual(["down"]);
  });
});
