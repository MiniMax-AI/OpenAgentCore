import { AlertTriangle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import type { AgentSession } from "@agents-core-web/agents-client";

import { TimeSeriesChart, type TimeSeries } from "../../components/charts/TimeSeriesChart";
import {
  EmptyState,
  HelpTip,
  Kpi,
  KpiStrip,
  PageBody,
  PageHeader,
  RefreshButton,
  Section,
  SegmentedControl,
  StatusDot,
} from "../../components/console-ui";
import type { CoreConnectionState } from "../../lib/connection";
import { formatClock, formatCompact, formatDuration, formatInteger, formatPercent, MISSING } from "../../lib/format";
import {
  aggregateAgentMetrics,
  agentMetricsOutcome,
  INLINE_AGENT_ID,
  metricsWindow,
  OTHER_SERIES_ID,
  type AgentMetrics,
  type AgentMetricsRange,
  type NamedSeries,
  type ToolBreakdown,
} from "./agent-metrics";
import { loadAgentMetricsActivity, type AgentMetricsSource } from "./agent-metrics-loader";
import { useStableColors } from "./use-stable-colors";
import "./MetricsView.css";

const RANGES: readonly AgentMetricsRange[] = ["1h", "6h", "24h", "7d"];

type LoadState =
  | { status: "loading"; previous: AgentMetrics | null }
  | { status: "ready"; metrics: AgentMetrics; loadedAt: number }
  | { status: "failed"; error: string; previous: AgentMetrics | null };

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function bucketLabel(seconds: number, t: TFunction<"metrics">): string {
  return seconds >= 3_600 ? t("bucket.hours", { count: seconds / 3_600 }) : t("bucket.minutes", { count: seconds / 60 });
}

export function AgentMetricsView({
  source,
  sessions,
  sessionsState,
  sessionsError,
  onRefreshSessions,
}: {
  source: AgentMetricsSource;
  sessions: readonly AgentSession[];
  sessionsState: CoreConnectionState;
  sessionsError: string | null;
  onRefreshSessions: () => void;
}) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const [range, setRange] = useState<AgentMetricsRange>("24h");
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<LoadState>({ status: "loading", previous: null });
  const latest = useRef<AgentMetrics | null>(null);
  // The Session list refreshes every 30s; read it at load time instead of reloading on each change.
  const sessionsRef = useRef(sessions);
  sessionsRef.current = sessions;
  const sessionsReady = sessionsState === "ready" || sessions.length > 0;

  useEffect(() => {
    if (!sessionsReady) return;
    const controller = new AbortController();
    const window = metricsWindow(range, Math.floor(Date.now() / 1000));
    setState({ status: "loading", previous: latest.current });
    void loadAgentMetricsActivity(source, sessionsRef.current, window, controller.signal, { includeTools: true })
      .then((load) => {
        if (controller.signal.aborted) return;
        const metrics = aggregateAgentMetrics(window, load.activities, load.coverage);
        latest.current = metrics;
        setState({ status: "ready", metrics, loadedAt: Date.now() });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setState({ status: "failed", error: errorText(error), previous: latest.current });
      });
    return () => controller.abort();
  }, [range, revision, source, sessionsReady]);

  const metrics = state.status === "ready" ? state.metrics : state.previous;
  const loading = state.status === "loading";

  // Both model charts share one ranked set, so the requests series carries every coloured model.
  const modelColor = useStableColors(metrics?.series.requestsByModel.map((entry) => entry.id) ?? []);
  const toolColor = useStableColors(metrics?.series.callsByTool.map((entry) => entry.id) ?? []);

  const refresh = () => {
    onRefreshSessions();
    setRevision((value) => value + 1);
  };

  return (
    <section className="page-section console-page metrics-page" aria-labelledby="agent-metrics-heading">
      <PageHeader
        headingId="agent-metrics-heading"
        title={t("agent.title")}
        help={<>{t("agent.description")}<br />{t("coverage.method")}</>}
        actions={<>
          {metrics && coverageNote(metrics, t) ? (
            <span className="partial-chip">
              <StatusDot tone="warning" label={t("coverage.partial")} />
              <HelpTip>{t("coverage.summary", { sessions: metrics.coverage.loadedSessions, range: t(`range.${metrics.window.range}`) })} {coverageNote(metrics, t)}</HelpTip>
            </span>
          ) : null}
          <SegmentedControl label={t("range.label")} value={range} options={RANGES.map((value) => ({ value, label: t(`range.${value}`) }))} onChange={setRange} />
          <RefreshButton refreshing={loading} updatedAt={state.status === "ready" ? formatClock(state.loadedAt, locale) : null} onClick={refresh} />
        </>}
      />
      <PageBody className={loading && metrics ? "is-refetching" : undefined}>
        {sessionsState === "failed" && !sessions.length ? (
          <EmptyState icon={AlertTriangle} title={t("agent.sessionsFailed")} description={sessionsError ?? undefined} />
        ) : !metrics ? (
          <p className="page-status" role="status">{state.status === "failed" ? t("agent.loadFailed", { reason: state.error }) : t("agent.loading")}</p>
        ) : (
          <AgentMetricsContent metrics={metrics} modelColor={modelColor} toolColor={toolColor} failure={state.status === "failed" ? state.error : null} />
        )}
      </PageBody>
    </section>
  );
}

