import type { AgentSession, RuntimeObservation } from "@agents-core-web/agents-client";

import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";

export const RUNTIME_TREND_WINDOW_MS = 60 * 60 * 1_000;
export const RUNTIME_TREND_MAX_SAMPLES = 120;
export const RUNTIME_TREND_SERIES_LIMIT = 3;
export const RUNTIME_TREND_MAX_TARGETS = RUNTIME_TREND_SERIES_LIMIT * 2;
export const RUNTIME_TREND_RANGES = [
  { label: "15m", milliseconds: 15 * 60 * 1_000 },
  { label: "1h", milliseconds: RUNTIME_TREND_WINDOW_MS },
] as const;
export type RuntimeTrendRange = typeof RUNTIME_TREND_RANGES[number]["milliseconds"];

export interface RuntimeTrendTarget {
  seriesId: string;
  label: string;
  cpuRatio: number | null;
  uptimeSeconds: number | null;
}

export interface RuntimeTrendCPUCandidate extends RuntimeTrendTarget {
  observedAt: number | null;
  startedAt: number | null;
  allocationKey: string | null;
  usageSecondsTotal: number | null;
  capacityCores: number | null;
  reportedRatio: number | null;
}

export interface RuntimeTrendSample {
  sampledAt: number;
  targets: RuntimeTrendTarget[];
  cpuCandidates: RuntimeTrendCPUCandidate[];
  memoryUsageBytes: number | null;
  memoryLimitBytes: number | null;
  tokenTotals: RuntimeTrendTokenTotal[];
  inputTokensPerMinute: number | null;
  outputTokensPerMinute: number | null;
}

