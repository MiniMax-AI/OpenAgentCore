import type { AgentSession, CoreProjectReader, SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";

import type { RuntimeDashboardSnapshot } from "../dashboard/runtime-snapshot";
import { type OwnedRuntimeObservation } from "../../lib/admin-view";

/**
 * Hosted Runtimes across every project: the observations come from the Web
 * API's deployment-wide list (each labelled with its project); the Sessions
 * they belong to are read by ID through their project, bounded. Only
 * Core-managed hosted sandboxes report CPU and memory.
 */

/** Most hosted Sessions read by ID on one load; the rest are listed by ID only. */
export const HOSTED_SESSION_LIMIT = 100;
const SESSION_READ_CONCURRENCY = 6;

export type SessionReader = Pick<CoreProjectReader, "retrieveSession">;

export interface HostedRuntimeLoad {
  observations: OwnedRuntimeObservation[];
  /** Sessions of the observations, by Session ID; absent when not read or not readable. */
  sessions: ReadonlyMap<string, AgentSession>;
  /** Hosted Sessions not read because of the limit. */
  unread: number;
  /** Hosted Sessions whose read failed. */
  failed: number;
  /** Epoch milliseconds. */
  loadedAt: number;
}

const lifecycleOrder: Record<string, number> = { active: 0, transitioning: 1, pending: 2, sleeping: 3, stopped: 4 };

function byRelevance(a: OwnedRuntimeObservation, b: OwnedRuntimeObservation): number {
  return (lifecycleOrder[a.lifecycle_state ?? "stopped"] ?? 5) - (lifecycleOrder[b.lifecycle_state ?? "stopped"] ?? 5)
    || (b.observed_at ?? b.resolved_at) - (a.observed_at ?? a.resolved_at)
    || a.session_id.localeCompare(b.session_id);
}

async function mapBounded<T, R>(values: readonly T[], concurrency: number, mapper: (value: T) => Promise<R>): Promise<R[]> {
  const result = new Array<R>(values.length);
  let next = 0;
  await Promise.all(Array.from({ length: Math.min(concurrency, values.length) }, async () => {
    while (next < values.length) {
      const index = next;
      next += 1;
      result[index] = await mapper(values[index]!);
    }
  }));
  return result;
}

export async function loadHostedRuntimes(
  deps: { observations: (signal: AbortSignal) => Promise<OwnedRuntimeObservation[]>; sessionReader: (projectId: string) => SessionReader },
  signal: AbortSignal,
  limit = HOSTED_SESSION_LIMIT,
): Promise<HostedRuntimeLoad> {
  const hosted = (await deps.observations(signal)).filter((observation) => observation.mode === "openai_hosted").sort(byRelevance);
  const selected = hosted.slice(0, limit);
  let failed = 0;
  const sessions = new Map<string, AgentSession>();
  await mapBounded(selected, SESSION_READ_CONCURRENCY, async (observation) => {
    try {
      sessions.set(observation.session_id, await deps.sessionReader(observation.project_id).retrieveSession(observation.session_id, { signal }));
    } catch (error) {
      if (signal.aborted) throw error;
      failed += 1;
    }
  });
  signal.throwIfAborted();
  return { observations: hosted, sessions, unread: hosted.length - selected.length, failed, loadedAt: Date.now() };
}

/** The trend snapshot of one project, or of every project when `projectId` is empty. */
export function runtimeSnapshot(load: HostedRuntimeLoad, projectId: string): RuntimeDashboardSnapshot {
  return runtimeSnapshotWhere(load, (observation) => !projectId || observation.project_id === projectId);
}

/** The snapshot of only the Runtimes `keep` accepts, e.g. those placed on one node. */
export function runtimeSnapshotWhere(load: HostedRuntimeLoad, keep: (observation: OwnedRuntimeObservation) => boolean): RuntimeDashboardSnapshot {
  const observations = load.observations.filter(keep);
  return {
    observations,
    sessions: observations.flatMap((observation) => {
      const session = load.sessions.get(observation.session_id);
      return session ? [session] : [];
    }),
    owners: new Map(observations.map((observation) => [observation.session_id, observation.project_id])),
    loadedAt: load.loadedAt,
  };
}

function sumKnown(values: ReadonlyArray<number | null | undefined>): number | null {
  const known = values.filter((value): value is number => typeof value === "number" && Number.isFinite(value));
  return known.length ? known.reduce((sum, value) => sum + value, 0) : null;
}

export interface HostedRuntimeUsage {
  hosted: number;
  active: number;
  sleeping: number;
  /** Transitioning or waiting for an allocation. */
  pending: number;
  /** Runtimes with a current CPU and memory sample. */
  observed: number;
  cpuUsageCores: number | null;
  cpuCapacityCores: number | null;
  memoryUsageBytes: number | null;
  memoryLimitBytes: number | null;
}

/** Current hosted Runtime usage; figures no Runtime reported stay null. */
export function hostedRuntimeUsage(observations: readonly OwnedRuntimeObservation[]): HostedRuntimeUsage {
  const hosted = observations.filter((observation) => observation.mode === "openai_hosted");
  const observed = hosted.filter((observation) => observation.status === "observed");
  return {
    hosted: hosted.length,
    active: hosted.filter((observation) => observation.lifecycle_state === "active").length,
    sleeping: hosted.filter((observation) => observation.lifecycle_state === "sleeping").length,
    pending: hosted.filter((observation) => observation.lifecycle_state === "pending" || observation.lifecycle_state === "transitioning").length,
    observed: observed.length,
    cpuUsageCores: sumKnown(observed.map((observation) => observation.cpu?.usage_cores)),
    cpuCapacityCores: sumKnown(observed.map((observation) => observation.cpu?.capacity_cores)),
    memoryUsageBytes: sumKnown(observed.map((observation) => observation.memory?.usage_bytes)),
    memoryLimitBytes: sumKnown(observed.map((observation) => observation.memory?.limit_bytes)),
  };
}

export interface HostedRuntimeRow {
  observation: OwnedRuntimeObservation;
  session: AgentSession | null;
  /** The host of the Runtime's allocation, when the fleet lists it. */
  node: SandboxNode | null;
  /** Seconds from the Runtime's start to its last sample. */
  uptimeSeconds: number | null;
}

export function hostedRuntimeRows(
  load: HostedRuntimeLoad,
  projectId: string,
  fleet: { nodes: readonly SandboxNode[]; allocations: readonly SandboxAllocation[] } | null,
): HostedRuntimeRow[] {
  const nodes = new Map((fleet?.nodes ?? []).map((node) => [node.id, node]));
  const nodeOfAllocation = new Map((fleet?.allocations ?? []).map((allocation) => [allocation.id, nodes.get(allocation.node_id) ?? null]));
  return load.observations
    .filter((observation) => !projectId || observation.project_id === projectId)
    .map((observation) => ({
      observation,
      session: load.sessions.get(observation.session_id) ?? null,
      node: observation.instance.allocation_id ? nodeOfAllocation.get(observation.instance.allocation_id) ?? null : null,
      uptimeSeconds: observation.status === "observed" && observation.started_at !== null && observation.observed_at !== null && observation.observed_at >= observation.started_at
        ? observation.observed_at - observation.started_at
        : null,
    }));
}

export function sessionTitle(session: AgentSession | null): string | null {
  if (!session) return null;
  const title = session.metadata?.title;
  if (typeof title === "string" && title.trim()) return title;
  return session.agent?.name?.trim() ? session.agent.name : null;
}

/** Local search over a Runtime row's Session, Agent, host and IDs. */
export function matchesRuntime(row: HostedRuntimeRow, query: string): boolean {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return true;
  return [
    sessionTitle(row.session),
    row.session?.agent?.name,
    row.session?.agent?.model,
    row.node?.name,
    row.observation.session_id,
    row.observation.environment_id,
    row.observation.instance.allocation_id,
  ].some((value) => typeof value === "string" && value.toLocaleLowerCase().includes(needle));
}
