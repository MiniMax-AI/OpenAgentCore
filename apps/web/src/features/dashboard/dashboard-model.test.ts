import { describe, expect, it } from "vitest";

import {
  type AgentSession,
  OpenAIAgentsClient,
  type RuntimeObservation,
  type SavedAgent,
  type TokenUsage,
} from "@agents-core-web/agents-client";

import {
  buildDashboardSnapshot,
  buildRuntimeDashboardModel,
  dashboardEnvironmentLabel,
  dashboardEnvironmentProfile,
  dashboardStatusLabel,
  formatDashboardTimestamp,
  formatDashboardBytes,
  formatDashboardDuration,
  reportedSessionTokens,
  runtimeObservationStatusLabel,
} from "./dashboard-model";
import { holdLastReported } from "./held-usage";

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

  it("counts Sessions created from an advanced Environment Template as managed", async () => {
    // The environment of a Session created from the advanced Template fixture
    // (packages/agents-client fixture session_environment_from_advanced_template).
    const advanced = {
      type: "openai_hosted",
      id: "9d6b4c3f-5e70-4fb1-8c43-0d9e8f7a6b52",
      capability_directories: ["/workspace/capabilities"],
      network: { access: "restricted", allowed_domains: ["pypi.org", "files.pythonhosted.org"] },
      packages: { npm: ["typescript@5.8.3"], python: ["packaging==26.0"], system: ["jq"] },
      files: [
        { id: "0e7c5d40-6f81-4ac2-9d54-1e0f9a8b7c63", type: "inline", path: "/workspace/config/settings.json", size_bytes: 128 },
        {
          id: "1f8d6e51-7092-4bd3-8e65-2f1a0b9c8d74",
          type: "file_id",
          path: "/workspace/data/input.csv",
          file_id: "file-2b7c9d10-4e5f-4a6b-8c7d-9e0f1a2b3c4d",
          size_bytes: 42,
        },
      ],
      plugins: [{ type: "inline", name: "release-notes", description: "Draft release notes from merged changes." }],
      skills: [
        { type: "skill_reference", skill_id: "skill_3f1c2a9e-7b4d-4e8a-9c21-5d6e7f8a9b0c", version: "1", name: "report", description: "Create the report." },
        { type: "inline", name: "triage", description: "Sort incoming issues." },
      ],
    };
    const sessionId = "5f9c1d2e-4b5a-4c7d-8e9f-0a1b2c3d4e5f";
    const client = new OpenAIAgentsClient({
      fetch: (async () => new Response(
        JSON.stringify({ ...session(sessionId), environment: advanced }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      )) as typeof fetch,
    });
    const projected = await client.retrieveSession(sessionId);

    expect(dashboardEnvironmentProfile(projected.environment)).toBe("openai_hosted");
    expect(buildDashboardSnapshot([], [projected]).recentSessions[0]?.environmentProfile).toBe("openai_hosted");

    const basic = {
      type: "openai_hosted",
      id: advanced.id,
      capability_directories: [],
      network: { access: "enabled", allowed_domains: [] },
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
    };
    for (const section of ["capability_directories", "network", "packages", "files", "plugins", "skills"] as const) {
      expect(dashboardEnvironmentProfile({ ...basic, [section]: advanced[section] })).toBe("openai_hosted");
    }
    for (const unknown of [
      { ...advanced, environment_template_id: "future" },
      { ...advanced, network: { access: "future", allowed_domains: [] } },
      { ...advanced, network: { access: "disabled", allowed_domains: ["pypi.org"] } },
      { ...advanced, packages: { npm: [], python: [] } },
      { ...advanced, files: ["settings.json"] },
      { ...advanced, skills: undefined },
      { ...advanced, id: "" },
    ]) expect(dashboardEnvironmentProfile(unknown)).toBe("unsupported");
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
      lifecycle_state: "active",
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
      lifecycle_state: null,
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
      sandboxTotalCount: 1,
      activeSandboxCount: 1,
      sleepingSandboxCount: 0,
      transitioningSandboxCount: 0,
      pendingSandboxCount: 0,
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

  it("counts a shared managed allocation once while retaining both Session rows", () => {
    const first = session("11111111-1111-4111-8111-111111111111", { usage: usage(21) });
    const second = session("22222222-2222-4222-8222-222222222222", { usage: usage(5) });
    const allocationId = "44444444-4444-4444-8444-444444444444";
    const firstObservation: RuntimeObservation = {
      id: first.id,
      object: "agent.runtime_observation",
      session_id: first.id,
      environment_id: "33333333-3333-4333-8333-333333333333",
      mode: "openai_hosted",
      provider_type: "docker",
      instance: { kind: "managed_allocation", allocation_id: allocationId, device_id: null, connection_generation: null },
      lifecycle_state: "active",
      status: "observed",
      reason: null,
      allocation_created_at: 100,
      resolved_at: 220,
      observed_at: 210,
      started_at: 150,
      cpu: { usage_seconds_total: 3.5, capacity_cores: 2, usage_cores: null, utilization_ratio: null },
      memory: { usage_bytes: 512, limit_bytes: 2048 },
    };
    const secondObservation: RuntimeObservation = {
      ...firstObservation,
      id: second.id,
      session_id: second.id,
      environment_id: "55555555-5555-4555-8555-555555555555",
      resolved_at: 221,
      observed_at: 211,
      cpu: { usage_seconds_total: 4.5, capacity_cores: 2, usage_cores: null, utilization_ratio: null },
      memory: { usage_bytes: 768, limit_bytes: 2048 },
    };

    const model = buildRuntimeDashboardModel([first, second], [firstObservation, secondObservation]);

    expect(model.rows).toHaveLength(2);
    expect(model.summary).toMatchObject({
      sessionCount: 2,
      managedRuntimeCount: 1,
      sandboxTotalCount: 1,
      activeSandboxCount: 1,
      observedRuntimeCount: 1,
      cpuUsageSecondsTotal: 4.5,
      cpuCapacityCores: 2,
      cpuCoverageCount: 1,
      memoryUsageBytes: 768,
      memoryLimitBytes: 2048,
      memoryCoverageCount: 1,
      totalTokens: 26,
      tokenCoverageCount: 2,
    });
  });

  it("holds each Session's last reported tokens in the summary while public usage is null", () => {
    const running = session("11111111-1111-4111-8111-111111111111", { usage: usage(21) });
    const other = session("22222222-2222-4222-8222-222222222222", { usage: usage(5) });
    const observation = (id: string): RuntimeObservation => ({
      id,
      object: "agent.runtime_observation",
      session_id: id,
      environment_id: null,
      mode: "none",
      provider_type: null,
      instance: { kind: "none", allocation_id: null, device_id: null, connection_generation: null },
      lifecycle_state: null,
      status: "unsupported",
      reason: "runtime_mode_not_observable",
      allocation_created_at: null,
      resolved_at: 225,
      observed_at: null,
      started_at: null,
      cpu: null,
      memory: null,
    });
    const observations = [observation(running.id), observation(other.id)];
    let held = holdLastReported(new Map<string, number>(), reportedSessionTokens([running, other]));
    expect(buildRuntimeDashboardModel([running, other], observations, held).summary)
      .toMatchObject({ totalTokens: 26, tokenCoverageCount: 2 });

    // A Turn starts: public usage is withheld, the summary keeps the last total.
    const withheld = { ...running, status: "in_progress", usage: null } as AgentSession;
    held = holdLastReported(held, reportedSessionTokens([withheld, { ...other, usage: usage(9) }]));
    const model = buildRuntimeDashboardModel([withheld, { ...other, usage: usage(9) }], observations, held);
    expect(model.summary).toMatchObject({ totalTokens: 30, tokenCoverageCount: 2 });
    expect(model.rows.find((row) => row.session.id === withheld.id)?.session.totalTokens).toBeNull();
    expect(buildRuntimeDashboardModel([withheld, other], observations).summary.totalTokens).toBe(5);

    // A newly reported value replaces the held one; unlisted Sessions are dropped.
    held = holdLastReported(held, reportedSessionTokens([{ ...running, usage: usage(40) }]));
    expect([...held]).toEqual([[running.id, 40]]);
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
      lifecycle_state: "stopped",
      status: "unavailable",
      reason: "runtime_not_running",
      allocation_created_at: 100,
      resolved_at: 10_000,
      observed_at: null,
      started_at: null,
      cpu: null,
      memory: null,
    };

    const model = buildRuntimeDashboardModel([stopped], [observation]);
    expect(model.rows[0]?.allocationAgeSeconds).toBeNull();
    expect(model.summary).toMatchObject({ sandboxTotalCount: 0, activeSandboxCount: 0, sleepingSandboxCount: 0 });
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
      lifecycle_state: "active",
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
