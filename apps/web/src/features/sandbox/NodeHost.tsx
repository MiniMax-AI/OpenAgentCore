import type { SandboxNode, SandboxNodeDetail, SandboxNodeHistoryRange } from "@oac/agents-client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { TimeSeriesChart } from "../../components/charts/TimeSeriesChart";
import { Section, SegmentedControl } from "../../components/console-ui";
import { TableSkeleton } from "../../components/Skeleton";
import { formatBucket, formatBytes, formatInteger, MISSING } from "../../lib/format";
import { nodeDetailQuery } from "../fleet/fleet-queries";

const GIB = 1024 ** 3;
const RANGES: readonly SandboxNodeHistoryRange[] = ["1h", "6h", "24h"];
const RANGE_LABELS = { "1h": "1 hour", "6h": "6 hours", "24h": "24 hours" } as const;

/** A share (0–1) as a percentage that never rounds a small load down to "0%". */
function formatShare(value: number, locale?: string): string {
  const percent = value * 100;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: percent === 0 ? 0 : percent < 1 ? 2 : percent < 10 ? 1 : 0 }).format(percent)}%`;
}

/**
 * The node's machine: CPU, memory and free disk from its last heartbeat, and
 * its host history over a range, read while the node page is open.
 */
export function NodeHost({ node, reporting }: { node: SandboxNode; reporting: boolean }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage;
  const [range, setRange] = useState<SandboxNodeHistoryRange>("1h");
  const detail = useQuery({ ...nodeDetailQuery(node.id, range), placeholderData: keepPreviousData, refetchInterval: 30_000, refetchIntervalInBackground: false });
  const current = detail.data?.id === node.id ? detail.data : null;
  const host = current?.host ?? null;
  const cores = host?.effective_cpu_cores ?? node.cpu_count;
  const coresLabel = cores === null ? null : formatInteger(cores, locale);
  const cpu = !reporting || coresLabel === null ? MISSING
    : host?.cpu_utilization != null ? t("{{percent}} of {{cores}} CPU", { percent: formatShare(host.cpu_utilization, locale), cores: coresLabel }) : t("{{cores}} CPU", { cores: coresLabel });
  const memory = !reporting ? MISSING
    : host?.total_memory_bytes != null && host.available_memory_bytes != null
      ? t("{{used}} of {{total}} used", { used: formatBytes(host.total_memory_bytes - host.available_memory_bytes), total: formatBytes(host.total_memory_bytes) })
      : formatBytes(node.available_memory_bytes);
  return (
    <Section
      headingId="node-host-heading"
      title={t("Host")}
      help={t("The whole machine as the node reports it with each heartbeat: the busy share of its CPU and its used memory, the highest in each interval. Intervals while the node was offline stay empty.")}
      actions={<SegmentedControl label={t("Time range")} value={range} options={RANGES.map((value) => ({ value, label: t(RANGE_LABELS[value]) }))} onChange={setRange} />}
    >
      <dl className="resource-facts">
        <div><dt>{t("CPU")}</dt><dd>{cpu}</dd></div>
        <div><dt>{t("Memory")}</dt><dd>{memory}</dd></div>
        <div><dt>{t("Free disk")}</dt><dd>{reporting ? formatBytes(node.available_disk_bytes) : MISSING}</dd></div>
      </dl>
      {current ? <NodeHostCharts detail={current} />
        : detail.isError ? <p className="page-status" role="alert">{t("Host history could not be read. {{reason}}", { reason: detail.error instanceof Error ? detail.error.message : "" })}</p>
          : <TableSkeleton label={t("Loading host history…")} rows={3} columns={4} />}
    </Section>
  );
}

/**
 * A node machine's host history as the console's chart pair: the busy share of
 * its CPU and its used memory against the total, the highest in each bucket.
 * Offline and unmeasured buckets stay gaps, never zero.
 */
function NodeHostCharts({ detail, height = 150 }: { detail: SandboxNodeDetail; height?: number }) {
  const { t, i18n } = useTranslation("sandbox");
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
        <figcaption>{t("Host CPU / {{bucket}}", { bucket })}</figcaption>
        <TimeSeriesChart
          label={t("Host CPU / {{bucket}}", { bucket })}
          kind="lines"
          buckets={series.buckets}
          bucketSeconds={resolution}
          series={[{ id: "used", label: t("Used"), color: "var(--series-1)", values: series.cpu }]}
          formatValue={percent}
          formatAxis={percent}
          height={height}
        />
      </figure>
      <figure className="chart-panel">
        <figcaption>{t("Host memory / {{bucket}}", { bucket })}</figcaption>
        <TimeSeriesChart
          label={t("Host memory / {{bucket}}", { bucket })}
          kind="lines"
          buckets={series.buckets}
          bucketSeconds={resolution}
          series={[
            { id: "used", label: t("Used"), color: "var(--series-1)", values: series.memory },
            { id: "total", label: t("Total"), color: "var(--ink-3)", values: series.total },
          ]}
          formatValue={(value) => formatBytes(value * GIB)}
          formatAxis={(value) => `${value.toFixed(value < 10 ? 1 : 0)} GiB`}
          height={height}
        />
      </figure>
    </div>
  );
}
