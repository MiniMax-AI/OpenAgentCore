import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { RuntimeTrendCharts, runtimeChartRenderedX, runtimeChartViewBoxX } from "./RuntimeTrendCharts";
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
    inputTokensPerMinute: 100,
    outputTokensPerMinute: 50,
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

  it("renders smooth honest paths and a memory area only after two real samples", () => {
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25), sample(120_000, .5)]} />,
    );

    expect(html).toContain("dashboard-runtime-trend-line");
    expect(html).toContain(" C ");
    expect(html).toContain("dashboard-runtime-trend-area dashboard-runtime-trend-fill-purple");
    expect(html).toContain("dashboard-runtime-trend-latest");
    expect(html).not.toContain("Collecting live samples");
  });

  it("exposes interactive series, point selection, and a draggable shared timeline", () => {
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25), sample(120_000, .5)]} />,
    );

    expect(html).toContain('role="application"');
    expect(html).toContain("Move the pointer over the plot for exact values. Click to pin a time.");
    expect(html).toContain('aria-label="Hide Runtime worker series"');
    expect(html).toContain('aria-label="Timeline start"');
    expect(html).toContain('aria-label="Timeline end"');
    expect(html).toContain("Drag either handle to zoom · drag the selected window to pan");
    expect(html).toContain('aria-label="Pan selected timeline window"');
  });

  it("maps pointer positions through horizontal SVG letterboxing", () => {
    // A 900 × 220 CSS box renders the 640 × 220 viewBox with a 130px horizontal inset.
    expect(runtimeChartViewBoxX(130 + 52, 900, 220)).toBe(52);
    expect(runtimeChartViewBoxX(130 + 624, 900, 220)).toBe(624);
    expect(runtimeChartRenderedX(52, 900, 220)).toBe(130 + 52);
    expect(runtimeChartRenderedX(624, 900, 220)).toBe(130 + 624);
  });

  it("names durable charts and buckets without claiming they are live", () => {
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25), sample(120_000, .5)]} source="durable" />,
    );

    expect(html).toContain('aria-label="CPU usage durable history chart"');
    expect(html).toContain('aria-label="CPU usage: 2 retained buckets"');
    expect(html).not.toContain('aria-label="CPU usage: 2 live samples"');
  });
});
