/** Bounded deployment metrics returned by the administrator API. */
export interface CoreMetricsView {
  object: "core.metrics";
  range: { start: string; end: string; resolution_seconds: number };
  service: { status: string; revision: string | null; started_at: string | null; execution_owner: boolean | null };
  execution: {
    slots_in_use: number | null;
    slots_total: number | null;
    queued_turns: number | null;
    waiting_for_daemon: number | null;
    in_progress_turns: number | null;
    oldest_queued_seconds: number | null;
    connected_daemons: number | null;
    interrupted: number | null;
    unavailable: number | null;
    queue_wait_ms: { p50: number | null; p95: number | null };
    series: Array<{ start: string; queued: number | null; in_progress: number | null; queue_wait_p95_ms: number | null }>;
  };
  database: {
    ping_ms: { p50: number | null; p95: number | null };
    pool: { in_use: number | null; idle: number | null; max: number | null };
    size_bytes: number | null;
    series: Array<{ start: string; ping_p95_ms: number | null; pool_in_use: number | null }>;
  };
  jobs: Array<{ id: string; status: string; last_run_at: string | null; processed: number | null; failed: number | null }>;
  process: { memory_bytes: number | null; goroutines: number | null };
}
