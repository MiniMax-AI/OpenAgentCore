import type { SavedAgent } from "@agents-core-web/agents-client";
import { ArrowLeft, Bot, ListTree, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { useFailureToast } from "../../components/Toast";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, Kpi, KpiStrip, PageBody, PageHeader, RefreshButton, Section } from "../../components/console-ui";
import { CopyableId, ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { useDeleteFlow } from "../../lib/delete-flow";
import { formatCompact, formatDateTime, formatInteger, formatPercent, formatRelative, MISSING } from "../../lib/format";
import { harnessNames, protocolNames } from "../../lib/harness-labels";
import { admin, CreatorCell, CreatorHeading, forgetCreators, ProjectFilter, ProjectName, projectClient, readAllPages, useCreators, useProjectCollection, useProjects, type Owned } from "../../lib/projects";
import "./AgentCatalog.css";
import { type ProjectSummary } from "../../lib/admin-view";
import { refreshAgentSummaries, useAgentSummaries } from "./agent-summaries";
import { collections } from "../../lib/queries";
import { DetailSkeleton, TableSkeleton } from "../../components/Skeleton";
import { useAgentDetail } from "../resources/detail-queries";


function coverage(summary: ProjectSummary | undefined, locale?: string): string {
  if (!summary || summary.coverage.sessions === 0) return MISSING;
  return formatPercent(summary.coverage.reported / summary.coverage.sessions, locale);
}

/**
 * Resources › Agent: every project's saved Agents with their usage. Agents are
 * created and edited by the project's keys through the Agents API; the console
 * inspects and deletes them.
 */
export function AgentsPage() {
  const { params } = useConsoleNavigation();
  if (params.project && params.id) return <AgentDetail key={`${params.project}:${params.id}`} projectId={params.project} agentId={params.id} />;
  return <AgentsList />;
}

function AgentsList() {
  const { t, i18n } = useTranslation("agents");
  const { t: tPages } = useTranslation("pages");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1000);
  const { params, navigate } = useConsoleNavigation();
  const { byId } = useProjects();
  const [filter, setFilter] = useState(params.project ?? "");
  const [query, setQuery] = useState("");
  const collection = useProjectCollection(collections.agents, filter);
  const rows = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return collection.items
      .filter((row) => !needle || [row.value.name, row.value.model, row.value.id].some((value) => value?.toLowerCase().includes(needle)))
      .sort((a, b) => b.value.updated_at - a.value.updated_at);
  }, [collection.items, query]);
  const projectIds = useMemo(() => [...new Set(collection.items.map((row) => row.project.id))].sort(), [collection.items]);
  const summaries = useAgentSummaries(projectIds);
  const creators = useCreators("agent", useMemo(() => collection.items.map((row) => ({ projectId: row.project.id, id: row.value.id })), [collection.items]));
  const refresh = useCallback(() => { forgetCreators(); collection.refresh(); void refreshAgentSummaries(); }, [collection]);
  const remove = useDeleteFlow<Owned<SavedAgent>>(
    useCallback((row: Owned<SavedAgent>) => projectClient(row.project.id).deleteAgent(row.value.id), []),
    refresh,
    { uncertain: tCommon("list.deleteUncertain") },
  );
  const showProject = !filter;
  const untitled = t("catalog.untitled");

  // Projects that could not be read are reported in a toast; the list shows the rest.
  const failedNames = collection.failures.map((failure) => failure.project.name).join(", ");
  useFailureToast(collection.items.length > 0 && collection.failures.length > 0, tCommon("project.partial", { names: failedNames }), "agents-partial");
  let body;
  if (collection.status === "loading" && !collection.items.length) {
    body = <TableSkeleton label={t("loading", { defaultValue: "…" })} columns={8} />;
  } else if (!collection.items.length && !collection.failures.length) {
    body = <EmptyState icon={Bot} title={t("catalog.noSaved")} />;
  } else if (!collection.items.length) {
    body = <EmptyState title={tCommon("project.failed", { names: failedNames })} description={collection.failures[0]?.message} action={<button className="button outline" type="button" onClick={collection.refresh}>{tCommon("actions.retry")}</button>} />;
  } else {
    body = (
      <>
        <ListToolbar label={t("view.filterLabel")} summary={listSummary(tCommon, rows.length, collection.items.length, { locale })}>
          <ProjectFilter value={filter} onChange={setFilter} />
          <SearchField value={query} onChange={setQuery} placeholder={t("view.searchPlaceholder")} label={t("view.filterLabel")} />
        </ListToolbar>
        {rows.length ? (
          <div className="table-frame agent-table-frame">
            <table className="data-table agent-table" aria-label={t("catalog.listLabel")}>
              <thead>
                <tr>
                  <th scope="col">{t("view.columns.agent")}</th>
                  {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
                  <th scope="col">{t("view.columns.model")}</th>
                  <th scope="col">{t("view.columns.harness")}</th>
                  <th scope="col" className="numeric">{t("view.columns.tools")}</th>
                  <th scope="col" className="numeric">{t("view.columns.lastActive")}</th>
                  <th scope="col"><CreatorHeading /></th>
                  <th scope="col"><span className="visually-hidden">{t("view.columns.actions")}</span></th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const agent = row.value;
                  const name = agent.name || untitled;
                  const harness = agent.x_agents_core?.harness;
                  const summary = summaries.get(`${row.project.id}:${agent.id}`);
                  const open = () => navigate("agents", { project: row.project.id, id: agent.id });
                  return (
                    <tr key={`${row.project.id}:${agent.id}`} className="clickable-row" onClick={open}>
                      <th scope="row"><NameCell name={agent.name} id={agent.id} fallback={untitled} onOpen={open} openLabel={t("view.open", { name })} /></th>
                      {showProject ? <td><ProjectName project={byId.get(row.project.id) ?? row.project} /></td> : null}
                      <td><code className="agent-table-model" title={agent.model}>{agent.model}</code></td>
                      <td className={harness ? undefined : "table-muted"}>{harness ? harnessNames[harness] : t("view.coreDefault")}</td>
                      <td className="numeric">{formatInteger(agent.tools.length, locale)}</td>
                      <td className="numeric" title={formatDateTime(summary?.last_active_at, locale)}>{summary?.last_active_at ? formatRelative(summary.last_active_at, now, locale) : MISSING}</td>
                      <td><CreatorCell creator={creators.creatorOf(row.project.id, agent.id)} /></td>
                      <td className="actions-cell" onClick={(event) => event.stopPropagation()}>
                        <RowActions>
                          <button className="text-action danger" type="button" aria-label={t("view.deletePrompt", { name })} onClick={() => remove.ask(row)}>{t("view.delete")}</button>
                        </RowActions>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState
            title={t("catalog.noMatch")}
            action={<button className="button outline" type="button" onClick={() => setQuery("")}>{tCommon("actions.clearSearch")}</button>}
          />
        )}
      </>
    );
  }

  return (
    <section className="page-section console-page agents-list-page" aria-labelledby="agents-heading">
      <PageHeader
        headingId="agents-heading"
        title={tPages("agents.title")}
        help={<>{tPages("agents.subtitle")} {t("view.usageHelp")}</>}
        actions={<RefreshButton onClick={refresh} refreshing={collection.status === "loading"} label={t("refreshLabel")} />}
      />
      <PageBody>{body}</PageBody>
      <ConfirmDialog
        open={remove.target !== null}
        title={t("view.deleteTitle")}
        confirmLabel={t("view.confirmDelete")}
        busyLabel={t("view.deleting")}
        busy={remove.busy}
        error={remove.error}
        onConfirm={() => void remove.confirm()}
        onClose={remove.cancel}
      >
        <p>{remove.target ? t("view.deletePrompt", { name: remove.target.value.name || untitled }) : null}</p>
        <p>{t("view.deleteConsequences")}</p>
      </ConfirmDialog>
    </section>
  );
}

function toolRows(agent: SavedAgent): Array<{ type: string; name: string; target: string }> {
  return agent.tools.map((tool) => {
    const value = tool as Record<string, unknown>;
    const type = typeof value.type === "string" ? value.type : "unknown";
    const name = typeof value.name === "string" ? value.name : typeof value.server_label === "string" ? value.server_label : MISSING;
    const target = typeof value.server_url === "string" ? value.server_url : typeof value.description === "string" && value.description ? value.description : MISSING;
    return { type, name, target };
  });
}

function AgentDetail({ projectId, agentId }: { projectId: string; agentId: string }) {
  const { t, i18n } = useTranslation("agents");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const { navigate, back: goBack } = useConsoleNavigation();
  const { byId } = useProjects();
  const project = byId.get(projectId);
  // Opens from the cache (or the list row) at once; a refresh keeps the Agent on screen.
  const { read, forget } = useAgentDetail(projectId, agentId);
  const back = useCallback(() => goBack("agents", { project: projectId }), [goBack, projectId]);
  const summaries = useAgentSummaries(useMemo(() => [projectId], [projectId]));
  const summary = summaries.get(`${projectId}:${agentId}`);
  const creators = useCreators("agent", useMemo(() => [{ projectId, id: agentId }], [projectId, agentId]));

  const remove = useDeleteFlow<SavedAgent>(
    useCallback((agent: SavedAgent) => projectClient(projectId).deleteAgent(agent.id), [projectId]),
    useCallback(() => { back(); forget(); }, [back, forget]),
    { uncertain: tCommon("list.deleteUncertain"), reread: () => void read.refetch() },
  );

  const agent = read.data ?? null;
  const failure = read.isError ? (read.error instanceof Error ? read.error.message : String(read.error)) : null;
  // Detail reads are not polled: each failed read was asked for and is reported.
  useFailureToast(agent !== null ? failure : null, t("refreshFailed"), "agent-refresh", read.errorUpdatedAt);
  const name = agent ? agent.name || t("catalog.untitled") : agentId;
  const tools = agent ? toolRows(agent) : [];
  const metadata = agent ? Object.entries(agent.metadata ?? {}) : [];
  const provider = agent?.x_agents_core?.model_provider;
  return (
    <section className="page-section console-page agents-list-page" aria-labelledby="agent-detail-heading">
      <PageHeader
        headingId="agent-detail-heading"
        title={(
          <>
            <button type="button" className="icon-button ghost back-button" aria-label={t("view.back")} title={t("view.back")} onClick={back}>
              <ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" />
            </button>
            {name}
          </>
        )}
        actions={(
          <>
            <RefreshButton onClick={() => { forgetCreators(); void read.refetch(); }} refreshing={read.isFetching} />
            <button className="button outline" type="button" onClick={() => navigate("sessions", { project: projectId, id: agentId })}>
              <ListTree size={14} aria-hidden="true" />{t("view.openSessions")}
            </button>
            <button className="button danger" type="button" disabled={!agent} onClick={() => { if (agent) remove.ask(agent); }}>
              <Trash2 size={14} aria-hidden="true" />{t("view.delete")}
            </button>
          </>
        )}
      />
      <PageBody>
        {!agent && read.isPending ? <DetailSkeleton label={t("loading")} /> : null}
        {!agent && failure !== null ? <EmptyState title={t("view.loadFailed")} description={failure} action={<button className="button outline" type="button" onClick={back}>{t("view.back")}</button>} /> : null}
        {agent ? (
          <>
            <dl className="resource-facts" aria-label={t("view.facts")}>
              <div><dt>{t("view.id")}</dt><dd><CopyableId id={agent.id} /></dd></div>
              <div><dt>{tCommon("project.column")}</dt><dd><ProjectName project={project} /></dd></div>
              <div><dt>{t("view.columns.model")}</dt><dd><code>{agent.model}</code></dd></div>
              <div><dt>{t("view.columns.harness")}</dt><dd>{agent.x_agents_core?.harness ? harnessNames[agent.x_agents_core.harness] : t("view.coreDefault")}</dd></div>
              <div><dt><CreatorHeading /></dt><dd><CreatorCell creator={creators.creatorOf(projectId, agent.id)} /></dd></div>
              <div><dt>{t("view.created")}</dt><dd>{formatDateTime(agent.created_at, locale)}</dd></div>
              <div><dt>{t("view.updated")}</dt><dd>{formatDateTime(agent.updated_at, locale)}</dd></div>
            </dl>

            <Section headingId="agent-usage-heading" title={t("view.usage")} help={t("view.usageHelp")}>
              <KpiStrip label={t("view.usage")}>
                <Kpi label={t("view.columns.sessions")} value={summary ? formatInteger(summary.sessions.total, locale) : MISSING} />
                <Kpi label={t("view.columns.tokens")} value={summary?.usage ? formatCompact(summary.usage.total_tokens, locale) : MISSING} />
                <Kpi label={t("view.coverage")} value={coverage(summary, locale)} />
                <Kpi label={t("view.columns.lastActive")} value={summary?.last_active_at ? formatDateTime(summary.last_active_at, locale) : MISSING} />
              </KpiStrip>
            </Section>

            <Section headingId="agent-instructions-heading" title={t("view.instructions")}>
              {agent.instructions ? <pre className="agent-instructions">{agent.instructions}</pre> : <p className="table-muted">{t("view.noInstructions")}</p>}
            </Section>

            <Section headingId="agent-tools-heading" title={<>{t("view.tools")} <span className="heading-count">{tools.length}</span></>} help={t("view.toolsHelp")}>
              {tools.length ? (
                <div className="table-frame">
                  <table className="data-table data-table-compact" aria-label={t("view.tools")}>
                    <thead><tr><th scope="col">{t("view.toolType")}</th><th scope="col">{t("view.toolName")}</th><th scope="col">{t("view.toolTarget")}</th></tr></thead>
                    <tbody>
                      {tools.map((tool, index) => (
                        <tr key={index}><td><code>{tool.type}</code></td><td>{tool.name}</td><td className="agent-tool-target" title={tool.target}>{tool.target}</td></tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : <p className="table-muted">{t("view.noTools")}</p>}
            </Section>

            {provider ? (
              <Section headingId="agent-provider-heading" title={t("view.provider.title")} help={t("view.provider.help")}>
                {/* The read view has no key, only whether one is saved. */}
                <dl className="resource-facts">
                  <div><dt>{t("view.provider.protocol")}</dt><dd>{protocolNames[provider.protocol]}</dd></div>
                  <div className="agent-provider-url"><dt>{t("view.provider.baseUrl")}</dt><dd><code>{provider.base_url}</code></dd></div>
                  <div><dt>{t("view.provider.apiKey")}</dt><dd>{provider.api_key_configured ? t("view.provider.keyConfigured") : t("view.provider.keyNotConfigured")}</dd></div>
                  {provider.context_window !== undefined ? <div><dt>{t("view.provider.contextWindow")}</dt><dd>{formatInteger(provider.context_window, locale)}</dd></div> : null}
                  {provider.max_output_tokens !== undefined ? <div><dt>{t("view.provider.maxOutputTokens")}</dt><dd>{formatInteger(provider.max_output_tokens, locale)}</dd></div> : null}
                </dl>
              </Section>
            ) : null}

            <Section headingId="agent-generation-heading" title={t("view.generation")}>
              <dl className="resource-facts">
                <div><dt>{t("view.reasoningEffort")}</dt><dd>{agent.reasoning?.effort ?? t("view.coreDefault")}</dd></div>
                <div><dt>{t("view.reasoningSummary")}</dt><dd>{agent.reasoning?.summary ?? t("view.coreDefault")}</dd></div>
                <div><dt>{t("view.textFormat")}</dt><dd>{agent.text?.format?.type ?? MISSING}</dd></div>
                <div><dt>{t("view.verbosity")}</dt><dd>{agent.text?.verbosity ?? MISSING}</dd></div>
                <div><dt>{t("view.serviceTier")}</dt><dd>{agent.service_tier ?? MISSING}</dd></div>
              </dl>
            </Section>

            <Section headingId="agent-metadata-heading" title={t("view.metadata")}>
              {metadata.length ? (
                <dl className="resource-facts">
                  {metadata.map(([key, value]) => <div key={key}><dt><code>{key}</code></dt><dd>{value}</dd></div>)}
                </dl>
              ) : <p className="table-muted">{t("view.noMetadata")}</p>}
            </Section>
          </>
        ) : null}
      </PageBody>
      <ConfirmDialog
        open={remove.target !== null}
        title={t("view.deleteTitle")}
        confirmLabel={t("view.confirmDelete")}
        busyLabel={t("view.deleting")}
        busy={remove.busy}
        error={remove.error}
        onConfirm={() => void remove.confirm()}
        onClose={remove.cancel}
      >
        <p>{t("view.deletePrompt", { name })}</p>
        <p>{t("view.deleteConsequences")}</p>
      </ConfirmDialog>
    </section>
  );
}
