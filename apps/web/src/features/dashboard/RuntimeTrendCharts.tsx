import { useMemo } from "react";

import { formatDashboardBytes, formatDashboardDuration, formatDashboardTokens } from "./dashboard-model";
import { tokenThroughput, type RuntimeTrendSample } from "./runtime-trends";

interface TrendPoint {
  sampledAt: number;
  value: number | null;
}

interface TrendSeries {
  id: string;
  label: string;
  tone: "orange" | "green" | "blue" | "purple";
  points: TrendPoint[];
  fill?: boolean;
}

interface TrendBand {
  from: number;
  to: number;
  tone: "safe" | "warning" | "danger";
}

const WIDTH = 640;
const HEIGHT = 220;
const PLOT = { left: 52, right: 16, top: 22, bottom: 34 };

function finite(values: readonly (number | null)[]): number[] {
  return values.filter((value): value is number => value !== null && Number.isFinite(value) && value >= 0);
}

function timeLabel(value: number): string {
  return new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function segments(points: readonly TrendPoint[]): TrendPoint[][] {
  const result: TrendPoint[][] = [];
  let current: TrendPoint[] = [];
  for (const point of points) {
    if (point.value === null || !Number.isFinite(point.value)) {
      if (current.length > 0) result.push(current);
      current = [];
    } else current.push(point);
  }
  if (current.length > 0) result.push(current);
  return result;
}

function linePath(points: readonly TrendPoint[], x: (value: number) => number, y: (value: number) => number): string {
  if (points.length === 0) return "";
  const first = points[0]!;
  let result = `M ${x(first.sampledAt)} ${y(first.value ?? 0)}`;
  for (let index = 1; index < points.length; index += 1) {
    const previous = points[index - 1]!;
    const current = points[index]!;
    const previousX = x(previous.sampledAt);
    const currentX = x(current.sampledAt);
    const controlOffset = (currentX - previousX) / 3;
    result += ` C ${previousX + controlOffset} ${y(previous.value ?? 0)} ${currentX - controlOffset} ${y(current.value ?? 0)} ${currentX} ${y(current.value ?? 0)}`;
  }
  return result;
}

function TrendChart({
  title,
  subtitle,
  samples,
  series,
  maximum,
  formatValue,
  bands = [],
  ticks = [1, .66, .33, 0],
}: {
  title: string;
  subtitle: string;
  samples: readonly RuntimeTrendSample[];
  series: TrendSeries[];
  maximum: number;
  formatValue: (value: number) => string;
  bands?: TrendBand[];
  ticks?: number[];
}) {
  const timestamps = samples.map((sample) => sample.sampledAt);
  const start = timestamps[0] ?? 0;
  const end = timestamps.at(-1) ?? start;
  const range = Math.max(1, end - start);
  const plotWidth = WIDTH - PLOT.left - PLOT.right;
  const plotHeight = HEIGHT - PLOT.top - PLOT.bottom;
  const yMaximum = Math.max(1, maximum);
  const x = (sampledAt: number) => PLOT.left + (sampledAt - start) / range * plotWidth;
  const y = (value: number) => PLOT.top + (1 - Math.min(yMaximum, Math.max(0, value)) / yMaximum) * plotHeight;
  const hasLine = series.some((entry) => segments(entry.points).some((segment) => segment.length >= 2));
  const validPoints = Math.max(0, ...series.map((entry) => entry.points.filter((point) => (
    point.value !== null && Number.isFinite(point.value)
  )).length));
  const xTicks = [start, start + range / 2, end];

  return (
    <section className="dashboard-runtime-trend-card" aria-label={`${title} live chart`}>
      <header>
        <div><h3>{title}</h3><p>{subtitle}</p></div>
        <div className="dashboard-runtime-trend-legend">
          {series.map((entry) => <span key={entry.id} title={entry.label}><i className={`dashboard-runtime-trend-${entry.tone}`} />{entry.label}</span>)}
        </div>
      </header>
      <div className="dashboard-runtime-chart-frame">
        <svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} role="img" aria-label={`${title}: ${samples.length} live samples`}>
          {bands.map((band) => (
            <rect key={`${band.from}:${band.to}`} className={`dashboard-runtime-trend-band dashboard-runtime-trend-band-${band.tone}`} x={PLOT.left} y={y(band.to)} width={plotWidth} height={Math.max(0, y(band.from) - y(band.to))} />
          ))}
          {ticks.map((tick) => {
            const value = yMaximum * tick;
            return (
              <g key={tick}>
                <line className="dashboard-runtime-trend-gridline" x1={PLOT.left} x2={WIDTH - PLOT.right} y1={y(value)} y2={y(value)} />
                <text className="dashboard-runtime-trend-axis" x={PLOT.left - 8} y={y(value) + 4} textAnchor="end">{formatValue(value)}</text>
              </g>
            );
          })}
          {samples.length > 0 ? xTicks.map((tick, index) => (
            <text key={`${tick}:${index}`} className="dashboard-runtime-trend-axis" x={x(tick)} y={HEIGHT - 8} textAnchor={index === 0 ? "start" : index === 2 ? "end" : "middle"}>{timeLabel(tick)}</text>
          )) : null}
          {series.flatMap((entry) => segments(entry.points).flatMap((segment, index) => {
            const key = `${entry.id}:${index}`;
            if (segment.length === 1) {
              return <circle key={key} className={`dashboard-runtime-trend-dot dashboard-runtime-trend-stroke-${entry.tone}`} cx={x(segment[0]!.sampledAt)} cy={y(segment[0]!.value ?? 0)} r="3" />;
            }
            const path = linePath(segment, x, y);
            const last = segment.at(-1)!;
            return [
              entry.fill ? (
                <path
                  key={`${key}:area`}
                  className={`dashboard-runtime-trend-area dashboard-runtime-trend-fill-${entry.tone}`}
                  d={`${path} L ${x(last.sampledAt)} ${y(0)} L ${x(segment[0]!.sampledAt)} ${y(0)} Z`}
                />
              ) : null,
              <path key={`${key}:line`} className={`dashboard-runtime-trend-line dashboard-runtime-trend-stroke-${entry.tone}`} d={path} />,
              <circle key={`${key}:latest`} className={`dashboard-runtime-trend-latest dashboard-runtime-trend-stroke-${entry.tone}`} cx={x(last.sampledAt)} cy={y(last.value ?? 0)} r="2.75" />,
            ];
          }))}
        </svg>
        {!hasLine ? <div className="dashboard-runtime-chart-collecting"><strong>Collecting live samples</strong><span>{validPoints}/2 valid points · {samples.length} snapshots · no history is synthesized</span></div> : null}
      </div>
      <table className="dashboard-runtime-trend-accessible">
        <caption>{hasLine ? `${title} live trend available` : `${title} collecting live samples; ${validPoints} of 2 valid points from ${samples.length} snapshots`}</caption>
        <thead><tr><th>Series</th><th>Latest value</th><th>Missing samples</th></tr></thead>
        <tbody>
          {series.map((entry) => {
            const latest = entry.points.at(-1)?.value ?? null;
            const missing = entry.points.filter((point) => point.value === null).length;
            return <tr key={entry.id}><th>{entry.label}</th><td>{latest === null ? "Unavailable" : formatValue(latest)}</td><td>{missing}</td></tr>;
          })}
        </tbody>
      </table>
    </section>
  );
}

