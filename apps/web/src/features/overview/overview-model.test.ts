import { describe, expect, it } from "vitest";

import type { AgentSession, RuntimeObservation, SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";

import { environmentKind, filterSessions } from "../resources/SessionsLogView";
import {
  allocationsByNode,
  attentionSessions,
  capacitySummary,
  nodeHealth,
  recentFailures,
  reportedTokens,
  runtimeUsage,
  serviceHealth,
  sessionActivity,
  sessionStatusCounts,
} from "./overview-model";

function node(id: string, overrides: Partial<SandboxNode> = {}): SandboxNode {
  return {
    id, name: id, provider: "docker", online: true, provider_ready: true, diagnostic: "",
    cpu_count: 8, available_memory_bytes: 1024, available_disk_bytes: 2048,
    running: 1, snapshots: 0, last_seen_at: "2026-09-24T00:00:00Z",
    max_active: 4, max_retained: 8, active: 1, reserved: 0, retained: 0, cleanup_pending: 0,
    created_at: "2026-09-01T00:00:00Z",
    ...overrides,
  };
}

function session(id: string, overrides: Partial<AgentSession> = {}): AgentSession {
  return {
    id, object: "agent.session",
    agent: { id: "agent_a", name: "Reviewer", model: "model-a" } as AgentSession["agent"],
    environment: { type: "none" } as AgentSession["environment"],
    status: "idle", error: null, metadata: {}, required_actions: [], vault_ids: [], usage: null,
    created_at: 100, last_active_at: 200,
    ...overrides,
  };
}

const usage = (total: number) => ({ input_tokens: total, output_tokens: 0, total_tokens: total, input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } });

describe("capacity and node health", () => {
  it("counts limits only on online nodes and keeps missing host metrics null", () => {
    const summary = capacitySummary([
      node("a", { active: 3, max_active: 4, retained: 1, max_retained: 8 }),
      node("b", { online: false, active: 0, max_active: 4, cpu_count: null, available_memory_bytes: null, available_disk_bytes: null }),
      node("c", { provider_ready: false, cpu_count: null }),
    ]);
    expect(summary).toMatchObject({ nodes: 3, online: 2, available: 1, active: 4, maxActive: 8, retained: 1, maxRetained: 16, cpuCount: 8 });
    expect(capacitySummary([]).cpuCount).toBeNull();
  });

  it("classifies node health", () => {
    expect(nodeHealth(node("a"))).toBe("available");
    expect(nodeHealth(node("a", { diagnostic: "provider_unavailable" }))).toBe("degraded");
    expect(nodeHealth(node("a", { online: false }))).toBe("offline");
  });

  it("groups allocations by node, newest first", () => {
    const allocation = (id: string, nodeId: string, created: string): SandboxAllocation => ({
      id, node_id: nodeId, tenant_id: "t", session_id: `s_${id}`, environment_id: "e", state: "active",
      compute_phase: "running", diagnostic: "" as SandboxAllocation["diagnostic"], initialization: "ready", created_at: created,
    });
    const grouped = allocationsByNode([allocation("1", "a", "2026-01-01"), allocation("2", "a", "2026-02-01"), allocation("3", "b", "2026-01-01")]);
    expect(grouped.get("a")?.map((entry) => entry.id)).toEqual(["2", "1"]);
    expect(grouped.get("b")).toHaveLength(1);
  });
});

