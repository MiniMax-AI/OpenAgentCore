import { createPortal } from "react-dom";
import { Check, Copy, Plus, Terminal } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import type { SandboxAdminClient, SandboxDeployment, SandboxNode } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";
import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { sandboxRequestError } from "../../lib/sandbox-labels";
import { sandboxCoreOrigin } from "./core-origin";
import type { SandboxConsoleConfig } from "./console-config";
import { nodeInstallCommand } from "./enrollment-command";

/**
 * Add node: the administrator sets the node's sandbox limits, then Core issues a
 * one-time enrollment command that approves them
 * (`POST /core/v1/sandbox/enrollment-tokens`). Only microsandbox suspends
 * sandboxes, so only it asks for a retained limit; Docker retains exactly the
 * sandboxes it runs at once.
 */
export function NodeEnrollment({ client, consoleConfig, deployment, nodes, disabled, fresh, onRefresh, onConnected }: {
  client: SandboxAdminClient;
  consoleConfig: SandboxConsoleConfig;
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  disabled: boolean;
  fresh: boolean;
  onRefresh: () => void;
  onConnected?: (id: string) => void;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const id = useId();
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState("2");
  const [retained, setRetained] = useState("8");
  const [busy, setBusy] = useState(false);
  const [enrollment, setEnrollment] = useState<{ token: string; expires_at: string } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const [now, setNow] = useState(Date.now());
  const generation = useRef(0);
  const request = useRef<AbortController | null>(null);
  const knownIds = useRef(new Set<string>());
  const revealedId = useRef<string | null>(null);
  const sourceUrl = sandboxCoreOrigin(window.location.origin);
  const coreUrl = sandboxCoreOrigin(deployment.core_url || window.location.origin);
  const available = Boolean(consoleConfig.node_installer && sourceUrl && coreUrl);
  // Core takes whole numbers from 1 to a million, with the retained limit at least the active one.
  const suspends = deployment.provider === "microsandbox";
  const whole = (value: string) => (/^\d+$/.test(value.trim()) ? Number(value.trim()) : null);
  const inRange = (limit: number | null): limit is number => limit !== null && limit >= 1 && limit <= 1_000_000;
  const activeLimit = whole(active);
  const retainedLimit = suspends ? whole(retained) : activeLimit;
  const activeProblem = inRange(activeLimit) ? null : t("Enter a whole number from 1 to 1,000,000.");
  const retainedProblem = !suspends ? null
    : !inRange(retainedLimit) ? t("Enter a whole number from 1 to 1,000,000.")
    : inRange(activeLimit) && retainedLimit < activeLimit ? t("Enter at least the number of sandboxes at once.") : null;
  const limitsReady = !activeProblem && !retainedProblem;
  const expired = Boolean(enrollment && new Date(enrollment.expires_at).getTime() <= now);
  const connected = enrollment && fresh ? nodes.find((node) => !knownIds.current.has(node.id) && node.online && node.provider_ready) : undefined;
  const command = enrollment && available && !expired && !connected
    ? nodeInstallCommand(enrollment.token, coreUrl!, sourceUrl!, deployment.provider, deployment.installation_id, consoleConfig.node_installer_sha256) : "";
  useEffect(() => () => { generation.current++; request.current?.abort(); }, []);
  useEffect(() => {
    if (!open || !enrollment || !connected || revealedId.current === connected.id) return;
    revealedId.current = connected.id;
    onConnected?.(connected.id);
  }, [open, enrollment, connected, onConnected]);
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
  function start() {
    setActive("2"); setRetained("8");
    setOpen(true);
  }
  function close() {
    generation.current++;
    request.current?.abort(); request.current = null;
    setOpen(false); setEnrollment(null); setError(null); setBusy(false);
    setCopied(false); setCopyFailed(false);
  }
  /**
   * Back to the limits, whose first field takes focus again. The command already
   * shown is abandoned (it lapses when it expires); Generate issues a fresh one.
   */
  function changeLimits() {
    generation.current++;
    setEnrollment(null); setError(null); setCopied(false); setCopyFailed(false);
  }
  async function generate() {
    if (request.current || !available || !limitsReady || activeLimit === null || retainedLimit === null) return;
    const capacity = { max_active: activeLimit, max_retained: retainedLimit };
    generation.current++;
    const controller = new AbortController(); request.current = controller;
    knownIds.current = new Set(nodes.map((node) => node.id));
    revealedId.current = null;
    setBusy(true); setError(null); setCopied(false); setCopyFailed(false);
    try {
      // Capture the current node set before issuing a one-time enrollment token.
      const current = await client.listNodes({ signal: controller.signal });
      if (controller.signal.aborted) return;
      knownIds.current = new Set(current.data.map((node) => node.id));
      const result = await client.createEnrollment({ signal: controller.signal }, capacity);
      if (!controller.signal.aborted) { setEnrollment(result); setNow(Date.now()); }
    } catch (error) {
      // The failure stays in the dialog, on the limits, so they can be corrected and sent again.
      if (!controller.signal.aborted) { setEnrollment(null); setError(error); }
    }
    finally { if (!controller.signal.aborted) { request.current = null; setBusy(false); } }
  }
  async function copyCommand() {
    const current = generation.current;
    try { await navigator.clipboard.writeText(command); if (current === generation.current) { setCopied(true); setCopyFailed(false); } }
    catch { if (current === generation.current) { setCopied(false); setCopyFailed(true); } }
  }
  // The host needs what this deployment's backend needs: a Docker engine, or Linux with KVM.
  const hostRequirements = <HelpTip>{t(suspends ? "The host needs Linux with KVM: /dev/kvm must be available to the node. Each sandbox runs as a small virtual machine with its own disks." : "The host needs Docker Engine that enforces CPU and memory limits, and the node needs access to its socket. Each sandbox runs as a container.")}</HelpTip>;
  const limitsForm = `${id}-limits`;
  const footer = !available ? undefined
    : !enrollment ? <>
      <button className="button outline" type="button" onClick={close}>{t("Cancel")}</button>
      <button className="button primary" type="submit" form={limitsForm} disabled={busy || !limitsReady}>{busy ? t("Preparing your command…") : t("Generate command")}</button>
    </>
    : connected ? <button className="button primary" type="button" autoFocus onClick={close}>{t("Done")}</button>
    : <>
      <button className="button outline" type="button" disabled={busy} onClick={changeLimits}>{t("Change limits")}</button>
      {expired ? <button className="button primary" type="button" autoFocus disabled={busy} onClick={() => void generate()}>{busy ? t("Preparing your command…") : t("Generate new command")}</button> : null}
    </>;
  return <>
    <button type="button" className="button primary" disabled={disabled} onClick={start}><Plus size={16} />{t("Add node")}</button>
    {createPortal(<Modal open={open} title={t("Add node")} onClose={close} footer={footer}>
      <div className="sandbox-add-node form-stack">
        {!available ? <p role="status">{t("Node installation is unavailable. Ask the deployment administrator to enable the node installer on this console.")}</p> : !enrollment ? (
          <form id={limitsForm} className="form-stack" onSubmit={(event) => { event.preventDefault(); void generate(); }}>
            <p className="sandbox-add-node-intro">{t("Set the sandbox limits for the host you want to add.")}{hostRequirements}</p>
            <div className="field">
              <span className="field-label-row"><label htmlFor={`${id}-active`}>{t("Sandboxes at once")}</label><HelpTip>{t("The most sandboxes Core places on this node at the same time.")}</HelpTip></span>
              <input id={`${id}-active`} inputMode="numeric" autoComplete="off" autoFocus={open} value={active} onChange={(event) => setActive(event.target.value)} aria-invalid={Boolean(activeProblem)} />
              {activeProblem ? <span className="field-error">{activeProblem}</span> : null}
            </div>
            {suspends ? (
              <div className="field">
                <span className="field-label-row"><label htmlFor={`${id}-retained`}>{t("Retained sandboxes")}</label><HelpTip>{t("Sandboxes kept on this node for resuming, the running ones included. At least the number at once.")}</HelpTip></span>
                <input id={`${id}-retained`} inputMode="numeric" autoComplete="off" value={retained} onChange={(event) => setRetained(event.target.value)} aria-invalid={Boolean(retainedProblem)} />
                {retainedProblem ? <span className="field-error">{retainedProblem}</span> : null}
              </div>
            ) : null}
            {error !== null ? <p role="alert" className="sandbox-error">{sandboxRequestError(error, locale)}</p> : null}
          </form>
        ) : <>
          {!connected ? <p className="sandbox-add-node-intro">{t("Run on the host you want to add.")}{hostRequirements}</p> : null}
          {command ? <div className="sandbox-command"><div className="sandbox-command-heading"><span><Terminal size={15} />{t("Terminal")}</span><button type="button" className="button outline" autoFocus aria-label={copied ? t("Copied") : t("Copy node command")} onClick={() => void copyCommand()}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t("Copied") : t("Copy command")}</button></div><label className="field"><span className="sr-only">{t("One-time enrollment command")}</span><textarea readOnly rows={5} value={command} onClick={(event) => event.currentTarget.select()} spellCheck={false} /></label></div> : null}
          {copyFailed && !connected ? <p role="alert">{t("Select the command above and copy it manually.")}</p> : null}
          <div className={`sandbox-enrollment-status ${connected ? "connected" : ""}`} role="status"><span className="sandbox-status-dot" />{connected ? `${connected.name} · ${t("Connected")}` : expired ? t("Command expired") : t("Waiting for your node to connect…")}</div>
          {!fresh ? <p>{t("Connection status unavailable. Refresh to check your node.")}</p> : null}
          {!connected ? <p className="sandbox-command-expiry">{expired ? t("Generate a new command to continue.") : `${t("One-time enrollment token expires")} ${new Date(enrollment.expires_at).toLocaleTimeString(locale)}.`}</p> : null}
        </>}
      </div>
    </Modal>, document.body)}
  </>;
}
