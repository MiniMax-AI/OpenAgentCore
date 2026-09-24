import { useCallback, useEffect, useRef, useState } from "react";

import {
  type AgentUsageRange,
  type AgentUsageSource,
  continueUsageLoad,
  createUsageLoadCursor,
  type UnrecognizedUsageSession,
  unrecognizedInUsageRange,
  type UsageLoadCursor,
  type UsageLoadProgress,
  type UsageSessionRecord,
  usageCursorCovers,
  usageLoadProgress,
  usageRangeStart,
} from "./agent-usage";

export type AgentUsageStatus = "idle" | "loading" | "ready" | "failed" | "cancelled";

export interface AgentUsageState {
  range: AgentUsageRange;
  /** Start of the selected range, fixed when the range was chosen or reloaded. */
  rangeStart: number | null;
  status: AgentUsageStatus;
  progress: UsageLoadProgress;
  error: string | null;
  /** Everything read so far; only meaningful for statistics when status is ready. */
  records: readonly UsageSessionRecord[];
  /**
   * Listed Sessions the client could not recognize that may belong to the
   * selected range (every one read for all time), with their raw IDs where
   * known. They are excluded from every total and count, never counted as zero
   * usage; for a bounded range the count is an upper bound. Only meaningful
   * when status is ready.
   */
  unrecognized: readonly UnrecognizedUsageSession[];
  /** Browser time of the last completed load, in milliseconds. */
  loadedAt: number | null;
}

export interface AgentUsageController extends AgentUsageState {
  setRange: (range: AgentUsageRange) => void;
  cancel: () => void;
  /** Continues from the last completed page after a failure or cancellation. */
  resume: () => void;
  /** Discards everything read and starts again. */
  reload: () => void;
}

export const initialAgentUsageState: AgentUsageState = {
  range: "all",
  rangeStart: null,
  status: "idle",
  progress: { pages: 0, sessions: 0 },
  error: null,
  records: [],
  unrecognized: [],
  loadedAt: null,
};

const nowSeconds = () => Math.floor(Date.now() / 1_000);

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Session statistics for the Agents page, held in memory for the life of the
 * page only. Loading starts once `enabled` is true and can be cancelled,
 * resumed and reloaded.
 */
export function useAgentUsage(source: AgentUsageSource | undefined, enabled: boolean): AgentUsageController {
  const [state, setState] = useState<AgentUsageState>(initialAgentUsageState);
  const cursorRef = useRef<UsageLoadCursor>(createUsageLoadCursor());
  const controllerRef = useRef<AbortController | null>(null);
  const stateRef = useRef(state);
  stateRef.current = state;

  const load = useCallback((range: AgentUsageRange, rangeStart: number | null, cursor: UsageLoadCursor) => {
    controllerRef.current?.abort();
    controllerRef.current = null;
    cursorRef.current = cursor;
    const progress = usageLoadProgress(cursor);
    if (!source) return;
    if (usageCursorCovers(cursor, rangeStart)) {
      setState((current) => ({
        ...current, range, rangeStart, status: "ready", progress, error: null, records: [...cursor.records],
        unrecognized: unrecognizedInUsageRange(cursor.records, cursor.unrecognized, rangeStart),
        loadedAt: current.loadedAt ?? Date.now(),
      }));
      return;
    }
    const controller = new AbortController();
    controllerRef.current = controller;
    setState((current) => ({ ...current, range, rangeStart, status: "loading", progress, error: null }));
    continueUsageLoad(source, cursor, rangeStart, {
      signal: controller.signal,
      onProgress: (next) => {
        if (controllerRef.current === controller) setState((current) => ({ ...current, progress: next }));
      },
    }).then(() => {
      if (controllerRef.current !== controller) return;
      controllerRef.current = null;
      setState((current) => ({
        ...current,
        status: "ready",
        progress: usageLoadProgress(cursor),
        records: [...cursor.records],
        unrecognized: unrecognizedInUsageRange(cursor.records, cursor.unrecognized, rangeStart),
        loadedAt: Date.now(),
      }));
    }, (error: unknown) => {
      if (controllerRef.current !== controller) return;
      controllerRef.current = null;
      setState((current) => ({
        ...current,
        status: "failed",
        progress: usageLoadProgress(cursor),
        error: errorText(error),
      }));
    });
  }, [source]);

  useEffect(() => {
    if (!enabled || !source || stateRef.current.status !== "idle") return;
    load(stateRef.current.range, usageRangeStart(stateRef.current.range, nowSeconds()), createUsageLoadCursor());
  }, [enabled, load, source]);

  useEffect(() => () => {
    controllerRef.current?.abort();
    controllerRef.current = null;
  }, []);

  const setRange = useCallback((range: AgentUsageRange) => {
    if (range === stateRef.current.range && stateRef.current.status !== "idle") return;
    load(range, usageRangeStart(range, nowSeconds()), cursorRef.current);
  }, [load]);

  const cancel = useCallback(() => {
    const controller = controllerRef.current;
    if (!controller) return;
    controllerRef.current = null;
    controller.abort();
    const cursor = cursorRef.current;
    setState((current) => ({
      ...current,
      status: "cancelled",
      progress: usageLoadProgress(cursor),
    }));
  }, []);

  const resume = useCallback(() => {
    load(stateRef.current.range, stateRef.current.rangeStart, cursorRef.current);
  }, [load]);

  const reload = useCallback(() => {
    setState((current) => ({ ...current, records: [], unrecognized: [], loadedAt: null }));
    load(stateRef.current.range, usageRangeStart(stateRef.current.range, nowSeconds()), createUsageLoadCursor());
  }, [load]);

  return { ...state, setRange, cancel, resume, reload };
}