function targetIds(samples: readonly RuntimeTrendSample[], field: "cpuRatio" | "uptimeSeconds"): string[] {
  const latest = new Map<string, number>();
  for (const sample of samples) {
    for (const target of sample.targets) {
      const value = target[field];
      if (value !== null) latest.set(target.sessionId, value);
    }
  }
  return [...latest.entries()].sort((left, right) => right[1] - left[1]).slice(0, 3).map(([id]) => id);
}

function targetLabel(samples: readonly RuntimeTrendSample[], id: string): string {
  for (let index = samples.length - 1; index >= 0; index -= 1) {
    const target = samples[index]?.targets.find((candidate) => candidate.sessionId === id);
    if (target) return target.label;
  }
  return "Runtime";
}

const tones: TrendSeries["tone"][] = ["orange", "green", "blue"];

export function RuntimeTrendCharts({ samples }: { samples: readonly RuntimeTrendSample[] }) {
  const charts = useMemo(() => {
    const cpuIds = targetIds(samples, "cpuRatio");
    const uptimeIds = targetIds(samples, "uptimeSeconds");
    const cpu = cpuIds.map((id, index): TrendSeries => ({
      id,
      label: targetLabel(samples, id),
      tone: tones[index] ?? "blue",
      points: samples.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.targets.find((target) => target.sessionId === id)?.cpuRatio ?? null })).map((point) => ({ ...point, value: point.value === null ? null : point.value * 100 })),
    }));
    const memoryUsed = samples.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.memoryUsageBytes }));
    const memoryLimit = samples.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.memoryLimitBytes }));
    const uptime = uptimeIds.map((id, index): TrendSeries => ({
      id,
      label: targetLabel(samples, id),
      tone: tones[(index + 2) % tones.length] ?? "blue",
      points: samples.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.targets.find((target) => target.sessionId === id)?.uptimeSeconds ?? null })),
    }));
    const throughput = tokenThroughput(samples);
    return {
      cpu,
      memory: [
        { id: "used", label: "used", tone: "purple", points: memoryUsed, fill: true },
        { id: "limit", label: "configured limit", tone: "green", points: memoryLimit },
      ] satisfies TrendSeries[],
      uptime,
      tokens: [
        { id: "input", label: "input", tone: "orange", points: throughput.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.inputPerMinute })) },
        { id: "output", label: "output", tone: "green", points: throughput.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.outputPerMinute })) },
      ] satisfies TrendSeries[],
    };
  }, [samples]);
  const cpuMaximum = Math.max(100, ...finite(charts.cpu.flatMap((series) => series.points.map((point) => point.value))));
  const memoryMaximum = Math.max(1, ...finite(charts.memory.flatMap((series) => series.points.map((point) => point.value))));
  const uptimeMaximum = Math.max(1, ...finite(charts.uptime.flatMap((series) => series.points.map((point) => point.value))));
  const tokenMaximum = Math.max(1, ...finite(charts.tokens.flatMap((series) => series.points.map((point) => point.value))));

  return (
    <div className="dashboard-runtime-trend-grid" aria-label="Runtime live-window charts">
      <TrendChart title="CPU usage" subtitle="reported or cumulative-delta utilization · live window" samples={samples} series={charts.cpu} maximum={cpuMaximum} formatValue={(value) => `${Math.round(value)}%`} bands={[{ from: 0, to: 30, tone: "safe" }, { from: 30, to: 70, tone: "warning" }, { from: 70, to: 100, tone: "danger" }]} ticks={[1, .7, .3, 0]} />
      <TrendChart title="Memory usage" subtitle="working set / configured limit · live window" samples={samples} series={charts.memory} maximum={memoryMaximum} formatValue={(value) => formatDashboardBytes(Math.round(value))} />
      <TrendChart title="Compute uptime" subtitle="provider started_at → observed_at · current incarnation" samples={samples} series={charts.uptime} maximum={uptimeMaximum} formatValue={(value) => formatDashboardDuration(value)} />
      <TrendChart title="Token throughput" subtitle="Session Usage deltas · missing usage excluded" samples={samples} series={charts.tokens} maximum={tokenMaximum} formatValue={(value) => `${formatDashboardTokens(Math.round(value))}/min`} />
    </div>
  );
}