export interface RuntimeTrendTokenTotal {
  sessionId: string;
  sampledAt: number;
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

function reportedCpuRatio(observation: RuntimeObservation): number | null {
  if (observation.status !== "observed") return null;
  const reported = finiteNonNegative(observation.cpu?.utilization_ratio);
  if (reported !== null) return reported;
  const usage = finiteNonNegative(observation.cpu?.usage_cores);
  const capacity = finiteNonNegative(observation.cpu?.capacity_cores);
  return usage !== null && capacity !== null && capacity > 0
    ? finiteNonNegative(usage / capacity)
    : null;
}

function allocationKey(observation: RuntimeObservation): string | null {
  if (observation.status !== "observed") return null;
  const allocationId = observation.instance.allocation_id;
  return typeof allocationId === "string" && allocationId.length > 0
    ? `${observation.instance.kind}:${allocationId}`
    : null;
}

function uptimeSeconds(observation: RuntimeObservation): number | null {
  if (observation.status !== "observed") return null;
  const startedAt = safeInteger(observation.started_at);
  const observedAt = safeInteger(observation.observed_at);
  return startedAt !== null && observedAt !== null && observedAt >= startedAt
    ? observedAt - startedAt
    : null;
}

function tokenTotals(sessions: readonly AgentSession[], sampledAt: number): RuntimeTrendTokenTotal[] {
  return sessions.flatMap((session): RuntimeTrendTokenTotal[] => {
    const inputTokens = safeInteger(session.usage?.input_tokens);
    const outputTokens = safeInteger(session.usage?.output_tokens);
    return inputTokens === null || outputTokens === null
      ? []
      : [{ sessionId: session.id, sampledAt, inputTokens, outputTokens }];
  });
}

export function runtimeTrendSample(snapshot: RuntimeDashboardSnapshot): RuntimeTrendSample {
  const sessions = new Map(snapshot.sessions.map((session) => [session.id, session]));
  const observed = snapshot.observations.flatMap((observation) => {
    const session = sessions.get(observation.session_id);
    if (!session || observation.status !== "observed") return [];
    const key = allocationKey(observation);
    if (key === null) return [];
    return [{
      seriesId: `${observation.session_id}:${key}`,
      label: sessionTitle(session),
      cpuRatio: reportedCpuRatio(observation),
      observedAt: safeInteger(observation.observed_at),
      startedAt: safeInteger(observation.started_at),
      allocationKey: allocationKey(observation),
      usageSecondsTotal: finiteNonNegative(observation.cpu?.usage_seconds_total),
      capacityCores: finiteNonNegative(observation.cpu?.capacity_cores),
      memoryUsageBytes: finiteNonNegative(observation.memory?.usage_bytes),
      memoryLimitBytes: finiteNonNegative(observation.memory?.limit_bytes),
      uptimeSeconds: uptimeSeconds(observation),
    }];
  });
  const targetIds = new Set([
    ...observed.filter((target) => target.cpuRatio !== null)
      .sort((left, right) => (right.cpuRatio ?? 0) - (left.cpuRatio ?? 0))
      .slice(0, RUNTIME_TREND_SERIES_LIMIT)
      .map((target) => target.seriesId),
    ...observed.filter((target) => target.uptimeSeconds !== null)
      .sort((left, right) => (right.uptimeSeconds ?? 0) - (left.uptimeSeconds ?? 0))
      .slice(0, RUNTIME_TREND_SERIES_LIMIT)
      .map((target) => target.seriesId),
  ]);
  const targets = observed.filter((target) => targetIds.has(target.seriesId)).map((target) => ({
    seriesId: target.seriesId,
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
    cpuCandidates: observed.flatMap((target): RuntimeTrendCPUCandidate[] => (
      target.cpuRatio !== null || (
        target.observedAt !== null && target.allocationKey !== null &&
        target.usageSecondsTotal !== null && target.capacityCores !== null && target.capacityCores > 0
      )
        ? [{
          seriesId: target.seriesId,
          label: target.label,
          cpuRatio: target.cpuRatio,
          uptimeSeconds: target.uptimeSeconds,
          observedAt: target.observedAt,
          startedAt: target.startedAt,
          allocationKey: target.allocationKey,
          usageSecondsTotal: target.usageSecondsTotal,
          capacityCores: target.capacityCores,
          reportedRatio: target.cpuRatio,
        }]
        : []
    )),
    memoryUsageBytes: pairedMemory.length === 0
      ? null
      : pairedMemory.reduce((total, target) => total + (target.memoryUsageBytes ?? 0), 0),
    memoryLimitBytes: pairedMemory.length === 0
      ? null
      : pairedMemory.reduce((total, target) => total + (target.memoryLimitBytes ?? 0), 0),
    tokenTotals: tokenTotals(snapshot.sessions, snapshot.loadedAt),
    inputTokensPerMinute: null,
    outputTokensPerMinute: null,
  };
}

function cpuRatios(previous: RuntimeTrendSample, next: RuntimeTrendSample): Map<string, number> {
  const previousCandidates = new Map(previous.cpuCandidates.map((candidate) => [candidate.seriesId, candidate]));
  const ratios = new Map<string, number>();
  for (const current of next.cpuCandidates) {
    const prior = previousCandidates.get(current.seriesId);
    if (current.reportedRatio !== null && !prior) {
      ratios.set(current.seriesId, current.reportedRatio);
      continue;
    }
    if (
      !prior || prior.allocationKey === null || prior.allocationKey !== current.allocationKey ||
      prior.observedAt === null || current.observedAt === null || current.observedAt <= prior.observedAt
    ) continue;
    if (current.reportedRatio !== null) {
      ratios.set(current.seriesId, current.reportedRatio);
      continue;
    }
    if (
      prior.startedAt === null || current.startedAt === null || prior.startedAt !== current.startedAt ||
      prior.usageSecondsTotal === null || current.usageSecondsTotal === null ||
      current.usageSecondsTotal < prior.usageSecondsTotal ||
      current.capacityCores === null || current.capacityCores <= 0
    ) continue;
    const usageCores = (current.usageSecondsTotal - prior.usageSecondsTotal) /
      (current.observedAt - prior.observedAt);
    const ratio = finiteNonNegative(usageCores / current.capacityCores);
    if (ratio !== null) ratios.set(current.seriesId, ratio);
  }
  return ratios;
}

function applyCPURatios(sample: RuntimeTrendSample, ratios: ReadonlyMap<string, number>): void {
  const uptime = sample.targets.filter((target) => target.uptimeSeconds !== null)
    .sort((left, right) => (right.uptimeSeconds ?? 0) - (left.uptimeSeconds ?? 0))
    .slice(0, RUNTIME_TREND_SERIES_LIMIT)
    .map((target) => ({ ...target, cpuRatio: null }));
  const cpu = sample.cpuCandidates.flatMap((candidate): RuntimeTrendTarget[] => {
    const ratio = ratios.get(candidate.seriesId);
    return ratio === undefined ? [] : [{
      seriesId: candidate.seriesId,
      label: candidate.label,
      cpuRatio: ratio,
      uptimeSeconds: candidate.uptimeSeconds,
    }];
  }).sort((left, right) => (right.cpuRatio ?? 0) - (left.cpuRatio ?? 0))
    .slice(0, RUNTIME_TREND_SERIES_LIMIT);
  const selected = new Map<string, RuntimeTrendTarget>(
    uptime.map((target) => [target.seriesId, target]),
  );
  for (const target of cpu) selected.set(target.seriesId, target);
  sample.targets = [...selected.values()];
}

function tokenRate(
  previous: RuntimeTrendSample,
  next: RuntimeTrendSample,
): Pick<RuntimeTrendSample, "inputTokensPerMinute" | "outputTokensPerMinute"> {
  const previousTotals = new Map(previous.tokenTotals.map((total) => [total.sessionId, total]));
  if (previousTotals.size === 0 || previousTotals.size !== next.tokenTotals.length ||
      next.tokenTotals.some((current) => !previousTotals.has(current.sessionId))) {
    return { inputTokensPerMinute: null, outputTokensPerMinute: null };
  }
  const pairs = next.tokenTotals.flatMap((current): Array<[RuntimeTrendTokenTotal, RuntimeTrendTokenTotal]> => {
    const prior = previousTotals.get(current.sessionId);
    return prior ? [[prior, current]] : [];
  });
  const inputRates = pairs.map(([prior, current]) => {
    const elapsedMinutes = (current.sampledAt - prior.sampledAt) / 60_000;
    const delta = current.inputTokens - prior.inputTokens;
    return elapsedMinutes > 0 && delta >= 0 ? delta / elapsedMinutes : null;
  });
  const outputRates = pairs.map(([prior, current]) => {
    const elapsedMinutes = (current.sampledAt - prior.sampledAt) / 60_000;
    const delta = current.outputTokens - prior.outputTokens;
    return elapsedMinutes > 0 && delta >= 0 ? delta / elapsedMinutes : null;
  });
  const inputRate = inputRates.length > 0 && inputRates.every((rate) => rate !== null)
    ? inputRates.reduce<number>((total, rate) => total + (rate ?? 0), 0)
    : null;
  const outputRate = outputRates.length > 0 && outputRates.every((rate) => rate !== null)
    ? outputRates.reduce<number>((total, rate) => total + (rate ?? 0), 0)
    : null;
  return {
    inputTokensPerMinute: inputRate,
    outputTokensPerMinute: outputRate,
  };
}

export function deriveTokenThroughput(samples: readonly RuntimeTrendSample[]): RuntimeTrendSample[] {
  return samples.map((sample, index) => {
    const current = { ...sample };
    const previous = samples[index - 1];
    if (previous) Object.assign(current, tokenRate(previous, sample));
    return current;
  });
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
  if (previous) {
    Object.assign(next, tokenRate(previous, next));
    applyCPURatios(next, cpuRatios(previous, next));
  }
  return [
    ...retained.map((sample) => ({ ...sample, cpuCandidates: [], tokenTotals: [] })),
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

export function runtimeTrendRange(
  samples: readonly RuntimeTrendSample[],
  range: RuntimeTrendRange,
): RuntimeTrendSample[] {
  const newest = samples.at(-1)?.sampledAt;
  if (newest === undefined) return [];
  const start = newest - range;
  return samples.filter((sample) => sample.sampledAt >= start && sample.sampledAt <= newest);
}
