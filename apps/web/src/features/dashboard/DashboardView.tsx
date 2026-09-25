import {
  AlertTriangle,
  ArrowRight,
  Bot,
  MessageSquare,
  RefreshCw,
  Rows3,
} from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { AdminClient, type AgentSession, type SandboxNode, type SavedAgent } from "@agents-core-web/agents-client";

import { StatusIcon, type StatusKind } from "../../components/StatusIcon";
import { backendFailureStatus } from "../../lib/core-readiness";
import { isLocalProxyBaseUrl } from "../../lib/connection";
import { sandboxConsoleConfig } from "../sandbox/console-config";
import {
  buildDashboardSnapshot,
  buildRuntimeDashboardModel,
  formatDashboardBytes,
  formatDashboardTimestamp,
  formatDashboardTokens,
  type DashboardCollectionState,
  type DashboardSessionRow,
} from "./dashboard-model";
import { RuntimeObservabilityContent, type RuntimeHistoryLoader } from "./RuntimeObservabilityContent";
import { SystemObservabilityContent } from "./SystemObservabilityContent";
import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";
import "./DashboardView.css";

export interface DashboardViewProps {
  agents: readonly SavedAgent[];
  sessions: readonly AgentSession[];
  agentCollectionState: DashboardCollectionState;
  agentCollectionError: string | null;
  agentCollectionHasSnapshot: boolean;
  sessionCollectionState: DashboardCollectionState;
  sessionCollectionError: string | null;
  sessionCollectionHasSnapshot: boolean;
  runtimeSnapshot: RuntimeDashboardSnapshot | null;
  runtimeCollectionState: DashboardCollectionState;
  runtimeCollectionError: string | null;
  runtimeCollectionHasSnapshot: boolean;
  loadRuntimeHistory: RuntimeHistoryLoader;
  coreBaseUrl?: string;
  initialTab?: "overview" | "observability";
  onRefresh: () => void;
  onCreateAgent: () => void;
  onStartSession: () => void;
  onViewAgents: () => void;
  onViewSessions: () => void;
  onConfigureConnection: () => void;
  onOpenSession: (sessionId: string) => void;
}

function collectionHasSnapshot(state: DashboardCollectionState, hasSnapshot: boolean): boolean {
  return state === "ready" || hasSnapshot;
}

function collectionStatusKind(state: DashboardCollectionState): StatusKind {
  if (state === "ready") return "completed";
  if (state === "failed") return "failed";
  return "running";
}

function CollectionStateBadge({
  label,
  state,
  hasSnapshot,
}: {
  label: string;
  state: DashboardCollectionState;
  hasSnapshot: boolean;
}) {
  const { t } = useTranslation("pages");
  const stateLabel = state === "ready"
    ? t("dashboard.collection.ready")
    : state === "connecting"
      ? t(hasSnapshot ? "dashboard.collection.refreshing" : "dashboard.collection.loading")
      : t(hasSnapshot ? "dashboard.collection.stale" : "dashboard.collection.unavailable");
  return (
    <span className={`dashboard-source-badge dashboard-source-badge-${state}`}>
      <StatusIcon status={collectionStatusKind(state)} title={t("dashboard.collection.statusLabel", { label, state: stateLabel })} />
      <strong>{label}</strong>
      <span>{stateLabel}</span>
    </span>
  );
}

function Metric({
  label,
  value,
  detail,
  emphasis = false,
}: {
  label: string;
  value: string;
  detail: string;
  emphasis?: boolean;
}) {
  return (
    <div className={emphasis ? "dashboard-metric dashboard-metric-attention" : "dashboard-metric"}>
      <dt>{label}</dt>
      <dd>{value}</dd>
      <small>{detail}</small>
    </div>
  );
}

