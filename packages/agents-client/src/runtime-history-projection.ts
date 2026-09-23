import { canonicalUuid, exactFields, isNonnegativeInteger, isRecord, sameResourceId } from "./response-projection";
import type {
  RuntimeHistory,
  RuntimeHistoryCPU,
  RuntimeHistoryCapabilities,
  RuntimeHistoryCoveragePoint,
  RuntimeHistoryMemory,
  RuntimeHistoryPoint,
  RuntimeHistoryQuery,
  RuntimeHistorySeries,
  RuntimeHistoryTokenUsagePoint,
} from "./types";

type InvalidRuntimeHistory = (message?: string) => never;

const capabilityFields = new Set([
  "object", "available", "reason", "collection_mode", "sample_interval_seconds",
  "retention_seconds", "minimum_step_seconds", "maximum_range_seconds", "maximum_points", "metrics",
]);
const historyFields = new Set([
  "object", "source", "session_id", "requested_range", "resolution_seconds", "generated_at", "coverage", "series", "token_usage",
]);
const rangeFields = new Set(["start", "end"]);
const coverageFields = new Set([
  "retained_start", "first_sample_at", "last_sample_at", "sample_count", "expected_sample_count", "buckets",
]);
const coveragePointFields = new Set([
  "start", "end", "first_observed_at", "last_observed_at", "observation_count", "observed_count", "unavailable_count",
]);
const seriesFields = new Set(["environment_id", "allocation_id", "started_at", "provider_type", "points"]);
const timeFields = new Set(["seconds", "nanoseconds"]);
const pointFields = new Set([...coveragePointFields, "cpu", "memory"]);
const cpuFields = new Set(["contributor_count", "utilization_ratio", "capacity_cores"]);
const memoryFields = new Set(["contributor_count", "usage_bytes", "limit_bytes"]);
const tokenUsageFields = new Set(["start", "end", "sampled_at", "input_tokens", "output_tokens"]);
const providerTypePattern = /^[a-z][a-z0-9_]{0,31}$/;
const metrics = new Set(["cpu", "memory", "tokens"]);
const reasons = new Set(["not_configured", "periodic_collection_required"]);
const maximumSeries = 1_000;
const maximumTotalPoints = 100_000;

function positiveInteger(value: unknown): value is number {
  return isNonnegativeInteger(value) && value > 0;
}

function nullableNonnegativeInteger(value: unknown, invalid: InvalidRuntimeHistory): number | null {
  if (value === null) return null;
  if (!isNonnegativeInteger(value)) return invalid();
  return value;
}

function nullablePositiveInteger(value: unknown, invalid: InvalidRuntimeHistory): number | null {
  if (value === null) return null;
  if (!positiveInteger(value)) return invalid();
  return value;
}

function nullableNonnegativeNumber(value: unknown, invalid: InvalidRuntimeHistory): number | null {
  if (value === null) return null;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return invalid();
  return value;
}

function nullablePositiveNumber(value: unknown, invalid: InvalidRuntimeHistory): number | null {
  const projected = nullableNonnegativeNumber(value, invalid);
  if (projected === 0) return invalid();
  return projected;
}

