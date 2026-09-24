import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Bot,
  Clock3,
  Ellipsis,
  ExternalLink,
  HardDrive,
  MessageSquare,
  Plus,
  RefreshCw,
  Square,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import type {
  AgentCore,
  AgentSession,
  AgentTurn,
  CreateEnvironmentTemplateInput,
  EnvironmentConnectionAction,
  EnvironmentTemplate,
  FunctionCallAction,
  FunctionResultInput,
  SavedAgent,
  SessionItem,
} from "@agents-core-web/agents-client";

import type { FailedPendingSend } from "../../lib/pending-send";

import { ErrorState } from "../../components/ErrorState";
import { Skeleton } from "../../components/Skeleton";
import { StatusIcon, type StatusKind } from "../../components/StatusIcon";
import type { CoreConnectionState } from "../../lib/connection";
import { useThreadScroll } from "../../lib/use-thread-scroll";
import type { VaultCatalog } from "../vaults/vault-catalog";
import type { RuntimeDashboardSnapshot } from "../dashboard/runtime-snapshot";
import { RuntimeTrendPanel, type RuntimeHistoryLoader } from "../dashboard/RuntimeTrendPanel";
import {
  SessionStartDialog,
  type SessionStartInput,
} from "./create/SessionStartDialog";
import {
  EnvironmentConnectionNotice,
  resolveEnvironmentPresentation,
} from "./environment/EnvironmentPanel";
import { EnvironmentDialog } from "./environment/EnvironmentDialog";
import type { ListEnvironmentFiles } from "./environment/EnvironmentFilesPanel";
import type { EnvironmentObservation } from "./environment/environment-state";
import type { EnvironmentTemplateCatalog } from "./environment/environment-templates";
import { ThreadItems } from "./items/ItemRenderers";
import {
  beginLocalPendingMessage,
  hasDurablePendingMessage,
  type LocalPendingMessage,
} from "./pending-message";
import { TraceView } from "./trace/TraceView";
import type { TurnTimelineLoadState } from "./turns/TurnTimeline";
import { FunctionActionPanel } from "./actions/FunctionActionPanel";
import { SessionActionsDialog } from "./actions/SessionActionsDialog";

export type StreamState = "idle" | "connecting" | "listening" | "recovering" | "failed";
export type SessionDetailState = "idle" | "loading" | "ready" | "failed";
type SessionView = "conversation" | "trace" | "metrics";
const SESSION_METRICS_CACHE_LIMIT = 6;

export interface SessionCreateRequest {
  agentId: string | null;
  requestId: number;
}

interface SessionsViewProps {
  /** The Session console is a secondary page of the Session log. */
  onBack?: () => void;
  agents: SavedAgent[];
  agentFilter?: string | null;
  sessions: AgentSession[];
  selected: AgentSession | null;
  items: SessionItem[];
  turns?: AgentTurn[];
  busy: boolean;
  coreError: string | null;
  coreState: CoreConnectionState;
  createRequest?: SessionCreateRequest | null;
  onCreateRequestConsumed?: (request: number) => void;
  detailError: string | null;
  detailState: SessionDetailState;
  turnError?: string | null;
  turnState?: TurnTimelineLoadState;
  environmentObservation?: EnvironmentObservation | null;
  runtimeSnapshot?: RuntimeDashboardSnapshot | null;
  runtimeError?: string | null;
  runtimeStale?: boolean;
  loadRuntimeHistory?: RuntimeHistoryLoader;
  sendError?: FailedPendingSend | null;
  streamError: string | null;
  streamState: StreamState;
  selfHostedEnabled?: boolean;
  openAIHostedEnabled?: boolean;
  environmentTemplates?: EnvironmentTemplateCatalog | null;
  vaultCatalog?: VaultCatalog | null;
  onCancel: () => Promise<void>;
  onAgentFilterChange?: (agentId: string | null) => void;
  onCreateEnvironmentTemplate?: (input: CreateEnvironmentTemplateInput) => Promise<EnvironmentTemplate>;
  onCreateSession: (input: SessionStartInput) => Promise<void>;
  onDeleteSession: (sessionId: string) => Promise<boolean>;
  onCancelAndDeleteSession: (sessionId: string) => Promise<boolean>;
  onFunctionResult: (input: FunctionResultInput) => Promise<void>;
  onListEnvironmentFiles?: ListEnvironmentFiles;
  onCreateEnvironmentFile?: AgentCore["createEnvironmentFile"];
  onRefresh: () => void;
  onRetrySession: () => void;
  onRetryStream: () => void;
  onRetrieveSession: (sessionId: string) => Promise<AgentSession | undefined>;
  onSelect: (sessionId: string) => void;
  onSend: (text: string) => Promise<void>;
  onUpdateSession: (
    sessionId: string,
    baselineMetadata: Record<string, string>,
    draftMetadata: Record<string, string>,
  ) => Promise<AgentSession | undefined>;
}

