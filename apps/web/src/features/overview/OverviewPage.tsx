import type { AgentSession, SandboxNode } from "@agents-core-web/agents-client";
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import { TimeSeriesChart } from "../../components/charts/TimeSeriesChart";
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
import { admin, projectClient, ProjectName, useProjects } from "../../lib/projects";
import { capacitySummary, coreStatus, type CoreStatus } from "../fleet/fleet-model";
import { fleetSnapshot, useSandboxFleet, type FleetState } from "../fleet/use-sandbox-fleet";
import { isAbortError, type InProject } from "../metrics/project-sessions";
import { FleetTopology, TOPOLOGY_LIMIT } from "./FleetTopology";
import { loadOverview, type OverviewData } from "./overview-loader";
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
import { loadSummary, type Project, type ProjectSummary } from "../../lib/admin-view";

export const OVERVIEW_REFRESH_MS = 30_000;
const ATTENTION_LIMIT = 8;

const serviceTone: Record<ServiceHealth, Tone> = { healthy: "ok", degraded: "warning", down: "danger", unknown: "pending" };
const coreTone: Record<CoreStatus, Tone> = { checking: "pending", running: "ok", maintenance: "warning", unreachable: "danger" };

type LoadState =
  | { status: "loading"; data: OverviewData | null }
  | { status: "ready"; data: OverviewData }
  | { status: "failed"; data: OverviewData | null; error: string };

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function useOverviewData(projects: readonly Project[], projectsReady: boolean) {
  const [state, setState] = useState<LoadState>({ status: "loading", data: null });
  const [revision, setRevision] = useState(0);
  const latest = useRef<OverviewData | null>(null);

  useEffect(() => {
    if (!projectsReady) return;
    const controller = new AbortController();
    setState({ status: "loading", data: latest.current });
    loadOverview(projects, {
      summary: (signal) => loadSummary({ signal }),
      sessions: (project) => projectClient(project.id),
    }, Math.floor(Date.now() / 1000), controller.signal).then((data) => {
      latest.current = data;
      setState({ status: "ready", data });
    }, (error: unknown) => {
      if (controller.signal.aborted || isAbortError(error)) return;
      setState({ status: "failed", data: latest.current, error: errorText(error) });
    });
    return () => controller.abort();
  }, [projects, projectsReady, revision]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!document.hidden) setRevision((value) => value + 1);
    }, OVERVIEW_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);

  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  return { state, refresh };
}