function SessionMeta({ session }: { session: DashboardSessionRow }) {
  const { t, i18n } = useTranslation("pages");
  return (
    <span className="dashboard-session-meta">
      <span>{session.agentLabel}</span>
      <span aria-hidden="true">·</span>
      <span
        title={session.environmentProfile === "self_hosted"
          ? t("dashboard.environment.selfHostedHint")
          : session.environmentProfile === "openai_hosted"
            ? t("dashboard.environment.managedHint")
            : undefined}
      >
        {t(`dashboard.environment.${session.environmentProfile}` as never)}
      </span>
      <span aria-hidden="true">·</span>
      <time dateTime={session.lastActiveAt === null ? undefined : new Date(session.lastActiveAt * 1_000).toISOString()}>
        {formatDashboardTimestamp(session.lastActiveAt, i18n.resolvedLanguage)}
      </time>
    </span>
  );
}

function AttentionList({
  sessions,
  onOpenSession,
}: {
  sessions: readonly DashboardSessionRow[];
  onOpenSession: (sessionId: string) => void;
}) {
  const { t } = useTranslation("pages");
  return (
    <div className="dashboard-attention-list" role="list" aria-label={t("dashboard.attentionList")}>
      {sessions.map((session, index) => (
        <div role="listitem" key={`${session.id ?? "unavailable"}:${index}`}>
          <button
            className="dashboard-attention-item"
            type="button"
            disabled={session.id === null}
            onClick={() => {
              if (session.id !== null) onOpenSession(session.id);
            }}
          >
            <span className={`dashboard-status-dot dashboard-status-dot-${session.status}`} aria-hidden="true" />
            <span className="dashboard-attention-copy">
              <span className="dashboard-attention-title">
                <strong>{session.title}</strong>
                <span className={`dashboard-status-pill dashboard-status-pill-${session.status}`}>
                  {t(`dashboard.status.${session.status}` as never)}
                </span>
              </span>
              <SessionMeta session={session} />
            </span>
            <ArrowRight size={15} strokeWidth={1.7} aria-hidden="true" />
          </button>
        </div>
      ))}
    </div>
  );
}

function QuickAction({
  icon,
  title,
  description,
  onClick,
}: {
  icon: ReactNode;
  title: string;
  description: string;
  onClick: () => void;
}) {
  return (
    <button className="dashboard-quick-action" type="button" onClick={onClick}>
      <span className="dashboard-quick-icon" aria-hidden="true">{icon}</span>
      <span>
        <strong>{title}</strong>
        <small>{description}</small>
      </span>
      <ArrowRight size={15} strokeWidth={1.7} aria-hidden="true" />
    </button>
  );
}

function RecentSessions({
  sessions,
  onOpenSession,
}: {
  sessions: readonly DashboardSessionRow[];
  onOpenSession: (sessionId: string) => void;
}) {
  const { t, i18n } = useTranslation("pages");
  return (
    <div className="dashboard-recent-ledger" role="table" aria-label={t("dashboard.recentTable")}>
      <div className="dashboard-recent-header" role="row">
        <span role="columnheader">{t("dashboard.session")}</span>
        <span role="columnheader">{t("dashboard.statusLabel")}</span>
        <span role="columnheader">{t("dashboard.environmentLabel")}</span>
        <span role="columnheader">{t("dashboard.lastActive")}</span>
      </div>
      {sessions.map((session, index) => (
        <div className="dashboard-recent-row" role="row" key={`${session.id ?? "unavailable"}:${index}`}>
          <span className="dashboard-recent-identity" role="cell">
            <button
              type="button"
              disabled={session.id === null}
              onClick={() => {
                if (session.id !== null) onOpenSession(session.id);
              }}
            >
              {session.title}
            </button>
            <small>{session.agentLabel}</small>
          </span>
          <span role="cell">
            <span className={`dashboard-status-pill dashboard-status-pill-${session.status}`}>
              {t(`dashboard.status.${session.status}` as never)}
            </span>
          </span>
          <span role="cell">{t(`dashboard.environment.${session.environmentProfile}` as never)}</span>
          <time
            role="cell"
            dateTime={session.lastActiveAt === null ? undefined : new Date(session.lastActiveAt * 1_000).toISOString()}
          >
            {formatDashboardTimestamp(session.lastActiveAt, i18n.resolvedLanguage)}
          </time>
        </div>
      ))}
    </div>
  );
}

