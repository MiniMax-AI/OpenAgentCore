/** How long an allocation has been in its compute phase, from Core's `compute_phase_changed_at`. */
export interface PhaseTiming {
  /** When it entered the phase, in epoch seconds. */
  since: number;
  /** Seconds in the phase so far. */
  elapsed: number;
  /**
   * While suspended: about how many seconds remain until Core reclaims the snapshot, 0 once that is
   * due; otherwise null. Core fixes the deadline when suspension starts, so this is an estimate.
   */
  reclaimIn: number | null;
}

/** Null when Core has not recorded when the phase began (allocations older than that record). */
export function phaseTiming(phase: string, changedAt: string | null, retentionSeconds: number, nowSeconds: number): PhaseTiming | null {
  const parsed = changedAt ? Date.parse(changedAt) : Number.NaN;
  if (Number.isNaN(parsed)) return null;
  const since = Math.floor(parsed / 1000);
  return {
    since,
    // A time ahead of the browser is clock skew, not a future change.
    elapsed: Math.max(0, nowSeconds - since),
    reclaimIn: phase === "suspended" ? Math.max(0, since + retentionSeconds - nowSeconds) : null,
  };
}