export function projectRuntimeHistoryCapabilities(
  value: unknown,
  invalid: InvalidRuntimeHistory,
): RuntimeHistoryCapabilities {
  if (
    !isRecord(value) || !exactFields(value, capabilityFields) ||
    value.object !== "agent.runtime_history_capabilities" || typeof value.available !== "boolean" ||
    !(value.reason === null || (typeof value.reason === "string" && reasons.has(value.reason))) ||
    !(value.collection_mode === null || value.collection_mode === "on_read" || value.collection_mode === "periodic") ||
    !Array.isArray(value.metrics) || value.metrics.some((metric) => typeof metric !== "string" || !metrics.has(metric)) ||
    new Set(value.metrics).size !== value.metrics.length || value.metrics.length > metrics.size
  ) return invalid();

  const sampleInterval = nullablePositiveInteger(value.sample_interval_seconds, invalid);
  const retention = nullablePositiveInteger(value.retention_seconds, invalid);
  const minimumStep = nullablePositiveInteger(value.minimum_step_seconds, invalid);
  const maximumRange = nullablePositiveInteger(value.maximum_range_seconds, invalid);
  const maximumPoints = nullablePositiveInteger(value.maximum_points, invalid);
  const disabled = value.reason === "not_configured";
  const onRead = value.reason === "periodic_collection_required";
  if (
    (disabled && (
      value.available || value.collection_mode !== null || sampleInterval !== null || retention !== null ||
      minimumStep !== null || maximumRange !== null || maximumPoints !== null || value.metrics.length !== 0
    )) ||
    (onRead && (
      value.available || value.collection_mode !== "on_read" || sampleInterval !== null || retention === null ||
      minimumStep === null || maximumRange === null || maximumPoints === null || value.metrics.length === 0
    )) ||
    (value.available && (
      value.reason !== null || value.collection_mode !== "periodic" || sampleInterval === null || retention === null ||
      minimumStep === null || maximumRange === null || maximumPoints === null || value.metrics.length === 0
    )) ||
    (!value.available && value.reason === null) ||
    (retention !== null && maximumRange !== null && maximumRange > retention) ||
    (maximumPoints !== null && (maximumPoints < 2 || maximumPoints > 10_000))
  ) return invalid();

  return {
    object: "agent.runtime_history_capabilities",
    available: value.available,
    reason: value.reason as RuntimeHistoryCapabilities["reason"],
    collection_mode: value.collection_mode as RuntimeHistoryCapabilities["collection_mode"],
    sample_interval_seconds: sampleInterval,
    retention_seconds: retention,
    minimum_step_seconds: minimumStep,
    maximum_range_seconds: maximumRange,
    maximum_points: maximumPoints,
    metrics: [...value.metrics] as RuntimeHistoryCapabilities["metrics"],
  };
}

function projectCoveragePoint(
  value: unknown,
  fields: Set<string>,
  requestedStart: number,
  requestedEnd: number,
  resolution: number,
  invalid: InvalidRuntimeHistory,
): RuntimeHistoryCoveragePoint {
  if (
    !isRecord(value) || !exactFields(value, fields) ||
    !isNonnegativeInteger(value.start) || !positiveInteger(value.end) ||
    value.start < requestedStart || value.end <= value.start || value.end > requestedEnd || value.end - value.start > resolution ||
    !isNonnegativeInteger(value.observation_count) || !isNonnegativeInteger(value.observed_count) ||
    !isNonnegativeInteger(value.unavailable_count) || value.observed_count + value.unavailable_count !== value.observation_count
  ) return invalid();
  const first = nullableNonnegativeInteger(value.first_observed_at, invalid);
  const last = nullableNonnegativeInteger(value.last_observed_at, invalid);
  if (
    (value.observation_count === 0 && (first !== null || last !== null)) ||
    (value.observation_count > 0 && (
      first === null || last === null || first < value.start || last < first || last >= value.end
    ))
  ) return invalid();
  return {
    start: value.start,
    end: value.end,
    first_observed_at: first,
    last_observed_at: last,
    observation_count: value.observation_count,
    observed_count: value.observed_count,
    unavailable_count: value.unavailable_count,
  };
}

function projectCPU(value: unknown, observedCount: number, invalid: InvalidRuntimeHistory): RuntimeHistoryCPU | null {
  if (value === null) return null;
  if (!isRecord(value) || !exactFields(value, cpuFields) || !positiveInteger(value.contributor_count) || value.contributor_count > observedCount) return invalid();
  const utilization = nullableNonnegativeNumber(value.utilization_ratio, invalid);
  const capacity = nullablePositiveNumber(value.capacity_cores, invalid);
  if (utilization === null && capacity === null) return invalid();
  return { contributor_count: value.contributor_count, utilization_ratio: utilization, capacity_cores: capacity };
}

function projectMemory(value: unknown, observedCount: number, invalid: InvalidRuntimeHistory): RuntimeHistoryMemory | null {
  if (value === null) return null;
  if (!isRecord(value) || !exactFields(value, memoryFields) || !positiveInteger(value.contributor_count) || value.contributor_count > observedCount) return invalid();
  const usage = nullableNonnegativeInteger(value.usage_bytes, invalid);
  const limit = nullablePositiveInteger(value.limit_bytes, invalid);
  if (usage === null && limit === null) return invalid();
  return { contributor_count: value.contributor_count, usage_bytes: usage, limit_bytes: limit };
}

