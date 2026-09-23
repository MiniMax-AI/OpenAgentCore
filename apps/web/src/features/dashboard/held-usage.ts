/**
 * Public Session usage is null while a root Turn runs and after a Turn ends
 * unmeasured. Dashboard token totals keep each listed Session's last reported
 * value instead, so they neither drop to zero nor break. A newly reported value
 * replaces the held one, and Sessions no longer listed are dropped.
 */
export function holdLastReported<T>(
  previous: ReadonlyMap<string, T>,
  current: ReadonlyMap<string, T | null>,
): Map<string, T> {
  const held = new Map<string, T>();
  for (const [sessionId, value] of current) {
    const kept = value ?? previous.get(sessionId);
    if (kept !== undefined) held.set(sessionId, kept);
  }
  return held;
}
