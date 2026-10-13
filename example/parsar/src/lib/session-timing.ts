import { useEffect, useState } from "react";

interface SendTiming {
  operation: string;
  startedAt: number;
  previousTurnId?: string;
  responseMs?: number;
  firstTextMs?: number;
}
const cache = new Map<string, SendTiming>();
const storageKey = (id: string) => `oac-example-timing-${id}`;
export const timingNow = () => performance.timeOrigin + performance.now();
function read(id: string): SendTiming | undefined {
  if (!cache.has(id)) {
    try {
      const saved = JSON.parse(
        sessionStorage.getItem(storageKey(id)) || "null",
      );
      if (saved && typeof saved.startedAt === "number") cache.set(id, saved);
    } catch {
      /* Timing remains usable without browser storage. */
    }
  }
  return cache.get(id);
}
function write(id: string, value: SendTiming) {
  cache.set(id, value);
  try {
    sessionStorage.setItem(storageKey(id), JSON.stringify(value));
  } catch {
    /* Optional persistence. */
  }
  window.dispatchEvent(new CustomEvent("session-timing", { detail: id }));
}
export function beginTiming(
  id: string,
  operation: string,
  previousTurnId?: string,
  startedAt = timingNow(),
) {
  write(id, { operation, startedAt, previousTurnId });
}
export function requestReturned(id: string, operation: string) {
  const value = read(id);
  if (value?.operation === operation)
    write(id, { ...value, responseMs: timingNow() - value.startedAt });
}
export function firstTextArrived(id: string, turnId?: string | null) {
  const value = read(id);
  if (
    value &&
    value.firstTextMs === undefined &&
    (!value.previousTurnId || turnId !== value.previousTurnId)
  )
    write(id, { ...value, firstTextMs: timingNow() - value.startedAt });
}
export function useSendTiming(id: string) {
  const [timing, setTiming] = useState(() => read(id));
  useEffect(() => {
    const update = (event: Event) => {
      if ((event as CustomEvent).detail === id) setTiming(read(id));
    };
    window.addEventListener("session-timing", update);
    return () => window.removeEventListener("session-timing", update);
  }, [id]);
  return timing;
}
export function timingLabel(ms?: number) {
  return ms === undefined ? "—" : `${(ms / 1000).toFixed(2)} 秒`;
}
