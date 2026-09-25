import { AgentCoreError, type CoreJobStatus, type CoreMetrics, type CoreMetricsRange } from "@agents-core-web/agents-client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Network } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { LiveNumber } from "../../components/live-number";
import { TimeSeriesChart } from "../../components/charts/TimeSeriesChart";
import { DashboardSkeleton, TableSkeleton } from "../../components/Skeleton";
import { EmptyState, Kpi, KpiStrip, PageBody, PageHeader, RefreshButton, Section, SegmentedControl, StatusDot, type Tone } from "../../components/console-ui";
import { formatBucket, formatBytes, formatClock, formatDuration, formatInteger, formatRelative, MISSING } from "../../lib/format";
import { coreMetricsQuery } from "./metrics-queries";
import "./MetricsView.css";

const RANGES: readonly CoreMetricsRange[] = ["1h", "6h", "24h", "7d"];
const REFRESH_MS = 30_000;
const GIB = 2 ** 30;

const jobTone: Record<CoreJobStatus, Tone> = { ok: "ok", failing: "danger", stopped: "warning", unknown: "neutral" };
const statusTone: Record<CoreMetrics["service"]["status"], Tone> = { running: "ok", maintenance: "warning", degraded: "warning", unknown: "neutral" };

function seconds(value: string | null): number | null {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? null : Math.floor(parsed / 1000);
}

function milliseconds(value: number | null): string {
  return value === null ? MISSING : formatDuration(value / 1000);
}

/** Cores to two decimals below ten ("0.35"), whole above ("12"). */
function cores(value: number, locale: string | undefined): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: value < 10 ? 2 : 0 }).format(value);
}

/** A share of a limit at or above which a figure is shown as a warning. */
function nearLimit(value: number | null, limit: number | null, share: number): boolean {
  return value !== null && limit !== null && limit > 0 && value / limit >= share;
}

/** A figure with its unit or limit set small beside it. */
function Figure({ value, unit }: { value: ReactNode; unit?: ReactNode }) {
  return <>{value}{unit ? <span className="kpi-unit">{unit}</span> : null}</>;
}

/**
 * Monitor › Core metrics: the health of the one Core process — can it run
 * Turns (slots, queue, daemons), is its database responsive, are its
 * background jobs running. Agent outcomes stay on Agent metrics and sandbox
 * capacity on Sandbox metrics; the page follows the same header, headline
 * figures and chart-then-table sections as they do.
 */
export function CoreMetricsPage() {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const [range, setRange] = useState<CoreMetricsRange>("1h");
  const query = useQuery({ ...coreMetricsQuery(range), placeholderData: keepPreviousData, refetchInterval: REFRESH_MS, refetchIntervalInBackground: false });
  const metrics = query.data ?? null;
  const missing = query.error instanceof AgentCoreError && (query.error.status === 404 || query.error.status === 501);
  const error = query.error instanceof Error ? query.error.message : query.error ? String(query.error) : "";

  let body: ReactNode;
  if (!metrics) {
    body = missing
      ? <EmptyState icon={Network} title={t("core.missingTitle")} description={t("core.missingDescription")} />
      : query.isError
        ? <p className="page-status" role="alert">{t("core.failed", { reason: error })}</p>
        : <DashboardSkeleton label={t("core.loading")} figures={5} />;
  } else {
    body = <CoreMetricsBody metrics={metrics} stale={query.isError ? error : null} />;
  }

  return (
    <section className="page-section console-page metrics-page core-metrics-page" aria-labelledby="core-metrics-heading">
      <PageHeader
        headingId="core-metrics-heading"
        title={<>{t("core.title")}{metrics ? <ServiceMeta metrics={metrics} /> : null}</>}
        help={t("core.description")}
        actions={<>
          <SegmentedControl label={t("range.label")} value={range} options={RANGES.map((value) => ({ value, label: t(`range.${value}`) }))} onChange={setRange} />
          <RefreshButton refreshing={query.isFetching} updatedAt={query.dataUpdatedAt ? formatClock(query.dataUpdatedAt, locale) : null} onClick={() => void query.refetch()} />
        </>}
      />
      <PageBody>{body}</PageBody>
    </section>
  );
}

function ServiceMeta({ metrics }: { metrics: CoreMetrics }) {
  const { t } = useTranslation("metrics");
  const started = seconds(metrics.service.started_at);
  const now = Math.floor(Date.now() / 1000);
  const notOwner = metrics.service.execution_owner === false;
  return (
    <span className="page-title-meta">
      <StatusDot
        tone={notOwner ? "danger" : statusTone[metrics.service.status]}
        label={t("core.meta", {
          status: notOwner ? t("core.notOwner") : t(`core.status.${metrics.service.status}`),
          revision: metrics.service.revision ?? MISSING,
          uptime: started === null ? MISSING : formatDuration(Math.max(0, now - started)),
        })}
      />
    </span>
  );
}

