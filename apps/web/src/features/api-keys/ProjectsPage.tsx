import { useIsFetching, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, FolderKanban, Plus } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Trans, useTranslation } from "react-i18next";


import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, revealInPageBody } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { Modal } from "../../components/Modal";
import { useFailureToast } from "../../components/Toast";
import { useConsoleIntent, useConsoleNavigation } from "../../lib/console-navigation";
import { formatDateTime, formatInteger, formatRelative } from "../../lib/format";
import { admin, useProjects } from "../../lib/projects";
import { activeKeyNames, archiveKeyCount, flowError, isAbort, isArchiveConfirmed, isUsableName, matchesProject, normalizeName, prefixLabel, projectNameProblem, type FlowError } from "./key-flows";
import { FlowErrorMessage, KeyFlowDialogs, NameField, PendingKeyNotice } from "./KeyFlowDialogs";
import { PROJECT_CALL_HEADING_ID } from "./HowToCall";
import { ProjectDetail, useProjectKeys } from "./ProjectDetail";
import { invalidateProjects, projectActivityQuery, projectKeysQuery, projectScope, projectSummaryQuery } from "./project-queries";
import { installationQuery } from "../../lib/installation";
import { projectsQuery } from "../../lib/queries";
import { ProjectStatus } from "./ProjectStatus";
import { useKeyFlow } from "./use-key-flow";
import "./api-keys.css";
import { type AdminKey, archiveProject, createProject, type Project, renameProject, revokeKey } from "../../lib/admin-view";
import { TableSkeleton } from "../../components/Skeleton";

