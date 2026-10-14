import { AgentCoreError, type AgentSession } from "@oac/agents-client";

import type { ProjectClient } from "../../lib/projects";
import { RUNTIME_DURABLE_RANGES, runtimeDurableTrendSamples, type RuntimeDurableRange } from "../dashboard/runtime-history";
import type { RuntimeTrendSample } from "../dashboard/runtime-trends";

/** Runtime history ranges offered for one Session (1 h, 6 h, 24 h). */
export const SESSION_RUNTIME_RANGES = RUNTIME_DURABLE_RANGES;
export type SessionRuntimeRange = RuntimeDurableRange;

export interface SessionRuntimeHistory {
  samples: RuntimeTrendSample[];
}

/** Only Core-managed hosted sandboxes report CPU and memory. */
export function hasObservableRuntime(session: AgentSession): boolean {
  return session.environment.type === "openai_hosted";
}

/**
 * Reads the retained runtime samples of one hosted Session for the range
 * ending at Core's last complete bucket. Resolves to null when Core keeps no
 * history for it (404).
 */
export async function loadSessionRuntimeHistory(
  client: Pick<ProjectClient, "retrieveRuntimeHistory">,
  session: AgentSession,
  range: SessionRuntimeRange,
  signal?: AbortSignal,
): Promise<SessionRuntimeHistory | null> {
  try {
    const history = await client.retrieveRuntimeHistory(session.id, range, { signal });
    return { samples: runtimeDurableTrendSamples([session], [history]) };
  } catch (error) {
    if (error instanceof AgentCoreError && error.status === 404) return null;
    throw error;
  }
}
