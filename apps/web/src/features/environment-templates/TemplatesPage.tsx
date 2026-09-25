import type { EnvironmentTemplateResource } from "@agents-core-web/agents-client";
import { Boxes } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, StatusDot } from "../../components/console-ui";
import { ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { useDeleteFlow } from "../../lib/delete-flow";
import { formatDateTime, MISSING } from "../../lib/format";
import { CreatorCell, CreatorHeading, forgetCreators, ProjectFilter, ProjectName, projectClient, readAllPages, useCreators, useProjectCollection, useProjects, type Owned } from "../../lib/projects";
import { CopyDialog, type CopySource } from "../copy/CopyDialog";
import { TemplateDetailPage } from "./TemplateDetail";
import { filterTemplates, templateName } from "./template-name";
import "./EnvironmentTemplatesView.css";
import { collections } from "../../lib/queries";
import { DetailSkeleton, TableSkeleton } from "../../components/Skeleton";
import { useTemplateDetail } from "../resources/detail-queries";

function count(value: readonly unknown[] | undefined): string | number {
  return value === undefined ? MISSING : value.length;
}

/**
 * Resources › Environment templates: every project's reusable Session
 * configurations. Read-only here; `env` and setup commands are write-only and
 * never returned. Administrators delete and copy but never edit.
 */
export function TemplatesPage() {
  const { params } = useConsoleNavigation();
  if (params.project && params.id) return <TemplateDetailRoute key={`${params.project}:${params.id}`} projectId={params.project} templateId={params.id} />;
  return <TemplatesList />;
}

function TemplatesList() {
  const { t, i18n } = useTranslation("templates");
  const { t: tPages } = useTranslation("pages");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const { params, navigate } = useConsoleNavigation();
  const { byId } = useProjects();
  const [filter, setFilter] = useState(params.project ?? "");
  const [query, setQuery] = useState("");
  const [copy, setCopy] = useState<CopySource | null>(null);
  const collection = useProjectCollection(collections.templates, filter);
  const rows = useMemo(() => {
    const visible = new Set(filterTemplates(collection.items.map((row) => row.value), query));
    return collection.items.filter((row) => visible.has(row.value)).sort((a, b) => b.value.updated_at - a.value.updated_at);
  }, [collection.items, query]);
  const creators = useCreators("environment_template", useMemo(() => collection.items.map((row) => ({ projectId: row.project.id, id: row.value.id })), [collection.items]));
  const refresh = useCallback(() => { forgetCreators(); collection.refresh(); }, [collection]);
  const remove = useDeleteFlow<Owned<EnvironmentTemplateResource>>(
    useCallback((row: Owned<EnvironmentTemplateResource>) => projectClient(row.project.id).deleteEnvironmentTemplate(row.value.id), []),
    refresh,
    { uncertain: tCommon("list.deleteUncertain") },
  );
  const showProject = !filter;

  let body;
  if (collection.status === "loading" && !collection.items.length) {
    body = <TableSkeleton label={t("loading")} columns={6} />;
  } else if (!collection.items.length && !collection.failures.length) {
    body = <EmptyState icon={Boxes} title={t("emptyTitle")} />;
  } else {
    body = (
      <>
        <ListToolbar label={t("filterLabel")} summary={listSummary(tCommon, rows.length, collection.items.length, { locale })}>
          <ProjectFilter value={filter} onChange={setFilter} />
          <SearchField value={query} onChange={setQuery} placeholder={t("filterPlaceholder")} label={t("filterLabel")} />
        </ListToolbar>
        {collection.failures.length ? (
          <p className="list-failures" role="alert">{tCommon("project.partial", { names: collection.failures.map((failure) => failure.project.name).join(", ") })}</p>
        ) : null}
        {rows.length ? (
          <div className="table-frame">
            <table className="data-table templates-table" aria-label={t("listLabel")}>
              <thead>
                <tr>
                  <th scope="col">{t("columns.name")}</th>
                  {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
                  <th scope="col">{t("columns.network")}</th>
                  <th scope="col" className="numeric">{t("columns.packages")}</th>
                  <th scope="col" className="numeric">{t("columns.files")}</th>
                  <th scope="col" className="numeric">{t("columns.skills")}</th>
                  <th scope="col" className="numeric">{t("columns.plugins")}</th>
                  <th scope="col">{t("columns.updated")}</th>
                  <th scope="col"><CreatorHeading /></th>
                  <th scope="col"><span className="visually-hidden">{t("columns.actions")}</span></th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const template = row.value;
                  const name = templateName(template);
                  const packages = template.packages;
                  const open = () => navigate("templates", { project: row.project.id, id: template.id });
                  return (
                    <tr key={`${row.project.id}:${template.id}`} className="clickable-row" onClick={open}>
                      <th scope="row">
                        <NameCell name={name} id={template.id} onOpen={open} openLabel={t("open", { name })}>
                          {template.unrecognized ? (
                            <span className="status-with-help template-flag" onClick={(event) => event.stopPropagation()}>
                              <StatusDot tone="warning" label={t("unrecognized")} />
                              <HelpTip>{t("unrecognizedHelp")}</HelpTip>
                            </span>
                          ) : null}
                        </NameCell>
                      </th>
                      {showProject ? <td><ProjectName project={byId.get(row.project.id) ?? row.project} /></td> : null}
                      <td className="template-nowrap" title={template.network?.allowed_domains.join("\n") || undefined}>
                        {template.network ? (
                          <>
                            {t(`access.${template.network.access}`)}
                            {template.network.access === "restricted" ? <span className="table-muted"> ({template.network.allowed_domains.length})</span> : null}
                          </>
                        ) : MISSING}
                      </td>
                      <td className="numeric">{packages ? packages.npm.length + packages.python.length + packages.system.length : MISSING}</td>
                      <td className="numeric">{count(template.files)}</td>
                      <td className="numeric">{count(template.skills)}</td>
                      <td className="numeric">{count(template.plugins)}</td>
                      <td className="template-nowrap">{formatDateTime(template.updated_at, locale)}</td>
                      <td><CreatorCell creator={creators.creatorOf(row.project.id, template.id)} /></td>
                      <td className="actions-cell" onClick={(event) => event.stopPropagation()}>
                        <RowActions>
                          <button className="text-action" type="button" aria-label={tCommon("copy.actionLabel", { name })} onClick={() => setCopy({ type: "environment_template", id: template.id, name, project: row.project })}>{tCommon("copy.action")}</button>
                          <button className="text-action danger" type="button" aria-label={t("deleteLabel", { name })} onClick={() => remove.ask(row)}>{t("delete")}</button>
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
            title={t("noMatch")}
            action={<button className="button outline" type="button" onClick={() => setQuery("")}>{tCommon("actions.clearSearch")}</button>}
          />
        )}
      </>
    );
  }

  return (
    <section className="page-section console-page templates-page" aria-labelledby="templates-heading">
      <PageHeader
        headingId="templates-heading"
        title={tPages("templates.title")}
        help={<>{tPages("templates.subtitle")} {t("boundary")}</>}
        actions={<RefreshButton onClick={refresh} refreshing={collection.status === "loading"} />}
      />
      <PageBody>{body}</PageBody>
      <CopyDialog source={copy} onClose={() => setCopy(null)} />
      <ConfirmDialog
        open={remove.target !== null}
        title={t("modal.delete")}
        confirmLabel={t("deleteTemplate")}
        busyLabel={t("deleting")}
        busy={remove.busy}
        error={remove.error}
        onConfirm={() => void remove.confirm()}
        onClose={remove.cancel}
      >
        {remove.target ? <><p>{t("deletePrompt", { name: templateName(remove.target.value) })}</p><p>{t("deleteWarning")}</p></> : null}
      </ConfirmDialog>
    </section>
  );
}

function TemplateDetailRoute({ projectId, templateId }: { projectId: string; templateId: string }) {
  const { t } = useTranslation("templates");
  const { t: tCommon } = useTranslation();
  const { navigate, back: goBack } = useConsoleNavigation();
  const { byId } = useProjects();
  const project = byId.get(projectId);
  // Opens from the cache (or the list row) at once; a refresh keeps the Template on screen.
  const { read, forget } = useTemplateDetail(projectId, templateId);
  const [copy, setCopy] = useState<CopySource | null>(null);
  const back = useCallback(() => goBack("templates", { project: projectId }), [goBack, projectId]);
  const creators = useCreators("environment_template", useMemo(() => [{ projectId, id: templateId }], [projectId, templateId]));
  const refresh = () => { forgetCreators(); void read.refetch(); };
  const failure = read.isError ? (read.error instanceof Error ? read.error.message : String(read.error)) : null;

  const remove = useDeleteFlow<EnvironmentTemplateResource>(
    useCallback((template: EnvironmentTemplateResource) => projectClient(projectId).deleteEnvironmentTemplate(template.id), [projectId]),
    useCallback(() => { back(); forget(); }, [back, forget]),
    { uncertain: tCommon("list.deleteUncertain"), reread: () => void read.refetch() },
  );

  if (!read.data) {
    return (
      <section className="page-section console-page templates-page" aria-labelledby="template-detail-heading">
        <PageHeader headingId="template-detail-heading" title={t("detail.loadingTitle", { defaultValue: templateId })} actions={<RefreshButton onClick={refresh} refreshing={read.isFetching} />} />
        <PageBody>
          {failure === null ? <DetailSkeleton label={t("loading")} /> : (
            <EmptyState title={t("loadFailedTitle")} description={failure} action={<button className="button outline" type="button" onClick={back}>{t("back")}</button>} />
          )}
        </PageBody>
      </section>
    );
  }

  const template = read.data;
  return (
    <>
      <TemplateDetailPage
        template={template}
        blocked={remove.busy}
        refreshing={read.isFetching}
        notice={failure !== null ? <p className="coverage-note coverage-note-error" role="alert">{t("refreshFailed")} {failure}</p> : undefined}
        onBack={back}
        onRefresh={refresh}
        onDelete={() => remove.ask(template)}
        onCopy={project ? () => setCopy({ type: "environment_template", id: template.id, name: templateName(template), project }) : undefined}
        facts={(
          <>
            <div><dt>{tCommon("project.column")}</dt><dd><ProjectName project={project} /></dd></div>
            <div><dt><CreatorHeading /></dt><dd><CreatorCell creator={creators.creatorOf(projectId, template.id)} /></dd></div>
          </>
        )}
        onOpenFile={(fileId) => navigate("files", { project: projectId, id: fileId })}
        onOpenSkill={(skillId) => navigate("skills", { project: projectId, id: skillId })}
      />
      <CopyDialog source={copy} onClose={() => setCopy(null)} />
      <ConfirmDialog
        open={remove.target !== null}
        title={t("modal.delete")}
        confirmLabel={t("deleteTemplate")}
        busyLabel={t("deleting")}
        busy={remove.busy}
        error={remove.error}
        onConfirm={() => void remove.confirm()}
        onClose={remove.cancel}
      >
        <p>{t("deletePrompt", { name: templateName(template) })}</p>
        <p>{t("deleteWarning")}</p>
      </ConfirmDialog>
    </>
  );
}
