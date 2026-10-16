import { type WorkspaceStorageSelection } from "@oac/agents-client";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { HelpTip, Section } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { CopyableId } from "../../components/list-ui";
import { Modal } from "../../components/Modal";
import { TableSkeleton } from "../../components/Skeleton";
import { knownCoreError } from "../../lib/core-error";
import { formatBytes } from "../../lib/format";
import { docsUrl } from "../../lib/docs";
import { admin } from "../../lib/projects";
import { Fact } from "../system/Fact";
import { storageCapacity, storageObject, workspaceStorageQuery } from "./workspace-storage";

function StorageEditor({ current, replacement, onClose }: { current: WorkspaceStorageSelection | null; replacement: boolean; onClose: () => void }) {
  const { t, i18n } = useTranslation("workspaceStorage");
  const { t: common } = useTranslation();
  const client = useQueryClient();
  const formId = useId();
  // Retain this identity across failed or uncertain saves. Credentials never enter the query cache.
  const [selection] = useState(current);
  const [configurationId] = useState(() => replacement ? crypto.randomUUID() : selection?.id ?? "");
  const selectionChanged = !replacement && (!selection || current?.id !== selection.id);
  const [adapter, setAdapter] = useState(current?.adapter ?? "");
  const [parameters, setParameters] = useState(JSON.stringify(current?.parameters ?? {}, null, 2));
  const [credential, setCredential] = useState("");
  const [capacity, setCapacity] = useState(String(current?.tenant_capacity_mib ?? 0));
  const [busy, setBusy] = useState(false);
  const writing = useRef(false);
  const [error, setError] = useState("");
  const [observed, setObserved] = useState<string | null>(null);
  const refresh = async () => {
    try {
      const latest = await client.fetchQuery({ ...workspaceStorageQuery, staleTime: 0 });
      setObserved(latest ? `${latest.id} · ${latest.tenant_capacity_mib.toLocaleString()} MiB` : t("notConfigured"));
    } catch { setError(t("refreshFailed")); }
  };
  const close = () => { if (!writing.current) { setCredential(""); onClose(); } };
  const save = async () => {
    if (writing.current || selectionChanged) return;
    const quota = storageCapacity(capacity);
    const parsed = replacement ? storageObject(parameters) : selection?.parameters;
    const secret = credential.trim() ? storageObject(credential) : undefined;
    if (quota === null || !parsed || !adapter.trim() || secret === null) { setError(t("invalid")); return; }
    writing.current = true; setBusy(true); setError("");
    try {
      const result = await admin.configureWorkspaceStorage({ id: configurationId, adapter: replacement ? adapter.trim() : selection!.adapter, parameters: parsed, tenant_capacity_mib: quota, ...(secret === undefined ? {} : { credential: secret }) }, { signal: AbortSignal.timeout(30_000) });
      setCredential("");
      await client.cancelQueries({ queryKey: workspaceStorageQuery.queryKey });
      client.setQueryData(workspaceStorageQuery.queryKey, result);
      onClose();
    } catch (cause) {
      // Never render raw server messages: adapter credentials may appear in a malformed response.
      setError(knownCoreError(cause, common) ?? t("uncertain"));
      void client.invalidateQueries({ queryKey: workspaceStorageQuery.queryKey });
    } finally { writing.current = false; setBusy(false); }
  };
  return <Modal open title={t(replacement ? "configure" : "editCapacity")} onClose={close} footer={<><button className="button outline" onClick={close} disabled={busy}>{t("cancel")}</button><button className="button primary" type="submit" form={formId} disabled={busy || selectionChanged}>{t(busy ? "saving" : "save")}</button></>}>
    <form id={formId} className="form-stack" onSubmit={(event) => { event.preventDefault(); void save(); }}>
      {replacement ? <>
        <p>{t("newSelection")}</p>
        <a href={docsUrl("docs/getting-started/workspace-storage", i18n.resolvedLanguage)} target="_blank" rel="noreferrer">{t("instructions")}</a>
        <div className="field"><div className="field-label-row"><label htmlFor={`${formId}-adapter`}>{t("adapter")}</label><HelpTip>{t("adapterHelp")}</HelpTip></div><input id={`${formId}-adapter`} value={adapter} onChange={(event) => setAdapter(event.target.value)} required disabled={busy} /></div>
        <div className="field"><div className="field-label-row"><label htmlFor={`${formId}-parameters`}>{t("parameters")}</label><HelpTip>{t("parametersHelp")}</HelpTip></div><textarea id={`${formId}-parameters`} rows={8} value={parameters} onChange={(event) => setParameters(event.target.value)} disabled={busy} spellCheck={false} /></div>
        <div className="field"><div className="field-label-row"><label htmlFor={`${formId}-credential`}>{t("credential")}</label><HelpTip>{t("credentialHelp")}</HelpTip></div><input id={`${formId}-credential`} type="password" value={credential} onChange={(event) => setCredential(event.target.value)} disabled={busy} autoComplete="new-password" spellCheck={false} /></div>
      </> : null}
      <div className="field"><div className="field-label-row"><label htmlFor={`${formId}-capacity`}>{t("capacityInput")}</label><HelpTip>{t("capacityHelp")}</HelpTip></div><input id={`${formId}-capacity`} type="number" min={0} max={4294967295} step={1} required value={capacity} onChange={(event) => setCapacity(event.target.value)} disabled={busy} /></div>
      {selectionChanged ? <p role="alert" className="confirm-dialog-error">{t("selectionChanged")}</p> : null}
      {error ? <><p role="alert" className="confirm-dialog-error">{error}</p><button type="button" className="button outline" disabled={busy} onClick={() => void refresh()}>{t("refresh")}</button></> : null}
      {observed ? <p role="status">{t("observed", { value: observed })}</p> : null}
    </form>
  </Modal>;
}

