import type { AgentSession } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import { InstallationNotice } from "../../components/InstallationNotice";
import { installationQuery } from "../../lib/installation";
import { ReadFailure } from "../../components/ReadFailure";
import { readError } from "../../lib/read-error";
import { failedLast, useFailureToast } from "../../components/Toast";
import { LiveNumber } from "../../components/live-number";
import { TimeSeriesChart } from "../../components/charts/TimeSeriesChart";
import { TableSkeleton } from "../../components/Skeleton";
import {
  EmptyState,
  HelpTip,
  PageBody,
  PageHeader,
  RefreshButton,
  StatusDot,
  type Tone,
} from "../../components/console-ui";
import { NameCell } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatClock, formatCompact, formatInteger, formatPercent, formatRelative, MISSING } from "../../lib/format";
import { ProjectName, useProjects } from "../../lib/projects";
import { capacitySummary, coreStatus, type CoreStatus } from "../fleet/fleet-model";
import { FleetReadNotice, fleetObservationStale } from "../fleet/FleetReadNotice";
import { SandboxResetNotice } from "../fleet/SandboxResetNotice";
import { fleetSnapshot, useSandboxFleet, type FleetSnapshot, type FleetState } from "../fleet/use-sandbox-fleet";
import { type InProject } from "../metrics/project-sessions";
import { FleetOverview, FLEET_LIMIT, type CloudHost } from "./FleetOverview";
import { SessionFailure } from "../sessions/session-diagnostics";
import { useWaitingFor } from "../sessions/SessionStatus";
import { GettingStarted } from "./GettingStarted";
import { type OverviewData } from "./overview-loader";
import { overviewQuery } from "./overview-queries";
import {
  attentionCount,
  attentionSessions,
  coverageRatio,
  projectUsageRows,
  recentFailures,
  serviceHealth,
  sessionActivity,
  summaryTotals,
  webApiReachable,
  type ProjectUsageRow,
  type ServiceHealth,
} from "./overview-model";
import "./overview.css";
import { type Project, type ProjectSummary } from "../../lib/admin-view";

export const OVERVIEW_REFRESH_MS = 30_000;
const ATTENTION_LIMIT = 8;

const serviceTone: Record<ServiceHealth, Tone> = { healthy: "ok", degraded: "warning", down: "danger", unknown: "pending" };
const coreTone: Record<CoreStatus, Tone> = { checking: "pending", running: "ok", unreachable: "danger" };

type LoadState =
  | { status: "loading"; data: OverviewData | null }
  | { status: "ready"; data: OverviewData }
  | { status: "failed"; data: OverviewData | null; error: string };

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * The Overview's reads through the query cache: a revisit opens from the cache,
 * a refresh retains figures within the same project scope, and the
 * page polls while it is visible.
 */
function useOverviewData(projects: readonly Project[], projectsReady: boolean) {
  const query = useQuery({
    ...overviewQuery(projects),
    enabled: projectsReady,
    refetchInterval: OVERVIEW_REFRESH_MS,
    refetchIntervalInBackground: false,
  });
  const data = query.data ?? null;
  let state: LoadState;
  if (query.isError && !query.isFetching) state = { status: "failed", data, error: errorText(query.error) };
  else if (data && !query.isFetching) state = { status: "ready", data };
  else state = { status: "loading", data };

  const { refetch } = query;
  const refresh = useCallback(() => { void refetch(); }, [refetch]);
  // The failure lasts through the polls that retry it, so its toast shows once.
  return { state, refresh, refreshing: query.isFetching, failure: failedLast(query) ? errorText(query.error) : null };
}

