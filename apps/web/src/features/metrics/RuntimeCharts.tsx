import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { TimeSeriesChart } from "../../components/charts/TimeSeriesChart";
import { formatBucket, formatBytes } from "../../lib/format";
import type { RuntimeTrendSample } from "../dashboard/runtime-trends";

const GIB = 1024 ** 3;

function known(values: readonly (number | null)[]): number[] {
  return values.filter((value): value is number => typeof value === "number" && Number.isFinite(value));
}

/**
 * Hosted Runtime trends as the console's standard chart pair: CPU utilization
 * (average and peak across the sampled sandboxes) and memory against its
 * configured limit. Missing samples stay gaps, never zero.
 */
export function RuntimeCharts({ samples, resolutionSeconds, height = 168 }: { samples: readonly RuntimeTrendSample[]; resolutionSeconds: number; height?: number }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const bucket = formatBucket(resolutionSeconds, locale);
  const series = useMemo(() => {
    const buckets = samples.map((sample) => Math.floor(sample.sampledAt / 1000));
    const ratios = samples.map((sample) => known(sample.targets.map((target) => target.cpuRatio)));
    return {
      buckets,
      // One sandbox has no spread to show: its average and peak are the same line.
      single: ratios.every((values) => values.length <= 1),
      cpuAverage: ratios.map((values) => (values.length ? (values.reduce((sum, value) => sum + value, 0) / values.length) * 100 : null)),
      cpuPeak: ratios.map((values) => (values.length ? Math.max(...values) * 100 : null)),
      memoryUsed: samples.map((sample) => (sample.memoryUsageBytes === null ? null : sample.memoryUsageBytes / GIB)),
      memoryLimit: samples.map((sample) => (sample.memoryLimitBytes === null ? null : sample.memoryLimitBytes / GIB)),
    };
  }, [samples]);
  // Idle sandboxes use well under 1%: keep decimals there so the axis never reads "0%" on every tick.
  const percent = (value: number) => `${new Intl.NumberFormat(locale, { maximumFractionDigits: value === 0 ? 0 : value < 1 ? 2 : value < 10 ? 1 : 0 }).format(value)}%`;
  return (
    <div className="chart-grid">
      <figure className="chart-panel">
        <figcaption>{t("sandbox.charts.cpu", { bucket })}</figcaption>
        <TimeSeriesChart
          label={t("sandbox.charts.cpu", { bucket })}
          kind="lines"
          buckets={series.buckets}
          bucketSeconds={resolutionSeconds}
          series={series.single
            ? [{ id: "used", label: t("sandbox.charts.cpuUsed"), color: "var(--series-1)", values: series.cpuAverage }]
            : [
              { id: "average", label: t("sandbox.charts.cpuAverage"), color: "var(--series-1)", values: series.cpuAverage },
              { id: "peak", label: t("sandbox.charts.cpuPeak"), color: "var(--series-3)", values: series.cpuPeak },
            ]}
          formatValue={percent}
          formatAxis={percent}
          height={height}
        />
      </figure>
      <figure className="chart-panel">
        <figcaption>{t("sandbox.charts.memory", { bucket })}</figcaption>
        <TimeSeriesChart
          label={t("sandbox.charts.memory", { bucket })}
          kind="lines"
          buckets={series.buckets}
          bucketSeconds={resolutionSeconds}
          series={[
            { id: "used", label: t("sandbox.charts.memoryUsed"), color: "var(--series-1)", values: series.memoryUsed },
            { id: "limit", label: t("sandbox.charts.memoryLimit"), color: "var(--ink-3)", values: series.memoryLimit },
          ]}
          formatValue={(value) => formatBytes(value * GIB)}
          formatAxis={(value) => `${value.toFixed(value < 10 ? 1 : 0)} GiB`}
          height={height}
        />
      </figure>
    </div>
  );
}
