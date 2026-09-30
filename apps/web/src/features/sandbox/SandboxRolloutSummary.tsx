import type { SandboxDeployment } from "@oac/agents-client";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { HelpTip, StatusDot, type Tone } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { formatInteger } from "../../lib/format";
import "./SandboxRolloutSummary.css";

/** Core's projection is authoritative; node lists never establish rollout completion. */
export function SandboxRolloutSummary({ deployment, stale = false, onOpen }: { deployment: SandboxDeployment; stale?: boolean; compact?: boolean; onOpen?: () => void }) {
  const { t, i18n } = useTranslation("sandbox");
  const id = useId();
  const [open, setOpen] = useState(false);
  if (!deployment.provider || deployment.reset) return null;
  const { rollout } = deployment;
  const needsAttention = !!rollout.nodes && (rollout.nodes.failed > 0 || rollout.nodes.update_required > 0);
  const unknown = !!rollout.nodes && rollout.nodes.unknown > 0;
  const label = stale ? "Rollout state unconfirmed" : needsAttention ? "Needs attention" : unknown ? "Target readiness unknown" : rollout.state === "preparing" ? "Preparing configuration" : "No active preparation";
  const tone: Tone = stale || needsAttention || unknown ? "warning" : rollout.state === "preparing" ? "pending" : "neutral";
  const count = (value: number) => formatInteger(value, i18n.resolvedLanguage);
  return <>
    <section className="sandbox-rollout-row" aria-labelledby={id}>
      <h3 id={id}>{t("Configuration rollout")}</h3>
      <span role={stale ? "alert" : "status"}><StatusDot tone={tone} label={t(label)} /></span>
      <button type="button" className="text-action" onClick={() => setOpen(true)}>{t("View rollout details")}</button>
    </section>
    <Modal open={open} title={t("Configuration rollout")} onClose={() => setOpen(false)} footer={onOpen ? <button type="button" className="button outline" onClick={() => { setOpen(false); onOpen(); }}>{t("Review nodes")}</button> : undefined}>
      <div className="sandbox-rollout-detail">
        <div className="sandbox-rollout-detail-status">
          <StatusDot tone={tone} label={t(label)} />
          <HelpTip>
            {t(stale ? "Rollout state is unconfirmed. These are the last confirmed observations." : rollout.state === "settled" ? "Core reports no active preparation. This does not mean every node is ready." : "Core is preparing the target configuration.")}
            {needsAttention || unknown ? ` ${t("Review affected nodes for preparation errors, incompatible node software or an unconfirmed connection. Qualified earlier generations may still serve work.")}` : null}
          </HelpTip>
        </div>
        <dl className="sandbox-rollout-facts">
          <div><dt>{t("Core preparation")}</dt><dd>{t(rollout.state === "preparing" ? "Preparing configuration" : "No active preparation")}</dd></div>
          <div><dt>{t("Target generation")}<HelpTip>{t("Existing sandboxes keep their configuration generation. Saving does not move them or prove the target is ready.")}</HelpTip></dt><dd>{count(deployment.generation)}</dd></div>
          <div><dt>{t("Previous-generation sandboxes")}</dt><dd>{count(rollout.previous_generation_sandboxes)}</dd></div>
          {rollout.nodes ? <>
            <div><dt>{t("Ready for target")}</dt><dd>{count(rollout.nodes.ready)}</dd></div>
            <div><dt>{t("Preparing target")}</dt><dd>{count(rollout.nodes.preparing)}</dd></div>
            <div><dt>{t("Preparation failed")}</dt><dd>{count(rollout.nodes.failed)}</dd></div>
            <div><dt>{t("Node software incompatible")}</dt><dd>{count(rollout.nodes.update_required)}</dd></div>
            <div><dt>{t("Target readiness unknown")}</dt><dd>{count(rollout.nodes.unknown)}</dd></div>
          </> : null}
        </dl>
      </div>
    </Modal>
  </>;
}
