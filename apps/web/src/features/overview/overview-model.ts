import type { AdminProject, AgentSession } from "@oac/agents-client";

import type { CapacitySummary } from "../fleet/fleet-model";
import type { InProject } from "../metrics/project-sessions";
import { type ProjectSummary } from "../../lib/admin-view";

/** Pure projections behind the Overview. Missing inputs stay null, never zero. */

export type SessionCounts = ProjectSummary["sessions"];

export interface ProjectUsageRow {
  project: AdminProject;
  /** The project's `/summary` row, or null when Core returned none for it. */
  summary: ProjectSummary | null;
}

/** Project rows of a `/summary` response (Agent and key rows are skipped). */
export function projectRows(summary: readonly ProjectSummary[]): ProjectSummary[] {
  return summary.filter((row) => row.agent_id === null && row.key_id === null);
}

/**
 * One row per project, joined with its `/summary` row. Active projects come
 * first, then the most recently active.
 */
export function projectUsageRows(projects: readonly AdminProject[], summary: readonly ProjectSummary[]): ProjectUsageRow[] {
  const byProject = new Map(projectRows(summary).map((row) => [row.project_id, row]));
  return projects
    .map((project) => ({ project, summary: byProject.get(project.id) ?? null }))
    .sort((a, b) => (
      Number(a.project.archived_at !== null) - Number(b.project.archived_at !== null)
      || (b.summary?.last_active_at ?? -1) - (a.summary?.last_active_at ?? -1)
      || a.project.name.localeCompare(b.project.name)
    ));
}

/** Session counts over every project row. */
export function summaryTotals(summary: readonly ProjectSummary[]): SessionCounts {
  const sessions: SessionCounts = { total: 0, idle: 0, in_progress: 0, requires_action: 0, failed: 0 };
  for (const row of projectRows(summary)) {
    sessions.total += row.sessions.total;
    sessions.idle += row.sessions.idle;
    sessions.in_progress += row.sessions.in_progress;
    sessions.requires_action += row.sessions.requires_action;
    sessions.failed += row.sessions.failed;
  }
  return sessions;
}

export function attentionCount(counts: Pick<SessionCounts, "failed" | "requires_action">): number {
  return counts.failed + counts.requires_action;
}

function needsAttention(session: AgentSession): boolean {
  return session.status === "failed" || session.status === "requires_action";
}

/** Sessions that need an operator (failed or waiting for a required action), newest activity first. */
export function attentionSessions(sessions: readonly InProject<AgentSession>[], limit = 8): InProject<AgentSession>[] {
  return sessions
    .filter((entry) => needsAttention(entry.value))
    .sort((a, b) => b.value.last_active_at - a.value.last_active_at || a.value.id.localeCompare(b.value.id))
    .slice(0, limit);
}

/**
 * Whether one project's newest-first Session read may stop: it has passed the
 * start of the activity window and found every Session the summary counts as
 * needing attention.
 */
export function overviewReadDone(sessions: readonly AgentSession[], since: number, expectedAttention: number | null): boolean {
  const oldest = sessions.at(-1);
  if (!oldest || oldest.created_at >= since) return false;
  if (expectedAttention === null) return true;
  return sessions.filter(needsAttention).length >= expectedAttention;
}

/** A failure where the Web API itself did not answer: a gateway status or a network error. */
export function isGatewayOrNetworkFailure(error: unknown): boolean {
  const status = typeof error === "object" && error !== null && "status" in error ? (error as { status: unknown }).status : undefined;
  if (typeof status === "number") return status === 502 || status === 503 || status === 504;
  const message = typeof error === "string" ? error : error instanceof Error ? error.message : "";
  return /HTTP 50[234]\b|\(50[234]\)|failed to fetch|networkerror|load failed|network request failed/i.test(message);
}

export type ReadOutcome = { status: "pending" } | { status: "ready" } | { status: "failed"; error: unknown };

/**
 * Whether the Web API answers, from the reads the page made: any success or
 * any application error means it answered; only gateway or network failures
 * mean it did not. Null while nothing has settled.
 */
export function webApiReachable(reads: readonly ReadOutcome[]): boolean | null {
  if (reads.some((read) => read.status === "ready")) return true;
  const failures = reads.flatMap((read) => (read.status === "failed" ? [read.error] : []));
  if (failures.some((error) => !isGatewayOrNetworkFailure(error))) return true;
  return failures.length ? false : null;
}

export type ServiceHealth = "healthy" | "degraded" | "down" | "unknown";

/** Only recent failures describe current health; old failed Sessions stay failed forever. */
export const RECENT_FAILURE_WINDOW_SECONDS = 3_600;

export function recentFailures(sessions: readonly AgentSession[], now: number): number {
  return sessions.filter((session) => session.status === "failed" && session.last_active_at >= now - RECENT_FAILURE_WINDOW_SECONDS).length;
}

/**
 * Overall service verdict from evidence the console can read: whether the Web
 * API answers, the reads it serves, node availability and recently failing
 * Sessions. It never claims model readiness.
 */
export function serviceHealth(input: {
  coreReachable: boolean | null;
  /** A Web API read (the summary or a project's Sessions) failed. */
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

export const ACTIVITY_HOURS = 24;

/** Hourly Session activity over the last `hours`, ending at the current hour. */
export function sessionActivity(sessions: readonly AgentSession[], now: number, hours = ACTIVITY_HOURS): SessionActivity {
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

/** Start (epoch seconds) of the first activity bucket. */
export function activityStart(now: number, hours = ACTIVITY_HOURS): number {
  return Math.floor(now / 3600) * 3600 + 3600 - hours * 3600;
}
