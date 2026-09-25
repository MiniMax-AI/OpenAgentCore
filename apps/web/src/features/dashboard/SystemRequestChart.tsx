import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import type { OperatorMetricsSummary } from "@agents-core-web/agents-client";

export function SystemRequestChart({ summary }: { summary: OperatorMetricsSummary | null }) {
  const { t, i18n } = useTranslation("dashboard");
  const points = useMemo(() => {
    if (!summary) return [];
    const start = Date.parse(summary.start);
    const end = Date.parse(summary.end);
    const stepMS = summary.step_seconds * 1_000;
    const requests = new Map<number, { total: number; errors: number }>();
    for (const row of summary.requests) {
      const at = Date.parse(row.start);
      const prior = requests.get(at) ?? { total: 0, errors: 0 };
      prior.total += row.count;
      if (row.outcome === "server_error") prior.errors += row.count;
      requests.set(at, prior);
    }
    const covered = new Set(summary.collector.filter((row) => row.source === "request").map((row) => Date.parse(row.start)));
    const result = [];
    for (let at = start; at < end; at += stepMS) {
      result.push({ at, value: covered.has(at) ? requests.get(at) ?? { total: 0, errors: 0 } : null });
    }
    return result;
  }, [summary]);
  const known = points.filter((point) => point.value !== null);
  const maximum = Math.max(1, ...known.map((point) => point.value?.total ?? 0));
  const width = 780;
  const height = 150;
  const left = 28;
  const baseline = 124;
  const plotWidth = width - left - 8;
  const slot = plotWidth / Math.max(1, points.length);
  return <figure className="dashboard-system-request-chart">
    <figcaption><strong>{t("system.requestTrend")}</strong><span>{t("system.requestTrendDetail", { covered: known.length, total: points.length })}</span></figcaption>
    {known.length ? <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label={t("system.requestTrendLabel", { covered: known.length, total: points.length })}>
      <line x1={left} y1={baseline} x2={width - 8} y2={baseline} stroke="currentColor" opacity=".25" />
      {points.map((point, index) => {
        if (point.value === null) return null;
        const x = left + index * slot + slot * .12;
        const barWidth = Math.max(1, slot * .75);
        const barHeight = Math.max(1, point.value.total / maximum * 100);
        const errorHeight = Math.max(0, point.value.errors / maximum * 100);
        return <g key={point.at}>
          <title>{`${new Date(point.at).toLocaleString(i18n.resolvedLanguage)}: ${point.value.total} ${t("system.requests")}, ${point.value.errors} ${t("system.errors")}`}</title>
          <rect x={x} y={baseline - barHeight} width={barWidth} height={barHeight} fill="var(--accent, #377db8)" opacity=".8" />
          {errorHeight > 0 ? <rect x={x} y={baseline - errorHeight} width={barWidth} height={errorHeight} fill="#d66b62" /> : null}
        </g>;
      })}
    </svg> : <p>{t("system.noRequestSamples")}</p>}
  </figure>;
}