export function OverviewPage() {
  const { t, i18n } = useTranslation("overview");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  const projectsState = useProjects();
  const projects = projectsState.state.projects;
  const projectsReady = projectsState.state.status === "ready" || projects.length > 0;
  const projectsFailed = projectsState.state.status === "failed" || projectsState.refreshError !== null;
  const installation = useQuery(installationQuery);
  const { state, refresh, refreshing, failure } = useOverviewData(projects, projectsReady);
  const { state: fleetState, refresh: refreshFleet, deployment } = useSandboxFleet();
  const fleet = fleetSnapshot(fleetState);
  const data = projectsReady ? state.data : null;
  const refreshAll = () => { projectsState.refresh(); if (projectsReady) refresh(); refreshFleet(); void installation.refetch(); };
  const readFailed = projectsFailed || failure !== null;
  const sessionReadsFailed = readFailed || Boolean(data?.sessions.failures.length);
  const now = Math.floor((data?.loadedAt ?? Date.now()) / 1000);

  const summaryRows = data?.summary.rows ?? null;
  const totals = useMemo(() => (summaryRows ? summaryTotals(summaryRows) : null), [summaryRows]);
  const usageRows = useMemo(() => (summaryRows ? projectUsageRows(projects, summaryRows) : null), [projects, summaryRows]);
  const sessions = data && (data.sessions.sessions.length || !sessionReadsFailed) ? data.sessions.sessions : null;
  const activity = useMemo(() => (sessions ? sessionActivity(sessions.map((entry) => entry.value), now) : null), [now, sessions]);
  const attention = useMemo(() => (sessions ? attentionSessions(sessions, ATTENTION_LIMIT) : null), [sessions]);
  const capacity = fleet ? capacitySummary(fleet.nodes) : null;
  // An E2B deployment has no machines: capacity is what Core holds in E2B's cloud.
  const cloud = cloudHost(fleet);
  const failuresLastHour = sessions ? recentFailures(sessions.map((entry) => entry.value), now) : null;

  const summaryError = data?.summary.status === "failed" ? data.summary.error : null;
  const readFailures = data?.sessions.failures ?? [];
  const reachable = webApiReachable([
    projectsFailed ? { status: "failed", error: projectsState.failure } : projectsState.state.status === "ready" ? { status: "ready" } : { status: "pending" },
    data === null ? { status: "pending" } : data.summary.status === "failed" ? { status: "failed", error: data.summary.error } : { status: "ready" },
  ]);
  const health = serviceHealth({
    coreReachable: reachable,
    collectionFailed: summaryError !== null || readFailures.length > 0 || projectsFailed,
    capacity,
    recentFailedSessions: failuresLastHour,
  });
  const core = coreStatus({ webApiReachable: reachable });
  const offline = capacity ? capacity.nodes - capacity.online : 0;
  const degraded = capacity ? capacity.online - capacity.available : 0;
  const serviceReason = reachable === false
    ? t("kpi.coreUnreachable")
    : offline > 0
      ? t("tiles.nodesOffline", { count: offline })
      : degraded > 0
        ? t("kpi.nodesNeedAttention", { count: degraded })
        : summaryError !== null || readFailures.length > 0 || projectsFailed
          ? t("kpi.readsFailed")
          : failuresLastHour
            ? t("tiles.failuresLastHour", { count: failuresLastHour })
            : reachable ? t("kpi.coreReachable") : t("health.unknown");

  const loading = refreshing || projectsState.state.status === "loading" || (fleetState.status === "ready" && fleetState.refreshing);
  const updatedAt = data?.loadedAt ?? fleet?.loadedAt ?? null;
  const attentionTotal = totals ? attentionCount(totals.sessions) : null;
  const truncated = data?.sessions.truncated ?? [];
  // Whether any Session exists: Core's summary counts them all; without it, any Session read counts.
  const sessionCount = totals ? totals.sessions.total
    : sessions?.length ? sessions.length
    : summaryError !== null || sessionReadsFailed ? "failed" : null;

  // Each failed read is its own toast, shown once while it lasts.
  useFailureToast(failure !== null, tCommon("readFailure.unknown"), "overview-load");
  useFailureToast(summaryError !== null, t("errors.summary", { reason: summaryError === null ? "" : readError(summaryError, tCommon) }), "overview-summary");
  useFailureToast(readFailures.length > 0, tCommon("project.partial", { names: readFailures.map((entry) => entry.project.name).join(", ") }), "overview-partial");
  useFailureToast(projectsFailed, t("errors.projects", { reason: projectsState.state.status === "failed" ? projectsState.state.error : projectsState.refreshError ?? "" }), "overview-projects");

  const openSession = (entry: InProject<AgentSession>) => navigate("session", { project: entry.project.id, id: entry.value.id });

  return (
    <section className="page-section console-page overview-page" aria-labelledby="overview-heading">
      <PageHeader
        headingId="overview-heading"
        title={t("title")}
        description={t("description")}
        actions={<RefreshButton refreshing={loading} updatedAt={updatedAt ? formatClock(updatedAt, locale) : null} onClick={refreshAll} />}
      />
      <PageBody>
        <SandboxResetNotice deployment={deployment.data} failed={deployment.isError} onRetry={() => void deployment.refetch()} />
        <FleetReadNotice state={fleetState} onRetry={refreshFleet} />
        <InstallationNotice installation={installation.data} />
        {installation.isError ? <ReadFailure onRetry={() => void installation.refetch()} partial={installation.data !== undefined} /> : null}
        <GettingStarted sandboxReset={deployment.isError ? "failed" : deployment.data ? deployment.data.reset !== null : undefined} fleet={fleetState} sessions={sessionCount} localOnly={installation.isError ? "failed" : installation.data?.local_only} onRetryInstallation={() => void installation.refetch()} />
        {(readFailed || summaryError !== null) && data !== null ? <ReadFailure onRetry={refreshAll} partial /> : null}
        <div className="overview-tiles" aria-label={t("kpi.label")}>
          <MetricTile
            label={t("kpi.service")}
            help={t("kpi.serviceHelp")}
            value={<><span className={`metric-tile-dot metric-tile-dot-${serviceTone[health]}`} aria-hidden="true" />{t(`health.${health}`)}</>}
            sub={serviceReason}
          />
          <MetricTile
            label={t("kpi.running")}
            help={t("kpi.runningHelp")}
            value={totals ? <LiveNumber value={totals.sessions.in_progress} /> : MISSING}
            sub={totals ? t("tiles.sessionSplit", { idle: formatInteger(totals.sessions.idle, locale), total: formatInteger(totals.sessions.total, locale) }) : t("kpi.summaryUnavailable")}
          />
          {cloud ? (
            <MetricTile
              label={t("kpi.cloudRunning")}
              help={t("kpi.cloudRunningHelp")}
              value={<LiveNumber value={cloud.running} />}
              sub={t("tiles.cloudPending", { count: cloud.pending })}
            />
          ) : (
            <MetricTile
              label={t("kpi.slots")}
              help={t("kpi.slotsHelp")}
              value={capacity ? <><LiveNumber value={capacity.active} /><span className="kpi-unit">/ {formatInteger(capacity.maxActive, locale)}</span></> : MISSING}
              sub={capacity
                ? fleet?.deployment.provider === "microsandbox"
                  ? t("tiles.nodesOnlineSuspended", { online: capacity.online, total: capacity.nodes, suspended: capacity.suspended })
                  : t("tiles.nodesOnline", { online: capacity.online, total: capacity.nodes })
                : fleetDetail(fleetState, t)}
            />
          )}
          <MetricTile
            label={t("kpi.attention")}
            help={t("attention.subtitle")}
            value={<LiveNumber value={attentionTotal} />}
            sub={totals ? t("tiles.attentionSplit", { failed: totals.sessions.failed, waiting: totals.sessions.requires_action }) : t("kpi.summaryUnavailable")}
          />
        </div>

        <div className="overview-grid">
          <section className="overview-card overview-activity" aria-labelledby="activity-heading">
            <header className="overview-card-header">
              <div className="console-section-title">
                <h2 id="activity-heading">{t("activity.title")}</h2>
                <span className="overview-card-meta">{t("activity.range")}</span>
                <HelpTip>{t("activity.help")}</HelpTip>
              </div>
              {truncated.length || readFailures.length ? (
                <span className="partial-chip">
                  <StatusDot tone="warning" label={t("activity.partial")} />
                  <HelpTip>
                    {readFailures.length ? t("activity.unreadHelp", { names: readFailures.map((failure) => failure.project.name).join(", ") }) : null}
                    {readFailures.length && truncated.length ? " " : null}
                    {truncated.length ? t("activity.partialHelp", { names: truncated.map((project) => project.name).join(", ") }) : null}
                  </HelpTip>
                </span>
              ) : null}
            </header>
            {sessionReadsFailed ? <ReadFailure onRetry={refreshAll} partial={activity !== null} /> : null}
            {activity && sessionCount === 0 && !sessionReadsFailed && summaryError === null ? (
              <EmptyState title={t("activity.emptyTitle")} description={t("activity.emptyDescription")} action={<button className="button outline" type="button" onClick={() => navigate("projects")}>{t("gettingStarted.session.open")}</button>} />
            ) : activity ? (
              <div className="overview-card-body">
                <TimeSeriesChart
                  label={t("activity.title")}
                  kind="columns"
                  buckets={activity.buckets}
                  bucketSeconds={activity.bucketSeconds}
                  series={[{ id: "created", label: t("activity.created"), color: "var(--data)", values: activity.created, total: formatInteger(activity.created.reduce((sum, value) => sum + value, 0), locale) }]}
                  tooltipOnly={[{ id: "failed", label: t("activity.failed"), color: "var(--danger)", values: activity.failed }]}
                  formatValue={(value) => formatInteger(value, locale)}
                  counts
                  height={204}
                />
              </div>
            ) : sessionReadsFailed ? null : <p className="detail-note overview-card-note" role="status">{t("activity.loading")}</p>}
          </section>

          <FleetCard fleetState={fleetState} core={core} localOnly={installation.data?.local_only === true} />
        </div>


        <section className="overview-card overview-table-card" aria-labelledby="attention-heading">
          <header className="overview-card-header">
            <div className="console-section-title">
              <h2 id="attention-heading">{t("attention.title")}</h2>
              <HelpTip>{t("attention.subtitle")}</HelpTip>
              {attention && attentionTotal !== null && attention.length < attentionTotal ? (
                <>
                  <span className="overview-card-meta">{t("attention.shown", { shown: formatInteger(attention.length, locale), total: formatInteger(attentionTotal, locale) })}</span>
                  <HelpTip>{attention.length < Math.min(attentionTotal, ATTENTION_LIMIT) ? t("attention.partialHelp") : t("attention.limitHelp", { count: ATTENTION_LIMIT })}</HelpTip>
                </>
              ) : null}
            </div>
            <button className="button outline" type="button" onClick={() => navigate("sessions")}>{t("attention.viewLog")}</button>
          </header>
          {sessionReadsFailed || summaryError !== null ? <ReadFailure onRetry={refreshAll} partial={Boolean(attention?.length)} /> : null}
          {attention?.length || (!sessionReadsFailed && summaryError === null) ? <AttentionTable sessions={attention} expected={attentionTotal} unread={readFailures.map((failure) => failure.project.name)} truncated={truncated.length > 0} now={now} onOpen={openSession} /> : null}
        </section>

        <section className="overview-card overview-table-card" aria-labelledby="projects-heading">
          <header className="overview-card-header">
            <div className="console-section-title">
              <h2 id="projects-heading">{t("projects.title")}</h2>
              <HelpTip>{t("projects.help")}</HelpTip>
            </div>
            <button className="button outline" type="button" onClick={() => navigate("projects")}>{t("projects.manage")}</button>
          </header>
          {readFailed || summaryError !== null ? <ReadFailure onRetry={refreshAll} partial={Boolean(usageRows?.length)} /> : null}
          {usageRows?.length || (!readFailed && summaryError === null) ? <ProjectUsageTable rows={usageRows} failed={false} now={now} onOpen={(project) => navigate("projects", { id: project.id })} /> : null}
        </section>
      </PageBody>
    </section>
  );
}

