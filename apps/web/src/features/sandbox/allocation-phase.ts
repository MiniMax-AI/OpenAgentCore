/** Elapsed time in Core's recorded compute phase; the API does not expose the snapshot deadline. */
export function phaseTiming(changedAt: string | null, nowSeconds: number): { since: number; elapsed: number } | null {
  const parsed = changedAt ? Date.parse(changedAt) : Number.NaN;
  if (!Number.isFinite(parsed)) return null;
  const since = Math.floor(parsed / 1000);
  return { since, elapsed: Math.max(0, nowSeconds - since) };
}
