import { MessageSquareText } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import type { AgentSession } from "@agents-core-web/agents-client";

import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, SegmentedControl, StatusDot, type Tone } from "../../components/console-ui";
import { ListToolbar, listSummary, NameCell, SearchField } from "../../components/list-ui";
import { OwnerCell, OwnerHeading } from "../ownership/OwnerCell";
import { useOwners } from "../ownership/use-owners";
import type { CoreConnectionState } from "../../lib/connection";
import { formatCompact, formatDateTime, formatRelative, MISSING } from "../../lib/format";

const statuses = ["in_progress", "requires_action", "failed", "idle"] as const;
type StatusFilter = "all" | (typeof statuses)[number];

const statusTone: Record<string, Tone> = { in_progress: "pending", requires_action: "warning", failed: "danger", idle: "neutral" };
const PAGE_SIZE = 50;

export function environmentKind(session: AgentSession): "none" | "self_hosted" | "openai_hosted" | "other" {
  const type: string | undefined = session.environment?.type;
  return type === "none" || type === "self_hosted" || type === "openai_hosted" ? type : "other";
}

export function filterSessions(
  sessions: readonly AgentSession[],
  filters: { status: StatusFilter; agentId: string; environment: string; query: string },
): AgentSession[] {
  const query = filters.query.trim().toLowerCase();
  return sessions
    .filter((session) => filters.status === "all" || session.status === filters.status)
    .filter((session) => !filters.agentId || session.agent?.id === filters.agentId)
    .filter((session) => !filters.environment || environmentKind(session) === filters.environment)
    .filter((session) => !query
      || session.id.toLowerCase().includes(query)
      || (session.agent?.name ?? "").toLowerCase().includes(query)
      || (session.agent?.model ?? "").toLowerCase().includes(query)
      || (session.error ?? "").toLowerCase().includes(query))
    .sort((a, b) => b.last_active_at - a.last_active_at || a.id.localeCompare(b.id));
}

