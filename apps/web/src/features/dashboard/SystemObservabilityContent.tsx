import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  AdminClient,
  type OperatorMetricsRange,
  type OperatorMetricsSummary,
  type RequestMetricBucket,
  type SandboxNode,
} from "@agents-core-web/agents-client";

import { isLocalProxyBaseUrl } from "../../lib/connection";
import { formatDashboardBytes } from "./dashboard-model";
import { SystemRequestChart } from "./SystemRequestChart";
import { CoreServiceMetricsContent } from "./CoreServiceMetricsContent";

const latencyBounds = [10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000] as const;

function percentile95(rows: readonly RequestMetricBucket[]): number | null {
  const counts = Array.from({ length: 11 }, (_, index) => rows.reduce((sum, row) => sum + (row.latency_bucket_counts[index] ?? 0), 0));
  const total = counts.reduce((sum, count) => sum + count, 0);
  if (total === 0) return null;
  const target = Math.ceil(total * .95);
  let current = 0;
  for (let index = 0; index < counts.length; index += 1) {
    current += counts[index]!;
    if (current >= target) return latencyBounds[index] ?? 10000;
  }
  return null;
}

interface RouteSummary {
  family: string;
  count: number;
  errors: number;
  latency: number | null;
}

function routeSummaries(rows: readonly RequestMetricBucket[]): RouteSummary[] {
  const families = new Map<string, RequestMetricBucket[]>();
  for (const row of rows) {
    const previous = families.get(row.route_family) ?? [];
    previous.push(row);
    families.set(row.route_family, previous);
  }
  return [...families].map(([family, group]) => ({
    family,
    count: group.reduce((sum, row) => sum + row.count, 0),
    errors: group.filter((row) => row.outcome === "server_error").reduce((sum, row) => sum + row.count, 0),
    latency: percentile95(group),
  })).sort((left, right) => right.count - left.count);
}

export function hasCompleteRequestCoverage(summary: OperatorMetricsSummary | null, expectedBuckets: number): boolean {
  if (summary === null) return false;
  const requestCollector = summary.collector.filter((row) => row.source === "request");
  return new Set(requestCollector.map((row) => row.start)).size >= expectedBuckets &&
    requestCollector.every((row) => row.dropped_count === 0 && row.export_failed_count === 0);
}

