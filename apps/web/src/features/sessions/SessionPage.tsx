import type { AgentSession } from "@agents-core-web/agents-client";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { useFailureToast } from "../../components/Toast";
import { DetailSkeleton } from "../../components/Skeleton";
import { EmptyState, Kpi, KpiStrip, PageBody, PageHeader, RefreshButton, revealInPageBody, Section, SegmentedControl } from "../../components/console-ui";
import { CopyableId } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatClock, formatDateTime, formatInteger, MISSING } from "../../lib/format";
import { CreatorCell, ProjectName, useCreators, useProjects } from "../../lib/projects";
import { collections } from "../../lib/queries";
import { forgetDeleted } from "../resources/detail-queries";
import { ExecutorCredentialsSection } from "./ExecutorCredentialsSection";
import { SessionDeleteDialog, type SessionDeleteTarget } from "./SessionDeleteDialog";
import { sessionKey } from "./session-queries";
import { isSessionActive, turnAnchorId } from "./session-history";
import { environmentKind, isDeletable } from "./session-log";
import { SessionStatus, useWaitingFor } from "./SessionStatus";
import { hasObservableRuntime } from "./session-runtime";
import { SessionRuntimeSection } from "./SessionRuntimeSection";
import { SessionTranscript } from "./SessionTranscript";
import { SessionTurnsTable } from "./SessionTurnsTable";
import { TraceView } from "./trace/TraceView";
import { isNotFound, useSessionHistory } from "./use-session-history";
import "./sessions.css";

type HistoryView = "conversation" | "trace" | "turns";

function sessionTitle(session: AgentSession, fallback: string): string {
  return session.metadata.title?.trim() || session.agent.name?.trim() || fallback;
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error ?? "");
}

/**
 * Monitor › Session log › one Session: its facts, usage, conversation, trace,
 * Turns and (for hosted sandboxes) runtime, read-only; a self-hosted Session's
 * environment also has its executor credentials. The page polls while the
 * Session has work in flight; there is no composer and no live stream.
 */
