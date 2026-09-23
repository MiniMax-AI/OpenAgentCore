import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";

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

export type RuntimeTrendSource = "live" | "durable";

const WIDTH = 640;
const HEIGHT = 220;
const PLOT = { left: 52, right: 16, top: 22, bottom: 34 };

interface TimeWindow {
  start: number;
  end: number;
}

type TimelineDrag = {
  kind: "start" | "end" | "window";
  originClientX: number;
  originWindow: TimeWindow;
};

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

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

function nearestTime(times: readonly number[], target: number): number | null {
  if (times.length === 0) return null;
  let nearest = times[0]!;
  let distance = Math.abs(nearest - target);
  for (let index = 1; index < times.length; index += 1) {
    const candidate = times[index]!;
    const candidateDistance = Math.abs(candidate - target);
    if (candidateDistance < distance) {
      nearest = candidate;
      distance = candidateDistance;
    }
  }
  return nearest;
}

export function runtimeChartViewBoxX(clientOffsetX: number, renderedWidth: number, renderedHeight: number): number {
  if (renderedWidth <= 0 || renderedHeight <= 0) return PLOT.left;
  const scale = Math.min(renderedWidth / WIDTH, renderedHeight / HEIGHT);
  const contentWidth = WIDTH * scale;
  const horizontalInset = (renderedWidth - contentWidth) / 2;
  return clamp((clientOffsetX - horizontalInset) / scale, PLOT.left, WIDTH - PLOT.right);
}

export function runtimeChartRenderedX(viewBoxX: number, renderedWidth: number, renderedHeight: number): number {
  if (renderedWidth <= 0 || renderedHeight <= 0) return 0;
  const scale = Math.min(renderedWidth / WIDTH, renderedHeight / HEIGHT);
  const contentWidth = WIDTH * scale;
  const horizontalInset = (renderedWidth - contentWidth) / 2;
  return horizontalInset + clamp(viewBoxX, 0, WIDTH) * scale;
}

