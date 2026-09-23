import type {
  AgentSession,
  RuntimeObservation,
  SavedAgent,
  SessionStatus,
  TokenUsage,
} from "@agents-core-web/agents-client";

import { isSupportedSelfHostedEnvironmentProjection } from "../sessions/environment/environment-launcher";
import { isSupportedOpenAIHostedEnvironmentProjection } from "../sessions/environment/environment-state";

export type DashboardCollectionState = "connecting" | "ready" | "failed";
export type DashboardEnvironmentProfile = "none" | "self_hosted" | "openai_hosted" | "unsupported";
export type DashboardSessionStatus = SessionStatus | "unknown";

export interface DashboardStatusCounts {
  idle: number;
  in_progress: number;
  requires_action: number;
  failed: number;
  unknown: number;
}

export interface DashboardSessionRow {
  id: string | null;
  title: string;
  agentLabel: string;
  model: string;
  status: DashboardSessionStatus;
  environmentProfile: DashboardEnvironmentProfile;
  lastActiveAt: number | null;
  totalTokens: number | null;
}

export interface DashboardUsageSummary {
  reportedSessionCount: number;
  totalTokens: number | null;
}

export interface DashboardSnapshot {
  loadedAgentCount: number;
  loadedSessionCount: number;
  statusCounts: DashboardStatusCounts;
  usage: DashboardUsageSummary;
  attentionSessions: DashboardSessionRow[];
  recentSessions: DashboardSessionRow[];
}

export interface RuntimeDashboardRow {
  session: DashboardSessionRow;
  observation: RuntimeObservation;
  computeUptimeSeconds: number | null;
  allocationAgeSeconds: number | null;
}

export interface RuntimeDashboardSummary {
  sessionCount: number;
  managedRuntimeCount: number;
  observedRuntimeCount: number;
  unavailableRuntimeCount: number;
  unsupportedRuntimeCount: number;
  cpuUsageSecondsTotal: number | null;
  cpuCapacityCores: number | null;
  cpuCoverageCount: number;
  memoryUsageBytes: number | null;
  memoryLimitBytes: number | null;
  memoryCoverageCount: number;
  totalTokens: number | null;
  tokenCoverageCount: number;
  oldestResolvedAt: number | null;
  newestResolvedAt: number | null;
}

export interface RuntimeDashboardModel {
  summary: RuntimeDashboardSummary;
  rows: RuntimeDashboardRow[];
}

const sessionStatuses = new Set<SessionStatus>([
  "idle",
  "in_progress",
  "requires_action",
  "failed",
]);

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function nonEmptyString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function safeNonNegativeInteger(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
}

function canonicalUsage(value: unknown): TokenUsage | null {
  const usage = record(value);
  const inputDetails = record(usage?.input_tokens_details);
  const outputDetails = record(usage?.output_tokens_details);
  if (!usage || !inputDetails || !outputDetails) return null;
  if (
    safeNonNegativeInteger(usage.input_tokens) === null ||
    safeNonNegativeInteger(usage.output_tokens) === null ||
    safeNonNegativeInteger(usage.total_tokens) === null ||
    safeNonNegativeInteger(inputDetails.cached_tokens) === null ||
    safeNonNegativeInteger(outputDetails.reasoning_tokens) === null
  ) return null;
  return value as TokenUsage;
}

function canonicalTimestamp(value: unknown): number | null {
  const seconds = safeNonNegativeInteger(value);
  if (seconds === null) return null;
  const date = new Date(seconds * 1_000);
  return Number.isNaN(date.getTime()) ? null : seconds;
}

function canonicalStatus(value: unknown): DashboardSessionStatus {
  return typeof value === "string" && sessionStatuses.has(value as SessionStatus)
    ? value as SessionStatus
    : "unknown";
}

