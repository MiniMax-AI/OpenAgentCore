import { describe, expect, it } from "vitest";

import { projectCoreMetrics } from "./core-metrics";

describe("Core metrics projection", () => {
  it("keeps unmeasured figures null instead of zero", () => {
    const metrics = projectCoreMetrics({
      object: "core.metrics",
      range: { start: "2026-09-24T00:00:00Z", end: "2026-09-24T01:00:00Z", resolution_seconds: 60 },
      service: { status: "running", revision: "b134a1b5", execution_owner: true },
      execution: { slots_in_use: 3, slots_total: 4, queue_wait_ms: { p95: 2600 }, series: [{ start: "2026-09-24T00:00:00Z", queued: 1 }] },
      jobs: [{ id: "runtime_sampler", status: "weird", processed: 12 }],
    });
    expect(metrics.service).toMatchObject({ revision: "b134a1b5", execution_owner: true, started_at: null });
    expect(metrics.execution).toMatchObject({ slots_in_use: 3, queued_turns: null, connected_daemons: null });
    expect(metrics.execution.queue_wait_ms).toEqual({ p50: null, p95: 2600 });
    expect(metrics.execution.series[0]).toMatchObject({ queued: 1, in_progress: null });
    expect(metrics.database).toMatchObject({ size_bytes: null, pool: { in_use: null, idle: null, max: null }, series: [] });
    expect(metrics.jobs[0]).toMatchObject({ status: "unknown", processed: 12, failed: null });
    // A Core without the process extension reports it as missing, not zero.
    expect(metrics.process).toEqual({ memory_bytes: null, goroutines: null, cpu_cores: null, cpu_limit_cores: null, rss_bytes: null, memory_limit_bytes: null, series: [] });
  });

  it("never shows an unrecognised service status as running", () => {
    const metrics = projectCoreMetrics({
      object: "core.metrics",
      range: { start: "2026-09-24T00:00:00Z", end: "2026-09-24T01:00:00Z", resolution_seconds: 60 },
      service: { status: "draining" },
    });
    expect(metrics.service.status).toBe("unknown");
  });

  it("rejects a response that is not Core metrics or has no resolution", () => {
    expect(() => projectCoreMetrics({ object: "list" })).toThrow();
    expect(() => projectCoreMetrics({ object: "core.metrics", range: { start: "", end: "" }, service: { status: "running" } })).toThrow();
  });
});
