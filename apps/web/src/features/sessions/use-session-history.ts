import { AgentCoreError } from "@agents-core-web/agents-client";
import { useCallback, useEffect, useRef, useState } from "react";

import { projectClient } from "../../lib/projects";
import { loadSessionHistory, nextPollDelay, type SessionHistory } from "./session-history";

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

export function isNotFound(error: unknown): boolean {
  return error instanceof AgentCoreError && error.status === 404;
}

/**
 * Loads one Session's history through its project's read scope and polls it
 * while the Session has work in flight. Polls read incrementally; a manual
 * refresh reads everything again. Polls pause while the page is hidden.
 */
export function useSessionHistory(projectId: string | undefined, sessionId: string | undefined): SessionHistoryState & { refresh: () => void } {
  const [state, setState] = useState<SessionHistoryState>({ phase: "loading", history: null, error: null, gone: false, refreshing: false });
  const runRef = useRef<(incremental: boolean, manual: boolean) => void>(() => undefined);

  useEffect(() => {
    if (!projectId || !sessionId) return;
    const client = projectClient(projectId);
    let disposed = false;
    let timer: number | undefined;
    let controller: AbortController | null = null;
    let latest: SessionHistory | null = null;
    let failures = 0;

    const schedule = () => {
      const delay = nextPollDelay(latest?.session?.status, failures);
      if (delay === null) return;
      timer = window.setTimeout(function tick() {
        if (document.visibilityState === "hidden") {
          timer = window.setTimeout(tick, delay);
          return;
        }
        void run(true, false);
      }, delay);
    };

    const run = async (incremental: boolean, manual: boolean) => {
      window.clearTimeout(timer);
      controller?.abort();
      const current = new AbortController();
      controller = current;
      if (manual) setState((value) => ({ ...value, refreshing: true }));
      let next: SessionHistory;
      try {
        next = await loadSessionHistory(client, sessionId, latest, { incremental: incremental && latest !== null, signal: current.signal });
      } catch (error) {
        if (disposed || current.signal.aborted) return;
        // Only an abort escapes the loader; anything else is a bug worth surfacing as a failure.
        setState((value) => ({ ...value, phase: value.history ? "ready" : "failed", error, refreshing: false }));
        return;
      }
      if (disposed || current.signal.aborted) return;
      const gone = latest !== null && isNotFound(next.sessionError);
      failures = next.sessionError ? failures + 1 : 0;
      latest = next.session ? next : latest;
      setState({
        phase: latest ? "ready" : "failed",
        history: latest,
        error: next.sessionError,
        gone,
        refreshing: false,
      });
      if (!gone) schedule();
    };

    runRef.current = (incremental, manual) => void run(incremental, manual);
    setState({ phase: "loading", history: null, error: null, gone: false, refreshing: false });
    void run(false, false);
    return () => {
      disposed = true;
      window.clearTimeout(timer);
      controller?.abort();
      runRef.current = () => undefined;
    };
  }, [projectId, sessionId]);

  const refresh = useCallback(() => runRef.current(false, true), []);
  return { ...state, refresh };
}
