import type { AgentSession, RuntimeObservation } from "@agents-core-web/agents-client";

export const RUNTIME_SNAPSHOT_REFRESH_MS = 30_000;

/**
 * One read of hosted Runtime observations and the Sessions they belong to.
 * Trend charts sample it; durable history is read per Session.
 */
export interface RuntimeDashboardSnapshot {
  sessions: AgentSession[];
  observations: RuntimeObservation[];
  /** Session ID to the ID of its project, when the snapshot spans projects. */
  owners?: ReadonlyMap<string, string>;
  loadedAt: number;
}
