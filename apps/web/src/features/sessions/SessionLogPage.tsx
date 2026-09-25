import { MessageSquareText } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { useFailureToast } from "../../components/Toast";
import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, SegmentedControl } from "../../components/console-ui";
import { ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatClock, formatCompact, formatDateTime, formatInteger, formatRelative, MISSING } from "../../lib/format";
import { CreatorCell, CreatorHeading, ProjectFilter, ProjectName, useCreators, useProjectCollection, useProjects, type Creators, type Owned, type ProjectFilterValue } from "../../lib/projects";
import { SessionDeleteDialog, type SessionDeleteTarget } from "./SessionDeleteDialog";
import { SessionStatus } from "./SessionStatus";
import {
  agentOptions,
  environmentKind,
  environmentKinds,
  filterSessionLog,
  initialSessionLogFilters,
  isDeletable,
  isLogTruncated,
  readSessionLog,
  sessionStatuses,
  statusCounts,
  type SessionLogEntry,
  type SessionLogFilters,
} from "./session-log";
import "./sessions.css";
import { type Project } from "../../lib/admin-view";
import { collections, queryClient } from "../../lib/queries";
import { forgetDeleted } from "../resources/detail-queries";
import { sessionKey } from "./session-queries";
import { TableSkeleton } from "../../components/Skeleton";
import { ConsoleSelect } from "../../components/console-select";

const PAGE_SIZE = 50;


/** Filters survive a visit to a Session and back within the same page load. */
let remembered: { project: ProjectFilterValue; filters: SessionLogFilters } = { project: "", filters: initialSessionLogFilters };

function rowKey(row: Owned<SessionLogEntry>): string {
  return `${row.project.id}:${row.value.kind === "session" ? row.value.session.id : row.value.key}`;
}