function coverageNote(metrics: AgentMetrics, t: TFunction<"metrics">): string | null {
  const { coverage } = metrics;
  const parts: string[] = [];
  if (coverage.skippedSessions) parts.push(t("coverage.skipped", { loaded: coverage.loadedSessions, total: coverage.candidateSessions }));
  if (coverage.truncatedSessions) parts.push(t("coverage.truncated", { count: coverage.truncatedSessions }));
  if (coverage.failedSessions) parts.push(t("coverage.failed", { count: coverage.failedSessions }));
  if (coverage.itemFailedSessions) parts.push(t("coverage.itemsFailed", { count: coverage.itemFailedSessions }));
  return parts.length ? parts.join(" ") : null;
}

function AgentMetricsContent({
  metrics,
  modelColor,
  toolColor,
  failure,
}: {
  metrics: AgentMetrics;
  modelColor: (id: string) => string;
  toolColor: (id: string) => string;
  failure: string | null;
}) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { window, totals, series } = metrics;
  const bucket = bucketLabel(window.bucketSeconds, t);
  const modelLabel = (id: string) => (id === OTHER_SERIES_ID ? t("chart.other") : id === "unknown" ? t("agent.unknownModel") : id);
  const agentLabel = (id: string, label: string) => (id === INLINE_AGENT_ID ? t("agent.inlineAgent") : label);
  const toolLabel = (id: string, tool?: Pick<ToolBreakdown, "kind" | "name">) => {
    if (id === OTHER_SERIES_ID) return t("chart.other");
    const kind = tool?.kind ?? (id === "command" || id === "web_search" || id === "subagent" ? id : id.startsWith("mcp:") ? "mcp" : "function");
    if (kind === "command") return t("tools.command");
    if (kind === "web_search") return t("tools.webSearch");
    if (kind === "subagent") return t("tools.subagent");
    return tool?.name ?? id.replace(/^(function|mcp):/, "");
  };
  const integer = (value: number) => formatInteger(value, locale);
  const compact = (value: number) => formatCompact(value, locale);
  const named = (entries: NamedSeries[], label: (id: string) => string, color: (id: string) => string, totals?: ReadonlyMap<string, string>): TimeSeries[] =>
    entries.map((entry) => ({ id: entry.id, label: label(entry.id), color: color(entry.id), values: entry.values, total: totals?.get(entry.id) }));

  const ok = series.requests.map((value, index) => value - (series.failed[index] ?? 0));
  const tokenItems = foldBreakdown(metrics.byModel.map((entry) => ({ id: entry.id, value: entry.tokens })), series.tokensByModel);
  const requestItems = foldBreakdown(metrics.byModel.map((entry) => ({ id: entry.id, value: entry.requests })), series.requestsByModel);
  const toolItems = metrics.byTool ? foldBreakdown(metrics.byTool.map((entry) => ({ id: entry.id, value: entry.calls })), series.callsByTool) : null;
  const toolIndex = new Map((metrics.byTool ?? []).map((tool) => [tool.id, tool]));

  const outcome = agentMetricsOutcome(metrics);
  if (outcome === "failed") {
    return <EmptyState icon={AlertTriangle} title={t("agent.readsFailedTitle")} description={t("agent.readsFailedDescription", { count: metrics.coverage.failedSessions })} />;
  }
  const failedReads = metrics.coverage.failedSessions
    ? <p className="coverage-note coverage-note-error" role="alert">{t("coverage.failed", { count: metrics.coverage.failedSessions })}</p>
    : null;
  if (outcome === "empty") {
    return (
      <>
        {failedReads}
        <EmptyState title={t("agent.emptyTitle")} description={t("agent.emptyDescription", { range: t(`range.${window.range}`) })} />
      </>
    );
  }
  const tokensKnown = totals.tokens.reportedTurns > 0;

  return (
    <>
      {failure ? <p className="coverage-note coverage-note-error" role="alert">{t("agent.refreshFailed", { reason: failure, range: t(`range.${window.range}`) })}</p> : null}
      {failedReads}
      <KpiStrip label={t("agent.kpiLabel")}>
        <Kpi label={t("agent.requests")} value={integer(totals.requests)} help={t("agent.requestsDetail", { completed: integer(totals.completed), unfinished: integer(totals.unfinished) })} />
        <Kpi
          label={t("agent.errorRate")}
          value={formatPercent(totals.errorRate, locale)}
          tone={totals.errorRate === null ? undefined : totals.errorRate >= 0.05 ? "danger" : totals.errorRate > 0 ? "warning" : "ok"}
          help={<>{t("agent.errorDetail", { failed: integer(totals.failed), cancelled: integer(totals.cancelled) })}<br />{t("agent.errorFormula")}</>}
        />
        <Kpi label={t("agent.latency")} value={formatDuration(totals.averageLatencySeconds)} help={t("agent.queueDetail", { value: formatDuration(totals.averageQueueSeconds) })} />
        <Kpi label={t("agent.p95")} value={formatDuration(totals.p95LatencySeconds)} help={t("agent.p95Detail")} />
        <Kpi
          label={t("agent.tokens")}
          value={tokensKnown ? compact(totals.tokens.total) : MISSING}
          help={tokensKnown
            ? <>{t("agent.tokenSplit", { input: compact(totals.tokens.input), output: compact(totals.tokens.output) })}<br />{t("agent.tokenCoverage", { reported: integer(totals.tokens.reportedTurns), total: integer(totals.requests) })}</>
            : t("agent.tokensUnreported")}
        />
        <Kpi
          label={t("agent.toolCalls")}
          value={totals.toolCalls === null ? MISSING : integer(totals.toolCalls)}
          help={totals.toolFailures === null ? t("agent.toolsUnavailable") : t("agent.failedCount", { count: totals.toolFailures })}
        />
      </KpiStrip>

      <Section headingId="requests-heading" title={t("agent.requestsSection")} help={t("agent.requestsSectionDetail")}>
        <div className="chart-grid">
          <figure className="chart-panel">
            <figcaption>{t("agent.requestsChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("agent.requestsChart", { bucket })}
              kind="columns"
              stacked
              buckets={window.buckets}
              bucketSeconds={window.bucketSeconds}
              series={[
                { id: "ok", label: t("agent.nonFailed"), color: "var(--series-1)", values: ok },
                { id: "failed", label: t("agent.failed"), color: "var(--danger)", values: series.failed },
              ]}
              tooltipOnly={[{ id: "total", label: t("agent.requests"), color: "var(--fg-muted)", values: series.requests }]}
              formatValue={integer}
              formatAxis={compact}
            />
          </figure>
          <figure className="chart-panel">
            <figcaption>{t("agent.latencyChart", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("agent.latencyChart", { bucket })}
              kind="lines"
              buckets={window.buckets}
              bucketSeconds={window.bucketSeconds}
              series={[
                { id: "average", label: t("agent.latency"), color: "var(--series-1)", values: series.averageLatency },
                { id: "p95", label: t("agent.p95"), color: "var(--series-2)", values: series.p95Latency },
              ]}
              formatValue={formatDuration}
              formatAxis={(value) => (value === 0 ? "0" : formatDuration(value))}
            />
          </figure>
        </div>
      </Section>

      <Section headingId="models-heading" title={t("agent.modelSection")} help={t("agent.modelSectionDetail")}>
        <div className="chart-grid">
          <figure className="chart-panel">
            <figcaption>{t("agent.tokenTrend", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("agent.tokenTrend", { bucket })}
              kind="columns"
              stacked
              buckets={window.buckets}
              bucketSeconds={window.bucketSeconds}
              series={named(series.tokensByModel, modelLabel, modelColor, totalsOf(tokenItems, compact))}
              formatValue={integer}
              formatAxis={compact}
            />
          </figure>
          <figure className="chart-panel">
            <figcaption>{t("agent.modelTrend", { bucket })}</figcaption>
            <TimeSeriesChart
              label={t("agent.modelTrend", { bucket })}
              kind="columns"
              stacked
              buckets={window.buckets}
              bucketSeconds={window.bucketSeconds}
              series={named(series.requestsByModel, modelLabel, modelColor, totalsOf(requestItems, integer))}
              formatValue={integer}
              formatAxis={compact}
            />
          </figure>
        </div>
      </Section>

      <Section headingId="tools-heading" title={t("agent.toolSection")} help={t("agent.toolSectionDetail")}>
        {metrics.coverage.itemFailedSessions ? <p className="coverage-note coverage-note-error" role="alert">{t("coverage.itemsFailed", { count: metrics.coverage.itemFailedSessions })}</p> : null}
        {toolItems && toolItems.length ? (
          <div className="chart-grid chart-grid-single">
            <figure className="chart-panel">
              <figcaption>{t("agent.toolTrend", { bucket })}</figcaption>
              <TimeSeriesChart
                label={t("agent.toolTrend", { bucket })}
                kind="columns"
                stacked
                buckets={window.buckets}
                bucketSeconds={window.bucketSeconds}
                series={named(series.callsByTool, (id) => toolLabel(id, toolIndex.get(id)), toolColor, totalsOf(toolItems, integer))}
                formatValue={integer}
                formatAxis={compact}
              />
            </figure>
          </div>
        ) : <p className="page-status">{toolItems ? t("agent.noToolCalls") : t("agent.toolsUnavailable")}</p>}
      </Section>

      <Section headingId="agents-heading" title={t("agent.agentSection")} help={t("agent.agentSectionDetail")}>
        <div className="table-frame">
          <table className="data-table">
            <thead>
              <tr>
                <th scope="col">{t("agent.agent")}</th>
                <th scope="col" className="numeric">{t("agent.sessions")}</th>
                <th scope="col" className="numeric">{t("agent.requests")}</th>
                <th scope="col" className="numeric">{t("agent.failed")}</th>
                <th scope="col" className="numeric">{t("agent.errorRate")}</th>
                <th scope="col" className="numeric">{t("agent.latency")}</th>
                <th scope="col" className="numeric">{t("agent.tokens")}</th>
              </tr>
            </thead>
            <tbody>
              {metrics.byAgent.map((agent) => {
                return (
                  <tr key={agent.id}>
                    <th scope="row" title={agent.id}><span className="table-primary">{agentLabel(agent.id, agent.label)}</span></th>
                    <td className="numeric">{integer(agent.sessions)}</td>
                    <td className="numeric">{integer(agent.requests)}</td>
                    <td className="numeric">{integer(agent.failed)}</td>
                    <td className="numeric">{formatPercent(agent.finished ? agent.failed / agent.finished : null, locale)}</td>
                    <td className="numeric">{formatDuration(agent.averageLatencySeconds)}</td>
                    <td className="numeric">{agent.reportedTurns ? compact(agent.tokens) : MISSING}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Section>
    </>
  );
}

interface Breakdown {
  id: string;
  value: number;
}

/** Range totals per trend series, formatted for the chart legend. */
function totalsOf(items: readonly Breakdown[], format: (value: number) => string): Map<string, string> {
  return new Map(items.map((item) => [item.id, format(item.value)]));
}

/** Match range totals to a trend: the same top entities plus one "other" entry. */
function foldBreakdown(items: Breakdown[], trend: readonly NamedSeries[]): Breakdown[] {
  const kept = new Set(trend.map((entry) => entry.id));
  const visible = items.filter((item) => kept.has(item.id) && item.value > 0).sort((a, b) => b.value - a.value);
  const rest = items.filter((item) => !kept.has(item.id));
  const restTotal = rest.reduce((sum, item) => sum + item.value, 0);
  return restTotal > 0 ? [...visible, { id: OTHER_SERIES_ID, value: restTotal }] : visible;
}
