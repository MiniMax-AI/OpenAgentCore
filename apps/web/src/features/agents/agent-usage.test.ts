import { describe, expect, it } from "vitest";

import type { AgentSession, ListPage, PageOptions, TokenUsage } from "@agents-core-web/agents-client";

import {
  aggregateAgentUsage,
  type AgentUsageSource,
  continueUsageLoad,
  createUsageLoadCursor,
  usageCoverage,
  usageCursorCovers,
  usageRangeStart,
  usageRecord,
} from "./agent-usage";

const DAY = 86_400;
const NOW = 1_789_438_800;

function tokens(input: number, output: number, cached = 0, reasoning = 0): TokenUsage {
  return {
    input_tokens: input,
    output_tokens: output,
    total_tokens: input + output,
    input_tokens_details: { cached_tokens: cached },
    output_tokens_details: { reasoning_tokens: reasoning },
  };
}

function session(
  id: string,
  agentId: string,
  createdAt: number,
  usage: TokenUsage | null = null,
  status: AgentSession["status"] = "idle",
  lastActiveAt = createdAt + 60,
): AgentSession {
  return {
    id,
    object: "agent.session",
    agent: { id: agentId, model: "provider/model", name: null } as AgentSession["agent"],
    environment: { type: "none" },
    status,
    error: null,
    metadata: {},
    required_actions: [],
    vault_ids: [],
    usage,
    created_at: createdAt,
    last_active_at: lastActiveAt,
  } as AgentSession;
}

function records(sessions: AgentSession[]) {
  return sessions.map(usageRecord);
}

