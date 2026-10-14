import { AgentCoreError, type AgentSession, type RuntimeHistory, type RuntimeHistoryRange } from "@oac/agents-client";

import type { ProjectClient } from "../../lib/projects";
import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";
import { deriveTokenThroughput, type RuntimeTrendSample, type RuntimeTrendTarget } from "./runtime-trends";

export const RUNTIME_DURABLE_TARGET_LIMIT = 24;
/** Ranges Core ends on its own clock, so the browser's clock never enters a read. */
export const RUNTIME_DURABLE_RANGES = ["1h", "6h", "24h"] as const satisfies readonly RuntimeHistoryRange[];

export type RuntimeDurableRange = typeof RUNTIME_DURABLE_RANGES[number];

export interface RuntimeDurableSnapshot {
  samples: RuntimeTrendSample[];
  resolutionSeconds: number;
  targetCount: number;
  sampleCount: number;
  expectedSampleCount: number;
}

/** Web's own read bound: more Sessions than `limit`, or, when it is null, histories of different bucket widths. */
export class RuntimeDurableHistoryIncompleteError extends Error {
  constructor(readonly limit: number | null) {
    super(limit === null ? "Durable Runtime histories returned inconsistent resolutions." : `Durable Runtime history exceeds the ${limit} target Web query budget.`);
    this.name = "RuntimeDurableHistoryIncompleteError";
  }
}

function sessionTitle(session: AgentSession): string {
  const title = session.metadata?.title;
  if (typeof title === "string" && title.length > 0) return title;
  return session.agent.name ?? session.agent.model;
}

function durableTargets(snapshot: RuntimeDashboardSnapshot): AgentSession[] {
  const sessions = new Map(snapshot.sessions.map((session) => [session.id, session]));
  const targets = new Map<string, AgentSession>();
  for (const observation of snapshot.observations) {
    if (observation.mode !== "openai_hosted" || observation.environment_id === null) continue;
    const session = sessions.get(observation.session_id);
    if (session) targets.set(session.id, session);
  }
  return [...targets.values()];
}

async function mapBounded<T, R>(
  values: readonly T[],
  concurrency: number,
  mapper: (value: T) => Promise<R>,
): Promise<R[]> {
  const result = new Array<R>(values.length);
  let next = 0;
  const workers = Array.from({ length: Math.min(concurrency, values.length) }, async () => {
    while (next < values.length) {
      const index = next;
      next += 1;
      result[index] = await mapper(values[index]!);
    }
  });
  await Promise.all(workers);
  return result;
}

interface MutableBucket {
  sampledAt: number;
  hasObservationCoverage: boolean;
  targets: Map<string, RuntimeTrendTarget>;
  activeSandboxes: Map<string, { observedAt: number }>;
  memory: Map<string, { observedAt: number; usage: number; limit: number }>;
  tokens: Map<string, { sampledAt: number; inputTokens: number; outputTokens: number }>;
}

