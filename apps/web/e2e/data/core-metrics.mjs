// Synthetic Core metrics for the browser acceptance fixture (GET /core/v1/metrics).
const RANGES = { "1h": [60, 60], "6h": [72, 300], "24h": [96, 900], "7d": [84, 7200] };

export function coreMetrics(range = "1h", now = Math.floor(Date.now() / 1000)) {
  const [count, step] = RANGES[range] ?? RANGES["1h"];
  const end = Math.floor(now / step) * step;
  const start = end - count * step;
  const wave = (i, period, phase = 0) => Math.sin((i / period) * Math.PI * 2 + phase);
  const execution = [];
  const database = [];
  const processSeries = [];
  for (let i = 0; i < count; i += 1) {
    const at = new Date((start + i * step) * 1000).toISOString();
    const load = 1 + 0.4 * wave(i, 24) + 0.2 * wave(i, 7, 1);
    const running = Math.max(0, Math.min(4, Math.round(2.4 * load)));
    const burst = i % 23 === 7 ? 5 : 0;
    const queued = Math.max(0, Math.round((running >= 4 ? 2 : 0) + burst + (load > 1.3 ? 1 : 0)));
    execution.push({ start: at, queued, in_progress: running, queue_wait_p95_ms: queued ? Math.round(900 + 700 * queued) : 180 });
    database.push({ start: at, ping_p95_ms: Number((2.2 + 0.8 * load + (i % 31 === 11 ? 9 : 0)).toFixed(1)), pool_in_use: Math.max(1, Math.round(4 + 3 * load)) });
    processSeries.push({ start: at, cpu_cores: Number((0.22 + 0.24 * load + (i % 23 === 7 ? 0.5 : 0)).toFixed(2)), rss_bytes: Math.round((296 + 34 * load + 0.4 * i) * 2 ** 20) });
  }
  const ago = (s) => new Date((now - s) * 1000).toISOString();
  return {
    object: "core.metrics",
    range: { start: new Date(start * 1000).toISOString(), end: new Date(end * 1000).toISOString(), resolution_seconds: step },
    service: { status: "running", revision: "b134a1b5", started_at: ago(86400 * 3 + 4 * 3600), execution_owner: true },
    execution: {
      slots_in_use: 3, slots_total: 4, queued_turns: 2, waiting_for_daemon: 1, in_progress_turns: 3,
      oldest_queued_seconds: 130, connected_daemons: 9, interrupted: 0, unavailable: 3,
      queue_wait_ms: { p50: 420, p95: 2600 },
      series: execution,
    },
    database: { ping_ms: { p50: 1.8, p95: 3.4 }, pool: { in_use: 6, idle: 4, max: 20 }, size_bytes: Math.round(1.24 * 2 ** 30), series: database },
    jobs: [
      { id: "scheduler", status: "ok", last_run_at: ago(0), processed: 1, failed: 0 },
      { id: "runtime_sampler", status: "ok", last_run_at: ago(18), processed: 12, failed: 1 },
      { id: "history_cleanup", status: "ok", last_run_at: ago(41), processed: 230, failed: 0 },
      { id: "audit_cleanup", status: "ok", last_run_at: ago(41), processed: 0, failed: 0 },
    ],
    // Resident memory unmeasured: the console must show it as missing, never as zero.
    process: { memory_bytes: Math.round(182 * 2 ** 20), goroutines: 214, cpu_cores: 0.41, cpu_limit_cores: 2, rss_bytes: null, memory_limit_bytes: 2 ** 30, series: processSeries },
  };
}