describe("per-Agent usage aggregation", () => {
  it("leaves missing or invalid usage out of token sums but counts it in coverage", () => {
    const invalid = { ...tokens(5, 5), input_tokens: -1 } as TokenUsage;
    const report = aggregateAgentUsage(records([
      session("s1", "agent_a", NOW - 10, tokens(70, 30, 10, 5)),
      session("s2", "agent_a", NOW - 20, null),
      session("s3", "agent_a", NOW - 30, invalid),
    ]), ["agent_a"], null);

    const totals = report.byAgent.get("agent_a")!;
    expect(totals.sessions).toBe(3);
    expect(totals.reported).toBe(1);
    expect(totals.tokens).toEqual({ input: 70, output: 30, total: 100, cached: 10, reasoning: 5 });
    expect(usageCoverage(totals)).toBeCloseTo(1 / 3);
  });

  it("reports no token totals, not zero, when no Session reported usage", () => {
    const report = aggregateAgentUsage(records([
      session("s1", "agent_a", NOW - 10, null),
      session("s2", "agent_a", NOW - 20, null),
    ]), ["agent_a", "agent_idle"], null);

    const unreported = report.byAgent.get("agent_a")!;
    expect(unreported.sessions).toBe(2);
    expect(unreported.reported).toBe(0);
    expect(unreported.tokens).toEqual({ input: null, output: null, total: null, cached: null, reasoning: null });
    expect(usageCoverage(unreported)).toBe(0);

    const empty = report.byAgent.get("agent_idle")!;
    expect(empty.sessions).toBe(0);
    expect(empty.tokens.total).toBeNull();
    expect(empty.lastActiveAt).toBeNull();
    expect(usageCoverage(empty)).toBeNull();
  });

  it("keeps Sessions of deleted or inline Agents in an Other group under their raw ID", () => {
    const report = aggregateAgentUsage(records([
      session("s1", "agent_a", NOW - 10, tokens(1, 1)),
      session("s2", "agent_deleted", NOW - 20, tokens(40, 60)),
      session("s3", "agent_deleted", NOW - 30, null),
      session("s4", "agent_inline_7", NOW - 40, null, "failed"),
    ]), ["agent_a"], null);

    expect(report.other.map((group) => [group.agentId, group.totals.sessions])).toEqual([
      ["agent_deleted", 2],
      ["agent_inline_7", 1],
    ]);
    expect(report.other[0]!.totals.tokens.total).toBe(100);
    expect(report.other[1]!.totals.statuses.failed).toBe(1);
    expect(report.otherTotals.sessions).toBe(3);
    expect(report.otherTotals.reported).toBe(1);
    expect(report.total.sessions).toBe(4);
    expect(report.total.tokens.total).toBe(102);
    expect(report.byAgent.has("agent_deleted")).toBe(false);
  });

  it("selects Sessions by creation time with an inclusive range start", () => {
    const start = usageRangeStart("7d", NOW)!;
    expect(start).toBe(NOW - 7 * DAY);
    expect(usageRangeStart("30d", NOW)).toBe(NOW - 30 * DAY);
    expect(usageRangeStart("all", NOW)).toBeNull();

    const loaded = records([
      session("recent", "agent_a", NOW - DAY, tokens(10, 0)),
      session("edge", "agent_a", start, tokens(1, 0)),
      // Created before the range but active inside it: still outside the range.
      session("old", "agent_a", start - 1, tokens(1_000, 0), "idle", NOW),
      session("old_other", "agent_gone", start - DAY, tokens(5, 0)),
    ]);
    const week = aggregateAgentUsage(loaded, ["agent_a"], start);
    expect(week.byAgent.get("agent_a")!.sessions).toBe(2);
    expect(week.byAgent.get("agent_a")!.tokens.total).toBe(11);
    expect(week.byAgent.get("agent_a")!.lastActiveAt).toBe(NOW - DAY + 60);
    expect(week.other).toEqual([]);

    const all = aggregateAgentUsage(loaded, ["agent_a"], null);
    expect(all.byAgent.get("agent_a")!.sessions).toBe(3);
    expect(all.byAgent.get("agent_a")!.lastActiveAt).toBe(NOW);
    expect(all.other.map((group) => group.agentId)).toEqual(["agent_gone"]);
  });

  it("counts every status and keeps unrecognized statuses visible", () => {
    const unknown = { ...session("s5", "agent_a", NOW - 50), status: "paused" } as unknown as AgentSession;
    const report = aggregateAgentUsage(records([
      session("s1", "agent_a", NOW - 10, null, "in_progress"),
      session("s2", "agent_a", NOW - 20, null, "requires_action"),
      session("s3", "agent_a", NOW - 30, null, "failed"),
      session("s4", "agent_a", NOW - 40, null, "idle"),
      unknown,
    ]), ["agent_a"], null);

    expect(report.byAgent.get("agent_a")!.statuses).toEqual({
      idle: 1,
      in_progress: 1,
      requires_action: 1,
      failed: 1,
      unknown: 1,
    });
  });

  it("drops a token sum that leaves the safe integer range instead of rounding it", () => {
    const huge = tokens(Number.MAX_SAFE_INTEGER - 1, 0);
    huge.total_tokens = Number.MAX_SAFE_INTEGER - 1;
    const report = aggregateAgentUsage(records([
      session("s1", "agent_a", NOW - 10, huge),
      session("s2", "agent_a", NOW - 20, huge),
    ]), ["agent_a"], null);

    const totals = report.byAgent.get("agent_a")!;
    expect(totals.reported).toBe(2);
    expect(totals.tokens.total).toBeNull();
    expect(totals.tokens.output).toBe(0);
  });
});

/** A newest-first Session collection with Core's cursor semantics. */
function fakeSource(sessions: AgentSession[], hooks: {
  beforePage?: (call: number, options: PageOptions) => Promise<void> | void;
} = {}) {
  const calls: PageOptions[] = [];
  const source: AgentUsageSource = {
    async listSessions(options = {}) {
      calls.push({ after: options.after, limit: options.limit, order: options.order });
      await hooks.beforePage?.(calls.length, options);
      const start = options.after ? sessions.findIndex((candidate) => candidate.id === options.after) + 1 : 0;
      const data = sessions.slice(start, start + (options.limit ?? 20));
      const page: ListPage<AgentSession> = {
        object: "list",
        data,
        has_more: start + data.length < sessions.length,
        first_id: data[0]?.id ?? null,
        last_id: data.at(-1)?.id ?? null,
      };
      return page;
    },
  };
  return { source, calls };
}

/** One Session per hour, newest first. */
function hourly(count: number, agentId = "agent_a"): AgentSession[] {
  return Array.from({ length: count }, (_, index) => session(`s${String(index).padStart(4, "0")}`, agentId, NOW - index * 3_600, tokens(1, 1)));
}

