import { AlertTriangle } from "lucide-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import { LiveNumber } from "../../components/live-number";
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
import { DashboardSkeleton, TableSkeleton } from "../../components/Skeleton";
import { useFailureToast } from "../../components/Toast";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatClock, formatCompact, formatDuration, formatInteger, formatPercent, formatRelative, MISSING } from "../../lib/format";
import { ProjectFilter, ProjectName, useProjects, type ProjectFilterValue } from "../../lib/projects";
import {
  agentMetricsOutcome,
  INLINE_AGENT_ID,
  isInlineAgent,
  OTHER_SERIES_ID,
  type AgentMetrics,
  type AgentMetricsRange,
  type MetricsWindow,
  type NamedSeries,
  type ToolBreakdown,
} from "./agent-metrics";
import { agentMetricsQuery, keyUsageQuery, type LoadedAgentMetrics } from "./metrics-queries";
import { type ProjectReadFailure } from "./project-sessions";
import { type KeyUsageRow } from "./key-usage";
import { useStableColors } from "./use-stable-colors";
import "./MetricsView.css";
import { type KeyRef, type Project, type ProjectSummary } from "../../lib/admin-view";

const RANGES: readonly AgentMetricsRange[] = ["1h", "6h", "24h", "7d"];

type Loaded = LoadedAgentMetrics;

type LoadState =
  | { status: "loading"; previous: Loaded | null }
  | { status: "ready"; loaded: Loaded }
  | { status: "failed"; error: string; previous: Loaded | null };

type KeyUsageState =
  | { status: "loading" }
  | { status: "ready"; rows: KeyUsageRow[] }
  | { status: "failed"; error: string };

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function bucketLabel(seconds: number, t: TFunction<"metrics">): string {
  return seconds >= 3_600 ? t("bucket.hours", { count: seconds / 3_600 }) : t("bucket.minutes", { count: seconds / 60 });
}

/**
 * Agent metrics and usage by key through the query cache. A changed project
 * filter or range keeps the last figures on screen until the new ones arrive.
 */
function useAgentMetricsData(projects: readonly Project[], projectsReady: boolean, filter: ProjectFilterValue, range: AgentMetricsRange) {
  const targets = useMemo(() => projects.filter((project) => !filter || project.id === filter), [filter, projects]);
  const metricsQuery = useQuery({ ...agentMetricsQuery(targets, filter, range), enabled: projectsReady, placeholderData: keepPreviousData });
  const keyQuery = useQuery({ ...keyUsageQuery(filter, range), enabled: projectsReady, placeholderData: keepPreviousData });

  const loaded = metricsQuery.data ?? null;
  let state: LoadState;
  if (metricsQuery.isError && !metricsQuery.isFetching) state = { status: "failed", error: errorText(metricsQuery.error), previous: loaded };
  else if (loaded && !metricsQuery.isFetching) state = { status: "ready", loaded };
  else state = { status: "loading", previous: loaded };

  let keyUsage: KeyUsageState;
  if (keyQuery.isError && !keyQuery.isFetching) keyUsage = { status: "failed", error: errorText(keyQuery.error) };
  else if (keyQuery.data) keyUsage = { status: "ready", rows: keyQuery.data };
  else keyUsage = { status: "loading" };

  const { refetch: refetchMetrics } = metricsQuery;
  const { refetch: refetchKeys } = keyQuery;
  const refresh = useCallback(() => { void refetchMetrics(); void refetchKeys(); }, [refetchKeys, refetchMetrics]);
  return { state, keyUsage, refreshing: metricsQuery.isFetching || keyQuery.isFetching, refresh };
}