describe("service health", () => {
  const healthy = capacitySummary([node("a")]);
  it("reports unknown while checking and down when Core is unreachable", () => {
    expect(serviceHealth({ coreReachable: null, collectionFailed: false, capacity: null, recentFailedSessions: null })).toBe("unknown");
    expect(serviceHealth({ coreReachable: false, collectionFailed: true, capacity: healthy, recentFailedSessions: 0 })).toBe("down");
  });
  it("degrades on failed collections, unavailable nodes or recent failures", () => {
    expect(serviceHealth({ coreReachable: true, collectionFailed: false, capacity: healthy, recentFailedSessions: 0 })).toBe("healthy");
    expect(serviceHealth({ coreReachable: true, collectionFailed: true, capacity: healthy, recentFailedSessions: null })).toBe("degraded");
    expect(serviceHealth({ coreReachable: true, collectionFailed: false, capacity: capacitySummary([node("a"), node("b", { online: false })]), recentFailedSessions: 0 })).toBe("degraded");
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

describe("Session projections", () => {
  const sessions = [
    session("s1", { status: "failed", error: "boom", last_active_at: 300, usage: usage(10) }),
    session("s2", { status: "requires_action", required_actions: [], last_active_at: 400 }),
    session("s3", { status: "in_progress", last_active_at: 500 }),
    session("s4", { usage: usage(5), agent: { id: "agent_b", name: "Writer", model: "model-b" } as AgentSession["agent"], environment: { type: "openai_hosted" } as AgentSession["environment"] }),
  ];

  it("counts statuses and sorts attention by recency", () => {
    expect(sessionStatusCounts(sessions)).toMatchObject({ idle: 1, in_progress: 1, requires_action: 1, failed: 1, total: 4 });
    expect(attentionSessions(sessions).map((entry) => entry.id)).toEqual(["s2", "s1"]);
  });

  it("sums only reported token usage", () => {
    expect(reportedTokens(sessions)).toEqual({ total: 15, reporting: 2 });
    expect(reportedTokens([session("x")])).toEqual({ total: null, reporting: 0 });
  });

  it("filters the Session log", () => {
    expect(filterSessions(sessions, { status: "all", agentId: "", environment: "", query: "" }).map((entry) => entry.id)).toEqual(["s3", "s2", "s1", "s4"]);
    expect(filterSessions(sessions, { status: "failed", agentId: "", environment: "", query: "" }).map((entry) => entry.id)).toEqual(["s1"]);
    expect(filterSessions(sessions, { status: "all", agentId: "agent_b", environment: "", query: "" }).map((entry) => entry.id)).toEqual(["s4"]);
    expect(filterSessions(sessions, { status: "all", agentId: "", environment: "openai_hosted", query: "" }).map((entry) => entry.id)).toEqual(["s4"]);
    expect(filterSessions(sessions, { status: "all", agentId: "", environment: "", query: "BOOM" }).map((entry) => entry.id)).toEqual(["s1"]);
    expect(environmentKind(session("x", { environment: { type: "future" } as unknown as AgentSession["environment"] }))).toBe("other");
  });

});

describe("runtimeUsage", () => {
  const hosted = (sessionId: string, lifecycle: "active" | "sleeping", cpu: number | null): RuntimeObservation => ({
    id: sessionId, object: "agent.runtime_observation", session_id: sessionId, resolved_at: 1,
    environment_id: "env", mode: "openai_hosted", provider_type: "docker",
    instance: { kind: "managed_allocation", allocation_id: "alloc", device_id: null, connection_generation: null },
    lifecycle_state: lifecycle, status: "observed", reason: null, allocation_created_at: 1, observed_at: 1, started_at: 1,
    cpu: cpu === null ? null : { usage_seconds_total: 1, capacity_cores: 2, usage_cores: cpu, utilization_ratio: null },
    memory: { usage_bytes: 100, limit_bytes: 400 },
  });
  const none: RuntimeObservation = {
    id: "n", object: "agent.runtime_observation", session_id: "n", resolved_at: 1, environment_id: null, mode: "none", provider_type: null,
    instance: { kind: "none", allocation_id: null, device_id: null, connection_generation: null }, lifecycle_state: null,
    status: "unsupported", reason: "runtime_mode_not_observable", allocation_created_at: null, observed_at: null, started_at: null, cpu: null, memory: null,
  };

  it("aggregates hosted observations and can scope to a node's Sessions", () => {
    const observations = [hosted("a", "active", 0.5), hosted("b", "sleeping", null), none];
    expect(runtimeUsage(observations)).toMatchObject({ hosted: 2, active: 1, sleeping: 1, observed: 2, cpuUsageCores: 0.5, cpuCapacityCores: 2, memoryUsageBytes: 200, memoryLimitBytes: 800 });
    expect(runtimeUsage(observations, new Set(["b"]))).toMatchObject({ hosted: 1, cpuUsageCores: null });
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
    expect(activity.buckets.at(-1)).toBe(100 * 3600);
    expect(activity.created.at(-1)).toBe(1);
    expect(activity.created.at(-4)).toBe(1);
    expect(activity.created.reduce((sum, value) => sum + value, 0)).toBe(2);
    expect(activity.failed.at(-3)).toBe(1);
  });
});
