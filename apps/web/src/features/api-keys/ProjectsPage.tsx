import { ArrowLeft, FolderKanban, Plus } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";


import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { Modal } from "../../components/Modal";
import { hashWithParams, useConsoleNavigation } from "../../lib/console-navigation";
import { consoleHashForView } from "../../lib/console-routes";
import { formatDateTime, formatInteger, formatRelative } from "../../lib/format";
import { admin, useProjects } from "../../lib/projects";
import { activeKeyNames, flowError, isAbort, isUsableName, matchesProject, normalizeName, prefixLabel, projectNameProblem, type FlowError } from "./key-flows";
import { FlowErrorMessage, KeyFlowDialogs, NameField, PendingKeyNotice } from "./KeyFlowDialogs";
import { ProjectDetail, useProjectKeys } from "./ProjectDetail";
import { ProjectStatus } from "./ProjectStatus";
import { useKeyFlow } from "./use-key-flow";
import "./api-keys.css";
import { type AdminKey, archiveProject, createProject, loadSummary, type Project, type ProjectSummary, renameProject, revokeKey } from "../../lib/admin-view";

type Dialog =
  | { kind: "create"; name: string }
  | { kind: "rename"; project: Project; name: string }
  | { kind: "archive"; project: Project }
  | { kind: "revoke"; project: Project; key: AdminKey; activeCount: number };

/** Keeps the address in step with the open project without remounting the page. */
function replaceHash(id: string | null) {
  const hash = hashWithParams(consoleHashForView("projects"), id ? { id } : {});
  if (window.location.hash !== hash) window.history.replaceState(window.history.state, "", hash);
}

const manageable = (project: Project) => project.status === "active";

/**
 * Platform › Projects and keys. A project owns the assets shared by all of
 * its named keys. The list shows every project; the detail shows its keys
 * (issue with a one-time plaintext, revoke), assets, usage by key and write
 * operations.
 */