function projectPoint(
  value: unknown,
  requestedStart: number,
  requestedEnd: number,
  resolution: number,
  startedAt: number,
  invalid: InvalidRuntimeHistory,
): RuntimeHistoryPoint {
  const coverage = projectCoveragePoint(value, pointFields, requestedStart, requestedEnd, resolution, invalid);
  if (!isRecord(value) || coverage.end <= startedAt || (coverage.first_observed_at !== null && coverage.first_observed_at < startedAt)) return invalid();
  const cpu = projectCPU(value.cpu, coverage.observed_count, invalid);
  const memory = projectMemory(value.memory, coverage.observed_count, invalid);
  if (coverage.observation_count === 0 && (cpu !== null || memory !== null)) return invalid();
  return { ...coverage, cpu, memory };
}

function ordered(points: readonly { start: number; end: number }[]): boolean {
  return points.every((point, index) => index === 0 || points[index - 1]!.end <= point.start);
}

function projectTokenUsagePoint(
  value: unknown,
  requestedStart: number,
  requestedEnd: number,
  resolution: number,
  generatedAt: number,
  invalid: InvalidRuntimeHistory,
): RuntimeHistoryTokenUsagePoint {
  if (
    !isRecord(value) || !exactFields(value, tokenUsageFields) ||
    !isNonnegativeInteger(value.start) || !positiveInteger(value.end) ||
    value.start < requestedStart || value.end <= value.start || value.end > requestedEnd || value.end - value.start > resolution ||
    !isNonnegativeInteger(value.sampled_at) || value.sampled_at < value.start || value.sampled_at >= value.end || value.sampled_at > generatedAt ||
    !isNonnegativeInteger(value.input_tokens) || !isNonnegativeInteger(value.output_tokens)
  ) return invalid();
  return {
    start: value.start,
    end: value.end,
    sampled_at: value.sampled_at,
    input_tokens: value.input_tokens,
    output_tokens: value.output_tokens,
  };
}

function projectSeries(
  value: unknown,
  requestedStart: number,
  requestedEnd: number,
  resolution: number,
  generatedAt: number,
  maximumPoints: number,
  invalid: InvalidRuntimeHistory,
): RuntimeHistorySeries {
  if (!isRecord(value) || !exactFields(value, seriesFields) || !Array.isArray(value.points)) return invalid();
  const environmentId = canonicalUuid(value.environment_id);
  const allocationId = canonicalUuid(value.allocation_id);
  if (!isRecord(value.started_at) || !exactFields(value.started_at, timeFields)) return invalid();
  const startedAtSeconds = value.started_at.seconds;
  const startedAtNanoseconds = value.started_at.nanoseconds;
  if (
    environmentId === null || allocationId === null || !isNonnegativeInteger(startedAtSeconds) ||
    !isNonnegativeInteger(startedAtNanoseconds) || startedAtNanoseconds > 999_999_999 ||
    startedAtSeconds > generatedAt || startedAtSeconds >= requestedEnd ||
    typeof value.provider_type !== "string" || !providerTypePattern.test(value.provider_type) || value.points.length > maximumPoints
  ) return invalid();
  const points = value.points.map((point) => projectPoint(
    point, requestedStart, requestedEnd, resolution, startedAtSeconds, invalid,
  ));
  if (!ordered(points)) return invalid();
  return {
    environment_id: environmentId,
    allocation_id: allocationId,
    started_at: { seconds: startedAtSeconds, nanoseconds: startedAtNanoseconds },
    provider_type: value.provider_type,
    points,
  };
}

