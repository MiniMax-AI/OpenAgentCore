import { describe, expect, it } from "vitest";

import { projectCoreMetrics } from "./core-metrics";

describe("Core metrics projection", () => {
  it("keeps unmeasured figures null instead of zero", () => {
    const metrics = projectCoreMetrics({
      object: "core.metrics",
      range: { start: "2026-09-24T00:00:00Z", end: "2026-09-24T01:00:00Z", resolution_seconds: 60 },
      service: { status: "running", version: "0.9.0" },
      ingress: { requests: 10, latency_ms: { p95: 120 }, series: [{ start: "2026-09-24T00:00:00Z", success: 10 }] },
      execution: {},
      dependencies: [{ id: "db", kind: "database", name: "PostgreSQL", status: "weird" }],
      process: {},
    });
    expect(metrics.ingress.server_errors).toBeNull();
    expect(metrics.ingress.latency_ms).toEqual({ p50: null, p95: 120 });
    expect(metrics.ingress.series[0]).toMatchObject({ success: 10, server_error: null });
    expect(metrics.execution.active_turns).toBeNull();
    expect(metrics.dependencies[0]).toMatchObject({ kind: "database", status: "unknown", latency_p95_ms: null });
    expect(metrics.process.series).toEqual([]);
  });

  it("rejects a response that is not Core metrics", () => {
    expect(() => projectCoreMetrics({ object: "list" })).toThrow();
  });
});
