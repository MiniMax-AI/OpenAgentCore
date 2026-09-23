import {
  AlertTriangle,
  ArrowRight,
  Bot,
  MessageSquare,
  RefreshCw,
  Rows3,
} from "lucide-react";
import { useMemo, type ReactNode } from "react";

import type { AgentSession, SavedAgent } from "@agents-core-web/agents-client";

import { StatusIcon, type StatusKind } from "../../components/StatusIcon";
import { backendFailureStatus } from "../../lib/core-readiness";
import {
  buildDashboardSnapshot,
  buildRuntimeDashboardModel,
  dashboardEnvironmentLabel,
  dashboardStatusLabel,
  formatDashboardTimestamp,
  type DashboardCollectionState,
  type DashboardSessionRow,
} from "./dashboard-model";
import { RuntimeObservabilityContent, type RuntimeHistoryLoader } from "./RuntimeObservabilityContent";
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

function collectionStateLabel(state: DashboardCollectionState, hasSnapshot: boolean): string {
  if (state === "ready") return "Ready";
  if (state === "connecting") return hasSnapshot ? "Refreshing" : "Loading";
  return hasSnapshot ? "Stale" : "Unavailable";
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
  return (
    <span className={`dashboard-source-badge dashboard-source-badge-${state}`}>
      <StatusIcon status={collectionStatusKind(state)} title={`${label} collection ${state}`} />
      <strong>{label}</strong>
      <span>{collectionStateLabel(state, hasSnapshot)}</span>
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
  return (
    <span className="dashboard-session-meta">
      <span>{session.agentLabel}</span>
      <span aria-hidden="true">·</span>
      <span
        title={session.environmentProfile === "self_hosted"
          ? "This is a Session profile, not proof that an executor is connected."
          : session.environmentProfile === "openai_hosted"
            ? "Core owns this managed placement; the label is not proof that its Runtime is ready."
            : undefined}
      >
        {dashboardEnvironmentLabel(session.environmentProfile)}
      </span>
      <span aria-hidden="true">·</span>
      <time dateTime={session.lastActiveAt === null ? undefined : new Date(session.lastActiveAt * 1_000).toISOString()}>
        {formatDashboardTimestamp(session.lastActiveAt)}
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
  return (
    <div className="dashboard-attention-list" role="list" aria-label="Sessions needing attention">
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
                  {dashboardStatusLabel(session.status)}
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
  return (
    <div className="dashboard-recent-ledger" role="table" aria-label="Recent Sessions">
      <div className="dashboard-recent-header" role="row">
        <span role="columnheader">Session</span>
        <span role="columnheader">Status</span>
        <span role="columnheader">Environment</span>
        <span role="columnheader">Last active</span>
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
              {dashboardStatusLabel(session.status)}
            </span>
          </span>
          <span role="cell">{dashboardEnvironmentLabel(session.environmentProfile)}</span>
          <time
            role="cell"
            dateTime={session.lastActiveAt === null ? undefined : new Date(session.lastActiveAt * 1_000).toISOString()}
          >
            {formatDashboardTimestamp(session.lastActiveAt)}
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
  onRefresh,
  onCreateAgent,
  onStartSession,
  onViewAgents,
  onViewSessions,
  onConfigureConnection,
  onOpenSession,
}: DashboardViewProps) {
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
    status === "network" ? "network failure" : `HTTP ${status}`
  )).join(" / ");
  const snapshotTitle = hasUnavailableSource
    ? "Snapshot incomplete"
    : hasStaleSnapshot
      ? "Using the last successful snapshot"
      : refreshing
        ? "Refreshing snapshot"
        : "Snapshot ready";

  return (
    <section className="page-section dashboard-page" aria-labelledby="dashboard-heading">
      <header className="page-header dashboard-header">
        <div>
          <h1 id="dashboard-heading">Dashboard</h1>
          <p>Agents and Sessions that may need your attention.</p>
        </div>
        <div className="page-actions">
          <button
            className="button outline"
            type="button"
            onClick={onRefresh}
            disabled={refreshing}
            aria-label="Refresh Dashboard snapshot"
          >
            <RefreshCw className={refreshing ? "refresh-spinning" : undefined} size={14} strokeWidth={1.5} aria-hidden="true" />
            {refreshing ? "Refreshing…" : "Refresh"}
          </button>
        </div>
      </header>

      <div className="dashboard-scroll">
        <section className="dashboard-overview" aria-labelledby="dashboard-overview-heading">
          <div className="dashboard-snapshot-bar">
            <div className="dashboard-snapshot-copy">
              <StatusIcon
                status={hasUnavailableSource || hasStaleSnapshot ? "failed" : refreshing ? "running" : "completed"}
                title={snapshotTitle}
              />
              <span>
                <strong id="dashboard-overview-heading">{snapshotTitle}</strong>
                <small>Latest complete paginated reads · not a live Core total or runtime-readiness signal</small>
              </span>
            </div>
            <div className="dashboard-source-badges" aria-label="Dashboard data sources">
              <CollectionStateBadge label="Agents" state={agentCollectionState} hasSnapshot={agentCollectionHasSnapshot} />
              <CollectionStateBadge label="Sessions" state={sessionCollectionState} hasSnapshot={sessionCollectionHasSnapshot} />
              <CollectionStateBadge label="Runtime" state={runtimeCollectionState} hasSnapshot={runtimeCollectionHasSnapshot} />
            </div>
          </div>

          {sourceErrors.length ? (
            <div className="dashboard-snapshot-error" role="alert">
              {backendUnavailable ? (
                <button
                  className="dashboard-backend-recovery"
                  type="button"
                  onClick={onConfigureConnection}
                  aria-label="Agent Core backend is not ready. Open Docker startup guide"
                >
                  <AlertTriangle size={16} aria-hidden="true" />
                  <span>
                    <strong>Agent Core backend is not ready</strong>
                    <small>
                      Web is running, but its local `/v1` proxy cannot reach a ready Core
                      {backendFailureDetail ? ` (${backendFailureDetail})` : ""}. Start the Docker backend, then test the connection.
                    </small>
                  </span>
                  <span className="dashboard-backend-recovery-action">
                    Open startup guide
                    <ArrowRight size={13} strokeWidth={1.7} aria-hidden="true" />
                  </span>
                </button>
              ) : (
                <>
                  <span className="dashboard-snapshot-error-copy">
                    <AlertTriangle size={14} aria-hidden="true" />
                    <span>
                      {sourceErrors.map(([label, error]) => `${label}: ${error || "Collection request failed."}`).join(" · ")}
                    </span>
                  </span>
                  <button className="dashboard-connection-action" type="button" onClick={onConfigureConnection}>
                    Connection settings
                    <ArrowRight size={13} strokeWidth={1.7} aria-hidden="true" />
                  </button>
                </>
              )}
            </div>
          ) : null}

          <dl className="dashboard-summary" aria-label="Core resource snapshot">
            <Metric label="Agents" value={agentsAvailable ? snapshot.loadedAgentCount.toLocaleString("en-US") : "Unavailable"} detail="Saved definitions" />
            <Metric label="Sessions" value={sessionsAvailable ? snapshot.loadedSessionCount.toLocaleString("en-US") : "Unavailable"} detail="In this snapshot" />
            <Metric label="In progress" value={sessionsAvailable ? snapshot.statusCounts.in_progress.toLocaleString("en-US") : "Unavailable"} detail="Core-reported status" />
            <Metric
              label="Needs attention"
              value={sessionsAvailable ? attentionCount.toLocaleString("en-US") : "Unavailable"}
              detail="Requires action or failed"
              emphasis={sessionsAvailable && attentionCount > 0}
            />
          </dl>
        </section>

        <section className="dashboard-panel dashboard-runtime-panel" aria-labelledby="dashboard-runtime-heading">
          <header>
            <div>
              <h2 id="dashboard-runtime-heading">Runtime monitoring</h2>
              <p>Current provider status · retained metrics history</p>
            </div>
            {runtimeModel ? (
              <span className="dashboard-runtime-freshness">
                {runtimeModel.summary.observedRuntimeCount}/{runtimeModel.summary.managedRuntimeCount} managed observed
                {runtimeModel.summary.newestResolvedAt === null ? "" : ` · ${formatDashboardTimestamp(runtimeModel.summary.newestResolvedAt)}`}
              </span>
            ) : null}
          </header>
          {!runtimeAvailable || !runtimeSnapshot || !runtimeModel ? (
            <p className="dashboard-empty">
              <AlertTriangle size={14} aria-hidden="true" />
              {runtimeCollectionState === "connecting"
                ? "Loading Runtime observations…"
                : backendUnavailable
                  ? "Runtime observations unavailable while the Core backend is offline."
                  : runtimeCollectionError
                    ? `Runtime observations unavailable: ${runtimeCollectionError}`
                    : "Runtime observations unavailable."}
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
                <p className="dashboard-empty dashboard-empty-positive">No Session-owned Runtime contexts in this snapshot.</p>
              )}
            </>
          )}
        </section>

        <div className="dashboard-primary-grid">
          <section className="dashboard-panel dashboard-attention-panel" aria-labelledby="dashboard-attention-heading">
            <header>
              <div>
                <h2 id="dashboard-attention-heading">Needs attention</h2>
                <p>Most recent Sessions requiring action or reporting failure.</p>
              </div>
              {sessionsAvailable && attentionCount > 0 ? <span className="dashboard-count-badge">{attentionCount}</span> : null}
            </header>
            {!sessionsAvailable ? (
              <p className="dashboard-empty"><AlertTriangle size={14} aria-hidden="true" />Session snapshot unavailable.</p>
            ) : snapshot.attentionSessions.length ? (
              <AttentionList sessions={snapshot.attentionSessions} onOpenSession={onOpenSession} />
            ) : (
              <p className="dashboard-empty dashboard-empty-positive">No Sessions currently need attention.</p>
            )}
            <footer>
              <button className="dashboard-text-action" type="button" onClick={onViewSessions}>
                View all Sessions <ArrowRight size={13} aria-hidden="true" />
              </button>
            </footer>
          </section>

          <section className="dashboard-panel dashboard-quick-panel" aria-labelledby="dashboard-quick-heading">
            <header>
              <div>
                <h2 id="dashboard-quick-heading">Quick actions</h2>
                <p>Move directly into the two common workflows.</p>
              </div>
            </header>
            <div className="dashboard-quick-list">
              <QuickAction icon={<Bot size={17} strokeWidth={1.6} />} title="Create agent" description="Start from a blank definition or template." onClick={onCreateAgent} />
              <QuickAction icon={<MessageSquare size={17} strokeWidth={1.6} />} title="Start session" description="Choose a saved Agent and Environment profile." onClick={onStartSession} />
            </div>
            <footer className="dashboard-quick-footer">
              <button className="dashboard-text-action" type="button" onClick={onViewAgents}>Browse Agents</button>
              <button className="dashboard-text-action" type="button" onClick={onViewSessions}>Browse Sessions</button>
            </footer>
          </section>
        </div>

        <section className="dashboard-panel dashboard-recent-panel" aria-labelledby="dashboard-recent-heading">
          <header>
            <div>
              <h2 id="dashboard-recent-heading">Recent activity</h2>
              <p>Latest Sessions that are not already listed under Needs attention.</p>
            </div>
            <button className="dashboard-text-action" type="button" onClick={onViewSessions}>
              View all <ArrowRight size={13} aria-hidden="true" />
            </button>
          </header>
          {!sessionsAvailable ? (
            <p className="dashboard-empty"><AlertTriangle size={14} aria-hidden="true" />Recent Sessions unavailable.</p>
          ) : snapshot.recentSessions.length ? (
            <RecentSessions sessions={snapshot.recentSessions} onOpenSession={onOpenSession} />
          ) : (
            <p className="dashboard-empty"><Rows3 size={14} aria-hidden="true" />No other recent Sessions in this snapshot.</p>
          )}
        </section>
      </div>
    </section>
  );
}
