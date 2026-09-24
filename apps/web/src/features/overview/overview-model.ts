import type { AgentSession, RuntimeObservation, SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";

/** Pure projections behind the fleet overview. Missing inputs stay null, never zero. */

export type NodeHealth = "available" | "degraded" | "offline";

export function nodeHealth(node: SandboxNode): NodeHealth {
  if (!node.online) return "offline";
  return node.provider_ready && !node.diagnostic ? "available" : "degraded";
}

export interface CapacitySummary {
  nodes: number;
  online: number;
  available: number;
  active: number;
  /** Active-sandbox limit across online nodes. */
  maxActive: number;
  retained: number;
  maxRetained: number;
  reserved: number;
  cleanupPending: number;
  cpuCount: number | null;
  availableMemoryBytes: number | null;
  availableDiskBytes: number | null;
}

function sumKnown(values: ReadonlyArray<number | null>): number | null {
  const known = values.filter((value): value is number => typeof value === "number");
  return known.length ? known.reduce((sum, value) => sum + value, 0) : null;
}

export function capacitySummary(nodes: readonly SandboxNode[]): CapacitySummary {
  const online = nodes.filter((node) => node.online);
  return {
    nodes: nodes.length,
    online: online.length,
    available: nodes.filter((node) => nodeHealth(node) === "available").length,
    active: nodes.reduce((sum, node) => sum + node.active, 0),
    maxActive: online.reduce((sum, node) => sum + node.max_active, 0),
    retained: nodes.reduce((sum, node) => sum + node.retained, 0),
    maxRetained: online.reduce((sum, node) => sum + node.max_retained, 0),
    reserved: nodes.reduce((sum, node) => sum + node.reserved, 0),
    cleanupPending: nodes.reduce((sum, node) => sum + node.cleanup_pending, 0),
    cpuCount: sumKnown(online.map((node) => node.cpu_count)),
    availableMemoryBytes: sumKnown(online.map((node) => node.available_memory_bytes)),
    availableDiskBytes: sumKnown(online.map((node) => node.available_disk_bytes)),
  };
}

export interface SessionStatusCounts {
  idle: number;
  in_progress: number;
  requires_action: number;
  failed: number;
  other: number;
  total: number;
}

export function sessionStatusCounts(sessions: readonly AgentSession[]): SessionStatusCounts {
  const counts: SessionStatusCounts = { idle: 0, in_progress: 0, requires_action: 0, failed: 0, other: 0, total: sessions.length };
  for (const session of sessions) {
    if (session.status === "idle" || session.status === "in_progress" || session.status === "requires_action" || session.status === "failed") counts[session.status] += 1;
    else counts.other += 1;
  }
  return counts;
}

export function activeSince(sessions: readonly AgentSession[], since: number): number {
  return sessions.filter((session) => session.last_active_at >= since).length;
}

/** Sessions that need an operator: failed or waiting for a required action. */
export function attentionSessions(sessions: readonly AgentSession[], limit = 8): AgentSession[] {
  return sessions
    .filter((session) => session.status === "failed" || session.status === "requires_action")
    .sort((a, b) => b.last_active_at - a.last_active_at || a.id.localeCompare(b.id))
    .slice(0, limit);
}

export interface RuntimeUsage {
  hosted: number;
  active: number;
  sleeping: number;
  pending: number;
  observed: number;
  cpuUsageCores: number | null;
  cpuCapacityCores: number | null;
  memoryUsageBytes: number | null;
  memoryLimitBytes: number | null;
}

/** Current hosted Runtime usage, optionally limited to the Sessions placed on one node. */
export function runtimeUsage(observations: readonly RuntimeObservation[], sessionIds?: ReadonlySet<string>): RuntimeUsage {
  const hosted = observations.filter((observation) => observation.mode === "openai_hosted" && (!sessionIds || sessionIds.has(observation.session_id)));
  const observed = hosted.filter((observation) => observation.status === "observed");
  return {
    hosted: hosted.length,
    active: hosted.filter((observation) => observation.lifecycle_state === "active").length,
    sleeping: hosted.filter((observation) => observation.lifecycle_state === "sleeping").length,
    pending: hosted.filter((observation) => observation.lifecycle_state === "pending" || observation.lifecycle_state === "transitioning").length,
    observed: observed.length,
    cpuUsageCores: sumKnown(observed.map((observation) => observation.cpu?.usage_cores ?? null)),
    cpuCapacityCores: sumKnown(observed.map((observation) => observation.cpu?.capacity_cores ?? null)),
    memoryUsageBytes: sumKnown(observed.map((observation) => observation.memory?.usage_bytes ?? null)),
    memoryLimitBytes: sumKnown(observed.map((observation) => observation.memory?.limit_bytes ?? null)),
  };
}

/** Sum of each Session's last reported cumulative usage; null when none reported. */
export function reportedTokens(sessions: readonly AgentSession[]): { total: number | null; reporting: number } {
  const reporting = sessions.filter((session) => session.usage !== null);
  return {
    total: reporting.length ? reporting.reduce((sum, session) => sum + (session.usage?.total_tokens ?? 0), 0) : null,
    reporting: reporting.length,
  };
}

export function allocationsByNode(allocations: readonly SandboxAllocation[]): Map<string, SandboxAllocation[]> {
  const grouped = new Map<string, SandboxAllocation[]>();
  for (const allocation of allocations) {
    const list = grouped.get(allocation.node_id) ?? [];
    list.push(allocation);
    grouped.set(allocation.node_id, list);
  }
  for (const list of grouped.values()) list.sort((a, b) => b.created_at.localeCompare(a.created_at) || a.id.localeCompare(b.id));
  return grouped;
}

export type ServiceHealth = "healthy" | "degraded" | "down" | "unknown";

/** Only recent failures describe current health; old failed Sessions stay failed forever. */
export const RECENT_FAILURE_WINDOW_SECONDS = 3_600;

export function recentFailures(sessions: readonly AgentSession[], now: number): number {
  return sessions.filter((session) => session.status === "failed" && session.last_active_at >= now - RECENT_FAILURE_WINDOW_SECONDS).length;
}

/**
 * Overall service verdict from evidence the console can read: the Core API,
 * the collections it serves, node availability and recently failing Sessions.
 * It never claims model readiness.
 */
export function serviceHealth(input: {
  coreReachable: boolean | null;
  /** A Core collection read (Agents or Sessions) failed. */
  collectionFailed: boolean;
  capacity: CapacitySummary | null;
  recentFailedSessions: number | null;
}): ServiceHealth {
  if (input.coreReachable === null) return "unknown";
  if (!input.coreReachable) return "down";
  if (input.collectionFailed) return "degraded";
  if (input.capacity && input.capacity.nodes > 0 && input.capacity.available < input.capacity.nodes) return "degraded";
  if (input.recentFailedSessions !== null && input.recentFailedSessions > 0) return "degraded";
  return "healthy";
}

export interface SessionActivity {
  /** Bucket start times (epoch seconds), oldest first. */
  buckets: number[];
  bucketSeconds: number;
  /** Sessions created in each bucket. */
  created: number[];
  /** Failed Sessions by the bucket of their last activity. */
  failed: number[];
}

/** Hourly Session activity over the last `hours`, ending at the current hour. */
export function sessionActivity(sessions: readonly AgentSession[], now: number, hours = 24): SessionActivity {
  const bucketSeconds = 3600;
  const end = Math.floor(now / bucketSeconds) * bucketSeconds + bucketSeconds;
  const start = end - hours * bucketSeconds;
  const buckets = Array.from({ length: hours }, (_, index) => start + index * bucketSeconds);
  const created = new Array<number>(hours).fill(0);
  const failed = new Array<number>(hours).fill(0);
  const slot = (time: number) => Math.floor((time - start) / bucketSeconds);
  for (const session of sessions) {
    const createdSlot = slot(session.created_at);
    if (createdSlot >= 0 && createdSlot < hours) created[createdSlot] = (created[createdSlot] ?? 0) + 1;
    if (session.status === "failed") {
      const failedSlot = slot(session.last_active_at);
      if (failedSlot >= 0 && failedSlot < hours) failed[failedSlot] = (failed[failedSlot] ?? 0) + 1;
    }
  }
  return { buckets, bucketSeconds, created, failed };
}
