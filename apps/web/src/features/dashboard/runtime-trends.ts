import type { AgentSession, RuntimeObservation } from "@agents-core-web/agents-client";

import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";

export const RUNTIME_TREND_WINDOW_MS = 60 * 60 * 1_000;
export const RUNTIME_TREND_MAX_SAMPLES = 120;
export const RUNTIME_TREND_SERIES_LIMIT = 3;
export const RUNTIME_TREND_MAX_TARGETS = RUNTIME_TREND_SERIES_LIMIT * 2;

export interface RuntimeTrendTarget {
  sessionId: string;
  label: string;
  cpuRatio: number | null;
  uptimeSeconds: number | null;
}

export interface RuntimeTrendSample {
  sampledAt: number;
  targets: RuntimeTrendTarget[];
  memoryUsageBytes: number | null;
  memoryLimitBytes: number | null;
  tokenTotals: RuntimeTrendTokenTotal[];
  inputTokensPerMinute: number | null;
  outputTokensPerMinute: number | null;
}

export interface RuntimeTrendTokenTotal {
  sessionId: string;
  inputTokens: number;
  outputTokens: number;
}

export interface TokenThroughputSample {
  sampledAt: number;
  inputPerMinute: number | null;
  outputPerMinute: number | null;
}

function finiteNonNegative(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : null;
}

function safeInteger(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
}

function sessionTitle(session: AgentSession): string {
  const title = session.metadata?.title;
  if (typeof title === "string" && title.length > 0) return title;
  return session.agent.name ?? session.agent.model;
}

function cpuRatio(observation: RuntimeObservation): number | null {
  if (observation.status !== "observed") return null;
  const reported = finiteNonNegative(observation.cpu?.utilization_ratio);
  if (reported !== null) return reported;
  const usage = finiteNonNegative(observation.cpu?.usage_cores);
  const capacity = finiteNonNegative(observation.cpu?.capacity_cores);
  return usage !== null && capacity !== null && capacity > 0 ? usage / capacity : null;
}

function uptimeSeconds(observation: RuntimeObservation): number | null {
  if (observation.status !== "observed") return null;
  const startedAt = safeInteger(observation.started_at);
  const observedAt = safeInteger(observation.observed_at);
  return startedAt !== null && observedAt !== null && observedAt >= startedAt
    ? observedAt - startedAt
    : null;
}

function tokenTotals(sessions: readonly AgentSession[]): RuntimeTrendTokenTotal[] {
  return sessions.flatMap((session): RuntimeTrendTokenTotal[] => {
    const inputTokens = safeInteger(session.usage?.input_tokens);
    const outputTokens = safeInteger(session.usage?.output_tokens);
    return inputTokens === null || outputTokens === null
      ? []
      : [{ sessionId: session.id, inputTokens, outputTokens }];
  });
}

export function runtimeTrendSample(snapshot: RuntimeDashboardSnapshot): RuntimeTrendSample {
  const sessions = new Map(snapshot.sessions.map((session) => [session.id, session]));
  const observed = snapshot.observations.flatMap((observation) => {
    const session = sessions.get(observation.session_id);
    if (!session || observation.status !== "observed") return [];
    return [{
      sessionId: observation.session_id,
      label: sessionTitle(session),
      cpuRatio: cpuRatio(observation),
      memoryUsageBytes: finiteNonNegative(observation.memory?.usage_bytes),
      memoryLimitBytes: finiteNonNegative(observation.memory?.limit_bytes),
      uptimeSeconds: uptimeSeconds(observation),
    }];
  });
  const targetIds = new Set([
    ...observed.filter((target) => target.cpuRatio !== null)
      .sort((left, right) => (right.cpuRatio ?? 0) - (left.cpuRatio ?? 0))
      .slice(0, RUNTIME_TREND_SERIES_LIMIT)
      .map((target) => target.sessionId),
    ...observed.filter((target) => target.uptimeSeconds !== null)
      .sort((left, right) => (right.uptimeSeconds ?? 0) - (left.uptimeSeconds ?? 0))
      .slice(0, RUNTIME_TREND_SERIES_LIMIT)
      .map((target) => target.sessionId),
  ]);
  const targets = observed.filter((target) => targetIds.has(target.sessionId)).map((target) => ({
    sessionId: target.sessionId,
    label: target.label,
    cpuRatio: target.cpuRatio,
    uptimeSeconds: target.uptimeSeconds,
  }));
  const pairedMemory = observed.filter((target) => (
    target.memoryUsageBytes !== null && target.memoryLimitBytes !== null
  ));
  return {
    sampledAt: snapshot.loadedAt,
    targets,
    memoryUsageBytes: pairedMemory.length === 0
      ? null
      : pairedMemory.reduce((total, target) => total + (target.memoryUsageBytes ?? 0), 0),
    memoryLimitBytes: pairedMemory.length === 0
      ? null
      : pairedMemory.reduce((total, target) => total + (target.memoryLimitBytes ?? 0), 0),
    tokenTotals: tokenTotals(snapshot.sessions),
    inputTokensPerMinute: null,
    outputTokensPerMinute: null,
  };
}