function MetricTile({ label, help, value, sub }: { label: string; help?: ReactNode; value: ReactNode; sub?: ReactNode }) {
  return (
    <article className="metric-tile">
      <header className="console-section-title"><span className="metric-tile-label">{label}</span>{help ? <HelpTip label={label}>{help}</HelpTip> : null}</header>
      <div className="metric-tile-value kpi-value">{value}</div>
      {sub ? <p className="metric-tile-sub">{sub}</p> : null}
    </article>
  );
}

function fleetDetail(state: FleetState, t: TFunction<"overview">): string {
  if (state.status === "unconfigured") return t("fleet.unconfigured");
  if (state.status === "failed") return t("fleet.failed");
  return t("fleet.loading");
}

function cloudHost(fleet: FleetSnapshot | null): CloudHost | null {
  if (fleet?.deployment.provider !== "e2b") return null;
  return { running: fleet.deployment.resources.allocations, pending: fleet.deployment.resources.pending, template: fleet.deployment.configuration?.template || null };
}

/** Core and its sandbox nodes (or E2B's cloud) as an inventory; each opens a popover with the way onward. */
function FleetCard({ fleetState, core, localOnly }: { fleetState: FleetState; core: CoreStatus; localOnly: boolean }) {
  const { t } = useTranslation("overview");
  const { navigate } = useConsoleNavigation();
  const fleet = fleetSnapshot(fleetState);
  const cloud = cloudHost(fleet);
  const hosts = cloud ? [] : fleet?.nodes ?? [];
  const hidden = Math.max(0, hosts.length - FLEET_LIMIT);
  useFailureToast(fleetState.status === "ready" && Boolean(fleetState.error), t("fleet.stale"), "overview-fleet-refresh");
  return (
    <section className="overview-card overview-fleet" aria-labelledby="fleet-heading">
      <header className="overview-card-header">
        <div className="console-section-title">
          <h2 id="fleet-heading">{t("fleet.title")}</h2>
          <HelpTip>{t(cloud ? "fleet.cloudHelp" : "fleet.help")}</HelpTip>
        </div>
        {fleetState.status === "ready" ? (
          !fleet?.deployment.provider
            ? <button className="button outline" type="button" onClick={() => navigate("system", { id: "sandbox" })}>{t("fleet.setUp")}</button>
            : cloud || hosts.length
              ? <button className="button outline" type="button" onClick={() => cloud ? navigate("system", { id: "sandbox" }) : navigate("nodes")}>{cloud ? t("fleet.cloud.openBackend") : t("fleet.manageNodes")}</button>
              : <button className="button outline" type="button" onClick={() => navigate("nodes", {}, localOnly ? undefined : "add-node")}>{t(localOnly ? "fleet.manageNodes" : "fleet.addNode")}</button>
        ) : null}
      </header>
      <div className="overview-card-body fleet-body">
        <FleetOverview
          nodes={hosts}
          cloud={cloud}
          onOpenBackend={() => navigate("system", { id: "sandbox" })}
          coreLabel={t(`coreStatus.${core}`)}
          coreTone={coreTone[core]}
          stale={fleetObservationStale(fleetState)}
          onOpenNode={(node) => navigate("nodes", { id: node.id })}
          onOpenSandboxMetrics={() => navigate("sandbox-metrics")}
          onOpenCoreMetrics={() => navigate("core-metrics")}
        />
        {hidden ? <button className="text-action fleet-more" type="button" onClick={() => navigate("nodes")}>{t("fleet.more", { count: hidden })}</button> : null}
        <FleetFooter state={fleetState} empty={fleet && !cloud ? hosts.length === 0 : false} unset={fleet ? !fleet.deployment.provider : false} />
      </div>
    </section>
  );
}