export function ProjectsPage() {
  const { t, i18n } = useTranslation("keys");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const { state, refresh, byId } = useProjects();
  const { params } = useConsoleNavigation();
  const [selectedId, setSelectedId] = useState<string | null>(params.id ?? null);
  const [created, setCreated] = useState<Project | null>(null);
  const [query, setQuery] = useState("");
  const [revision, setRevision] = useState(0);
  const [summaries, setSummaries] = useState<ReadonlyMap<string, ProjectSummary> | null>(null);
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [dialogBusy, setDialogBusy] = useState(false);
  const [dialogError, setDialogError] = useState<FlowError | null>(null);

  // Links from other pages (and the sidebar) re-target the page.
  useEffect(() => { setSelectedId(params.id ?? null); }, [params]);

  const changed = useCallback(() => {
    refresh();
    setRevision((value) => value + 1);
  }, [refresh]);
  const controls = useKeyFlow(changed);
  const { flow, dispatch } = controls;
  const flowBusy = flow.step !== "idle";

  // Last activity for the list: one summary row per project.
  useEffect(() => {
    const controller = new AbortController();
    loadSummary({ signal: controller.signal }).then(
      (rows) => setSummaries(new Map(rows.filter((row) => row.agent_id === null && row.key === null).map((row) => [row.project_id, row]))),
      () => undefined,
    );
    return () => controller.abort();
  }, [revision]);

  const projects = state.projects;
  const names = useMemo(() => projects.map((project) => project.name), [projects]);
  const visible = useMemo(() => projects.filter((project) => matchesProject(project, query)), [projects, query]);
  const selected = selectedId ? byId.get(selectedId) ?? (created?.id === selectedId ? created : null) : null;
  const keys = useProjectKeys(selected?.id ?? null, revision);
  const now = Math.floor(Date.now() / 1000);

  const open = (id: string | null) => {
    setSelectedId(id);
    replaceHash(id);
  };
  const openDialog = (next: Dialog) => { setDialogError(null); setDialog(next); };
  const closeDialog = () => { if (!dialogBusy) setDialog(null); };

  const dialogNameProblem = dialog?.kind === "create"
    ? projectNameProblem(dialog.name, names)
    : dialog?.kind === "rename"
      ? projectNameProblem(dialog.name, names.filter((name) => name !== dialog.project.name))
      : null;
  const dialogReady = !dialogBusy && (dialog?.kind === "create" || dialog?.kind === "rename"
    ? isUsableName(dialog.name, dialogNameProblem) && !(dialog.kind === "rename" && normalizeName(dialog.name) === dialog.project.name)
    : Boolean(dialog));

  const runDialog = async () => {
    if (!dialog || !dialogReady) return;
    setDialogBusy(true);
    setDialogError(null);
    try {
      if (dialog.kind === "create") {
        const project = await createProject(normalizeName(dialog.name));
        setCreated(project);
        open(project.id);
      } else if (dialog.kind === "rename") {
        await renameProject(dialog.project.id, normalizeName(dialog.name));
      } else if (dialog.kind === "archive") {
        await archiveProject(dialog.project.id);
      } else {
        await revokeKey(dialog.project.id, dialog.key.id);
      }
      setDialog(null);
    } catch (error) {
      if (!isAbort(error)) setDialogError(flowError(error));
    } finally {
      setDialogBusy(false);
      changed();
    }
  };

  const refreshButton = <RefreshButton onClick={changed} refreshing={state.status === "loading"} label={t("page.refresh")} />;
  const createButton = (
    <button className="button primary" type="button" onClick={() => openDialog({ kind: "create", name: "" })}>
      <Plus size={14} aria-hidden="true" />{t("page.create")}
    </button>
  );

  let page;
  if (selectedId) {
    page = (
      <section className="page-section console-page projects-page" aria-labelledby="project-detail-heading">
        <PageHeader
          headingId="project-detail-heading"
          title={(
            <>
              <button type="button" className="icon-button ghost back-button" aria-label={t("detail.back")} title={t("detail.back")} onClick={() => open(null)}>
                <ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" />
              </button>
              {selected?.name ?? t("page.title")}
            </>
          )}
          actions={(
            <>
              {refreshButton}
              {selected && manageable(selected) ? (
                <>
                  <button className="button outline" type="button" onClick={() => openDialog({ kind: "rename", project: selected, name: selected.name })}>{t("actions.rename")}</button>
                  <button className="button danger" type="button" aria-label={t("actions.archiveLabel", { name: selected.name })} onClick={() => openDialog({ kind: "archive", project: selected })}>{t("actions.archive")}</button>
                </>
              ) : null}
            </>
          )}
        />
        <PageBody>
          <PendingKeyNotice controls={controls} />
          {selected ? (
            <ProjectDetail
              key={selected.id}
              project={selected}
              keys={keys}
              revision={revision}
              busy={flowBusy || dialogBusy}
              onIssue={() => dispatch({ type: "openIssue", project: selected })}
              onRevoke={(key, activeCount) => openDialog({ kind: "revoke", project: selected, key, activeCount })}
            />
          ) : state.status === "loading" ? (
            <p className="page-status" role="status">{t("page.loading")}</p>
          ) : (
            <EmptyState
              icon={FolderKanban}
              title={state.status === "failed" ? t("page.loadFailed") : t("detail.notFound")}
              action={<button className="button outline" type="button" onClick={() => open(null)}>{t("detail.back")}</button>}
            />
          )}
        </PageBody>
      </section>
    );
  } else {
    let body;
    if (state.status === "failed" && !projects.length) {
      body = <ErrorState title={t("page.loadFailed")} detail={state.error} onRetry={changed} />;
    } else if (state.status === "loading" && !projects.length) {
      body = <p className="page-status" role="status">{t("page.loading")}</p>;
    } else if (!projects.length) {
      body = <EmptyState icon={FolderKanban} title={t("page.empty")} action={createButton} />;
    } else {
      body = (
        <>
          {state.status === "failed" ? <p className="coverage-note coverage-note-error" role="alert">{t("page.refreshFailed")}</p> : null}
          <ListToolbar label={t("list.label")} summary={listSummary(tCommon, visible.length, projects.length, { locale })}>
            <SearchField value={query} onChange={setQuery} placeholder={t("list.search")} label={t("list.search")} />
          </ListToolbar>
          {visible.length ? (
            <div className="table-frame">
              <table className="data-table projects-table" aria-label={t("list.label")}>
                <thead>
                  <tr>
                    <th scope="col">{t("list.name")}</th>
                    <th scope="col">{t("list.status")}</th>
                    <th scope="col" className="numeric">{t("list.activeKeys")}</th>
                    <th scope="col">{t("list.created")}</th>
                    <th scope="col"><span className="column-help">{t("list.lastActive")}<HelpTip>{t("list.lastActiveHelp")}</HelpTip></span></th>
                    <th scope="col"><span className="visually-hidden">{t("list.actions")}</span></th>
                  </tr>
                </thead>
                <tbody>
                  {visible.map((project) => {
                    const lastActive = summaries?.get(project.id)?.last_active_at ?? null;
                    return (
                      <tr key={project.id} className="clickable-row" onClick={() => open(project.id)}>
                        <th scope="row">
                          <NameCell name={project.name} id={project.id} onOpen={() => open(project.id)} openLabel={t("list.open", { name: project.name })} />
                        </th>
                        <td><ProjectStatus project={project} /></td>
                        <td className="numeric">{formatInteger(project.active_key_count, locale)}</td>
                        <td className="key-nowrap">{formatDateTime(project.created_at, locale)}</td>
                        <td className="key-nowrap" title={lastActive !== null ? formatDateTime(lastActive, locale) : undefined}>{formatRelative(lastActive, now, locale)}</td>
                        <td className="actions-cell" onClick={(event) => event.stopPropagation()}>
                          {manageable(project) ? (
                            <RowActions>
                              <button className="text-action" type="button" aria-label={t("actions.renameLabel", { name: project.name })} onClick={() => openDialog({ kind: "rename", project, name: project.name })}>{t("actions.rename")}</button>
                              <button className="text-action danger" type="button" aria-label={t("actions.archiveLabel", { name: project.name })} onClick={() => openDialog({ kind: "archive", project })}>{t("actions.archive")}</button>
                            </RowActions>
                          ) : null}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <EmptyState
              title={t("list.noMatch")}
              description={tCommon("list.noMatchesDescription")}
              action={<button className="button outline" type="button" onClick={() => setQuery("")}>{tCommon("actions.clearSearch")}</button>}
            />
          )}
        </>
      );
    }
    page = (
      <section className="page-section console-page projects-page" aria-labelledby="projects-heading">
        <PageHeader headingId="projects-heading" title={t("page.title")} help={t("page.help")} actions={<>{refreshButton}{createButton}</>} />
        <PageBody>
          <PendingKeyNotice controls={controls} />
          {body}
        </PageBody>
      </section>
    );
  }

  const destructive = dialog?.kind === "archive" || dialog?.kind === "revoke";
  const dialogTitle = !dialog ? "" : dialog.kind === "create" ? t("createDialog.title") : dialog.kind === "rename" ? t("renameDialog.title") : dialog.kind === "archive" ? t("archiveDialog.title") : t("revokeDialog.title");
  const submitLabel = !dialog ? "" : dialog.kind === "create"
    ? dialogBusy ? t("createDialog.submitting") : t("createDialog.submit")
    : dialog.kind === "rename"
      ? dialogBusy ? t("renameDialog.submitting") : t("renameDialog.submit")
      : dialog.kind === "archive"
        ? dialogBusy ? t("archiveDialog.submitting") : t("archiveDialog.submit")
        : dialogBusy ? t("revokeDialog.submitting") : t("revokeDialog.submit");

  return (
    <>
      {page}
      <KeyFlowDialogs controls={controls} taken={activeKeyNames(keys.value)} />
      <Modal
        open={Boolean(dialog)}
        onClose={closeDialog}
        title={dialogTitle}
        footer={(
          <>
            <button className="button outline" type="button" onClick={closeDialog} disabled={dialogBusy}>{tCommon("actions.cancel")}</button>
            <button className={destructive ? "button danger" : "button primary"} type="submit" form="project-dialog-form" disabled={!dialogReady}>{submitLabel}</button>
          </>
        )}
      >
        {dialog ? (
          <form id="project-dialog-form" className="key-dialog-form" onSubmit={(event) => { event.preventDefault(); void runDialog(); }}>
            {dialog.kind === "create" || dialog.kind === "rename" ? (
              <NameField
                name="project-name"
                label={t("createDialog.name")}
                help={t("createDialog.nameHelp")}
                value={dialog.name}
                onChange={(name) => setDialog({ ...dialog, name })}
                problem={dialogNameProblem}
                problemText={dialogNameProblem ? t(`createDialog.problems.${dialogNameProblem}`) : ""}
                placeholder={t("createDialog.placeholder")}
                disabled={dialogBusy}
              />
            ) : dialog.kind === "archive" ? (
              <p>{t("archiveDialog.prompt", { name: dialog.project.name })}</p>
            ) : (
              <>
                <p>{t("revokeDialog.prompt", { name: dialog.key.name, prefix: prefixLabel(dialog.key.prefix) })}</p>
                {dialog.activeCount <= 1 ? <p>{t("revokeDialog.lastKey", { project: dialog.project.name })}</p> : null}
              </>
            )}
            <FlowErrorMessage error={dialogError} name={dialog.kind === "create" || dialog.kind === "rename" ? normalizeName(dialog.name) : undefined} />
          </form>
        ) : null}
      </Modal>
    </>
  );
}
