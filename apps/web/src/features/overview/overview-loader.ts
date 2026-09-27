import type { AgentSession } from "@agents-core-web/agents-client";

import { readProjectsSessions, type InProject, type ProjectReadFailure, type SessionLister } from "../metrics/project-sessions";
import { activityStart, attentionCount, overviewReadDone, projectRows } from "./overview-model";
import { type Project, type ProjectSummary } from "../../lib/admin-view";

/** Most Sessions read per project for the 24-hour activity and the attention list. */
export const OVERVIEW_SESSION_CAP = 1_000;

export interface OverviewSessions {
  sessions: InProject<AgentSession>[];
  /** Projects whose read stopped at the cap before covering the window or every Session needing attention. */
  truncated: Project[];
  failures: ProjectReadFailure[];
  /** Failed projects whose previously read Sessions are retained. */
  stale: Project[];
}

export type OverviewSummary =
  | { status: "ready"; rows: ProjectSummary[] }
  | { status: "failed"; error: unknown; rows?: ProjectSummary[] };

export interface OverviewData {
  /** Retention is allowed only within this exact project scope. */
  projectIds: string[];
  summary: OverviewSummary;
  sessions: OverviewSessions;
  /** Epoch milliseconds. */
  loadedAt: number;
}

export interface OverviewSource {
  summary: (signal: AbortSignal) => Promise<ProjectSummary[]>;
  sessions: (project: Project) => SessionLister;
}

/**
 * Whether a project's Sessions must be read at all. The summary already
 * counts every Session; the list is read only for Sessions created in the
 * activity window and for those needing attention. A project whose last
 * activity precedes the window and that has nothing needing attention is
 * skipped.
 */
export function needsSessionRead(row: ProjectSummary | undefined, since: number): boolean {
  if (!row) return true;
  if (row.sessions.total === 0) return false;
  return attentionCount(row.sessions) > 0 || (row.last_active_at ?? Number.POSITIVE_INFINITY) >= since;
}

export async function loadOverview(
  projects: readonly Project[],
  source: OverviewSource,
  nowSeconds: number,
  signal: AbortSignal,
  previous?: OverviewData,
): Promise<OverviewData> {
  const projectIds = projects.map((project) => project.id);
  const prior = previous?.projectIds.length === projectIds.length && previous.projectIds.every((id, index) => id === projectIds[index]) ? previous : undefined;
  let summary: OverviewSummary;
  try {
    summary = { status: "ready", rows: await source.summary(signal) };
  } catch (error) {
    if (signal.aborted) throw error;
    summary = { status: "failed", error, ...(prior?.summary.rows ? { rows: prior.summary.rows } : {}) };
  }
  const since = activityStart(nowSeconds);
  const rows = summary.status === "ready" ? new Map(projectRows(summary.rows).map((row) => [row.project_id, row])) : null;
  const targets = projects.filter((project) => !rows || needsSessionRead(rows.get(project.id), since));
  const { reads, failures } = await readProjectsSessions(targets, source.sessions, (project) => {
    const row = rows?.get(project.id);
    const expected = row ? attentionCount(row.sessions) : null;
    return { signal, maxSessions: OVERVIEW_SESSION_CAP, enough: (sessions) => overviewReadDone(sessions, since, expected) };
  });
  if (signal.aborted) throw new DOMException("The overview load was aborted.", "AbortError");
  // A failed source must not erase its last successful evidence. Other projects
  // still replace their own rows, including a successful empty read.
  const stale = failures.filter(({ project }) => prior && (
    !prior.sessions.failures.some((failure) => failure.project.id === project.id)
    || prior.sessions.stale.some((entry) => entry.id === project.id)
  )).map(({ project }) => project);
  const staleIds = new Set(stale.map((project) => project.id));
  return {
    projectIds,
    summary,
    sessions: {
      sessions: [...reads.flatMap((read) => read.sessions.map((value) => ({ project: read.project, value }))), ...(prior?.sessions.sessions.filter((entry) => staleIds.has(entry.project.id)) ?? [])],
      truncated: [...reads.filter((read) => !read.complete).map((read) => read.project), ...(prior?.sessions.truncated.filter((project) => staleIds.has(project.id)) ?? [])],
      failures,
      stale,
    },
    loadedAt: Date.now(),
  };
}