export function OverviewPage() {
  const { t, i18n } = useTranslation("overview");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  const projectsState = useProjects();
  const projects = projectsState.state.projects;
  const projectsReady = projectsState.state.status !== "loading" || projects.length > 0;
  const { state, refresh } = useOverviewData(projects, projectsReady);
  const { state: fleetState, refresh: refreshFleet } = useSandboxFleet();
  const fleet = fleetSnapshot(fleetState);
  const data = state.data;
  const now = Math.floor((data?.loadedAt ?? Date.now()) / 1000);

  const summaryRows = data?.summary.status === "ready" ? data.summary.rows : null;
  const totals = useMemo(() => (summaryRows ? summaryTotals(summaryRows) : null), [summaryRows]);
  const usageRows = useMemo(() => (summaryRows ? projectUsageRows(projects, summaryRows) : null), [projects, summaryRows]);
  const sessions = data?.sessions.sessions ?? null;
  const activity = useMemo(() => (sessions ? sessionActivity(sessions.map((entry) => entry.value), now) : null), [now, sessions]);
  const attention = useMemo(() => (sessions ? attentionSessions(sessions, ATTENTION_LIMIT) : null), [sessions]);
  const capacity = fleet ? capacitySummary(fleet.nodes) : null;
  const failuresLastHour = sessions ? recentFailures(sessions.map((entry) => entry.value), now) : null;

  const summaryError = data?.summary.status === "failed" ? data.summary.error : null;
  const readFailures = data?.sessions.failures ?? [];
  const reachable = webApiReachable([
    projectsState.state.status === "failed" ? { status: "failed", error: projectsState.state.error } : projectsState.state.status === "ready" ? { status: "ready" } : { status: "pending" },
    data === null ? { status: "pending" } : data.summary.status === "failed" ? { status: "failed", error: data.summary.error } : { status: "ready" },
  ]);
  const health = serviceHealth({
    coreReachable: reachable,
    collectionFailed: summaryError !== null || readFailures.length > 0 || projectsState.state.status === "failed",
    capacity,
    recentFailedSessions: failuresLastHour,
  });
  const core = coreStatus({ webApiReachable: reachable, maintenance: fleet ? fleet.deployment.maintenance : null });
  const offline = capacity ? capacity.nodes - capacity.online : 0;
  const degraded = capacity ? capacity.online - capacity.available : 0;
  const serviceReason = reachable === false
    ? t("kpi.coreUnreachable")
    : offline > 0
      ? t("tiles.nodesOffline", { count: offline })
      : degraded > 0
        ? t("kpi.nodesNeedAttention", { count: degraded })
        : summaryError !== null || readFailures.length > 0
          ? t("kpi.readsFailed")
          : failuresLastHour
            ? t("tiles.failuresLastHour", { count: failuresLastHour })
            : reachable ? t("kpi.coreReachable") : t("health.unknown");

  const loading = state.status === "loading" || (fleetState.status === "ready" && fleetState.refreshing);
  const updatedAt = data?.loadedAt ?? fleet?.loadedAt ?? null;
  const attentionTotal = totals ? attentionCount(totals.sessions) : null;
  const truncated = data?.sessions.truncated ?? [];

  const notices: string[] = [];
  if (state.status === "failed") notices.push(t("errors.load", { reason: state.error }));
  if (summaryError !== null) notices.push(t("errors.summary", { reason: errorText(summaryError) }));
  if (readFailures.length) notices.push(tCommon("project.partial", { names: readFailures.map((failure) => failure.project.name).join(", ") }));
  if (projectsState.state.status === "failed") notices.push(t("errors.projects", { reason: String(projectsState.state.error) }));

  const openSession = (entry: InProject<AgentSession>) => navigate("session", { project: entry.project.id, id: entry.value.id });

  return (
    <section className="page-section console-page overview-page" aria-labelledby="overview-heading">
      <PageHeader
        headingId="overview-heading"
        title={t("title")}
        help={t("description")}
        actions={<RefreshButton refreshing={loading} updatedAt={updatedAt ? formatClock(updatedAt, locale) : null} onClick={() => { projectsState.refresh(); refresh(); refreshFleet(); }} />}
      />
      <PageBody>
        {notices.length ? <p className="coverage-note coverage-note-error" role="alert">{notices.join(" ")}</p> : null}

        <div className="overview-tiles" aria-label={t("kpi.label")}>
          <MetricTile
            index={0}
            label={t("kpi.service")}
            help={t("kpi.serviceHelp")}
            value={<><span className={`metric-tile-dot metric-tile-dot-${serviceTone[health]}`} aria-hidden="true" />{t(`health.${health}`)}</>}
            sub={serviceReason}
          />
          <MetricTile
            index={1}
            label={t("kpi.running")}
            help={t("kpi.runningHelp")}
            value={totals ? formatInteger(totals.sessions.in_progress, locale) : MISSING}
            sub={totals ? t("tiles.sessionSplit", { idle: formatInteger(totals.sessions.idle, locale), total: formatInteger(totals.sessions.total, locale) }) : t("kpi.summaryUnavailable")}
          />
          <MetricTile
            index={2}
            label={t("kpi.slots")}
            help={t("kpi.slotsHelp")}
            value={capacity ? `${formatInteger(capacity.active, locale)} / ${formatInteger(capacity.maxActive, locale)}` : MISSING}
            sub={capacity ? t("tiles.nodesOnline", { online: capacity.online, total: capacity.nodes }) : fleetDetail(fleetState, t)}
          />
          <MetricTile
            index={3}
            label={t("kpi.attention")}
            help={t("attention.subtitle")}
            value={attentionTotal === null ? MISSING : formatInteger(attentionTotal, locale)}
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
              {truncated.length ? (
                <span className="partial-chip">
                  <StatusDot tone="warning" label={t("activity.partial")} />
                  <HelpTip>{t("activity.partialHelp", { names: truncated.map((project) => project.name).join(", ") })}</HelpTip>
                </span>
              ) : null}
            </header>
            {activity ? (
              <div className="overview-card-body">
                <TimeSeriesChart
                  label={t("activity.title")}
                  kind="columns"
                  buckets={activity.buckets}
                  bucketSeconds={activity.bucketSeconds}
                  series={[{ id: "created", label: t("activity.created"), color: "color-mix(in srgb, var(--data) 62%, var(--surface))", values: activity.created, total: formatInteger(activity.created.reduce((sum, value) => sum + value, 0), locale) }]}
                  tooltipOnly={[{ id: "failed", label: t("activity.failed"), color: "var(--danger)", values: activity.failed }]}
                  formatValue={(value) => formatInteger(value, locale)}
                  height={196}
                />
              </div>
            ) : <p className="detail-note overview-card-note" role="status">{t("activity.loading")}</p>}
          </section>

          <FleetCard fleetState={fleetState} core={core} onManage={() => navigate("nodes")} onOpen={(node) => navigate("nodes", { id: node.id })} />
        </div>

        <section className="overview-card overview-table-card" aria-labelledby="projects-heading">
          <header className="overview-card-header">
            <div className="console-section-title">
              <h2 id="projects-heading">{t("projects.title")}</h2>
              <HelpTip>{t("projects.help")}</HelpTip>
            </div>
            <button className="text-action" type="button" onClick={() => navigate("projects")}>{t("projects.manage")}</button>
          </header>
          <ProjectUsageTable rows={usageRows} failed={summaryError !== null} now={now} onOpen={(project) => navigate("projects", { id: project.id })} />
        </section>

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
            <button className="text-action" type="button" onClick={() => navigate("sessions")}>{t("attention.viewLog")}</button>
          </header>
          <AttentionTable sessions={attention} now={now} onOpen={openSession} />
        </section>
      </PageBody>
    </section>
  );
}

