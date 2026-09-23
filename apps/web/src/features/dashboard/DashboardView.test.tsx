import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { AgentSession, RuntimeObservation, SavedAgent } from "@agents-core-web/agents-client";

import { DashboardView, type DashboardViewProps } from "./DashboardView";

function agent(overrides: Partial<SavedAgent> = {}): SavedAgent {
  return {
    id: "agent-1",
    object: "agent",
    model: "fixture/model",
    name: "Research Agent",
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

function session(id: string, overrides: Partial<AgentSession> = {}): AgentSession {
  const saved = agent();
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

const callbacks = {
  loadRuntimeHistory: async () => null,
  onRefresh: () => undefined,
  onCreateAgent: () => undefined,
  onStartSession: () => undefined,
  onViewAgents: () => undefined,
  onViewSessions: () => undefined,
  onConfigureConnection: () => undefined,
  onOpenSession: () => undefined,
};

function render(overrides: Partial<DashboardViewProps> = {}): string {
  return renderToStaticMarkup(
    <DashboardView
      agents={[]}
      sessions={[]}
      agentCollectionState="ready"
      agentCollectionError={null}
      agentCollectionHasSnapshot
      sessionCollectionState="ready"
      sessionCollectionError={null}
      sessionCollectionHasSnapshot
      runtimeSnapshot={{ sessions: [], observations: [], loadedAt: 1_700_000_000_000 }}
      runtimeCollectionState="ready"
      runtimeCollectionError={null}
      runtimeCollectionHasSnapshot
      {...callbacks}
      {...overrides}
    />,
  );
}

describe("Dashboard loaded-result presentation", () => {
  it("distinguishes a previously loaded empty result from an unavailable collection", () => {
    const staleEmpty = render({
      agentCollectionState: "failed",
      agentCollectionError: "refresh failed",
      agentCollectionHasSnapshot: true,
    });
    const unavailable = render({
      agentCollectionState: "failed",
      agentCollectionError: "initial load failed",
      agentCollectionHasSnapshot: false,
    });

    expect(staleEmpty).toContain("Using the last successful snapshot");
    expect(staleEmpty).toContain("Agents: refresh failed");
    expect(staleEmpty).toContain(">0<");
    expect(unavailable).toContain("Snapshot incomplete");
    expect(unavailable).toContain("Unavailable");
    expect(unavailable).toContain("Connection settings");
    expect(unavailable).not.toContain("Using the last successful snapshot");
  });

  it("turns local proxy gateway failures into an explicit, clickable backend recovery path", () => {
    const html = render({
      agentCollectionState: "failed",
      agentCollectionError: "Agent core request failed (502).",
      agentCollectionHasSnapshot: false,
      sessionCollectionState: "failed",
      sessionCollectionError: "Agent core request failed (502).",
      sessionCollectionHasSnapshot: false,
    });

    expect(html).toContain("Agent Core backend is not ready");
    expect(html).toContain("local `/v1` proxy cannot reach a ready Core (HTTP 502)");
    expect(html).toContain("Start the Docker backend, then test the connection");
    expect(html).toContain("Open startup guide");
    expect(html).toContain('aria-label="Agent Core backend is not ready. Open Docker startup guide"');
    expect(html).not.toContain("Agents: Agent core request failed (502)");
    expect(html).not.toContain("Sessions: Agent core request failed (502)");
  });

  it("keeps a Runtime-only 503 scoped to the optional observation feature", () => {
    const html = render({
      runtimeSnapshot: null,
      runtimeCollectionState: "failed",
      runtimeCollectionError: "Agent core request failed (503).",
      runtimeCollectionHasSnapshot: false,
    });

    expect(html).toContain("Runtime: Agent core request failed (503).");
    expect(html).toContain("Runtime observations unavailable");
    expect(html).not.toContain("Agent Core backend is not ready");
    expect(html).not.toContain("Core backend is offline");
    expect(html).not.toContain("Open startup guide");
  });

  it("renders a compact actionable overview while preserving Environment qualifications", () => {
    const selfHosted: AgentSession["environment"] = {
      type: "self_hosted",
      id: "0f745b0d-b545-49cd-8d7e-4c31c80dc564",
      remote_url: "https://executor.example.test",
      workspace_directory: "/workspace",
      capability_directories: [],
    };
    const html = render({
      agents: [agent()],
      sessions: [
        session("action-session", {
          metadata: { title: "Needs a result" },
          status: "requires_action",
          environment: selfHosted,
          usage: {
            input_tokens: 30,
            output_tokens: 12,
            total_tokens: 42,
            input_tokens_details: { cached_tokens: 7 },
            output_tokens_details: { reasoning_tokens: 3 },
          },
          last_active_at: 1_700_000_200,
        }),
        session("idle-session", { metadata: { title: "Waiting" }, last_active_at: 1_700_000_100 }),
      ],
    });

    expect(html).toContain("Dashboard");
    expect(html).toContain("Agents and Sessions that may need your attention.");
    expect(html).toContain("Snapshot ready");
    expect(html).toContain("Latest complete paginated reads · not a live Core total or runtime-readiness signal");
    expect(html).toContain('aria-label="Core resource snapshot"');
    expect(html).toContain("Saved definitions");
    expect(html).toContain("In this snapshot");
    expect(html).toContain("Needs attention");
    expect(html).not.toContain("Session-admissible Agents");
    expect(html).toContain("Requires action");
    expect(html).toContain("Self-hosted profile");
    expect(html).toContain("not proof that an executor is connected");
    expect(html).toContain('aria-label="Sessions needing attention"');
    expect(html).toContain('aria-label="Recent Sessions"');
    expect(html).toContain("Needs a result");
    expect(html).toContain("Waiting");
    expect(html).toContain("2023-11-14 22:16 UTC");
    expect(html).toContain("Create agent");
    expect(html).toContain("Start session");
    expect(html).not.toContain("Reported aggregate tokens");
    expect(html).not.toContain("action-session");
    expect(html).not.toContain("Connected Environment");
    expect(html).not.toContain("Execution ready");
  });

  it("renders current Docker resources without inventing a CPU percentage or history", () => {
    const hosted = session("11111111-1111-4111-8111-111111111111", {
      metadata: { title: "Managed research" },
      environment: {
        type: "openai_hosted",
        id: "22222222-2222-4222-8222-222222222222",
        capability_directories: [],
        network: { access: "enabled", allowed_domains: [] },
        packages: { npm: [], python: [], system: [] },
        files: [],
        plugins: [],
        skills: [],
      },
      usage: {
        input_tokens: 30,
        output_tokens: 12,
        total_tokens: 42,
        input_tokens_details: { cached_tokens: 7 },
        output_tokens_details: { reasoning_tokens: 3 },
      },
    });
    const observation: RuntimeObservation = {
      id: hosted.id,
      object: "agent.runtime_observation",
      session_id: hosted.id,
      environment_id: "22222222-2222-4222-8222-222222222222",
      mode: "openai_hosted",
      provider_type: "docker",
      instance: {
        kind: "managed_allocation",
        allocation_id: "33333333-3333-4333-8333-333333333333",
        device_id: null,
        connection_generation: null,
      },
      status: "observed",
      reason: null,
      allocation_created_at: 1_700_000_000,
      resolved_at: 1_700_000_100,
      observed_at: 1_700_000_090,
      started_at: 1_700_000_010,
      cpu: { usage_seconds_total: 73.5, capacity_cores: 2, usage_cores: 3, utilization_ratio: 1.5 },
      memory: { usage_bytes: 536_870_912, limit_bytes: 2_147_483_648 },
    };
    const html = render({
      runtimeSnapshot: { sessions: [hosted], observations: [observation], loadedAt: 1_700_000_100_000 },
    });

    expect(html).toContain("Runtime monitoring");
    expect(html).toContain("1/1 managed observed");
    expect(html).toContain("Cumulative CPU / capacity");
    expect(html).toContain("1m 13s / 2 cores");
    expect(html).toContain("512 MiB / 2.00 GiB");
    expect(html).toContain('aria-label="Runtime durable-history charts"');
    expect(html).toContain("Resource trends");
    expect(html).toContain("Retained samples · durable history");
    expect(html).not.toContain('aria-label="Runtime trend source"');
    expect(html).toContain("History · loading");
    expect(html).toContain("0 buckets");
    expect(html).toContain('aria-label="Runtime durable range"');
    expect(html).toContain('aria-pressed="true">1h</button>');
    expect(html).toContain("CPU usage");
    expect(html).toContain("Memory usage");
    expect(html).not.toContain("Compute uptime");
    expect(html).toContain("Token throughput");
    expect(html).toContain("No retained CPU samples");
    expect(html).toContain("0/2 valid points · 0 snapshots · no history is synthesized");
    expect(html).toContain("Latest value");
    expect(html).toContain("Missing samples");
    expect(html).toContain("Runtime targets");
    expect(html).toContain('<details class="dashboard-runtime-explorer">');
    expect(html).toContain("Search Runtime targets");
    expect(html).toContain("All statuses");
    expect(html).toContain("All modes");
    expect(html).toContain('<table class="dashboard-runtime-table" aria-label="Runtime targets">');
    expect(html).toContain("CPU time");
    expect(html).toContain("Managed research");
    expect(html).toContain("Identity");
    expect(html).toContain("unknown remains unknown, never zero");
    expect(html).not.toContain("CPU %");
    expect(html).not.toContain("CPU now");
    expect(html).not.toContain("historical chart");
  });

  it("keeps partial Usage out of the primary overview", () => {
    const html = render({ sessions: [session("usage-unknown")] });

    expect(html).not.toContain("Reported aggregate tokens");
    expect(html).not.toContain("aggregate Usage");
    expect(html).toContain("Sessions");
  });

  it("does not turn a failed empty collection into a zero-sized healthy snapshot", () => {
    const html = render({
      agentCollectionState: "failed",
      agentCollectionError: "agents unavailable",
      agentCollectionHasSnapshot: false,
      sessionCollectionState: "failed",
      sessionCollectionError: "sessions unavailable",
      sessionCollectionHasSnapshot: false,
    });

    expect(html).toContain("agents unavailable");
    expect(html).toContain("sessions unavailable");
    expect(html).toContain("Unavailable");
    expect(html).toContain("Session snapshot unavailable.");
    expect(html).toContain("Recent Sessions unavailable.");
    expect(html).not.toContain('aria-label="Recent Sessions"');
  });

  it("retains stale loaded facts while making refresh failures explicit", () => {
    const html = render({
      agents: [agent()],
      sessions: [session("stale-session", { metadata: { title: "Last loaded" } })],
      agentCollectionState: "failed",
      agentCollectionError: "Agent refresh failed",
      sessionCollectionState: "failed",
      sessionCollectionError: "Session refresh failed",
    });

    expect(html).toContain("Using the last successful snapshot");
    expect(html.match(/>Stale</g)).toHaveLength(2);
    expect(html).toContain("Agent refresh failed");
    expect(html).toContain("Session refresh failed");
    expect(html).toContain("Last loaded");
    expect(html).toContain("Saved definitions");
    expect(html).toContain("In this snapshot");
  });

  it("does not present retained Runtime samples as live after a refresh failure", () => {
    const runtimeSession = session("runtime-stale", {
      environment: {
        type: "openai_hosted",
        id: "22222222-2222-4222-8222-222222222222",
        capability_directories: [],
        network: { access: "enabled", allowed_domains: [] },
        packages: { npm: [], python: [], system: [] },
        files: [],
        plugins: [],
        skills: [],
      },
    });
    const observation: RuntimeObservation = {
      id: runtimeSession.id,
      object: "agent.runtime_observation",
      session_id: runtimeSession.id,
      environment_id: "22222222-2222-4222-8222-222222222222",
      mode: "openai_hosted",
      provider_type: "docker",
      instance: { kind: "managed_allocation", allocation_id: "33333333-3333-4333-8333-333333333333", device_id: null, connection_generation: null },
      status: "observed",
      reason: null,
      allocation_created_at: 1_700_000_000,
      resolved_at: 1_700_000_100,
      observed_at: 1_700_000_090,
      started_at: 1_700_000_010,
      cpu: null,
      memory: null,
    };
    const html = render({
      runtimeSnapshot: { sessions: [runtimeSession], observations: [observation], loadedAt: 1_700_000_100_000 },
      runtimeCollectionState: "failed",
      runtimeCollectionError: "Runtime refresh failed",
      runtimeCollectionHasSnapshot: true,
    });

    expect(html).toContain("Runtime: Runtime refresh failed");
    expect(html).toContain("History · loading");
    expect(html).not.toContain("Live · 30s");
  });

  it("keeps an existing snapshot visible during a refresh and disables duplicate refresh", () => {
    const html = render({
      agents: [agent()],
      sessions: [session("refreshing")],
      agentCollectionState: "connecting",
      sessionCollectionState: "connecting",
    });

    expect(html).toContain("Refreshing snapshot");
    expect(html.match(/>Refreshing</g)).toHaveLength(2);
    expect(html).toContain('aria-label="Refresh Dashboard snapshot"');
    expect(html).toContain('type="button" disabled=""');
    expect(html).toContain("Last active");
  });

  it("renders unknown Session and Environment values as unavailable", () => {
    const future = session("future", {
      status: "future" as AgentSession["status"],
      environment: { type: "future_environment" } as AgentSession["environment"],
    });
    const html = render({ sessions: [future] });

    expect(html).toContain("Unavailable");
    expect(html).not.toContain("Future environment");
  });
});