export function SystemObservabilityContent({ coreBaseUrl, nodes }: { coreBaseUrl: string; nodes: SandboxNode[] | null }) {
  const { t, i18n } = useTranslation("dashboard");
  const locale = i18n.resolvedLanguage;
  const [range, setRange] = useState<OperatorMetricsRange>("1h");
  const [refresh, setRefresh] = useState(0);
  const [summary, setSummary] = useState<OperatorMetricsSummary | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "unavailable" | "failed">("loading");
  useEffect(() => {
    const timer = window.setInterval(() => setRefresh((value) => value + 1), 30_000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    if (!isLocalProxyBaseUrl(coreBaseUrl)) {
      setSummary(null);
      setState("unavailable");
      return () => controller.abort();
    }
    setState("loading");
    setSummary(null);
    void (async () => {
      const metricsClient = new AdminClient();
      const metrics = await metricsClient.retrieveObservability(range, { signal: controller.signal });
      if (controller.signal.aborted) return;
      setSummary(metrics);
      setState("ready");
    })().catch(() => { if (!controller.signal.aborted) setState("failed"); });
    return () => controller.abort();
  }, [coreBaseUrl, range, refresh]);

  const routes = useMemo(() => routeSummaries(summary?.requests ?? []), [summary]);
  const total = routes.reduce((sum, row) => sum + row.count, 0);
  const serverErrors = routes.reduce((sum, row) => sum + row.errors, 0);
  const p95 = percentile95(summary?.requests ?? []);
  const durationMinutes = range === "1h" ? 60 : range === "6h" ? 360 : 1440;
  const expectedBuckets = durationMinutes / ((summary?.step_seconds ?? 60) / 60);
  const requestCollector = (summary?.collector ?? []).filter((row) => row.source === "request");
  const heartbeatBuckets = new Set(requestCollector.map((row) => row.start)).size;
  const completeRequestCoverage = hasCompleteRequestCoverage(summary, expectedBuckets);
  const requestRate = completeRequestCoverage ? total / durationMinutes : null;
  const errorRate = completeRequestCoverage && total > 0 ? serverErrors / total * 100 : null;
  const collector = summary?.collector ?? [];
  const dropped = collector.reduce((sum, row) => sum + row.dropped_count + row.export_failed_count, 0);
  const onlineNodes = nodes?.filter((node) => node.online && node.provider_ready).length;
  const nodeCapacity = nodes?.reduce((sum, node) => sum + node.max_active, 0);
  const number = (value: number) => value.toLocaleString(locale);
  const unavailable = t("system.unavailableValue");

  return <section className="dashboard-system" aria-labelledby="dashboard-system-heading">
    <header className="dashboard-system-header">
      <div><h2 id="dashboard-system-heading">{t("system.title")}</h2><p>{t("system.subtitle")}</p></div>
      <div className="dashboard-system-controls" role="group" aria-label={t("system.range")}>
        {(["1h", "6h", "24h"] as const).map((option) => <button key={option} type="button" aria-pressed={range === option} onClick={() => setRange(option)}>{option}</button>)}
      </div>
    </header>
    <p className="dashboard-system-source" role="status">{state === "loading" ? t("system.loading") : state === "failed" ? t("system.stale") : state === "unavailable" ? t("system.adminUnavailable") : t("system.source")}</p>
    <div className="dashboard-system-metrics">
      <div><small>{t("system.requestRate")}</small><strong>{requestRate === null ? unavailable : `${number(Math.round(requestRate))}/min`}</strong><span>{t("system.requestCount", { count: total })}</span></div>
      <div><small>{t("system.serverErrorRate")}</small><strong>{errorRate === null ? unavailable : `${errorRate.toFixed(1)}%`}</strong><span>{t("system.serverErrorCount", { count: serverErrors })}</span></div>
      <div><small>{t("system.latencyP95")}</small><strong>{!completeRequestCoverage || p95 === null ? unavailable : p95 === 10000 ? ">10 s" : `${number(p95)} ms`}</strong><span>{t("system.histogramEstimate")}</span></div>
      <div><small>{t("system.collectorCoverage")}</small><strong>{summary === null ? unavailable : `${number(heartbeatBuckets)}/${number(expectedBuckets)}`}</strong><span>{t("system.collectorLoss", { count: dropped })}</span></div>
      <div><small>{t("system.nodes")}</small><strong>{nodes === null ? unavailable : `${number(onlineNodes ?? 0)}/${number(nodes.length)}`}</strong><span>{nodeCapacity === undefined ? unavailable : t("system.nodeCapacity", { count: nodeCapacity })}</span></div>
    </div>
    <SystemRequestChart summary={summary} />
    <div className="dashboard-system-tables">
      <section><h3>{t("system.routeTable")}</h3><p>{t("system.coveredBuckets", { covered: heartbeatBuckets, total: expectedBuckets })}</p>
        {routes.length ? <div className="dashboard-system-table-scroll"><table><thead><tr><th>{t("system.route")}</th><th>{t("system.requests")}</th><th>{t("system.errors")}</th><th>{t("system.p95")}</th></tr></thead><tbody>{routes.map((row) => <tr key={row.family}><th>{row.family}</th><td>{number(row.count)}</td><td>{number(row.errors)}</td><td>{row.latency === null ? unavailable : row.latency === 10000 ? ">10 s" : `${number(row.latency)} ms`}</td></tr>)}</tbody></table></div> : <p>{t("system.noRequestSamples")}</p>}
      </section>
      <section><h3>{t("system.collectorTable")}</h3>
        {collector.length ? <div className="dashboard-system-table-scroll"><table><thead><tr><th>{t("system.sourceName")}</th><th>{t("system.attempted")}</th><th>{t("system.observed")}</th><th>{t("system.lost")}</th></tr></thead><tbody>{[...new Set(collector.map((row) => row.source))].map((source) => { const rows = collector.filter((row) => row.source === source); return <tr key={source}><th>{source}</th><td>{number(rows.reduce((sum, row) => sum + row.attempted_count, 0))}</td><td>{number(rows.reduce((sum, row) => sum + row.observed_count, 0))}</td><td>{number(rows.reduce((sum, row) => sum + row.dropped_count + row.export_failed_count, 0))}</td></tr>; })}</tbody></table></div> : <p>{t("system.noCollectorSamples")}</p>}
      </section>
      <section className="dashboard-system-nodes"><h3>{t("system.turnTable")}</h3>
        {summary?.turns.length ? <div className="dashboard-system-table-scroll"><table><thead><tr><th>{t("system.turnStatus")}</th><th>{t("system.turnCount")}</th><th>{t("system.queueP95")}</th><th>{t("system.executionP95")}</th></tr></thead><tbody>{[...new Set(summary.turns.map((row) => row.status))].map((status) => { const rows = summary.turns.filter((row) => row.status === status); const count = rows.reduce((sum, row) => sum + row.count, 0); const queue = rows.reduce<number[]>((values, row) => row.queue_p95_ms === null ? values : [...values, row.queue_p95_ms], []); const execution = rows.reduce<number[]>((values, row) => row.execution_p95_ms === null ? values : [...values, row.execution_p95_ms], []); return <tr key={status}><th>{status}</th><td>{number(count)}</td><td>{queue.length ? `${number(Math.round(Math.max(...queue)))} ms` : unavailable}</td><td>{execution.length ? `${number(Math.round(Math.max(...execution)))} ms` : unavailable}</td></tr>; })}</tbody></table></div> : <p>{t("system.noTurns")}</p>}
      </section>
      <section><h3>{t("system.toolTable")}</h3>
        {summary?.tools.length ? <div className="dashboard-system-table-scroll"><table><thead><tr><th>{t("system.toolCategory")}</th><th>{t("system.toolOutcome")}</th><th>{t("system.toolCount")}</th><th>{t("system.toolTiming")}</th></tr></thead><tbody>{summary.tools.map((row, index) => <tr key={`${row.start}:${row.category}:${row.outcome}:${index}`}><th>{row.category}</th><td>{row.outcome}</td><td>{number(row.count)}</td><td>{row.duration_p95_ms === null ? unavailable : `${number(Math.round(row.duration_p95_ms))} ms · ${number(row.timed_count)}/${number(row.count)}`}</td></tr>)}</tbody></table></div> : <p>{t("system.noToolAttempts")}</p>}
      </section>
      <section><h3>{t("system.modelTable")}</h3><p>{t("system.modelUnavailable")}</p></section>
      <section className="dashboard-system-nodes"><h3>{t("system.nodeTable")}</h3>
        {nodes?.length ? <div className="dashboard-system-table-scroll"><table><thead><tr><th>{t("system.node")}</th><th>{t("system.status")}</th><th>{t("system.active")}</th><th>{t("system.retained")}</th><th>{t("system.cores")}</th><th>{t("system.availableMemory")}</th></tr></thead><tbody>{nodes.map((node) => <tr key={node.id}><th>{node.name}</th><td>{t(node.online && node.provider_ready ? "system.online" : "system.offline")}</td><td>{number(node.active)} / {number(node.max_active)}</td><td>{number(node.retained)}</td><td>{node.cpu_count === null ? unavailable : number(node.cpu_count)}</td><td>{node.available_memory_bytes === null ? unavailable : formatDashboardBytes(node.available_memory_bytes)}</td></tr>)}</tbody></table></div> : <p>{nodes === null ? t("system.nodeUnavailable") : t("system.noNodes")}</p>}
      </section>
    </div>
    <CoreServiceMetricsContent coreBaseUrl={coreBaseUrl} range={range} refresh={refresh} />
  </section>;
}
