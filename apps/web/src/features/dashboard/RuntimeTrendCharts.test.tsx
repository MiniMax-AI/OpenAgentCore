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

  it("renders uPlot chart mounts and reports trends only after two real samples", () => {
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25), sample(120_000, .5)]} />,
    );

    expect(html.match(/data-chart-engine="uplot"/g)).toHaveLength(4);
    expect(html).not.toContain("Collecting live samples");
  });

  it("exposes interactive series, point selection, and Grafana-style in-plot range selection", () => {
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25), sample(120_000, .5)]} />,
    );

    expect(html).toContain('role="application"');
    expect(html).toContain("Drag horizontally to select and zoom a time range.");
    expect(html).toContain('aria-label="Hide Runtime worker series"');
    const instructionId = html.match(/aria-describedby="([^"]+-instructions)"/)?.[1];
    expect(instructionId).toBeTruthy();
    expect(html).toContain(`id="${instructionId}"`);
    expect(html).not.toContain(">Reset zoom</button>");
    expect(html).not.toContain("dashboard-runtime-timeline");
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
