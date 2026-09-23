import type {
  AgentCore,
  AgentSession,
  RuntimeObservation,
} from "@agents-core-web/agents-client";

import { listAllCollectionPages } from "../../lib/collection-pagination";

export const RUNTIME_SNAPSHOT_TARGET_LIMIT = 10_000;
export const RUNTIME_SNAPSHOT_TIMEOUT_MS = 15_000;
export const RUNTIME_SNAPSHOT_REFRESH_MS = 30_000;

export interface RuntimeDashboardSnapshot {
  sessions: AgentSession[];
  observations: RuntimeObservation[];
  loadedAt: number;
}

export class RuntimeSnapshotIncompleteError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RuntimeSnapshotIncompleteError";
  }
}

function identitySet(values: readonly { id: string }[]): Set<string> {
  return new Set(values.map((value) => value.id));
}

function setsEqual(left: ReadonlySet<string>, right: ReadonlySet<string>): boolean {
  if (left.size !== right.size) return false;
  for (const value of left) if (!right.has(value)) return false;
  return true;
}

export async function loadRuntimeDashboardSnapshot(
  core: Pick<AgentCore, "listSessions" | "listRuntimeObservations">,
  signal?: AbortSignal,
  targetLimit = RUNTIME_SNAPSHOT_TARGET_LIMIT,
): Promise<RuntimeDashboardSnapshot | null> {
  const [sessions, observations] = await Promise.all([
    listAllCollectionPages((options) => core.listSessions(options), signal),
    listAllCollectionPages((options) => core.listRuntimeObservations(options), signal),
  ]);
  signal?.throwIfAborted();

  if (sessions.length > targetLimit || observations.length > targetLimit) {
    throw new RuntimeSnapshotIncompleteError("Runtime snapshot exceeded the Web target budget.");
  }
  if (!setsEqual(identitySet(sessions), identitySet(observations))) {
    throw new RuntimeSnapshotIncompleteError(
      "Sessions changed while Runtime observations were loading. The previous complete snapshot was retained.",
    );
  }
  return { sessions, observations, loadedAt: Date.now() };
}