function MetricTile({ index, label, help, value, sub }: { index: number; label: string; help?: ReactNode; value: ReactNode; sub?: ReactNode }) {
  return (
    <article className="overview-card metric-tile" style={{ "--i": index } as CSSProperties}>
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

/** Core and its sandbox nodes as a topology; a node opens on the Nodes page. */
function FleetCard({ fleetState, core, onManage, onOpen }: { fleetState: FleetState; core: CoreStatus; onManage: () => void; onOpen: (node: SandboxNode) => void }) {
  const { t } = useTranslation("overview");
  const fleet = fleetSnapshot(fleetState);
  const hosts = fleet?.nodes ?? [];
  const hidden = Math.max(0, hosts.length - TOPOLOGY_LIMIT);
  return (
    <section className="overview-card overview-fleet" aria-labelledby="fleet-heading">
      <header className="overview-card-header">
        <div className="console-section-title">
          <h2 id="fleet-heading">{t("fleet.title")}</h2>
          <HelpTip>{t("fleet.help")}</HelpTip>
        </div>
        {fleetState.status === "ready" ? (
          <button className="text-action" type="button" onClick={onManage}>{hosts.length ? t("fleet.manageNodes") : t("fleet.addNode")}</button>
        ) : null}
      </header>
      <div className="overview-card-body fleet-body">
        <FleetTopology
          nodes={hosts}
          coreLabel={t(`coreStatus.${core}`)}
          coreTone={coreTone[core]}
          stale={fleetState.status === "ready" && fleetState.error !== null}
          onOpen={onOpen}
        />
        {hidden ? <button className="text-action fleet-more" type="button" onClick={onManage}>{t("fleet.more", { n: hidden })}</button> : null}
        <FleetFooter state={fleetState} empty={fleet ? hosts.length === 0 : false} />
      </div>
    </section>
  );
}

function FleetFooter({ state, empty }: { state: FleetState; empty: boolean }) {
  const { t } = useTranslation("overview");
  if (state.status === "ready") {
    if (!empty && !state.error) return null;
    return (
      <footer className="fleet-list-footer">
        {empty ? <p>{t("fleet.noNodes")}</p> : null}
        {state.error ? <p role="alert">{t("fleet.stale")}</p> : null}
      </footer>
    );
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
  if (rows === null) return <p className="detail-note overview-card-note" role={failed ? "alert" : "status"}>{failed ? t("projects.unavailable") : t("projects.loading")}</p>;
  if (!rows.length) return <div className="overview-card-body"><EmptyState title={t("projects.emptyTitle")} /></div>;
  const count = (value: number | undefined) => (value === undefined ? MISSING : formatInteger(value, locale));
  return (
    <div className="overview-table-scroll">
      <table className="data-table overview-projects-table">
        <thead>
          <tr className="data-table-groups">
            <th scope="col" rowSpan={2}>{tCommon("project.column")}</th>
            <th scope="col" rowSpan={2} className="numeric">{t("projects.keys")}</th>
            <th scope="colgroup" colSpan={5} className="column-group">{t("projects.assets")}</th>
            <th scope="colgroup" colSpan={4} className="column-group">{t("projects.sessions")}</th>
            <th scope="col" rowSpan={2} className="numeric column-group-start">{t("projects.tokens")}</th>
            <th scope="col" rowSpan={2} className="numeric">{t("projects.coverage")}</th>
            <th scope="col" rowSpan={2} className="numeric">{t("projects.lastActive")}</th>
          </tr>
          <tr>
            <th scope="col" className="numeric column-group-start">{t("projects.agents")}</th>
            <th scope="col" className="numeric">{t("projects.templates")}</th>
            <th scope="col" className="numeric">{t("projects.skills")}</th>
            <th scope="col" className="numeric">{t("projects.files")}</th>
            <th scope="col" className="numeric">{t("projects.vaults")}</th>
            <th scope="col" className="numeric column-group-start">{t("projects.total")}</th>
            <th scope="col" className="numeric">{t("projects.running")}</th>
            <th scope="col" className="numeric">{t("projects.waiting")}</th>
            <th scope="col" className="numeric">{t("projects.failed")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ project, summary }) => {
            const assets = summary?.assets ?? null;
            const coverage = summary ? coverageRatio(summary.coverage) : null;
            return (
              <tr key={project.id} className="clickable-row" onClick={() => onOpen(project)}>
                <th scope="row">
                  <button className="table-link" type="button" onClick={(event) => { event.stopPropagation(); onOpen(project); }} aria-label={t("projects.open", { name: project.name })}>
                    <strong><ProjectName project={project} /></strong>
                  </button>
                  {project.status === "archived" ? <span className="overview-project-state">{t("projects.archived")}</span> : null}
                </th>
                <td className="numeric">{count(project.active_key_count)}</td>
                <td className="numeric column-group-start">{count(assets?.agents)}</td>
                <td className="numeric">{count(assets?.environment_templates)}</td>
                <td className="numeric">{count(assets?.skills)}</td>
                <td className="numeric">{count(assets?.files)}</td>
                <td className="numeric">{count(assets?.vaults)}</td>
                <td className="numeric column-group-start">{count(summary?.sessions.total)}</td>
                <td className="numeric">{count(summary?.sessions.in_progress)}</td>
                <td className="numeric">{count(summary?.sessions.requires_action)}</td>
                <td className={summary?.sessions.failed ? "numeric numeric-danger" : "numeric"}>{count(summary?.sessions.failed)}</td>
                <td className="numeric column-group-start" title={summary ? usageTitle(summary, t, locale) : undefined}>{summary?.usage ? formatCompact(summary.usage.total_tokens, locale) : MISSING}</td>
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

function AttentionTable({ sessions, now, onOpen }: { sessions: InProject<AgentSession>[] | null; now: number; onOpen: (entry: InProject<AgentSession>) => void }) {
  const { t, i18n } = useTranslation("overview");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  if (sessions === null) return <p className="detail-note overview-card-note" role="status">{t("attention.loading")}</p>;
  if (!sessions.length) return <div className="overview-card-body"><EmptyState title={t("attention.emptyTitle")} description={t("attention.emptyDescription")} /></div>;
  return (
    <div className="overview-table-scroll">
      <table className="data-table">
        <thead>
          <tr>
            <th scope="col">{t("attention.session")}</th>
            <th scope="col">{tCommon("project.column")}</th>
            <th scope="col">{t("attention.status")}</th>
            <th scope="col">{t("attention.reason")}</th>
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
                <td><StatusDot tone={failed ? "danger" : "neutral"} label={t(`sessions.${failed ? "failed" : "requires_action"}`)} /></td>
                <td className="table-truncate" title={session.error ?? undefined}>{failed ? session.error || t("attention.noError") : t("attention.requiredActions", { count: session.required_actions.length })}</td>
                <td className="numeric">{formatRelative(session.last_active_at, now, locale)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