function tokenRate(
  previous: RuntimeTrendSample,
  next: RuntimeTrendSample,
): Pick<RuntimeTrendSample, "inputTokensPerMinute" | "outputTokensPerMinute"> {
  const elapsedMinutes = (next.sampledAt - previous.sampledAt) / 60_000;
  if (elapsedMinutes <= 0) return { inputTokensPerMinute: null, outputTokensPerMinute: null };
  const previousTotals = new Map(previous.tokenTotals.map((total) => [total.sessionId, total]));
  const pairs = next.tokenTotals.flatMap((current): Array<[RuntimeTrendTokenTotal, RuntimeTrendTokenTotal]> => {
    const prior = previousTotals.get(current.sessionId);
    return prior ? [[prior, current]] : [];
  });
  const inputDeltas = pairs.map(([prior, current]) => current.inputTokens - prior.inputTokens);
  const outputDeltas = pairs.map(([prior, current]) => current.outputTokens - prior.outputTokens);
  const inputDelta = inputDeltas.length > 0 && inputDeltas.every((delta) => delta >= 0)
    ? inputDeltas.reduce((total, delta) => total + delta, 0)
    : null;
  const outputDelta = outputDeltas.length > 0 && outputDeltas.every((delta) => delta >= 0)
    ? outputDeltas.reduce((total, delta) => total + delta, 0)
    : null;
  return {
    inputTokensPerMinute: inputDelta === null ? null : inputDelta / elapsedMinutes,
    outputTokensPerMinute: outputDelta === null ? null : outputDelta / elapsedMinutes,
  };
}

export function appendRuntimeTrendSample(
  current: readonly RuntimeTrendSample[],
  snapshot: RuntimeDashboardSnapshot,
  windowMs = RUNTIME_TREND_WINDOW_MS,
  maximum = RUNTIME_TREND_MAX_SAMPLES,
): RuntimeTrendSample[] {
  const next = runtimeTrendSample(snapshot);
  const retained = current
    .filter((sample) => sample.sampledAt !== next.sampledAt)
    .filter((sample) => sample.sampledAt >= next.sampledAt - windowMs && sample.sampledAt <= next.sampledAt)
    .sort((left, right) => left.sampledAt - right.sampledAt)
    .slice(-(maximum - 1));
  const previous = retained.at(-1);
  if (previous) Object.assign(next, tokenRate(previous, next));
  return [
    ...retained.map((sample) => ({ ...sample, tokenTotals: [] })),
    next,
  ].slice(-maximum);
}

export function tokenThroughput(samples: readonly RuntimeTrendSample[]): TokenThroughputSample[] {
  return samples.map((sample) => ({
    sampledAt: sample.sampledAt,
    inputPerMinute: sample.inputTokensPerMinute,
    outputPerMinute: sample.outputTokensPerMinute,
  }));
}