function FleetFooter({ state, empty, unset }: { state: FleetState; empty: boolean; unset: boolean }) {
  const { t } = useTranslation("overview");
  if (state.status === "ready") {
    return empty ? <footer className="fleet-list-footer"><p>{t(unset ? "fleet.notSetUp" : "fleet.noNodes")}</p></footer> : null;
  }
  return (
    <footer className="fleet-list-footer">
      <p role={state.status === "failed" ? "alert" : "status"}>{fleetDetail(state, t)}</p>
    </footer>
  );
}

function usageTitle(summary: ProjectSummary, t: TFunction<"overview">, locale: string | undefined): string | undefined {
  if (!summary.usage) return undefined;
  const number = (value: number) => formatInteger(value, locale);
  return t("projects.tokenDetail", {
    input: number(summary.usage.input_tokens),
    output: number(summary.usage.output_tokens),
    cached: number(summary.usage.cached_tokens),
    reasoning: number(summary.usage.reasoning_tokens),
  });
}

function ProjectUsageTable({ rows, failed, now, onOpen }: { rows: ProjectUsageRow[] | null; failed: boolean; now: number; onOpen: (project: Project) => void }) {
  const { t, i18n } = useTranslation("overview");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  if (rows === null) return failed ? <p className="detail-note overview-card-note" role="alert">{t("projects.unavailable")}</p> : <TableSkeleton label={t("projects.loading")} rows={4} columns={7} />;
  if (!rows.length) return <div className="overview-card-body"><EmptyState title={t("projects.emptyTitle")} /></div>;
  const count = (value: number | undefined) => (value === undefined ? MISSING : formatInteger(value, locale));
  return (
    <div className="overview-table-scroll">
      <table className="data-table overview-projects-table">
        <thead>
          <tr>
            <th scope="col">{tCommon("project.column")}</th>
            <th scope="col" className="numeric">{t("projects.keys")}</th>
            <th scope="col" className="numeric">{t("projects.sessions")}</th>
            <th scope="col" className="numeric">{t("projects.running")}</th>
            <th scope="col" className="numeric">{t("projects.waiting")}</th>
            <th scope="col" className="numeric">{t("projects.failed")}</th>
            <th scope="col" className="numeric">{t("projects.tokens")}</th>
            <th scope="col" className="numeric">{t("projects.coverage")}</th>
            <th scope="col" className="numeric">{t("projects.lastActive")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ project, summary }) => {
            const coverage = summary ? coverageRatio(summary.coverage) : null;
            return (
              <tr key={project.id} className="clickable-row" onClick={() => onOpen(project)}>
                <th scope="row">
                  <button className="table-link" type="button" onClick={(event) => { event.stopPropagation(); onOpen(project); }} aria-label={t("projects.open", { name: project.name })}>
                    <strong><ProjectName project={project} /></strong>
                  </button>
                  {project.status === "archived" ? <span className="pill">{t("projects.archived")}</span> : null}
                </th>
                <td className="numeric">{count(project.active_key_count)}</td>
                <td className="numeric">{count(summary?.sessions.total)}</td>
                <td className="numeric">{count(summary?.sessions.in_progress)}</td>
                <td className="numeric">{count(summary?.sessions.requires_action)}</td>
                <td className={summary?.sessions.failed ? "numeric numeric-danger" : "numeric"}>{count(summary?.sessions.failed)}</td>
                <td className="numeric" title={summary ? usageTitle(summary, t, locale) : undefined}>{summary?.usage ? formatCompact(summary.usage.total_tokens, locale) : MISSING}</td>
                <td className="numeric" title={summary && summary.coverage.sessions ? t("projects.coverageDetail", { reported: summary.coverage.reported, total: summary.coverage.sessions }) : undefined}>
                  {formatPercent(coverage, locale)}
                </td>
                <td className="numeric">{formatRelative(summary?.last_active_at ?? null, now, locale)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function sessionTitle(session: AgentSession): string | null {
  const title = session.metadata?.title;
  if (typeof title === "string" && title.trim()) return title;
  return session.agent?.name?.trim() ? session.agent.name : null;
}

/**
 * Sessions needing attention. "Nothing needs attention" is claimed only when
 * every Session list was read and Core's summary agrees; otherwise the empty
 * table says the Sessions could not be listed.
 */
function AttentionTable({ sessions, expected, unread, truncated, now, onOpen }: {
  sessions: InProject<AgentSession>[] | null;
  /** Sessions needing attention by Core's summary, when it was read. */
  expected: number | null;
  /** Projects whose Session list could not be read. */
  unread: string[];
  truncated: boolean;
  now: number;
  onOpen: (entry: InProject<AgentSession>) => void;
}) {
  const { t, i18n } = useTranslation("overview");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const waitingFor = useWaitingFor();
  if (sessions === null) return <TableSkeleton label={t("attention.loading")} rows={4} columns={5} />;
  if (!sessions.length) {
    const description = unread.length
      ? t("attention.unreadDescription", { names: unread.join(", ") })
      : expected
        ? t("attention.unlistedDescription", { count: expected })
        : truncated || expected === null ? t("attention.partialHelp") : null;
    return (
      <div className="overview-card-body">
        {description ? <EmptyState title={t("attention.unlistedTitle")} description={description} /> : <EmptyState title={t("attention.emptyTitle")} hint={t("attention.emptyDescription")} />}
      </div>
    );
  }
  return (
    <div className="overview-table-scroll">
      <table className="data-table">
        <thead>
          <tr>
            <th scope="col">{t("attention.session")}</th>
            <th scope="col">{tCommon("project.column")}</th>
            <th scope="col">{t("attention.status")}</th>
            <th scope="col" className="numeric">{t("attention.lastActive")}</th>
          </tr>
        </thead>
        <tbody>
          {sessions.map((entry) => {
            const session = entry.value;
            const failed = session.status === "failed";
            return (
              <tr key={`${entry.project.id}:${session.id}`} className="clickable-row" onClick={() => onOpen(entry)}>
                <th scope="row">
                  <NameCell name={sessionTitle(session)} id={session.id} fallback={t("attention.untitled")} onOpen={() => onOpen(entry)} />
                </th>
                <td><ProjectName project={entry.project} /></td>
                <td onClick={(event) => event.stopPropagation()}>
                  <StatusDot tone={failed ? "danger" : "neutral"} label={t(`sessions.${failed ? "failed" : "requires_action"}`)} />
                  <div className="overview-attention-reason">{failed ? <SessionFailure projectId={entry.project.id} session={session} truncate={false} /> : waitingFor(session).join(" · ")}</div>
                </td>
                <td className="numeric">{formatRelative(session.last_active_at, now, locale)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
