import { useRef } from "react";

import { assignStableSlots, seriesColor } from "../../components/charts/chart-scale";
import { OTHER_SERIES_ID } from "./agent-metrics";

/** Remembers each entity's categorical slot across ranges and refreshes on one page. */
export function useStableColors(visible: readonly string[]): (id: string) => string {
  const slots = useRef(new Map<string, number>());
  const key = visible.join("\u0000");
  const lastKey = useRef<string | null>(null);
  if (lastKey.current !== key) {
    slots.current = assignStableSlots(slots.current, visible.filter((id) => id !== OTHER_SERIES_ID));
    lastKey.current = key;
  }
  return (id: string) => (id === OTHER_SERIES_ID ? seriesColor(undefined) : seriesColor(slots.current.get(id)));
}