export function runtimeDurableTrendSamples(
  sessions: readonly AgentSession[],
  histories: readonly RuntimeHistory[],
): RuntimeTrendSample[] {
  const titles = new Map(sessions.map((session) => [session.id, sessionTitle(session)]));
  const buckets = new Map<number, MutableBucket>();
  const bucket = (sampledAt: number): MutableBucket => {
    let value = buckets.get(sampledAt);
    if (!value) {
      value = { sampledAt, hasObservationCoverage: false, targets: new Map(), activeSandboxes: new Map(), memory: new Map(), tokens: new Map() };
      buckets.set(sampledAt, value);
    }
    return value;
  };

  for (const history of histories) {
    const { start, end } = history.requested_range;
    for (let bucketStart = start; bucketStart < end; bucketStart += history.resolution_seconds) {
      const bucketEnd = Math.min(bucketStart + history.resolution_seconds, end);
      bucket(bucketEnd * 1_000);
    }
    for (const coverage of history.coverage.buckets) {
      const value = bucket(coverage.end * 1_000);
      value.hasObservationCoverage ||= coverage.observation_count > 0;
    }
    for (const usage of history.token_usage) {
      bucket(usage.end * 1_000).tokens.set(history.session_id, {
        sampledAt: usage.sampled_at * 1_000,
        inputTokens: usage.input_tokens,
        outputTokens: usage.output_tokens,
      });
    }
    for (const series of history.series) {
      const targetID = `${history.session_id}:${series.allocation_id}`;
      const label = titles.get(history.session_id) ?? "Runtime";
      for (const point of series.points) {
        const value = bucket(point.end * 1_000);
        value.hasObservationCoverage ||= point.observation_count > 0;
        const observedAt = point.last_observed_at;
        value.targets.set(targetID, {
          seriesId: targetID,
          label,
          cpuRatio: point.cpu?.utilization_ratio ?? null,
        });
        if (observedAt !== null && point.observed_count > 0) {
          const previous = value.activeSandboxes.get(series.allocation_id);
          if (!previous || observedAt >= previous.observedAt) {
            value.activeSandboxes.set(series.allocation_id, { observedAt });
          }
        }
        const usage = point.memory?.usage_bytes;
        const limit = point.memory?.limit_bytes;
        if (observedAt !== null && usage != null && limit != null) {
          const previous = value.memory.get(series.allocation_id);
          if (!previous || observedAt >= previous.observedAt) {
            value.memory.set(series.allocation_id, { observedAt, usage, limit });
          }
        }
      }
    }
  }

  // Core ends each range at its last complete bucket, so reads on either side of
  // a bucket boundary differ by one bucket: keep the buckets every history covers.
  const from = Math.max(...histories.map((history) => history.requested_range.start)) * 1_000;
  const to = Math.min(...histories.map((history) => history.requested_range.end)) * 1_000;
  const samples = [...buckets.values()].filter((value) => value.sampledAt > from && value.sampledAt <= to).sort((left, right) => left.sampledAt - right.sampledAt).map((value) => {
    const observedMemory = [...value.memory.values()];
    return {
      sampledAt: value.sampledAt,
      activeSandboxCount: value.hasObservationCoverage ? value.activeSandboxes.size : null,
      targets: [...value.targets.values()],
      cpuCandidates: [],
      memoryUsageBytes: observedMemory.length > 0
        ? observedMemory.reduce((total, current) => total + current.usage, 0)
        : null,
      memoryLimitBytes: observedMemory.length > 0
        ? observedMemory.reduce((total, current) => total + current.limit, 0)
        : null,
      tokenTotals: value.tokens.size === sessions.length
        ? [...value.tokens.entries()].map(([sessionId, usage]) => ({ sessionId, ...usage }))
        : [],
      inputTokensPerMinute: null,
      outputTokensPerMinute: null,
    };
  });
  return deriveTokenThroughput(samples);
}

export type RuntimeHistoryReader = Pick<ProjectClient, "retrieveRuntimeHistory">;

function isNotFound(error: unknown): boolean {
  return error instanceof AgentCoreError && error.status === 404;
}

/**
 * Reads the retained Runtime history of each hosted Session in the snapshot
 * through the reader of the Session's project. The Web API has no
 * deployment-wide capability read: a 404 for every Session means Core keeps
 * no history (null), and a 404 for one Session only leaves that Session out.
 */
export async function loadRuntimeDurableSnapshot(
  readerFor: (sessionId: string) => RuntimeHistoryReader | null,
  snapshot: RuntimeDashboardSnapshot,
  range: RuntimeDurableRange,
  signal?: AbortSignal,
  targetLimit = RUNTIME_DURABLE_TARGET_LIMIT,
): Promise<RuntimeDurableSnapshot | null> {
  const targets = durableTargets(snapshot);
  if (targets.length > targetLimit) {
    throw new RuntimeDurableHistoryIncompleteError(targetLimit);
  }
  const results = await mapBounded(targets, 4, async (session): Promise<RuntimeHistory | null> => {
    const reader = readerFor(session.id);
    if (!reader) return null;
    try {
      return await reader.retrieveRuntimeHistory(session.id, range, { signal });
    } catch (error) {
      if (isNotFound(error)) return null;
      throw error;
    }
  });
  signal?.throwIfAborted();
  if (targets.length > 0 && results.every((history) => history === null)) return null;
  const histories = results.filter((history): history is RuntimeHistory => history !== null);
  const covered = targets.filter((_, index) => results[index] !== null);
  const resolutions = new Set(histories.map((history) => history.resolution_seconds));
  if (resolutions.size > 1) {
    throw new RuntimeDurableHistoryIncompleteError(null);
  }
  return {
    samples: runtimeDurableTrendSamples(covered, histories),
    resolutionSeconds: histories[0]?.resolution_seconds ?? 30,
    targetCount: covered.length,
    sampleCount: histories.reduce((total, history) => total + history.coverage.sample_count, 0),
    expectedSampleCount: histories.reduce((total, history) => total + history.coverage.expected_sample_count, 0),
  };
}
