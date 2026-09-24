import type { AgentSession, AgentTurn, ListPage, PageOptions, SessionItem } from "@agents-core-web/agents-client";

import type { MetricsCoverage, MetricsWindow, SessionActivity } from "./agent-metrics";

export interface AgentMetricsSource {
  listTurns(sessionId: string, options?: PageOptions): Promise<ListPage<AgentTurn>>;
  listItems(sessionId: string, options?: PageOptions): Promise<ListPage<SessionItem>>;
}

export interface AgentMetricsLoadLimits {
  /** Most recently active Sessions read per load. */
  maxSessions: number;
  concurrency: number;
  maxTurnPages: number;
  maxItemPages: number;
  /** A Session whose reads take longer counts as failed. */
  sessionTimeoutMs: number;
  /** No further Session starts after this budget; the rest count as skipped. */
  loadBudgetMs: number;
}

export const DEFAULT_AGENT_METRICS_LIMITS: AgentMetricsLoadLimits = {
  maxSessions: 200,
  concurrency: 4,
  maxTurnPages: 10,
  maxItemPages: 5,
  sessionTimeoutMs: 15_000,
  loadBudgetMs: 45_000,
};

const PAGE_SIZE = 100;

/**
 * One Session's reads stop when the page load stops or the Session times out.
 * Built by hand so browsers without AbortSignal.any still work.
 */
function sessionSignal(parent: AbortSignal, timeoutMs: number): { signal: AbortSignal; dispose: () => void } {
  const controller = new AbortController();
  const abort = () => controller.abort(parent.reason);
  if (parent.aborted) abort();
  else parent.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(() => controller.abort(new DOMException("The Session read timed out.", "TimeoutError")), timeoutMs);
  return {
    signal: controller.signal,
    dispose: () => {
      clearTimeout(timer);
      parent.removeEventListener("abort", abort);
    },
  };
}

/** Sessions that may own a Turn created inside the window. */
export function candidateSessions(sessions: readonly AgentSession[], window: MetricsWindow): AgentSession[] {
  return sessions
    .filter((session) => session.last_active_at >= window.start || session.status === "in_progress" || session.status === "requires_action")
    .sort((a, b) => b.last_active_at - a.last_active_at || b.created_at - a.created_at || a.id.localeCompare(b.id));
}

async function readWindowTurns(
  source: AgentMetricsSource,
  sessionId: string,
  window: MetricsWindow,
  maxPages: number,
  signal: AbortSignal,
): Promise<{ turns: AgentTurn[]; olderTurnIds: Set<string>; truncated: boolean }> {
  const turns: AgentTurn[] = [];
  const olderTurnIds = new Set<string>();
  let after: string | undefined;
  for (let page = 0; page < maxPages; page += 1) {
    const result = await source.listTurns(sessionId, { order: "desc", limit: PAGE_SIZE, after, signal });
    let reachedStart = false;
    for (const turn of result.data) {
      if (turn.created_at >= window.start) turns.push(turn);
      else {
        olderTurnIds.add(turn.id);
        reachedStart = true;
      }
    }
    if (reachedStart || !result.has_more) return { turns, olderTurnIds, truncated: false };
    const next = result.last_id ?? result.data[result.data.length - 1]?.id;
    if (!next || next === after) return { turns, olderTurnIds, truncated: false };
    after = next;
  }
  return { turns, olderTurnIds, truncated: true };
}

async function readWindowItems(
  source: AgentMetricsSource,
  sessionId: string,
  windowTurnIds: ReadonlySet<string>,
  olderTurnIds: ReadonlySet<string>,
  maxPages: number,
  signal: AbortSignal,
): Promise<{ items: SessionItem[]; truncated: boolean }> {
  const items: SessionItem[] = [];
  let after: string | undefined;
  for (let page = 0; page < maxPages; page += 1) {
    const result = await source.listItems(sessionId, { order: "desc", limit: PAGE_SIZE, after, signal });
    let reachedOlder = false;
    for (const item of result.data) {
      if (windowTurnIds.has(item.turn_id)) items.push(item);
      else if (olderTurnIds.has(item.turn_id)) reachedOlder = true;
    }
    if (reachedOlder || !result.has_more) return { items, truncated: false };
    const next = result.last_id ?? result.data[result.data.length - 1]?.id;
    if (!next || next === after) return { items, truncated: false };
    after = next;
  }
  return { items, truncated: true };
}

export interface AgentMetricsLoad {
  activities: SessionActivity[];
  coverage: MetricsCoverage;
}

/**
 * Reads the bounded Turn and Item history of the most recently active Sessions.
 * Individual Session failures reduce coverage instead of failing the page.
 */
export async function loadAgentMetricsActivity(
  source: AgentMetricsSource,
  sessions: readonly AgentSession[],
  window: MetricsWindow,
  signal: AbortSignal,
  options: { includeTools: boolean; limits?: AgentMetricsLoadLimits },
): Promise<AgentMetricsLoad> {
  const limits = options.limits ?? DEFAULT_AGENT_METRICS_LIMITS;
  const candidates = candidateSessions(sessions, window);
  const selected = candidates.slice(0, limits.maxSessions);
  const activities: SessionActivity[] = [];
  const deadline = Date.now() + limits.loadBudgetMs;
  let failedSessions = 0;
  let itemFailedSessions = 0;
  let started = 0;
  let cursor = 0;

  async function worker() {
    while (cursor < selected.length && Date.now() < deadline) {
      const session = selected[cursor];
      cursor += 1;
      if (!session) continue;
      started += 1;
      const scoped = sessionSignal(signal, limits.sessionTimeoutMs);
      try {
        let turnRead: Awaited<ReturnType<typeof readWindowTurns>>;
        try {
          turnRead = await readWindowTurns(source, session.id, window, limits.maxTurnPages, scoped.signal);
        } catch (error) {
          if (signal.aborted) throw error;
          failedSessions += 1;
          continue;
        }
        let items: SessionItem[] | null = options.includeTools ? [] : null;
        let itemsTruncated = false;
        if (options.includeTools && turnRead.turns.length) {
          try {
            const itemRead = await readWindowItems(
              source,
              session.id,
              new Set(turnRead.turns.map((turn) => turn.id)),
              turnRead.olderTurnIds,
              limits.maxItemPages,
              scoped.signal,
            );
            items = itemRead.items;
            itemsTruncated = itemRead.truncated;
          } catch (error) {
            if (signal.aborted) throw error;
            // Keep the Turns; this Session's tool calls are unknown.
            items = null;
            itemFailedSessions += 1;
          }
        }
        activities.push({ session, turns: turnRead.turns, items, truncated: turnRead.truncated || itemsTruncated });
      } finally {
        scoped.dispose();
      }
    }
  }

  await Promise.all(Array.from({ length: Math.min(limits.concurrency, selected.length) }, () => worker()));
  if (signal.aborted) throw new DOMException("The metrics load was aborted.", "AbortError");

  return {
    activities,
    coverage: {
      candidateSessions: candidates.length,
      loadedSessions: activities.length,
      skippedSessions: candidates.length - started,
      failedSessions,
      truncatedSessions: activities.filter((activity) => activity.truncated).length,
      itemFailedSessions,
    },
  };
}
