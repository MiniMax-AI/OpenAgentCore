import { AgentCoreError, type CoreDependencyHealth, type CoreMetrics, type CoreMetricsRange } from "@agents-core-web/agents-client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Network } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { TimeSeriesChart } from "../../components/charts/TimeSeriesChart";
import { TableSkeleton } from "../../components/Skeleton";
import { EmptyState, Kpi, KpiStrip, Meter, PageBody, PageHeader, RefreshButton, Section, SegmentedControl, StatusDot, type Tone } from "../../components/console-ui";
import { formatBucket, formatBytes, formatClock, formatCompact, formatCores, formatDuration, formatInteger, formatPercent, formatRelative, MISSING } from "../../lib/format";
import { coreMetricsQuery } from "./metrics-queries";
import "./MetricsView.css";

const RANGES: readonly CoreMetricsRange[] = ["1h", "6h", "24h", "7d"];
const REFRESH_MS = 30_000;

const healthTone: Record<CoreDependencyHealth, Tone> = { ok: "ok", degraded: "warning", down: "danger", unknown: "neutral" };
const statusTone: Record<CoreMetrics["service"]["status"], Tone> = { running: "ok", maintenance: "warning", degraded: "warning" };

function seconds(value: string | null): number | null {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? null : Math.floor(parsed / 1000);
}

function milliseconds(value: number | null): string {
  return value === null ? MISSING : formatDuration(value / 1000);
}

function ratio(part: number | null, whole: number | null): number | null {
  return part === null || whole === null || whole === 0 ? null : part / whole;
}

/** A figure with its unit or limit set small beside it. */
function Figure({ value, unit }: { value: ReactNode; unit?: ReactNode }) {
  return <>{value}{unit ? <span className="kpi-unit">{unit}</span> : null}</>;
}

/**
 * Monitor › Core metrics: the control plane's own health. It answers what no
 * other page does — is Core serving requests, is its scheduling queue keeping
 * up, are its dependencies healthy, is its process within limits — and leaves
 * Agent outcomes to Agent metrics and sandbox capacity to Sandbox metrics.
 */
export function CoreMetricsPage() {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const [range, setRange] = useState<CoreMetricsRange>("1h");
  const query = useQuery({ ...coreMetricsQuery(range), placeholderData: keepPreviousData, refetchInterval: REFRESH_MS, refetchIntervalInBackground: false });
  const metrics = query.data ?? null;
  const missing = query.error instanceof AgentCoreError && (query.error.status === 404 || query.error.status === 501);
  const error = query.error instanceof Error ? query.error.message : query.error ? String(query.error) : "";

  const header = (
    <PageHeader
      headingId="core-metrics-heading"
      title={<>{t("core.title")}{metrics ? <ServiceMeta metrics={metrics} /> : null}</>}
      help={t("core.description")}
      actions={<>
        <SegmentedControl label={t("range.label")} value={range} options={RANGES.map((value) => ({ value, label: t(`range.${value}`) }))} onChange={setRange} />
        <RefreshButton refreshing={query.isFetching} updatedAt={query.dataUpdatedAt ? formatClock(query.dataUpdatedAt, locale) : null} onClick={() => void query.refetch()} />
      </>}
    />
  );

  let body: ReactNode;
  if (!metrics) {
    body = missing
      ? <EmptyState icon={Network} title={t("core.missingTitle")} description={t("core.missingDescription")} />
      : query.isError
        ? <p className="page-status" role="alert">{t("core.failed", { reason: error })}</p>
        : <TableSkeleton label={t("core.loading")} rows={3} columns={5} />;
  } else {
    body = <CoreMetricsBody metrics={metrics} stale={query.isError ? error : null} />;
  }

  return (
    <section className="page-section console-page metrics-page core-metrics-page" aria-labelledby="core-metrics-heading">
      {header}
      <PageBody>{body}</PageBody>
    </section>
  );
}

function ServiceMeta({ metrics }: { metrics: CoreMetrics }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const started = seconds(metrics.service.started_at);
  const now = Math.floor(Date.now() / 1000);
  return (
    <span className="page-title-meta">
      <StatusDot
        tone={statusTone[metrics.service.status]}
        label={t("core.meta", {
          status: t(`core.status.${metrics.service.status}`),
          version: metrics.service.version ?? MISSING,
          uptime: started === null ? MISSING : formatDuration(Math.max(0, now - started)),
          instances: metrics.service.instances === null ? MISSING : formatInteger(metrics.service.instances, locale),
        })}
      />
    </span>
  );
}

