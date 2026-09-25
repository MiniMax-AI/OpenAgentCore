/** Pure scale helpers shared by the console charts. */

/** Round a positive maximum up to a clean axis limit and return evenly spaced ticks from zero. */
export function niceTicks(maximum: number, targetCount = 4): number[] {
  if (!Number.isFinite(maximum) || maximum <= 0) return [0, 1];
  const rough = maximum / targetCount;
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const residual = rough / magnitude;
  const step = (residual <= 1 ? 1 : residual <= 2 ? 2 : residual <= 2.5 ? 2.5 : residual <= 5 ? 5 : 10) * magnitude;
  const count = Math.max(1, Math.ceil(maximum / step - 1e-9));
  return Array.from({ length: count + 1 }, (_, index) => Number((index * step).toPrecision(12)));
}

/** Pick at most `maximum` evenly spaced indices, always including the first and last. */
export function tickIndices(length: number, maximum = 6): number[] {
  if (length <= 0) return [];
  if (length <= maximum) return Array.from({ length }, (_, index) => index);
  const stride = Math.ceil((length - 1) / (maximum - 1));
  const indices: number[] = [];
  for (let index = 0; index < length - 1; index += stride) indices.push(index);
  if (length - 1 - (indices[indices.length - 1] ?? 0) < stride / 2) indices.pop();
  indices.push(length - 1);
  return indices;
}

export function formatCompactNumber(value: number, locale?: string): string {
  return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: value >= 1000 ? 1 : 0 }).format(value);
}

export function formatBucketTime(seconds: number, bucketSeconds: number, spanSeconds: number, locale?: string): string {
  const date = new Date(seconds * 1000);
  if (spanSeconds > 36 * 3600) {
    return new Intl.DateTimeFormat(locale, bucketSeconds >= 86_400
      ? { month: "numeric", day: "numeric" }
      : { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(date);
  }
  return new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(date);
}

export const CATEGORICAL_SLOTS = 6;

/**
 * Categorical colour follows the entity, never its rank: an entity keeps its slot
 * while it stays visible, and only slots of entities that left the view are reused.
 */
export function assignStableSlots(previous: ReadonlyMap<string, number>, visible: readonly string[]): Map<string, number> {
  const next = new Map<string, number>();
  const visibleSet = new Set(visible);
  for (const [id, slot] of previous) if (visibleSet.has(id)) next.set(id, slot);
  const used = new Set(next.values());
  for (const id of visible) {
    if (next.has(id)) continue;
    const reclaimed = previous.get(id);
    if (reclaimed !== undefined && !used.has(reclaimed)) {
      next.set(id, reclaimed);
      used.add(reclaimed);
      continue;
    }
    // Prefer a slot nobody remembers, then reuse one whose entity left the view.
    const remembered = new Set(previous.values());
    const free = [...Array(CATEGORICAL_SLOTS).keys()].map((index) => index + 1).filter((slot) => !used.has(slot));
    const slot = free.find((candidate) => !remembered.has(candidate)) ?? free[0];
    if (slot !== undefined) {
      next.set(id, slot);
      used.add(slot);
    }
  }
  // Keep remembered slots of hidden entities so they can return to their colour.
  for (const [id, slot] of previous) if (!next.has(id) && ![...next.values()].includes(slot)) next.set(id, slot);
  return next;
}

export function seriesColor(slot: number | undefined): string {
  return slot === undefined ? "var(--series-other)" : `var(--series-${slot})`;
}
