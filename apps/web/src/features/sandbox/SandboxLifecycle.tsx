import { useState, type ReactNode } from "react";
import type { SandboxDeployment, SandboxNode } from "@oac/agents-client";
import { useTranslation } from "react-i18next";
import { HelpTip, Kpi, KpiStrip, Section } from "../../components/console-ui";
import { formatInteger, formatSpan, MISSING } from "../../lib/format";
import { capacitySummary } from "../fleet/fleet-model";

/** Core accounting and the declared policy, separate from host CPU and memory observations. */
export function SandboxLifecycle({ deployment, nodes, stale, controls, children }: { deployment: SandboxDeployment; nodes: readonly SandboxNode[] | null; stale: boolean; controls?: { disabled: boolean; onSave: (seconds: number) => Promise<boolean>; }; children?: ReactNode }) {
  const { t, i18n } = useTranslation("sandbox");
  const policy = deployment.suspension;
  const [draft, setDraft] = useState<string | null>(null);
  const idle = draft ?? String(policy?.idle_seconds ?? 300);
  const valid = /^\d+$/.test(idle) && Number(idle) >= 30 && Number(idle) <= 86400;
  if (!policy && !controls) return null;
  const count = (value: number) => formatInteger(value, i18n.resolvedLanguage);
  const capacity = nodes ? capacitySummary(nodes) : null;
  return <Section headingId="sandbox-lifecycle-heading" title={t(controls ? "Capacity and suspension" : "Automatic suspension")} help={t("Active and retained limits cover online nodes. Suspended counts include all registered nodes. These are Core placement records, not CPU or memory measurements.")}>
    {stale ? <p className="detail-note" role="status">{t("Showing the last loaded lifecycle counts; current state is unconfirmed.")}</p> : null}
    <KpiStrip label={t("Automatic suspension")}>
      <Kpi label={t("Active / limit")} value={capacity ? `${count(capacity.active)} / ${count(capacity.maxActive)}` : MISSING} />
      {policy ? <><Kpi label={t("Suspended")} value={capacity ? count(capacity.suspended) : MISSING} help={t("Suspended placements across all registered nodes, including offline nodes. They do not consume an active slot.")} />
      <Kpi label={t("Retained / limit")} value={capacity ? `${count(capacity.retained)} / ${count(capacity.maxRetained)}` : MISSING} />
      <Kpi label={t("Idle before suspension")} value={formatSpan(policy.idle_seconds, i18n.resolvedLanguage)} />
      <Kpi label={t("Snapshot retention")} value={formatSpan(policy.retention_seconds, i18n.resolvedLanguage)} help={<>{t("Current policy for new suspensions, not a countdown for existing snapshots. The next Turn requests a resume; capacity and a compatible node must be available.")}{deployment.specification?.workspace ? <> {t("Snapshot expiry releases memory state. With independent workspace storage, files and conversation remain available for a cold continuation; processes restart.")}</> : null}</>} />
      </> : null}
    </KpiStrip>
    {controls ? <>
      {policy ? <form className="form-stack" onSubmit={async (event) => { event.preventDefault(); if (valid && !controls.disabled && await controls.onSave(Number(idle))) setDraft(null); }}>
        <div className="field">
          <span className="field-label-row"><label htmlFor="sandbox-idle-seconds">{t("Idle before suspension (seconds)")}</label><HelpTip>{t("Deployment-wide: 30–86,400 seconds. Applies to existing and new running sandboxes without restarting them. Suspension already in progress and snapshot retention are unchanged. Capacity pressure may suspend an idle sandbox after 15 seconds.")}</HelpTip></span>
          <input id="sandbox-idle-seconds" inputMode="numeric" value={idle} disabled={controls.disabled} onChange={(event) => setDraft(event.target.value)} aria-invalid={!valid} aria-describedby={!valid ? "sandbox-idle-error" : undefined} />
          {!valid ? <span className="field-error" id="sandbox-idle-error">{t("Enter a whole number from 30 to 86,400.")}</span> : null}
        </div>
        <div className="sandbox-actions"><button type="submit" className="button outline" disabled={controls.disabled || !valid || Number(idle) === policy.idle_seconds}>{t("Save")}</button></div>
      </form> : null}

    </> : null}
    {children}
  </Section>;
}