/** Monitor › Session log: every Session of one project or of all projects, read-only with deletion of idle ones. */
export function SessionLogPage() {
  const { t, i18n } = useTranslation("sessions");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const { params, navigate } = useConsoleNavigation();
  const { state: projects, byId, refresh: refreshProjects } = useProjects();
  const [project, setProject] = useState<ProjectFilterValue>(() => params.project ?? remembered.project);
  // A linked ID (a Session or an Agent) prefills the search, like the other lists.
  const [filters, setFilters] = useState<SessionLogFilters>(() => (params.id ? { ...initialSessionLogFilters, query: params.id } : remembered.filters));
  const [limit, setLimit] = useState(PAGE_SIZE);
  const [deleted, setDeleted] = useState<ReadonlySet<string>>(() => new Set());
  const [deleteTarget, setDeleteTarget] = useState<SessionDeleteTarget | null>(null);
  const [loadedAt, setLoadedAt] = useState<number | null>(null);

  useEffect(() => { remembered = { project, filters }; }, [project, filters]);

  // A remembered or linked project that no longer exists falls back to every project.
  const selected = project && projects.status === "ready" && !byId.has(project) ? "" : project;
  const collection = useProjectCollection(collections.sessions, selected);
  useEffect(() => { if (collection.status === "ready") setLoadedAt(Date.now()); }, [collection.status, collection.items]);

  // Switching from every project to one narrows the rows at once; the reload follows.
  const rows = useMemo(
    () => collection.items.filter((row) => (!selected || row.project.id === selected) && !deleted.has(rowKey(row))),
    [collection.items, deleted, selected],
  );
  const filtered = useMemo(() => filterSessionLog(rows, filters), [filters, rows]);
  const counts = useMemo(() => statusCounts(rows, filters), [filters, rows]);
  const agents = useMemo(() => agentOptions(rows, t("common.untitledAgent")), [rows, t]);
  const visible = useMemo(() => filtered.slice(0, limit), [filtered, limit]);
  const creatorRows = useMemo(() => visible.flatMap((row) => (row.value.kind === "session" ? [{ projectId: row.project.id, id: row.value.session.id }] : [])), [visible]);
  const creators = useCreators("session", creatorRows);
  const allProjects = selected === "";
  const loading = collection.status === "loading" || (projects.status === "loading" && !projects.projects.length);
  const now = Math.floor(Date.now() / 1000);

  const update = (patch: Partial<SessionLogFilters>) => {
    setFilters((current) => ({ ...current, ...patch }));
    setLimit(PAGE_SIZE);
  };
  const refresh = () => {
    setDeleted(new Set());
    if (projects.status === "failed") refreshProjects();
    collection.refresh();
  };
  const open = (projectId: string, sessionId: string) => navigate("session", { project: projectId, id: sessionId });
  const failures = collection.failures;
  const allFailed = collection.status === "ready" && !rows.length && failures.length > 0 && failures.length >= (allProjects ? projects.projects.length : 1);

  useFailureToast(failures.length > 0 && !allFailed, tCommon("project.partial", { names: failures.map((failure) => failure.project.name).join(", ") }), "sessions-partial");
  let body;
  if (projects.status === "failed" && !projects.projects.length) {
    body = <EmptyState title={t("log.loadFailed")} description={projects.error} action={<button className="button outline" type="button" onClick={refresh}>{tCommon("actions.retry")}</button>} />;
  } else if (loading && !rows.length) {
    body = <TableSkeleton label={t("log.loading")} rows={8} columns={8} />;
  } else if (allFailed) {
    body = <EmptyState title={t("log.loadFailed")} description={failures[0]?.message} action={<button className="button outline" type="button" onClick={refresh}>{tCommon("actions.retry")}</button>} />;
  } else if (!rows.length) {
    body = <EmptyState icon={MessageSquareText} title={t("log.emptyTitle")} hint={t("log.emptyDescription")} />;
  } else if (!filtered.length) {
    body = (
      <EmptyState
        title={t("log.noMatch")}
        action={<button className="button outline" type="button" onClick={() => update(initialSessionLogFilters)}>{t("log.clearFilters")}</button>}
      />
    );
  } else {
    body = (
      <>
        <div className="table-frame">
          <table className="data-table session-log-table" aria-label={t("log.title")}>
            <thead>
              <tr>
                <th scope="col">{t("log.session")}</th>
                {allProjects ? <th scope="col">{tCommon("project.column")}</th> : null}
                <th scope="col">{t("log.status")}</th>
                <th scope="col">{t("log.model")}</th>
                <th scope="col">{t("log.environment")}</th>
                <th scope="col" className="numeric">{t("log.tokens")}</th>
                <th scope="col" className="numeric">{t("log.created")}</th>
                <th scope="col" className="numeric">{t("log.lastActive")}</th>
                <th scope="col"><CreatorHeading /></th>
                <th scope="col"><span className="visually-hidden">{t("log.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {visible.map((row) => (
                <SessionLogRow
                  key={rowKey(row)}
                  row={row}
                  allProjects={allProjects}
                  creators={creators}
                  now={now}
                  locale={locale}
                  onOpen={open}
                  onDelete={(target) => setDeleteTarget(target)}
                />
              ))}
            </tbody>
          </table>
        </div>
        {visible.length < filtered.length ? (
          <footer className="table-footer">
            <button className="button outline" type="button" onClick={() => setLimit((value) => value + PAGE_SIZE)}>{t("log.more")}</button>
          </footer>
        ) : null}
      </>
    );
  }

  return (
    <section className="page-section console-page session-log-page" aria-labelledby="session-log-heading">
      <PageHeader
        headingId="session-log-heading"
        title={t("log.title")}
        help={t("log.help")}
        actions={<RefreshButton onClick={refresh} refreshing={loading} updatedAt={loadedAt ? formatClock(loadedAt, locale) : null} />}
      />
      <PageBody>
        <ListToolbar
          label={t("log.filters")}
          summary={rows.length ? listSummary(tCommon, filtered.length, rows.length, { hasMore: isLogTruncated(rows), locale }) : undefined}
        >
          <ProjectFilter value={selected} onChange={(value) => { setProject(value); setLimit(PAGE_SIZE); }} />
          <SearchField value={filters.query} onChange={(query) => update({ query })} placeholder={t("log.search")} />
          <SegmentedControl
            label={t("log.statusFilter")}
            value={filters.status}
            options={(["all", ...sessionStatuses] as const).map((value) => ({ value, label: t(`sessionStatus.${value}`), count: formatInteger(counts[value], locale) }))}
            onChange={(status) => update({ status })}
          />
          <ConsoleSelect
            label={t("log.agentFilter")}
            value={filters.agentId}
            onChange={(agentId) => update({ agentId })}
            options={[
              { value: "", label: t("log.allAgents") },
              ...(filters.agentId && !agents.some((agent) => agent.id === filters.agentId) ? [{ value: filters.agentId, label: filters.agentId }] : []),
              ...agents.map((agent) => ({ value: agent.id, label: agent.label })),
            ]}
          />
          <ConsoleSelect
            label={t("log.environmentFilter")}
            value={filters.environment}
            onChange={(environment) => update({ environment })}
            options={[
              { value: "", label: t("log.allEnvironments") },
              ...environmentKinds.map((kind) => ({ value: kind, label: t(`environment.${kind}`) })),
            ]}
          />
        </ListToolbar>
        {body}
      </PageBody>
      <SessionDeleteDialog
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onUncertain={refresh}
        onDeleted={(target) => {
          setDeleted((current) => new Set(current).add(`${target.project.id}:${target.sessionId}`));
          setDeleteTarget(null);
          // Drop it from the cached lists too, so another page or a revisit does not list it again.
          forgetDeleted(queryClient, collections.sessions, sessionKey(target.project.id, target.sessionId));
        }}
      />
    </section>
  );
}