export function WorkspaceStorageSettings() {
  const { t } = useTranslation("workspaceStorage");
  const query = useQuery(workspaceStorageQuery);
  const [editor, setEditor] = useState<"configuration" | "capacity" | null>(null);
  const current = query.data;
  const blocked = query.isError || query.isFetching;
  return <Section headingId="workspace-storage-heading" title={t("title")} help={t("help")} actions={current !== undefined ? <button className="button outline" disabled={blocked} onClick={() => setEditor("configuration")}>{t("configure")}</button> : undefined}>
    {query.isError ? <ErrorState title={t(current === undefined ? "readFailed" : "refreshFailed")} onRetry={() => void query.refetch()} /> : null}
    {current === undefined && !query.isError ? <TableSkeleton rows={4} columns={2} /> : null}
    {current ? <div className="settings-rows">
      <Fact label={t("adapter")}>{current.adapter}</Fact>
      <Fact label={t("capacity")} help={t("capacityHelp")} action={<button className="button outline" disabled={blocked} onClick={() => setEditor("capacity")}>{t("editCapacity")}</button>}>{current.tenant_capacity_mib === 0 ? t("noQuota") : formatBytes(current.tenant_capacity_mib * 1024 * 1024)}</Fact>
      {/* The immutable identity and the adapter's raw parameters matter only when restoring or preparing a new configuration. */}
      <details className="settings-details">
        <summary>{t("details")}</summary>
        <Fact label={t("id")}><CopyableId id={current.id} /></Fact>
        <Fact label={t("parameters")} help={t("parametersHelp")}><pre className="system-code">{JSON.stringify(current.parameters, null, 2)}</pre></Fact>
      </details>
    </div> : current === null ? <div className="settings-rows"><p className="settings-empty">{t("notConfigured")}</p></div> : null}
    {editor && current !== undefined ? <StorageEditor current={current} replacement={editor === "configuration"} onClose={() => setEditor(null)} /> : null}
  </Section>;
}
