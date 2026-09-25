import { AgentCoreError } from "./client";
import { CoreRequester, type CoreClientOptions } from "./core-request";
import type { ReadOptions } from "./types";

function invalidCoreMetrics(): never {
  throw new AgentCoreError("Core metrics: the response is not JSON.", 0, "invalid_response");
}

/**
 * Core's own health as the one `agents-api` process sees it: execution slots
 * and the Turn queue (a Postgres table polled by the worker), connected
 * daemons, the PostgreSQL database, background jobs and the process itself.
 * `GET /core/v1/metrics`, defined in
 * contracts/agents-api/core-metrics.md; every figure Core cannot measure is
 * null, never zero.
 */
export type CoreMetricsRange = "1h" | "6h" | "24h" | "7d";
export type CoreJobStatus = "ok" | "failing" | "stopped" | "unknown";

export interface CoreLatency {
  p50: number | null;
  p95: number | null;
}

export interface CoreExecutionBucket {
  start: string;
  /** Highest queued Turn count seen in the bucket. */
  queued: number | null;
  /** Highest in-progress Turn count seen in the bucket. */
  in_progress: number | null;
  queue_wait_p95_ms: number | null;
}

export interface CoreDatabaseBucket {
  start: string;
  ping_p95_ms: number | null;
  pool_in_use: number | null;
}

export interface CoreProcessBucket {
  start: string;
  /** Highest CPU use observed in the bucket, in cores. */
  cpu_cores: number | null;
  /** Highest resident memory observed in the bucket. */
  rss_bytes: number | null;
}

export interface CoreJob {
  /** `scheduler`, `runtime_sampler`, `history_cleanup`, `audit_cleanup`, or another bounded name. */
  id: string;
  status: CoreJobStatus;
  last_run_at: string | null;
  /** Items the last run handled (Turns dispatched, Runtimes sampled, rows removed). */
  processed: number | null;
  failed: number | null;
}

export interface CoreMetrics {
  object: "core.metrics";
  range: { start: string; end: string; resolution_seconds: number };
  service: {
    /** `unknown` stands for a status this client does not recognise; it is never shown as running. */
    status: "running" | "maintenance" | "degraded" | "unknown";
    /** Build revision (source commit) of the running Core. */
    revision: string | null;
    started_at: string | null;
    /** Whether this process holds the database's execution lease. */
    execution_owner: boolean | null;
  };
  execution: {
    slots_in_use: number | null;
    slots_total: number | null;
    queued_turns: number | null;
    /** Queued Turns whose Session has no connected daemon (part of queued_turns). */
    waiting_for_daemon: number | null;
    in_progress_turns: number | null;
    oldest_queued_seconds: number | null;
    connected_daemons: number | null;
    /** Turns failed with execution_interrupted in the range. */
    interrupted: number | null;
    /** Requests refused with execution_unavailable in the range. */
    unavailable: number | null;
    queue_wait_ms: CoreLatency;
    series: CoreExecutionBucket[];
  };
  database: {
    ping_ms: CoreLatency;
    pool: { in_use: number | null; idle: number | null; max: number | null };
    size_bytes: number | null;
    series: CoreDatabaseBucket[];
  };
  jobs: CoreJob[];
  process: {
    /** Go heap in use (runtime.MemStats.Alloc), not resident memory. */
    memory_bytes: number | null;
    goroutines: number | null;
    /**
     * Requested extension (docs/web/core-process-metrics-requirements.md):
     * CPU used over the last sample interval, in cores; the CPU available to
     * the process; resident memory; its memory limit; and a series. Null until
     * Core reports them.
     */
    cpu_cores: number | null;
    cpu_limit_cores: number | null;
    rss_bytes: number | null;
    memory_limit_bytes: number | null;
    series: CoreProcessBucket[];
  };
}

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

function text(value: unknown): string | null {
  return typeof value === "string" && value ? value : null;
}

function list<T>(value: unknown, item: (entry: Json, index: number) => T, path: string): T[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new AgentCoreError(`Core metrics: ${path} is not a list.`, 0, "invalid_response");
  return value.map((entry, index) => item(record(entry, `${path}[${index}]`), index));
}

function latency(value: unknown): CoreLatency {
  const entry = optional(value);
  return { p50: number(entry.p50), p95: number(entry.p95) };
}

const jobStatuses = new Set<CoreJobStatus>(["ok", "failing", "stopped", "unknown"]);

/** Validates the envelope and normalises every missing figure to null. */
export function projectCoreMetrics(value: unknown): CoreMetrics {
  const body = record(value, "response");
  if (body.object !== "core.metrics") throw new AgentCoreError("Core metrics: unexpected object type.", 0, "invalid_response");
  const range = record(body.range, "range");
  const service = record(body.service, "service");
  const execution = optional(body.execution);
  const database = optional(body.database);
  const pool = optional(database.pool);
  const process = optional(body.process);
  const status = service.status === "running" || service.status === "maintenance" || service.status === "degraded" ? service.status : "unknown";
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
      slots_in_use: number(execution.slots_in_use),
      slots_total: number(execution.slots_total),
      queued_turns: number(execution.queued_turns),
      waiting_for_daemon: number(execution.waiting_for_daemon),
      in_progress_turns: number(execution.in_progress_turns),
      oldest_queued_seconds: number(execution.oldest_queued_seconds),
      connected_daemons: number(execution.connected_daemons),
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
      status: jobStatuses.has(entry.status as CoreJobStatus) ? entry.status as CoreJobStatus : "unknown",
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
