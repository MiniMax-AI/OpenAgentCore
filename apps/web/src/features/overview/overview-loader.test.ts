import { describe, expect, it } from "vitest";

import type { AgentSession } from "@agents-core-web/agents-client";

import { loadOverview, needsSessionRead } from "./overview-loader";
import { activityStart } from "./overview-model";
import { project, session, sessionLister, summary } from "./test-fixtures";

const NOW = 200 * 3600;
const SINCE = activityStart(NOW);

/** Newest first, one Session per hour going back `count` hours. */
function hourly(prefix: string, count: number, overrides: (index: number) => Partial<AgentSession> = () => ({})): AgentSession[] {
  return Array.from({ length: count }, (_, index) => session(`${prefix}${index}`, { created_at: NOW - index * 3600 - 60, last_active_at: NOW - index * 3600, ...overrides(index) }));
}

describe("needsSessionRead", () => {
  it("skips projects without Sessions, or idle since before the window with nothing needing attention", () => {
    expect(needsSessionRead(undefined, SINCE)).toBe(true);
    expect(needsSessionRead(summary("a"), SINCE)).toBe(false);
    const idle = { total: 4, idle: 4, in_progress: 0, requires_action: 0, failed: 0 };
    expect(needsSessionRead(summary("a", { sessions: idle, last_active_at: SINCE - 1 }), SINCE)).toBe(false);
    expect(needsSessionRead(summary("a", { sessions: idle, last_active_at: SINCE + 1 }), SINCE)).toBe(true);
    expect(needsSessionRead(summary("a", { sessions: { ...idle, failed: 1 }, last_active_at: SINCE - 1 }), SINCE)).toBe(true);
  });
});

describe("loadOverview", () => {
  it("reads each active project only as far as the window and its attention Sessions", async () => {
    const busy = project("busy");
    const quiet = project("quiet");
    // 300 Sessions over 300 hours; the one failed Session is 30 hours old.
    const sessions = hourly("s", 300, (index) => (index === 30 ? { status: "failed" } : {}));
    const lister = sessionLister(sessions);
    const quietLister = sessionLister([]);
    const data = await loadOverview([busy, quiet], {
      summary: async () => [
        summary("busy", { sessions: { total: 300, idle: 299, in_progress: 0, requires_action: 0, failed: 1 }, last_active_at: NOW }),
        summary("quiet", { sessions: { total: 5, idle: 5, in_progress: 0, requires_action: 0, failed: 0 }, last_active_at: SINCE - 3600 }),
      ],
      sessions: (target) => (target.id === "busy" ? lister : quietLister),
    }, NOW, new AbortController().signal);

    expect(quietLister.calls).toEqual([]);
    // The first page (100 Sessions) passes the window and holds the failed Session.
    expect(lister.calls).toEqual(["first"]);
    expect(data.summary.status).toBe("ready");
    expect(data.sessions.truncated).toEqual([]);
    expect(data.sessions.sessions.filter((entry) => entry.value.status === "failed").map((entry) => entry.project.id)).toEqual(["busy"]);
  });

  it("keeps reading for an old Session needing attention and reports a read that hit its cap", async () => {
    const sessions = hourly("s", 1_200, (index) => (index === 1_150 ? { status: "requires_action" } : {}));
    const lister = sessionLister(sessions);
    const data = await loadOverview([project("big")], {
      summary: async () => [summary("big", { sessions: { total: 1_200, idle: 1_199, in_progress: 0, requires_action: 1, failed: 0 }, last_active_at: NOW })],
      sessions: () => lister,
    }, NOW, new AbortController().signal);
    expect(lister.calls).toHaveLength(10);
    expect(data.sessions.truncated.map((entry) => entry.id)).toEqual(["big"]);
  });

  it("still reads the window when the summary fails and reports project failures by name", async () => {
    const good = sessionLister(hourly("g", 30));
    const data = await loadOverview([project("good"), project("broken")], {
      summary: async () => { throw new Error("HTTP 500"); },
      sessions: (target) => (target.id === "good" ? good : { listSessionsTolerant: async () => { throw new Error("boom"); } }),
    }, NOW, new AbortController().signal);
    expect(data.summary.status).toBe("failed");
    expect(data.sessions.sessions).toHaveLength(30);
    expect(data.sessions.failures.map((failure) => [failure.project.id, failure.message])).toEqual([["broken", "boom"]]);
  });

  it("propagates an abort", async () => {
    const controller = new AbortController();
    const load = loadOverview([project("a")], {
      summary: async () => { controller.abort(); return []; },
      sessions: () => sessionLister([]),
    }, NOW, controller.signal);
    await expect(load).rejects.toMatchObject({ name: "AbortError" });
  });
});