export function dashboardEnvironmentProfile(value: unknown): DashboardEnvironmentProfile {
  const environment = record(value);
  if (!environment) return "unsupported";
  if (environment.type === "none") return "none";
  if (
    environment.type === "self_hosted" &&
    isSupportedSelfHostedEnvironmentProjection(
      environment.id,
      environment.remote_url,
      environment.workspace_directory,
      environment.capability_directories,
    )
  ) return "self_hosted";
  if (isSupportedOpenAIHostedEnvironmentProjection(environment)) return "openai_hosted";
  return "unsupported";
}

function sessionTitle(value: unknown): string {
  const session = record(value);
  const metadata = record(session?.metadata);
  const agent = record(session?.agent);
  return nonEmptyString(metadata?.title)
    ?? nonEmptyString(agent?.name)
    ?? "Untitled Session";
}

function sessionAgentLabel(value: unknown): string {
  const session = record(value);
  const agent = record(session?.agent);
  return nonEmptyString(agent?.name)
    ?? nonEmptyString(agent?.model)
    ?? "Unavailable";
}

function sessionModel(value: unknown): string {
  const session = record(value);
  const agent = record(session?.agent);
  return nonEmptyString(agent?.model) ?? "Unavailable";
}

function toSessionRow(session: AgentSession): DashboardSessionRow {
  const usage = canonicalUsage(session.usage);
  return {
    id: nonEmptyString(session.id),
    title: sessionTitle(session),
    agentLabel: sessionAgentLabel(session),
    model: sessionModel(session),
    status: canonicalStatus(session.status),
    environmentProfile: dashboardEnvironmentProfile(session.environment),
    lastActiveAt: canonicalTimestamp(session.last_active_at),
    totalTokens: usage?.total_tokens ?? null,
  };
}

function compareSessionRows(left: DashboardSessionRow, right: DashboardSessionRow): number {
  if (left.lastActiveAt === null && right.lastActiveAt !== null) return 1;
  if (left.lastActiveAt !== null && right.lastActiveAt === null) return -1;
  if (left.lastActiveAt !== right.lastActiveAt) {
    return (right.lastActiveAt ?? 0) - (left.lastActiveAt ?? 0);
  }
  return (left.id ?? "").localeCompare(right.id ?? "");
}

export function buildDashboardSnapshot(
  agents: readonly SavedAgent[],
  sessions: readonly AgentSession[],
  recentLimit = 8,
  attentionLimit = 5,
): DashboardSnapshot {
  const statusCounts: DashboardStatusCounts = {
    idle: 0,
    in_progress: 0,
    requires_action: 0,
    failed: 0,
    unknown: 0,
  };
  const rows = sessions.map((session) => {
    const row = toSessionRow(session);
    statusCounts[row.status] += 1;
    return row;
  }).sort(compareSessionRows);

  let reportedSessionCount = 0;
  let totalTokens = 0;
  let totalTokensKnown = true;
  for (const session of sessions) {
    const usage = canonicalUsage(session.usage);
    if (!usage) continue;
    reportedSessionCount += 1;
    const nextTotal = totalTokens + usage.total_tokens;
    if (!Number.isSafeInteger(nextTotal)) totalTokensKnown = false;
    else totalTokens = nextTotal;
  }

  return {
    loadedAgentCount: agents.length,
    loadedSessionCount: sessions.length,
    statusCounts,
    usage: {
      reportedSessionCount,
      totalTokens: reportedSessionCount > 0 && totalTokensKnown ? totalTokens : null,
    },
    attentionSessions: rows
      .filter((session) => session.status === "requires_action" || session.status === "failed")
      .slice(0, Math.max(0, attentionLimit)),
    recentSessions: rows
      .filter((session) => session.status !== "requires_action" && session.status !== "failed")
      .slice(0, Math.max(0, recentLimit)),
  };
}

export function dashboardStatusLabel(status: DashboardSessionStatus): string {
  switch (status) {
    case "idle": return "Idle";
    case "in_progress": return "In progress";
    case "requires_action": return "Requires action";
    case "failed": return "Failed";
    case "unknown": return "Unavailable";
  }
}

export function dashboardEnvironmentLabel(profile: DashboardEnvironmentProfile): string {
  switch (profile) {
    case "none": return "None";
    case "self_hosted": return "Self-hosted profile";
    case "openai_hosted": return "Managed hosted";
    case "unsupported": return "Unavailable";
  }
}