function CoreMetricsBody({ metrics, stale }: { metrics: CoreMetrics; stale: string | null }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1000);
  const start = seconds(metrics.range.start);
  const end = seconds(metrics.range.end);
  const minutes = start !== null && end !== null && end > start ? (end - start) / 60 : null;
  const { ingress, execution, process } = metrics;
  const bucketSeconds = metrics.range.resolution_seconds;
  const bucket = formatBucket(bucketSeconds, locale);
  const ingressBuckets = useMemo(() => ingress.series.map((entry) => seconds(entry.start) ?? 0), [ingress.series]);
  const queueBuckets = useMemo(() => execution.series.map((entry) => seconds(entry.start) ?? 0), [execution.series]);
  const processBuckets = useMemo(() => process.series.map((entry) => seconds(entry.start) ?? 0), [process.series]);
  const integer = (value: number) => formatInteger(value, locale);
  const compact = (value: number) => formatCompact(value, locale);
  const gib = 1024 ** 3;

  return (
    <>
      {stale ? <p className="coverage-note coverage-note-error" role="alert">{t("core.stale", { reason: stale })}</p> : null}

      <KpiStrip label={t("core.kpiLabel")}>
        <Kpi label={t("core.requestRate")} value={ingress.requests === null || minutes === null ? MISSING : formatCompact(ingress.requests / minutes, locale)} />
        <Kpi
          label={t("core.errorRate")}
          help={t("core.errorRateHelp")}
          value={formatPercent(ratio(ingress.server_errors, ingress.requests), locale)}
          tone={(ratio(ingress.server_errors, ingress.requests) ?? 0) >= 0.01 ? "danger" : undefined}
        />
        <Kpi label={t("core.latencyP95")} value={milliseconds(ingress.latency_ms.p95)} />
        <Kpi label={t("core.activeTurns")} value={execution.active_turns === null ? MISSING : integer(execution.active_turns)} />
        <Kpi label={t("core.queuedTurns")} help={t("core.queuedHelp")} value={execution.queued_turns === null ? MISSING : integer(execution.queued_turns)} tone={(execution.queued_turns ?? 0) > 0 ? "warning" : undefined} />
      </KpiStrip>

      <Section headingId="core-ingress-heading" title={t("core.ingress.title")} help={t("core.ingress.help")}>
        <div className="chart-grid">
          <figure className="chart-panel">
            <figcaption>{t("core.ingress.chart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.ingress.chart", { bucket })}
              kind="columns"
              stacked
              buckets={ingressBuckets}
              bucketSeconds={bucketSeconds}
              series={[
                { id: "success", label: t("core.ingress.success"), color: "var(--series-1)", values: ingress.series.map((entry) => entry.success) },
                { id: "client", label: t("core.ingress.clientError"), color: "var(--series-3)", values: ingress.series.map((entry) => entry.client_error) },
                { id: "server", label: t("core.ingress.serverError"), color: "var(--red)", values: ingress.series.map((entry) => entry.server_error) },
              ]}
              formatValue={integer}
              formatAxis={compact}
            />
          </figure>
          <figure className="chart-panel">
            <figcaption>{t("core.ingress.latencyChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.ingress.latencyChart", { bucket })}
              kind="lines"
              buckets={ingressBuckets}
              bucketSeconds={bucketSeconds}
              series={[
                { id: "p50", label: t("core.ingress.p50"), color: "var(--series-1)", values: ingress.series.map((entry) => entry.p50_ms) },
                { id: "p95", label: t("core.ingress.p95"), color: "var(--series-3)", values: ingress.series.map((entry) => entry.p95_ms) },
              ]}
              formatValue={(value) => milliseconds(value)}
              formatAxis={(value) => (value === 0 ? "0" : milliseconds(value))}
            />
          </figure>
        </div>
        {ingress.routes.length ? (
          <div className="table-frame">
            <table className="data-table" aria-label={t("core.ingress.title")}>
              <thead>
                <tr>
                  <th scope="col">{t("core.ingress.route")}</th>
                  <th scope="col" className="numeric">{t("core.ingress.requests")}</th>
                  <th scope="col" className="numeric">{t("core.ingress.clientError")}</th>
                  <th scope="col" className="numeric">{t("core.ingress.serverError")}</th>
                  <th scope="col">{t("core.ingress.serverShare")}</th>
                  <th scope="col" className="numeric">{t("core.ingress.p95")}</th>
                </tr>
              </thead>
              <tbody>
                {ingress.routes.map((route) => {
                  const share = ratio(route.server_errors, route.requests);
                  return (
                    <tr key={route.family}>
                      <th scope="row"><span className="table-primary">{t(`core.ingress.families.${route.family}`, { defaultValue: route.family })}</span></th>
                      <td className="numeric">{route.requests === null ? MISSING : integer(route.requests)}</td>
                      <td className="numeric">{route.client_errors === null ? MISSING : integer(route.client_errors)}</td>
                      <td className={route.server_errors ? "numeric numeric-danger" : "numeric"}>{route.server_errors === null ? MISSING : integer(route.server_errors)}</td>
                      <td>
                        <span className="table-meter">
                          <Meter value={share} limit={1} warnAt={0.01} dangerAt={0.05} label={t("core.ingress.serverShare")} />
                          <span>{formatPercent(share, locale)}</span>
                        </span>
                      </td>
                      <td className="numeric">{milliseconds(route.p95_ms)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
      </Section>

      <Section
        headingId="core-queue-heading"
        title={<>{t("core.queue.title")}<span className="section-meta">{t("core.queue.meta", { p50: milliseconds(execution.queue_wait_ms.p50), p95: milliseconds(execution.queue_wait_ms.p95) })}</span></>}
        help={t("core.queue.help")}
      >
        <div className="chart-grid chart-grid-single">
          <figure className="chart-panel">
            <figcaption>{t("core.queue.chart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.queue.chart", { bucket })}
              kind="lines"
              buckets={queueBuckets}
              bucketSeconds={bucketSeconds}
              series={[{ id: "queued", label: t("core.queue.queued"), color: "var(--series-1)", values: execution.series.map((entry) => entry.queued) }]}
              formatValue={integer}
              height={140}
            />
          </figure>
        </div>
      </Section>

      <Section headingId="core-dependencies-heading" title={t("core.dependencies.title")} help={t("core.dependencies.help")}>
        {metrics.dependencies.length ? (
          <div className="table-frame">
            <table className="data-table" aria-label={t("core.dependencies.title")}>
              <thead>
                <tr>
                  <th scope="col">{t("core.dependencies.name")}</th>
                  <th scope="col">{t("core.dependencies.kind")}</th>
                  <th scope="col">{t("core.dependencies.status")}</th>
                  <th scope="col" className="numeric">{t("core.dependencies.latency")}</th>
                  <th scope="col" className="numeric">{t("core.dependencies.errors")}</th>
                  <th scope="col" className="numeric">{t("core.dependencies.checked")}</th>
                </tr>
              </thead>
              <tbody>
                {metrics.dependencies.map((dependency) => (
                  <tr key={dependency.id}>
                    <th scope="row"><span className="table-primary">{dependency.name}</span></th>
                    <td className="table-muted">{t(`core.dependencies.kinds.${dependency.kind}`)}</td>
                    <td><StatusDot tone={healthTone[dependency.status]} label={t(`core.dependencies.health.${dependency.status}`)} /></td>
                    <td className="numeric">{milliseconds(dependency.latency_p95_ms)}</td>
                    <td className={(dependency.error_rate ?? 0) >= 0.01 ? "numeric numeric-danger" : "numeric"}>{formatPercent(dependency.error_rate, locale)}</td>
                    <td className="numeric">{formatRelative(seconds(dependency.checked_at), now, locale)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : <EmptyState title={t("core.dependencies.empty")} />}
      </Section>

      <Section headingId="core-process-heading" title={t("core.process.title")} help={t("core.process.help")}>
        <KpiStrip label={t("core.process.kpiLabel")}>
          <Kpi label={t("core.process.cpu")} value={process.cpu_cores === null ? MISSING : <Figure value={formatCores(process.cpu_cores, locale)} unit={process.cpu_limit_cores === null ? t("core.process.cores") : `/ ${formatCores(process.cpu_limit_cores, locale)} ${t("core.process.cores")}`} />} />
          <Kpi label={t("core.process.memory")} value={process.memory_bytes === null ? MISSING : <Figure value={formatBytes(process.memory_bytes)} unit={process.memory_limit_bytes === null ? undefined : `/ ${formatBytes(process.memory_limit_bytes)}`} />} />
          <Kpi label={t("core.process.connections")} value={process.open_connections === null ? MISSING : integer(process.open_connections)} />
          <Kpi label={t("core.process.disk")} value={process.disk_used_bytes === null ? MISSING : <Figure value={formatBytes(process.disk_used_bytes)} unit={process.disk_total_bytes === null ? undefined : `/ ${formatBytes(process.disk_total_bytes)}`} />} />
        </KpiStrip>
        <div className="chart-grid">
          <figure className="chart-panel">
            <figcaption>{t("core.process.cpuChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.process.cpuChart", { bucket })}
              kind="lines"
              buckets={processBuckets}
              bucketSeconds={bucketSeconds}
              series={[{ id: "cpu", label: t("core.process.cpu"), color: "var(--series-1)", values: process.series.map((entry) => entry.cpu_cores) }]}
              formatValue={(value) => `${formatCores(value, locale)} ${t("core.process.cores")}`}
              formatAxis={(value) => formatCores(value, locale)}
              height={140}
            />
          </figure>
          <figure className="chart-panel">
            <figcaption>{t("core.process.memoryChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.process.memoryChart", { bucket })}
              kind="lines"
              buckets={processBuckets}
              bucketSeconds={bucketSeconds}
              series={[{ id: "memory", label: t("core.process.memory"), color: "var(--series-2)", values: process.series.map((entry) => entry.memory_bytes === null ? null : entry.memory_bytes / gib) }]}
              formatValue={(value) => formatBytes(value * gib)}
              formatAxis={(value) => `${value.toFixed(value < 10 ? 1 : 0)} GiB`}
              height={140}
            />
          </figure>
        </div>
      </Section>
    </>
  );
}
