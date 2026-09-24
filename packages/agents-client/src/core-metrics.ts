import { AgentCoreError, OpenAIAgentsClient } from "./client";
import type { ReadOptions } from "./types";

/**
 * Core's own health: ingress, Turn execution, dependencies and the Core
 * process, aggregated server-side over a fixed range. Provisional contract
 * for `GET /core/v1/admin/core-metrics` (see docs/web/core-metrics-requirements.md);
 * every figure Core cannot measure is null, never zero.
 */
export type CoreMetricsRange = "1h" | "6h" | "24h" | "7d";
export type CoreDependencyHealth = "ok" | "degraded" | "down" | "unknown";
export type CoreDependencyKind = "database" | "queue" | "object_storage" | "model_provider" | "runtime_sampler" | "other";

export interface CoreLatency {
  p50: number | null;
  p95: number | null;
}

export interface CoreIngressBucket {
  start: string;
  success: number | null;
  client_error: number | null;
  server_error: number | null;
  p50_ms: number | null;
  p95_ms: number | null;
}

export interface CoreRouteFamily {
  /** Bounded route family, for example `agents_api`, `web_api`, `sandbox_admin`, `node_channel`. */
  family: string;
  requests: number | null;
  client_errors: number | null;
  server_errors: number | null;
  p95_ms: number | null;
}

export interface CoreExecutionBucket {
  start: string;
  completed: number | null;
  failed: number | null;
  cancelled: number | null;
  /** Highest queued Turn count seen in the bucket. */
  queued: number | null;
}

export interface CoreDependency {
  id: string;
  kind: CoreDependencyKind;
  name: string;
  status: CoreDependencyHealth;
  latency_p95_ms: number | null;
  /** Share of failed operations in the range, 0..1. */
  error_rate: number | null;
  checked_at: string | null;
}

export interface CoreProcessBucket {
  start: string;
  cpu_cores: number | null;
  memory_bytes: number | null;
}

export interface CoreMetrics {
  object: "core.metrics";
  range: { start: string; end: string; resolution_seconds: number };
  service: {
    status: "running" | "maintenance" | "degraded";
    version: string | null;
    started_at: string | null;
    instances: number | null;
  };
  ingress: {
    requests: number | null;
    client_errors: number | null;
    server_errors: number | null;
    latency_ms: CoreLatency;
    series: CoreIngressBucket[];
    routes: CoreRouteFamily[];
  };
  execution: {
    active_turns: number | null;
    queued_turns: number | null;
    completed: number | null;
    failed: number | null;
    cancelled: number | null;
    queue_wait_ms: CoreLatency;
    run_duration_ms: CoreLatency;
    series: CoreExecutionBucket[];
  };
  dependencies: CoreDependency[];
  process: {
    cpu_cores: number | null;
    cpu_limit_cores: number | null;
    memory_bytes: number | null;
    memory_limit_bytes: number | null;
    open_connections: number | null;
    disk_used_bytes: number | null;
    disk_total_bytes: number | null;
    series: CoreProcessBucket[];
  };
}

type Json = Record<string, unknown>;

function record(value: unknown, path: string): Json {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new AgentCoreError(`Core metrics: ${path} is not an object.`, 0, "invalid_response");
  return value as Json;
}

