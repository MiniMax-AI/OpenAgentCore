import { describe, expect, it } from "vitest";

import type { AgentSession, RuntimeObservation, SavedAgent, TokenUsage } from "@agents-core-web/agents-client";

import {
  buildDashboardSnapshot,
  buildRuntimeDashboardModel,
  dashboardEnvironmentLabel,
  dashboardEnvironmentProfile,
  dashboardStatusLabel,
  formatDashboardTimestamp,
  formatDashboardBytes,
  formatDashboardDuration,
  runtimeObservationStatusLabel,
} from "./dashboard-model";

function agent(id: string, overrides: Partial<SavedAgent> = {}): SavedAgent {
  return {
    id,
    object: "agent",
    model: "fixture/model",
    name: `Agent ${id}`,
    instructions: null,
    metadata: {},
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [],
    created_at: 1_700_000_000,
    updated_at: 1_700_000_000,
    ...overrides,
  };
}

function usage(totalTokens: number): TokenUsage {
  return {
    input_tokens: 10,
    output_tokens: 5,
    total_tokens: totalTokens,
    input_tokens_details: { cached_tokens: 2 },
    output_tokens_details: { reasoning_tokens: 1 },
  };
}

function session(id: string, overrides: Partial<AgentSession> = {}): AgentSession {
  const saved = agent(`for-${id}`);
  return {
    id,
    object: "agent.session",
    agent: {
      id: saved.id,
      model: saved.model,
      name: saved.name,
      instructions: saved.instructions,
      multi_agent: saved.multi_agent,
      reasoning: saved.reasoning,
      service_tier: saved.service_tier,
      text: saved.text,
      tools: saved.tools,
    },
    environment: { type: "none" },
    status: "idle",
    error: null,
    metadata: {},
    required_actions: [],
    vault_ids: [],
    usage: null,
    created_at: 1_700_000_000,
    last_active_at: 1_700_000_000,
    ...overrides,
  };
}

