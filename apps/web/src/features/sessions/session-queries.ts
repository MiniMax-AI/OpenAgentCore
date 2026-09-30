import { AgentCoreError, type AgentSession, type RuntimeObservation } from "@oac/agents-client";
import { queryOptions } from "@tanstack/react-query";

import { projectClient } from "../../lib/projects";
import { retryTransient } from "../resources/detail-queries";
import { loadSessionHistory, nextPollDelay, type SessionHistory } from "./session-history";
import { loadSessionRuntimeHistory, type SessionRuntimeHistory, type SessionRuntimeRange } from "./session-runtime";

/**
 * One Session's reads, cached under `["session", projectId, sessionId, …]`.
 * A Session already seen opens from the cache; its history keeps polling while
 * the Session has work in flight.
 */

/** Every cached read of one Session starts with this key. */
export function sessionKey(projectId: string, sessionId: string) {
  return ["session", projectId, sessionId] as const;
}

export function isNotFound(error: unknown): boolean {
  return error instanceof AgentCoreError && error.status === 404;
}

/** What the history cache holds for one Session between reads. */
export interface SessionHistoryRead {
  /** The last history read, kept while later reads fail. */
  history: SessionHistory;
  /** The last Session read's error; with a history this marks it stale. */
  error: unknown;
  /** Core no longer has the Session read before; polling stopped. */
  gone: boolean;
  /** Consecutive failed Session reads, for the poll back-off. */
  failures: number;
}

/**
 * Folds one read into the cached state. Until a Session has been read there
 * is nothing to show, so the read fails with the Session's error; afterwards
 * a failing read keeps the last history and marks it stale, or gone on 404.
 */
export function foldSessionHistory(previous: SessionHistoryRead | undefined, next: SessionHistory): SessionHistoryRead {
  const latest = previous?.history ?? null;
  // The loader keeps the previous Session when its read fails, so this happens only before the first one.
  if (!next.session) throw next.sessionError;
  return {
    history: next,
    error: next.sessionError,
    gone: latest !== null && isNotFound(next.sessionError),
    failures: next.sessionError ? (previous?.failures ?? 0) + 1 : 0,
  };
}

/** Delay before the next poll, or false once the Session is idle, failed or gone. */
export function sessionPollInterval(read: SessionHistoryRead | undefined): number | false {
  if (!read || read.gone) return false;
  return nextPollDelay(read.history.session?.status, read.failures) ?? false;
}

/** Sessions whose next history read must read everything again (a manual refresh). */
const fullReads = new Set<string>();

export function requestFullHistoryRead(projectId: string, sessionId: string): void {
  fullReads.add(`${projectId}:${sessionId}`);
}

/**
 * The Session, its Items and its Turns. Polls read incrementally after the
 * settled prefix; a manual refresh reads everything again. Polls pause while
 * the page is hidden (`refetchIntervalInBackground` stays off).
 */
export function sessionHistoryQuery(projectId: string, sessionId: string) {
  return queryOptions<SessionHistoryRead>({
    queryKey: [...sessionKey(projectId, sessionId), "history"],
    queryFn: async ({ client, queryKey, signal }) => {
      const previous = client.getQueryData<SessionHistoryRead>(queryKey);
      const full = fullReads.delete(`${projectId}:${sessionId}`);
      const latest = previous?.history ?? null;
      const next = await loadSessionHistory(projectClient(projectId), sessionId, latest, { incremental: !full && latest !== null, signal });
      return foldSessionHistory(previous, next);
    },
    // A read only throws for an unread Session or a bug; neither is retried, and polling stops.
    retry: false,
    refetchInterval: (query) => (query.state.status === "error" ? false : sessionPollInterval(query.state.data)),
  });
}

export function sessionObservationQuery(projectId: string, sessionId: string) {
  return queryOptions<RuntimeObservation>({
    queryKey: [...sessionKey(projectId, sessionId), "runtime", "observation"],
    queryFn: ({ signal }) => projectClient(projectId).retrieveRuntimeObservation(sessionId, { signal }),
    retry: retryTransient,
  });
}

/** Retained runtime samples for the range ending now; null when Core keeps none. */
export function sessionRuntimeHistoryQuery(projectId: string, session: AgentSession, range: SessionRuntimeRange) {
  return queryOptions<SessionRuntimeHistory | null>({
    queryKey: [...sessionKey(projectId, session.id), "runtime", "history", range],
    queryFn: ({ signal }) => loadSessionRuntimeHistory(projectClient(projectId), session, range, signal),
    retry: retryTransient,
  });
}
