import { Plus, RefreshCw, Search } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { AgentCore, EnvironmentTemplate } from "@agents-core-web/agents-client";
import { Modal } from "../../components/Modal";
import { ErrorState } from "../../components/ErrorState";
import { formatDashboardTimestamp } from "../dashboard/dashboard-model";
import type { EnvironmentTemplateCatalog } from "../sessions/environment/environment-templates";
import { TemplateForm } from "./TemplateForm";
import { createTemplateWriteScope, templateName, templatePatch, type TemplateDraft } from "./template-editor";
import "./EnvironmentTemplatesView.css";

export interface EnvironmentTemplatesViewProps {
  catalog: EnvironmentTemplateCatalog | null;
  operations: Pick<AgentCore, "createEnvironmentTemplate" | "updateEnvironmentTemplate" | "deleteEnvironmentTemplate">;
  onRefresh: () => Promise<void>;
  onConfigureConnection: () => void;
}

type Dialog = { kind: "create" } | { kind: "edit" | "delete"; template: EnvironmentTemplate } | null;

export function EnvironmentTemplatesView({ catalog, operations, onRefresh, onConfigureConnection }: EnvironmentTemplatesViewProps) {
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState<Dialog>(null);
  const [busy, setBusy] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
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
  const needle = query.trim().toLocaleLowerCase();
  const visible = templates.filter((template) => [templateName(template), template.id, template.network.access].some((value) => value.toLocaleLowerCase().includes(needle)));

  const refresh = async () => {
    if (refreshPending.current || writePending.current) return;
    refreshPending.current = true;
    setRefreshing(true);
    try { await onRefresh(); }
    catch { if (mounted.current) setError("The catalog could not be refreshed. Check the connection and try again."); }
    finally { refreshPending.current = false; if (mounted.current) setRefreshing(false); }
  };

  const write = async (request: (signal: AbortSignal) => Promise<unknown>, success: string) => {
    if (blocked || writePending.current) return;
    writePending.current = true;
    setBusy(true); setError(null); setNotice(null);
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
      setNotice(success);
      if (result.kind === "saved-refresh-failed") {
        failedCatalog.current = catalog;
        setNeedsRefresh(true);
        setError("The change was saved, but the catalog refresh failed. Refresh before making another change.");
      }
    }
  };

  const save = (draft: TemplateDraft) => {
    if (dialog?.kind === "create") {
      void write((signal) => operations.createEnvironmentTemplate({ name: draft.name.trim() || null, network: { access: draft.access } }, { signal }), "Template created. No Runtime was allocated.");
    } else if (dialog?.kind === "edit") {
      const template = dialog.template;
      const patch = templatePatch(template, draft);
      if (Object.keys(patch).length) void write((signal) => operations.updateEnvironmentTemplate(template.id, patch, { signal }), "Template updated. Existing Sessions keep their configuration.");
    }
  };
  const close = () => { if (!busy) setDialog(null); };

  return (
    <section className="page-section templates-page" aria-labelledby="templates-heading">
      <header className="page-header">
        <div><h1 id="templates-heading">Environment Templates</h1><p>Reusable configuration for managed Sessions.</p></div>
        <div className="page-actions">
          <button className="button outline" type="button" disabled={busy || refreshing} onClick={() => void refresh()}><RefreshCw size={14} aria-hidden="true" />{refreshing ? "Refreshing…" : "Refresh"}</button>
          <button className="button primary" type="button" disabled={blocked} onClick={() => setDialog({ kind: "create" })}><Plus size={14} aria-hidden="true" />New Template</button>
        </div>
      </header>
      <div className="templates-content">
        <p className="templates-boundary">Manage names and network access for basic Templates. Select a saved Template when starting a Session. Saving configuration does not start a Runtime.</p>
        {notice ? <p className="notice success" role="status">{notice}</p> : null}
        {error ? <p className="notice warning" role="alert">{error}</p> : null}
        {catalog === null ? <p role="status" aria-busy="true">Loading Environment Templates…</p> : catalog.state === "unsupported" ? (
          <ErrorState title="Environment Templates are not available" description="This Core does not expose the Template resource. No catalog is inferred." action={<button className="button outline" onClick={onConfigureConnection}>Configure connection</button>} />
        ) : catalog.state === "failed" ? (
          <ErrorState title="Environment Templates could not be loaded" description="Check the Core connection, access permissions, and supported Template profile. The catalog is unavailable." action={<button className="button outline" onClick={onConfigureConnection}>Configure connection</button>} />
        ) : (
          <>
            <div className="templates-toolbar">
              <label className="templates-search"><Search size={15} aria-hidden="true" /><span className="sr-only">Filter Templates</span><input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter by name, ID, or network" /></label>
              <span>{visible.length} of {templates.length} Templates</span>
            </div>
            {templates.length === 0 ? <div className="templates-empty"><h2>No Environment Templates yet</h2><p>Create a reusable configuration, then select it when starting a Session.</p></div> : visible.length === 0 ? <div className="templates-empty"><h2>No matching Templates</h2><button className="button outline" onClick={() => setQuery("")}>Clear filter</button></div> : (
              <div className="templates-list" aria-label="Environment Templates">
                {visible.map((template) => (
                  <article className="template-row" key={template.id}>
                    <div className="template-identity"><h2>{templateName(template)}</h2><code>{template.id}</code><small>Updated {formatDashboardTimestamp(template.updated_at)}</small></div>
                    <dl className="template-summary"><div><dt>Network</dt><dd>{template.network.access === "enabled" ? "Enabled" : "Disabled"}</dd></div><div><dt>Allowed domains</dt><dd>{template.network.allowed_domains.length}</dd></div></dl>
                    <div className="template-row-actions"><button className="button outline" type="button" disabled={blocked} aria-label={`Edit ${templateName(template)}`} onClick={() => setDialog({ kind: "edit", template })}>Edit</button><button className="button outline" type="button" disabled={blocked} aria-label={`Delete ${templateName(template)}`} onClick={() => setDialog({ kind: "delete", template })}>Delete</button></div>
                  </article>
                ))}
              </div>
            )}
          </>
        )}
      </div>
      <Modal open={dialog !== null} title={dialog?.kind === "delete" ? "Delete Template?" : dialog?.kind === "edit" ? "Edit Template" : "Create Template"} onClose={close}>
        {dialog?.kind === "delete" ? <div className="template-delete">
          <p>Delete <strong>{templateName(dialog.template)}</strong>?</p><code>{dialog.template.id}</code>
          <p>New Sessions will no longer be able to select this Template. Existing Sessions keep their configuration. This action cannot be undone.</p>
          <div className="template-form-actions"><button className="button outline" disabled={busy} onClick={close}>Cancel</button><button className="button danger" disabled={blocked} onClick={() => { const template = dialog.template; void write((signal) => operations.deleteEnvironmentTemplate(template.id, { signal }), "Template deleted."); }}>{busy ? "Deleting…" : "Delete Template"}</button></div>
        </div> : dialog ? <TemplateForm key={dialog.kind === "edit" ? dialog.template.id : "new"} template={dialog.kind === "edit" ? dialog.template : undefined} busy={blocked} onSave={save} onCancel={close} /> : null}
      </Modal>
    </section>
  );
}