const coreRuntimeSetupUrl = "https://github.com/MiniMax-AI/parsar-core/blob/main/services/agents-api/README.md#public-text-execution";

function SessionsListSkeleton() {
  const { t } = useTranslation("sessions");
  return (
    <div className="sessions-list-loading" aria-busy="true" aria-label={t("list.loading")}>
      {Array.from({ length: 5 }).map((_, index) => (
        <div className="session-loading-row" key={index}>
          <Skeleton className="skeleton-status" />
          <div className="session-loading-copy">
            <Skeleton />
            <Skeleton />
          </div>
          <Skeleton className="skeleton-session-age" />
        </div>
      ))}
    </div>
  );
}

function SessionWorkspaceSkeleton() {
  const { t } = useTranslation("sessions");
  return (
    <div className="session-workspace-loading" aria-busy="true" aria-label={t("conversation.loadingWorkspace")}>
      <div className="session-workspace-loading-header">
        <Skeleton />
        <Skeleton />
      </div>
      <div className="session-workspace-loading-body">
        <Skeleton />
        <Skeleton />
        <Skeleton />
      </div>
    </div>
  );
}

function SessionTimelineSkeleton() {
  const { t } = useTranslation("sessions");
  return (
    <div className="session-timeline-loading" aria-busy="true" aria-label={t("conversation.loadingTimeline")}>
      <Skeleton className="session-timeline-user" />
      <Skeleton className="session-timeline-agent" />
      <Skeleton className="session-timeline-user session-timeline-user-short" />
    </div>
  );
}

function sessionTitle(session: AgentSession, fallback = "Untitled Session"): string {
  return session.metadata.title || session.agent.name || fallback;
}

function relativeTime(seconds: number, locale: string): string {
  const delta = Math.max(0, Math.floor(Date.now() / 1000) - seconds);
  const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "narrow" });
  if (delta < 60) return formatter.format(0, "second");
  if (delta < 3_600) return formatter.format(-Math.floor(delta / 60), "minute");
  if (delta < 86_400) return formatter.format(-Math.floor(delta / 3_600), "hour");
  return formatter.format(-Math.floor(delta / 86_400), "day");
}

export function restoreDraftAfterFailedSend(currentDraft: string, failedDraft: string): string {
  return currentDraft || failedDraft;
}

function sessionStatusKind(status: AgentSession["status"]): StatusKind {
  if (status === "in_progress" || status === "requires_action") return "running";
  if (status === "failed") return "failed";
  return "completed";
}

function streamStatusKind(state: StreamState): StatusKind {
  if (state === "connecting" || state === "recovering") return "running";
  if (state === "listening") return "completed";
  if (state === "failed") return "failed";
  return "queued";
}

function isEnvironmentConnectionAction(value: unknown): value is EnvironmentConnectionAction {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const action = value as Record<string, unknown>;
  return action.type === "environment_connection" && typeof action.environment_id === "string" && Boolean(action.environment_id);
}

function isFunctionCallAction(value: unknown): value is FunctionCallAction {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const action = value as Record<string, unknown>;
  return action.type === "function_call" &&
    typeof action.call_id === "string" && Boolean(action.call_id) &&
    typeof action.turn_id === "string" && Boolean(action.turn_id) &&
    typeof action.name === "string" && Boolean(action.name) &&
    Object.hasOwn(action, "arguments");
}

function UnsupportedActionNotice() {
  const { t } = useTranslation("sessions");
  return (
    <section className="environment-connection-notice" aria-label={t("conversation.unsupportedAction")}>
      <div className="environment-connection-notice-heading">
        <StatusIcon status="interrupted" />
        <strong>{t("conversation.requiredActionUnavailable")}</strong>
      </div>
      <p>{t("conversation.requiredActionDetail")}</p>
    </section>
  );
}

function CancelActiveTurnButton({ busy, onCancel }: { busy: boolean; onCancel: () => void }) {
  const { t } = useTranslation("sessions");
  return (
    <button
      className="composer-action"
      type="button"
      onClick={onCancel}
      disabled={busy}
      aria-label={t("common.cancelActiveTurn")}
      title={t("common.cancelActiveTurn")}
    >
      <Square size={13} fill="currentColor" strokeWidth={1.5} />
    </button>
  );
}

function CancelOnlyBar({ busy, onCancel }: { busy: boolean; onCancel: () => void }) {
  const { t } = useTranslation("sessions");
  return (
    <section className="active-turn-cancel-bar" aria-label={t("conversation.activeTurnControls")}>
      <p>{t("conversation.cancelOnly")}</p>
      <CancelActiveTurnButton busy={busy} onCancel={onCancel} />
    </section>
  );
}

