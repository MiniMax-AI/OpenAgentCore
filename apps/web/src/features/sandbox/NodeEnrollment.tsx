import { createPortal } from "react-dom";
import { Check, Copy, Plus, Terminal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { SandboxAdminClient, SandboxDeployment, SandboxNode } from "@agents-core-web/agents-client";
import { Modal } from "../../components/Modal";
import { useLocale } from "../../lib/LocaleProvider";
import { sandboxRequestError } from "../../lib/sandbox-labels";
import { sandboxCoreOrigin } from "./core-origin";
import type { SandboxConsoleConfig } from "./console-config";
import { nodeInstallCommand } from "./enrollment-command";

export function NodeEnrollment({ client, consoleConfig, deployment, nodes, disabled, fresh, onRefresh }: {
  client: SandboxAdminClient;
  consoleConfig: SandboxConsoleConfig;
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  disabled: boolean;
  fresh: boolean;
  onRefresh: () => void;
}) {
  const { t, locale } = useLocale();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [enrollment, setEnrollment] = useState<{ token: string; expires_at: string } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const [now, setNow] = useState(Date.now());
  const generation = useRef(0);
  const request = useRef<AbortController | null>(null);
  const knownIds = useRef(new Set<string>());
  const sourceUrl = sandboxCoreOrigin(window.location.origin);
  const coreUrl = sandboxCoreOrigin(deployment.core_url || window.location.origin);
  const available = Boolean(consoleConfig.node_installer && sourceUrl && coreUrl);
  const expired = Boolean(enrollment && new Date(enrollment.expires_at).getTime() <= now);
  const connected = fresh && nodes.find((node) => !knownIds.current.has(node.id) && node.online && node.provider_ready);
  const command = enrollment && available && !expired && !connected
    ? nodeInstallCommand(enrollment.token, coreUrl!, sourceUrl!, deployment.provider, deployment.installation_id, consoleConfig.node_installer_sha256) : "";
  useEffect(() => () => { generation.current++; request.current?.abort(); }, []);
  useEffect(() => {
    if (!open || !enrollment) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [open, enrollment]);
  useEffect(() => {
    if (!open || !enrollment || expired) return;
    const interval = window.setInterval(onRefresh, 3000);
    return () => window.clearInterval(interval);
  }, [open, enrollment, expired, onRefresh]);
  function close() {
    generation.current++;
    request.current?.abort(); request.current = null;
    setOpen(false); setEnrollment(null); setError(null); setBusy(false);
    setCopied(false); setCopyFailed(false);
  }
  async function generate() {
    if (request.current || !available) return;
    generation.current++;
    const controller = new AbortController(); request.current = controller;
    knownIds.current = new Set(nodes.map((node) => node.id));
    setBusy(true); setEnrollment(null); setError(null); setCopied(false); setCopyFailed(false);
    try {
      // Capture the current node set before issuing a one-time enrollment token.
      const current = await client.listNodes({ signal: controller.signal });
      if (controller.signal.aborted) return;
      knownIds.current = new Set(current.data.map((node) => node.id));
      const result = await client.createEnrollment({ signal: controller.signal });
      if (!controller.signal.aborted) { setEnrollment(result); setNow(Date.now()); }
    } catch (error) { if (!controller.signal.aborted) setError(error); }
    finally { if (!controller.signal.aborted) { request.current = null; setBusy(false); } }
  }
  async function copyCommand() {
    const current = generation.current;
    try { await navigator.clipboard.writeText(command); if (current === generation.current) { setCopied(true); setCopyFailed(false); } }
    catch { if (current === generation.current) { setCopied(false); setCopyFailed(true); } }
  }
  return <>
    <button type="button" className="button primary" disabled={disabled} onClick={() => { setOpen(true); void generate(); }}><Plus size={16} />{t("Add node")}</button>
    {createPortal(<Modal open={open} title={t("Add node")} onClose={close}>
      <div className="sandbox-add-node form-stack">
        {!available ? <p role="status">{t("Node installation is unavailable. Ask the deployment administrator to enable the node installer on this console.")}</p> : <>
          {!connected ? <p>{t("Run on the host you want to add.")}</p> : null}
          {busy ? <p role="status">{t("Preparing your command…")}</p> : null}
          {error !== null ? <><p role="alert" className="sandbox-error">{sandboxRequestError(error, locale)}</p><button className="button outline" type="button" onClick={() => void generate()}>{t("Try again")}</button></> : null}
          {enrollment ? <>
            {command ? <div className="sandbox-command"><div className="sandbox-command-heading"><span><Terminal size={15} />{t("Terminal")}</span><button type="button" className="button outline" aria-label={copied ? t("Copied") : t("Copy node command")} onClick={() => void copyCommand()}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t("Copied") : t("Copy command")}</button></div><label className="field"><span className="sr-only">{t("One-time enrollment command")}</span><textarea readOnly rows={5} value={command} onClick={(event) => event.currentTarget.select()} spellCheck={false} /></label></div> : null}
            {copyFailed && !connected ? <p role="alert">{t("Select the command above and copy it manually.")}</p> : null}
            <div className={`sandbox-enrollment-status ${connected ? "connected" : ""}`} role="status"><span className="sandbox-status-dot" />{connected ? `${connected.name} · ${t("Connected")}` : expired ? t("Command expired") : t("Waiting for your node to connect…")}</div>
            {!fresh ? <p>{t("Connection status unavailable. Refresh to check your node.")}</p> : null}
            {connected ? <button className="button primary" type="button" onClick={close}>{t("Done")}</button> : <>
            <p className="sandbox-command-expiry">{expired ? t("Generate a new command to continue.") : `${t("One-time enrollment token expires")} ${new Date(enrollment.expires_at).toLocaleTimeString(locale)}.`}</p>
            {expired ? <button className="button primary" type="button" onClick={() => void generate()}>{t("Generate new command")}</button> : null}
            </>}
          </> : null}
        </>}
      </div>
    </Modal>, document.body)}
  </>;
}
