import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { useTranslation } from "react-i18next";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";

import { formatDashboardBytes, formatDashboardTokens } from "./dashboard-model";
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
  stepped?: boolean;
}

interface TrendBand {
  from: number;
  to: number;
  tone: "safe" | "warning" | "danger";
}

export type RuntimeTrendSource = "live" | "durable";

interface TimeWindow {
  start: number;
  end: number;
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

function finite(values: readonly (number | null)[]): number[] {
  return values.filter((value): value is number => value !== null && Number.isFinite(value) && value >= 0);
}

function timeLabel(value: number, locale: string): string {
  return new Date(value).toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

const toneColors: Record<TrendSeries["tone"], string> = {
  orange: "#f59e52",
  green: "#50d5a0",
  blue: "#78a7ff",
  purple: "#b998f4",
};

function withAlpha(hex: string, alpha: number): string {
  const value = Number.parseInt(hex.slice(1), 16);
  return `rgba(${value >> 16}, ${(value >> 8) & 255}, ${value & 255}, ${alpha})`;
}

export function runtimeChartShowsSparsePoints(values: readonly (number | null | undefined)[]): boolean {
  let valid = 0;
  let previousValid = false;
  let connected = false;
  for (const value of values) {
    const currentValid = value !== null && value !== undefined && Number.isFinite(value);
    if (currentValid) {
      valid += 1;
      connected ||= previousValid;
    }
    previousValid = currentValid;
  }
  return valid > 0 && (valid <= 12 || !connected);
}

export function runtimeChartCaption({
  title,
  source,
  hasLine,
  allSeriesHidden,
  validPoints,
  sampleCount,
  emptyMessage,
  emptyDetail,
}: {
  title: string;
  source: RuntimeTrendSource;
  hasLine: boolean;
  allSeriesHidden: boolean;
  validPoints: number;
  sampleCount: number;
  emptyMessage: string;
  emptyDetail?: string;
}): string {
  if (allSeriesHidden) return `${title} all series hidden; use the legend to show a series`;
  if (hasLine) return `${title} ${source} trend available`;
  if (validPoints > 0) {
    return `${title} ${source} trend has ${validPoints} sparse valid point${validPoints === 1 ? "" : "s"}; a line requires consecutive buckets`;
  }
  if (source === "live") {
    return `${title} collecting live samples; ${validPoints} of 2 valid points from ${sampleCount} snapshots`;
  }
  return `${title} ${emptyMessage}; ${emptyDetail ?? `${validPoints} valid points from ${sampleCount} retained buckets`}`;
}

function TrendChart({
  title,
  subtitle,
  samples,
  series,
  maximum,
  formatValue,
  rangeStart,
  rangeEnd,
  source,
  bands = [],
  ticks = [1, .66, .33, 0],
  emptyMessage,
  emptyDetail,
}: {
  title: string;
  subtitle: string;
  samples: readonly RuntimeTrendSample[];
  series: TrendSeries[];
  maximum: number;
  formatValue: (value: number) => string;
  rangeStart: number;
  rangeEnd: number;
  source: RuntimeTrendSource;
  bands?: TrendBand[];
  ticks?: number[];
  emptyMessage?: string;
  emptyDetail?: string;
}) {
  const { t, i18n } = useTranslation("dashboard");
  const locale = i18n.resolvedLanguage ?? "en";
  const displayEmptyMessage = emptyMessage ?? t("charts.collecting");
  const instructionId = `${useId().replaceAll(":", "")}-instructions`;
  const mountRef = useRef<HTMLDivElement>(null);
  const plotRef = useRef<uPlot | null>(null);
  const pinnedRef = useRef(false);
  const zoomedRef = useRef(false);
  const [hiddenSeries, setHiddenSeries] = useState<ReadonlySet<string>>(() => new Set());
  const [tooltip, setTooltip] = useState<{ idx: number; left: number; pinned: boolean } | null>(null);
  const [zoomed, setZoomed] = useState(false);
  const [theme, setTheme] = useState("");
  const domain = useMemo(() => ({ start: rangeStart, end: Math.max(rangeStart + 1, rangeEnd) }), [rangeEnd, rangeStart]);
  const domainRef = useRef(domain);
  const hiddenSeriesRef = useRef(hiddenSeries);
  domainRef.current = domain;
  hiddenSeriesRef.current = hiddenSeries;
  const [viewRange, setViewRange] = useState<TimeWindow>(domain);
  const visibleSeries = series.filter((entry) => !hiddenSeries.has(entry.id));
  const allSeriesHidden = series.length > 0 && visibleSeries.length === 0;
  const hasLine = visibleSeries.some((entry) => entry.points.some((point, index) => (
    point.value !== null && Number.isFinite(point.value)
      && index > 0
      && entry.points[index - 1]?.value !== null
      && Number.isFinite(entry.points[index - 1]?.value)
  )));
  const validPoints = Math.max(0, ...visibleSeries.map((entry) => entry.points.filter((point) => (
    point.value !== null && Number.isFinite(point.value)
  )).length));
  const times = useMemo(() => [...new Set(series.flatMap((entry) => entry.points.map((point) => point.sampledAt)))].sort((left, right) => left - right), [series]);
  const chartData = useMemo<uPlot.AlignedData>(() => [
    times.map((value) => value / 1_000),
    ...series.map((entry) => {
      const values = new Map(entry.points.map((point) => [point.sampledAt, point.value]));
      return times.map((value) => values.get(value) ?? null);
    }),
  ], [series, times]);
  const dataRef = useRef(chartData);
  const formatRef = useRef(formatValue);
  const maximumRef = useRef(maximum);
  dataRef.current = chartData;
  formatRef.current = formatValue;
  maximumRef.current = maximum;
  const seriesKey = series.map((entry) => `${entry.id}:${entry.label}:${entry.tone}`).join("|");
  const bandsKey = bands.map((band) => `${band.from}:${band.to}:${band.tone}`).join("|");
  const ticksKey = ticks.join(":");
  const selectedTimestamp = tooltip === null ? null : chartData[0][tooltip.idx];
  const selectedAt = selectedTimestamp === null || selectedTimestamp === undefined ? null : selectedTimestamp * 1_000;
  const selectedValues = tooltip === null ? [] : visibleSeries.map((entry) => {
    const seriesIndex = series.findIndex((candidate) => candidate.id === entry.id);
    return { ...entry, value: chartData[seriesIndex + 1]?.[tooltip.idx] ?? null };
  });

  useEffect(() => {
    const update = () => setTheme(document.documentElement.dataset.theme ?? "light");
    update();
    const observer = new MutationObserver(update);
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const mount = mountRef.current;
    if (!mount || theme === "") return;
    const styles = getComputedStyle(mount);
    const color = (name: string, fallback: string) => styles.getPropertyValue(name).trim() || fallback;
    const axisColor = color("--fg-muted", theme === "dark" ? "#a8adb8" : "#6f7480");
    const gridColor = color("--line", theme === "dark" ? "#30343b" : "#e3e5e8");
    const surfaceColor = color("--surface", theme === "dark" ? "#17191d" : "#ffffff");
    const bandColors: Record<TrendBand["tone"], string> = {
      safe: withAlpha("#50d5a0", .07),
      warning: withAlpha("#f59e52", .07),
      danger: withAlpha("#ef6a72", .07),
    };
    const options: uPlot.Options = {
      width: Math.max(320, mount.clientWidth),
      height: 220,
      padding: [10, 8, 0, 0],
      legend: { show: false },
      scales: {
        x: { time: true },
        // Stable series can create the plot before their first data response.
        // Read the current maximum whenever uPlot ranges the y scale so the
        // initial 0..1 fallback does not survive a later data update.
        y: { range: () => [0, Math.max(1, maximumRef.current)] },
      },
      axes: [
        {
          stroke: axisColor,
          grid: { stroke: gridColor, width: 1 },
          ticks: { stroke: gridColor, width: 1 },
          values: (_plot, values) => values.map((value) => timeLabel(value * 1_000, locale)),
          font: "8px ui-monospace, SFMono-Regular, Menlo, monospace",
          size: 28,
        },
        {
          stroke: axisColor,
          grid: { stroke: gridColor, width: 1 },
          ticks: { stroke: gridColor, width: 1 },
          splits: () => ticks.map((tick) => Math.max(1, maximumRef.current) * tick).sort((left, right) => left - right),
          values: (_plot, values) => values.map((value) => formatRef.current(value)),
          font: "8px ui-monospace, SFMono-Regular, Menlo, monospace",
          size: 52,
        },
      ],
      cursor: {
        x: true,
        y: true,
        lock: false,
        drag: { x: true, y: false, setScale: true, dist: 8 },
        points: { size: 7, width: 2, fill: surfaceColor },
      },
      select: { show: true, left: 0, top: 0, width: 0, height: 0 },
      series: [
        {},
        ...series.map((entry): uPlot.Series => ({
          label: entry.label,
          show: !hiddenSeriesRef.current.has(entry.id),
          stroke: toneColors[entry.tone],
          width: 2,
          spanGaps: false,
          paths: entry.stepped ? uPlot.paths.stepped!({ align: 1 }) : undefined,
          points: {
            show: (plot, seriesIndex, first, last) => runtimeChartShowsSparsePoints(
              Array.from(plot.data[seriesIndex] ?? []).slice(first, last + 1),
            ),
            size: 6,
            width: 2,
            fill: surfaceColor,
          },
        })),
      ],
      hooks: {
        drawClear: bands.length === 0 ? [] : [(plot) => {
          for (const band of bands) {
            const top = plot.valToPos(band.to, "y", true);
            const bottom = plot.valToPos(band.from, "y", true);
            plot.ctx.fillStyle = bandColors[band.tone];
            plot.ctx.fillRect(plot.bbox.left, top, plot.bbox.width, Math.max(0, bottom - top));
          }
        }],
        setCursor: [(plot) => {
          const idx = plot.cursor.idx;
          if (idx === null || idx === undefined || pinnedRef.current) return;
          const left = ((plot.cursor.left ?? 0) + plot.bbox.left / uPlot.pxRatio) / Math.max(1, plot.width) * 100;
          setTooltip({ idx, left: clamp(left, 18, 82), pinned: pinnedRef.current });
        }],
        setScale: [(plot, scaleKey) => {
          if (scaleKey !== "x") return;
          const scale = plot.scales.x;
          if (!scale || scale.min === undefined || scale.max === undefined) return;
          const currentDomain = domainRef.current;
          const tolerance = Math.max(1, (currentDomain.end - currentDomain.start) / 100_000);
          const isZoomed = Math.abs(scale.min * 1_000 - currentDomain.start) > tolerance || Math.abs(scale.max * 1_000 - currentDomain.end) > tolerance;
          zoomedRef.current = isZoomed;
          setZoomed(isZoomed);
          setViewRange({ start: scale.min * 1_000, end: scale.max * 1_000 });
        }],
      },
    };
    const plot = new uPlot(options, dataRef.current, mount);
    plotRef.current = plot;
    plot.setScale("x", { min: domain.start / 1_000, max: domain.end / 1_000 });
    let pointerStart = 0;
    let moved = false;
    const pointerDown = (event: PointerEvent) => { pointerStart = event.clientX; moved = false; };
    const pointerMove = (event: PointerEvent) => {
      if (moved || Math.abs(event.clientX - pointerStart) < 8) return;
      moved = true;
      pinnedRef.current = false;
      setTooltip(null);
    };
    const click = () => {
      if (moved || plot.cursor.idx === null || plot.cursor.idx === undefined) return;
      pinnedRef.current = !pinnedRef.current;
      setTooltip((current) => current === null ? null : { ...current, pinned: pinnedRef.current });
    };
    const leave = () => { if (!pinnedRef.current) setTooltip(null); };
    const reset = (event: MouseEvent) => {
      event.preventDefault();
      pinnedRef.current = false;
      setTooltip(null);
      const currentDomain = domainRef.current;
      plot.setScale("x", { min: currentDomain.start / 1_000, max: currentDomain.end / 1_000 });
    };
    plot.over.addEventListener("pointerdown", pointerDown);
    plot.over.addEventListener("pointermove", pointerMove);
    plot.over.addEventListener("click", click);
    plot.over.addEventListener("mouseleave", leave);
    plot.over.addEventListener("dblclick", reset);
    const resize = new ResizeObserver(() => {
      const width = mount.clientWidth;
      if (width > 0 && width !== plot.width) plot.setSize({ width, height: 220 });
    });
    resize.observe(mount);
    return () => {
      resize.disconnect();
      plot.destroy();
      if (plotRef.current === plot) plotRef.current = null;
    };
  // Data updates are applied without rebuilding so a selected time range remains stable.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bandsKey, locale, seriesKey, source, theme, ticksKey]);

  useEffect(() => {
    const plot = plotRef.current;
    if (!plot) return;
    const currentX = plot.scales.x;
    const preserveZoom = zoomed && currentX !== undefined &&
      Number.isFinite(currentX.min) && Number.isFinite(currentX.max);
    const xMinimum = preserveZoom ? currentX!.min! : domain.start / 1_000;
    const xMaximum = preserveZoom ? currentX!.max! : domain.end / 1_000;
    plot.batch(() => {
      plot.setData(chartData, false);
      // Reapplying x recalculates visible indices and the auto y scale after
      // setData, while preserving an active drag-selected range.
      plot.setScale("x", { min: xMinimum, max: xMaximum });
    });
  }, [chartData, domain, maximum, zoomed]);

  useEffect(() => {
    const plot = plotRef.current;
    if (!plot) return;
    series.forEach((entry, index) => plot.setSeries(index + 1, { show: !hiddenSeries.has(entry.id) }));
  }, [hiddenSeries, series]);

  const resetZoom = () => {
    zoomedRef.current = false;
    setZoomed(false);
    setViewRange(domain);
    plotRef.current?.setScale("x", { min: domain.start / 1_000, max: domain.end / 1_000 });
  };

  const handleKeyboard = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      pinnedRef.current = false;
      setTooltip(null);
      return;
    }
    const visibleIndices = times.flatMap((time, index) => time >= viewRange.start && time <= viewRange.end ? [index] : []);
    if (visibleIndices.length === 0) return;
    const currentPosition = tooltip === null ? visibleIndices.length - 1 : visibleIndices.indexOf(tooltip.idx);
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault();
      const offset = event.key === "ArrowLeft" ? -1 : 1;
      const position = currentPosition < 0
        ? visibleIndices.length - 1
        : clamp(currentPosition + offset, 0, visibleIndices.length - 1);
      const idx = visibleIndices[position]!;
      const plot = plotRef.current;
      const plotLeft = plot?.valToPos(times[idx]! / 1_000, "x") ?? 0;
      const left = plot === null
        ? 50
        : (plotLeft + plot.bbox.left / uPlot.pxRatio) / Math.max(1, plot.width) * 100;
      pinnedRef.current = true;
      if (plot) plot.setCursor({ left: plotLeft, top: 0 });
      setTooltip({ idx, left: clamp(left, 18, 82), pinned: true });
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      pinnedRef.current = !pinnedRef.current;
      const idx = tooltip?.idx ?? visibleIndices.at(-1)!;
      setTooltip({ idx, left: tooltip?.left ?? 50, pinned: pinnedRef.current });
    }
  };

  const sourceLabel = t(source === "durable" ? "charts.durableHistory" : "charts.live");
  const sampleLabel = t(source === "durable" ? "charts.retainedBuckets" : "charts.liveSamples");

  return (
    <section className="dashboard-runtime-trend-card" aria-label={t("charts.chartLabel", { title, source: sourceLabel })}>
      <header>
        <div><h3>{title}</h3><p>{subtitle}</p></div>
        <div className="dashboard-runtime-trend-legend">
          {series.map((entry) => {
            const visible = !hiddenSeries.has(entry.id);
            return (
              <button
                type="button"
                key={entry.id}
                className={visible ? "" : "is-hidden"}
                aria-pressed={visible}
                aria-label={t(visible ? "charts.hideSeries" : "charts.showSeries", { series: entry.label })}
                title={t(visible ? "charts.hide" : "charts.show", { series: entry.label })}
                onClick={() => setHiddenSeries((current) => {
                  const next = new Set(current);
                  if (next.has(entry.id)) next.delete(entry.id);
                  else next.add(entry.id);
                  return next;
                })}
              ><i className={`dashboard-runtime-trend-${entry.tone}`} />{entry.label}</button>
            );
          })}
          {zoomed ? <button type="button" className="dashboard-runtime-chart-reset" onClick={resetZoom}>{t("charts.resetZoom")}</button> : null}
        </div>
      </header>
      <div
        className="dashboard-runtime-chart-frame dashboard-runtime-uplot-frame"
        onKeyDown={handleKeyboard}
      >
        <p id={instructionId} className="dashboard-runtime-visually-hidden">{t("charts.instructions")}</p>
        <div
          ref={mountRef}
          className="dashboard-runtime-uplot"
          role="application"
          tabIndex={0}
          aria-label={`${title}: ${samples.length} ${sampleLabel}`}
          aria-describedby={instructionId}
          data-chart-engine="uplot"
          data-view-start={Math.round(viewRange.start)}
          data-view-end={Math.round(viewRange.end)}
          data-selected-at={selectedAt === null ? undefined : Math.round(selectedAt)}
        />
        {selectedAt === null ? null : (
          <div
            className={`dashboard-runtime-trend-tooltip${tooltip?.pinned ? " is-pinned" : ""}`}
            role="status"
            style={{ left: `${tooltip?.left ?? 50}%` }}
          >
            <header><time dateTime={new Date(selectedAt).toISOString()}>{new Date(selectedAt).toLocaleString(locale)}</time>{tooltip?.pinned ? <span>{t("charts.pinned")}</span> : <span>{t("charts.hover")}</span>}</header>
            {selectedValues.map((entry) => (
              <div key={entry.id}><i className={`dashboard-runtime-trend-${entry.tone}`} /><span>{entry.label}</span><strong>{entry.value === null ? t("charts.unavailable") : formatValue(entry.value)}</strong></div>
            ))}
          </div>
        )}
        {!hasLine ? <div className="dashboard-runtime-chart-collecting"><strong>{allSeriesHidden ? t("charts.allHidden") : validPoints > 0 ? t("charts.sparse") : displayEmptyMessage}</strong><span>{allSeriesHidden ? t("charts.showLegend") : validPoints > 0 ? t("charts.sparseDetail", { count: validPoints }) : emptyDetail ?? t("charts.emptyDetail", { valid: validPoints, count: samples.length })}</span></div> : null}
      </div>
      <table className="dashboard-runtime-trend-accessible">
        <caption>{runtimeChartCaption({
          title,
          source,
          hasLine,
          allSeriesHidden,
          validPoints,
          sampleCount: samples.length,
          emptyMessage: displayEmptyMessage,
          emptyDetail,
        })}</caption>
        <thead><tr><th>{t("charts.table.series")}</th><th>{t("charts.table.latest")}</th><th>{t("charts.table.missing")}</th></tr></thead>
        <tbody>
          {series.map((entry) => {
            const latest = entry.points.at(-1)?.value ?? null;
            const missing = entry.points.filter((point) => point.value === null).length;
            return <tr key={entry.id}><th>{entry.label}</th><td>{latest === null ? t("charts.unavailable") : formatValue(latest)}</td><td>{missing.toLocaleString(locale)}</td></tr>;
          })}
        </tbody>
      </table>
    </section>
  );
}

function targetIds(samples: readonly RuntimeTrendSample[], field: "cpuRatio"): string[] {
  const latest = new Map<string, number>();
  for (const sample of samples) {
    for (const target of sample.targets) {
      const value = target[field];
      if (value !== null) latest.set(target.seriesId, value);
    }
  }
  return [...latest.entries()].sort((left, right) => right[1] - left[1]).slice(0, 3).map(([id]) => id);
}

function targetLabel(samples: readonly RuntimeTrendSample[], id: string): string {
  for (let index = samples.length - 1; index >= 0; index -= 1) {
    const target = samples[index]?.targets.find((candidate) => candidate.seriesId === id);
    if (target) return target.label;
  }
  return "Runtime";
}

const tones: TrendSeries["tone"][] = ["orange", "green", "blue"];

export function integerTickRatios(maximum: number): number[] {
  const integerMaximum = Math.max(1, Math.ceil(maximum));
  const values = integerMaximum <= 4
    ? Array.from({ length: integerMaximum + 1 }, (_, index) => integerMaximum - index)
    : [integerMaximum, Math.round(integerMaximum * 2 / 3), Math.round(integerMaximum / 3), 0];
  return [...new Set(values)].map((value) => value / integerMaximum);
}

export function RuntimeTrendCharts({
  samples,
  source = "live",
  rangeStart,
  rangeEnd,
  activeDisplay = "sum",
}: {
  samples: readonly RuntimeTrendSample[];
  source?: RuntimeTrendSource;
  rangeStart?: number;
  rangeEnd?: number;
  activeDisplay?: "sum" | "binary";
}) {
  const { t, i18n } = useTranslation("dashboard");
  const locale = i18n.resolvedLanguage ?? "en";
  const charts = useMemo(() => {
    const cpuIds = targetIds(samples, "cpuRatio");
    const cpu = cpuIds.map((id, index): TrendSeries => ({
      id,
      label: targetLabel(samples, id),
      tone: tones[index] ?? "blue",
      points: samples.map((sample) => {
        const ratio = sample.targets.find((target) => target.seriesId === id)?.cpuRatio;
        return { sampledAt: sample.sampledAt, value: ratio == null ? null : ratio * 100 };
      }),
    }));
    if (cpu.length === 0 && samples.length > 0) {
      cpu.push({ id: "cpu", label: t("charts.usage"), tone: "orange", points: samples.map((sample) => ({ sampledAt: sample.sampledAt, value: null })) });
    }
    const memoryUsed = samples.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.memoryUsageBytes }));
    const memoryLimit = samples.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.memoryLimitBytes }));
    const active = [{
      id: "active",
      label: t(activeDisplay === "binary" ? "charts.runtime" : "charts.active.series"),
      tone: "green",
      stepped: true,
      points: samples.map((sample) => ({
        sampledAt: sample.sampledAt,
        value: activeDisplay === "binary"
          ? sample.activeSandboxCount === null ? null : sample.activeSandboxCount > 0 ? 1 : 0
          : sample.activeSandboxCount,
      })),
    }] satisfies TrendSeries[];
    const throughput = tokenThroughput(samples);
    return {
      cpu,
      memory: [
        { id: "used", label: t("charts.used"), tone: "purple", points: memoryUsed },
        { id: "limit", label: t("charts.configuredLimit"), tone: "green", points: memoryLimit },
      ] satisfies TrendSeries[],
      active,
      tokens: [
        { id: "input", label: t("charts.input"), tone: "orange", points: throughput.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.inputPerMinute })) },
        { id: "output", label: t("charts.output"), tone: "green", points: throughput.map((sample) => ({ sampledAt: sample.sampledAt, value: sample.outputPerMinute })) },
      ] satisfies TrendSeries[],
    };
  }, [activeDisplay, samples, t]);
  const cpuMaximum = Math.max(100, ...finite(charts.cpu.flatMap((series) => series.points.map((point) => point.value))));
  const memoryMaximum = Math.max(1, ...finite(charts.memory.flatMap((series) => series.points.map((point) => point.value))));
  const activeMaximum = Math.max(1, ...finite(charts.active.flatMap((series) => series.points.map((point) => point.value))));
  const activeTicks = integerTickRatios(activeMaximum);
  const tokenMaximum = Math.max(1, ...finite(charts.tokens.flatMap((series) => series.points.map((point) => point.value))));
  const newest = rangeEnd ?? samples.at(-1)?.sampledAt ?? Date.now();
  const oldest = rangeStart ?? samples[0]?.sampledAt ?? newest - 60 * 60 * 1_000;
  const durable = source === "durable";
  const binaryActive = activeDisplay === "binary";
  const activeTitle = t(binaryActive ? "charts.active.runtimeTitle" : "charts.active.sandboxTitle");
  const activeSubtitle = t(binaryActive
    ? durable ? "charts.active.binaryDurable" : "charts.active.binaryLive"
    : durable ? "charts.active.sumDurable" : "charts.active.sumLive");
  const formatActive = binaryActive
    ? (value: number) => t(value >= .5 ? "charts.active.active" : "charts.active.inactive")
    : (value: number) => `${Math.round(value)}`;

  return (
    <div className="dashboard-runtime-trend-grid" aria-label={t(durable ? "charts.gridDurable" : "charts.gridLive")}>
      <TrendChart title={t("charts.cpu.title")} subtitle={t(durable ? "charts.cpu.durable" : "charts.cpu.live")} samples={samples} series={charts.cpu} maximum={cpuMaximum} formatValue={(value) => `${Math.round(value)}%`} rangeStart={oldest} rangeEnd={newest} source={source} bands={[{ from: 0, to: 30, tone: "safe" }, { from: 30, to: 70, tone: "warning" }, { from: 70, to: 100, tone: "danger" }]} ticks={[1, .7, .3, 0]} emptyMessage={durable ? t("charts.cpu.empty") : undefined} />
      <TrendChart title={t("charts.memory.title")} subtitle={t(durable ? "charts.memory.durable" : "charts.memory.live")} samples={samples} series={charts.memory} maximum={memoryMaximum} formatValue={(value) => formatDashboardBytes(Math.round(value))} rangeStart={oldest} rangeEnd={newest} source={source} emptyMessage={durable ? t("charts.memory.empty") : undefined} />
      <TrendChart title={activeTitle} subtitle={activeSubtitle} samples={samples} series={charts.active} maximum={binaryActive ? 1 : activeMaximum} formatValue={formatActive} rangeStart={oldest} rangeEnd={newest} source={source} ticks={binaryActive ? [1, 0] : activeTicks} emptyMessage={durable ? t("charts.active.empty") : undefined} />
      <TrendChart title={t("charts.tokens.title")} subtitle={t(durable ? "charts.tokens.durable" : "charts.tokens.live")} samples={samples} series={charts.tokens} maximum={tokenMaximum} formatValue={(value) => t("charts.tokens.perMinute", { value: formatDashboardTokens(Math.round(value), locale) })} rangeStart={oldest} rangeEnd={newest} source={source} emptyMessage={durable ? t("charts.tokens.empty") : undefined} />
    </div>
  );
}
