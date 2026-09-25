import { describe, expect, it } from "vitest";

import { AgentCoreError } from "@agents-core-web/agents-client";

import { capacitySummary } from "../fleet/fleet-model";
import { project, session, summary } from "./test-fixtures";
import {
  activityStart,
  attentionCount,
  attentionSessions,
  coverageRatio,
  isGatewayOrNetworkFailure,
  overviewReadDone,
  projectUsageRows,
  recentFailures,
  serviceHealth,
  sessionActivity,
  summaryTotals,
  webApiReachable,
} from "./overview-model";

const usage = (total: number) => ({ input_tokens: total, output_tokens: 0, total_tokens: total, cached_tokens: 0, reasoning_tokens: 0 });

describe("project usage rows", () => {
  it("joins every project with its summary row, active and recent first, missing rows as null", () => {
    const rows = projectUsageRows(
      [project("old"), project("archived", { status: "archived" }), project("recent"), project("silent")],
      [
        summary("old", { last_active_at: 100 }),
        summary("recent", { last_active_at: 500 }),
        summary("archived", { last_active_at: 900 }),
        // Agent rows never stand in for a project row.
        summary("silent", { agent_id: "agent_x", last_active_at: 999 }),
      ],
    );
    expect(rows.map((row) => row.project.id)).toEqual(["recent", "old", "silent", "archived"]);
    expect(rows.find((row) => row.project.id === "silent")?.summary).toBeNull();
  });

  it("adds up Session counts and keeps usage unknown when no project reported any", () => {
    const withUsage = summaryTotals([
      summary("a", { sessions: { total: 3, idle: 1, in_progress: 1, requires_action: 0, failed: 1 }, usage: usage(10), coverage: { sessions: 3, reported: 2 } }),
      summary("b", { sessions: { total: 2, idle: 1, in_progress: 0, requires_action: 1, failed: 0 }, coverage: { sessions: 2, reported: 0 } }),
      summary("a", { agent_id: "agent_a", sessions: { total: 50, idle: 50, in_progress: 0, requires_action: 0, failed: 0 }, usage: usage(99) }),
    ]);
    expect(withUsage.sessions).toEqual({ total: 5, idle: 2, in_progress: 1, requires_action: 1, failed: 1 });
    expect(withUsage.usage?.total_tokens).toBe(10);
    expect(withUsage.coverage).toEqual({ sessions: 5, reported: 2 });
    expect(attentionCount(withUsage.sessions)).toBe(2);
    expect(summaryTotals([summary("b")]).usage).toBeNull();
  });

  it("reports coverage only when there are Sessions", () => {
    expect(coverageRatio({ sessions: 4, reported: 3 })).toBe(0.75);
    expect(coverageRatio({ sessions: 0, reported: 0 })).toBeNull();
  });
});

describe("Sessions needing attention", () => {
  it("lists failed and waiting Sessions of every project by recent activity", () => {
    const a = project("a");
    const b = project("b");
    const listed = attentionSessions([
      { project: a, value: session("s1", { status: "failed", last_active_at: 300 }) },
      { project: b, value: session("s2", { status: "requires_action", last_active_at: 400 }) },
      { project: b, value: session("s3", { status: "in_progress", last_active_at: 500 }) },
    ]);
    expect(listed.map((entry) => [entry.project.id, entry.value.id])).toEqual([["b", "s2"], ["a", "s1"]]);
  });

  it("stops a project's read once the window is passed and every attention Session was found", () => {
    const since = 1_000;
    const recent = [session("new", { created_at: 1_500 })];
    expect(overviewReadDone(recent, since, 0)).toBe(false);
    const passed = [...recent, session("old", { created_at: 900 })];
    expect(overviewReadDone(passed, since, 0)).toBe(true);
    expect(overviewReadDone(passed, since, 1)).toBe(false);
    expect(overviewReadDone([...passed, session("failed", { created_at: 10, status: "failed" })], since, 1)).toBe(true);
    // Without a summary the read stops at the window.
    expect(overviewReadDone(passed, since, null)).toBe(true);
  });
});

describe("service health", () => {
  const healthy = capacitySummary([]);
  it("treats only gateway and network failures as an unreachable Web API", () => {
    expect(isGatewayOrNetworkFailure(new AgentCoreError("Core returned HTTP 502.", 502))).toBe(true);
    expect(isGatewayOrNetworkFailure(new AgentCoreError("Not found.", 404))).toBe(false);
    expect(isGatewayOrNetworkFailure(new TypeError("Failed to fetch"))).toBe(true);
    expect(isGatewayOrNetworkFailure("Core returned HTTP 503.")).toBe(true);
    expect(isGatewayOrNetworkFailure("Core returned HTTP 500.")).toBe(false);
  });

  it("decides reachability from any settled read", () => {
    expect(webApiReachable([{ status: "pending" }, { status: "pending" }])).toBeNull();
    expect(webApiReachable([{ status: "failed", error: new TypeError("Failed to fetch") }, { status: "pending" }])).toBe(false);
    expect(webApiReachable([{ status: "failed", error: new TypeError("Failed to fetch") }, { status: "ready" }])).toBe(true);
    expect(webApiReachable([{ status: "failed", error: new AgentCoreError("Bad request.", 400) }])).toBe(true);
  });

  it("reports unknown while checking, down when unreachable and degraded on problems", () => {
    expect(serviceHealth({ coreReachable: null, collectionFailed: false, capacity: null, recentFailedSessions: null })).toBe("unknown");
    expect(serviceHealth({ coreReachable: false, collectionFailed: true, capacity: healthy, recentFailedSessions: 0 })).toBe("down");
    expect(serviceHealth({ coreReachable: true, collectionFailed: false, capacity: healthy, recentFailedSessions: 0 })).toBe("healthy");
    expect(serviceHealth({ coreReachable: true, collectionFailed: true, capacity: healthy, recentFailedSessions: null })).toBe("degraded");
    expect(serviceHealth({ coreReachable: true, collectionFailed: false, capacity: null, recentFailedSessions: 2 })).toBe("degraded");
  });

  it("counts only failures from the last hour", () => {
    const now = 10_000;
    expect(recentFailures([
      session("old", { status: "failed", last_active_at: now - 7_200 }),
      session("new", { status: "failed", last_active_at: now - 60 }),
      session("idle", { last_active_at: now }),
    ], now)).toBe(1);
  });
});

describe("sessionActivity", () => {
  it("buckets creations and failures by hour over the last day", () => {
    const now = 100 * 3600 + 1800;
    const activity = sessionActivity([
      session("a", { created_at: now - 60, last_active_at: now - 30 }),
      session("b", { created_at: now - 3 * 3600, status: "failed", last_active_at: now - 2 * 3600 }),
      session("c", { created_at: now - 30 * 3600 }),
    ], now);
    expect(activity.buckets).toHaveLength(24);
    expect(activity.buckets[0]).toBe(activityStart(now));
    expect(activity.buckets.at(-1)).toBe(100 * 3600);
    expect(activity.created.at(-1)).toBe(1);
    expect(activity.created.at(-4)).toBe(1);
    expect(activity.created.reduce((sum, value) => sum + value, 0)).toBe(2);
    expect(activity.failed.at(-3)).toBe(1);
  });
});
