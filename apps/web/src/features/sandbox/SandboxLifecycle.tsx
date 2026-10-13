import { useState, type ReactNode } from "react";
import type { SandboxDeployment, SandboxNode } from "@oac/agents-client";
import { useTranslation } from "react-i18next";
import { HelpTip, Kpi, KpiStrip, Section } from "../../components/console-ui";
import { formatInteger, formatSpan, MISSING } from "../../lib/format";
import { capacitySummary } from "../fleet/fleet-model";
import { Fact } from "../system/Fact";

function RetentionHelp({ deployment }: { deployment: SandboxDeployment }) {
  const { t } = useTranslation("sandbox");
  return <>{t("Current policy for new suspensions, not a countdown for existing snapshots. The next Turn requests a resume; capacity and a compatible node must be available.")}{deployment.specification?.workspace ? <> {t("Snapshot expiry releases memory state. With independent workspace storage, files and conversation remain available for a cold continuation; processes restart.")}</> : null}</>;
}

/** Core accounting and the declared policy, separate from host CPU and memory observations. */
export function SandboxLifecycle({ deployment, nodes, stale, children }: { deployment: SandboxDeployment; nodes: readonly SandboxNode[] | null; stale: boolean; children?: ReactNode }) {
  const { t, i18n } = useTranslation("sandbox");
  const policy = deployment.suspension;
  if (!policy) return <>{children}</>;
  const count = (value: number) => formatInteger(value, i18n.resolvedLanguage);
  const capacity = nodes ? capacitySummary(nodes) : null;
  return <Section headingId="sandbox-lifecycle-heading" title={t("Automatic suspension")} help={t("Active and retained limits cover online nodes. Suspended counts include all registered nodes. These are Core placement records, not CPU or memory measurements.")}>
    {stale ? <p className="detail-note" role="status">{t("Showing the last loaded lifecycle counts; current state is unconfirmed.")}</p> : null}
    <KpiStrip label={t("Automatic suspension")}>
      <Kpi label={t("Active / limit")} value={capacity ? `${count(capacity.active)} / ${count(capacity.maxActive)}` : MISSING} />
      <Kpi label={t("Suspended")} value={capacity ? count(capacity.suspended) : MISSING} help={t("Suspended placements across all registered nodes, including offline nodes. They do not consume an active slot.")} />
      <Kpi label={t("Retained / limit")} value={capacity ? `${count(capacity.retained)} / ${count(capacity.maxRetained)}` : MISSING} />
      <Kpi label={t("Idle before suspension")} value={formatSpan(policy.idle_seconds, i18n.resolvedLanguage)} />
      <Kpi label={t("Snapshot retention")} value={formatSpan(policy.retention_seconds, i18n.resolvedLanguage)} help={<RetentionHelp deployment={deployment} />} />
    </KpiStrip>
    {children}
  </Section>;
}

/** Settings edits the deployment-wide idle time; snapshot retention is Core's read-only policy. */
export function SuspensionSettings({ deployment, disabled, onSave }: { deployment: SandboxDeployment; disabled: boolean; onSave: (seconds: number) => Promise<boolean> }) {
  const { t, i18n } = useTranslation("sandbox");
  const policy = deployment.suspension;
  const [draft, setDraft] = useState<string | null>(null);
  if (!policy) return null;
  const idle = draft ?? String(policy.idle_seconds);
  const valid = /^\d+$/.test(idle) && Number(idle) >= 30 && Number(idle) <= 86400;
  return <Section headingId="sandbox-lifecycle-heading" title={t("Capacity and suspension")} help={t("The idle policy applies to the whole deployment. Active and retained limits are set for each node under Sandbox nodes.")}>
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
