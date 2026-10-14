import type { AgentSession, AgentTurn } from "@oac/agents-client";
import { queryOptions, useQuery } from "@tanstack/react-query";
import { createContext, useContext } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { diagnosticFailure, diagnosticNext, diagnosticReason } from "../../lib/diagnostic-failure";
import { admin } from "../../lib/projects";
import { sessionKey } from "./session-queries";

type Scope = { projectId: string; sessionId: string };
export const DiagnosticScope = createContext<Scope | null>(null);

export function sessionDiagnosticsQuery(projectId: string, session: AgentSession) {
  return queryOptions({
    queryKey: [...sessionKey(projectId, session.id), "diagnostics", "session", session.status, session.last_active_at],
    queryFn: ({ signal }) => admin.retrieveSessionDiagnostics(projectId, session.id, { signal }),
    enabled: session.status === "failed",
    retry: false,
    staleTime: 5_000,
  });
}

export function turnDiagnosticsQuery(scope: Scope, turn: AgentTurn) {
  return queryOptions({
    queryKey: [...sessionKey(scope.projectId, scope.sessionId), "diagnostics", "turn", turn.id, turn.status, turn.completed_at],
    queryFn: ({ signal }) => admin.retrieveTurnDiagnostics(scope.projectId, scope.sessionId, turn.id, { signal }),
    retry: false,
    staleTime: 5_000,
    refetchInterval: ["queued", "in_progress", "waiting"].includes(turn.status) ? 5_000 : false,
  });
}

export function DiagnosticsUnavailable({ pending, retry }: { pending: boolean; retry: () => void }) {
  const { t } = useTranslation("diagnostics");
  return <span className="status-with-help" role="status">
    {pending ? <span className="skeleton-bar" aria-label={t("checking")} /> : <span>{t("unavailable")}</span>}
    {!pending ? <><HelpTip label={t("failureLabel")}>{t("unavailableHelp")}</HelpTip><button className="text-action" type="button" onClick={retry}>{t("retry")}</button></> : null}
  </span>;
}

function useSessionFailure(projectId: string, session: AgentSession) {
  const query = useQuery(sessionDiagnosticsQuery(projectId, session));
  return { query, failure: !query.isError && query.data?.status === session.status ? query.data.failure : null };
}

/**
 * Mounted for failed rows on the current page only; no fan-out over hidden
 * Sessions. Lists show the reason alone and keep its next step in the title.
 */
export function SessionFailure({ projectId, session }: { projectId: string; session: AgentSession }) {
  const { t } = useTranslation("diagnostics");
  const { query, failure } = useSessionFailure(projectId, session);
  return <span className="session-status-reason" title={failure ? diagnosticFailure(failure, t) : undefined}>
    {failure ? diagnosticReason(failure, t) : <DiagnosticsUnavailable pending={query.isPending} retry={() => void query.refetch()} />}
  </span>;
}

/** The Session page's failure, under its facts: the reason, then its next step. */
export function SessionFailureDetails({ projectId, session }: { projectId: string; session: AgentSession }) {
  const { t } = useTranslation("diagnostics");
  const { query, failure } = useSessionFailure(projectId, session);
  const next = failure ? diagnosticNext(failure, t) : "";
  return <section className="session-failure" aria-label={t("failureLabel")}>
    {failure ? <><p className="session-failure-reason">{diagnosticReason(failure, t)}</p>{next ? <p>{next}</p> : null}</> : <DiagnosticsUnavailable pending={query.isPending} retry={() => void query.refetch()} />}
  </section>;
}

function ScopedTurnFailure({ turn, scope }: { turn: AgentTurn; scope: Scope }) {
  const { t } = useTranslation("diagnostics");
  const query = useQuery(turnDiagnosticsQuery(scope, turn));
  const failure = !query.isError && query.data?.status === turn.status ? query.data.failure : null;
  return <span className="session-status-reason">{failure ? diagnosticFailure(failure, t) : <DiagnosticsUnavailable pending={query.isPending} retry={() => void query.refetch()} />}</span>;
}

export function TurnFailure({ turn }: { turn: AgentTurn }) {
  const scope = useContext(DiagnosticScope);
  if (turn.status !== "failed") return null;
  return scope ? <ScopedTurnFailure scope={scope} turn={turn} /> : <span className="session-status-reason">{turn.error?.message}</span>;
}