export function AgentMetricsPage() {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { state: projectsState, byId } = useProjects();
  const [filter, setFilter] = useState<ProjectFilterValue>("");
  const [range, setRange] = useState<AgentMetricsRange>("24h");
  const projects = projectsState.projects;
  const projectsReady = projectsState.status !== "loading" || projects.length > 0;
  const { state, keyUsage, refreshing, refresh } = useAgentMetricsData(projects, projectsReady, filter, range);

  const loaded = state.status === "ready" ? state.loaded : state.previous;
  const metrics = loaded?.metrics ?? null;
  const partial = loaded ? coverageNote(loaded, t) : null;

  // Both model charts share one ranked set, so the requests series carries every coloured model.
  const modelColor = useStableColors(metrics?.series.requestsByModel.map((entry) => entry.id) ?? []);
  const toolColor = useStableColors(metrics?.series.callsByTool.map((entry) => entry.id) ?? []);

  return (
    <section className="page-section console-page metrics-page" aria-labelledby="agent-metrics-heading">
      <PageHeader
        headingId="agent-metrics-heading"
        title={t("agent.title")}
        help={<>{t("agent.description")}<br />{t("coverage.method")}</>}
        actions={<>
          {metrics && partial ? (
            <span className="partial-chip">
              <StatusDot tone="warning" label={t("coverage.partial")} />
              <HelpTip>{t("coverage.summary", { sessions: metrics.coverage.loadedSessions, range: t(`range.${metrics.window.range}`) })} {partial}</HelpTip>
            </span>
          ) : null}
          <ProjectFilter value={filter} onChange={setFilter} />
          <SegmentedControl label={t("range.label")} value={range} options={RANGES.map((value) => ({ value, label: t(`range.${value}`) }))} onChange={setRange} />
          <RefreshButton refreshing={refreshing} updatedAt={loaded ? formatClock(loaded.loadedAt, locale) : null} onClick={refresh} />
        </>}
      />
      <PageBody>
        {projectsState.status === "failed" && !projects.length ? (
          <EmptyState icon={AlertTriangle} title={t("agent.projectsFailed")} description={projectsState.error} />
        ) : !metrics ? (
          state.status === "failed"
            ? <p className="page-status" role="alert">{t("agent.loadFailed", { reason: state.error })}</p>
            : <DashboardSkeleton label={t("agent.loading")} />
        ) : (
          <AgentMetricsContent
            metrics={metrics}
            listFailures={loaded?.listFailures ?? []}
            showProject={!filter}
            projectOf={(id) => (id ? byId.get(id) : undefined)}
            modelColor={modelColor}
            toolColor={toolColor}
            failure={state.status === "failed" ? state.error : null}
          />
        )}
        {metrics ? <KeyUsageSection state={keyUsage} window={metrics.window} showProject={!filter} projectOf={(id) => byId.get(id)} /> : null}
      </PageBody>
    </section>
  );
}

function coverageNote(loaded: Loaded, t: TFunction<"metrics">): string | null {
  const { coverage } = loaded.metrics;
  const parts: string[] = [];
  if (loaded.truncatedLists.length) parts.push(t("coverage.listTruncated", { names: loaded.truncatedLists.map((project) => project.name).join(", ") }));
  if (loaded.unrecognizedSessions) parts.push(t("coverage.unrecognized", { count: loaded.unrecognizedSessions }));
  if (coverage.skippedSessions) parts.push(t("coverage.skipped", { loaded: coverage.loadedSessions, total: coverage.candidateSessions }));
  if (coverage.truncatedSessions) parts.push(t("coverage.truncated", { count: coverage.truncatedSessions }));
  if (coverage.failedSessions) parts.push(t("coverage.failed", { count: coverage.failedSessions }));
  if (coverage.itemFailedSessions) parts.push(t("coverage.itemsFailed", { count: coverage.itemFailedSessions }));
  return parts.length ? parts.join(" ") : null;
}

