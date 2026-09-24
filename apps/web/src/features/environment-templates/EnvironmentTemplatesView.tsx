import { Boxes, Plus, Search } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { AgentCore, EnvironmentTemplateResource } from "@agents-core-web/agents-client";
import { EmptyState, HelpTip, PageBody, PageHeader, RefreshButton, StatusDot } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { useToast } from "../../components/Toast";
import { formatDateTime, MISSING } from "../../lib/format";
import type { EnvironmentTemplateCatalog } from "../sessions/environment/environment-templates";
import { TemplateDetailPage, type TemplateLinks } from "./TemplateDetail";
import { TemplateForm } from "./TemplateForm";
import { createTemplateWriteScope, draftNetwork, templateName, templatePatch, type TemplateDraft } from "./template-editor";
import "./EnvironmentTemplatesView.css";

export interface EnvironmentTemplatesViewProps extends TemplateLinks {
  catalog: EnvironmentTemplateCatalog | null;
  operations: Pick<AgentCore, "createEnvironmentTemplate" | "updateEnvironmentTemplate" | "deleteEnvironmentTemplate">;
  onRefresh: () => Promise<void>;
  onConfigureConnection: () => void;
}

type Dialog = { kind: "create" } | { kind: "edit" | "delete"; template: EnvironmentTemplateResource } | null;

/** Filters by name or ID; the catalog holds every page, so this covers all Templates. */
export function filterTemplates(templates: readonly EnvironmentTemplateResource[], query: string): EnvironmentTemplateResource[] {
  const needle = query.trim().toLocaleLowerCase();
  return templates.filter((template) => [templateName(template), template.id].some((value) => value.toLocaleLowerCase().includes(needle)));
}

function count(value: readonly unknown[] | undefined): string | number {
  return value === undefined ? MISSING : value.length;
}

