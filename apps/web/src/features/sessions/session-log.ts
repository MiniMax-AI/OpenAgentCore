import type { AgentSession, CoreProjectReader } from "@oac/agents-client";

import type { Owned } from "../../lib/projects";

/**
 * Session log model: every Session of one project or of all projects, read
 * tolerantly so one malformed Session is listed as unrecognized instead of
 * failing the page, then filtered and ordered in the browser.
 */

export const SESSION_LOG_LIMIT = 10_000;
const SESSION_PAGE_SIZE = 100;

export const sessionStatuses = ["in_progress", "requires_action", "failed", "idle"] as const;
export type SessionStatusKey = (typeof sessionStatuses)[number];
export type StatusFilter = "all" | SessionStatusKey;

export const environmentKinds = ["openai_hosted", "self_hosted", "none"] as const;
export type EnvironmentKind = (typeof environmentKinds)[number] | "other";

export type SessionLogEntry =
  | { kind: "session"; session: AgentSession }
  /** A listed entry this console cannot read; only its Session ID (when it has one) is kept. */
  | { kind: "unrecognized"; id: string | null; key: string };

export interface SessionLogFilters {
  status: StatusFilter;
  agentId: string;
  environment: string;
  query: string;
}

export const initialSessionLogFilters: SessionLogFilters = { status: "all", agentId: "", environment: "", query: "" };

type TolerantLister = Pick<CoreProjectReader, "listSessionsTolerant">;

/** Walks every Session page of one project, newest first, bounded at `limit` entries. */
export async function readSessionLog(client: TolerantLister, signal?: AbortSignal, limit = SESSION_LOG_LIMIT): Promise<SessionLogEntry[]> {
  const entries: SessionLogEntry[] = [];
  let after: string | undefined;
  for (let page = 0; entries.length < limit; page += 1) {
    const result = await client.listSessionsTolerant({ after, limit: SESSION_PAGE_SIZE, order: "desc", signal });
    for (const session of result.data) entries.push({ kind: "session", session });
    for (const entry of result.unrecognized) entries.push({ kind: "unrecognized", id: entry.id, key: entry.id ?? `${page}:${entry.index}` });
    if (!result.has_more || !result.last_id || result.last_id === after) break;
    after = result.last_id;
  }
  return entries.slice(0, limit);
}

export function environmentKind(session: AgentSession): EnvironmentKind {
  const type: string = session.environment?.type;
  return type === "none" || type === "self_hosted" || type === "openai_hosted" ? type : "other";
}

export function statusKey(status: string): SessionStatusKey | "other" {
  return (sessionStatuses as readonly string[]).includes(status) ? status as SessionStatusKey : "other";
}

/** Only an idle or failed Session without required actions can be deleted; Core enforces this too. */
export function isDeletable(session: AgentSession): boolean {
  return (session.status === "idle" || session.status === "failed") && !session.required_actions.length;
}

function matchesQuery(session: AgentSession, query: string): boolean {
  if (!query) return true;
  return session.id.toLowerCase().includes(query)
    || session.agent.id.toLowerCase().includes(query)
    || (session.agent.name ?? "").toLowerCase().includes(query)
    || session.agent.model.toLowerCase().includes(query)
    || (session.metadata.title ?? "").toLowerCase().includes(query)
    || (session.error ?? "").toLowerCase().includes(query);
}

function matches(session: AgentSession, filters: SessionLogFilters, query: string, ignoreStatus = false): boolean {
  return (ignoreStatus || filters.status === "all" || session.status === filters.status)
    && (!filters.agentId || session.agent.id === filters.agentId)
    && (!filters.environment || environmentKind(session) === filters.environment)
    && matchesQuery(session, query);
}

function neutral(filters: SessionLogFilters): boolean {
  return filters.status === "all" && !filters.agentId && !filters.environment;
}

/**
 * Rows of every selected project merged into one list: most recent activity first
 * (ties by Session ID), unrecognized entries last. Unrecognized entries carry no
 * status, Agent or environment, so any such filter hides them; search matches their ID.
 */
export function filterSessionLog(rows: readonly Owned<SessionLogEntry>[], filters: SessionLogFilters): Owned<SessionLogEntry>[] {
  const query = filters.query.trim().toLowerCase();
  const sessions: Array<Owned<SessionLogEntry> & { value: { kind: "session" } }> = [];
  const unrecognized: Owned<SessionLogEntry>[] = [];
  for (const row of rows) {
    if (row.value.kind === "session") {
      if (matches(row.value.session, filters, query)) sessions.push(row as Owned<SessionLogEntry> & { value: { kind: "session" } });
    } else if (neutral(filters) && (!query || (row.value.id ?? "").toLowerCase().includes(query))) {
      unrecognized.push(row);
    }
  }
  sessions.sort((a, b) => b.value.session.last_active_at - a.value.session.last_active_at || a.value.session.id.localeCompare(b.value.session.id));
  return [...sessions, ...unrecognized];
}

/** Counts per status for the rows the other filters (search, Agent, environment) keep. */
export function statusCounts(rows: readonly Owned<SessionLogEntry>[], filters: SessionLogFilters): Record<StatusFilter, number> {
  const query = filters.query.trim().toLowerCase();
  const counts: Record<StatusFilter, number> = { all: 0, in_progress: 0, requires_action: 0, failed: 0, idle: 0 };
  for (const row of rows) {
    if (row.value.kind !== "session") {
      if (neutral({ ...filters, status: "all" }) && (!query || (row.value.id ?? "").toLowerCase().includes(query))) counts.all += 1;
      continue;
    }
    const session = row.value.session;
    if (!matches(session, filters, query, true)) continue;
    counts.all += 1;
    const key = statusKey(session.status);
    if (key !== "other") counts[key] += 1;
  }
  return counts;
}

export interface AgentOption {
  id: string;
  label: string;
}

/**
 * One option per Agent seen in the loaded Sessions, by name. When every
 * project is shown, a name used by Agents of different projects carries the
 * project name.
 */
export function agentOptions(rows: readonly Owned<SessionLogEntry>[], fallback: string): AgentOption[] {
  const agents = new Map<string, { name: string; project: string }>();
  for (const row of rows) {
    if (row.value.kind !== "session") continue;
    const agent = row.value.session.agent;
    if (!agents.has(agent.id)) agents.set(agent.id, { name: agent.name?.trim() || fallback, project: row.project.name });
  }
  const names = new Map<string, Set<string>>();
  for (const { name, project } of agents.values()) names.set(name, (names.get(name) ?? new Set()).add(project));
  return [...agents.entries()]
    .map(([id, { name, project }]) => ({ id, label: (names.get(name)?.size ?? 0) > 1 ? `${name} · ${project}` : name }))
    .sort((a, b) => a.label.localeCompare(b.label) || a.id.localeCompare(b.id));
}

/** True when some project hit the read bound, so more Sessions exist than are shown. */
export function isLogTruncated(rows: readonly Owned<SessionLogEntry>[], limit = SESSION_LOG_LIMIT): boolean {
  const perProject = new Map<string, number>();
  for (const row of rows) perProject.set(row.project.id, (perProject.get(row.project.id) ?? 0) + 1);
  return [...perProject.values()].some((count) => count >= limit);
}
