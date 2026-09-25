import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AdminClient, type CoreMetricsView, type OperatorMetricsRange } from "@agents-core-web/agents-client";

import { isLocalProxyBaseUrl } from "../../lib/connection";
import { formatDashboardBytes } from "./dashboard-model";

export function CoreServiceMetricsContent({ coreBaseUrl, range, refresh }: { coreBaseUrl: string; range: OperatorMetricsRange; refresh: number }) {
  const { t, i18n } = useTranslation("dashboard");
  const localSource = isLocalProxyBaseUrl(coreBaseUrl);
  const [metrics, setMetrics] = useState<CoreMetricsView | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    if (!localSource) {
      setMetrics(null);
      setFailed(false);
      return () => controller.abort();
    }
    setMetrics(null);
    setFailed(false);
    void new AdminClient().retrieveCoreMetrics(range, { signal: controller.signal }).then((value) => {
      if (!controller.signal.aborted) { setMetrics(value); setFailed(false); }
    }).catch(() => {
      if (!controller.signal.aborted) { setMetrics(null); setFailed(true); }
    });
    return () => controller.abort();
  }, [localSource, range, refresh]);
  const unavailable = t("system.unavailableValue");
  const number = (value: number | null | undefined) => value == null ? unavailable : value.toLocaleString(i18n.resolvedLanguage);
  const milliseconds = (value: number | null | undefined) => value == null ? unavailable : `${number(Math.round(value))} ms`;
  const execution = metrics?.execution;
  const database = metrics?.database;
  const process = metrics?.process;
  const rows: Array<[string, string]> = [
    [t("core.queued"), number(execution?.queued_turns)],
    [t("core.running"), number(execution?.in_progress_turns)],
    [t("core.waitingDaemon"), number(execution?.waiting_for_daemon)],
    [t("core.connectedDaemons"), number(execution?.connected_daemons)],
    [t("core.oldestQueue"), execution?.oldest_queued_seconds == null ? unavailable : `${number(Math.round(execution.oldest_queued_seconds))} s`],
    [t("core.queueP95"), milliseconds(execution?.queue_wait_ms.p95)],
    [t("core.interrupted"), number(execution?.interrupted)],
    [t("core.unavailable"), number(execution?.unavailable)],
  ];
  return <section className="dashboard-system dashboard-core-metrics" aria-labelledby="dashboard-core-metrics-heading">
    <header className="dashboard-system-header"><div>
      <h2 id="dashboard-core-metrics-heading">{t("core.title")}</h2>
      <p>{t("core.subtitle")}</p>
    </div><span className="dashboard-system-source">{metrics ? t("core.status", { status: metrics.service.status }) : !localSource ? t("system.adminUnavailable") : failed ? t("system.stale") : t("system.loading")}</span></header>
    <div className="dashboard-system-metrics">
      <div><small>{t("core.slots")}</small><strong>{execution?.slots_in_use == null || execution.slots_total == null ? unavailable : `${number(execution.slots_in_use)}/${number(execution.slots_total)}`}</strong><span>{t("core.executionOwner", { status: metrics?.service.execution_owner == null ? unavailable : metrics.service.execution_owner ? t("core.yes") : t("core.no") })}</span></div>
      <div><small>{t("core.queue")}</small><strong>{number(execution?.queued_turns)}</strong><span>{t("core.runningCount", { value: number(execution?.in_progress_turns) })}</span></div>
      <div><small>{t("core.databasePing")}</small><strong>{milliseconds(database?.ping_ms.p95)}</strong><span>{t("core.pool", { used: number(database?.pool.in_use), max: number(database?.pool.max) })}</span></div>
      <div><small>{t("core.processMemory")}</small><strong>{process?.memory_bytes == null ? unavailable : formatDashboardBytes(process.memory_bytes)}</strong><span>{t("core.goroutines", { value: number(process?.goroutines) })}</span></div>
    </div>
    <div className="dashboard-system-tables">
      <section><h3>{t("core.executionTable")}</h3><div className="dashboard-system-table-scroll"><table><tbody>{rows.map(([label, value]) => <tr key={label}><th>{label}</th><td>{value}</td></tr>)}</tbody></table></div></section>
      <section><h3>{t("core.databaseTable")}</h3><div className="dashboard-system-table-scroll"><table><tbody>
        <tr><th>{t("core.pingP50")}</th><td>{milliseconds(database?.ping_ms.p50)}</td></tr>
        <tr><th>{t("core.pingP95")}</th><td>{milliseconds(database?.ping_ms.p95)}</td></tr>
        <tr><th>{t("core.poolInUse")}</th><td>{number(database?.pool.in_use)}</td></tr>
        <tr><th>{t("core.poolIdle")}</th><td>{number(database?.pool.idle)}</td></tr>
        <tr><th>{t("core.databaseSize")}</th><td>{database?.size_bytes == null ? unavailable : formatDashboardBytes(database.size_bytes)}</td></tr>
      </tbody></table></div></section>
      <section className="dashboard-system-nodes"><h3>{t("core.jobsTable")}</h3>
        {metrics?.jobs.length ? <div className="dashboard-system-table-scroll"><table><thead><tr><th>{t("core.job")}</th><th>{t("core.jobStatus")}</th><th>{t("core.lastRun")}</th><th>{t("core.processed")}</th><th>{t("core.failed")}</th></tr></thead><tbody>{metrics.jobs.map((job) => <tr key={job.id}><th>{job.id}</th><td>{job.status}</td><td>{job.last_run_at == null ? unavailable : new Date(job.last_run_at).toLocaleString(i18n.resolvedLanguage)}</td><td>{number(job.processed)}</td><td>{number(job.failed)}</td></tr>)}</tbody></table></div> : <p>{!localSource ? t("system.adminUnavailable") : failed ? t("core.metricsUnavailable") : t("system.loading")}</p>}
      </section>
    </div>
  </section>;
}