function number(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function text(value: unknown): string | null {
  return typeof value === "string" && value ? value : null;
}

function list<T>(value: unknown, item: (entry: Json, index: number) => T, path: string): T[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new AgentCoreError(`Core metrics: ${path} is not a list.`, 0, "invalid_response");
  return value.map((entry, index) => item(record(entry, `${path}[${index}]`), index));
}

function latency(value: unknown): CoreLatency {
  const entry = value && typeof value === "object" ? value as Json : {};
  return { p50: number(entry.p50), p95: number(entry.p95) };
}

const dependencyKinds = new Set<CoreDependencyKind>(["database", "queue", "object_storage", "model_provider", "runtime_sampler", "other"]);
const dependencyHealth = new Set<CoreDependencyHealth>(["ok", "degraded", "down", "unknown"]);

/** Validates the envelope and normalises every missing figure to null. */
export function projectCoreMetrics(value: unknown): CoreMetrics {
  const body = record(value, "response");
  if (body.object !== "core.metrics") throw new AgentCoreError("Core metrics: unexpected object type.", 0, "invalid_response");
  const range = record(body.range, "range");
  const service = record(body.service, "service");
  const ingress = record(body.ingress, "ingress");
  const execution = record(body.execution, "execution");
  const process = record(body.process, "process");
  const status = service.status === "maintenance" || service.status === "degraded" ? service.status : "running";
  return {
    object: "core.metrics",
    range: { start: String(range.start ?? ""), end: String(range.end ?? ""), resolution_seconds: number(range.resolution_seconds) ?? 60 },
    service: { status, version: text(service.version), started_at: text(service.started_at), instances: number(service.instances) },
    ingress: {
      requests: number(ingress.requests),
      client_errors: number(ingress.client_errors),
      server_errors: number(ingress.server_errors),
      latency_ms: latency(ingress.latency_ms),
      series: list(ingress.series, (entry) => ({
        start: String(entry.start ?? ""),
        success: number(entry.success),
        client_error: number(entry.client_error),
        server_error: number(entry.server_error),
        p50_ms: number(entry.p50_ms),
        p95_ms: number(entry.p95_ms),
      }), "ingress.series"),
      routes: list(ingress.routes, (entry) => ({
        family: String(entry.family ?? "other"),
        requests: number(entry.requests),
        client_errors: number(entry.client_errors),
        server_errors: number(entry.server_errors),
        p95_ms: number(entry.p95_ms),
      }), "ingress.routes"),
    },
    execution: {
      active_turns: number(execution.active_turns),
      queued_turns: number(execution.queued_turns),
      completed: number(execution.completed),
      failed: number(execution.failed),
      cancelled: number(execution.cancelled),
      queue_wait_ms: latency(execution.queue_wait_ms),
      run_duration_ms: latency(execution.run_duration_ms),
      series: list(execution.series, (entry) => ({
        start: String(entry.start ?? ""),
        completed: number(entry.completed),
        failed: number(entry.failed),
        cancelled: number(entry.cancelled),
        queued: number(entry.queued),
      }), "execution.series"),
    },
    dependencies: list(body.dependencies, (entry, index) => ({
      id: String(entry.id ?? index),
      kind: dependencyKinds.has(entry.kind as CoreDependencyKind) ? entry.kind as CoreDependencyKind : "other",
      name: String(entry.name ?? entry.id ?? ""),
      status: dependencyHealth.has(entry.status as CoreDependencyHealth) ? entry.status as CoreDependencyHealth : "unknown",
      latency_p95_ms: number(entry.latency_p95_ms),
      error_rate: number(entry.error_rate),
      checked_at: text(entry.checked_at),
    }), "dependencies"),
    process: {
      cpu_cores: number(process.cpu_cores),
      cpu_limit_cores: number(process.cpu_limit_cores),
      memory_bytes: number(process.memory_bytes),
      memory_limit_bytes: number(process.memory_limit_bytes),
      open_connections: number(process.open_connections),
      disk_used_bytes: number(process.disk_used_bytes),
      disk_total_bytes: number(process.disk_total_bytes),
      series: list(process.series, (entry) => ({
        start: String(entry.start ?? ""),
        cpu_cores: number(entry.cpu_cores),
        memory_bytes: number(entry.memory_bytes),
      }), "process.series"),
    },
  };
}

/** Reads Core's own metrics through the console's Web API (`/core/v1/admin`). */
export class CoreMetricsClient extends OpenAIAgentsClient {
  async retrieveCoreMetrics(range: CoreMetricsRange, options?: ReadOptions): Promise<CoreMetrics> {
    const value: unknown = await this.request(`/core-metrics?range=${encodeURIComponent(range)}`, { signal: options?.signal }, undefined, false);
    return projectCoreMetrics(value);
  }
}
