import { describe, expect, it } from "vitest";
import type { OperatorMetricsSummary } from "@agents-core-web/agents-client";

import { hasCompleteRequestCoverage } from "./SystemObservabilityContent";

const start = "2026-09-25T00:00:00Z";
const next = "2026-09-25T00:01:00Z";
const heartbeat = (time: string, dropped = 0, exportFailed = 0) => ({
  start: time, source: "request" as const, attempted_count: 0, observed_count: 0,
  unavailable_count: 0, timeout_count: 0, dropped_count: dropped, export_failed_count: exportFailed,
});
const summary = (collector: OperatorMetricsSummary["collector"]): OperatorMetricsSummary => ({
  generated_at: next, start, end: next, step_seconds: 60,
  requests: [], collector, turns: [], tools: [],
});

describe("request coverage", () => {
  it("requires every heartbeat and withholds complete-range metrics after known loss", () => {
    expect(hasCompleteRequestCoverage(summary([heartbeat(start)]), 2)).toBe(false);
    expect(hasCompleteRequestCoverage(summary([heartbeat(start), heartbeat(next)]), 2)).toBe(true);
    expect(hasCompleteRequestCoverage(summary([heartbeat(start, 1), heartbeat(next)]), 2)).toBe(false);
    expect(hasCompleteRequestCoverage(summary([heartbeat(start), heartbeat(next, 0, 1)]), 2)).toBe(false);
  });
});
