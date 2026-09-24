import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { integerAxis, RuntimeTrendCharts, runtimeChartCaption, runtimeChartShowsSparsePoints } from "./RuntimeTrendCharts";
import type { RuntimeTrendSample } from "./runtime-trends";

function sample(sampledAt: number, cpuRatio: number | null): RuntimeTrendSample {
  return {
    sampledAt,
    activeSandboxCount: 1,
    targets: [{
      seriesId: "session-1:allocation-1",
      label: "Runtime worker",
      cpuRatio,
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
  it("uses round, evenly spaced integer y-axis positions for Sandbox counts", () => {
    const ticks = (maximum: number) => {
      const axis = integerAxis(maximum);
      return axis.ratios.map((ratio) => Math.round(ratio * axis.maximum));
    };
    expect(ticks(3)).toEqual([3, 2, 1, 0]);
    expect(ticks(8)).toEqual([8, 6, 4, 2, 0]);
    expect(ticks(5)).toEqual([6, 4, 2, 0]);
    expect(ticks(17)).toEqual([20, 15, 10, 5, 0]);
  });

  it("shows isolated or sparse values as points without inventing continuity", () => {
    expect(runtimeChartShowsSparsePoints([null, 512, null])).toBe(true);
    expect(runtimeChartShowsSparsePoints([512, 768])).toBe(true);
    expect(runtimeChartShowsSparsePoints(Array.from({ length: 13 }, (_, index) => index))).toBe(false);
    expect(runtimeChartShowsSparsePoints([null, null])).toBe(false);
  });

  it("announces hidden series instead of claiming retained data is empty", () => {
    expect(runtimeChartCaption({
      title: "Memory usage",
      source: "durable",
      hasLine: false,
      allSeriesHidden: true,
      validPoints: 0,
      sampleCount: 24,
      emptyMessage: "No retained observed memory samples",
    })).toBe("Memory usage all series hidden; use the legend to show a series");
  });

  it("renders an unavailable current value as missing without retaining a stale value", () => {
    const unavailable = {
      ...sample(120_000, null),
      activeSandboxCount: 0,
      memoryUsageBytes: null,
      memoryLimitBytes: null,
    } satisfies RuntimeTrendSample;
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .5), unavailable]} />,
    );

    expect(html).toContain("Runtime worker</th><td>Unavailable</td><td>1</td>");
    expect(html).not.toContain("Runtime worker</th><td>50%</td>");
    expect(html).not.toContain("Runtime worker</th><td>0%</td>");
    expect(html).toContain("used</th><td>Unavailable</td><td>1</td>");
    // A reported zero is a value, not a gap.
    expect(html).toContain("active</th><td>0</td><td>0</td>");
  });

  it("keeps empty retained buckets missing instead of drawing zero-value series", () => {
    const empty = (sampledAt: number): RuntimeTrendSample => ({
      ...sample(sampledAt, null),
      targets: [],
      activeSandboxCount: 0,
      memoryUsageBytes: null,
      memoryLimitBytes: null,
      inputTokensPerMinute: null,
      outputTokensPerMinute: null,
    });
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[empty(60_000), empty(120_000)]} source="durable" />,
    );

    expect(html).not.toContain("<td>0%</td>");
    expect(html).not.toContain("<td>0 B</td>");
    expect(html).not.toContain("<td>0/min</td>");
    expect(html).toContain("used</th><td>Unavailable</td><td>2</td>");
    expect(html).toContain("input</th><td>Unavailable</td><td>2</td>");
    expect(html).toContain("active</th><td>0</td><td>0</td>");
    expect(html).toContain("No retained CPU samples");
    expect(html).toContain("No retained observed memory samples");
    expect(html).toContain("No retained token samples");
  });

  it("renders uPlot chart mounts and reports trends only after two real samples", () => {
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25), sample(120_000, .5)]} />,
    );

    expect(html.match(/data-chart-engine="uplot"/g)).toHaveLength(4);
    expect(html).not.toContain("Collecting live samples");
    expect(html).toContain("Active sandboxes");
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
    expect(html.match(/data-chart-engine="uplot"/g)).toHaveLength(4);
    expect(html).not.toContain("Compute uptime");
    expect(html).toContain('aria-label="CPU usage: 2 retained buckets"');
    expect(html).toContain('aria-label="Active sandboxes durable history chart"');
    expect(html).toContain("active</th><td>1</td><td>0</td>");
    expect(html).not.toContain('aria-label="CPU usage: 2 live samples"');
  });

  it("renders one summed active-Sandbox series instead of one series per Session", () => {
    const latest = { ...sample(120_000, .5), activeSandboxCount: 3 };
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25), latest]} />,
    );

    expect(html.match(/data-chart-engine="uplot"/g)).toHaveLength(4);
    expect(html).toContain("active</th><td>3</td><td>0</td>");
    expect(html.match(/aria-label="Hide active series"/g)).toHaveLength(1);
  });

  it("renders the Session view as one binary Runtime-active series", () => {
    const latest = { ...sample(120_000, .5), activeSandboxCount: 3 };
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25), latest]} activeDisplay="binary" />,
    );

    expect(html).toContain("Runtime active");
    expect(html).toContain("1 active / 0 inactive");
    expect(html).toContain("Runtime</th><td>Active</td><td>0</td>");
    expect(html).not.toContain("Active sandboxes");
  });
  it("announces an isolated durable value as sparse rather than empty", () => {
    const html = renderToStaticMarkup(
      <RuntimeTrendCharts samples={[sample(60_000, .25)]} source="durable" />,
    );

    expect(html).toContain("Memory usage durable trend has 1 sparse valid point; a line requires consecutive buckets");
    expect(html).not.toContain("Memory usage No retained observed memory samples");
  });
});
