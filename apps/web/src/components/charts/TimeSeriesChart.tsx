import { Table2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent } from "react";
import { useTranslation } from "react-i18next";

import { Modal } from "../Modal";
import { formatBucketTime, niceTicks, tickIndices } from "./chart-scale";

export interface TimeSeries {
  id: string;
  label: string;
  /** A CSS colour, normally a `var(--series-n)` or status token. */
  color: string;
  values: ReadonlyArray<number | null>;
  /** Formatted range total shown beside the legend label. */
  total?: string;
}

export interface TimeSeriesChartProps {
  label: string;
  kind: "columns" | "lines";
  stacked?: boolean;
  /** Bucket start times in epoch seconds. */
  buckets: readonly number[];
  bucketSeconds: number;
  series: readonly TimeSeries[];
  formatValue: (value: number) => string;
  formatAxis?: (value: number) => string;
  height?: number;
  /** Columns only: include this series in the tooltip without drawing it. */
  tooltipOnly?: readonly TimeSeries[];
  /** The values are counts, so the axis ticks stay whole numbers. */
  counts?: boolean;
}

/** `left` is a floor: the axis gutter grows to the widest tick label. */
const MARGIN = { top: 10, right: 12, bottom: 24, left: 20 };
/** Space between the tick labels and the plot. */
const AXIS_GAP = 10;
const COLUMN_MAX = 24;
const GAP = 2;
const RADIUS = 4;

function topRoundedRect(x: number, y: number, width: number, height: number, radius: number): string {
  const r = Math.min(radius, width / 2, height);
  return `M${x},${y + height}V${y + r}Q${x},${y} ${x + r},${y}H${x + width - r}Q${x + width},${y} ${x + width},${y + r}V${y + height}Z`;
}

/** Approximate width of an 11px tick label: tabular digits and Latin, full-width CJK. */
export function axisLabelWidth(text: string): number {
  let width = 0;
  for (const char of text) width += /[\u2e80-\u9fff\uf900-\ufaff\uff00-\uffef]/.test(char) ? 11 : char === " " ? 3 : 6.4;
  return width;
}

function linePath(points: ReadonlyArray<[number, number] | null>): string {
  let path = "";
  let open = false;
  for (const point of points) {
    if (!point) {
      open = false;
      continue;
    }
    path += `${open ? "L" : "M"}${point[0]},${point[1]}`;
    open = true;
  }
  return path;
}