function PendingUserMessage({ message }: { message: LocalPendingMessage }) {
  const { t } = useTranslation("sessions");
  return (
    <article className="message-row user pending-message" data-send-state="sending">
      <div className="message-body">
        <div className="message-copy">{message.payload}</div>
        <span className="message-delivery-state">{t("conversation.sending")}</span>
      </div>
    </article>
  );
}

function ConversationActivity({ label }: { label: string }) {
  return (
    <div className="conversation-activity" role="status" aria-live="polite" aria-atomic="true">
      <StatusIcon status="running" />
      <span>{label}</span>
    </div>
  );
}

export function SessionsView({
  agents,
  agentFilter = null,
  sessions,
  selected,
  items,
  turns = [],
  busy,
  coreError,
  coreState,
  createRequest = null,
  onCreateRequestConsumed,
  detailError,
  detailState,
  turnError = null,
  turnState = "idle",
  environmentObservation = null,
  runtimeSnapshot = null,
  runtimeError = null,
  runtimeStale = false,
  loadRuntimeHistory,
  sendError = null,
  streamError,
  streamState,
  selfHostedEnabled = false,
  openAIHostedEnabled = false,
  environmentTemplates = null,
  vaultCatalog = null,
  onCancel,
  onAgentFilterChange,
  onCreateEnvironmentTemplate,
  onCreateSession,
  onDeleteSession,
  onCancelAndDeleteSession,
  onFunctionResult,
  onListEnvironmentFiles,
  onCreateEnvironmentFile,
  onRefresh,
  onRetrySession,
  onRetryStream,
  onRetrieveSession,
  onSelect,
  onSend,
  onUpdateSession,
  onBack,
}: SessionsViewProps) {
  const { t, i18n } = useTranslation("sessions");
  const locale = i18n.resolvedLanguage || "en";
  // Inline Agent creation remains available without a saved Agent. Saved-only
  // incompatibilities can also be repaired through explicit Session overrides.
  const newSessionUnavailableReason = null;
  const [message, setMessage] = useState("");
  const [sessionView, setSessionView] = useState<SessionView>("conversation");
  const [metricsSessionIds, setMetricsSessionIds] = useState<string[]>([]);
  const [newSessionOpen, setNewSessionOpen] = useState(false);
  const [environmentDialogSessionId, setEnvironmentDialogSessionId] = useState<string | null>(null);
  const [preselectedAgentId, setPreselectedAgentId] = useState<string | null>(null);
  const [actionSession, setActionSession] = useState<AgentSession | null>(null);
  const [viewport, setViewport] = useState<HTMLDivElement | null>(null);
  const [threadContent, setThreadContent] = useState<HTMLDivElement | null>(null);
  const [pendingMessage, setPendingMessage] = useState<LocalPendingMessage | null>(null);
  const sendingRef = useRef(false);
  const pageRef = useRef<HTMLElement>(null);
  const newSessionActionRef = useRef<HTMLButtonElement>(null);
  const lastCreateRequestRef = useRef(0);
  const conversationActionRef = useRef<HTMLButtonElement>(null);
  const restoreFocusAfterDeleteRef = useRef(false);
  const draftsBySessionRef = useRef(new Map<string, string>());
  const selectedIdRef = useRef(selected?.id ?? null);
  selectedIdRef.current = selected?.id ?? null;
  const { scrollToLatest, showScrollToLatest } = useThreadScroll(
    selected?.id ?? "",
    viewport,
    threadContent,
  );

  useEffect(() => {
    if (!createRequest || createRequest.requestId === lastCreateRequestRef.current) return;
    lastCreateRequestRef.current = createRequest.requestId;
    setPreselectedAgentId(createRequest.agentId);
    setNewSessionOpen(true);
    onCreateRequestConsumed?.(createRequest.requestId);
  }, [createRequest, onCreateRequestConsumed]);

  useEffect(() => {
    if (actionSession && !sessions.some((session) => session.id === actionSession.id)) {
      setActionSession(null);
    }
  }, [actionSession, sessions]);

  useEffect(() => {
    if (actionSession || !restoreFocusAfterDeleteRef.current) return;
    const frame = window.requestAnimationFrame(() => {
      const newSessionAction = newSessionActionRef.current?.disabled ? null : newSessionActionRef.current;
      (conversationActionRef.current ?? newSessionAction ?? pageRef.current)?.focus();
      restoreFocusAfterDeleteRef.current = false;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [actionSession, selected?.id, sessions]);

  useEffect(() => {
    const sessionId = selected?.id;
    setMessage(sessionId ? draftsBySessionRef.current.get(sessionId) ?? "" : "");
  }, [selected?.id]);

  useEffect(() => {
    const sessionId = selected?.id;
    if (!sessionId || !sendError?.payload) return;
    const restored = restoreDraftAfterFailedSend(
      draftsBySessionRef.current.get(sessionId) ?? "",
      sendError.payload,
    );
    draftsBySessionRef.current.set(sessionId, restored);
    setMessage((current) => restoreDraftAfterFailedSend(current, sendError.payload));
  }, [selected?.id, sendError]);

  const visiblePendingMessage = pendingMessage &&
    pendingMessage.sessionId === selected?.id &&
    !hasDurablePendingMessage(pendingMessage, items)
    ? pendingMessage
    : null;

  useEffect(() => {
    if (
      !pendingMessage ||
      pendingMessage.sessionId !== selected?.id ||
      !hasDurablePendingMessage(pendingMessage, items)
    ) return;
    setPendingMessage((current) => current === pendingMessage ? null : current);
  }, [items, pendingMessage, selected?.id]);

  const requiredActionsValue: unknown = selected?.required_actions;
  const requiredActionsAreValid = Array.isArray(requiredActionsValue);
  const requiredActions: unknown[] = requiredActionsAreValid ? requiredActionsValue : [];
  const environmentConnections = requiredActions.filter(isEnvironmentConnectionAction);
  const functionActions = requiredActions.filter(isFunctionCallAction);
  const unsupportedActionCount = requiredActions.length - environmentConnections.length - functionActions.length + (
    !requiredActionsAreValid || selected?.status === "requires_action" && !requiredActions.length ? 1 : 0
  );
  const showCancelOnly = Boolean(
    (selected?.status === "in_progress" || selected?.status === "requires_action") &&
    (unsupportedActionCount > 0 || environmentConnections.length > 0 && functionActions.length === 0),
  );
  const environmentPresentation = selected
    ? resolveEnvironmentPresentation(selected.environment, environmentObservation, environmentConnections)
    : null;
  const inputBlockedByTerminalEnvironment = Boolean(
    environmentPresentation?.visible &&
    (environmentPresentation.status === "failed" || environmentPresentation.status === "expired"),
  );
  const retainedMetricsSessionIds = useMemo(() => {
    if (!selected) return metricsSessionIds;
    return [selected.id, ...metricsSessionIds.filter((sessionId) => sessionId !== selected.id)]
      .slice(0, SESSION_METRICS_CACHE_LIMIT);
  }, [metricsSessionIds, selected]);
  useEffect(() => {
    if (!selected) return;
    setMetricsSessionIds((current) => {
      const next = [selected.id, ...current.filter((sessionId) => sessionId !== selected.id)]
        .slice(0, SESSION_METRICS_CACHE_LIMIT);
      return next.length === current.length && next.every((sessionId, index) => sessionId === current[index])
        ? current
        : next;
    });
  }, [selected]);
  const sessionRuntimeSnapshots = useMemo(() => {
    const result = new Map<string, RuntimeDashboardSnapshot>();
    if (!runtimeSnapshot) return result;
    const runtimeSessions = new Map(runtimeSnapshot.sessions.map((session) => [session.id, session]));
    const observations = new Map(runtimeSnapshot.observations.map((observation) => [observation.session_id, observation]));
    for (const sessionId of retainedMetricsSessionIds) {
      const runtimeSession = runtimeSessions.get(sessionId);
      const observation = observations.get(sessionId);
      if (runtimeSession && observation) {
        result.set(sessionId, { sessions: [runtimeSession], observations: [observation], loadedAt: runtimeSnapshot.loadedAt });
      }
    }
    return result;
  }, [retainedMetricsSessionIds, runtimeSnapshot]);
  const sessionRuntimeSnapshot = selected ? sessionRuntimeSnapshots.get(selected.id) ?? null : null;

  const send = async (event?: FormEvent) => {
    event?.preventDefault();
    const value = message.trim();
    if (
      sendingRef.current ||
      busy ||
      !value ||
      !selected ||
      detailState !== "ready" ||
      streamState !== "listening" ||
      inputBlockedByTerminalEnvironment
    ) return;
    const sessionId = selected.id;
    const pending = beginLocalPendingMessage(sessionId, value, items);
    sendingRef.current = true;
    draftsBySessionRef.current.set(sessionId, "");
    setMessage("");
    setPendingMessage(pending);
    try {
      await onSend(value);
      if (selectedIdRef.current === sessionId) scrollToLatest();
    } catch {
      const restored = restoreDraftAfterFailedSend(
        draftsBySessionRef.current.get(sessionId) ?? "",
        value,
      );
      draftsBySessionRef.current.set(sessionId, restored);
      if (selectedIdRef.current === sessionId) {
        setMessage((current) => {
          const next = restoreDraftAfterFailedSend(current, value);
          draftsBySessionRef.current.set(sessionId, next);
          return next;
        });
      }
    } finally {
      setPendingMessage((current) => current === pending ? null : current);
      sendingRef.current = false;
    }
  };

  const onComposerKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.nativeEvent.isComposing) return;
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void send();
    }
  };

  const cancel = () => {
    void onCancel().catch(() => undefined);
  };

  const onViewTabKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    const tabs = Array.from(event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>("[role=tab]") ?? []);
    const index = tabs.indexOf(event.currentTarget);
    if (index < 0) return;
    const target = event.key === "ArrowRight"
      ? tabs[(index + 1) % tabs.length]
      : event.key === "ArrowLeft"
        ? tabs[(index - 1 + tabs.length) % tabs.length]
        : event.key === "Home"
          ? tabs[0]
          : event.key === "End"
            ? tabs[tabs.length - 1]
            : null;
    if (!target) return;
    event.preventDefault();
    target.click();
    target.focus();
  };

  return (
    <section ref={pageRef} className="page-section session-page" tabIndex={-1}>
      <aside className="session-browser">
        <header className="session-browser-header">
          <div className="session-browser-title">
            {onBack ? <button className="session-back" type="button" onClick={onBack}><ArrowLeft size={14} strokeWidth={1.6} aria-hidden="true" />{t("list.back")}</button> : null}
            <h1>{t("list.title")}</h1>
          </div>
          <div className="session-browser-actions">
            <button className="icon-button ghost" type="button" onClick={onRefresh} disabled={coreState === "connecting"} aria-label={t("list.recover")}>
              <RefreshCw className={coreState === "connecting" ? "refresh-spinning" : undefined} size={14} strokeWidth={1.5} />
            </button>
            <span className="action-tooltip">
              <button
                ref={newSessionActionRef}
                className="icon-button primary session-create-trigger"
                type="button"
                onClick={() => {
                  if (!newSessionUnavailableReason) {
                    setPreselectedAgentId(null);
                    setNewSessionOpen(true);
                  }
                }}
                disabled={coreState !== "ready"}
                aria-disabled={newSessionUnavailableReason ? true : undefined}
                aria-describedby={newSessionUnavailableReason ? "new-session-unavailable-reason" : undefined}
                aria-label={t("list.new")}
              >
                <Plus size={14} strokeWidth={1.5} />
              </button>
              {newSessionUnavailableReason ? (
                <span className="action-tooltip-content" role="tooltip" id="new-session-unavailable-reason">
                  {newSessionUnavailableReason}
                </span>
              ) : null}
            </span>
          </div>
        </header>

        <label className="session-agent-filter">
          <Bot size={13} strokeWidth={1.5} aria-hidden="true" />
          <span className="sr-only">{t("list.filterByAgent")}</span>
          <select
            aria-label={t("list.filterByAgent")}
            value={agentFilter ?? ""}
            onChange={(event) => onAgentFilterChange?.(event.target.value || null)}
            disabled={!agents.length}
          >
            <option value="">{t("list.allAgents")}</option>
            {agentFilter && !agents.some((agent) => agent.id === agentFilter) ? (
              <option value={agentFilter}>{t("list.unavailableAgent")}</option>
            ) : null}
            {agents.map((agent) => (
              <option value={agent.id} key={agent.id}>{agent.name || agent.model}</option>
            ))}
          </select>
        </label>

        <div className="session-list-heading">
          <span>{t("list.recent")}</span>
          <span>{coreState === "ready" || sessions.length ? sessions.length.toLocaleString(locale) : "—"}</span>
        </div>
        <div className="session-list">
          {coreState === "connecting" && !sessions.length ? <SessionsListSkeleton /> : null}
          {coreState === "failed" && sessions.length ? (
            <ErrorState
              className="session-collection-error"
              title={t("list.refreshFailed")}
              description={t("list.lastLoadedAvailable")}
              detail={coreError ?? undefined}
              hint={t("list.checkConnection")}
              onRetry={onRefresh}
            />
          ) : null}
          {coreState === "ready" || sessions.length ? sessions.map((session) => (
            <div
              className={`session-row ${selected?.id === session.id ? "active" : ""}`}
              key={session.id}
            >
              <button
                type="button"
                className="session-row-select"
                aria-label={t("list.sessionLabel", { status: t(`status.${session.status}` as never), agent: session.agent.name || session.agent.model, title: sessionTitle(session, t("common.untitledSession")) })}
                onClick={() => onSelect(session.id)}
              >
                <StatusIcon status={sessionStatusKind(session.status)} title={t(`status.${session.status}` as never)} />
                <span className="session-row-copy">
                  <strong>{sessionTitle(session, t("common.untitledSession"))}</strong>
                  <small>{session.agent.name || session.agent.model}</small>
                </span>
                <span className="session-age">{relativeTime(session.last_active_at, locale)}</span>
              </button>
              <button
                className="session-row-action icon-button ghost"
                type="button"
                aria-label={t("list.manage", { title: sessionTitle(session, t("common.untitledSession")) })}
                title={t("list.detailsActions")}
                disabled={busy}
                onClick={() => setActionSession(session)}
              >
                <Ellipsis size={14} strokeWidth={1.5} aria-hidden="true" />
              </button>
            </div>
          )) : null}
          {coreState === "ready" && !sessions.length ? (
            <div className="session-list-empty">
              <MessageSquare size={20} strokeWidth={1.5} />
              <span>{t("list.none")}</span>
            </div>
          ) : null}
        </div>
      </aside>

      {coreState === "connecting" && !selected ? (
        <SessionWorkspaceSkeleton />
      ) : coreState === "failed" && !selected ? (
        <div className="workspace-error">
          <ErrorState
            title={t("list.loadFailed")}
            description={t("list.readFailed")}
            detail={coreError ?? undefined}
            hint={t("list.checkSidebarConnection")}
            onRetry={onRefresh}
          />
        </div>
      ) : selected ? (
        <div className="conversation-panel">
          <header className="conversation-header">
            <div className="conversation-heading-copy">
              <div className="conversation-title-row">
                <h2>{sessionTitle(selected, t("common.untitledSession"))}</h2>
                <StatusIcon status={sessionStatusKind(selected.status)} />
                <span className="status-label">{t(`status.${selected.status}` as never)}</span>
              </div>
              <p>{selected.agent.name || t("common.untitledAgent")} <span>·</span> <code>{selected.agent.model}</code></p>
            </div>
            <div className="conversation-header-actions">
              {environmentPresentation?.visible ? (
                <button
                  className={`environment-trigger environment-trigger-${environmentPresentation.status}`}
                  type="button"
                  aria-haspopup="dialog"
                  aria-expanded={environmentDialogSessionId === selected.id}
                  aria-label={environmentPresentation.status === "required" ? t("environment.triggerConnect") : t("environment.trigger", { status: t(`environment.status.${environmentPresentation.status}` as never) })}
                  title={t("environment.viewStatus")}
                  onClick={() => setEnvironmentDialogSessionId(selected.id)}
                >
                  <HardDrive size={14} strokeWidth={1.5} aria-hidden="true" />
                  <span>{environmentPresentation.status === "required" ? t("environment.triggerConnect") : t("environment.trigger", { status: t(`environment.status.${environmentPresentation.status}` as never) })}</span>
                </button>
              ) : null}
              <div
                className={`stream-indicator ${streamState}`}
                role="status"
                aria-live="polite"
                aria-label={t("conversation.liveEventsAria", { status: t(`stream.${streamState}` as never) })}
                title={t("conversation.liveEventsTitle")}
              >
                <StatusIcon status={streamStatusKind(streamState)} />
                <span>{t(`stream.${streamState}` as never)}</span>
              </div>
              <button
                ref={conversationActionRef}
                className="icon-button ghost conversation-session-action"
                type="button"
                aria-label={t("list.manage", { title: sessionTitle(selected, t("common.untitledSession")) })}
                title={t("list.detailsActions")}
                disabled={busy}
                onClick={() => setActionSession(selected)}
              >
                <Ellipsis size={14} strokeWidth={1.5} aria-hidden="true" />
              </button>
            </div>
          </header>

          {environmentPresentation?.visible ? (
            <EnvironmentDialog
              open={environmentDialogSessionId === selected.id}
              onClose={() => setEnvironmentDialogSessionId(null)}
              environment={selected.environment}
              observation={environmentObservation}
              connectionActions={environmentConnections}
              defaultLauncherGuideOpen={environmentPresentation.defaultLauncherGuideOpen}
              onListFiles={onListEnvironmentFiles}
              onCreateFile={onCreateEnvironmentFile}
            />
          ) : null}

          <div className="session-view-tabs" role="tablist" aria-label={t("conversation.sessionView")}>
            <button
              id="session-conversation-tab"
              type="button"
              role="tab"
              aria-controls="session-conversation-panel"
              aria-selected={sessionView === "conversation"}
              tabIndex={sessionView === "conversation" ? 0 : -1}
              onClick={() => setSessionView("conversation")}
              onKeyDown={onViewTabKeyDown}
            >
              {t("conversation.conversation")}
            </button>
            <button
              id="session-trace-tab"
              type="button"
              role="tab"
              aria-controls="session-trace-panel"
              aria-selected={sessionView === "trace"}
              tabIndex={sessionView === "trace" ? 0 : -1}
              onClick={() => setSessionView("trace")}
              onKeyDown={onViewTabKeyDown}
            >
              {t("conversation.trace")}
            </button>
            <button
              id="session-metrics-tab"
              type="button"
              role="tab"
              aria-controls="session-metrics-panel"
              aria-selected={sessionView === "metrics"}
              tabIndex={sessionView === "metrics" ? 0 : -1}
              onClick={() => setSessionView("metrics")}
              onKeyDown={onViewTabKeyDown}
            >
              {t("conversation.metrics")}
            </button>
          </div>

          <div
            className="session-view-panel conversation-view-panel"
            id="session-conversation-panel"
            role="tabpanel"
            aria-labelledby="session-conversation-tab"
            hidden={sessionView !== "conversation"}
          >
          <div className="conversation-thread-frame">
            <div ref={setViewport} className="conversation-scroll">
              <div ref={setThreadContent} className="thread-content">
                <div className="session-origin">
                  <Clock3 size={13} strokeWidth={1.5} />
                  <span>{t("common.session")}</span>
                  <code>{selected.id}</code>
                </div>

                <div className="message-stack">
                  <ThreadItems items={items} agentName={selected.agent.name || t("common.agent")} />
                  {visiblePendingMessage ? <PendingUserMessage message={visiblePendingMessage} /> : null}
                </div>

                {detailState === "loading" && !items.length ? <SessionTimelineSkeleton /> : null}

                {detailState === "failed" ? (
                  <ErrorState
                    className="session-detail-error"
                    title={t("conversation.sessionLoadFailed")}
                    description={t("conversation.sessionReadFailed")}
                    detail={detailError ?? undefined}
                    hint={items.length ? t("conversation.lastItemsVisible") : t("list.checkConnection")}
                    onRetry={onRetrySession}
                  />
                ) : null}

                {streamState === "failed" && streamError ? (
                  <ErrorState
                    className="session-stream-error"
                    title={t("conversation.eventsOpenFailed")}
                    description={t("conversation.eventsRejected")}
                    detail={streamError}
                    hint={t("conversation.eventsRetryHint")}
                    onRetry={onRetryStream}
                  />
                ) : null}

                {sendError ? (
                  <ErrorState
                    className="session-send-error"
                    title={sendError.code === "execution_unavailable" ? t("conversation.executionUnavailable") : t("conversation.messageNotSent")}
                    description={sendError.code === "execution_unavailable"
                      ? t("conversation.executionRejected")
                      : sendError.uncertain
                        ? t("conversation.messageUncertain")
                        : t("conversation.messageRejected")}
                    detail={sendError.message}
                    hint={sendError.code === "execution_unavailable"
                      ? t("conversation.executionRetryHint")
                      : sendError.uncertain
                        ? t("conversation.uncertainRetryHint")
                        : t("conversation.rejectedRetryHint")}
                    action={sendError.code === "execution_unavailable" ? (
                      <a className="button outline" href={coreRuntimeSetupUrl} target="_blank" rel="noreferrer">
                        {t("conversation.coreRuntimeSetup")}
                        <ExternalLink size={13} strokeWidth={1.5} aria-hidden="true" />
                      </a>
                    ) : undefined}
                  />
                ) : null}

                {selected.status === "failed" ? (
                  <ErrorState
                    className="session-runtime-error"
                    title={inputBlockedByTerminalEnvironment ? t("conversation.cannotContinue") : t("conversation.latestFailed")}
                    description={inputBlockedByTerminalEnvironment
                      ? t("conversation.environmentTerminal")
                      : t("conversation.latestFailedDetail")}
                    detail={selected.error ?? undefined}
                  />
                ) : null}

                {detailState === "ready" && streamState !== "failed" && selected.status !== "failed" && !items.length && !visiblePendingMessage ? (
                  <div className="conversation-empty">
                    <Bot size={24} strokeWidth={1.5} />
                    <h3>{t("conversation.ready")}</h3>
                    <p>
                      {streamState === "listening"
                        ? t("conversation.liveConnected")
                        : t("conversation.openingStream")}
                    </p>
                  </div>
                ) : null}
              </div>
            </div>
            {showScrollToLatest ? (
              <button className="button outline scroll-to-latest" type="button" onClick={scrollToLatest}>
                <ArrowDown size={14} strokeWidth={1.5} aria-hidden="true" />
                {t("conversation.backLatest")}
              </button>
            ) : null}
          </div>

          <footer className="composer-footer">
            {environmentConnections.map((action, index) => (
              <EnvironmentConnectionNotice
                action={action}
                key={`${action.environment_id}:${index}`}
                onOpenSetup={environmentPresentation?.visible ? () => setEnvironmentDialogSessionId(selected.id) : undefined}
              />
            ))}
            {unsupportedActionCount ? <UnsupportedActionNotice /> : null}
            {selected.status === "in_progress" ? (
              <ConversationActivity label={t("conversation.agentWorking", { agent: selected.agent.name || t("common.agent") })} />
            ) : visiblePendingMessage ? (
              <ConversationActivity label={t("conversation.sendingMessage")} />
            ) : null}
            {showCancelOnly ? (
              <CancelOnlyBar busy={busy} onCancel={cancel} />
            ) : null}
            {!unsupportedActionCount && functionActions.length ? (
                <FunctionActionPanel
                  actions={functionActions}
                  agentName={selected.agent.name || t("common.agent")}
                  autoFocus={!environmentConnections.length && !unsupportedActionCount}
                  busy={busy || detailState !== "ready"}
                  onCancel={cancel}
                  onSubmit={onFunctionResult}
                />
            ) : environmentConnections.length || unsupportedActionCount ? null : (
              <form className="composer" onSubmit={(event) => void send(event)}>
              <textarea
                value={message}
                onChange={(event) => {
                  const next = event.target.value;
                  setMessage(next);
                  draftsBySessionRef.current.set(selected.id, next);
                  const element = event.currentTarget;
                  element.style.height = "auto";
                  element.style.height = `${Math.min(element.scrollHeight, 200)}px`;
                }}
                onKeyDown={onComposerKeyDown}
                placeholder={inputBlockedByTerminalEnvironment
                  ? t("conversation.startNewToContinue")
                  : t("conversation.messageAgentPlaceholder", { agent: selected.agent.name || t("conversation.theAgent") })}
                aria-label={t("conversation.messageAgent")}
                rows={1}
                disabled={detailState !== "ready" || inputBlockedByTerminalEnvironment}
              />
              <div className="composer-bar">
                <span className="composer-context">
                  <span className="initial-tile">{(selected.agent.name?.charAt(0) || "A").toUpperCase()}</span>
                  <span>{selected.agent.name || t("common.untitledAgent")}</span>
                </span>
                {selected.status === "in_progress" || selected.status === "requires_action" ? (
                  <CancelActiveTurnButton busy={busy} onCancel={cancel} />
                ) : (
                  <button
                    className="composer-action send"
                    type="submit"
                    aria-label={t("conversation.sendMessage")}
                    title={t("conversation.sendMessage")}
                    disabled={busy || detailState !== "ready" || !message.trim() || inputBlockedByTerminalEnvironment || streamState !== "listening"}
                  >
                    <ArrowUp size={16} strokeWidth={2} />
                  </button>
                )}
              </div>
              </form>
            )}
          </footer>
          </div>
          {sessionView === "trace" ? (
            <TraceView
              id="session-trace-panel"
              labelledBy="session-trace-tab"
              session={selected}
              turns={turns}
              items={items}
              detailState={detailState}
              detailError={detailError}
              turnState={turnState}
              turnError={turnError}
            />
          ) : (
            <div
              id="session-trace-panel"
              role="tabpanel"
              aria-labelledby="session-trace-tab"
              hidden
            />
          )}
          <div
            className="session-view-panel session-metrics-panel"
            id="session-metrics-panel"
            role="tabpanel"
            aria-labelledby="session-metrics-tab"
            hidden={sessionView !== "metrics"}
          >
            {loadRuntimeHistory ? retainedMetricsSessionIds.map((sessionId) => {
              const cachedSnapshot = sessionRuntimeSnapshots.get(sessionId);
              return cachedSnapshot ? (
                <div key={sessionId} hidden={sessionId !== selected.id}>
                  <RuntimeTrendPanel
                    snapshot={cachedSnapshot}
                    stale={runtimeStale ?? false}
                    loadRuntimeHistory={loadRuntimeHistory}
                    headingId={`session-runtime-trends-heading-${sessionId}`}
                    title={t("metrics.resourceTrends")}
                    allowSourceSelection
                    activeDisplay="binary"
                  />
                </div>
              ) : null;
            }) : null}
            {sessionRuntimeSnapshot ? null : runtimeError ? (
              <ErrorState
                title={t("metrics.loadFailed")}
                description={t("metrics.observationUnavailable")}
                detail={runtimeError}
                onRetry={onRefresh}
              />
            ) : (
              <div className="session-metrics-loading" aria-busy="true" aria-label={t("metrics.loading")}>
                <Skeleton />
                <Skeleton />
                <Skeleton />
                <Skeleton />
              </div>
            )}
          </div>
        </div>
      ) : (
        <div className="workspace-empty">
          <MessageSquare size={24} strokeWidth={1.5} />
          <h2>{t("list.selectOrCreate")}</h2>
          <p>{t("list.emptyDescription")}</p>
        </div>
      )}

      <SessionStartDialog
        agents={agents}
        disabled={busy || coreState !== "ready"}
        open={newSessionOpen}
        preselectedAgentId={preselectedAgentId}
        selfHostedEnabled={selfHostedEnabled}
        openAIHostedEnabled={openAIHostedEnabled}
        environmentTemplates={environmentTemplates}
        vaultCatalog={vaultCatalog}
        onClose={() => setNewSessionOpen(false)}
        onCreateEnvironmentTemplate={onCreateEnvironmentTemplate}
        onSubmit={onCreateSession}
      />
      <SessionActionsDialog
        busy={busy}
        session={actionSession}
        onClose={() => setActionSession(null)}
        onDelete={onDeleteSession}
        onCancelAndDelete={onCancelAndDeleteSession}
        onDeleted={(sessionId) => {
          draftsBySessionRef.current.delete(sessionId);
          if (selectedIdRef.current === sessionId) setMessage("");
          restoreFocusAfterDeleteRef.current = true;
        }}
        onRetrieve={onRetrieveSession}
        onUpdate={onUpdateSession}
      />
    </section>
  );
}
