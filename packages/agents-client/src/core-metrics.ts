import { AgentCoreError } from "./client";
import { CoreRequester, type CoreClientOptions } from "./core-request";
import { isOneOf } from "./response-projection";
import type { ReadOptions } from "./types";
import { jobStatusValues, serviceStatusValues, type CoremetricsView, type JobStatus, type Latency, type ServiceState, type ServiceStatus } from "./generated/core-api";

function invalidCoreMetrics(): never {
  throw new AgentCoreError("Core metrics: the response is not JSON.", 0, "invalid_response");
}

export type CoreMetricsRange = "1h" | "6h" | "24h" | "7d";
export type CoreJobStatus = JobStatus;
/**
 * Core's own health as the one `oac-core` process sees it: execution slots
 * and the Turn queue (a Postgres table polled by the worker), connected
 * daemons, the PostgreSQL database, background jobs and the process itself.
 * `GET /core/v1/metrics`, defined in
 * contracts/agents-api/core-metrics.md; every figure Core cannot measure is
 * null, never zero. The client reads a service status it does not recognise
 * as `unknown`, which is never shown as running.
 */
export type CoreMetrics = Omit<CoremetricsView, "service"> & { service: Omit<ServiceState, "status"> & { status: ServiceStatus | "unknown" } };

type Json = Record<string, unknown>;

function record(value: unknown, path: string): Json {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new AgentCoreError(`Core metrics: ${path} is not an object.`, 0, "invalid_response");
  return value as Json;
}

function optional(value: unknown): Json {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Json : {};
}

function number(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function count(value: unknown, path: string): number {
  const result = number(value);
  if (result === null) throw new AgentCoreError(`Core metrics: ${path} is missing.`, 0, "invalid_response");
  return result;
}

function text(value: unknown): string | null {
  return typeof value === "string" && value ? value : null;
}

function list<T>(value: unknown, item: (entry: Json, index: number) => T, path: string): T[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new AgentCoreError(`Core metrics: ${path} is not a list.`, 0, "invalid_response");
  return value.map((entry, index) => item(record(entry, `${path}[${index}]`), index));
}

function latency(value: unknown): Latency {
  const entry = optional(value);
  return { p50: number(entry.p50), p95: number(entry.p95) };
}

/** Validates the envelope and normalises every missing figure to null. */
export function projectCoreMetrics(value: unknown): CoreMetrics {
  const body = record(value, "response");
  if (body.object !== "core.metrics") throw new AgentCoreError("Core metrics: unexpected object type.", 0, "invalid_response");
  const range = record(body.range, "range");
  const service = record(body.service, "service");
  const execution = record(body.execution, "execution");
  const database = optional(body.database);
  const pool = optional(database.pool);
  const process = optional(body.process);
  const status = isOneOf(serviceStatusValues, service.status) ? service.status : "unknown";
  const resolution = number(range.resolution_seconds);
  if (resolution === null || resolution <= 0) throw new AgentCoreError("Core metrics: range.resolution_seconds is missing.", 0, "invalid_response");
  return {
    object: "core.metrics",
    range: { start: String(range.start ?? ""), end: String(range.end ?? ""), resolution_seconds: resolution },
    service: {
      status,
      revision: text(service.revision),
      started_at: text(service.started_at),
      execution_owner: typeof service.execution_owner === "boolean" ? service.execution_owner : null,
    },
    execution: {
      slots_in_use: count(execution.slots_in_use, "execution.slots_in_use"),
      slots_total: count(execution.slots_total, "execution.slots_total"),
      queued_turns: number(execution.queued_turns),
      waiting_for_daemon: number(execution.waiting_for_daemon),
      in_progress_turns: number(execution.in_progress_turns),
      oldest_queued_seconds: number(execution.oldest_queued_seconds),
      connected_daemons: count(execution.connected_daemons, "execution.connected_daemons"),
      interrupted: number(execution.interrupted),
      unavailable: number(execution.unavailable),
      queue_wait_ms: latency(execution.queue_wait_ms),
      series: list(execution.series, (entry) => ({
        start: String(entry.start ?? ""),
        queued: number(entry.queued),
        in_progress: number(entry.in_progress),
        queue_wait_p95_ms: number(entry.queue_wait_p95_ms),
      }), "execution.series"),
    },
    database: {
      ping_ms: latency(database.ping_ms),
      pool: { in_use: number(pool.in_use), idle: number(pool.idle), max: number(pool.max) },
      size_bytes: number(database.size_bytes),
      series: list(database.series, (entry) => ({
        start: String(entry.start ?? ""),
        ping_p95_ms: number(entry.ping_p95_ms),
        pool_in_use: number(entry.pool_in_use),
      }), "database.series"),
    },
    jobs: list(body.jobs, (entry, index) => ({
      id: String(entry.id ?? index),
      status: isOneOf(jobStatusValues, entry.status) ? entry.status : "unknown",
      last_run_at: text(entry.last_run_at),
      processed: number(entry.processed),
      failed: number(entry.failed),
    }), "jobs"),
    process: {
      memory_bytes: number(process.memory_bytes),
      goroutines: number(process.goroutines),
      cpu_cores: number(process.cpu_cores),
      cpu_limit_cores: number(process.cpu_limit_cores),
      rss_bytes: number(process.rss_bytes),
      memory_limit_bytes: number(process.memory_limit_bytes),
      series: list(process.series, (entry) => ({
        start: String(entry.start ?? ""),
        cpu_cores: number(entry.cpu_cores),
        rss_bytes: number(entry.rss_bytes),
      }), "process.series"),
    },
  };
}

/**
 * Reads Core's own metrics from `/metrics` under `baseUrl` (default `/core/v1`),
 * through an authenticated console or an explicit Core key.
 */
export class CoreMetricsClient {
  readonly #core: CoreRequester;

  constructor(options: CoreClientOptions = {}) {
    this.#core = new CoreRequester(options.baseUrl ?? "/core/v1", options.token, options.fetch, invalidCoreMetrics);
  }

  async retrieveCoreMetrics(range: CoreMetricsRange, options?: ReadOptions): Promise<CoreMetrics> {
    return projectCoreMetrics(await this.#core.json(`/metrics?range=${encodeURIComponent(range)}`, options));
  }
}