export function EnvironmentTemplatesView({ catalog, operations, onRefresh, onConfigureConnection, onOpenFile, onOpenSkill }: EnvironmentTemplatesViewProps) {
  const { t, i18n } = useTranslation("templates");
  const { t: tPages } = useTranslation("pages");
  const { t: tCommon } = useTranslation("common");
  const toast = useToast();
  const locale = i18n.resolvedLanguage;
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState<Dialog>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [needsRefresh, setNeedsRefresh] = useState(false);
  const scope = useRef(createTemplateWriteScope());
  const mounted = useRef(true);
  const refreshPending = useRef(false);
  const writePending = useRef(false);
  const failedCatalog = useRef<EnvironmentTemplateCatalog | null>(null);

  useEffect(() => {
    mounted.current = true;
    scope.current = createTemplateWriteScope();
    return () => { mounted.current = false; scope.current.dispose(); };
  }, []);

  useEffect(() => {
    if (needsRefresh && catalog?.state === "ready" && catalog !== failedCatalog.current) setNeedsRefresh(false);
  }, [catalog, needsRefresh]);

  const ready = catalog?.state === "ready";
  const blocked = busy || refreshing || needsRefresh || !ready;
  const templates = ready ? catalog.templates : [];
  const visible = filterTemplates(templates, query);
  const selected = selectedId ? templates.find((template) => template.id === selectedId) ?? null : null;

  useEffect(() => {
    // A Template that the refreshed catalog no longer lists closes its detail.
    if (selectedId && ready && !selected) setSelectedId(null);
  }, [ready, selected, selectedId]);

  const refresh = async () => {
    if (refreshPending.current || writePending.current) return;
    refreshPending.current = true;
    setRefreshing(true);
    try { await onRefresh(); }
    catch { if (mounted.current) setError(t("refreshFailed")); }
    finally { refreshPending.current = false; if (mounted.current) setRefreshing(false); }
  };

  const write = async (request: (signal: AbortSignal) => Promise<unknown>, success: string, after?: () => void) => {
    if (blocked || writePending.current) return;
    writePending.current = true;
    setBusy(true); setError(null);
    const result = await scope.current.run(request, onRefresh);
    writePending.current = false;
    if (!mounted.current || result.kind === "ignored") return;
    setBusy(false);
    setDialog(null);
    if (result.kind === "failed") {
      failedCatalog.current = catalog;
      setNeedsRefresh(true);
      setError(result.message);
    } else {
      toast.show(success, { tone: "success" });
      after?.();
      if (result.kind === "saved-refresh-failed") {
        failedCatalog.current = catalog;
        setNeedsRefresh(true);
        setError(t("savedRefreshFailed"));
      }
    }
  };

  const save = (draft: TemplateDraft) => {
    if (dialog?.kind === "create") {
      void write((signal) => operations.createEnvironmentTemplate({ name: draft.name.trim() || null, network: draftNetwork(draft) }, { signal }), t("created"));
    } else if (dialog?.kind === "edit") {
      const template = dialog.template;
      const patch = templatePatch(template, draft);
      if (Object.keys(patch).length) void write((signal) => operations.updateEnvironmentTemplate(template.id, patch, { signal }), t("updated"));
    }
  };
  const close = () => { if (!busy) setDialog(null); };
  const errorNote: ReactNode = error ? <p className="coverage-note coverage-note-error" role="alert">{error}</p> : null;

  const modal = (
    <Modal open={dialog !== null} title={dialog?.kind === "delete" ? t("modal.delete") : dialog?.kind === "edit" ? t("modal.edit") : t("modal.create")} onClose={close}>
      {dialog?.kind === "delete" ? <div className="template-delete">
        <p>{t("deletePrompt", { name: templateName(dialog.template) })}</p><code>{dialog.template.id}</code>
        <p>{t("deleteWarning")}</p>
        <div className="template-form-actions"><button className="button outline" disabled={busy} onClick={close}>{tCommon("actions.cancel")}</button><button className="button danger" disabled={blocked} onClick={() => { const template = dialog.template; void write((signal) => operations.deleteEnvironmentTemplate(template.id, { signal }), t("deleted"), () => setSelectedId((current) => current === template.id ? null : current)); }}>{busy ? t("deleting") : t("deleteTemplate")}</button></div>
      </div> : dialog ? <TemplateForm key={dialog.kind === "edit" ? dialog.template.id : "new"} template={dialog.kind === "edit" ? dialog.template : undefined} busy={blocked} onSave={save} onCancel={close} /> : null}
    </Modal>
  );

  if (selected) {
    return (
      <>
        <TemplateDetailPage
          template={selected}
          blocked={blocked}
          refreshing={refreshing}
          notice={errorNote}
          onBack={() => setSelectedId(null)}
          onRefresh={() => void refresh()}
          onEdit={() => setDialog({ kind: "edit", template: selected })}
          onDelete={() => setDialog({ kind: "delete", template: selected })}
          onOpenFile={onOpenFile}
          onOpenSkill={onOpenSkill}
        />
        {modal}
      </>
    );
  }

  const configure = <button className="button outline" type="button" onClick={onConfigureConnection}>{t("configureConnection")}</button>;
  let body: ReactNode;
  if (catalog === null) {
    body = <p className="page-status" role="status" aria-busy="true">{t("loading")}</p>;
  } else if (catalog.state === "unsupported") {
    body = <EmptyState icon={Boxes} title={t("unavailableTitle")} description={t("unavailableDescription")} action={configure} />;
  } else if (catalog.state === "failed") {
    body = <EmptyState title={t("loadFailedTitle")} description={t("loadFailedDescription")} action={configure} />;
  } else if (templates.length === 0) {
    body = <EmptyState icon={Boxes} title={t("emptyTitle")} description={t("emptyDescription")} />;
  } else {
    body = (
      <>
        <div className="filter-bar">
          <label className="search-control templates-search">
            <Search size={14} strokeWidth={1.5} aria-hidden="true" />
            <input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("filterPlaceholder")} aria-label={t("filterLabel")} />
          </label>
          <span className="filter-count" role="status">{t("count", { visible: visible.length, total: templates.length })}</span>
        </div>
        {visible.length === 0 ? (
          <EmptyState title={t("noMatch")} action={<button className="button outline" type="button" onClick={() => setQuery("")}>{t("clearFilter")}</button>} />
        ) : (
          <div className="table-frame">
            <table className="data-table templates-table" aria-label={t("listLabel")}>
              <thead>
                <tr>
                  <th scope="col">{t("columns.name")}</th>
                  <th scope="col">{t("columns.network")}</th>
                  <th scope="col" className="numeric">{t("columns.packages")}</th>
                  <th scope="col" className="numeric">{t("columns.files")}</th>
                  <th scope="col" className="numeric">{t("columns.skills")}</th>
                  <th scope="col" className="numeric">{t("columns.plugins")}</th>
                  <th scope="col">{t("columns.updated")}</th>
                  <th scope="col"><span className="visually-hidden">{t("columns.actions")}</span></th>
                </tr>
              </thead>
              <tbody>
                {visible.map((template) => {
                  const name = templateName(template);
                  const packages = template.packages;
                  return (
                    <tr key={template.id} className="clickable-row" onClick={() => setSelectedId(template.id)}>
                      <th scope="row">
                        <button className="table-link" type="button" aria-label={t("open", { name })} onClick={(event) => { event.stopPropagation(); setSelectedId(template.id); }}>
                          <strong>{name}</strong>
                          <code>{template.id}</code>
                        </button>
                        {template.unrecognized ? (
                          <span className="status-with-help template-flag" onClick={(event) => event.stopPropagation()}>
                            <StatusDot tone="warning" label={t("unrecognized")} />
                            <HelpTip>{t("unrecognizedHelp")}</HelpTip>
                          </span>
                        ) : null}
                      </th>
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
                      <td className="row-actions">
                        <button className="text-action" type="button" disabled={blocked} aria-label={t("editLabel", { name })} onClick={(event) => { event.stopPropagation(); setDialog({ kind: "edit", template }); }}>{t("edit")}</button>
                        <button className="text-action" type="button" disabled={blocked} aria-label={t("deleteLabel", { name })} onClick={(event) => { event.stopPropagation(); setDialog({ kind: "delete", template }); }}>{t("delete")}</button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </>
    );
  }

  return (
    <>
      <section className="page-section console-page templates-page" aria-labelledby="templates-heading">
        <PageHeader
          headingId="templates-heading"
          title={tPages("templates.title")}
          help={<>{tPages("templates.subtitle")} {t("boundary")}</>}
          actions={(
            <>
              <RefreshButton disabled={busy} refreshing={refreshing} onClick={() => void refresh()} />
              <button className="button primary" type="button" disabled={blocked} onClick={() => setDialog({ kind: "create" })}><Plus size={14} aria-hidden="true" />{tPages("templates.newTemplate")}</button>
            </>
          )}
        />
        <PageBody>
          {errorNote}
          {body}
        </PageBody>
      </section>
      {modal}
    </>
  );
}