type Dialog =
  /** `thenIssue`: Getting started continues from the new project to its first key. */
  | { kind: "create"; name: string; thenIssue?: boolean }
  | { kind: "rename"; project: Project; name: string }
  /** `typed`: the project name, required while it has active keys. */
  | { kind: "archive"; project: Project; typed: string }
  | { kind: "revoke"; project: Project; key: AdminKey; activeCount: number };

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
  const { state, byId, refreshError } = useProjects();
  const queryClient = useQueryClient();
  const { params, navigate, back } = useConsoleNavigation();
  const [selectedId, setSelectedId] = useState<string | null>(params.id ?? null);
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [dialogBusy, setDialogBusy] = useState(false);
  const [dialogError, setDialogError] = useState<FlowError | null>(null);

  // Links from other pages (and the sidebar) re-target the page.
  useEffect(() => { setSelectedId(params.id ?? null); }, [params]);

  // After a key is issued (or its outcome is uncertain), re-read the project and its keys.
  const keyChanged = useCallback((project: Project) => {
    void invalidateProjects(queryClient, { projectId: project.id });
  }, [queryClient]);
  const controls = useKeyFlow(keyChanged);
  const { flow, dispatch } = controls;
  const flowBusy = flow.step !== "idle";

  // Last activity for the list: one summary row per project. Unavailable activity shows "—".
  const activity = useQuery(projectActivityQuery);
  const summaries = activity.data ?? null;

  const projects = state.projects;
  useFailureToast(refreshError, t("page.refreshFailed"), "projects-refresh");
  const names = useMemo(() => projects.map((project) => project.name), [projects]);
  const visible = useMemo(() => projects.filter((project) => matchesProject(project, query)), [projects, query]);
  const selected = selectedId ? byId.get(selectedId) ?? null : null;
  const keys = useProjectKeys(selected?.id ?? null);
  // The open project's keys and usage; its write operations show their own progress.
  const detailFetching = useIsFetching({ queryKey: projectScope(selectedId ?? ""), predicate: (entry) => entry.queryKey[2] !== "write-operations" }) > 0;
  const refreshAll = useCallback(() => {
    void invalidateProjects(queryClient, { activity: true });
    void queryClient.invalidateQueries({ queryKey: ["project"] });
  }, [queryClient]);
  const now = Math.floor(Date.now() / 1000);

  // Opening a project is a page like any other, so going back returns to where it was opened from.
  const open = (id: string | null) => {
    if (id) navigate("projects", { id });
    else back("projects");
  };
  const openDialog = (next: Dialog) => { setDialogError(null); setDialog(next); };
  const closeDialog = () => { if (!dialogBusy) setDialog(null); };
  // Getting started opens a dialog on arrival: a new project (then its first key), or a key for an open project.
  useConsoleIntent("create-project", "ready", () => openDialog({ kind: "create", name: "", thenIssue: true }));
  useConsoleIntent("issue-key", selected ? (manageable(selected) && flow.step === "idle" ? "ready" : "unavailable") : state.status === "loading" ? "wait" : "unavailable", () => {
    if (selected) dispatch({ type: "openIssue", project: selected });
  });
  // Getting started's last step opens a project on its call samples. It waits
  // until the reads that size the page above them (keys, usage) and the
  // samples themselves (the installation) settle, so the heading stays in view.
  // The same reads as the project's page, which shows its call samples only while it is active.
  const summary = useQuery({ ...projectSummaryQuery(selected?.id ?? ""), enabled: selected !== null });
  const installation = useQuery({ ...installationQuery, enabled: selected !== null && manageable(selected) });
  const settled = (query: { isFetching: boolean; isError: boolean; data: unknown }) => !query.isFetching && (query.data !== undefined || query.isError);
  const callReadiness = !selected
    ? (state.status === "loading" ? "wait" : "unavailable")
    : !manageable(selected) ? "unavailable"
      : keys.status !== "loading" && settled(summary) && settled(installation) ? "ready" : "wait";
  useConsoleIntent("how-to-call", callReadiness, () => {
    revealInPageBody(document.getElementById(PROJECT_CALL_HEADING_ID));
  });

  // The archive dialog counts the keys it revokes from the latest project read,
  // or from the project's key list when that shows more. While the project list
  // is read again, or after that read failed, the count may be out of date, so
  // Archive waits.
  const archiving = dialog?.kind === "archive" ? byId.get(dialog.project.id) ?? dialog.project : null;
  const archiveKeys = useQuery({ ...projectKeysQuery(archiving?.id ?? ""), enabled: archiving !== null });
  const archiveActive = archiving ? archiveKeyCount(archiving, archiveKeys.data) : 0;
  const projectsStale = state.status === "failed" || refreshError !== null;
  const projectsSettled = state.status === "ready" && !projectsStale;

  const dialogNameProblem = dialog?.kind === "create"
    ? projectNameProblem(dialog.name, names)
    : dialog?.kind === "rename"
      ? projectNameProblem(dialog.name, names.filter((name) => name !== dialog.project.name))
      : null;
  const dialogReady = !dialogBusy && (dialog?.kind === "create" || dialog?.kind === "rename"
    ? isUsableName(dialog.name, dialogNameProblem) && !(dialog.kind === "rename" && normalizeName(dialog.name) === dialog.project.name)
    : dialog?.kind === "archive" && archiving
      ? projectsSettled && isArchiveConfirmed(archiving.name, archiveActive, dialog.typed)
      : Boolean(dialog));

  const runDialog = async () => {
    if (!dialog || !dialogReady) return;
    const current = dialog;
    setDialogBusy(true);
    setDialogError(null);
    try {
      if (current.kind === "create") {
        const project = await createProject(normalizeName(current.name));
        // The project page opens anew and finds the project in the list at once;
        // the re-read below confirms it. The key dialog follows as its intent.
        queryClient.setQueryData(projectsQuery.queryKey, (list) => (list && !list.some((entry) => entry.id === project.id) ? [...list, project] : list ?? [project]));
        navigate("projects", { id: project.id }, current.thenIssue ? "issue-key" : undefined);
      } else if (current.kind === "rename") {
        await renameProject(current.project.id, normalizeName(current.name));
      } else if (current.kind === "archive") {
        await archiveProject(current.project.id);
      } else {
        await revokeKey(current.project.id, current.key.id);
      }
      setDialog(null);
    } catch (error) {
      if (!isAbort(error)) setDialogError(flowError(error));
    } finally {
      setDialogBusy(false);
      // Confirmed or uncertain, re-read what the write may have changed so the
      // operator can check it; the write itself is never sent again.
      void invalidateProjects(queryClient, current.kind === "create"
        ? { activity: true }
        : current.kind === "rename"
          ? {}
          // Archiving revokes every key; keys, usage by key and write operations show revocation.
          : { projectId: current.project.id });
    }
  };

  const refreshing = state.status === "loading" || activity.isFetching || detailFetching;
  const refreshButton = <RefreshButton onClick={refreshAll} refreshing={refreshing} label={t("page.refresh")} />;
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
                  <button className="button danger" type="button" aria-label={t("actions.archiveLabel", { name: selected.name })} onClick={() => openDialog({ kind: "archive", project: selected, typed: "" })}>{t("actions.archive")}</button>
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
              busy={flowBusy || dialogBusy}
              onIssue={() => dispatch({ type: "openIssue", project: selected })}
              onRevoke={(key, activeCount) => openDialog({ kind: "revoke", project: selected, key, activeCount })}
            />
          ) : state.status === "loading" ? (
            <TableSkeleton label={t("page.loading")} rows={4} columns={6} />
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
      body = <ErrorState title={t("page.loadFailed")} detail={state.error} onRetry={refreshAll} />;
    } else if (state.status === "loading" && !projects.length) {
      body = <TableSkeleton label={t("page.loading")} rows={4} columns={5} />;
    } else if (!projects.length) {
      body = <EmptyState icon={FolderKanban} title={t("page.empty")} action={createButton} />;
    } else {
      body = (
        <>
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
                              <button className="text-action danger" type="button" aria-label={t("actions.archiveLabel", { name: project.name })} onClick={() => openDialog({ kind: "archive", project, typed: "" })}>{t("actions.archive")}</button>
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
            ) : dialog.kind === "archive" && archiving ? (
              <>
                <p><strong>{t("archiveDialog.prompt", { name: archiving.name })}</strong></p>
                <p>{archiveActive ? t("archiveDialog.keys", { count: archiveActive }) : t("archiveDialog.noKeys")}</p>
                <p>{t("archiveDialog.kept")}</p>
                {archiveActive ? (
                  <label className="field">
                    {/* The name as typed, inner spaces visible, so it can be read and copied exactly. */}
                    <span><Trans t={t} i18nKey="archiveDialog.typeName" components={{ name: <code className="confirm-name">{archiving.name}</code> }} /></span>
                    <input name="archive-confirm-name" value={dialog.typed} onChange={(event) => setDialog({ ...dialog, typed: event.target.value })} disabled={dialogBusy} autoComplete="off" spellCheck={false} />
                  </label>
                ) : null}
                {projectsStale ? <p className="key-flow-error" role="alert">{t("archiveDialog.stale")}</p> : null}
              </>
            ) : dialog.kind === "revoke" ? (
              <>
                <p>{t("revokeDialog.prompt", { name: dialog.key.name, prefix: prefixLabel(dialog.key.prefix) })}</p>
                {dialog.activeCount <= 1 ? <p>{t("revokeDialog.lastKey", { project: dialog.project.name })}</p> : null}
              </>
            ) : null}
            <FlowErrorMessage error={dialogError} name={dialog.kind === "create" || dialog.kind === "rename" ? normalizeName(dialog.name) : undefined} />
          </form>
        ) : null}
      </Modal>
    </>
  );
}
