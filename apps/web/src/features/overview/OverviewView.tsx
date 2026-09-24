import { AlertTriangle, ArrowRight } from "lucide-react";
import { useMemo, useState, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import type { AgentSession, RuntimeObservation, SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";

import { TimeSeriesChart } from "../../components/charts/TimeSeriesChart";
import { Modal } from "../../components/Modal";
import {
  EmptyState,
  HelpTip,
  Meter,
  PageBody,
  PageHeader,
  RefreshButton,
  StatusDot,
  type Tone,
} from "../../components/console-ui";
import type { CoreConnectionState } from "../../lib/connection";
import { backendFailureStatus } from "../../lib/core-readiness";
import type { ConsoleView } from "../../lib/console-routes";
import {
  formatBytes,
  formatClock,
  formatCores,
  formatInteger,
  formatRelative,
  MISSING,
  shortId,
} from "../../lib/format";
import { sandboxStateLabel } from "../../lib/sandbox-labels";
import type { RuntimeDashboardSnapshot } from "../dashboard/runtime-snapshot";
import { fleetSnapshot, useSandboxFleet, type FleetState } from "../fleet/use-sandbox-fleet";
import {
  allocationsByNode,
  attentionSessions,
  capacitySummary,
  nodeHealth,
  recentFailures,
  runtimeUsage,
  serviceHealth,
  sessionActivity,
  sessionStatusCounts,
  type NodeHealth,
  type ServiceHealth,
} from "./overview-model";
import "./OverviewView.css";

export interface OverviewViewProps {
  coreBaseUrl: string;
  coreState: CoreConnectionState;
  agentsState: CoreConnectionState;
  agentsError: string | null;
  sessions: readonly AgentSession[];
  sessionsState: CoreConnectionState;
  sessionsError: string | null;
  runtimeSnapshot: RuntimeDashboardSnapshot | null;
  runtimeState: CoreConnectionState;
  runtimeError: string | null;
  onRefresh: () => void;
  onOpenSession: (sessionId: string) => void;
  onNavigate: (view: ConsoleView) => void;
  onConfigureConnection: () => void;
}

const healthTone: Record<NodeHealth, Tone> = { available: "ok", degraded: "warning", offline: "danger" };
const serviceTone: Record<ServiceHealth, Tone> = { healthy: "ok", degraded: "warning", down: "danger", unknown: "pending" };

export function OverviewView(props: OverviewViewProps) {
  const { t, i18n } = useTranslation("overview");
  const { t: tPages } = useTranslation("pages");
  const locale = i18n.resolvedLanguage;
  const { state: fleetState, refresh: refreshFleet } = useSandboxFleet(props.coreBaseUrl);
  const fleet = fleetSnapshot(fleetState);
  const [openNodeId, setOpenNodeId] = useState<string | null>(null);
  const now = Math.floor(Date.now() / 1000);

  // The runtime snapshot re-reads the Session list every 30s, so prefer it when present.
  const sessions = props.runtimeSnapshot?.sessions ?? props.sessions;
  const sessionsReady = props.runtimeSnapshot !== null || props.sessionsState === "ready";
  const observations = props.runtimeSnapshot?.observations ?? null;
  const capacity = fleet ? capacitySummary(fleet.nodes) : null;
  const counts = sessionsReady ? sessionStatusCounts(sessions) : null;
  const activity = useMemo(() => (sessionsReady ? sessionActivity(sessions, now) : null), [sessions, sessionsReady, now]);
  const failuresLastHour = sessionsReady ? recentFailures(sessions, now) : null;
  const health = serviceHealth({
    coreReachable: props.coreState === "connecting" ? null : props.coreState === "ready",
    collectionFailed: props.agentsState === "failed" || props.sessionsState === "failed",
    capacity,
    recentFailedSessions: failuresLastHour,
  });
  const allocationMap = useMemo(() => allocationsByNode(fleet?.allocations ?? []), [fleet]);
  const openNode = openNodeId ? fleet?.nodes.find((node) => node.id === openNodeId) ?? null : null;
  const sessionIndex = useMemo(() => new Map(sessions.map((session) => [session.id, session])), [sessions]);
  const refreshing = props.sessionsState === "connecting" || props.runtimeState === "connecting" || (fleetState.status === "ready" && fleetState.refreshing);
  const updatedAt = props.runtimeSnapshot?.loadedAt ?? fleet?.loadedAt ?? null;
  const runtime = observations ? runtimeUsage(observations) : null;

  const collectionErrors: Array<{ label: string; error: string | null }> = [];
  if (props.agentsState === "failed") collectionErrors.push({ label: tPages("dashboard.agents"), error: props.agentsError });
  if (props.sessionsState === "failed") collectionErrors.push({ label: tPages("dashboard.sessions"), error: props.sessionsError });
  // Backend recovery depends on the Core collections only; Runtime failures are listed but never trigger it.
  const failureStatuses = collectionErrors.map((entry) => backendFailureStatus(entry.error));
  const visibleErrors = props.runtimeState === "failed" && props.runtimeError
    ? [...collectionErrors, { label: tPages("dashboard.runtime"), error: props.runtimeError }]
    : collectionErrors;
  const backendUnavailable = collectionErrors.length > 0 && failureStatuses.every(Boolean);
  const failureDetail = Array.from(new Set(failureStatuses.filter(Boolean))).map((status) => (
    status === "network" ? tPages("dashboard.backend.networkFailure") : `HTTP ${status}`
  )).join(" / ");
  const offline = capacity ? capacity.nodes - capacity.online : 0;
  const degraded = capacity ? capacity.online - capacity.available : 0;
  const attention = counts ? counts.failed + counts.requires_action : null;
  const serviceReason = props.coreState === "failed"
    ? t("kpi.coreUnreachable")
    : offline > 0
      ? t("tiles.nodesOffline", { count: offline })
      : degraded > 0
        ? t("kpi.nodesNeedAttention", { count: degraded })
        : failuresLastHour
          ? t("tiles.failuresLastHour", { count: failuresLastHour })
          : props.coreState === "ready" ? t("kpi.coreReachable") : t("health.unknown");

  return (
    <section className="page-section console-page overview-page" aria-labelledby="overview-heading">
      <PageHeader
        headingId="overview-heading"
        title={t("title")}
        help={t("description")}
        actions={<RefreshButton refreshing={refreshing} updatedAt={updatedAt ? formatClock(updatedAt, locale) : null} onClick={() => { props.onRefresh(); refreshFleet(); }} />}
      />
      <PageBody>
        {backendUnavailable ? (
          <button className="recovery-banner" type="button" onClick={props.onConfigureConnection} aria-label={tPages("dashboard.backend.label")}>
            <AlertTriangle size={16} aria-hidden="true" />
            <span>
              <strong>{tPages("dashboard.backend.title")}</strong>
              <small>{tPages("dashboard.backend.detail", { failure: failureDetail ? ` (${failureDetail})` : "" })}</small>
            </span>
            <span className="recovery-banner-action">{tPages("dashboard.backend.openGuide")} <ArrowRight size={13} strokeWidth={1.7} aria-hidden="true" /></span>
          </button>
        ) : visibleErrors.length ? (
          <p className="coverage-note coverage-note-error" role="alert">
            {visibleErrors.map((entry) => `${entry.label}: ${entry.error || tPages("dashboard.backend.collectionFailed")}`).join(" · ")}
            {" "}
            <button className="text-action" type="button" onClick={props.onConfigureConnection}>{tPages("dashboard.connectionSettings")}</button>
          </p>
        ) : null}

        <div className="overview-tiles" aria-label={t("kpi.label")}>
          <MetricTile index={0} label={t("kpi.service")} help={t("kpi.serviceHelp")} value={<><span className={`metric-tile-dot metric-tile-dot-${serviceTone[health]}`} aria-hidden="true" />{t(`health.${health}`)}</>} sub={serviceReason} />
          <MetricTile
            index={1}
            label={t("kpi.running")}
            help={sessionsReady ? t("kpi.active24h", { count: activity ? activity.created.reduce((sum, value) => sum + value, 0) : 0 }) : t("kpi.sessionsUnavailable")}
            value={counts ? formatInteger(counts.in_progress, locale) : MISSING}
            sub={counts ? t("tiles.sessionSplit", { idle: formatInteger(counts.idle, locale), total: formatInteger(sessions.length, locale) }) : t("kpi.sessionsUnavailable")}
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
            value={attention === null ? MISSING : formatInteger(attention, locale)}
            sub={counts ? t("tiles.attentionSplit", { failed: counts.failed, waiting: counts.requires_action }) : t("kpi.sessionsUnavailable")}
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
            ) : <p className="detail-note overview-card-note">{props.sessionsError ? t("attention.unavailable", { reason: props.sessionsError }) : t("attention.loading")}</p>}
          </section>

          <section className="overview-card overview-fleet" aria-labelledby="fleet-heading">
            <header className="overview-card-header">
              <div className="console-section-title">
                <h2 id="fleet-heading">{t("fleet.title")}</h2>
                <HelpTip>{t("fleet.help")}</HelpTip>
                {fleet?.deployment.maintenance ? <StatusDot tone="warning" label={t("detail.maintenance")} /> : null}
                {runtime && props.runtimeState === "failed" ? <StatusDot tone="warning" label={t("runtime.stale")} /> : null}
              </div>
              {fleetState.status === "ready" ? (
                <button className="text-action" type="button" onClick={() => props.onNavigate("nodes")}>
                  {fleet?.nodes.length ? t("fleet.manageNodes") : t("fleet.addNode")}
                </button>
              ) : null}
            </header>
            <div className="overview-card-body">
              <div className="metric-group-rows overview-runtime">
                <RuntimeRows runtime={runtime} note={runtimeNote(runtime, props.runtimeState, t)} />
              </div>
              {fleet?.nodes.length ? (
                <ul className="fleet-nodes" aria-label={t("fleet.title")}>
                  {fleet.nodes.map((node) => {
                    const state = nodeHealth(node);
                    return (
                      <li key={node.id}>
                        <button type="button" className="fleet-node" onClick={() => setOpenNodeId(node.id)} title={t("fleet.openNode", { name: node.name || shortId(node.id) })}>
                          <span className="fleet-node-name">{node.name || shortId(node.id)}</span>
                          {state === "available" ? <span className="fleet-node-state" /> : <StatusDot tone={healthTone[state]} label={t(`nodeHealth.${state}`)} />}
                          <Meter value={node.active} limit={node.max_active} label={t("capacity.slotsOf", { name: node.name })} />
                          <span className="fleet-node-count">{node.active}/{node.max_active}</span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              ) : null}
              <FleetListFooter state={fleetState} empty={fleet ? fleet.nodes.length === 0 : false} />
            </div>
          </section>
        </div>

        <section className="overview-card overview-attention" aria-labelledby="attention-heading">
          <header className="overview-card-header">
            <div className="console-section-title">
              <h2 id="attention-heading">{t("attention.title")}</h2>
              <HelpTip>{t("attention.subtitle")}</HelpTip>
            </div>
            <button className="text-action" type="button" onClick={() => props.onNavigate("sessions")}>{t("attention.viewLog")}</button>
          </header>
          <AttentionTable
            sessions={sessionsReady ? attentionSessions(sessions) : null}
            error={props.sessionsError}
            now={now}
            onOpenSession={props.onOpenSession}
          />
        </section>

        <Modal open={openNode !== null} title={openNode ? openNode.name || shortId(openNode.id) : ""} onClose={() => setOpenNodeId(null)}>
          {openNode ? (
            <NodeDetail
              node={openNode}
              allocations={allocationMap.get(openNode.id) ?? []}
              observations={observations}
              runtimeStale={props.runtimeState === "failed"}
              runtimeState={props.runtimeState}
              sessionIndex={sessionIndex}
              now={now}
              onOpenSession={(sessionId) => { setOpenNodeId(null); props.onOpenSession(sessionId); }}
            />
          ) : null}
        </Modal>
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
  if (state.status === "remote") return t("fleet.remote");
  if (state.status === "unconfigured") return t("fleet.unconfigured");
  if (state.status === "failed") return t("fleet.failed");
  return t("fleet.loading");
}

function FleetListFooter({ state, empty }: { state: FleetState; empty: boolean }) {
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

function MetricRow({
  label,
  value,
  limit,
  display,
  color,
  note,
}: {
  label: string;
  value: number | null;
  limit: number | null;
  display: string;
  color?: string;
  note?: string | null;
}) {
  return (
    <div className="metric-row">
      <span className="metric-row-label">{label}</span>
      {note ? <span className="metric-row-note">{note}</span> : <Meter value={value} limit={limit} label={label} color={color} />}
      <span className="metric-row-value">{display}</span>
    </div>
  );
}

function FactRow({ label, children, title }: { label: string; children: ReactNode; title?: string }) {
  return (
    <div className="metric-row metric-row-fact">
      <span className="metric-row-label">{label}</span>
      <span className="metric-row-value" title={title}>{children}</span>
    </div>
  );
}

function MetricGroup({
  title,
  help,
  status,
  action,
  children,
}: {
  title: string;
  help?: ReactNode;
  status?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="metric-group">
      <div className="metric-group-header">
        <h3 className="console-section-title">{title}{help ? <HelpTip label={title}>{help}</HelpTip> : null}{status}</h3>
        {action}
      </div>
      <div className="metric-group-rows">{children}</div>
    </div>
  );
}

function runtimeNote(
  runtime: ReturnType<typeof runtimeUsage> | null,
  runtimeState: CoreConnectionState,
  t: TFunction<"overview">,
): string | null {
  if (runtime) return runtime.hosted ? null : t("runtime.none");
  if (runtimeState === "connecting") return t("runtime.loading");
  return t("runtime.unavailable");
}

function RuntimeRows({ runtime, note }: { runtime: ReturnType<typeof runtimeUsage> | null; note: string | null }) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const usable = runtime && runtime.hosted ? runtime : null;
  return (
    <>
      <MetricRow
        label={t("runtime.cpu")}
        value={usable?.cpuUsageCores ?? null}
        limit={usable?.cpuCapacityCores ?? null}
        note={note}
        display={usable ? `${formatCores(usable.cpuUsageCores, locale)} / ${t("capacity.cores", { value: formatCores(usable.cpuCapacityCores, locale) })}` : MISSING}
      />
      <MetricRow
        label={t("runtime.memory")}
        value={usable?.memoryUsageBytes ?? null}
        limit={usable?.memoryLimitBytes ?? null}
        note={note}
        display={usable ? `${formatBytes(usable.memoryUsageBytes)} / ${formatBytes(usable.memoryLimitBytes)}` : MISSING}
      />
    </>
  );
}

function NodeDetail({
  node,
  allocations,
  observations,
  runtimeStale,
  runtimeState,
  sessionIndex,
  now,
  onOpenSession,
}: {
  node: SandboxNode;
  allocations: readonly SandboxAllocation[];
  observations: readonly RuntimeObservation[] | null;
  runtimeStale: boolean;
  runtimeState: CoreConnectionState;
  sessionIndex: ReadonlyMap<string, AgentSession>;
  now: number;
  onOpenSession: (sessionId: string) => void;
}) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const sandboxLocale = locale?.startsWith("zh") ? "zh" : "en";
  const health = nodeHealth(node);
  const runtime = observations ? runtimeUsage(observations, new Set(allocations.map((allocation) => allocation.session_id))) : null;
  const lastSeen = node.last_seen_at ? Date.parse(node.last_seen_at) / 1000 : null;
  const hostValue = (value: string) => (node.online ? value : MISSING);
  return (
    <>
      <p className="node-detail-status" title={`${t("node.id")}: ${node.id}`}>
        <StatusDot tone={healthTone[health]} label={t(`nodeHealth.${health}`)} />
        <code>{shortId(node.id)}</code>
      </p>
      <div className="detail-columns">
        <MetricGroup
          title={t("capacity.title")}
          help={<>{t("capacity.help")}<br />{t("capacity.nodeCounts", { retained: node.retained, maxRetained: node.max_retained, reserved: node.reserved, cleanup: node.cleanup_pending })}</>}
          status={runtime && runtimeStale ? <StatusDot tone="warning" label={t("runtime.stale")} /> : null}
        >
          <MetricRow
            label={t("capacity.active")}
            value={node.active}
            limit={node.max_active}
            display={`${formatInteger(node.active, locale)} / ${formatInteger(node.max_active, locale)}`}
          />
          <RuntimeRows runtime={runtime} note={runtimeNote(runtime, runtimeState, t)} />
        </MetricGroup>
        <MetricGroup title={t("node.host")} help={node.online ? t("node.hostHelp") : t("node.staleHost")}>
          <FactRow label={t("node.heartbeat")}>{formatRelative(lastSeen, now, locale)}</FactRow>
          <FactRow label={t("node.provider")}>
            {node.online ? <StatusDot tone={node.provider_ready ? "ok" : "danger"} label={node.provider_ready ? t("node.providerReady") : t("node.providerUnavailable")} /> : MISSING}
          </FactRow>
          <FactRow label={t("capacity.hostCpu")}>{hostValue(node.cpu_count === null ? MISSING : t("capacity.cores", { value: node.cpu_count }))}</FactRow>
          <FactRow label={t("capacity.hostMemory")}>{hostValue(formatBytes(node.available_memory_bytes))}</FactRow>
          <FactRow label={t("capacity.hostDisk")}>{hostValue(formatBytes(node.available_disk_bytes))}</FactRow>
        </MetricGroup>
      </div>
      <section className="detail-allocations" aria-labelledby="node-allocations-heading">
        <h3 id="node-allocations-heading">{t("node.allocations", { count: allocations.length })}</h3>
        {allocations.length ? (
          <table className="data-table">
            <thead>
              <tr>
                <th scope="col">{t("node.session")}</th>
                <th scope="col">{t("node.state")}</th>
                <th scope="col">{t("node.phase")}</th>
                <th scope="col" className="numeric">{t("node.created")}</th>
              </tr>
            </thead>
            <tbody>
              {allocations.map((allocation) => {
                const session = sessionIndex.get(allocation.session_id);
                return (
                  <tr key={allocation.id}>
                    <td>
                      {session ? (
                        <button className="table-link" type="button" title={session.id} onClick={() => onOpenSession(session.id)}>
                          <strong>{session.agent?.name || shortId(session.id)}</strong>
                        </button>
                      ) : (
                        <span className="table-muted" title={t("node.otherProject")}><code>{shortId(allocation.session_id)}</code></span>
                      )}
                    </td>
                    <td>{sandboxStateLabel(allocation.state, sandboxLocale)}</td>
                    <td>{sandboxStateLabel(allocation.compute_phase, sandboxLocale)}</td>
                    <td className="numeric">{formatRelative(Date.parse(allocation.created_at) / 1000, now, locale)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        ) : <p className="detail-note">{t("node.noAllocations")}</p>}
      </section>
    </>
  );
}

function AttentionTable({
  sessions,
  error,
  now,
  onOpenSession,
}: {
  sessions: AgentSession[] | null;
  error: string | null;
  now: number;
  onOpenSession: (sessionId: string) => void;
}) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  if (sessions === null) return <p className="detail-note" role={error ? "alert" : "status"}>{error ? t("attention.unavailable", { reason: error }) : t("attention.loading")}</p>;
  if (!sessions.length) return <EmptyState title={t("attention.emptyTitle")} description={t("attention.emptyDescription")} />;
  return (
    <table className="data-table">
      <thead>
        <tr>
          <th scope="col">{t("attention.session")}</th>
          <th scope="col">{t("attention.status")}</th>
          <th scope="col">{t("attention.reason")}</th>
          <th scope="col" className="numeric">{t("attention.lastActive")}</th>
        </tr>
      </thead>
      <tbody>
        {sessions.map((session) => (
          <tr key={session.id} className="clickable-row" onClick={() => onOpenSession(session.id)}>
            <td>
              <button className="table-link" type="button" title={session.id} onClick={(event) => { event.stopPropagation(); onOpenSession(session.id); }}>
                <strong>{session.agent?.name || shortId(session.id)}</strong>
              </button>
            </td>
            <td><StatusDot tone={session.status === "failed" ? "danger" : "neutral"} label={t(`sessions.${session.status === "failed" ? "failed" : "requires_action"}`)} /></td>
            <td className="table-truncate" title={session.error ?? undefined}>{session.status === "failed" ? session.error || t("attention.noError") : t("attention.requiredActions", { count: session.required_actions.length })}</td>
            <td className="numeric">{formatRelative(session.last_active_at, now, locale)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
