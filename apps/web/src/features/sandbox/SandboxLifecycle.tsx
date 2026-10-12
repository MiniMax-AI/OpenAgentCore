import type { SandboxDeployment, SandboxNode } from "@oac/agents-client";
import { useTranslation } from "react-i18next";
import { Kpi, KpiStrip, Section } from "../../components/console-ui";
import { formatInteger, formatSpan, MISSING } from "../../lib/format";
import { capacitySummary } from "../fleet/fleet-model";

/** Core accounting and the declared policy, separate from host CPU and memory observations. */
export function SandboxLifecycle({ deployment, nodes, stale }: { deployment: SandboxDeployment; nodes: readonly SandboxNode[] | null; stale: boolean }) {
  const { t, i18n } = useTranslation("sandbox");
  const policy = deployment.suspension;
  if (!policy) return null;
  const count = (value: number) => formatInteger(value, i18n.resolvedLanguage);
  const capacity = nodes ? capacitySummary(nodes) : null;
  return <Section headingId="sandbox-lifecycle-heading" title={t("Automatic suspension")} help={t("Active and retained limits cover online nodes. Suspended counts include all registered nodes. These are Core placement records, not CPU or memory measurements.")}>
    {stale ? <p className="detail-note" role="status">{t("Showing the last loaded lifecycle counts; current state is unconfirmed.")}</p> : null}
    <KpiStrip label={t("Automatic suspension")}>
      <Kpi label={t("Active / limit")} value={capacity ? `${count(capacity.active)} / ${count(capacity.maxActive)}` : MISSING} />
      <Kpi label={t("Suspended")} value={capacity ? count(capacity.suspended) : MISSING} help={t("Suspended placements across all registered nodes, including offline nodes. They do not consume an active slot.")} />
      <Kpi label={t("Retained / limit")} value={capacity ? `${count(capacity.retained)} / ${count(capacity.maxRetained)}` : MISSING} />
      <Kpi label={t("Idle before suspension")} value={formatSpan(policy.idle_seconds, i18n.resolvedLanguage)} />
      <Kpi label={t("Snapshot retention")} value={formatSpan(policy.retention_seconds, i18n.resolvedLanguage)} help={<>{t("Current policy for new suspensions, not a countdown for existing snapshots. The next Turn requests a resume; capacity and a compatible node must be available.")}{deployment.specification?.workspace ? <> {t("Snapshot expiry releases memory state. With independent workspace storage, files and conversation remain available for a cold continuation; processes restart.")}</> : null}</>} />
    </KpiStrip>
  </Section>;
}
