import type { SandboxDeployment, StartSandboxReset } from "@oac/agents-client";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { formatDateTime, formatInteger } from "../../lib/format";
import "./reset-confirmation.css";
import { validResetDeadline } from "./sandbox-reset";

type Action = "start" | "force" | "cancel";

/** Reset is durable Core work. These controls submit once and only display Core's progress. */
export function SandboxResetControls({ deployment, disabled, stale, onStart, onCancel }: {
  deployment: SandboxDeployment;
  disabled: boolean;
  stale: boolean;
  onStart: (input: StartSandboxReset) => Promise<boolean>;
  onCancel: (expectedGeneration: number) => Promise<boolean>;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const id = useId();
  const reset = deployment.reset;
  const [dialog, setDialog] = useState<{ action: Action; generation: number; requestedAt: string | null } | null>(null);
  const [clear, setClear] = useState<"auto" | "force">("auto");
  const [deadline, setDeadline] = useState("3600");
  const [submitting, setSubmitting] = useState(false);
  const changed = dialog !== null && (dialog.generation !== deployment.generation || dialog.requestedAt !== (reset?.requested_at ?? null));
  const valid = dialog?.action !== "start" || clear === "force" || validResetDeadline(deadline);
  const blocked = disabled || submitting || changed || !valid;
  const open = (action: Action) => {
    setClear("auto"); setDeadline("3600");
    setDialog({ action, generation: deployment.generation, requestedAt: reset?.requested_at ?? null });
  };
  async function confirm() {
    if (!dialog || blocked) return;
    setSubmitting(true);
    try {
      await (dialog.action === "cancel"
        ? onCancel(dialog.generation)
        : onStart({ expected_generation: dialog.generation, clear: dialog.action === "force" ? "force" : clear, ...(dialog.action === "start" && clear === "auto" ? { deadline_seconds: Number(deadline) } : {}) }));
    } finally {
      // A submitted confirmation is consumed even if its outcome is uncertain.
      // The shared write owner and error dialog govern reconciliation and retry.
      setDialog(null);
      setSubmitting(false);
    }
  }
  const title = t(dialog?.action === "cancel" ? "Cancel reset?" : dialog?.action === "force" ? "Force reset now?" : "Reset sandbox deployment?");
  const label = t(dialog?.action === "cancel" ? "Cancel reset" : dialog?.action === "force" ? "Force reset now" : "Reset deployment");
  return <>
    {reset ? <section className="sandbox-reset form-stack" aria-labelledby={`${id}-progress`}>
      <div className="sandbox-provider-title"><h3 id={`${id}-progress`}>{t("Reset in progress")}</h3><HelpTip>{t("Progress comes from Core. Offline resources still need confirmed cleanup, even in force mode.")}</HelpTip></div>
      <p role="status">{t(reset.clear === "auto" ? "Idle sandboxes are being archived. Busy work can finish until the deadline." : "Remaining hosted Sessions are being archived and their work cancelled.")}</p>
      {stale ? <p className="sandbox-error" role="alert">{t("Reset progress could not be refreshed. These are the last confirmed counts.")}</p> : null}
      <dl className="sandbox-summary">
        <div><dt>{t("Busy")}</dt><dd>{formatInteger(reset.remaining.busy, i18n.resolvedLanguage)}</dd></div>
        <div><dt>{t("Idle")}</dt><dd>{formatInteger(reset.remaining.idle, i18n.resolvedLanguage)}</dd></div>
        <div><dt>{t("Awaiting cleanup")}</dt><dd>{formatInteger(reset.remaining.cleanup, i18n.resolvedLanguage)}</dd></div>
        <div><dt>{t("On offline nodes")}</dt><dd>{formatInteger(reset.remaining.on_offline_nodes, i18n.resolvedLanguage)}</dd></div>
        {reset.clear === "auto" && reset.deadline_at ? <div><dt>{t("Force deadline")}</dt><dd>{formatDateTime(Date.parse(reset.deadline_at) / 1000, i18n.resolvedLanguage)}</dd></div> : null}
      </dl>
      {reset.remaining.offline_nodes.length ? <div className="sandbox-reset-offline">
        <p>{t("Bring these nodes back online so Core can confirm cleanup. Force reset does not bypass them.")}</p>
        <ul>{reset.remaining.offline_nodes.map((node) => <li key={node.node_id}><span>{node.name || node.node_id}</span>{" · "}{t("{{count}} resources", { count: node.resources })}</li>)}</ul>
        <p>{t("Remove a node only after Core confirms it holds no resources.")}</p>
      </div> : null}
      <p>{t("New hosted Sessions and node enrollment are paused. Setup becomes available only after Core completes the reset.")}</p>
      <div className="sandbox-actions">
        {reset.clear === "auto" ? <button type="button" className="button outline" disabled={disabled || submitting} onClick={() => open("force")}>{t("Force reset now")}</button> : null}
        <button type="button" className="button outline" disabled={disabled || submitting} onClick={() => open("cancel")}>{t("Cancel reset")}</button>
      </div>
    </section> : <button type="button" className="button outline" disabled={disabled || submitting} onClick={() => open("start")}>{t("Reset deployment")}</button>}
    <Modal open={dialog !== null} title={title} onClose={() => { if (!submitting) setDialog(null); }} footer={<>
      <button type="button" className="button outline" disabled={submitting} onClick={() => setDialog(null)}>{t("Back")}</button>
      <button type="button" className="button danger" disabled={blocked} onClick={() => void confirm()}>{submitting ? t("Saving sandbox change…") : label}</button>
    </>}>
      <div className="confirm-dialog-body sandbox-reset-dialog">
        {dialog?.action === "cancel" ? <p>{t("Stop the reset and allow new hosted Sessions again. Sessions already archived stay archived and cannot be resumed.")}</p> : <>
          <p>{t("Hosted Sessions will be archived permanently. Deployment configuration and node registrations will be cleared. Unsaved workspace contents may be lost.")} <HelpTip label={t("What reset affects")}>{t("Reset archives this deployment's hosted Sessions. Archived Sessions cannot be resumed. History, Files and Artifacts are kept; self-hosted Sessions are unchanged.")} {t("After cleanup, Core clears the provider and saved E2B credential, retires nodes and enrollment commands, and returns to setup.")}</HelpTip></p>
          {dialog?.action === "force" ? <p>{t("Force immediately cancels remaining work. Offline resources can still block cleanup.")}</p> : <fieldset className="sandbox-reset-options" disabled={submitting || disabled}>
            <legend>{t("Clear hosted work")}</legend>
            <div className="sandbox-reset-choice"><label><input type="radio" name={`${id}-clear`} value="auto" checked={clear === "auto"} onChange={() => setClear("auto")} />{t("Auto — let busy work finish")}</label><HelpTip label={t("How automatic reset works")}>{t("Idle, suspended and queued Sessions are archived immediately. Running or waiting Turns and file writes may finish until the deadline; then Core forces the rest.")}</HelpTip></div>
            <div className="sandbox-reset-choice"><label><input type="radio" name={`${id}-clear`} value="force" checked={clear === "force"} onChange={() => setClear("force")} />{t("Force — cancel remaining work now")}</label><HelpTip>{t("Force immediately cancels remaining work. Offline resources can still block cleanup.")}</HelpTip></div>
            {clear === "auto" ? <label className="field" htmlFor={`${id}-deadline`}>
              <span>{t("Wait before forcing (seconds)")}<HelpTip>{t("Default: 1 hour. Choose a whole number from 300 to 86400 seconds (5 minutes to 24 hours).")}</HelpTip></span>
              <input id={`${id}-deadline`} type="number" min={300} max={86400} step={1} value={deadline} onChange={(event) => setDeadline(event.target.value)} aria-invalid={!validResetDeadline(deadline)} />
              {!validResetDeadline(deadline) ? <span role="alert" className="field-error">{t("Enter a whole number from 300 to 86400 seconds.")}</span> : null}
            </label> : null}
            {clear === "force" ? <p>{t("Force immediately cancels remaining work. Offline resources can still block cleanup.")}</p> : null}
          </fieldset>}
        </>}
        {changed ? <p role="alert" className="sandbox-error">{t("Sandbox state changed. Close this dialog and review the current state.")}</p> : null}
      </div>
    </Modal>
  </>;
}
