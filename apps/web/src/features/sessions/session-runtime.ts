import { AgentCoreError, type AgentSession, type CoreProjectReader } from "@oac/agents-client";

import { RUNTIME_DURABLE_MAX_POINTS, RUNTIME_DURABLE_RANGES, runtimeDurableTrendSamples, type RuntimeDurableRange } from "../dashboard/runtime-history";
import type { RuntimeTrendSample } from "../dashboard/runtime-trends";

/** Runtime history ranges offered for one Session (1 h, 6 h, 24 h). */
export const SESSION_RUNTIME_RANGES = RUNTIME_DURABLE_RANGES;
export type SessionRuntimeRange = RuntimeDurableRange;

export interface SessionRuntimeHistory {
  samples: RuntimeTrendSample[];
  /** Milliseconds. */
  rangeStart: number;
  rangeEnd: number;
}

/** Only Core-managed hosted sandboxes report CPU and memory. */
export function hasObservableRuntime(session: AgentSession): boolean {
  return session.environment.type === "openai_hosted";
}

/**
 * Reads the retained runtime samples of one hosted Session for the range
 * ending now. Resolves to null when Core keeps no history for it (404).
 */
export async function loadSessionRuntimeHistory(
  client: Pick<CoreProjectReader, "retrieveRuntimeHistory">,
  session: AgentSession,
  range: SessionRuntimeRange,
  signal?: AbortSignal,
  now: () => number = Date.now,
): Promise<SessionRuntimeHistory | null> {
  const end = Math.floor(now() / 1_000);
  const start = end - range / 1_000;
  try {
    const history = await client.retrieveRuntimeHistory(session.id, { start, end, maxPoints: RUNTIME_DURABLE_MAX_POINTS, signal });
    return { samples: runtimeDurableTrendSamples([session], [history]), rangeStart: start * 1_000, rangeEnd: end * 1_000 };
  } catch (error) {
    if (error instanceof AgentCoreError && error.status === 404) return null;
    throw error;
  }
}