function CoreMetricsBody({ metrics, stale }: { metrics: CoreMetrics; stale: string | null }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1000);
  const { execution, database, process } = metrics;
  const processBuckets = useMemo(() => process.series.map((entry) => seconds(entry.start) ?? 0), [process.series]);
  const bucketSeconds = metrics.range.resolution_seconds;
  const bucket = formatBucket(bucketSeconds, locale);
  const executionBuckets = useMemo(() => execution.series.map((entry) => seconds(entry.start) ?? 0), [execution.series]);
  const databaseBuckets = useMemo(() => database.series.map((entry) => seconds(entry.start) ?? 0), [database.series]);
  const count = (value: number | null) => (value === null ? MISSING : formatInteger(value, locale));
  const integer = (value: number) => formatInteger(value, locale);
  const slotsFull = execution.slots_in_use !== null && execution.slots_total !== null && execution.slots_in_use >= execution.slots_total;

  return (
    <>
      {stale ? <p className="coverage-note coverage-note-error" role="alert">{t("core.stale", { reason: stale })}</p> : null}

      <KpiStrip label={t("core.kpiLabel")}>
        <Kpi
          label={t("core.slots")}
          help={t("core.slotsHelp")}
          value={execution.slots_in_use === null ? MISSING : <Figure value={<LiveNumber value={execution.slots_in_use} />} unit={execution.slots_total === null ? undefined : `/ ${integer(execution.slots_total)}`} />}
          tone={slotsFull ? "warning" : undefined}
        />
        <Kpi
          label={t("core.queued")}
          help={t("core.queuedHelp")}
          value={execution.queued_turns === null ? MISSING : <Figure value={<LiveNumber value={execution.queued_turns} />} unit={execution.waiting_for_daemon ? t("core.waitingForDaemon", { n: execution.waiting_for_daemon }) : undefined} />}
          tone={(execution.queued_turns ?? 0) > 0 ? "warning" : undefined}
        />
        <Kpi label={t("core.daemons")} help={t("core.daemonsHelp")} value={<LiveNumber value={execution.connected_daemons} />} />
        <Kpi label={t("core.databaseLatency")} value={milliseconds(database.ping_ms.p95)} />
        <Kpi
          label={t("core.cpu")}
          help={t("core.cpuHelp")}
          value={process.cpu_cores === null ? MISSING : <Figure value={cores(process.cpu_cores, locale)} unit={process.cpu_limit_cores === null ? t("core.cores", { value: "" }).trim() : `/ ${t("core.cores", { value: cores(process.cpu_limit_cores, locale) })}`} />}
          tone={nearLimit(process.cpu_cores, process.cpu_limit_cores, 0.8) ? "warning" : undefined}
        />
        <Kpi
          label={t("core.memory")}
          help={t("core.memoryHelp")}
          value={process.rss_bytes === null ? MISSING : <Figure value={formatBytes(process.rss_bytes)} unit={process.memory_limit_bytes === null ? undefined : `/ ${formatBytes(process.memory_limit_bytes)}`} />}
          tone={nearLimit(process.rss_bytes, process.memory_limit_bytes, 0.85) ? "warning" : undefined}
        />
      </KpiStrip>

      <Section
        headingId="core-execution-heading"
        title={<>{t("core.execution.title")}<span className="section-meta">{t("core.execution.meta", {
          oldest: execution.oldest_queued_seconds === null ? MISSING : formatDuration(execution.oldest_queued_seconds),
          interrupted: count(execution.interrupted),
          unavailable: count(execution.unavailable),
        })}</span></>}
        help={t("core.execution.help")}
      >
        <div className="chart-grid">
          <figure className="chart-panel">
            <figcaption>{t("core.execution.turnsChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.execution.turnsChart", { bucket })}
              kind="lines"
              buckets={executionBuckets}
              bucketSeconds={bucketSeconds}
              series={[
                { id: "in-progress", label: t("core.execution.inProgress"), color: "var(--series-1)", values: execution.series.map((entry) => entry.in_progress) },
                { id: "queued", label: t("core.execution.queued"), color: "var(--series-3)", values: execution.series.map((entry) => entry.queued) },
              ]}
              formatValue={integer}
            />
          </figure>
          <figure className="chart-panel">
            <figcaption>{t("core.execution.waitChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.execution.waitChart", { bucket })}
              kind="lines"
              buckets={executionBuckets}
              bucketSeconds={bucketSeconds}
              series={[{ id: "wait", label: t("core.execution.wait"), color: "var(--series-1)", values: execution.series.map((entry) => entry.queue_wait_p95_ms) }]}
              formatValue={(value) => milliseconds(value)}
              formatAxis={(value) => (value === 0 ? "0" : milliseconds(value))}
            />
          </figure>
        </div>
      </Section>

      <Section
        headingId="core-database-heading"
        title={<>{t("core.database.title")}<span className="section-meta">{t("core.database.meta", {
          size: formatBytes(database.size_bytes),
          inUse: count(database.pool.in_use),
          max: count(database.pool.max),
        })}</span></>}
        help={t("core.database.help")}
      >
        <div className="chart-grid">
          <figure className="chart-panel">
            <figcaption>{t("core.database.latencyChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.database.latencyChart", { bucket })}
              kind="lines"
              buckets={databaseBuckets}
              bucketSeconds={bucketSeconds}
              series={[{ id: "latency", label: t("core.database.latency"), color: "var(--series-1)", values: database.series.map((entry) => entry.ping_p95_ms) }]}
              formatValue={(value) => milliseconds(value)}
              formatAxis={(value) => (value === 0 ? "0" : milliseconds(value))}
            />
          </figure>
          <figure className="chart-panel">
            <figcaption>{t("core.database.poolChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.database.poolChart", { bucket })}
              kind="lines"
              buckets={databaseBuckets}
              bucketSeconds={bucketSeconds}
              series={[
                { id: "in-use", label: t("core.database.inUse"), color: "var(--series-1)", values: database.series.map((entry) => entry.pool_in_use) },
                // The limit is drawn only where Core observed the pool.
                { id: "max", label: t("core.database.max"), color: "var(--ink-3)", values: database.series.map((entry) => (entry.pool_in_use === null ? null : database.pool.max)) },
              ]}
              formatValue={integer}
            />
          </figure>
        </div>
      </Section>

      <Section
        headingId="core-process-heading"
        title={<>{t("core.process.title")}<span className="section-meta">{t("core.process.meta", {
          heap: formatBytes(process.memory_bytes),
          goroutines: count(process.goroutines),
        })}</span></>}
        help={t("core.process.help")}
      >
        <div className="chart-grid">
          <figure className="chart-panel">
            <figcaption>{t("core.process.cpuChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.process.cpuChart", { bucket })}
              kind="lines"
              buckets={processBuckets}
              bucketSeconds={bucketSeconds}
              series={[
                { id: "cpu", label: t("core.process.cpu"), color: "var(--series-1)", values: process.series.map((entry) => entry.cpu_cores) },
                // The limit is drawn only where Core observed the process.
                { id: "limit", label: t("core.process.cpuLimit"), color: "var(--ink-3)", values: process.series.map((entry) => (entry.cpu_cores === null ? null : process.cpu_limit_cores)) },
              ]}
              formatValue={(value) => t("core.cores", { value: cores(value, locale) })}
              formatAxis={(value) => cores(value, locale)}
            />
          </figure>
          <figure className="chart-panel">
            <figcaption>{t("core.process.memoryChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("core.process.memoryChart", { bucket })}
              kind="lines"
              buckets={processBuckets}
              bucketSeconds={bucketSeconds}
              series={[
                // In GiB, so the axis ticks fall on round values.
                { id: "rss", label: t("core.process.rss"), color: "var(--series-1)", values: process.series.map((entry) => (entry.rss_bytes === null ? null : entry.rss_bytes / GIB)) },
                { id: "limit", label: t("core.process.memoryLimit"), color: "var(--ink-3)", values: process.series.map((entry) => (entry.rss_bytes === null || process.memory_limit_bytes === null ? null : process.memory_limit_bytes / GIB)) },
              ]}
              formatValue={(value) => formatBytes(value * GIB)}
              formatAxis={(value) => (value === 0 ? "0" : `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(value)} GiB`)}
            />
          </figure>
        </div>
      </Section>

      <Section headingId="core-jobs-heading" title={t("core.jobs.title")} help={t("core.jobs.help")}>
        {metrics.jobs.length ? (
          <div className="table-frame">
            <table className="data-table" aria-label={t("core.jobs.title")}>
              <thead>
                <tr>
                  <th scope="col">{t("core.jobs.job")}</th>
                  <th scope="col">{t("core.jobs.status")}</th>
                  <th scope="col" className="numeric">{t("core.jobs.lastRun")}</th>
                  <th scope="col" className="numeric">{t("core.jobs.processed")}</th>
                  <th scope="col" className="numeric">{t("core.jobs.failed")}</th>
                </tr>
              </thead>
              <tbody>
                {metrics.jobs.map((job) => (
                  <tr key={job.id}>
                    <th scope="row"><span className="table-primary">{t(`core.jobs.names.${job.id}`, { defaultValue: job.id })}</span></th>
                    <td><StatusDot tone={jobTone[job.status]} label={t(`core.jobs.health.${job.status}`)} /></td>
                    <td className="numeric" title={job.last_run_at ?? undefined}>{formatRelative(seconds(job.last_run_at), now, locale)}</td>
                    <td className="numeric">{count(job.processed)}</td>
                    <td className={job.failed ? "numeric numeric-danger" : "numeric"}>{count(job.failed)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : <EmptyState title={t("core.jobs.empty")} />}
      </Section>
    </>
  );
}