export function DashboardView({
  agents,
  sessions,
  agentCollectionState,
  agentCollectionError,
  agentCollectionHasSnapshot,
  sessionCollectionState,
  sessionCollectionError,
  sessionCollectionHasSnapshot,
  runtimeSnapshot,
  runtimeCollectionState,
  runtimeCollectionError,
  runtimeCollectionHasSnapshot,
  loadRuntimeHistory,
  coreBaseUrl = "/v1",
  initialTab = "overview",
  onRefresh,
  onCreateAgent,
  onStartSession,
  onViewAgents,
  onViewSessions,
  onConfigureConnection,
  onOpenSession,
}: DashboardViewProps) {
  const { t, i18n } = useTranslation("pages");
  const locale = i18n.resolvedLanguage;
	const [tab, setTab] = useState<"overview" | "observability">(initialTab);
	const [nodes, setNodes] = useState<SandboxNode[] | null>(null);
	const [nodeRefresh, setNodeRefresh] = useState(0);
	useEffect(() => {
		const timer = window.setInterval(() => setNodeRefresh((value) => value + 1), 30_000);
		return () => window.clearInterval(timer);
	}, []);
	useEffect(() => {
		if (!isLocalProxyBaseUrl(coreBaseUrl)) { setNodes(null); return; }
		const controller = new AbortController();
		void (async () => {
			const config = await sandboxConsoleConfig(controller.signal);
			if (!config?.sandbox_admin) { if (!controller.signal.aborted) setNodes(null); return; }
			const client = new AdminClient();
			const result = await client.listSandboxNodes({ signal: controller.signal });
			if (!controller.signal.aborted) setNodes(result.data);
		})().catch(() => { if (!controller.signal.aborted) setNodes(null); });
		return () => controller.abort();
	}, [coreBaseUrl, nodeRefresh]);
  const snapshot = useMemo(() => buildDashboardSnapshot(agents, sessions, 6, 5), [agents, sessions]);
  const runtimeModel = useMemo(() => runtimeSnapshot
    ? buildRuntimeDashboardModel(runtimeSnapshot.sessions, runtimeSnapshot.observations)
    : null, [runtimeSnapshot]);
  const agentsAvailable = collectionHasSnapshot(agentCollectionState, agentCollectionHasSnapshot);
  const sessionsAvailable = collectionHasSnapshot(sessionCollectionState, sessionCollectionHasSnapshot);
  const runtimeAvailable = runtimeCollectionState === "ready" || runtimeCollectionHasSnapshot;
  const refreshing = agentCollectionState === "connecting" || sessionCollectionState === "connecting" || runtimeCollectionState === "connecting";
  const hasStaleSnapshot = (
    (agentCollectionState === "failed" && agentCollectionHasSnapshot) ||
    (sessionCollectionState === "failed" && sessionCollectionHasSnapshot) ||
    (runtimeCollectionState === "failed" && runtimeCollectionHasSnapshot)
  );
  const hasUnavailableSource = !agentsAvailable || !sessionsAvailable || !runtimeAvailable;
  const attentionCount = snapshot.statusCounts.requires_action + snapshot.statusCounts.failed;
  const sourceErrors: Array<readonly ["Agents" | "Sessions" | "Runtime", string | null]> = [
    agentCollectionState === "failed" ? ["Agents", agentCollectionError] as const : null,
    sessionCollectionState === "failed" ? ["Sessions", sessionCollectionError] as const : null,
    runtimeCollectionState === "failed" ? ["Runtime", runtimeCollectionError] as const : null,
  ].filter((entry): entry is readonly ["Agents" | "Sessions" | "Runtime", string | null] => entry !== null);
  const coreSourceErrors = sourceErrors.filter(([label]) => label !== "Runtime");
  const backendFailureStatuses = coreSourceErrors.map(([, error]) => backendFailureStatus(error));
  const backendUnavailable = coreSourceErrors.length > 0 && backendFailureStatuses.every(Boolean);
  const backendFailureDetail = Array.from(new Set(backendFailureStatuses.filter(Boolean))).map((status) => (
    status === "network" ? t("dashboard.backend.networkFailure") : `HTTP ${status}`
  )).join(" / ");
  const snapshotTitle = hasUnavailableSource
    ? t("dashboard.snapshotIncomplete")
    : hasStaleSnapshot
      ? t("dashboard.snapshotStale")
      : refreshing
        ? t("dashboard.snapshotRefreshing")
        : t("dashboard.snapshotReady");

  return (
    <section className="page-section dashboard-page" aria-labelledby="dashboard-heading">
      <header className="page-header dashboard-header">
        <div>
          <h1 id="dashboard-heading">{t("dashboard.title")}</h1>
          <p>{t("dashboard.subtitle")}</p>
        </div>
        <div className="page-actions">
          <button
            className="button outline"
            type="button"
            onClick={() => { setNodeRefresh((value) => value + 1); onRefresh(); }}
            disabled={refreshing}
            aria-label={t("dashboard.refreshLabel")}
          >
            <RefreshCw className={refreshing ? "refresh-spinning" : undefined} size={14} strokeWidth={1.5} aria-hidden="true" />
            {refreshing ? t("dashboard.refreshing") : t("dashboard.refresh")}
          </button>
        </div>
      </header>

      <div className="dashboard-scroll">
        <div className="dashboard-view-tabs" role="tablist" aria-label={t("dashboard.viewTabs")}>
          <button type="button" role="tab" aria-selected={tab === "overview"} onClick={() => setTab("overview")}>{t("dashboard.overviewTab")}</button>
          <button type="button" role="tab" aria-selected={tab === "observability"} onClick={() => setTab("observability")}>{t("dashboard.observabilityTab")}</button>
        </div>
        {tab === "overview" ? <>
        <section className="dashboard-overview" aria-labelledby="dashboard-overview-heading">
          <div className="dashboard-snapshot-bar">
            <div className="dashboard-snapshot-copy">
              <StatusIcon
                status={hasUnavailableSource || hasStaleSnapshot ? "failed" : refreshing ? "running" : "completed"}
                title={snapshotTitle}
              />
              <span>
                <strong id="dashboard-overview-heading">{snapshotTitle}</strong>
                <small>{t("dashboard.snapshotDetail")}</small>
              </span>
            </div>
            <div className="dashboard-source-badges" aria-label={t("dashboard.dataSources")}>
              <CollectionStateBadge label={t("dashboard.agents")} state={agentCollectionState} hasSnapshot={agentCollectionHasSnapshot} />
              <CollectionStateBadge label={t("dashboard.sessions")} state={sessionCollectionState} hasSnapshot={sessionCollectionHasSnapshot} />
              <CollectionStateBadge label={t("dashboard.runtime")} state={runtimeCollectionState} hasSnapshot={runtimeCollectionHasSnapshot} />
            </div>
          </div>

          {sourceErrors.length ? (
            <div className="dashboard-snapshot-error" role="alert">
              {backendUnavailable ? (
                <button
                  className="dashboard-backend-recovery"
                  type="button"
                  onClick={onConfigureConnection}
                  aria-label={t("dashboard.backend.label")}
                >
                  <AlertTriangle size={16} aria-hidden="true" />
                  <span>
                    <strong>{t("dashboard.backend.title")}</strong>
                    <small>{t("dashboard.backend.detail", { failure: backendFailureDetail ? ` (${backendFailureDetail})` : "" })}</small>
                  </span>
                  <span className="dashboard-backend-recovery-action">
                    {t("dashboard.backend.openGuide")}
                    <ArrowRight size={13} strokeWidth={1.7} aria-hidden="true" />
                  </span>
                </button>
              ) : (
                <>
                  <span className="dashboard-snapshot-error-copy">
                    <AlertTriangle size={14} aria-hidden="true" />
                    <span>
                      {sourceErrors.map(([label, error]) => `${t(`dashboard.${label.toLowerCase()}` as never)}: ${error || t("dashboard.backend.collectionFailed")}`).join(" · ")}
                    </span>
                  </span>
                  <button className="dashboard-connection-action" type="button" onClick={onConfigureConnection}>
                    {t("dashboard.connectionSettings")}
                    <ArrowRight size={13} strokeWidth={1.7} aria-hidden="true" />
                  </button>
                </>
              )}
            </div>
          ) : null}

          <dl className="dashboard-summary" aria-label={t("dashboard.coreSnapshot")}>
            <Metric label={t("dashboard.agents")} value={agentsAvailable ? snapshot.loadedAgentCount.toLocaleString(locale) : t("dashboard.unavailable")} detail={t("dashboard.savedDefinitions")} />
            <Metric label={t("dashboard.sessions")} value={sessionsAvailable ? snapshot.loadedSessionCount.toLocaleString(locale) : t("dashboard.unavailable")} detail={t("dashboard.inSnapshot")} />
            <Metric label={t("dashboard.activeSandboxes")} value={runtimeModel ? runtimeModel.summary.activeSandboxCount.toLocaleString(locale) : t("dashboard.unavailable")} detail={t("dashboard.currentRuntimeSnapshot")} />
            <Metric label={t("dashboard.totalTokens")} value={runtimeModel && runtimeModel.summary.totalTokens !== null ? formatDashboardTokens(runtimeModel.summary.totalTokens, locale) : t("dashboard.unavailable")} detail={t("dashboard.reportedUsage")} />
            <Metric label={t("dashboard.cpuCapacity")} value={runtimeModel && runtimeModel.summary.cpuCapacityCores !== null ? runtimeModel.summary.cpuCapacityCores.toLocaleString(locale) : t("dashboard.unavailable")} detail={t("dashboard.measuredCores")} />
            <Metric label={t("dashboard.memoryLimit")} value={runtimeModel && runtimeModel.summary.memoryLimitBytes !== null ? formatDashboardBytes(runtimeModel.summary.memoryLimitBytes) : t("dashboard.unavailable")} detail={t("dashboard.measuredLimit")} />
            <Metric label={t("dashboard.readyNodes")} value={nodes === null ? t("dashboard.unavailable") : `${nodes.filter((node) => node.online && node.provider_ready).length}/${nodes.length}`} detail={t("dashboard.nodeSnapshot")} />
          </dl>
        </section>
        </> : null}

        {tab === "observability" ? <>
        <section className="dashboard-panel dashboard-runtime-panel" aria-labelledby="dashboard-runtime-heading">
          <header>
            <div>
              <h2 id="dashboard-runtime-heading">{t("dashboard.runtimeMonitoring")}</h2><p>{t("dashboard.runtimeSubtitle")}</p>
            </div>
            {runtimeModel ? (
              <span className="dashboard-runtime-freshness">
                {t("dashboard.managedObserved", { observed: runtimeModel.summary.observedRuntimeCount, managed: runtimeModel.summary.managedRuntimeCount })}
                {runtimeModel.summary.newestResolvedAt === null ? "" : ` · ${formatDashboardTimestamp(runtimeModel.summary.newestResolvedAt, locale)}`}
              </span>
            ) : null}
          </header>
          {runtimeCollectionState === "failed" && runtimeCollectionError ? <p className="dashboard-runtime-history-error" role="status">{runtimeCollectionError}</p> : null}
          {!runtimeAvailable || !runtimeSnapshot || !runtimeModel ? (
            <p className="dashboard-empty">
              <AlertTriangle size={14} aria-hidden="true" />
              {runtimeCollectionState === "connecting"
                ? t("dashboard.loadingRuntime")
                : backendUnavailable
                  ? t("dashboard.runtimeOffline")
                  : runtimeCollectionError
                    ? t("dashboard.runtimeError", { error: runtimeCollectionError })
                    : t("dashboard.runtimeUnavailable")}
            </p>
          ) : (
            <>
              {runtimeModel.rows.length ? (
                <RuntimeObservabilityContent
                  snapshot={runtimeSnapshot}
                  stale={runtimeCollectionState === "failed" && runtimeCollectionHasSnapshot}
                  loadRuntimeHistory={loadRuntimeHistory}
                  onOpenSession={onOpenSession}
                />
              ) : (
                <p className="dashboard-empty dashboard-empty-positive">{t("dashboard.noRuntime")}</p>
              )}
            </>
          )}
        </section>
        <SystemObservabilityContent coreBaseUrl={coreBaseUrl} nodes={nodes} />
        </> : null}

        {tab === "overview" ? <>
        <div className="dashboard-primary-grid">
          <section className="dashboard-panel dashboard-attention-panel" aria-labelledby="dashboard-attention-heading">
            <header>
              <div>
                <h2 id="dashboard-attention-heading">{t("dashboard.needsAttention")}</h2><p>{t("dashboard.attentionSubtitle")}</p>
              </div>
              {sessionsAvailable && attentionCount > 0 ? <span className="dashboard-count-badge">{attentionCount}</span> : null}
            </header>
            {!sessionsAvailable ? (
              <p className="dashboard-empty"><AlertTriangle size={14} aria-hidden="true" />{t("dashboard.sessionUnavailable")}</p>
            ) : snapshot.attentionSessions.length ? (
              <AttentionList sessions={snapshot.attentionSessions} onOpenSession={onOpenSession} />
            ) : (
              <p className="dashboard-empty dashboard-empty-positive">{t("dashboard.noAttention")}</p>
            )}
            <footer>
              <button className="dashboard-text-action" type="button" onClick={onViewSessions}>
                {t("dashboard.viewSessions")} <ArrowRight size={13} aria-hidden="true" />
              </button>
            </footer>
          </section>

          <section className="dashboard-panel dashboard-quick-panel" aria-labelledby="dashboard-quick-heading">
            <header>
              <div>
                <h2 id="dashboard-quick-heading">{t("dashboard.quickActions")}</h2><p>{t("dashboard.quickSubtitle")}</p>
              </div>
            </header>
            <div className="dashboard-quick-list">
              <QuickAction icon={<Bot size={17} strokeWidth={1.6} />} title={t("dashboard.createAgent")} description={t("dashboard.createAgentDetail")} onClick={onCreateAgent} />
              <QuickAction icon={<MessageSquare size={17} strokeWidth={1.6} />} title={t("dashboard.startSession")} description={t("dashboard.startSessionDetail")} onClick={onStartSession} />
            </div>
            <footer className="dashboard-quick-footer">
              <button className="dashboard-text-action" type="button" onClick={onViewAgents}>{t("dashboard.browseAgents")}</button>
              <button className="dashboard-text-action" type="button" onClick={onViewSessions}>{t("dashboard.browseSessions")}</button>
            </footer>
          </section>
        </div>

        <section className="dashboard-panel dashboard-recent-panel" aria-labelledby="dashboard-recent-heading">
          <header>
            <div>
              <h2 id="dashboard-recent-heading">{t("dashboard.recent")}</h2><p>{t("dashboard.recentSubtitle")}</p>
            </div>
            <button className="dashboard-text-action" type="button" onClick={onViewSessions}>
              {t("dashboard.viewAll")} <ArrowRight size={13} aria-hidden="true" />
            </button>
          </header>
          {!sessionsAvailable ? (
            <p className="dashboard-empty"><AlertTriangle size={14} aria-hidden="true" />{t("dashboard.recentUnavailable")}</p>
          ) : snapshot.recentSessions.length ? (
            <RecentSessions sessions={snapshot.recentSessions} onOpenSession={onOpenSession} />
          ) : (
            <p className="dashboard-empty"><Rows3 size={14} aria-hidden="true" />{t("dashboard.noRecent")}</p>
          )}
        </section>
        </> : null}
      </div>
    </section>
  );
}