function AgentMetricsContent({
  metrics,
  listFailures,
  showProject,
  projectOf,
  modelColor,
  toolColor,
  failure,
}: {
  metrics: AgentMetrics;
  listFailures: ProjectReadFailure[];
  showProject: boolean;
  projectOf: (id: string | null) => Project | undefined;
  modelColor: (id: string) => string;
  toolColor: (id: string) => string;
  failure: string | null;
}) {
  const { t, i18n } = useTranslation("metrics");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
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
  useFailureToast(Boolean(failure), t("agent.refreshFailed", { reason: failure ?? "", range: t(`range.${window.range}`) }), "agent-metrics-refresh");
  useFailureToast(listFailures.length > 0, tCommon("project.partial", { names: listFailures.map((entry) => entry.project.name).join(", ") }), "agent-metrics-partial");

  const outcome = agentMetricsOutcome(metrics, listFailures.length);
  if (outcome === "failed") {
    return <EmptyState icon={AlertTriangle} title={t("agent.readsFailedTitle")} description={t("agent.readsFailedDescription")} />;
  }
  const failedReads = metrics.coverage.failedSessions
    ? <p className="coverage-note coverage-note-error" role="alert">{t("coverage.failed", { count: metrics.coverage.failedSessions })}</p>
    : null;
  if (outcome === "empty") {
    return (
      <>
        {failedReads}
        <EmptyState title={t("agent.emptyTitle")} hint={t("agent.emptyDescription", { range: t(`range.${window.range}`) })} />
      </>
    );
  }
  const tokensKnown = totals.tokens.reportedTurns > 0;

  return (
    <>
      {failedReads}
      <KpiStrip label={t("agent.kpiLabel")}>
        <Kpi label={t("agent.requests")} value={<LiveNumber value={totals.requests} />} help={t("agent.requestsDetail", { completed: integer(totals.completed), unfinished: integer(totals.unfinished) })} />
        <Kpi
          label={t("agent.errorRate")}
          value={<LiveNumber value={totals.errorRate} format="percent" />}
          tone={totals.errorRate === null ? undefined : totals.errorRate >= 0.05 ? "danger" : totals.errorRate > 0 ? "warning" : "ok"}
          help={<>{t("agent.errorDetail", { failed: integer(totals.failed), cancelled: integer(totals.cancelled) })}<br />{t("agent.errorFormula")}</>}
        />
        <Kpi label={t("agent.latency")} value={formatDuration(totals.averageLatencySeconds)} help={t("agent.queueDetail", { value: formatDuration(totals.averageQueueSeconds) })} />
        <Kpi label={t("agent.p95")} value={formatDuration(totals.p95LatencySeconds)} help={t("agent.p95Detail")} />
        <Kpi
          label={t("agent.tokens")}
          value={tokensKnown ? <LiveNumber value={totals.tokens.total} format="compact" /> : MISSING}
          help={tokensKnown
            ? <>{t("agent.tokenSplit", { input: compact(totals.tokens.input), output: compact(totals.tokens.output) })}<br />{t("agent.tokenCoverage", { reported: integer(totals.tokens.reportedTurns), total: integer(totals.requests) })}</>
            : t("agent.tokensUnreported")}
        />
        <Kpi
          label={t("agent.toolCalls")}
          value={<LiveNumber value={totals.toolCalls} />}
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
              counts
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
              counts
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
              counts
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
                counts
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
                {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
                <th scope="col" className="numeric">{t("agent.sessions")}</th>
                <th scope="col" className="numeric">{t("agent.requests")}</th>
                <th scope="col" className="numeric"><span className="column-help">{t("agent.failed")}<HelpTip>{t("agent.failedHelp")}</HelpTip></span></th>
                <th scope="col" className="numeric">{t("agent.errorRate")}</th>
                <th scope="col" className="numeric">{t("agent.latency")}</th>
                <th scope="col" className="numeric">{t("agent.tokens")}</th>
              </tr>
            </thead>
            <tbody>
              {metrics.byAgent.map((agent) => {
                const label = agentLabel(agent.agentId, agent.label);
                // A saved Agent opens its page, and its failures its Sessions in the Session log; an inline Agent has neither.
                const saved = agent.projectId !== null && !isInlineAgent(agent.agentId) ? { project: agent.projectId, id: agent.agentId } : null;
                // The figure counts failed Turns in the range; the link lists all the Agent's Sessions, and its name and tooltip say so.
                const openSessions = t("agent.openSessions", { count: agent.failed, agent: label });
                return (
                  <tr key={agent.id}>
                    <th scope="row" title={agent.agentId}>
                      {saved ? (
                        <button type="button" className="name-cell-link table-primary" aria-label={t("agent.openAgent", { name: label })} onClick={() => navigate("agents", saved)}>{label}</button>
                      ) : <span className="table-primary">{label}</span>}
                    </th>
                    {showProject ? <td><ProjectName project={projectOf(agent.projectId)} /></td> : null}
                    <td className="numeric">{integer(agent.sessions)}</td>
                    <td className="numeric">{integer(agent.requests)}</td>
                    <td className={agent.failed ? "numeric numeric-danger" : "numeric"}>
                      {saved && agent.failed ? (
                        <button type="button" className="figure-link" aria-label={openSessions} title={openSessions} onClick={() => navigate("sessions", saved, "agent-sessions")}>{integer(agent.failed)}</button>
                      ) : integer(agent.failed)}
                    </td>
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

function keyLabel(key: KeyRef | null, t: TFunction<"metrics">): string {
  if (!key) return t("keys.unknown");
  return key.name ?? (key.prefix ? `${key.prefix}…` : t("keys.unknown"));
}

/** Usage by the API key that created each Session, from Core's summary for the range. */
function KeyUsageSection({ state, window, showProject, projectOf }: { state: KeyUsageState; window: MetricsWindow; showProject: boolean; projectOf: (id: string) => Project | undefined }) {
  const { t, i18n } = useTranslation("metrics");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1000);
  return (
    <Section headingId="keys-heading" title={t("keys.title")} help={t("keys.help", { range: t(`range.${window.range}`) })}>
      {state.status === "loading" ? <TableSkeleton label={t("keys.loading")} rows={3} columns={showProject ? 8 : 7} />
        : state.status === "failed" ? <p className="coverage-note coverage-note-error" role="alert">{t("keys.loadFailed", { reason: state.error })}</p>
          : !state.rows.length ? <p className="page-status">{t("keys.empty", { range: t(`range.${window.range}`) })}</p> : (
            <div className="table-frame">
              <table className="data-table">
                <thead>
                  <tr>
                    <th scope="col">{t("keys.key")}</th>
                    {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
                    <th scope="col" className="numeric">{t("keys.sessions")}</th>
                    <th scope="col" className="numeric">{t("keys.running")}</th>
                    <th scope="col" className="numeric">{t("keys.failed")}</th>
                    <th scope="col" className="numeric">{t("keys.tokens")}</th>
                    <th scope="col" className="numeric">{t("keys.coverage")}</th>
                    <th scope="col" className="numeric">{t("keys.lastActive")}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.rows.map((row) => <KeyUsageTableRow key={row.id} row={row.summary} showProject={showProject} project={projectOf(row.summary.project_id)} now={now} locale={locale} />)}
                </tbody>
              </table>
            </div>
          )}
    </Section>
  );
}

function KeyUsageTableRow({ row, showProject, project, now, locale }: { row: ProjectSummary; showProject: boolean; project: Project | undefined; now: number; locale: string | undefined }) {
  const { t } = useTranslation("metrics");
  const key = row.key;
  return (
    <tr>
      <th scope="row">
        <span className={key ? "table-primary" : "table-primary table-muted"} title={key?.prefix ? `${key.prefix}…` : t("keys.unknownHelp")}>{keyLabel(key, t)}</span>
        {key?.revoked_at ? <span className="pill">{t("keys.revoked")}</span> : null}
      </th>
      {showProject ? <td><ProjectName project={project} /></td> : null}
      <td className="numeric">{formatInteger(row.sessions.total, locale)}</td>
      <td className="numeric">{formatInteger(row.sessions.in_progress, locale)}</td>
      <td className={row.sessions.failed ? "numeric numeric-danger" : "numeric"}>{formatInteger(row.sessions.failed, locale)}</td>
      <td className="numeric">{row.usage ? formatCompact(row.usage.total_tokens, locale) : MISSING}</td>
      <td className="numeric" title={row.coverage.sessions ? t("keys.coverageDetail", { reported: row.coverage.reported, total: row.coverage.sessions }) : undefined}>
        {formatPercent(row.coverage.sessions ? row.coverage.reported / row.coverage.sessions : null, locale)}
      </td>
      <td className="numeric">{formatRelative(row.last_active_at, now, locale)}</td>
    </tr>
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