function SessionLogRow({
  row,
  allProjects,
  creators,
  now,
  locale,
  onOpen,
  onDelete,
}: {
  row: Owned<SessionLogEntry>;
  allProjects: boolean;
  creators: Creators;
  now: number;
  locale: string | undefined;
  onOpen: (projectId: string, sessionId: string) => void;
  onDelete: (target: { project: Project; sessionId: string }) => void;
}) {
  const { t } = useTranslation("sessions");
  const projectCell = allProjects ? <td><ProjectName project={row.project} /></td> : null;
  const entry = row.value;

  if (entry.kind === "unrecognized") {
    const name = (
      <span className="column-help">
        <span className="muted">{t("log.unrecognized")}</span>
        <HelpTip>{t("log.unrecognizedHelp")}</HelpTip>
      </span>
    );
    return (
      <tr>
        <th scope="row">{entry.id ? <NameCell name={null} id={entry.id} fallback={t("log.unrecognized")}><HelpTip>{t("log.unrecognizedHelp")}</HelpTip></NameCell> : <span className="name-cell">{name}</span>}</th>
        {projectCell}
        <td className="table-muted">{MISSING}</td>
        <td className="table-muted">{MISSING}</td>
        <td className="table-muted">{MISSING}</td>
        <td className="numeric table-muted">{MISSING}</td>
        <td className="table-muted">{MISSING}</td>
        <td className="numeric table-muted">{MISSING}</td>
        <td className="numeric table-muted">{MISSING}</td>
        <td className="actions-cell">
          {entry.id ? (
            <RowActions>
              <button className="text-action danger" type="button" aria-label={t("delete.actionLabel", { id: entry.id })} onClick={() => onDelete({ project: row.project, sessionId: entry.id! })}>{t("delete.action")}</button>
            </RowActions>
          ) : null}
        </td>
      </tr>
    );
  }

  const session = entry.session;
  const open = () => onOpen(row.project.id, session.id);
  return (
    <tr className="clickable-row" onClick={open}>
      <th scope="row">
        <NameCell name={session.agent.name} id={session.id} fallback={t("common.untitledAgent")} onOpen={open} openLabel={t("log.open", { id: session.id })} />
      </th>
      {projectCell}
      <td onClick={(event) => event.stopPropagation()}><SessionStatus session={session} /></td>
      <td><code>{session.agent.model || MISSING}</code></td>
      <td className="session-nowrap">{t(`environment.${environmentKind(session)}`)}</td>
      <td className="numeric" title={session.usage ? t("log.exactTokens", { tokens: session.usage.total_tokens.toLocaleString(locale) }) : undefined}>
        {session.usage ? formatCompact(session.usage.total_tokens, locale) : MISSING}
      </td>
      <td className="numeric" title={formatDateTime(session.created_at, locale)}>{formatRelative(session.created_at, now, locale)}</td>
      <td className="numeric" title={formatDateTime(session.last_active_at, locale)}>{formatRelative(session.last_active_at, now, locale)}</td>
      <td><CreatorCell creator={creators.creatorOf(row.project.id, session.id)} /></td>
      <td className="actions-cell" onClick={(event) => event.stopPropagation()}>
        {isDeletable(session) ? (
          <RowActions>
            <button className="text-action danger" type="button" aria-label={t("delete.actionLabel", { id: session.id })} onClick={() => onDelete({ project: row.project, sessionId: session.id })}>{t("delete.action")}</button>
          </RowActions>
        ) : null}
      </td>
    </tr>
  );
}
