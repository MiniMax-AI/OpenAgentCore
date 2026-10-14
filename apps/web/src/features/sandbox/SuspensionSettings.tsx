import { useState } from "react";
import type { SandboxDeployment } from "@oac/agents-client";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { HelpTip, Section } from "../../components/console-ui";
import { useToast } from "../../components/Toast";
import { formatSpan } from "../../lib/format";
import { sandboxRequestError, sandboxWriteUncertain } from "../../lib/sandbox-labels";
import { Fact } from "../system/Fact";
import { sandboxAdmin } from "./sandbox-queries";
import { writeSandboxDeployment } from "./sandbox-deployment-write";

function RetentionHelp({ deployment }: { deployment: SandboxDeployment }) {
  const { t } = useTranslation("sandbox");
  return <>{t("Current policy for new suspensions, not a countdown for existing snapshots. The next Turn requests a resume; capacity and a compatible node must be available.")}{deployment.specification?.workspace ? <> {t("Snapshot expiry releases memory state. With independent workspace storage, files and conversation remain available for a cold continuation; processes restart.")}</> : null}</>;
}

/** Settings edits the deployment-wide idle time; each node's limits are on Nodes. */
export function SuspensionSettings({ deployment, disabled }: { deployment: SandboxDeployment; disabled: boolean }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const queryClient = useQueryClient();
  const toast = useToast();
  async function save(seconds: number) {
    if (disabled) return false;
    try {
      return await writeSandboxDeployment(queryClient, (signal) => sandboxAdmin.updateSuspension({ idle_seconds: seconds }, { signal })) !== null;
    } catch (error) {
      toast.show(t(sandboxWriteUncertain(error) ? "Couldn't confirm the sandbox change" : "Core rejected the sandbox change"), { tone: "error", detail: sandboxRequestError(error, locale), key: "sandbox-write" });
      return false;
    }
  }
  return <SuspensionForm deployment={deployment} disabled={disabled} onSave={save} />;
}

/** The idle time form and Core's read-only snapshot retention. */
export function SuspensionForm({ deployment, disabled, onSave }: { deployment: SandboxDeployment; disabled: boolean; onSave: (seconds: number) => Promise<boolean> }) {
  const { t, i18n } = useTranslation("sandbox");
  const policy = deployment.suspension;
  const [draft, setDraft] = useState<string | null>(null);
  if (!policy) return null;
  const idle = draft ?? String(policy.idle_seconds);
  const valid = /^\d+$/.test(idle) && Number(idle) >= 30 && Number(idle) <= 86400;
  return <Section headingId="sandbox-lifecycle-heading" title={t("Automatic suspension")} help={t("The idle policy applies to the whole deployment. Each node's active and retained limits are set on Nodes with Edit node.")}>
    <div className="settings-rows">
      <form className="settings-row" onSubmit={async (event) => { event.preventDefault(); if (valid && !disabled && await onSave(Number(idle))) setDraft(null); }}>
        <div className="settings-row-label"><label htmlFor="sandbox-idle-seconds">{t("Idle before suspension (seconds)")}</label><HelpTip>{t("Deployment-wide: 30–86,400 seconds. Applies to existing and new running sandboxes without restarting them. Suspension already in progress and snapshot retention are unchanged. Capacity pressure may suspend an idle sandbox after 15 seconds.")}</HelpTip></div>
        <div className="settings-row-control field">
          <input id="sandbox-idle-seconds" inputMode="numeric" value={idle} disabled={disabled} onChange={(event) => setDraft(event.target.value)} aria-invalid={!valid} aria-describedby={!valid ? "sandbox-idle-error" : undefined} />
          <button type="submit" className="button outline" disabled={disabled || !valid || Number(idle) === policy.idle_seconds}>{t("Save")}</button>
          {!valid ? <span className="field-error" id="sandbox-idle-error">{t("Enter a whole number from 30 to 86,400.")}</span> : null}
        </div>
      </form>
      <Fact label={t("Snapshot retention")} help={<RetentionHelp deployment={deployment} />}>{formatSpan(policy.retention_seconds, i18n.resolvedLanguage)}</Fact>
    </div>
  </Section>;
}