describe("Dashboard loaded-snapshot model", () => {
  it("counts loaded Agents without publishing a partial Session-admission estimate", () => {
    const malformed = { ...agent("malformed"), multi_agent: null } as unknown as SavedAgent;
    const snapshot = buildDashboardSnapshot([
      agent("compatible"),
      agent("saved-only", { tools: [{ type: "tool_search" }] }),
      agent("multi-agent", { multi_agent: { enabled: true, max_concurrent_subagents: 2 } }),
      agent("credentialed-mcp", {
        tools: [{
          type: "mcp",
          server_label: "qualified",
          transport: { type: "http", server_url: "https://mcp.example.test", headers: {} },
          allowed_tools: null,
          connection_origin: "service",
          credential_id: "credential-1",
          request_metadata: {},
          required: false,
        }],
      }),
      malformed,
    ], []);

    expect(snapshot.loadedAgentCount).toBe(5);
    expect(snapshot).not.toHaveProperty("sessionAdmissibleAgentCount");
  });

  it("keeps exact Session statuses, unknown variants, and attention ordering", () => {
    const snapshot = buildDashboardSnapshot([], [
      session("idle", { status: "idle", last_active_at: 100 }),
      session("progress", { status: "in_progress", last_active_at: 500 }),
      session("action", { status: "requires_action", last_active_at: 400 }),
      session("failed", { status: "failed", last_active_at: 300 }),
      session("future", { status: "future" as AgentSession["status"], last_active_at: 200 }),
    ]);

    expect(snapshot.statusCounts).toEqual({
      idle: 1,
      in_progress: 1,
      requires_action: 1,
      failed: 1,
      unknown: 1,
    });
    expect(snapshot.attentionSessions.map((row) => row.id)).toEqual(["action", "failed"]);
    expect(snapshot.recentSessions.map((row) => row.id)).toEqual([
      "progress", "future", "idle",
    ]);
    expect(dashboardStatusLabel("idle")).toBe("Idle");
    expect(dashboardStatusLabel("unknown")).toBe("Unavailable");
  });

  it("aggregates only complete canonical Usage and keeps absent or unsafe totals unknown", () => {
    const partial = { input_tokens: 7, total_tokens: 7 } as AgentSession["usage"];
    const snapshot = buildDashboardSnapshot([], [
      session("measured", { usage: usage(21) }),
      session("partial", { usage: partial }),
      session("missing", { usage: null }),
    ]);

    expect(snapshot.usage).toEqual({ reportedSessionCount: 1, totalTokens: 21 });
    expect(snapshot.recentSessions.find((row) => row.id === "partial")?.totalTokens).toBeNull();
    expect(buildDashboardSnapshot([], []).usage).toEqual({ reportedSessionCount: 0, totalTokens: null });

    const overflow = buildDashboardSnapshot([], [
      session("large-a", { usage: usage(Number.MAX_SAFE_INTEGER) }),
      session("large-b", { usage: usage(Number.MAX_SAFE_INTEGER) }),
    ]);
    expect(overflow.usage).toEqual({ reportedSessionCount: 2, totalTokens: null });
  });

  it("labels only complete known Environment projections and never infers connection", () => {
    expect(dashboardEnvironmentProfile({ type: "none" })).toBe("none");
    expect(dashboardEnvironmentProfile({
      type: "self_hosted",
      id: "0f745b0d-b545-49cd-8d7e-4c31c80dc564",
      remote_url: "https://executor.example.test",
      workspace_directory: "/workspace",
      capability_directories: [],
    })).toBe("self_hosted");
    expect(dashboardEnvironmentProfile({ type: "self_hosted", id: "environment-1" })).toBe("unsupported");
    expect(dashboardEnvironmentProfile({
      type: "self_hosted",
      id: "0f745b0d-b545-49cd-8d7e-4c31c80dc564",
      remote_url: "https://user:secret@executor.example.test",
      workspace_directory: "/workspace",
      capability_directories: [],
    })).toBe("unsupported");
    expect(dashboardEnvironmentProfile({
      type: "self_hosted",
      id: "0f745b0d-b545-49cd-8d7e-4c31c80dc564",
      remote_url: "https://executor.example.test",
      workspace_directory: "relative",
      capability_directories: [],
    })).toBe("unsupported");
    expect(dashboardEnvironmentProfile({
      type: "self_hosted",
      id: "0f745b0d-b545-49cd-8d7e-4c31c80dc564",
      remote_url: "https://executor.example.test",
      workspace_directory: "/workspace",
      capability_directories: ["/extra"],
    })).toBe("unsupported");
    expect(dashboardEnvironmentProfile({ type: "hosted" })).toBe("unsupported");
    expect(dashboardEnvironmentProfile({
      type: "openai_hosted",
      id: "7a263c51-6bf0-4d53-8518-c792eb1f0d21",
      capability_directories: [],
      network: { access: "disabled", allowed_domains: [] },
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
    })).toBe("openai_hosted");
    expect(dashboardEnvironmentProfile({
      type: "openai_hosted",
      id: "7a263c51-6bf0-4d53-8518-c792eb1f0d21",
      capability_directories: [],
      network: { access: "restricted", allowed_domains: [] },
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
    })).toBe("unsupported");
    expect(dashboardEnvironmentLabel("self_hosted")).toBe("Self-hosted profile");
    expect(dashboardEnvironmentLabel("openai_hosted")).toBe("Managed hosted");
    expect(dashboardEnvironmentLabel("unsupported")).toBe("Unavailable");
  });

  it("uses safe UTC timestamps and sends malformed activity to the end", () => {
    const snapshot = buildDashboardSnapshot([], [
      session("unknown-time", { last_active_at: -1 }),
      session("known-time", { last_active_at: 1_700_000_000 }),
    ]);

    expect(snapshot.recentSessions.map((row) => row.id)).toEqual(["known-time", "unknown-time"]);
    expect(formatDashboardTimestamp(1_700_000_000)).toBe("2023-11-14 22:13 UTC");
    expect(formatDashboardTimestamp(-1)).toBe("Unknown");
    expect(formatDashboardTimestamp(null)).toBe("Unknown");
  });

  it("uses bounded title and identity fallbacks without inventing values", () => {
    const titled = session("titled", {
      metadata: { title: "Production triage" },
      agent: { ...session("nested").agent, name: "Support Agent", model: "fixture/support" },
    });
    const untitled = session("untitled", {
      metadata: {},
      agent: { ...session("nested-2").agent, name: null, model: "fixture/fallback" },
    });
    const snapshot = buildDashboardSnapshot([], [titled, untitled]);

    expect(snapshot.recentSessions.find((row) => row.id === "titled")).toMatchObject({
      title: "Production triage",
      agentLabel: "Support Agent",
      model: "fixture/support",
    });
    expect(snapshot.recentSessions.find((row) => row.id === "untitled")).toMatchObject({
      title: "Untitled Session",
      agentLabel: "fixture/fallback",
      model: "fixture/fallback",
    });
  });

  it("aggregates only present Runtime measurements and preserves coverage", () => {
    const managed = session("11111111-1111-4111-8111-111111111111", { usage: usage(21) });
    const unsupported = session("22222222-2222-4222-8222-222222222222");
    const observations: RuntimeObservation[] = [{
      id: managed.id,
      object: "agent.runtime_observation",
      session_id: managed.id,
      environment_id: "33333333-3333-4333-8333-333333333333",
      mode: "openai_hosted",
      provider_type: "docker",
      instance: { kind: "managed_allocation", allocation_id: "44444444-4444-4444-8444-444444444444", device_id: null, connection_generation: null },
      status: "observed",
      reason: null,
      allocation_created_at: 100,
      resolved_at: 220,
      observed_at: 210,
      started_at: 150,
      cpu: { usage_seconds_total: 3.5, capacity_cores: 2, usage_cores: null, utilization_ratio: null },
      memory: { usage_bytes: 512, limit_bytes: 2048 },
    }, {
      id: unsupported.id,
      object: "agent.runtime_observation",
      session_id: unsupported.id,
      environment_id: null,
      mode: "none",
      provider_type: null,
      instance: { kind: "none", allocation_id: null, device_id: null, connection_generation: null },
      status: "unsupported",
      reason: "runtime_mode_not_observable",
      allocation_created_at: null,
      resolved_at: 225,
      observed_at: null,
      started_at: null,
      cpu: null,
      memory: null,
    }];
    const model = buildRuntimeDashboardModel([managed, unsupported], observations);

    expect(model.summary).toMatchObject({
      sessionCount: 2,
      managedRuntimeCount: 1,
      observedRuntimeCount: 1,
      unavailableRuntimeCount: 0,
      unsupportedRuntimeCount: 1,
      cpuUsageSecondsTotal: 3.5,
      cpuCapacityCores: 2,
      cpuCoverageCount: 1,
      memoryUsageBytes: 512,
      memoryLimitBytes: 2048,
      memoryCoverageCount: 1,
      totalTokens: 21,
      tokenCoverageCount: 1,
      oldestResolvedAt: 220,
      newestResolvedAt: 225,
    });
    expect(model.rows[0]?.computeUptimeSeconds).toBe(60);
    expect(model.rows[0]?.allocationAgeSeconds).toBe(120);
    expect(runtimeObservationStatusLabel(observations[0]!)).toBe("Observed");
    expect(formatDashboardBytes(2048)).toBe("2.00 KiB");
    expect(formatDashboardDuration(90)).toBe("1m 30s");
  });

  it("does not infer a released allocation lifetime from the current resolution time", () => {
    const stopped = session("11111111-1111-4111-8111-111111111111");
    const observation: RuntimeObservation = {
      id: stopped.id,
      object: "agent.runtime_observation",
      session_id: stopped.id,
      environment_id: "33333333-3333-4333-8333-333333333333",
      mode: "openai_hosted",
      provider_type: "docker",
      instance: {
        kind: "managed_allocation",
        allocation_id: "44444444-4444-4444-8444-444444444444",
        device_id: null,
        connection_generation: null,
      },
      status: "unavailable",
      reason: "runtime_not_running",
      allocation_created_at: 100,
      resolved_at: 10_000,
      observed_at: null,
      started_at: null,
      cpu: null,
      memory: null,
    };

    expect(buildRuntimeDashboardModel([stopped], [observation]).rows[0]?.allocationAgeSeconds).toBeNull();
  });

  it("does not count capacity-only or limit-only samples as usage coverage", () => {
    const managed = session("11111111-1111-4111-8111-111111111111", {
      environment: {
        type: "openai_hosted",
        id: "33333333-3333-4333-8333-333333333333",
        capability_directories: [],
        network: { access: "disabled", allowed_domains: [] },
        packages: { npm: [], python: [], system: [] },
        files: [],
        plugins: [],
        skills: [],
      },
    });
    const observation: RuntimeObservation = {
      id: managed.id,
      object: "agent.runtime_observation",
      session_id: managed.id,
      environment_id: "33333333-3333-4333-8333-333333333333",
      mode: "openai_hosted",
      provider_type: "docker",
      instance: {
        kind: "managed_allocation",
        allocation_id: "44444444-4444-4444-8444-444444444444",
        device_id: null,
        connection_generation: null,
      },
      status: "observed",
      reason: null,
      allocation_created_at: null,
      resolved_at: 220,
      observed_at: 210,
      started_at: null,
      cpu: { usage_seconds_total: null, capacity_cores: 2, usage_cores: null, utilization_ratio: null },
      memory: { usage_bytes: null, limit_bytes: 2048 },
    };

    expect(buildRuntimeDashboardModel([managed], [observation]).summary).toMatchObject({
      cpuUsageSecondsTotal: null,
      cpuCapacityCores: 2,
      cpuCoverageCount: 0,
      memoryUsageBytes: null,
      memoryLimitBytes: 2048,
      memoryCoverageCount: 0,
    });
  });
});