export function formatDashboardTimestamp(value: number | null): string {
  const seconds = canonicalTimestamp(value);
  if (seconds === null) return "Unknown";
  return `${new Date(seconds * 1_000).toISOString().slice(0, 16).replace("T", " ")} UTC`;
}

function safeFiniteNonNegative(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : null;
}

function safeAdd(left: number, right: number): number | null {
  const value = left + right;
  return Number.isSafeInteger(left) && Number.isSafeInteger(right) && Number.isSafeInteger(value) ? value : null;
}

function elapsedSeconds(start: number | null, end: number | null): number | null {
  if (start === null || end === null || end < start) return null;
  return end - start;
}

/** Each Session's reported total tokens, null when its public usage is null. */
export function reportedSessionTokens(sessions: readonly AgentSession[]): Map<string, number | null> {
  return new Map(sessions.map((session) => [session.id, canonicalUsage(session.usage)?.total_tokens ?? null]));
}

/**
 * Rows show current public usage. The summary token total adds each Session's
 * held last reported total (see holdLastReported) while its usage is null.
 */
export function buildRuntimeDashboardModel(
  sessions: readonly AgentSession[],
  observations: readonly RuntimeObservation[],
  heldTokens: ReadonlyMap<string, number> = new Map(),
): RuntimeDashboardModel {
  const sessionsById = new Map(sessions.map((session) => [session.id, session]));
  const rows: RuntimeDashboardRow[] = [];
  let managedRuntimeCount = 0;
  let observedRuntimeCount = 0;
  let unavailableRuntimeCount = 0;
  let unsupportedRuntimeCount = 0;
  let cpuUsageSecondsTotal = 0;
  let cpuUsageKnown = false;
  let cpuUsageSafe = true;
  let cpuCapacityCores = 0;
  let cpuCapacityKnown = false;
  let cpuCapacitySafe = true;
  let cpuCoverageCount = 0;
  let memoryUsageBytes = 0;
  let memoryUsageKnown = false;
  let memoryUsageSafe = true;
  let memoryLimitBytes = 0;
  let memoryLimitKnown = false;
  let memoryLimitSafe = true;
  let memoryCoverageCount = 0;
  let totalTokens = 0;
  let tokensKnown = false;
  let tokensSafe = true;
  let tokenCoverageCount = 0;
  let oldestResolvedAt: number | null = null;
  let newestResolvedAt: number | null = null;

  for (const observation of observations) {
    const session = sessionsById.get(observation.session_id);
    if (!session) continue;
    const sessionRow = toSessionRow(session);
    const resolvedAt = canonicalTimestamp(observation.resolved_at);
    if (resolvedAt !== null) {
      oldestResolvedAt = oldestResolvedAt === null ? resolvedAt : Math.min(oldestResolvedAt, resolvedAt);
      newestResolvedAt = newestResolvedAt === null ? resolvedAt : Math.max(newestResolvedAt, resolvedAt);
    }
    if (observation.mode === "openai_hosted") managedRuntimeCount += 1;
    if (observation.status === "observed") {
      observedRuntimeCount += 1;
      const cpuUsage = safeFiniteNonNegative(observation.cpu?.usage_seconds_total);
      const cpuCapacity = safeFiniteNonNegative(observation.cpu?.capacity_cores);
      if (cpuUsage !== null) {
        const next = cpuUsageSecondsTotal + cpuUsage;
        if (Number.isFinite(next)) {
          cpuUsageSecondsTotal = next;
          cpuUsageKnown = true;
        } else cpuUsageSafe = false;
      }
      if (cpuCapacity !== null) {
        const next = cpuCapacityCores + cpuCapacity;
        if (Number.isFinite(next)) {
          cpuCapacityCores = next;
          cpuCapacityKnown = true;
        } else cpuCapacitySafe = false;
      }
      if (cpuUsage !== null) cpuCoverageCount += 1;

      const memoryUsage = safeNonNegativeInteger(observation.memory?.usage_bytes);
      const memoryLimit = safeNonNegativeInteger(observation.memory?.limit_bytes);
      if (memoryUsage !== null) {
        const next = safeAdd(memoryUsageBytes, memoryUsage);
        if (next !== null) {
          memoryUsageBytes = next;
          memoryUsageKnown = true;
        } else memoryUsageSafe = false;
      }
      if (memoryLimit !== null) {
        const next = safeAdd(memoryLimitBytes, memoryLimit);
        if (next !== null) {
          memoryLimitBytes = next;
          memoryLimitKnown = true;
        } else memoryLimitSafe = false;
      }
      if (memoryUsage !== null) memoryCoverageCount += 1;
    } else if (observation.status === "unavailable") {
      unavailableRuntimeCount += 1;
    } else {
      unsupportedRuntimeCount += 1;
    }

    const sessionTokens = sessionRow.totalTokens ?? heldTokens.get(session.id) ?? null;
    if (sessionTokens !== null) {
      const next = safeAdd(totalTokens, sessionTokens);
      if (next !== null) {
        totalTokens = next;
        tokensKnown = true;
      } else tokensSafe = false;
      tokenCoverageCount += 1;
    }
    rows.push({
      session: sessionRow,
      observation,
      computeUptimeSeconds: observation.status === "observed"
        ? elapsedSeconds(canonicalTimestamp(observation.started_at), canonicalTimestamp(observation.observed_at))
        : null,
      allocationAgeSeconds: observation.mode === "openai_hosted" && observation.reason !== "runtime_not_running"
        ? elapsedSeconds(canonicalTimestamp(observation.allocation_created_at), resolvedAt)
        : null,
    });
  }

  const statusOrder = { observed: 0, unavailable: 1, unsupported: 2 } as const;
  rows.sort((left, right) => (
    statusOrder[left.observation.status] - statusOrder[right.observation.status]
    || compareSessionRows(left.session, right.session)
  ));
  return {
    summary: {
      sessionCount: rows.length,
      managedRuntimeCount,
      observedRuntimeCount,
      unavailableRuntimeCount,
      unsupportedRuntimeCount,
      cpuUsageSecondsTotal: cpuUsageKnown && cpuUsageSafe ? cpuUsageSecondsTotal : null,
      cpuCapacityCores: cpuCapacityKnown && cpuCapacitySafe ? cpuCapacityCores : null,
      cpuCoverageCount,
      memoryUsageBytes: memoryUsageKnown && memoryUsageSafe ? memoryUsageBytes : null,
      memoryLimitBytes: memoryLimitKnown && memoryLimitSafe ? memoryLimitBytes : null,
      memoryCoverageCount,
      totalTokens: tokensKnown && tokensSafe ? totalTokens : null,
      tokenCoverageCount,
      oldestResolvedAt,
      newestResolvedAt,
    },
    rows,
  };
}

export function formatDashboardBytes(value: number | null): string {
  if (value === null) return "Unavailable";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let index = 0;
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024;
    index += 1;
  }
  const digits = amount >= 100 || index === 0 ? 0 : amount >= 10 ? 1 : 2;
  return `${amount.toFixed(digits)} ${units[index]}`;
}

export function formatDashboardDuration(value: number | null): string {
  if (value === null || !Number.isFinite(value) || value < 0) return "Unavailable";
  const seconds = Math.floor(value);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function formatDashboardTokens(value: number | null): string {
  if (value === null) return "Unavailable";
  return value.toLocaleString("en-US");
}

export function runtimeObservationStatusLabel(observation: RuntimeObservation): string {
  if (observation.status === "observed") return "Observed";
  if (observation.status === "unsupported") return "Unsupported";
  switch (observation.reason) {
    case "allocation_pending": return "Allocation pending";
    case "runtime_not_running": return "Not running";
    case "source_not_configured": return "Source unavailable";
    case "sample_timeout": return "Sample timeout";
    case "sample_unavailable": return "Sample unavailable";
  }
}
