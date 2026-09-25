import type { SandboxNodeDetail } from "@agents-core-web/agents-client";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { TimeSeriesChart } from "../../components/charts/TimeSeriesChart";
import { formatBucket, formatBytes } from "../../lib/format";

const GIB = 1024 ** 3;

/** A share (0–1) as a percentage that never rounds a small load down to "0%". */
export function formatShare(value: number, locale?: string): string {
  const percent = value * 100;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: percent === 0 ? 0 : percent < 1 ? 2 : percent < 10 ? 1 : 0 }).format(percent)}%`;
}

/**
 * A node machine's host history as the console's chart pair: the busy share of
 * its CPU and its used memory against the total, the highest in each bucket.
 * Offline and unmeasured buckets stay gaps, never zero.
 */
export function NodeHostCharts({ detail, height = 150 }: { detail: SandboxNodeDetail; height?: number }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { resolution_seconds: resolution, points } = detail.history;
  const bucket = formatBucket(resolution, locale);
  const total = detail.host.total_memory_bytes;
  const series = useMemo(() => ({
    buckets: points.map((point) => Math.floor(Date.parse(point.start) / 1000)),
    cpu: points.map((point) => (point.cpu_utilization_max === null ? null : point.cpu_utilization_max * 100)),
    memory: points.map((point) => (point.memory_used_bytes_max === null ? null : point.memory_used_bytes_max / GIB)),
    total: points.map(() => (total === null ? null : total / GIB)),
  }), [points, total]);
  const percent = (value: number) => formatShare(value / 100, locale);
  return (
    <div className="chart-grid">
      <figure className="chart-panel">
        <figcaption>{t("sandbox.nodeDialog.hostCpu", { bucket })}</figcaption>
        <TimeSeriesChart
          label={t("sandbox.nodeDialog.hostCpu", { bucket })}
          kind="lines"
          buckets={series.buckets}
          bucketSeconds={resolution}
          series={[{ id: "used", label: t("sandbox.nodeDialog.used"), color: "var(--series-1)", values: series.cpu }]}
          formatValue={percent}
          formatAxis={percent}
          height={height}
        />
      </figure>
      <figure className="chart-panel">
        <figcaption>{t("sandbox.nodeDialog.hostMemory", { bucket })}</figcaption>
        <TimeSeriesChart
          label={t("sandbox.nodeDialog.hostMemory", { bucket })}
          kind="lines"
          buckets={series.buckets}
          bucketSeconds={resolution}
          series={[
            { id: "used", label: t("sandbox.nodeDialog.used"), color: "var(--series-1)", values: series.memory },
            { id: "total", label: t("sandbox.nodeDialog.total"), color: "var(--ink-3)", values: series.total },
          ]}
          formatValue={(value) => formatBytes(value * GIB)}
          formatAxis={(value) => `${value.toFixed(value < 10 ? 1 : 0)} GiB`}
          height={height}
        />
      </figure>
    </div>
  );
}
