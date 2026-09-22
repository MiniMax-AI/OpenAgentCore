import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { RuntimeTrendCharts } from "./RuntimeTrendCharts";
import type { RuntimeTrendSample } from "./runtime-trends";

function sample(sampledAt: number, cpuRatio: number | null): RuntimeTrendSample {
  return {
    sampledAt,
    targets: [{
      sessionId: "session-1",
      label: "Runtime worker",
      cpuRatio,
      uptimeSeconds: 120,
    }],
    cpuCandidates: [],
    memoryUsageBytes: 512,
    memoryLimitBytes: 1_024,
    tokenTotals: [],
    inputTokensPerMinute: null,
    outputTokensPerMinute: null,
  };
}

describe("Runtime live-window chart accessibility", () => {
  it("reports a current gap as unavailable instead of announcing a stale value as latest", () => {
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .5), sample(120_000, null)]} />,
    );

    expect(html).toContain("Runtime worker</th><td>Unavailable</td><td>1</td>");
    expect(html).not.toContain("Runtime worker</th><td>50%</td><td>1</td>");
  });
});