export function projectRuntimeHistory(
  value: unknown,
  expectedSessionId: string,
  requested: RuntimeHistoryQuery,
  invalid: InvalidRuntimeHistory,
): RuntimeHistory {
  if (
    !isRecord(value) || !exactFields(value, historyFields) || value.object !== "agent.runtime_history" || value.source !== "durable" ||
    typeof value.session_id !== "string" || !sameResourceId(value.session_id, expectedSessionId) ||
    !isRecord(value.requested_range) || !exactFields(value.requested_range, rangeFields) ||
    value.requested_range.start !== requested.start || value.requested_range.end !== requested.end ||
    !positiveInteger(value.resolution_seconds) || !isNonnegativeInteger(value.generated_at) ||
    !isRecord(value.coverage) || !exactFields(value.coverage, coverageFields) || !Array.isArray(value.coverage.buckets) ||
    !Array.isArray(value.series) || !Array.isArray(value.token_usage)
  ) return invalid();
  const sessionId = canonicalUuid(value.session_id);
  if (sessionId === null) return invalid();
  const generatedAt = value.generated_at as number;
  const maximumPoints = requested.maxPoints ?? 120;
  if (value.coverage.buckets.length > maximumPoints || value.series.length > maximumSeries) return invalid();
  const retainedStart = value.coverage.retained_start;
  const firstSample = nullableNonnegativeInteger(value.coverage.first_sample_at, invalid);
  const lastSample = nullableNonnegativeInteger(value.coverage.last_sample_at, invalid);
  if (
    generatedAt < requested.start ||
    !isNonnegativeInteger(retainedStart) || retainedStart < requested.start || retainedStart > requested.end || retainedStart > generatedAt ||
    !isNonnegativeInteger(value.coverage.sample_count) || !isNonnegativeInteger(value.coverage.expected_sample_count) ||
    ((firstSample === null) !== (lastSample === null)) ||
    (firstSample !== null && (firstSample < retainedStart || lastSample! < firstSample || lastSample! >= requested.end || lastSample! > generatedAt))
  ) return invalid();
  const buckets = value.coverage.buckets.map((point) => projectCoveragePoint(
    point, coveragePointFields, requested.start, requested.end, value.resolution_seconds as number, invalid,
  ));
  if (!ordered(buckets)) return invalid();
  const sampleCount = buckets.reduce((sum, point) => sum + point.observation_count, 0);
  const observedTimes = buckets.flatMap((point) => point.first_observed_at === null
    ? []
    : [point.first_observed_at, point.last_observed_at as number]);
  if (
    sampleCount !== value.coverage.sample_count ||
    (observedTimes.length === 0 && firstSample !== null) ||
    (observedTimes.length > 0 && (
      firstSample !== Math.min(...observedTimes) || lastSample !== Math.max(...observedTimes)
    ))
  ) return invalid();
  const series = value.series.map((entry) => projectSeries(
    entry, requested.start, requested.end, value.resolution_seconds as number, generatedAt, maximumPoints, invalid,
  ));
  const tokenUsage = value.token_usage.map((entry) => projectTokenUsagePoint(
    entry, requested.start, requested.end, value.resolution_seconds as number, generatedAt, invalid,
  ));
  if (
    new Set(series.map((entry) => entry.environment_id)).size > 1 ||
    new Set(series.map((entry) => entry.allocation_id)).size !== series.length ||
    tokenUsage.length > maximumPoints || !ordered(tokenUsage) ||
    buckets.length + tokenUsage.length + series.reduce((sum, entry) => sum + entry.points.length, 0) > maximumTotalPoints ||
    tokenUsage.some((point) => point.sampled_at < retainedStart) ||
    series.some((entry) => entry.points.some((point) => point.first_observed_at !== null && point.first_observed_at < retainedStart)) ||
    series.some((entry) => entry.points.some((point) => point.last_observed_at !== null && point.last_observed_at > generatedAt))
  ) return invalid();
  return {
    object: "agent.runtime_history",
    source: "durable",
    session_id: sessionId,
    requested_range: { start: requested.start, end: requested.end },
    resolution_seconds: value.resolution_seconds,
    generated_at: generatedAt,
    coverage: {
      retained_start: retainedStart,
      first_sample_at: firstSample,
      last_sample_at: lastSample,
      sample_count: value.coverage.sample_count,
      expected_sample_count: value.coverage.expected_sample_count,
      buckets,
    },
    series,
    token_usage: tokenUsage,
  };
}
