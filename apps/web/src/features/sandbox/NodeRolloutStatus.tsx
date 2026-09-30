import type { SandboxNode, SandboxNodeRollout } from "@oac/agents-client";
import { useTranslation } from "react-i18next";
import { HelpTip, StatusDot, type Tone } from "../../components/console-ui";
import type { MessageKey } from "../../lib/locale-strings";
import { DiagnosticTip } from "../fleet/DiagnosticTip";

const labels: Record<SandboxNodeRollout["state"], MessageKey> = { ready: "Ready for target", preparing: "Preparing target", failed: "Preparation failed", update_required: "Node software incompatible", unknown: "Target readiness unknown" };
const tones: Record<SandboxNodeRollout["state"], Tone> = { ready: "ok", preparing: "neutral", failed: "warning", update_required: "warning", unknown: "neutral" };

/** A serving pin is historical ownership, not proof of a live connection or capacity. */
export function NodeRolloutStatus({ node, stale = false }: { node: SandboxNode; stale?: boolean }) {
  const { t } = useTranslation("sandbox");
  const state = stale || !node.online ? "unknown" : node.rollout.state;
  return <span className="status-with-help">
    <StatusDot tone={tones[state]} label={t(labels[state])} />
    {state === "failed" && node.rollout.diagnostic ? <DiagnosticTip code={node.rollout.diagnostic} /> : null}
    {state === "update_required" ? <HelpTip>{t("In-place upgrades from older project versions are not supported. Reinstall using the current version. Existing data is not removed automatically.")}</HelpTip> : null}
  </span>;
}
