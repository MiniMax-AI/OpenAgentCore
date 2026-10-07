import type { AgentSession } from "@oac/agents-client";

import type { Owned, ProjectClient } from "../../lib/projects";
import { type Project } from "../../lib/admin-view";

/**
 * Bounded reads of each project's Session list through its admin scope. The
 * list is ordered newest-created first, so a read can stop once older Sessions
 * are no longer needed. A read that stops at its cap says so; callers report
 * the gap instead of treating the rest as zero.
 */

export const SESSION_PAGE_SIZE = 100;

export type SessionLister = Pick<ProjectClient, "listSessions">;

/** A value and the project it belongs to. */
export type InProject<T> = Owned<T>;

export interface SessionRead {
  sessions: AgentSession[];
  /** False when the read stopped at `maxSessions` before the list ended or `enough` held. */
  complete: boolean;
}

export interface SessionReadOptions {
  signal?: AbortSignal;
  maxSessions: number;
  /** Stop early when the Sessions read so far are sufficient. */
  enough?: (sessions: readonly AgentSession[]) => boolean;
}

export async function readSessions(client: SessionLister, options: SessionReadOptions): Promise<SessionRead> {
  const sessions: AgentSession[] = [];
  let after: string | undefined;
  while (sessions.length < options.maxSessions) {
    const limit = Math.min(SESSION_PAGE_SIZE, options.maxSessions - sessions.length);
    const page = await client.listSessions({ order: "desc", limit, after, signal: options.signal });
    sessions.push(...page.data);
    if (!page.has_more || !page.last_id || page.last_id === after) return { sessions, complete: true };
    if (options.enough?.(sessions)) return { sessions, complete: true };
    after = page.last_id;
  }
  return { sessions, complete: false };
}

export interface ProjectSessionRead extends SessionRead {
  project: Project;
}

export interface ProjectReadFailure {
  project: Project;
  message: string;
}

/** Reads several projects in parallel; a failing project is reported by name and the others still count. */
export async function readProjectsSessions(
  projects: readonly Project[],
  clientFor: (project: Project) => SessionLister,
  options: (project: Project) => SessionReadOptions,
): Promise<{ reads: ProjectSessionRead[]; failures: ProjectReadFailure[] }> {
  const results = await Promise.allSettled(projects.map(async (project) => ({ project, ...(await readSessions(clientFor(project), options(project))) })));
  const reads: ProjectSessionRead[] = [];
  const failures: ProjectReadFailure[] = [];
  results.forEach((result, index) => {
    if (result.status === "fulfilled") reads.push(result.value);
    else failures.push({ project: projects[index]!, message: result.reason instanceof Error ? result.reason.message : String(result.reason) });
  });
  return { reads, failures };
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}