function TimeRangeNavigator({
  domain,
  value,
  onChange,
  source,
  title,
}: {
  domain: TimeWindow;
  value: TimeWindow;
  onChange: (next: TimeWindow) => void;
  source: RuntimeTrendSource;
  title: string;
}) {
  const trackRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<TimelineDrag | null>(null);
  const [dragging, setDragging] = useState<TimelineDrag["kind"] | null>(null);
  const span = Math.max(1, domain.end - domain.start);
  const step = Math.max(1_000, Math.round(span / 100));
  const minimumWindow = Math.min(span, Math.max(step, 30_000, Math.ceil(span * .1)));
  const position = (time: number) => clamp((time - domain.start) / span * 100, 0, 100);

  const timeFromClientX = (clientX: number): number => {
    const rect = trackRef.current?.getBoundingClientRect();
    if (!rect || rect.width <= 0) return domain.start;
    return domain.start + clamp((clientX - rect.left) / rect.width, 0, 1) * span;
  };

  const updateDrag = (clientX: number) => {
    const drag = dragRef.current;
    if (!drag) return;
    if (drag.kind === "start") {
      onChange({ start: clamp(timeFromClientX(clientX), domain.start, value.end - minimumWindow), end: value.end });
      return;
    }
    if (drag.kind === "end") {
      onChange({ start: value.start, end: clamp(timeFromClientX(clientX), value.start + minimumWindow, domain.end) });
      return;
    }
    const rect = trackRef.current?.getBoundingClientRect();
    if (!rect || rect.width <= 0) return;
    const delta = (clientX - drag.originClientX) / rect.width * span;
    const width = drag.originWindow.end - drag.originWindow.start;
    const start = clamp(drag.originWindow.start + delta, domain.start, domain.end - width);
    onChange({ start, end: start + width });
  };

  const beginDrag = (event: ReactPointerEvent, kind: TimelineDrag["kind"], moveImmediately = false) => {
    event.preventDefault();
    event.stopPropagation();
    dragRef.current = { kind, originClientX: event.clientX, originWindow: value };
    setDragging(kind);
    event.currentTarget.setPointerCapture(event.pointerId);
    if (moveImmediately) updateDrag(event.clientX);
  };

  const endDrag = (event: ReactPointerEvent) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    dragRef.current = null;
    setDragging(null);
  };

  const adjustHandle = (kind: "start" | "end", event: ReactKeyboardEvent<HTMLButtonElement>) => {
    let delta = 0;
    if (event.key === "ArrowLeft" || event.key === "ArrowDown") delta = -step;
    else if (event.key === "ArrowRight" || event.key === "ArrowUp") delta = step;
    else if (event.key === "Home") delta = kind === "start" ? domain.start - value.start : value.start + minimumWindow - value.end;
    else if (event.key === "End") delta = kind === "start" ? value.end - minimumWindow - value.start : domain.end - value.end;
    else return;
    event.preventDefault();
    if (kind === "start") onChange({ start: clamp(value.start + delta, domain.start, value.end - minimumWindow), end: value.end });
    else onChange({ start: value.start, end: clamp(value.end + delta, value.start + minimumWindow, domain.end) });
  };

  const panWindow = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    const width = value.end - value.start;
    let start = value.start;
    if (event.key === "ArrowLeft" || event.key === "ArrowDown") start -= step;
    else if (event.key === "ArrowRight" || event.key === "ArrowUp") start += step;
    else if (event.key === "Home") start = domain.start;
    else if (event.key === "End") start = domain.end - width;
    else return;
    event.preventDefault();
    const clampedStart = clamp(start, domain.start, domain.end - width);
    onChange({ start: clampedStart, end: clampedStart + width });
  };

  const startPercent = position(value.start);
  const endPercent = position(value.end);
  const fullRange = value.start <= domain.start && value.end >= domain.end;

  return (
    <section className="dashboard-runtime-timeline" aria-label={`${title} timeline`}>
      <header>
        <div>
          <strong>Time range</strong>
          <span>Drag handles to zoom · window to pan</span>
        </div>
        <div>
          <time dateTime={new Date(value.start).toISOString()}>{timeLabel(value.start)}</time>
          <span>—</span>
          <time dateTime={new Date(value.end).toISOString()}>{timeLabel(value.end)}</time>
          <button type="button" disabled={fullRange} onClick={() => onChange(domain)}>Reset</button>
        </div>
      </header>
      <div
        ref={trackRef}
        className={`dashboard-runtime-timeline-track${dragging ? " is-dragging" : ""}`}
        onPointerDown={(event) => {
          const time = timeFromClientX(event.clientX);
          beginDrag(event, Math.abs(time - value.start) <= Math.abs(time - value.end) ? "start" : "end", true);
        }}
        onPointerMove={(event) => updateDrag(event.clientX)}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onLostPointerCapture={() => { dragRef.current = null; setDragging(null); }}
      >
        <div className="dashboard-runtime-timeline-rail" />
        <button
          type="button"
          className="dashboard-runtime-timeline-selection"
          style={{ left: `${startPercent}%`, width: `${Math.max(0, endPercent - startPercent)}%` }}
          aria-label={`Pan ${title} timeline window`}
          title="Drag to pan; use arrow keys for precise movement"
          disabled={fullRange}
          onKeyDown={panWindow}
          onPointerDown={(event) => beginDrag(event, "window")}
        />
        <button
          type="button"
          className="dashboard-runtime-timeline-handle dashboard-runtime-timeline-handle-start"
          style={{ left: `${startPercent}%` }}
          role="slider"
          aria-label={`${title} timeline start`}
          aria-valuemin={domain.start}
          aria-valuemax={value.end - minimumWindow}
          aria-valuenow={Math.round(value.start)}
          aria-valuetext={new Date(value.start).toLocaleString()}
          onKeyDown={(event) => adjustHandle("start", event)}
          onPointerDown={(event) => beginDrag(event, "start")}
        />
        <button
          type="button"
          className="dashboard-runtime-timeline-handle dashboard-runtime-timeline-handle-end"
          style={{ left: `${endPercent}%` }}
          role="slider"
          aria-label={`${title} timeline end`}
          aria-valuemin={value.start + minimumWindow}
          aria-valuemax={domain.end}
          aria-valuenow={Math.round(value.end)}
          aria-valuetext={new Date(value.end).toLocaleString()}
          onKeyDown={(event) => adjustHandle("end", event)}
          onPointerDown={(event) => beginDrag(event, "end")}
        />
      </div>
      <div className="dashboard-runtime-timeline-bounds" aria-hidden="true">
        <time>{timeLabel(domain.start)}</time><span>{source === "durable" ? "retained history" : "live window"}</span><time>{timeLabel(domain.end)}</time>
      </div>
    </section>
  );
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
  emptyMessage = "Collecting live samples",
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
  const clipId = useId().replaceAll(":", "");
  const instructionId = `${clipId}-instructions`;
  const svgRef = useRef<SVGSVGElement>(null);
  const [hiddenSeries, setHiddenSeries] = useState<ReadonlySet<string>>(() => new Set());
  const [hoveredAt, setHoveredAt] = useState<number | null>(null);
  const [pinnedAt, setPinnedAt] = useState<number | null>(null);
  const [svgSize, setSvgSize] = useState({ width: WIDTH, height: HEIGHT });
  const domain = useMemo(() => ({ start: rangeStart, end: Math.max(rangeStart + 1, rangeEnd) }), [rangeEnd, rangeStart]);
  const [viewRange, setViewRange] = useState<TimeWindow>(domain);
  const previousDomain = useRef(domain);
  const start = Math.max(rangeStart, viewRange.start);
  const end = Math.min(Math.max(rangeStart + 1, rangeEnd), viewRange.end);
  const range = Math.max(1, end - start);
  const plotWidth = WIDTH - PLOT.left - PLOT.right;
  const plotHeight = HEIGHT - PLOT.top - PLOT.bottom;
  const yMaximum = Math.max(1, maximum);
  const x = (sampledAt: number) => PLOT.left + (sampledAt - start) / range * plotWidth;
  const y = (value: number) => PLOT.top + (1 - Math.min(yMaximum, Math.max(0, value)) / yMaximum) * plotHeight;
  const visibleSeries = series.filter((entry) => !hiddenSeries.has(entry.id)).map((entry) => ({
    ...entry,
    points: entry.points.filter((point) => point.sampledAt >= start && point.sampledAt <= end),
  }));
  const visibleSamples = samples.filter((sample) => sample.sampledAt >= start && sample.sampledAt <= end);
  const hasLine = visibleSeries.some((entry) => segments(entry.points).some((segment) => segment.length >= 2));
  const validPoints = Math.max(0, ...visibleSeries.map((entry) => entry.points.filter((point) => (
    point.value !== null && Number.isFinite(point.value)
  )).length));
  const xTicks = [start, start + range / 2, end];
  const interactiveTimes = [...new Set(visibleSeries.flatMap((entry) => entry.points
    .map((point) => point.sampledAt)))].sort((left, right) => left - right);
  const selectedAt = pinnedAt ?? hoveredAt;
  const selectedValues = selectedAt === null ? [] : visibleSeries.map((entry) => ({
    ...entry,
    value: entry.points.find((point) => point.sampledAt === selectedAt)?.value ?? null,
  }));
  const selectedX = selectedAt === null ? null : x(selectedAt);
  const tooltipLeft = selectedX === null
    ? 50
    : runtimeChartRenderedX(selectedX, svgSize.width, svgSize.height) / Math.max(1, svgSize.width) * 100;

  useEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const updateSize = () => {
      const rect = svg.getBoundingClientRect();
      if (rect.width > 0 && rect.height > 0) setSvgSize({ width: rect.width, height: rect.height });
    };
    updateSize();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(updateSize);
    observer.observe(svg);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const previous = previousDomain.current;
    setViewRange((current) => {
      const tolerance = Math.max(1_000, (previous.end - previous.start) / 100);
      const wasFullRange = current.start <= previous.start + tolerance && current.end >= previous.end - tolerance;
      if (wasFullRange) return domain;
      const width = Math.min(current.end - current.start, domain.end - domain.start);
      const wasFollowing = current.end >= previous.end - tolerance;
      const nextEnd = wasFollowing ? domain.end : clamp(current.end, domain.start + width, domain.end);
      return { start: clamp(nextEnd - width, domain.start, domain.end - width), end: nextEnd };
    });
    previousDomain.current = domain;
  }, [domain]);

  useEffect(() => {
    if (hoveredAt !== null && (hoveredAt < start || hoveredAt > end)) setHoveredAt(null);
    if (pinnedAt !== null && (pinnedAt < start || pinnedAt > end)) setPinnedAt(null);
  }, [end, hoveredAt, pinnedAt, start]);

  const selectFromPointer = (event: { currentTarget: SVGSVGElement; clientX: number }): number | null => {
    const rect = event.currentTarget.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return null;
    const viewBoxX = runtimeChartViewBoxX(event.clientX - rect.left, rect.width, rect.height);
    return nearestTime(interactiveTimes, start + (viewBoxX - PLOT.left) / plotWidth * range);
  };

  const handleKeyboard = (event: ReactKeyboardEvent<SVGSVGElement>) => {
    if (event.key === "Escape") {
      setPinnedAt(null);
      setHoveredAt(null);
      return;
    }
    if (interactiveTimes.length === 0) return;
    const current = selectedAt === null ? interactiveTimes.length - 1 : interactiveTimes.indexOf(selectedAt);
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault();
      const offset = event.key === "ArrowLeft" ? -1 : 1;
      const next = interactiveTimes[clamp(current + offset, 0, interactiveTimes.length - 1)]!;
      setPinnedAt(next);
      setHoveredAt(next);
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      const next = selectedAt ?? interactiveTimes.at(-1)!;
      setPinnedAt(pinnedAt === next ? null : next);
      setHoveredAt(next);
    }
  };

  const sourceLabel = source === "durable" ? "durable history" : "live";
  const sampleLabel = source === "durable" ? "retained buckets" : "live samples";

  return (
    <section className="dashboard-runtime-trend-card" aria-label={`${title} ${sourceLabel} chart`}>
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
                aria-label={`${visible ? "Hide" : "Show"} ${entry.label} series`}
                title={`${visible ? "Hide" : "Show"} ${entry.label}`}
                onClick={() => setHiddenSeries((current) => {
                  const next = new Set(current);
                  if (next.has(entry.id)) next.delete(entry.id);
                  else next.add(entry.id);
                  return next;
                })}
              ><i className={`dashboard-runtime-trend-${entry.tone}`} />{entry.label}</button>
            );
          })}
        </div>
      </header>
      <div className="dashboard-runtime-chart-frame">
        <p id={instructionId} className="dashboard-runtime-visually-hidden">Move the pointer over the plot for exact values. Click to pin a time. Use Left and Right arrows to move the pinned selection, and Escape to clear it.</p>
        <svg
          ref={svgRef}
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          role="application"
          tabIndex={0}
          aria-label={`${title}: ${samples.length} ${sampleLabel}`}
          aria-describedby={instructionId}
          data-view-start={Math.round(start)}
          data-view-end={Math.round(end)}
          onPointerMove={(event) => {
            const nearest = selectFromPointer(event);
            if (nearest !== null) setHoveredAt(nearest);
          }}
          onPointerLeave={() => { if (pinnedAt === null) setHoveredAt(null); }}
          onClick={(event) => {
            const nearest = selectFromPointer(event);
            if (nearest !== null) {
              setHoveredAt(nearest);
              setPinnedAt((current) => current === nearest ? null : nearest);
            }
          }}
          onKeyDown={handleKeyboard}
        >
          <defs><clipPath id={clipId}><rect x={PLOT.left} y={PLOT.top} width={plotWidth} height={plotHeight} /></clipPath></defs>
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
          {visibleSamples.length > 0 ? xTicks.map((tick, index) => (
            <text key={`${tick}:${index}`} className="dashboard-runtime-trend-axis" x={x(tick)} y={HEIGHT - 8} textAnchor={index === 0 ? "start" : index === 2 ? "end" : "middle"}>{timeLabel(tick)}</text>
          )) : null}
          <g clipPath={`url(#${clipId})`}>
          {visibleSeries.flatMap((entry) => segments(entry.points).flatMap((segment, index) => {
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
          {selectedX === null ? null : (
            <>
              <line className="dashboard-runtime-trend-crosshair" x1={selectedX} x2={selectedX} y1={PLOT.top} y2={HEIGHT - PLOT.bottom} />
              {selectedValues.map((entry) => entry.value === null ? null : (
                <circle key={`selected:${entry.id}`} className={`dashboard-runtime-trend-selected dashboard-runtime-trend-stroke-${entry.tone}`} cx={selectedX} cy={y(entry.value)} r="4" />
              ))}
            </>
          )}
          </g>
        </svg>
        {selectedAt === null ? null : (
          <div
            className={`dashboard-runtime-trend-tooltip${pinnedAt === null ? "" : " is-pinned"}`}
            role="status"
            style={{ left: `${clamp(tooltipLeft, 18, 82)}%` }}
          >
            <header><time dateTime={new Date(selectedAt).toISOString()}>{new Date(selectedAt).toLocaleString()}</time>{pinnedAt === null ? <span>Hover</span> : <span>Pinned</span>}</header>
            {selectedValues.map((entry) => (
              <div key={entry.id}><i className={`dashboard-runtime-trend-${entry.tone}`} /><span>{entry.label}</span><strong>{entry.value === null ? "Unavailable" : formatValue(entry.value)}</strong></div>
            ))}
          </div>
        )}
        {!hasLine ? <div className="dashboard-runtime-chart-collecting"><strong>{visibleSeries.length === 0 ? "All series hidden" : emptyMessage}</strong><span>{visibleSeries.length === 0 ? "Use the legend to show a series" : emptyDetail ?? `${validPoints}/2 valid points · ${visibleSamples.length} snapshots · no history is synthesized`}</span></div> : null}
      </div>
      <TimeRangeNavigator domain={domain} value={viewRange} onChange={setViewRange} source={source} title={title} />
      <table className="dashboard-runtime-trend-accessible">
        <caption>{hasLine
          ? `${title} ${source} trend available`
          : source === "live"
            ? `${title} collecting live samples; ${validPoints} of 2 valid points from ${samples.length} snapshots`
            : `${title} ${emptyMessage}; ${emptyDetail ?? `${validPoints} valid points from ${samples.length} retained buckets`}`}</caption>
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

export function RuntimeTrendCharts({
  samples,
  source = "live",
  rangeStart,
  rangeEnd,
}: {
  samples: readonly RuntimeTrendSample[];
  source?: RuntimeTrendSource;
  rangeStart?: number;
  rangeEnd?: number;
}) {
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
  const newest = rangeEnd ?? samples.at(-1)?.sampledAt ?? Date.now();
  const oldest = rangeStart ?? samples[0]?.sampledAt ?? newest - 60 * 60 * 1_000;
  const durable = source === "durable";

  return (
    <div className="dashboard-runtime-trend-grid" aria-label={durable ? "Runtime durable-history charts" : "Runtime live-window charts"}>
      <TrendChart title="CPU usage" subtitle={durable ? "bucketed cumulative-delta utilization · durable history" : "reported or cumulative-delta utilization · live window"} samples={samples} series={charts.cpu} maximum={cpuMaximum} formatValue={(value) => `${Math.round(value)}%`} rangeStart={oldest} rangeEnd={newest} source={source} bands={[{ from: 0, to: 30, tone: "safe" }, { from: 30, to: 70, tone: "warning" }, { from: 70, to: 100, tone: "danger" }]} ticks={[1, .7, .3, 0]} emptyMessage={durable ? "No retained CPU samples" : undefined} />
      <TrendChart title="Memory usage" subtitle={durable ? "complete target aggregate / configured limit · durable history" : "working set / configured limit · live window"} samples={samples} series={charts.memory} maximum={memoryMaximum} formatValue={(value) => formatDashboardBytes(Math.round(value))} rangeStart={oldest} rangeEnd={newest} source={source} emptyMessage={durable ? "No complete retained memory samples" : undefined} />
      <TrendChart title="Compute uptime" subtitle={durable ? "provider started_at → bucket observation · incarnation-fenced" : "provider started_at → observed_at · current incarnation"} samples={samples} series={charts.uptime} maximum={uptimeMaximum} formatValue={(value) => formatDashboardDuration(value)} rangeStart={oldest} rangeEnd={newest} source={source} emptyMessage={durable ? "No retained uptime samples" : undefined} />
      <TrendChart title="Token throughput" subtitle={durable ? "canonical Session Usage · not retained in Runtime history" : "Session Usage deltas · missing usage excluded"} samples={samples} series={charts.tokens} maximum={tokenMaximum} formatValue={(value) => `${formatDashboardTokens(Math.round(value))}/min`} rangeStart={oldest} rangeEnd={newest} source={source} emptyMessage={durable ? "Live-only metric" : undefined} emptyDetail={durable ? "Runtime history does not duplicate canonical token usage" : undefined} />
    </div>
  );
}
