import type { AgentSession, SandboxNode } from "@oac/agents-client";
import { type OwnedRuntimeObservation, type Project, type ProjectSummary } from "../../lib/admin-view";

/** Fixtures shared by the Monitor page tests. Not part of the application bundle. */

export function project(id: string, overrides: Partial<Project> = {}): Project {
  return { id, name: id, status: "active", created_at: 1, archived_at: null, active_key_count: 1, ...overrides };
}

export function summary(projectId: string, overrides: Partial<ProjectSummary> = {}): ProjectSummary {
  return {
    project_id: projectId,
    key_id: null,
    agent_id: null,
    key: null,
    assets: { agents: 1, skills: 0, environment_templates: 0, files: 0, vaults: 0, credentials: 0 },
    sessions: { total: 0, idle: 0, in_progress: 0, requires_action: 0, failed: 0 },
    usage: null,
    coverage: { sessions: 0, reported: 0 },
    last_active_at: null,
    ...overrides,
  };
}

export function session(id: string, overrides: Partial<AgentSession> = {}): AgentSession {
  return {
    id, object: "agent.session",
    agent: { id: "agent_a", name: "Reviewer", model: "model-a" } as AgentSession["agent"],
    environment: { type: "none" } as AgentSession["environment"],
    status: "idle", error: null, metadata: {}, required_actions: [], vault_ids: [], usage: null,
    created_at: 100, last_active_at: 200,
    ...overrides,
  };
}

export function node(id: string, overrides: Partial<SandboxNode> = {}): SandboxNode {
  return {
    id, name: id, provider: "docker", online: true, provider_ready: true,
    rollout: { state: overrides.online === false ? "unknown" : "ready", ready_generation: 1 },
    cpu_count: 8, available_memory_bytes: 1024, available_disk_bytes: 2048,
    running: 1, snapshots: 0, last_seen_at: "2026-09-24T00:00:00Z",
    max_active: 4, max_retained: 8, active: 1, reserved: 0, retained: 0, cleanup_pending: 0,
    created_at: "2026-09-01T00:00:00Z", core_url: "https://core.example", enrollment_id: null,
    ...overrides,
  };
}

export function hostedObservation(sessionId: string, projectId: string, overrides: Partial<OwnedRuntimeObservation> = {}): OwnedRuntimeObservation {
  return {
    id: sessionId, object: "agent.runtime_observation", session_id: sessionId, resolved_at: 1_000,
    environment_id: `env_${sessionId}`, mode: "openai_hosted", provider_type: "docker",
    instance: { kind: "managed_allocation", allocation_id: `alloc_${sessionId}`, device_id: null, connection_generation: null },
    lifecycle_state: "active", status: "observed", reason: null, allocation_created_at: 100, observed_at: 1_000, started_at: 400,
    cpu: { usage_seconds_total: 10, capacity_cores: 2, usage_cores: 0.5, utilization_ratio: null },
    memory: { usage_bytes: 100, limit_bytes: 400 },
    project_id: projectId,
    ...overrides,
  } as OwnedRuntimeObservation;
}

/** A newest-first Session lister over a fixed list, recording the pages it served. */
export function sessionLister(sessions: readonly AgentSession[], calls: string[] = []) {
  return {
    calls,
    async listSessionsTolerant(options?: { after?: string; limit?: number; signal?: AbortSignal }) {
      options?.signal?.throwIfAborted();
      const start = options?.after ? sessions.findIndex((entry) => entry.id === options.after) + 1 : 0;
      const data = sessions.slice(start, start + (options?.limit ?? 20));
      calls.push(options?.after ?? "first");
      return { object: "list" as const, data, unrecognized: [], has_more: start + data.length < sessions.length, first_id: data[0]?.id ?? null, last_id: data.at(-1)?.id ?? null };
    },
  };
}