export function SessionsLogView({
  sessions,
  state,
  error,
  onRefresh,
  onOpenSession,
  initialAgentId,
}: {
  initialAgentId?: string;
  sessions: readonly AgentSession[];
  state: CoreConnectionState;
  error: string | null;
  onRefresh: () => void;
  onOpenSession: (sessionId: string) => void;
}) {
  const { t, i18n } = useTranslation("resources");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const [status, setStatus] = useState<StatusFilter>("all");
  const [agentId, setAgentId] = useState(initialAgentId ?? "");
  const [environment, setEnvironment] = useState("");
  const [query, setQuery] = useState("");
  const [limit, setLimit] = useState(PAGE_SIZE);
  const now = Math.floor(Date.now() / 1000);

  const agents = useMemo(() => {
    const seen = new Map<string, string>();
    for (const session of sessions) if (session.agent?.id && !seen.has(session.agent.id)) seen.set(session.agent.id, session.agent.name || session.agent.id);
    return [...seen.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [sessions]);
  const counts = useMemo(() => {
    const result: Record<StatusFilter, number> = { all: sessions.length, in_progress: 0, requires_action: 0, failed: 0, idle: 0 };
    for (const session of sessions) if (session.status in result) result[session.status as StatusFilter] += 1;
    return result;
  }, [sessions]);
  const filtered = useMemo(() => filterSessions(sessions, { status, agentId, environment, query }), [agentId, environment, query, sessions, status]);
  const visible = filtered.slice(0, limit);
  const visibleIds = useMemo(() => visible.map((session) => session.id), [visible]);
  const owners = useOwners("session", visibleIds, state === "ready");

  return (
    <section className="page-section console-page" aria-labelledby="sessions-log-heading">
      <PageHeader
        headingId="sessions-log-heading"
        title={t("sessions.title")}
        help={t("sessions.description")}
        actions={<RefreshButton refreshing={state === "connecting"} onClick={onRefresh} />}
      />
      <PageBody>
        <ListToolbar label={t("filters.label")} summary={sessions.length ? listSummary(tCommon, filtered.length, sessions.length, { locale }) : undefined}>
          <SearchField value={query} onChange={(value) => { setQuery(value); setLimit(PAGE_SIZE); }} placeholder={t("sessions.search")} />
          <SegmentedControl
            label={t("filters.status")}
            value={status}
            options={(["all", ...statuses] as const).map((value) => ({ value, label: t(`status.${value}`), count: counts[value] }))}
            onChange={(value) => { setStatus(value); setLimit(PAGE_SIZE); }}
          />
          <label className="select-control">
            <span className="visually-hidden">{t("filters.agent")}</span>
            <select value={agentId} onChange={(event) => { setAgentId(event.target.value); setLimit(PAGE_SIZE); }}>
              <option value="">{t("filters.allAgents")}</option>
              {agents.map(([id, name]) => <option key={id} value={id}>{name}</option>)}
            </select>
          </label>
          <label className="select-control">
            <span className="visually-hidden">{t("filters.environment")}</span>
            <select value={environment} onChange={(event) => { setEnvironment(event.target.value); setLimit(PAGE_SIZE); }}>
              <option value="">{t("filters.allEnvironments")}</option>
              <option value="openai_hosted">{t("environment.openai_hosted")}</option>
              <option value="self_hosted">{t("environment.self_hosted")}</option>
              <option value="none">{t("environment.none")}</option>
            </select>
          </label>
        </ListToolbar>

        {state === "failed" && !sessions.length ? (
          <EmptyState title={t("sessions.loadFailed")} description={error ?? undefined} action={<button className="button outline" type="button" onClick={onRefresh}>{tCommon("actions.retry")}</button>} />
        ) : state === "connecting" && !sessions.length ? (
          <p className="page-status" role="status">{t("sessions.loading")}</p>
        ) : !filtered.length ? (
          <EmptyState icon={MessageSquareText} title={sessions.length ? t("sessions.noMatch") : t("sessions.emptyTitle")} description={sessions.length ? t("sessions.noMatchDescription") : t("sessions.emptyDescription")} />
        ) : (
          <>
            {state === "failed" ? <p className="coverage-note coverage-note-error" role="alert">{t("sessions.stale", { reason: error ?? "" })}</p> : null}
            <div className="table-frame">
              <table className="data-table">
                <thead>
                  <tr>
                    <th scope="col">{t("sessions.session")}</th>
                    <th scope="col">{t("sessions.status")}</th>
                    <th scope="col">{t("sessions.model")}</th>
                    <th scope="col">{t("sessions.environment")}</th>
                    <th scope="col" className="numeric">{t("sessions.tokens")}</th>
                    <th scope="col" className="numeric">{t("sessions.created")}</th>
                    <th scope="col" className="numeric">{t("sessions.lastActive")}</th>
                    {owners.available ? <th scope="col"><OwnerHeading /></th> : null}
                  </tr>
                </thead>
                <tbody>
                  {visible.map((session) => (
                    <tr key={session.id} className="clickable-row" onClick={() => onOpenSession(session.id)}>
                      <th scope="row">
                        <NameCell name={session.agent?.name} id={session.id} fallback={t("sessions.inlineAgent")} onOpen={() => onOpenSession(session.id)} />
                      </th>
                      <td>
                        <span className="status-with-help" onClick={(event) => event.stopPropagation()}>
                          <StatusDot tone={statusTone[session.status] ?? "neutral"} label={t(`status.${(statuses as readonly string[]).includes(session.status) ? session.status as (typeof statuses)[number] : "other"}`)} />
                          {session.status === "failed" && session.error ? <HelpTip label={t("sessions.errorLabel")}>{session.error}</HelpTip> : null}
                        </span>
                      </td>
                      <td><code>{session.agent?.model || MISSING}</code></td>
                      <td>{t(`environment.${environmentKind(session)}`)}</td>
                      <td className="numeric">{session.usage ? formatCompact(session.usage.total_tokens, locale) : MISSING}</td>
                      <td className="numeric" title={formatDateTime(session.created_at, locale)}>{formatRelative(session.created_at, now, locale)}</td>
                      <td className="numeric" title={formatDateTime(session.last_active_at, locale)}>{formatRelative(session.last_active_at, now, locale)}</td>
                      {owners.available ? <td><OwnerCell record={owners.ownerOf(session.id)} /></td> : null}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {visible.length < filtered.length ? (
              <footer className="table-footer">
                <button className="button outline" type="button" onClick={() => setLimit((value) => value + PAGE_SIZE)}>{t("sessions.more")}</button>
              </footer>
            ) : null}
          </>
        )}
      </PageBody>
    </section>
  );
}
