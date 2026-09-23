import { useEffect, useState } from "react";
import type { SandboxDirectoryNode } from "@agents-core-web/agents-client";
import { useLocale } from "../../lib/LocaleProvider";
import { useSandboxClient } from "./SandboxContext";

export function SandboxNodeSelector({ value, onChange, disabled }: {
  value: string; onChange: (value: string) => void; disabled: boolean;
}) {
  const { t, locale } = useLocale();
  const client = useSandboxClient();
  const [nodes, setNodes] = useState<SandboxDirectoryNode[]>([]);
  const [error, setError] = useState(false);
  const [loading, setLoading] = useState(true);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setNodes([]); setError(false); setLoading(true);
    if (!client) { setLoading(false); return; }
    void client.listSandboxNodes({ signal: controller.signal }).then((result) => {
      if (!controller.signal.aborted) setNodes(result.data);
    }).catch(() => {
      if (!controller.signal.aborted) setError(true);
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [client, revision]);
  return <div className="form-stack" lang={locale}>
    <label className="field"><span>{t("Sandbox node")}</span>
      <select aria-label={t("Sandbox node")} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}>
        <option value="">{t("Automatic placement")}</option>
        {value && !nodes.some((node) => node.id === value) ? <option value={value}>{value} — {t("unavailable")}</option> : null}
        {nodes.map((node) => <option key={node.id} value={node.id} disabled={!node.available}>{node.name} — {node.available ? t("available") : t("unavailable")}</option>)}
      </select>
    </label>
    <small>{t("Optional. Local nodes run on the Core server. A selected node must be available; Core will not fall back to another node.")}</small>
    {loading ? <p role="status">{t("Loading nodes…")}</p> : null}
    {error ? <div role="alert">{t("Node directory unavailable. Automatic placement remains available; an explicit choice is never replaced automatically.")} <button type="button" disabled={disabled} onClick={() => setRevision((v) => v + 1)}>{t("Retry directory")}</button></div> : null}
    {!loading && !error && nodes.length === 0 ? <p>{t("No nodes are registered.")}</p> : null}
  </div>;
}