describe("per-Agent usage loading", () => {
  it("reads newest-first pages of 100 until Core reports the end", async () => {
    const { source, calls } = fakeSource(hourly(250));
    const progress: Array<{ pages: number; sessions: number }> = [];
    const cursor = await continueUsageLoad(source, createUsageLoadCursor(), null, { onProgress: (next) => progress.push(next) });

    expect(calls).toEqual([
      { after: undefined, limit: 100, order: "desc" },
      { after: "s0099", limit: 100, order: "desc" },
      { after: "s0199", limit: 100, order: "desc" },
    ]);
    expect(cursor.records).toHaveLength(250);
    expect(cursor.exhausted).toBe(true);
    expect(progress).toEqual([{ pages: 1, sessions: 100 }, { pages: 2, sessions: 200 }, { pages: 3, sessions: 250 }]);
  });

  it("stops paging once a page passes the range start", async () => {
    // 1,000 hourly Sessions span ~41 days; the 7-day range ends inside the second page.
    const { source, calls } = fakeSource(hourly(1_000));
    const start = usageRangeStart("7d", NOW)!;
    const cursor = await continueUsageLoad(source, createUsageLoadCursor(), start);

    expect(calls).toHaveLength(2);
    expect(cursor.exhausted).toBe(false);
    expect(usageCursorCovers(cursor, start)).toBe(true);
    const report = aggregateAgentUsage(cursor.records, ["agent_a"], start);
    expect(report.byAgent.get("agent_a")!.sessions).toBe(7 * 24 + 1);
  });

  it("serves a narrower range from memory and continues a wider one from the cursor", async () => {
    const { source, calls } = fakeSource(hourly(1_000));
    const cursor = createUsageLoadCursor();
    await continueUsageLoad(source, cursor, usageRangeStart("30d", NOW));
    expect(calls).toHaveLength(8);

    await continueUsageLoad(source, cursor, usageRangeStart("7d", NOW));
    expect(calls).toHaveLength(8);

    await continueUsageLoad(source, cursor, null);
    expect(calls).toHaveLength(10);
    expect(calls[8]!.after).toBe("s0799");
    expect(cursor.records).toHaveLength(1_000);
    expect(new Set(cursor.records.map((record) => record.id)).size).toBe(1_000);
  });

  it("cancels on a page boundary and resumes without re-reading committed pages", async () => {
    const controller = new AbortController();
    const { source, calls } = fakeSource(hourly(250), {
      beforePage: (call, options) => {
        if (call !== 2) return;
        return new Promise<void>((_, reject) => {
          options.signal?.addEventListener("abort", () => reject(options.signal?.reason));
          controller.abort();
        });
      },
    });
    const cursor = createUsageLoadCursor();

    await expect(continueUsageLoad(source, cursor, null, { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
    expect(cursor.pages).toBe(1);
    expect(cursor.records).toHaveLength(100);
    expect(cursor.after).toBe("s0099");

    await continueUsageLoad(source, cursor, null, { signal: new AbortController().signal });
    expect(calls.map((call) => call.after)).toEqual([undefined, "s0099", "s0099", "s0199"]);
    expect(cursor.records).toHaveLength(250);
  });

  it("does not commit a failed page, so a retry resumes where the read stopped", async () => {
    let failures = 1;
    const { source, calls } = fakeSource(hourly(150), {
      beforePage: (call) => {
        if (call === 2 && failures-- > 0) throw new Error("Fixture Session list failed.");
      },
    });
    const cursor = createUsageLoadCursor();

    await expect(continueUsageLoad(source, cursor, null)).rejects.toThrow("Fixture Session list failed.");
    expect(cursor.pages).toBe(1);

    await continueUsageLoad(source, cursor, null);
    expect(calls.map((call) => call.after)).toEqual([undefined, "s0099", "s0099"]);
    expect(cursor.records).toHaveLength(150);
  });

  it("rejects duplicate identities and cursors that cannot advance", async () => {
    const duplicate: AgentUsageSource = {
      listSessions: async () => ({ data: [session("same", "agent_a", NOW), session("same", "agent_a", NOW - 1)], has_more: false }),
    };
    await expect(continueUsageLoad(duplicate, createUsageLoadCursor(), null)).rejects.toThrow("duplicate or invalid Session identities");

    const stuck: AgentUsageSource = { listSessions: async () => ({ data: [], has_more: true }) };
    await expect(continueUsageLoad(stuck, createUsageLoadCursor(), null)).rejects.toThrow("invalid Session pagination cursor");
  });
});
