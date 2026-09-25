import { useQuery } from "@tanstack/react-query";
import { useCallback, useState } from "react";

import type { SessionHistory } from "./session-history";
import { isNotFound, requestFullHistoryRead, sessionHistoryQuery } from "./session-queries";

export { isNotFound };

export interface SessionHistoryState {
  /** "failed" only when no Session has been read yet. */
  phase: "loading" | "ready" | "failed";
  /** The last history read, kept while later reads fail. */
  history: SessionHistory | null;
  /** The last Session read's error; with a history this marks it stale. */
  error: unknown;
  /** Core no longer has the Session; polling stopped. */
  gone: boolean;
  /** A refresh the administrator asked for is running. */
  refreshing: boolean;
}

/**
 * Loads one Session's history through its project's read scope and the query
 * cache, and polls it while the Session has work in flight. A Session already
 * seen opens from the cache. Polls read incrementally; a manual refresh reads
 * everything again. Polls pause while the page is hidden.
 */
export function useSessionHistory(projectId: string | undefined, sessionId: string | undefined): SessionHistoryState & { refresh: () => void } {
  const query = useQuery({ ...sessionHistoryQuery(projectId ?? "", sessionId ?? ""), enabled: Boolean(projectId && sessionId) });
  const [manual, setManual] = useState(false);
  const { refetch } = query;
  const refresh = useCallback(() => {
    if (!projectId || !sessionId) return;
    requestFullHistoryRead(projectId, sessionId);
    setManual(true);
    void refetch().finally(() => setManual(false));
  }, [projectId, sessionId, refetch]);

  const read = query.data;
  return {
    phase: read ? "ready" : query.isError ? "failed" : "loading",
    history: read?.history ?? null,
    error: query.isError ? query.error : read?.error ?? null,
    gone: read?.gone ?? false,
    refreshing: manual && query.isFetching,
    refresh,
  };
}