export function SessionPage() {
  const { t, i18n } = useTranslation("sessions");
  const locale = i18n.resolvedLanguage;
  const { params, navigate, back: goBack } = useConsoleNavigation();
  const projectId = params.project;
  const sessionId = params.id;
  const { state: projects, byId } = useProjects();
  const project = projectId ? byId.get(projectId) : undefined;
  const queryClient = useQueryClient();
  const history = useSessionHistory(projectId, sessionId);
  const [view, setView] = useState<HistoryView>("conversation");
  // A jump to a failed Turn: the Turn to show once its view is on screen. Repeated jumps go through every failed Turn.
  const [jump, setJump] = useState<string | null>(null);
  const jumps = useRef(0);
  useEffect(() => {
    if (!jump) return;
    const target = document.getElementById(turnAnchorId(jump));
    if (!target) return;
    revealInPageBody(target);
    setJump(null);
  }, [jump, view]);
  const [deleteTarget, setDeleteTarget] = useState<SessionDeleteTarget | null>(null);
  const [refreshToken, setRefreshToken] = useState(0);
  const waitingFor = useWaitingFor();
  const creatorRows = useMemo(() => (projectId && sessionId ? [{ projectId, id: sessionId }] : []), [projectId, sessionId]);
  const creators = useCreators("session", creatorRows);

  const back = () => goBack("sessions");
  const backButton = (
    <button type="button" className="icon-button ghost back-button" aria-label={t("detail.back")} title={t("detail.back")} onClick={back}>
      <ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" />
    </button>
  );
  const session = history.history?.session ?? null;
  const loadedAt = history.history?.loadedAt ?? null;
  const refresh = () => {
    setRefreshToken((value) => value + 1);
    history.refresh();
    if (projectId && sessionId) void queryClient.invalidateQueries({ queryKey: [...sessionKey(projectId, sessionId), "executor-credentials"] });
  };
  const time = loadedAt ? formatClock(loadedAt, locale) : MISSING;
  const itemsError = session ? history.history?.itemsError ?? null : null;
  const turnsError = session ? history.history?.turnsError ?? null : null;
  // Failed reads that leave the last history on screen are reported in toasts.
  useFailureToast(session !== null && !history.gone && history.error ? errorText(history.error) : null, t("detail.stale"), "session-refresh");
  useFailureToast(Boolean(itemsError), t("history.itemsFailed", { reason: itemsError ?? "" }), "session-items");
  useFailureToast(Boolean(turnsError), t("history.turnsFailed", { reason: turnsError ?? "" }), "session-turns");

  let body;
  if (!projectId || !sessionId) {
    body = <EmptyState title={t("detail.missing")} hint={t("detail.missingDescription")} action={<button className="button outline" type="button" onClick={back}>{t("detail.back")}</button>} />;
  } else if (projects.status === "ready" && !project) {
    body = <EmptyState title={t("detail.projectMissing")} action={<button className="button outline" type="button" onClick={back}>{t("detail.back")}</button>} />;
  } else if (!session) {
    if (history.phase === "loading") body = <DetailSkeleton label={t("detail.loading")} />;
    else if (isNotFound(history.error)) body = <EmptyState title={t("detail.notFound")} hint={t("detail.notFoundDescription")} action={<button className="button outline" type="button" onClick={back}>{t("detail.back")}</button>} />;
    else body = <EmptyState title={t("detail.loadFailed")} description={errorText(history.error)} action={<button className="button outline" type="button" onClick={refresh}>{t("detail.retry")}</button>} />;
  } else {
    const items = history.history?.items ?? [];
    const turns = history.history?.turns ?? [];
    const failedTurns = turns.filter((turn) => turn.status === "failed");
    const jumpToFailed = () => {
      const target = failedTurns[jumps.current % failedTurns.length];
      if (!target) return;
      jumps.current += 1;
      // The conversation shows the Turn with its error; without Items, the Turn table does.
      setView(items.length ? "conversation" : "turns");
      setJump(target.id);
    };
    const usage = session.usage;
    const waiting = waitingFor(session);
    const metadata = Object.entries(session.metadata).filter(([key]) => key !== "title");
    const harness = session.agent.x_agents_core?.harness;
    const environment = session.environment as { type: string; id?: unknown; remote_url?: unknown };
    body = (
      <>
        {history.gone ? <p className="coverage-note coverage-note-error" role="alert">{t("detail.gone", { time })}</p> : null}
        <dl className="resource-facts session-facts" aria-label={t("detail.facts")}>
          <div><dt>{t("detail.id")}</dt><dd><CopyableId id={session.id} /></dd></div>
          <div><dt>{t("detail.project")}</dt><dd><ProjectName project={project} /></dd></div>
          <div><dt>{t("detail.creator")}</dt><dd><CreatorCell creator={creators.creatorOf(projectId, session.id)} /></dd></div>
          <div><dt>{t("detail.status")}</dt><dd><SessionStatus session={session} /></dd></div>
          <div>
            <dt>{t("detail.agent")}</dt>
            <dd className="session-fact-stack">
              <button type="button" className="session-link" onClick={() => navigate("agents", { project: projectId, id: session.agent.id })} aria-label={t("detail.openAgent", { name: session.agent.name || session.agent.id })}>
                {session.agent.name?.trim() || t("common.untitledAgent")}
              </button>
              <CopyableId id={session.agent.id} compact />
            </dd>
          </div>
          <div><dt>{t("detail.model")}</dt><dd><code>{session.agent.model || MISSING}</code></dd></div>
          <div><dt>{t("detail.harness")}</dt><dd>{harness ? t(`harness.${harness}`, { defaultValue: harness }) : MISSING}</dd></div>
          <div>
            <dt>{t("detail.environment")}</dt>
            <dd className="session-fact-stack">
              <span>{t(`environment.${environmentKind(session)}`)}</span>
              {typeof environment.id === "string" ? <CopyableId id={environment.id} compact /> : null}
            </dd>
          </div>
          <div><dt>{t("detail.created")}</dt><dd>{formatDateTime(session.created_at, locale)}</dd></div>
          <div><dt>{t("detail.lastActive")}</dt><dd>{formatDateTime(session.last_active_at, locale)}</dd></div>
          <div>
            <dt>{t("detail.vaults")}</dt>
            <dd className="session-fact-stack">
              {session.vault_ids.length ? session.vault_ids.map((id) => <CopyableId key={id} id={id} compact />) : MISSING}
            </dd>
          </div>
          {waiting.length ? <div><dt>{t("detail.waitingFor")}</dt><dd className="session-fact-stack">{waiting.map((entry, index) => <span key={index}>{entry}</span>)}</dd></div> : null}
          {metadata.length ? (
            <div>
              <dt>{t("detail.metadata")}</dt>
              <dd className="session-metadata">{metadata.map(([key, value]) => <code key={key}>{key}={value}</code>)}</dd>
            </div>
          ) : null}
        </dl>
        <KpiStrip label={t("kpi.label")}>
          <Kpi label={t("kpi.turns")} value={history.history?.turnsError && !turns.length ? MISSING : formatInteger(turns.length, locale)} />
          <Kpi label={t("kpi.input")} value={formatInteger(usage?.input_tokens, locale)} help={t("kpi.help")} />
          <Kpi label={t("kpi.output")} value={formatInteger(usage?.output_tokens, locale)} />
          <Kpi label={t("kpi.total")} value={formatInteger(usage?.total_tokens, locale)} />
          <Kpi label={t("kpi.cached")} value={formatInteger(usage?.input_tokens_details.cached_tokens, locale)} />
          <Kpi label={t("kpi.reasoning")} value={formatInteger(usage?.output_tokens_details.reasoning_tokens, locale)} />
        </KpiStrip>
        {environment.type === "self_hosted" && typeof environment.id === "string" ? (
          <ExecutorCredentialsSection key={environment.id} projectId={projectId} sessionId={session.id} environmentId={environment.id} remoteUrl={typeof environment.remote_url === "string" ? environment.remote_url : ""} />
        ) : null}
        <Section
          headingId="session-history-heading"
          title={t("history.title")}
          help={t("history.help")}
          actions={(
            <>
              {failedTurns.length ? (
                <button className="button outline" type="button" onClick={jumpToFailed}>{failedTurns.length > 1 ? t("history.jumpToFailedOfMany", { count: failedTurns.length }) : t("history.jumpToFailed")}</button>
              ) : null}
              <SegmentedControl
                label={t("history.view")}
                value={view}
                options={[
                  { value: "conversation", label: t("history.conversation") },
                  { value: "trace", label: t("history.trace") },
                  { value: "turns", label: t("history.turns"), count: formatInteger(turns.length, locale) },
                ]}
                onChange={setView}
              />
            </>
          )}
        >
          {view === "conversation" ? (
            items.length ? (
              <SessionTranscript turns={turns} items={items} agentName={session.agent.name || t("common.agent")} />
            ) : <EmptyState title={itemsError ? t("history.itemsFailed", { reason: itemsError }) : t("history.noItems")} />
          ) : view === "trace" ? (
            <div className="session-trace-frame">
              <TraceView
                id="session-trace"
                labelledBy="session-history-heading"
                session={session}
                turns={turns}
                items={items}
                detailState={itemsError ? "failed" : "ready"}
                detailError={itemsError}
                turnState={turnsError ? "failed" : "ready"}
                turnError={turnsError}
              />
            </div>
          ) : (
            <SessionTurnsTable turns={turns} items={items} failure={turnsError ? t("history.turnsFailed", { reason: turnsError }) : null} />
          )}
        </Section>
        {hasObservableRuntime(session) ? (
          <SessionRuntimeSection
            projectId={projectId}
            session={session}
            active={isSessionActive(session.status)}
            revision={loadedAt ?? 0}
            refreshToken={refreshToken}
          />
        ) : null}
      </>
    );
  }

  const title = session ? sessionTitle(session, t("common.untitledAgent")) : t("common.session");
  return (
    <section className="page-section console-page session-detail-page" aria-labelledby="session-detail-heading">
      <PageHeader
        headingId="session-detail-heading"
        title={<>{backButton}<span className="session-detail-title">{title}</span></>}
        actions={(
          <>
            <RefreshButton onClick={refresh} refreshing={history.refreshing} disabled={!projectId || !sessionId} updatedAt={loadedAt ? formatClock(loadedAt, locale) : null} />
            {session && project ? (
              <button
                className="button danger"
                type="button"
                disabled={!isDeletable(session)}
                title={isDeletable(session) ? undefined : t("delete.onlyIdle")}
                aria-label={t("delete.actionLabel", { id: session.id })}
                onClick={() => setDeleteTarget({ project, sessionId: session.id })}
              >
                <Trash2 size={14} aria-hidden="true" />{t("delete.action")}
              </button>
            ) : null}
          </>
        )}
      />
      <PageBody>{body}</PageBody>
      <SessionDeleteDialog target={deleteTarget} onClose={() => setDeleteTarget(null)} onUncertain={refresh} onDeleted={(target) => { setDeleteTarget(null); back(); forgetDeleted(queryClient, collections.sessions, sessionKey(target.project.id, target.sessionId)); }} />
    </section>
  );
}