export function TimeSeriesChart({
  label,
  kind,
  stacked = false,
  buckets,
  bucketSeconds,
  series,
  formatValue,
  formatAxis = formatValue,
  height = 168,
  tooltipOnly = [],
  counts = false,
}: TimeSeriesChartProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(640);
  const [active, setActive] = useState<number | null>(null);
  const [showTable, setShowTable] = useState(false);

  useEffect(() => {
    const element = wrapperRef.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      const next = Math.floor(entries[0]?.contentRect.width ?? 0);
      if (next > 0) setWidth(next);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const count = buckets.length;
  const bucketAt = (index: number) => buckets[index] ?? 0;
  const spanSeconds = count ? bucketAt(count - 1) - bucketAt(0) + bucketSeconds : 0;

  const maximum = useMemo(() => {
    let max = 0;
    for (let index = 0; index < count; index += 1) {
      if (kind === "columns" && stacked) {
        max = Math.max(max, series.reduce((sum, entry) => sum + (entry.values[index] ?? 0), 0));
      } else {
        for (const entry of series) max = Math.max(max, entry.values[index] ?? 0);
      }
    }
    return max;
  }, [count, kind, series, stacked]);
  const ticks = niceTicks(maximum, 4, counts);
  const top = ticks[ticks.length - 1] || 1;
  // Tick labels start at the card's content edge, under the title and legend;
  // the plot begins after the widest label, so every chart lines up the same way.
  const left = Math.max(MARGIN.left, Math.ceil(Math.max(0, ...ticks.map((tick) => axisLabelWidth(formatAxis(tick))))) + AXIS_GAP);
  const plotWidth = Math.max(40, width - left - MARGIN.right);
  const band = count ? plotWidth / count : plotWidth;
  const y = (value: number) => MARGIN.top + height - (value / top) * height;
  const xCenter = (index: number) => left + band * index + band / 2;
  const hasData = series.some((entry) => entry.values.some((value) => value !== null && value !== undefined));

  const columnWidth = Math.max(2, Math.min(COLUMN_MAX, band * 0.64));

  function indexFromPointer(event: PointerEvent<SVGRectElement>): number | null {
    const bounds = event.currentTarget.getBoundingClientRect();
    const offset = event.clientX - bounds.left;
    const index = Math.floor(offset / band);
    return index >= 0 && index < count ? index : null;
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (!count) return;
    if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
      event.preventDefault();
      const delta = event.key === "ArrowRight" ? 1 : -1;
      setActive((current) => Math.min(count - 1, Math.max(0, (current ?? (delta > 0 ? -1 : count)) + delta)));
    } else if (event.key === "Escape") {
      setActive(null);
    }
  }

  const tooltipSeries = [...series, ...tooltipOnly];
  const tooltipLeft = active === null ? 0 : xCenter(active);
  const tooltipOnRight = tooltipLeft < width / 2;

  return (
    <div className="ts-chart">
      {series.length > 1 || series.some((entry) => entry.total) ? (
        <ul className="chart-legend" aria-label={t("chart.legend", { ns: "metrics" })}>
          {series.map((entry) => (
            <li key={entry.id}>
              <span className={kind === "lines" ? "legend-key legend-key-line" : "legend-key"} style={{ background: entry.color }} aria-hidden="true" />
              {entry.label}
              {entry.total ? <span className="legend-total">{entry.total}</span> : null}
            </li>
          ))}
        </ul>
      ) : null}
      <div
        ref={wrapperRef}
        className="ts-chart-plot"
        role="group"
        aria-label={label}
        aria-roledescription="chart"
        tabIndex={count ? 0 : -1}
        onKeyDown={onKeyDown}
        onBlur={() => setActive(null)}
        style={{ height: height + MARGIN.top + MARGIN.bottom }}
      >
        <svg width={width} height={height + MARGIN.top + MARGIN.bottom} aria-hidden="true">
          {ticks.map((tick) => (
            <g key={tick} className="chart-gridline">
              <line x1={left} x2={width - MARGIN.right} y1={y(tick)} y2={y(tick)} />
              <text x={0} y={y(tick)} dy="0.32em" textAnchor="start">{formatAxis(tick)}</text>
            </g>
          ))}
          {tickIndices(count, Math.max(2, Math.min(7, Math.floor(plotWidth / 90)))).map((index) => (
            <text key={index} className="chart-axis-label" x={xCenter(index)} y={MARGIN.top + height + 16} textAnchor="middle">
              {formatBucketTime(bucketAt(index), bucketSeconds, spanSeconds, locale)}
            </text>
          ))}
          {active !== null ? (
            kind === "columns"
              ? <rect className="chart-band-highlight" x={left + band * active} y={MARGIN.top} width={band} height={height} />
              : <line className="chart-crosshair" x1={xCenter(active)} x2={xCenter(active)} y1={MARGIN.top} y2={MARGIN.top + height} />
          ) : null}
          {/* Keyed by the bucketing: bars and lines draw in when a range is first shown, not on every refresh. */}
          <g key={`${bucketSeconds}:${count}`} className="chart-series">
          {kind === "columns" ? buckets.map((_, index) => {
            let base = 0;
            const drawn = series
              .map((entry) => ({ entry, value: entry.values[index] ?? 0 }))
              .filter((segment) => segment.value > 0);
            return (
              <g key={index} className="chart-bar" style={{ "--i": index } as CSSProperties}>
                {drawn.map(({ entry, value }, position) => {
                  if (!stacked) {
                    const slot = columnWidth / series.length;
                    const seriesIndex = series.indexOf(entry);
                    const x = xCenter(index) - columnWidth / 2 + slot * seriesIndex;
                    const barHeight = Math.max(1, (value / top) * height);
                    return <path key={entry.id} d={topRoundedRect(x, y(value), Math.max(1, slot - (series.length > 1 ? GAP : 0)), barHeight, RADIUS)} fill={entry.color} />;
                  }
                  const bottom = y(base);
                  base += value;
                  const segmentTop = y(base);
                  const isTop = position === drawn.length - 1;
                  const gap = position > 0 ? GAP : 0;
                  const segmentHeight = Math.max(1, bottom - segmentTop - gap);
                  const x = xCenter(index) - columnWidth / 2;
                  return isTop
                    ? <path key={entry.id} d={topRoundedRect(x, segmentTop, columnWidth, segmentHeight, RADIUS)} fill={entry.color} />
                    : <rect key={entry.id} x={x} y={segmentTop} width={columnWidth} height={segmentHeight} fill={entry.color} />;
                })}
              </g>
            );
          }) : series.map((entry) => {
            const points = entry.values.map((value, index) => value === null || value === undefined ? null : [xCenter(index), y(value)] as [number, number]);
            const last = [...points].reverse().find(Boolean) ?? null;
            return (
              <g key={entry.id}>
                <path className="chart-line" pathLength={1} d={linePath(points)} stroke={entry.color} />
                {last ? <circle className="chart-end-dot chart-last-dot" cx={last[0]} cy={last[1]} r={4} fill={entry.color} /> : null}
                {active !== null && points[active] ? <circle className="chart-end-dot" cx={points[active]![0]} cy={points[active]![1]} r={4} fill={entry.color} /> : null}
              </g>
            );
          })}
          </g>
          <rect
            className="chart-hit"
            x={left}
            y={MARGIN.top}
            width={plotWidth}
            height={height}
            onPointerMove={(event) => setActive(indexFromPointer(event))}
            onPointerLeave={() => setActive(null)}
          />
        </svg>
        {!hasData ? <p className="chart-empty">{t("chart.noData", { ns: "metrics" })}</p> : null}
        {active !== null && hasData ? (
          <div
            className="chart-tooltip"
            aria-hidden="true"
            style={tooltipOnRight ? { left: tooltipLeft + 12 } : { right: width - tooltipLeft + 12 }}
          >
            <p className="chart-tooltip-time">{formatBucketTime(bucketAt(active), bucketSeconds, 0, locale)}{spanSeconds > 36 * 3600 ? ` · ${new Intl.DateTimeFormat(locale, { month: "numeric", day: "numeric" }).format(new Date(bucketAt(active) * 1000))}` : ""}</p>
            {tooltipSeries.map((entry) => {
              const value = entry.values[active];
              return (
                <p key={entry.id} className="chart-tooltip-row">
                  <span className="legend-key legend-key-line" style={{ background: entry.color }} aria-hidden="true" />
                  <strong>{value === null || value === undefined ? "—" : formatValue(value)}</strong>
                  <span>{entry.label}</span>
                </p>
              );
            })}
          </div>
        ) : null}
      </div>
      {/* A persistent live region, so the first keyboard reading is announced too. */}
      <p className="visually-hidden" aria-live="polite">
        {active !== null && hasData
          ? `${formatBucketTime(bucketAt(active), bucketSeconds, 0, locale)}: ${tooltipSeries.map((entry) => {
            const value = entry.values[active];
            return `${entry.label} ${value === null || value === undefined ? "—" : formatValue(value)}`;
          }).join(", ")}`
          : ""}
      </p>
      {/* The numbers open in a dialog, so the chart grid keeps its layout. */}
      <button
        className={showTable ? "chart-table-toggle active" : "chart-table-toggle"}
        type="button"
        aria-haspopup="dialog"
        aria-label={t("actions.showData")}
        title={t("actions.showData")}
        onClick={() => setShowTable(true)}
      >
        <Table2 size={14} strokeWidth={1.6} aria-hidden="true" />
      </button>
      <Modal open={showTable} title={label} onClose={() => setShowTable(false)}>
        <div className="chart-table-wrap">
          <table className="data-table data-table-compact">
            <caption className="visually-hidden">{label}</caption>
            <thead>
              <tr>
                <th scope="col">{t("chart.time", { ns: "metrics" })}</th>
                {tooltipSeries.map((entry) => <th key={entry.id} scope="col" className="numeric">{entry.label}</th>)}
              </tr>
            </thead>
            <tbody>
              {buckets.map((bucket, index) => (
                <tr key={bucket}>
                  <th scope="row">{new Intl.DateTimeFormat(locale, { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", ...(bucketSeconds < 60 ? { second: "2-digit" } : {}), hourCycle: "h23" }).format(new Date(bucket * 1000))}</th>
                  {tooltipSeries.map((entry) => {
                    const value = entry.values[index];
                    return <td key={entry.id} className="numeric">{value === null || value === undefined ? "—" : formatValue(value)}</td>;
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Modal>
    </div>
  );
}
